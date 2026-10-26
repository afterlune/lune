package tui

import (
	"errors"
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
)

// TestSessionUsageRestoreIsFinal covers the rule that the footer metrics must be
// final as soon as a session snapshot is rendered: restoring a session applies the
// persisted totals, the accumulated cache rate and the last request numbers in one
// pass, without leaving a second update for later.
func TestSessionUsageRestoreIsFinal(t *testing.T) {
	m := newTUIModel(nil, nil, "usage-session")
	window := int64(200000)
	m.loadSessionSnapshot(core.Session{
		ID:           "usage-session",
		SessionUsage: core.UsageTotals{Input: 150000, Output: 9500, Cached: 127500},
		Settings:     core.SessionSettings{ContextWindow: &window},
		UsageRequests: []core.UsageRecord{{
			RequestID:            "req-1",
			RequestAttempt:       2,
			EstimatedInputTokens: 12400,
			Usage: core.Usage{
				InputTokens:        150000,
				InputTokensDetails: core.TokenInputDetails{CachedTokens: 127500},
				OutputTokens:       9500,
			},
		}},
	})

	summary := formatContextAndTokenSummary(m, 200)
	for _, want := range []string{"Context: 150k/200k", "Cache: 85% session", "Total: 160k tok"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("footer summary must be final after restore, missing %q in %q", want, summary)
		}
	}
	if m.usage.lastRequestID != "req-1" || m.usage.lastRequestAttempt != 2 {
		t.Fatalf("restored request identity = %q/%d, want req-1/2", m.usage.lastRequestID, m.usage.lastRequestAttempt)
	}
}

// TestUsageEventAccumulatesAndKeepsWindow covers the live path: usage events keep
// accumulating on top of the persisted baseline and only a positive window number
// replaces the budget.
func TestUsageEventAccumulatesAndKeepsWindow(t *testing.T) {
	m := newTUIModel(nil, nil, "usage-session")
	m.usage.applyTotals(core.UsageTotals{Input: 1000, Output: 500, Cached: 800})
	m.usage.contextWindow = 128000
	// A seeded session only keeps accumulating while a turn is running; that is
	// what stops a restored snapshot from double counting.
	m.run.running = true

	m.Update(runtimeEventMsg{event: core.Event{
		Kind:          core.EventUsage,
		RequestID:     "req-2",
		ContextWindow: 0,
		Usage: core.Usage{
			InputTokens:        2000,
			InputTokensDetails: core.TokenInputDetails{CachedTokens: 1500},
			OutputTokens:       400,
		},
	}})

	if m.usage.totalInputTokens != 3000 || m.usage.totalOutputTokens != 900 || m.usage.cachedTokens != 2300 {
		t.Fatalf("usage totals must accumulate on the seeded baseline: in=%d out=%d cached=%d",
			m.usage.totalInputTokens, m.usage.totalOutputTokens, m.usage.cachedTokens)
	}
	if m.usage.contextWindow != 128000 {
		t.Fatalf("a zero window in an event must keep the current budget, got %d", m.usage.contextWindow)
	}
	if m.usage.lastRequestID != "req-2" || m.usage.lastInputTokens != 2000 {
		t.Fatalf("last request bookkeeping = %q/%d, want req-2/2000", m.usage.lastRequestID, m.usage.lastInputTokens)
	}
}

// TestTranscriptCacheClearsMarkersAfterRender covers the view cache contract:
// rendering the transcript leaves neither stale marker behind.
func TestTranscriptCacheClearsMarkersAfterRender(t *testing.T) {
	m := newTUIModel(nil, nil, "cache-session")
	m.width, m.height = 100, 40
	m.entries = []TranscriptEntry{{ID: "u1", Kind: KindUser, Text: "hello"}}
	m.view.invalidateBlocks()
	if !m.view.cachedDirty || !m.view.transcriptDirty {
		t.Fatal("invalidateBlocks must mark both the blocks and the composed text")
	}
	m.refreshViewportContent()
	if m.view.cachedDirty || m.view.transcriptDirty {
		t.Fatalf("refresh must clear both markers, got blocks=%v content=%v", m.view.cachedDirty, m.view.transcriptDirty)
	}
	if len(m.view.cachedBlocks) != 1 || len(m.view.entryLineRanges) == 0 {
		t.Fatalf("refresh must rebuild blocks and line ranges, got %d blocks and %d ranges",
			len(m.view.cachedBlocks), len(m.view.entryLineRanges))
	}

	m.view.invalidateContent()
	if m.view.cachedDirty {
		t.Fatal("invalidateContent must not require re-rendering the entry blocks")
	}
	if !m.view.transcriptDirty {
		t.Fatal("invalidateContent must mark the composed text stale")
	}
}

