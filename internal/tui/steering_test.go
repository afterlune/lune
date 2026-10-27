package tui

import (
	"strings"
	"testing"
	"time"

	core "github.com/afterlune/luneCore"
)

func TestSteeredInputPromotesAfterCurrentAssistantSegment(t *testing.T) {
	m := newTUIModel(nil, nil, "steered-input")
	m.width, m.height = 100, 30
	m.run.running = true
	m.run.runningExecution = "parent-execution"
	m.run.taskStartedAt = time.Now()
	m.run.appendStreamDelta("Before.\n<proposed_plan>## Steps\n1. Inspect</proposed_plan>")
	m.entries = append(m.entries, TranscriptEntry{
		ID: "queued-placeholder", Kind: KindQueued, ToolCallID: "input-execution", Text: "Continue with the follow-up",
	})
	m.run.queuedTurns = []QueuedItem{{ExecutionID: "input-execution", QueueID: "queue-id", ParentExecutionID: "parent-execution"}}

	updated, _ := m.handleRuntimeEvent(core.Event{
		Kind: core.EventTaskInputInjected, ExecutionID: "parent-execution", InputExecutionID: "input-execution", QueueID: "queue-id",
	})
	m = updated.(*tuiModel)
	planIndex, userIndex := -1, -1
	for i, entry := range m.entries {
		if entry.Kind == KindPlan && strings.Contains(entry.Text, "Inspect") {
			planIndex = i
		}
		if entry.Kind == KindUser && entry.ToolCallID == "input-execution" {
			userIndex = i
		}
	}
	if planIndex < 0 || userIndex <= planIndex {
		t.Fatalf("the prior assistant plan must precede the injected user message: %+v", m.entries)
	}
	if m.activePlanReview != nil {
		t.Fatalf("an intermediate plan opened review choices: %#v", m.activePlanReview)
	}
	if !m.run.running || m.run.runningExecution != "parent-execution" || len(m.run.queuedTurns) != 0 {
		t.Fatalf("injection changed execution or queue state: running=%v execution=%q queue=%+v", m.run.running, m.run.runningExecution, m.run.queuedTurns)
	}
	if m.run.currentStreamText != "" {
		t.Fatalf("the previous live segment was not finalized: %q", m.run.currentStreamText)
	}
}

func TestLateSteeringEventInsertsUserBeforeFinalAssistant(t *testing.T) {
	m := newTUIModel(nil, nil, "late-steering-event")
	m.run.runningExecution = "parent-execution"
	m.run.streamSegment = 0
	m.entries = []TranscriptEntry{
		{ID: "queued-placeholder", Kind: KindQueued, ToolCallID: "input-execution", Text: "Follow-up"},
		{ID: assistantOutputID("parent-execution"), ExecutionID: "parent-execution", Kind: KindAssistant, Text: "Final response"},
	}
	event := core.Event{Kind: core.EventTaskInputInjected, ExecutionID: "parent-execution", InputExecutionID: "input-execution"}
	if !m.promoteInjectedInput(event) {
		t.Fatal("injected input was not promoted")
	}
	if m.entries[0].Kind != KindUser || m.entries[0].ToolCallID != "input-execution" || m.entries[1].Kind != KindAssistant {
		t.Fatalf("late event produced the wrong transcript order: %+v", m.entries)
	}
}

