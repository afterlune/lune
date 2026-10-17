package desktop

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/afterlune/lune/internal/settings"
	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
	"github.com/wailsapp/wails/v3/pkg/application"
	_ "golang.org/x/image/webp"
)

type DesktopService struct {
	mu                 sync.Mutex
	store              *settings.Store
	app                *application.App
	appContext         context.Context
	core               *core.Core
	localCore          *core.Core
	runtime            *core.SessionRuntime
	activityStore      *ActivityStore
	workspace          string
	activeWorkspace    string
	sessionID          string
	activeContext      *desktopSessionContext
	sessionContexts    map[string]*desktopSessionContext
	stateRoot          string
	modelConfig        *publicmodel.Config
	planMode           bool
	previousMode       core.PermissionMode
	waiting            map[string]bool
	suppressedActivity map[string]struct{}
	projectModelView   *ModelSettingsView
}

type desktopSessionContext struct {
	core           *core.Core
	runtime        *core.SessionRuntime
	sessionID      string
	workspace      string
	mode           string
	stateDir       string
	worktreePath   string
	repositoryRoot string
	eventCancel    context.CancelFunc
	planMode       bool
	previousMode   core.PermissionMode
}

const (
	workspaceModeLocal    = "local"
	workspaceModeWorktree = "worktree"
)

func NewDesktopService() (*DesktopService, error) {
	store, err := settings.OpenUser()
	if err != nil {
		return nil, err
	}
	return &DesktopService{
		store: store, waiting: map[string]bool{}, sessionContexts: map[string]*desktopSessionContext{},
	}, nil
}

func (s *DesktopService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appContext = ctx
	s.app = application.Get()
	return nil
}

func (s *DesktopService) ServiceShutdown() error {
	closeErr := s.CloseWorkspace(true)
	if s.store != nil {
		closeErr = errors.Join(closeErr, s.store.Close())
	}
	return closeErr
}

type Bootstrap struct {
	Theme      string                     `json:"theme"`
	Recent     []settings.RecentWorkspace `json:"recentWorkspaces"`
	Models     ModelSettingsView          `json:"models"`
	Workspace  string                     `json:"workspace"`
	SessionID  string                     `json:"sessionId"`
	Permission string                     `json:"permissionMode"`
}

type ProviderView struct {
	Name             string `json:"name"`
	Protocol         string `json:"protocol"`
	BaseURL          string `json:"baseURL"`
	HasAPIKey        bool   `json:"hasAPIKey"`
	ServerCompaction bool   `json:"serverCompaction"`
}

type ModelProfileView struct {
	Name               string `json:"name"`
	Provider           string `json:"provider"`
	Model              string `json:"model"`
	MaxOutputTokens    int64  `json:"maxOutputTokens"`
	SupportsInputAudio bool   `json:"supportsInputAudio"`
}

type ModelSettingsView struct {
	Providers         []ProviderView     `json:"providers"`
	Models            []ModelProfileView `json:"profiles"`
	DefaultModel      string             `json:"defaultModel"`
	MaxParallelAgents int                `json:"maxParallelAgents"`
	Configured        bool               `json:"configured"`
}

type ProviderInput struct {
	Name             string `json:"name"`
	Protocol         string `json:"protocol"`
	BaseURL          string `json:"baseURL"`
	APIKey           string `json:"apiKey"`
	ServerCompaction bool   `json:"serverCompaction"`
}

type ModelProfileInput struct {
	Name               string `json:"name"`
	Provider           string `json:"provider"`
	Model              string `json:"model"`
	MaxOutputTokens    int64  `json:"maxOutputTokens"`
	SupportsInputAudio bool   `json:"supportsInputAudio"`
}

type ModelSettingsInput struct {
	Providers         []ProviderInput     `json:"providers"`
	Models            []ModelProfileInput `json:"profiles"`
	DefaultModel      string              `json:"defaultModel"`
	MaxParallelAgents int                 `json:"maxParallelAgents"`
}

func (s *DesktopService) Bootstrap() (Bootstrap, error) {
	theme, err := s.store.Theme()
	if err != nil {
		return Bootstrap{}, err
	}
	recent, err := s.store.RecentWorkspaces()
	if err != nil {
		return Bootstrap{}, err
	}
	modelConfig, err := s.store.LoadModelConfig()
	if err != nil {
		return Bootstrap{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := Bootstrap{Theme: theme, Recent: recent, Models: modelSettingsView(modelConfig), Workspace: s.workspace, SessionID: s.sessionID}
	if s.core != nil {
		result.Permission = string(s.core.PermissionMode())
	}
	if s.projectModelView != nil {
		result.Models = *s.projectModelView
	}
	return result, nil
}

func modelSettingsView(config publicmodel.Config) ModelSettingsView {
	view := ModelSettingsView{DefaultModel: config.DefaultModel, MaxParallelAgents: config.MaxParallelAgents, Configured: len(config.Providers) > 0 && len(config.Models) > 0}
	for name, provider := range config.Providers {
		view.Providers = append(view.Providers, ProviderView{
			Name: name, Protocol: provider.Protocol, BaseURL: provider.BaseURL,
			HasAPIKey: strings.TrimSpace(provider.APIKey) != "", ServerCompaction: provider.ServerCompaction,
		})
	}
	for name, profile := range config.Models {
		view.Models = append(view.Models, ModelProfileView{
			Name: name, Provider: profile.Provider, Model: profile.Model,
			MaxOutputTokens: profile.MaxOutputTokens, SupportsInputAudio: profile.SupportsInputAudio,
		})
	}
	sort.Slice(view.Providers, func(i, j int) bool { return view.Providers[i].Name < view.Providers[j].Name })
	sort.Slice(view.Models, func(i, j int) bool { return view.Models[i].Name < view.Models[j].Name })
	return view
}

func (s *DesktopService) SaveModelSettings(input ModelSettingsInput) (ModelSettingsView, error) {
	current, err := s.store.LoadModelConfig()
	if err != nil {
		return ModelSettingsView{}, err
	}
	config := publicmodel.Config{
		Providers:    make(map[string]publicmodel.ProviderConfig, len(input.Providers)),
		Models:       make(map[string]publicmodel.ModelProfile, len(input.Models)),
		DefaultModel: input.DefaultModel, MaxParallelAgents: input.MaxParallelAgents,
	}
	for _, provider := range input.Providers {
		apiKey := strings.TrimSpace(provider.APIKey)
		if apiKey == "" {
			apiKey = current.Providers[provider.Name].APIKey
		}
		config.Providers[provider.Name] = publicmodel.ProviderConfig{
			Protocol: provider.Protocol, BaseURL: provider.BaseURL, APIKey: apiKey, ServerCompaction: provider.ServerCompaction,
		}
	}
	for _, profile := range input.Models {
		config.Models[profile.Name] = publicmodel.ModelProfile{
			Provider: profile.Provider, Model: profile.Model, MaxOutputTokens: profile.MaxOutputTokens,
			SupportsInputAudio: profile.SupportsInputAudio,
		}
	}
	s.mu.Lock()
	workspace, sessionID := s.workspace, s.sessionID
	for _, sessionContext := range s.sessionContexts {
		if sessionContext == nil || sessionContext.runtime == nil {
			continue
		}
		state, stateErr := sessionContext.runtime.State(context.Background())
		if stateErr != nil {
			s.mu.Unlock()
			return ModelSettingsView{}, stateErr
		}
		if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued {
			s.mu.Unlock()
			return ModelSettingsView{}, errors.New("请等待所有会话任务结束后再修改模型设置")
		}
	}
	if workspace != "" {
		if err := s.closeWorkspaceLocked(false); err != nil {
			s.mu.Unlock()
			return ModelSettingsView{}, err
		}
	}
	s.mu.Unlock()
	if err := s.store.SaveModelConfig(config); err != nil {
		if workspace != "" {
			if reopenErr := s.startWorkspace(workspace); reopenErr == nil && sessionID != "" {
				_, reopenErr = s.OpenSession(sessionID)
				err = errors.Join(err, reopenErr)
			} else {
				err = errors.Join(err, reopenErr)
			}
		}
		return ModelSettingsView{}, err
	}
	if workspace != "" {
		if err := s.startWorkspace(workspace); err != nil {
			return ModelSettingsView{}, err
		}
		if sessionID != "" {
			if _, err := s.OpenSession(sessionID); err != nil {
				return ModelSettingsView{}, err
			}
		}
	}
	return modelSettingsView(config), nil
}

func (s *DesktopService) SetTheme(theme string) error { return s.store.SetTheme(theme) }

func (s *DesktopService) PickWorkspace() (string, error) {
	s.mu.Lock()
	app := s.app
	s.mu.Unlock()
	if app == nil {
		return "", errors.New("桌面文件选择器尚未就绪")
	}
	return app.Dialog.OpenFileWithOptions(&application.OpenFileDialogOptions{
		CanChooseDirectories: true, CanChooseFiles: false, CanCreateDirectories: false,
		Title: "打开工作区", ButtonText: "打开文件夹",
	}).PromptForSingleSelection()
}

type SessionSummary struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	UpdatedAt        string `json:"updatedAt"`
	Mode             string `json:"mode"`
	WorkingDirectory string `json:"workingDirectory"`
	WorktreePath     string `json:"worktreePath,omitempty"`
	Status           string `json:"status"`
}

type WorkspaceOpenResult struct {
	Workspace         string             `json:"workspace"`
	Sessions          []SessionSummary   `json:"sessions"`
	Permission        string             `json:"permissionMode"`
	Models            *ModelSettingsView `json:"models,omitempty"`
	NeedsMigration    bool               `json:"needsMigration"`
	Migration         *MigrationPreview  `json:"migration,omitempty"`
	WorktreeAvailable bool               `json:"worktreeAvailable"`
}

func (s *DesktopService) OpenWorkspace(path string) (WorkspaceOpenResult, error) {
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
	if s.workspace != "" && !samePath(s.workspace, absolute) {
		if err := s.closeWorkspaceLocked(false); err != nil {
			s.mu.Unlock()
			return WorkspaceOpenResult{}, err
		}
	}
	if s.localCore != nil && samePath(s.workspace, absolute) {
		result, resultErr := s.workspaceResultLocked(false, nil)
		s.mu.Unlock()
		return result, resultErr
	}
	s.mu.Unlock()
	current, err := s.store.LoadModelConfig()
	if err != nil {
		return WorkspaceOpenResult{}, err
	}
	_, _, _, incoming, hasLegacy, err := readLegacyModelSettings(absolute)
	if err != nil {
		return WorkspaceOpenResult{}, err
	}
	if hasLegacy {
		preview, err := previewMigration(absolute, current, incoming)
		if err != nil {
			return WorkspaceOpenResult{}, err
		}
		return WorkspaceOpenResult{Workspace: absolute, NeedsMigration: true, Migration: preview}, nil
	}
	if err := s.startWorkspace(absolute); err != nil {
		return WorkspaceOpenResult{}, err
	}
	return s.workspaceResult(false, nil)
}

func (s *DesktopService) CompleteModelMigration(path string, choices map[string]string) (WorkspaceOpenResult, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return WorkspaceOpenResult{}, err
	}
	_, original, _, _, hasLegacy, err := readLegacyModelSettings(absolute)
	if err != nil {
		return WorkspaceOpenResult{}, err
	}
	if !hasLegacy {
		return WorkspaceOpenResult{}, errors.New("项目中没有可迁移的模型设置")
	}
	current, err := s.store.LoadModelConfig()
	if err != nil {
		return WorkspaceOpenResult{}, err
	}
	err = migrateProjectModelSettings(absolute, current, choices, s.store.SaveModelConfigWith)
	if err != nil {
		return WorkspaceOpenResult{}, err
	}
	if err := s.startWorkspace(absolute); err != nil {
		restoreErr := restoreOriginalConfig(filepath.Join(absolute, "config.json"), original)
		settingsErr := s.store.RestoreModelConfig(current)
		return WorkspaceOpenResult{}, errors.Join(err, restoreErr, settingsErr)
	}
	return s.workspaceResult(false, nil)
}

