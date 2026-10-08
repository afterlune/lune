package tool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFileToolsRespectCanceledContext(t *testing.T) {
	workspace := t.TempDir()
	file := filepath.Join(workspace, "existing.txt")
	if err := os.WriteFile(file, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	writeResult := (WriteFileTool{Workspace: workspace}).Run(ctx, []byte(`{"path":"created.txt","content":"new"}`))
	if !errors.Is(writeResult.Err, context.Canceled) {
		t.Fatalf("write_file error = %v, want context.Canceled", writeResult.Err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "created.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created.txt stat error = %v, want os.ErrNotExist", err)
	}

	readResult := (ReadFileTool{Workspace: workspace}).Run(ctx, []byte(`{"path":"existing.txt"}`))
	if !errors.Is(readResult.Err, context.Canceled) {
		t.Fatalf("read_file error = %v, want context.Canceled", readResult.Err)
	}

	editResult := (EditFileTool{Workspace: workspace}).Run(ctx, []byte(`{"path":"existing.txt","old_text":"original","new_text":"changed"}`))
	if !errors.Is(editResult.Err, context.Canceled) {
		t.Fatalf("edit_file error = %v, want context.Canceled", editResult.Err)
	}
	contents, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "original" {
		t.Fatalf("existing.txt = %q, want original", contents)
	}
}
