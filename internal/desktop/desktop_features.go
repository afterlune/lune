package desktop

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	core "github.com/afterlune/luneCore"
)

type MCPServerView struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Enabled   bool   `json:"enabled"`
	Connected bool   `json:"connected"`
	ToolCount int    `json:"toolCount"`
}

func (s *DesktopService) ListMCPServers(sessionID string) ([]MCPServerView, error) {
	c, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return nil, err
	}
	defer release()
	if c.core == nil {
		return nil, errors.New("请先打开工作区")
	}
	statuses := c.core.MCPServers()
	servers := make([]MCPServerView, 0, len(statuses))
	for _, status := range statuses {
		servers = append(servers, MCPServerView{
			Name: status.Name, Transport: status.Transport, Enabled: status.Enabled,
			Connected: status.Connected, ToolCount: status.ToolCount,
		})
	}
	return servers, nil
}

func (s *DesktopService) SetMCPServerEnabled(sessionID, name string, enabled bool) error {
	name = strings.TrimSpace(name)
	c, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return err
	}
	defer release()
	if c.core == nil {
		return errors.New("请先打开工作区")
	}
	if name == "" {
		return errors.New("MCP 服务名称不能为空")
	}
	if enabled {
		return c.core.EnableMCPServer(name)
	}
	return c.core.DisableMCPServer(name)
}

func (s *DesktopService) AuthenticateMCPServer(sessionID, name string) error {
	name = strings.TrimSpace(name)
	c, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return err
	}
	defer release()
	s.mu.Lock()
	agent := c.core
	if agent != nil {
		s.mcpOperations++
	}
	s.mu.Unlock()
	if agent == nil {
		return errors.New("请先打开工作区")
	}
	defer func() {
		s.mu.Lock()
		s.mcpOperations--
		s.mu.Unlock()
	}()
	if name == "" {
		return errors.New("MCP 服务名称不能为空")
	}
	return agent.AuthenticateMCPServer(context.Background(), name)
}

func (s *DesktopService) DeleteSession(sessionID string) error {
	s.workspaceMu.Lock()
	defer s.workspaceMu.Unlock()
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.localCore == nil {
		return errors.New("请先打开工作区")
	}
	if sessionID == "" {
		return errors.New("会话标识不能为空")
	}
	if s.activeSessionIDLocked() == sessionID {
		return errors.New("请先切换到其他会话，再删除当前会话")
	}
	sessionContext := s.sessionContexts[sessionID]
	if sessionContext == nil {
		return errors.New("会话不存在")
	}
	if sessionContext.runtime != nil {
		state, err := sessionContext.runtime.State(context.Background())
		if err != nil {
			return err
		}
		if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued || state.Task.Status == core.TaskPaused {
			return errors.New("请先中断或处理该会话中的任务，再删除会话")
		}
	}
	if err := sessionContext.core.DeleteSession(context.Background(), sessionID); err != nil {
		return err
	}
	sharedWorktreeCore := false
	if sessionContext.mode == workspaceModeWorktree {
		for otherID, otherContext := range s.sessionContexts {
			if otherID != sessionID && otherContext != nil && otherContext.core == sessionContext.core {
				sharedWorktreeCore = true
				break
			}
		}
		if !sharedWorktreeCore && s.activityStore != nil {
			if records, err := s.activityStore.DesktopSessions(context.Background()); err == nil {
				for _, record := range records {
					if record.ID != sessionID && record.StateDir == sessionContext.stateDir {
						sharedWorktreeCore = true
						break
					}
				}
			} else {
				sharedWorktreeCore = true
				slog.Warn("preserve Worktree state because the session index could not be read", "session_id", sessionID, "error", err)
			}
		}
	}
	if sessionContext.eventCancel != nil {
		sessionContext.eventCancel()
	}
	s.mu.Unlock()
	if sessionContext.eventDone != nil {
		<-sessionContext.eventDone
	}
	s.mu.Lock()
	delete(s.sessionContexts, sessionID)
	var closeErr error
	if sessionContext.mode == workspaceModeWorktree {
		if !sharedWorktreeCore {
			closeErr = sessionContext.core.Close()
		}
		if closeErr == nil && s.activityStore != nil {
			closeErr = s.activityStore.DeleteDesktopSession(context.Background(), sessionID)
		}
		if closeErr == nil && !sharedWorktreeCore {
			closeErr = removeDesktopWorktreeStateDir(s.stateRoot, sessionContext.stateDir)
		}
	}
	if s.activityStore != nil {
		if err := s.activityStore.DeleteSession(context.Background(), sessionID); err != nil {
			slog.Warn("delete desktop activity for removed session", "session_id", sessionID, "error", err)
		}
	}
	return closeErr
}