func (s *DesktopService) OpenWorkspaceWithProjectModels(path string) (WorkspaceOpenResult, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return WorkspaceOpenResult{}, err
	}
	_, _, _, _, hasLegacy, err := readLegacyModelSettings(absolute)
	if err != nil {
		return WorkspaceOpenResult{}, err
	}
	if !hasLegacy {
		return WorkspaceOpenResult{}, errors.New("项目没有本地模型设置")
	}
	if err := s.startWorkspaceWithModelConfig(absolute, nil); err != nil {
		return WorkspaceOpenResult{}, err
	}
	return s.workspaceResult(false, nil)
}

func (s *DesktopService) startWorkspace(path string) error {
	modelConfig, err := s.store.LoadModelConfig()
	if err != nil {
		return err
	}
	var supplied *publicmodel.Config
	if len(modelConfig.Models) > 0 {
		supplied = &modelConfig
	}
	return s.startWorkspaceWithModelConfig(path, supplied)
}

func (s *DesktopService) startWorkspaceWithModelConfig(path string, modelConfig *publicmodel.Config) error {
	var projectView *ModelSettingsView
	if modelConfig == nil {
		_, _, _, projectConfig, found, err := readLegacyModelSettings(path)
		if err != nil {
			return err
		}
		if found {
			view := modelSettingsView(projectConfig)
			for index := range view.Providers {
				view.Providers[index].HasAPIKey = false
			}
			projectView = &view
		}
	}
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
	config := core.Config{Workspace: path, StateDir: localStateDir, Logger: slog.Default(), SandboxHelperPath: helperPath, ModelConfig: modelConfig}
	created, err := core.New(config)
	if err != nil {
		if errors.Is(err, core.ErrStateDirInUse) {
			return errors.New("这个 Desktop 工作区的本地会话状态已被另一个 Desktop 实例打开")
		}
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
	s.core, s.localCore, s.workspace, s.activeWorkspace, s.runtime, s.sessionID = created, created, path, path, nil, ""
	s.activityStore = activityStore
	s.projectModelView = projectView
	s.stateRoot = stateRoot
	s.modelConfig = modelConfig
	if s.sessionContexts == nil {
		s.sessionContexts = make(map[string]*desktopSessionContext)
	}
	s.activeContext = nil
	s.planMode, s.previousMode = false, ""
	s.mu.Unlock()
	if err := s.restoreSessionContexts(); err != nil {
		return errors.Join(err, s.CloseWorkspace(true))
	}
	return nil
}

func (s *DesktopService) restoreSessionContexts() error {
	s.mu.Lock()
	localCore, workspace, stateRoot := s.localCore, s.workspace, s.stateRoot
	store := s.activityStore
	s.mu.Unlock()
	if localCore == nil {
		return errors.New("没有打开工作区")
	}
	localSessions, err := localCore.ListSessions(context.Background(), 1000, true)
	if err != nil {
		return fmt.Errorf("读取 Desktop 本地会话: %w", err)
	}
	for _, saved := range localSessions {
		if err := s.attachSessionRuntime(&desktopSessionContext{
			core: localCore, sessionID: saved.ID, workspace: workspace,
			mode: workspaceModeLocal, stateDir: filepath.Join(stateRoot, "local"),
		}); err != nil {
			return err
		}
	}
	if store == nil {
		return nil
	}
	worktreeSessions, err := store.DesktopSessions(context.Background())
	if err != nil {
		return err
	}
	worktreeCores := make(map[string]*core.Core)
	worktreeCoreErrors := make(map[string]error)
	for _, record := range worktreeSessions {
		if _, err := os.Stat(record.Workspace); err != nil {
			_ = store.UpdateDesktopSession(context.Background(), record.ID, record.Title, "missing", record.UpdatedAt)
			continue
		}
		agent := worktreeCores[record.StateDir]
		createdCore := false
		if agent == nil {
			if priorErr := worktreeCoreErrors[record.StateDir]; priorErr != nil {
				status := "unavailable"
				if errors.Is(priorErr, core.ErrStateDirInUse) {
					status = "in use"
				}
				_ = store.UpdateDesktopSession(context.Background(), record.ID, record.Title, status, record.UpdatedAt)
				continue
			}
			var err error
			agent, err = s.newCore(record.Workspace, record.StateDir)
			if err != nil {
				worktreeCoreErrors[record.StateDir] = err
				status := "unavailable"
				if errors.Is(err, core.ErrStateDirInUse) {
					status = "in use"
				}
				_ = store.UpdateDesktopSession(context.Background(), record.ID, record.Title, status, record.UpdatedAt)
				continue
			}
			createdCore = true
			worktreeCores[record.StateDir] = agent
		}
		if _, err := agent.GetSession(context.Background(), record.ID); err != nil {
			if createdCore {
				_ = agent.Close()
				delete(worktreeCores, record.StateDir)
			}
			_ = store.UpdateDesktopSession(context.Background(), record.ID, record.Title, "unavailable", record.UpdatedAt)
			continue
		}
		if err := s.attachSessionRuntime(&desktopSessionContext{
			core: agent, sessionID: record.ID, workspace: record.Workspace,
			mode: workspaceModeWorktree, stateDir: record.StateDir,
			worktreePath: record.WorktreePath, repositoryRoot: record.RepositoryRoot,
		}); err != nil {
			if createdCore {
				_ = agent.Close()
				delete(worktreeCores, record.StateDir)
			}
			status := "unavailable"
			_ = store.UpdateDesktopSession(context.Background(), record.ID, record.Title, status, record.UpdatedAt)
		}
	}
	return nil
}

func (s *DesktopService) newCore(workspace, stateDir string) (*core.Core, error) {
	helper := "lune-sandbox.exe"
	if runtime.GOOS != "windows" {
		helper = "lune-sandbox"
	}
	helperPath := filepath.Join(filepath.Dir(os.Args[0]), helper)
	if _, err := os.Stat(helperPath); err != nil {
		helperPath = ""
	}
	s.mu.Lock()
	modelConfig := s.modelConfig
	s.mu.Unlock()
	return core.New(core.Config{
		Workspace: workspace, StateDir: stateDir, Logger: slog.Default(),
		SandboxHelperPath: helperPath, ModelConfig: modelConfig,
	})
}

func (s *DesktopService) attachSessionRuntime(sessionContext *desktopSessionContext, replayFromStart ...bool) error {
	if sessionContext == nil || sessionContext.core == nil || sessionContext.sessionID == "" {
		return errors.New("会话运行时信息无效")
	}
	runtime, err := sessionContext.core.OpenSessionRuntime(context.Background(), sessionContext.sessionID)
	if err != nil {
		return fmt.Errorf("打开会话运行时: %w", err)
	}
	runtime.SetUserInteractionAvailable(true)
	sessionContext.runtime = runtime
	state, err := runtime.State(context.Background())
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.sessionContexts == nil {
		s.sessionContexts = make(map[string]*desktopSessionContext)
	}
	s.sessionContexts[sessionContext.sessionID] = sessionContext
	replay := len(replayFromStart) > 0 && replayFromStart[0]
	s.subscribeEventsLocked(sessionContext, replay)
	s.mu.Unlock()
	s.watchTurnLocked(runtime, sessionContext.sessionID, state.Task.ExecutionID)
	return nil
}

func sessionContextStatus(sessionContext *desktopSessionContext) string {
	if sessionContext == nil || sessionContext.runtime == nil {
		return "unavailable"
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

func (s *DesktopService) workspaceResult(needsMigration bool, preview *MigrationPreview) (WorkspaceOpenResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workspaceResultLocked(needsMigration, preview)
}

func (s *DesktopService) workspaceResultLocked(needsMigration bool, preview *MigrationPreview) (WorkspaceOpenResult, error) {
	localCore := s.localCore
	if localCore == nil {
		localCore = s.core
	}
	if localCore == nil {
		return WorkspaceOpenResult{}, errors.New("没有打开工作区")
	}
	sessions, err := localCore.ListSessions(context.Background(), 100, false)
	if err != nil {
		return WorkspaceOpenResult{}, err
	}
	result := WorkspaceOpenResult{
		Workspace: s.workspace, Permission: string(localCore.PermissionMode()), Models: s.projectModelView,
		NeedsMigration: needsMigration, Migration: preview,
	}
	if _, err := inspectGitWorkspace(s.workspace); err == nil {
		result.WorktreeAvailable = true
	}
	for _, session := range sessions {
		mode, workingDirectory, status := workspaceModeLocal, s.workspace, "idle"
		if context := s.sessionContexts[session.ID]; context != nil {
			mode, workingDirectory = context.mode, context.workspace
			status = sessionContextStatus(context)
		}
		result.Sessions = append(result.Sessions, SessionSummary{
			ID: session.ID, Title: session.Title, UpdatedAt: session.UpdatedAt.Format(time.RFC3339),
			Mode: mode, WorkingDirectory: workingDirectory, Status: status,
		})
	}
	if s.activityStore != nil {
		worktreeSessions, err := s.activityStore.DesktopSessions(context.Background())
		if err != nil {
			return WorkspaceOpenResult{}, err
		}
		for _, session := range worktreeSessions {
			status, workingDirectory := session.Status, session.Workspace
			if context := s.sessionContexts[session.ID]; context != nil {
				status, workingDirectory = sessionContextStatus(context), context.workspace
			}
			result.Sessions = append(result.Sessions, SessionSummary{
				ID: session.ID, Title: session.Title, UpdatedAt: session.UpdatedAt.Format(time.RFC3339),
				Mode: workspaceModeWorktree, WorkingDirectory: workingDirectory, Status: status,
				WorktreePath: session.WorktreePath,
			})
		}
	}
	sort.Slice(result.Sessions, func(i, j int) bool { return result.Sessions[i].UpdatedAt > result.Sessions[j].UpdatedAt })
	if len(result.Sessions) > 100 {
		result.Sessions = result.Sessions[:100]
	}
	return result, nil
}

func (s *DesktopService) CloseWorkspace(interrupt bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeWorkspaceLocked(interrupt)
}

func (s *DesktopService) closeWorkspaceLocked(interrupt bool) error {
	localCore := s.localCore
	if localCore == nil {
		localCore = s.core
	}
	if localCore == nil {
		return nil
	}
	uniqueContexts := make(map[*desktopSessionContext]struct{}, len(s.sessionContexts))
	for _, sessionContext := range s.sessionContexts {
		if sessionContext != nil {
			uniqueContexts[sessionContext] = struct{}{}
		}
	}
	for sessionContext := range uniqueContexts {
		if sessionContext.runtime == nil {
			continue
		}
		state, err := sessionContext.runtime.State(context.Background())
		if err != nil {
			return err
		}
		requests, err := sessionContext.runtime.PendingRequests(context.Background())
		if err != nil {
			return err
		}
		running := state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued
		pausedForUser := state.Task.Status == core.TaskPaused && len(requests) > 0
		if !running && !pausedForUser {
			continue
		}
		if !interrupt {
			return errors.New("当前工作区仍有任务或待处理请求，请先确认中断并关闭工作区")
		}
		if pausedForUser {
			if _, err := sessionContext.runtime.DiscardPaused(context.Background()); err != nil {
				return fmt.Errorf("中断待处理任务: %w", err)
			}
			continue
		}
		if state.Task.ExecutionID == "" {
			return errors.New("无法确定当前任务，暂时不能关闭工作区")
		}
		sessionContext.runtime.Interrupt(state.Task.ExecutionID)
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		_, waitErr := sessionContext.runtime.WaitTurn(ctx, state.Task.ExecutionID)
		cancel()
		if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
			return fmt.Errorf("等待任务停止: %w", waitErr)
		}
	}
	cores := map[*core.Core]struct{}{localCore: {}}
	for _, sessionContext := range s.sessionContexts {
		if sessionContext != nil && sessionContext.core != nil {
			cores[sessionContext.core] = struct{}{}
		}
	}
	var closeErr error
	for _, sessionContext := range s.sessionContexts {
		if sessionContext != nil && sessionContext.eventCancel != nil {
			sessionContext.eventCancel()
			sessionContext.eventCancel = nil
		}
	}
	for agent := range cores {
		if agent != nil {
			closeErr = errors.Join(closeErr, agent.Close())
		}
	}
	var activityCloseErr error
	if s.activityStore != nil {
		activityCloseErr = s.activityStore.Close()
	}
	s.core, s.localCore, s.runtime = nil, nil, nil
	s.workspace, s.activeWorkspace, s.sessionID = "", "", ""
	s.activeContext = nil
	s.sessionContexts = map[string]*desktopSessionContext{}
	s.activityStore, s.stateRoot, s.modelConfig = nil, "", nil
	s.projectModelView = nil
	s.planMode, s.previousMode = false, ""
	return errors.Join(closeErr, activityCloseErr)
}

func samePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

type ConversationMessage struct {
	ID     string   `json:"id"`
	Role   string   `json:"role"`
	Text   string   `json:"text"`
	Images []string `json:"images,omitempty"`
}

type SessionView struct {
	ID        string                `json:"id"`
	Title     string                `json:"title"`
	UpdatedAt string                `json:"updatedAt"`
	Settings  core.SessionSettings  `json:"settings"`
	Messages  []ConversationMessage `json:"messages"`
}

type WorkspaceState struct {
	Workspace        string                  `json:"workspace"`
	ProjectRoot      string                  `json:"projectRoot"`
	WorkspaceMode    string                  `json:"workspaceMode"`
	WorkingDirectory string                  `json:"workingDirectory"`
	WorktreePath     string                  `json:"worktreePath,omitempty"`
	Session          SessionView             `json:"session"`
	Sessions         []SessionSummary        `json:"sessions"`
	Permission       string                  `json:"permissionMode"`
	Models           *ModelSettingsView      `json:"models,omitempty"`
	State            core.RuntimeState       `json:"runtimeState"`
	Pending          []PendingRequestView    `json:"pending"`
	ExecutionHistory []ExecutionActivityView `json:"executionHistory"`
}

type PendingRequestView struct {
	Kind         string                         `json:"kind"`
	ID           string                         `json:"id"`
	ExecutionID  string                         `json:"executionId"`
	RunID        string                         `json:"runId"`
	InvocationID string                         `json:"invocationId"`
	CallID       string                         `json:"callId"`
	Tool         string                         `json:"tool"`
	Arguments    json.RawMessage                `json:"arguments,omitempty"`
	Questions    []publicmodel.UserQuestionItem `json:"questions,omitempty"`
	Reason       string                         `json:"reason,omitempty"`
}

func (s *DesktopService) OpenSession(sessionID string) (WorkspaceState, error) {
	s.mu.Lock()
	localCore := s.localCore
	if localCore == nil {
		localCore = s.core
	}
	if localCore == nil {
		s.mu.Unlock()
		return WorkspaceState{}, errors.New("请先打开工作区")
	}
	sessionContext := s.sessionContexts[sessionID]
	projectRoot, store, stateRoot := s.workspace, s.activityStore, s.stateRoot
	s.mu.Unlock()
	if sessionContext == nil {
		if _, err := localCore.GetSession(context.Background(), sessionID); err == nil {
			sessionContext = &desktopSessionContext{
				core: localCore, sessionID: sessionID, workspace: projectRoot,
				mode: workspaceModeLocal, stateDir: filepath.Join(stateRoot, "local"),
			}
			if err := s.attachSessionRuntime(sessionContext); err != nil {
				return WorkspaceState{}, err
			}
		} else {
			if store == nil {
				return WorkspaceState{}, err
			}
			record, found, recordErr := store.DesktopSession(context.Background(), sessionID)
			if recordErr != nil {
				return WorkspaceState{}, recordErr
			}
			if !found {
				return WorkspaceState{}, err
			}
			if _, statErr := os.Stat(record.Workspace); statErr != nil {
				return WorkspaceState{}, fmt.Errorf("Worktree 已不存在，保留目录信息供 Git 手动恢复: %s", record.WorktreePath)
			}
			s.mu.Lock()
			var agent *core.Core
			for _, existing := range s.sessionContexts {
				if existing != nil && existing.mode == workspaceModeWorktree && existing.stateDir == record.StateDir {
					agent = existing.core
					break
				}
			}
			s.mu.Unlock()
			createdCore := agent == nil
			if createdCore {
				var openErr error
				agent, openErr = s.newCore(record.Workspace, record.StateDir)
				if openErr != nil {
					if errors.Is(openErr, core.ErrStateDirInUse) {
						return WorkspaceState{}, errors.New("这个会话已由另一个 Desktop 实例打开")
					}
					return WorkspaceState{}, openErr
				}
			}
			sessionContext = &desktopSessionContext{
				core: agent, sessionID: sessionID, workspace: record.Workspace, mode: workspaceModeWorktree,
				stateDir: record.StateDir, worktreePath: record.WorktreePath, repositoryRoot: record.RepositoryRoot,
			}
			if err := s.attachSessionRuntime(sessionContext); err != nil {
				if createdCore {
					_ = agent.Close()
				}
				return WorkspaceState{}, err
			}
		}
	}
	s.mu.Lock()
	s.setActiveSessionContextLocked(sessionContext)
	defer s.mu.Unlock()
	saved, err := sessionContext.core.GetSession(context.Background(), sessionID)
	if err != nil {
		return WorkspaceState{}, err
	}
	runtime := sessionContext.runtime
	state, err := runtime.State(context.Background())
	if err != nil {
		return WorkspaceState{}, err
	}
	pending, err := runtime.PendingRequests(context.Background())
	if err != nil {
		return WorkspaceState{}, err
	}
	list, err := s.workspaceResultLocked(false, nil)
	if err != nil {
		return WorkspaceState{}, err
	}
	activityHistory, err := s.loadExecutionHistoryLocked(sessionID)
	if err != nil {
		return WorkspaceState{}, err
	}
	view := SessionView{ID: saved.ID, Title: saved.Title, UpdatedAt: saved.UpdatedAt.Format(time.RFC3339), Settings: saved.Settings, Messages: transcriptMessages(saved.History)}
	if sessionContext.mode == workspaceModeWorktree && s.activityStore != nil {
		_ = s.activityStore.UpdateDesktopSession(context.Background(), saved.ID, saved.Title, sessionContextStatus(sessionContext), saved.UpdatedAt)
	}
	s.watchTurnLocked(runtime, sessionID, state.Task.ExecutionID)
	return WorkspaceState{
		Workspace: sessionContext.workspace, ProjectRoot: s.workspace, WorkspaceMode: sessionContext.mode,
		WorkingDirectory: sessionContext.workspace, WorktreePath: sessionContext.worktreePath, Session: view, Sessions: list.Sessions,
		Permission: string(s.core.PermissionMode()), Models: s.projectModelView, State: state, Pending: pendingRequestViews(pending),
		ExecutionHistory: activityHistory,
	}, nil
}

func (s *DesktopService) CreateSession(mode string) (WorkspaceState, error) {
	s.mu.Lock()
	localCore := s.localCore
	if localCore == nil {
		localCore = s.core
	}
	if localCore == nil {
		s.mu.Unlock()
		return WorkspaceState{}, errors.New("请先打开工作区")
	}
	if mode == "" {
		mode = workspaceModeLocal
	}
	if mode != workspaceModeLocal && mode != workspaceModeWorktree {
		s.mu.Unlock()
		return WorkspaceState{}, errors.New("会话工作区模式无效")
	}
	projectRoot, stateRoot, store := s.workspace, s.stateRoot, s.activityStore
	s.mu.Unlock()
	if mode == workspaceModeLocal {
		session, err := localCore.CreateSession(context.Background(), nil)
		if err != nil {
			return WorkspaceState{}, err
		}
		sessionContext := &desktopSessionContext{
			core: localCore, sessionID: session.ID, workspace: projectRoot,
			mode: workspaceModeLocal, stateDir: filepath.Join(stateRoot, "local"),
		}
		if err := s.attachSessionRuntime(sessionContext); err != nil {
			return WorkspaceState{}, err
		}
		return s.OpenSession(session.ID)
	}
	if store == nil {
		return WorkspaceState{}, errors.New("Desktop 会话存储尚未就绪")
	}
	gitInfo, err := inspectGitWorkspace(projectRoot)
	if err != nil {
		return WorkspaceState{}, err
	}
	key, err := newWorktreeID()
	if err != nil {
		return WorkspaceState{}, err
	}
	worktreePath, err := desktopWorktreeDir(projectRoot, key)
	if err != nil {
		return WorkspaceState{}, err
	}
	gitInfo, workingDirectory, err := createGitWorktree(projectRoot, worktreePath)
	if err != nil {
		return WorkspaceState{}, err
	}
	stateDir := filepath.Join(stateRoot, "worktree-state", key)
	agent, err := s.newCore(workingDirectory, stateDir)
	if err != nil {
		cleanupErr := removeGitWorktree(gitInfo.Root, worktreePath)
		stateCleanupErr := removeDesktopWorktreeStateDir(stateRoot, stateDir)
		return WorkspaceState{}, errors.Join(fmt.Errorf("打开 Worktree 会话失败: %w", err), cleanupErr, stateCleanupErr)
	}
	session, err := agent.CreateSession(context.Background(), nil)
	if err != nil {
		_ = agent.Close()
		cleanupErr := removeGitWorktree(gitInfo.Root, worktreePath)
		stateCleanupErr := removeDesktopWorktreeStateDir(stateRoot, stateDir)
		return WorkspaceState{}, errors.Join(fmt.Errorf("创建 Worktree 会话失败: %w", err), cleanupErr, stateCleanupErr)
	}
	record := DesktopSessionRecord{
		ID: session.ID, Mode: workspaceModeWorktree, Workspace: workingDirectory, StateDir: stateDir,
		WorktreePath: worktreePath, RepositoryRoot: gitInfo.Root, Title: session.Title,
		UpdatedAt: session.UpdatedAt, Status: "idle",
	}
	if err := store.SaveDesktopSession(context.Background(), record); err != nil {
		_ = agent.Close()
		cleanupErr := removeGitWorktree(gitInfo.Root, worktreePath)
		stateCleanupErr := removeDesktopWorktreeStateDir(stateRoot, stateDir)
		return WorkspaceState{}, errors.Join(fmt.Errorf("保存 Worktree 会话: %w", err), cleanupErr, stateCleanupErr)
	}
	sessionContext := &desktopSessionContext{
		core: agent, sessionID: session.ID, workspace: workingDirectory, mode: workspaceModeWorktree,
		stateDir: stateDir, worktreePath: worktreePath, repositoryRoot: gitInfo.Root,
	}
	if err := s.attachSessionRuntime(sessionContext); err != nil {
		_ = store.DeleteDesktopSession(context.Background(), session.ID)
		_ = agent.Close()
		cleanupErr := removeGitWorktree(gitInfo.Root, worktreePath)
		stateCleanupErr := removeDesktopWorktreeStateDir(stateRoot, stateDir)
		return WorkspaceState{}, errors.Join(fmt.Errorf("打开 Worktree 会话: %w", err), cleanupErr, stateCleanupErr)
	}
	return s.OpenSession(session.ID)
}

func (s *DesktopService) SetSessionTitle(sessionID, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessionContext := s.sessionContexts[sessionID]
	if sessionContext == nil || sessionContext.core == nil {
		return errors.New("没有打开工作区")
	}
	if err := sessionContext.core.SetSessionTitle(context.Background(), sessionID, title); err != nil {
		return err
	}
	if sessionContext.mode == workspaceModeWorktree && s.activityStore != nil {
		return s.activityStore.UpdateDesktopSession(context.Background(), sessionID, title, sessionContextStatus(sessionContext), time.Now())
	}
	return nil
}

func (s *DesktopService) setActiveSessionContextLocked(sessionContext *desktopSessionContext) {
	s.activeContext = sessionContext
	s.core, s.runtime = sessionContext.core, sessionContext.runtime
	s.sessionID = sessionContext.sessionID
	s.activeWorkspace = sessionContext.workspace
	s.planMode, s.previousMode = sessionContext.planMode, sessionContext.previousMode
}

func (s *DesktopService) SetSessionModel(profile string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.core == nil || s.sessionID == "" {
		return errors.New("没有打开会话")
	}
	saved, err := s.core.GetSession(context.Background(), s.sessionID)
	if err != nil {
		return err
	}
	saved.Settings.ModelProfile = profile
	return s.core.SetSessionSettings(context.Background(), s.sessionID, saved.Settings)
}

func (s *DesktopService) SetPermissionMode(mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.core == nil {
		return errors.New("没有打开工作区")
	}
	if s.planMode {
		return errors.New("方案模式期间权限固定为只读")
	}
	return s.core.SetPermissionMode(core.PermissionMode(mode))
}

func (s *DesktopService) SetPlanMode(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.core == nil {
		return errors.New("没有打开工作区")
	}
	if enabled == s.planMode {
		return nil
	}
	if enabled {
		s.previousMode = s.core.PermissionMode()
		if err := s.core.SetPermissionMode(core.PermissionReadOnly); err != nil {
			return err
		}
		s.planMode = true
	} else {
		if err := s.core.SetPermissionMode(s.previousMode); err != nil {
			return err
		}
		s.planMode = false
	}
	if s.activeContext != nil {
		s.activeContext.planMode = s.planMode
		s.activeContext.previousMode = s.previousMode
	}
	return nil
}

type ImageInput struct {
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Data      string `json:"data"`
}

type SubmitInput struct {
	Text             string       `json:"text"`
	Images           []ImageInput `json:"images"`
	UserMessageIndex *int         `json:"userMessageIndex,omitempty"`
}

type contentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type userMessage struct {
	Type    string        `json:"type"`
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

func (s *DesktopService) Submit(input SubmitInput) (core.TurnSubmission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtime == nil || s.core == nil {
		return core.TurnSubmission{}, errors.New("请先打开会话")
	}
	taskInput, err := s.buildTaskInput(input)
	if err != nil {
		return core.TurnSubmission{}, err
	}
	state, err := s.runtime.State(context.Background())
	if err != nil {
		return core.TurnSubmission{}, err
	}
	if state.Task.Status == core.TaskPaused {
		return core.TurnSubmission{}, errors.New("请先处理当前等待中的请求")
	}
	mode := core.StartIfIdle
	if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued || len(state.Queue) > 0 {
		mode = core.QueueInput
	}
	return s.submitPreparedLocked(taskInput, input, mode)
}

func (s *DesktopService) EditUserMessage(messageID string, input SubmitInput) (core.TurnSubmission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtime == nil || s.core == nil {
		return core.TurnSubmission{}, errors.New("请先打开会话")
	}
	if strings.TrimSpace(messageID) == "" {
		return core.TurnSubmission{}, errors.New("用户消息标识无效")
	}
	taskInput, err := s.buildTaskInput(input)
	if err != nil {
		return core.TurnSubmission{}, err
	}
	state, err := s.runtime.State(context.Background())
	if err != nil {
		return core.TurnSubmission{}, err
	}
	pending, err := s.runtime.PendingRequests(context.Background())
	if err != nil {
		return core.TurnSubmission{}, err
	}
	if len(pending) > 0 || state.Task.Status == core.TaskPaused {
		return core.TurnSubmission{}, errors.New("请先处理当前等待中的请求")
	}
	saved, err := s.core.GetSession(context.Background(), s.sessionID)
	if err != nil {
		return core.TurnSubmission{}, err
	}
	conversation := transcriptMessages(saved.History)
	userMessageIndex := findTranscriptUserMessageIndex(conversation, messageID)
	if userMessageIndex >= 0 {
		return s.editConversationFromIndexLocked(messageID, userMessageIndex, input, taskInput)
	}

	queuedTarget := core.QueuedTurnState{}
	queuedFound := false
	for _, item := range state.Queue {
		if item.ExecutionID == messageID {
			queuedTarget, queuedFound = item, true
			break
		}
	}
	activityIndex := input.UserMessageIndex
	if s.activityStore != nil {
		runs, listErr := s.activityStore.ListSession(context.Background(), s.sessionID)
		if listErr != nil {
			return core.TurnSubmission{}, listErr
		}
		for _, run := range runs {
			if run.ExecutionID == messageID {
				index := run.UserMessageIndex
				activityIndex = &index
				break
			}
		}
	}
	if !queuedFound && activityIndex == nil {
		return core.TurnSubmission{}, errors.New("要编辑的用户消息已不在当前会话历史或任务队列中")
	}
	if activityIndex == nil || *activityIndex < 0 {
		return core.TurnSubmission{}, errors.New("无法确定排队消息在会话中的位置")
	}
	if queuedFound && queuedTarget.Status == "queued" {
		submission, cancelled, replaceErr := s.runtime.ReplaceQueuedTurnInput(context.Background(), messageID, taskInput)
		if replaceErr == nil {
			index := *activityIndex
			input.UserMessageIndex = &index
			if s.activityStore != nil {
				if err := s.activityStore.AssociateExecution(context.Background(), s.sessionID, submission.ExecutionID, index, "queued"); err != nil {
					slog.Warn("update edited queued execution", "error", err)
				}
				images := imageDataURLs(input.Images)
				if err := s.activityStore.SaveExecutionMessage(context.Background(), s.sessionID, submission.ExecutionID, strings.TrimSpace(input.Text), images, "queued", submission.Position); err != nil {
					slog.Warn("update edited queued message", "error", err)
				}
				if err := s.activityStore.DeleteFromUserMessageIndexExcept(context.Background(), s.sessionID, index+1, ""); err != nil {
					slog.Warn("remove queue entries after edited message", "error", err)
				} else {
					s.suppressExecutionActivityLocked(cancelled)
				}
			} else {
				s.suppressExecutionActivityLocked(cancelled)
			}
			return submission, nil
		}
		if !errors.Is(replaceErr, core.ErrQueuedTurnStarted) && !errors.Is(replaceErr, core.ErrTurnNotFound) {
			return core.TurnSubmission{}, replaceErr
		}
	}
	return s.editConversationFromIndexLocked("", *activityIndex, input, taskInput)
}

func (s *DesktopService) CancelQueuedTurn(executionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtime == nil || s.core == nil {
		return errors.New("请先打开会话")
	}
	return s.runtime.CancelQueuedTurn(context.Background(), strings.TrimSpace(executionID))
}

func findTranscriptUserMessageIndex(messages []ConversationMessage, messageID string) int {
	userIndex := 0
	for _, message := range messages {
		if message.Role != "user" {
			continue
		}
		if message.ID == messageID {
			return userIndex
		}
		userIndex++
	}
	return -1
}

func (s *DesktopService) editConversationFromIndexLocked(messageID string, requestedIndex int, input SubmitInput, taskInput core.TaskInput) (core.TurnSubmission, error) {
	state, err := s.runtime.State(context.Background())
	if err != nil {
		return core.TurnSubmission{}, err
	}
	pending, err := s.runtime.PendingRequests(context.Background())
	if err != nil {
		return core.TurnSubmission{}, err
	}
	if len(pending) > 0 || state.Task.Status == core.TaskPaused {
		return core.TurnSubmission{}, errors.New("请先处理当前等待中的请求")
	}
	var affected []string
	if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued || len(state.Queue) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		affected, err = s.runtime.CancelPendingTurns(ctx)
		cancel()
		if err != nil {
			return core.TurnSubmission{}, fmt.Errorf("中断当前任务并清空后续队列失败: %w", err)
		}
	}
	saved, err := s.core.GetSession(context.Background(), s.sessionID)
	if err != nil {
		return core.TurnSubmission{}, err
	}
	conversation := transcriptMessages(saved.History)
	userMessageCount := 0
	for _, message := range conversation {
		if message.Role == "user" {
			userMessageCount++
		}
	}
	userMessageIndex := requestedIndex
	if messageID != "" {
		if resolved := findTranscriptUserMessageIndex(conversation, messageID); resolved >= 0 {
			userMessageIndex = resolved
		}
	}
	if userMessageIndex < 0 || userMessageIndex > userMessageCount || (userMessageIndex == userMessageCount && len(affected) == 0) {
		return core.TurnSubmission{}, errors.New("要编辑的用户消息已不在当前会话历史中")
	}
	originalHistory := append(json.RawMessage(nil), saved.History...)
	rewound := false
	if userMessageIndex < userMessageCount {
		removeCount := userMessageCount - userMessageIndex
		removed, err := s.core.RewindSession(context.Background(), s.sessionID, removeCount)
		if err != nil {
			return core.TurnSubmission{}, err
		}
		if removed != removeCount {
			_ = s.core.SetSessionHistory(context.Background(), s.sessionID, originalHistory)
			return core.TurnSubmission{}, errors.New("回退会话历史时消息数量发生变化")
		}
		rewound = true
	}
	input.UserMessageIndex = &userMessageIndex
	submission, err := s.submitPreparedLocked(taskInput, input, core.StartIfIdle)
	if err != nil {
		if rewound {
			err = errors.Join(err, s.core.SetSessionHistory(context.Background(), s.sessionID, originalHistory))
		}
		return core.TurnSubmission{}, err
	}
	if s.activityStore != nil {
		if err := s.activityStore.DeleteFromUserMessageIndexExcept(context.Background(), s.sessionID, userMessageIndex, submission.ExecutionID); err != nil {
			slog.Warn("remove execution history after editing a user message", "error", err)
		} else {
			s.suppressExecutionActivityLocked(affected)
		}
	} else {
		s.suppressExecutionActivityLocked(affected)
	}
	return submission, nil
}

func imageDataURLs(images []ImageInput) []string {
	result := make([]string, 0, len(images))
	for _, item := range images {
		result = append(result, "data:"+item.MediaType+";base64,"+item.Data)
	}
	return result
}

func (s *DesktopService) buildTaskInput(input SubmitInput) (core.TaskInput, error) {
	text := strings.TrimSpace(input.Text)
	if text == "" && len(input.Images) == 0 {
		return core.TaskInput{}, errors.New("请输入内容或添加图片")
	}
	var taskInput core.TaskInput
	var err error
	if len(input.Images) == 0 {
		taskInput, err = core.NewTaskInput(text)
	} else {
		if s.core == nil {
			return core.TaskInput{}, errors.New("请先打开工作区")
		}
		limits := s.core.ProjectAttachmentLimits()
		if len(input.Images) > limits.MaxFiles {
			return core.TaskInput{}, fmt.Errorf("图片数量超出限制（最多 %d 张）", limits.MaxFiles)
		}
		parts := make([]contentPart, 0, len(input.Images)*2+1)
		if text != "" {
			parts = append(parts, contentPart{Type: "input_text", Text: text})
		}
		var total int64
		for _, item := range input.Images {
			if item.MediaType != "image/png" && item.MediaType != "image/jpeg" && item.MediaType != "image/gif" && item.MediaType != "image/webp" {
				return core.TaskInput{}, fmt.Errorf("不支持的图片格式: %s", item.MediaType)
			}
			data, decodeErr := base64.StdEncoding.DecodeString(item.Data)
			if decodeErr != nil {
				return core.TaskInput{}, fmt.Errorf("读取图片 %q: %w", item.Name, decodeErr)
			}
			if int64(len(data)) > limits.MaxFileBytes {
				return core.TaskInput{}, fmt.Errorf("图片 %q 超出单文件大小限制", item.Name)
			}
			total += int64(len(data))
			if total > limits.MaxTotalBytes {
				return core.TaskInput{}, errors.New("图片总大小超出限制")
			}
			config, format, decodeErr := image.DecodeConfig(bytes.NewReader(data))
			if decodeErr != nil || config.Width < 1 || config.Height < 1 || !supportedImageFormat(format) {
				return core.TaskInput{}, fmt.Errorf("图片 %q 无法解码", item.Name)
			}
			parts = append(parts, contentPart{Type: "input_image", ImageURL: "data:" + item.MediaType + ";base64," + base64.StdEncoding.EncodeToString(data)})
		}
		taskInput, err = core.NewTaskInput([]userMessage{{Type: "message", Role: "user", Content: parts}})
	}
	if err != nil {
		return core.TaskInput{}, err
	}
	return taskInput, nil
}

func (s *DesktopService) submitPreparedLocked(taskInput core.TaskInput, input SubmitInput, mode core.SubmissionMode) (core.TurnSubmission, error) {
	request := core.TurnRequest{Input: taskInput, PlanMode: s.planMode}
	submission, err := s.runtime.SubmitTurn(context.Background(), request, mode)
	if err != nil {
		return core.TurnSubmission{}, err
	}
	messageIndex := userMessageIndex(input.UserMessageIndex, s.core, s.sessionID)
	if s.activityStore != nil {
		status := "running"
		if submission.Status == core.SubmissionQueued {
			status = "queued"
		}
		if err := s.activityStore.AssociateExecution(context.Background(), s.sessionID, submission.ExecutionID, messageIndex, status); err != nil {
			slog.Warn("save desktop execution activity", "error", err)
		}
		images := make([]string, 0, len(input.Images))
		for _, item := range input.Images {
			images = append(images, "data:"+item.MediaType+";base64,"+item.Data)
		}
		if err := s.activityStore.SaveExecutionMessage(context.Background(), s.sessionID, submission.ExecutionID, strings.TrimSpace(input.Text), images, status, submission.Position); err != nil {
			slog.Warn("save desktop queued message", "error", err)
		}
	}
	s.watchTurnLocked(s.runtime, s.sessionID, submission.ExecutionID)
	return submission, nil
}

func userMessageIndex(requested *int, agent *core.Core, sessionID string) int {
	if requested != nil && *requested >= 0 {
		return *requested
	}
	if agent == nil || sessionID == "" {
		return -1
	}
	saved, err := agent.GetSession(context.Background(), sessionID)
	if err != nil {
		return -1
	}
	index := 0
	for _, message := range transcriptMessages(saved.History) {
		if message.Role == "user" {
			index++
		}
	}
	return index
}

func supportedImageFormat(format string) bool {
	switch strings.ToLower(format) {
	case "png", "jpeg", "gif", "webp":
		return true
	default:
		return false
	}
}

func (s *DesktopService) Interrupt() bool {
	s.mu.Lock()
	runtime := s.runtime
	s.mu.Unlock()
	if runtime == nil {
		return false
	}
	state, err := runtime.State(context.Background())
	if err != nil {
		return false
	}
	if state.Task.Status == core.TaskPaused {
		discarded, err := runtime.DiscardPaused(context.Background())
		if err == nil && discarded {
			saveInterruptedActivity(s, runtime.SessionID(), state.Task.ExecutionID)
		}
		return err == nil && discarded
	}
	if state.Task.Status != core.TaskRunning && state.Task.Status != core.TaskQueued {
		return false
	}
	executionID := state.Task.ExecutionID
	if executionID == "" || !runtime.Interrupt(executionID) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	result, err := runtime.WaitTurn(ctx, executionID)
	if err != nil {
		return false
	}
	if result.Status == core.TaskPaused {
		discarded, discardErr := runtime.DiscardPaused(ctx)
		if discardErr != nil || !discarded {
			return false
		}
		saveInterruptedActivity(s, runtime.SessionID(), executionID)
	}
	return true
}

func saveInterruptedActivity(service *DesktopService, sessionID, executionID string) {
	if service == nil || sessionID == "" || executionID == "" {
		return
	}
	service.mu.Lock()
	store := service.activityStore
	service.mu.Unlock()
	if store == nil {
		return
	}
	if err := store.UpsertExecution(context.Background(), sessionID, executionID, nil, "interrupted"); err != nil {
		slog.Warn("save interrupted execution activity", "error", err)
	}
	if err := store.UpdateExecutionMessageStatus(context.Background(), sessionID, executionID, "interrupted", 0); err != nil {
		slog.Warn("remove interrupted desktop message receipt", "error", err)
	}
}

func (s *DesktopService) ResolveApproval(id string, allowed bool) error {
	s.mu.Lock()
	runtime := s.runtime
	s.mu.Unlock()
	if runtime == nil {
		return errors.New("会话已关闭")
	}
	return runtime.ResolveApproval(id, allowed)
}

type QuestionAnswerInput struct {
	ExecutionID  string                             `json:"executionId"`
	RunID        string                             `json:"runId"`
	InvocationID string                             `json:"invocationId"`
	CallID       string                             `json:"callId"`
	Answers      []publicmodel.UserQuestionResponse `json:"answers"`
}

func (s *DesktopService) AnswerQuestion(input QuestionAnswerInput) error {
	s.mu.Lock()
	runtime := s.runtime
	s.mu.Unlock()
	if runtime == nil {
		return errors.New("会话已关闭")
	}
	return runtime.ResolveUserQuestion(context.Background(), core.UserQuestionAnswer{
		ExecutionID: input.ExecutionID, RunID: input.RunID, InvocationID: input.InvocationID,
		CallID: input.CallID, Answers: input.Answers,
	})
}

func (s *DesktopService) ResolveToolRecovery(invocationID, decision string) error {
	s.mu.Lock()
	runtime := s.runtime
	s.mu.Unlock()
	if runtime == nil {
		return errors.New("会话已关闭")
	}
	return runtime.ResolveToolRecovery(context.Background(), invocationID, core.ToolRecoveryDecision(decision))
}

func (s *DesktopService) RecoverModel() error {
	s.mu.Lock()
	runtime := s.runtime
	s.mu.Unlock()
	if runtime == nil {
		return errors.New("会话已关闭")
	}
	submission, err := runtime.Recover(context.Background())
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.watchTurnLocked(runtime, runtime.SessionID(), submission.ExecutionID)
	s.mu.Unlock()
	return nil
}

type PlanResult struct {
	SessionID   string `json:"sessionId"`
	ExecutionID string `json:"executionId"`
}

func (s *DesktopService) ImplementPlan(executionID, mode string) (PlanResult, error) {
	s.mu.Lock()
	runtime := s.runtime
	if runtime == nil || s.core == nil {
		s.mu.Unlock()
		return PlanResult{}, errors.New("没有等待确认的方案")
	}
	if s.planMode {
		if err := s.core.SetPermissionMode(s.previousMode); err != nil {
			s.mu.Unlock()
			return PlanResult{}, err
		}
		s.planMode = false
		if s.activeContext != nil {
			s.activeContext.planMode = false
		}
	}
	sourceContext := s.activeContext
	s.mu.Unlock()
	implementation, err := runtime.ImplementPlan(context.Background(), executionID, core.PlanImplementationMode(mode))
	if err != nil {
		return PlanResult{}, err
	}
	s.mu.Lock()
	targetContext := s.sessionContexts[implementation.SessionID]
	s.mu.Unlock()
	if targetContext == nil {
		if sourceContext == nil {
			return PlanResult{}, errors.New("无法关联方案实施会话")
		}
		saved, err := sourceContext.core.GetSession(context.Background(), implementation.SessionID)
		if err != nil {
			return PlanResult{}, err
		}
		targetContext = &desktopSessionContext{
			core: sourceContext.core, sessionID: implementation.SessionID, workspace: sourceContext.workspace,
			mode: sourceContext.mode, stateDir: sourceContext.stateDir, worktreePath: sourceContext.worktreePath,
			repositoryRoot: sourceContext.repositoryRoot,
		}
		if targetContext.mode == workspaceModeWorktree && s.activityStore != nil {
			if err := s.activityStore.SaveDesktopSession(context.Background(), DesktopSessionRecord{
				ID: saved.ID, Mode: workspaceModeWorktree, Workspace: targetContext.workspace, StateDir: targetContext.stateDir,
				WorktreePath: targetContext.worktreePath, RepositoryRoot: targetContext.repositoryRoot,
				Title: saved.Title, UpdatedAt: saved.UpdatedAt, Status: "running",
			}); err != nil {
				return PlanResult{}, err
			}
		}
		if err := s.attachSessionRuntime(targetContext, true); err != nil {
			return PlanResult{}, err
		}
	}
	s.mu.Lock()
	s.watchTurnLocked(targetContext.runtime, implementation.SessionID, implementation.Submission.ExecutionID)
	s.mu.Unlock()
	return PlanResult{SessionID: implementation.SessionID, ExecutionID: implementation.Submission.ExecutionID}, nil
}

func (s *DesktopService) RestorePlanMode() error { return s.SetPlanMode(true) }

func pendingRequestViews(requests []core.PendingRequest) []PendingRequestView {
	var result []PendingRequestView
	for _, request := range requests {
		view := pendingRequestView(request)
		if view.Kind != "" {
			result = append(result, view)
		}
	}
	return result
}

func pendingRequestView(request core.PendingRequest) PendingRequestView {
	switch request.Kind {
	case core.PendingApproval:
		if request.Approval == nil {
			break
		}
		return PendingRequestView{Kind: string(request.Kind), ID: request.Approval.ID, Tool: request.Approval.Tool, Arguments: request.Approval.Arguments}
	case core.PendingUserQuestion:
		if request.UserQuestion == nil {
			break
		}
		q := request.UserQuestion
		return PendingRequestView{Kind: string(request.Kind), ID: q.ID, ExecutionID: q.ExecutionID, RunID: q.RunID, InvocationID: q.InvocationID, CallID: q.CallID, Questions: q.Questions}
	case core.PendingToolRecovery:
		if request.ToolRecovery == nil {
			break
		}
		r := request.ToolRecovery
		return PendingRequestView{Kind: string(request.Kind), ID: r.InvocationID, InvocationID: r.InvocationID, CallID: r.CallID, Tool: r.Tool, Arguments: r.Arguments}
	case core.PendingModelRecoveryRequest:
		if request.ModelRecovery == nil {
			break
		}
		r := request.ModelRecovery
		return PendingRequestView{Kind: string(request.Kind), ID: r.CallID, CallID: r.CallID, ExecutionID: r.ExecutionID, InvocationID: r.InvocationID, RunID: r.RunID, Reason: string(r.Failure.Category)}
	case core.PendingExecutionBlocker:
		if request.Blocker == nil {
			break
		}
		return PendingRequestView{Kind: string(request.Kind), ID: request.Blocker.ID, ExecutionID: request.Blocker.ExecutionID, Tool: request.Blocker.Tool, Reason: request.Blocker.Reason}
	case core.PendingUserInteraction:
		if request.Interaction == nil {
			break
		}
		return PendingRequestView{Kind: string(request.Kind), ID: request.Interaction.ID, Tool: request.Interaction.ServerName, Arguments: request.Interaction.Request}
	}
	return PendingRequestView{}
}

func (s *DesktopService) RefreshSession() (WorkspaceState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.core == nil || s.runtime == nil {
		return WorkspaceState{}, errors.New("没有打开会话")
	}
	saved, err := s.core.GetSession(context.Background(), s.sessionID)
	if err != nil {
		return WorkspaceState{}, err
	}
	state, err := s.runtime.State(context.Background())
	if err != nil {
		return WorkspaceState{}, err
	}
	requests, err := s.runtime.PendingRequests(context.Background())
	if err != nil {
		return WorkspaceState{}, err
	}
	list, err := s.workspaceResultLocked(false, nil)
	if err != nil {
		return WorkspaceState{}, err
	}
	activityHistory, err := s.loadExecutionHistoryLocked(s.sessionID)
	if err != nil {
		return WorkspaceState{}, err
	}
	return WorkspaceState{
		Workspace: s.activeWorkspace, ProjectRoot: s.workspace, WorkspaceMode: s.activeContextModeLocked(),
		WorkingDirectory: s.activeWorkspace, WorktreePath: s.activeWorktreePathLocked(),
		Session:  SessionView{ID: saved.ID, Title: saved.Title, UpdatedAt: saved.UpdatedAt.Format(time.RFC3339), Settings: saved.Settings, Messages: transcriptMessages(saved.History)},
		Sessions: list.Sessions, Permission: string(s.core.PermissionMode()), Models: s.projectModelView, State: state,
		Pending: pendingRequestViews(requests), ExecutionHistory: activityHistory,
	}, nil
}

func (s *DesktopService) activeContextModeLocked() string {
	if s.activeContext == nil {
		return workspaceModeLocal
	}
	return s.activeContext.mode
}

func (s *DesktopService) activeWorktreePathLocked() string {
	if s.activeContext == nil {
		return ""
	}
	return s.activeContext.worktreePath
}

func (s *DesktopService) loadExecutionHistoryLocked(sessionID string) ([]ExecutionActivityView, error) {
	if s.activityStore == nil {
		return []ExecutionActivityView{}, nil
	}
	return s.activityStore.ListSession(context.Background(), sessionID)
}

type AgentEvent struct {
	Kind        string       `json:"kind"`
	SessionID   string       `json:"sessionId"`
	TaskID      string       `json:"taskId"`
	ExecutionID string       `json:"executionId"`
	Status      string       `json:"status"`
	Err         string       `json:"error,omitempty"`
	Tool        ToolEvent    `json:"tool"`
	Stream      *StreamEvent `json:"stream,omitempty"`
	ApprovalID  string       `json:"approvalId,omitempty"`
}

type ToolEvent struct {
	ID           string                    `json:"id"`
	InvocationID string                    `json:"invocationId"`
	AgentID      string                    `json:"agentId,omitempty"`
	Name         string                    `json:"name"`
	Arguments    json.RawMessage           `json:"arguments,omitempty"`
	Details      *ToolExecutionDetailsView `json:"details,omitempty"`
	Started      bool                      `json:"started"`
	Failed       bool                      `json:"failed"`
	Canceled     bool                      `json:"canceled"`
	Duration     int64                     `json:"durationMs"`
	Truncated    bool                      `json:"truncated,omitempty"`
}

type StreamEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

func (s *DesktopService) subscribeEventsLocked(sessionContext *desktopSessionContext, replayFromStart bool) {
	cursor := int64(0)
	if !replayFromStart {
		cursor = latestEventCursor(sessionContext.runtime)
	}
	base := s.appContext
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	sessionContext.eventCancel = cancel
	events, err := sessionContext.runtime.SubscribeEvents(ctx, cursor)
	if err != nil {
		cancel()
		s.emit("agent-error", map[string]string{"message": err.Error(), "sessionId": sessionContext.sessionID})
		return
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				if s.persistActivityEvent(event) {
					continue
				}
				s.emit("agent-event", toAgentEvent(event))
				if sessionSummaryEvent(event.Kind) {
					s.updateSessionSummary(event.SessionID, string(event.Status))
				}
				if pendingRequestChanged(event.Kind) {
					s.emit("pending-changed", sessionContext.sessionID)
				}
			}
		}
	}()
}

