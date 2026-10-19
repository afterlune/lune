package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	"github.com/atotto/clipboard"
)

type planImplementedMsg struct {
	impl     core.PlanImplementation
	planText string
	err      error
}

type clearCopyFeedbackMsg struct {
	at time.Time
}

// Update handles all incoming Bubble Tea messages and key events.
func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		UpdateTerminalBackground(msg.IsDark())
		m.applyThemeStyles()
		m.invalidateEntryCache()
		m.refreshViewportContent()
		m.updateLayout()
		return m, nil

	case tea.PasteMsg:
		if m.activeQuestion != nil && m.activeQuestion.Submitting {
			return m, nil
		}
		if m.activeQuestion != nil && len(m.activeQuestion.Request.Questions) > 0 {
			currIdx := min(m.activeQuestion.CurrentIndex, len(m.activeQuestion.Request.Questions)-1)
			currQ := m.activeQuestion.Request.Questions[currIdx]
			if m.activeQuestion.NotesOpen {
				m.activeQuestion.NoteText += cleanToolInline(msg.Content)
				m.updateLayout()
				return m, nil
			}
			if m.activeQuestion.Selected >= len(currQ.Options) {
				m.activeQuestion.Custom += cleanToolInline(msg.Content)
				m.updateLayout()
				return m, nil
			}
		}
		m.composer.InsertString(msg.Content)
		m.updateLayout()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateLayout()
		return m, nil

	case tickMsg:
		if m.running || (m.activeRecovery != nil && !m.activeRecovery.RetryNotBefore.IsZero()) {
			m.refreshViewportContent()
			return m, tickClock()
		}

	case runtimeStreamReadyMsg:
		if msg.sessionID != "" && msg.sessionID != m.sessionID {
			return m, nil
		}
		if msg.err != nil {
			m.addSystemEntry("Failed to connect to runtime event stream: " + msg.err.Error())
			return m, nil
		}
		return m, nextRuntimeEvent(m.sessionID, msg.events)

	case runtimeEventMsg:
		if msg.sessionID != "" && msg.sessionID != m.sessionID {
			return m, nil
		}
		if msg.event.Kind == "" && msg.events == nil {
			return m, nil
		}
		var mod tea.Model
		var cmd tea.Cmd
		mod, cmd = m.processRuntimeEventNoRefresh(msg.event)
		m = mod.(*tuiModel)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		for _, batchEv := range msg.batch {
			mod, cmd = m.processRuntimeEventNoRefresh(batchEv)
			m = mod.(*tuiModel)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if m.transcriptDirty || m.cachedDirty {
			m.refreshViewportContent()
		}
		cmds = append(cmds, nextRuntimeEvent(m.sessionID, msg.events))
		return m, tea.Batch(cmds...)

	case turnSubmittedMsg:
		if msg.sessionID != "" && msg.sessionID != m.sessionID {
			return m, nil
		}
		m.pausedCheckpoint = nil
		if msg.err != nil {
			if len(m.queuedTurns) == 0 {
				m.running = false
			}
			m.addSystemEntry("Failed to submit task: " + msg.err.Error())
			m.updateLayout()
			return m, nil
		}
		if msg.submission.Status == core.SubmissionQueued {
			m.queuedTurns = append(m.queuedTurns, QueuedItem{
				ExecutionID: msg.submission.ExecutionID,
			})
			for i := len(m.entries) - 1; i >= 0; i-- {
				if m.entries[i].Kind == KindQueued && m.entries[i].ToolCallID == "" {
					m.entries[i].ToolCallID = msg.submission.ExecutionID
					break
				}
			}
			m.refreshViewportContent()
			return m, nil
		}
		m.running = true
		m.runningExecution = msg.submission.ExecutionID
		m.refreshViewportContent()
		cmds = append(cmds, tickClock(), m.waitForExecution(msg.submission.ExecutionID))
		return m, tea.Batch(cmds...)

	case turnFinishedMsg:
		if msg.sessionID != "" && msg.sessionID != m.sessionID {
			return m, nil
		}
		return m, m.finishTurn(msg)

	case questionSubmittedMsg:
		return m, m.finishQuestionSubmission(msg)

	case compactDoneMsg:
		if msg.err != nil {
			m.addSystemEntry("Compaction failed: " + msg.err.Error())
		} else {
			m.addSystemEntry("Compacted older conversation history.")
		}
		return m, nil

	case sessionForkedMsg:
		if msg.err != nil {
			m.addSystemEntry("Fork failed: " + msg.err.Error())
			return m, nil
		}
		if err := m.bindSessionRuntime(context.Background(), msg.session.ID); err != nil {
			m.addSystemEntry("Failed to bind forked session runtime: " + err.Error())
			return m, nil
		}
		m.addSystemEntry("Forked into new session: " + msg.session.ID)
		return m, m.subscribeRuntimeEvents()

	case sessionCreatedMsg:
		if msg.err != nil {
			m.addSystemEntry("New session failed: " + msg.err.Error())
			return m, nil
		}
		if err := m.bindSessionRuntime(context.Background(), msg.session.ID); err != nil {
			m.addSystemEntry("Failed to bind new session runtime: " + err.Error())
			return m, nil
		}
		m.addSystemEntry("Started fresh session.")
		return m, m.subscribeRuntimeEvents()

	case planImplementedMsg:
		if msg.err != nil {

			m.addSystemEntry("Plan implementation failed: " + msg.err.Error())
			return m, nil
		}
		var planCmds []tea.Cmd
		if msg.impl.SessionID != "" && msg.impl.SessionID != m.sessionID {
			if err := m.bindSessionRuntime(context.Background(), msg.impl.SessionID); err != nil {

				m.addSystemEntry("Failed to bind new plan session: " + err.Error())
				return m, nil
			}
			planCmds = append(planCmds, m.subscribeRuntimeEvents())
		}
		message := "Starting plan implementation."
		if archivedPath := m.archivePlan(msg.planText); archivedPath != "" {
			message += fmt.Sprintf(" Archived plan: `%s`.", archivedPath)
		}
		m.addSystemEntry(message)
		m.running = true
		m.taskStartedAt = time.Now()
		m.runningExecution = msg.impl.Submission.ExecutionID
		planCmds = append(planCmds, tickClock(), m.waitForExecution(msg.impl.Submission.ExecutionID))
		return m, tea.Batch(planCmds...)

	case taskDiscardedMsg:
		m.pausedCheckpoint = nil
		m.activeRecovery = nil
		m.activeToolRecovery = nil
		m.running = false
		if msg.err != nil {
			m.addSystemEntry("Discard failed: " + msg.err.Error())
		} else {
			m.addSystemEntry("Discarded paused task.")
		}
		m.updateLayout()
		return m, nil

	case clearCopyFeedbackMsg:
		if !time.Now().Before(msg.at) {
			m.copyFeedback = ""
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)

	case tea.MouseWheelMsg:
		var vpCmd tea.Cmd
		m.viewport, vpCmd = m.viewport.Update(msg)
		m.scrolledUp = !m.viewport.AtBottom()
		return m, vpCmd

	case tea.MouseClickMsg:
		if m.shortcutsOpen {
			m.shortcutsOpen = false
			m.updateLayout()
			return m, nil
		}
		headerHeight := 3
		vpEnd := headerHeight + m.viewport.Height()
		if msg.Button == tea.MouseLeft {
			if msg.Y >= headerHeight && msg.Y < vpEnd {
				m.isSelecting = true
				m.selectStartX = msg.X
				m.selectStartY = msg.Y
				m.selectEndX = msg.X
				m.selectEndY = msg.Y
				m.selectedText = ""
				return m, nil
			}
			if m.scrolledUp {
				m.viewport.GotoBottom()
				m.scrolledUp = false
				m.isSelecting = false
				m.selectedText = ""
				m.selectStartX = 0
				m.selectEndX = 0
				m.selectStartY = 0
				m.selectEndY = 0
				return m, nil
			}
			m.isSelecting = false
			m.selectedText = ""
			m.selectStartX = 0
			m.selectEndX = 0
			m.selectStartY = 0
			m.selectEndY = 0
		}

	case tea.MouseMotionMsg:
		if m.isSelecting {
			m.selectEndX = msg.X
			m.selectEndY = msg.Y
			return m, nil
		}

	case tea.MouseReleaseMsg:
		if m.isSelecting {
			m.selectEndX = msg.X
			m.selectEndY = msg.Y
			text := m.extractSelectedText()
			m.isSelecting = false
			if strings.TrimSpace(text) != "" {
				m.selectedText = text
				_ = clipboard.WriteAll(text)
				count := utf8.RuneCountInString(text)
				m.copyFeedback = fmt.Sprintf("Copied %d chars to host clipboard", count)
				m.copyFeedbackExpiresAt = time.Now().Add(3 * time.Second)
				expiresAt := m.copyFeedbackExpiresAt
				return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
					return clearCopyFeedbackMsg{at: expiresAt}
				})
			}
			if msg.X == m.selectStartX && msg.Y == m.selectStartY {
				headerHeight := 3
				vpRow := msg.Y - headerHeight
				absLine := m.viewport.YOffset() + vpRow
				toggled := false
				for _, rng := range m.entryLineRanges {
					if absLine >= rng.StartLine && absLine <= rng.EndLine && rng.EntryIndex >= 0 && rng.EntryIndex < len(m.entries) {
						if rng.IsRoundHeader {
							m.entries[rng.EntryIndex].RoundExpanded = !m.entries[rng.EntryIndex].RoundExpanded
							toggled = true
							break
						}
						switch m.entries[rng.EntryIndex].Kind {
						case KindTool, KindQuestionAnswered:
							m.entries[rng.EntryIndex].ToolExpanded = !m.entries[rng.EntryIndex].ToolExpanded
							toggled = true
						case KindReasoning:
							m.entries[rng.EntryIndex].ReasoningExpanded = !m.entries[rng.EntryIndex].ReasoningExpanded
							toggled = true
						case KindSubagent:
							m.entries[rng.EntryIndex].SubagentExpanded = !m.entries[rng.EntryIndex].SubagentExpanded
							toggled = true
						}
						break
					}
				}
				if !toggled {
					lines := strings.Split(m.viewport.View(), "\n")
					if vpRow >= 0 && vpRow < len(lines) {
						lineText := lines[vpRow]
						if strings.Contains(lineText, "Ran") || strings.Contains(lineText, "Explored") || strings.Contains(lineText, "Failed") || strings.Contains(lineText, "Running") || strings.Contains(lineText, "expand") || strings.Contains(lineText, "collapse") {
							for i := len(m.entries) - 1; i >= 0; i-- {
								if isRoundMember(m.entries, i) {
									start := i
									for start > 0 && isRoundMember(m.entries, start-1) {
										start--
									}
									m.entries[start].RoundExpanded = !m.entries[start].RoundExpanded
									toggled = true
									break
								}
							}
						}
					}
				}
				if toggled {
					m.invalidateEntryCache()
					m.refreshViewportContent()
				}
			}
			m.selectedText = ""
			m.selectStartX = 0
			m.selectEndX = 0
			m.selectStartY = 0
			m.selectEndY = 0
			return m, nil
		}
	}

	// Default composer update
	var compCmd tea.Cmd
	m.composer, compCmd = m.composer.Update(msg)
	if compCmd != nil {
		cmds = append(cmds, compCmd)
	}

	return m, tea.Batch(cmds...)
}

