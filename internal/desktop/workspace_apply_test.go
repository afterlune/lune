package desktop

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

func TestApplySelectedWorktreeChangesSupportsGitChangeTypesAndPreservesIndex(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required for Worktree apply tests")
	}
	repository := t.TempDir()
	initApplyTestRepository(t, gitPath, repository, map[string][]byte{
		"modified.txt":     []byte("before\n"),
		"deleted.txt":      []byte("delete me\n"),
		"rename-old.txt":   []byte("rename me\n"),
		"binary.bin":       {0, 1, 2, 3},
		"unrelated.txt":    []byte("unchanged\n"),
		"staged-local.txt": []byte("stage base\n"),
	})
	worktree := filepath.Join(t.TempDir(), "agent-worktree")
	_, workingDirectory, err := createGitWorktree(repository, worktree)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command(gitPath, "-C", repository, "worktree", "remove", "--force", worktree).Run()
	})
	writeApplyTestFile(t, workingDirectory, "modified.txt", []byte("after\n"))
	if err := os.Remove(filepath.Join(workingDirectory, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(workingDirectory, "rename-old.txt"), filepath.Join(workingDirectory, "rename-new.txt")); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, gitPath, workingDirectory, "add", "-A", "--", "rename-old.txt", "rename-new.txt")
	worktreeIndexBefore, err := runGitOutput(workingDirectory, "diff", "--cached", "--name-only")
	if err != nil {
		t.Fatal(err)
	}
	writeApplyTestFile(t, workingDirectory, "binary.bin", []byte{0, 8, 0, 9})
	writeApplyTestFile(t, workingDirectory, "new file.txt", []byte("new content\n"))

	writeApplyTestFile(t, repository, "unrelated.txt", []byte("keep this local change\n"))
	writeApplyTestFile(t, repository, "staged-local.txt", []byte("staged local change\n"))
	runGitForTest(t, gitPath, repository, "add", "--", "staged-local.txt")
	indexBefore, err := runGitOutput(repository, "diff", "--cached", "--name-only")
	if err != nil {
		t.Fatal(err)
	}
	result, err := applySelectedWorktreeChanges(workingDirectory, repository, []string{
		"modified.txt", "deleted.txt", "rename-new.txt", "binary.bin", "new file.txt",
	})
	if err != nil {
		t.Fatalf("apply selected Worktree changes: %v", err)
	}
	if len(result.Files) != 5 {
		t.Fatalf("applied files = %#v, want five selected files", result.Files)
	}
	worktreeIndexAfter, err := runGitOutput(workingDirectory, "diff", "--cached", "--name-only")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(worktreeIndexBefore, worktreeIndexAfter) {
		t.Fatalf("applying changes modified the Worktree index: before=%q after=%q", worktreeIndexBefore, worktreeIndexAfter)
	}
	assertApplyTestFile(t, repository, "modified.txt", []byte("after\n"))
	assertApplyTestFile(t, repository, "new file.txt", []byte("new content\n"))
	assertApplyTestFile(t, repository, "binary.bin", []byte{0, 8, 0, 9})
	assertApplyTestFile(t, repository, "unrelated.txt", []byte("keep this local change\n"))
	if _, err := os.Stat(filepath.Join(repository, "deleted.txt")); !os.IsNotExist(err) {
		t.Fatalf("deleted file still exists or returned another error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repository, "rename-old.txt")); !os.IsNotExist(err) {
		t.Fatalf("renamed source still exists or returned another error: %v", err)
	}
	assertApplyTestFile(t, repository, "rename-new.txt", []byte("rename me\n"))
	indexAfter, err := runGitOutput(repository, "diff", "--cached", "--name-only")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(indexBefore, indexAfter) {
		t.Fatalf("applying changes modified the target index: before=%q after=%q", indexBefore, indexAfter)
	}
	status, err := runGitOutput(repository, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(status), "?? new file.txt\x00") {
		t.Fatalf("new file was not left unstaged: %q", status)
	}
	repeated, err := applySelectedWorktreeChanges(workingDirectory, repository, []string{
		"modified.txt", "deleted.txt", "rename-new.txt", "binary.bin", "new file.txt",
	})
	if err != nil || !repeated.AlreadyPresent {
		t.Fatalf("reapplying the same changes = %#v, error=%v; want an already-present no-op", repeated, err)
	}
}