func sessionSummaryEvent(kind core.EventKind) bool {
	switch kind {
	case core.EventTaskQueued, core.EventTaskStarted, core.EventTaskFinished, core.EventTaskDiscarded,
		core.EventApprovalRequired, core.EventApprovalCancelled, core.EventUserQuestionRequired,
		core.EventUserInteractionRequired, core.EventUserInteractionCancelled, core.EventModelRecoveryRequired,
		core.EventModelRecoveryResumed, core.EventToolRecoveryRequired:
		return true
	default:
		return false
	}
}

func (s *DesktopService) updateSessionSummary(sessionID, status string) {
	s.mu.Lock()
	sessionContext, store := s.sessionContexts[sessionID], s.activityStore
	s.mu.Unlock()
	if sessionContext == nil || sessionContext.mode != workspaceModeWorktree || store == nil {
		return
	}
	saved, err := sessionContext.core.GetSession(context.Background(), sessionID)
	if err != nil {
		return
	}
	if status == "" {
		status = sessionContextStatus(sessionContext)
	}
	if err := store.UpdateDesktopSession(context.Background(), sessionID, saved.Title, status, saved.UpdatedAt); err != nil {
		slog.Warn("update Worktree session summary", "session_id", sessionID, "error", err)
	}
}

func (s *DesktopService) persistActivityEvent(event core.Event) bool {
	s.mu.Lock()
	if _, suppressed := s.suppressedActivity[event.ExecutionID]; suppressed {
		s.mu.Unlock()
		return true
	}
	store := s.activityStore
	s.mu.Unlock()
	if store == nil || event.SessionID == "" || event.ExecutionID == "" {
		return false
	}
	ctx := context.Background()
	switch event.Kind {
	case core.EventTaskQueued:
		if err := store.UpsertExecution(ctx, event.SessionID, event.ExecutionID, nil, "queued"); err != nil {
			slog.Warn("save queued desktop execution activity", "error", err)
		}
		if err := store.UpdateExecutionMessageStatus(ctx, event.SessionID, event.ExecutionID, "queued", 0); err != nil {
			slog.Warn("save queued desktop message status", "error", err)
		}
	case core.EventTaskStarted:
		if err := store.UpsertExecution(ctx, event.SessionID, event.ExecutionID, nil, "running"); err != nil {
			slog.Warn("save desktop execution activity", "error", err)
		}
		if err := store.UpdateExecutionMessageStatus(ctx, event.SessionID, event.ExecutionID, "running", 0); err != nil {
			slog.Warn("save running desktop message status", "error", err)
		}
	case core.EventTaskFinished:
		status := activityStatus(string(event.Status))
		if err := store.UpsertExecution(ctx, event.SessionID, event.ExecutionID, nil, status); err != nil {
			slog.Warn("save desktop execution activity status", "error", err)
		}
		if err := store.UpdateExecutionMessageStatus(ctx, event.SessionID, event.ExecutionID, status, 0); err != nil {
			slog.Warn("save finished desktop message status", "error", err)
		}
	case core.EventTaskDiscarded:
		if err := store.UpsertExecution(ctx, event.SessionID, event.ExecutionID, nil, "interrupted"); err != nil {
			slog.Warn("save interrupted execution activity", "error", err)
		}
		if err := store.UpdateExecutionMessageStatus(ctx, event.SessionID, event.ExecutionID, "interrupted", 0); err != nil {
			slog.Warn("remove discarded desktop message receipt", "error", err)
		}
	case core.EventToolDetails:
		if err := store.UpsertExecution(ctx, event.SessionID, event.ExecutionID, nil, "running"); err != nil {
			slog.Warn("save desktop execution activity", "error", err)
		}
		tool := toolActivityView(event)
		if err := store.UpsertTool(ctx, event.SessionID, event.ExecutionID, tool); err != nil {
			slog.Warn("save desktop tool activity", "error", err)
		}
	}
	return false
}