func (m *tuiModel) finishTurn(msg turnFinishedMsg) tea.Cmd {
	executionID := msg.executionID
	if executionID == "" {
		executionID = msg.result.ExecutionID
	}
	if executionID == "" {
		executionID = m.runningExecution
	}
	if executionID == "" {
		return nil
	}
	if msg.generation != 0 && m.awaitingTurns[executionID] != msg.generation {
		return nil
	}
	if msg.result.Status == core.TaskPaused && m.stalePause(executionID, "") {
		delete(m.awaitingTurns, executionID)
		if m.taskSnapshot.ExecutionID == executionID {
			switch m.taskSnapshot.Status {
			case core.TaskCompleted, core.TaskFailed, core.TaskCancelled, core.TaskDiscarded:
				m.runningExecution = executionID
				return m.awaitExecutionResult(executionID)
			}
		}
		return m.waitForExecution(executionID)
	}
	event, eventFinalized := m.terminalEvents[executionID]
	if m.runningExecution != executionID && !eventFinalized {
		return nil
	}
	if msg.err != nil && eventFinalized {
		msg.result = core.TurnResult{ExecutionID: executionID, Status: event.Status, BudgetReason: event.BudgetReason}
		if event.Err == "" {
			msg.err = nil
		} else {
			msg.err = fmt.Errorf("%s", event.Err)
		}
	}
	delete(m.awaitingTurns, executionID)
	delete(m.terminalEvents, executionID)
	if m.taskSnapshot.ExecutionID == executionID {
		m.taskSnapshot.Status = msg.result.Status
	}

	var planText string
	if msg.result.ProposedPlan != nil {
		planText = msg.result.ProposedPlan.Text
	}
	m.finalizeTurnOutput(planText, msg.result.Response, executionID)
	if msg.err != nil {
		m.addSystemEntry("Task failed: " + msg.err.Error())
	} else if msg.result.Status == core.TaskFailed {
		if msg.result.ModelFailure != nil {
			reason := string(msg.result.ModelFailure.Category)
			if msg.result.ModelFailure.Code != "" {
				reason += " (" + msg.result.ModelFailure.Code + ")"
			} else if msg.result.ModelFailure.StatusCode > 0 {
				reason += fmt.Sprintf(" (HTTP %d)", msg.result.ModelFailure.StatusCode)
			}
			if reason == "" {
				reason = "model request failed"
			}
			m.addSystemEntry("Task failed: " + reason)
		} else if msg.result.BudgetReason != "" {
			m.addSystemEntry(fmt.Sprintf("Task stopped: execution budget limit reached (`%s`).", msg.result.BudgetReason))
		}
	}
	if m.runningExecution == executionID {
		m.running = false
		m.thinking = false
		if msg.result.Status == core.TaskPaused {
			if m.pausedCheckpoint == nil {
				m.pausedCheckpoint = m.pausedCheckpointFor(executionID)
			}
			m.checkPendingState(context.Background())
		} else {
			m.runningExecution = ""
			m.pausedCheckpoint = nil
			m.activeRecovery = nil
			m.activeToolRecovery = nil
		}
	}
	m.refreshViewportContent()
	m.updateLayout()
	return nil
}

