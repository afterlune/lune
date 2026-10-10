package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
)

const (
	ProviderDeepSeek          = "deepseek"
	ProviderAnthropic         = "anthropic"
	ProtocolResponses         = "responses"
	ProtocolAnthropicMessages = "anthropic-messages"
	DeepSeekResponsesBaseURL  = "https://api.deepseek.com"
	DefaultMaxParallelAgents  = 4
	ConfigFileName            = "config.json"
)

// ProviderConfig selects an API format independently from the provider name.
// Credentials are never included in logs or model profiles.
type ProviderConfig struct {
	Protocol string `json:"protocol"`
	BaseURL  string `json:"baseURL"` // API root, or the full endpoint path for this protocol.
	APIKey   string `json:"apiKey"`
}

// ModelProfile binds a provider model ID to a stable catalog name. Agent runs
// receive a profile at start and keep it for their lifetime.
type ModelProfile struct {
	Provider        string `json:"provider"`
	Model           string `json:"model"`
	MaxOutputTokens int64  `json:"maxOutputTokens,omitempty"`
}

// Config is the validated model catalog loaded from config.json.
type Config struct {
	Providers         map[string]ProviderConfig `json:"providers"`
	Models            map[string]ModelProfile   `json:"models"`
	DefaultModel      string                    `json:"defaultModel"`
	MaxParallelAgents int                       `json:"maxParallelAgents,omitempty"`

	legacyAPIKey string
}

func LoadConfig(path string) (Config, error) {
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
		config.Providers = map[string]ProviderConfig{ProviderDeepSeek: {
			Protocol: ProtocolResponses, BaseURL: DeepSeekResponsesBaseURL, APIKey: config.legacyAPIKey,
		}}
		config.Models = map[string]ModelProfile{
			DeepSeekFlashModel: {Provider: ProviderDeepSeek, Model: DeepSeekFlashModel},
		}
		config.DefaultModel = DeepSeekFlashModel
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate %s: %w", path, err)
	}
	return config, nil
}

// UnmarshalJSON accepts the old single-provider apikey file as a one-time
// migration input while rejecting all other unknown fields.
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
		case "providers", "models", "defaultModel", "maxParallelAgents":
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

func (c Config) Validate() error {
	if len(c.Providers) == 0 {
		return errors.New("at least one provider must be configured")
	}
	if len(c.Models) == 0 {
		return errors.New("at least one model profile must be configured")
	}
	if strings.TrimSpace(c.DefaultModel) == "" {
		return errors.New("defaultModel is required")
	}
	if _, ok := c.Models[c.DefaultModel]; !ok {
		return fmt.Errorf("defaultModel %q does not name a configured model profile", c.DefaultModel)
	}
	if c.MaxParallelAgents < 0 {
		return errors.New("maxParallelAgents must not be negative")
	}
	for name, provider := range c.Providers {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name {
			return errors.New("provider names must be non-empty and unpadded")
		}
		if provider.Protocol != ProtocolResponses && provider.Protocol != ProtocolAnthropicMessages {
			return fmt.Errorf("provider %q has unsupported protocol %q", name, provider.Protocol)
		}
		if err := validateBaseURL(provider.BaseURL); err != nil {
			return fmt.Errorf("provider %q: %w", name, err)
		}
	}
	for name, profile := range c.Models {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(name) != name {
			return errors.New("model profile names must be non-empty and unpadded")
		}
		if strings.TrimSpace(profile.Model) == "" {
			return fmt.Errorf("model profile %q requires a model ID", name)
		}
		provider, ok := c.Providers[profile.Provider]
		if !ok {
			return fmt.Errorf("model profile %q references unconfigured provider %q", name, profile.Provider)
		}
		if strings.TrimSpace(provider.APIKey) == "" {
			return fmt.Errorf("provider %q requires a non-empty apiKey", profile.Provider)
		}
		if profile.MaxOutputTokens < 0 {
			return fmt.Errorf("model profile %q maxOutputTokens must not be negative", name)
		}
		if provider.Protocol == ProtocolAnthropicMessages && profile.MaxOutputTokens == 0 {
			return fmt.Errorf("model profile %q using %s requires maxOutputTokens", name, ProtocolAnthropicMessages)
		}
	}
	return nil
}

func validateBaseURL(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("baseURL must be an http or https URL without query or fragment")
	}
	if strings.TrimSpace(value) != value {
		return errors.New("baseURL must not contain surrounding whitespace")
	}
	return nil
}

func (c Config) ParallelLimit() int {
	if c.MaxParallelAgents == 0 {
		return DefaultMaxParallelAgents
	}
	return c.MaxParallelAgents
}
