package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

func lastEntryText(m *tuiModel) string {
	if len(m.entries) == 0 {
		return ""
	}
	return m.entries[len(m.entries)-1].Text
}

func TestCompactDoneMessages(t *testing.T) {
	t.Run("compacted", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-compact")
		m.usage.estimatedInputTokens = 621_000
		m.usage.lastInputTokens = 700_000
		m.usage.lastCachedTokens = 500_000
		m.usage.compacting = true
		m.Update(compactDoneMsg{result: core.CompactionResult{Backend: "local", Turns: 3, Before: 621_000, After: 92_000, Compacted: true}, sessionID: "session-compact"})

		if m.usage.compacting {
			t.Fatalf("compaction result must clear the in-progress state")
		}
		if m.usage.lastInputTokens != 0 || m.usage.lastCachedTokens != 0 || m.usage.estimatedInputTokens != 92_000 {
			t.Fatalf("stale context metrics survived compaction: last=%d cached=%d estimated=%d",
				m.usage.lastInputTokens, m.usage.lastCachedTokens, m.usage.estimatedInputTokens)
		}
		if got := lastEntryText(m); got != "Compacted 3 older turns locally: 621k → 92k tokens (local summary)." {
			t.Fatalf("compacted message = %q", got)
		}
	})

	t.Run("server compaction does not show an after-token count", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-compact")
		m.Update(compactDoneMsg{result: core.CompactionResult{Backend: "server", Turns: 2, Before: 621_000, After: 92_000, Compacted: true}, sessionID: "session-compact"})
		got := lastEntryText(m)
		if !strings.Contains(got, "provider compaction") || !strings.Contains(got, "621k") || strings.Contains(got, "92k") {
			t.Fatalf("server compacted message = %q", got)
		}
	})

	t.Run("fallback result identifies the fallback", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-compact")
		m.Update(compactDoneMsg{result: core.CompactionResult{
			Backend: "local", Turns: 1, Before: 2400, After: 900, Fallback: true,
			FallbackReason: "standalone endpoint unsupported", Compacted: true,
		}, sessionID: "session-compact"})
		if got := lastEntryText(m); !strings.Contains(got, "Fallback: standalone endpoint unsupported") {
			t.Fatalf("fallback message = %q", got)
		}
	})

	t.Run("fallback result keeps the server and local reasons", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-compact")
		m.Update(compactDoneMsg{result: core.CompactionResult{
			Backend: "local", Turns: 1, Before: 104000, After: 73249, Fallback: true,
			FallbackReason: "provider compaction is unsupported; context remains above the 51875-token target after 2 summary passes (73249 tokens)",
			Compacted:      true,
		}, sessionID: "session-compact"})
		got := lastEntryText(m)
		if !strings.Contains(got, "provider compaction is unsupported") || !strings.Contains(got, "above the 51875-token target") {
			t.Fatalf("fallback message lost its causes: %q", got)
		}
	})

	t.Run("nothing to compact", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-compact")
		m.usage.estimatedInputTokens = 621_000
		m.usage.compacting = true
		m.Update(compactDoneMsg{sessionID: "session-compact"})

		if m.usage.compacting {
			t.Fatalf("an empty compaction must not change the footer state")
		}
		if m.usage.estimatedInputTokens != 621_000 {
			t.Fatalf("empty compaction changed the estimate: %d", m.usage.estimatedInputTokens)
		}
		if got := lastEntryText(m); got != "No older conversation to compact." {
			t.Fatalf("empty message = %q", got)
		}
	})

	t.Run("failed", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-compact")
		m.usage.compacting = true
		m.Update(compactDoneMsg{err: errString("summary provider unavailable"), sessionID: "session-compact"})

		if m.usage.compacting {
			t.Fatalf("failed compaction must not fake a compacted footer")
		}
		if got := lastEntryText(m); got != "Compaction failed: summary provider unavailable" {
			t.Fatalf("failure message = %q", got)
		}
	})

	t.Run("stale session", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-b")
		m.Update(compactDoneMsg{result: core.CompactionResult{Turns: 2, Compacted: true}, sessionID: "session-a"})
		if len(m.entries) != 0 {
			t.Fatalf("result from another session must be ignored: %+v", m.entries)
		}
	})
}

