package desktop

import (
	"context"
	"errors"
	"fmt"
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
		_ = sessionContext.runtime.Close(context.Background())
		sessionContext.runtime = nil
	}
	if sessionContext.eventDone != nil {
		<-sessionContext.eventDone
	}
	s.mu.Lock()
	delete(s.sessionContexts, sessionID)
	s.clearSessionReasoningLocked(sessionID)
	store := s.activityStore
	s.mu.Unlock()
	if store != nil {
		if err := store.DeleteSession(context.Background(), sessionID); err != nil {
			slog.Warn("delete desktop activity for removed session", "session_id", sessionID, "error", err)
		}
	}
	return nil
}

type CompactionResultView struct {
	Before    int64  `json:"before"`
	After     int64  `json:"after"`
	Threshold int64  `json:"threshold"`
	Mode      string `json:"mode"`
	Backend   string `json:"backend,omitempty"`
	Fallback  bool   `json:"fallback,omitempty"`
	Compacted bool   `json:"compacted"`
	Turns     int    `json:"turns"`
}

func (s *DesktopService) CompactSession(sessionID string) (CompactionResultView, error) {
	sessionContext, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return CompactionResultView{}, err
	}
	defer release()
	if sessionContext.core == nil || sessionContext.sessionID == "" {
		return CompactionResultView{}, errors.New("没有打开会话")
	}
	result, err := sessionContext.core.Compact(context.Background(), sessionContext.sessionID)
	if err != nil {
		return CompactionResultView{}, err
	}
	return CompactionResultView{
		Before:    result.Before,
		After:     result.After,
		Threshold: result.Threshold,
		Mode:      result.Mode,
		Backend:   result.Backend,
		Fallback:  result.Fallback,
		Compacted: result.Compacted,
		Turns:     result.Turns,
	}, nil
}

func (s *DesktopService) ForkSession(sessionID string) (WorkspaceState, error) {
	s.workspaceMu.Lock()
	defer s.workspaceMu.Unlock()
	sessionID = strings.TrimSpace(sessionID)
	s.mu.Lock()
	if s.reconfiguring {
		s.mu.Unlock()
		return WorkspaceState{}, errors.New("项目设置正在应用")
	}
	localCore := s.localCore
	if localCore == nil {
		localCore = s.activeCoreLocked()
	}
	if localCore == nil {
		s.mu.Unlock()
		return WorkspaceState{}, errors.New("请先打开工作区")
	}
	if sessionID == "" {
		sessionID = s.activeSessionIDLocked()
	}
	workspace := s.workspace
	s.mu.Unlock()
	if sessionID == "" {
		return WorkspaceState{}, errors.New("没有可分支的会话")
	}
	forked, err := localCore.ForkSession(context.Background(), sessionID)
	if err != nil {
		return WorkspaceState{}, fmt.Errorf("分支会话失败: %w", err)
	}
	forkedContext := &desktopSessionContext{
		permissionMode: core.PermissionWorkspace,
		core:           localCore,
		sessionID:      forked.ID,
		workspace:      workspace,
	}
	s.mu.Lock()
	if s.sessionContexts == nil {
		s.sessionContexts = make(map[string]*desktopSessionContext)
	}
	s.sessionContexts[forked.ID] = forkedContext
	s.mu.Unlock()
	if err := s.attachSessionRuntime(forkedContext); err != nil {
		return WorkspaceState{}, err
	}
	return s.openSession(forked.ID, false)
}