func (s *DesktopService) suppressExecutionActivityLocked(executionIDs []string) {
	if len(executionIDs) == 0 {
		return
	}
	if s.suppressedActivity == nil {
		s.suppressedActivity = make(map[string]struct{}, len(executionIDs))
	}
	for _, executionID := range executionIDs {
		if executionID != "" {
			s.suppressedActivity[executionID] = struct{}{}
		}
	}
}

func activityStatus(status string) string {
	switch strings.ToLower(status) {
	case "completed":
		return "completed"
	case "failed":
		return "failed"
	case "cancelled", "canceled":
		return "cancelled"
	case "discarded":
		return "interrupted"
	case "paused":
		return "paused"
	default:
		return "running"
	}
}

func toolActivityView(event core.Event) ToolActivityView {
	tool := event.Tool
	status := "done"
	if tool.Started {
		status = "running"
	} else if tool.Failed {
		status = "failed"
	} else if tool.Canceled {
		status = "cancelled"
	}
	view := ToolActivityView{
		ID: tool.ID, InvocationID: tool.InvocationID, AgentID: tool.AgentID, Name: tool.Name,
		Arguments: append(json.RawMessage(nil), tool.Arguments...), Status: status, DurationMs: tool.Duration.Milliseconds(),
	}
	if tool.Details != nil {
		details := &ToolExecutionDetailsView{Output: tool.Details.Output}
		if command := tool.Details.Command; command != nil {
			details.Command = &ActivityCommandOutput{
				Text: command.Command.Command, Output: command.Output, Status: string(command.Command.Status),
				ExitCode: command.Command.ExitCode, Truncated: command.Truncated, Error: command.Error,
			}
		}
		for _, change := range tool.Details.Changes {
			converted := ActivityFileChange{Path: change.Path, Added: change.Added, Removed: change.Removed, Truncated: change.Truncated}
			for _, line := range change.Lines {
				converted.Lines = append(converted.Lines, ActivityDiffLine{Kind: line.Kind, OldLine: line.OldLine, NewLine: line.NewLine, Text: line.Text})
			}
			details.Changes = append(details.Changes, converted)
		}
		view.Details = details
	}
	if payload, err := boundedToolActivityPayload(view); err == nil {
		_ = json.Unmarshal(payload, &view)
	}
	return view
}

