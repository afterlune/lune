package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
	publictool "github.com/afterlune/luneCore/tool"
)

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

type SessionUsageView struct {
	Requests             int     `json:"requests"`
	Measured             int     `json:"measured"`
	Input                int64   `json:"input"`
	EstimatedInput       int64   `json:"estimatedInput,omitempty"`
	Output               int64   `json:"output"`
	Reasoning            int64   `json:"reasoning"`
	Total                int64   `json:"total"`
	Cached               int64   `json:"cached"`
	CacheCreation        int64   `json:"cacheCreation,omitempty"`
	ContextWindow        int64   `json:"contextWindow,omitempty"`
	ReserveOutputTokens  int64   `json:"reserveOutputTokens,omitempty"`
	AutoCompact          bool    `json:"autoCompact"`
	AutoCompactThreshold float64 `json:"autoCompactThreshold,omitempty"`
	LastInputTokens      int64   `json:"lastInputTokens,omitempty"`
	LastEstimatedInput   int64   `json:"lastEstimatedInput,omitempty"`
	LastOutputTokens     int64   `json:"lastOutputTokens,omitempty"`
	LastTotalTokens      int64   `json:"lastTotalTokens,omitempty"`
}

type WorkspaceState struct {
	Workspace        string                  `json:"workspace"`
	Session          SessionView             `json:"session"`
	Sessions         []SessionSummary        `json:"sessions"`
	Controls         SessionControlsView     `json:"controls"`
	Models           *ModelSettingsView      `json:"models,omitempty"`
	State            core.RuntimeState       `json:"runtimeState"`
	Usage            SessionUsageView        `json:"usage"`
	Pending          []PendingRequestView    `json:"pending"`
	ExecutionHistory []ExecutionActivityView `json:"executionHistory"`
}

type ModelFailureView struct {
	Category       string `json:"category,omitempty"`
	StatusCode     int    `json:"statusCode,omitempty"`
	Code           string `json:"code,omitempty"`
	RequestID      string `json:"requestId,omitempty"`
	Attempts       int    `json:"attempts,omitempty"`
	StreamStarted  bool   `json:"streamStarted,omitempty"`
	RetryNotBefore string `json:"retryNotBefore,omitempty"`
}

type PendingRequestView struct {
	Kind             string                         `json:"kind"`
	ID               string                         `json:"id"`
	ExecutionID      string                         `json:"executionId"`
	RunID            string                         `json:"runId"`
	InvocationID     string                         `json:"invocationId"`
	CallID           string                         `json:"callId"`
	Tool             string                         `json:"tool"`
	Arguments        json.RawMessage                `json:"arguments,omitempty"`
	PathGrant        *publictool.PathGrant          `json:"pathGrant,omitempty"`
	AutoReviewReason string                         `json:"autoReviewReason,omitempty"`
	Fingerprint      string                         `json:"fingerprint,omitempty"`
	Questions        []publicmodel.UserQuestionItem `json:"questions,omitempty"`
	Reason           string                         `json:"reason,omitempty"`
	ModelFailure     *ModelFailureView              `json:"modelFailure,omitempty"`
}

func (s *DesktopService) OpenSession(sessionID string) (WorkspaceState, error) {
	s.workspaceMu.Lock()
	defer s.workspaceMu.Unlock()
	return s.openSession(sessionID, false)
}

