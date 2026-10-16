package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	publicmodel "github.com/afterlune/luneCore/model"
)

type MigrationItem struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Existing string `json:"existing,omitempty"`
	Incoming string `json:"incoming"`
}

type MigrationPreview struct {
	Workspace string          `json:"workspace"`
	Providers []string        `json:"providers"`
	Models    []string        `json:"models"`
	Default   string          `json:"defaultModel"`
	Conflicts []MigrationItem `json:"conflicts"`
}

type legacyModelSettings struct {
	Providers         map[string]publicmodel.ProviderConfig `json:"providers"`
	Models            map[string]publicmodel.ModelProfile   `json:"models"`
	DefaultModel      string                                `json:"defaultModel"`
	MaxParallelAgents int                                   `json:"maxParallelAgents"`
	LegacyAPIKey      string                                `json:"apikey"`
}

func readLegacyModelSettings(workspace string) (string, []byte, map[string]json.RawMessage, publicmodel.Config, bool, error) {
	absolute, err := filepath.Abs(workspace)
	if err != nil {
		return "", nil, nil, publicmodel.Config{}, false, fmt.Errorf("resolve workspace: %w", err)
	}
	path := filepath.Join(absolute, "config.json")
	original, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return path, nil, nil, publicmodel.Config{}, false, nil
	}
	if err != nil {
		return "", nil, nil, publicmodel.Config{}, false, fmt.Errorf("read project config: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(original, &fields); err != nil {
		return "", nil, nil, publicmodel.Config{}, false, fmt.Errorf("decode project config: %w", err)
	}
	if fields == nil {
		return "", nil, nil, publicmodel.Config{}, false, errors.New("project config must be a JSON object")
	}
	legacy := legacyModelSettings{}
	if err := json.Unmarshal(original, &legacy); err != nil {
		return "", nil, nil, publicmodel.Config{}, false, fmt.Errorf("decode project model settings: %w", err)
	}
	if legacy.LegacyAPIKey != "" && len(legacy.Providers) == 0 && len(legacy.Models) == 0 {
		legacy.Providers = map[string]publicmodel.ProviderConfig{
			publicmodel.ProviderDeepSeek: {Protocol: publicmodel.ProtocolResponses, BaseURL: publicmodel.DeepSeekResponsesBaseURL, APIKey: legacy.LegacyAPIKey},
		}
		legacy.Models = map[string]publicmodel.ModelProfile{"deepseek-flash": {Provider: publicmodel.ProviderDeepSeek, Model: "deepseek-flash"}}
		legacy.DefaultModel = "deepseek-flash"
	}
	hasLegacy := len(legacy.Providers) > 0 || len(legacy.Models) > 0 || legacy.DefaultModel != "" || legacy.MaxParallelAgents != 0 || legacy.LegacyAPIKey != ""
	if !hasLegacy {
		return path, original, fields, publicmodel.Config{}, false, nil
	}
	legacyConfig := publicmodel.Config{
		Providers: legacy.Providers, Models: legacy.Models, DefaultModel: legacy.DefaultModel,
		MaxParallelAgents: legacy.MaxParallelAgents,
	}
	if legacyConfig.Providers == nil {
		legacyConfig.Providers = map[string]publicmodel.ProviderConfig{}
	}
	if legacyConfig.Models == nil {
		legacyConfig.Models = map[string]publicmodel.ModelProfile{}
	}
	if err := legacyConfig.Validate(); err != nil {
		return "", nil, nil, publicmodel.Config{}, false, fmt.Errorf("validate project model settings: %w", err)
	}
	for name, provider := range legacyConfig.Providers {
		if strings.TrimSpace(provider.APIKey) == "" {
			return "", nil, nil, publicmodel.Config{}, false, fmt.Errorf("project provider %q has no API key", name)
		}
	}
	return path, original, fields, legacyConfig, true, nil
}

