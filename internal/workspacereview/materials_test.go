package workspacereview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCollectMaterialsRejectsPathsOutsideWorkspace covers the guard that runs
// before any Git command is executed, so it holds without a repository.
func TestCollectMaterialsRejectsPathsOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()

	for _, requested := range []string{"..", "../escape", "sub/../../escape"} {
		if _, _, err := CollectMaterials(workspace, requested); err == nil {
			t.Fatalf("requested path %q must be rejected", requested)
		} else if !strings.Contains(err.Error(), "inside the workspace") {
			t.Fatalf("requested path %q: unexpected error %v", requested, err)
		}
	}

	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := CollectMaterials(workspace, outside); err == nil {
		t.Fatal("an absolute path outside the workspace must be rejected")
	}
}

func TestPromptKeepsScopeAndMaterials(t *testing.T) {
	prompt := Prompt("diff --git a/x.go b/x.go", "")
	if !strings.Contains(prompt, "Scope: all staged and unstaged changes") {
		t.Fatalf("whole-workspace scope missing: %q", prompt)
	}
	if !strings.Contains(prompt, "diff --git a/x.go b/x.go") {
		t.Fatalf("materials missing from prompt: %q", prompt)
	}

	scoped := Prompt("materials", "internal/tui")
	if !strings.Contains(scoped, "Scope: changes under `internal/tui`") {
		t.Fatalf("narrowed scope missing: %q", scoped)
	}
	if strings.Contains(scoped, "Scope: all staged and unstaged changes") {
		t.Fatalf("scoped prompt kept the whole-workspace wording: %q", scoped)
	}
}

// TestPromptEscapesCollidingBoundary keeps untrusted patch content from closing
// the fenced block early.
func TestPromptEscapesCollidingBoundary(t *testing.T) {
	prompt := Prompt("payload LUNE_WORKSPACE_REVIEW_INPUT_ END", "")
	if strings.Contains(prompt, "END LUNE_WORKSPACE_REVIEW_INPUT\n") {
		t.Fatalf("colliding boundary was not extended: %q", prompt)
	}
	if !strings.Contains(prompt, "BEGIN LUNE_WORKSPACE_REVIEW_INPUT_") {
		t.Fatalf("extended boundary missing: %q", prompt)
	}
}
