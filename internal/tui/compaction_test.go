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
		m.estimatedInputTokens = 621_000
		m.lastInputTokens = 700_000
		m.lastCachedTokens = 500_000
		m.compacting = true
		m.Update(compactDoneMsg{trimmed: 3, sessionID: "session-compact"})

		if m.compacting {
			t.Fatalf("compaction result must clear the in-progress state")
		}
		if !m.contextCompacted || m.compactedFromTokens != 621_000 {
			t.Fatalf("manual compaction state = (%t, %d)", m.contextCompacted, m.compactedFromTokens)
		}
		if m.lastInputTokens != 0 || m.lastCachedTokens != 0 || m.estimatedInputTokens != 0 {
			t.Fatalf("stale context metrics survived compaction: last=%d cached=%d estimated=%d",
				m.lastInputTokens, m.lastCachedTokens, m.estimatedInputTokens)
		}
		if got := lastEntryText(m); got != "Compacted 3 older turns (kept the latest 4)." {
			t.Fatalf("compacted message = %q", got)
		}
	})

	t.Run("nothing to compact", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-compact")
		m.estimatedInputTokens = 621_000
		m.compacting = true
		m.Update(compactDoneMsg{trimmed: 0, sessionID: "session-compact"})

		if m.compacting || m.contextCompacted {
			t.Fatalf("an empty compaction must not change the footer state")
		}
		if m.estimatedInputTokens != 621_000 {
			t.Fatalf("empty compaction changed the estimate: %d", m.estimatedInputTokens)
		}
		if got := lastEntryText(m); got != "No older conversation to compact (the latest 4 turns are always kept)." {
			t.Fatalf("empty message = %q", got)
		}
	})

	t.Run("failed", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-compact")
		m.compacting = true
		m.Update(compactDoneMsg{err: errString("summary provider unavailable"), sessionID: "session-compact"})

		if m.compacting || m.contextCompacted {
			t.Fatalf("failed compaction must not fake a compacted footer")
		}
		if got := lastEntryText(m); got != "Compaction failed: summary provider unavailable" {
			t.Fatalf("failure message = %q", got)
		}
	})

	t.Run("stale session", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-b")
		m.Update(compactDoneMsg{trimmed: 2, sessionID: "session-a"})
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
	if cmd == nil || !m.compacting || m.compactionStartedAt.IsZero() {
		t.Fatalf("compact command did not start: compacting=%t started=%v cmd=%v", m.compacting, m.compactionStartedAt, cmd)
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
	if m.running {
		t.Fatalf("submitting during compaction must be refused")
	}
	if got := lastEntryText(m); got != "Compaction is in progress; wait for it to finish." {
		t.Fatalf("submit guard message = %q", got)
	}
}

func TestCompactEscDoesNotCancelOrRecallHistory(t *testing.T) {
	m := newTUIModel(nil, nil, "session-esc")
	m.promptHistory = []string{"previous prompt"}
	m.historyIndex = len(m.promptHistory)
	m.compacting = true

	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})

	if got := m.composer.Value(); got != "" {
		t.Fatalf("esc recalled history during compaction: %q", got)
	}
	if !m.compacting {
		t.Fatalf("esc must not pretend to cancel compaction")
	}
}

func TestCompactionFooterStates(t *testing.T) {
	m := newTUIModel(nil, nil, "session-footer")
	m.contextWindow = 1_000_000
	m.estimatedInputTokens = 621_000

	m.compacting = true
	if got := formatContextAndTokenSummary(m, 0); !strings.Contains(got, "Context: compacting…/1M") {
		t.Fatalf("compacting footer = %q", got)
	}

	m.compacting = false
	m.contextCompacted = true
	m.compactedFromTokens = 621_000
	if got := formatContextAndTokenSummary(m, 0); !strings.Contains(got, "Context: compacted (was 621k)/1M") {
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

	if m.contextCompacted {
		t.Fatalf("the next main usage report must clear the compacted footer")
	}
	if got := formatContextAndTokenSummary(m, 0); !strings.Contains(got, "Context: 92k/1M (9.2%)") {
		t.Fatalf("recovered footer = %q", got)
	}
}

func TestCompactionAutoNotices(t *testing.T) {
	t.Run("started", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-auto")
		m.handleCompactionEvent(core.ContextCompactionDetails{Stage: "started", Mode: "local", Threshold: 600_000})
		if !m.compacting {
			t.Fatalf("started compaction must mark the model as compacting")
		}
		if got := m.ensureAgentMetrics("lead").Activity; got != "Compacting context" {
			t.Fatalf("lead activity = %q", got)
		}
	})

	t.Run("completed local", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-auto")
		m.compacting = true
		m.lastInputTokens = 700_000
		m.handleCompactionEvent(core.ContextCompactionDetails{
			Stage: "completed", Mode: "local", BeforeTokens: 621_000, AfterTokens: 92_000,
		})
		if m.compacting || m.lastInputTokens != 0 || m.estimatedInputTokens != 92_000 {
			t.Fatalf("completed compaction metrics: compacting=%t last=%d estimated=%d",
				m.compacting, m.lastInputTokens, m.estimatedInputTokens)
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

	t.Run("fallback", func(t *testing.T) {
		m := newTUIModel(nil, nil, "session-auto")
		m.compacting = true
		m.handleCompactionEvent(core.ContextCompactionDetails{Stage: "fallback", Mode: "server", Threshold: 600_000})
		if m.compacting {
			t.Fatalf("fallback must clear the compacting state")
		}
		if got := lastEntryText(m); got != "Provider compaction unavailable; fell back to a local summary." {
			t.Fatalf("fallback message = %q", got)
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
		m.compacting = true
		m.handleCompactionEvent(core.ContextCompactionDetails{Stage: "failed", Mode: "local", Err: "summary model failed"})
		if m.compacting {
			t.Fatalf("failed compaction must clear the compacting state")
		}
		if got := lastEntryText(m); got != "Context compaction failed: summary model failed" {
			t.Fatalf("failed message = %q", got)
		}
	})
}

func TestCompactionTitleAndPill(t *testing.T) {
	m := newTUIModel(nil, nil, "session-title")
	m.compacting = true
	if got := m.terminalStatePrefix(); got != "compacting · " {
		t.Fatalf("terminal prefix = %q", got)
	}
	m.compactionStartedAt = time.Now().Add(-3 * time.Second)
	if got := scrollPillText(m); !strings.HasPrefix(got, "Compacting 3s · ") {
		t.Fatalf("scroll pill = %q", got)
	}
	m.running = true
	if got := m.terminalStatePrefix(); got != "running · " {
		t.Fatalf("running must win over compacting in the title: %q", got)
	}
}

func TestCompactionSettingsLabel(t *testing.T) {
	client := newLazySessionTestCore(t)
	m := newTUIModel(client, nil, "")
	m.contextWindow = 1_000_000

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
	m.compacting = true
	m.contextCompacted = true
	m.compactedFromTokens = 621_000
	m.compactionStartedAt = time.Now()

	m.resetSessionState()

	if m.compacting || m.contextCompacted || m.compactedFromTokens != 0 || !m.compactionStartedAt.IsZero() {
		t.Fatalf("session reset kept compaction state")
	}
}
