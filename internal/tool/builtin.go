package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/afterlune/lune/internal/model"
)

const (
	commandSchema    = `{"type":"object","properties":{"command":{"type":"string","description":"Shell command to run in the workspace"}},"required":["command"],"additionalProperties":false}`
	readFileSchema   = `{"type":"object","properties":{"path":{"type":"string","description":"Path relative to the workspace"}},"required":["path"],"additionalProperties":false}`
	writeFileSchema  = `{"type":"object","properties":{"path":{"type":"string","description":"Path relative to the workspace"},"content":{"type":"string","description":"Complete file contents to write"}},"required":["path","content"],"additionalProperties":false}`
	editFileSchema   = `{"type":"object","properties":{"path":{"type":"string","description":"Path relative to the workspace"},"old_text":{"type":"string","description":"Exact text to find; must occur once"},"new_text":{"type":"string","description":"Replacement text"}},"required":["path","old_text","new_text"],"additionalProperties":false}`
	maxCommandOutput = 1 << 20
)

// CommandTool executes a command in the workspace. Shell commands can still
// access files outside the workspace; Workspace only sets their working dir.
type CommandTool struct {
	Workspace string
	Timeout   time.Duration
}

func (CommandTool) Definition() model.FunctionTool {
	return functionDefinition("command", "Run a shell command in the workspace and return its stdout and stderr.", commandSchema)
}

func (t CommandTool) Run(ctx context.Context, raw json.RawMessage) Result {
	var args struct {
		Command string `json:"command"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return Result{Err: fmt.Errorf("invalid command arguments: %w", err)}
	}
	if strings.TrimSpace(args.Command) == "" {
		return Result{Err: errors.New("command must not be empty")}
	}
	workspace, err := canonicalWorkspace(t.Workspace)
	if err != nil {
		return Result{Err: err}
	}

	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(commandCtx, "powershell.exe", "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", args.Command)
	} else {
		cmd = exec.CommandContext(commandCtx, "/bin/sh", "-c", args.Command)
	}
	cmd.Dir = workspace

	output := &limitedBuffer{limit: maxCommandOutput}
	cmd.Stdout = output
	cmd.Stderr = output
	runErr := cmd.Run()
	text, truncated := output.String()
	if truncated {
		text += "\n[command output truncated at 1 MiB]"
	}
	if commandCtx.Err() != nil {
		return Result{Output: text, Err: fmt.Errorf("command stopped: %w", commandCtx.Err())}
	}
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return Result{Output: text, Err: fmt.Errorf("command exited with code %d", exitErr.ExitCode())}
		}
		return Result{Output: text, Err: fmt.Errorf("run command: %w", runErr)}
	}
	return Result{Output: text}
}

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

// EditFileTool replaces one exact, unique text occurrence inside a file.
type EditFileTool struct {
	Workspace string
}

func (EditFileTool) Definition() model.FunctionTool {
	return functionDefinition("edit_file", "Replace one unique exact text occurrence in a file in the workspace.", editFileSchema)
}

func (t EditFileTool) Run(ctx context.Context, raw json.RawMessage) Result {
	if err := ctx.Err(); err != nil {
		return Result{Err: fmt.Errorf("edit stopped: %w", err)}
	}
	var args struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := decodeArguments(raw, &args); err != nil {
		return Result{Err: fmt.Errorf("invalid edit_file arguments: %w", err)}
	}
	if args.OldText == "" {
		return Result{Err: errors.New("old_text must not be empty")}
	}
	path, err := resolveWorkspacePath(t.Workspace, args.Path, true)
	if err != nil {
		return Result{Err: err}
	}
	if err := ctx.Err(); err != nil {
		return Result{Err: fmt.Errorf("edit stopped: %w", err)}
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return Result{Err: fmt.Errorf("read %q: %w", args.Path, err)}
	}
	if err := ctx.Err(); err != nil {
		return Result{Err: fmt.Errorf("edit stopped: %w", err)}
	}
	count := bytes.Count(contents, []byte(args.OldText))
	if count != 1 {
		return Result{Err: fmt.Errorf("old_text must occur exactly once in %q; found %d occurrences", args.Path, count)}
	}
	updated := bytes.Replace(contents, []byte(args.OldText), []byte(args.NewText), 1)
	info, err := os.Stat(path)
	if err != nil {
		return Result{Err: fmt.Errorf("stat %q: %w", args.Path, err)}
	}
	if err := ctx.Err(); err != nil {
		return Result{Err: fmt.Errorf("edit stopped: %w", err)}
	}
	if err := os.WriteFile(path, updated, info.Mode().Perm()); err != nil {
		return Result{Err: fmt.Errorf("write %q: %w", args.Path, err)}
	}
	return Result{Output: fmt.Sprintf("Updated %s", args.Path)}
}

func functionDefinition(name, description, schema string) model.FunctionTool {
	strict := true
	return model.FunctionTool{
		Name:        name,
		Description: description,
		Parameters:  json.RawMessage(schema),
		Strict:      &strict,
	}
}

func canonicalWorkspace(workspace string) (string, error) {
	if workspace == "" {
		var err error
		workspace, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("get current directory: %w", err)
		}
	}
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve workspace symlinks: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stat workspace: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace %q is not a directory", workspace)
	}
	return resolved, nil
}

func resolveWorkspacePath(workspace, requestedPath string, mustExist bool) (string, error) {
	root, err := canonicalWorkspace(workspace)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(requestedPath) == "" {
		return "", errors.New("path must not be empty")
	}
	if filepath.IsAbs(requestedPath) {
		return "", errors.New("path must be relative to the workspace")
	}

	candidate := filepath.Clean(filepath.Join(root, requestedPath))
	if !pathWithin(root, candidate) || candidate == root {
		return "", errors.New("path must stay inside the workspace and name a file")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err == nil {
		if !pathWithin(root, resolved) {
			return "", errors.New("path resolves outside the workspace")
		}
		if mustExist {
			return resolved, nil
		}
		return resolved, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("resolve path %q: %w", requestedPath, err)
	}
	if mustExist {
		return "", fmt.Errorf("file %q does not exist", requestedPath)
	}
	if _, lstatErr := os.Lstat(candidate); lstatErr == nil {
		return "", errors.New("cannot write through an unresolved symlink")
	} else if !errors.Is(lstatErr, fs.ErrNotExist) {
		return "", fmt.Errorf("inspect path %q: %w", requestedPath, lstatErr)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(candidate))
	if err != nil {
		return "", fmt.Errorf("parent directory for %q must exist: %w", requestedPath, err)
	}
	if !pathWithin(root, parent) {
		return "", errors.New("path resolves outside the workspace")
	}
	return filepath.Join(parent, filepath.Base(candidate)), nil
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	originalLength := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return originalLength, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		b.truncated = true
	}
	_, _ = b.buf.Write(p)
	return originalLength, nil
}

func (b *limitedBuffer) String() (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String(), b.truncated
}
