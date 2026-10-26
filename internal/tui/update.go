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
	impl              core.PlanImplementation
	planText          string
	sessionID         string
	bindingGeneration uint64
	err               error
}

// planArchivedMsg reports the archived plan path produced by planArchiveCmd.
type planArchivedMsg struct {
	path string
	err  error
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
	case goalActionMsg:
		return m.finishGoalAction(msg)
	case exportResultMsg:
		return m.handleExportResult(msg)
	case planArchivedMsg:
		return m.handlePlanArchived(msg)
	case fileMentionResultsMsg:
		return m.applyFileMentionResults(msg)
	case diffLoadedMsg:
		return m.handleDiffLoaded(msg)
	case reviewMaterialsMsg:
		return m.handleReviewMaterials(msg)
	case tea.BackgroundColorMsg:
		UpdateTerminalBackground(msg.IsDark())
		m.applyThemeStyles()
		m.view.invalidateBlocks()
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
		if m.activeCardKind() == cardQuestion && m.activeQuestion.Submitting {
			return m, nil
		}
		if m.activeCardKind() == cardQuestion && len(m.activeQuestion.Request.Questions) > 0 && m.input.composer.Value() == "" {
			draft := m.activeQuestion.currentDraft()
			currIdx := min(m.activeQuestion.CurrentIndex, len(m.activeQuestion.Request.Questions)-1)
			currQ := m.activeQuestion.Request.Questions[currIdx]
			text := cleanToolInline(msg.Content)
			if draft.NotesOpen {
				if text != "" {
					draft.NoteText += text
					saveCmd := m.questionDraftChanged(m.activeQuestion)
					m.updateLayout()
					return m, saveCmd
				}
				m.updateLayout()
				return m, nil
			}
			if draft.Selected >= len(currQ.Options) {
				if text != "" {
					draft.Custom += text
					saveCmd := m.questionDraftChanged(m.activeQuestion)
					m.updateLayout()
					return m, saveCmd
				}
				m.updateLayout()
				return m, nil
			}
		}
		m.input.composer.InsertString(msg.Content)
		m.updateLayout()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateLayout()
		return m, nil

	case streamRenderMsg:
		if msg.generation != m.run.streamRenderGeneration || !m.run.streamRenderPending {
			return m, nil
		}
		m.run.streamRenderPending = false
		m.run.lastStreamRenderAt = time.Now()
		m.view.invalidateLive()
		m.updateLayout()
		return m, nil

	case tickMsg:
		if m.run.running || (m.activeRecovery != nil && !m.activeRecovery.RetryNotBefore.IsZero()) {
			m.view.invalidateLive()
			if m.input.composer.Placeholder != "" {
				m.updateLayout()
			}
			return m, tickClock()
		}

	case runtimeStreamReadyMsg:
		if msg.sessionID != "" && msg.sessionID != m.sessionID {
			return m, nil
		}
		if !m.matchesSubscription(msg.generation) {
			return m, nil
		}
		if msg.err != nil {
			if m.run.restoringSession {
				m.finishSessionRestore()
			}
			m.addSystemEntry("Failed to connect to runtime event stream: " + msg.err.Error())
			return m, m.reconnectRuntimeEventStream()
		}
		m.run.streamReconnects = 0
		cmds := []tea.Cmd{nextRuntimeEvent(m.sessionID, msg.generation, msg.events)}
		return m, tea.Batch(cmds...)

	case runtimeReconnectMsg:
		if msg.sessionID != "" && msg.sessionID != m.sessionID {
			return m, nil
		}
		if msg.generation != 0 && !m.matchesSubscription(msg.generation) {
			return m, nil
		}
		return m, m.startRuntimeEventSubscription(true)

	case runtimeEventMsg:
		if msg.sessionID != "" && msg.sessionID != m.sessionID {
			return m, nil
		}
		if !m.matchesSubscription(msg.generation) {
			// A replaced subscription closed its channel; only the live one may
			// trigger a reconnect.
			return m, nil
		}
		if msg.event.Kind == "" && msg.events == nil {
			if m.run.restoringSession {
				m.finishSessionRestore()
			}
			return m, m.reconnectRuntimeEventStream()
		}
		var mod tea.Model
		var cmd tea.Cmd
		mod, cmd = m.processRuntimeEventNoRefresh(msg.event)
		m = mod.(*tuiModel)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.advanceEventCursor(msg.event)
		for _, batchEv := range msg.batch {
			mod, cmd = m.processRuntimeEventNoRefresh(batchEv)
			m = mod.(*tuiModel)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			m.advanceEventCursor(batchEv)
		}
		if !m.run.restoringSession && (m.view.transcriptDirty || m.view.cachedDirty || m.view.liveDirty) {
			m.updateLayout()
		}
		cmds = append(cmds, nextRuntimeEvent(m.sessionID, msg.generation, msg.events))
		return m, tea.Batch(cmds...)

	case runtimeRestoreSettledMsg:
		if m.run.restoringSession && msg.generation == m.run.restoreGeneration {
			m.finishSessionRestore()
		}
		return m, nil

	case turnSubmittedMsg:
		if (msg.sessionID != "" && msg.sessionID != m.sessionID) || !m.matchesBinding(msg.bindingGeneration) {
			return m, nil
		}
		m.pausedCheckpoint = nil
		if msg.err != nil {
			if len(m.run.queuedTurns) == 0 {
				m.run.running = false
			}
			m.addSystemEntry("Failed to submit task: " + msg.err.Error())
			m.updateLayout()
			return m, nil
		}
		if msg.submission.Status == core.SubmissionQueued {
			m.run.queuedTurns = append(m.run.queuedTurns, QueuedItem{
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
		m.run.running = true
		m.run.runningExecution = msg.submission.ExecutionID
		m.roundsCollapsedByUser = false
		m.finishSessionRestore()
		m.refreshViewportContent()
		cmds = append(cmds, tickClock(), m.waitForExecution(msg.submission.ExecutionID))
		return m, tea.Batch(cmds...)

	case turnFinishedMsg:
		if (msg.sessionID != "" && msg.sessionID != m.sessionID) || !m.matchesBinding(msg.bindingGeneration) {
			return m, nil
		}
		return m, m.finishTurn(msg)

	case questionSubmittedMsg:
		if !m.matchesBinding(msg.bindingGeneration) {
			return m, nil
		}
		return m, m.finishQuestionSubmission(msg)

	case questionDraftSaveDueMsg:
		return m, m.handleQuestionDraftSaveDue(msg)

	case questionDraftSavedMsg:
		return m, m.finishQuestionDraftSave(msg)

	case approvalResolvedMsg:
		return m, m.finishApprovalResolution(msg)

	case compactDoneMsg:
		if (msg.sessionID != "" && msg.sessionID != m.sessionID) || !m.matchesBinding(msg.bindingGeneration) {
			return m, nil
		}
		m.run.compactInFlight = false
		m.run.compactionQuitRequested = false
		m.usage.resetCompaction()
		switch {
		case msg.err != nil:
			m.addSystemEntry("Compaction failed: " + msg.err.Error())
		case msg.result.Compacted && msg.result.Turns > 0:
			m.usage.applyCompactResult(msg.result.After)
			m.addSystemEntry(formatManualCompactionResult(msg.result))
		default:
			m.addSystemEntry("No older conversation to compact.")
		}
		m.updateLayout()
		return m, nil

	case mcpAuthDoneMsg:
		if msg.generation != m.modals.mcp.authGeneration {
			return m, nil
		}
		if m.modals.mcp.authCancel != nil {
			m.modals.mcp.authCancel()
			m.modals.mcp.authCancel = nil
		}
		m.modals.mcp.authName = ""
		if msg.err != nil {
			m.addSystemEntry(fmt.Sprintf("Failed to authenticate MCP server `%s`: %v", msg.name, msg.err))
		} else {
			m.addSystemEntry(fmt.Sprintf("MCP server `%s` authorized successfully.", msg.name))
		}
		m.updateLayout()
		return m, nil

	case sessionForkedMsg:
		if (msg.sessionID != "" && msg.sessionID != m.sessionID) ||
			!m.matchesBinding(msg.bindingGeneration) {
			return m, nil
		}
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
		if (msg.sessionID != "" && msg.sessionID != m.sessionID) ||
			!m.matchesBinding(msg.bindingGeneration) {
			return m, nil
		}
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
		m.addSystemEntry(message)
		if archive := planArchiveCmd(m.workspace, msg.planText); archive != nil {
			planCmds = append(planCmds, archive)
		}
		m.run.running = true
		m.run.taskStartedAt = time.Now()
		m.run.runningExecution = msg.impl.Submission.ExecutionID
		planCmds = append(planCmds, tickClock(), m.waitForExecution(msg.impl.Submission.ExecutionID))
		return m, tea.Batch(planCmds...)

	case taskDiscardedMsg:
		if !m.matchesBinding(msg.bindingGeneration) {
			return m, nil
		}
		m.run.discardInFlight = false
		if msg.err != nil {
			m.addSystemEntry("Discard failed: " + msg.err.Error())
			m.updateLayout()
			return m, nil
		}
		if m.activeQuestion != nil {
			delete(m.run.questionSubmissions, m.questionKey(m.activeQuestion.Request))
		}
		m.pausedCheckpoint = nil
		m.activeRecovery = nil
		m.activeToolRecovery = nil
		m.activeQuestion = nil
		m.run.running = false
		m.run.compactInFlight = false
		m.run.compactionQuitRequested = false
		m.usage.resetCompaction()
		if !msg.discarded {
			m.addSystemEntry("No interrupted task to discard.")
		} else if msg.source == discardSourceModelRecovery {
			m.addSystemEntry("Model recovery discarded.")
		} else {
			m.addSystemEntry("Paused task discarded.")
		}
		m.updateLayout()
		return m, nil

	case queuedTurnCancelledMsg:
		if !m.matchesBinding(msg.bindingGeneration) {
			return m, nil
		}
		m.settleQueuedTurnCancellation(msg)
		return m, nil

	case clearCopyFeedbackMsg:
		if msg.at.Equal(m.input.copyFeedbackExpiresAt) && !time.Now().Before(msg.at) {
			m.input.copyFeedback = ""
			m.input.copyFeedbackError = false
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)

	case tea.MouseWheelMsg:
		var vpCmd tea.Cmd
		m.viewport, vpCmd = m.viewport.Update(msg)
		m.scrolledUp = !m.viewport.AtBottom()
		m.view.invalidateLive()
		if m.input.selecting {
			headerHeight := 3
			totalLines := m.viewport.TotalLineCount()
			m.input.selectEndDocRow = clamp(m.viewport.YOffset()+(m.input.selectEndY-headerHeight), 0, max(0, totalLines-1))
			if m.viewport.AtBottom() && msg.Button == tea.MouseWheelDown && totalLines > 0 {
				m.input.selectEndDocRow = totalLines - 1
				m.input.selectEndX = m.viewport.Width()
			}
		}
		if m.layoutReady {
			m.updateLayout()
		}
		return m, vpCmd

	case tea.MouseClickMsg:
		if m.modals.shortcuts {
			m.modals.shortcuts = false
			m.updateLayout()
			return m, nil
		}
		if msg.Button == tea.MouseLeft && m.isComposerMousePoint(msg.X, msg.Y) {
			m.clearTranscriptSelection()
			m.input.composerSelecting = true
			m.input.composer.Focus()
			x, y := m.composerMousePosition(msg.X, msg.Y)
			m.input.composer.BeginSelection(x, y)
			return m, nil
		}
		m.input.composerSelecting = false
		m.input.composer.ClearSelection()
		headerHeight := 3
		vpEnd := headerHeight + m.viewport.Height()
		if msg.Button == tea.MouseLeft {
			if msg.Y >= headerHeight && msg.Y < vpEnd {
				m.input.selecting = true
				m.input.selectStartX = msg.X
				m.input.selectStartY = msg.Y
				m.input.selectEndX = msg.X
				m.input.selectEndY = msg.Y
				m.input.selectStartDocRow = m.viewport.YOffset() + (msg.Y - headerHeight)
				m.input.selectEndDocRow = m.input.selectStartDocRow
				m.input.selectedText = ""
				return m, nil
			}
			if m.scrolledUp {
				m.viewport.GotoBottom()
				m.scrolledUp = false
				m.clearTranscriptSelection()
				return m, nil
			}
			m.clearTranscriptSelection()
		}

	case tea.MouseMotionMsg:
		if m.input.composerSelecting {
			x, y := m.composerMousePosition(msg.X, msg.Y)
			m.input.composer.ExtendSelection(x, y)
			return m, nil
		}
		if m.input.selecting {
			headerHeight := 3
			vpEnd := headerHeight + m.viewport.Height()
			m.input.selectEndX = msg.X
			m.input.selectEndY = msg.Y
			if msg.Y >= vpEnd {
				m.viewport.ScrollDown(2)
				m.scrolledUp = !m.viewport.AtBottom()
			} else if msg.Y < headerHeight {
				m.viewport.ScrollUp(2)
				m.scrolledUp = !m.viewport.AtBottom()
			}
			m.view.invalidateLive()
			if m.layoutReady {
				m.updateLayout()
			}
			totalLines := m.viewport.TotalLineCount()
			m.input.selectEndDocRow = clamp(m.viewport.YOffset()+(m.input.selectEndY-headerHeight), 0, max(0, totalLines-1))
			return m, nil
		}

	case tea.MouseReleaseMsg:
		if m.input.composerSelecting {
			m.input.composerSelecting = false
			m.input.composer.EndSelection()
			return m, nil
		}
		if m.input.selecting {
			headerHeight := 3
			totalLines := m.viewport.TotalLineCount()
			m.input.selectEndX = msg.X
			m.input.selectEndY = msg.Y
			m.input.selectEndDocRow = clamp(m.viewport.YOffset()+(msg.Y-headerHeight), 0, max(0, totalLines-1))
			text := m.extractSelectedText()
			m.input.selecting = false
			if strings.TrimSpace(text) != "" {
				m.input.selectedText = text
				return m, m.copyTextToClipboard(text)
			}
			if msg.X == m.input.selectStartX && m.input.selectStartDocRow == m.input.selectEndDocRow {
				headerHeight := 3
				vpRow := msg.Y - headerHeight
				absLine := m.viewport.YOffset() + vpRow
				toggled := false
				for _, rng := range m.view.entryLineRanges {
					if absLine >= rng.StartLine && absLine <= rng.EndLine && rng.EntryIndex >= 0 && rng.EntryIndex < len(m.entries) {
						if rng.IsRoundHeader || rng.IsRoundFooter {
							m.toggleRoundExpansion(rng.EntryIndex)
							toggled = true
							break
						}
						switch m.entries[rng.EntryIndex].Kind {
						case KindTool:
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
					m.view.invalidateBlocks()
					m.refreshViewportContent()
				}
			}
			m.clearTranscriptSelection()
			return m, nil
		}
	}

	// Default composer update
	var compCmd tea.Cmd
	m.input.composer, compCmd = m.input.composer.Update(msg)
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
		executionID = m.run.runningExecution
	}
	if executionID == "" {
		return nil
	}
	if msg.generation != 0 && m.run.awaitingTurns[executionID] != msg.generation {
		return nil
	}
	if msg.result.Status == core.TaskPaused && m.stalePause(executionID, "") {
		delete(m.run.awaitingTurns, executionID)
		if m.run.taskSnapshot.ExecutionID == executionID {
			switch m.run.taskSnapshot.Status {
			case core.TaskCompleted, core.TaskFailed, core.TaskCancelled, core.TaskDiscarded:
				m.run.runningExecution = executionID
				return m.awaitExecutionResult(executionID)
			}
		}
		return m.waitForExecution(executionID)
	}
	event, eventFinalized := m.run.terminalEvents[executionID]
	if m.run.runningExecution != executionID && !eventFinalized {
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
	delete(m.run.awaitingTurns, executionID)
	delete(m.run.terminalEvents, executionID)
	if m.run.taskSnapshot.ExecutionID == executionID {
		m.run.taskSnapshot.Status = msg.result.Status
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
	if m.run.runningExecution == executionID {
		m.run.running = false
		m.run.thinking = false
		m.run.taskActivity = ""
		if msg.result.Status == core.TaskPaused {
			if m.pausedCheckpoint == nil {
				m.pausedCheckpoint = m.pausedCheckpointFor(executionID)
			}
			m.checkPendingState(context.Background())
		} else {
			m.run.runningExecution = ""
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
	if executionID == "" || executionID != m.run.runningExecution {
		return
	}
	if m.staleTaskAttempt(executionID, event.TaskID) ||
		(event.Status == core.TaskPaused && m.stalePause(executionID, event.TaskID)) {
		return
	}
	if event.Status == core.TaskPaused && !m.run.running {
		if _, awaiting := m.run.awaitingTurns[executionID]; !awaiting {
			return
		}
	}
	if _, finalized := m.run.terminalEvents[executionID]; finalized {
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
	if main.Duration <= 0 && !m.run.taskStartedAt.IsZero() {
		main.Duration = time.Since(m.run.taskStartedAt)
	}
	m.noteTurnFinished(executionID, event.Status, main.Duration)
	m.run.terminalEvents[executionID] = event
	m.run.taskSnapshot.Status = event.Status
	m.run.thinking = false
	m.run.taskActivity = ""
	m.usage.resetCompaction()
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
	m.view.invalidateLive()
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
