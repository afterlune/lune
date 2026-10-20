package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	"github.com/atotto/clipboard"
)

func (m *tuiModel) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	keyStr := msg.String()
	if m.activeQuestion != nil && m.activeQuestion.Submitting && keyStr != "ctrl+c" {
		return m, nil
	}

	if m.shortcutsOpen {
		return m.handleShortcutModalKeyPress(msg, keyStr)
	}
	if m.agentsPanelOpen {
		return m.handleAgentsPanelKeyPress(msg, keyStr)
	}
	if m.sessionsModalOpen {
		return m.handleSessionsModalKeyPress(msg, keyStr)
	}
	if m.toolPagerOpen {
		return m.handleToolPagerModalKeyPress(msg, keyStr)
	}
	if m.diffModalOpen {
		return m.handleDiffModalKeyPress(msg, keyStr)
	}
	// Trigger shortcut modal when typing '?' in an empty composer
	if keyStr == "?" && m.composer.Value() == "" && !m.running {
		m.shortcutsOpen = true
		m.updateLayout()
		return m, nil
	}

	// 1. Interrupt / Quit / Esc
	switch keyStr {
	case "ctrl+c":
		if m.selectedText != "" {
			_ = clipboard.WriteAll(m.selectedText)
			count := utf8.RuneCountInString(m.selectedText)
			m.copyFeedback = fmt.Sprintf("Copied %d chars to host clipboard", count)
			m.copyFeedbackExpiresAt = time.Now().Add(3 * time.Second)
			expiresAt := m.copyFeedbackExpiresAt
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearCopyFeedbackMsg{at: expiresAt}
			})
		}
		if m.activeQuestion != nil {
			delete(m.questionSubmissions, m.questionKey(m.activeQuestion.Request))
			m.activeQuestion = nil
			if m.runtime != nil {
				_, _ = m.runtime.DiscardPaused(context.Background())
			}
			m.running = false
			m.addSystemEntry("Question cancelled.")
			m.updateLayout()
			return m, nil
		}
		if m.running && m.runtime != nil && m.runningExecution != "" {
			return m.interruptRunningTask()
		}
		return m, tea.Quit

	case "esc":
		if m.shortcutsOpen {
			m.shortcutsOpen = false
			m.updateLayout()
			return m, nil
		}
		if m.sessionsModalOpen {
			m.sessionsModalOpen = false
			m.sessionsFilter = ""
			m.sessionsConfirmDelete = false
			m.sessionsDeletingID = ""
			m.updateLayout()
			return m, nil
		}
		if m.toolPagerOpen {
			m.toolPagerOpen = false
			m.updateLayout()
			return m, nil
		}
		if m.diffModalOpen {
			m.diffModalOpen = false
			m.updateLayout()
			return m, nil
		}
		if m.selectedText != "" || m.hasSelection() {
			m.isSelecting = false
			m.selectedText = ""
			m.selectStartX = 0
			m.selectEndX = 0
			m.selectStartY = 0
			m.selectEndY = 0
			return m, nil
		}
		if m.autocompleteOpen {
			m.autocompleteOpen = false
			m.updateLayout()
			return m, nil
		}
		if m.activeRecovery != nil {
			m.activeRecovery = nil
			m.pausedCheckpoint = nil
			if m.runtime != nil {
				_, _ = m.runtime.DiscardPaused(context.Background())
			}
			m.running = false
			m.addSystemEntry("Model recovery discarded.")
			m.updateLayout()
			return m, nil
		}
		if m.activeToolRecovery != nil {
			m.activeToolRecovery.Selected = 1
			return m.resolveActiveToolRecovery()
		}
		if m.activeQuestion != nil {
			m.activeQuestion = nil
			m.pausedCheckpoint = nil
			if m.runtime != nil {
				_, _ = m.runtime.DiscardPaused(context.Background())
			}
			m.running = false
			m.addSystemEntry("Question cancelled.")
			m.updateLayout()
			return m, nil
		}
		if m.activeApproval != nil {
			m.activeApproval.Selected = 1
			return m.resolveActiveApproval()
		}
		if m.effortPicker != nil {
			m.effortPicker = nil
			m.updateLayout()
			return m, nil
		}
		if m.activePlanReview != nil {
			m.activePlanReview = nil
			m.updateLayout()
			return m, nil
		}
		if m.scrolledUp {
			m.scrolledUp = false
			m.viewport.GotoBottom()
			return m, nil
		}
		if len(m.queuedTurns) > 0 {
			return m.cancelQueuedTurn()
		}
		if m.running && m.runtime != nil && m.runningExecution != "" {
			return m.interruptRunningTask()
		}
		// If composer is empty and not running, Esc steps back to edit the last message
		if !m.running && m.composer.Value() == "" && len(m.promptHistory) > 0 {
			m.historyIndex = len(m.promptHistory) - 1
			m.historyDraft = ""
			m.composer.SetValue(m.promptHistory[m.historyIndex])
			m.composer.CursorEnd()
			m.updateLayout()
			return m, nil
		}
		return m, nil

	case "shift+tab":
		if !m.planMode {
			// Cycle permission mode: read-only -> workspace -> ask -> full-access -> read-only
			nextMode := core.PermissionWorkspace
			switch m.permissionMode {
			case core.PermissionReadOnly:
				nextMode = core.PermissionWorkspace
			case core.PermissionWorkspace:
				nextMode = core.PermissionAsk
			case core.PermissionAsk:
				nextMode = core.PermissionFullAccess
			case core.PermissionFullAccess:
				nextMode = core.PermissionReadOnly
			}
			_ = m.client.SetPermissionMode(nextMode)
			m.permissionMode = nextMode
			m.updateLayout()
		}
		return m, nil

	case "ctrl+t":
		return m.openToolPager(-1)

	case "ctrl+d":
		return m.openDiffModal("")

	case "ctrl+o":
		if m.running {
			m.addSystemEntry("Cannot switch sessions while a task is running. Cancel or wait for it to finish.")
			return m, nil
		}
		return m.openSessionsModal()

	case "ctrl+r":
		return m.toggleReasoningExpansion()
	}

	// 2. Navigation in Modal / Autocomplete
	if m.autocompleteOpen && len(m.autocompleteItems) > 0 {
		switch keyStr {
		case "up":
			if m.autocompleteIndex > 0 {
				m.autocompleteIndex--
			} else {
				m.autocompleteIndex = len(m.autocompleteItems) - 1
			}
			return m, nil
		case "down":
			if m.autocompleteIndex < len(m.autocompleteItems)-1 {
				m.autocompleteIndex++
			} else {
				m.autocompleteIndex = 0
			}
			return m, nil
		case "tab", "enter":
			if m.autocompleteMode == AutocompleteModeFile {
				selected := m.autocompleteItems[m.autocompleteIndex].Command
				if !strings.HasPrefix(selected, "@") {
					selected = "@" + selected
				}
				val := m.composer.Value()
				start := m.autocompleteStart
				end := m.autocompleteEnd
				if start < 0 {
					start = 0
				}
				if end > len(val) {
					end = len(val)
				}
				if start <= end {
					replacement := selected
					if !strings.HasPrefix(val[end:], " ") {
						replacement += " "
					}
					newVal := val[:start] + replacement + val[end:]
					m.composer.SetValue(newVal)
					m.setCursorTo(start + len(replacement))
				}
				m.autocompleteOpen = false
				m.updateLayout()
				return m, nil
			}
			selected := m.autocompleteItems[m.autocompleteIndex].Command
			if keyStr == "enter" && strings.EqualFold(strings.TrimSpace(m.composer.Value()), selected) {
				m.autocompleteOpen = false
				m.composer.SetValue(selected)
			} else {
				m.composer.SetValue(selected + " ")
				m.autocompleteOpen = false
				m.updateLayout()
				return m, nil
			}
		}
	}

	if m.activeRecovery != nil {
		switch keyStr {
		case "up", "k":
			if m.activeRecovery.Selected > 0 {
				m.activeRecovery.Selected--
				m.updateLayout()
			}
			return m, nil
		case "down", "j":
			if m.activeRecovery.Selected < 1 {
				m.activeRecovery.Selected++
				m.updateLayout()
			}
			return m, nil
		case "1", "r":
			m.activeRecovery.Selected = 0
			m.activeRecovery = nil
			m.pausedCheckpoint = nil
			m.running = true
			m.taskStartedAt = time.Now()
			m.updateLayout()
			sessionID := m.sessionID
			return m, func() tea.Msg {
				sub, err := m.runtime.Recover(context.Background())
				return turnSubmittedMsg{sessionID: sessionID, submission: sub, err: err}
			}
		case "2", "d":
			m.activeRecovery.Selected = 1
			m.activeRecovery = nil
			m.pausedCheckpoint = nil
			m.running = false
			m.updateLayout()
			return m, func() tea.Msg {
				ok, err := m.runtime.DiscardPaused(context.Background())
				return taskDiscardedMsg{discarded: ok, err: err}
			}
		case "enter":
			if m.activeRecovery.Selected == 0 {
				m.activeRecovery = nil
				m.pausedCheckpoint = nil
				m.running = true
				m.taskStartedAt = time.Now()
				m.updateLayout()
				sessionID := m.sessionID
				return m, func() tea.Msg {
					sub, err := m.runtime.Recover(context.Background())
					return turnSubmittedMsg{sessionID: sessionID, submission: sub, err: err}
				}
			} else {
				m.activeRecovery = nil
				m.pausedCheckpoint = nil
				m.running = false
				m.updateLayout()
				return m, func() tea.Msg {
					ok, err := m.runtime.DiscardPaused(context.Background())
					return taskDiscardedMsg{discarded: ok, err: err}
				}
			}
		}
		return m, nil
	}

	if m.activeToolRecovery != nil {
		switch keyStr {
		case "up", "k":
			if m.activeToolRecovery.Selected > 0 {
				m.activeToolRecovery.Selected--
				m.updateLayout()
			}
			return m, nil
		case "down", "j":
			if m.activeToolRecovery.Selected < 1 {
				m.activeToolRecovery.Selected++
				m.updateLayout()
			}
			return m, nil
		case "1", "r":
			m.activeToolRecovery.Selected = 0
			return m.resolveActiveToolRecovery()
		case "2", "s":
			m.activeToolRecovery.Selected = 1
			return m.resolveActiveToolRecovery()
		case "enter":
			return m.resolveActiveToolRecovery()
		}
		return m, nil
	}

	if m.activeApproval != nil {
		switch keyStr {
		case "up", "k":
			if m.activeApproval.Selected > 0 {
				m.activeApproval.Selected--
				m.updateLayout()
			}
			return m, nil
		case "down", "j":
			if m.activeApproval.Selected < 1 {
				m.activeApproval.Selected++
				m.updateLayout()
			}
			return m, nil
		case "1", "y", "Y":
			m.activeApproval.Selected = 0
			return m.resolveActiveApproval()
		case "2", "n", "N":
			m.activeApproval.Selected = 1
			return m.resolveActiveApproval()
		case "enter":
			return m.resolveActiveApproval()
		}
		return m, nil
	}

	if m.activeQuestion != nil && len(m.activeQuestion.Request.Questions) > 0 {
		currIdx := m.activeQuestion.CurrentIndex
		if currIdx >= len(m.activeQuestion.Request.Questions) {
			currIdx = len(m.activeQuestion.Request.Questions) - 1
		}
		currQ := m.activeQuestion.Request.Questions[currIdx]
		maxOpt := len(currQ.Options) // index of "None of the above"
		inCustomInput := m.activeQuestion.Selected == maxOpt
		inNotesInput := m.activeQuestion.NotesOpen

		switch keyStr {
		case "up":
			if m.activeQuestion.Selected > 0 {
				m.activeQuestion.Selected--
			} else {
				m.activeQuestion.Selected = maxOpt
			}
			m.updateLayout()
			return m, nil
		case "down":
			if m.activeQuestion.Selected < maxOpt {
				m.activeQuestion.Selected++
			} else {
				m.activeQuestion.Selected = 0
			}
			m.updateLayout()
			return m, nil
		case "tab":
			m.activeQuestion.NotesOpen = !m.activeQuestion.NotesOpen
			m.updateLayout()
			return m, nil
		case "enter":
			return m.resolveActiveQuestion()
		case "backspace":
			if inNotesInput {
				if len(m.activeQuestion.NoteText) > 0 {
					runes := []rune(m.activeQuestion.NoteText)
					m.activeQuestion.NoteText = string(runes[:len(runes)-1])
					m.updateLayout()
				}
				return m, nil
			}
			if inCustomInput {
				if len(m.activeQuestion.Custom) > 0 {
					runes := []rune(m.activeQuestion.Custom)
					m.activeQuestion.Custom = string(runes[:len(runes)-1])
					m.updateLayout()
				}
				return m, nil
			}
			return m, nil
		case "space":
			if inNotesInput {
				m.activeQuestion.NoteText += " "
				m.updateLayout()
				return m, nil
			}
			if inCustomInput {
				m.activeQuestion.Custom += " "
				m.updateLayout()
				return m, nil
			}
		default:
			text := msg.Key().Text
			if text == "" && msg.Key().Code >= ' ' && msg.Key().Code <= '~' && msg.Key().Mod == 0 {
				text = string(msg.Key().Code)
			}
			if inNotesInput && text != "" {
				m.activeQuestion.NoteText += text
				m.updateLayout()
				return m, nil
			}
			if inCustomInput && text != "" {
				m.activeQuestion.Custom += text
				m.updateLayout()
				return m, nil
			}
			// Quick numeric option selection when not typing in custom/notes input
			if len(keyStr) == 1 && keyStr[0] >= '1' && keyStr[0] <= '9' {
				idx := int(keyStr[0] - '1')
				if idx <= maxOpt {
					m.activeQuestion.Selected = idx
					m.updateLayout()
					return m, nil
				}
			}
		}
		return m, nil
	}

	if m.effortPicker != nil {
		options := reasoningEffortOptions()
		switch keyStr {
		case "esc", "ctrl+c", "q":
			m.effortPicker = nil
			m.updateLayout()
			return m, nil
		case "up", "k", "ctrl+p":
			if m.effortPicker.Selected > 0 {
				m.effortPicker.Selected--
			} else {
				m.effortPicker.Selected = len(options) - 1
			}
			m.updateLayout()
			return m, nil
		case "down", "j", "ctrl+n":
			if m.effortPicker.Selected < len(options)-1 {
				m.effortPicker.Selected++
			} else {
				m.effortPicker.Selected = 0
			}
			m.updateLayout()
			return m, nil
		case "enter":
			if m.effortPicker.Selected >= 0 && m.effortPicker.Selected < len(options) {
				value := options[m.effortPicker.Selected].Value
				m.effortPicker = nil
				return m.applyReasoningEffort(value)
			}
			m.effortPicker = nil
			m.updateLayout()
			return m, nil
		default:
			if len(keyStr) == 1 && keyStr[0] >= '1' && keyStr[0] <= '9' {
				idx := int(keyStr[0] - '1')
				if idx < len(options) {
					value := options[idx].Value
					m.effortPicker = nil
					return m.applyReasoningEffort(value)
				}
			}
		}
		return m, nil
	}

	if m.activePlanReview != nil {
		switch keyStr {
		case "up", "k":
			if m.activePlanReview.Selected > 0 {
				m.activePlanReview.Selected--
			}
			m.updateLayout()
			return m, nil
		case "down", "j":
			if m.activePlanReview.Selected < 2 {
				m.activePlanReview.Selected++
			}
			m.updateLayout()
			return m, nil
		case "1":
			m.activePlanReview.Selected = 0
			return m.resolveActivePlanReview()
		case "2":
			m.activePlanReview.Selected = 1
			return m.resolveActivePlanReview()
		case "3":
			m.activePlanReview.Selected = 2
			return m.resolveActivePlanReview()
		case "enter":
			return m.resolveActivePlanReview()
		}
		return m, nil
	}

	// 2.5 Prompt History Navigation (Up / Down)
	if !m.autocompleteOpen && m.activeApproval == nil && m.activeQuestion == nil && m.activePlanReview == nil {
		switch keyStr {
		case "up":
			if m.composer.Line() == 0 && len(m.promptHistory) > 0 {
				if m.historyIndex == len(m.promptHistory) {
					m.historyDraft = m.composer.Value()
				}
				if m.historyIndex > 0 {
					m.historyIndex--
					m.composer.SetValue(m.promptHistory[m.historyIndex])
					m.composer.CursorEnd()
					m.updateLayout()
					return m, nil
				}
			}
		case "down":
			if (m.composer.Line() >= m.composer.LineCount()-1 || m.composer.LineCount() <= 1) && len(m.promptHistory) > 0 {
				if m.historyIndex < len(m.promptHistory)-1 {
					m.historyIndex++
					m.composer.SetValue(m.promptHistory[m.historyIndex])
					m.composer.CursorEnd()
					m.updateLayout()
					return m, nil
				} else if m.historyIndex == len(m.promptHistory)-1 {
					m.historyIndex = len(m.promptHistory)
					m.composer.SetValue(m.historyDraft)
					m.historyDraft = ""
					m.composer.CursorEnd()
					m.updateLayout()
					return m, nil
				}
			}
		}
	}

	// 3. Viewport Scrolling
	switch keyStr {
	case "pgup":
		m.viewport.HalfPageUp()
		m.scrolledUp = !m.viewport.AtBottom()
		return m, nil
	case "pgdown":
		m.viewport.HalfPageDown()
		m.scrolledUp = !m.viewport.AtBottom()
		return m, nil
	case "alt+end":
		m.viewport.GotoBottom()
		m.scrolledUp = false
		return m, nil
	}

	// 4. Shift+Enter / Alt+Enter to insert newline
	key := msg.Key()
	isShiftEnter := (key.Code == tea.KeyEnter && (key.Mod&tea.ModShift != 0 || key.Mod&tea.ModAlt != 0 || key.Mod&tea.ModCtrl != 0)) ||
		keyStr == "shift+enter" || keyStr == "alt+enter" || keyStr == "ctrl+enter"
	if isShiftEnter {
		m.composer.InsertRune('\n')
		m.updateLayout()
		return m, nil
	}

	// 5. Enter to submit Composer
	if keyStr == "enter" {
		val := strings.TrimSpace(m.composer.Value())
		if val == "" {
			return m, nil
		}
		m.composer.Reset()
		m.autocompleteOpen = false
		m.updateLayout()

		if strings.HasPrefix(val, "/") {
			return m.handleSlashCommand(val)
		}
		return m.submitTurn(val)
	}

	// 5. Update composer text & autocomplete triggers
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	val := m.composer.Value()
	if strings.HasPrefix(val, "/") && !strings.Contains(val, " ") {
		items := filterCommands(val)
		if len(items) > 0 {
			m.autocompleteOpen = true
			m.autocompleteMode = AutocompleteModeCommand
			m.autocompleteItems = items
			m.autocompleteIndex = 0
		} else {
			m.autocompleteOpen = false
		}
	} else if query, start, end, ok := DetectAtQuery(val, m.cursorOffset()); ok {
		matches := m.getFileScanner().Search(query, 50)
		if len(matches) > 0 {
			m.autocompleteOpen = true
			m.autocompleteMode = AutocompleteModeFile
			m.autocompleteStart = start
			m.autocompleteEnd = end
			m.autocompleteIndex = 0
			var items []AutocompleteItem
			for _, match := range matches {
				desc := FormatFileSize(match.Size)
				if match.IsModified {
					desc += " · modified"
				}
				items = append(items, AutocompleteItem{
					Command:     "@" + match.Path,
					Description: desc,
				})
			}
			m.autocompleteItems = items
		} else {
			m.autocompleteOpen = false
		}
	} else {
		m.autocompleteOpen = false
	}
	m.updateLayout()

	return m, cmd
}

