package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
	"github.com/charmbracelet/x/ansi"
)

func questionFixture() core.UserQuestionRequest {
	return core.UserQuestionRequest{
		ID: "call", ExecutionID: "execution", RunID: "run", InvocationID: "invocation", CallID: "call",
		Questions: []core.UserQuestionItem{{ID: "q1", Question: "Which scope?", Options: []core.UserQuestionOption{{Label: "TUI"}, {Label: "Core"}}}},
	}
}

func TestQuestionSubmissionRetainsAnswersAndRetriesWithoutDuplicates(t *testing.T) {
	m := newTUIModel(nil, nil, "session")
	request := questionFixture()
	m.applyPendingRequests([]core.PendingRequest{{Kind: core.PendingUserQuestion, UserQuestion: &request}})
	m.activeQuestion.currentDraft().Selected = 1
	m.activeQuestion.currentDraft().NoteText = "keep this note"
	_, cmd := m.resolveActiveQuestion()
	if cmd == nil || !m.activeQuestion.Submitting {
		t.Fatal("answer submission was not started")
	}
	_, duplicate := m.resolveActiveQuestion()
	if duplicate != nil {
		t.Fatal("repeated enter started a second submission")
	}
	m.applyPendingRequests([]core.PendingRequest{{Kind: core.PendingUserQuestion, UserQuestion: &request}})
	if m.activeQuestion.currentDraft().Selected != 1 || m.activeQuestion.currentDraft().NoteText != "keep this note" || len(m.activeQuestion.confirmedAnswers()) != 1 {
		t.Fatalf("refresh reset the submitting batch: %#v", m.activeQuestion)
	}
	if !strings.Contains(ansi.Strip(renderQuestionPanel(m.activeQuestion, 100)), "Submitting answers (1)") {
		t.Fatal("submitting state is not visible")
	}
	m.Update(cmd()) // A missing runtime produces a real typed failure.
	if m.activeQuestion.Submitting || m.activeQuestion.Error == "" || len(m.activeQuestion.confirmedAnswers()) != 1 {
		t.Fatalf("submission failure lost answers or error: %#v", m.activeQuestion)
	}
	if !strings.Contains(ansi.Strip(renderQuestionPanel(m.activeQuestion, 100)), "Failed to submit answers") {
		t.Fatal("submission error is not visible")
	}
	_, retry := m.resolveActiveQuestion()
	if retry == nil || len(m.entries) != 1 || len(m.entries[0].AnsweredQuestions) != 1 || len(m.activeQuestion.confirmedAnswers()) != 1 {
		t.Fatalf("retry duplicated the question or answer: %#v", m.entries)
	}
	key := m.questionKey(request)
	m.Update(questionSubmittedMsg{key: questionWaitKey{SessionID: "old-session"}, err: errors.New("old result")})
	if !m.activeQuestion.Submitting || m.run.questionSubmissions[key] != questionSubmitting {
		t.Fatal("another session's result changed the current submission")
	}
}

func TestPendingQuestionDefaultsToRecommendedOption(t *testing.T) {
	m := newTUIModel(nil, nil, "session")
	request := questionFixture()
	request.Questions[0].Options[1].Recommended = true
	m.applyPendingRequests([]core.PendingRequest{{Kind: core.PendingUserQuestion, UserQuestion: &request}})
	if m.activeQuestion == nil || m.activeQuestion.currentDraft().Selected != 1 {
		t.Fatalf("recommended option was not selected: %#v", m.activeQuestion)
	}
	_, cmd := m.resolveActiveQuestion()
	if cmd == nil || m.entries[0].AnsweredQuestions[0].Answer != "Core" {
		t.Fatalf("recommended answer was not submitted as the raw label: %#v", m.entries)
	}
}

func TestAnsweredQuestionIdentityDoesNotHideNewQuestionWithSameText(t *testing.T) {
	m := newTUIModel(nil, nil, "session")
	request := questionFixture()
	m.run.questionSubmissions = map[questionWaitKey]questionSubmissionState{m.questionKey(request): questionAccepted}
	m.applyPendingRequests([]core.PendingRequest{{Kind: core.PendingUserQuestion, UserQuestion: &request}})
	if m.activeQuestion != nil {
		t.Fatal("accepted question reopened")
	}
	next := request
	next.InvocationID = "next-invocation"
	m.applyPendingRequests([]core.PendingRequest{{Kind: core.PendingUserQuestion, UserQuestion: &next}})
	if m.activeQuestion == nil || m.activeQuestion.Request.InvocationID != next.InvocationID {
		t.Fatal("a new question with identical content was hidden")
	}
}

