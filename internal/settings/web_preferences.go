package settings

import (
	"encoding/json"
	"errors"
)

type UIPreferences struct {
	Theme                   string `json:"theme"`
	Language                string `json:"language"`
	BodyFontSize            int    `json:"bodyFontSize"`
	CodeFontSize            int    `json:"codeFontSize"`
	RunningEnter            string `json:"runningEnter"`
	TimelineDetailsExpanded bool   `json:"timelineDetailsExpanded"`
	UsageDensity            string `json:"usageDensity"`
}

func DefaultUIPreferences() UIPreferences {
	return UIPreferences{
		Theme: "system", Language: "en", BodyFontSize: 14, CodeFontSize: 12,
		RunningEnter: "queue", TimelineDetailsExpanded: false, UsageDensity: "comfortable",
	}
}

func (p UIPreferences) Validate() error {
	if p.Theme != "system" && p.Theme != "light" && p.Theme != "dark" {
		return errors.New("invalid appearance preference")
	}
	if p.Language != "en" && p.Language != "zh" {
		return errors.New("invalid interface language")
	}
	if p.BodyFontSize < 12 || p.BodyFontSize > 18 || p.CodeFontSize < 10 || p.CodeFontSize > 16 {
		return errors.New("text size is outside the supported range")
	}
	if p.RunningEnter != "queue" && p.RunningEnter != "steer" {
		return errors.New("invalid Enter behavior")
	}
	if p.UsageDensity != "comfortable" && p.UsageDensity != "compact" {
		return errors.New("invalid usage display density")
	}
	return nil
}

func (s *Store) WebUIPreferences() (UIPreferences, error) {
	prefs := DefaultUIPreferences()
	raw, found, err := s.setting("web.preferences")
	if err != nil {
		return prefs, err
	}
	fields := map[string]json.RawMessage{}
	if found {
		if err := json.Unmarshal([]byte(raw), &fields); err != nil {
			return prefs, errors.New("saved interface preferences are invalid")
		}
		if err := json.Unmarshal([]byte(raw), &prefs); err != nil {
			return prefs, errors.New("saved interface preferences are invalid")
		}
	}
	if _, ok := fields["theme"]; !ok {
		if value, err := s.WebTheme(); err == nil && value != "" {
			prefs.Theme = value
		}
	}
	if _, ok := fields["language"]; !ok {
		if value, err := s.WebLanguage(); err == nil && value != "" {
			prefs.Language = value
		}
	}
	return prefs, prefs.Validate()
}

func (s *Store) SaveWebUIPreferences(prefs UIPreferences) error {
	if err := prefs.Validate(); err != nil {
		return err
	}
	encoded, err := json.Marshal(prefs)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range map[string]string{
		"web.preferences": string(encoded), webThemeKey: prefs.Theme, webLanguageKey: prefs.Language,
	} {
		if _, err := tx.Exec(`INSERT INTO app_settings(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}
