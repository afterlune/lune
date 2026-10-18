package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	WorkspaceMode    string                  `json:"workspaceMode"`
	WorkingDirectory string                  `json:"workingDirectory"`
	WorktreePath     string                  `json:"worktreePath,omitempty"`
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
	if sessionContext.mode == workspaceModeWorktree && store != nil {
		_ = store.UpdateDesktopSession(ctx, saved.ID, saved.Title, sessionContextStatus(sessionContext), saved.UpdatedAt)
	}
	s.mu.Lock()
	controls := sessionControls(sessionContext)
	s.watchTurnLocked(runtime, sessionID, state.Task.ExecutionID)
	s.mu.Unlock()
	return WorkspaceState{
		Workspace: sessionContext.workspace, ProjectRoot: projectRoot, WorkspaceMode: sessionContext.mode,
		WorkingDirectory: sessionContext.workspace, WorktreePath: sessionContext.worktreePath, Session: view, Sessions: list.Sessions,
		Controls: controls, Models: models, State: state, Pending: pendingRequestViews(pending),
		ExecutionHistory: activityHistory,
	}, nil
}

func (s *DesktopService) CreateSession(mode string) (WorkspaceState, error) {
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
		return s.openSession(session.ID, false)
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
		Workspace: sessionContext.workspace, ProjectRoot: projectRoot, WorkspaceMode: sessionContext.mode,
		WorkingDirectory: sessionContext.workspace, WorktreePath: sessionContext.worktreePath,
		Session:  SessionView{ID: saved.ID, Title: saved.Title, UpdatedAt: saved.UpdatedAt.Format(time.RFC3339), Settings: saved.Settings, Messages: transcriptMessages(saved.History)},
		Sessions: list.Sessions, Controls: controls, Models: models, State: state,
		Pending: pendingRequestViews(requests), ExecutionHistory: activityHistory,
	}, nil
}

func loadExecutionHistory(store *ActivityStore, sessionID string) ([]ExecutionActivityView, error) {
	if store == nil {
		return []ExecutionActivityView{}, nil
	}
	return store.ListSession(context.Background(), sessionID)
}
