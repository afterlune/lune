package model

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeModelConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ConfigFileName)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfigValidatesCatalogAndDefaultsParallelLimit(t *testing.T) {
	path := writeModelConfig(t, `{
		"providers":{
			"vendor-a":{"protocol":"responses","baseURL":"https://responses.example/v1","apiKey":" ds-key "},
			"vendor-b":{"protocol":"anthropic-messages","baseURL":"https://messages.example","apiKey":"anthropic-key"}
		},
		"models":{
			"lead":{"provider":"vendor-a","model":"model-a"},
			"vision-worker":{"provider":"vendor-b","model":"model-b","maxOutputTokens":2048}
		},
		"defaultModel":"lead"
	}`)
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if config.ParallelLimit() != DefaultMaxParallelAgents {
		t.Fatalf("parallel limit = %d, want %d", config.ParallelLimit(), DefaultMaxParallelAgents)
	}
	if config.Providers["vendor-a"].APIKey != " ds-key " {
		t.Fatal("LoadConfig should preserve the key value; providers trim it when constructing clients")
	}
}

func TestLoadConfigMigratesLegacyAPIKey(t *testing.T) {
	path := writeModelConfig(t, `{"apikey":"old-deepseek-key"}`)
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig legacy config: %v", err)
	}
	if config.DefaultModel != DeepSeekFlashModel || config.Providers[ProviderDeepSeek].APIKey != "old-deepseek-key" {
		t.Fatalf("legacy config migration = %#v", config)
	}
	if profile, ok := config.Models[DeepSeekFlashModel]; !ok || profile.Provider != ProviderDeepSeek || profile.Model != DeepSeekFlashModel {
		t.Fatalf("legacy model profile = %#v, exists=%v", profile, ok)
	}
}

func TestLoadConfigRejectsUnknownAndInvalidFields(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{"top-level unknown", `{"providers":{},"models":{},"defaultModel":"x","mispelled":true}`, "unknown config field"},
		{"nested unknown", `{"providers":{"deepseek":{"protocol":"responses","baseURL":"https://example.com","apiKey":"key","endpoint":"x"}},"models":{"lead":{"provider":"deepseek","model":"m"}},"defaultModel":"lead"}`, "unknown field"},
		{"missing provider key", `{"providers":{"deepseek":{"protocol":"responses","baseURL":"https://example.com","apiKey":" "}},"models":{"lead":{"provider":"deepseek","model":"m"}},"defaultModel":"lead"}`, "non-empty apiKey"},
		{"missing messages output limit", `{"providers":{"anthropic":{"protocol":"anthropic-messages","baseURL":"https://example.com","apiKey":"key"}},"models":{"lead":{"provider":"anthropic","model":"m"}},"defaultModel":"lead"}`, "requires maxOutputTokens"},
		{"bad default profile", `{"providers":{"deepseek":{"protocol":"responses","baseURL":"https://example.com","apiKey":"key"}},"models":{"lead":{"provider":"deepseek","model":"m"}},"defaultModel":"other"}`, "does not name"},
		{"legacy plus new", `{"apikey":"key","providers":{"deepseek":{"apiKey":"key"}}}`, "cannot be combined"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadConfig(writeModelConfig(t, test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadConfig error = %v, want text %q", err, test.want)
			}
		})
	}
}
