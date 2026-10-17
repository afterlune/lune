package desktop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

func TestDesktopWorktreeManagerListsOnlyManagedPathsAndRemovesCleanOrphans(t *testing.T) {
	gitPath := requireWorktreeManagerGit(t)
	repository := initWorktreeManagerRepository(t, gitPath)
	setDesktopWorktreeCacheForTest(t)
	managedPath, err := desktopWorktreeDir(repository, "managed")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := createGitWorktree(repository, managedPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command(gitPath, "-C", repository, "worktree", "remove", "--force", managedPath).Run()
	})
	externalPath := filepath.Join(t.TempDir(), "external-worktree")
	if _, _, err := createGitWorktree(repository, externalPath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command(gitPath, "-C", repository, "worktree", "remove", "--force", externalPath).Run()
	})
	managedRoot := filepath.Dir(managedPath)
	manualPath := filepath.Join(managedRoot, "manual-folder")
	if err := os.MkdirAll(manualPath, 0700); err != nil {
		t.Fatal(err)
	}

	service := &DesktopService{workspace: repository, sessionContexts: map[string]*desktopSessionContext{}}
	views, err := service.ListDesktopWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 {
		t.Fatalf("managed Worktrees = %#v; want the linked path and unregistered Desktop-cache folder", views)
	}
	var managedView, manualView *DesktopWorktreeView
	for index := range views {
		switch {
		case samePath(views[index].Path, managedPath):
			managedView = &views[index]
		case samePath(views[index].Path, manualPath):
			manualView = &views[index]
		}
		if samePath(views[index].Path, externalPath) {
			t.Fatal("external Git Worktree appeared in the current project's Desktop manager")
		}
	}
	if managedView == nil || !managedView.Orphaned || !managedView.CanRemove || managedView.GitStatus != "clean" {
		t.Fatalf("managed orphan view = %#v", managedView)
	}
	if manualView == nil || manualView.GitRegistered || manualView.CanRemove {
		t.Fatalf("unregistered cache directory view = %#v", manualView)
	}
	if err := service.RemoveDesktopWorktree(externalPath); err == nil {
		t.Fatal("removed an external Git Worktree")
	}
	if _, err := os.Stat(externalPath); err != nil {
		t.Fatalf("external Worktree was changed: %v", err)
	}
	if err := service.RemoveDesktopWorktree(manualPath); err == nil {
		t.Fatal("removed a directory that Git does not register")
	}
	if err := service.RemoveDesktopWorktree(managedPath); err != nil {
		t.Fatalf("remove clean orphan: %v", err)
	}
	if _, err := os.Stat(managedPath); !os.IsNotExist(err) {
		t.Fatalf("clean orphan still exists after removal: %v", err)
	}
}

func TestDesktopWorktreeManagerCountsTrackedStagedUntrackedAndIgnoredFiles(t *testing.T) {
	gitPath := requireWorktreeManagerGit(t)
	repository := initWorktreeManagerRepository(t, gitPath)
	setDesktopWorktreeCacheForTest(t)
	worktreePath, err := desktopWorktreeDir(repository, "dirty")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := createGitWorktree(repository, worktreePath); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command(gitPath, "-C", repository, "worktree", "remove", "--force", worktreePath).Run()
	})
	if err := os.WriteFile(filepath.Join(worktreePath, "README.md"), []byte("modified\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, "staged.txt"), []byte("staged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, gitPath, worktreePath, "add", "--", "staged.txt")
	if err := os.WriteFile(filepath.Join(worktreePath, "untracked.txt"), []byte("untracked\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, "ignored.txt"), []byte("ignored\n"), 0600); err != nil {
		t.Fatal(err)
	}

	service := &DesktopService{workspace: repository}
	views, err := service.ListDesktopWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 {
		t.Fatalf("Worktree views = %#v", views)
	}
	if views[0].GitStatus != "dirty" || views[0].ChangedFiles != 4 || views[0].CanRemove {
		t.Fatalf("dirty Worktree view = %#v, want four blocking Git entries", views[0])
	}
	if !strings.Contains(views[0].BlockReason, "忽略文件") {
		t.Fatalf("dirty reason does not describe ignored-file protection: %q", views[0].BlockReason)
	}
	if err := service.RemoveDesktopWorktree(worktreePath); err == nil {
		t.Fatal("removed a dirty Worktree")
	}
	for _, name := range []string{"README.md", "staged.txt", "untracked.txt", "ignored.txt"} {
		if _, err := os.Stat(filepath.Join(worktreePath, name)); err != nil {
			t.Errorf("dirty file %s was not preserved: %v", name, err)
		}
	}
}

func TestDesktopWorktreeManagerDoesNotPruneMissingGitWorktree(t *testing.T) {
	gitPath := requireWorktreeManagerGit(t)
	repository := initWorktreeManagerRepository(t, gitPath)
	setDesktopWorktreeCacheForTest(t)
	worktreePath, err := desktopWorktreeDir(repository, "missing")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := createGitWorktree(repository, worktreePath); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(worktreePath); err != nil {
		t.Fatal(err)
	}
	service := &DesktopService{workspace: repository}
	views, err := service.ListDesktopWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || views[0].Available || views[0].CanRemove || !views[0].GitRegistered {
		t.Fatalf("missing Worktree view = %#v", views)
	}
	if err := service.RemoveDesktopWorktree(worktreePath); err == nil {
		t.Fatal("automatically pruned a missing Git Worktree")
	}
	entries, err := listGitWorktrees(repository)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if samePath(entry.path, worktreePath) {
			return
		}
	}
	t.Fatal("failed removal silently pruned the missing Git Worktree")
}

