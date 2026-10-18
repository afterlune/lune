package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceFileBrowserListsSearchesAndPreviewsWithinWorkspace(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"src", ".git", "node_modules"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "src", "Main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "ignored.go"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "ignored.js"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := &DesktopService{sessionContexts: map[string]*desktopSessionContext{"test": {workspace: root}}}

	entries, err := service.ListWorkspaceFiles("test", "")
	if err != nil {
		t.Fatalf("ListWorkspaceFiles: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "src" || !entries[0].Directory {
		t.Fatalf("workspace root entries = %#v, want only visible src directory", entries)
	}
	children, err := service.ListWorkspaceFiles("test", "src")
	if err != nil {
		t.Fatalf("ListWorkspaceFiles(src): %v", err)
	}
	if len(children) != 1 || children[0].Path != "src/Main.go" || children[0].Directory {
		t.Fatalf("src entries = %#v, want Main.go", children)
	}
	search, err := service.SearchWorkspaceFiles("test", "main")
	if err != nil {
		t.Fatalf("SearchWorkspaceFiles: %v", err)
	}
	if len(search.Files) != 1 || search.Files[0].Path != "src/Main.go" || search.Truncated {
		t.Fatalf("search result = %#v, want src/Main.go", search)
	}
	preview, err := service.ReadWorkspaceFile("test", "src/Main.go")
	if err != nil {
		t.Fatalf("ReadWorkspaceFile: %v", err)
	}
	if preview.Path != "src/Main.go" || preview.Content != "package main\n" || preview.Truncated {
		t.Fatalf("file preview = %#v", preview)
	}
	if _, err := service.ReadWorkspaceFile("test", "../outside.txt"); err == nil {
		t.Fatal("ReadWorkspaceFile accepted a path outside the workspace")
	}
	if hidden, err := service.ReadWorkspaceFile("test", ".git/ignored.go"); err != nil || hidden.Content != "ignored" {
		t.Fatalf("explicit hidden file preview = %#v, error=%v", hidden, err)
	}
}

func TestWorkspaceFilePreviewRejectsBinaryAndReportsTruncation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "binary.bin"), []byte{0, 1, 2}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(strings.Repeat("x", maxWorkspacePreviewBytes+10)), 0o600); err != nil {
		t.Fatal(err)
	}
	service := &DesktopService{sessionContexts: map[string]*desktopSessionContext{"test": {workspace: root}}}
	if _, err := service.ReadWorkspaceFile("test", "binary.bin"); err == nil {
		t.Fatal("ReadWorkspaceFile accepted a binary file")
	}
	preview, err := service.ReadWorkspaceFile("test", "large.txt")
	if err != nil {
		t.Fatalf("ReadWorkspaceFile(large): %v", err)
	}
	if len(preview.Content) != maxWorkspacePreviewBytes || !preview.Truncated {
		t.Fatalf("large preview length=%d truncated=%v, want %d and true", len(preview.Content), preview.Truncated, maxWorkspacePreviewBytes)
	}
}
