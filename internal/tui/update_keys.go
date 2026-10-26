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
	if m.run.discardInFlight {
		return m, nil
	}
	// Exactly one overlay can own the keyboard; modalState decides which one.
	switch m.modals.active() {
	case modalShortcuts:
		return m.handleShortcutModalKeyPress(msg, keyStr)
	case modalMemoryClear, modalMemoryPicker:
		return m.handleMemoryPickerKeyPress(keyStr)
	case modalMCPPicker:
		return m.handleMCPServerPickerKeyPress(keyStr)
	case modalTheme:
		return m.handleThemePickerKeyPress(keyStr)
	case modalModel, modalPermission:
		return m.handleProfilePickerKeyPress(keyStr)
	case modalProcess:
		return m.handleProcessPickerKeyPress(keyStr)
	case modalAgents:
		return m.handleAgentsPanelKeyPress(msg, keyStr)
	case modalSessions:
		return m.handleSessionsModalKeyPress(msg, keyStr)
	case modalToolPager:
		return m.handleToolPagerModalKeyPress(msg, keyStr)
	case modalDiff:
		return m.handleDiffModalKeyPress(msg, keyStr)
	}
	if m.activeCardKind() == cardQuestion && m.activeQuestion.Submitting && keyStr != "ctrl+c" {
		return m, nil
	}

	// Trigger shortcut modal when typing '?' in an empty composer
	if keyStr == "?" && m.input.composer.Value() == "" && !m.run.running {
		m.modals.open()
		m.modals.shortcuts = true
		m.updateLayout()
		return m, nil
	}

	if keyStr != "ctrl+c" {
		m.run.compactionQuitRequested = false
	}

	// 1. Interrupt / Quit / Esc
	switch keyStr {
	case "ctrl+c":
		if m.input.composer.HasSelection() {
			return m, m.copyTextToClipboard(m.input.composer.SelectedText())
		}
		if m.input.selectedText != "" {
			return m, m.copyTextToClipboard(m.input.selectedText)
		}
		if m.activeCardKind() == cardQuestion {
			if m.input.composer.Value() != "" {
				m.input.composer.Reset()
				m.input.autocompleteOpen = false
				m.updateLayout()
				return m, nil
			}
			if len(m.entries) > 0 && m.entries[len(m.entries)-1].Kind == KindSystem && m.entries[len(m.entries)-1].Text == questionPendingNotice {
				m.cancelMCPAuthentication(false)
				return m, tea.Quit
			}
			return m.noteQuestionPending()
		}
		if m.run.running && m.run.runningExecution != "" {
			return m.interruptRunningTask()
		}
		if m.usage.compacting || m.run.compactInFlight {
			if m.run.compactionQuitRequested {
				m.usage.resetCompaction()
				m.run.compactInFlight = false
				m.run.compactionQuitRequested = false
				m.cancelMCPAuthentication(false)
				return m, tea.Quit
			}
			m.run.compactionQuitRequested = true
			m.addSystemEntry("Compaction is in progress; press Ctrl+C again to quit.")
			return m, nil
		}
		m.cancelMCPAuthentication(false)
		return m, tea.Quit

	case "ctrl+shift+c":
		if m.input.composer.HasSelection() {
			return m, m.copyTextToClipboard(m.input.composer.SelectedText())
		}

	case "esc":
		if m.modals.memory.pendingAdd {
			m.modals.memory.pendingAdd = false
			m.modals.memory.pendingCategory = ""
			m.input.composer.Placeholder = "Ask Lune to do anything"
			m.addSystemEntry("Memory entry cancelled; the draft remains in the composer.")
			m.updateLayout()
			return m, nil
		}
		if m.modals.mcp.authCancel != nil {
			m.cancelMCPAuthentication(true)
			return m, nil
		}
		if m.modals.active() != modalNone {
			// Every overlay handler consumes Esc itself; this is the safety net
			// for a modal opened by a path that never reached its handler.
			m.modals.close()
			m.updateLayout()
			return m, nil
		}
		if m.input.selectedText != "" || m.hasSelection() || m.input.composer.HasSelection() {
			m.input.clearSelection()
			return m, nil
		}
		if m.input.autocompleteOpen {
			m.input.autocompleteOpen = false
			m.updateLayout()
			return m, nil
		}
		if m.activeCardKind() != cardNone && m.input.composer.Value() != "" {
			m.input.composer.Reset()
			m.updateLayout()
			return m, nil
		}
		switch m.activeCardKind() {
		case cardRecovery:
			return m.discardPausedTask(discardSourceRecovery)
		case cardToolRecovery:
			m.activeToolRecovery.Selected = 1
			return m.resolveActiveToolRecovery()
		case cardApproval:
			m.activeApproval.Selected = 2
			return m.resolveActiveApproval()
		case cardQuestion:
			draft := m.activeQuestion.currentDraft()
			if draft != nil && draft.NotesOpen {
				draft.NotesOpen = false
				m.updateLayout()
				return m, nil
			}
			currIdx := m.activeQuestion.CurrentIndex
			if draft != nil && currIdx >= 0 && currIdx < len(m.activeQuestion.Request.Questions) {
				maxOpt := len(m.activeQuestion.Request.Questions[currIdx].Options)
				if draft.Selected == maxOpt && draft.Custom != "" {
					draft.Custom = ""
					cmd := m.questionDraftChanged(m.activeQuestion)
					m.updateLayout()
					return m, cmd
				}
			}
			return m.noteQuestionPending()
		case cardEffort:
			m.effortPicker = nil
			m.updateLayout()
			return m, nil
		case cardPlanReview:
			return m.cancelActivePlanReview()
		}
		if m.scrolledUp {
			m.scrolledUp = false
			m.viewport.GotoBottom()
			m.view.invalidateLive()
			m.updateLayout()
			return m, nil
		}
		if m.run.running && m.run.runningExecution != "" {
			return m.interruptRunningTask()
		}
		if m.usage.compacting || m.run.compactInFlight {
			return m, nil
		}
		// If composer is empty and not running, Esc steps back to edit the last message
		if !m.run.running && m.input.composer.Value() == "" && len(m.input.history) > 0 {
			m.input.historyIndex = len(m.input.history) - 1
			m.input.historyDraft = ""
			m.input.composer.SetValue(m.input.history[m.input.historyIndex])
			m.input.composer.CursorEnd()
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
	if m.input.autocompleteOpen && len(m.input.autocompleteItems) > 0 {
		switch keyStr {
		case "up":
			if m.input.autocompleteIndex > 0 {
				m.input.autocompleteIndex--
			} else {
				m.input.autocompleteIndex = len(m.input.autocompleteItems) - 1
			}
			return m, nil
		case "down":
			if m.input.autocompleteIndex < len(m.input.autocompleteItems)-1 {
				m.input.autocompleteIndex++
			} else {
				m.input.autocompleteIndex = 0
			}
			return m, nil
		case "tab", "enter":
			if m.input.autocompleteMode == AutocompleteModeFile {
				selected := m.input.autocompleteItems[m.input.autocompleteIndex].Command
				if !strings.HasPrefix(selected, "@") {
					selected = "@" + selected
				}
				val := m.input.composer.Value()
				start := m.input.autocompleteStart
				end := m.input.autocompleteEnd
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
					m.input.composer.SetValue(newVal)
					m.setCursorTo(start + len(replacement))
				}
				m.input.autocompleteOpen = false
				m.updateLayout()
				return m, nil
			}
			selected := m.input.autocompleteItems[m.input.autocompleteIndex].Command
			if keyStr == "enter" {
				m.input.autocompleteOpen = false
				m.input.composer.SetValue(selected)
				m.input.composer.CursorEnd()
				// Let the normal Enter path execute the selected command now.
			} else {
				m.input.composer.SetValue(selected + " ")
				m.input.autocompleteOpen = false
				m.updateLayout()
				return m, nil
			}
		}
	}

	if m.activeCardKind() == cardRecovery && m.input.composer.Value() == "" {
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
			m.run.running = true
			m.run.taskStartedAt = time.Now()
			m.updateLayout()
			runtime, sessionID, bindingGeneration := m.runtime, m.sessionID, m.bindingGeneration
			ctx := m.boundContext
			if ctx == nil {
				ctx = context.Background()
			}
			return m, func() tea.Msg {
				sub, err := runtime.Recover(ctx)
				return turnSubmittedMsg{sessionID: sessionID, bindingGeneration: bindingGeneration, submission: sub, err: err}
			}
		case "2", "d":
			m.activeRecovery.Selected = 1
			return m.discardPausedTask(discardSourceRecovery)
		case "enter":
			if m.activeRecovery.Selected == 0 {
				m.activeRecovery = nil
				m.pausedCheckpoint = nil
				m.run.running = true
				m.run.taskStartedAt = time.Now()
				m.updateLayout()
				runtime, sessionID, bindingGeneration := m.runtime, m.sessionID, m.bindingGeneration
				ctx := m.boundContext
				if ctx == nil {
					ctx = context.Background()
				}
				return m, func() tea.Msg {
					sub, err := runtime.Recover(ctx)
					return turnSubmittedMsg{sessionID: sessionID, bindingGeneration: bindingGeneration, submission: sub, err: err}
				}
			} else {
				return m.discardPausedTask(discardSourceRecovery)
			}
		}
	}

	if m.activeCardKind() == cardToolRecovery && m.input.composer.Value() == "" {
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
	}

	if m.activeCardKind() == cardApproval && m.input.composer.Value() == "" {
		switch keyStr {
		case "up", "k":
			if m.activeApproval.Selected > 0 {
				m.activeApproval.Selected--
				m.updateLayout()
			}
			return m, nil
		case "down", "j":
			if m.activeApproval.Selected < 2 {
				m.activeApproval.Selected++
				m.updateLayout()
			}
			return m, nil
		case "1", "y", "Y":
			m.activeApproval.Selected = 0
			return m.resolveActiveApproval()
		case "2", "s", "S":
			m.activeApproval.Selected = 1
			return m.resolveActiveApproval()
		case "3", "n", "N":
			m.activeApproval.Selected = 2
			return m.resolveActiveApproval()
		case "enter":
			return m.resolveActiveApproval()
		}
	}

	if m.activeCardKind() == cardQuestion && m.input.composer.Value() == "" {
		if handled, cmd := m.handleQuestionKeyPress(msg, keyStr); handled {
			return m, cmd
		}
	}

	if m.activeCardKind() == cardEffort {
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

	if m.activeCardKind() == cardPlanReview && m.input.composer.Value() == "" {
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
	}

	// 2.5 Prompt History Navigation (Up / Down)
	if !m.input.autocompleteOpen && m.activeCardKind() == cardNone {
		switch keyStr {
		case "up":
			if m.input.composer.Line() == 0 && len(m.input.history) > 0 {
				if m.input.historyIndex == len(m.input.history) {
					m.input.historyDraft = m.input.composer.Value()
				}
				if m.input.historyIndex > 0 {
					m.input.historyIndex--
					m.input.composer.SetValue(m.input.history[m.input.historyIndex])
					m.input.composer.CursorEnd()
					m.updateLayout()
					return m, nil
				}
			}
		case "down":
			if (m.input.composer.Line() >= m.input.composer.LineCount()-1 || m.input.composer.LineCount() <= 1) && len(m.input.history) > 0 {
				if m.input.historyIndex < len(m.input.history)-1 {
					m.input.historyIndex++
					m.input.composer.SetValue(m.input.history[m.input.historyIndex])
					m.input.composer.CursorEnd()
					m.updateLayout()
					return m, nil
				} else if m.input.historyIndex == len(m.input.history)-1 {
					m.input.historyIndex = len(m.input.history)
					m.input.composer.SetValue(m.input.historyDraft)
					m.input.historyDraft = ""
					m.input.composer.CursorEnd()
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
		m.view.invalidateLive()
		m.updateLayout()
		return m, nil
	case "pgdown":
		m.viewport.HalfPageDown()
		m.scrolledUp = !m.viewport.AtBottom()
		m.view.invalidateLive()
		m.updateLayout()
		return m, nil
	case "alt+end":
		m.viewport.GotoBottom()
		m.scrolledUp = false
		m.view.invalidateLive()
		m.updateLayout()
		return m, nil
	}

	// 4. Shift+Enter / Alt+Enter to insert newline
	key := msg.Key()
	isShiftEnter := (key.Code == tea.KeyEnter && (key.Mod&tea.ModShift != 0 || key.Mod&tea.ModAlt != 0 || key.Mod&tea.ModCtrl != 0)) ||
		keyStr == "shift+enter" || keyStr == "alt+enter" || keyStr == "ctrl+enter"
	if isShiftEnter {
		m.input.composer.InsertRune('\n')
		m.updateLayout()
		return m, nil
	}

	// 5. Enter to submit Composer
	if keyStr == "enter" {
		if m.modals.memory.pendingAdd {
			content := strings.TrimSpace(m.input.composer.Value())
			category := m.modals.memory.pendingCategory
			m.modals.memory.pendingAdd = false
			m.modals.memory.pendingCategory = ""
			m.input.composer.Reset()
			m.input.composer.Placeholder = "Ask Lune to do anything"
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
		val := strings.TrimSpace(m.input.composer.Value())
		if val == "" {
			return m, nil
		}
		isCommand := isKnownSlashCommand(val)
		if isCommand {
			if reason := m.slashCommandUnavailable(val); reason != "" {
				m.addSystemEntry(reason)
				m.updateLayout()
				return m, nil
			}
		}
		m.input.composer.Reset()
		m.input.autocompleteOpen = false
		m.updateLayout()

		if isCommand {
			return m.handleSlashCommand(val)
		}
		// Slash-prefixed text that is not a registered command (for example a pasted
		// absolute path) is submitted as a normal prompt.
		return m.submitTurn(val)
	}

	// 5. Update composer text & autocomplete triggers
	var cmd tea.Cmd
	m.input.composer, cmd = m.input.composer.Update(msg)
	val := m.input.composer.Value()
	if m.modals.memory.pendingAdd {
		m.input.autocompleteOpen = false
	} else if strings.HasPrefix(val, "/") && !strings.Contains(val, " ") {
		items := m.commandsForAutocomplete(val)
		if len(items) > 0 {
			m.input.autocompleteOpen = true
			m.input.autocompleteMode = AutocompleteModeCommand
			m.input.autocompleteItems = items
			m.input.autocompleteIndex = 0
		} else {
			m.input.autocompleteOpen = false
		}
	} else if query, start, end, ok := filemention.DetectAtQuery(val, m.cursorOffset()); ok {
		// Cached matches render immediately; a missing or stale cache refreshes in
		// the background so the key handler never walks the workspace.
		if mentionCmd := m.refreshFileMentions(query, start, end); mentionCmd != nil {
			cmd = tea.Batch(cmd, mentionCmd)
		}
	} else {
		m.input.autocompleteOpen = false
	}
	m.updateLayout()

	return m, cmd
}

func (m *tuiModel) cursorOffset() int {
	val := m.input.composer.Value()
	lines := strings.Split(val, "\n")
	line := m.input.composer.Line()
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

	col := m.input.composer.Column()
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
	val := m.input.composer.Value()
	if targetOffset >= len(val) {
		m.input.composer.CursorEnd()
		return
	}
	if targetOffset <= 0 {
		m.input.composer.MoveToBegin()
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

	m.input.composer.MoveToBegin()
	for i := 0; i < targetRow; i++ {
		m.input.composer.CursorDown()
	}
	m.input.composer.SetCursorColumn(targetCol)
}