func TestStaleWaiterAndTaskPauseCannotOverwriteResumedAttempt(t *testing.T) {
	m := newTUIModel(nil, nil, "session")
	m.run.running, m.run.runningExecution = true, "execution"
	m.run.taskSnapshot = core.TaskState{ExecutionID: "execution", TaskID: "new-attempt", Status: core.TaskRunning}
	m.run.awaitingTurns["execution"] = 2
	m.Update(turnFinishedMsg{executionID: "execution", generation: 1, result: core.TurnResult{Status: core.TaskPaused}})
	m.processRuntimeEventNoRefresh(core.Event{Kind: core.EventTaskFinished, ExecutionID: "execution", TaskID: "old-attempt", Status: core.TaskPaused})
	if !m.run.running || m.hasPausedTask() || m.run.awaitingTurns["execution"] != 2 || len(m.run.terminalEvents) != 0 {
		t.Fatalf("old pause overwrote the resumed attempt: running=%v paused=%v waits=%v", m.run.running, m.hasPausedTask(), m.run.awaitingTurns)
	}
}

type questionResumeProvider struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
}

func (*questionResumeProvider) Name() string { return "host" }

func (p *questionResumeProvider) CreateResponse(ctx context.Context, _ publicmodel.ModelRequest, _ publicmodel.ModelProfile) (*publicmodel.ModelResponse, error) {
	call := p.calls.Add(1)
	if call == 1 {
		return &publicmodel.ModelResponse{Status: "completed", Output: []publicmodel.ResponseOutputItem{publicmodel.OutputFunctionCall{
			CallID: "ask-scope", Name: "ask_user", Arguments: `{"questions":[{"id":"scope","question":"Which scope?","options":[{"label":"TUI","description":"Fix UI"},{"label":"Core","description":"Fix runtime"}]},{"id":"tests","question":"Run tests?","options":[{"label":"Yes","description":"Run tests"},{"label":"No","description":"Skip tests"}]}]}`,
		}}}, nil
	}
	if call != 2 {
		return nil, errors.New("unexpected duplicate model request")
	}
	close(p.started)
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &publicmodel.ModelResponse{Status: "completed", Output: []publicmodel.ResponseOutputItem{publicmodel.OutputMessage{
		Role: publicmodel.RoleAssistant, Content: []publicmodel.OutputContentPart{publicmodel.OutputText{Text: "Plan ready"}},
	}}}, nil
}

func (p *questionResumeProvider) StreamResponse(ctx context.Context, request publicmodel.ModelRequest, profile publicmodel.ModelProfile, _ func(publicmodel.ModelStreamEvent) error) (*publicmodel.ModelResponse, error) {
	return p.CreateResponse(ctx, request, profile)
}

func TestQuestionAnswerAcknowledgementSurvivesDelayedPauseEvents(t *testing.T) {
	t.Run("model still running", func(t *testing.T) { testQuestionAnswerAcknowledgement(t, false) })
	t.Run("model completed before acknowledgement", func(t *testing.T) { testQuestionAnswerAcknowledgement(t, true) })
}

