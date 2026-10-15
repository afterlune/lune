package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

type taskClockMsg struct{ sessionID string }

func taskClock(sessionID string) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return taskClockMsg{sessionID: sessionID} })
}

func (m *tuiModel) taskElapsed() time.Duration {
	if m.taskStartedAt.IsZero() {
		return 0
	}
	return max(0, time.Since(m.taskStartedAt).Truncate(time.Second))
}
