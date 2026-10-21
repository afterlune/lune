package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
	"github.com/charmbracelet/x/ansi"
)

func TestToolSummaryUsesTransientArgumentsRegardlessOfEventOrder(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "internal", "tui", "model.go")
	rawArgs, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}

	durableStart := core.Event{
		Kind: core.EventToolStarted,
		Tool: core.ToolCall{ID: "read-call", Name: "read_file", Started: true},
	}
	liveStart := core.Event{
		Kind: core.EventToolDetails,
		Tool: core.ToolCall{ID: "read-call", Name: "read_file", Arguments: rawArgs, Started: true},
	}
	finished := core.Event{
		Kind: core.EventToolFinished,
		Tool: core.ToolCall{ID: "read-call", Name: "read_file", Duration: time.Second},
	}

	cases := []struct {
		name   string
		events []core.Event
	}{
		{name: "durable event first", events: []core.Event{durableStart, liveStart, finished}},
		{name: "live detail first", events: []core.Event{liveStart, durableStart, finished}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTUIModel(nil, nil, "tool-summary")
			m.workspace = workspace
			m.running = true
			for _, event := range tc.events {
				mod, _ := m.handleRuntimeEvent(event)
				m = mod.(*tuiModel)
			}
			if len(m.entries) != 1 {
				t.Fatalf("expected one merged tool entry, got %d: %+v", len(m.entries), m.entries)
			}
			entry := m.entries[0]
			if entry.ToolCommand != "Read internal/tui/model.go" {
				t.Fatalf("expected workspace-relative read summary, got %q", entry.ToolCommand)
			}
			if entry.ToolArgs != "" {
				t.Fatalf("transient tool arguments should not be retained, got %q", entry.ToolArgs)
			}
			collapsed := ansi.Strip(renderRoundChildEntry(entry, false, 120))
			if !strings.Contains(collapsed, "Read internal/tui/model.go") || strings.Contains(collapsed, "Read read_file") {
				t.Fatalf("collapsed execution step should show the file path:\n%s", collapsed)
			}
		})
	}
}

func TestToolDetailsCommandOverridesArgumentSummary(t *testing.T) {
	m := newTUIModel(nil, nil, "tool-summary-command")
	m.running = true
	m.handleRuntimeEvent(core.Event{
		Kind: core.EventToolDetails,
		Tool: core.ToolCall{
			ID:        "command-call",
			Name:      "run_command",
			Arguments: json.RawMessage(`{"command":"go test ./..."}`),
			Details: &publictool.ExecutionDetails{Command: &publictool.CommandOutput{
				Command: publictool.CommandInfo{Command: "go test ./..."},
			}},
			Started: true,
		},
	})
	if len(m.entries) != 1 || m.entries[0].ToolCommand != "go test ./..." {
		t.Fatalf("actual command detail should override the generated summary: %+v", m.entries)
	}
}

