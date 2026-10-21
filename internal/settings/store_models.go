package settings

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	publicmodel "github.com/afterlune/luneCore/model"
)

func (s *Store) LoadModelSettings() (SharedModelSettings, error) {
	if s == nil || s.db == nil {
		return SharedModelSettings{}, errors.New("settings store is closed")
	}
	settings := SharedModelSettings{Catalog: publicmodel.Config{
		Providers: map[string]publicmodel.ProviderConfig{}, Profiles: map[string]publicmodel.ModelProfile{},
	}}
	config := &settings.Catalog
	rows, err := s.db.Query(`SELECT name, protocol, base_url, encrypted_api_key, server_compaction FROM providers ORDER BY name`)
	if err != nil {
		return SharedModelSettings{}, fmt.Errorf("read providers: %w", err)
	}
	for rows.Next() {
		var name, protocol, baseURL string
		var encrypted []byte
		var serverCompaction bool
		if err := rows.Scan(&name, &protocol, &baseURL, &encrypted, &serverCompaction); err != nil {
			_ = rows.Close()
			return SharedModelSettings{}, fmt.Errorf("read provider: %w", err)
		}
		apiKey, err := unprotectSecret(encrypted)
		if err != nil {
			_ = rows.Close()
			return SharedModelSettings{}, fmt.Errorf("decrypt API key for provider %q: %w", name, err)
		}
		config.Providers[name] = publicmodel.ProviderConfig{
			Protocol: protocol, BaseURL: baseURL, APIKey: apiKey, ServerCompaction: serverCompaction,
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return SharedModelSettings{}, fmt.Errorf("read providers: %w", err)
	}
	if err := rows.Close(); err != nil {
		return SharedModelSettings{}, fmt.Errorf("close provider rows: %w", err)
	}
	modelRows, err := s.db.Query(`SELECT name, provider, model, max_output_tokens, supports_input_audio FROM models ORDER BY name`)
	if err != nil {
		return SharedModelSettings{}, fmt.Errorf("read model profiles: %w", err)
	}
	for modelRows.Next() {
		var name, provider, modelID string
		var maxOutputTokens int64
		var supportsInputAudio bool
		if err := modelRows.Scan(&name, &provider, &modelID, &maxOutputTokens, &supportsInputAudio); err != nil {
			_ = modelRows.Close()
			return SharedModelSettings{}, fmt.Errorf("read model profile: %w", err)
		}
		config.Profiles[name] = publicmodel.ModelProfile{
			Provider: provider, Model: modelID, MaxOutputTokens: maxOutputTokens, SupportsInputAudio: supportsInputAudio,
		}
	}
	if err := modelRows.Err(); err != nil {
		_ = modelRows.Close()
		return SharedModelSettings{}, fmt.Errorf("read model profiles: %w", err)
	}
	if err := modelRows.Close(); err != nil {
		return SharedModelSettings{}, fmt.Errorf("close model rows: %w", err)
	}
	if err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = 'defaultModel'`).Scan(&config.DefaultProfile); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SharedModelSettings{}, fmt.Errorf("read default model: %w", err)
	}
	var maxParallel string
	if err := s.db.QueryRow(`SELECT value FROM app_settings WHERE key = 'maxParallelAgents'`).Scan(&maxParallel); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SharedModelSettings{}, fmt.Errorf("read parallel agent setting: %w", err)
	}
	if maxParallel != "" {
		if _, err := fmt.Sscan(maxParallel, &settings.MaxParallelAgents); err != nil {
			return SharedModelSettings{}, fmt.Errorf("decode parallel agent setting: %w", err)
		}
	}
	if len(config.Providers) == 0 && len(config.Profiles) == 0 {
		return settings, nil
	}
	if err := config.Validate(); err != nil {
		return SharedModelSettings{}, fmt.Errorf("validate shared model settings: %w", err)
	}
	for name, provider := range config.Providers {
		if strings.TrimSpace(provider.APIKey) == "" {
			return SharedModelSettings{}, fmt.Errorf("provider %q has no API key", name)
		}
	}
	return settings, nil
}

func (s *Store) SaveModelSettings(settings SharedModelSettings) error {
	if s == nil || s.db == nil {
		return errors.New("settings store is closed")
	}
	config := settings.Catalog
	if err := config.Validate(); err != nil {
		return fmt.Errorf("validate model settings: %w", err)
	}
	if settings.MaxParallelAgents < 0 {
		return errors.New("maxParallelAgents must not be negative")
	}
	for name, provider := range config.Providers {
		if strings.TrimSpace(provider.APIKey) == "" {
			return fmt.Errorf("provider %q requires an API key", name)
		}
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return fmt.Errorf("begin settings transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM providers`); err != nil {
		return fmt.Errorf("replace providers: %w", err)
	}
	for name, provider := range config.Providers {
		secret, err := protectSecret(provider.APIKey)
		if err != nil {
			return fmt.Errorf("encrypt API key for provider %q: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO providers(name, protocol, base_url, encrypted_api_key, server_compaction) VALUES (?, ?, ?, ?, ?)`,
			name, provider.Protocol, provider.BaseURL, secret, provider.ServerCompaction); err != nil {
			return fmt.Errorf("save provider %q: %w", name, err)
		}
	}
	if _, err := tx.Exec(`DELETE FROM models`); err != nil {
		return fmt.Errorf("replace model profiles: %w", err)
	}
	for name, profile := range config.Profiles {
		if _, err := tx.Exec(`INSERT INTO models(name, provider, model, max_output_tokens, supports_input_audio) VALUES (?, ?, ?, ?, ?)`,
			name, profile.Provider, profile.Model, profile.MaxOutputTokens, profile.SupportsInputAudio); err != nil {
			return fmt.Errorf("save model profile %q: %w", name, err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO app_settings(key, value) VALUES ('defaultModel', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, config.DefaultProfile); err != nil {
		return fmt.Errorf("save default model: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO app_settings(key, value) VALUES ('maxParallelAgents', ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, fmt.Sprint(settings.MaxParallelAgents)); err != nil {
		return fmt.Errorf("save parallel agent setting: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit shared model settings: %w", err)
	}
	return nil
}
