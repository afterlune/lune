package app

import (
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/slashcmd"
)

// TestHelpUsesSharedPermissionModeList keeps the help text on the same source as
// the parser: the four modes come from slashcmd, not a second hardcoded list.
func TestHelpUsesSharedPermissionModeList(t *testing.T) {
	var out strings.Builder
	PrintHelp(&out)

	want := slashcmd.PermissionModeUsage()
	permissionLine := ""
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "--permission") {
			permissionLine = line
			break
		}
	}
	if permissionLine == "" {
		t.Fatalf("help has no --permission line:\n%s", out.String())
	}
	if !strings.Contains(permissionLine, want) {
		t.Fatalf("permission line = %q, want it to contain %q", permissionLine, want)
	}
	if strings.Contains(permissionLine, "ask") {
		t.Fatalf("permission line still mentions the removed ask mode: %q", permissionLine)
	}
}