func previewMigration(workspace string, current, incoming publicmodel.Config) (*MigrationPreview, error) {
	path, _, _, _, hasLegacy, err := readLegacyModelSettings(workspace)
	if err != nil {
		return nil, err
	}
	if !hasLegacy {
		return nil, nil
	}
	absolute, _ := filepath.Abs(workspace)
	preview := &MigrationPreview{Workspace: absolute}
	for name, provider := range incoming.Providers {
		preview.Providers = append(preview.Providers, name)
		if existing, found := current.Providers[name]; found {
			preview.Conflicts = append(preview.Conflicts, MigrationItem{
				Kind: "provider", Name: name,
				Existing: fmt.Sprintf("%s · %s", existing.Protocol, existing.BaseURL),
				Incoming: fmt.Sprintf("%s · %s", provider.Protocol, provider.BaseURL),
			})
		}
	}
	for name, model := range incoming.Models {
		preview.Models = append(preview.Models, name)
		if existing, found := current.Models[name]; found {
			preview.Conflicts = append(preview.Conflicts, MigrationItem{
				Kind: "model", Name: name,
				Existing: fmt.Sprintf("%s · %s", existing.Provider, existing.Model),
				Incoming: fmt.Sprintf("%s · %s", model.Provider, model.Model),
			})
		}
	}
	if incoming.DefaultModel != "" {
		preview.Default = incoming.DefaultModel
		if current.DefaultModel != "" && current.DefaultModel != incoming.DefaultModel {
			preview.Conflicts = append(preview.Conflicts, MigrationItem{
				Kind: "defaultModel", Name: "defaultModel", Existing: current.DefaultModel, Incoming: incoming.DefaultModel,
			})
		}
	}
	for _, names := range [][]string{preview.Providers, preview.Models} {
		sort.Strings(names)
	}
	sort.Slice(preview.Conflicts, func(i, j int) bool {
		if preview.Conflicts[i].Kind != preview.Conflicts[j].Kind {
			return preview.Conflicts[i].Kind < preview.Conflicts[j].Kind
		}
		return preview.Conflicts[i].Name < preview.Conflicts[j].Name
	})
	_ = path
	return preview, nil
}

func mergeLegacyModelSettings(current, incoming publicmodel.Config, choices map[string]string) (publicmodel.Config, error) {
	merged := publicmodel.Config{
		Providers:    make(map[string]publicmodel.ProviderConfig, len(current.Providers)+len(incoming.Providers)),
		Models:       make(map[string]publicmodel.ModelProfile, len(current.Models)+len(incoming.Models)),
		DefaultModel: current.DefaultModel, MaxParallelAgents: current.MaxParallelAgents,
	}
	for key, value := range current.Providers {
		merged.Providers[key] = value
	}
	for key, value := range current.Models {
		merged.Models[key] = value
	}
	for name, provider := range incoming.Providers {
		if _, exists := current.Providers[name]; !exists || choices["provider:"+name] == "replace" {
			merged.Providers[name] = provider
		}
	}
	for name, profile := range incoming.Models {
		if _, exists := current.Models[name]; !exists || choices["model:"+name] == "replace" {
			merged.Models[name] = profile
		}
	}
	if current.DefaultModel == "" || choices["defaultModel:defaultModel"] == "replace" {
		merged.DefaultModel = incoming.DefaultModel
	}
	if merged.MaxParallelAgents == 0 {
		merged.MaxParallelAgents = incoming.MaxParallelAgents
	}
	if err := merged.Validate(); err != nil {
		return publicmodel.Config{}, fmt.Errorf("merged model settings are invalid: %w", err)
	}
	for name, provider := range merged.Providers {
		if strings.TrimSpace(provider.APIKey) == "" {
			return publicmodel.Config{}, fmt.Errorf("provider %q has no API key", name)
		}
	}
	return merged, nil
}

func projectConfigWithoutModels(original []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(original, &fields); err != nil {
		return nil, fmt.Errorf("decode project config: %w", err)
	}
	for _, key := range []string{"providers", "models", "defaultModel", "maxParallelAgents", "apikey"} {
		delete(fields, key)
	}
	if len(fields) == 0 {
		return nil, nil
	}
	updated, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode project config: %w", err)
	}
	return append(updated, '\n'), nil
}

func writeAtomic(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create config replacement: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write config replacement: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync config replacement: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close config replacement: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace project config: %w", err)
	}
	return nil
}

func migrateProjectModelSettings(workspace string, current publicmodel.Config, choices map[string]string, save func(publicmodel.Config, func() error, func() error) error) error {
	path, original, _, incoming, hasLegacy, err := readLegacyModelSettings(workspace)
	if err != nil {
		return err
	}
	if !hasLegacy {
		return errors.New("project has no model settings to migrate")
	}
	merged, err := mergeLegacyModelSettings(current, incoming, choices)
	if err != nil {
		return err
	}
	updated, err := projectConfigWithoutModels(original)
	if err != nil {
		return err
	}
	apply := func() error {
		if updated == nil {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove migrated project config: %w", err)
			}
			return nil
		}
		return writeAtomic(path, updated)
	}
	return save(merged, apply, func() error { return writeAtomic(path, original) })
}

func restoreOriginalConfig(path string, original []byte) error {
	if len(original) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove newly created project config: %w", err)
		}
		return nil
	}
	return writeAtomic(path, bytes.Clone(original))
}