func TestBatchedQuestionNavigationAndResolution(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.width = 100
	m.height = 30

	m.activeQuestion = &ActiveQuestion{
		Request: core.UserQuestionRequest{
			ID: "req-batch-1",
			Questions: []core.UserQuestionItem{
				{
					ID:       "q1",
					Question: "First Question?",
					Options: []core.UserQuestionOption{
						{Label: "Choice 1A", Description: "Desc 1A"},
						{Label: "Choice 1B", Description: "Desc 1B"},
					},
				},
				{
					ID:       "q2",
					Question: "Second Question?",
					Options: []core.UserQuestionOption{
						{Label: "Choice 2A", Description: "Desc 2A"},
						{Label: "Choice 2B", Description: "Desc 2B"},
					},
				},
				{
					ID:       "q3",
					Question: "Third Question?",
					Options: []core.UserQuestionOption{
						{Label: "Choice 3A", Description: "Desc 3A"},
					},
				},
			},
		},
		Selected: 0,
	}

	// 1. Initial view: Question 1/3 (3 unanswered)
	rendered := renderQuestionPanel(m.activeQuestion, 100)
	if !strings.Contains(rendered, "Question 1/3 (3 unanswered)") {
		t.Fatalf("expected 'Question 1/3 (3 unanswered)', got: %s", rendered)
	}
	if !strings.Contains(rendered, "First Question?") {
		t.Fatalf("expected 'First Question?', got: %s", rendered)
	}

	// Down arrow to select Choice 1B
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mod.(*tuiModel)
	if m.activeQuestion.Selected != 1 {
		t.Fatalf("expected Selected=1 for Choice 1B, got %d", m.activeQuestion.Selected)
	}

	// Press Enter to submit answer for question 1
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)

	// Question 1 should be recorded in transcript entries
	if len(m.entries) != 1 || m.entries[0].Kind != KindQuestionAnswered || len(m.entries[0].AnsweredQuestions) != 1 || m.entries[0].AnsweredQuestions[0].Answer != "Choice 1B" {
		t.Fatalf("expected Question 1 answered with Choice 1B, got %+v", m.entries)
	}

	// Active question should now be at Question 2/3 (2 unanswered)
	if m.activeQuestion == nil || m.activeQuestion.CurrentIndex != 1 {
		t.Fatalf("expected ActiveQuestion at CurrentIndex=1, got %+v", m.activeQuestion)
	}
	rendered2 := renderQuestionPanel(m.activeQuestion, 100)
	if !strings.Contains(rendered2, "Question 2/3 (2 unanswered)") {
		t.Fatalf("expected 'Question 2/3 (2 unanswered)', got: %s", rendered2)
	}
	if !strings.Contains(rendered2, "Second Question?") {
		t.Fatalf("expected 'Second Question?', got: %s", rendered2)
	}

	// Press Enter to submit choice 2A for question 2
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)
	if len(m.entries) != 1 || len(m.entries[0].AnsweredQuestions) != 2 || m.entries[0].AnsweredQuestions[1].Answer != "Choice 2A" {
		t.Fatalf("expected Question 2 merged into batch entry with Choice 2A, got %+v", m.entries)
	}

	// Active question should now be at Question 3/3 (1 unanswered)
	if m.activeQuestion == nil || m.activeQuestion.CurrentIndex != 2 {
		t.Fatalf("expected ActiveQuestion at CurrentIndex=2, got %+v", m.activeQuestion)
	}
	rendered3 := renderQuestionPanel(m.activeQuestion, 100)
	if !strings.Contains(rendered3, "Question 3/3 (1 unanswered)") {
		t.Fatalf("expected 'Question 3/3 (1 unanswered)', got: %s", rendered3)
	}

	// Press Enter to submit choice 3A for question 3
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)

	// Keep the batch visible until Core acknowledges the submitted answers.
	if m.activeQuestion == nil || !m.activeQuestion.Submitting {
		t.Fatalf("expected activeQuestion to wait for submission acknowledgement")
	}
	if len(m.entries) != 1 || len(m.entries[0].AnsweredQuestions) != 3 {
		t.Fatalf("expected 1 grouped question batch entry with 3 answers, got %+v", m.entries)
	}
	renderedAnswered := ansi.Strip(renderEntry(m.entries[0], 100))
	if !strings.Contains(renderedAnswered, "Questions 3/3 answered") ||
		!strings.Contains(renderedAnswered, "Choice 1B") ||
		!strings.Contains(renderedAnswered, "Choice 2A") ||
		!strings.Contains(renderedAnswered, "Choice 3A") {
		t.Fatalf("expected grouped Questions 3/3 answered card, got:\n%s", renderedAnswered)
	}
}

func TestTurnFinishedPausedClearsRunningState(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.running = true
	m.runningExecution = "exec-paused"
	m.thinking = true

	mod, _ := m.Update(turnFinishedMsg{
		sessionID: "test-sess",
		result: core.TurnResult{
			ExecutionID: "exec-paused",
			Status:      core.TaskPaused,
		},
	})
	m = mod.(*tuiModel)

	if m.running {
		t.Errorf("expected m.running to be false when turn is paused")
	}
	if m.thinking {
		t.Errorf("expected m.thinking to be false when turn is paused")
	}
	if m.runningExecution != "exec-paused" {
		t.Errorf("expected paused execution id to remain available for resolution, got %q", m.runningExecution)
	}
}

