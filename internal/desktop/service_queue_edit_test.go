package desktop

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
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

func desktopLatestUserInput(input *publicmodel.ResponseInput) string {
	if input == nil {
		return ""
	}
	if input.Text != nil {
		return *input.Text
	}
	for index := len(input.Items) - 1; index >= 0; index-- {
		message, ok := input.Items[index].(publicmodel.InputMessage)
		if !ok || message.Role != publicmodel.RoleUser {
			continue
		}
		if message.Content.Text != nil {
			return *message.Content.Text
		}
		var text string
		for _, part := range message.Content.Parts {
			if value, ok := part.(publicmodel.InputText); ok {
				text += value.Text
			}
		}
		return text
	}
	return ""
}

type gatedDesktopProvider struct {
	entered chan string
	release chan struct{}
}

type planThenCompleteDesktopProvider struct{ calls atomic.Int32 }

func (p *planThenCompleteDesktopProvider) Name() string { return "host" }

func (p *planThenCompleteDesktopProvider) CreateResponse(ctx context.Context, _ publicmodel.ModelRequest, _ publicmodel.ModelProfile) (*publicmodel.ModelResponse, error) {
	return p.respond(ctx, nil)
}

func (p *planThenCompleteDesktopProvider) StreamResponse(ctx context.Context, _ publicmodel.ModelRequest, _ publicmodel.ModelProfile, emit func(publicmodel.ModelStreamEvent) error) (*publicmodel.ModelResponse, error) {
	return p.respond(ctx, emit)
}

func (p *planThenCompleteDesktopProvider) respond(ctx context.Context, emit func(publicmodel.ModelStreamEvent) error) (*publicmodel.ModelResponse, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	text := "实现已完成。"
	if p.calls.Add(1) == 1 {
		text = "<proposed_plan>\n## 实施方案\n- 完成用户批准的改动。\n</proposed_plan>"
	}
	response := desktopTextResponse(text)
	if emit != nil {
		if err := emit(publicmodel.ModelStreamEvent{Type: publicmodel.ModelStreamCompleted, Response: response}); err != nil {
			return nil, err
		}
	}
	return response, nil
}

func (p *gatedDesktopProvider) Name() string { return "host" }

func (p *gatedDesktopProvider) CreateResponse(ctx context.Context, request publicmodel.ModelRequest, _ publicmodel.ModelProfile) (*publicmodel.ModelResponse, error) {
	return p.respond(ctx, request, nil)
}

func (p *gatedDesktopProvider) StreamResponse(ctx context.Context, request publicmodel.ModelRequest, _ publicmodel.ModelProfile, emit func(publicmodel.ModelStreamEvent) error) (*publicmodel.ModelResponse, error) {
	return p.respond(ctx, request, emit)
}

