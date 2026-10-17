package desktop

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/afterlune/lune/internal/settings"
)

func TestSaveWorkspaceConfigRecreatesCoreAndRestoresActiveSession(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Desktop project settings require Windows DPAPI")
	}
	t.Setenv("APPDATA", filepath.Join(t.TempDir(), "roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "local"))
	workspace := t.TempDir()
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	saveDesktopTestModels(t, store)
	service := &DesktopService{store: store, waiting: map[string]bool{}, sessionContexts: map[string]*desktopSessionContext{}}
	t.Cleanup(func() {
		if err := service.CloseWorkspace(true); err != nil {
			t.Errorf("close Desktop service: %v", err)
		}
		if err := store.Close(); err != nil {
			t.Errorf("close settings store: %v", err)
		}
	})
	if _, err := service.OpenWorkspace(workspace); err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateSession(workspaceModeLocal)
	if err != nil {
		t.Fatal(err)
	}
	previousCore := service.localCore
	if _, err := service.SaveWorkspaceConfig(WorkspaceConfigInput{AutoCompact: true, ContextWindow: -1}); err == nil {
		t.Fatal("invalid project settings were accepted")
	}
	if service.localCore != previousCore {
		t.Fatal("invalid project settings restarted Core")
	}
	if _, found, err := store.LoadWorkspaceConfig(workspace); err != nil || found {
		t.Fatalf("invalid settings were persisted: found=%v error=%v", found, err)
	}

	toolchainPath := t.TempDir()
	input := WorkspaceConfigInput{
		ContextWindow: 32000, AutoCompact: true, AutoCompactThreshold: 0.7,
		ReserveOutputTokens: 4096, MaxAttachmentFiles: 4, MaxAttachmentBytes: 5 << 20, MaxTotalBytes: 10 << 20,
		CommandToolchainPaths: []string{toolchainPath},
	}
	view, err := service.SaveWorkspaceConfig(input)
	if err != nil {
		t.Fatal(err)
	}
	if view.ContextWindow != input.ContextWindow || view.MaxAttachmentFiles != input.MaxAttachmentFiles || len(view.CommandToolchainPaths) != 1 || view.CommandToolchainPaths[0] != toolchainPath {
		t.Fatalf("saved view = %#v", view)
	}
	if service.localCore == previousCore {
		t.Fatal("saving project settings did not recreate Core")
	}
	if service.sessionID != created.Session.ID {
		t.Fatalf("active session after restart = %q, want %q", service.sessionID, created.Session.ID)
	}
	if len(service.commandToolchainPaths) != 1 || service.commandToolchainPaths[0] != toolchainPath {
		t.Fatalf("active Core toolchain paths = %#v", service.commandToolchainPaths)
	}
	state, err := service.RefreshSession()
	if err != nil || state.Session.ID != created.Session.ID {
		t.Fatalf("active session state = %#v, error=%v", state, err)
	}
	stored, found, err := store.LoadWorkspaceConfig(workspace)
	if err != nil || !found || stored.ContextWindow != input.ContextWindow || stored.ReserveOutputTokens != input.ReserveOutputTokens {
		t.Fatalf("stored settings = %#v, found=%v, error=%v", stored, found, err)
	}
	storedPaths, pathsFound, err := store.LoadWorkspaceCommandToolchainPaths(workspace)
	if err != nil || !pathsFound || len(storedPaths) != 1 || storedPaths[0] != toolchainPath {
		t.Fatalf("stored command toolchain paths = %#v, found=%v, error=%v", storedPaths, pathsFound, err)
	}
}
