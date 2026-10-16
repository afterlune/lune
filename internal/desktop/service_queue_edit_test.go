package desktop

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

type immediateDesktopProvider struct{}

func (immediateDesktopProvider) Name() string { return "host" }

func (immediateDesktopProvider) CreateResponse(context.Context, publicmodel.ModelRequest, publicmodel.ModelProfile) (*publicmodel.ModelResponse, error) {
	return desktopTextResponse("edited response"), nil
}

func (p immediateDesktopProvider) StreamResponse(ctx context.Context, request publicmodel.ModelRequest, profile publicmodel.ModelProfile, emit func(publicmodel.ModelStreamEvent) error) (*publicmodel.ModelResponse, error) {
	response, err := p.CreateResponse(ctx, request, profile)
	if err != nil {
		return nil, err
	}
	if emit != nil {
		if err := emit(publicmodel.ModelStreamEvent{Type: publicmodel.ModelStreamCompleted, Response: response}); err != nil {
			return nil, err
		}
	}
	return response, nil
}

func desktopTextResponse(text string) *publicmodel.ModelResponse {
	return &publicmodel.ModelResponse{
		Status: "completed",
		Output: []publicmodel.ResponseOutputItem{publicmodel.OutputMessage{
			Role: publicmodel.RoleAssistant, Content: []publicmodel.OutputContentPart{publicmodel.OutputText{Text: text}},
		}},
	}
}

func newDesktopServiceForTest(t *testing.T, provider publicmodel.Provider) (*DesktopService, *core.Core, string) {
	t.Helper()
	workspace := t.TempDir()
	stateDir := filepath.Join(workspace, ".luneagent")
	agent, err := core.New(core.Config{
		Workspace: workspace,
		StateDir:  stateDir,
		Memory:    core.MemoryConfig{Disabled: true},
		ModelConfig: &publicmodel.Config{
			Providers:    map[string]publicmodel.ProviderConfig{"host": {Protocol: publicmodel.ProtocolResponses}},
			Models:       map[string]publicmodel.ModelProfile{"primary": {Provider: "host", Model: "test-model"}},
			DefaultModel: "primary",
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
	service := &DesktopService{core: agent, workspace: workspace, activityStore: activityStore, waiting: map[string]bool{}}
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
	return service, agent, saved.ID
}

func TestSubmitQueuesMessageWhileAnotherExecutionRuns(t *testing.T) {
	provider := &blockingDesktopProvider{started: make(chan struct{}, 3)}
	service, _, sessionID := newDesktopServiceForTest(t, provider)
	firstIndex := 0
	first, err := service.Submit(SubmitInput{Text: "first", UserMessageIndex: &firstIndex})
	if err != nil {
		t.Fatalf("Submit first message: %v", err)
	}
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first model request did not start")
	}
	secondIndex := 1
	second, err := service.Submit(SubmitInput{Text: "second", UserMessageIndex: &secondIndex})
	if err != nil {
		t.Fatalf("Submit second message while running: %v", err)
	}
	if second.Status != core.SubmissionQueued || second.ExecutionID == "" || second.Position < 1 {
		t.Fatalf("queued submission = %#v, want a queued execution behind the active turn", second)
	}
	state, err := service.runtime.State(context.Background())
	if err != nil {
		t.Fatalf("runtime.State: %v", err)
	}
	queuedFound := false
	for _, item := range state.Queue {
		if item.ExecutionID == second.ExecutionID && item.Status == "queued" {
			queuedFound = true
		}
	}
	if !queuedFound {
		t.Fatalf("runtime queue = %#v, want second execution queued after the active turn", state.Queue)
	}
	history, err := service.activityStore.ListSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("ListSession: %v", err)
	}
	var queued *ExecutionActivityView
	for index := range history {
		if history[index].ExecutionID == second.ExecutionID {
			queued = &history[index]
		}
	}
	if queued == nil || queued.UserMessageIndex != 1 || queued.MessageText != "second" || queued.Status != "queued" {
		t.Fatalf("queued message receipt = %#v, want persisted queued message at index 1", queued)
	}
	if !service.Interrupt() {
		t.Fatal("Interrupt returned false")
	}
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("queued message did not start after interrupting the first turn")
	}
	if first.ExecutionID == second.ExecutionID {
		t.Fatal("queued execution reused the active execution ID")
	}
}

