package tool

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// canonicalWorkspace resolves a workspace path to an absolute, symlink-free
// directory. Shared by every tool that touches the file system.
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

// resolveWorkspacePath maps a relative tool argument onto a path inside the
// workspace, refusing anything that escapes it.
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
	if !pathWithin(root, candidate) {
		return "", errors.New("path escapes the workspace")
	}
	if candidate == root {
		return "", errors.New("path refers to a directory; use list_dir to inspect it")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err == nil {
		if !pathWithin(root, resolved) {
			return "", errors.New("path resolves outside the workspace")
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil {
			return "", fmt.Errorf("stat path %q: %w", requestedPath, statErr)
		}
		if info.IsDir() {
			return "", errors.New("path refers to a directory; use list_dir to inspect it")
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("path does not refer to a regular file")
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

// resolveWorkspaceEntry resolves an existing workspace-relative path and
// returns its metadata. An empty path or "." refers to the workspace root.
func resolveWorkspaceEntry(workspace, requestedPath string) (string, fs.FileInfo, error) {
	root, err := canonicalWorkspace(workspace)
	if err != nil {
		return "", nil, err
	}
	if strings.TrimSpace(requestedPath) == "" {
		requestedPath = "."
	}
	if filepath.IsAbs(requestedPath) {
		return "", nil, errors.New("path must be relative to the workspace")
	}

	candidate := filepath.Clean(filepath.Join(root, requestedPath))
	if !pathWithin(root, candidate) {
		return "", nil, errors.New("path escapes the workspace")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil, fmt.Errorf("path %q does not exist", requestedPath)
		}
		return "", nil, fmt.Errorf("resolve path %q: %w", requestedPath, err)
	}
	if !pathWithin(root, resolved) {
		return "", nil, errors.New("path resolves outside the workspace")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", nil, fmt.Errorf("stat path %q: %w", requestedPath, err)
	}
	return resolved, info, nil
}

func pathWithin(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
