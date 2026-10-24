package slashcmd

import (
	"sort"
	"strings"
	"testing"
)

func TestRegistryNamesAreUniqueAndSlashPrefixed(t *testing.T) {
	seen := map[string]bool{}
	for _, command := range All() {
		if !strings.HasPrefix(command.Name, "/") {
			t.Errorf("%q must start with a slash", command.Name)
		}
		if seen[command.Name] {
			t.Errorf("%q is registered twice", command.Name)
		}
		seen[command.Name] = true
		if command.Targets == 0 {
			t.Errorf("%q must target at least one frontend", command.Name)
		}
		if command.Summary == "" {
			t.Errorf("%q needs a TUI summary", command.Name)
		}
	}
}

func TestCLICommandsCarryUsageAndDescription(t *testing.T) {
	for _, command := range For(TargetCLI) {
		if !strings.HasPrefix(command.Usage, command.Name) {
			t.Errorf("%q usage %q must start with the command name", command.Name, command.Usage)
		}
		if strings.TrimSpace(command.Description) == "" {
			t.Errorf("%q needs a CLI description", command.Name)
		}
	}
}

// TestTargetDifferenceMatchesFrontends pins the only place the two frontends
// diverge: four TUI-only commands.
func TestTargetDifferenceMatchesFrontends(t *testing.T) {
	wantTUIOnly := []string{"/cancel-queue", "/shortcuts", "/theme", "/thought"}
	if got := names(difference(For(TargetTUI), For(TargetCLI))); !equal(got, wantTUIOnly) {
		t.Fatalf("TUI-only commands = %v, want %v", got, wantTUIOnly)
	}
	if got := difference(For(TargetCLI), For(TargetTUI)); len(got) != 0 {
		t.Fatalf("CLI must not advertise commands the TUI lacks, got %v", names(got))
	}
}

func TestNeedsSessionSetIsStable(t *testing.T) {
	want := []string{
		"/agents", "/compact", "/continue", "/detail", "/discard", "/effort", "/export",
		"/fork", "/logs", "/model", "/ps", "/review", "/rewind", "/stop", "/title",
	}
	var got []string
	for _, command := range All() {
		if command.NeedsSession {
			got = append(got, command.Name)
		}
	}
	sort.Strings(got)
	if !equal(got, want) {
		t.Fatalf("NeedsSession = %v, want %v", got, want)
	}
}

func TestDraftCommandsMatchTUIDraftGate(t *testing.T) {
	want := []string{"/agents", "/compact", "/continue", "/discard", "/export", "/fork", "/logs", "/ps", "/rewind", "/stop"}
	var got []string
	for _, command := range All() {
		if command.NeedsSession && !command.DraftOK {
			got = append(got, command.Name)
		}
	}
	sort.Strings(got)
	if !equal(got, want) {
		t.Fatalf("TUI draft-blocked commands = %v, want %v", got, want)
	}
}

func TestAvailableDuringTaskSetIsStable(t *testing.T) {
	want := []string{
		"/agents", "/cancel-queue", "/context", "/detail", "/diff", "/effort", "/goal", "/help",
		"/logs", "/mcp", "/memory", "/model", "/permissions", "/ps", "/sessions",
		"/shortcuts", "/status", "/stop", "/theme", "/thought", "/title",
	}
	var got []string
	for _, command := range All() {
		if command.AvailableDuringTask {
			got = append(got, command.Name)
		}
	}
	sort.Strings(got)
	if !equal(got, want) {
		t.Fatalf("AvailableDuringTask = %v, want %v", got, want)
	}
}

func TestLookupIgnoresCaseAndSurroundingText(t *testing.T) {
	for input, want := range map[string]string{
		"/model":           "/model",
		"  /Model gpt-5":   "/model",
		"/permissions ask": "/permissions",
		"/detail 3":        "/detail",
	} {
		command, ok := Lookup(input)
		if !ok || command.Name != want {
			t.Fatalf("Lookup(%q) = %q/%v, want %q", input, command.Name, ok, want)
		}
	}
	for _, input := range []string{"", "   ", "hello", "/unknown"} {
		if command, ok := Lookup(input); ok {
			t.Fatalf("Lookup(%q) = %q, want no match", input, command.Name)
		}
	}
}

func names(commands []Command) []string {
	out := make([]string, 0, len(commands))
	for _, command := range commands {
		out = append(out, command.Name)
	}
	sort.Strings(out)
	return out
}

func difference(all, subset []Command) []Command {
	keep := make(map[string]bool, len(subset))
	for _, command := range subset {
		keep[command.Name] = true
	}
	var out []Command
	for _, command := range all {
		if !keep[command.Name] {
			out = append(out, command)
		}
	}
	return out
}

func equal(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
