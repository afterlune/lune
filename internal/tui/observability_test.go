package tui

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/afterlune/luneCore"
)

func TestRunningToolShowsLiveElapsed(t *testing.T) {
	entry := TranscriptEntry{
		Kind:          KindTool,
		ToolName:      "run_command",
		ToolCommand:   "npm test",
		ToolRunning:   true,
		ToolStartedAt: time.Now().Add(-125 * time.Second),
	}
	rendered := renderToolEntry(entry, 80)
	if !strings.Contains(rendered, "(2m 5s)") {
		t.Fatalf("running tool card has no live elapsed badge:\n%s", rendered)
	}
}

func TestRunningToolHidesSubSecondElapsed(t *testing.T) {
	entry := TranscriptEntry{
		Kind:          KindTool,
		ToolName:      "run_command",
		ToolCommand:   "npm test",
		ToolRunning:   true,
		ToolStartedAt: time.Now(),
	}
	if rendered := renderToolEntry(entry, 80); strings.Contains(rendered, "(0s)") {
		t.Fatalf("running tool card showed a zero badge:\n%s", rendered)
	}
}

func TestFinishedToolKeepsFinalDuration(t *testing.T) {
	entry := TranscriptEntry{
		Kind:         KindTool,
		ToolName:     "run_command",
		ToolCommand:  "npm test",
		ToolDuration: 2 * time.Second,
	}
	if rendered := renderToolEntry(entry, 80); !strings.Contains(rendered, "(2s)") {
		t.Fatalf("finished tool card lost its duration:\n%s", rendered)
	}
}

func TestRunningSubagentShowsLiveElapsed(t *testing.T) {
	entry := TranscriptEntry{
		Kind:            KindSubagent,
		AgentID:         "researcher",
		AgentName:       "researcher",
		SubagentRunning: true,
		Timestamp:       time.Now().Add(-75 * time.Second),
	}
	rendered := renderEntry(entry, 80)
	if !strings.Contains(rendered, "1m 15s") {
		t.Fatalf("running subagent has no live elapsed time:\n%s", rendered)
	}
}

func TestScrollPillReflectsTaskState(t *testing.T) {
	m := newTUIModel(nil, nil, "pill-session")
	m.taskStartedAt = time.Now().Add(-70 * time.Second)
	m.running = true
	if got := scrollPillText(m); !strings.Contains(got, "⏳ Working 1m 10s") {
		t.Fatalf("running pill = %q", got)
	}

	m.running = false
	m.pausedCheckpoint = &PausedCheckpoint{ExecutionID: "paused"}
	if got := scrollPillText(m); !strings.Contains(got, "⏸ Paused") {
		t.Fatalf("paused pill = %q", got)
	}

	m.pausedCheckpoint = nil
	m.unseenCompletion = "✓ Finished in 3m 12s"
	if got := scrollPillText(m); !strings.Contains(got, "✓ Finished in 3m 12s") {
		t.Fatalf("finished pill = %q", got)
	}
	if rendered := renderScrollPill(m, 80); !strings.Contains(rendered, "Back to bottom") {
		t.Fatalf("pill lost its return hint:\n%s", rendered)
	}

	m.unseenCompletion = ""
	if got := scrollPillText(m); got != "↓ Back to bottom · esc" {
		t.Fatalf("idle pill = %q", got)
	}
}

func TestFooterShowsQueuedCount(t *testing.T) {
	m := newTUIModel(nil, nil, "footer-session")
	m.width, m.height = 120, 40
	m.running = true
	m.taskStartedAt = time.Now()
	m.queuedTurns = []QueuedItem{{ExecutionID: "q1"}, {ExecutionID: "q2"}}

	footer := renderFooter(m, 120)
	if !strings.Contains(footer, "⏳ 2 queued") {
		t.Fatalf("footer has no queued badge:\n%s", footer)
	}
}

func TestStatusTableShowsTaskAndQueue(t *testing.T) {
	m := newTUIModel(nil, nil, "status-session")
	m.running = true
	m.taskStartedAt = time.Now().Add(-65 * time.Second)
	m.queuedTurns = []QueuedItem{{ExecutionID: "q1"}}

	status := m.renderStatusTable()
	if !strings.Contains(status, "Task:") || !strings.Contains(status, "running (1m 5s)") {
		t.Fatalf("status table has no running task line:\n%s", status)
	}
	if !strings.Contains(status, "Queued:") || !strings.Contains(status, "1 waiting") {
		t.Fatalf("status table has no queue line:\n%s", status)
	}
}

func TestTerminalTitleCarriesStateMarker(t *testing.T) {
	m := newTUIModel(nil, nil, "title-session")
	m.workspace = filepath.Join("work", "demo")
	m.sessionTitle = "Refactor"

	if got := m.desiredTerminalTitle(); got != "Refactor | demo" {
		t.Fatalf("idle title = %q", got)
	}

	m.running = true
	if got := m.desiredTerminalTitle(); got != "running · Refactor | demo" {
		t.Fatalf("running title = %q", got)
	}

	m.running = false
	m.activeApproval = &ActiveApproval{}
	if got := m.desiredTerminalTitle(); got != "waiting · Refactor | demo" {
		t.Fatalf("decision title = %q", got)
	}

	m.activeApproval = nil
	m.pausedCheckpoint = &PausedCheckpoint{ExecutionID: "paused"}
	if got := m.desiredTerminalTitle(); got != "paused · Refactor | demo" {
		t.Fatalf("paused title = %q", got)
	}
}

func TestTerminalTitleNotifyWritesBell(t *testing.T) {
	var out bytes.Buffer
	manager := &terminalTitleManager{output: &out}
	if err := manager.Notify(); err != nil {
		t.Fatalf("Notify returned error: %v", err)
	}
	if out.String() != "\a" {
		t.Fatalf("Notify wrote %q, want a bell", out.String())
	}
}

func TestCompletionLabel(t *testing.T) {
	cases := []struct {
		status   core.TaskStatus
		duration time.Duration
		want     string
	}{
		{core.TaskCompleted, 3*time.Minute + 12*time.Second, "✓ Finished in 3m 12s"},
		{core.TaskCompleted, 0, "✓ Finished"},
		{core.TaskFailed, time.Minute, "! Task failed"},
		{core.TaskCancelled, time.Minute, "⏹ Cancelled"},
	}
	for _, tc := range cases {
		if got := completionLabel(tc.status, tc.duration); got != tc.want {
			t.Fatalf("completionLabel(%s, %s) = %q, want %q", tc.status, tc.duration, got, tc.want)
		}
	}
}