func TestDesktopWorktreeManagerRemovesAllStoppedSessionsInSharedWorktree(t *testing.T) {
	env := newDesktopWorktreeManagerSessionGroup(t, 2)
	for index, sessionID := range env.worktreeSessionIDs {
		executionID := "execution-" + sessionID
		if err := env.store.UpsertExecution(context.Background(), sessionID, executionID, nil, "completed"); err != nil {
			t.Fatal(err)
		}
		activity, err := env.store.ListSession(context.Background(), sessionID)
		if err != nil || len(activity) != 1 {
			t.Fatalf("seed Worktree execution activity %d: rows=%#v error=%v", index, activity, err)
		}
	}
	views, err := env.service.ListDesktopWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || !views[0].CanRemove || views[0].Orphaned || len(views[0].Sessions) != 2 {
		t.Fatalf("shared Worktree view = %#v", views)
	}
	if err := env.service.RemoveDesktopWorktree(env.worktreePath); err != nil {
		t.Fatalf("remove stopped shared Worktree: %v", err)
	}
	if _, err := os.Stat(env.worktreePath); !os.IsNotExist(err) {
		t.Fatalf("Worktree directory remains after removal: %v", err)
	}
	if _, err := os.Stat(env.worktreeStateDir); !os.IsNotExist(err) {
		t.Fatalf("shared Core state remains after removal: %v", err)
	}
	records, err := env.store.DesktopSessions(context.Background())
	if err != nil || len(records) != 0 {
		t.Fatalf("Worktree session records = %#v, error=%v", records, err)
	}
	for _, sessionID := range env.worktreeSessionIDs {
		activity, err := env.store.ListSession(context.Background(), sessionID)
		if err != nil || len(activity) != 0 {
			t.Errorf("execution history for %s remains: rows=%#v error=%v", sessionID, activity, err)
		}
	}
	if views, err := env.service.ListDesktopWorktrees(); err != nil || len(views) != 0 {
		t.Fatalf("Worktrees after cleanup = %#v, error=%v", views, err)
	}
}

func TestDesktopWorktreeManagerRequiresSwitchingAwayFromAssociatedSession(t *testing.T) {
	env := newDesktopWorktreeManagerSessionGroup(t, 1)
	if _, err := env.service.OpenSession(env.worktreeSessionIDs[0]); err != nil {
		t.Fatal(err)
	}
	views, err := env.service.ListDesktopWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || !views[0].CanRemove || !views[0].RequiresLocalSwitch {
		t.Fatalf("active Worktree view = %#v", views)
	}
	if err := env.service.RemoveDesktopWorktree(env.worktreePath); err == nil {
		t.Fatal("removed the active Worktree")
	}
	if _, err := env.service.OpenSession(env.localSessionID); err != nil {
		t.Fatal(err)
	}
	if err := env.service.RemoveDesktopWorktree(env.worktreePath); err != nil {
		t.Fatalf("remove Worktree after switching to Local: %v", err)
	}
}

func TestDesktopWorktreeManagerListsDeletedSessionWorktreeAsOrphan(t *testing.T) {
	env := newDesktopWorktreeManagerSessionGroup(t, 1)
	if err := env.service.DeleteSession(env.worktreeSessionIDs[0]); err != nil {
		t.Fatal(err)
	}
	views, err := env.service.ListDesktopWorktrees()
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 1 || !views[0].Orphaned || !views[0].CanRemove {
		t.Fatalf("deleted-session Worktree view = %#v", views)
	}
	if err := env.service.RemoveDesktopWorktree(env.worktreePath); err != nil {
		t.Fatalf("remove orphaned Worktree: %v", err)
	}
}

type desktopWorktreeManagerTestEnv struct {
	service            *DesktopService
	store              *ActivityStore
	worktreePath       string
	worktreeStateDir   string
	localSessionID     string
	worktreeSessionIDs []string
}

