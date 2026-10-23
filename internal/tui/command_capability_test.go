package tui

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/settings"
	core "github.com/afterlune/luneCore"
	"github.com/afterlune/luneCore/capability"
	publicmodel "github.com/afterlune/luneCore/model"
)

func TestTUICapabilityManagement(t *testing.T) {
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	project := core.DefaultProjectConfig()
	project.Models = publicmodel.Config{
		Providers: map[string]publicmodel.ProviderConfig{
			"host": {Protocol: publicmodel.ProtocolResponses, BaseURL: "https://example.invalid", APIKey: "test"},
		},
		Profiles:       map[string]publicmodel.ModelProfile{"default": {Provider: "host", Model: "test-model"}},
		DefaultProfile: "default",
	}
	client, err := core.New(core.Config{Workspace: t.TempDir(), ProjectConfig: &project, Memory: core.MemoryConfig{Disabled: true}, GeneratedTools: store})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	item, _, err := store.Save(t.Context(), capability.GeneratedTool{
		Name: "Search project", Description: "Find source files by a query.", Purpose: capability.PurposeResearch,
		Tags: []string{"source"}, InputSchema: json.RawMessage(`{"type":"object"}`),
		Script:       `function run(input, tools) { return tools.call("grep", {pattern: input.query}); }`,
		Dependencies: []string{"grep"},
	})
	if err != nil {
		t.Fatal(err)
	}
	m := newTUIModel(client, nil, "draft")
	m.handleSlashCommand("/capability list source")
	if got := m.entries[len(m.entries)-1].Text; !strings.Contains(got, "Search project") || !strings.Contains(got, "source") {
		t.Fatalf("capability list output = %q", got)
	}
	m.handleSlashCommand("/capability disable " + item.ID[:8])
	items, err := client.ListGeneratedTools(t.Context(), 10, true)
	if err != nil || len(items) != 1 || items[0].Enabled {
		t.Fatalf("disable state = %#v, %v", items, err)
	}
	m.handleSlashCommand("/capability delete " + item.ID[:8])
	items, err = client.ListGeneratedTools(t.Context(), 10, true)
	if err != nil || len(items) != 0 {
		t.Fatalf("delete result = %#v, %v", items, err)
	}
}
