package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

func (m *tuiModel) startDraftSession() (tea.Model, tea.Cmd) {
	if err := m.flushQuestionDraft(context.Background()); err != nil {
		m.addSystemEntry("Could not save question draft; staying in the current session: " + err.Error())
		m.updateLayout()
		return m, nil
	}
	if m.runtime != nil {
		if err := m.runtime.Close(); err != nil {
			m.addSystemEntry("Failed to close the current session: " + err.Error())
			return m, nil
		}
	}
	if m.run.runtimeCancel != nil {
		m.run.runtimeCancel()
		m.run.runtimeCancel = nil
	}
	m.replaceSessionBinding()

	profile := m.pendingModelProfile
	effort := m.reasoningEffort

	m.roundsCollapsedByUser = false
	m.resetSessionState()
	m.sessionID = ""
	m.runtime = nil
	m.sessionTitle = ""
	m.pendingTitle = ""
	m.pendingModelProfile = profile
	m.reasoningEffort = effort
	m.refreshMainContextWindow()
	m.updateLayout()
	m.addSystemEntry("Started a fresh session. It is saved with your first message.")
	return m, nil
}

type compactDoneMsg struct {
	result            core.CompactionResult
	err               error
	sessionID         string
	bindingGeneration uint64
}

type sessionForkedMsg struct {
	session           core.Session
	sessionID         string
	bindingGeneration uint64
	err               error
}

type taskDiscardedMsg struct {
	source            discardSource
	discarded         bool
	err               error
	bindingGeneration uint64
}

type discardSource uint8

const (
	discardSourcePausedTask discardSource = iota
	discardSourceModelRecovery
)

type queuedTurnCancelledMsg struct {
	executionID       string
	bindingGeneration uint64
	err               error
}

