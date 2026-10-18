package desktop

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/settings"
	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

func TestDesktopStateIsSeparateFromWorkspaceState(t *testing.T) {
	configRoot := filepath.Join(t.TempDir(), "roaming")
	cacheRoot := filepath.Join(t.TempDir(), "local")
	t.Setenv("APPDATA", configRoot)
	t.Setenv("LOCALAPPDATA", cacheRoot)
	workspace := t.TempDir()

	stateRoot, err := desktopProjectStateDir(workspace)
	if err != nil {
		t.Fatal(err)
	}
	stateDir, err := filepath.Abs(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	legacyDir, err := filepath.Abs(filepath.Join(workspace, ".luneagent"))
	if err != nil {
		t.Fatal(err)
	}
	if samePath(stateDir, legacyDir) || strings.HasPrefix(strings.ToLower(stateDir), strings.ToLower(legacyDir+string(filepath.Separator))) {
		t.Fatalf("Desktop state directory %q is inside TUI state directory %q", stateDir, legacyDir)
	}
	stateAgain, err := desktopProjectStateDir(workspace)
	if err != nil || stateAgain != stateRoot {
		t.Fatalf("Desktop state path was not stable: first=%q second=%q err=%v", stateRoot, stateAgain, err)
	}
}

func TestDesktopCanOpenWorkspaceWhileCoreOwnsLegacyStateDir(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Desktop settings require Windows DPAPI")
	}
	configRoot := filepath.Join(t.TempDir(), "roaming")
	cacheRoot := filepath.Join(t.TempDir(), "local")
	t.Setenv("APPDATA", configRoot)
	t.Setenv("LOCALAPPDATA", cacheRoot)
	workspace := t.TempDir()
	projectConfig := `{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"models":{"primary":{"provider":"host","model":"test-model"}},"defaultModel":"primary"}`
	if err := os.WriteFile(filepath.Join(workspace, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	settingsStore, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	saveDesktopTestModels(t, settingsStore)
	legacyCore, err := core.New(core.Config{Workspace: workspace})
	if err != nil {
		_ = settingsStore.Close()
		t.Fatal(err)
	}
	defer legacyCore.Close()
	configPath := filepath.Join(workspace, "config.json")
	ignoredConfig := []byte(`{"not":"a Desktop config"}`)
	if err := os.WriteFile(configPath, ignoredConfig, 0600); err != nil {
		t.Fatal(err)
	}
	service := &DesktopService{store: settingsStore, waiting: map[string]bool{}, sessionContexts: map[string]*desktopSessionContext{}}
	t.Cleanup(func() {
		if err := service.CloseWorkspace(true); err != nil {
			t.Errorf("close Desktop workspace: %v", err)
		}
		if err := settingsStore.Close(); err != nil {
			t.Errorf("close settings store: %v", err)
		}
	})

	opened, err := service.OpenWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !samePath(opened.Workspace, workspace) {
		t.Fatalf("opened workspace = %q, want %q", opened.Workspace, workspace)
	}
	if len(service.projectConfig.MCPServers) != 0 || service.projectConfig.ModelConfig.DefaultModel != "main" {
		t.Fatalf("Desktop loaded project config.json instead of SQLite settings: %#v", service.projectConfig)
	}
	unchanged, err := os.ReadFile(configPath)
	if err != nil || string(unchanged) != string(ignoredConfig) {
		t.Fatalf("Desktop changed or read project config.json: contents=%q error=%v", unchanged, err)
	}
	if samePath(filepath.Join(service.stateRoot, "local"), filepath.Join(workspace, ".luneagent")) {
		t.Fatalf("Desktop Core reused TUI state directory %q", service.stateRoot)
	}
	if _, err := legacyCore.ListSessions(context.Background(), 10, false); err != nil {
		t.Fatalf("TUI/Core became unusable after Desktop opened: %v", err)
	}
}

func saveDesktopTestModels(t *testing.T, store *settings.Store) {
	t.Helper()
	config := publicmodel.Config{
		Providers: map[string]publicmodel.ProviderConfig{"host": {
			Protocol: "responses", BaseURL: "https://example.invalid", APIKey: "test-key",
		}},
		Models:       map[string]publicmodel.ModelProfile{"main": {Provider: "host", Model: "test-model"}},
		DefaultModel: "main",
	}
	if err := store.SaveModelConfig(config); err != nil {
		t.Fatal(err)
	}
}
