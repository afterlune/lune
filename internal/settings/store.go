package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

type Store struct {
	db           *sql.DB
	importMu     sync.Mutex
	importDrafts map[string]map[string]any
}

const tuiThemeKey = "tui.theme"
const webLanguageKey = "web.language"
const webThemeKey = "web.theme"

func UserSettingsPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(root, "lune", "settings.db"), nil
}

func UserSettingsDirectory() (string, error) {
	path, err := UserSettingsPath()
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
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
	// Allow concurrent TUI/CLI processes to wait rather than fail with
	// "database is locked" on shared settings updates.
	const pragmas = `
PRAGMA busy_timeout = 5000;
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;`
	if _, err := db.ExecContext(context.Background(), pragmas); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("configure settings database: %w", err)
	}
	const schema = `
CREATE TABLE IF NOT EXISTS app_settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS recent_workspaces (
  path TEXT PRIMARY KEY,
  last_used TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS command_toolchains (
  workspace_key TEXT PRIMARY KEY,
  paths_json TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS web_configs (
  scope TEXT PRIMARY KEY,
  document TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS web_secrets (
  scope TEXT NOT NULL,
  name TEXT NOT NULL,
  encoding TEXT NOT NULL,
  value BLOB NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(scope, name)
);
CREATE TABLE IF NOT EXISTS web_connections (
  mode TEXT NOT NULL,
  address TEXT NOT NULL,
  name TEXT NOT NULL,
  touched INTEGER NOT NULL,
  session TEXT,
  PRIMARY KEY(mode, address)
);
CREATE TABLE IF NOT EXISTS workspaces (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  is_open INTEGER NOT NULL DEFAULT 1,
  last_session_id TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  last_opened_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workspaces_open_time
  ON workspaces(is_open, last_opened_at DESC, created_at DESC);
CREATE TABLE IF NOT EXISTS workspace_sessions (
  workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  session_id TEXT NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  archived INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  last_opened_at INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(workspace_id, session_id)
);
CREATE INDEX IF NOT EXISTS idx_workspace_sessions_ws_time
  ON workspace_sessions(workspace_id, updated_at DESC, created_at DESC);`
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize settings database: %w", err)
	}
	if err := store.migrateLegacyWorkspaces(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate legacy workspaces: %w", err)
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(path, 0o600)
	}
	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

type RecentWorkspace struct {
	Path     string `json:"path"`
	LastUsed string `json:"lastUsed"`
}

func (s *Store) setting(key string) (string, bool, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s setting: %w", key, err)
	}
	return value, true, nil
}

func (s *Store) saveSetting(key, value string) error {
	if _, err := s.db.Exec(`INSERT INTO app_settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
		return fmt.Errorf("save %s setting: %w", key, err)
	}
	return nil
}

func (s *Store) WebLanguage() (string, error) {
	value, _, err := s.setting(webLanguageKey)
	return value, err
}

func (s *Store) SaveWebLanguage(value string) error {
	if value != "en" && value != "zh" {
		return errors.New("invalid interface language")
	}
	if _, found, err := s.setting("web.preferences"); err != nil {
		return err
	} else if found {
		preferences, err := s.WebUIPreferences()
		if err != nil {
			return err
		}
		preferences.Language = value
		return s.SaveWebUIPreferences(preferences)
	}
	return s.saveSetting(webLanguageKey, value)
}

func (s *Store) WebTheme() (string, error) {
	value, _, err := s.setting(webThemeKey)
	return value, err
}

func (s *Store) SaveWebTheme(value string) error {
	if value != "system" && value != "light" && value != "dark" {
		return errors.New("invalid web theme preference")
	}
	if _, found, err := s.setting("web.preferences"); err != nil {
		return err
	} else if found {
		preferences, err := s.WebUIPreferences()
		if err != nil {
			return err
		}
		preferences.Theme = value
		return s.SaveWebUIPreferences(preferences)
	}
	return s.saveSetting(webThemeKey, value)
}
