package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	core "github.com/afterlune/luneCore"
)

// TestRestoreSuppressesUsageChurn keeps the footer metrics frozen while Core
// replays a freshly loaded session's durable events.
func TestRestoreSuppressesUsageChurn(t *testing.T) {
	m := newTUIModel(nil, nil, "restore-usage")
	m.beginSessionRestore()
	m.usage.totalInputTokens = 1000
	m.usage.cachedTokens = 400
	m.usage.lastInputTokens = 900
	m.usage.lastCachedTokens = 350
	m.usage.estimatedInputTokens = 950
	m.usage.contextWindow = 128000

	usage := core.Usage{InputTokens: 500, OutputTokens: 100}
	usage.InputTokensDetails.CachedTokens = 200
	event := core.Event{
		Kind:                 core.EventUsage,
		Cursor:               3,
		AgentID:              "lead",
		Usage:                usage,
		ContextWindow:        64000,
		EstimatedInputTokens: 1200,
		RequestID:            "req-replay",
	}
	mod, _ := m.processRuntimeEventNoRefresh(event)
	m = mod.(*tuiModel)

	if m.usage.totalInputTokens != 1000 || m.usage.cachedTokens != 400 {
		t.Fatalf("session totals changed during restore: input=%d cached=%d", m.usage.totalInputTokens, m.usage.cachedTokens)
	}
	if m.usage.lastInputTokens != 900 || m.usage.lastCachedTokens != 350 || m.usage.estimatedInputTokens != 950 || m.usage.contextWindow != 128000 {
		t.Fatalf("footer metrics changed during restore: last=%d cached=%d estimated=%d context=%d",
			m.usage.lastInputTokens, m.usage.lastCachedTokens, m.usage.estimatedInputTokens, m.usage.contextWindow)
	}

	m.finishSessionRestore()
	mod, _ = m.processRuntimeEventNoRefresh(event)
	m = mod.(*tuiModel)

	if m.usage.totalInputTokens != 1500 || m.usage.cachedTokens != 600 {
		t.Fatalf("live usage not accumulated after restore: input=%d cached=%d", m.usage.totalInputTokens, m.usage.cachedTokens)
	}
	if m.usage.lastInputTokens != 500 || m.usage.estimatedInputTokens != 1200 || m.usage.contextWindow != 64000 {
		t.Fatalf("live usage did not update the latest request: last=%d estimated=%d context=%d",
			m.usage.lastInputTokens, m.usage.estimatedInputTokens, m.usage.contextWindow)
	}
}

// TestRestoreDefersTranscriptRefresh renders a replayed transcript once instead of
// rebuilding it for every batch.
func TestRestoreDefersTranscriptRefresh(t *testing.T) {
	m := newTUIModel(nil, nil, "restore-refresh")
	m.width = 100
	m.height = 30
	m.entries = []TranscriptEntry{{Kind: KindUser, Text: "hello"}}
	m.updateLayout()
	m.beginSessionRestore()
	renderedBefore := m.view.cachedEntryCount

	args, err := json.Marshal(map[string]string{"command": "ls"})
	if err != nil {
		t.Fatal(err)
	}
	mod, _ := m.Update(runtimeEventMsg{event: core.Event{
		Kind:   core.EventToolStarted,
		Cursor: 9,
		Tool:   core.ToolCall{ID: "call-1", Name: "run_command", Arguments: args, Started: true},
	}})
	m = mod.(*tuiModel)

	if len(m.entries) != 2 {
		t.Fatalf("replayed tool entry was not appended: %+v", m.entries)
	}
	if m.view.cachedEntryCount != renderedBefore {
		t.Fatalf("transcript rebuilt during restore: cached=%d entries=%d", m.view.cachedEntryCount, len(m.entries))
	}
	if !m.run.restoringSession {
		t.Fatalf("restore finished before the replay settled")
	}

	mod, _ = m.Update(runtimeRestoreSettledMsg{generation: m.run.restoreGeneration})
	m = mod.(*tuiModel)

	if m.run.restoringSession {
		t.Fatalf("restore did not settle on the idle tick")
	}
	if m.view.cachedEntryCount != len(m.entries) {
		t.Fatalf("transcript not rendered after settle: cached=%d entries=%d", m.view.cachedEntryCount, len(m.entries))
	}
}

// TestRestoreWatchdogSettlesQuietRestore settles a restore that stopped receiving
// events, so a missing replay-complete cannot freeze the footer forever.
func TestRestoreWatchdogSettlesQuietRestore(t *testing.T) {
	m := newTUIModel(nil, nil, "restore-quiet")
	m.beginSessionRestore()
	m.run.restoreLastProgressAt = time.Now().Add(-restoreStallWindow - time.Second)

	mod, _ := m.Update(restoreWatchdogMsg{generation: m.run.restoreGeneration})
	m = mod.(*tuiModel)
	if m.run.restoringSession {
		t.Fatal("a quiet restore should be settled by the watchdog")
	}
}

// TestRestoreWatchdogKeepsActiveReplay checks that a replay which keeps delivering
// events holds the freeze until Core reports completion.
func TestRestoreWatchdogKeepsActiveReplay(t *testing.T) {
	m := newTUIModel(nil, nil, "restore-active")
	m.beginSessionRestore()

	mod, cmd := m.Update(restoreWatchdogMsg{generation: m.run.restoreGeneration})
	m = mod.(*tuiModel)
	if !m.run.restoringSession {
		t.Fatal("an active replay must keep the freeze")
	}
	if cmd == nil {
		t.Fatal("the watchdog should schedule another check")
	}
}

