package desktop

import (
	"encoding/json"
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

func TestMergeWorkspaceConfigPreservesUnchangedMCPSecretsAndOmitsRemovedVariables(t *testing.T) {
	current := core.DefaultProjectConfig()
	current.ModelConfig = publicmodel.Config{
		Providers:    map[string]publicmodel.ProviderConfig{"host": {Protocol: "responses", BaseURL: "https://example.invalid", APIKey: "model-key"}},
		Models:       map[string]publicmodel.ModelProfile{"main": {Provider: "host", Model: "test-model"}},
		DefaultModel: "main",
	}
	current.MCPServers = map[string]core.MCPServerConfig{
		"private-api": {
			Transport: core.MCPTransportStdio, Command: "mcp-client",
			Env: map[string]string{"TOKEN": "preserved-secret", "REMOVE_ME": "old-value"},
		},
	}
	input := WorkspaceConfigInput{
		AutoCompact: true, AutoCompactThreshold: 0.75, ContextWindow: 32000,
		MCPServers: []MCPServerConfigInput{{
			Name: "private-api-renamed", OriginalName: "private-api", Transport: "stdio", Command: "mcp-client",
			Env: []MCPEnvironmentInput{{Name: "TOKEN"}, {Name: "NEW_TOKEN", Value: "new-secret"}},
		}},
	}
	merged, err := mergeWorkspaceConfig(current, input)
	if err != nil {
		t.Fatal(err)
	}
	env := merged.MCPServers["private-api-renamed"].Env
	if env["TOKEN"] != "preserved-secret" || env["NEW_TOKEN"] != "new-secret" {
		t.Fatalf("secret merge result = %#v", env)
	}
	if _, exists := env["REMOVE_ME"]; exists {
		t.Fatalf("removed environment key was retained: %#v", env)
	}
	if merged.ContextWindow != 32000 || merged.AutoCompactThreshold != 0.75 {
		t.Fatalf("runtime settings were not applied: %#v", merged)
	}

	encoded, err := json.Marshal(workspaceConfigView(merged))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "preserved-secret") || strings.Contains(string(encoded), "new-secret") {
		t.Fatalf("project settings view exposed an MCP value: %s", encoded)
	}
}

func TestMergeWorkspaceConfigRejectsInvalidMCPAndRuntimeValues(t *testing.T) {
	current := core.DefaultProjectConfig()
	current.ModelConfig = publicmodel.Config{
		Providers:    map[string]publicmodel.ProviderConfig{"host": {Protocol: "responses", BaseURL: "https://example.invalid", APIKey: "model-key"}},
		Models:       map[string]publicmodel.ModelProfile{"main": {Provider: "host", Model: "test-model"}},
		DefaultModel: "main",
	}
	for _, input := range []WorkspaceConfigInput{
		{AutoCompact: true, MCPServers: []MCPServerConfigInput{{Name: "bad", Transport: "stdio"}}},
		{AutoCompact: true, ContextWindow: -1},
	} {
		if _, err := mergeWorkspaceConfig(current, input); err == nil {
			t.Fatalf("invalid project config was accepted: %#v", input)
		}
	}
}
