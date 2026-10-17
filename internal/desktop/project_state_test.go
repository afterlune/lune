package desktop

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/settings"
	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

func TestDesktopStateIsSeparateFromWorkspaceState(t *testing.T) {
	configRoot := filepath.Join(t.TempDir(), "roaming")
	cacheRoot := filepath.Join(t.TempDir(), "local")
	t.Setenv("APPDATA", configRoot)
	t.Setenv("LOCALAPPDATA", cacheRoot)
	workspace := t.TempDir()

	stateRoot, err := desktopProjectStateDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	stateDir, err := filepath.Abs(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	legacyDir, err := filepath.Abs(filepath.Join(workspace, ".luneagent"))
	if err != nil {
		t.Fatal(err)
	}
	if samePath(stateDir, legacyDir) || strings.HasPrefix(strings.ToLower(stateDir), strings.ToLower(legacyDir+string(filepath.Separator))) {
		t.Fatalf("Desktop state directory %q is inside TUI state directory %q", stateDir, legacyDir)
	}
	stateAgain, err := desktopProjectStateDir(workspace)
	if err != nil || stateAgain != stateRoot {
		t.Fatalf("Desktop state path was not stable: first=%q second=%q err=%v", stateRoot, stateAgain, err)
	}
	firstWorktree, err := desktopWorktreeDir(workspace, "session-one")
	if err != nil {
		t.Fatal(err)
	}
	secondWorktree, err := desktopWorktreeDir(workspace, "session-two")
	if err != nil {
		t.Fatal(err)
	}
	if firstWorktree == secondWorktree || !strings.HasPrefix(strings.ToLower(firstWorktree), strings.ToLower(cacheRoot)) {
		t.Fatalf("worktree paths are not unique and user-scoped: first=%q second=%q", firstWorktree, secondWorktree)
	}
}

func TestRemoveDesktopWorktreeStateDirRejectsPathsOutsideManagedRoot(t *testing.T) {
	stateRoot := t.TempDir()
	external := t.TempDir()
	keep := filepath.Join(external, "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeDesktopWorktreeStateDir(stateRoot, external); err == nil {
		t.Fatal("removed a directory outside the managed Worktree state root")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("external file was removed: %v", err)
	}
}

func TestDesktopCanOpenWorkspaceWhileCoreOwnsLegacyStateDir(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Desktop settings require Windows DPAPI")
	}
	configRoot := filepath.Join(t.TempDir(), "roaming")
	cacheRoot := filepath.Join(t.TempDir(), "local")
	t.Setenv("APPDATA", configRoot)
	t.Setenv("LOCALAPPDATA", cacheRoot)
	workspace := t.TempDir()
	projectConfig := `{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"models":{"primary":{"provider":"host","model":"test-model"}},"defaultModel":"primary"}`
	if err := os.WriteFile(filepath.Join(workspace, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	settingsStore, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	saveDesktopTestModels(t, settingsStore)
	legacyCore, err := core.New(core.Config{Workspace: workspace})
	if err != nil {
		_ = settingsStore.Close()
		t.Fatal(err)
	}
	defer legacyCore.Close()
	configPath := filepath.Join(workspace, "config.json")
	ignoredConfig := []byte(`{"not":"a Desktop config"}`)
	if err := os.WriteFile(configPath, ignoredConfig, 0600); err != nil {
		t.Fatal(err)
	}
	service := &DesktopService{store: settingsStore, waiting: map[string]bool{}, sessionContexts: map[string]*desktopSessionContext{}}
	t.Cleanup(func() {
		if err := service.CloseWorkspace(true); err != nil {
			t.Errorf("close Desktop workspace: %v", err)
		}
		if err := settingsStore.Close(); err != nil {
			t.Errorf("close settings store: %v", err)
		}
	})

	opened, err := service.OpenWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(opened.Workspace, workspace) {
		t.Fatalf("opened workspace = %q, want %q", opened.Workspace, workspace)
	}
	if len(service.projectConfig.MCPServers) != 0 || service.projectConfig.ModelConfig.DefaultModel != "main" {
		t.Fatalf("Desktop loaded project config.json instead of SQLite settings: %#v", service.projectConfig)
	}
	unchanged, err := os.ReadFile(configPath)
	if err != nil || string(unchanged) != string(ignoredConfig) {
		t.Fatalf("Desktop changed or read project config.json: contents=%q error=%v", unchanged, err)
	}
	if samePath(filepath.Join(service.stateRoot, "local"), filepath.Join(workspace, ".luneagent")) {
		t.Fatalf("Desktop Core reused TUI state directory %q", service.stateRoot)
	}
	if _, err := legacyCore.ListSessions(context.Background(), 10, false); err != nil {
		t.Fatalf("TUI/Core became unusable after Desktop opened: %v", err)
	}
}

func TestCreateGitWorktreeUsesHeadAndMapsNestedWorkspace(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required for the Worktree integration test")
	}
	repository := t.TempDir()
	runGitForTest(t, gitPath, repository, "init", "--quiet")
	runGitForTest(t, gitPath, repository, "config", "user.email", "test@example.invalid")
	runGitForTest(t, gitPath, repository, "config", "user.name", "Test")
	runGitForTest(t, gitPath, repository, "config", "core.autocrlf", "false")
	if err := os.MkdirAll(filepath.Join(repository, "apps", "desktop"), 0700); err != nil {
		t.Fatal(err)
	}
	trackedFile := filepath.Join(repository, "apps", "desktop", "README.md")
	if err := os.WriteFile(trackedFile, []byte("committed version\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, gitPath, repository, "add", ".")
	runGitForTest(t, gitPath, repository, "commit", "--quiet", "-m", "initial")
	if err := os.WriteFile(trackedFile, []byte("uncommitted version\n"), 0600); err != nil {
		t.Fatal(err)
	}

	selectedWorkspace := filepath.Join(repository, "apps", "desktop")
	destination := filepath.Join(t.TempDir(), "worktree")
	gitInfo, workingDirectory, err := createGitWorktree(selectedWorkspace, destination)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command(gitPath, "-C", repository, "worktree", "remove", "--force", destination).Run()
	})
	if !samePath(gitInfo.Root, repository) {
		t.Fatalf("git root = %q, want %q", gitInfo.Root, repository)
	}
	if !samePath(workingDirectory, filepath.Join(destination, "apps", "desktop")) {
		t.Fatalf("mapped working directory = %q", workingDirectory)
	}
	data, err := os.ReadFile(filepath.Join(workingDirectory, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(data), "\r\n", "\n") != "committed version\n" {
		t.Fatalf("Worktree content = %q, want committed HEAD content", data)
	}
	localData, err := os.ReadFile(trackedFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(localData) != "uncommitted version\n" {
		t.Fatalf("creating Worktree changed Local content to %q", localData)
	}
}

func TestDesktopWorktreeSessionPersistsAcrossRestartAndDeleteKeepsCheckout(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Desktop settings require Windows DPAPI")
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required for the Worktree integration test")
	}
	configRoot := filepath.Join(t.TempDir(), "roaming")
	cacheRoot := filepath.Join(t.TempDir(), "local")
	t.Setenv("APPDATA", configRoot)
	t.Setenv("LOCALAPPDATA", cacheRoot)
	repository := t.TempDir()
	runGitForTest(t, gitPath, repository, "init", "--quiet")
	runGitForTest(t, gitPath, repository, "config", "user.email", "test@example.invalid")
	runGitForTest(t, gitPath, repository, "config", "user.name", "Test")
	projectConfig := `{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"models":{"primary":{"provider":"host","model":"test-model"}},"defaultModel":"primary"}`
	if err := os.WriteFile(filepath.Join(repository, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("from HEAD\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, gitPath, repository, "add", ".")
	runGitForTest(t, gitPath, repository, "commit", "--quiet", "-m", "initial")

	settingsStore, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	saveDesktopTestModels(t, settingsStore)
	service := &DesktopService{store: settingsStore, waiting: map[string]bool{}, sessionContexts: map[string]*desktopSessionContext{}}
	t.Cleanup(func() {
		if err := service.CloseWorkspace(true); err != nil {
			t.Errorf("close Desktop workspace: %v", err)
		}
		if err := settingsStore.Close(); err != nil {
			t.Errorf("close settings store: %v", err)
		}
	})
	tuiCore, err := core.New(core.Config{Workspace: repository})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tuiCore.Close() })

	if _, err := service.OpenWorkspace(repository); err != nil {
		t.Fatalf("open project using Desktop model settings: %v", err)
	}
	created, err := service.CreateSession(workspaceModeWorktree)
	if err != nil {
		t.Fatalf("create Worktree session while TUI owns legacy state: %v", err)
	}
	if created.WorkspaceMode != workspaceModeWorktree || created.WorktreePath == "" || !samePath(created.Workspace, created.WorktreePath) {
		t.Fatalf("Worktree session workspace = %#v", created)
	}
	worktreePath := created.WorktreePath
	t.Cleanup(func() {
		_ = exec.Command(gitPath, "-C", repository, "worktree", "remove", "--force", worktreePath).Run()
	})
	content, err := os.ReadFile(filepath.Join(created.WorkingDirectory, "README.md"))
	if err != nil || strings.ReplaceAll(string(content), "\r\n", "\n") != "from HEAD\n" {
		t.Fatalf("new Worktree file content = %q, error=%v", content, err)
	}
	worktreeSessionID := created.Session.ID
	local, err := service.CreateSession(workspaceModeLocal)
	if err != nil {
		t.Fatal(err)
	}
	if local.WorkspaceMode != workspaceModeLocal || !samePath(local.WorkingDirectory, repository) {
		t.Fatalf("Local session workspace = %#v", local)
	}
	if err := service.CloseWorkspace(false); err != nil {
		t.Fatalf("close Desktop workspace: %v", err)
	}
	if _, err := service.OpenWorkspace(repository); err != nil {
		t.Fatalf("restore Desktop workspace: %v", err)
	}
	restored, err := service.workspaceResult()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, session := range restored.Sessions {
		if session.ID == worktreeSessionID && session.Mode == workspaceModeWorktree && samePath(session.WorktreePath, worktreePath) {
			found = true
		}
	}
	if !found {
		t.Fatalf("restored session list does not contain Worktree session: %#v", restored.Sessions)
	}
	if _, err := service.OpenSession(worktreeSessionID); err != nil {
		t.Fatalf("open restored Worktree session: %v", err)
	}
	if _, err := service.OpenSession(local.Session.ID); err != nil {
		t.Fatalf("switch to Local session: %v", err)
	}
	if err := service.DeleteSession(worktreeSessionID); err != nil {
		t.Fatalf("delete Worktree session: %v", err)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("deleting session removed the user-managed Worktree: %v", err)
	}
	if err := service.CloseWorkspace(false); err != nil {
		t.Fatalf("close Desktop workspace before Worktree cleanup: %v", err)
	}
	runGitForTest(t, gitPath, repository, "worktree", "remove", "--force", worktreePath)
}

func saveDesktopTestModels(t *testing.T, store *settings.Store) {
	t.Helper()
	config := publicmodel.Config{
		Providers: map[string]publicmodel.ProviderConfig{"host": {
			Protocol: "responses", BaseURL: "https://example.invalid", APIKey: "test-key",
		}},
		Models:       map[string]publicmodel.ModelProfile{"main": {Provider: "host", Model: "test-model"}},
		DefaultModel: "main",
	}
	if err := store.SaveModelConfig(config); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreWorktreeSessionsSharesCoreStateAndDeletesOneSessionSafely(t *testing.T) {
	stateRoot := t.TempDir()
	localWorkspace := t.TempDir()
	worktreeWorkspace := t.TempDir()
	config := &publicmodel.Config{
		Providers: map[string]publicmodel.ProviderConfig{"host": {
			Protocol: publicmodel.ProtocolResponses, BaseURL: "https://example.invalid", APIKey: "test-key",
		}},
		Models:       map[string]publicmodel.ModelProfile{"primary": {Provider: "host", Model: "test-model"}},
		DefaultModel: "primary",
	}
	worktreeStateDir := filepath.Join(stateRoot, "worktree-state", "shared-worktree")
	worktreeAgent, err := core.New(core.Config{
		Workspace: worktreeWorkspace, StateDir: worktreeStateDir, ModelConfig: config,
		Memory: core.MemoryConfig{Disabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := worktreeAgent.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := worktreeAgent.CreateSession(context.Background(), nil)
	if err != nil {
		_ = worktreeAgent.Close()
		t.Fatal(err)
	}
	if err := worktreeAgent.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := OpenActivityStore(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	localCore, err := core.New(core.Config{
		Workspace: localWorkspace, StateDir: filepath.Join(stateRoot, "local"), ModelConfig: config,
		Memory: core.MemoryConfig{Disabled: true},
	})
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
	for _, session := range []core.Session{first, second} {
		if err := store.SaveDesktopSession(context.Background(), DesktopSessionRecord{
			ID: session.ID, Mode: workspaceModeWorktree, Workspace: worktreeWorkspace,
			StateDir: worktreeStateDir, WorktreePath: worktreeWorkspace, RepositoryRoot: worktreeWorkspace,
			Title: session.Title, UpdatedAt: session.UpdatedAt, Status: "idle",
		}); err != nil {
			_ = localCore.Close()
			_ = store.Close()
			t.Fatal(err)
		}
	}
	service := &DesktopService{
		core: localCore, localCore: localCore, workspace: localWorkspace, activeWorkspace: localWorkspace,
		stateRoot: stateRoot, projectConfig: core.ProjectConfig{ModelConfig: *config}, activityStore: store, waiting: map[string]bool{},
		sessionContexts: map[string]*desktopSessionContext{},
	}
	t.Cleanup(func() {
		if err := service.CloseWorkspace(true); err != nil {
			t.Errorf("close Desktop service: %v", err)
		}
	})

	if err := service.restoreSessionContexts(); err != nil {
		t.Fatal(err)
	}
	firstContext, secondContext := service.sessionContexts[first.ID], service.sessionContexts[second.ID]
	if firstContext == nil || secondContext == nil || firstContext.core != secondContext.core {
		t.Fatalf("Worktree sessions did not share the restored Core: first=%p second=%p", firstContext, secondContext)
	}
	if _, err := service.OpenSession(localSession.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteSession(first.ID); err != nil {
		t.Fatalf("delete one sibling Worktree session: %v", err)
	}
	if _, err := os.Stat(worktreeStateDir); err != nil {
		t.Fatalf("deleting one sibling removed shared Core state: %v", err)
	}
	if _, err := service.OpenSession(second.ID); err != nil {
		t.Fatalf("open remaining sibling Worktree session: %v", err)
	}
}

func runGitForTest(t *testing.T, gitPath, directory string, args ...string) {
	t.Helper()
	command := exec.Command(gitPath, append([]string{"-C", directory}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
}