// TestRestoreSettlesOnStaleGeneration ignores idle ticks from an earlier restore.
func TestRestoreSettlesOnStaleGeneration(t *testing.T) {
	m := newTUIModel(nil, nil, "restore-stale")
	m.beginSessionRestore()
	stale := m.run.restoreGeneration
	m.beginSessionRestore()

	mod, _ := m.Update(runtimeRestoreSettledMsg{generation: stale})
	m = mod.(*tuiModel)
	if !m.run.restoringSession {
		t.Fatalf("a stale idle tick settled the newer restore")
	}

	mod, _ = m.Update(runtimeRestoreSettledMsg{generation: m.run.restoreGeneration})
	m = mod.(*tuiModel)
	if m.run.restoringSession {
		t.Fatalf("current restore did not settle")
	}
}

// TestStartingTurnEndsRestore counts live usage immediately once the user submits.
func TestStartingTurnEndsRestore(t *testing.T) {
	client := newLazySessionTestCore(t)
	m := newTUIModel(client, nil, "")
	m.width = 100
	m.height = 30
	m.updateLayout()
	m.beginSessionRestore()

	mod, _ := m.submitTurn("inspect the restored session")
	m = mod.(*tuiModel)

	if m.run.restoringSession {
		t.Fatalf("starting a turn must stop deferring the transcript restore")
	}
	if !m.run.running {
		t.Fatalf("expected the submitted turn to be running")
	}
}

// TestRestoreIgnoresReplayedLifecycle keeps task state, usage and interaction
// cards out of the replay window; they come from the snapshot and Core state.
func TestRestoreIgnoresReplayedLifecycle(t *testing.T) {
	m := newTUIModel(nil, nil, "restore-lifecycle")
	m.beginSessionRestore()

	events := []core.Event{
		{Kind: core.EventTaskStarted, Cursor: 2, ExecutionID: "exec-old", TaskID: "task-old", ContextWindow: 64000, ModelProfile: "fast"},
		{Kind: core.EventTaskFinished, Cursor: 3, ExecutionID: "exec-old"},
		{Kind: core.EventApprovalRequired, Cursor: 4, Approval: &core.ApprovalRequest{ID: "approval-old"}},
	}
	for _, event := range events {
		mod, _ := m.processRuntimeEventNoRefresh(event)
		m = mod.(*tuiModel)
	}

	if m.run.running || m.run.runningExecution != "" || m.run.taskSnapshot.TaskID != "" {
		t.Fatalf("replayed lifecycle mutated task state: running=%t execution=%q task=%q",
			m.run.running, m.run.runningExecution, m.run.taskSnapshot.TaskID)
	}
	if m.usage.contextWindow != 0 || m.currentModelProfile != "" {
		t.Fatalf("replayed lifecycle mutated metrics: context=%d profile=%q", m.usage.contextWindow, m.currentModelProfile)
	}
	if m.activeApproval != nil {
		t.Fatalf("replayed approval resurfaced a card")
	}
	if len(m.entries) != 0 {
		t.Fatalf("replayed lifecycle added transcript entries: %+v", m.entries)
	}
}

// TestRestoreSettlesOnIdleWithoutEvents covers sessions whose replay is empty.
func TestRestoreSettlesOnIdleWithoutEvents(t *testing.T) {
	m := newTUIModel(nil, nil, "restore-idle")
	m.beginSessionRestore()

	mod, _ := m.Update(runtimeRestoreSettledMsg{generation: m.run.restoreGeneration})
	m = mod.(*tuiModel)

	if m.run.restoringSession {
		t.Fatalf("idle tick did not settle an empty replay")
	}
}

// TestLiveStreamEndsRestore treats zero-cursor live deltas as real work.
func TestLiveStreamEndsRestore(t *testing.T) {
	m := newTUIModel(nil, nil, "restore-live")
	m.beginSessionRestore()

	mod, _ := m.Update(runtimeEventMsg{event: core.Event{
		Kind:   core.EventModelStream,
		Stream: &core.ModelStreamEvent{Type: "text_delta", Delta: "streaming"},
	}})
	m = mod.(*tuiModel)

	if m.run.restoringSession {
		t.Fatalf("live stream delta must end the restore window")
	}
	if !strings.Contains(m.run.currentStreamText, "streaming") {
		t.Fatalf("live stream delta was dropped: %q", m.run.currentStreamText)
	}
}

// TestFinishRestoreResyncsIdleState adopts Core's idle state after the replay.
func TestFinishRestoreResyncsIdleState(t *testing.T) {
	client := newLazySessionTestCore(t)
	session, err := client.CreateSession(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := client.OpenSessionRuntime(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	m := newTUIModel(client, runtime, session.ID)
	if !m.run.restoringSession {
		t.Fatalf("a model opened with a runtime must start in the restore window")
	}
	m.finishSessionRestore()
	if m.run.restoringSession || m.run.running || m.hasPausedTask() {
		t.Fatalf("idle session state not adopted: restoring=%t running=%t paused=%t",
			m.run.restoringSession, m.run.running, m.hasPausedTask())
	}
}
