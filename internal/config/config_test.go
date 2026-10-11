package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/model"
)

func TestValidateRejectsNonFiniteAutoCompactThreshold(t *testing.T) {
	base := Config{
		Providers: map[string]model.ProviderConfig{
			"provider": {Protocol: model.ProtocolResponses, BaseURL: "https://example.test", APIKey: "key"},
		},
		Models:       map[string]model.ModelProfile{"main": {Provider: "provider", Model: "model"}},
		DefaultModel: "main",
	}
	for _, threshold := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		config := base
		config.AutoCompactThreshold = threshold
		if err := config.Validate(); err == nil {
			t.Errorf("Validate accepted non-finite autoCompactThreshold %v", threshold)
		}
	}
}

func writeConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadProjectConfigAndApplyDefaults(t *testing.T) {
	path := writeConfig(t, `{
		"providers": {"vendor-a": {"protocol":"responses","baseURL":"https://example.com","apiKey":"key"}},
		"models": {"lead": {"provider":"vendor-a","model":"model-a"}},
		"defaultModel":"lead",
		"contextWindow": 128000,
		"autoCompact": true,
		"reserveOutputTokens": 4096,
		"attachments": {"maxFiles": 4, "maxFileBytes": 1048576, "maxTotalBytes": 4194304}
	}`)
	config, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.ContextWindow != 128000 || config.ReserveOutputTokens != 4096 {
		t.Fatalf("context settings = (%d, %d)", config.ContextWindow, config.ReserveOutputTokens)
	}
	if !config.AutoCompact || config.AutoCompactThreshold != 0.8 {
		t.Fatalf("auto compact = (%v, %v), want true and 0.8", config.AutoCompact, config.AutoCompactThreshold)
	}
	if config.Attachments.MaxFiles != 4 || config.Attachments.MaxFileBytes != 1048576 || config.Attachments.MaxTotalBytes != 4194304 {
		t.Fatalf("attachment limits = %#v", config.Attachments)
	}
	modelConfig := config.ModelConfig()
	if modelConfig.DefaultModel != "lead" || modelConfig.ParallelLimit() != model.DefaultMaxParallelAgents {
		t.Fatalf("model config = %#v", modelConfig)
	}
}

func TestLoadProjectConfigKeepsUnsetDefaults(t *testing.T) {
	config, err := Load(writeConfig(t, `{
		"providers":{"deepseek":{"protocol":"responses","baseURL":"https://example.com","apiKey":"key"}},
		"models":{"lead":{"provider":"deepseek","model":"model-a"}},
		"defaultModel":"lead"
	}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if config.ContextWindow != 0 || config.ReserveOutputTokens != 0 || config.AutoCompact || config.AutoCompactThreshold != 0 {
		t.Fatalf("unset project settings should remain zero: %#v", config)
	}
	if config.Attachments != (AttachmentLimits{}) {
		t.Fatalf("unset attachment limits = %#v", config.Attachments)
	}
}

func TestLoadMigratesLegacyAPIKeyAndAllowsProjectSettings(t *testing.T) {
	config, err := Load(writeConfig(t, `{"apikey":"old-key","contextWindow":64000,"autoCompact":true}`))
	if err != nil {
		t.Fatalf("Load legacy config: %v", err)
	}
	if config.DefaultModel != model.DeepSeekFlashModel || config.Providers[model.ProviderDeepSeek].APIKey != "old-key" {
		t.Fatalf("legacy config migration = %#v", config)
	}
	if config.ContextWindow != 64000 || config.AutoCompactThreshold != 0.8 {
		t.Fatalf("project settings after legacy migration = %#v", config)
	}
}

func TestLoadRejectsUnknownAndInvalidFields(t *testing.T) {
	base := `"providers":{"deepseek":{"protocol":"responses","baseURL":"https://example.com","apiKey":"key"}},"models":{"lead":{"provider":"deepseek","model":"m"}},"defaultModel":"lead"`
	cases := []struct {
		name string
		data string
		want string
	}{
		{"unknown top-level", `{` + base + `,"mispelled":true}`, "unknown config field"},
		{"deferred mcp config", `{` + base + `,"mcpServers":{}}`, "unknown config field \"mcpServers\""},
		{"unknown provider field", `{"providers":{"deepseek":{"protocol":"responses","baseURL":"https://example.com","apiKey":"key","endpoint":"x"}},"models":{"lead":{"provider":"deepseek","model":"m"}},"defaultModel":"lead"}`, "unknown field"},
		{"unknown attachment field", `{` + base + `,"attachments":{"maxFileSize":12}}`, "unknown field"},
		{"missing provider key", `{"providers":{"deepseek":{"protocol":"responses","baseURL":"https://example.com","apiKey":" "}},"models":{"lead":{"provider":"deepseek","model":"m"}},"defaultModel":"lead"}`, "non-empty apiKey"},
		{"missing messages output limit", `{"providers":{"anthropic":{"protocol":"anthropic-messages","baseURL":"https://example.com","apiKey":"key"}},"models":{"lead":{"provider":"anthropic","model":"m"}},"defaultModel":"lead"}`, "requires maxOutputTokens"},
		{"bad default profile", `{"providers":{"deepseek":{"protocol":"responses","baseURL":"https://example.com","apiKey":"key"}},"models":{"lead":{"provider":"deepseek","model":"m"}},"defaultModel":"other"}`, "does not name"},
		{"legacy plus model catalog", `{"apikey":"key","providers":{"deepseek":{"protocol":"responses","baseURL":"https://example.com","apiKey":"key"}}}`, "cannot be combined"},
		{"negative context window", `{` + base + `,"contextWindow":-1}`, "contextWindow must not be negative"},
		{"negative reserved tokens", `{` + base + `,"reserveOutputTokens":-1}`, "reserveOutputTokens must not be negative"},
		{"reserved tokens exceed window", `{` + base + `,"contextWindow":1000,"reserveOutputTokens":1000}`, "must be smaller than contextWindow"},
		{"threshold below zero", `{` + base + `,"autoCompactThreshold":-0.1}`, "between 0 and 1"},
		{"threshold above one", `{` + base + `,"autoCompactThreshold":1.1}`, "between 0 and 1"},
		{"negative max files", `{` + base + `,"attachments":{"maxFiles":-1}}`, "attachments.maxFiles must not be negative"},
		{"negative file size", `{` + base + `,"attachments":{"maxFileBytes":-1}}`, "attachments.maxFileBytes must not be negative"},
		{"negative total size", `{` + base + `,"attachments":{"maxTotalBytes":-1}}`, "attachments.maxTotalBytes must not be negative"},
		{"wrong field type", `{` + base + `,"contextWindow":"large"}`, "cannot unmarshal string"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v, want text %q", err, test.want)
			}
		})
	}
}

func TestLoadRejectsTrailingJSON(t *testing.T) {
	data := `{"apikey":"key"} {}`
	_, err := Load(writeConfig(t, data))
	if err == nil || !strings.Contains(err.Error(), "expected one JSON object") {
		t.Fatalf("Load error = %v, want trailing JSON error", err)
	}
}