func testQuestionAnswerAcknowledgement(t *testing.T, completeBeforeAck bool) {
	provider := &questionResumeProvider{started: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(provider.release) })
	client, err := core.New(core.Config{
		Workspace: t.TempDir(), Memory: core.MemoryConfig{Disabled: true},
		ModelConfig: &publicmodel.Config{
			Providers: map[string]publicmodel.ProviderConfig{"host": {Protocol: "responses", BaseURL: "https://example.invalid"}},
			Profiles:  map[string]publicmodel.ModelProfile{"main": {Provider: "host", Model: "test"}}, DefaultProfile: "main",
		},
		ModelProviders: map[string]publicmodel.Provider{"host": provider},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	t.Cleanup(release)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	saved, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := client.OpenSessionRuntime(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	input, _ := core.ParseTaskInput("Ask me about scope")
	submission, err := runtime.SubmitTurn(ctx, core.TurnRequest{Input: input}, core.StartIfIdle)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := runtime.WaitTurn(ctx, submission.ExecutionID)
	if err != nil || paused.Status != core.TaskPaused {
		t.Fatalf("initial pause = %#v, %v", paused, err)
	}
	m := newTUIModel(client, runtime, saved.ID)
	if m.activeQuestion == nil || len(m.activeQuestion.Request.Questions) != 2 {
		t.Fatal("pending two-question batch was not restored")
	}
	request := m.activeQuestion.Request
	oldTaskID := m.run.taskSnapshot.TaskID
	m.run.awaitingTurns[submission.ExecutionID] = 1
	m.run.waitGeneration = 1
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyRight})
	m.resolveActiveQuestion()
	// Confirm the second question first, then revise and confirm the first.
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd := m.resolveActiveQuestion()
	answers := m.activeQuestion.confirmedAnswers()
	if cmd == nil || len(answers) != 2 || answers[0].QuestionID != "scope" || answers[0].Answer != "Core" || answers[1].QuestionID != "tests" {
		t.Fatalf("navigation did not produce a complete batch in request order: %#v", answers)
	}
	m.processRuntimeEventNoRefresh(core.Event{Kind: core.EventTaskFinished, TaskID: oldTaskID, ExecutionID: submission.ExecutionID, Status: core.TaskPaused})
	m.checkPendingState(ctx)
	if m.activeQuestion == nil || !m.activeQuestion.Submitting || len(m.activeQuestion.confirmedAnswers()) != 2 {
		t.Fatal("delayed pause reset the submitted answers")
	}
	ack := cmd()
	select {
	case <-provider.started:
	case <-ctx.Done():
		t.Fatal("answer did not resume the model")
	}
	if completeBeforeAck {
		release()
		if completed, err := runtime.WaitTurn(ctx, submission.ExecutionID); err != nil || completed.Status != core.TaskCompleted {
			t.Fatalf("fast resumed task did not complete: %#v, %v", completed, err)
		}
		_, resultCmd := m.Update(ack)
		if resultCmd == nil {
			t.Fatal("acknowledgement lost the completed task's canonical response")
		}
		// Core may return the preceding pause to a waiter admitted during resume.
		// Reconcile it against the completed runtime and fetch the final result.
		generation := m.run.awaitingTurns[submission.ExecutionID]
		_, resultCmd = m.Update(turnFinishedMsg{sessionID: saved.ID, executionID: submission.ExecutionID, generation: generation, result: paused})
		if resultCmd == nil {
			t.Fatal("a delayed paused result hid the completed canonical response")
		}
		m.Update(resultCmd())
		if m.run.running || m.hasPausedTask() || m.activeQuestion != nil {
			t.Fatal("completed task left a stale running/pause/question state")
		}
		for _, entry := range m.entries {
			if entry.Kind == KindAssistant && entry.Text == "Plan ready" {
				// The initial start notification can also arrive after a very
				// fast task completed. It must still collect the final response.
				late := newTUIModel(client, runtime, saved.ID)
				late.entries = nil
				late.run.running, late.run.runningExecution = true, submission.ExecutionID
				late.run.taskSnapshot = core.TaskState{}
				state, _ := runtime.State(ctx)
				_, resultCmd := late.processRuntimeEventNoRefresh(core.Event{
					Kind: core.EventTaskStarted, TaskID: state.Task.TaskID, ExecutionID: submission.ExecutionID,
				})
				if resultCmd == nil {
					t.Fatal("delayed task_started event lost the completed response")
				}
				late.Update(resultCmd())
				if len(late.entries) != 1 || late.entries[0].Kind != KindAssistant || late.entries[0].Text != "Plan ready" {
					t.Fatalf("delayed task_started did not collect the response: %#v", late.entries)
				}
				return
			}
		}
		t.Fatal("canonical response from fast resumed model was not displayed")
	}
	m.Update(ack)
	if m.activeQuestion != nil || !m.run.running || m.hasPausedTask() {
		t.Fatalf("accepted answer left a stale modal or pause: question=%v running=%v paused=%v", m.activeQuestion != nil, m.run.running, m.hasPausedTask())
	}
	generation := m.run.awaitingTurns[submission.ExecutionID]
	m.Update(turnFinishedMsg{sessionID: saved.ID, executionID: submission.ExecutionID, generation: 1, result: paused})
	m.processRuntimeEventNoRefresh(core.Event{Kind: core.EventUserQuestionRequired, TaskID: oldTaskID, ExecutionID: submission.ExecutionID, UserQuestion: &request})
	m.processRuntimeEventNoRefresh(core.Event{Kind: core.EventTaskFinished, TaskID: oldTaskID, ExecutionID: submission.ExecutionID, Status: core.TaskPaused})
	m.processRuntimeEventNoRefresh(core.Event{Kind: core.EventTaskStarted, TaskID: oldTaskID, ExecutionID: submission.ExecutionID})
	m.checkPendingState(ctx)
	if m.activeQuestion != nil || !m.run.running || m.hasPausedTask() || m.run.awaitingTurns[submission.ExecutionID] != generation {
		t.Fatal("old question/pause events overwrote the running attempt")
	}
	answerCards := 0
	for _, entry := range m.entries {
		if entry.Kind == KindQuestionAnswered {
			answerCards++
			if len(entry.AnsweredQuestions) != 2 {
				t.Fatalf("answer card lost batch answers: %#v", entry)
			}
		}
	}
	if provider.calls.Load() != 2 || answerCards != 1 {
		t.Fatalf("duplicate model request or answer record: calls=%d entries=%#v", provider.calls.Load(), m.entries)
	}
	release()
	completed, err := runtime.WaitTurn(ctx, submission.ExecutionID)
	if err != nil || completed.Status != core.TaskCompleted {
		t.Fatalf("resumed task did not complete: %#v, %v", completed, err)
	}
	m.Update(turnFinishedMsg{sessionID: saved.ID, executionID: submission.ExecutionID, generation: generation, result: completed})
}

