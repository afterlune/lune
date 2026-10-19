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

func TestStorePersistsDesktopRestoreState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLastWorkspace(`D:\project`); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLastSession("session-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveWindowSize(1280, 800); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	restore, err := store.DesktopRestore()
	if err != nil {
		t.Fatal(err)
	}
	if restore.Workspace != `D:\project` || restore.SessionID != "session-1" || restore.WindowWidth != 1280 || restore.WindowHeight != 800 {
		t.Fatalf("DesktopRestore() = %#v", restore)
	}
	if err := store.ClearDesktopLocation(); err != nil {
		t.Fatal(err)
	}
	restore, err = store.DesktopRestore()
	if err != nil {
		t.Fatal(err)
	}
	if restore.Workspace != "" || restore.SessionID != "" || restore.WindowWidth != 1280 || restore.WindowHeight != 800 {
		t.Fatalf("after ClearDesktopLocation() = %#v", restore)
	}
	if err := store.ClearDesktopRestore(); err != nil {
		t.Fatal(err)
	}
	restore, err = store.DesktopRestore()
	if err != nil || restore != (DesktopRestore{}) {
		t.Fatalf("after ClearDesktopRestore() = %#v, %v", restore, err)
	}
	if err := store.SaveWindowSize(0, 800); err == nil {
		t.Fatal("SaveWindowSize accepted an invalid width")
	}
}

func TestStoreDesktopRestoreDefaultsToZero(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	restore, err := store.DesktopRestore()
	if err != nil || restore != (DesktopRestore{}) {
		t.Fatalf("DesktopRestore() = %#v, %v", restore, err)
	}
}

func TestStorePersistsAppearance(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetTheme("dark"); err != nil {
		t.Fatal(err)
	}
	appearance, err := store.Appearance()
	if err != nil || appearance.Theme != "dark" || appearance.PanelOpacity != defaultPanelOpacity {
		t.Fatalf("Appearance() = %#v, %v", appearance, err)
	}
	appearance.Theme = "anime"
	appearance.WallpaperType = "dynamic"
	appearance.WallpaperFile = "wallpaper-123.webm"
	appearance.PanelOpacity = 82
	if err := store.SaveAppearance(appearance); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Appearance()
	if err != nil || loaded != appearance {
		t.Fatalf("Appearance() = %#v, %v; want %#v", loaded, err, appearance)
	}
	theme, err := store.Theme()
	if err != nil || theme != "anime" {
		t.Fatalf("Theme() = %q, %v; want anime", theme, err)
	}
}

func TestStoreAppearanceIgnoresLegacyTheme(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.db.Exec(`INSERT INTO app_settings(key, value) VALUES ('theme', 'light')`); err != nil {
		t.Fatal(err)
	}
	appearance, err := store.Appearance()
	if err != nil || appearance.Theme != defaultTheme || appearance.WallpaperType != "static" || appearance.PanelOpacity != defaultPanelOpacity {
		t.Fatalf("Appearance() = %#v, %v; want current defaults", appearance, err)
	}
}

func TestStoreRejectsInvalidAppearance(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, appearance := range []Appearance{
		{Theme: "anime", WallpaperType: "video", PanelOpacity: 80},
		{Theme: "anime", WallpaperType: "static", PanelOpacity: 99},
		{Theme: "anime", WallpaperType: "static", WallpaperFile: `..\\secrets.png`, PanelOpacity: 80},
	} {
		if err := store.SaveAppearance(appearance); err == nil {
			t.Errorf("SaveAppearance(%#v) succeeded, want validation error", appearance)
		}
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
