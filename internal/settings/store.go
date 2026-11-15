package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

const tuiThemeKey = "tui.theme"

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
PRAGMA journal_mode = WAL;`
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