func (m *tuiModel) finishTurnEvent(event core.Event) {
	executionID := event.ExecutionID
	if executionID == "" || executionID != m.runningExecution {
		return
	}
	if m.staleTaskAttempt(executionID, event.TaskID) ||
		(event.Status == core.TaskPaused && m.stalePause(executionID, event.TaskID)) {
		return
	}
	if event.Status == core.TaskPaused && !m.running {
		if _, awaiting := m.awaitingTurns[executionID]; !awaiting {
			return
		}
	}
	if _, finalized := m.terminalEvents[executionID]; finalized {
		return
	}
	main := m.ensureAgentMetrics("lead")
	switch event.Status {
	case core.TaskPaused:
		main.Status, main.Activity = "Paused", "Paused for input or recovery"
	case core.TaskFailed:
		main.Status, main.Activity = "Failed", "Failed"
	case core.TaskCancelled:
		main.Status, main.Activity = "Cancelled", "Cancelled"
	default:
		main.Status, main.Activity = "Completed", "Completed"
	}
	if !main.StartedAt.IsZero() {
		main.Duration = time.Since(main.StartedAt)
	}
	m.terminalEvents[executionID] = event
	m.taskSnapshot.Status = event.Status
	m.thinking = false
	m.finalizeTurnOutput("", nil, executionID)
	if event.Status == core.TaskPaused {
		if m.pausedCheckpoint == nil {
			m.pausedCheckpoint = m.pausedCheckpointFor(executionID)
		}
		m.checkPendingState(context.Background())
	} else {
		m.pausedCheckpoint = nil
		m.activeRecovery = nil
		m.activeToolRecovery = nil
	}
	m.transcriptDirty = true
	m.updateLayout()
}

