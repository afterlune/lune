package settings

import (
	"path/filepath"
	"reflect"
	"testing"
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
	if journalMode != "wal" && journalMode != "WAL" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}
}

// TestTwoStoresWriteTheSameDatabase keeps multiple TUI/CLI processes from
// failing when sharing settings.db concurrently.
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

func TestStorePersistsRecentWorkspace(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

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

func TestStoreTUIThemeIsPersistent(t *testing.T) {
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
	if err := store.SaveTUITheme("sepia"); err == nil {
		t.Fatal("SaveTUITheme accepted an invalid mode")
	}
}

func TestStorePersistsCommandToolchains(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	workspace := t.TempDir()
	if _, found, err := store.LoadWorkspaceCommandToolchainPaths(workspace); err != nil || found {
		t.Fatalf("LoadWorkspaceCommandToolchainPaths() = found %v, err %v; want false", found, err)
	}
	want := []string{filepath.Join(workspace, "go"), filepath.Join(workspace, "git")}
	if err := store.SaveWorkspaceCommandToolchainPaths(workspace, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := store.LoadWorkspaceCommandToolchainPaths(workspace)
	if err != nil || !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadWorkspaceCommandToolchainPaths() = (%#v, %v, %v); want (%#v, true, nil)", got, found, err, want)
	}
}

func TestStorePersistsWorkspacePermissionMode(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	workspace := t.TempDir()
	if _, found, err := store.LoadWorkspacePermissionMode(workspace); err != nil || found {
		t.Fatalf("LoadWorkspacePermissionMode() = found %v, err %v; want false", found, err)
	}
	if err := store.SaveWorkspacePermissionMode(workspace, "auto"); err != nil {
		t.Fatal(err)
	}
	if mode, found, err := store.LoadWorkspacePermissionMode(workspace); err != nil || !found || mode != "auto" {
		t.Fatalf("LoadWorkspacePermissionMode() = %q, %v, %v; want auto", mode, found, err)
	}
	if err := store.SaveWorkspacePermissionMode(workspace, "invalid-mode"); err == nil {
		t.Fatal("SaveWorkspacePermissionMode accepted an invalid mode")
	}
}
