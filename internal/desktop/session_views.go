package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
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

type WorkspaceState struct {
	Workspace        string                  `json:"workspace"`
	ProjectRoot      string                  `json:"projectRoot"`
	WorkingDirectory string                  `json:"workingDirectory"`
	Session          SessionView             `json:"session"`
	Sessions         []SessionSummary        `json:"sessions"`
	Controls         SessionControlsView     `json:"controls"`
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
	s.workspaceMu.RLock()
	defer s.workspaceMu.RUnlock()
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
	projectRoot, store := s.workspace, s.activityStore
	s.mu.Unlock()
	if sessionContext == nil {
		if _, err := localCore.GetSession(context.Background(), sessionID); err != nil {
			return WorkspaceState{}, fmt.Errorf("读取会话: %w", err)
		}
		sessionContext = &desktopSessionContext{
			core: localCore, sessionID: sessionID, workspace: projectRoot,
		}
		if err := s.attachSessionRuntime(sessionContext); err != nil {
			return WorkspaceState{}, err
		}
	}
	s.mu.Lock()
	s.setActiveSessionContextLocked(sessionContext)
	projectRoot = s.workspace
	models := s.projectModelsView()
	s.mu.Unlock()

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
	activityHistory, err := loadExecutionHistory(store, sessionID)
	if err != nil {
		return WorkspaceState{}, err
	}
	view := SessionView{ID: saved.ID, Title: saved.Title, UpdatedAt: saved.UpdatedAt.Format(time.RFC3339), Settings: saved.Settings, Messages: transcriptMessages(saved.History)}
	s.mu.Lock()
	controls := sessionControls(sessionContext)
	s.watchTurnLocked(runtime, sessionID, state.Task.ExecutionID)
	s.mu.Unlock()
	return WorkspaceState{
		Workspace: sessionContext.workspace, ProjectRoot: projectRoot,
		WorkingDirectory: sessionContext.workspace, Session: view, Sessions: list.Sessions,
		Controls: controls, Models: models, State: state, Pending: pendingRequestViews(pending),
		ExecutionHistory: activityHistory,
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
	projectRoot := s.workspace
	s.mu.Unlock()
	session, err := localCore.CreateSession(context.Background(), nil)
	if err != nil {
		return WorkspaceState{}, err
	}
	sessionContext := &desktopSessionContext{
		core: localCore, sessionID: session.ID, workspace: projectRoot,
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
	projectRoot := s.workspace
	store := s.activityStore
	models := s.projectModelsView()
	s.mu.Unlock()
	if sessionContext == nil {
		return WorkspaceState{}, errors.New("没有打开会话")
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
	activityHistory, err := loadExecutionHistory(store, sessionContext.sessionID)
	if err != nil {
		return WorkspaceState{}, err
	}
	s.mu.Lock()
	controls := sessionControls(sessionContext)
	s.mu.Unlock()
	return WorkspaceState{
		Workspace: sessionContext.workspace, ProjectRoot: projectRoot,
		WorkingDirectory: sessionContext.workspace,
		Session:          SessionView{ID: saved.ID, Title: saved.Title, UpdatedAt: saved.UpdatedAt.Format(time.RFC3339), Settings: saved.Settings, Messages: transcriptMessages(saved.History)},
		Sessions:         list.Sessions, Controls: controls, Models: models, State: state,
		Pending: pendingRequestViews(requests), ExecutionHistory: activityHistory,
	}, nil
}

func loadExecutionHistory(store *ActivityStore, sessionID string) ([]ExecutionActivityView, error) {
	if store == nil {
		return []ExecutionActivityView{}, nil
	}
	return store.ListSession(context.Background(), sessionID)
}