func TestEditUserMessageRewindsFollowingTurnsAndTheirActivity(t *testing.T) {
	service, agent, sessionID := newDesktopServiceForTest(t, immediateDesktopProvider{})
	ctx := context.Background()
	history, err := json.Marshal(publicmodel.NewResponseInputItems(
		publicmodel.InputMessage{Role: publicmodel.RoleUser, Content: publicmodel.NewMessageText("first original")},
		publicmodel.InputMessage{Role: publicmodel.RoleAssistant, Content: publicmodel.NewMessageText("first answer")},
		publicmodel.InputMessage{Role: publicmodel.RoleUser, Content: publicmodel.NewMessageText("second original")},
		publicmodel.InputMessage{Role: publicmodel.RoleAssistant, Content: publicmodel.NewMessageText("second answer")},
	))
	if err != nil {
		t.Fatalf("marshal history: %v", err)
	}
	if err := agent.SetSessionHistory(ctx, sessionID, history); err != nil {
		t.Fatalf("SetSessionHistory: %v", err)
	}
	for index, executionID := range []string{"old-first", "old-second"} {
		if err := service.activityStore.UpsertExecution(ctx, sessionID, executionID, &index, "completed"); err != nil {
			t.Fatalf("UpsertExecution(%s): %v", executionID, err)
		}
	}
	messageID := transcriptMessages(history)[2].ID
	submission, err := service.EditUserMessage(messageID, SubmitInput{Text: "second revised"})
	if err != nil {
		t.Fatalf("EditUserMessage: %v", err)
	}
	ctxWait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if result, err := service.runtime.WaitTurn(ctxWait, submission.ExecutionID); err != nil || result.Status != core.TaskCompleted {
		t.Fatalf("edited turn result = %#v, error = %v", result, err)
	}
	saved, err := agent.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	messages := transcriptMessages(saved.History)
	if len(messages) != 4 || messages[0].Role != "user" || messages[0].Text != "first original" || messages[1].Role != "assistant" || messages[1].Text != "first answer" || messages[2].Role != "user" || messages[2].Text != "second revised" || messages[3].Role != "assistant" || messages[3].Text != "edited response" {
		t.Fatalf("history after edit = %#v, want the earlier turn preserved and selected turn replaced", messages)
	}
	activity, err := service.activityStore.ListSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("ListSession after edit: %v", err)
	}
	if len(activity) != 2 || activity[0].ExecutionID != "old-first" || activity[0].UserMessageIndex != 0 || activity[1].ExecutionID != submission.ExecutionID || activity[1].UserMessageIndex != 1 {
		t.Fatalf("activity after edit = %#v, want earlier activity and the replacement execution", activity)
	}
}

func TestEditUserMessageRejectsInvalidAndActiveTargetsWithoutChangingHistory(t *testing.T) {
	provider := &blockingDesktopProvider{started: make(chan struct{}, 2)}
	service, agent, sessionID := newDesktopServiceForTest(t, provider)
	original, _ := json.Marshal(publicmodel.NewResponseInputItems(publicmodel.InputMessage{
		Role: publicmodel.RoleUser, Content: publicmodel.NewMessageText("original"),
	}))
	if err := agent.SetSessionHistory(context.Background(), sessionID, original); err != nil {
		t.Fatalf("SetSessionHistory: %v", err)
	}
	messageID := transcriptMessages(original)[0].ID
	if _, err := service.EditUserMessage("missing-message", SubmitInput{Text: "invalid index"}); err == nil {
		t.Fatal("EditUserMessage accepted an unknown user message ID")
	}
	firstIndex := 1
	if _, err := service.Submit(SubmitInput{Text: "active", UserMessageIndex: &firstIndex}); err != nil {
		t.Fatalf("Submit active message: %v", err)
	}
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("model request did not start")
	}
	if _, err := service.EditUserMessage(messageID, SubmitInput{Text: "edited"}); err == nil {
		t.Fatal("EditUserMessage accepted while a task was active")
	}
	saved, err := agent.GetSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	messages := transcriptMessages(saved.History)
	if len(messages) != 1 || messages[0].Text != "original" {
		t.Fatalf("history changed after rejected edit: %#v", messages)
	}
}
