package desktop

import (
	"context"
	"errors"
	"fmt"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/afterlune/lune/internal/settings"
	core "github.com/afterlune/luneCore"
	_ "golang.org/x/image/webp"
)

type SessionSummary struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	UpdatedAt        string `json:"updatedAt"`
	WorkingDirectory string `json:"workingDirectory"`
	Status           string `json:"status"`
}

type WorkspaceOpenResult struct {
	Workspace  string             `json:"workspace"`
	Sessions   []SessionSummary   `json:"sessions"`
	Permission string             `json:"permissionMode"`
	Models     *ModelSettingsView `json:"models,omitempty"`
}

func (s *DesktopService) OpenWorkspace(path string) (WorkspaceOpenResult, error) {
	s.workspaceMu.Lock()
	defer s.workspaceMu.Unlock()
	path = strings.TrimSpace(path)
	if path == "" {
		return WorkspaceOpenResult{}, errors.New("请选择工作区文件夹")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return WorkspaceOpenResult{}, fmt.Errorf("解析工作区路径: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return WorkspaceOpenResult{}, fmt.Errorf("工作区不可用: %s", absolute)
	}
	s.mu.Lock()
	if s.reconfiguring {
		s.mu.Unlock()
		return WorkspaceOpenResult{}, errors.New("项目设置正在应用")
	}
	if s.workspace != "" && !samePath(s.workspace, absolute) {
		if err := s.closeWorkspaceLocked(false); err != nil {
			s.mu.Unlock()
			return WorkspaceOpenResult{}, err
		}
	}
	if s.localCore != nil && samePath(s.workspace, absolute) {
		result, resultErr := s.workspaceResultLocked()
		s.mu.Unlock()
		if resultErr == nil {
			s.rememberWorkspace(result.Workspace)
		}
		return result, resultErr
	}
	s.mu.Unlock()
	if err := s.startWorkspace(absolute); err != nil {
		return WorkspaceOpenResult{}, err
	}
	result, err := s.workspaceResult()
	if err != nil {
		return WorkspaceOpenResult{}, err
	}
	s.rememberWorkspace(result.Workspace)
	return result, nil
}

func (s *DesktopService) startWorkspace(path string) error {
	modelSettings, err := s.store.LoadModelSettings()
	if err != nil {
		return err
	}
	projectConfig, _, err := s.store.LoadWorkspaceConfig(path)
	if err != nil {
		return err
	}
	commandToolchainPaths, _, err := s.store.LoadWorkspaceCommandToolchainPaths(path)
	if err != nil {
		return err
	}
	projectConfig.Models = modelSettings.Catalog
	projectConfig.Runtime.MaxParallelAgents = modelSettings.MaxParallelAgents
	if err := projectConfig.Validate(); err != nil {
		return fmt.Errorf("桌面项目配置无效: %w", err)
	}
	return s.startWorkspaceWithConfig(path, projectConfig, commandToolchainPaths)
}

func (s *DesktopService) startWorkspaceWithConfig(path string, projectConfig core.ProjectConfig, commandToolchainPaths []string) error {
	helper := "lune-sandbox.exe"
	if runtime.GOOS != "windows" {
		helper = "lune-sandbox"
	}
	helperPath := filepath.Join(filepath.Dir(os.Args[0]), helper)
	if _, err := os.Stat(helperPath); err != nil {
		helperPath = ""
	}
	stateRoot, err := desktopProjectStateDir(path)
	if err != nil {
		return err
	}
	localStateDir := filepath.Join(stateRoot, "local")
	config := core.Config{
		Workspace: path, StateDir: localStateDir, Logger: slog.Default(), SandboxHelperPath: helperPath,
		ProjectConfig: &projectConfig, CommandToolchainPaths: availableCommandToolchainPaths(commandToolchainPaths),
		RestoreEnabledMCP: true,
	}
	created, err := core.New(config)
	if err != nil {
		return fmt.Errorf("打开工作区失败: %w", err)
	}
	activityStore, err := OpenActivityStore(stateRoot)
	if err != nil {
		return errors.Join(fmt.Errorf("打开桌面执行记录: %w", err), created.Close())
	}
	if err := s.store.TouchWorkspace(path); err != nil {
		return errors.Join(fmt.Errorf("保存最近工作区: %w", err), activityStore.Close(), created.Close())
	}
	s.mu.Lock()
	s.localCore, s.workspace = created, path
	s.workspaceContext, s.workspaceCancel = context.WithCancel(context.Background())
	s.activityStore = activityStore
	s.stateRoot = stateRoot
	s.projectConfig = projectConfig
	s.commandToolchainPaths = append([]string(nil), commandToolchainPaths...)
	if s.sessionContexts == nil {
		s.sessionContexts = make(map[string]*desktopSessionContext)
	}
	s.activeContext = nil
	s.mu.Unlock()
	if err := s.restoreSessionContexts(); err != nil {
		s.mu.Lock()
		closeErr := s.closeWorkspaceLocked(true)
		s.mu.Unlock()
		return errors.Join(err, closeErr)
	}
	return nil
}

func (s *DesktopService) restoreSessionContexts() error {
	s.mu.Lock()
	localCore, workspace := s.localCore, s.workspace
	s.mu.Unlock()
	if localCore == nil {
		return errors.New("没有打开工作区")
	}
	s.mu.Lock()
	if s.sessionContexts == nil {
		s.sessionContexts = make(map[string]*desktopSessionContext)
	}
	localSessions, err := localCore.ListSessions(context.Background(), 1000, true)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("读取 Desktop 本地会话: %w", err)
	}
	for _, saved := range localSessions {
		if s.sessionContexts[saved.ID] == nil {
			s.sessionContexts[saved.ID] = &desktopSessionContext{
				permissionMode: core.PermissionWorkspace, core: localCore, sessionID: saved.ID, workspace: workspace,
			}
		}
	}
	s.mu.Unlock()
	return nil
}

