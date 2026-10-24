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
	toolchainPath := t.TempDir()
	config := core.DefaultProjectConfig()
	config.Runtime.ContextWindow = 48000
	config.MCPServers = map[string]core.MCPServerConfig{
		"private-api": {
			Transport: core.MCPTransportStdio, Command: "mcp-client",
			Env: map[string]string{"TOKEN": "never-store-this-in-plaintext"},
		},
	}
	config.Models = publicmodel.Config{
		Providers:      map[string]publicmodel.ProviderConfig{"host": {Protocol: "responses", BaseURL: "https://example.invalid", APIKey: "model-secret"}},
		Profiles:       map[string]publicmodel.ModelProfile{"main": {Provider: "host", Model: "example"}},
		DefaultProfile: "main",
	}
	if err := store.SaveWorkspaceSettings(firstWorkspace, config, []string{toolchainPath}); err != nil {
		t.Fatal(err)
	}

	loaded, found, err := store.LoadWorkspaceConfig(firstWorkspace)
	if err != nil || !found {
		t.Fatalf("LoadWorkspaceConfig found=%v, error=%v", found, err)
	}
	if loaded.Runtime.ContextWindow != 48000 || loaded.MCPServers["private-api"].Env["TOKEN"] != "never-store-this-in-plaintext" {
		t.Fatalf("loaded config lost runtime fields: %#v", loaded)
	}
	if len(loaded.Models.Providers) != 0 || len(loaded.Models.Profiles) != 0 || loaded.Models.DefaultProfile != "" {
		t.Fatalf("model settings leaked into the project-scoped config: %#v", loaded.Models)
	}
	if _, found, err := store.LoadWorkspaceConfig(secondWorkspace); err != nil || found {
		t.Fatalf("second project config found=%v, error=%v", found, err)
	}
	paths, found, err := store.LoadWorkspaceCommandToolchainPaths(firstWorkspace)
	if err != nil || !found || len(paths) != 1 || paths[0] != toolchainPath {
		t.Fatalf("loaded command toolchains = %#v, found=%v, error=%v", paths, found, err)
	}
	if _, found, err := store.LoadWorkspaceCommandToolchainPaths(secondWorkspace); err != nil || found {
		t.Fatalf("second project toolchain paths found=%v, error=%v", found, err)
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
	if _, found, err := store.LoadWorkspaceCommandToolchainPaths(firstWorkspace); err != nil || found {
		t.Fatalf("deleted project toolchain paths found=%v, error=%v", found, err)
	}
}

func TestLegacyWorkspaceConfigPayloadResetsToDefaults(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Desktop project configuration is encrypted with Windows DPAPI")
	}
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	workspace := t.TempDir()
	key, err := workspaceConfigKey(workspace)
	if err != nil {
		t.Fatal(err)
	}
	legacy := `{"contextWindow":48000,"autoCompact":false,"models":{"main":{"provider":"host","model":"legacy-model"}},"defaultModel":"main"}`
	encrypted, err := protectSecret(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO desktop_project_configs(workspace_key, encrypted_config, updated_at) VALUES (?, ?, ?)`, key, encrypted, 1); err != nil {
		t.Fatal(err)
	}

	loaded, found, err := store.LoadWorkspaceConfig(workspace)
	if err != nil || !found {
		t.Fatalf("LoadWorkspaceConfig found=%v, error=%v", found, err)
	}
	if loaded.Runtime.ContextWindow != 0 || !loaded.Runtime.AutoCompact || loaded.Runtime.AutoCompactThreshold != core.DefaultAutoCompactThreshold {
		t.Fatalf("legacy workspace settings were not reset to defaults: %#v", loaded.Runtime)
	}
	if len(loaded.Models.Providers) != 0 || len(loaded.Models.Profiles) != 0 || loaded.Models.DefaultProfile != "" {
		t.Fatalf("legacy model settings leaked into reset config: %#v", loaded.Models)
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
	if _, _, err := (*Store)(nil).LoadWorkspaceCommandToolchainPaths(t.TempDir()); err == nil {
		t.Fatal("LoadWorkspaceCommandToolchainPaths succeeded on a nil store")
	}
	if err := (*Store)(nil).SaveWorkspaceSettings(t.TempDir(), core.DefaultProjectConfig(), nil); err == nil {
		t.Fatal("SaveWorkspaceSettings succeeded on a nil store")
	}
	if err := (*Store)(nil).SaveWorkspaceCommandToolchainPaths(t.TempDir(), nil); err == nil {
		t.Fatal("SaveWorkspaceCommandToolchainPaths succeeded on a nil store")
	}
}

func TestSaveAndLoadWorkspaceCommandToolchainPaths(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	workspace := t.TempDir()
	paths := []string{t.TempDir(), t.TempDir()}

	if err := store.SaveWorkspaceCommandToolchainPaths(workspace, paths); err != nil {
		t.Fatalf("SaveWorkspaceCommandToolchainPaths failed: %v", err)
	}

	loaded, found, err := store.LoadWorkspaceCommandToolchainPaths(workspace)
	if err != nil || !found {
		t.Fatalf("LoadWorkspaceCommandToolchainPaths failed: found=%v, err=%v", found, err)
	}
	if len(loaded) != len(paths) || loaded[0] != paths[0] || loaded[1] != paths[1] {
		t.Fatalf("loaded paths = %v, want %v", loaded, paths)
	}
}
