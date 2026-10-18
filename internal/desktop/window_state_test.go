package desktop

import (
	"path/filepath"
	"testing"

	"github.com/afterlune/lune/internal/settings"
)

func newDesktopServiceWithSettings(t *testing.T) *DesktopService {
	t.Helper()
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &DesktopService{store: store, waiting: map[string]bool{}, sessionContexts: map[string]*desktopSessionContext{}}
}

func TestBootstrapReportsDesktopRestoreState(t *testing.T) {
	service := newDesktopServiceWithSettings(t)
	if err := service.store.SaveLastWorkspace(`D:\project`); err != nil {
		t.Fatal(err)
	}
	if err := service.store.SaveLastSession("session-1"); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := service.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.LastWorkspace != `D:\project` || bootstrap.LastSessionID != "session-1" {
		t.Fatalf("restore fields = %q, %q", bootstrap.LastWorkspace, bootstrap.LastSessionID)
	}
}

func TestWindowSizePersistsAndNormalizes(t *testing.T) {
	service := newDesktopServiceWithSettings(t)
	if width, height := service.WindowSize(); width != defaultWindowWidth || height != defaultWindowHeight {
		t.Fatalf("default window size = %d×%d", width, height)
	}
	if err := service.SaveWindowSize(1280, 800); err != nil {
		t.Fatal(err)
	}
	if width, height := service.WindowSize(); width != 1280 || height != 800 {
		t.Fatalf("saved window size = %d×%d", width, height)
	}
	if err := service.SaveWindowSize(10, 20); err != nil {
		t.Fatal(err)
	}
	if width, height := service.WindowSize(); width != minWindowWidth || height != minWindowHeight {
		t.Fatalf("normalized window size = %d×%d", width, height)
	}
	if err := service.SaveWindowSize(0, 800); err == nil {
		t.Fatal("SaveWindowSize accepted a zero width")
	}
}

func TestRememberedLocationCanBeCleared(t *testing.T) {
	service := newDesktopServiceWithSettings(t)
	service.rememberWorkspace(`D:\project`)
	service.rememberSession("session-1")
	service.clearRememberedLocation()
	restore, err := service.store.DesktopRestore()
	if err != nil {
		t.Fatal(err)
	}
	if restore.Workspace != "" || restore.SessionID != "" {
		t.Fatalf("location after clear = %#v", restore)
	}
}