func (m *tuiModel) pausedCheckpointFor(executionID string) *PausedCheckpoint {
	checkpoint := &PausedCheckpoint{ExecutionID: executionID}
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindUser {
			break
		}
		if m.entries[i].Kind == KindTool {
			checkpoint.ToolCount++
			if checkpoint.LastToolName == "" {
				checkpoint.LastToolName = m.entries[i].ToolName
			}
		}
	}
	return checkpoint
}

func (m *tuiModel) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	keyStr := msg.String()
	if m.activeQuestion != nil && m.activeQuestion.Submitting && keyStr != "ctrl+c" {
		return m, nil
	}

	// 0. Shortcut Help Modal handling
	if m.shortcutsOpen {
		switch keyStr {
		case "esc", "?", "q", "enter":
			m.shortcutsOpen = false
			m.updateLayout()
			return m, nil
		}
		return m, nil
	}

	if m.agentsPanelOpen {
		ids := m.agentMetricIDs()
		switch keyStr {
		case "esc":
			m.closeAgentsPanel()
			return m, nil
		case "backspace", "left":
			if m.agentsPanelDetail {
				m.agentsPanelDetail = false
				m.agentsPanelDetailID = ""
				m.updateLayout()
			}
			return m, nil
		case "up", "ctrl+p":
			if len(ids) > 0 {
				if m.agentsPanelSelected > 0 {
					m.agentsPanelSelected--
				} else {
					m.agentsPanelSelected = len(ids) - 1
				}
				if m.agentsPanelDetail {
					m.agentsPanelDetailID = ids[m.agentsPanelSelected]
				}
				m.updateLayout()
			}
			return m, nil
		case "down", "ctrl+n":
			if len(ids) > 0 {
				if m.agentsPanelSelected < len(ids)-1 {
					m.agentsPanelSelected++
				} else {
					m.agentsPanelSelected = 0
				}
				if m.agentsPanelDetail {
					m.agentsPanelDetailID = ids[m.agentsPanelSelected]
				}
				m.updateLayout()
			}
			return m, nil
		case "enter", "right":
			if m.agentsPanelDetail {
				m.agentsPanelDetail = false
				m.agentsPanelDetailID = ""
			} else if len(ids) > 0 && m.agentsPanelSelected >= 0 && m.agentsPanelSelected < len(ids) {
				m.agentsPanelDetail = true
				m.agentsPanelDetailID = ids[m.agentsPanelSelected]
			}
			m.updateLayout()
			return m, nil
		default:
			return m, nil
		}
	}

	// 0.1 Session Switcher Modal handling
	if m.sessionsModalOpen {
		if m.sessionsConfirmDelete {
			switch keyStr {
			case "y", "Y":
				targetID := m.sessionsDeletingID
				m.sessionsConfirmDelete = false
				m.sessionsDeletingID = ""
				if targetID == m.sessionID {
					m.addSystemEntry("Cannot delete currently active session. Switch to another session first.")
					m.updateLayout()
					return m, nil
				}
				if err := m.client.DeleteSession(context.Background(), targetID); err != nil {
					m.addSystemEntry("Failed to delete session: " + err.Error())
					m.updateLayout()
					return m, nil
				}
				var updated []core.Session
				for _, s := range m.sessionsItems {
					if s.ID != targetID {
						updated = append(updated, s)
					}
				}
				m.sessionsItems = updated
				m.filterSessions()
				m.addSystemEntry("Deleted session: " + targetID)
				m.updateLayout()
				return m, nil
			case "n", "N", "esc":
				m.sessionsConfirmDelete = false
				m.sessionsDeletingID = ""
				m.updateLayout()
				return m, nil
			default:
				return m, nil
			}
		}

		switch keyStr {
		case "esc", "ctrl+o":
			m.sessionsModalOpen = false
			m.sessionsFilter = ""
			m.sessionsConfirmDelete = false
			m.sessionsDeletingID = ""
			m.updateLayout()
			return m, nil
		case "ctrl+d", "delete":
			if len(m.sessionsFiltered) > 0 && m.sessionsIndex >= 0 && m.sessionsIndex < len(m.sessionsFiltered) {
				target := m.sessionsFiltered[m.sessionsIndex]
				if target.ID == m.sessionID {
					m.addSystemEntry("Cannot delete currently active session. Switch to another session first.")
					m.updateLayout()
					return m, nil
				}
				m.sessionsConfirmDelete = true
				m.sessionsDeletingID = target.ID
				m.updateLayout()
				return m, nil
			}
			return m, nil
		case "up", "ctrl+p":
			if len(m.sessionsFiltered) > 0 {
				if m.sessionsIndex > 0 {
					m.sessionsIndex--
				} else {
					m.sessionsIndex = len(m.sessionsFiltered) - 1
				}
				m.updateLayout()
			}
			return m, nil
		case "down", "ctrl+n":
			if len(m.sessionsFiltered) > 0 {
				if m.sessionsIndex < len(m.sessionsFiltered)-1 {
					m.sessionsIndex++
				} else {
					m.sessionsIndex = 0
				}
				m.updateLayout()
			}
			return m, nil
		case "enter":
			if len(m.sessionsFiltered) > 0 && m.sessionsIndex >= 0 && m.sessionsIndex < len(m.sessionsFiltered) {
				targetID := m.sessionsFiltered[m.sessionsIndex].ID
				m.sessionsModalOpen = false
				m.sessionsFilter = ""
				if targetID == m.sessionID {
					m.addSystemEntry("Already in session: " + targetID)
					m.updateLayout()
					return m, nil
				}
				return m.switchToSession(targetID)
			}
			m.sessionsModalOpen = false
			m.sessionsFilter = ""
			m.updateLayout()
			return m, nil
		case "backspace":
			if len(m.sessionsFilter) > 0 {
				runes := []rune(m.sessionsFilter)
				m.sessionsFilter = string(runes[:len(runes)-1])
				m.filterSessions()
				m.updateLayout()
			}
			return m, nil
		default:
			text := msg.Key().Text
			if text == "" && msg.Key().Code >= ' ' && msg.Key().Code <= '~' && msg.Key().Mod == 0 {
				text = string(msg.Key().Code)
			}
			if text != "" {
				m.sessionsFilter += text
				m.filterSessions()
				m.updateLayout()
				return m, nil
			}
		}
		return m, nil
	}

	// 0.2 Tool Output Pager Modal handling
	if m.toolPagerOpen {
		toolIndices := m.getToolEntryIndices()
		toolCount := len(toolIndices)
		if toolCount == 0 {
			m.toolPagerOpen = false
			m.updateLayout()
			return m, nil
		}
		if m.toolPagerIndex < 0 {
			m.toolPagerIndex = 0
		} else if m.toolPagerIndex >= toolCount {
			m.toolPagerIndex = toolCount - 1
		}

		entry := m.entries[toolIndices[m.toolPagerIndex]]
		trimmedOut := strings.TrimRight(entry.ToolOutput, "\r\n")
		var rawLines []string
		if strings.TrimSpace(trimmedOut) == "" {
			rawLines = []string{}
		} else {
			rawLines = strings.Split(trimmedOut, "\n")
		}
		totalLines := len(rawLines)
		visibleLines := m.toolPagerVisibleLines()
		maxScroll := max(0, totalLines-visibleLines)

		switch keyStr {
		case "esc", "q", "ctrl+t":
			m.toolPagerOpen = false
			m.updateLayout()
			return m, nil

		case "up", "k":
			if m.toolPagerScrollY > 0 {
				m.toolPagerScrollY--
				m.updateLayout()
			}
			return m, nil

		case "down", "j":
			if m.toolPagerScrollY < maxScroll {
				m.toolPagerScrollY++
				m.updateLayout()
			}
			return m, nil

		case "pgup":
			m.toolPagerScrollY = max(0, m.toolPagerScrollY-visibleLines)
			m.updateLayout()
			return m, nil

		case "pgdown":
			m.toolPagerScrollY = min(maxScroll, m.toolPagerScrollY+visibleLines)
			m.updateLayout()
			return m, nil

		case "home", "g":
			m.toolPagerScrollY = 0
			m.updateLayout()
			return m, nil

		case "end", "G":
			m.toolPagerScrollY = maxScroll
			m.updateLayout()
			return m, nil

		case "[", "p", "ctrl+p":
			if m.toolPagerIndex > 0 {
				m.toolPagerIndex--
			} else {
				m.toolPagerIndex = toolCount - 1
			}
			m.toolPagerScrollY = 0
			m.updateLayout()
			return m, nil

		case "]", "n", "ctrl+n":
			if m.toolPagerIndex < toolCount-1 {
				m.toolPagerIndex++
			} else {
				m.toolPagerIndex = 0
			}
			m.toolPagerScrollY = 0
			m.updateLayout()
			return m, nil

		case "c", "ctrl+c":
			outText := entry.ToolOutput
			_ = clipboard.WriteAll(outText)
			count := utf8.RuneCountInString(outText)
			m.copyFeedback = fmt.Sprintf("Copied %d chars of tool output to clipboard", count)
			m.copyFeedbackExpiresAt = time.Now().Add(3 * time.Second)
			expiresAt := m.copyFeedbackExpiresAt
			m.updateLayout()
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearCopyFeedbackMsg{at: expiresAt}
			})
		}
		return m, nil
	}

	// 0.3 Workspace Diff Viewer Modal handling
	if m.diffModalOpen {
		fileCount := len(m.diffFiles)
		if fileCount == 0 {
			m.diffModalOpen = false
			m.updateLayout()
			return m, nil
		}
		if m.diffFileIndex < 0 {
			m.diffFileIndex = 0
		} else if m.diffFileIndex >= fileCount {
			m.diffFileIndex = fileCount - 1
		}

		file := m.diffFiles[m.diffFileIndex]
		totalLines := len(file.Lines)
		visibleLines := m.diffModalVisibleLines()
		maxScroll := max(0, totalLines-visibleLines)

		switch keyStr {
		case "esc", "q", "ctrl+d":
			m.diffModalOpen = false
			m.updateLayout()
			return m, nil

		case "up", "k":
			if m.diffScrollY > 0 {
				m.diffScrollY--
				m.updateLayout()
			}
			return m, nil

		case "down", "j":
			if m.diffScrollY < maxScroll {
				m.diffScrollY++
				m.updateLayout()
			}
			return m, nil

		case "pgup":
			m.diffScrollY = max(0, m.diffScrollY-visibleLines)
			m.updateLayout()
			return m, nil

		case "pgdown":
			m.diffScrollY = min(maxScroll, m.diffScrollY+visibleLines)
			m.updateLayout()
			return m, nil

		case "home", "g":
			m.diffScrollY = 0
			m.updateLayout()
			return m, nil

		case "end", "G":
			m.diffScrollY = maxScroll
			m.updateLayout()
			return m, nil

		case "tab", "]", "n", "ctrl+n":
			if m.diffFileIndex < fileCount-1 {
				m.diffFileIndex++
			} else {
				m.diffFileIndex = 0
			}
			m.diffScrollY = 0
			m.updateLayout()
			return m, nil

		case "shift+tab", "[", "p", "ctrl+p":
			if m.diffFileIndex > 0 {
				m.diffFileIndex--
			} else {
				m.diffFileIndex = fileCount - 1
			}
			m.diffScrollY = 0
			m.updateLayout()
			return m, nil

		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			targetIdx := int(keyStr[0] - '1')
			if targetIdx < fileCount {
				m.diffFileIndex = targetIdx
				m.diffScrollY = 0
				m.updateLayout()
			}
			return m, nil

		case "a":
			var allDiffs []string
			for _, f := range m.diffFiles {
				allDiffs = append(allDiffs, f.RawDiff)
			}
			fullText := strings.Join(allDiffs, "\n\n")
			_ = clipboard.WriteAll(fullText)
			count := utf8.RuneCountInString(fullText)
			m.copyFeedback = fmt.Sprintf("Copied all diffs (%d chars) to clipboard", count)
			m.copyFeedbackExpiresAt = time.Now().Add(3 * time.Second)
			expiresAt := m.copyFeedbackExpiresAt
			m.updateLayout()
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearCopyFeedbackMsg{at: expiresAt}
			})

		case "c", "ctrl+c":
			diffText := file.RawDiff
			_ = clipboard.WriteAll(diffText)
			count := utf8.RuneCountInString(diffText)
			m.copyFeedback = fmt.Sprintf("Copied %d chars of diff to clipboard", count)
			m.copyFeedbackExpiresAt = time.Now().Add(3 * time.Second)
			expiresAt := m.copyFeedbackExpiresAt
			m.updateLayout()
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearCopyFeedbackMsg{at: expiresAt}
			})
		}
		return m, nil
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

