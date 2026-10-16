package settings

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	publicmodel "github.com/afterlune/luneCore/model"
)

func TestStorePersistsThemeAndRecentWorkspace(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetTheme("dark"); err != nil {
		t.Fatal(err)
	}
	theme, err := store.Theme()
	if err != nil || theme != "dark" {
		t.Fatalf("Theme() = %q, %v", theme, err)
	}
	workspace := t.TempDir()
	if err := store.TouchWorkspace(workspace); err != nil {
		t.Fatal(err)
	}
	recent, err := store.RecentWorkspaces()
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].Path != workspace {
		t.Fatalf("RecentWorkspaces() = %#v", recent)
	}
}

func TestStoreEncryptsAndRestoresProviderKeys(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("DPAPI is Windows-only")
	}
	path := filepath.Join(t.TempDir(), "settings.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	config := publicmodel.Config{
		Providers: map[string]publicmodel.ProviderConfig{
			"test": {Protocol: publicmodel.ProtocolResponses, BaseURL: "https://model.example.test/v1", APIKey: "test-secret-value"},
		},
		Models:       map[string]publicmodel.ModelProfile{"test-model": {Provider: "test", Model: "unit-model"}},
		DefaultModel: "test-model",
	}
	if err := store.SaveModelConfig(config); err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	if err := store.db.QueryRow(`SELECT encrypted_api_key FROM providers WHERE name = 'test'`).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encrypted), config.Providers["test"].APIKey) {
		t.Fatal("API key appears in plaintext in the settings database")
	}
	loaded, err := store.LoadModelConfig()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Providers["test"].APIKey; got != config.Providers["test"].APIKey {
		t.Fatalf("API key round trip = %q", got)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bytes), config.Providers["test"].APIKey) {
		t.Fatal("API key appears in the SQLite file")
	}
}
