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
	Theme                themeName         `json:"theme"`
	Keymap               map[string]string `json:"keymap"`
	CopyOnSelect         string            `json:"copy_on_select"`
	ClearSelectionOnCopy bool              `json:"clear_selection_on_copy"`
}

func defaultUIPreferences() uiPreferences {
	return uiPreferences{Theme: themeAuto, Keymap: defaultKeymap(), CopyOnSelect: "auto"}
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
		storedKeymap := cloneKeymap(stored.Keymap)
		if storedKeymap["input_newline"] == "ctrl+o" {
			storedKeymap["input_newline"] = "shift+enter"
		}
		bindings, err := normalizeKeymap(storedKeymap)
		if err != nil {
			return prefs, err
		}
		prefs.Keymap = bindings
	}
	if stored.CopyOnSelect == "auto" || stored.CopyOnSelect == "always" || stored.CopyOnSelect == "never" {
		prefs.CopyOnSelect = stored.CopyOnSelect
	}
	prefs.ClearSelectionOnCopy = stored.ClearSelectionOnCopy
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
	if prefs.CopyOnSelect != "auto" && prefs.CopyOnSelect != "always" && prefs.CopyOnSelect != "never" {
		prefs.CopyOnSelect = "auto"
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
