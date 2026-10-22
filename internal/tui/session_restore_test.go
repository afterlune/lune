package tui

import (
	"encoding/json"
	"testing"

	core "github.com/afterlune/luneCore"
)

// TestRestoreSuppressesUsageChurn keeps the footer metrics frozen while Core
// replays a freshly loaded session's durable events.
func TestRestoreSuppressesUsageChurn(t *testing.T) {
	m := newTUIModel(nil, nil, "restore-usage")
	m.beginSessionRestore()
	m.totalInputTokens = 1000
	m.cachedTokens = 400
	m.lastInputTokens = 900
	m.lastCachedTokens = 350
	m.estimatedInputTokens = 950
	m.contextWindow = 128000

	usage := core.Usage{InputTokens: 500, OutputTokens: 100}
	usage.InputTokensDetails.CachedTokens = 200
	event := core.Event{
		Kind:                 core.EventUsage,
		AgentID:              "lead",
		Usage:                usage,
		ContextWindow:        64000,
		EstimatedInputTokens: 1200,
		RequestID:            "req-replay",
	}
	mod, _ := m.processRuntimeEventNoRefresh(event)
	m = mod.(*tuiModel)

	if m.totalInputTokens != 1000 || m.cachedTokens != 400 {
		t.Fatalf("session totals changed during restore: input=%d cached=%d", m.totalInputTokens, m.cachedTokens)
	}
	if m.lastInputTokens != 900 || m.lastCachedTokens != 350 || m.estimatedInputTokens != 950 || m.contextWindow != 128000 {
		t.Fatalf("footer metrics changed during restore: last=%d cached=%d estimated=%d context=%d",
			m.lastInputTokens, m.lastCachedTokens, m.estimatedInputTokens, m.contextWindow)
	}

	m.finishSessionRestore()
	mod, _ = m.processRuntimeEventNoRefresh(event)
	m = mod.(*tuiModel)

	if m.totalInputTokens != 1500 || m.cachedTokens != 600 {
		t.Fatalf("live usage not accumulated after restore: input=%d cached=%d", m.totalInputTokens, m.cachedTokens)
	}
	if m.lastInputTokens != 500 || m.estimatedInputTokens != 1200 || m.contextWindow != 64000 {
		t.Fatalf("live usage did not update the latest request: last=%d estimated=%d context=%d",
			m.lastInputTokens, m.estimatedInputTokens, m.contextWindow)
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
	renderedBefore := m.cachedEntryCount

	args, err := json.Marshal(map[string]string{"command": "ls"})
	if err != nil {
		t.Fatal(err)
	}
	mod, _ := m.Update(runtimeEventMsg{event: core.Event{
		Kind: core.EventToolStarted,
		Tool: core.ToolCall{ID: "call-1", Name: "run_command", Arguments: args, Started: true},
	}})
	m = mod.(*tuiModel)

	if len(m.entries) != 2 {
		t.Fatalf("replayed tool entry was not appended: %+v", m.entries)
	}
	if m.cachedEntryCount != renderedBefore {
		t.Fatalf("transcript rebuilt during restore: cached=%d entries=%d", m.cachedEntryCount, len(m.entries))
	}
	if !m.restoringSession {
		t.Fatalf("restore finished before the replay settled")
	}

	mod, _ = m.Update(runtimeRestoreSettledMsg{generation: m.restoreGeneration})
	m = mod.(*tuiModel)

	if m.restoringSession {
		t.Fatalf("restore did not settle on the idle tick")
	}
	if m.cachedEntryCount != len(m.entries) {
		t.Fatalf("transcript not rendered after settle: cached=%d entries=%d", m.cachedEntryCount, len(m.entries))
	}
}

// TestRestoreSettlesOnStaleGeneration ignores idle ticks from an earlier restore.
func TestRestoreSettlesOnStaleGeneration(t *testing.T) {
	m := newTUIModel(nil, nil, "restore-stale")
	m.beginSessionRestore()
	stale := m.restoreGeneration
	m.beginSessionRestore()

	mod, _ := m.Update(runtimeRestoreSettledMsg{generation: stale})
	m = mod.(*tuiModel)
	if !m.restoringSession {
		t.Fatalf("a stale idle tick settled the newer restore")
	}

	mod, _ = m.Update(runtimeRestoreSettledMsg{generation: m.restoreGeneration})
	m = mod.(*tuiModel)
	if m.restoringSession {
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

	if m.restoringSession {
		t.Fatalf("starting a turn must stop deferring the transcript restore")
	}
	if !m.running {
		t.Fatalf("expected the submitted turn to be running")
	}
}