func TestRestoreRuntimeSnapshotIgnoresFinishedExecution(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.restoreRuntimeSnapshot(core.RuntimeState{Task: core.TaskState{
		ExecutionID: "finished-exec",
		Status:      core.TaskCompleted,
	}})
	if m.running || m.runningExecution != "" {
		t.Fatalf("finished execution must not be treated as active: running=%v execution=%q", m.running, m.runningExecution)
	}
}

func TestTaskStartedEventDoesNotReviveHistory(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	_, cmd := m.processRuntimeEventNoRefresh(core.Event{
		Kind:        core.EventTaskStarted,
		ExecutionID: "old-exec",
	})
	if m.running || m.runningExecution != "" || cmd != nil {
		t.Fatalf("historical task start must not revive an idle session: running=%v execution=%q cmd=%v", m.running, m.runningExecution, cmd != nil)
	}
}

func TestTurnCompletionEventAndWaiterDeduplicate(t *testing.T) {
	response := json.RawMessage(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Canonical response"}]}]}`)
	completion := core.Event{Kind: core.EventTaskFinished, ExecutionID: "exec-complete", Status: core.TaskCompleted}
	result := turnFinishedMsg{
		sessionID:   "test-sess",
		executionID: "exec-complete",
		result: core.TurnResult{
			ExecutionID: "exec-complete",
			Status:      core.TaskCompleted,
			Response:    response,
		},
	}

	for _, eventFirst := range []bool{true, false} {
		name := "waiter first"
		if eventFirst {
			name = "event first"
		}
		t.Run(name, func(t *testing.T) {
			m := newTUIModel(nil, nil, "test-sess")
			m.running = true
			m.runningExecution = "exec-complete"
			m.taskStartedAt = time.Now().Add(-time.Second)
			m.entries = append(m.entries,
				TranscriptEntry{ID: "user-current", Kind: KindUser, Text: "answer"},
				TranscriptEntry{ID: "assistant-partial", ExecutionID: "exec-complete", Kind: KindAssistant, Text: "Old partial"},
			)
			m.currentStreamText = "Latest draft"

			if eventFirst {
				m.finishTurnEvent(completion)
				m.finishTurn(result)
			} else {
				m.finishTurn(result)
				m.finishTurnEvent(completion)
			}
			m.finishTurnEvent(completion)
			m.finishTurn(result)

			var assistant []TranscriptEntry
			for _, entry := range m.entries {
				if entry.Kind == KindAssistant && entry.ExecutionID == "exec-complete" {
					assistant = append(assistant, entry)
				}
			}
			if len(assistant) != 1 || assistant[0].Text != "Canonical response" {
				t.Fatalf("expected one canonical assistant response, got %+v", assistant)
			}
			if m.running || m.currentStreamText != "" {
				t.Fatalf("expected completion to clear live turn state, running=%v stream=%q", m.running, m.currentStreamText)
			}
		})
	}
}

func TestPlanCompletionEventDefersFinalizationUntilWaiterResult(t *testing.T) {
	const executionID = "exec-plan-completion"
	response := json.RawMessage(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Before.\n<proposed_plan>## Steps\n1. Inspect<proposed_plan_end/>\nAfter."}]}]}`)
	m := newTUIModel(nil, nil, "test-sess")
	m.running = true
	m.runningExecution = executionID
	m.currentStreamText = "Before.\n<proposed_plan>## Steps\n1. Inspect"
	m.finishTurnEvent(core.Event{Kind: core.EventTaskFinished, ExecutionID: executionID, Status: core.TaskCompleted})
	if m.currentStreamText == "" || m.activePlanReview != nil {
		t.Fatalf("task event finalized the plan before the authoritative waiter result: stream=%q review=%#v", m.currentStreamText, m.activePlanReview)
	}

	m.finishTurn(turnFinishedMsg{
		sessionID: "test-sess", executionID: executionID,
		result: core.TurnResult{
			ExecutionID: executionID, Status: core.TaskCompleted, Response: response,
			ProposedPlan: &core.ProposedPlan{Text: "## Steps\n1. Inspect"},
		},
	})
	if m.activePlanReview == nil || len(m.entries) != 3 || m.entries[1].Kind != KindPlan || m.entries[2].Text != "After." {
		t.Fatalf("waiter result did not finalize plan ordering and review: review=%#v entries=%#v", m.activePlanReview, m.entries)
	}
}

func TestFinishTurnReportsTaskFailedWithoutModelFailure(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.running = true
	m.runningExecution = "exec-failed"
	m.finishTurn(turnFinishedMsg{
		executionID: "exec-failed",
		result:      core.TurnResult{ExecutionID: "exec-failed", Status: core.TaskFailed},
	})

	var found bool
	for _, entry := range m.entries {
		if entry.Kind == KindSystem && strings.Contains(entry.Text, "Task failed:") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected system entry reporting task failure, got entries: %+v", m.entries)
	}
	if m.running {
		t.Fatal("expected running=false after task failure")
	}
}

func TestEscCancelsActiveQuestion(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.activeQuestion = &ActiveQuestion{
		Request: core.UserQuestionRequest{
			ID: "req-1",
			Questions: []core.UserQuestionItem{
				{ID: "q1", Question: "Test?"},
			},
		},
	}

	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)

	if m.activeQuestion == nil {
		t.Fatal("question should remain visible until Core confirms the discard")
	}
	if !strings.Contains(m.entries[len(m.entries)-1].Text, "Paused-task management is unavailable") {
		t.Fatalf("missing unavailable-runtime feedback: %+v", m.entries)
	}
	mod, _ = m.Update(taskDiscardedMsg{source: discardSourceQuestion, discarded: true})
	m = mod.(*tuiModel)
	if m.activeQuestion != nil || !strings.Contains(m.entries[len(m.entries)-1].Text, "Question cancelled") {
		t.Fatalf("confirmed discard did not cancel question: active=%+v entries=%+v", m.activeQuestion, m.entries)
	}
}

func TestClearCommand(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.entries = []TranscriptEntry{
		{Kind: KindUser, Text: "User msg 1"},
		{Kind: KindAssistant, Text: "Assistant answer 1"},
	}
	// Active interactive modal should NOT be wiped by /clear
	m.activeQuestion = &ActiveQuestion{
		Request: core.UserQuestionRequest{ID: "q-preserve"},
	}

	m.handleSlashCommand("/clear")

	if len(m.entries) != 0 {
		t.Errorf("expected entries to be cleared, got %d", len(m.entries))
	}
	if strings.TrimSpace(m.viewport.View()) != "" {
		t.Errorf("expected viewport to be empty, got: %q", m.viewport.View())
	}
	if m.activeQuestion == nil || m.activeQuestion.Request.ID != "q-preserve" {
		t.Errorf("expected activeQuestion modal to be preserved across /clear")
	}
}

func TestThemeSwitching(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")

	// /theme dark
	m.handleSlashCommand("/theme dark")
	if CurrentThemeMode() != "dark" {
		t.Errorf("expected dark theme mode, got: %s", CurrentThemeMode())
	}

	// /theme light
	m.handleSlashCommand("/theme light")
	if CurrentThemeMode() != "light" {
		t.Errorf("expected light theme mode, got: %s", CurrentThemeMode())
	}

	// /theme auto
	m.handleSlashCommand("/theme auto")
	if CurrentThemeMode() != "auto" {
		t.Errorf("expected auto theme mode, got: %s", CurrentThemeMode())
	}

	// /theme opens the interactive selector and Enter applies its current selection.
	m.handleSlashCommand("/theme")
	if m.themePicker == nil || m.themePicker.Selected != 0 {
		t.Fatalf("expected theme picker to open on current auto mode: %+v", m.themePicker)
	}
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)
	if m.themePicker != nil || CurrentThemeMode() != ThemeModeAuto {
		t.Fatalf("expected Enter to apply auto theme, picker=%v mode=%q", m.themePicker, CurrentThemeMode())
	}
}