func TestInjectedEventBeforeSubmissionResultDoesNotRestoreQueueItem(t *testing.T) {
	m := newTUIModel(nil, nil, "early-steering-event")
	m.width, m.height = 100, 30
	m.run.running = true
	m.run.runningExecution = "parent-execution"
	m.entries = []TranscriptEntry{{ID: "queued-placeholder", Kind: KindQueued, Text: "Follow-up"}}
	_, _ = m.handleRuntimeEvent(core.Event{
		Kind: core.EventTaskQueued, ExecutionID: "input-execution", QueueID: "queue-id", ParentExecutionID: "parent-execution",
	})
	_, _ = m.handleRuntimeEvent(core.Event{
		Kind: core.EventTaskInputInjected, ExecutionID: "parent-execution", InputExecutionID: "input-execution", QueueID: "queue-id",
	})
	updated, _ := m.Update(turnSubmittedMsg{
		sessionID: m.sessionID,
		entryID:   "queued-placeholder",
		submission: core.TurnSubmission{
			Status: core.SubmissionQueued, ExecutionID: "input-execution", QueueID: "queue-id", ParentExecutionID: "parent-execution",
		},
	})
	m = updated.(*tuiModel)
	if len(m.run.queuedTurns) != 0 {
		t.Fatalf("late submission result restored an already injected queue item: %+v", m.run.queuedTurns)
	}
	users := 0
	for _, entry := range m.entries {
		if entry.Kind == KindUser && entry.ToolCallID == "input-execution" {
			users++
		}
	}
	if users != 1 {
		t.Fatalf("promoted user entry count = %d, want 1: %+v", users, m.entries)
	}
}

func TestCompletedPlanReviewWaitsWhileQueuedInputRemains(t *testing.T) {
	m := newTUIModel(nil, nil, "deferred-plan-review")
	m.run.running = true
	m.run.runningExecution = "parent-execution"
	m.run.queuedTurns = []QueuedItem{{ExecutionID: "queued-input"}}
	_ = m.finishTurn(turnFinishedMsg{
		executionID: "parent-execution",
		result:      core.TurnResult{ExecutionID: "parent-execution", Status: core.TaskCompleted, ProposedPlan: &core.ProposedPlan{Text: "## Steps\n1. Follow up"}},
	})
	if m.activePlanReview != nil {
		t.Fatalf("plan review appeared while a queued input remained: %#v", m.activePlanReview)
	}
	if !containsEntry(m.entries, KindPlan, "Follow up") {
		t.Fatalf("the completed plan should remain visible in the transcript: %+v", m.entries)
	}
}

func TestLiveOutputUsesMainTranscriptViewportAndRetainsLongText(t *testing.T) {
	m := newTUIModel(nil, nil, "long-live-output")
	m.width, m.height = 100, 22
	m.run.running = true
	m.run.taskStartedAt = time.Now()
	for line := 0; line < 100; line++ {
		m.run.appendStreamDelta("stream line\n")
	}
	m.view.invalidateLive()
	m.updateLayout()
	transcript := strings.Join(m.view.transcriptLines, "\n")
	if count := strings.Count(transcript, "stream line"); count != 100 {
		t.Fatalf("main viewport retained %d of 100 live lines", count)
	}
	if m.viewport.TotalLineCount() < 100 {
		t.Fatalf("viewport document has only %d lines despite complete live output", m.viewport.TotalLineCount())
	}
}

func TestAllRoundsFromActiveExecutionStayExpanded(t *testing.T) {
	m := newTUIModel(nil, nil, "active-rounds")
	m.run.running = true
	m.run.runningExecution = "same-execution"
	m.entries = []TranscriptEntry{
		{Kind: KindTool, ExecutionID: "same-execution", ToolName: "read_file"},
		{Kind: KindAssistant, ExecutionID: "same-execution", Text: "between rounds"},
		{Kind: KindTool, ExecutionID: "same-execution", ToolName: "grep"},
	}
	if !m.roundExpanded(0, false) || !m.roundExpanded(2, false) {
		t.Fatal("earlier tool rounds in the active execution should stay expanded")
	}
	m.roundsCollapsedByUser = true
	if m.roundExpanded(0, false) || m.roundExpanded(2, false) {
		t.Fatal("the explicit collapse should apply to all active rounds")
	}
	m.run.running = false
	m.roundsCollapsedByUser = false
	if m.roundExpanded(0, false) || m.roundExpanded(2, false) {
		t.Fatal("completed rounds should return to their normal collapsed state")
	}
}

func containsEntry(entries []TranscriptEntry, kind EntryKind, text string) bool {
	for _, entry := range entries {
		if entry.Kind == kind && strings.Contains(entry.Text, text) {
			return true
		}
	}
	return false
}