func TestApplySelectedWorktreeChangesStopsWholeBatchOnConflict(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required for Worktree apply tests")
	}
	repository := t.TempDir()
	initApplyTestRepository(t, gitPath, repository, map[string][]byte{
		"conflict.txt": []byte("base\n"),
		"clean.txt":    []byte("clean base\n"),
	})
	worktree := filepath.Join(t.TempDir(), "agent-worktree")
	_, workingDirectory, err := createGitWorktree(repository, worktree)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command(gitPath, "-C", repository, "worktree", "remove", "--force", worktree).Run()
	})
	writeApplyTestFile(t, workingDirectory, "conflict.txt", []byte("agent result\n"))
	writeApplyTestFile(t, workingDirectory, "clean.txt", []byte("clean result\n"))
	writeApplyTestFile(t, repository, "conflict.txt", []byte("local result\n"))
	indexBefore, err := runGitOutput(repository, "diff", "--cached", "--name-only")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := applySelectedWorktreeChanges(workingDirectory, repository, []string{"conflict.txt", "clean.txt"}); err == nil || !strings.Contains(err.Error(), "未应用任何文件") {
		t.Fatalf("conflicting apply error = %v, want a no-change conflict error", err)
	}
	assertApplyTestFile(t, repository, "conflict.txt", []byte("local result\n"))
	assertApplyTestFile(t, repository, "clean.txt", []byte("clean base\n"))
	indexAfter, err := runGitOutput(repository, "diff", "--cached", "--name-only")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(indexBefore, indexAfter) {
		t.Fatalf("failed batch modified the target index: before=%q after=%q", indexBefore, indexAfter)
	}
}

func TestApplySelectedWorktreeChangesRejectsStaleAndEscapingPaths(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required for Worktree apply tests")
	}
	repository := t.TempDir()
	initApplyTestRepository(t, gitPath, repository, map[string][]byte{"file.txt": []byte("base\n")})
	worktree := filepath.Join(t.TempDir(), "agent-worktree")
	_, workingDirectory, err := createGitWorktree(repository, worktree)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command(gitPath, "-C", repository, "worktree", "remove", "--force", worktree).Run()
	})
	writeApplyTestFile(t, workingDirectory, "file.txt", []byte("worktree change\n"))
	for _, path := range []string{"missing.txt", "../outside.txt"} {
		if _, err := applySelectedWorktreeChanges(workingDirectory, repository, []string{path}); err == nil {
			t.Errorf("ApplySelectedWorktreeChanges accepted stale/escaping path %q", path)
		}
	}
}