func TestDelegationFooterUsesEffectiveLimits(t *testing.T) {
	workspace := t.TempDir()
	config := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test"}},"profiles":{"main":{"provider":"host","model":"test"}},"defaultProfile":"main"},"runtime":{"maxParallelAgents":0,"maxTeamChildrenPerAgent":0}}`
	if err := os.WriteFile(filepath.Join(workspace, "config.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: workspace, Memory: core.MemoryConfig{Disabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	m := newTUIModel(client, nil, "session")
	footer := ansi.Strip(renderFooter(m, 160))
	if !strings.Contains(footer, "0 active · disabled") || strings.Contains(footer, "4 max") || m.maxParallelAgents() != 4 {
		t.Fatalf("zero config was displayed as enabled delegation: %s", footer)
	}
	if !strings.Contains(m.delegationLimitsLabel(), "children per agent 0") || !strings.Contains(m.delegationLimitsLabel(), "parallel 4") {
		t.Fatalf("diagnostics did not distinguish concurrency and delegation: %s", m.delegationLimitsLabel())
	}
	m.restoreRuntimeSnapshot(core.RuntimeState{Task: core.TaskState{
		TaskID: "task", ExecutionID: "execution", Status: core.TaskPaused,
		Execution: core.ExecutionOptions{MaxTeamChildrenPerAgent: 3, MaxTeamDepth: 1},
	}})
	if !strings.Contains(ansi.Strip(renderFooter(m, 160)), "0 active · 4 parallel") || !strings.Contains(m.delegationLimitsLabel(), "children per agent 3; depth 1") {
		t.Fatal("paused task's effective limits did not override defaults")
	}
	m.restoreRuntimeSnapshot(core.RuntimeState{Task: core.TaskState{Status: core.TaskCompleted}})
	if m.delegationEnabled() {
		t.Fatal("finished task's limits leaked into idle defaults")
	}
}

func TestActiveQuestionCustomAndNotesInput(t *testing.T) {
	m := newTUIModel(nil, nil, "q-sess")
	m.activeQuestion = &ActiveQuestion{
		Drafts: []questionDraft{{
			Selected: 1, // "None of the above"
		}},
		Request: core.UserQuestionRequest{
			ID: "q-req-1",
			Questions: []core.UserQuestionItem{
				{
					ID:       "q1",
					Question: "Choose an option",
					Options: []core.UserQuestionOption{
						{Label: "Opt A"},
					},
				},
			},
		},
	}

	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'h', Text: "h"}))
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'i', Text: "i"}))
	m = mod.(*tuiModel)
	if m.activeQuestion.currentDraft().Custom != "hi" {
		t.Fatalf("expected Custom answer 'hi', got %q", m.activeQuestion.currentDraft().Custom)
	}

	// Toggle notes with Tab and type into NoteText
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = mod.(*tuiModel)
	if !m.activeQuestion.currentDraft().NotesOpen {
		t.Fatalf("expected NotesOpen to be true after Tab")
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"}))
	m = mod.(*tuiModel)
	if m.activeQuestion.currentDraft().NoteText != "n" {
		t.Fatalf("expected NoteText 'n', got %q", m.activeQuestion.currentDraft().NoteText)
	}
}

