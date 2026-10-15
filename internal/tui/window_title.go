package tui

import (
	"context"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	core "github.com/afterlune/luneCore"
)

const (
	defaultWindowTitle    = "lune"
	windowTitleTickPeriod = 200 * time.Millisecond
)

var windowTitleSpinner = [...]string{"|", "/", "-", "\\"}

type windowTitleTickMsg struct {
	sessionID string
	token     uint64
}

type sessionWindowTitleMsg struct {
	sessionID string
	revision  uint64
	title     string
	err       error
}

func (m *tuiModel) setSessionWindowTitle(saved core.Session) {
	m.windowTitle = terminalTitleForSession(saved)
	m.windowTitleRevision++
}

func (m *tuiModel) windowTitleText() string {
	title := safeWindowTitle(m.windowTitle)
	if title == "" {
		title = defaultWindowTitle
	}
	if m.hasPendingUserQuestion() {
		return "! " + title
	}
	if m.running {
		frame := m.windowTitleFrame % len(windowTitleSpinner)
		return windowTitleSpinner[frame] + " " + title
	}
	return title
}

func (m *tuiModel) hasPendingUserQuestion() bool {
	return m.question != nil || (m.pendingRequest != nil && m.pendingRequest.Kind == core.PendingUserQuestion)
}

func (m *tuiModel) startWindowTitleAnimation() tea.Cmd {
	m.windowTitleAnimationToken++
	m.windowTitleFrame = 0
	if !m.running || m.hasPendingUserQuestion() {
		return nil
	}
	return nextWindowTitleTick(m.sessionID, m.windowTitleAnimationToken)
}

func nextWindowTitleTick(sessionID string, token uint64) tea.Cmd {
	return tea.Tick(windowTitleTickPeriod, func(time.Time) tea.Msg {
		return windowTitleTickMsg{sessionID: sessionID, token: token}
	})
}

func safeWindowTitle(title string) string {
	title = strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return -1
		}
		return char
	}, title)
	return strings.TrimSpace(title)
}

func (m *tuiModel) loadSessionWindowTitle() tea.Cmd {
	if m.client == nil || m.sessionID == "" {
		return nil
	}
	client := m.client
	sessionID := m.sessionID
	revision := m.windowTitleRevision
	return func() tea.Msg {
		saved, err := client.GetSession(context.Background(), sessionID)
		if err != nil {
			return sessionWindowTitleMsg{sessionID: sessionID, revision: revision, err: err}
		}
		return sessionWindowTitleMsg{sessionID: sessionID, revision: revision, title: terminalTitleForSession(saved)}
	}
}

func terminalTitleForSession(saved core.Session) string {
	if strings.TrimSpace(saved.Title) == "" && len(rawHistoryItems(saved.History)) == 0 {
		return defaultWindowTitle
	}
	return sessionDisplayTitle(saved)
}