func TestDesktopApplyWorktreeChangesChecksPermissionAndWritesToOriginalNestedProject(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required for Worktree apply tests")
	}
	repository := t.TempDir()
	initApplyTestRepository(t, gitPath, repository, map[string][]byte{
		"app/file.txt": []byte("base\n"),
		"root.txt":     []byte("outside nested workspace\n"),
	})
	nestedWorkspace := filepath.Join(repository, "app")
	worktree := filepath.Join(t.TempDir(), "agent-worktree")
	gitInfo, workingDirectory, err := createGitWorktree(nestedWorkspace, worktree)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = exec.Command(gitPath, "-C", repository, "worktree", "remove", "--force", worktree).Run()
	})
	writeApplyTestFile(t, workingDirectory, "file.txt", []byte("worktree result\n"))

	projectConfig := core.DefaultProjectConfig()
	modelConfig := &publicmodel.Config{
		Providers: map[string]publicmodel.ProviderConfig{"test": {
			Protocol: publicmodel.ProtocolResponses, BaseURL: "https://example.invalid", APIKey: "test-key",
		}},
		Models: map[string]publicmodel.ModelProfile{"primary": {Provider: "test", Model: "test-model"}}, DefaultModel: "primary",
	}
	agent, err := core.New(core.Config{
		Workspace: workingDirectory, StateDir: filepath.Join(t.TempDir(), "state"),
		ProjectConfig: &projectConfig, ModelConfig: modelConfig, Memory: core.MemoryConfig{Disabled: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := agent.Close(); err != nil {
			t.Errorf("close Worktree Core: %v", err)
		}
	})
	saved, err := agent.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := agent.OpenSessionRuntime(context.Background(), saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	context := &desktopSessionContext{
		core: agent, runtime: runtime, sessionID: saved.ID, workspace: workingDirectory,
		mode: workspaceModeWorktree, worktreePath: worktree, repositoryRoot: gitInfo.Root,
	}
	service := &DesktopService{
		workspace: nestedWorkspace, sessionContexts: map[string]*desktopSessionContext{saved.ID: context},
	}
	if err := agent.SetPermissionMode(core.PermissionReadOnly); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApplyWorktreeChanges(saved.ID, []string{"file.txt"}); err == nil || !strings.Contains(err.Error(), "只读权限") {
		t.Fatalf("read-only apply error = %v, want permission rejection", err)
	}
	assertApplyTestFile(t, nestedWorkspace, "file.txt", []byte("base\n"))

	if err := agent.SetPermissionMode(core.PermissionWorkspace); err != nil {
		t.Fatal(err)
	}
	service.workspace = t.TempDir()
	if _, err := service.ApplyWorktreeChanges(saved.ID, []string{"file.txt"}); err == nil || !strings.Contains(err.Error(), "原项目不一致") {
		t.Fatalf("mismatched project apply error = %v, want project identity rejection", err)
	}
	service.workspace = nestedWorkspace
	result, err := service.ApplyWorktreeChanges(saved.ID, []string{"file.txt"})
	if err != nil {
		t.Fatalf("apply Worktree result through Desktop service: %v", err)
	}
	if len(result.Files) != 1 || result.Files[0] != "file.txt" || !samePath(result.Destination, nestedWorkspace) {
		t.Fatalf("Desktop apply result = %#v", result)
	}
	assertApplyTestFile(t, nestedWorkspace, "file.txt", []byte("worktree result\n"))
	assertApplyTestFile(t, repository, "root.txt", []byte("outside nested workspace\n"))
}

func TestValidateWorktreeApplyPermission(t *testing.T) {
	tests := []struct {
		mode core.PermissionMode
		want bool
	}{
		{mode: core.PermissionReadOnly, want: false},
		{mode: core.PermissionWorkspace, want: true},
		{mode: core.PermissionAsk, want: true},
		{mode: core.PermissionFullAccess, want: true},
		{mode: "unknown", want: false},
	}
	for _, test := range tests {
		if err := validateWorktreeApplyPermission(test.mode); (err == nil) != test.want {
			t.Errorf("validateWorktreeApplyPermission(%q) error = %v, wantAllowed=%t", test.mode, err, test.want)
		}
	}
}

func TestValidateWorktreeApplyTaskState(t *testing.T) {
	active := []core.TaskStatus{core.TaskRunning, core.TaskQueued, core.TaskPaused}
	for _, status := range active {
		if err := validateWorktreeApplyTaskState(status, 0, 0); err == nil {
			t.Errorf("validateWorktreeApplyTaskState(%q) should block active task", status)
		}
	}
	for _, status := range []core.TaskStatus{core.TaskIdle, core.TaskCompleted, core.TaskFailed, core.TaskCancelled, core.TaskDiscarded} {
		if err := validateWorktreeApplyTaskState(status, 0, 0); err != nil {
			t.Errorf("validateWorktreeApplyTaskState(%q) unexpectedly blocked: %v", status, err)
		}
	}
	if err := validateWorktreeApplyTaskState(core.TaskCompleted, 1, 0); err == nil {
		t.Fatal("queued follow-up task was allowed")
	}
	if err := validateWorktreeApplyTaskState(core.TaskCompleted, 0, 1); err == nil {
		t.Fatal("pending user request was allowed")
	}
}

func initApplyTestRepository(t *testing.T, gitPath, repository string, files map[string][]byte) {
	t.Helper()
	runGitForTest(t, gitPath, repository, "init", "--quiet")
	runGitForTest(t, gitPath, repository, "config", "user.email", "test@example.invalid")
	runGitForTest(t, gitPath, repository, "config", "user.name", "Test")
	runGitForTest(t, gitPath, repository, "config", "core.autocrlf", "false")
	for name, contents := range files {
		writeApplyTestFile(t, repository, name, contents)
	}
	runGitForTest(t, gitPath, repository, "add", ".")
	runGitForTest(t, gitPath, repository, "commit", "--quiet", "-m", "initial")
}

func writeApplyTestFile(t *testing.T, root, relativePath string, contents []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
}

func assertApplyTestFile(t *testing.T, root, relativePath string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relativePath)))
	if err != nil {
		t.Fatalf("read %s: %v", relativePath, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s = %q, want %q", relativePath, got, want)
	}
}