func TestPasteMsgSafety(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	pasted := "Line 1 of multiline text\nLine 2 of multiline text\nLine 3"

	mod, _ := m.Update(tea.PasteMsg{Content: pasted})
	m = mod.(*tuiModel)

	if m.running {
		t.Errorf("pasting multiline text must NOT start task execution automatically")
	}
	if m.composer.Value() != pasted {
		t.Errorf("expected composer to contain pasted text, got: %q", m.composer.Value())
	}
}

func TestActiveRecoveryModalNavigationAndEsc(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.activeRecovery = &ActiveRecovery{
		Reason:   "rate_limit (status=429, attempts=3)",
		Selected: 0,
	}

	// Render panel
	rendered := renderRecoveryPanel(m.activeRecovery, 80)
	if !strings.Contains(rendered, "Model Recovery Required") {
		t.Errorf("expected title in recovery panel, got: %s", rendered)
	}
	if !strings.Contains(rendered, "rate_limit") {
		t.Errorf("expected reason in recovery panel, got: %s", rendered)
	}
	if !strings.Contains(rendered, "1. Retry model request") {
		t.Errorf("expected retry option in recovery panel, got: %s", rendered)
	}

	// Down arrow moves selection to discard
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mod.(*tuiModel)
	if m.activeRecovery == nil || m.activeRecovery.Selected != 1 {
		t.Errorf("expected selection 1 after down arrow, got %+v", m.activeRecovery)
	}

	// Up arrow moves selection back to retry
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mod.(*tuiModel)
	if m.activeRecovery == nil || m.activeRecovery.Selected != 0 {
		t.Errorf("expected selection 0 after up arrow, got %+v", m.activeRecovery)
	}

	// Esc requests discard but keeps the card until Core confirms it.
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.activeRecovery == nil {
		t.Fatal("expected activeRecovery to remain until Core confirmation")
	}
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Paused-task management is unavailable") {
		t.Errorf("expected unavailable-runtime message, got: %s", lastSys)
	}
	mod, _ = m.Update(taskDiscardedMsg{source: discardSourceRecovery, discarded: true})
	m = mod.(*tuiModel)
	if m.activeRecovery != nil || !strings.Contains(m.entries[len(m.entries)-1].Text, "Model recovery discarded") {
		t.Errorf("confirmed discard did not close recovery panel: recovery=%+v entries=%+v", m.activeRecovery, m.entries)
	}
}

