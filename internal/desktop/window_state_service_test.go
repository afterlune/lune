package desktop

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/afterlune/lune/internal/settings"
)

func TestOpenWorkspaceAndSessionRememberLocation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Desktop settings require Windows DPAPI")
	}
	t.Setenv("PATH", t.TempDir()) // Avoid starting a user's local Engram during this lifecycle test.
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	saveDesktopTestModels(t, store)
	service := &DesktopService{store: store, waiting: map[string]bool{}, sessionContexts: map[string]*desktopSessionContext{}}
	t.Cleanup(func() {
		_ = service.CloseWorkspace(false)
		_ = store.Close()
	})
	workspace := t.TempDir()
	if _, err := service.OpenWorkspace(workspace); err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSession()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateSession(); err != nil {
		t.Fatal(err)
	}
	if _, err := service.OpenSession(created.Session.ID); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := service.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.LastWorkspace == "" || bootstrap.LastSessionID != created.Session.ID {
		t.Fatalf("restore fields = workspace %q, session %q; want %q / %q",
			bootstrap.LastWorkspace, bootstrap.LastSessionID, workspace, created.Session.ID)
	}
	if err := service.CloseWorkspace(false); err != nil {
		t.Fatal(err)
	}
	restore, err := store.DesktopRestore()
	if err != nil {
		t.Fatal(err)
	}
	if restore.Workspace != "" || restore.SessionID != "" {
		t.Fatalf("location after close = %#v", restore)
	}
}