func (s *DesktopService) attachSessionRuntime(sessionContext *desktopSessionContext, replayFromStart ...bool) error {
	if sessionContext == nil || sessionContext.core == nil || sessionContext.sessionID == "" {
		return errors.New("会话运行时信息无效")
	}
	if sessionContext.runtime != nil {
		return nil
	}
	runtime, err := sessionContext.core.OpenSessionRuntime(context.Background(), sessionContext.sessionID)
	if err != nil {
		return fmt.Errorf("打开会话运行时: %w", err)
	}
	runtime.SetUserInteractionAvailable(true)
	sessionContext.runtime = runtime
	state, err := runtime.State(context.Background())
	if err != nil {
		_ = runtime.Close()
		sessionContext.runtime = nil
		return err
	}
	s.mu.Lock()
	if s.sessionContexts == nil {
		s.sessionContexts = make(map[string]*desktopSessionContext)
	}
	if s.workspaceContext == nil {
		s.workspaceContext, s.workspaceCancel = context.WithCancel(context.Background())
	}
	if sessionContext.permissionMode == "" {
		sessionContext.permissionMode = core.PermissionWorkspace
	}
	s.sessionContexts[sessionContext.sessionID] = sessionContext
	replay := len(replayFromStart) > 0 && replayFromStart[0]
	s.subscribeEventsLocked(sessionContext, replay)
	s.watchTurnLocked(runtime, sessionContext.sessionID, state.Task.ExecutionID)
	s.mu.Unlock()
	return nil
}

func (s *DesktopService) releaseInactiveSessionRuntime(sessionID string) {
	s.mu.Lock()
	sessionContext := s.sessionContexts[sessionID]
	active := sessionContext != nil && s.activeContext == sessionContext
	s.mu.Unlock()
	if sessionContext == nil || active {
		return
	}
	sessionContext.opMu.Lock()
	defer sessionContext.opMu.Unlock()

	s.mu.Lock()
	if s.sessionContexts[sessionID] != sessionContext || s.activeContext == sessionContext || sessionContext.runtime == nil {
		s.mu.Unlock()
		return
	}
	runtime := sessionContext.runtime
	s.mu.Unlock()
	state, err := runtime.State(context.Background())
	if err != nil || state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued || len(state.Queue) > 0 {
		return
	}
	runtime.SetUserInteractionAvailable(false)
	if err := runtime.Close(); err != nil {
		return
	}

	s.mu.Lock()
	if s.sessionContexts[sessionID] == sessionContext && sessionContext.runtime == runtime {
		sessionContext.runtime = nil
	}
	cancel, done := sessionContext.eventCancel, sessionContext.eventDone
	sessionContext.eventCancel, sessionContext.eventDone = nil, nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}

