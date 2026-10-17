package desktop

import (
	"context"
	"errors"
	"log/slog"
	"strings"
)

type MCPServerView struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	Enabled   bool   `json:"enabled"`
	Connected bool   `json:"connected"`
	ToolCount int    `json:"toolCount"`
}

func (s *DesktopService) ListMCPServers() ([]MCPServerView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.core == nil {
		return nil, errors.New("请先打开工作区")
	}
	statuses := s.core.MCPServers()
	servers := make([]MCPServerView, 0, len(statuses))
	for _, status := range statuses {
		servers = append(servers, MCPServerView{
			Name: status.Name, Transport: status.Transport, Enabled: status.Enabled,
			Connected: status.Connected, ToolCount: status.ToolCount,
		})
	}
	return servers, nil
}

func (s *DesktopService) SetMCPServerEnabled(name string, enabled bool) error {
	name = strings.TrimSpace(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.core == nil {
		return errors.New("请先打开工作区")
	}
	if name == "" {
		return errors.New("MCP 服务名称不能为空")
	}
	if enabled {
		return s.core.EnableMCPServer(name)
	}
	return s.core.DisableMCPServer(name)
}

func (s *DesktopService) AuthenticateMCPServer(name string) error {
	name = strings.TrimSpace(name)
	s.mu.Lock()
	agent := s.core
	s.mu.Unlock()
	if agent == nil {
		return errors.New("请先打开工作区")
	}
	if name == "" {
		return errors.New("MCP 服务名称不能为空")
	}
	return agent.AuthenticateMCPServer(context.Background(), name)
}

func (s *DesktopService) DeleteSession(sessionID string) error {
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.core == nil {
		return errors.New("请先打开工作区")
	}
	if sessionID == "" {
		return errors.New("会话标识不能为空")
	}
	if s.runtime != nil && s.runtime.SessionID() == sessionID {
		return errors.New("请先切换到其他会话，再删除当前会话")
	}
	if err := s.core.DeleteSession(context.Background(), sessionID); err != nil {
		return err
	}
	if s.activityStore != nil {
		if err := s.activityStore.DeleteSession(context.Background(), sessionID); err != nil {
			slog.Warn("delete desktop activity for removed session", "session_id", sessionID, "error", err)
		}
	}
	return nil
}