// handleRewindPreview displays a non-destructive summary of which turns would be rewound.
func (m *tuiModel) handleRewindPreview(turns int) (tea.Model, tea.Cmd) {
	var userIndices []int
	for i, entry := range m.entries {
		if entry.Kind == KindUser && entry.Text != "" {
			userIndices = append(userIndices, i)
		}
	}
	if len(userIndices) == 0 {
		m.addSystemEntry("No user turns in session history to rewind.")
		return m, nil
	}

	actualTurns := turns
	if actualTurns > len(userIndices) {
		actualTurns = len(userIndices)
	}

	startUserIdx := userIndices[len(userIndices)-actualTurns]
	affectedEntries := m.entries[startUserIdx:]

	userCount := 0
	assistantCount := 0
	toolCount := 0
	var userPrompts []string

	for _, entry := range affectedEntries {
		switch entry.Kind {
		case KindUser:
			userCount++
			userPrompts = append(userPrompts, entry.Text)
		case KindAssistant:
			assistantCount++
		case KindTool:
			toolCount++
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Rewind Preview (%d turn(s) would be removed):**\n\n", userCount))
	for i, p := range userPrompts {
		turnNum := len(userIndices) - userCount + i + 1
		sb.WriteString(fmt.Sprintf("- **Turn %d:** %q\n", turnNum, truncateString(p, 60)))
	}
	sb.WriteString(fmt.Sprintf("\n↳ Impact: %d assistant response(s), %d tool execution(s) would be undone.\n", assistantCount, toolCount))
	sb.WriteString(fmt.Sprintf("Run `/rewind %d` to execute this rewind.", userCount))

	m.addSystemEntry(sb.String())
	return m, nil
}

// handleRewindExecute performs the rewind of turns, reloads session history, and restores the rewound prompt to the input composer.
func (m *tuiModel) handleRewindExecute(turns int) (tea.Model, tea.Cmd) {
	var lastRewoundPrompt string
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindUser && m.entries[i].Text != "" {
			lastRewoundPrompt = m.entries[i].Text
			break
		}
	}

	ctx := context.Background()
	removed, err := m.client.RewindSession(ctx, m.sessionID, turns)
	if err != nil {
		m.addSystemEntry("Rewind failed: " + err.Error())
		return m, nil
	}
	if removed == 0 {
		m.addSystemEntry("No turns were rewound (session history is already at the beginning).")
		return m, nil
	}

	saved, err := m.client.GetSession(ctx, m.sessionID)
	if err != nil {
		m.addSystemEntry(fmt.Sprintf("Rewound %d turn(s), but failed to reload session: %v", removed, err))
		return m, nil
	}

	m.sessionTitle = saved.Title
	m.entries = transcriptFromHistory(saved.History)
	if m.entries == nil {
		m.entries = []TranscriptEntry{}
	}
	m.input.history = nil
	for _, entry := range m.entries {
		if entry.Kind == KindUser && entry.Text != "" {
			m.input.history = append(m.input.history, entry.Text)
		}
	}
	m.input.historyIndex = len(m.input.history)
	m.input.historyDraft = ""
	m.run.resetStream()
	m.scrolledUp = false
	m.view.invalidateBlocks()
	m.refreshViewportContent()

	if lastRewoundPrompt != "" {
		m.input.composer.SetValue(lastRewoundPrompt)
		m.input.composer.CursorEnd()
		m.addSystemEntry(fmt.Sprintf("Rewound %d turn(s). Restored prompt to input composer: %q", removed, truncateString(lastRewoundPrompt, 40)))
	} else {
		m.addSystemEntry(fmt.Sprintf("Rewound %d turn(s).", removed))
	}
	m.updateLayout()
	return m, nil
}

// handleResumeCommand handles switching to a session by ID/prefix or opening the modal.
func (m *tuiModel) handleResumeCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.taskBusy() {
		m.addSystemEntry("Cannot switch sessions while a task is running. Cancel or wait for it to finish.")
		return m, nil
	}
	if m.client == nil {
		m.addSystemEntry("Session management unavailable.")
		return m, nil
	}
	if len(parts) > 2 {
		m.addSystemEntry("Usage: `/resume [session-id|prefix]`.")
		return m, nil
	}
	if len(parts) < 2 {
		return m.openSessionsModal()
	}
	targetPrefix := parts[1]
	targetID, matches, err := m.resolveSessionID(targetPrefix)
	if err != nil {
		m.addSystemEntry("Failed to query sessions: " + err.Error())
		return m, nil
	}
	if targetID == "" {
		if len(matches) == 0 {
			m.addSystemEntry(fmt.Sprintf("No session found matching prefix `%s`.", targetPrefix))
			return m, nil
		}
		var ids []string
		for _, match := range matches {
			short := match.ID
			if len(short) > 12 {
				short = short[:12]
			}
			ids = append(ids, short)
		}
		m.addSystemEntry(fmt.Sprintf("Prefix `%s` is ambiguous. Matches: %s", targetPrefix, strings.Join(ids, ", ")))
		return m, nil
	}

	if targetID == m.sessionID {
		m.addSystemEntry("Already in session: " + targetID)
		return m, nil
	}

	return m.switchToSession(targetID)
}

// openSessionsModalWithFilter queries workspace sessions and opens the modal with an optional pre-set filter.
func (m *tuiModel) openSessionsModalWithFilter(filter string) (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.addSystemEntry("Session management unavailable.")
		return m, nil
	}
	sessions, err := m.client.ListSessions(context.Background(), 100, false)
	if err != nil {
		m.addSystemEntry("Failed to query sessions: " + err.Error())
		return m, nil
	}
	m.modals.open()
	m.modals.sessions.open = true
	m.input.autocompleteOpen = false
	m.modals.sessions.confirmDelete = false
	m.modals.sessions.deletingID = ""
	m.modals.sessions.deleteMode = false
	m.modals.sessions.items = sessions
	m.modals.sessions.filter = strings.TrimSpace(filter)
	m.filterSessions()
	m.modals.sessions.index = 0
	for i, s := range m.modals.sessions.filtered {
		if s.ID == m.sessionID {
			m.modals.sessions.index = i
			break
		}
	}
	m.updateLayout()
	return m, nil
}

// openSessionsModal queries workspace sessions and displays the interactive session switcher modal.
func (m *tuiModel) openSessionsModal() (tea.Model, tea.Cmd) {
	return m.openSessionsModalWithFilter("")
}