func sessionContextStatus(sessionContext *desktopSessionContext) string {
	if sessionContext == nil || sessionContext.runtime == nil {
		return "idle"
	}
	state, err := sessionContext.runtime.State(context.Background())
	if err != nil {
		return "unavailable"
	}
	switch state.Task.Status {
	case core.TaskRunning:
		return "running"
	case core.TaskQueued:
		return "queued"
	case core.TaskPaused:
		return "waiting"
	case core.TaskFailed:
		return "failed"
	case core.TaskCancelled:
		return "cancelled"
	case core.TaskCompleted:
		return "completed"
	default:
		return "idle"
	}
}

func (s *DesktopService) workspaceResult() (WorkspaceOpenResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workspaceResultLocked()
}

func (s *DesktopService) workspaceResultLocked() (WorkspaceOpenResult, error) {
	localCore := s.localCore
	if localCore == nil {
		localCore = s.activeCoreLocked()
	}
	if localCore == nil {
		return WorkspaceOpenResult{}, errors.New("没有打开工作区")
	}
	sessions, err := localCore.ListSessions(context.Background(), 100, false)
	if err != nil {
		return WorkspaceOpenResult{}, err
	}
	models := modelSettingsView(settings.SharedModelSettings{Catalog: s.projectConfig.Models, MaxParallelAgents: s.projectConfig.Runtime.MaxParallelAgents})
	result := WorkspaceOpenResult{Workspace: s.workspace, Permission: string(core.PermissionWorkspace), Models: &models}
	for _, session := range sessions {
		workingDirectory, status := s.workspace, "idle"
		if context := s.sessionContexts[session.ID]; context != nil {
			workingDirectory = context.workspace
			status = sessionContextStatus(context)
		}
		result.Sessions = append(result.Sessions, SessionSummary{
			ID: session.ID, Title: session.Title, UpdatedAt: session.UpdatedAt.Format(time.RFC3339),
			WorkingDirectory: workingDirectory, Status: status,
		})
	}
	sort.Slice(result.Sessions, func(i, j int) bool { return result.Sessions[i].UpdatedAt > result.Sessions[j].UpdatedAt })
	if len(result.Sessions) > 100 {
		result.Sessions = result.Sessions[:100]
	}
	return result, nil
}

func (s *DesktopService) CloseWorkspace(interrupt bool) error {
	s.workspaceMu.Lock()
	defer s.workspaceMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reconfiguring {
		return errors.New("项目设置正在应用")
	}
	return s.closeWorkspaceLocked(interrupt)
}

// closeWorkspaceLocked shuts down the active workspace. Callers hold
// workspaceMu and mu; mu is released while tasks stop and resources close so
// background event and activity writers are not blocked by shutdown.
func (s *DesktopService) closeWorkspaceLocked(interrupt bool) error {
	localCore := s.localCore
	if localCore == nil {
		localCore = s.activeCoreLocked()
	}
	if localCore == nil {
		return nil
	}
	contexts := make([]*desktopSessionContext, 0, len(s.sessionContexts))
	seen := make(map[*desktopSessionContext]struct{}, len(s.sessionContexts))
	cores := map[*core.Core]struct{}{localCore: {}}
	for _, sessionContext := range s.sessionContexts {
		if sessionContext == nil {
			continue
		}
		if _, ok := seen[sessionContext]; ok {
			continue
		}
		seen[sessionContext] = struct{}{}
		contexts = append(contexts, sessionContext)
		if sessionContext.core != nil {
			cores[sessionContext.core] = struct{}{}
		}
	}
	activityStore := s.activityStore

	s.mu.Unlock()
	if err := s.stopWorkspaceSessions(contexts, interrupt); err != nil {
		s.mu.Lock()
		return err
	}

	s.mu.Lock()
	for _, sessionContext := range contexts {
		if sessionContext.eventCancel != nil {
			sessionContext.eventCancel()
			sessionContext.eventCancel = nil
		}
	}
	if s.workspaceCancel != nil {
		s.workspaceCancel()
		s.workspaceCancel = nil
	}
	s.mu.Unlock()

	s.workers.Wait()

	var closeErr error
	for agent := range cores {
		if agent != nil {
			closeErr = errors.Join(closeErr, closeCoreWithRetry(agent))
		}
	}
	if activityStore != nil {
		closeErr = errors.Join(closeErr, activityStore.Close())
	}

	s.mu.Lock()
	s.localCore = nil
	s.workspace = ""
	s.activeContext = nil
	s.sessionContexts = map[string]*desktopSessionContext{}
	s.activityStore, s.stateRoot = nil, ""
	s.projectConfig = core.ProjectConfig{}
	s.commandToolchainPaths = nil
	s.workspaceContext, s.workspaceCancel = nil, nil
	s.waiting = map[string]bool{}
	s.suppressedActivity = nil
	s.clearRememberedLocation()
	return closeErr
}

