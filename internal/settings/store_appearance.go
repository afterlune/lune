package settings

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (s *Store) Appearance() (Appearance, error) {
	var encoded string
	err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = 'appearance'`).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultAppearance(defaultTheme), nil
	}
	if err != nil {
		return Appearance{}, fmt.Errorf("read appearance setting: %w", err)
	}
	var appearance Appearance
	if err := json.Unmarshal([]byte(encoded), &appearance); err != nil {
		return Appearance{}, fmt.Errorf("decode appearance setting: %w", err)
	}
	if appearance.Theme == "" || !supportedTheme(appearance.Theme) {
		appearance.Theme = defaultTheme
	}
	if appearance.WallpaperType == "" {
		appearance.WallpaperType = "static"
	}
	if appearance.PanelOpacity == 0 {
		appearance.PanelOpacity = defaultPanelOpacity
	}
	if err := validateAppearance(appearance); err != nil {
		return Appearance{}, fmt.Errorf("invalid saved appearance setting: %w", err)
	}
	return appearance, nil
}

func (s *Store) SaveAppearance(appearance Appearance) error {
	if err := validateAppearance(appearance); err != nil {
		return err
	}
	encoded, err := json.Marshal(appearance)
	if err != nil {
		return fmt.Errorf("encode appearance setting: %w", err)
	}
	if _, err := s.db.Exec(`INSERT INTO app_settings(key, value) VALUES ('appearance', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, string(encoded)); err != nil {
		return fmt.Errorf("save appearance setting: %w", err)
	}
	return nil
}

func (s *Store) Theme() (string, error) {
	appearance, err := s.Appearance()
	if err != nil {
		return "", err
	}
	return appearance.Theme, nil
}

func (s *Store) SetTheme(theme string) error {
	if !supportedTheme(theme) {
		return fmt.Errorf("unsupported theme %q", theme)
	}
	appearance, err := s.Appearance()
	if err != nil {
		return err
	}
	appearance.Theme = theme
	return s.SaveAppearance(appearance)
}

func defaultAppearance(theme string) Appearance {
	return Appearance{Theme: theme, WallpaperType: "static", PanelOpacity: defaultPanelOpacity}
}

func supportedTheme(theme string) bool {
	return theme == "system" || theme == "light" || theme == "dark" || theme == "anime"
}

func validateAppearance(appearance Appearance) error {
	if !supportedTheme(appearance.Theme) {
		return fmt.Errorf("unsupported theme %q", appearance.Theme)
	}
	if appearance.WallpaperType != "static" && appearance.WallpaperType != "dynamic" {
		return fmt.Errorf("unsupported wallpaper type %q", appearance.WallpaperType)
	}
	if appearance.PanelOpacity < 60 || appearance.PanelOpacity > 95 {
		return fmt.Errorf("panel opacity must be between 60 and 95")
	}
	if strings.ContainsAny(appearance.WallpaperFile, `/\\`) || appearance.WallpaperFile == "." || appearance.WallpaperFile == ".." {
		return fmt.Errorf("wallpaper file must be a file name")
	}
	if strings.ContainsAny(appearance.WallpaperName, `/\\`) {
		return fmt.Errorf("wallpaper name must be a file name")
	}
	return nil
}