type errString string

func (e errString) Error() string { return string(e) }

func TestCompactCommandStartsAndGuards(t *testing.T) {
	m := newTUIModel(nil, nil, "session-command")
	_, cmd := m.handleSlashCommand("/compact")
	if cmd == nil || !m.usage.compacting || m.usage.compactionStartedAt.IsZero() {
		t.Fatalf("compact command did not start: compacting=%t started=%v cmd=%v", m.usage.compacting, m.usage.compactionStartedAt, cmd)
	}
	if got := lastEntryText(m); got != "Compacting older conversation history…" {
		t.Fatalf("start message = %q", got)
	}

	_, _ = m.handleSlashCommand("/compact")
	if got := lastEntryText(m); got != "Compaction is already in progress." {
		t.Fatalf("re-entry message = %q", got)
	}

	mod, _ := m.submitTurn("hello")
	m = mod.(*tuiModel)
	if m.run.running {
		t.Fatalf("submitting during compaction must be refused")
	}
	if got := lastEntryText(m); got != "Compaction is in progress; wait for it to finish." {
		t.Fatalf("submit guard message = %q", got)
	}
}

func TestCompactEscDoesNotCancelOrRecallHistory(t *testing.T) {
	m := newTUIModel(nil, nil, "session-esc")
	m.input.history = []string{"previous prompt"}
	m.input.historyIndex = len(m.input.history)
	m.usage.compacting = true

	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})

	if got := m.input.composer.Value(); got != "" {
		t.Fatalf("esc recalled history during compaction: %q", got)
	}
	if !m.usage.compacting {
		t.Fatalf("esc must not pretend to cancel compaction")
	}
}

func TestCompactionFooterStates(t *testing.T) {
	m := newTUIModel(nil, nil, "session-footer")
	m.usage.contextWindow = 1_000_000
	m.usage.estimatedInputTokens = 621_000

	m.usage.compacting = true
	if got := formatContextAndTokenSummary(m, 0); !strings.Contains(got, "Context: compacting…/1M") {
		t.Fatalf("compacting footer = %q", got)
	}

	m.usage.compacting = false
	m.usage.estimatedInputTokens = 92_000
	if got := formatContextAndTokenSummary(m, 0); !strings.Contains(got, "Context: 92k/1M (9.2%)") {
		t.Fatalf("compacted footer = %q", got)
	}

	mod, _ := m.processRuntimeEventNoRefresh(core.Event{
		Kind:                 core.EventUsage,
		AgentID:              "lead",
		UsagePurpose:         core.UsagePurposeMain,
		Usage:                core.Usage{InputTokens: 92_000},
		EstimatedInputTokens: 92_000,
		ContextWindow:        1_000_000,
	})
	m = mod.(*tuiModel)

	if got := formatContextAndTokenSummary(m, 0); !strings.Contains(got, "Context: 92k/1M (9.2%)") {
		t.Fatalf("recovered footer = %q", got)
	}
}

