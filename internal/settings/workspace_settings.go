package settings

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// LoadWorkspaceCommandToolchainPaths returns the explicitly authorized host
// toolchain roots for a workspace.
func (s *Store) LoadWorkspaceCommandToolchainPaths(workspace string) ([]string, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, errors.New("settings store is closed")
	}
	key, err := workspaceConfigKey(workspace)
	if err != nil {
		return nil, false, err
	}
	var encoded string
	err = s.db.QueryRow(`SELECT paths_json FROM command_toolchains WHERE workspace_key = ?`, key).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read command toolchain paths: %w", err)
	}
	var paths []string
	if err := json.Unmarshal([]byte(encoded), &paths); err != nil {
		return nil, false, fmt.Errorf("decode command toolchain paths: %w", err)
	}
	return paths, true, nil
}

// SaveWorkspaceCommandToolchainPaths stores the authorized host toolchain roots for a workspace.
func (s *Store) SaveWorkspaceCommandToolchainPaths(workspace string, paths []string) error {
	if s == nil || s.db == nil {
		return errors.New("settings store is closed")
	}
	key, err := workspaceConfigKey(workspace)
	if err != nil {
		return err
	}
	encodedPaths, err := json.Marshal(paths)
	if err != nil {
		return fmt.Errorf("encode command toolchain paths: %w", err)
	}
	updatedAt := time.Now().UTC().UnixNano()
	_, err = s.db.Exec(`
		INSERT INTO command_toolchains(workspace_key, paths_json, updated_at)
		VALUES(?, ?, ?)
		ON CONFLICT(workspace_key) DO UPDATE SET paths_json=excluded.paths_json, updated_at=excluded.updated_at
	`, key, string(encodedPaths), updatedAt)
	if err != nil {
		return fmt.Errorf("save command toolchain paths: %w", err)
	}
	return nil
}

func workspaceConfigKey(workspace string) (string, error) {
	if strings.TrimSpace(workspace) == "" {
		return "", errors.New("workspace path is required")
	}
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		absolute = resolved
	}
	absolute = filepath.Clean(absolute)
	if runtime.GOOS == "windows" {
		absolute = strings.ToLower(absolute)
	}
	return absolute, nil
}

const workspacePermissionKeyPrefix = "workspace.permission:"

func isValidWorkspacePermissionMode(mode string) bool {
	switch mode {
	case "read-only", "workspace", "auto", "full-access":
		return true
	default:
		return false
	}
}

// LoadWorkspacePermissionMode returns the persisted tool permission mode for a workspace.
func (s *Store) LoadWorkspacePermissionMode(workspace string) (string, bool, error) {
	if s == nil || s.db == nil {
		return "", false, errors.New("settings store is closed")
	}
	key, err := workspaceConfigKey(workspace)
	if err != nil {
		return "", false, err
	}
	mode, found, err := s.setting(workspacePermissionKeyPrefix + key)
	if err != nil || !found || !isValidWorkspacePermissionMode(mode) {
		return "", false, err
	}
	return mode, true, nil
}

// SaveWorkspacePermissionMode stores the selected tool permission mode for a workspace.
func (s *Store) SaveWorkspacePermissionMode(workspace, mode string) error {
	if s == nil || s.db == nil {
		return errors.New("settings store is closed")
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if !isValidWorkspacePermissionMode(mode) {
		return fmt.Errorf("unknown permission mode %q", mode)
	}
	key, err := workspaceConfigKey(workspace)
	if err != nil {
		return err
	}
	return s.saveSetting(workspacePermissionKeyPrefix+key, mode)
}
