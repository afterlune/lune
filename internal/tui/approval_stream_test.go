package tui

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

func newApprovalTestModel(t *testing.T) (*tuiModel, *core.SessionRuntime) {
	t.Helper()
	client := newLazySessionTestCore(t)
	session, err := client.CreateSession(context.Background(), json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := client.OpenSessionRuntime(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return newTUIModel(client, runtime, session.ID), runtime
}

func countEntriesContaining(m *tuiModel, fragment string) int {
	count := 0
	for _, entry := range m.entries {
		if strings.Contains(entry.Text, fragment) {
			count++
		}
	}
	return count
}

// TestFullAccessAutoApprovalFailureFallsBackToCard covers C1: a silent approval
// that Core rejects must surface the card instead of leaving the task paused with
// nothing on screen.
func TestFullAccessAutoApprovalFailureFallsBackToCard(t *testing.T) {
	m, _ := newApprovalTestModel(t)
	m.permissionMode = core.PermissionFullAccess

	request := core.ApprovalRequest{ID: "approval-unknown", Tool: "command"}
	if m.approveWithoutCard(request) {
		t.Fatal("a rejected silent approval must fall back to the approval card")
	}
	if _, failed := m.run.autoApprovalFailures[request.ID]; !failed {
		t.Fatal("failed auto-approval was not recorded, so it would be retried forever")
	}
	const message = "Full-access auto-approval failed"
	if got := countEntriesContaining(m, message); got != 1 {
		t.Fatalf("auto-approval failure entries = %d, want exactly one", got)
	}
	// A second pass over the same request must neither retry nor repeat the message.
	if m.approveWithoutCard(request) {
		t.Fatal("a recorded failure must keep the card visible")
	}
	if got := countEntriesContaining(m, message); got != 1 {
		t.Fatalf("auto-approval failure entries = %d after retry, want one", got)
	}
}

// TestApprovalOutsideFullAccessAlwaysShowsCard guards the silent path: only
// full-access may resolve an approval without the user.
func TestApprovalOutsideFullAccessAlwaysShowsCard(t *testing.T) {
	m, _ := newApprovalTestModel(t)
	m.permissionMode = core.PermissionWorkspace
	if m.approveWithoutCard(core.ApprovalRequest{ID: "approval-1", Tool: "command"}) {
		t.Fatal("workspace mode must never auto-approve a tool call")
	}
}

// TestApprovalResolutionErrorRestoresRunState covers C2: the decision is sent
// through a tea.Cmd and a failure stops the optimistic running state instead of
// silently diverging from Core.
func TestApprovalResolutionErrorRestoresRunState(t *testing.T) {
	m, _ := newApprovalTestModel(t)
	m.activeApproval = &ActiveApproval{Request: core.ApprovalRequest{ID: "approval-1", Tool: "command"}}
	m.run.running = true
	m.run.runningExecution = "exec-1"

	mod, _ := m.Update(approvalResolvedMsg{approvalID: "approval-1", err: errors.New("request is no longer pending")})
	m = mod.(*tuiModel)

	if m.run.running {
		t.Fatal("a failed approval decision must not leave the model claiming a running task")
	}
	if got := countEntriesContaining(m, "Failed to resolve the tool approval"); got != 1 {
		t.Fatalf("approval failure entries = %d, want one", got)
	}
}

// TestClosedStreamSchedulesReconnect covers C3: an early close must re-open the
// stream instead of silently freezing the transcript.
func TestClosedStreamSchedulesReconnect(t *testing.T) {
	m, _ := newApprovalTestModel(t)
	m.finishSessionRestore()
	closed := make(chan core.Event)
	close(closed)

	mod, cmd := m.Update(nextRuntimeEvent(m.sessionID, m.run.subscriptionGeneration, closed)())
	m = mod.(*tuiModel)

	if cmd == nil {
		t.Fatal("a closed event stream must schedule a reconnect")
	}
	if m.run.streamReconnects != 1 {
		t.Fatalf("reconnect attempts = %d, want 1", m.run.streamReconnects)
	}
}

// TestReplacedSubscriptionDoesNotReconnect keeps a replaced subscription from
// starting a second one when its channel closes.
func TestReplacedSubscriptionDoesNotReconnect(t *testing.T) {
	m, _ := newApprovalTestModel(t)
	m.finishSessionRestore()
	m.run.subscriptionGeneration = 5

	_, cmd := m.Update(runtimeEventMsg{sessionID: m.sessionID, generation: 4, events: nil})
	if cmd != nil {
		t.Fatal("a message from a replaced subscription must not reconnect")
	}
}

// TestStreamReconnectGivesUpAfterLimit surfaces one actionable message instead of
// retrying a dead stream forever.
func TestStreamReconnectGivesUpAfterLimit(t *testing.T) {
	m, _ := newApprovalTestModel(t)
	m.finishSessionRestore()
	m.run.streamReconnects = maxRuntimeStreamReconnects
	closed := make(chan core.Event)
	close(closed)

	mod, cmd := m.Update(nextRuntimeEvent(m.sessionID, m.run.subscriptionGeneration, closed)())
	m = mod.(*tuiModel)

	if cmd != nil {
		t.Fatal("the reconnect limit must stop automatic retries")
	}
	if got := countEntriesContaining(m, "runtime event stream is lost"); got != 1 {
		t.Fatalf("lost-stream entries = %d, want one", got)
	}
}

// TestStreamFailureEventReconnects covers the Core-reported failure path.
func TestStreamFailureEventReconnects(t *testing.T) {
	m, _ := newApprovalTestModel(t)
	m.finishSessionRestore()

	mod, cmd := m.Update(runtimeEventMsg{
		sessionID:  m.sessionID,
		generation: m.run.subscriptionGeneration,
		event:      core.Event{Kind: core.EventRuntimeStreamFailed, Err: "channel overflow"},
	})
	m = mod.(*tuiModel)

	if cmd == nil {
		t.Fatal("a runtime stream failure must schedule a reconnect")
	}
	if got := countEntriesContaining(m, "Runtime stream error"); got != 1 {
		t.Fatalf("stream error entries = %d, want one", got)
	}
}

// TestReconnectResumesAfterLastCursor keeps a reconnect from replaying the whole
// journal into the transcript.
func TestReconnectResumesAfterLastCursor(t *testing.T) {
	m, _ := newApprovalTestModel(t)
	m.finishSessionRestore()
	m.run.lastEventCursor = 42

	original := openRuntimeEventStream
	t.Cleanup(func() { openRuntimeEventStream = original })
	gotCursor := int64(-1)
	openRuntimeEventStream = func(_ context.Context, _ *core.SessionRuntime, cursor int64) (<-chan core.Event, error) {
		gotCursor = cursor
		return nil, errors.New("subscribe failed")
	}

	if _, cmd := m.Update(runtimeReconnectMsg{sessionID: m.sessionID}); cmd == nil {
		t.Fatal("a reconnect message must re-open the stream")
	}
	if gotCursor != 42 {
		t.Fatalf("reconnect cursor = %d, want the last processed cursor 42", gotCursor)
	}
}

// TestEventCursorAdvances records the cursor that a reconnect resumes after.
func TestEventCursorAdvances(t *testing.T) {
	m, _ := newApprovalTestModel(t)
	m.finishSessionRestore()

	mod, _ := m.Update(runtimeEventMsg{
		sessionID:  m.sessionID,
		generation: m.run.subscriptionGeneration,
		event:      core.Event{Kind: core.EventUsage, Cursor: 11},
	})
	m = mod.(*tuiModel)
	if m.run.lastEventCursor != 11 {
		t.Fatalf("cursor = %d, want 11", m.run.lastEventCursor)
	}
}

// TestInteractiveCardRenderAndKeyDispatchConsistency covers P0-1:
// Ensures visual-dispatch consistency between activeCardKind(), rendering, and key dispatch.
func TestInteractiveCardRenderAndKeyDispatchConsistency(t *testing.T) {
	m, _ := newApprovalTestModel(t)
	m.activeApproval = &ActiveApproval{
		Request:  core.ApprovalRequest{ID: "appr-1", Tool: "command"},
		Selected: 0,
	}
	m.activeQuestion = &ActiveQuestion{
		Request: core.UserQuestionRequest{
			ID: "q-1",
			Questions: []core.UserQuestionItem{
				{ID: "q1", Question: "Proceed?", Options: []core.UserQuestionOption{{Label: "Yes"}}},
			},
		},
		Drafts: []questionDraft{{Selected: 0}},
	}

	// Approval has priority over Question
	if kind := m.activeCardKind(); kind != cardApproval {
		t.Fatalf("activeCardKind() = %v, want cardApproval", kind)
	}

	// Esc when composer is empty should deny approval, not cancel question
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	m = mod.(*tuiModel)

	// In test model without live execution, resolving active approval adds a system entry:
	// "Denied tool execution for `command`."
	foundDenied := false
	for _, entry := range m.entries {
		if strings.Contains(entry.Text, "Denied tool execution") {
			foundDenied = true
			break
		}
	}
	if !foundDenied {
		t.Fatalf("expected Esc to resolve activeApproval as Denied, entries: %+v", m.entries)
	}
	// After approval is resolved, activeQuestion becomes the next active card!
	if kind := m.activeCardKind(); kind != cardQuestion {
		t.Fatalf("activeCardKind() after approval resolution = %v, want cardQuestion", kind)
	}
}
