package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/settings"
	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

const migrationConfig = `{
  "providers": {"vendor": {"protocol":"responses","baseURL":"https://project.example.test/v1","apiKey":"project-secret"}},
  "models": {"main": {"provider":"vendor","model":"project-model"}},
  "defaultModel":"main",
  "maxParallelAgents":2,
  "contextWindow":64000,
  "attachments":{"maxFiles":3},
  "mcpServers":{"docs":{"transport":"stdio","command":"docs-mcp"}}
}`

const modelOnlyConfig = `{"providers":{"vendor":{"protocol":"responses","baseURL":"https://project.example.test/v1","apiKey":"project-secret"}},"models":{"main":{"provider":"vendor","model":"project-model"}},"defaultModel":"main"}`

func writeMigrationConfig(t *testing.T, contents string) string {
	t.Helper()
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "config.json"), []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func TestMigrationPreviewDoesNotExposeAPIKeys(t *testing.T) {
	workspace := writeMigrationConfig(t, migrationConfig)
	_, _, _, incoming, found, err := readLegacyModelSettings(workspace)
	if err != nil || !found {
		t.Fatalf("readLegacyModelSettings = found %v, error %v", found, err)
	}
	preview, err := previewMigration(workspace, publicmodel.Config{}, incoming)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "project-secret") {
		t.Fatalf("migration preview leaked a provider key: %s", encoded)
	}
	if len(preview.Providers) != 1 || preview.Providers[0] != "vendor" || len(preview.Models) != 1 || preview.Models[0] != "main" {
		t.Fatalf("unexpected preview: %#v", preview)
	}
}

func TestMigrationRemovesModelFieldsAndPreservesProjectSettings(t *testing.T) {
	workspace := writeMigrationConfig(t, migrationConfig)
	merged := publicmodel.Config{}
	err := migrateProjectModelSettings(workspace, merged, nil, func(config publicmodel.Config, apply, _ func() error) error {
		if err := config.Validate(); err != nil {
			t.Fatal(err)
		}
		merged = config
		return apply()
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(filepath.Join(workspace, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(updated, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"providers", "models", "defaultModel", "maxParallelAgents", "apikey"} {
		if _, ok := fields[name]; ok {
			t.Fatalf("project model field %q remains in config", name)
		}
	}
	for _, name := range []string{"contextWindow", "attachments", "mcpServers"} {
		if _, ok := fields[name]; !ok {
			t.Fatalf("project setting %q was lost", name)
		}
	}

	runtime, err := core.New(core.Config{
		Workspace: workspace, StateDir: filepath.Join(t.TempDir(), "state"), ModelConfig: &merged,
	})
	if err != nil {
		t.Fatalf("open migrated workspace with host model settings: %v", err)
	}
	defer runtime.Close()
	if got := runtime.ProjectAttachmentLimits().MaxFiles; got != 3 {
		t.Fatalf("attachment limit after migration = %d, want 3", got)
	}
	if servers := runtime.MCPServers(); len(servers) != 1 || servers[0].Name != "docs" {
		t.Fatalf("MCP servers after migration = %#v, want docs server", servers)
	}
}

func TestMigrationRestoresOriginalConfigWhenSettingsCommitFails(t *testing.T) {
	workspace := writeMigrationConfig(t, migrationConfig)
	path := filepath.Join(workspace, "config.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	commitFailure := errors.New("commit failed")
	err = migrateProjectModelSettings(workspace, publicmodel.Config{}, nil, func(_ publicmodel.Config, apply, rollback func() error) error {
		if err := apply(); err != nil {
			return err
		}
		return errors.Join(commitFailure, rollback())
	})
	if !errors.Is(err, commitFailure) {
		t.Fatalf("migration error = %v", err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(original) {
		t.Fatal("project config changed after a failed migration")
	}
}

func TestModelOnlyMigrationOpensWithHostModelConfig(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("global model secrets require Windows DPAPI")
	}
	workspace := writeMigrationConfig(t, modelOnlyConfig)
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := migrateProjectModelSettings(workspace, publicmodel.Config{}, nil, store.SaveModelConfigWith); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(workspace, "config.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("model-only project config remains: %v", err)
	}
	modelConfig, err := store.LoadModelConfig()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := core.New(core.Config{Workspace: workspace, ModelConfig: &modelConfig})
	if err != nil {
		t.Fatalf("open migrated workspace: %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteModelMigrationRestoresBothStoresWhenCoreOpenFails(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("global model secrets require Windows DPAPI")
	}
	original := `{"providers":{"vendor":{"protocol":"responses","baseURL":"https://project.example.test/v1","apiKey":"project-secret"}},"models":{"main":{"provider":"vendor","model":"project-model"}},"defaultModel":"main","contextWindow":-1}`
	workspace := writeMigrationConfig(t, original)
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := &DesktopService{store: store}

	if _, err := service.CompleteModelMigration(workspace, nil); err == nil {
		t.Fatal("migration succeeded despite invalid project runtime settings")
	}
	current, err := os.ReadFile(filepath.Join(workspace, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != original {
		t.Fatal("project config was not restored after Core startup failed")
	}
	modelConfig, err := store.LoadModelConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(modelConfig.Providers) != 0 || len(modelConfig.Models) != 0 || modelConfig.DefaultModel != "" {
		t.Fatalf("user model settings were not rolled back: %#v", modelConfig)
	}
}
