package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	core "github.com/afterlune/luneCore"
)

func (m *tuiModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	return m.update(message)
}

func (m *tuiModel) update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case terminalColorTimeoutMsg:
		if m.terminalColorQueryPending {
			m.terminalColorQueryPending = false
			if m.preferences.Theme == themeAuto {
				m.applyResolvedTheme()
			}
		}
		return m, nil
	case tea.BackgroundColorMsg:
		m.terminalBackground = message.Color
		if m.preferences.Theme == themeAuto {
			m.terminalBackgroundReceived = true
			m.finishTerminalColorQueryIfReady()
			m.applyResolvedTheme()
		}
		return m, nil
	case tea.ForegroundColorMsg:
		m.terminalForeground = message.Color
		if m.preferences.Theme == themeAuto {
			m.terminalForegroundReceived = true
			m.finishTerminalColorQueryIfReady()
			m.applyResolvedTheme()
		}
		return m, nil
	case tea.ColorProfileMsg:
		m.colorProfile = message.Profile
		m.updateLayout()
		m.refreshPanels()
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = message.Width, message.Height
		m.updateLayout()
		m.refreshPanels()
		return m, nil
	case coreEventMsg:
		return m, m.handleRuntimeEvent(message.event)
	case runtimeStreamReadyMsg:
		if message.err != nil {
			m.status = "Could not subscribe to session events: " + message.err.Error()
			return m, nil
		}
		return m, nextRuntimeEvent(message.events)
	case runtimeEventMsg:
		if message.events == nil {
			if m.runtimeCancel != nil {
				m.status = "Session event stream ended."
			}
			return m, nil
		}
		if message.event.Cursor > 0 {
			if message.event.Cursor <= m.eventCursor {
				return m, nextRuntimeEvent(message.events)
			}
			m.eventCursor = message.event.Cursor
		}
		if message.event.ExecutionID != "" && !m.activeExecutions[message.event.ExecutionID] {
			return m, nextRuntimeEvent(message.events)
		}
		return m, tea.Batch(m.handleRuntimeEvent(message.event), nextRuntimeEvent(message.events))
	case mcpCommandResultMsg:
		m.finishMCPCommand(message)
		m.refreshPanels()
		return m, nil
	case mcpOpenURLResultMsg:
		return m.finishMCPURLOpen(message)
	case modelStreamFlushMsg:
		if message.token != m.modelStreamToken || !m.modelStreamTick {
			return m, nil
		}
		if m.flushModelStreamEvents() {
			m.refreshPanels()
		}
		return m, nil
	case toolActivityMsg:
		m.updateToolActivity(message.event)
		m.refreshPanels()
		return m, nil
	case toolApprovalRequestMsg:
		m.pendingApproval = &message
		m.status = "Permission required: " + m.keyLabel("approve_tool") + " approve, " + m.keyLabel("deny_tool") + " deny."
		m.addEntry("permission", formatToolApprovalWithKeys(message.request, m.workspace, m.keyLabel("approve_tool"), m.keyLabel("deny_tool")), true)
		return m, nil
	case toolApprovalCancelledMsg:
		if m.pendingApproval != nil && m.pendingApproval.request.ID == message.requestID {
			m.pendingApproval = nil
			m.status = "Tool approval cancelled."
			m.refreshPanels()
		}
		return m, nil
	case modelUsageMsg:
		m.updateModelUsage(message.usage, message.agentID, message.profile, message.provider, message.model,
			message.contextWindow, message.estimatedInputTokens)
		m.refreshPanels()
		return m, nil
	case turnSubmittedMsg:
		if message.err != nil {
			m.running = false
			m.runtimeTask = false
			m.addEntry("error", "Could not submit turn: "+message.err.Error(), true)
			m.status = "Turn was not accepted."
			return m, nil
		}
		if message.submission.ExecutionID != "" {
			m.activeExecutions[message.submission.ExecutionID] = true
		}
		if message.submission.Status == core.SubmissionQueued {
			m.status = fmt.Sprintf("Turn queued at position %d.", message.submission.Position)
			if err := m.refreshRuntimeProjection(); err != nil {
				m.addEntry("error", err.Error(), true)
			}
			m.refreshPanels()
			return m, nil
		}
		m.running = true
		m.runtimeTask = true
		m.runningExecutionID = message.submission.ExecutionID
		return m, waitForTurn(m.runtime, message.submission.ExecutionID)
	case taskFinishedMsg:
		m.runtimeTask = false
		m.runningExecutionID = ""
		m.finishTask(message.result)
		m.refreshPanels()
		if err := m.refreshRuntimeProjection(); err != nil {
			m.addEntry("error", "Could not refresh task state: "+err.Error(), true)
		}
		if m.running && m.runningExecutionID != "" {
			return m, waitForTurn(m.runtime, m.runningExecutionID)
		}
		if m.exitAfterTask {
			return m, tea.Quit
		}
		return m, nil
	case imageInputReadyMsg:
		m.applyImageInputResult(message)
		m.updateCommandSuggestions()
		m.updateLayout()
		m.refreshPanels()
		return m, nil
	case tea.PasteMsg:
		if m.pendingMCPInteraction != nil {
			return m, m.updateMCPInteractionInput(message)
		}
		if m.canAttachImages() {
			if paths, ok := parsePastedImagePaths(message.Content, m.workspace); ok {
				return m, m.loadPastedImagePaths(paths, message.Content)
			}
		}
		command := m.updateInput(message)
		m.updateCommandSuggestions()
		m.updateLayout()
		return m, command
	case tea.MouseWheelMsg:
		if m.pendingMCPInteraction != nil {
			return m, nil
		}
		if m.detailsOpen {
			m.activity, _ = m.activity.Update(message)
		} else {
			m.chat, _ = m.chat.Update(message)
			if m.chat.AtBottom() {
				m.unreadEntries = 0
			}
		}
		return m, nil
	case tea.MouseClickMsg:
		if m.pendingMCPInteraction != nil {
			return m, nil
		}
		if m.handleTranscriptMouseClick(message) {
			return m, nil
		}
		m.clearTranscriptSelection()
		return m, nil
	case tea.MouseMotionMsg:
		if m.handleTranscriptMouseMotion(message) {
			return m, nil
		}
		return m, nil
	case tea.MouseReleaseMsg:
		if m.handleTranscriptMouseRelease(message) {
			return m, nil
		}
		return m, nil
	case tea.KeyPressMsg:
		if m.exitConfirm {
			switch message.String() {
			case "esc":
				m.exitConfirm = false
				m.status = "Exit cancelled."
				return m, nil
			case "i":
				return m.confirmExit(true)
			case "w":
				return m.confirmExit(false)
			default:
				return m, nil
			}
		}
		if m.pendingMCPInteraction != nil {
			return m.handleMCPInteractionKey(message)
		}
		if m.mcpAuthCancel != nil && message.String() == "esc" {
			m.mcpAuthCancel()
			m.status = "Cancelling MCP authorization…"
			return m, nil
		}
		if m.keymapOpen {
			return m.handleKeymapKey(message)
		}
		if m.searchOpen {
			return m.handleTranscriptSearchKey(message)
		}
		if m.detailsOpen {
			switch {
			case m.matchesKey("interrupt_or_copy", message):
				if m.copyTranscriptSelection(false) {
					return m, nil
				}
				if m.input.HasSelection() {
					if err := writeClipboard(m.input.SelectedText()); err != nil {
						m.status = "Selection could not be copied."
					} else {
						m.status = "Selection copied."
					}
					return m, nil
				}
				if m.running {
					m.detailsOpen = false
					m.updateLayout()
					m.interrupt()
					return m, nil
				}
				return m, tea.Quit
			case m.matchesKey("transcript_copy", message):
				if !m.copyTranscriptSelection(false) && m.input.HasSelection() {
					if err := writeClipboard(m.input.SelectedText()); err != nil {
						m.status = "Selection could not be copied."
					} else {
						m.status = "Selection copied."
					}
				}
				return m, nil
			case message.String() == "esc", m.matchesKey("toggle_activity", message):
				m.detailsOpen = false
				m.refreshPanels()
				m.updateLayout()
				return m, nil
			case m.matchesKey("chat_scroll_up", message), m.matchesKey("activity_scroll_up", message), m.matchesKey("details_scroll_up", message):
				m.activity.HalfPageUp()
				return m, nil
			case m.matchesKey("chat_scroll_down", message), m.matchesKey("activity_scroll_down", message), m.matchesKey("details_scroll_down", message):
				m.activity.HalfPageDown()
				return m, nil
			default:
				return m, nil
			}
		}
		if m.activityFocus {
			switch {
			case message.String() == "esc":
				m.activityFocus = false
				m.status = "Transcript focused."
			case m.matchesKey("activity_previous", message):
				m.adjustActivityFocus(-1)
			case m.matchesKey("activity_next", message):
				m.adjustActivityFocus(1)
			case m.matchesKey("activity_toggle", message):
				m.toggleFocusedActivity()
			case m.matchesKey("interrupt_or_copy", message):
				if m.copyTranscriptSelection(false) {
					return m, nil
				}
				m.activityFocus = false
				if m.running {
					m.interrupt()
					return m, nil
				}
				return m, tea.Quit
			case m.matchesKey("transcript_copy", message):
				if !m.copyTranscriptSelection(false) && m.input.HasSelection() {
					if err := writeClipboard(m.input.SelectedText()); err != nil {
						m.status = "Selection could not be copied."
					} else {
						m.status = "Selection copied."
					}
				}
				return m, nil
			default:
				return m, nil
			}
			m.refreshPanels()
			m.updateLayout()
			return m, nil
		}
		if m.matchesKey("jump_chat_bottom", message) && !m.sessionPicker && !m.modelPicker &&
			!m.effortPicker && !m.queuePicker && !m.permissionMenu && m.pendingApproval == nil {
			m.chat.GotoBottom()
			m.unreadEntries = 0
			return m, nil
		}
		if m.matchesKey("transcript_copy", message) && m.transcriptSelection.active {
			m.copyTranscriptSelection(false)
			return m, nil
		}
		if m.matchesKey("search_open", message) && !m.sessionPicker && !m.modelPicker && !m.effortPicker &&
			!m.queuePicker && !m.permissionMenu && m.pendingApproval == nil {
			m.openTranscriptSearch()
			return m, nil
		}
		if m.matchesKey("focus_activity", message) && !m.sessionPicker && !m.modelPicker && !m.effortPicker &&
			!m.queuePicker && !m.permissionMenu && m.pendingApproval == nil {
			m.toggleActivityFocus()
			return m, nil
		}
		if !(m.running && message.String() == "esc") && !m.sessionPicker && !m.modelPicker && !m.effortPicker && !m.queuePicker && !m.permissionMenu {
			if handled, command := m.handleVimKey(message); handled {
				m.updateLayout()
				return m, command
			}
		}
		if m.matchesKey("send", message) {
			if m.queuePicker {
				return m.editQueuedInput()
			}
			if m.running && strings.TrimSpace(m.input.Value()) == "/queue" {
				return m.openQueuePicker()
			}
			if m.running && (m.pendingApproval == nil || strings.TrimSpace(m.input.Value()) != "") {
				return m.enqueueInput()
			}
			if m.sessionPicker {
				if m.deleteConfirm {
					return m, nil
				}
				return m.selectSession()
			}
			if m.permissionMenu {
				return m.selectPermissionMode()
			}
			if m.modelPicker {
				return m.selectModelProfile()
			}
			if m.effortPicker {
				return m.selectReasoningEffort()
			}
			if m.pendingApproval != nil {
				return m, nil
			}
			if m.planReview && strings.TrimSpace(m.input.Value()) == "" {
				return m.acceptPlan()
			}
			if len(m.commandSuggestions) > 0 {
				index := min(m.suggestionIndex, len(m.commandSuggestions)-1)
				m.setInputValue(m.commandSuggestions[index])
			}
			return m.submit()
		}
		if message.String() == "esc" {
			if m.queuePicker {
				m.queuePicker = false
				m.status = "Queue selection closed."
				m.updateLayout()
				return m, nil
			}
			if m.planReview {
				m.planReview = false
				m.planMode = false
				m.clearQueue()
				m.status = "Plan cancelled."
				m.updateLayout()
				return m, nil
			}
			if m.sessionPicker {
				if m.renamingSession {
					m.renamingSession = false
					m.input.Placeholder = defaultInputPlaceholder
					m.status = "Session rename cancelled."
					m.resetInput()
					m.updateLayout()
					return m, nil
				}
				if m.deleteConfirm {
					m.deleteConfirm = false
					m.status = "Session deletion cancelled."
					m.updateLayout()
					return m, nil
				}
				m.sessionPicker = false
				m.status = "Session selection cancelled."
				m.updateLayout()
				return m, nil
			}
			if m.permissionMenu {
				m.permissionMenu = false
				m.status = "Permission selection cancelled."
				m.updateLayout()
				return m, nil
			}
			if m.modelPicker {
				m.modelPicker = false
				m.status = "Model selection cancelled."
				m.updateLayout()
				return m, nil
			}
			if m.effortPicker {
				m.effortPicker = false
				m.status = "Effort selection cancelled."
				m.updateLayout()
				return m, nil
			}
			m.interrupt()
			return m, nil
		}
		if m.matchesKey("interrupt_or_copy", message) {
			if m.copyTranscriptSelection(false) {
				return m, nil
			}
			if m.input.HasSelection() && (!m.sessionPicker || m.renamingSession) {
				if err := writeClipboard(m.input.SelectedText()); err != nil {
					m.status = "Selection could not be copied."
				} else {
					m.status = "Selection copied."
				}
				return m, nil
			}
			if m.running {
				m.interrupt()
				return m, nil
			}
			return m, tea.Quit
		}
		if m.matchesKey("toggle_activity", message) {
			m.detailsOpen = true
			m.refreshPanels()
			m.updateLayout()
			return m, nil
		}
		if m.matchesKey("cycle_permissions", message) {
			if !m.running && !m.permissionMenu && !m.sessionPicker && !m.queuePicker && !m.modelPicker && !m.effortPicker && !m.detailsOpen {
				return m.cyclePermissionMode()
			}
			return m, nil
		}
		if m.matchesKey("interrupt_send", message) {
			if m.running {
				if strings.TrimSpace(m.input.Value()) != "" {
					return m.enqueueInput()
				}
				m.interrupt()
				return m, nil
			}
			return m.submit()
		}
		if m.matchesKey("chat_scroll_up", message) {
			m.chat.HalfPageUp()
			return m, nil
		}
		if m.matchesKey("chat_scroll_down", message) {
			m.chat.HalfPageDown()
			if m.chat.AtBottom() {
				m.unreadEntries = 0
			}
			return m, nil
		}
		if m.matchesKey("activity_scroll_up", message) {
			m.chat.HalfPageUp()
			return m, nil
		}
		if m.matchesKey("activity_scroll_down", message) {
			m.chat.HalfPageDown()
			if m.chat.AtBottom() {
				m.unreadEntries = 0
			}
			return m, nil
		}
		if m.permissionMenu {
			switch {
			case m.matchesKey("picker_previous", message):
				if m.permissionIndex > 0 {
					m.permissionIndex--
				}
			case m.matchesKey("picker_next", message):
				if m.permissionIndex+1 < len(tuiPermissionOptions) {
					m.permissionIndex++
				}
			}
			return m, nil
		}
		if m.sessionPicker {
			if m.renamingSession {
				if handled, command := m.handleInputEdit(message); handled {
					m.updateLayout()
					return m, command
				}
				command := m.updateInput(message)
				m.updateLayout()
				return m, command
			}
			if m.deleteConfirm {
				switch {
				case m.matchesKey("session_confirm_delete", message):
					return m.deleteSelectedSession()
				case m.matchesKey("session_cancel_delete", message):
					m.deleteConfirm = false
					m.status = "Session deletion cancelled."
					m.updateLayout()
				}
				return m, nil
			}
			switch {
			case m.matchesKey("picker_previous", message):
				if m.sessionIndex > 0 {
					m.sessionIndex--
				}
			case m.matchesKey("picker_next", message):
				if m.sessionIndex+1 < len(m.sessionList) {
					m.sessionIndex++
				}
			case m.matchesKey("session_toggle_archived", message):
				m.showArchived = !m.showArchived
				if err := m.reloadSessionList(m.sessionID); err != nil {
					m.showArchived = !m.showArchived
					m.status = "Could not update the session list."
					m.addEntry("error", err.Error(), true)
					return m, nil
				}
				if m.showArchived {
					m.status = "Showing archived sessions; " + m.keyLabel("session_toggle_archived") + " hides them."
				} else {
					m.status = "Showing active sessions; " + m.keyLabel("session_toggle_archived") + " shows archived sessions."
				}
			case m.matchesKey("session_archive", message):
				return m.toggleSelectedArchive()
			case m.matchesKey("session_rename", message):
				return m.beginSessionRename()
			case m.matchesKey("session_delete", message):
				if m.sessionIndex >= 0 && m.sessionIndex < len(m.sessionList) {
					m.deleteConfirm = true
					m.status = "Delete this session and all saved task state? Press " + m.keyLabel("session_confirm_delete") + " to confirm or " + m.keyLabel("session_cancel_delete") + "/Esc to cancel."
				}
			}
			m.updateLayout()
			return m, nil
		}
		if m.queuePicker {
			switch {
			case m.matchesKey("picker_previous", message):
				if m.queueIndex > 0 {
					m.queueIndex--
				}
			case m.matchesKey("picker_next", message):
				if m.queueIndex+1 < len(m.queued) {
					m.queueIndex++
				}
			case m.matchesKey("queue_remove", message):
				m.deleteQueuedInput()
			}
			m.updateLayout()
			return m, nil
		}
		if m.modelPicker {
			switch {
			case m.matchesKey("picker_previous", message):
				if m.modelIndex > 0 {
					m.modelIndex--
				}
			case m.matchesKey("picker_next", message):
				if m.modelIndex+1 < len(m.modelOptions) {
					m.modelIndex++
				}
			}
			m.updateLayout()
			return m, nil
		}
		if m.effortPicker {
			switch {
			case m.matchesKey("picker_previous", message):
				if m.effortIndex > 0 {
					m.effortIndex--
				}
			case m.matchesKey("picker_next", message):
				if m.effortIndex+1 < len(reasoningEffortOptions) {
					m.effortIndex++
				}
			}
			m.updateLayout()
			return m, nil
		}
		if m.pendingApproval != nil && m.running {
			switch {
			case m.matchesKey("approve_tool", message):
				m.resolveToolApproval(true)
			case m.matchesKey("deny_tool", message):
				m.resolveToolApproval(false)
			}
			return m, nil
		}
		if m.matchesKey("input_paste", message) && m.canAttachImages() {
			return m, m.readClipboardInput()
		}
		if len(m.commandSuggestions) > 0 {
			switch {
			case m.matchesKey("suggestion_previous", message):
				if m.suggestionIndex > 0 {
					m.suggestionIndex--
				}
				return m, nil
			case m.matchesKey("suggestion_next", message):
				if m.suggestionIndex+1 < len(m.commandSuggestions) {
					m.suggestionIndex++
				}
				return m, nil
			case m.matchesKey("suggestion_complete", message):
				m.setInputValue(m.commandSuggestions[m.suggestionIndex])
				m.updateCommandSuggestions()
				m.updateLayout()
				return m, nil
			}
		}
		if handled, command := m.handleInputEdit(message); handled {
			m.updateCommandSuggestions()
			m.updateLayout()
			return m, command
		}
		command := m.updateInput(message)
		m.updateCommandSuggestions()
		m.updateLayout()
		return m, command
	default:
		command := m.updateInput(message)
		m.updateCommandSuggestions()
		m.updateLayout()
		return m, command
	}
}

