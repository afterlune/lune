package desktop

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	core "github.com/afterlune/luneCore"
)

type DesktopProcessListView struct {
	Processes           []DesktopProcessView        `json:"processes"`
	UnavailableSessions []DesktopProcessSessionView `json:"unavailableSessions"`
}

type DesktopProcessView struct {
	ID               string     `json:"id"`
	SessionID        string     `json:"sessionId"`
	SessionTitle     string     `json:"sessionTitle"`
	WorkingDirectory string     `json:"workingDirectory"`
	ExecutionID      string     `json:"executionId"`
	Command          string     `json:"command"`
	Status           string     `json:"status"`
	StartedAt        time.Time  `json:"startedAt"`
	FinishedAt       *time.Time `json:"finishedAt,omitempty"`
	ExitCode         *int       `json:"exitCode,omitempty"`
}

type DesktopProcessSessionView struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	WorkingDirectory string `json:"workingDirectory"`
	Status           string `json:"status"`
	Reason           string `json:"reason"`
}

type DesktopProcessOutputView struct {
	SessionID  string `json:"sessionId"`
	CommandID  string `json:"commandId"`
	Command    string `json:"command"`
	Status     string `json:"status"`
	Output     string `json:"output"`
	Cursor     int64  `json:"cursor"`
	NextCursor int64  `json:"nextCursor"`
	EndCursor  int64  `json:"endCursor"`
	Truncated  bool   `json:"truncated"`
	Error      string `json:"error,omitempty"`
	ExitCode   *int   `json:"exitCode,omitempty"`
}

