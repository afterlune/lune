package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

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

	if m.activeQuestion != nil {
		t.Errorf("expected activeQuestion to be nil after Esc")
	}
	foundCancelled := false
	for _, entry := range m.entries {
		if strings.Contains(entry.Text, "Question cancelled") {
			foundCancelled = true
			break
		}
	}
	if !foundCancelled {
		t.Errorf("expected 'Question cancelled' system message in entries, got %+v", m.entries)
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

	// /theme status inspect
	m.handleSlashCommand("/theme")
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Current theme mode: `auto`") {
		t.Errorf("expected theme inspect message, got: %s", lastSys)
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

	// Esc discards recovery
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.activeRecovery != nil {
		t.Errorf("expected activeRecovery to be nil after Esc")
	}
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Model recovery discarded") {
		t.Errorf("expected 'Model recovery discarded' system entry, got: %s", lastSys)
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

	// /open while running
	m.handleSlashCommand("/open other-sess")
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

	// 4. Test cancel queued turn
	m.queuedTurns = append(m.queuedTurns, QueuedItem{ExecutionID: "exec-q2"})
	m.entries = append(m.entries, TranscriptEntry{Kind: KindQueued, ToolCallID: "exec-q2", Text: "Cancel me"})
	m.cancelQueuedTurn()
	if len(m.queuedTurns) != 0 {
		t.Errorf("expected queued turns to be empty after cancellation")
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