func (m *tuiModel) resolveActiveQuestion() (tea.Model, tea.Cmd) {
	q := m.activeQuestion
	if q == nil || q.Submitting || len(q.Request.Questions) == 0 {
		return m, nil
	}

	currIdx := q.CurrentIndex
	if currIdx >= len(q.Request.Questions) {
		currIdx = len(q.Request.Questions) - 1
	}
	qItem := q.Request.Questions[currIdx]
	optCount := len(qItem.Options)
	var answerText string
	if q.Selected < optCount {
		opt := qItem.Options[q.Selected]
		answerText = opt.Label
	} else {
		answerText = "None of the above"
		if strings.TrimSpace(q.Custom) != "" {
			answerText = strings.TrimSpace(q.Custom)
		}
	}
	if note := strings.TrimSpace(q.NoteText); note != "" {
		if answerText == "None of the above" {
			answerText = note
		} else {
			answerText += " (" + note + ")"
		}
	}

	qaItem := AnsweredQuestionItem{
		Question: qItem.Question,
		Answer:   answerText,
	}
	m.recordQuestionAnswer(q, currIdx, qaItem)

	answer := core.UserQuestionResponse{
		QuestionID: qItem.ID,
		Answer:     answerText,
	}
	if currIdx < len(q.Answers) {
		q.Answers[currIdx] = answer
	} else {
		q.Answers = append(q.Answers, answer)
	}

	// If there are more questions in this batch, advance to the next question
	if currIdx+1 < len(q.Request.Questions) {
		q.CurrentIndex = currIdx + 1
		q.Selected = 0
		q.Custom = ""
		q.NoteText = ""
		q.NotesOpen = false
		m.refreshViewportContent()
		m.updateLayout()
		return m, nil
	}

	// Retain the answered batch until Core acknowledges the exact wait identity.
	cmd := m.submitQuestionBatch(q)
	m.refreshViewportContent()
	m.viewport.GotoBottom()
	m.updateLayout()
	return m, cmd
}

