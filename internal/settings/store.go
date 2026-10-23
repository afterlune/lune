package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	publicmodel "github.com/afterlune/luneCore/model"
	_ "modernc.org/sqlite"
)

const defaultTheme = "system"
const defaultPanelOpacity = 80

type Appearance struct {
	Theme         string `json:"theme"`
	WallpaperType string `json:"wallpaperType"`
	WallpaperFile string `json:"wallpaperFile,omitempty"`
	WallpaperName string `json:"wallpaperName,omitempty"`
	PanelOpacity  int    `json:"panelOpacity"`
}

type Store struct{ db *sql.DB }

const (
	desktopLastWorkspaceKey = "desktop.lastWorkspace"
	desktopLastSessionKey   = "desktop.lastSession"
	desktopWindowWidthKey   = "desktop.windowWidth"
	desktopWindowHeightKey  = "desktop.windowHeight"
	tuiThemeKey             = "tui.theme"
)

// DesktopRestore is the small amount of UI state needed to make the next
// launch continue where the previous one stopped.
type DesktopRestore struct {
	Workspace    string
	SessionID    string
	WindowWidth  int
	WindowHeight int
}

func UserSettingsPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(root, "luneAgent", "settings.db"), nil
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
);
CREATE TABLE IF NOT EXISTS desktop_command_toolchains (
  workspace_key TEXT PRIMARY KEY,
  paths_json TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS task_experiences (
  id TEXT PRIMARY KEY,
  dedupe_key TEXT NOT NULL UNIQUE,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  purpose TEXT NOT NULL,
  tags_json TEXT NOT NULL,
  input_schema_json TEXT NOT NULL,
  preconditions_json TEXT NOT NULL,
  script TEXT NOT NULL,
  required_tools_json TEXT NOT NULL,
  required_capabilities_json TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  source_session_id TEXT NOT NULL DEFAULT '',
  source_execution_id TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  last_used_at INTEGER,
  use_count INTEGER NOT NULL DEFAULT 0
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

type SharedModelSettings struct {
	Catalog           publicmodel.Config
	MaxParallelAgents int
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
