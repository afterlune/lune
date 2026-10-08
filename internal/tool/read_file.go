package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/afterlune/lune/internal/model"
)

const readFileSchema = `{"type":"object","properties":{"path":{"type":"string","description":"Path relative to the workspace"}},"required":["path"],"additionalProperties":false}`

// ReadFileTool reads a UTF-8 or plain-text file inside the workspace.
type ReadFileTool struct {
	Workspace string
}

func (ReadFileTool) Definition() model.FunctionTool {
	return functionDefinition("read_file", "Read and return the contents of a file in the workspace.", readFileSchema)
}

func (t ReadFileTool) Run(ctx context.Context, raw json.RawMessage) Result {
	if err := ctx.Err(); err != nil {
		return Result{Err: fmt.Errorf("read stopped: %w", err)}
	}
	var args struct {
		Path string `json:"path"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return Result{Err: fmt.Errorf("invalid read_file arguments: %w", err)}
	}
	path, err := resolveWorkspacePath(t.Workspace, args.Path, true)
	if err != nil {
		return Result{Err: err}
	}
	if err := ctx.Err(); err != nil {
		return Result{Err: fmt.Errorf("read stopped: %w", err)}
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return Result{Err: fmt.Errorf("read %q: %w", args.Path, err)}
	}
	return Result{Output: string(contents)}
}
