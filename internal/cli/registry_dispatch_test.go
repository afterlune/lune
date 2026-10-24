package cli

import (
	"strings"
	"testing"
)

// TestCLIRejectsTUIOnlyCommandsFromMetadata keeps the frontend split in the
// shared registry: a command the registry lists only for the TUI must be refused
// instead of being submitted to the model as a prompt.
func TestCLIRejectsTUIOnlyCommandsFromMetadata(t *testing.T) {
	client := &fakeCLICore{}
	_, stderr := runCLI(t, client, "/thought\n/theme dark\n/exit\n", nil)
	if len(client.inputs) != 0 {
		t.Fatalf("inputs = %#v, want no prompt submitted for TUI-only commands", client.inputs)
	}
	for _, name := range []string{"/thought", "/theme"} {
		if !strings.Contains(stderr, name) {
			t.Fatalf("stderr = %q, want the refusal to name %s", stderr, name)
		}
	}
	if !strings.Contains(stderr, "only available in the full-screen TUI") {
		t.Fatalf("stderr = %q, want the shared registry wording", stderr)
	}
}