func (m *tuiModel) resolveActivePlanReview() (tea.Model, tea.Cmd) {
	review := m.activePlanReview
	if review == nil {
		return m, nil
	}

	var mode core.PlanImplementationMode
	switch review.Selected {
	case 0:
		mode = core.PlanImplementSameContext
	case 1:
		mode = core.PlanImplementNewContext
	default:
		m.activePlanReview = nil
		m.updateLayout()
		return m, nil
	}

	m.activePlanReview = nil
	m.planMode = false
	permissionMode := m.savedMode
	_ = m.client.SetPermissionMode(m.savedMode)
	m.permissionMode = m.savedMode
	m.updateLayout()

	runtime := m.runtime
	return m, func() tea.Msg {
		impl, err := runtime.ImplementPlan(context.Background(), review.ExecutionID, core.PlanImplementationOptions{Mode: mode, PermissionMode: permissionMode})
		return planImplementedMsg{impl: impl, planText: review.Text, err: err}
	}
}

func (m *tuiModel) resolveActiveApproval() (tea.Model, tea.Cmd) {
	appr := m.activeApproval
	if appr == nil {
		return m, nil
	}
	allowed := appr.Selected == 0
	apprID := appr.Request.ID
	toolName := appr.Request.Tool
	m.activeApproval = nil
	m.running = true
	m.thinking = false
	m.taskStartedAt = time.Now()
	m.scrolledUp = false
	m.refreshViewportContent()
	m.viewport.GotoBottom()
	m.updateLayout()

	decision := "Denied"
	if allowed {
		decision = "Allowed"
	}
	m.addSystemEntry(fmt.Sprintf("%s tool execution for `%s`.", decision, toolName))

	runtime := m.runtime
	executionID := m.runningExecution
	return m, tea.Batch(
		tickClock(),
		m.waitForExecution(executionID),
		func() tea.Msg {
			_ = runtime.ResolveApproval(apprID, allowed)
			return nil
		},
	)
}