func (m *tuiModel) cursorOffset() int {
	val := m.composer.Value()
	lines := strings.Split(val, "\n")
	line := m.composer.Line()
	if line < 0 {
		return 0
	}
	if line >= len(lines) {
		return len(val)
	}

	offset := 0
	for i := 0; i < line; i++ {
		offset += len(lines[i]) + 1
	}

	col := m.composer.Column()
	currentRunes := []rune(lines[line])
	if col > len(currentRunes) {
		col = len(currentRunes)
	}
	if col < 0 {
		col = 0
	}
	offset += len(string(currentRunes[:col]))
	if offset > len(val) {
		offset = len(val)
	}
	return offset
}

func (m *tuiModel) setCursorTo(targetOffset int) {
	val := m.composer.Value()
	if targetOffset >= len(val) {
		m.composer.CursorEnd()
		return
	}
	if targetOffset <= 0 {
		m.composer.MoveToBegin()
		return
	}

	lines := strings.Split(val, "\n")
	targetRow := 0
	targetCol := 0
	curr := 0
	for r, line := range lines {
		lineLen := len(line)
		if curr+lineLen >= targetOffset {
			targetRow = r
			targetCol = len([]rune(val[curr:targetOffset]))
			break
		}
		curr += lineLen + 1
	}

	m.composer.MoveToBegin()
	for i := 0; i < targetRow; i++ {
		m.composer.CursorDown()
	}
	m.composer.SetCursorColumn(targetCol)
}
