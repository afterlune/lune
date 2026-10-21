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

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

// LoadWorkspaceConfig returns Desktop-owned project settings. The payload is
// encrypted as a whole so MCP environment values never persist in plaintext.
func (s *Store) LoadWorkspaceConfig(workspace string) (core.ProjectConfig, bool, error) {
	if s == nil || s.db == nil {
		return core.ProjectConfig{}, false, errors.New("settings store is closed")
	}
	key, err := workspaceConfigKey(workspace)
	if err != nil {
		return core.ProjectConfig{}, false, err
	}
	var encrypted []byte
	err = s.db.QueryRow(`SELECT encrypted_config FROM desktop_project_configs WHERE workspace_key = ?`, key).Scan(&encrypted)
	if errors.Is(err, sql.ErrNoRows) {
		return core.DefaultProjectConfig(), false, nil
	}
	if err != nil {
		return core.ProjectConfig{}, false, fmt.Errorf("read Desktop project configuration: %w", err)
	}
	plaintext, err := unprotectSecret(encrypted)
	if err != nil {
		return core.ProjectConfig{}, false, fmt.Errorf("decrypt Desktop project configuration: %w", err)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal([]byte(plaintext), &shape); err != nil {
		return core.ProjectConfig{}, false, fmt.Errorf("decode Desktop project configuration: %w", err)
	}
	if _, currentShape := shape["runtime"]; !currentShape {
		return core.DefaultProjectConfig(), true, nil
	}
	var config core.ProjectConfig
	if err := json.Unmarshal([]byte(plaintext), &config); err != nil {
		return core.ProjectConfig{}, false, fmt.Errorf("decode Desktop project configuration: %w", err)
	}
	config.Models = publicmodel.Config{Providers: map[string]publicmodel.ProviderConfig{}, Profiles: map[string]publicmodel.ModelProfile{}}
	return config, true, nil
}

// SaveWorkspaceConfig encrypts and replaces one workspace's Desktop runtime
// settings. Model settings stay in their existing user-level tables.
func (s *Store) SaveWorkspaceConfig(workspace string, config core.ProjectConfig) error {
	if s == nil || s.db == nil {
		return errors.New("settings store is closed")
	}
	key, err := workspaceConfigKey(workspace)
	if err != nil {
		return err
	}
	config.Models = publicmodel.Config{Providers: map[string]publicmodel.ProviderConfig{}, Profiles: map[string]publicmodel.ModelProfile{}}
	plaintext, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("encode Desktop project configuration: %w", err)
	}
	encrypted, err := protectSecret(string(plaintext))
	if err != nil {
		return fmt.Errorf("encrypt Desktop project configuration: %w", err)
	}
	if _, err := s.db.Exec(`
		INSERT INTO desktop_project_configs(workspace_key, encrypted_config, updated_at)
		VALUES(?, ?, ?)
		ON CONFLICT(workspace_key) DO UPDATE SET encrypted_config=excluded.encrypted_config, updated_at=excluded.updated_at
	`, key, encrypted, time.Now().UTC().UnixNano()); err != nil {
		return fmt.Errorf("save Desktop project configuration: %w", err)
	}
	return nil
}

// DeleteWorkspaceConfig removes the Desktop-owned settings for one workspace.
// It is used to roll back a first-time save if recreating Core fails.
func (s *Store) DeleteWorkspaceConfig(workspace string) error {
	if s == nil || s.db == nil {
		return errors.New("settings store is closed")
	}
	key, err := workspaceConfigKey(workspace)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin Desktop workspace settings delete: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM desktop_project_configs WHERE workspace_key = ?`, key); err != nil {
		return fmt.Errorf("delete Desktop project configuration: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM desktop_command_toolchains WHERE workspace_key = ?`, key); err != nil {
		return fmt.Errorf("delete Desktop command toolchain paths: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Desktop workspace settings delete: %w", err)
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