func pendingRequestChanged(kind core.EventKind) bool {
	switch kind {
	case core.EventApprovalRequired,
		core.EventApprovalCancelled,
		core.EventUserQuestionRequired,
		core.EventUserInteractionRequired,
		core.EventUserInteractionCancelled,
		core.EventModelRecoveryRequired,
		core.EventModelRecoveryResumed,
		core.EventToolRecoveryRequired:
		return true
	default:
		return false
	}
}

func latestEventCursor(runtime *core.SessionRuntime) int64 {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events, err := runtime.SubscribeEvents(ctx, 0)
	if err != nil {
		return 0
	}
	var cursor int64
	idle := time.NewTimer(30 * time.Millisecond)
	defer idle.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return cursor
			}
			if event.Cursor > cursor {
				cursor = event.Cursor
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(30 * time.Millisecond)
			}
		case <-idle.C:
			return cursor
		case <-ctx.Done():
			return cursor
		}
	}
}

func toAgentEvent(event core.Event) AgentEvent {
	tool := toolActivityView(event)
	result := AgentEvent{
		Kind: string(event.Kind), SessionID: event.SessionID, TaskID: event.TaskID, ExecutionID: event.ExecutionID,
		Status: string(event.Status), Err: event.Err, ApprovalID: event.ApprovalID,
		Tool: ToolEvent{ID: event.Tool.ID, InvocationID: event.Tool.InvocationID, AgentID: event.Tool.AgentID,
			Name: event.Tool.Name, Arguments: tool.Arguments, Details: tool.Details,
			Started: event.Tool.Started, Failed: event.Tool.Failed, Canceled: event.Tool.Canceled,
			Duration: event.Tool.Duration.Milliseconds(), Truncated: tool.Truncated},
	}
	if event.Stream != nil {
		result.Stream = &StreamEvent{Type: event.Stream.Type, Delta: event.Stream.Delta}
	}
	return result
}

