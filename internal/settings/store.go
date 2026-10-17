package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
	_ "modernc.org/sqlite"
)

const defaultTheme = "system"

type Store struct{ db *sql.DB }

func UserSettingsPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(root, "luneAgent", "settings.db"), nil
}

func OpenUser() (*Store, error) {
	path, err := UserSettingsPath()
	if err != nil {
		return nil, err
	}
	return Open(path)
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("settings database path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create settings directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open settings database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connect to settings database: %w", err)
	}
	const schema = `
CREATE TABLE IF NOT EXISTS app_settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS providers (
  name TEXT PRIMARY KEY,
  protocol TEXT NOT NULL,
  base_url TEXT NOT NULL,
  encrypted_api_key BLOB NOT NULL,
  server_compaction INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS models (
  name TEXT PRIMARY KEY,
  provider TEXT NOT NULL,
  model TEXT NOT NULL,
  max_output_tokens INTEGER NOT NULL DEFAULT 0,
  supports_input_audio INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS recent_workspaces (
  path TEXT PRIMARY KEY,
  last_used TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS desktop_project_configs (
  workspace_key TEXT PRIMARY KEY,
  encrypted_config BLOB NOT NULL,
  updated_at INTEGER NOT NULL
);`
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize settings database: %w", err)
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) LoadModelConfig() (publicmodel.Config, error) {
	if s == nil || s.db == nil {
		return publicmodel.Config{}, errors.New("settings store is closed")
	}
	config := publicmodel.Config{Providers: map[string]publicmodel.ProviderConfig{}, Models: map[string]publicmodel.ModelProfile{}}
	rows, err := s.db.Query(`SELECT name, protocol, base_url, encrypted_api_key, server_compaction FROM providers ORDER BY name`)
	if err != nil {
		return publicmodel.Config{}, fmt.Errorf("read providers: %w", err)
	}
	for rows.Next() {
		var name, protocol, baseURL string
		var encrypted []byte
		var serverCompaction bool
		if err := rows.Scan(&name, &protocol, &baseURL, &encrypted, &serverCompaction); err != nil {
			_ = rows.Close()
			return publicmodel.Config{}, fmt.Errorf("read provider: %w", err)
		}
		apiKey, err := unprotectSecret(encrypted)
		if err != nil {
			_ = rows.Close()
			return publicmodel.Config{}, fmt.Errorf("decrypt API key for provider %q: %w", name, err)
		}
		config.Providers[name] = publicmodel.ProviderConfig{
			Protocol: protocol, BaseURL: baseURL, APIKey: apiKey, ServerCompaction: serverCompaction,
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return publicmodel.Config{}, fmt.Errorf("read providers: %w", err)
	}
	if err := rows.Close(); err != nil {
		return publicmodel.Config{}, fmt.Errorf("close provider rows: %w", err)
	}
	modelRows, err := s.db.Query(`SELECT name, provider, model, max_output_tokens, supports_input_audio FROM models ORDER BY name`)
	if err != nil {
		return publicmodel.Config{}, fmt.Errorf("read model profiles: %w", err)
	}
	for modelRows.Next() {
		var name, provider, modelID string
		var maxOutputTokens int64
		var supportsInputAudio bool
		if err := modelRows.Scan(&name, &provider, &modelID, &maxOutputTokens, &supportsInputAudio); err != nil {
			_ = modelRows.Close()
			return publicmodel.Config{}, fmt.Errorf("read model profile: %w", err)
		}
		config.Models[name] = publicmodel.ModelProfile{
			Provider: provider, Model: modelID, MaxOutputTokens: maxOutputTokens, SupportsInputAudio: supportsInputAudio,
		}
	}
	if err := modelRows.Err(); err != nil {
		_ = modelRows.Close()
		return publicmodel.Config{}, fmt.Errorf("read model profiles: %w", err)
	}
	if err := modelRows.Close(); err != nil {
		return publicmodel.Config{}, fmt.Errorf("close model rows: %w", err)
	}
	if err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = 'defaultModel'`).Scan(&config.DefaultModel); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return publicmodel.Config{}, fmt.Errorf("read default model: %w", err)
	}
	var maxParallel string
	if err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = 'maxParallelAgents'`).Scan(&maxParallel); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return publicmodel.Config{}, fmt.Errorf("read parallel agent setting: %w", err)
	}
	if maxParallel != "" {
		if _, err := fmt.Sscan(maxParallel, &config.MaxParallelAgents); err != nil {
			return publicmodel.Config{}, fmt.Errorf("decode parallel agent setting: %w", err)
		}
	}
	if len(config.Providers) == 0 && len(config.Models) == 0 {
		return config, nil
	}
	if err := config.Validate(); err != nil {
		return publicmodel.Config{}, fmt.Errorf("validate shared model settings: %w", err)
	}
	for name, provider := range config.Providers {
		if strings.TrimSpace(provider.APIKey) == "" {
			return publicmodel.Config{}, fmt.Errorf("provider %q has no API key", name)
		}
	}
	return config, nil
}

func (s *Store) SaveModelConfig(config publicmodel.Config) error {
	return s.SaveModelConfigWith(config, nil, nil)
}

