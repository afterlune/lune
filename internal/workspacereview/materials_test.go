package workspacereview

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestCollectMaterialsTruncatesOversizedUntrackedFile keeps one huge untracked
// file from flooding the review context.
func TestCollectMaterialsTruncatesOversizedUntrackedFile(t *testing.T) {
	workspace := initReviewRepository(t)
	huge := filepath.Join(workspace, "huge.txt")
	if err := os.WriteFile(huge, []byte(strings.Repeat("a", int(maxUntrackedFileBytes)+(64<<10))), 0o644); err != nil {
		t.Fatal(err)
	}

	materials, changed, err := CollectMaterials(workspace, "")
	if err != nil {
		t.Fatalf("collect materials: %v", err)
	}
	if !changed {
		t.Fatal("expected the oversized untracked file to count as a change")
	}
	if !strings.Contains(materials, "[truncated: first 1024 KiB shown]") {
		t.Fatalf("missing per-file truncation marker: %q", materials[:min(len(materials), 200)])
	}
	if len(materials) > int(maxUntrackedFileBytes)+(256<<10) {
		t.Fatalf("materials kept %d bytes for a %d byte cap", len(materials), maxUntrackedFileBytes)
	}
}

// TestCollectMaterialsStopsAtTotalLimit reports omitted files instead of silently
// dropping them once the collection is full.
func TestCollectMaterialsStopsAtTotalLimit(t *testing.T) {
	workspace := initReviewRepository(t)
	chunk := []byte(strings.Repeat("b", int(maxUntrackedFileBytes)))
	for index := 0; index < 10; index++ {
		path := filepath.Join(workspace, fmt.Sprintf("bulk-%02d.txt", index))
		if err := os.WriteFile(path, chunk, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	materials, changed, err := CollectMaterials(workspace, "")
	if err != nil {
		t.Fatalf("collect materials: %v", err)
	}
	if !changed {
		t.Fatal("expected the untracked files to count as changes")
	}
	if !strings.Contains(materials, "omitted at the 8 MiB workspace review limit") {
		t.Fatal("missing total-limit marker for omitted files")
	}
	if len(materials) > maxMaterialsBytes+(512<<10) {
		t.Fatalf("materials kept %d bytes for a %d byte cap", len(materials), maxMaterialsBytes)
	}
}

// TestRunGitReportsTimeout keeps a hung repository diagnostic instead of an
// opaque exec error.
func TestRunGitReportsTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	if _, err := runGit(ctx, t.TempDir(), "status"); err == nil {
		t.Fatal("expected a timeout error")
	} else if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected a timeout diagnostic, got %v", err)
	}
}

// TestRunGitSurfacesCommandFailures keeps the git stderr attached to the error.
func TestRunGitSurfacesCommandFailures(t *testing.T) {
	if _, err := runGit(context.Background(), t.TempDir(), "rev-parse", "--git-dir"); err == nil {
		t.Fatal("expected a failure outside a repository")
	} else if !strings.Contains(err.Error(), "git rev-parse --git-dir") {
		t.Fatalf("expected the failed command in the error, got %v", err)
	}
}

// initReviewRepository creates a committed repository so untracked files are the
// only changes Git reports.
func initReviewRepository(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"config", "user.email", "review@example.invalid"},
		{"config", "user.name", "Review Test"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = directory
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return directory
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
