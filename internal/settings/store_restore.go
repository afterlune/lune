package settings

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func (s *Store) DesktopRestore() (DesktopRestore, error) {
	var restore DesktopRestore
	workspace, found, err := s.setting(desktopLastWorkspaceKey)
	if err != nil {
		return DesktopRestore{}, err
	}
	if found {
		restore.Workspace = workspace
	}
	sessionID, found, err := s.setting(desktopLastSessionKey)
	if err != nil {
		return DesktopRestore{}, err
	}
	if found {
		restore.SessionID = sessionID
	}
	width, found, err := s.setting(desktopWindowWidthKey)
	if err != nil {
		return DesktopRestore{}, err
	}
	if found {
		restore.WindowWidth, _ = strconv.Atoi(width)
	}
	height, found, err := s.setting(desktopWindowHeightKey)
	if err != nil {
		return DesktopRestore{}, err
	}
	if found {
		restore.WindowHeight, _ = strconv.Atoi(height)
	}
	return restore, nil
}

func (s *Store) SaveLastWorkspace(workspace string) error {
	return s.saveSetting(desktopLastWorkspaceKey, strings.TrimSpace(workspace))
}

func (s *Store) SaveLastSession(sessionID string) error {
	return s.saveSetting(desktopLastSessionKey, strings.TrimSpace(sessionID))
}

func (s *Store) SaveWindowSize(width, height int) error {
	if width <= 0 || height <= 0 {
		return errors.New("window size must be positive")
	}
	if err := s.saveSetting(desktopWindowWidthKey, strconv.Itoa(width)); err != nil {
		return err
	}
	return s.saveSetting(desktopWindowHeightKey, strconv.Itoa(height))
}

func (s *Store) ClearDesktopLocation() error {
	_, err := s.db.Exec(`DELETE FROM app_settings WHERE key IN (?, ?)`,
		desktopLastWorkspaceKey, desktopLastSessionKey)
	if err != nil {
		return fmt.Errorf("clear desktop location state: %w", err)
	}
	return nil
}

func (s *Store) ClearDesktopRestore() error {
	_, err := s.db.Exec(`DELETE FROM app_settings WHERE key IN (?, ?, ?, ?)`,
		desktopLastWorkspaceKey, desktopLastSessionKey, desktopWindowWidthKey, desktopWindowHeightKey)
	if err != nil {
		return fmt.Errorf("clear desktop restore state: %w", err)
	}
	return nil
}

// TUITheme returns the saved TUI theme mode. The boolean is false when no
// preference has been saved; Desktop appearance settings use a separate key.
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

// SaveTUITheme persists the TUI-only theme preference independently of Desktop.
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
