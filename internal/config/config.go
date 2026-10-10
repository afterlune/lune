// Package config loads and validates the workspace's project configuration.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/afterlune/lune/internal/model"
)

const (
	FileName                    = "config.json"
	defaultAutoCompactThreshold = 0.8
)

// Config is the project configuration. Its JSON shape stays flat so existing
// provider and model settings remain unchanged.
type Config struct {
	Providers            map[string]model.ProviderConfig `json:"providers"`
	Models               map[string]model.ModelProfile   `json:"models"`
	DefaultModel         string                          `json:"defaultModel"`
	MaxParallelAgents    int                             `json:"maxParallelAgents,omitempty"`
	ContextWindow        int64                           `json:"contextWindow,omitempty"`        // Tokens; zero means unset.
	AutoCompact          bool                            `json:"autoCompact,omitempty"`          // Defaults to false.
	AutoCompactThreshold float64                         `json:"autoCompactThreshold,omitempty"` // Ratio from 0 to 1; defaults to 0.8 when enabled.
	ReserveOutputTokens  int64                           `json:"reserveOutputTokens,omitempty"`  // Tokens; zero means unset.
	Attachments          AttachmentLimits                `json:"attachments,omitempty"`
	legacyAPIKey         string
}

// AttachmentLimits configures future attachment ingestion limits. A zero
// value means the project has not set an override.
type AttachmentLimits struct {
	MaxFiles      int   `json:"maxFiles,omitempty"`
	MaxFileBytes  int64 `json:"maxFileBytes,omitempty"`
	MaxTotalBytes int64 `json:"maxTotalBytes,omitempty"`
}

// Load reads and validates one project config file.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	var config Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Config{}, fmt.Errorf("decode %s: expected one JSON object", path)
	}
	if config.legacyAPIKey != "" {
		if len(config.Providers) != 0 || len(config.Models) != 0 || config.DefaultModel != "" {
			return Config{}, fmt.Errorf("%s: legacy apikey cannot be combined with providers or models", path)
		}
		config.Providers = map[string]model.ProviderConfig{model.ProviderDeepSeek: {
			Protocol: model.ProtocolResponses, BaseURL: model.DeepSeekResponsesBaseURL, APIKey: config.legacyAPIKey,
		}}
		config.Models = map[string]model.ModelProfile{
			model.DeepSeekFlashModel: {Provider: model.ProviderDeepSeek, Model: model.DeepSeekFlashModel},
		}
		config.DefaultModel = model.DeepSeekFlashModel
	}
	config.applyDefaults()
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return config, nil
}

// UnmarshalJSON accepts the old single-provider apikey input and rejects
// unknown fields at both the project and nested setting levels.
func (c *Config) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	legacyAPIKey := ""
	if raw, exists := fields["apikey"]; exists {
		if err := json.Unmarshal(raw, &legacyAPIKey); err != nil {
			return fmt.Errorf("apikey must be a string: %w", err)
		}
		delete(fields, "apikey")
	}
	for key := range fields {
		switch key {
		case "providers", "models", "defaultModel", "maxParallelAgents", "contextWindow", "autoCompact", "autoCompactThreshold", "reserveOutputTokens", "attachments":
		default:
			return fmt.Errorf("unknown config field %q", key)
		}
	}
	cleaned, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	type plain Config
	var decoded plain
	decoder := json.NewDecoder(bytes.NewReader(cleaned))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*c = Config(decoded)
	c.legacyAPIKey = legacyAPIKey
	return nil
}

// Validate checks project settings and the model catalog.
func (c *Config) applyDefaults() {
	if c.AutoCompact && c.AutoCompactThreshold == 0 {
		c.AutoCompactThreshold = defaultAutoCompactThreshold
	}
}

func (c Config) Validate() error {
	if err := c.ModelConfig().Validate(); err != nil {
		return err
	}
	if c.ContextWindow < 0 {
		return errors.New("contextWindow must not be negative")
	}
	if c.ReserveOutputTokens < 0 {
		return errors.New("reserveOutputTokens must not be negative")
	}
	if c.ContextWindow > 0 && c.ReserveOutputTokens >= c.ContextWindow {
		return errors.New("reserveOutputTokens must be smaller than contextWindow")
	}
	if c.AutoCompactThreshold < 0 || c.AutoCompactThreshold > 1 {
		return errors.New("autoCompactThreshold must be between 0 and 1; 0 means unset")
	}
	if c.Attachments.MaxFiles < 0 {
		return errors.New("attachments.maxFiles must not be negative")
	}
	if c.Attachments.MaxFileBytes < 0 {
		return errors.New("attachments.maxFileBytes must not be negative")
	}
	if c.Attachments.MaxTotalBytes < 0 {
		return errors.New("attachments.maxTotalBytes must not be negative")
	}
	return nil
}

// ModelConfig returns the model catalog portion consumed by model providers.
func (c Config) ModelConfig() model.Config {
	return model.Config{
		Providers:         c.Providers,
		Models:            c.Models,
		DefaultModel:      c.DefaultModel,
		MaxParallelAgents: c.MaxParallelAgents,
	}
}
