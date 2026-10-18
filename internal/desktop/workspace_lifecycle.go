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

type SessionSearchResult struct {
	SessionID        string `json:"sessionId"`
	SessionTitle     string `json:"sessionTitle"`
	WorkingDirectory string `json:"workingDirectory"`
	UpdatedAt        string `json:"updatedAt"`
	MatchKind        string `json:"matchKind"`
	MessageID        string `json:"messageId,omitempty"`
	MessageIndex     int    `json:"messageIndex"`
	Role             string `json:"role,omitempty"`
	SnippetBefore    string `json:"snippetBefore"`
	SnippetMatch     string `json:"snippetMatch"`
	SnippetAfter     string `json:"snippetAfter"`
	TitleMatched     bool   `json:"titleMatched,omitempty"`
}

type sessionSearchCandidate struct {
	result    SessionSearchResult
	updatedAt time.Time
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
	modelConfig, err := s.store.LoadModelConfig()
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
	projectConfig.ModelConfig = modelConfig
	if err := projectConfig.Validate(); err != nil {
		return fmt.Errorf("Desktop 项目配置无效: %w", err)
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
	}
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
	localSessions, err := localCore.ListSessions(context.Background(), 1000, true)
	if err != nil {
		return fmt.Errorf("读取 Desktop 本地会话: %w", err)
	}
	for _, saved := range localSessions {
		if err := s.attachSessionRuntime(&desktopSessionContext{
			core: localCore, sessionID: saved.ID, workspace: workspace,
		}); err != nil {
			return err
		}
	}
	return nil
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
	models := modelSettingsView(s.projectConfig.ModelConfig)
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

const (
	sessionSearchPageSize      = 100
	sessionSearchMaxResults    = 100
	sessionSearchMaxPerSession = 3
)

func (s *DesktopService) SearchWorkspaceConversation(query string) ([]SessionSearchResult, error) {
	s.workspaceMu.RLock()
	defer s.workspaceMu.RUnlock()
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		return []SessionSearchResult{}, nil
	}
	if len([]rune(query)) > 256 {
		return nil, errors.New("搜索内容不能超过 256 个字符")
	}

	s.mu.Lock()
	localCore, workspace := s.localCore, s.workspace
	if localCore == nil {
		localCore = s.activeCoreLocked()
	}
	s.mu.Unlock()
	if localCore == nil || workspace == "" {
		return nil, errors.New("请先打开工作区")
	}

	candidates := make([]sessionSearchCandidate, 0)
	ctx := context.Background()
	for offset := 0; ; offset += sessionSearchPageSize {
		page, err := localCore.ListSessionPage(ctx, offset, sessionSearchPageSize, false)
		if err != nil {
			return nil, fmt.Errorf("读取本地会话历史: %w", err)
		}
		for _, saved := range page {
			candidates = append(candidates, searchSession(saved, workspace, query)...)
		}
		if len(page) < sessionSearchPageSize {
			break
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if !left.updatedAt.Equal(right.updatedAt) {
			return left.updatedAt.After(right.updatedAt)
		}
		if left.result.SessionID == right.result.SessionID && left.result.MessageIndex != right.result.MessageIndex {
			return left.result.MessageIndex > right.result.MessageIndex
		}
		if left.result.SessionID != right.result.SessionID {
			return left.result.SessionID < right.result.SessionID
		}
		return left.result.MatchKind < right.result.MatchKind
	})
	if len(candidates) > sessionSearchMaxResults {
		candidates = candidates[:sessionSearchMaxResults]
	}
	results := make([]SessionSearchResult, 0, len(candidates))
	for _, candidate := range candidates {
		results = append(results, candidate.result)
	}
	return results, nil
}

func searchSession(saved core.Session, workingDirectory, query string) []sessionSearchCandidate {
	title := strings.TrimSpace(saved.Title)
	titleBefore, titleMatch, titleAfter, titleMatched := searchSnippet(title, query)
	messages := transcriptMessages(saved.History)
	results := make([]sessionSearchCandidate, 0, sessionSearchMaxPerSession)
	for index := len(messages) - 1; index >= 0 && len(results) < sessionSearchMaxPerSession; index-- {
		message := messages[index]
		before, match, after, ok := searchSnippet(message.Text, query)
		if !ok {
			continue
		}
		results = append(results, sessionSearchCandidate{
			result: SessionSearchResult{
				SessionID: saved.ID, SessionTitle: title, WorkingDirectory: workingDirectory,
				UpdatedAt: saved.UpdatedAt.Format(time.RFC3339), MatchKind: "message", MessageID: message.ID,
				MessageIndex: index, Role: message.Role, SnippetBefore: before, SnippetMatch: match, SnippetAfter: after,
				TitleMatched: titleMatched,
			},
			updatedAt: saved.UpdatedAt,
		})
	}
	if len(results) == 0 && titleMatched {
		results = append(results, sessionSearchCandidate{
			result: SessionSearchResult{
				SessionID: saved.ID, SessionTitle: title, WorkingDirectory: workingDirectory,
				UpdatedAt: saved.UpdatedAt.Format(time.RFC3339), MatchKind: "title", MessageIndex: -1,
				SnippetBefore: titleBefore, SnippetMatch: titleMatch, SnippetAfter: titleAfter,
			},
			updatedAt: saved.UpdatedAt,
		})
	}
	return results
}

func searchSnippet(text, query string) (before, match, after string, ok bool) {
	text = strings.Join(strings.Fields(text), " ")
	textRunes := []rune(text)
	lowerText := []rune(strings.ToLower(text))
	lowerQuery := []rune(strings.ToLower(query))
	if len(lowerQuery) == 0 || len(lowerQuery) > len(lowerText) {
		return "", "", "", false
	}
	start := -1
	for index := 0; index+len(lowerQuery) <= len(lowerText); index++ {
		matched := true
		for queryIndex := range lowerQuery {
			if lowerText[index+queryIndex] != lowerQuery[queryIndex] {
				matched = false
				break
			}
		}
		if matched {
			start = index
			break
		}
	}
	if start < 0 {
		return "", "", "", false
	}
	end := start + len(lowerQuery)
	const snippetRunes = 42
	beforeStart := max(0, start-snippetRunes)
	afterEnd := min(len(textRunes), end+snippetRunes)
	before = string(textRunes[beforeStart:start])
	match = string(textRunes[start:end])
	after = string(textRunes[end:afterEnd])
	if beforeStart > 0 {
		before = "…" + before
	}
	if afterEnd < len(textRunes) {
		after += "…"
	}
	return before, match, after, true
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
		if sessionContext == nil || sessionContext.runtime == nil {
			continue
		}
		state, requests, err := workspaceSessionActivity(ctx, sessionContext)
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
			if sessionContext == nil || sessionContext.runtime == nil {
				continue
			}
			state, requests, err := workspaceSessionActivity(ctx, sessionContext)
			if err != nil {
				return err
			}
			if !workspaceActivityBusy(state, requests) {
				continue
			}
			busyIDs = append(busyIDs, fmt.Sprintf("%s:%s(queue=%d requests=%d)", sessionContext.sessionID, state.Task.Status, len(state.Queue), len(requests)))
			if err := cancelWorkspaceSession(ctx, sessionContext, state, requests); err != nil {
				return err
			}
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
