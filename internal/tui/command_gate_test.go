package tui

import (
	"testing"

	"github.com/afterlune/lune/internal/slashcmd"
)

// TestSlashCommandUnavailableUsesRegistryText keeps the running-task interlock in
// one place: the message a blocked command shows comes from the shared registry.
func TestSlashCommandUnavailableUsesRegistryText(t *testing.T) {
	blocked := []string{"/rewind", "/resume", "/drop", "/new", "/fork", "/continue", "/discard", "/exit"}
	for _, name := range blocked {
		spec, ok := slashcmd.Find(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		if spec.BlockedDuringTask == "" {
			t.Fatalf("%s must carry BlockedDuringTask", name)
		}
		m := newTUIModel(nil, nil, "session")
		m.run.running = true
		if got := m.slashCommandUnavailable(name); got != spec.BlockedDuringTask {
			t.Fatalf("%s message = %q, want %q", name, got, spec.BlockedDuringTask)
		}
	}
}

// TestSlashCommandUnavailableKeepsAvailableCommands guards the metadata contract:
// a command marked available during a task is never blocked by the generic gate.
func TestSlashCommandUnavailableKeepsAvailableCommands(t *testing.T) {
	m := newTUIModel(nil, nil, "session")
	m.run.running = true
	for _, name := range []string{"/permissions", "/help", "/status", "/mcp"} {
		spec, ok := slashcmd.Find(name)
		if !ok || !spec.AvailableDuringTask {
			t.Fatalf("%s must stay available during a task", name)
		}
		if got := m.slashCommandUnavailable(name); got != "" {
			t.Fatalf("%s was blocked with %q", name, got)
		}
	}
}
