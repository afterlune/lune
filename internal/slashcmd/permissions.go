package slashcmd

import (
	"fmt"
	"strings"

	core "github.com/afterlune/luneCore"
)

// permissionModes lists the accepted permission modes in display order. The
// legacy "ask" mode is deliberately absent: "auto" replaced it, and keeping the
// alias alive would keep the four frontends parsing modes themselves.
var permissionModes = []core.PermissionMode{
	core.PermissionReadOnly,
	core.PermissionWorkspace,
	core.PermissionAuto,
	core.PermissionFullAccess,
}

// PermissionModes returns the accepted permission modes in display order. Hosts
// render pickers from this list so their index order cannot drift from parsing.
func PermissionModes() []core.PermissionMode {
	return append([]core.PermissionMode(nil), permissionModes...)
}

// PermissionModeNames returns the accepted permission modes in display order.
func PermissionModeNames() []string {
	names := make([]string, 0, len(permissionModes))
	for _, mode := range permissionModes {
		names = append(names, string(mode))
	}
	return names
}

// ParsePermissionMode resolves a user-supplied permission mode. Callers share it
// so flags, the TUI, the CLI and the desktop accept exactly the same modes.
func ParsePermissionMode(raw string) (core.PermissionMode, error) {
	mode := core.PermissionMode(strings.ToLower(strings.TrimSpace(raw)))
	for _, known := range permissionModes {
		if mode == known {
			return known, nil
		}
	}
	return "", fmt.Errorf("unknown permission mode %q (use %s)", strings.TrimSpace(raw), strings.Join(PermissionModeNames(), ", "))
}

// PermissionModeUsage formats the modes for a usage or hint line.
func PermissionModeUsage() string {
	return strings.Join(PermissionModeNames(), ", ")
}
