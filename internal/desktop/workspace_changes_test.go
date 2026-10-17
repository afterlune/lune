package desktop

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceChangesListsStatusAndReadsDiffs(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required for workspace changes tests")
	}
	repository := t.TempDir()
	runGitForTest(t, gitPath, repository, "init", "--quiet")
	runGitForTest(t, gitPath, repository, "config", "user.email", "test@example.invalid")
	runGitForTest(t, gitPath, repository, "config", "user.name", "Test")
	runGitForTest(t, gitPath, repository, "config", "core.autocrlf", "false")
	for name, contents := range map[string]string{
		".gitignore":     "ignored.log\n",
		"staged.txt":     "base staged\n",
		"unstaged.txt":   "base unstaged\n",
		"mixed.txt":      "first base\nsecond base\n",
		"deleted.txt":    "delete this line\n",
		"rename-old.txt": "rename this file\n",
	} {
		if err := os.WriteFile(filepath.Join(repository, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGitForTest(t, gitPath, repository, "add", ".")
	runGitForTest(t, gitPath, repository, "commit", "--quiet", "-m", "initial")

	if err := os.WriteFile(filepath.Join(repository, "staged.txt"), []byte("staged replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, gitPath, repository, "add", "--", "staged.txt")
	if err := os.WriteFile(filepath.Join(repository, "unstaged.txt"), []byte("unstaged replacement\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "mixed.txt"), []byte("first staged\nsecond base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, gitPath, repository, "add", "--", "mixed.txt")
	if err := os.WriteFile(filepath.Join(repository, "mixed.txt"), []byte("first staged\nsecond unstaged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repository, "deleted.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(repository, "rename-old.txt"), filepath.Join(repository, "rename-new.txt")); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, gitPath, repository, "add", "-A", "--", "rename-old.txt", "rename-new.txt")
	for name, data := range map[string][]byte{
		"new file.txt": []byte("first new line\nsecond new line\n"),
		"binary.bin":   {0, 1, 2, 3},
		"ignored.log":  []byte("ignored\n"),
	} {
		if err := os.WriteFile(filepath.Join(repository, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	service := &DesktopService{activeWorkspace: repository}
	view, err := service.ListWorkspaceChanges()
	if err != nil {
		t.Fatal(err)
	}
	if !view.Available || len(view.Files) != 7 {
		t.Fatalf("change list available=%v files=%#v", view.Available, view.Files)
	}
	byPath := make(map[string]WorkspaceChangeView, len(view.Files))
	for _, file := range view.Files {
		byPath[file.Path] = file
	}
	for _, name := range []string{"staged.txt", "unstaged.txt", "mixed.txt", "deleted.txt", "rename-new.txt", "new file.txt"} {
		if _, found := byPath[name]; !found {
			t.Errorf("missing changed file %q in %#v", name, byPath)
		}
	}
	if _, found := byPath["ignored.log"]; found {
		t.Fatal("ignored file appeared in Git changes")
	}
	if byPath["staged.txt"].Status[0] != 'M' || byPath["unstaged.txt"].Status[1] != 'M' {
		t.Fatalf("staged/unstaged statuses = %q / %q", byPath["staged.txt"].Status, byPath["unstaged.txt"].Status)
	}
	if byPath["new file.txt"].Added != 2 || !byPath["binary.bin"].Binary {
		t.Fatalf("untracked stats = %#v / %#v", byPath["new file.txt"], byPath["binary.bin"])
	}
	if !strings.Contains(byPath["rename-new.txt"].Status, "R") || byPath["rename-new.txt"].OriginalPath != "rename-old.txt" {
		t.Fatalf("rename entry = %#v", byPath["rename-new.txt"])
	}

	diff, err := service.ReadWorkspaceDiff("mixed.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff.Content, "-second base") || !strings.Contains(diff.Content, "+second unstaged") || !strings.Contains(diff.Content, "+first staged") {
		t.Fatalf("combined staged and unstaged diff = %q", diff.Content)
	}
	newFileDiff, err := service.ReadWorkspaceDiff("new file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(newFileDiff.Content, "+first new line") || !strings.Contains(newFileDiff.Content, "+++ b/new file.txt") {
		t.Fatalf("untracked file diff = %q", newFileDiff.Content)
	}
	binaryDiff, err := service.ReadWorkspaceDiff("binary.bin")
	if err != nil || !binaryDiff.Binary {
		t.Fatalf("binary diff = %#v, error=%v", binaryDiff, err)
	}
	if _, err := service.ReadWorkspaceDiff("../outside.txt"); err == nil {
		t.Fatal("ReadWorkspaceDiff accepted a path outside the current change list")
	}
}

func TestWorkspaceChangesAreLimitedToCurrentNestedWorkspace(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is required for workspace changes tests")
	}
	repository := t.TempDir()
	nested := filepath.Join(repository, "app")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	runGitForTest(t, gitPath, repository, "init", "--quiet")
	runGitForTest(t, gitPath, repository, "config", "user.email", "test@example.invalid")
	runGitForTest(t, gitPath, repository, "config", "user.name", "Test")
	for name, contents := range map[string]string{"root.txt": "root\n", "app/app.txt": "app\n"} {
		if err := os.WriteFile(filepath.Join(repository, filepath.FromSlash(name)), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGitForTest(t, gitPath, repository, "add", ".")
	runGitForTest(t, gitPath, repository, "commit", "--quiet", "-m", "initial")
	if err := os.WriteFile(filepath.Join(repository, "root.txt"), []byte("root changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "app.txt"), []byte("app changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	service := &DesktopService{activeWorkspace: nested}
	view, err := service.ListWorkspaceChanges()
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Files) != 2 {
		t.Fatalf("nested workspace changes = %#v, want app.txt and new.txt only", view.Files)
	}
	for _, file := range view.Files {
		if strings.Contains(file.Path, "root.txt") || strings.Contains(file.Path, "app/") {
			t.Fatalf("change path was not scoped to nested workspace: %#v", file)
		}
	}
}

func TestWorkspaceChangesReportNonGitDirectory(t *testing.T) {
	service := &DesktopService{activeWorkspace: t.TempDir()}
	view, err := service.ListWorkspaceChanges()
	if err != nil {
		t.Fatal(err)
	}
	if view.Available || view.Message == "" || len(view.Files) != 0 {
		t.Fatalf("non-Git result = %#v", view)
	}
}

func TestParseGitStatusZPreservesWhitespaceAndRenamePaths(t *testing.T) {
	entries := parseGitStatusZ([]byte(" M file with spaces.txt\x00R  new name.txt\x00old name.txt\x00?? tab\tname.txt\x00"))
	if len(entries) != 3 {
		t.Fatalf("parsed status entries = %#v", entries)
	}
	if entries[0].path != "file with spaces.txt" || entries[1].path != "new name.txt" || entries[1].originalPath != "old name.txt" || entries[2].path != "tab\tname.txt" {
		t.Fatalf("parsed paths = %#v", entries)
	}
}