func (s *Store) RestoreModelConfig(config publicmodel.Config) error {
	if len(config.Providers) > 0 || len(config.Models) > 0 {
		return s.SaveModelConfig(config)
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin model settings restore: %w", err)
	}
	defer tx.Rollback()
	for _, table := range []string{"providers", "models"} {
		if _, err := tx.Exec(`DELETE FROM ` + table); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM app_settings WHERE key IN ('defaultModel', 'maxParallelAgents')`); err != nil {
		return fmt.Errorf("clear model settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit model settings restore: %w", err)
	}
	return nil
}

// SaveModelConfigWith applies a related file update while the SQLite write is
// open. If the update or commit fails, rollbackFile restores the previous file.
func (s *Store) SaveModelConfigWith(config publicmodel.Config, applyFile, rollbackFile func() error) error {
	if s == nil || s.db == nil {
		return errors.New("settings store is closed")
	}
	if err := config.Validate(); err != nil {
		return fmt.Errorf("validate model settings: %w", err)
	}
	for name, provider := range config.Providers {
		if strings.TrimSpace(provider.APIKey) == "" {
			return fmt.Errorf("provider %q requires an API key", name)
		}
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin settings transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM providers`); err != nil {
		return fmt.Errorf("replace providers: %w", err)
	}
	for name, provider := range config.Providers {
		secret, err := protectSecret(provider.APIKey)
		if err != nil {
			return fmt.Errorf("encrypt API key for provider %q: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO providers(name, protocol, base_url, encrypted_api_key, server_compaction) VALUES (?, ?, ?, ?, ?)`,
			name, provider.Protocol, provider.BaseURL, secret, provider.ServerCompaction); err != nil {
			return fmt.Errorf("save provider %q: %w", name, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM models`); err != nil {
		return fmt.Errorf("replace model profiles: %w", err)
	}
	for name, profile := range config.Models {
		if _, err := tx.Exec(`INSERT INTO models(name, provider, model, max_output_tokens, supports_input_audio) VALUES (?, ?, ?, ?, ?)`,
			name, profile.Provider, profile.Model, profile.MaxOutputTokens, profile.SupportsInputAudio); err != nil {
			return fmt.Errorf("save model profile %q: %w", name, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO app_settings(key, value) VALUES ('defaultModel', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, config.DefaultModel); err != nil {
		return fmt.Errorf("save default model: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO app_settings(key, value) VALUES ('maxParallelAgents', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, fmt.Sprint(config.MaxParallelAgents)); err != nil {
		return fmt.Errorf("save parallel agent setting: %w", err)
	}
	fileApplied := false
	if applyFile != nil {
		if err := applyFile(); err != nil {
			return err
		}
		fileApplied = true
	}
	if err := tx.Commit(); err != nil {
		if fileApplied && rollbackFile != nil {
			return errors.Join(fmt.Errorf("commit shared model settings: %w", err), rollbackFile())
		}
		return fmt.Errorf("commit shared model settings: %w", err)
	}
	return nil
}

func (s *Store) Theme() (string, error) {
	var theme string
	err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = 'theme'`).Scan(&theme)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultTheme, nil
	}
	if err != nil {
		return "", fmt.Errorf("read theme setting: %w", err)
	}
	switch theme {
	case "system", "light", "dark":
		return theme, nil
	default:
		return defaultTheme, nil
	}
}

func (s *Store) SetTheme(theme string) error {
	if theme != "system" && theme != "light" && theme != "dark" {
		return fmt.Errorf("unsupported theme %q", theme)
	}
	_, err := s.db.Exec(`INSERT INTO app_settings(key, value) VALUES ('theme', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, theme)
	if err != nil {
		return fmt.Errorf("save theme setting: %w", err)
	}
	return nil
}

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
	var config core.ProjectConfig
	if err := json.Unmarshal([]byte(plaintext), &config); err != nil {
		return core.ProjectConfig{}, false, fmt.Errorf("decode Desktop project configuration: %w", err)
	}
	config.ModelConfig = publicmodel.Config{Providers: map[string]publicmodel.ProviderConfig{}, Models: map[string]publicmodel.ModelProfile{}}
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
	config.ModelConfig = publicmodel.Config{Providers: map[string]publicmodel.ProviderConfig{}, Models: map[string]publicmodel.ModelProfile{}}
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
	if _, err := s.db.Exec(`DELETE FROM desktop_project_configs WHERE workspace_key = ?`, key); err != nil {
		return fmt.Errorf("delete Desktop project configuration: %w", err)
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

type RecentWorkspace struct {
	Path     string `json:"path"`
	LastUsed string `json:"lastUsed"`
}

func (s *Store) RecentWorkspaces() ([]RecentWorkspace, error) {
	rows, err := s.db.Query(`SELECT path, last_used FROM recent_workspaces ORDER BY last_used DESC LIMIT 12`)
	if err != nil {
		return nil, fmt.Errorf("read recent workspaces: %w", err)
	}
	defer rows.Close()
	var result []RecentWorkspace
	for rows.Next() {
		var item RecentWorkspace
		if err := rows.Scan(&item.Path, &item.LastUsed); err != nil {
			return nil, fmt.Errorf("read recent workspace: %w", err)
		}
		if info, err := os.Stat(item.Path); err == nil && info.IsDir() {
			result = append(result, item)
		}
	}
	return result, rows.Err()
}

func (s *Store) TouchWorkspace(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve workspace path: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("workspace directory is unavailable: %s", absolute)
	}
	if _, err := s.db.Exec(`INSERT INTO recent_workspaces(path, last_used) VALUES (?, ?) ON CONFLICT(path) DO UPDATE SET last_used = excluded.last_used`, absolute, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("save recent workspace: %w", err)
	}
	if _, err := s.db.Exec(`DELETE FROM recent_workspaces WHERE path NOT IN (SELECT path FROM recent_workspaces ORDER BY last_used DESC LIMIT 12)`); err != nil {
		return fmt.Errorf("trim recent workspaces: %w", err)
	}
	return nil
}