func (m *tuiModel) resolveActiveToolRecovery() (tea.Model, tea.Cmd) {
	tr := m.activeToolRecovery
	if tr == nil {
		return m, nil
	}
	retry := tr.Selected == 0
	invocationID := tr.InvocationID
	toolName := tr.ToolName
	m.activeToolRecovery = nil
	m.running = true
	m.thinking = false
	m.taskStartedAt = time.Now()
	m.scrolledUp = false
	m.refreshViewportContent()
	m.viewport.GotoBottom()
	m.updateLayout()

	decisionStr := "Skipping"
	decision := core.ToolRecoverySkip
	if retry {
		decisionStr = "Retrying"
		decision = core.ToolRecoveryRetry
	}
	m.addSystemEntry(fmt.Sprintf("%s tool execution for `%s`.", decisionStr, toolName))

	runtime := m.runtime
	sessionID := m.sessionID
	return m, func() tea.Msg {
		ctx := context.Background()
		if err := runtime.ResolveToolRecovery(ctx, invocationID, decision); err != nil {
			return turnSubmittedMsg{sessionID: sessionID, err: err}
		}
		sub, err := runtime.Recover(ctx)
		return turnSubmittedMsg{sessionID: sessionID, submission: sub, err: err}
	}
}

// interruptRunningTask halts an ongoing execution, preserves the execution checkpoint,
// and presents a rich checkpoint status card in the transcript.
func (m *tuiModel) interruptRunningTask() (tea.Model, tea.Cmd) {
	if !m.running || m.runtime == nil || m.runningExecution == "" {
		return m, nil
	}

	if m.currentReasoningText != "" {
		dur := time.Since(m.reasoningStartTime)
		m.entries = append(m.entries, TranscriptEntry{
			Kind:              KindReasoning,
			Text:              m.currentReasoningText,
			ReasoningDuration: dur,
			ReasoningExpanded: false,
			Timestamp:         time.Now(),
		})
		m.currentReasoningText = ""
		m.reasoningStartTime = time.Time{}
		m.invalidateEntryCache()
	}
	if m.currentStreamText != "" {
		m.entries = append(m.entries, TranscriptEntry{
			ID:        fmt.Sprintf("asst-%d", time.Now().UnixNano()),
			Kind:      KindAssistant,
			Text:      m.currentStreamText,
			Timestamp: time.Now(),
		})
		m.currentStreamText = ""
		m.invalidateEntryCache()
	}

	execID := m.runningExecution
	toolCount := 0
	lastToolName := ""
	lastToolCmd := ""
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindUser {
			break
		}
		if m.entries[i].Kind == KindTool {
			toolCount++
			if lastToolName == "" {
				lastToolName = m.entries[i].ToolName
				lastToolCmd = m.entries[i].ToolCommand
			}
		}
	}

	m.pausedCheckpoint = &PausedCheckpoint{
		ExecutionID:  execID,
		ToolCount:    toolCount,
		LastToolName: lastToolName,
	}

	m.runtime.Interrupt(execID)
	m.running = false
	m.thinking = false

	card := m.formatCheckpointCard(m.pausedCheckpoint, lastToolCmd)
	m.addSystemEntry(card)
	m.refreshViewportContent()
	m.updateLayout()
	return m, nil
}