func TestCompactionAutoNotices(t *testing.T) {
	t.Run("started", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-auto")
		m.handleCompactionEvent(core.ContextCompactionDetails{Stage: "started", Mode: "local", Threshold: 600_000})
		if !m.usage.compacting {
			t.Fatalf("started compaction must mark the model as compacting")
		}
		if got := m.ensureAgentMetrics("lead").Activity; got != "Compacting context" {
			t.Fatalf("lead activity = %q", got)
		}
	})

	t.Run("completed local", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-auto")
		m.usage.compacting = true
		m.usage.lastInputTokens = 700_000
		m.handleCompactionEvent(core.ContextCompactionDetails{
			Stage: "completed", Mode: "local", BeforeTokens: 621_000, AfterTokens: 92_000,
		})
		if m.usage.compacting || m.usage.lastInputTokens != 0 || m.usage.estimatedInputTokens != 92_000 {
			t.Fatalf("completed compaction metrics: compacting=%t last=%d estimated=%d",
				m.usage.compacting, m.usage.lastInputTokens, m.usage.estimatedInputTokens)
		}
		if got := lastEntryText(m); got != "Context compacted: 621k → 92k tokens (local summary)." {
			t.Fatalf("completed message = %q", got)
		}
	})

	t.Run("completed server", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-auto")
		m.handleCompactionEvent(core.ContextCompactionDetails{Stage: "completed", Mode: "server", BeforeTokens: 621_000})
		if got := lastEntryText(m); got != "Context compacted by the provider: was 621k tokens." {
			t.Fatalf("server message = %q", got)
		}
	})

	t.Run("manual event updates state without duplicate notice", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-auto")
		m.usage.compacting = true
		m.handleCompactionEvent(core.ContextCompactionDetails{
			Stage: "completed", Mode: "server", Reason: "manual", BeforeTokens: 621_000, AfterTokens: 92_000,
		})
		if m.usage.compacting {
			t.Fatal("manual completion event left compaction active")
		}
		if len(m.entries) != 0 {
			t.Fatalf("manual event duplicated the command result notice: %#v", m.entries)
		}
	})

	t.Run("fallback", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-auto")
		m.usage.compacting = true
		m.handleCompactionEvent(core.ContextCompactionDetails{Stage: "fallback", Mode: "server", Threshold: 600_000})
		if m.usage.compacting {
			t.Fatalf("fallback must clear the compacting state")
		}
		if got := lastEntryText(m); got != "Provider compaction unavailable; using a local summary." {
			t.Fatalf("fallback message = %q", got)
		}
	})

	t.Run("local fallback explains the target miss", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-auto")
		m.handleCompactionEvent(core.ContextCompactionDetails{
			Stage: "fallback", Mode: "local", Err: "local compaction reached 73k tokens, above the 51k-token target",
		})
		got := lastEntryText(m)
		if !strings.Contains(got, "Local compaction did not reach its target") || !strings.Contains(got, "above the 51k-token target") {
			t.Fatalf("local fallback message = %q", got)
		}
	})

	t.Run("skipped", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-auto")
		m.handleCompactionEvent(core.ContextCompactionDetails{Stage: "skipped", Mode: "local", Err: "contextWindow is unset"})
		if got := lastEntryText(m); got != "Automatic compaction skipped: contextWindow is unset" {
			t.Fatalf("skipped message = %q", got)
		}
	})

	t.Run("failed", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-auto")
		m.usage.compacting = true
		m.handleCompactionEvent(core.ContextCompactionDetails{Stage: "failed", Mode: "local", Err: "summary model failed"})
		if m.usage.compacting {
			t.Fatalf("failed compaction must clear the compacting state")
		}
		if got := lastEntryText(m); got != "Context compaction failed: summary model failed" {
			t.Fatalf("failed message = %q", got)
		}
	})
}

func TestCompactionTitleAndPill(t *testing.T) {
	m := newTUIModel(nil, nil, "session-title")
	m.usage.compacting = true
	if got := m.terminalStatePrefix(); got != "compacting · " {
		t.Fatalf("terminal prefix = %q", got)
	}
	m.usage.compactionStartedAt = time.Now().Add(-3 * time.Second)
	if got := scrollPillText(m); !strings.HasPrefix(got, "Compacting 3s · ") {
		t.Fatalf("scroll pill = %q", got)
	}
	m.run.running = true
	if got := m.terminalStatePrefix(); got != "running · " {
		t.Fatalf("running must win over compacting in the title: %q", got)
	}
}

func TestCompactionSettingsLabel(t *testing.T) {
	client := newLazySessionTestCore(t)
	m := newTUIModel(client, nil, "")
	m.usage.contextWindow = 1_000_000

	label := m.compactionSettingsLabel()
	if label == "" || !strings.HasPrefix(label, "on at ") {
		t.Fatalf("settings label = %q", label)
	}
	if !strings.Contains(label, "tokens)") {
		t.Fatalf("settings label misses the threshold: %q", label)
	}
}

func TestResetSessionStateClearsCompaction(t *testing.T) {
	m := newTUIModel(nil, nil, "session-reset")
	m.usage.compacting = true
	m.usage.compactionStartedAt = time.Now()

	m.resetSessionState()

	if m.usage.compacting || !m.usage.compactionStartedAt.IsZero() {
		t.Fatalf("session reset kept compaction state")
	}
}

