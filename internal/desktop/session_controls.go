package desktop

import (
	"context"
	"errors"

	core "github.com/afterlune/luneCore"
)

type SessionControlsView struct {
	PermissionMode          string `json:"permissionMode"`
	EffectivePermissionMode string `json:"effectivePermissionMode"`
	PlanMode                bool   `json:"planMode"`
}

// sessionControls reads policy while the service state mutex is held.
func sessionControls(c *desktopSessionContext) SessionControlsView {
	mode := c.permissionMode
	if mode == "" {
		mode = core.PermissionWorkspace
	}
	effective := mode
	if c.planMode {
		effective = core.PermissionReadOnly
	}
	return SessionControlsView{PermissionMode: string(mode), EffectivePermissionMode: string(effective), PlanMode: c.planMode}
}

func (s *DesktopService) controlsFor(c *desktopSessionContext) SessionControlsView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return sessionControls(c)
}

// sessionOperation pins workspace resources and serializes changes to one session.
// Lock order: workspace lifetime, session operation, service state.
func (s *DesktopService) sessionOperation(id string) (*desktopSessionContext, func(), error) {
	s.workspaceMu.RLock()
	s.mu.Lock()
	c := s.sessionContexts[id]
	reconfiguring := s.reconfiguring
	s.mu.Unlock()
	if id == "" || c == nil || reconfiguring {
		s.workspaceMu.RUnlock()
		return nil, nil, errors.New("会话不可用，请重新打开会话")
	}
	c.opMu.Lock()
	s.mu.Lock()
	current := s.sessionContexts[id] == c
	s.mu.Unlock()
	if !current {
		c.opMu.Unlock()
		s.workspaceMu.RUnlock()
		return nil, nil, errors.New("会话已关闭")
	}
	if c.runtime == nil {
		if err := s.attachSessionRuntime(c); err != nil {
			c.opMu.Unlock()
			s.workspaceMu.RUnlock()
			return nil, nil, err
		}
	}
	return c, func() {
		c.opMu.Unlock()
		s.workspaceMu.RUnlock()
		s.releaseInactiveSessionRuntime(id)
	}, nil
}

func idleSession(c *desktopSessionContext) error {
	state, err := c.runtime.State(context.Background())
	if err != nil {
		return err
	}
	requests, err := c.runtime.PendingRequests(context.Background())
	if err != nil {
		return err
	}
	if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued || state.Task.Status == core.TaskPaused || len(state.Queue) > 0 || len(requests) > 0 {
		return errors.New("请等待当前会话任务结束并处理待处理请求后再修改控制设置")
	}
	return nil
}

func (s *DesktopService) activeCoreLocked() *core.Core {
	if s.activeContext != nil {
		return s.activeContext.core
	}
	return s.localCore
}
func (s *DesktopService) activeSessionIDLocked() string {
	if s.activeContext != nil {
		return s.activeContext.sessionID
	}
	return ""
}

func (s *DesktopService) captureControlsLocked() map[string]SessionControlsView {
	controls := make(map[string]SessionControlsView, len(s.sessionContexts))
	for id, c := range s.sessionContexts {
		controls[id] = sessionControls(c)
	}
	return controls
}
func (s *DesktopService) restoreControls(controls map[string]SessionControlsView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, v := range controls {
		if c := s.sessionContexts[id]; c != nil {
			c.permissionMode = core.PermissionMode(v.PermissionMode)
			c.planMode = v.PlanMode
		}
	}
}
func (s *DesktopService) SetSessionTitle(sessionID, title string) error {
	s.workspaceMu.RLock()
	defer s.workspaceMu.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	sessionContext := s.sessionContexts[sessionID]
	if sessionContext == nil || sessionContext.core == nil {
		return errors.New("没有打开工作区")
	}
	return sessionContext.core.SetSessionTitle(context.Background(), sessionID, title)
}

func (s *DesktopService) setActiveSessionContextLocked(sessionContext *desktopSessionContext) {
	s.activeContext = sessionContext
}

func (s *DesktopService) SetSessionModel(sessionID, profile string) error {
	sessionContext, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return err
	}
	defer release()
	if sessionContext.core == nil || sessionContext.sessionID == "" {
		return errors.New("没有打开会话")
	}
	saved, err := sessionContext.core.GetSession(context.Background(), sessionContext.sessionID)
	if err != nil {
		return err
	}
	saved.Settings.ModelProfile = profile
	return sessionContext.core.SetSessionSettings(context.Background(), sessionContext.sessionID, saved.Settings)
}

func (s *DesktopService) SetPermissionMode(sessionID, mode string) (SessionControlsView, error) {
	c, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return SessionControlsView{}, err
	}
	defer release()
	if err := idleSession(c); err != nil {
		return SessionControlsView{}, err
	}
	permMode := core.PermissionMode(mode)
	if permMode == "ask" {
		permMode = core.PermissionAuto
	}
	switch permMode {
	case core.PermissionReadOnly, core.PermissionWorkspace, core.PermissionAuto, core.PermissionFullAccess:
	default:
		return SessionControlsView{}, errors.New("未知的权限档位")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.planMode {
		return SessionControlsView{}, errors.New("方案模式期间权限固定为只读")
	}
	c.permissionMode = permMode
	return sessionControls(c), nil
}

func (s *DesktopService) SetPlanMode(sessionID string, enabled bool) (SessionControlsView, error) {
	c, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return SessionControlsView{}, err
	}
	defer release()
	if err := idleSession(c); err != nil {
		return SessionControlsView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c.planMode = enabled
	return sessionControls(c), nil
}