func (p *gatedDesktopProvider) respond(ctx context.Context, request publicmodel.ModelRequest, emit func(publicmodel.ModelStreamEvent) error) (*publicmodel.ModelResponse, error) {
	select {
	case p.entered <- desktopLatestUserInput(request.Input):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	response := desktopTextResponse("edited response")
	if emit != nil {
		if err := emit(publicmodel.ModelStreamEvent{Type: publicmodel.ModelStreamCompleted, Response: response}); err != nil {
			return nil, err
		}
	}
	return response, nil
}

type interruptThenCompleteDesktopProvider struct {
	calls   int
	started chan string
}

func (p *interruptThenCompleteDesktopProvider) Name() string { return "host" }

func (p *interruptThenCompleteDesktopProvider) CreateResponse(ctx context.Context, request publicmodel.ModelRequest, _ publicmodel.ModelProfile) (*publicmodel.ModelResponse, error) {
	return p.respond(ctx, request, nil)
}

func (p *interruptThenCompleteDesktopProvider) StreamResponse(ctx context.Context, request publicmodel.ModelRequest, _ publicmodel.ModelProfile, emit func(publicmodel.ModelStreamEvent) error) (*publicmodel.ModelResponse, error) {
	return p.respond(ctx, request, emit)
}

func (p *interruptThenCompleteDesktopProvider) respond(ctx context.Context, request publicmodel.ModelRequest, emit func(publicmodel.ModelStreamEvent) error) (*publicmodel.ModelResponse, error) {
	p.calls++
	if p.calls == 1 {
		select {
		case p.started <- desktopLatestUserInput(request.Input):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	response := desktopTextResponse("replacement response")
	if emit != nil {
		if err := emit(publicmodel.ModelStreamEvent{Type: publicmodel.ModelStreamCompleted, Response: response}); err != nil {
			return nil, err
		}
	}
	return response, nil
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
	service := &DesktopService{localCore: agent, workspace: workspace, activityStore: activityStore, waiting: map[string]bool{}}
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
	first, err := service.Submit(service.activeSessionIDLocked(), SubmitInput{Text: "first", UserMessageIndex: &firstIndex})
	if err != nil {
		t.Fatalf("Submit first message: %v", err)
	}
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first model request did not start")
	}
	secondIndex := 1
	second, err := service.Submit(service.activeSessionIDLocked(), SubmitInput{Text: "second", UserMessageIndex: &secondIndex})
	if err != nil {
		t.Fatalf("Submit second message while running: %v", err)
	}
	if second.Status != core.SubmissionQueued || second.ExecutionID == "" || second.Position < 1 {
		t.Fatalf("queued submission = %#v, want a queued execution behind the active turn", second)
	}
	state, err := service.activeContext.runtime.State(context.Background())
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
	if !service.Interrupt(service.activeSessionIDLocked(), desktopTestExecutionID(service)) {
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

func TestSeparateSessionRuntimesCanRunInParallelAndSwitchWhileRunning(t *testing.T) {
	provider := &gatedDesktopProvider{entered: make(chan string, 2), release: make(chan struct{})}
	service, _, localSessionID := newDesktopServiceForTest(t, provider)
	worktreeWorkspace := t.TempDir()
	worktreeStateDir := filepath.Join(t.TempDir(), "worktree-state")
	worktreeCore, err := core.New(core.Config{
		Workspace: worktreeWorkspace, StateDir: worktreeStateDir, Memory: core.MemoryConfig{Disabled: true},
		ModelConfig: &publicmodel.Config{
			Providers:    map[string]publicmodel.ProviderConfig{"host": {Protocol: publicmodel.ProtocolResponses}},
			Models:       map[string]publicmodel.ModelProfile{"primary": {Provider: "host", Model: "test-model"}},
			DefaultModel: "primary",
		},
		ModelProviders: map[string]publicmodel.Provider{"host": provider},
	})
	if err != nil {
		t.Fatal(err)
	}
	worktreeSession, err := worktreeCore.CreateSession(context.Background(), nil)
	if err != nil {
		_ = worktreeCore.Close()
		t.Fatal(err)
	}
	worktreeContext := &desktopSessionContext{
		core: worktreeCore, sessionID: worktreeSession.ID, workspace: worktreeWorkspace,
		mode: workspaceModeWorktree, stateDir: worktreeStateDir, worktreePath: worktreeWorkspace,
	}
	if err := service.attachSessionRuntime(worktreeContext); err != nil {
		_ = worktreeCore.Close()
		t.Fatal(err)
	}

	localIndex, worktreeIndex := 0, 0
	if _, err := service.Submit(service.activeSessionIDLocked(), SubmitInput{Text: "local task", UserMessageIndex: &localIndex}); err != nil {
		t.Fatalf("submit local task: %v", err)
	}
	if _, err := service.OpenSession(worktreeSession.ID); err != nil {
		t.Fatalf("switch to running worktree session: %v", err)
	}
	if _, err := service.Submit(service.activeSessionIDLocked(), SubmitInput{Text: "worktree task", UserMessageIndex: &worktreeIndex}); err != nil {
		t.Fatalf("submit worktree task: %v", err)
	}
	seen := map[string]bool{}
	for range 2 {
		select {
		case input := <-provider.entered:
			seen[input] = true
		case <-time.After(5 * time.Second):
			t.Fatal("both session runtimes did not reach the provider concurrently")
		}
	}
	if !seen["local task"] || !seen["worktree task"] {
		t.Fatalf("provider requests = %#v", seen)
	}
	if _, err := service.OpenSession(localSessionID); err != nil {
		t.Fatalf("switch back while tasks are running: %v", err)
	}
	localState, err := service.sessionContexts[localSessionID].runtime.State(context.Background())
	if err != nil || localState.Task.Status != core.TaskRunning {
		t.Fatalf("local task state after switching = %#v, error=%v", localState.Task, err)
	}
	worktreeState, err := service.sessionContexts[worktreeSession.ID].runtime.State(context.Background())
	if err != nil || worktreeState.Task.Status != core.TaskRunning {
		t.Fatalf("worktree task state after switching = %#v, error=%v", worktreeState.Task, err)
	}

	close(provider.release)
	for _, sessionID := range []string{localSessionID, worktreeSession.ID} {
		runtime := service.sessionContexts[sessionID].runtime
		state, err := runtime.State(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.WaitTurn(context.Background(), state.Task.ExecutionID); err != nil {
			t.Fatalf("wait for session %q: %v", sessionID, err)
		}
	}
}

func TestWorktreePlanNewContextRegistersSiblingInSharedCore(t *testing.T) {
	provider := &planThenCompleteDesktopProvider{}
	service, agent, sessionID := newDesktopServiceForTest(t, provider)
	source := service.sessionContexts[sessionID]
	source.mode = workspaceModeWorktree
	source.worktreePath = source.workspace
	source.repositoryRoot = source.workspace
	if err := service.activityStore.SaveDesktopSession(context.Background(), DesktopSessionRecord{
		ID: sessionID, Mode: workspaceModeWorktree, Workspace: source.workspace, StateDir: source.stateDir,
		WorktreePath: source.worktreePath, RepositoryRoot: source.repositoryRoot, Title: "plan source", Status: "idle",
	}); err != nil {
		t.Fatal(err)
	}
	service.localCore = agent
	if _, err := service.SetPlanMode(service.activeSessionIDLocked(), true); err != nil {
		t.Fatal(err)
	}
	index := 0
	submission, err := service.Submit(service.activeSessionIDLocked(), SubmitInput{Text: "plan the change", UserMessageIndex: &index})
	if err != nil {
		t.Fatal(err)
	}
	result, err := source.runtime.WaitTurn(context.Background(), submission.ExecutionID)
	if err != nil || result.Status != core.TaskCompleted || result.ProposedPlan == nil {
		t.Fatalf("plan result = %#v, error=%v", result, err)
	}
	implementation, err := service.ImplementPlan(service.activeSessionIDLocked(), submission.ExecutionID, string(core.PlanImplementNewContext))
	if err != nil {
		t.Fatal(err)
	}
	if implementation.SessionID == sessionID {
		t.Fatal("new-context plan implementation reused the source session")
	}
	child := service.sessionContexts[implementation.SessionID]
	if child == nil || child.mode != workspaceModeWorktree || child.core != source.core || child.stateDir != source.stateDir {
		t.Fatalf("plan child was not registered in its source Worktree Core: %#v", child)
	}
	registered, found, err := service.activityStore.DesktopSession(context.Background(), implementation.SessionID)
	if err != nil || !found || registered.StateDir != source.stateDir || registered.WorktreePath != source.worktreePath {
		t.Fatalf("Worktree child registry = %#v, found=%t, error=%v", registered, found, err)
	}
	if _, err := service.OpenSession(implementation.SessionID); err != nil {
		t.Fatalf("open Worktree child: %v", err)
	}
	if _, err := child.runtime.WaitTurn(context.Background(), implementation.ExecutionID); err != nil {
		t.Fatalf("wait for Worktree child implementation: %v", err)
	}
}

func TestEditQueuedMessageReplacesItsPositionAndCancelsLaterQueue(t *testing.T) {
	provider := &gatedDesktopProvider{entered: make(chan string, 3), release: make(chan struct{}, 3)}
	service, _, sessionID := newDesktopServiceForTest(t, provider)
	firstIndex := 0
	first, err := service.Submit(service.activeSessionIDLocked(), SubmitInput{Text: "first", UserMessageIndex: &firstIndex})
	if err != nil {
		t.Fatal(err)
	}
	if got := <-provider.entered; got != "first" {
		t.Fatalf("first provider input = %q", got)
	}
	secondIndex := 1
	second, err := service.Submit(service.activeSessionIDLocked(), SubmitInput{Text: "second original", UserMessageIndex: &secondIndex})
	if err != nil {
		t.Fatal(err)
	}
	thirdIndex := 2
	third, err := service.Submit(service.activeSessionIDLocked(), SubmitInput{Text: "third original", UserMessageIndex: &thirdIndex})
	if err != nil {
		t.Fatal(err)
	}

	revised, err := service.EditUserMessage(service.activeSessionIDLocked(), second.ExecutionID, SubmitInput{Text: "second revised", UserMessageIndex: &secondIndex})
	if err != nil {
		t.Fatalf("EditUserMessage(queued): %v", err)
	}
	if revised.Status != core.SubmissionQueued || revised.ExecutionID != second.ExecutionID || revised.Position != second.Position {
		t.Fatalf("revised queued submission = %#v, want original queue identity and position %#v", revised, second)
	}
	state, err := service.activeContext.runtime.State(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Queue) != 2 || state.Queue[0].ExecutionID != first.ExecutionID || state.Queue[1].ExecutionID != second.ExecutionID {
		t.Fatalf("queue after edit = %#v, want active first and revised second only", state.Queue)
	}
	history, err := service.activityStore.ListSession(context.Background(), sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var editedMessage *ExecutionActivityView
	for index := range history {
		if history[index].ExecutionID == second.ExecutionID {
			editedMessage = &history[index]
		}
		if history[index].ExecutionID == third.ExecutionID {
			t.Fatalf("later queued activity was retained: %#v", history[index])
		}
	}
	if editedMessage == nil || editedMessage.MessageText != "second revised" || editedMessage.Status != "queued" {
		t.Fatalf("edited queued activity = %#v", editedMessage)
	}
	if result, err := service.activeContext.runtime.WaitTurn(context.Background(), third.ExecutionID); err != nil || result.Status != core.TaskCancelled {
		t.Fatalf("later queued result = %#v, error=%v; want cancelled", result, err)
	}

	provider.release <- struct{}{}
	select {
	case got := <-provider.entered:
		if got != "second revised" {
			t.Fatalf("next provider input = %q, want revised queued message", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("edited queued message did not start")
	}
	provider.release <- struct{}{}
	if result, err := service.activeContext.runtime.WaitTurn(context.Background(), first.ExecutionID); err != nil || result.Status != core.TaskCompleted {
		t.Fatalf("first result = %#v, error=%v", result, err)
	}
	if result, err := service.activeContext.runtime.WaitTurn(context.Background(), second.ExecutionID); err != nil || result.Status != core.TaskCompleted {
		t.Fatalf("edited queued result = %#v, error=%v", result, err)
	}
}

func TestEditHistoricalMessageInterruptsCurrentTurnAndDropsQueue(t *testing.T) {
	provider := &interruptThenCompleteDesktopProvider{started: make(chan string, 2)}
	service, agent, sessionID := newDesktopServiceForTest(t, provider)
	ctx := context.Background()
	originalHistory, err := json.Marshal(publicmodel.NewResponseInputItems(
		publicmodel.InputMessage{Role: publicmodel.RoleUser, Content: publicmodel.NewMessageText("first original")},
		publicmodel.InputMessage{Role: publicmodel.RoleAssistant, Content: publicmodel.NewMessageText("first answer")},
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.SetSessionHistory(ctx, sessionID, originalHistory); err != nil {
		t.Fatal(err)
	}
	messageID := transcriptMessages(originalHistory)[0].ID
	activeIndex := 1
	active, err := service.Submit(service.activeSessionIDLocked(), SubmitInput{Text: "active work", UserMessageIndex: &activeIndex})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("active task did not start")
	}
	queuedIndex := 2
	queued, err := service.Submit(service.activeSessionIDLocked(), SubmitInput{Text: "queued work", UserMessageIndex: &queuedIndex})
	if err != nil {
		t.Fatal(err)
	}
	targetIndex := 0
	replacement, err := service.EditUserMessage(service.activeSessionIDLocked(), messageID, SubmitInput{Text: "first revised", UserMessageIndex: &targetIndex})
	if err != nil {
		t.Fatalf("EditUserMessage(active history): %v", err)
	}
	if result, err := service.activeContext.runtime.WaitTurn(ctx, replacement.ExecutionID); err != nil || result.Status != core.TaskCompleted {
		t.Fatalf("replacement result = %#v, error=%v", result, err)
	}
	if result, err := service.activeContext.runtime.WaitTurn(ctx, queued.ExecutionID); err != nil || result.Status != core.TaskCancelled {
		t.Fatalf("queued result = %#v, error=%v; want cancelled", result, err)
	}
	if _, err := service.activeContext.runtime.WaitTurn(ctx, active.ExecutionID); err != nil {
		t.Fatalf("interrupted active result: %v", err)
	}
	saved, err := agent.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	messages := transcriptMessages(saved.History)
	if len(messages) != 2 || messages[0].Role != "user" || messages[0].Text != "first revised" || messages[1].Role != "assistant" || messages[1].Text != "replacement response" {
		t.Fatalf("history after active edit = %#v, want only the edited turn and its response", messages)
	}
}

func TestEditActiveUncommittedMessageInterruptsAndReplacesItsTurn(t *testing.T) {
	provider := &interruptThenCompleteDesktopProvider{started: make(chan string, 2)}
	service, agent, sessionID := newDesktopServiceForTest(t, provider)
	ctx := context.Background()
	activeIndex := 0
	active, err := service.Submit(service.activeSessionIDLocked(), SubmitInput{Text: "active original", UserMessageIndex: &activeIndex})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("active task did not start")
	}
	queuedIndex := 1
	queued, err := service.Submit(service.activeSessionIDLocked(), SubmitInput{Text: "queued follow-up", UserMessageIndex: &queuedIndex})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := service.EditUserMessage(service.activeSessionIDLocked(), active.ExecutionID, SubmitInput{Text: "active revised", UserMessageIndex: &activeIndex})
	if err != nil {
		t.Fatalf("EditUserMessage(active execution): %v", err)
	}
	if result, err := service.activeContext.runtime.WaitTurn(ctx, replacement.ExecutionID); err != nil || result.Status != core.TaskCompleted {
		t.Fatalf("replacement result = %#v, error=%v", result, err)
	}
	if result, err := service.activeContext.runtime.WaitTurn(ctx, queued.ExecutionID); err != nil || result.Status != core.TaskCancelled {
		t.Fatalf("queued result = %#v, error=%v; want cancelled", result, err)
	}
	if _, err := service.activeContext.runtime.WaitTurn(ctx, active.ExecutionID); err != nil {
		t.Fatalf("interrupted active result: %v", err)
	}
	saved, err := agent.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	messages := transcriptMessages(saved.History)
	if len(messages) != 2 || messages[0].Role != "user" || messages[0].Text != "active revised" || messages[1].Role != "assistant" || messages[1].Text != "replacement response" {
		t.Fatalf("history after active prompt edit = %#v, want only replacement turn", messages)
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
	submission, err := service.EditUserMessage(service.activeSessionIDLocked(), messageID, SubmitInput{Text: "second revised"})
	if err != nil {
		t.Fatalf("EditUserMessage: %v", err)
	}
	ctxWait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if result, err := service.activeContext.runtime.WaitTurn(ctxWait, submission.ExecutionID); err != nil || result.Status != core.TaskCompleted {
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

func TestEditUserMessageRejectsInvalidTargetsWithoutChangingHistory(t *testing.T) {
	provider := &blockingDesktopProvider{started: make(chan struct{}, 2)}
	service, agent, sessionID := newDesktopServiceForTest(t, provider)
	original, _ := json.Marshal(publicmodel.NewResponseInputItems(publicmodel.InputMessage{
		Role: publicmodel.RoleUser, Content: publicmodel.NewMessageText("original"),
	}))
	if err := agent.SetSessionHistory(context.Background(), sessionID, original); err != nil {
		t.Fatalf("SetSessionHistory: %v", err)
	}
	if _, err := service.EditUserMessage(service.activeSessionIDLocked(), "missing-message", SubmitInput{Text: "invalid index"}); err == nil {
		t.Fatal("EditUserMessage accepted an unknown user message ID")
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

func desktopTestExecutionID(s *DesktopService) string {
	state, err := s.activeContext.runtime.State(context.Background())
	if err != nil {
		return ""
	}
	return state.Task.ExecutionID
}