func TestActiveQuestionAllowsComposerAndDiscard(t *testing.T) {
	m := newTUIModel(nil, nil, "q-sess")
	m.activeQuestion = &ActiveQuestion{
		Request: core.UserQuestionRequest{
			ID: "q-req-1",
			Questions: []core.UserQuestionItem{
				{
					ID:       "q1",
					Question: "Choose an option",
					Options: []core.UserQuestionOption{
						{Label: "Opt A"},
					},
				},
			},
		},
	}

	// Typing "/" should pass through to composer and trigger autocomplete
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '/', Text: "/"}))
	m = mod.(*tuiModel)
	if m.input.composer.Value() != "/" {
		t.Fatalf("expected composer to receive '/', got %q", m.input.composer.Value())
	}
	if !m.input.autocompleteOpen {
		t.Fatalf("expected autocomplete to be open for slash command")
	}

	// Type the rest of "/discard"
	for _, ch := range "discard" {
		mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: rune(ch), Text: string(ch)}))
		m = mod.(*tuiModel)
	}
	if m.input.composer.Value() != "/discard" {
		t.Fatalf("expected composer to hold '/discard', got %q", m.input.composer.Value())
	}

	// Submitting with Enter executes /discard
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)

	// Since m.runtime is nil in this test model, discardPausedTask records:
	// "Paused-task management is unavailable."
	lastEntry := m.entries[len(m.entries)-1]
	if !strings.Contains(lastEntry.Text, "Paused-task management is unavailable") {
		t.Fatalf("expected /discard execution note, got %+v", lastEntry)
	}
	if m.input.composer.Value() != "" {
		t.Fatalf("expected composer to be reset after submit, got %q", m.input.composer.Value())
	}
}

func TestActiveQuestionEscHierarchy(t *testing.T) {
	m := newTUIModel(nil, nil, "q-sess")
	m.activeQuestion = &ActiveQuestion{
		Drafts: []questionDraft{{
			Selected:  1, // "None of the above"
			Custom:    "some answer",
			NotesOpen: true,
			NoteText:  "some note",
		}},
		Request: core.UserQuestionRequest{
			ID: "q-req-1",
			Questions: []core.UserQuestionItem{
				{
					ID:       "q1",
					Question: "Choose an option",
					Options: []core.UserQuestionOption{
						{Label: "Opt A"},
					},
				},
			},
		},
	}

	// 1. Esc when Notes is open closes Notes
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.activeQuestion.currentDraft().NotesOpen {
		t.Fatalf("expected Esc to close NotesOpen")
	}
	if len(m.entries) > 0 {
		t.Fatalf("closing NotesOpen should not add system notices: %+v", m.entries)
	}

	// 2. Esc when Custom has text clears Custom
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.activeQuestion.currentDraft().Custom != "" {
		t.Fatalf("expected Esc to clear Custom answer, got %q", m.activeQuestion.currentDraft().Custom)
	}
	if len(m.entries) > 0 {
		t.Fatalf("clearing Custom should not add system notices: %+v", m.entries)
	}

	// 3. Esc when composer has text clears composer
	m.input.composer.SetValue("draft")
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.input.composer.Value() != "" {
		t.Fatalf("expected Esc to clear composer draft, got %q", m.input.composer.Value())
	}

	// 4. Esc when on options list notes question pending
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if len(m.entries) == 0 || !strings.Contains(m.entries[len(m.entries)-1].Text, "waiting for your answer") {
		t.Fatalf("expected pending-question notice on options Esc, got: %+v", m.entries)
	}
}
