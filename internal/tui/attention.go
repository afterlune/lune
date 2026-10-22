package tui

import (
	"log/slog"
	"time"

	core "github.com/afterlune/luneCore"
)

// attentionMinTurnDuration is the shortest completed turn that warrants a
// terminal bell. Short turns finish while the user is still watching, so a bell
// there is pure noise.
const attentionMinTurnDuration = 10 * time.Second

// needsUserDecision reports whether the session is blocked on a decision that
// only the user can make.
func (m *tuiModel) needsUserDecision() bool {
	return m.activeQuestion != nil ||
		m.activeApproval != nil ||
		m.activePlanReview != nil ||
		m.activeRecovery != nil ||
		m.activeToolRecovery != nil
}

// notifyAttention rings the terminal bell. Non-TTY output degrades silently.
func (m *tuiModel) notifyAttention() {
	if m.terminalTitle == nil {
		return
	}
	if err := m.terminalTitle.Notify(); err != nil {
		slog.Debug("failed to write terminal bell", "error", err)
	}
}

// syncAttention rings the bell on rising edges only: when a decision card
// appears. The first call records a baseline so sessions restored with already
// pending cards do not ring on launch.
func (m *tuiModel) syncAttention() {
	decision := m.needsUserDecision()
	if !m.attentionInitialized {
		m.attentionInitialized = true
		m.attentionDecisionShown = decision
		return
	}
	if decision && !m.attentionDecisionShown {
		m.notifyAttention()
	}
	m.attentionDecisionShown = decision
}

// noteTurnFinished records a finished turn for the scrolled-away pill and rings
// the bell for turns long enough that the user probably left the window. Each
// execution notifies at most once.
func (m *tuiModel) noteTurnFinished(executionID string, status core.TaskStatus, duration time.Duration) {
	if status != core.TaskPaused {
		if m.scrolledUp {
			m.unseenCompletion = completionLabel(status, duration)
		} else {
			m.unseenCompletion = ""
		}
	}
	if executionID == "" {
		return
	}
	if status == core.TaskPaused {
		// Paused work surfaces a decision card, which syncAttention rings for.
		// The execution stays unmarked so resumed work can still ring when it ends.
		return
	}
	if m.attentionExecutions == nil {
		m.attentionExecutions = map[string]struct{}{}
	}
	if _, done := m.attentionExecutions[executionID]; done {
		return
	}
	m.attentionExecutions[executionID] = struct{}{}
	if duration < attentionMinTurnDuration {
		return
	}
	m.notifyAttention()
}

// taskStatusLabel summarizes the current execution state for /status.
func (m *tuiModel) taskStatusLabel() string {
	switch {
	case m.running:
		if !m.taskStartedAt.IsZero() {
			return "running (" + formatDuration(time.Since(m.taskStartedAt)) + ")"
		}
		return "running"
	case m.needsUserDecision():
		return "waiting for your decision"
	case m.hasPausedTask():
		return "paused"
	}
	return "idle"
}

// completionLabel renders the scrolled-away pill text for a finished turn.
func completionLabel(status core.TaskStatus, duration time.Duration) string {
	switch status {
	case core.TaskFailed:
		return "! Task failed"
	case core.TaskCancelled, core.TaskDiscarded:
		return "Cancelled"
	}
	if duration >= time.Second {
		return "✓ Finished in " + formatDuration(duration)
	}
	return "✓ Finished"
}
