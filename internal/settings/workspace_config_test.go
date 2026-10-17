package settings

import (
	"bytes"
	"path/filepath"
	"runtime"
	"testing"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

func TestWorkspaceConfigEncryptsAndScopesRuntimeSettings(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Desktop project configuration is encrypted with Windows DPAPI")
	}
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	firstWorkspace, secondWorkspace := t.TempDir(), t.TempDir()
	config := core.DefaultProjectConfig()
	config.ContextWindow = 48000
	config.MCPServers = map[string]core.MCPServerConfig{
		"private-api": {
			Transport: core.MCPTransportStdio, Command: "mcp-client",
			Env: map[string]string{"TOKEN": "never-store-this-in-plaintext"},
		},
	}
	config.ModelConfig = publicmodel.Config{
		Providers:    map[string]publicmodel.ProviderConfig{"host": {Protocol: "responses", BaseURL: "https://example.invalid", APIKey: "model-secret"}},
		Models:       map[string]publicmodel.ModelProfile{"main": {Provider: "host", Model: "example"}},
		DefaultModel: "main",
	}
	if err := store.SaveWorkspaceConfig(firstWorkspace, config); err != nil {
		t.Fatal(err)
	}

	loaded, found, err := store.LoadWorkspaceConfig(firstWorkspace)
	if err != nil || !found {
		t.Fatalf("LoadWorkspaceConfig found=%v, error=%v", found, err)
	}
	if loaded.ContextWindow != 48000 || loaded.MCPServers["private-api"].Env["TOKEN"] != "never-store-this-in-plaintext" {
		t.Fatalf("loaded config lost runtime fields: %#v", loaded)
	}
	if len(loaded.ModelConfig.Providers) != 0 || len(loaded.ModelConfig.Models) != 0 || loaded.ModelConfig.DefaultModel != "" {
		t.Fatalf("model settings leaked into the project-scoped config: %#v", loaded.ModelConfig)
	}
	if _, found, err := store.LoadWorkspaceConfig(secondWorkspace); err != nil || found {
		t.Fatalf("second project config found=%v, error=%v", found, err)
	}

	key, err := workspaceConfigKey(firstWorkspace)
	if err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	if err := store.db.QueryRow("SELECT encrypted_config FROM desktop_project_configs WHERE workspace_key = ?", key).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte("never-store-this-in-plaintext")) || bytes.Contains(encrypted, []byte("model-secret")) {
		t.Fatal("encrypted Desktop config contains a plaintext secret")
	}

	if err := store.DeleteWorkspaceConfig(firstWorkspace); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.LoadWorkspaceConfig(firstWorkspace); err != nil || found {
		t.Fatalf("deleted project config found=%v, error=%v", found, err)
	}
}

func TestWorkspaceConfigStoreRejectsNilStore(t *testing.T) {
	if _, _, err := (*Store)(nil).LoadWorkspaceConfig(t.TempDir()); err == nil {
		t.Fatal("LoadWorkspaceConfig succeeded on a nil store")
	}
	if err := (*Store)(nil).SaveWorkspaceConfig(t.TempDir(), core.DefaultProjectConfig()); err == nil {
		t.Fatal("SaveWorkspaceConfig succeeded on a nil store")
	}
	if err := (*Store)(nil).DeleteWorkspaceConfig(t.TempDir()); err == nil {
		t.Fatal("DeleteWorkspaceConfig succeeded on a nil store")
	}
}
