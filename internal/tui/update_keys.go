package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/lune/internal/filemention"
)

func (m *tuiModel) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	keyStr := msg.String()
	if m.discardInFlight {
		return m, nil
	}
	if m.activeQuestion != nil && m.activeQuestion.Submitting && keyStr != "ctrl+c" {
		return m, nil
	}

	if m.shortcutsOpen {
		return m.handleShortcutModalKeyPress(msg, keyStr)
	}
	if m.memoryPicker != nil || m.memoryClearConfirm {
		return m.handleMemoryPickerKeyPress(keyStr)
	}
	if m.mcpServerPicker != nil {
		return m.handleMCPServerPickerKeyPress(keyStr)
	}
	if m.themePicker != nil {
		return m.handleThemePickerKeyPress(keyStr)
	}
	if m.modelPicker != nil || m.permissionPicker != nil {
		return m.handleProfilePickerKeyPress(keyStr)
	}
	if m.processPickerOpen {
		return m.handleProcessPickerKeyPress(keyStr)
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
		if m.composer.HasSelection() {
			return m, m.copyTextToClipboard(m.composer.SelectedText())
		}
		if m.selectedText != "" {
			return m, m.copyTextToClipboard(m.selectedText)
		}
		if m.activeQuestion != nil {
			return m.discardPausedTask(discardSourceQuestion)
		}
		if m.running && m.runtime != nil && m.runningExecution != "" {
			return m.interruptRunningTask()
		}
		m.cancelMCPAuthentication(false)
		return m, tea.Quit

	case "ctrl+shift+c":
		if m.composer.HasSelection() {
			return m, m.copyTextToClipboard(m.composer.SelectedText())
		}

	case "esc":
		if m.pendingMemoryAdd {
			m.pendingMemoryAdd = false
			m.pendingMemoryCategory = ""
			m.composer.Placeholder = "Ask Lune to do anything"
			m.addSystemEntry("Memory entry cancelled; the draft remains in the composer.")
			m.updateLayout()
			return m, nil
		}
		if m.mcpAuthCancel != nil {
			m.cancelMCPAuthentication(true)
			return m, nil
		}
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
		if m.selectedText != "" || m.hasSelection() || m.composer.HasSelection() {
			m.isSelecting = false
			m.composerMouseSelecting = false
			m.composer.ClearSelection()
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
			return m.discardPausedTask(discardSourceRecovery)
		}
		if m.activeToolRecovery != nil {
			m.activeToolRecovery.Selected = 1
			return m.resolveActiveToolRecovery()
		}
		if m.activeQuestion != nil {
			return m.discardPausedTask(discardSourceQuestion)
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
			return m.cancelActivePlanReview()
		}
		if m.scrolledUp {
			m.scrolledUp = false
			m.viewport.GotoBottom()
			return m, nil
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
		return m.togglePlanMode()

	case "ctrl+o":
		return m.toggleAllToolRounds()

	case "ctrl+d":
		return m.openDiffModal("")
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
			if keyStr == "enter" {
				m.autocompleteOpen = false
				m.composer.SetValue(selected)
				m.composer.CursorEnd()
				// Let the normal Enter path execute the selected command now.
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
			return m.discardPausedTask(discardSourceRecovery)
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
				return m.discardPausedTask(discardSourceRecovery)
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
		if m.pendingMemoryAdd {
			content := strings.TrimSpace(m.composer.Value())
			category := m.pendingMemoryCategory
			m.pendingMemoryAdd = false
			m.pendingMemoryCategory = ""
			m.composer.Reset()
			m.composer.Placeholder = "Ask Lune to do anything"
			if content == "" {
				m.addSystemEntry("Memory content cannot be empty.")
				m.updateLayout()
				return m, nil
			}
			parts := []string{"/memory", "add"}
			if category != "" {
				parts = append(parts, category)
			}
			parts = append(parts, content)
			return m.handleMemoryCommand(parts)
		}
		val := strings.TrimSpace(m.composer.Value())
		if val == "" {
			return m, nil
		}
		if strings.HasPrefix(val, "/") {
			if reason := m.slashCommandUnavailable(val); reason != "" {
				m.addSystemEntry(reason)
				m.updateLayout()
				return m, nil
			}
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
	if m.pendingMemoryAdd {
		m.autocompleteOpen = false
	} else if strings.HasPrefix(val, "/") && !strings.Contains(val, " ") {
		items := m.commandsForAutocomplete(val)
		if len(items) > 0 {
			m.autocompleteOpen = true
			m.autocompleteMode = AutocompleteModeCommand
			m.autocompleteItems = items
			m.autocompleteIndex = 0
		} else {
			m.autocompleteOpen = false
		}
	} else if query, start, end, ok := filemention.DetectAtQuery(val, m.cursorOffset()); ok {
		matches := m.getFileScanner().Search(query, 50)
		if len(matches) > 0 {
			m.autocompleteOpen = true
			m.autocompleteMode = AutocompleteModeFile
			m.autocompleteStart = start
			m.autocompleteEnd = end
			m.autocompleteIndex = 0
			var items []AutocompleteItem
			for _, match := range matches {
				desc := filemention.FormatFileSize(match.Size)
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
