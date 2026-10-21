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
	if s.localCore == nil {
		s.mu.Unlock()
		return errors.New("请先打开工作区")
	}
	if sessionID == "" {
		s.mu.Unlock()
		return errors.New("会话标识不能为空")
	}
	if s.activeSessionIDLocked() == sessionID {
		s.mu.Unlock()
		return errors.New("请先切换到其他会话，再删除当前会话")
	}
	sessionContext := s.sessionContexts[sessionID]
	if sessionContext == nil {
		s.mu.Unlock()
		return errors.New("会话不存在")
	}
	if sessionContext.runtime != nil {
		state, err := sessionContext.runtime.State(context.Background())
		if err != nil {
			s.mu.Unlock()
			return err
		}
		if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued || state.Task.Status == core.TaskPaused {
			s.mu.Unlock()
			return errors.New("请先中断或处理该会话中的任务，再删除会话")
		}
	}
	if err := sessionContext.core.DeleteSession(context.Background(), sessionID); err != nil {
		s.mu.Unlock()
		return err
	}
	if sessionContext.eventCancel != nil {
		sessionContext.eventCancel()
		sessionContext.eventCancel = nil
	}
	s.mu.Unlock()
	if sessionContext.runtime != nil {
		_ = sessionContext.runtime.Close()
		sessionContext.runtime = nil
	}
	if sessionContext.eventDone != nil {
		<-sessionContext.eventDone
	}
	s.mu.Lock()
	delete(s.sessionContexts, sessionID)
	store := s.activityStore
	s.mu.Unlock()
	if store != nil {
		if err := store.DeleteSession(context.Background(), sessionID); err != nil {
			slog.Warn("delete desktop activity for removed session", "session_id", sessionID, "error", err)
		}
	}
	return nil
}