func (m *tuiModel) submit() (tea.Model, tea.Cmd) {
	raw := m.input.Value()
	line := strings.TrimSpace(raw)
	if m.pendingRequest != nil && m.pendingRequest.Kind == core.PendingUserQuestion && line != "" && !strings.HasPrefix(line, "/") {
		return m.answerUserQuestion(line)
	}
	if strings.HasPrefix(line, "/continue ") {
		return m.continueTask(line)
	}
	if strings.HasPrefix(line, "/plan replan") {
		return m.replanAfterBlocker(line)
	}
	if model, command, handled := m.updatePlanAuthorization(line); handled {
		return model, command
	}
	if len(m.attachments) > 0 && strings.HasPrefix(line, "/") {
		m.status = "Remove attached images before running a slash command."
		return m, nil
	}
	if m.mcpCommandPending && line != "" && !strings.HasPrefix(line, "/") {
		m.status = "Wait for the MCP operation to finish before starting a task."
		return m, nil
	}
	if m.planReview && line != "" && !strings.HasPrefix(line, "/") {
		m.planReview = false
	}
	switch line {
	case "":
		if len(m.attachments) == 0 {
			m.status = "Enter a message or command."
			return m, nil
		}
	case "/help":
		m.addEntry("notice", tuiHelpText(), false)
		m.resetInput()
		return m, nil
	case "/exit":
		return m.requestExit()
	case "/exit confirm":
		return m.confirmExit(true)
	case "/exit wait":
		return m.confirmExit(false)
	case "/resume":
		return m.openSessionPicker()
	case "/fork":
		return m.forkSession()
	case "/details":
		return m.showMessageDetails(line)
	case "/goal":
		return m.showGoal()
	case "/rewind":
		m.status = "Use /rewind <n> to remove the latest n user turns."
		return m, nil
	case "/queue":
		return m.openQueuePicker()
	case "/continue":
		return m.continueTask(line)
	case "/new":
		return m.newSession()
	case "/discard":
		return m.discard()
	case "/compact":
		return m.compact()
	case "/copyselect":
		return m.setCopySelectionFromCommand(line)
	case "/autocompact":
		return m.showAutoCompactSettings()
	case "/context":
		return m.showContextSettings()
	case "/permissions":
		return m.openPermissionMenu()
	case "/model":
		return m.openModelPicker()
	case "/mcp":
		return m.handleMCPCommand("/mcp")
	case "/effort":
		return m.openEffortPicker()
	case "/plan":
		return m.togglePlanMode()
	case "/usage":
		m.detailsOpen = !m.detailsOpen
		m.refreshPanels()
		m.status = "Usage details toggled."
		m.resetInput()
		m.updateLayout()
		return m, nil
	case "/vim":
		return m.toggleVimMode()
	case "/theme":
		m.status = fmt.Sprintf("Current theme: %s. Use /theme auto, /theme dark, or /theme light to change it.", m.themePreferenceLabel())
		m.resetInput()
		return m, nil
	case "/keymap":
		return m.openKeymapEditor()
	case "/keymap reset":
		return m.resetKeymap()
	case "/":
		m.updateCommandSuggestions()
		m.status = "Choose a command; use " + m.keyLabel("suggestion_previous") + "/" + m.keyLabel("suggestion_next") + " to select and " + m.keyLabel("send") + " to run."
		m.updateLayout()
		return m, nil
	}
	if strings.HasPrefix(line, "/permissions ") || strings.HasPrefix(line, "/permissions\t") {
		return m.setPermissionModeFromCommand(line)
	}
	if strings.HasPrefix(line, "/context ") || strings.HasPrefix(line, "/context\t") {
		return m.setContextSettingsFromCommand(line)
	}
	if strings.HasPrefix(line, "/copyselect ") || strings.HasPrefix(line, "/copyselect\t") {
		return m.setCopySelectionFromCommand(line)
	}
	if strings.HasPrefix(line, "/autocompact ") || strings.HasPrefix(line, "/autocompact\t") {
		return m.setAutoCompactFromCommand(line)
	}
	if strings.HasPrefix(line, "/goal ") || strings.HasPrefix(line, "/goal\t") {
		return m.setGoalFromCommand(line)
	}
	if strings.HasPrefix(line, "/details ") || strings.HasPrefix(line, "/details\t") {
		return m.showMessageDetails(line)
	}
	if strings.HasPrefix(line, "/model ") || strings.HasPrefix(line, "/model\t") {
		return m.setModelFromCommand(line)
	}
	if strings.HasPrefix(line, "/mcp ") || strings.HasPrefix(line, "/mcp\t") {
		return m.handleMCPCommand(line)
	}
	if strings.HasPrefix(line, "/effort ") || strings.HasPrefix(line, "/effort\t") {
		return m.setEffortFromCommand(line)
	}
	if strings.HasPrefix(line, "/rewind ") || strings.HasPrefix(line, "/rewind\t") {
		return m.rewindSession(line)
	}
	if strings.HasPrefix(line, "/theme ") || strings.HasPrefix(line, "/theme\t") {
		return m.setThemeFromCommand(line)
	}
	if strings.HasPrefix(line, "/keymap ") || strings.HasPrefix(line, "/keymap\t") {
		m.status = "Use /keymap to edit bindings or /keymap reset to restore defaults."
		return m, nil
	}
	if strings.HasPrefix(line, "/") {
		m.status = "Unknown command."
		m.addEntry("error", fmt.Sprintf("Unknown command %q. Available commands: %s", line, strings.Join(tuiCommandNames(), " ")), true)
		return m, nil
	}

	input, err := draftTaskInput(raw, m.attachments)
	if err != nil {
		m.addEntry("error", err.Error(), true)
		m.status = "Input was not accepted."
		return m, nil
	}
	if pending, err := m.hasPending(); err != nil {
		m.addEntry("error", err.Error(), true)
		m.status = "Could not inspect paused task."
		return m, nil
	} else if pending {
		m.status = "A task is paused. Use /continue or /discard before starting another turn."
		m.addEntry("notice", m.status, false)
		return m, nil
	}
	m.addEntry("you", inputTranscriptText(input, raw), false)
	m.resetInput()
	m.cancelled = false
	return m.submitTurn(input, core.StartIfIdle, true)
}

