package tui

import (
	"bytes"
	"testing"
	"time"

	core "github.com/afterlune/luneCore"
)

func newAttentionTestModel(buf *bytes.Buffer) *tuiModel {
	m := newTUIModel(nil, nil, "attention-session")
	m.terminalTitle = &terminalTitleManager{output: buf}
	return m
}

func TestSyncAttentionRingsOncePerDecisionCard(t *testing.T) {
	var out bytes.Buffer
	m := newAttentionTestModel(&out)
	m.syncAttention()
	if out.Len() != 0 {
		t.Fatalf("baseline sync rang the bell: %q", out.String())
	}

	m.activeApproval = &ActiveApproval{}
	m.syncAttention()
	if got := out.String(); got != "\a" {
		t.Fatalf("first decision card wrote %q, want a single bell", got)
	}
	m.syncAttention()
	if out.Len() != 1 {
		t.Fatalf("repeated sync rang again: %q", out.String())
	}

	m.activeApproval = nil
	m.syncAttention()
	m.activeQuestion = &ActiveQuestion{}
	m.syncAttention()
	if out.Len() != 2 {
		t.Fatalf("a second decision card should ring once, got %q", out.String())
	}
}

func TestSyncAttentionSuppressesLaunchBell(t *testing.T) {
	var out bytes.Buffer
	m := newAttentionTestModel(&out)
	m.activeQuestion = &ActiveQuestion{}
	m.syncAttention()
	if out.Len() != 0 {
		t.Fatalf("restored session rang on launch: %q", out.String())
	}
}

func TestNoteTurnFinishedRingsOnlyForLongTurns(t *testing.T) {
	var out bytes.Buffer
	m := newAttentionTestModel(&out)

	m.noteTurnFinished("exec-short", core.TaskCompleted, 2*time.Second)
	if out.Len() != 0 {
		t.Fatalf("short turn rang the bell: %q", out.String())
	}

	m.noteTurnFinished("exec-long", core.TaskCompleted, 12*time.Second)
	if out.Len() != 1 {
		t.Fatalf("long turn did not ring once: %q", out.String())
	}
	m.noteTurnFinished("exec-long", core.TaskCompleted, 12*time.Second)
	if out.Len() != 1 {
		t.Fatalf("same execution rang twice: %q", out.String())
	}

	m.noteTurnFinished("exec-paused", core.TaskPaused, 30*time.Second)
	if out.Len() != 1 {
		t.Fatalf("paused turn should ring through the decision card only: %q", out.String())
	}
}

func TestNoteTurnFinishedRecordsScrolledAwayCompletion(t *testing.T) {
	m := newAttentionTestModel(&bytes.Buffer{})

	m.scrolledUp = true
	m.noteTurnFinished("exec-1", core.TaskCompleted, 65*time.Second)
	if want := "✓ Finished in 1m 5s"; m.unseenCompletion != want {
		t.Fatalf("unseen completion = %q, want %q", m.unseenCompletion, want)
	}

	m.scrolledUp = false
	m.noteTurnFinished("exec-2", core.TaskFailed, 5*time.Second)
	if m.unseenCompletion != "" {
		t.Fatalf("completion recorded while at the bottom: %q", m.unseenCompletion)
	}

	m.scrolledUp = true
	m.noteTurnFinished("exec-3", core.TaskFailed, 5*time.Second)
	if m.unseenCompletion != "! Task failed" {
		t.Fatalf("failed turn label = %q", m.unseenCompletion)
	}
	m.unseenCompletion = ""
	m.noteTurnFinished("exec-4", core.TaskCancelled, 5*time.Second)
	if m.unseenCompletion != "Cancelled" {
		t.Fatalf("cancelled turn label = %q", m.unseenCompletion)
	}
}

func TestTaskStatusLabel(t *testing.T) {
	m := newAttentionTestModel(&bytes.Buffer{})
	if got := m.taskStatusLabel(); got != "idle" {
		t.Fatalf("idle label = %q", got)
	}
	m.running = true
	m.taskStartedAt = time.Now().Add(-70 * time.Second)
	if got := m.taskStatusLabel(); got != "running (1m 10s)" {
		t.Fatalf("running label = %q", got)
	}
	m.running = false
	m.taskStartedAt = time.Time{}
	m.activeToolRecovery = &ActiveToolRecovery{}
	if got := m.taskStatusLabel(); got != "waiting for your decision" {
		t.Fatalf("decision label = %q", got)
	}
	m.activeToolRecovery = nil
	m.pausedCheckpoint = &PausedCheckpoint{ExecutionID: "paused"}
	if got := m.taskStatusLabel(); got != "paused" {
		t.Fatalf("paused label = %q", got)
	}
}
