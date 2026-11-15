package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

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

// TUITheme returns the saved TUI theme mode. The boolean is false when no
// preference has been saved.
func (s *Store) TUITheme() (string, bool, error) {
	value, ok, err := s.setting(tuiThemeKey)
	if err != nil || !ok {
		return "", ok, err
	}
	switch value {
	case "auto", "dark", "light":
		return value, true, nil
	default:
		return "", false, fmt.Errorf("invalid saved TUI theme %q", value)
	}
}

// SaveTUITheme persists the TUI theme preference.
func (s *Store) SaveTUITheme(mode string) error {
	switch mode {
	case "auto", "dark", "light":
		return s.saveSetting(tuiThemeKey, mode)
	default:
		return fmt.Errorf("invalid TUI theme %q", mode)
	}
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
