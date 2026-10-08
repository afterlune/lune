package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/afterlune/lune/internal/model"
)

const writeFileSchema = `{"type":"object","properties":{"path":{"type":"string","description":"Path relative to the workspace"},"content":{"type":"string","description":"Complete file contents to write"}},"required":["path","content"],"additionalProperties":false}`

// WriteFileTool creates or replaces a file inside the workspace.
type WriteFileTool struct {
	Workspace string
}

func (WriteFileTool) Definition() model.FunctionTool {
	return functionDefinition("write_file", "Create or replace a file in the workspace with the supplied contents.", writeFileSchema)
}

func (t WriteFileTool) Run(ctx context.Context, raw json.RawMessage) Result {
	if err := ctx.Err(); err != nil {
		return Result{Err: fmt.Errorf("write stopped: %w", err)}
	}
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return Result{Err: fmt.Errorf("invalid write_file arguments: %w", err)}
	}
	path, err := resolveWorkspacePath(t.Workspace, args.Path, false)
	if err != nil {
		return Result{Err: err}
	}
	if err := ctx.Err(); err != nil {
		return Result{Err: fmt.Errorf("write stopped: %w", err)}
	}
	if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
		return Result{Err: fmt.Errorf("write %q: %w", args.Path, err)}
	}
	return Result{Output: fmt.Sprintf("Wrote %d bytes to %s", len(args.Content), args.Path)}
}