func TestInterruptDuringCompactingRunningTaskWithCtrlC(t *testing.T) {
	m := newTUIModel(nil, nil, "session-interrupt")
	m.run.running = true
	m.run.runningExecution = "exec-test-1"
	m.run.thinking = true
	m.usage.compacting = true
	m.usage.compactionStartedAt = time.Now()

	mod, cmd := m.handleKeyPress(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = mod.(*tuiModel)

	if m.run.running {
		t.Fatalf("Ctrl+C must interrupt running task even when compacting was true")
	}
	if m.run.thinking {
		t.Fatalf("thinking must be false after interrupt")
	}
	if m.usage.compacting {
		t.Fatalf("interrupting running task must reset compacting state")
	}
	if m.pausedCheckpoint != nil {
		t.Fatalf("paused checkpoint must be nil after interrupt: %#v", m.pausedCheckpoint)
	}
	if len(m.entries) == 0 || !strings.Contains(m.entries[len(m.entries)-1].Text, "Task interrupted") {
		t.Fatalf("expected task interrupted notice, got: %#v", m.entries)
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd from interrupt, got %v", cmd)
	}
}

func TestInterruptDuringCompactingRunningTaskWithEsc(t *testing.T) {
	m := newTUIModel(nil, nil, "session-interrupt")
	m.run.running = true
	m.run.runningExecution = "exec-test-2"
	m.run.thinking = true
	m.usage.compacting = true
	m.usage.compactionStartedAt = time.Now()

	mod, cmd := m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = mod.(*tuiModel)

	if m.run.running {
		t.Fatalf("Esc must interrupt running task even when compacting was true")
	}
	if m.usage.compacting {
		t.Fatalf("interrupting running task with Esc must reset compacting state")
	}
	if m.pausedCheckpoint != nil {
		t.Fatalf("paused checkpoint must be nil after interrupt: %#v", m.pausedCheckpoint)
	}
	if len(m.entries) == 0 || !strings.Contains(m.entries[len(m.entries)-1].Text, "Task interrupted") {
		t.Fatalf("expected task interrupted notice, got: %#v", m.entries)
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd from interrupt, got %v", cmd)
	}
}

func TestDiscardResetsCompactingState(t *testing.T) {
	m := newTUIModel(nil, nil, "session-discard")
	m.usage.compacting = true
	m.usage.compactionStartedAt = time.Now()
	m.run.compactInFlight = true
	m.pausedCheckpoint = &PausedCheckpoint{ExecutionID: "exec-test-3"}

	mod, _ := m.Update(taskDiscardedMsg{source: discardSourceCommand, discarded: true})
	m = mod.(*tuiModel)

	if m.usage.compacting {
		t.Fatalf("discarding task must reset compacting")
	}
	if m.run.compactInFlight {
		t.Fatalf("discarding task must clear compactInFlight")
	}
	if m.pausedCheckpoint != nil {
		t.Fatalf("paused checkpoint must be cleared")
	}
}

func TestCompactingQuitKeyDoubleCtrlC(t *testing.T) {
	m := newTUIModel(nil, nil, "session-quit")
	m.usage.compacting = true
	m.run.compactInFlight = true

	// First Ctrl+C: warns and requests confirmation
	mod, cmd := m.handleKeyPress(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = mod.(*tuiModel)
	if cmd != nil {
		t.Fatalf("first Ctrl+C should not quit immediately, got cmd: %v", cmd)
	}
	if !m.run.compactionQuitRequested {
		t.Fatalf("compactionQuitRequested should be set after first Ctrl+C")
	}
	if got := lastEntryText(m); !strings.Contains(got, "Compaction is in progress; press Ctrl+C again to quit.") {
		t.Fatalf("expected double Ctrl+C notice, got: %q", got)
	}

	// Second Ctrl+C: quits cleanly
	mod, cmd = m.handleKeyPress(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = mod.(*tuiModel)
	if cmd == nil {
		t.Fatalf("second Ctrl+C must quit application")
	}
	if m.usage.compacting {
		t.Fatalf("quit must reset compacting")
	}
	if m.run.compactInFlight {
		t.Fatalf("quit must reset compactInFlight")
	}
}
