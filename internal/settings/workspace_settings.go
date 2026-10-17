package settings

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

// LoadWorkspaceCommandToolchainPaths returns the explicitly authorized host
// toolchain roots for one Desktop workspace.
func (s *Store) LoadWorkspaceCommandToolchainPaths(workspace string) ([]string, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, errors.New("settings store is closed")
	}
	key, err := workspaceConfigKey(workspace)
	if err != nil {
		return nil, false, err
	}
	var encoded string
	err = s.db.QueryRow(`SELECT paths_json FROM desktop_command_toolchains WHERE workspace_key = ?`, key).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read Desktop command toolchain paths: %w", err)
	}
	var paths []string
	if err := json.Unmarshal([]byte(encoded), &paths); err != nil {
		return nil, false, fmt.Errorf("decode Desktop command toolchain paths: %w", err)
	}
	return paths, true, nil
}

// SaveWorkspaceSettings atomically stores the Desktop-only runtime settings
// and their explicit host toolchain grants.
func (s *Store) SaveWorkspaceSettings(workspace string, config core.ProjectConfig, paths []string) error {
	if s == nil || s.db == nil {
		return errors.New("settings store is closed")
	}
	key, err := workspaceConfigKey(workspace)
	if err != nil {
		return err
	}
	encryptedConfig, err := encodeWorkspaceConfig(config)
	if err != nil {
		return err
	}
	encodedPaths, err := json.Marshal(paths)
	if err != nil {
		return fmt.Errorf("encode Desktop command toolchain paths: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin Desktop workspace settings save: %w", err)
	}
	defer tx.Rollback()
	updatedAt := time.Now().UTC().UnixNano()
	if _, err := tx.Exec(`
		INSERT INTO desktop_project_configs(workspace_key, encrypted_config, updated_at)
		VALUES(?, ?, ?)
		ON CONFLICT(workspace_key) DO UPDATE SET encrypted_config=excluded.encrypted_config, updated_at=excluded.updated_at
	`, key, encryptedConfig, updatedAt); err != nil {
		return fmt.Errorf("save Desktop project configuration: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT INTO desktop_command_toolchains(workspace_key, paths_json, updated_at)
		VALUES(?, ?, ?)
		ON CONFLICT(workspace_key) DO UPDATE SET paths_json=excluded.paths_json, updated_at=excluded.updated_at
	`, key, string(encodedPaths), updatedAt); err != nil {
		return fmt.Errorf("save Desktop command toolchain paths: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Desktop workspace settings: %w", err)
	}
	return nil
}

// RestoreWorkspaceSettings rolls both records back to their previous presence
// and values after a Core restart fails.
func (s *Store) RestoreWorkspaceSettings(workspace string, config core.ProjectConfig, configFound bool, paths []string, pathsFound bool) error {
	if s == nil || s.db == nil {
		return errors.New("settings store is closed")
	}
	key, err := workspaceConfigKey(workspace)
	if err != nil {
		return err
	}
	var encryptedConfig []byte
	if configFound {
		encryptedConfig, err = encodeWorkspaceConfig(config)
		if err != nil {
			return err
		}
	}
	var encodedPaths []byte
	if pathsFound {
		encodedPaths, err = json.Marshal(paths)
		if err != nil {
			return fmt.Errorf("encode previous Desktop command toolchain paths: %w", err)
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin Desktop workspace settings rollback: %w", err)
	}
	defer tx.Rollback()
	if configFound {
		if _, err := tx.Exec(`
			INSERT INTO desktop_project_configs(workspace_key, encrypted_config, updated_at)
			VALUES(?, ?, ?)
			ON CONFLICT(workspace_key) DO UPDATE SET encrypted_config=excluded.encrypted_config, updated_at=excluded.updated_at
		`, key, encryptedConfig, time.Now().UTC().UnixNano()); err != nil {
			return fmt.Errorf("restore Desktop project configuration: %w", err)
		}
	} else if _, err := tx.Exec(`DELETE FROM desktop_project_configs WHERE workspace_key = ?`, key); err != nil {
		return fmt.Errorf("remove new Desktop project configuration: %w", err)
	}
	if pathsFound {
		if _, err := tx.Exec(`
			INSERT INTO desktop_command_toolchains(workspace_key, paths_json, updated_at)
			VALUES(?, ?, ?)
			ON CONFLICT(workspace_key) DO UPDATE SET paths_json=excluded.paths_json, updated_at=excluded.updated_at
		`, key, string(encodedPaths), time.Now().UTC().UnixNano()); err != nil {
			return fmt.Errorf("restore Desktop command toolchain paths: %w", err)
		}
	} else if _, err := tx.Exec(`DELETE FROM desktop_command_toolchains WHERE workspace_key = ?`, key); err != nil {
		return fmt.Errorf("remove new Desktop command toolchain paths: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit Desktop workspace settings rollback: %w", err)
	}
	return nil
}

func encodeWorkspaceConfig(config core.ProjectConfig) ([]byte, error) {
	config.ModelConfig = publicmodel.Config{Providers: map[string]publicmodel.ProviderConfig{}, Models: map[string]publicmodel.ModelProfile{}}
	plaintext, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("encode Desktop project configuration: %w", err)
	}
	encrypted, err := protectSecret(string(plaintext))
	if err != nil {
		return nil, fmt.Errorf("encrypt Desktop project configuration: %w", err)
	}
	return encrypted, nil
}