func (m *tuiModel) resolveSessionID(prefix string) (string, []core.Session, error) {
	ctx := context.Background()
	if _, err := m.client.GetSession(ctx, prefix); err == nil {
		return prefix, nil, nil
	}
	sessions, err := m.client.ListSessions(ctx, 100, false)
	if err != nil {
		return "", nil, err
	}
	var matches []core.Session
	for _, session := range sessions {
		if strings.HasPrefix(strings.ToLower(session.ID), strings.ToLower(prefix)) {
			matches = append(matches, session)
		}
	}
	if len(matches) == 1 {
		return matches[0].ID, matches, nil
	}
	return "", matches, nil
}

// handleDropCommand deletes a saved session matching the target prefix or ID.
func (m *tuiModel) handleDropCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.taskBusy() {
		m.addSystemEntry("Cannot delete sessions while a task is running.")
		return m, nil
	}
	if m.client == nil {
		m.addSystemEntry("Session management unavailable.")
		return m, nil
	}
	if len(parts) < 2 {
		mod, cmd := m.openSessionsModal()
		if m.modals.sessions.open {
			m.modals.sessions.deleteMode = true
			m.modals.sessions.index = -1
			for i, session := range m.modals.sessions.filtered {
				if session.ID != m.sessionID {
					m.modals.sessions.index = i
					break
				}
			}
			if m.modals.sessions.index < 0 {
				m.addSystemEntry("No other saved sessions are available to delete.")
			}
			m.updateLayout()
		}
		return mod, cmd
	}
	targetPrefix := parts[1]
	targetID, matches, err := m.resolveSessionID(targetPrefix)
	if err != nil {
		m.addSystemEntry("Failed to query sessions: " + err.Error())
		return m, nil
	}
	if targetID == "" {
		if len(matches) == 0 {
			m.addSystemEntry(fmt.Sprintf("No session found matching prefix `%s`.", targetPrefix))
			return m, nil
		}
		mod, cmd := m.openSessionsModal()
		if m.modals.sessions.open {
			m.modals.sessions.deleteMode = true
			m.modals.sessions.index = -1
			for i, session := range m.modals.sessions.filtered {
				if session.ID != m.sessionID {
					m.modals.sessions.index = i
					break
				}
			}
			m.updateLayout()
		}
		return mod, cmd
	}

	if targetID == m.sessionID {
		m.addSystemEntry("Cannot delete currently active session. Switch to another session first with `/resume`.")
		return m, nil
	}

	mod, cmd := m.openSessionsModalWithFilter("")
	if !m.modals.sessions.open {
		return mod, cmd
	}
	m.modals.sessions.deleteMode = true
	for i, session := range m.modals.sessions.filtered {
		if session.ID == targetID {
			m.modals.sessions.index = i
			m.modals.sessions.deletingID = targetID
			m.modals.sessions.confirmDelete = true
			break
		}
	}
	if !m.modals.sessions.confirmDelete {
		m.addSystemEntry("Session is no longer available to delete: " + targetID)
	}
	m.updateLayout()
	return mod, cmd
}

// filterSessions updates sessionsFiltered according to the current sessionsFilter.
func (m *tuiModel) filterSessions() {
	query := strings.ToLower(strings.TrimSpace(m.modals.sessions.filter))
	if query == "" {
		m.modals.sessions.filtered = m.modals.sessions.items
	} else {
		var matches []core.Session
		for _, s := range m.modals.sessions.items {
			if strings.Contains(strings.ToLower(s.ID), query) || strings.Contains(strings.ToLower(s.Title), query) {
				matches = append(matches, s)
			}
		}
		m.modals.sessions.filtered = matches
	}
	if m.modals.sessions.index >= len(m.modals.sessions.filtered) {
		m.modals.sessions.index = max(0, len(m.modals.sessions.filtered)-1)
	}
}