// stopWorkspaceSessions stops every attached session without the service
// state lock. With interrupt=false a busy workspace is rejected before any
// task is touched; with interrupt=true queued turns are cancelled and the
// active turn is interrupted until each session reaches idle.
func (s *DesktopService) stopWorkspaceSessions(contexts []*desktopSessionContext, interrupt bool) error {
	if len(contexts) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for _, sessionContext := range contexts {
		if sessionContext == nil {
			continue
		}
		sessionContext.opMu.Lock()
		if sessionContext.runtime == nil {
			sessionContext.opMu.Unlock()
			continue
		}
		state, requests, err := workspaceSessionActivity(ctx, sessionContext)
		sessionContext.opMu.Unlock()
		if err != nil {
			return err
		}
		if workspaceActivityBusy(state, requests) && !interrupt {
			return errors.New("当前工作区仍有任务或待处理请求，请先确认中断并关闭工作区")
		}
	}
	if !interrupt {
		return nil
	}
	lastBusy := make([]string, 0)
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("等待任务停止: %w (会话: %s)", err, strings.Join(lastBusy, ", "))
		}
		busyIDs := make([]string, 0)
		for _, sessionContext := range contexts {
			if sessionContext == nil {
				continue
			}
			sessionContext.opMu.Lock()
			if sessionContext.runtime == nil {
				sessionContext.opMu.Unlock()
				continue
			}
			state, requests, err := workspaceSessionActivity(ctx, sessionContext)
			if err != nil {
				sessionContext.opMu.Unlock()
				return err
			}
			if !workspaceActivityBusy(state, requests) {
				sessionContext.opMu.Unlock()
				continue
			}
			busyIDs = append(busyIDs, fmt.Sprintf("%s:%s(queue=%d requests=%d)", sessionContext.sessionID, state.Task.Status, len(state.Queue), len(requests)))
			if err := cancelWorkspaceSession(ctx, sessionContext, state, requests); err != nil {
				sessionContext.opMu.Unlock()
				return err
			}
			sessionContext.opMu.Unlock()
		}
		if len(busyIDs) == 0 {
			return nil
		}
		lastBusy = busyIDs
		time.Sleep(100 * time.Millisecond)
	}
}

func workspaceSessionActivity(ctx context.Context, sessionContext *desktopSessionContext) (core.RuntimeState, []core.PendingRequest, error) {
	state, err := sessionContext.runtime.State(ctx)
	if err != nil {
		return core.RuntimeState{}, nil, err
	}
	requests, err := sessionContext.runtime.PendingRequests(ctx)
	if err != nil {
		return core.RuntimeState{}, nil, err
	}
	return state, requests, nil
}

func workspaceActivityBusy(state core.RuntimeState, requests []core.PendingRequest) bool {
	switch state.Task.Status {
	case core.TaskRunning, core.TaskQueued:
		return true
	case core.TaskPaused:
		return len(requests) > 0
	}
	return len(requests) > 0
}

func cancelWorkspaceSession(ctx context.Context, sessionContext *desktopSessionContext, state core.RuntimeState, requests []core.PendingRequest) error {
	if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued {
		for _, item := range state.Queue {
			if item.Status != "queued" {
				continue
			}
			cancelErr := sessionContext.runtime.CancelQueuedTurn(ctx, item.ExecutionID)
			if cancelErr != nil && !errors.Is(cancelErr, core.ErrQueuedTurnStarted) && !errors.Is(cancelErr, core.ErrTurnNotFound) {
				return cancelErr
			}
		}
		if state.Task.ExecutionID == "" {
			return errors.New("无法确定当前任务，暂时不能关闭工作区")
		}
		sessionContext.runtime.Interrupt(state.Task.ExecutionID)
		return nil
	}
	if state.Task.Status == core.TaskPaused || len(requests) > 0 {
		_, err := sessionContext.runtime.DiscardPaused(ctx)
		return err
	}
	return nil
}

func closeCoreWithRetry(agent *core.Core) error {
	var err error
	for attempt := 0; attempt < 100; attempt++ {
		err = agent.Close()
		if err == nil || !strings.Contains(err.Error(), "cannot close core while a task is running") {
			return err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return err
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
