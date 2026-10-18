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
