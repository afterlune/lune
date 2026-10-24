package slashcmd

import (
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
)

func TestParsePermissionModeAcceptsSupportedModes(t *testing.T) {
	tests := map[string]core.PermissionMode{
		"read-only":   core.PermissionReadOnly,
		"workspace":   core.PermissionWorkspace,
		"auto":        core.PermissionAuto,
		"full-access": core.PermissionFullAccess,
		" AUTO ":      core.PermissionAuto,
		"Workspace":   core.PermissionWorkspace,
	}
	for raw, want := range tests {
		got, err := ParsePermissionMode(raw)
		if err != nil {
			t.Fatalf("ParsePermissionMode(%q): %v", raw, err)
		}
		if got != want {
			t.Fatalf("ParsePermissionMode(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestParsePermissionModeRejectsLegacyAsk keeps the removed alias out of every
// frontend.
func TestParsePermissionModeRejectsLegacyAsk(t *testing.T) {
	if mode, err := ParsePermissionMode("ask"); err == nil {
		t.Fatalf("the legacy ask mode must be rejected, got %q", mode)
	}
}

func TestParsePermissionModeRejectsUnknownModes(t *testing.T) {
	for _, raw := range []string{"", "  ", "nonsense", "readonly", "full_access", "auto extra"} {
		if mode, err := ParsePermissionMode(raw); err == nil {
			t.Fatalf("ParsePermissionMode(%q) accepted %q", raw, mode)
		} else if !strings.Contains(err.Error(), "read-only, workspace, auto, full-access") {
			t.Fatalf("ParsePermissionMode(%q) error does not list the modes: %v", raw, err)
		}
	}
}

func TestPermissionModeNamesKeepDisplayOrder(t *testing.T) {
	want := []string{"read-only", "workspace", "auto", "full-access"}
	got := PermissionModeNames()
	if len(got) != len(want) {
		t.Fatalf("PermissionModeNames() = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("PermissionModeNames() = %v, want %v", got, want)
		}
	}
}
