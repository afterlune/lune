package desktop

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

type blockingDesktopProvider struct {
	started chan struct{}
}

func (p *blockingDesktopProvider) Name() string { return "host" }

func (p *blockingDesktopProvider) CreateResponse(ctx context.Context, _ publicmodel.ModelRequest, _ publicmodel.ModelProfile) (*publicmodel.ModelResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *blockingDesktopProvider) StreamResponse(ctx context.Context, _ publicmodel.ModelRequest, _ publicmodel.ModelProfile, _ func(publicmodel.ModelStreamEvent) error) (*publicmodel.ModelResponse, error) {
	select {
	case p.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestInterruptCancelsExecutionAndAllowsNewMessage(t *testing.T) {
	workspace := t.TempDir()
	stateDir := filepath.Join(workspace, ".luneagent")
	provider := &blockingDesktopProvider{started: make(chan struct{}, 2)}
	agent, err := core.New(core.Config{
		Workspace: workspace,
		StateDir:  stateDir,
		Memory:    core.MemoryConfig{Disabled: true},
		ModelConfig: &publicmodel.Config{
			Providers:      map[string]publicmodel.ProviderConfig{"host": {Protocol: publicmodel.ProtocolResponses}},
			Profiles:       map[string]publicmodel.ModelProfile{"primary": {Provider: "host", Model: "test-model"}},
			DefaultProfile: "primary",
		},
		ModelProviders: map[string]publicmodel.Provider{"host": provider},
	})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	activityStore, err := OpenActivityStore(stateDir)
	if err != nil {
		_ = agent.Close()
		t.Fatalf("OpenActivityStore: %v", err)
	}
	service := &DesktopService{
		localCore: agent, workspace: workspace, activityStore: activityStore, waiting: map[string]bool{},
	}
	t.Cleanup(func() {
		if err := service.CloseWorkspace(true); err != nil {
			t.Errorf("CloseWorkspace: %v", err)
		}
	})

	saved, err := agent.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := service.OpenSession(saved.ID); err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	firstMessageIndex := 0
	first, err := service.Submit(service.activeSessionIDLocked(), SubmitInput{Text: "inspect", UserMessageIndex: &firstMessageIndex})
	if err != nil {
		t.Fatalf("Submit initial message: %v", err)
	}
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("model provider did not start")
	}

	if !service.Interrupt(service.activeSessionIDLocked(), desktopTestExecutionID(service)) {
		t.Fatal("Interrupt returned false")
	}
	state, err := service.activeContext.runtime.State(context.Background())
	if err != nil {
		t.Fatalf("runtime.State after interrupt: %v", err)
	}
	if state.Task.Status != core.TaskCancelled {
		t.Fatalf("task status after Interrupt = %q, want cancelled", state.Task.Status)
	}

	history, err := activityStore.ListSession(context.Background(), saved.ID)
	if err != nil {
		t.Fatalf("ListSession after interrupt: %v", err)
	}
	if len(history) != 1 || history[0].ExecutionID != first.ExecutionID || history[0].UserMessageIndex != 0 || history[0].Status != "interrupted" {
		t.Fatalf("interrupted execution history = %#v", history)
	}

	secondMessageIndex := 1
	second, err := service.Submit(service.activeSessionIDLocked(), SubmitInput{Text: "continue", UserMessageIndex: &secondMessageIndex})
	if err != nil {
		t.Fatalf("Submit follow-up after interrupt: %v", err)
	}
	if second.ExecutionID == first.ExecutionID {
		t.Fatalf("follow-up reused interrupted execution ID %q", second.ExecutionID)
	}
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("follow-up model provider did not start")
	}
}