func (s *DesktopService) watchTurnLocked(runtime *core.SessionRuntime, sessionID, executionID string) {
	if executionID == "" || s.waiting[executionID] {
		return
	}
	s.waiting[executionID] = true
	go func() {
		defer func() { s.mu.Lock(); delete(s.waiting, executionID); s.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
		defer cancel()
		result, err := runtime.WaitTurn(ctx, executionID)
		if err != nil {
			if !s.isExecutionActivitySuppressed(executionID) {
				s.emit("turn-result", TurnResultView{SessionID: sessionID, ExecutionID: executionID, Error: err.Error()})
			}
			return
		}
		plan := ""
		if result.ProposedPlan != nil {
			plan = result.ProposedPlan.Text
		}
		text := responseText(result.Response)
		if plan != "" {
			text = stripProposedPlan(text)
		}
		if !s.isExecutionActivitySuppressed(executionID) {
			s.emit("turn-result", TurnResultView{SessionID: sessionID, ExecutionID: executionID, Status: string(result.Status), Text: text, Plan: plan})
		}
	}()
}

func (s *DesktopService) isExecutionActivitySuppressed(executionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, suppressed := s.suppressedActivity[executionID]
	return suppressed
}

type TurnResultView struct {
	SessionID   string `json:"sessionId"`
	ExecutionID string `json:"executionId"`
	Status      string `json:"status"`
	Text        string `json:"text"`
	Plan        string `json:"plan,omitempty"`
	Error       string `json:"error,omitempty"`
}

func responseText(raw json.RawMessage) string {
	var response publicmodel.ModelResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return ""
	}
	var parts []string
	for _, item := range response.Output {
		message, ok := item.(publicmodel.OutputMessage)
		if !ok {
			continue
		}
		for _, part := range message.Content {
			if output, ok := part.(publicmodel.OutputText); ok {
				parts = append(parts, output.Text)
			}
		}
	}
	return strings.Join(parts, "")
}

