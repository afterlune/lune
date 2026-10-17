package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func desktopProjectStateDir(workspace string) (string, error) {
	canonical, err := canonicalWorkspacePath(workspace)
	if err != nil {
		return "", err
	}
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find desktop state directory: %w", err)
	}
	return filepath.Join(root, "luneAgent", "desktop", workspaceKey(canonical)), nil
}

func desktopWorktreeDir(workspace string, sessionID string) (string, error) {
	canonical, err := canonicalWorkspacePath(workspace)
	if err != nil {
		return "", err
	}
	root, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("find desktop worktree directory: %w", err)
	}
	return filepath.Join(root, "luneAgent", "worktrees", workspaceKey(canonical), sessionID), nil
}

func canonicalWorkspacePath(workspace string) (string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err == nil {
		absolute = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if runtime.GOOS == "windows" {
		absolute = strings.ToLower(absolute)
	}
	return absolute, nil
}

func workspaceKey(canonicalPath string) string {
	sum := sha256.Sum256([]byte(canonicalPath))
	return hex.EncodeToString(sum[:16])
}

func removeDesktopWorktreeStateDir(stateRoot, stateDir string) error {
	base, err := filepath.Abs(filepath.Join(stateRoot, "worktree-state"))
	if err != nil {
		return fmt.Errorf("resolve Worktree state root: %w", err)
	}
	target, err := filepath.Abs(stateDir)
	if err != nil {
		return fmt.Errorf("resolve Worktree state directory: %w", err)
	}
	relative, err := filepath.Rel(base, target)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("Worktree state directory is outside its managed root")
	}
	resolvedBase, err := filepath.EvalSymlinks(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve Worktree state root: %w", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve Worktree state directory: %w", err)
	}
	if !workspaceContains(resolvedBase, resolvedTarget) || samePath(resolvedBase, resolvedTarget) {
		return errors.New("Worktree state directory resolves outside its managed root")
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove Worktree session state: %w", err)
	}
	return nil
}