func newDesktopWorktreeManagerSessionGroup(t *testing.T, sessionCount int) desktopWorktreeManagerTestEnv {
	t.Helper()
	gitPath := requireWorktreeManagerGit(t)
	repository := initWorktreeManagerRepository(t, gitPath)
	setDesktopWorktreeCacheForTest(t)
	worktreePath, err := desktopWorktreeDir(repository, "shared")
	if err != nil {
		t.Fatal(err)
	}
	gitInfo, worktreeWorkspace, err := createGitWorktree(repository, worktreePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command(gitPath, "-C", repository, "worktree", "remove", "--force", worktreePath).Run()
	})
	stateRoot := t.TempDir()
	store, err := OpenActivityStore(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	localStateDir := filepath.Join(stateRoot, "local")
	models := &publicmodel.Config{
		Providers: map[string]publicmodel.ProviderConfig{"host": {Protocol: publicmodel.ProtocolResponses, BaseURL: "https://example.invalid", APIKey: "test-key"}},
		Models:    map[string]publicmodel.ModelProfile{"primary": {Provider: "host", Model: "test-model"}}, DefaultModel: "primary",
	}
	projectConfig := core.ProjectConfig{ModelConfig: *models}
	localCore, err := core.New(core.Config{Workspace: repository, StateDir: localStateDir, ProjectConfig: &projectConfig, Memory: core.MemoryConfig{Disabled: true}})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	localSession, err := localCore.CreateSession(context.Background(), nil)
	if err != nil {
		_ = localCore.Close()
		_ = store.Close()
		t.Fatal(err)
	}
	localRuntime, err := localCore.OpenSessionRuntime(context.Background(), localSession.ID)
	if err != nil {
		_ = localCore.Close()
		_ = store.Close()
		t.Fatal(err)
	}
	worktreeStateDir := filepath.Join(stateRoot, "worktree-state", "shared")
	worktreeCore, err := core.New(core.Config{Workspace: worktreeWorkspace, StateDir: worktreeStateDir, ProjectConfig: &projectConfig, ModelConfig: models, Memory: core.MemoryConfig{Disabled: true}})
	if err != nil {
		_ = localCore.Close()
		_ = store.Close()
		t.Fatal(err)
	}
	sessionContexts := map[string]*desktopSessionContext{
		localSession.ID: {core: localCore, runtime: localRuntime, sessionID: localSession.ID, workspace: repository, mode: workspaceModeLocal, stateDir: localStateDir},
	}
	worktreeIDs := make([]string, 0, sessionCount)
	for range sessionCount {
		saved, createErr := worktreeCore.CreateSession(context.Background(), nil)
		if createErr != nil {
			_ = worktreeCore.Close()
			_ = localCore.Close()
			_ = store.Close()
			t.Fatal(createErr)
		}
		worktreeRuntime, runtimeErr := worktreeCore.OpenSessionRuntime(context.Background(), saved.ID)
		if runtimeErr != nil {
			_ = worktreeCore.Close()
			_ = localCore.Close()
			_ = store.Close()
			t.Fatal(runtimeErr)
		}
		if err := store.SaveDesktopSession(context.Background(), DesktopSessionRecord{
			ID: saved.ID, Mode: workspaceModeWorktree, Workspace: worktreeWorkspace, StateDir: worktreeStateDir,
			WorktreePath: worktreePath, RepositoryRoot: gitInfo.Root, Title: saved.Title, UpdatedAt: saved.UpdatedAt, Status: "idle",
		}); err != nil {
			_ = worktreeCore.Close()
			_ = localCore.Close()
			_ = store.Close()
			t.Fatal(err)
		}
		sessionContexts[saved.ID] = &desktopSessionContext{
			core: worktreeCore, runtime: worktreeRuntime, sessionID: saved.ID, workspace: worktreeWorkspace,
			mode: workspaceModeWorktree, stateDir: worktreeStateDir, worktreePath: worktreePath, repositoryRoot: gitInfo.Root,
		}
		worktreeIDs = append(worktreeIDs, saved.ID)
	}
	service := &DesktopService{
		core: localCore, localCore: localCore, runtime: localRuntime, workspace: repository, activeWorkspace: repository,
		sessionID: localSession.ID, activeContext: sessionContexts[localSession.ID], sessionContexts: sessionContexts,
		activityStore: store, stateRoot: stateRoot, waiting: map[string]bool{}, suppressedActivity: map[string]struct{}{},
		projectConfig: projectConfig,
	}
	t.Cleanup(func() {
		if err := service.CloseWorkspace(true); err != nil {
			t.Errorf("close Desktop test service: %v", err)
		}
	})
	return desktopWorktreeManagerTestEnv{
		service: service, store: store, worktreePath: worktreePath, worktreeStateDir: worktreeStateDir,
		localSessionID: localSession.ID, worktreeSessionIDs: worktreeIDs,
	}
}

func requireWorktreeManagerGit(t *testing.T) string {
	t.Helper()
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required for Worktree manager tests")
	}
	return gitPath
}

func initWorktreeManagerRepository(t *testing.T, gitPath string) string {
	t.Helper()
	repository := t.TempDir()
	runGitForTest(t, gitPath, repository, "init", "--quiet")
	runGitForTest(t, gitPath, repository, "config", "user.email", "test@example.invalid")
	runGitForTest(t, gitPath, repository, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".gitignore"), []byte("ignored.txt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, gitPath, repository, "add", ".")
	runGitForTest(t, gitPath, repository, "commit", "--quiet", "-m", "initial")
	return repository
}

func setDesktopWorktreeCacheForTest(t *testing.T) {
	t.Helper()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", cacheRoot)
		return
	}
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
}