func (s *DesktopService) openSession(sessionID string, allowReconfigure bool) (WorkspaceState, error) {
	s.mu.Lock()
	if s.reconfiguring && !allowReconfigure {
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
	sessionContext := s.sessionContexts[sessionID]
	workspace, store := s.workspace, s.activityStore
	s.mu.Unlock()
	if sessionContext == nil {
		if _, err := localCore.GetSession(context.Background(), sessionID); err != nil {
			return WorkspaceState{}, fmt.Errorf("读取会话: %w", err)
		}
		sessionContext = &desktopSessionContext{
			permissionMode: core.PermissionWorkspace, core: localCore, sessionID: sessionID, workspace: workspace,
		}
		s.mu.Lock()
		if s.sessionContexts == nil {
			s.sessionContexts = make(map[string]*desktopSessionContext)
		}
		s.sessionContexts[sessionID] = sessionContext
		s.mu.Unlock()
	}
	s.mu.Lock()
	previous := s.activeContext
	s.setActiveSessionContextLocked(sessionContext)
	models := s.projectModelsView()
	s.mu.Unlock()
	sessionContext.opMu.Lock()
	defer sessionContext.opMu.Unlock()
	if sessionContext.runtime == nil {
		if err := s.attachSessionRuntime(sessionContext); err != nil {
			s.mu.Lock()
			if s.activeContext == sessionContext {
				s.activeContext = previous
			}
			s.mu.Unlock()
			return WorkspaceState{}, err
		}
	}

	ctx := context.Background()
	saved, err := sessionContext.core.GetSession(ctx, sessionID)
	if err != nil {
		return WorkspaceState{}, err
	}
	runtime := sessionContext.runtime
	state, err := runtime.State(ctx)
	if err != nil {
		return WorkspaceState{}, err
	}
	pending, err := runtime.PendingRequests(ctx)
	if err != nil {
		return WorkspaceState{}, err
	}
	list, err := s.workspaceResult()
	if err != nil {
		return WorkspaceState{}, err
	}
	s.flushSessionReasoning(sessionID)
	activityHistory, err := loadExecutionHistory(store, sessionID)
	if err != nil {
		return WorkspaceState{}, err
	}
	view := SessionView{ID: saved.ID, Title: saved.Title, UpdatedAt: saved.UpdatedAt.Format(time.RFC3339), Settings: saved.Settings, Messages: transcriptMessages(saved.History)}
	s.mu.Lock()
	controls := sessionControls(sessionContext)
	s.watchTurnLocked(runtime, sessionID, state.Task.ExecutionID)
	s.mu.Unlock()
	if previous != nil && previous != sessionContext {
		s.releaseInactiveSessionRuntime(previous.sessionID)
	}
	s.rememberSession(sessionID)
	return WorkspaceState{
		Workspace: sessionContext.workspace,
		Session:   view, Sessions: list.Sessions,
		Controls: controls, Models: models, State: state,
		Usage:   buildSessionUsageView(sessionContext.core, saved),
		Pending: pendingRequestViews(pending), ExecutionHistory: activityHistory,
	}, nil
}

func (s *DesktopService) CreateSession() (WorkspaceState, error) {
	s.workspaceMu.Lock()
	defer s.workspaceMu.Unlock()
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
	workspace := s.workspace
	s.mu.Unlock()
	session, err := localCore.CreateSession(context.Background(), nil)
	if err != nil {
		return WorkspaceState{}, err
	}
	sessionContext := &desktopSessionContext{
		core: localCore, sessionID: session.ID, workspace: workspace,
	}
	if err := s.attachSessionRuntime(sessionContext); err != nil {
		return WorkspaceState{}, err
	}
	return s.openSession(session.ID, false)
}

func (s *DesktopService) RefreshSession(sessionID string) (WorkspaceState, error) {
	s.workspaceMu.RLock()
	defer s.workspaceMu.RUnlock()
	s.mu.Lock()
	sessionContext := s.sessionContexts[sessionID]
	store := s.activityStore
	models := s.projectModelsView()
	s.mu.Unlock()
	if sessionContext == nil {
		return WorkspaceState{}, errors.New("没有打开会话")
	}
	sessionContext.opMu.Lock()
	defer func() {
		sessionContext.opMu.Unlock()
		s.releaseInactiveSessionRuntime(sessionID)
	}()
	if sessionContext.runtime == nil {
		if err := s.attachSessionRuntime(sessionContext); err != nil {
			return WorkspaceState{}, err
		}
	}
	ctx := context.Background()
	saved, err := sessionContext.core.GetSession(ctx, sessionContext.sessionID)
	if err != nil {
		return WorkspaceState{}, err
	}
	state, err := sessionContext.runtime.State(ctx)
	if err != nil {
		return WorkspaceState{}, err
	}
	requests, err := sessionContext.runtime.PendingRequests(ctx)
	if err != nil {
		return WorkspaceState{}, err
	}
	list, err := s.workspaceResult()
	if err != nil {
		return WorkspaceState{}, err
	}
	s.flushSessionReasoning(sessionContext.sessionID)
	activityHistory, err := loadExecutionHistory(store, sessionContext.sessionID)
	if err != nil {
		return WorkspaceState{}, err
	}
	s.mu.Lock()
	controls := sessionControls(sessionContext)
	s.mu.Unlock()
	return WorkspaceState{
		Workspace: sessionContext.workspace,
		Session:   SessionView{ID: saved.ID, Title: saved.Title, UpdatedAt: saved.UpdatedAt.Format(time.RFC3339), Settings: saved.Settings, Messages: transcriptMessages(saved.History)},
		Sessions:  list.Sessions, Controls: controls, Models: models, State: state,
		Usage:   buildSessionUsageView(sessionContext.core, saved),
		Pending: pendingRequestViews(requests), ExecutionHistory: activityHistory,
	}, nil
}

func buildSessionUsageView(coreClient *core.Core, saved core.Session) SessionUsageView {
	var defaults core.ContextDefaults
	if coreClient != nil {
		defaults = coreClient.ProjectContextDefaults()
	}
	contextWindow := defaults.ContextWindow
	if saved.Settings.ContextWindow != nil && *saved.Settings.ContextWindow > 0 {
		contextWindow = *saved.Settings.ContextWindow
	}
	reserveOutput := defaults.ReserveOutputTokens
	if saved.Settings.ReserveOutputTokens != nil {
		reserveOutput = *saved.Settings.ReserveOutputTokens
	}
	autoCompact := defaults.AutoCompact
	if saved.Settings.AutoCompact != nil {
		autoCompact = *saved.Settings.AutoCompact
	}
	autoCompactThreshold := defaults.AutoCompactThreshold
	if saved.Settings.AutoCompactThreshold != nil && *saved.Settings.AutoCompactThreshold > 0 {
		autoCompactThreshold = *saved.Settings.AutoCompactThreshold
	}
	var lastInput, lastEstimated, lastOutput, lastTotal int64
	for i := len(saved.UsageRequests) - 1; i >= 0; i-- {
		rec := saved.UsageRequests[i]
		purpose := string(rec.Purpose)
		if purpose != "" && purpose != "agent" {
			continue
		}
		if rec.Usage.InputTokens > 0 || rec.EstimatedInputTokens > 0 || rec.Usage.TotalTokens > 0 {
			lastInput = rec.Usage.InputTokens
			lastEstimated = rec.EstimatedInputTokens
			lastOutput = rec.Usage.OutputTokens
			lastTotal = rec.Usage.TotalTokens
			if contextWindow <= 0 && rec.ContextWindow > 0 {
				contextWindow = rec.ContextWindow
			}
			break
		}
	}
	return SessionUsageView{
		Requests:             saved.SessionUsage.Requests,
		Measured:             saved.SessionUsage.Measured,
		Input:                saved.SessionUsage.Input,
		EstimatedInput:       saved.SessionUsage.EstimatedInput,
		Output:               saved.SessionUsage.Output,
		Reasoning:            saved.SessionUsage.Reasoning,
		Total:                saved.SessionUsage.Total,
		Cached:               saved.SessionUsage.Cached,
		CacheCreation:        saved.SessionUsage.CacheCreation,
		ContextWindow:        contextWindow,
		ReserveOutputTokens:  reserveOutput,
		AutoCompact:          autoCompact,
		AutoCompactThreshold: autoCompactThreshold,
		LastInputTokens:      lastInput,
		LastEstimatedInput:   lastEstimated,
		LastOutputTokens:     lastOutput,
		LastTotalTokens:      lastTotal,
	}
}

func loadExecutionHistory(store *ActivityStore, sessionID string) ([]ExecutionActivityView, error) {
	if store == nil {
		return []ExecutionActivityView{}, nil
	}
	return store.ListSession(context.Background(), sessionID)
}