func TestRunStateResetKeepsAsyncGenerationsMonotonic(t *testing.T) {
	run := newRunState()
	run.subscriptionGeneration = 8
	run.waitGeneration = 12
	run.restoreGeneration = 4
	run.reset()
	if run.subscriptionGeneration != 8 || run.waitGeneration != 12 || run.restoreGeneration != 4 {
		t.Fatalf("session reset reused async identities: subscription=%d wait=%d restore=%d",
			run.subscriptionGeneration, run.waitGeneration, run.restoreGeneration)
	}
	run.beginRestore()
	if run.restoreGeneration != 5 {
		t.Fatalf("restore generation did not advance across reset: %d", run.restoreGeneration)
	}
}

func TestDelayedSessionResultsAreIgnoredAfterReturningToSameSession(t *testing.T) {
	m := newTUIModel(nil, nil, "session-a")
	oldGeneration := m.bindingGeneration
	oldContext := m.boundContext
	m.replaceSessionBinding() // Switch away.
	m.replaceSessionBinding() // Return to the same session ID.
	select {
	case <-oldContext.Done():
	default:
		t.Fatal("replacing a session binding did not cancel its context")
	}
	if m.bindingGeneration == oldGeneration {
		t.Fatal("binding generation was reused after returning to the same session")
	}
	m.Update(sessionForkedMsg{sessionID: "session-a", bindingGeneration: oldGeneration, err: errors.New("stale fork")})
	m.Update(planImplementedMsg{sessionID: "session-a", bindingGeneration: oldGeneration, err: errors.New("stale implementation")})
	if len(m.entries) != 0 {
		t.Fatalf("a delayed result from an earlier binding changed the returned session: %+v", m.entries)
	}
	m.Update(turnSubmittedMsg{
		sessionID: "session-a", bindingGeneration: oldGeneration,
		submission: core.TurnSubmission{ExecutionID: "stale", Status: core.SubmissionAccepted},
	})
	m.run.running = true
	m.run.runningExecution = "same-execution"
	m.run.awaitingTurns["same-execution"] = 1
	m.Update(turnFinishedMsg{
		sessionID: "session-a", bindingGeneration: oldGeneration, executionID: "same-execution",
		generation: 1, result: core.TurnResult{Status: core.TaskFailed},
	})
	m.run.discardInFlight = true
	m.Update(taskDiscardedMsg{bindingGeneration: oldGeneration, source: discardSourcePausedTask, discarded: true})
	m.Update(compactDoneMsg{sessionID: "session-a", bindingGeneration: oldGeneration, err: errors.New("stale compact")})
	m.Update(approvalResolvedMsg{bindingGeneration: oldGeneration, approvalID: "stale", err: errors.New("stale approval")})
	m.Update(goalActionMsg{sessionID: "session-a", bindingGeneration: oldGeneration, action: "pause", err: errors.New("stale goal")})
	m.Update(reviewMaterialsMsg{bindingGeneration: oldGeneration, err: errors.New("stale review")})
	if !m.run.running || !m.run.discardInFlight || m.run.runningExecution != "same-execution" || len(m.entries) != 0 {
		t.Fatalf("a stale session result changed the current task: running=%v discard=%v execution=%q entries=%+v",
			m.run.running, m.run.discardInFlight, m.run.runningExecution, m.entries)
	}
}