func (m *tuiModel) requestExit() (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		return m, tea.Quit
	}
	state, err := m.runtime.State(context.Background())
	if err != nil {
		m.status = "Could not inspect running turns before exit."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued || len(state.Queue) > 0 {
		m.exitConfirm = true
		m.status = fmt.Sprintf("Active work and %d queued turn(s) · /exit confirm interrupts and cancels queued turns · /exit wait finishes them · Esc returns.", len(state.Queue))
		m.resetInput()
		return m, nil
	}
	return m, tea.Quit
}

func (m *tuiModel) confirmExit(cancelQueued bool) (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		return m, tea.Quit
	}
	state, err := m.runtime.State(context.Background())
	if err != nil {
		m.addEntry("error", "Could not inspect the Core queue: "+err.Error(), true)
		return m, nil
	}
	if cancelQueued {
		for _, queued := range state.Queue {
			m.runtime.Interrupt(queued.ExecutionID)
		}
	}
	if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued {
		if cancelQueued {
			m.runtime.Interrupt(state.Task.ExecutionID)
		}
		m.exitConfirm = false
		m.exitAfterTask = true
		m.running = true
		m.runtimeTask = true
		m.runningExecutionID = state.Task.ExecutionID
		m.activeExecutions[state.Task.ExecutionID] = true
		m.status = "Waiting for Core to stop before exit…"
		return m, waitForTurn(m.runtime, state.Task.ExecutionID)
	}
	return m, tea.Quit
}

