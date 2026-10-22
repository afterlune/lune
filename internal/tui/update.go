package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
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
func (m *tuiModel) Update(msg tea.Msg) (updated tea.Model, cmd tea.Cmd) {
	defer func() {
		if current, ok := updated.(*tuiModel); ok {
			current.syncTerminalTitle()
			current.syncAttention()
			if !current.scrolledUp {
				current.unseenCompletion = ""
			}
		}
	}()

	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.BackgroundColorMsg:
		UpdateTerminalBackground(msg.IsDark())
		m.applyThemeStyles()
		m.invalidateEntryCache()
		m.refreshViewportContent()
		m.updateLayout()
		return m, nil

	case tea.ColorProfileMsg:
		commands := terminalColorProfileProbeCommands(msg.Profile, os.Getenv("TERM_PROGRAM"))
		if len(commands) == 0 {
			return m, nil
		}
		return m, tea.Batch(commands...)

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
		cmds := []tea.Cmd{nextRuntimeEvent(m.sessionID, msg.events)}
		if m.restoringSession {
			// Sessions without durable events must settle too.
			generation := m.restoreGeneration
			cmds = append(cmds, tea.Tick(sessionRestoreIdle, func(time.Time) tea.Msg {
				return runtimeRestoreSettledMsg{generation: generation}
			}))
		}
		return m, tea.Batch(cmds...)

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
		if m.restoringSession {
			// Defer the transcript rebuild so replaying a long session renders once.
			generation := m.restoreGeneration
			cmds = append(cmds, tea.Tick(sessionRestoreIdle, func(time.Time) tea.Msg {
				return runtimeRestoreSettledMsg{generation: generation}
			}))
		} else if m.transcriptDirty || m.cachedDirty {
			m.refreshViewportContent()
		}
		cmds = append(cmds, nextRuntimeEvent(m.sessionID, msg.events))
		return m, tea.Batch(cmds...)

	case runtimeRestoreSettledMsg:
		if m.restoringSession && msg.generation == m.restoreGeneration {
			m.finishSessionRestore()
		}
		return m, nil

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
		m.roundsCollapsedByUser = false
		m.finishSessionRestore()
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
		if msg.sessionID != "" && msg.sessionID != m.sessionID {
			return m, nil
		}
		m.compacting = false
		switch {
		case msg.err != nil:
			m.addSystemEntry("Compaction failed: " + msg.err.Error())
		case msg.result.Compacted && msg.result.Turns > 0:
			m.contextCompacted = false
			m.compactedFromTokens = 0
			m.lastInputTokens = 0
			m.lastCachedTokens = 0
			m.estimatedInputTokens = msg.result.After
			m.addSystemEntry(fmt.Sprintf("Compacted %d older turns, %s → %s tokens.",
				msg.result.Turns, formatCompactTokens(msg.result.Before), formatCompactTokens(msg.result.After)))
		default:
			m.addSystemEntry("No older conversation to compact.")
		}
		m.updateLayout()
		return m, nil

	case mcpAuthDoneMsg:
		if msg.generation != m.mcpAuthGeneration {
			return m, nil
		}
		if m.mcpAuthCancel != nil {
			m.mcpAuthCancel()
			m.mcpAuthCancel = nil
		}
		m.mcpAuthName = ""
		if msg.err != nil {
			m.addSystemEntry(fmt.Sprintf("Failed to authenticate MCP server `%s`: %v", msg.name, msg.err))
		} else {
			m.addSystemEntry(fmt.Sprintf("MCP server `%s` authorized successfully.", msg.name))
		}
		m.updateLayout()
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
		m.discardInFlight = false
		if msg.err != nil {
			m.addSystemEntry("Discard failed: " + msg.err.Error())
			m.updateLayout()
			return m, nil
		}
		if m.activeQuestion != nil {
			delete(m.questionSubmissions, m.questionKey(m.activeQuestion.Request))
		}
		m.pausedCheckpoint = nil
		m.activeRecovery = nil
		m.activeToolRecovery = nil
		m.activeQuestion = nil
		m.running = false
		if !msg.discarded {
			if msg.source == discardSourceQuestion {
				m.addSystemEntry("The question was already resolved; no paused task remained to discard.")
			} else {
				m.addSystemEntry("No interrupted task to discard.")
			}
		} else {
			switch msg.source {
			case discardSourceRecovery:
				m.addSystemEntry("Model recovery discarded.")
			case discardSourceQuestion:
				m.addSystemEntry("Question cancelled.")
			default:
				m.addSystemEntry("Discarded paused task.")
			}
		}
		m.updateLayout()
		return m, nil

	case queuedTurnCancelledMsg:
		m.settleQueuedTurnCancellation(msg)
		return m, nil

	case clearCopyFeedbackMsg:
		if msg.at.Equal(m.copyFeedbackExpiresAt) && !time.Now().Before(msg.at) {
			m.copyFeedback = ""
			m.copyFeedbackError = false
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
		if msg.Button == tea.MouseLeft && m.isComposerMousePoint(msg.X, msg.Y) {
			m.isSelecting = false
			m.selectedText = ""
			m.composerMouseSelecting = true
			m.composer.Focus()
			x, y := m.composerMousePosition(msg.X, msg.Y)
			m.composer.BeginSelection(x, y)
			return m, nil
		}
		m.composerMouseSelecting = false
		m.composer.ClearSelection()
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
		if m.composerMouseSelecting {
			x, y := m.composerMousePosition(msg.X, msg.Y)
			m.composer.ExtendSelection(x, y)
			return m, nil
		}
		if m.isSelecting {
			m.selectEndX = msg.X
			m.selectEndY = msg.Y
			return m, nil
		}

	case tea.MouseReleaseMsg:
		if m.composerMouseSelecting {
			m.composerMouseSelecting = false
			m.composer.EndSelection()
			return m, nil
		}
		if m.isSelecting {
			m.selectEndX = msg.X
			m.selectEndY = msg.Y
			text := m.extractSelectedText()
			m.isSelecting = false
			if strings.TrimSpace(text) != "" {
				m.selectedText = text
				return m, m.copyTextToClipboard(text)
			}
			if msg.X == m.selectStartX && msg.Y == m.selectStartY {
				headerHeight := 3
				vpRow := msg.Y - headerHeight
				absLine := m.viewport.YOffset() + vpRow
				toggled := false
				for _, rng := range m.entryLineRanges {
					if absLine >= rng.StartLine && absLine <= rng.EndLine && rng.EntryIndex >= 0 && rng.EntryIndex < len(m.entries) {
						if rng.IsRoundHeader || rng.IsRoundFooter {
							m.toggleRoundExpansion(rng.EntryIndex)
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
						if strings.Contains(lineText, "Ran") || strings.Contains(lineText, "Explored") || strings.Contains(lineText, "Failed") || strings.Contains(lineText, "Running") || strings.Contains(lineText, "expand") || strings.Contains(lineText, "collapse") || strings.Contains(lineText, "Show less") {
							for i := len(m.entries) - 1; i >= 0; i-- {
								if isRoundMember(m.entries, i) {
									m.toggleRoundExpansion(roundStartIndex(m.entries, i))
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
	if eventFinalized {
		if msg.err != nil {
			msg.result = core.TurnResult{ExecutionID: executionID, Status: event.Status, BudgetReason: event.BudgetReason}
		}
		if event.Err != "" {
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
		} else {
			m.addSystemEntry("Task failed: model request completed without a response. Check .luneagent/agent.log for details.")
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
	if main.Duration <= 0 && !m.taskStartedAt.IsZero() {
		main.Duration = time.Since(m.taskStartedAt)
	}
	m.noteTurnFinished(executionID, event.Status, main.Duration)
	m.terminalEvents[executionID] = event
	m.taskSnapshot.Status = event.Status
	m.thinking = false
	m.compacting = false
	// Keep the live transcript until WaitTurn supplies the authoritative response
	// and ProposedPlan; finalizing here would briefly erase a completed plan.
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