func TestStatusTableUsageFormatting(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.contextWindow = 128000
	m.estimatedInputTokens = 4200
	m.totalInputTokens = 12450
	m.cachedTokens = 8192
	m.totalOutputTokens = 3210
	m.reasoningTokens = 512

	table := m.renderStatusTable()
	if !strings.Contains(table, "Context Window: 128,000 tokens") {
		t.Errorf("expected formatted context window, got: %s", table)
	}
	if !strings.Contains(table, "est. input: 4,200") {
		t.Errorf("expected formatted estimated input, got: %s", table)
	}
	if !strings.Contains(table, "Total Tokens:   in: 12,450 (cached: 8,192)") {
		t.Errorf("expected formatted token totals, got: %s", table)
	}
	if !strings.Contains(table, "out: 3,210 (reasoning: 512)") {
		t.Errorf("expected formatted output & reasoning tokens, got: %s", table)
	}
}

func TestRewindAndOpenRunningGuards(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.running = true

	// /rewind while running
	m.handleSlashCommand("/rewind")
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Cannot rewind while a task is running") {
		t.Errorf("expected rewind guard error, got: %s", lastSys)
	}

	// /resume while running
	m.handleSlashCommand("/resume other-sess")
	lastSys2 := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys2, "Cannot switch sessions while a task is running") {
		t.Errorf("expected open guard error, got: %s", lastSys2)
	}

	// /new while running
	m.handleSlashCommand("/new")
	lastSys3 := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys3, "Cannot start a new session while a task is running") {
		t.Errorf("expected new session guard error, got: %s", lastSys3)
	}
}