func (m *tuiModel) handleVimKey(message tea.KeyPressMsg) (bool, tea.Cmd) {
	if !m.vimMode {
		return false, nil
	}
	if m.sessionPicker || m.permissionMenu || m.queuePicker {
		return false, nil
	}
	keyName := message.String()
	if keyName == "esc" && m.planReview {
		return false, nil
	}
	if !m.vimNormal && keyName == "esc" {
		m.vimNormal = true
		m.status = "Vim normal mode · " + m.keyLabel("vim_insert") + " insert · " + m.keyLabel("vim_left") + "/" + m.keyLabel("vim_down") + "/" + m.keyLabel("vim_up") + "/" + m.keyLabel("vim_right") + " move · " + m.keyLabel("send") + " sends."
		return true, nil
	}
	if !m.vimNormal {
		return false, nil
	}
	switch {
	case m.matchesKey("vim_insert", message):
		m.vimNormal = false
		m.vimPendingDelete = false
		m.status = "Vim insert mode."
		return true, nil
	case m.matchesKey("vim_append", message):
		command := m.updateInput(tea.KeyPressMsg{Code: tea.KeyRight})
		m.vimNormal = false
		m.status = "Vim insert mode."
		return true, command
	case m.matchesKey("vim_left", message):
		command := m.updateInput(tea.KeyPressMsg{Code: tea.KeyLeft})
		return true, command
	case m.matchesKey("vim_down", message):
		m.input.CursorDown()
		return true, nil
	case m.matchesKey("vim_up", message):
		m.input.CursorUp()
		return true, nil
	case m.matchesKey("vim_line_start", message):
		m.input.CursorStart()
		return true, nil
	case m.matchesKey("vim_line_end", message):
		m.input.CursorEnd()
		return true, nil
	case m.matchesKey("vim_delete_line", message):
		if m.vimPendingDelete {
			m.deleteCurrentLine()
			m.vimPendingDelete = false
		} else {
			m.vimPendingDelete = true
		}
		return true, nil
	case keyName == "esc":
		m.vimPendingDelete = false
		return true, nil
	}
	if message.Text != "" {
		return true, nil
	}
	return false, nil
}

func (m *tuiModel) deleteCurrentLine() {
	cursor := m.input.Cursor()
	if cursor == nil {
		return
	}
	lines := strings.Split(m.input.Value(), "\n")
	line := min(max(cursor.Y, 0), len(lines)-1)
	lines = append(lines[:line], lines[line+1:]...)
	before := m.input.Value()
	m.input.SetValue(strings.Join(lines, "\n"))
	m.recordInputChange(before, m.input.Value())
}
