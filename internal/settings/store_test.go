package settings

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	publicmodel "github.com/afterlune/luneCore/model"
)

func TestOpenConfiguresConcurrentAccessPragmas(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var busyTimeout int
	if err := store.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", busyTimeout)
	}

	var journalMode string
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if !strings.EqualFold(journalMode, "wal") {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
}

// TestTwoStoresWriteTheSameDatabase keeps the TUI and the desktop from failing
// when both processes hold settings.db open.
func TestTwoStoresWriteTheSameDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	for index := 0; index < 20; index++ {
		if err := first.saveSetting("shared.key", "first"); err != nil {
			t.Fatalf("first writer: %v", err)
		}
		if err := second.saveSetting("shared.key", "second"); err != nil {
			t.Fatalf("second writer: %v", err)
		}
	}
	if value, ok, err := first.setting("shared.key"); err != nil || !ok || value != "second" {
		t.Fatalf("setting() = %q, %v, %v", value, ok, err)
	}
}

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

func TestStoreTUIThemeIsIndependentAndPersistent(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if _, found, err := store.TUITheme(); err != nil || found {
		t.Fatalf("TUITheme() = found %v, err %v; want no saved value", found, err)
	}
	if err := store.SaveTUITheme("light"); err != nil {
		t.Fatal(err)
	}
	if mode, found, err := store.TUITheme(); err != nil || !found || mode != "light" {
		t.Fatalf("TUITheme() = %q, found %v, err %v; want light", mode, found, err)
	}
	appearance, err := store.Appearance()
	if err != nil {
		t.Fatal(err)
	}
	if appearance.Theme != defaultTheme {
		t.Fatalf("TUI preference changed Desktop theme: got %q, want %q", appearance.Theme, defaultTheme)
	}
	if err := store.SaveTUITheme("sepia"); err == nil {
		t.Fatal("SaveTUITheme accepted an invalid mode")
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
	settings := SharedModelSettings{Catalog: publicmodel.Config{
		Providers: map[string]publicmodel.ProviderConfig{
			"test": {Protocol: publicmodel.ProtocolResponses, BaseURL: "https://model.example.test/v1", APIKey: "test-secret-value"},
		},
		Profiles:       map[string]publicmodel.ModelProfile{"test-model": {Provider: "test", Model: "unit-model"}},
		DefaultProfile: "test-model",
	}, MaxParallelAgents: 3}
	if err := store.SaveModelSettings(settings); err != nil {
		t.Fatal(err)
	}
	var encrypted []byte
	if err := store.db.QueryRow(`SELECT encrypted_api_key FROM providers WHERE name = 'test'`).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encrypted), settings.Catalog.Providers["test"].APIKey) {
		t.Fatal("API key appears in plaintext in the settings database")
	}
	loaded, err := store.LoadModelSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Catalog.Providers["test"].APIKey; got != settings.Catalog.Providers["test"].APIKey {
		t.Fatalf("API key round trip = %q", got)
	}
	if loaded.Catalog.DefaultProfile != settings.Catalog.DefaultProfile || loaded.MaxParallelAgents != settings.MaxParallelAgents {
		t.Fatalf("model settings round trip = %#v", loaded)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bytes), settings.Catalog.Providers["test"].APIKey) {
		t.Fatal("API key appears in the SQLite file")
	}
}