func TestQueuedTurnWorkflowAndCancellation(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.running = true
	m.runningExecution = "exec-active"

	// 1. Submit turn while running -> should enter KindQueued
	m.submitTurn("Follow-up task while model works")
	if len(m.entries) == 0 {
		t.Fatalf("expected queued entry in transcript")
	}
	lastEntry := m.entries[len(m.entries)-1]
	if lastEntry.Kind != KindQueued || !strings.Contains(lastEntry.Text, "Follow-up task") {
		t.Errorf("expected KindQueued entry, got %+v", lastEntry)
	}
	rendered := renderEntry(lastEntry, 80)
	if !strings.Contains(rendered, "Queued ›") || !strings.Contains(rendered, "Follow-up task") {
		t.Errorf("expected queued badge in rendered view, got: %s", rendered)
	}

	// 2. Simulate Core responding with SubmissionQueued
	mod, _ := m.Update(turnSubmittedMsg{
		sessionID: "test-sess",
		submission: core.TurnSubmission{
			Status:      core.SubmissionQueued,
			ExecutionID: "exec-queued-1",
			QueueID:     "q-1",
		},
	})
	m = mod.(*tuiModel)
	if len(m.queuedTurns) != 1 || m.queuedTurns[0].ExecutionID != "exec-queued-1" {
		t.Fatalf("expected queued turn registered in m.queuedTurns, got %+v", m.queuedTurns)
	}

	// 3. Simulate Core promoting queued turn via EventTaskStarted
	mod, _ = m.handleRuntimeEvent(core.Event{
		Kind:        core.EventTaskStarted,
		ExecutionID: "exec-queued-1",
	})
	m = mod.(*tuiModel)
	if len(m.queuedTurns) != 0 {
		t.Errorf("expected queued turns to be empty after task started, got %d", len(m.queuedTurns))
	}
	if m.runningExecution != "exec-queued-1" {
		t.Errorf("expected runningExecution to be exec-queued-1, got %s", m.runningExecution)
	}
	// Check the entry was converted to KindUser
	promoted := false
	for _, e := range m.entries {
		if e.ToolCallID == "exec-queued-1" && e.Kind == KindUser {
			promoted = true
			break
		}
	}
	if !promoted {
		t.Errorf("expected queued entry to be promoted to KindUser")
	}

	// 4. Cancellation is not reflected until Core confirms it.
	m.queuedTurns = append(m.queuedTurns, QueuedItem{ExecutionID: "exec-q2"})
	m.entries = append(m.entries, TranscriptEntry{Kind: KindQueued, ToolCallID: "exec-q2", Text: "Cancel me"})
	m.cancelQueuedTurn()
	if len(m.queuedTurns) != 1 {
		t.Fatalf("queued turn was removed before Core confirmation: %+v", m.queuedTurns)
	}
	m.cancelingQueuedExecution = "exec-q2"
	mod, _ = m.Update(queuedTurnCancelledMsg{executionID: "exec-q2"})
	m = mod.(*tuiModel)
	if len(m.queuedTurns) != 0 {
		t.Fatalf("confirmed queued turn was not removed: %+v", m.queuedTurns)
	}
	cancelledFound := false
	for _, e := range m.entries {
		if e.ToolCallID == "exec-q2" && strings.Contains(e.Text, "cancelled") {
			cancelledFound = true
			break
		}
	}
	if !cancelledFound {
		t.Errorf("expected entry to be marked as cancelled")
	}
}