// formatCheckpointCard constructs a markdown status block summarizing an interrupted task checkpoint.
func (m *tuiModel) formatCheckpointCard(cp *PausedCheckpoint, lastToolCmd string) string {
	if cp == nil {
		return "Interrupted."
	}
	var lines []string
	lines = append(lines, "⏸ **Task Execution Paused (Checkpoint Saved)**")
	if cp.ExecutionID != "" {
		lines = append(lines, fmt.Sprintf("• **Execution ID**: `%s`", cp.ExecutionID))
	}
	if cp.ToolCount > 0 {
		toolDesc := fmt.Sprintf("%d tool step(s) completed", cp.ToolCount)
		if cp.LastToolName != "" {
			toolDesc += fmt.Sprintf(" (last: `%s`)", cp.LastToolName)
		}
		lines = append(lines, "• **Progress**: "+toolDesc)
		if lastToolCmd != "" && lastToolCmd != cp.LastToolName {
			cmdPreview := lastToolCmd
			if len(cmdPreview) > 80 {
				cmdPreview = cmdPreview[:77] + "..."
			}
			lines = append(lines, fmt.Sprintf("• **Last Command**: `%s`", cmdPreview))
		}
	} else {
		lines = append(lines, "• **Progress**: Interrupted during model output generation")
	}
	lines = append(lines, "• **Actions**:")
	lines = append(lines, "  - Type `/resume` to continue execution from this checkpoint")
	lines = append(lines, "  - Type `/discard` to abandon this execution checkpoint")
	return strings.Join(lines, "\n")
}

// archivePlan persists the raw markdown content of an approved plan to .luneagent/plans/.
func (m *tuiModel) archivePlan(planText string) string {
	if m.workspace == "" || planText == "" {
		return ""
	}
	plansDir := filepath.Join(m.workspace, ".luneagent", "plans")
	if err := os.MkdirAll(plansDir, 0755); err != nil {
		return ""
	}
	filename := fmt.Sprintf("plan-%s.md", time.Now().Format("20060102-150405"))
	targetPath := filepath.Join(plansDir, filename)
	if err := os.WriteFile(targetPath, []byte(planText), 0644); err != nil {
		return ""
	}
	return targetPath
}

// toggleReasoningExpansion toggles the expanded/collapsed state of the latest reasoning block.
func (m *tuiModel) toggleReasoningExpansion() (tea.Model, tea.Cmd) {
	if m.currentReasoningText != "" {
		m.reasoningLiveCollapsed = !m.reasoningLiveCollapsed
		m.refreshViewportContent()
		return m, nil
	}

	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindReasoning {
			m.entries[i].ReasoningExpanded = !m.entries[i].ReasoningExpanded
			if m.entries[i].ReasoningExpanded {
				start := i
				for start > 0 && isRoundMember(m.entries, start-1) {
					start--
				}
				m.entries[start].RoundExpanded = true
			}
			m.invalidateEntryCache()
			m.refreshViewportContent()
			return m, nil
		}
	}
	m.addSystemEntry("No thinking/reasoning blocks found in this session.")
	return m, nil
}