// switchToSession switches to the specified session runtime and refreshes TUI state.
func (m *tuiModel) switchToSession(targetID string) (tea.Model, tea.Cmd) {
	if m.taskBusy() {
		m.addSystemEntry("Cannot switch sessions while a task is running. Cancel or wait for it to finish.")
		return m, nil
	}
	ctx := context.Background()
	if err := m.bindSessionRuntime(ctx, targetID); err != nil {
		m.addSystemEntry("Failed to open session: " + err.Error())
		m.updateLayout()
		return m, nil
	}
	m.addSystemEntry("Resumed session: " + targetID)
	return m, m.subscribeRuntimeEvents()
}

// handleSessionsCommand handles the /sessions command.
func (m *tuiModel) handleSessionsCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.addSystemEntry("Session management unavailable.")
		return m, nil
	}
	query := ""
	if len(parts) > 1 {
		query = strings.ToLower(strings.TrimSpace(strings.Join(parts[1:], " ")))
	}
	sessions, err := m.client.ListSessions(context.Background(), 100, false)
	if err != nil {
		m.addSystemEntry("Failed to list sessions: " + err.Error())
		return m, nil
	}
	var lines []string
	if query == "" {
		lines = append(lines, fmt.Sprintf("**Recent sessions (%d):**", len(sessions)))
	} else {
		lines = append(lines, fmt.Sprintf("**Sessions matching %q:**", query))
	}
	count := 0
	for _, session := range sessions {
		if query != "" && !strings.Contains(strings.ToLower(session.ID), query) && !strings.Contains(strings.ToLower(session.Title), query) {
			continue
		}
		shortID := session.ID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		title := session.Title
		if title == "" {
			title = "<untitled>"
		}
		marker := "  "
		if session.ID == m.sessionID {
			marker = "› "
		}
		lines = append(lines, fmt.Sprintf("%s`%s` %s · %s", marker, shortID, title, session.UpdatedAt.Format("2006-01-02 15:04")))
		count++
	}
	if count == 0 {
		m.addSystemEntry("No matching sessions found.")
		return m, nil
	}
	lines = append(lines, "\nUse `/resume <id>` to switch sessions.")
	m.addSystemEntry(strings.Join(lines, "\n"))
	return m, nil
}

// handleRewindCommand handles the /rewind command.
func (m *tuiModel) handleRewindCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.taskBusy() {
		m.addSystemEntry("Cannot rewind while a task is running.")
		return m, nil
	}
	if m.client == nil {
		m.addSystemEntry("Core client unavailable.")
		return m, nil
	}

	isPreview := false
	turns := 1
	for _, arg := range parts[1:] {
		lower := strings.ToLower(arg)
		if lower == "preview" || lower == "-p" || lower == "--preview" {
			isPreview = true
		} else if n, err := strconv.Atoi(arg); err == nil && n > 0 {
			turns = n
		} else {
			m.addSystemEntry(fmt.Sprintf("Invalid argument %q. Usage: `/rewind [N]` or `/rewind preview [N]`", arg))
			return m, nil
		}
	}

	if isPreview {
		return m.handleRewindPreview(turns)
	}
	return m.handleRewindExecute(turns)
}

// handleTitleCommand handles the /title command.
func (m *tuiModel) handleTitleCommand(parts []string) (tea.Model, tea.Cmd) {
	if len(parts) > 1 {
		newTitle := strings.TrimSpace(strings.Join(parts[1:], " "))
		if m.sessionID == "" {
			m.pendingTitle = newTitle
			m.sessionTitle = newTitle
			m.updateLayout()
			m.addSystemEntry(fmt.Sprintf("Title `%s` will be used when the session starts with your first message.", newTitle))
			return m, nil
		}
		if m.client != nil {
			if err := m.client.SetSessionTitle(context.Background(), m.sessionID, newTitle); err != nil {
				m.addSystemEntry("Failed to set session title: " + err.Error())
				return m, nil
			}
		}
		m.sessionTitle = newTitle
		m.updateLayout()
		m.addSystemEntry(fmt.Sprintf("Session title updated to `%s`.", newTitle))
	} else {
		title := m.sessionTitle
		if title == "" {
			title = "<untitled>"
		}
		sessionID := m.sessionID
		if sessionID == "" {
			sessionID = "(pending)"
		}
		m.addSystemEntry(fmt.Sprintf("Current session title: `%s` (ID: `%s`)", title, sessionID))
	}
	return m, nil
}
