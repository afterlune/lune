package tui

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

type themeName string
type animeLayout string

const (
	themeDark  themeName = "dark"
	themeLight themeName = "light"
	themeAnime themeName = "anime"

	layoutFocus  animeLayout = "focus"
	layoutStudio animeLayout = "studio"
)

type uiPreferences struct {
	Theme       themeName   `json:"theme"`
	AnimeLayout animeLayout `json:"anime_layout"`
}

func defaultUIPreferences() uiPreferences {
	return uiPreferences{Theme: themeDark, AnimeLayout: layoutFocus}
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
	if stored.Theme == themeDark || stored.Theme == themeLight || stored.Theme == themeAnime {
		prefs.Theme = stored.Theme
	}
	if stored.AnimeLayout == layoutFocus || stored.AnimeLayout == layoutStudio {
		prefs.AnimeLayout = stored.AnimeLayout
	}
	return prefs, nil
}

func saveUIPreferences(path string, prefs uiPreferences) error {
	if path == "" {
		return errors.New("user preferences path is unavailable")
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