func TestQueuedTurnCancellationErrorsDoNotClaimSuccess(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.queuedTurns = []QueuedItem{{ExecutionID: "exec-q1"}, {ExecutionID: "exec-q2"}}
	m.entries = []TranscriptEntry{{Kind: KindQueued, ToolCallID: "exec-q1", Text: "first"}, {Kind: KindQueued, ToolCallID: "exec-q2", Text: "latest"}}
	m.cancelingQueuedExecution = "exec-q2"
	mod, _ := m.Update(queuedTurnCancelledMsg{executionID: "exec-q2", err: core.ErrQueuedTurnStarted})
	m = mod.(*tuiModel)
	if len(m.queuedTurns) != 2 || m.entries[1].Kind != KindQueued || m.cancelingQueuedExecution != "" {
		t.Fatalf("failed cancellation changed queue state: queue=%+v entry=%+v pending=%q", m.queuedTurns, m.entries[1], m.cancelingQueuedExecution)
	}
	if !strings.Contains(m.entries[len(m.entries)-1].Text, "already started") {
		t.Fatalf("missing started-task feedback: %+v", m.entries)
	}

	m.cancelingQueuedExecution = "exec-q2"
	mod, _ = m.Update(queuedTurnCancelledMsg{executionID: "exec-q2", err: core.ErrTurnNotFound})
	m = mod.(*tuiModel)
	if len(m.queuedTurns) != 1 || m.entries[1].Kind != KindUser {
		t.Fatalf("missing queued item was not reconciled: queue=%+v entry=%+v", m.queuedTurns, m.entries[1])
	}
}

func TestEscKeepsQueueCancellationSeparateFromRunningTaskInterrupt(t *testing.T) {
	workspace := t.TempDir()
	projectConfig := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{}}`
	if err := os.WriteFile(filepath.Join(workspace, "config.json"), []byte(projectConfig), 0600); err != nil {
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
	session, err := client.CreateSession(context.Background(), json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := client.OpenSessionRuntime(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := newTUIModel(client, runtime, session.ID)
	m.running = true
	m.runningExecution = "active-exec"
	m.queuedTurns = []QueuedItem{{ExecutionID: "queued-exec"}}
	m.entries = []TranscriptEntry{{Kind: KindQueued, ToolCallID: "queued-exec", Text: "waiting"}}
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.running || m.pausedCheckpoint == nil || len(m.queuedTurns) != 1 || m.cancelingQueuedExecution != "" {
		t.Fatalf("Esc did not interrupt active work independently of queued work: running=%t checkpoint=%+v queue=%+v canceling=%q", m.running, m.pausedCheckpoint, m.queuedTurns, m.cancelingQueuedExecution)
	}
}

func TestDiscardResultKeepsRecoveryOnErrorAndReportsMissingTask(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.activeRecovery = &ActiveRecovery{ExecutionID: "paused-exec"}
	m.pausedCheckpoint = &PausedCheckpoint{ExecutionID: "paused-exec"}
	m.discardInFlight = true
	mod, _ := m.Update(taskDiscardedMsg{source: discardSourceRecovery, err: errors.New("database locked")})
	m = mod.(*tuiModel)
	if m.activeRecovery == nil || m.pausedCheckpoint == nil || m.discardInFlight {
		t.Fatalf("failed discard lost recovery state: recovery=%+v checkpoint=%+v pending=%t", m.activeRecovery, m.pausedCheckpoint, m.discardInFlight)
	}
	if !strings.Contains(m.entries[len(m.entries)-1].Text, "database locked") {
		t.Fatalf("discard error not shown: %+v", m.entries)
	}

	m.discardInFlight = true
	mod, _ = m.Update(taskDiscardedMsg{source: discardSourceRecovery, discarded: false})
	m = mod.(*tuiModel)
	if m.activeRecovery != nil || m.pausedCheckpoint != nil || m.running {
		t.Fatalf("missing paused task left stale recovery state: recovery=%+v checkpoint=%+v running=%t", m.activeRecovery, m.pausedCheckpoint, m.running)
	}
	if !strings.Contains(m.entries[len(m.entries)-1].Text, "No interrupted task to discard") {
		t.Fatalf("missing-task outcome not reported: %+v", m.entries)
	}
}
