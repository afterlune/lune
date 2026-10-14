package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type themeName string

const (
	themeAuto  themeName = "auto"
	themeDark  themeName = "dark"
	themeLight themeName = "light"
)

type uiPreferences struct {
	Theme  themeName         `json:"theme"`
	Keymap map[string]string `json:"keymap"`
}

func defaultUIPreferences() uiPreferences {
	return uiPreferences{Theme: themeAuto, Keymap: defaultKeymap()}
}

func userPreferencesPath() (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "luneagent", "ui.json"), nil
}

func loadUserPreferences() (uiPreferences, string, error) {
	path, err := userPreferencesPath()
	if err != nil {
		return defaultUIPreferences(), "", err
	}
	prefs, err := loadUIPreferences(path)
	return prefs, path, err
}

func loadUIPreferences(path string) (uiPreferences, error) {
	prefs := defaultUIPreferences()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return prefs, nil
	}
	if err != nil {
		return prefs, err
	}
	var stored uiPreferences
	if err := json.Unmarshal(data, &stored); err != nil {
		return prefs, err
	}
	if stored.Theme == themeAuto || stored.Theme == themeDark || stored.Theme == themeLight {
		prefs.Theme = stored.Theme
	}
	if stored.Keymap != nil {
		bindings, err := normalizeKeymap(stored.Keymap)
		if err != nil {
			return prefs, err
		}
		prefs.Keymap = bindings
	}
	return prefs, nil
}

func saveUIPreferences(path string, prefs uiPreferences) error {
	if path == "" {
		return errors.New("user preferences path is unavailable")
	}
	if prefs.Theme != themeAuto && prefs.Theme != themeDark && prefs.Theme != themeLight {
		prefs.Theme = themeAuto
	}
	if prefs.Keymap == nil {
		prefs.Keymap = defaultKeymap()
	} else {
		bindings, err := normalizeKeymap(prefs.Keymap)
		if err != nil {
			return err
		}
		prefs.Keymap = bindings
	}
	data, err := json.MarshalIndent(prefs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