// ListDesktopProcesses returns commands owned by every runtime attached to the
// active Desktop workspace. Session metadata whose runtime could not be opened
// is returned separately so it remains visible without exposing any actions.
func (s *DesktopService) ListDesktopProcesses() (DesktopProcessListView, error) {
	s.workspaceMu.RLock()
	defer s.workspaceMu.RUnlock()
	result := DesktopProcessListView{
		Processes:           []DesktopProcessView{},
		UnavailableSessions: []DesktopProcessSessionView{},
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(s.workspace) == "" {
		return result, nil
	}

	seen := make(map[string]struct{}, len(s.sessionContexts))
	for sessionID, sessionContext := range s.sessionContexts {
		if sessionContext == nil {
			continue
		}
		seen[sessionID] = struct{}{}
		sessionView := DesktopProcessSessionView{
			ID: sessionID, WorkingDirectory: sessionContext.workspace,
		}
		if sessionContext.runtime == nil || sessionContext.core == nil {
			sessionView.Status = "unavailable"
			sessionView.Reason = "此会话的运行时未加载。"
			result.UnavailableSessions = append(result.UnavailableSessions, sessionView)
			continue
		}

		saved, err := sessionContext.core.GetSession(context.Background(), sessionID)
		if err != nil {
			sessionView.Status = "unavailable"
			sessionView.Reason = "无法读取会话信息：" + err.Error()
			result.UnavailableSessions = append(result.UnavailableSessions, sessionView)
			continue
		}
		sessionView.Title = saved.Title

		commands, err := sessionContext.runtime.ListCommands(context.Background())
		if err != nil {
			sessionView.Status = "unavailable"
			sessionView.Reason = "无法读取此会话的进程：" + err.Error()
			result.UnavailableSessions = append(result.UnavailableSessions, sessionView)
			continue
		}
		for _, command := range commands {
			view := DesktopProcessView{
				ID: command.ID, SessionID: sessionID, SessionTitle: saved.Title,
				WorkingDirectory: sessionContext.workspace,
				ExecutionID:      command.ExecutionID, Command: command.Command,
				Status: string(command.Status), StartedAt: command.StartedAt,
				ExitCode: command.ExitCode,
			}
			if !command.FinishedAt.IsZero() {
				finishedAt := command.FinishedAt
				view.FinishedAt = &finishedAt
			}
			result.Processes = append(result.Processes, view)
		}
	}

	localCore := s.localCore
	if localCore == nil {
		localCore = s.activeCoreLocked()
	}
	if localCore != nil {
		localSessions, err := localCore.ListSessions(context.Background(), 1000, true)
		if err != nil {
			return DesktopProcessListView{}, fmt.Errorf("读取本地会话进程索引失败: %w", err)
		}
		for _, saved := range localSessions {
			if _, ok := seen[saved.ID]; ok {
				continue
			}
			seen[saved.ID] = struct{}{}
			result.UnavailableSessions = append(result.UnavailableSessions, DesktopProcessSessionView{
				ID: saved.ID, Title: saved.Title,
				WorkingDirectory: s.workspace, Status: "unavailable",
				Reason: "此会话的运行时未加载。",
			})
		}
	}

	sort.SliceStable(result.Processes, func(i, j int) bool {
		leftActive := core.CommandStatus(result.Processes[i].Status).Active()
		rightActive := core.CommandStatus(result.Processes[j].Status).Active()
		if leftActive != rightActive {
			return leftActive
		}
		return result.Processes[i].StartedAt.After(result.Processes[j].StartedAt)
	})
	sort.SliceStable(result.UnavailableSessions, func(i, j int) bool {
		if result.UnavailableSessions[i].Title != result.UnavailableSessions[j].Title {
			return result.UnavailableSessions[i].Title < result.UnavailableSessions[j].Title
		}
		return result.UnavailableSessions[i].ID < result.UnavailableSessions[j].ID
	})
	return result, nil
}

func (s *DesktopService) processRuntime(sessionID string) (*core.SessionRuntime, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, errors.New("会话 ID 不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(s.workspace) == "" {
		return nil, errors.New("没有打开工作区")
	}
	sessionContext := s.sessionContexts[sessionID]
	if sessionContext == nil || sessionContext.runtime == nil {
		return nil, errors.New("此会话的运行时不可用")
	}
	return sessionContext.runtime, nil
}

// ReadDesktopProcessOutput reads one cursor-bounded chunk. Wait requests can
// block for at most Core's five-second follow interval.
func (s *DesktopService) ReadDesktopProcessOutput(sessionID, commandID string, cursor int64, wait bool) (DesktopProcessOutputView, error) {
	s.workspaceMu.RLock()
	defer s.workspaceMu.RUnlock()
	if strings.TrimSpace(commandID) == "" {
		return DesktopProcessOutputView{}, errors.New("进程 ID 不能为空")
	}
	runtime, err := s.processRuntime(sessionID)
	if err != nil {
		return DesktopProcessOutputView{}, err
	}
	if cursor < 0 {
		cursor = 0
	}
	waitFor := time.Duration(0)
	if wait {
		waitFor = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), waitFor+time.Second)
	defer cancel()
	output, err := runtime.ReadCommand(ctx, commandID, core.CommandReadOptions{Cursor: cursor, Wait: waitFor})
	if err != nil {
		return DesktopProcessOutputView{}, err
	}
	return DesktopProcessOutputView{
		SessionID: sessionID, CommandID: output.Command.ID, Command: output.Command.Command,
		Status: string(output.Command.Status), Output: output.Output,
		Cursor: output.Cursor, NextCursor: output.NextCursor, EndCursor: output.EndCursor,
		Truncated: output.Truncated, Error: output.Error, ExitCode: output.Command.ExitCode,
	}, nil
}

// StopDesktopProcess stops only the command addressed by its owning session runtime.
func (s *DesktopService) StopDesktopProcess(sessionID, commandID string) error {
	s.workspaceMu.RLock()
	defer s.workspaceMu.RUnlock()
	if strings.TrimSpace(commandID) == "" {
		return errors.New("进程 ID 不能为空")
	}
	runtime, err := s.processRuntime(sessionID)
	if err != nil {
		return err
	}
	_, err = runtime.StopCommand(context.Background(), commandID)
	return err
}
