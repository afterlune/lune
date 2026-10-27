package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
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

type firstTurnStreamProvider struct {
	started  chan struct{}
	release  chan struct{}
	finished chan struct{}
	start    sync.Once
}

func (*firstTurnStreamProvider) Name() string { return "host" }

func (p *firstTurnStreamProvider) CreateResponse(context.Context, publicmodel.ModelRequest, publicmodel.ModelProfile) (*publicmodel.ModelResponse, error) {
	return firstTurnStreamResponse(), nil
}

func (p *firstTurnStreamProvider) StreamResponse(ctx context.Context, _ publicmodel.ModelRequest, _ publicmodel.ModelProfile, emit func(publicmodel.ModelStreamEvent) error) (*publicmodel.ModelResponse, error) {
	if err := emit(publicmodel.ModelStreamEvent{Type: publicmodel.ModelStreamTextDelta, Delta: "first live token"}); err != nil {
		return nil, err
	}
	p.start.Do(func() { close(p.started) })
	select {
	case <-p.release:
		close(p.finished)
		return firstTurnStreamResponse(), nil
	case <-ctx.Done():
		close(p.finished)
		return nil, ctx.Err()
	}
}

func firstTurnStreamResponse() *publicmodel.ModelResponse {
	return &publicmodel.ModelResponse{
		Status: "completed",
		Output: []publicmodel.ResponseOutputItem{publicmodel.OutputMessage{
			Role:    publicmodel.RoleAssistant,
			Content: []publicmodel.OutputContentPart{publicmodel.OutputText{Text: "first live token"}},
		}},
	}
}

func TestFirstLazySessionTurnSubscribesBeforeLiveOutput(t *testing.T) {
	provider := &firstTurnStreamProvider{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		finished: make(chan struct{}),
	}
	release := sync.OnceFunc(func() { close(provider.release) })
	t.Cleanup(release)
	client, err := core.New(core.Config{
		Workspace: t.TempDir(), Memory: core.MemoryConfig{Disabled: true},
		ModelConfig: &publicmodel.Config{
			Providers: map[string]publicmodel.ProviderConfig{
				"host": {Protocol: "responses", BaseURL: "https://example.invalid"},
			},
			Profiles: map[string]publicmodel.ModelProfile{
				"main": {Provider: "host", Model: "test-model"},
			},
			DefaultProfile: "main",
		},
		ModelProviders: map[string]publicmodel.Provider{"host": provider},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Core: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	m := newTUIModel(client, nil, "")
	mod, submit := m.submitTurn("hello")
	m = mod.(*tuiModel)
	if m.sessionID == "" || m.runtime == nil || m.run.runtimeCancel == nil {
		t.Fatalf("first prompt did not create and subscribe its session: sessionID=%q runtime=%v cancel=%v", m.sessionID, m.runtime, m.run.runtimeCancel != nil)
	}
	commandsMsg := submit()
	commands, ok := commandsMsg.(tea.BatchMsg)
	if !ok || len(commands) != 3 {
		t.Fatalf("expected subscription, clock, and submission commands; got %T (%v)", commandsMsg, commandsMsg)
	}

	ready, ok := commands[0]().(runtimeStreamReadyMsg)
	if !ok || ready.err != nil || ready.events == nil {
		t.Fatalf("runtime event subscription was not ready before submission: %#v", ready)
	}
	mod, _ = m.Update(ready)
	m = mod.(*tuiModel)

	submitted, ok := commands[2]().(turnSubmittedMsg)
	if !ok || submitted.err != nil {
		t.Fatalf("first turn submission failed: %#v", submitted)
	}
	mod, _ = m.Update(submitted)
	m = mod.(*tuiModel)

	select {
	case <-provider.started:
	case <-ctx.Done():
		t.Fatal("model did not begin streaming")
	}

	events := ready.events
	for !strings.Contains(m.run.currentStreamText, "first live token") {
		msgCh := make(chan tea.Msg, 1)
		go func() { msgCh <- nextRuntimeEvent(m.sessionID, m.run.subscriptionGeneration, events)() }()
		select {
		case got := <-msgCh:
			eventMsg, ok := got.(runtimeEventMsg)
			if !ok {
				t.Fatalf("unexpected runtime event message %T", got)
			}
			mod, _ = m.Update(eventMsg)
			m = mod.(*tuiModel)
			events = eventMsg.events
		case <-ctx.Done():
			t.Fatalf("timed out waiting for first live token; current stream=%q", m.run.currentStreamText)
		}
	}
	select {
	case <-provider.finished:
		t.Fatal("model completed before the live token was observed")
	default:
	}

	release()
	if result, err := m.runtime.WaitTurn(ctx, submitted.submission.ExecutionID); err != nil || result.Status != core.TaskCompleted {
		t.Fatalf("first turn did not finish after releasing the provider: %#v, %v", result, err)
	}
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

// A rejected draft-time choice must not leave an empty session behind, and the
// pending values have to survive so a later attempt can still apply them.
func TestEnsureSessionRollsBackRejectedDraftSettings(t *testing.T) {
	client := newLazySessionTestCore(t)
	m := newTUIModel(client, nil, "")
	m.pendingTitle = "Deferred Title"
	m.pendingModelProfile = "missing"

	if err := m.ensureSession(context.Background()); err == nil {
		t.Fatal("expected the unknown model profile to fail the draft creation")
	}
	if got := sessionCount(t, client); got != 0 {
		t.Fatalf("failed draft creation left %d session(s) behind", got)
	}
	if m.sessionID != "" || m.runtime != nil {
		t.Fatalf("failed draft creation stayed bound, sessionID=%q runtime=%v", m.sessionID, m.runtime)
	}
	if m.pendingTitle != "Deferred Title" || m.pendingModelProfile != "missing" {
		t.Fatalf("pending choices were dropped, title=%q profile=%q", m.pendingTitle, m.pendingModelProfile)
	}

	m.pendingModelProfile = "fast"
	if err := m.ensureSession(context.Background()); err != nil {
		t.Fatalf("retry after fixing the profile: %v", err)
	}
	saved, err := client.GetSession(context.Background(), m.sessionID)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if saved.Title != "Deferred Title" || saved.Settings.ModelProfile != "fast" {
		t.Fatalf("retry did not apply the deferred choices: %+v", saved.Settings)
	}
	if got := sessionCount(t, client); got != 1 {
		t.Fatalf("expected one session after the retry, got %d", got)
	}
}

func TestDraftSessionRejectsSessionCommands(t *testing.T) {
	client := newLazySessionTestCore(t)
	m := newTUIModel(client, nil, "")

	for _, command := range []string{"/rewind", "/fork", "/export", "/agents", "/compact"} {
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