func stripProposedPlan(text string) string {
	const openTag, closeTag = "<proposed_plan>", "</proposed_plan>"
	start := strings.Index(text, openTag)
	if start < 0 {
		return text
	}
	end := strings.Index(text[start+len(openTag):], closeTag)
	if end < 0 {
		return text
	}
	end += start + len(openTag) + len(closeTag)
	return strings.TrimSpace(text[:start] + text[end:])
}

func transcriptMessages(raw json.RawMessage) []ConversationMessage {
	var input publicmodel.ResponseInput
	if len(raw) == 0 || json.Unmarshal(raw, &input) != nil {
		return nil
	}
	if input.Text != nil {
		if *input.Text == "" {
			return nil
		}
		return []ConversationMessage{{ID: "history-0", Role: "user", Text: *input.Text}}
	}
	var result []ConversationMessage
	for index, item := range input.Items {
		message, ok := item.(publicmodel.InputMessage)
		if !ok || (message.Role != publicmodel.RoleUser && message.Role != publicmodel.RoleAssistant) {
			continue
		}
		view := ConversationMessage{ID: message.ID, Role: string(message.Role)}
		if view.ID == "" {
			view.ID = fmt.Sprintf("history-%d", index)
		}
		if message.Content.Text != nil {
			view.Text = *message.Content.Text
		}
		for _, part := range message.Content.Parts {
			switch value := part.(type) {
			case publicmodel.InputText:
				view.Text += value.Text
			case publicmodel.OutputText:
				view.Text += value.Text
			case publicmodel.InputImage:
				if strings.HasPrefix(value.ImageURL, "data:image/") {
					view.Images = append(view.Images, value.ImageURL)
				}
			}
		}
		if view.Role == string(publicmodel.RoleAssistant) {
			view.Text = stripProposedPlan(view.Text)
		}
		if strings.TrimSpace(view.Text) != "" || len(view.Images) > 0 {
			result = append(result, view)
		}
	}
	return result
}

func (s *DesktopService) emit(name string, data ...any) {
	s.mu.Lock()
	app := s.app
	s.mu.Unlock()
	if app != nil {
		app.Event.Emit(name, data...)
	}
}
