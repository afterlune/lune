package tui

import (
	"testing"

	core "github.com/afterlune/luneCore"
)

// A mode this build does not know must not preselect read-only: pressing Enter in
// the picker would silently downgrade the permissions the session is running with.
func TestPermissionPickerFallsBackToAutoForUnknownMode(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.client = nil
	if _, _ = m.handleSlashCommand("/permissions"); m.modals.permission != nil {
		t.Fatal("the picker must not open without a Core client")
	}

	m = newTUIModel(newLazySessionTestCore(t), nil, "")
	m.permissionMode = core.PermissionMode("ask")
	if _, _ = m.openPermissionPicker(); m.modals.permission == nil {
		t.Fatal("expected the permission picker to open")
	}
	if got, want := m.modals.permission.Selected, indexOfPermissionMode(core.PermissionAuto); got != want {
		t.Fatalf("unknown mode preselected index %d, want the auto index %d", got, want)
	}

	m.permissionMode = core.PermissionWorkspace
	if _, _ = m.openPermissionPicker(); m.modals.permission.Selected != indexOfPermissionMode(core.PermissionWorkspace) {
		t.Fatalf("a known mode must preselect its own row, got %d", m.modals.permission.Selected)
	}
}
