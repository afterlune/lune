package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	core "github.com/afterlune/luneCore"
)

func newLazySessionTestCore(t *testing.T) *core.Core {
	t.Helper()
	workspace := t.TempDir()
	config := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test"}},"profiles":{"primary":{"provider":"host","model":"primary-model"},"fast":{"provider":"host","model":"fast-model"}},"defaultProfile":"primary"},"runtime":{}}`
	if err := os.WriteFile(filepath.Join(workspace, "config.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Core: %v", err)
		}
	})
	return client
}

func sessionCount(t *testing.T, client *core.Core) int {
	t.Helper()
	sessions, err := client.ListSessions(context.Background(), 100, false)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	return len(sessions)
}

func TestNewSessionKeepsDraftUnpersisted(t *testing.T) {
	client := newLazySessionTestCore(t)
	m := newTUIModel(client, nil, "")

	mod, _ := m.handleSlashCommand("/new")
	m = mod.(*tuiModel)

	if m.sessionID != "" || m.runtime != nil {
		t.Fatalf("/new must stay a draft, got sessionID=%q runtime=%v", m.sessionID, m.runtime)
	}
	if got := sessionCount(t, client); got != 0 {
		t.Fatalf("/new persisted %d empty session(s)", got)
	}
}

func TestEnsureSessionCreatesOnceAndAppliesPendingChoices(t *testing.T) {
	client := newLazySessionTestCore(t)
	m := newTUIModel(client, nil, "")
	m.pendingTitle = "Deferred Title"
	m.pendingModelProfile = "fast"
	m.reasoningEffort = "high"

	if err := m.ensureSession(context.Background()); err != nil {
		t.Fatalf("ensureSession: %v", err)
	}
	if m.sessionID == "" || m.runtime == nil {
		t.Fatalf("expected the draft session to be created, got sessionID=%q runtime=%v", m.sessionID, m.runtime)
	}
	if got := sessionCount(t, client); got != 1 {
		t.Fatalf("expected exactly one session, got %d", got)
	}

	saved, err := client.GetSession(context.Background(), m.sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if saved.Title != "Deferred Title" {
		t.Fatalf("pending title not applied, got %q", saved.Title)
	}
	if saved.Settings.ModelProfile != "fast" {
		t.Fatalf("pending model profile not applied, got %q", saved.Settings.ModelProfile)
	}
	if saved.Settings.ReasoningEffort != "high" {
		t.Fatalf("pending reasoning effort not applied, got %q", saved.Settings.ReasoningEffort)
	}
	if m.pendingTitle != "" || m.pendingModelProfile != "" {
		t.Fatalf("pending choices must be cleared after creation")
	}

	if err := m.ensureSession(context.Background()); err != nil {
		t.Fatalf("second ensureSession: %v", err)
	}
	if got := sessionCount(t, client); got != 1 {
		t.Fatalf("ensureSession created a second session, got %d", got)
	}
}

func TestDraftSessionRejectsSessionCommands(t *testing.T) {
	client := newLazySessionTestCore(t)
	m := newTUIModel(client, nil, "")

	for _, command := range []string{"/rewind", "/fork", "/export", "/agents", "/compact", "/continue", "/discard"} {
		before := len(m.entries)
		mod, _ := m.handleSlashCommand(command)
		m = mod.(*tuiModel)
		if m.sessionID != "" {
			t.Fatalf("%s created a session", command)
		}
		if len(m.entries) <= before {
			t.Fatalf("%s produced no feedback", command)
		}
	}
	if got := sessionCount(t, client); got != 0 {
		t.Fatalf("draft commands persisted %d session(s)", got)
	}
}

func TestTitleOnDraftDefersUntilFirstMessage(t *testing.T) {
	client := newLazySessionTestCore(t)
	m := newTUIModel(client, nil, "")

	mod, _ := m.handleSlashCommand("/title Draft Name")
	m = mod.(*tuiModel)

	if m.sessionID != "" {
		t.Fatalf("/title created a session: %q", m.sessionID)
	}
	if m.pendingTitle != "Draft Name" {
		t.Fatalf("expected the title to be pending, got %q", m.pendingTitle)
	}
	if got := sessionCount(t, client); got != 0 {
		t.Fatalf("/title persisted %d session(s)", got)
	}
}

func TestModelSelectionOnDraftStaysPending(t *testing.T) {
	client := newLazySessionTestCore(t)
	m := newTUIModel(client, nil, "")

	mod, _ := m.handleSlashCommand("/model fast")
	m = mod.(*tuiModel)

	if m.sessionID != "" {
		t.Fatalf("/model created a session: %q", m.sessionID)
	}
	if m.pendingModelProfile != "fast" {
		t.Fatalf("expected the model profile to be pending, got %q", m.pendingModelProfile)
	}
	if got := sessionCount(t, client); got != 0 {
		t.Fatalf("/model persisted %d session(s)", got)
	}
}

func TestNewSessionCarriesOnlyExplicitModelChoice(t *testing.T) {
	client := newLazySessionTestCore(t)
	m := newTUIModel(client, nil, "")

	mod, _ := m.handleSlashCommand("/new")
	m = mod.(*tuiModel)
	if m.pendingModelProfile != "" {
		t.Fatalf("a bare /new must not invent a model profile, got %q", m.pendingModelProfile)
	}

	mod, _ = m.handleSlashCommand("/model fast")
	m = mod.(*tuiModel)
	mod, _ = m.handleSlashCommand("/new")
	m = mod.(*tuiModel)
	if m.pendingModelProfile != "fast" {
		t.Fatalf("expected an explicit /model choice to survive /new, got %q", m.pendingModelProfile)
	}
}
