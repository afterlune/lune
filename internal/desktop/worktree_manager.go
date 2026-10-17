package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	core "github.com/afterlune/luneCore"
)

type DesktopWorktreeSessionView struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type DesktopWorktreeView struct {
	Path                string                       `json:"path"`
	GitRegistered       bool                         `json:"gitRegistered"`
	Available           bool                         `json:"available"`
	Orphaned            bool                         `json:"orphaned"`
	GitStatus           string                       `json:"gitStatus"`
	ChangedFiles        int                          `json:"changedFiles"`
	Sessions            []DesktopWorktreeSessionView `json:"sessions"`
	RequiresLocalSwitch bool                         `json:"requiresLocalSwitch"`
	CanRemove           bool                         `json:"canRemove"`
	BlockReason         string                       `json:"blockReason,omitempty"`
}

type gitWorktreeEntry struct {
	path     string
	prunable bool
}

func (s *DesktopService) ListDesktopWorktrees() ([]DesktopWorktreeView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listDesktopWorktreesLocked()
}

func (s *DesktopService) listDesktopWorktreesLocked() ([]DesktopWorktreeView, error) {
	if strings.TrimSpace(s.workspace) == "" {
		return nil, errors.New("请先打开工作区")
	}
	gitInfo, err := inspectGitWorkspace(s.workspace)
	if err != nil {
		return nil, err
	}
	canonicalRepositoryRoot, err := canonicalWorkspacePath(gitInfo.Root)
	if err != nil {
		return nil, fmt.Errorf("解析 Git 仓库根目录: %w", err)
	}
	managedRoot, err := desktopWorktreeManagedRoot(s.workspace)
	if err != nil {
		return nil, err
	}
	canonicalRoot, err := canonicalWorkspacePath(managedRoot)
	if err != nil {
		return nil, fmt.Errorf("解析 Desktop Worktree 目录: %w", err)
	}
	gitEntries, err := listGitWorktrees(gitInfo.Root)
	if err != nil {
		return nil, err
	}
	records := []DesktopSessionRecord{}
	if s.activityStore != nil {
		records, err = s.activityStore.DesktopSessions(context.Background())
		if err != nil {
			return nil, err
		}
	}
	candidates := map[string]*desktopWorktreeCandidate{}
	getCandidate := func(path string) (*desktopWorktreeCandidate, bool) {
		canonical, canonicalErr := canonicalWorkspacePath(path)
		if canonicalErr != nil || !desktopWorktreeChild(canonicalRoot, canonical) {
			return nil, false
		}
		candidate := candidates[canonical]
		if candidate == nil {
			candidate = &desktopWorktreeCandidate{key: canonical, path: filepath.Clean(path)}
			candidates[canonical] = candidate
		}
		return candidate, true
	}
	for _, entry := range gitEntries {
		candidate, inside := getCandidate(entry.path)
		if !inside {
			continue
		}
		candidate.gitEntry = &entry
		candidate.path = filepath.Clean(entry.path)
	}
	for _, record := range records {
		repositoryRoot, rootErr := canonicalWorkspacePath(record.RepositoryRoot)
		if rootErr != nil || repositoryRoot != canonicalRepositoryRoot {
			continue
		}
		candidate, inside := getCandidate(record.WorktreePath)
		if !inside {
			continue
		}
		candidate.records = append(candidate.records, record)
		if candidate.gitEntry == nil {
			candidate.path = filepath.Clean(record.WorktreePath)
		}
	}
	if entries, readErr := os.ReadDir(managedRoot); readErr == nil {
		for _, entry := range entries {
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			candidate, inside := getCandidate(filepath.Join(managedRoot, entry.Name()))
			if inside && candidate.gitEntry == nil && len(candidate.records) == 0 {
				candidate.path = filepath.Join(managedRoot, entry.Name())
			}
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return nil, fmt.Errorf("读取 Desktop Worktree 目录: %w", readErr)
	}

	views := make([]DesktopWorktreeView, 0, len(candidates))
	for _, candidate := range candidates {
		view := s.desktopWorktreeViewLocked(candidate)
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool {
		return strings.ToLower(views[i].Path) < strings.ToLower(views[j].Path)
	})
	return views, nil
}

type desktopWorktreeCandidate struct {
	key      string
	path     string
	gitEntry *gitWorktreeEntry
	records  []DesktopSessionRecord
}

func (s *DesktopService) desktopWorktreeViewLocked(candidate *desktopWorktreeCandidate) DesktopWorktreeView {
	view := DesktopWorktreeView{
		Path:          filepath.Clean(candidate.path),
		GitRegistered: candidate.gitEntry != nil,
		Orphaned:      candidate.gitEntry != nil && len(candidate.records) == 0,
		GitStatus:     "unavailable",
		Sessions:      make([]DesktopWorktreeSessionView, 0, len(candidate.records)),
	}
	blockReasons := make([]string, 0, 5)
	groupIDs := make(map[string]struct{}, len(candidate.records))
	for _, record := range candidate.records {
		groupIDs[record.ID] = struct{}{}
		sessionView := DesktopWorktreeSessionView{ID: record.ID, Title: record.Title, Status: record.Status}
		sessionContext := s.sessionContexts[record.ID]
		if sessionContext == nil || sessionContext.runtime == nil || sessionContext.core == nil {
			blockReasons = append(blockReasons, "至少一个关联会话未能加载")
			sessionView.Status = record.Status
		} else {
			sessionView.Status = sessionContextStatus(sessionContext)
			if sessionContext.mode != workspaceModeWorktree || !samePath(sessionContext.worktreePath, candidate.path) || !samePath(sessionContext.repositoryRoot, record.RepositoryRoot) || !samePath(sessionContext.stateDir, record.StateDir) {
				blockReasons = append(blockReasons, "关联会话的 Worktree 或 Core 路径信息不一致")
			}
			if reason := removableSessionBlockReason(sessionContext); reason != "" {
				blockReasons = append(blockReasons, reason)
			}
			if s.sessionID == record.ID {
				view.RequiresLocalSwitch = true
			}
		}
		view.Sessions = append(view.Sessions, sessionView)
	}
	if reason := unknownCoreSessionReason(candidate.records, groupIDs, s.sessionContexts); reason != "" {
		blockReasons = append(blockReasons, reason)
	}
	if candidate.gitEntry == nil {
		blockReasons = append(blockReasons, "Git 中未登记此缓存目录，请先手动检查")
	} else if candidate.gitEntry.prunable {
		blockReasons = append(blockReasons, "Git 标记此 Worktree 不可用；请手动检查后处理")
	} else {
		info, err := os.Lstat(candidate.path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			blockReasons = append(blockReasons, "Worktree 目录不存在或不可用；不会自动修剪 Git 登记")
		} else if status, statusErr := runGitOutput(candidate.path, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching"); statusErr != nil {
			blockReasons = append(blockReasons, fmt.Sprintf("读取 Git 状态失败: %v", statusErr))
		} else {
			view.Available = true
			view.ChangedFiles = len(parseGitStatusZ(status))
			if view.ChangedFiles == 0 {
				view.GitStatus = "clean"
			} else {
				view.GitStatus = "dirty"
				blockReasons = append(blockReasons, fmt.Sprintf("包含 %d 项暂存、未暂存、未跟踪或忽略文件", view.ChangedFiles))
			}
		}
	}
	if len(blockReasons) > 0 {
		view.BlockReason = strings.Join(uniqueStrings(blockReasons), "；")
		return view
	}
	view.CanRemove = true
	return view
}

func removableSessionBlockReason(sessionContext *desktopSessionContext) string {
	state, err := sessionContext.runtime.State(context.Background())
	if err != nil {
		return "无法读取关联会话状态"
	}
	if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued || state.Task.Status == core.TaskPaused {
		return "至少一个关联会话仍在运行、排队或等待处理"
	}
	requests, err := sessionContext.runtime.PendingRequests(context.Background())
	if err != nil {
		return "无法读取关联会话的待处理请求"
	}
	if len(requests) > 0 {
		return "至少一个关联会话仍有待处理请求"
	}
	return ""
}

func unknownCoreSessionReason(records []DesktopSessionRecord, groupIDs map[string]struct{}, contexts map[string]*desktopSessionContext) string {
	checked := make(map[*core.Core]struct{})
	for _, record := range records {
		sessionContext := contexts[record.ID]
		if sessionContext == nil || sessionContext.core == nil {
			continue
		}
		if _, exists := checked[sessionContext.core]; exists {
			continue
		}
		checked[sessionContext.core] = struct{}{}
		sessions, err := sessionContext.core.ListSessions(context.Background(), 10000, true)
		if err != nil {
			return "无法确认 Core 中的关联会话"
		}
		for _, saved := range sessions {
			if _, exists := groupIDs[saved.ID]; !exists {
				return "Core 中还有未关联到此 Worktree 的会话"
			}
		}
	}
	return ""
}

func (s *DesktopService) RemoveDesktopWorktree(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("Worktree 路径不能为空")
	}
	views, err := s.listDesktopWorktreesLocked()
	if err != nil {
		return err
	}
	canonicalTarget, err := canonicalWorkspacePath(path)
	if err != nil {
		return fmt.Errorf("解析 Worktree 路径: %w", err)
	}
	var selected *DesktopWorktreeView
	for index := range views {
		canonicalView, canonicalErr := canonicalWorkspacePath(views[index].Path)
		if canonicalErr == nil && canonicalView == canonicalTarget {
			selected = &views[index]
			break
		}
	}
	if selected == nil {
		return errors.New("此路径不是当前项目登记的 Desktop Worktree")
	}
	if !selected.GitRegistered {
		return errors.New("Git 未登记此目录，Desktop 不会自动删除它")
	}
	if !selected.CanRemove {
		return fmt.Errorf("无法清理此 Worktree：%s", selected.BlockReason)
	}
	gitInfo, err := inspectGitWorkspace(s.workspace)
	if err != nil {
		return err
	}
	registered, err := listGitWorktrees(gitInfo.Root)
	if err != nil {
		return err
	}
	linked := false
	for _, entry := range registered {
		canonicalEntry, canonicalErr := canonicalWorkspacePath(entry.path)
		if canonicalErr == nil && canonicalEntry == canonicalTarget && !entry.prunable {
			linked = true
			break
		}
	}
	if !linked {
		return errors.New("Git 不再登记此 Worktree；请刷新列表并手动检查")
	}
	info, err := os.Lstat(selected.Path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Worktree 目录已不可用；请手动检查 Git 登记")
	}
	status, err := runGitOutput(selected.Path, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching")
	if err != nil {
		return fmt.Errorf("再次检查 Worktree Git 状态: %w", err)
	}
	if entries := len(parseGitStatusZ(status)); entries != 0 {
		return fmt.Errorf("Worktree 已有 %d 项暂存、未暂存、未跟踪或忽略文件；文件已保留", entries)
	}

	records := []DesktopSessionRecord{}
	if s.activityStore != nil {
		allRecords, listErr := s.activityStore.DesktopSessions(context.Background())
		if listErr != nil {
			return listErr
		}
		for _, record := range allRecords {
			canonicalRecordPath, canonicalErr := canonicalWorkspacePath(record.WorktreePath)
			if canonicalErr == nil && canonicalRecordPath == canonicalTarget {
				records = append(records, record)
			}
		}
	}
	if len(records) > 0 {
		return s.removeWorktreeSessionGroupLocked(selected.Path, gitInfo.Root, records)
	}
	return removeGitWorktree(gitInfo.Root, selected.Path)
}

func (s *DesktopService) removeWorktreeSessionGroupLocked(worktreePath, repositoryRoot string, records []DesktopSessionRecord) error {
	groupIDs := make(map[string]struct{}, len(records))
	contexts := make([]*desktopSessionContext, 0, len(records))
	cores := make(map[*core.Core]struct{})
	stateDirs := make(map[string]struct{})
	sessionIDs := make([]string, 0, len(records))
	for _, record := range records {
		groupIDs[record.ID] = struct{}{}
		sessionContext := s.sessionContexts[record.ID]
		if record.Mode != workspaceModeWorktree || sessionContext == nil || sessionContext.runtime == nil || sessionContext.core == nil || sessionContext.mode != workspaceModeWorktree {
			return errors.New("至少一个关联会话未能加载；工作树和会话记录均已保留")
		}
		if sessionContext.worktreePath == "" || !samePath(sessionContext.worktreePath, worktreePath) || !samePath(sessionContext.repositoryRoot, repositoryRoot) || !samePath(sessionContext.stateDir, record.StateDir) {
			return errors.New("会话记录与 Worktree 路径不一致；拒绝清理")
		}
		if reason := removableSessionBlockReason(sessionContext); reason != "" {
			return errors.New(reason)
		}
		if s.sessionID == record.ID {
			return errors.New("当前会话位于此 Worktree，请先切换到 Local 会话")
		}
		if _, err := sessionContext.core.GetSession(context.Background(), record.ID); err != nil {
			return fmt.Errorf("读取关联 Core 会话 %s: %w", record.ID, err)
		}
		contexts = append(contexts, sessionContext)
		cores[sessionContext.core] = struct{}{}
		stateDirs[sessionContext.stateDir] = struct{}{}
		sessionIDs = append(sessionIDs, record.ID)
	}
	if reason := unknownCoreSessionReason(records, groupIDs, s.sessionContexts); reason != "" {
		return errors.New(reason)
	}
	for _, sessionContext := range contexts {
		if err := sessionContext.core.DeleteSession(context.Background(), sessionContext.sessionID); err != nil {
			return fmt.Errorf("删除 Worktree 会话 %s: %w", sessionContext.sessionID, err)
		}
	}
	for _, sessionContext := range contexts {
		if sessionContext.eventCancel != nil {
			sessionContext.eventCancel()
		}
	}
	for agent := range cores {
		if err := agent.Close(); err != nil {
			return fmt.Errorf("关闭 Worktree Core: %w", err)
		}
	}
	stateDirsList := make([]string, 0, len(stateDirs))
	for stateDir := range stateDirs {
		stateDirsList = append(stateDirsList, stateDir)
	}
	sort.Strings(stateDirsList)
	for _, stateDir := range stateDirsList {
		if err := removeDesktopWorktreeStateDir(s.stateRoot, stateDir); err != nil {
			return err
		}
	}
	if s.activityStore != nil {
		if err := s.activityStore.DeleteDesktopSessions(context.Background(), sessionIDs); err != nil {
			return err
		}
	}
	for _, sessionContext := range contexts {
		delete(s.sessionContexts, sessionContext.sessionID)
		delete(s.waiting, sessionContext.sessionID)
	}
	if err := removeGitWorktree(repositoryRoot, worktreePath); err != nil {
		return err
	}
	return nil
}

func desktopWorktreeManagedRoot(workspace string) (string, error) {
	sentinel, err := desktopWorktreeDir(workspace, "sentinel")
	if err != nil {
		return "", err
	}
	return filepath.Dir(sentinel), nil
}

func desktopWorktreeChild(root, path string) bool {
	return path != root && filepath.Dir(path) == root && workspaceContains(root, path)
}

func listGitWorktrees(repository string) ([]gitWorktreeEntry, error) {
	output, err := runGitOutput(repository, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, fmt.Errorf("读取 Git Worktree 列表: %w", err)
	}
	entries := make([]gitWorktreeEntry, 0)
	current := gitWorktreeEntry{}
	flush := func() {
		if current.path != "" {
			entries = append(entries, current)
			current = gitWorktreeEntry{}
		}
	}
	for _, field := range strings.Split(string(output), "\x00") {
		if field == "" {
			flush()
			continue
		}
		if strings.HasPrefix(field, "worktree ") {
			if current.path != "" {
				flush()
			}
			current.path = strings.TrimPrefix(field, "worktree ")
		} else if strings.HasPrefix(field, "prunable ") {
			current.prunable = true
		}
	}
	flush()
	return entries, nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
