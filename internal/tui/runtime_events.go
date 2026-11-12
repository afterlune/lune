package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
)

// handleRuntimeEvent processes incoming runtime events from Core.
func (m *tuiModel) handleRuntimeEvent(event core.Event) (tea.Model, tea.Cmd) {
	mod, cmd := m.processRuntimeEventNoRefresh(event)
	m = mod.(*tuiModel)
	if m.view.transcriptDirty || m.view.cachedDirty || m.trailingRoundViewChanged() {
		m.refreshViewportContent()
	} else if m.view.liveDirty {
		m.refreshViewportContent()
	}
	return m, cmd
}

// replaySafeDuringRestore reports events that only enrich the transcript while a
// freshly loaded session replays its durable events. Task state, usage and
// interaction cards come from the snapshot and Core state instead.
func replaySafeDuringRestore(kind core.EventKind) bool {
	switch kind {
	case core.EventToolStarted, core.EventToolDetails, core.EventToolFinished,
		core.EventAgentStarted, core.EventAgentFinished, core.EventRuntimeStreamFailed:
		return true
	}
	return false
}

func (m *tuiModel) queuedItem(executionID string) *QueuedItem {
	for i := range m.run.queuedTurns {
		if m.run.queuedTurns[i].ExecutionID == executionID {
			return &m.run.queuedTurns[i]
		}
	}
	return nil
}

func (m *tuiModel) promoteInjectedInput(event core.Event) bool {
	entryIndex := -1
	for i := range m.entries {
		entry := &m.entries[i]
		if entry.Kind == KindQueued && entry.ToolCallID == event.InputExecutionID {
			entryIndex = i
			break
		}
	}
	if entryIndex < 0 {
		return false
	}
	if m.run.running && m.run.runningExecution == event.ExecutionID {
		m.finalizeSteeredSegment()
	}
	entry := m.entries[entryIndex]
	entry.Kind = KindUser
	entry.ExecutionID = event.ExecutionID
	entry.ToolCallID = event.InputExecutionID
	m.entries = append(m.entries[:entryIndex], m.entries[entryIndex+1:]...)
	insertionIndex := len(m.entries)
	if !m.run.running || m.run.runningExecution != event.ExecutionID {
		finalOutputID := assistantOutputIDForSegment(event.ExecutionID, m.run.streamSegment)
		finalPlanID := planOutputIDForSegment(event.ExecutionID, m.run.streamSegment)
		for i := range m.entries {
			if m.entries[i].ExecutionID == event.ExecutionID &&
				(m.entries[i].ID == finalOutputID || m.entries[i].ID == finalOutputID+"-after-plan" || m.entries[i].ID == finalPlanID) {
				insertionIndex = i
				break
			}
		}
	}
	m.entries = append(m.entries, TranscriptEntry{})
	copy(m.entries[insertionIndex+1:], m.entries[insertionIndex:])
	m.entries[insertionIndex] = entry
	for i := range m.run.queuedTurns {
		if m.run.queuedTurns[i].ExecutionID == event.InputExecutionID || event.QueueID != "" && m.run.queuedTurns[i].QueueID == event.QueueID {
			m.run.queuedTurns = append(m.run.queuedTurns[:i], m.run.queuedTurns[i+1:]...)
			break
		}
	}
	m.activePlanReview = nil
	m.view.invalidateBlocks()
	return true
}

func (m *tuiModel) processRuntimeEventNoRefresh(event core.Event) (tea.Model, tea.Cmd) {
	if m.run.restoringSession {
		// Recorded before any early return so a replay that keeps delivering never
		// trips the stall watchdog.
		m.run.noteRestoreProgress()
	}
	if event.Kind == core.EventRuntimeReplayComplete {
		if m.run.restoringSession {
			m.finishSessionRestore()
		}
		return m, nil
	}
	if m.run.restoringSession && event.Cursor == 0 {
		// Live-only events follow the replay marker in Core; keep this as a
		// defensive boundary for event sources that only expose live events.
		m.finishSessionRestore()
	}
	if m.run.restoringSession && event.Kind == core.EventRuntimeStreamFailed {
		m.finishSessionRestore()
	}
	if m.run.restoringSession && !replaySafeDuringRestore(event.Kind) {
		return m, nil
	}
	switch event.Kind {
	case core.EventToolStarted, core.EventToolDetails, core.EventToolFinished:
		if (event.AgentID == "" || event.AgentID == "lead") && m.run.running && !m.run.restoringSession {
			m.view.animateTools = true
		}
	case core.EventTaskFinished, core.EventTaskDiscarded, core.EventUserQuestionRequired,
		core.EventUserInteractionRequired, core.EventApprovalRequired, core.EventModelRecoveryRequired,
		core.EventToolRecoveryRequired, core.EventExecutionBlocked:
		m.finishToolReveal()
	case core.EventModelStream:
		if event.Stream != nil && event.Stream.Delta != "" && (event.AgentID == "" || event.AgentID == "lead") && (event.Stream.Type == "text_delta" || event.Stream.Type == "") {
			purpose := strings.TrimSpace(event.Stream.Purpose)
			if purpose == "" || purpose == string(core.UsagePurposeMain) || purpose == string(core.UsagePurposeTeam) {
				m.finishToolReveal()
			}
		}
	}
	switch event.Kind {
	case core.EventTaskQueued:
		if event.ExecutionID == "" {
			return m, nil
		}
		if queued := m.queuedItem(event.ExecutionID); queued != nil {
			queued.QueueID = event.QueueID
			queued.ParentExecutionID = event.ParentExecutionID
		} else {
			m.run.queuedTurns = append(m.run.queuedTurns, QueuedItem{
				ExecutionID: event.ExecutionID, QueueID: event.QueueID,
				ParentExecutionID: event.ParentExecutionID,
			})
		}
		for i := range m.entries {
			if m.entries[i].Kind == KindQueued && m.entries[i].ToolCallID == "" {
				m.entries[i].ToolCallID = event.ExecutionID
				break
			}
		}
		m.view.invalidateLive()
		return m, nil
	case core.EventTaskInputInjected:
		if event.InputExecutionID == "" {
			return m, nil
		}
		if !m.promoteInjectedInput(event) {
			if m.run.pendingInjected == nil {
				m.run.pendingInjected = make(map[string]core.Event)
			}
			m.run.pendingInjected[event.InputExecutionID] = event
			m.view.invalidateLive()
		}
		return m, nil
	case core.EventGoalUpdated, core.EventGoalCleared:
		m.refreshGoal()
		m.view.invalidateLive()
		m.updateLayout()
		return m, nil
	case core.EventTaskStarted:
		execID := event.ExecutionID
		if execID == "" {
			return m, nil
		}
		queuedIndex := -1
		for i := range m.run.queuedTurns {
			if m.run.queuedTurns[i].ExecutionID == execID {
				queuedIndex = i
				break
			}
		}
		queued := queuedIndex >= 0
		goalTurn := event.GoalID != "" && m.runtime != nil
		if queued {
			m.run.queuedTurns = append(m.run.queuedTurns[:queuedIndex], m.run.queuedTurns[queuedIndex+1:]...)
			m.roundsCollapsedByUser = false
			for i := range m.entries {
				if m.entries[i].Kind == KindQueued && m.entries[i].ToolCallID == execID {
					m.entries[i].Kind = KindUser
					m.entries[i].ExecutionID = execID
					m.entries[i].ToolCallID = execID
					m.view.invalidateEntry(i)
					break
				}
			}
			m.run.resetStream()
			m.run.resetReasoning()
			m.run.streamSegment = 0
			m.view.invalidateLive()
		}
		if !queued && !goalTurn && ((!m.run.running && m.run.runningExecution != execID) || (m.run.runningExecution != "" && m.run.runningExecution != execID)) {
			return m, nil
		}
		if m.run.runningExecution == execID && m.run.taskSnapshot.TaskID == event.TaskID && m.run.running {
			return m, m.waitForExecution(execID)
		}
		limits := core.ExecutionOptions{}
		if m.client != nil {
			limits = m.client.ExecutionDefaults()
		}
		nextTask := core.TaskState{
			TaskID: event.TaskID, ExecutionID: execID, Status: core.TaskRunning, Execution: limits,
			GoalID: event.GoalID, GoalRevision: event.GoalRevision, AutomaticGoal: event.AutomaticGoal,
		}
		if m.runtime != nil {
			if state, err := m.runtime.State(context.Background()); err == nil && state.Task.ExecutionID == execID {
				nextTask = state.Task
				if state.Task.TaskID != event.TaskID || state.Task.Status != core.TaskRunning {
					m.restoreRuntimeSnapshot(state)
					m.checkPendingState(context.Background())
					switch state.Task.Status {
					case core.TaskCompleted, core.TaskFailed, core.TaskCancelled, core.TaskDiscarded:
						m.run.runningExecution = execID
						return m, m.awaitExecutionResult(execID)
					}
					return m, m.waitForExecution(m.run.runningExecution)
				}
			}
		}
		if m.run.taskSnapshot.TaskID != "" && m.run.taskSnapshot.TaskID != event.TaskID {
			delete(m.run.awaitingTurns, execID)

		}
		m.run.taskSnapshot = nextTask
		m.run.running = true
		m.run.runningExecution = execID
		m.run.taskActivity = ""
		m.run.taskStartedAt = time.Now()
		main := m.ensureAgentMetrics("lead")
		main.Status = "Running"
		main.Activity = "Starting"
		main.StartedAt = m.run.taskStartedAt
		main.Duration = 0
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].Kind == KindUser {
				main.Task = compactAgentTask(m.entries[i].Text)
				break
			}
		}
		if event.ContextWindow > 0 {
			main.ContextWindow = event.ContextWindow
			m.usage.setContextWindow(event.ContextWindow)
		}
		if event.ModelProfile != "" {
			main.ModelProfile = event.ModelProfile
			m.currentModelProfile = event.ModelProfile
		}
		if event.Model != "" {
			main.Model = event.Model
		}
		m.run.thinking = false
		m.view.invalidateLive()
		m.updateLayout()
		return m, m.waitForExecution(execID)

	case core.EventModelStream:
		if event.Stream == nil {
			return m, nil
		}
		// Ignore internal background stream purposes (e.g. memory extraction, compaction summary).
		purpose := strings.TrimSpace(event.Stream.Purpose)
		if purpose != "" && purpose != string(core.UsagePurposeMain) && purpose != string(core.UsagePurposeTeam) {
			return m, nil
		}
		m.recordAgentStream(event)
		// Isolate concurrent subagent streams from the lead assistant stream!
		agentID := strings.TrimSpace(event.AgentID)
		if agentID != "" && agentID != "lead" {
			m.upsertSubagentStream(event)
			return m, nil
		}

		switch event.Stream.Type {
		case "reasoning_delta":
			if m.run.reasoningStartTime.IsZero() {
				m.run.reasoningStartTime = time.Now()
			}
			if m.run.streamFirstTokenTime.IsZero() {
				m.run.streamFirstTokenTime = time.Now()
				m.run.streamGenerationTokens = 0
			}
			m.run.thinking = true
			m.run.appendReasoningDelta(event.Stream.Delta)
			if event.Stream.Delta != "" {
				toks := estimateDeltaTokens(event.Stream.Delta)
				m.run.streamGenerationTokens += toks
				elapsed := time.Since(m.run.streamFirstTokenTime).Seconds()
				if elapsed >= 0.05 {
					m.usage.setSpeed(float64(m.run.streamGenerationTokens) / elapsed)
				}
			}
			m.markStreamDirty()

		case "text_delta", "":
			if m.run.currentReasoningText != "" {
				m.flushReasoningEntry()
			}
			if m.run.streamFirstTokenTime.IsZero() {
				m.run.streamFirstTokenTime = time.Now()
				m.run.streamGenerationTokens = 0
			}
			if event.Stream.Delta != "" {
				m.run.thinking = false
				m.run.appendStreamDelta(event.Stream.Delta)
				toks := estimateDeltaTokens(event.Stream.Delta)
				m.run.streamGenerationTokens += toks
				elapsed := time.Since(m.run.streamFirstTokenTime).Seconds()
				if elapsed >= 0.05 {
					m.usage.setSpeed(float64(m.run.streamGenerationTokens) / elapsed)
				}
				m.markStreamDirty()
			}
		}

	case core.EventAgentStarted:
		m.updateAgentEvent(event, true)
		m.upsertSubagentEntry(event.AgentID, event.AgentName, event.AgentRole, true, 0)

	case core.EventAgentFinished:
		m.settleSubagentStream(event.AgentID)
		m.updateAgentEvent(event, false)
		m.upsertSubagentEntry(event.AgentID, event.AgentName, event.AgentRole, false, 0)

	case core.EventToolStarted:
		if strings.EqualFold(event.Tool.Name, "select_tool_category") {
			return m, nil
		}
		agentID := strings.TrimSpace(event.AgentID)
		if agentID != "" && agentID != "lead" {
			m.recordAgentToolStarted(agentID, event.Tool.Name)
			m.recordSubagentToolStarted(agentID, event.AgentName, event.AgentRole, event.Tool.Name, string(event.Tool.Arguments))
			return m, nil
		}
		m.run.streamFirstTokenTime = time.Time{}
		m.run.streamGenerationTokens = 0
		m.recordAgentToolStarted("lead", event.Tool.Name)
		m.flushReasoningEntry()
		m.flushStreamTextEntry()
		m.run.thinking = false

		rawArgs := string(event.Tool.Arguments)
		cmdText := formatToolSummary(event.Tool.Name, rawArgs, m.workspace)
		cmdOverride := ""
		outText := ""
		var changes []publictool.FileChange
		if event.Tool.Details != nil {
			outText = event.Tool.Details.Output
			changes = event.Tool.Details.Changes
			if event.Tool.Details.Command != nil && strings.TrimSpace(event.Tool.Details.Command.Command.Command) != "" {
				cmdOverride = event.Tool.Details.Command.Command.Command
				cmdText = cmdOverride
				if outText == "" {
					outText = event.Tool.Details.Command.Output
				}
			}
		}
		callID := event.Tool.ID
		// Deduplicate if this tool call is already present in m.entries (e.g., from session history replay).
		if callID != "" {
			for i := len(m.entries) - 1; i >= 0; i-- {
				if m.entries[i].Kind == KindTool && m.entries[i].ToolCallID == callID {
					m.entries[i].ToolName = event.Tool.Name
					if cmdOverride != "" || hasToolSummaryArguments(event.Tool.Name, rawArgs, cmdText) {
						m.entries[i].ToolCommand = cmdText
					}
					if outText != "" {
						m.entries[i].ToolOutput = outText
					}
					if len(changes) > 0 {
						m.entries[i].ToolChanges = changes
					}
					if m.run.running {
						m.entries[i].ToolRunning = true
						if m.entries[i].ToolStartedAt.IsZero() {
							m.entries[i].ToolStartedAt = time.Now()
						}
					}
					m.view.invalidateEntry(i)
					return m, nil
				}
			}
		}
		entry := TranscriptEntry{
			ID:          callID,
			ExecutionID: event.ExecutionID,
			Kind:        KindTool,
			ToolCallID:  callID,
			ToolName:    event.Tool.Name,
			Timestamp:   time.Now(),
			ToolCommand: cmdText,
			ToolRunning: m.run.running,
			ToolChanges: changes,
			ToolOutput:  outText,
		}
		if entry.ToolRunning {
			entry.ToolStartedAt = time.Now()
		}
		m.beginRoundIfNeeded()
		m.entries = append(m.entries, entry)
		m.view.invalidateContent()

	case core.EventToolDetails:
		if strings.EqualFold(event.Tool.Name, "select_tool_category") {
			return m, nil
		}
		agentID := strings.TrimSpace(event.AgentID)
		if agentID != "" && agentID != "lead" {
			return m, nil
		}
		rawArgs := string(event.Tool.Arguments)
		cmdText := formatToolSummary(event.Tool.Name, rawArgs, m.workspace)
		cmdOverride := ""
		outText := ""
		var changes []publictool.FileChange
		if event.Tool.Details != nil {
			outText = event.Tool.Details.Output
			changes = event.Tool.Details.Changes
			if event.Tool.Details.Command != nil {
				cmdOverride = strings.TrimSpace(event.Tool.Details.Command.Command.Command)
				if outText == "" {
					outText = event.Tool.Details.Command.Output
				}
			}
		}
		if cmdOverride != "" {
			cmdText = cmdOverride
		}
		hasSummary := hasToolSummaryArguments(event.Tool.Name, rawArgs, cmdText)
		if !hasSummary && cmdOverride == "" && event.Tool.Details == nil && event.CommandExitCode == nil {
			return m, nil
		}

		targetID := event.Tool.ID
		if targetID == "" {
			targetID = event.CommandID
		}
		found := false
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].Kind != KindTool || !((targetID != "" && m.entries[i].ToolCallID == targetID) || (targetID == "" && m.entries[i].ToolName == event.Tool.Name)) {
				continue
			}
			m.entries[i].ToolName = event.Tool.Name
			if cmdOverride != "" || hasSummary {
				m.entries[i].ToolCommand = cmdText
			}
			if event.Tool.Details != nil {
				if outText != "" {
					m.entries[i].ToolOutput = outText
				}
				if len(changes) > 0 {
					m.entries[i].ToolChanges = changes
				}
			}
			if event.CommandExitCode != nil {
				m.entries[i].ToolExitCode = event.CommandExitCode
			}
			if event.Tool.Started {
				m.entries[i].ToolRunning = true
				if m.entries[i].ToolStartedAt.IsZero() {
					m.entries[i].ToolStartedAt = time.Now()
				}
			} else if event.Tool.Duration > 0 || event.Tool.Details != nil {
				m.entries[i].ToolRunning = false
			}
			if event.Tool.Duration > 0 {
				m.entries[i].ToolDuration = event.Tool.Duration
			}
			m.view.invalidateEntry(i)
			found = true
			break
		}
		if !found {
			entry := TranscriptEntry{
				ID:          targetID,
				ExecutionID: event.ExecutionID,
				Kind:        KindTool,
				ToolCallID:  targetID,
				ToolName:    event.Tool.Name,
				ToolCommand: cmdText,
				ToolOutput:  outText,
				ToolRunning: event.Tool.Started,
				Timestamp:   time.Now(),
			}
			entry.ToolChanges = changes
			entry.ToolExitCode = event.CommandExitCode
			entry.ToolDuration = event.Tool.Duration
			if entry.ToolRunning {
				entry.ToolStartedAt = time.Now()
			}
			m.beginRoundIfNeeded()
			m.entries = append(m.entries, entry)
			m.view.invalidateContent()
		}

	case core.EventToolFinished:
		if strings.EqualFold(event.Tool.Name, "select_tool_category") {
			return m, nil
		}
		agentID := strings.TrimSpace(event.AgentID)
		if agentID != "" && agentID != "lead" {
			errText := event.Err
			if errText == "" && (event.Tool.Failed || event.Tool.Canceled || event.CommandStatus == core.CommandFailed || event.CommandStatus == core.CommandTimedOut) {
				errText = "tool failed"
			}
			m.recordAgentToolFinished(agentID, event.Tool.Name, errText)
			m.recordSubagentToolFinished(agentID, event.Tool.Name, event.Tool.Duration)
			return m, nil
		}
		isFailed := event.Tool.Failed ||
			event.Tool.Canceled ||
			event.CommandStatus == core.CommandFailed ||
			event.CommandStatus == core.CommandTimedOut ||
			(event.CommandExitCode != nil && *event.CommandExitCode != 0)
		errText := event.Err
		if errText == "" && isFailed {
			errText = "tool failed"
		}
		m.recordAgentToolFinished("lead", event.Tool.Name, errText)
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].Kind == KindTool && (m.entries[i].ToolCallID == event.Tool.ID || (event.Tool.ID == "" && m.entries[i].ToolName == event.Tool.Name)) {
				m.entries[i].ToolRunning = false
				m.entries[i].ToolFailed = isFailed
				m.entries[i].ToolStatus = event.CommandStatus
				if event.CommandExitCode != nil {
					m.entries[i].ToolExitCode = event.CommandExitCode
				}
				if event.Tool.Duration > 0 {
					m.entries[i].ToolDuration = event.Tool.Duration
				}
				if event.Tool.Details != nil {
					if event.Tool.Details.Output != "" {
						m.entries[i].ToolOutput = event.Tool.Details.Output
					} else if event.Tool.Details.Command != nil && event.Tool.Details.Command.Output != "" {
						m.entries[i].ToolOutput = event.Tool.Details.Command.Output
					}
					if len(event.Tool.Details.Changes) > 0 {
						m.entries[i].ToolChanges = event.Tool.Details.Changes
					}
				}
				m.view.invalidateEntry(i)
				break
			}
		}

	case core.EventContextCompaction:
		if event.Compaction != nil {
			m.handleCompactionEvent(*event.Compaction)
		}

	case core.EventUserQuestionRequired:
		if m.staleTaskAttempt(event.ExecutionID, event.TaskID) {
			return m, nil
		}
		if event.UserQuestion != nil {
			request := *event.UserQuestion
			if request.ExecutionID == "" {
				request.ExecutionID = event.ExecutionID
			}
			if m.run.questionSubmissions[m.questionKey(request)] != 0 {
				return m, nil
			}
		}
		m.checkPendingState(context.Background())
		if m.activeQuestion != nil && !m.activeQuestion.Submitting {
			m.run.thinking = false
			m.run.running = false
		}
		m.view.invalidateLive()
		m.updateLayout()

	case core.EventUserInteractionRequired:
		m.run.thinking = false
		m.run.running = false
		m.checkPendingState(context.Background())
		m.view.invalidateLive()
		m.updateLayout()

	case core.EventApprovalRequired:
		m.run.thinking = false
		if event.Approval != nil && m.approveWithoutCard(*event.Approval) {
			m.view.invalidateLive()
			m.updateLayout()
			return m, nil
		}
		m.checkPendingState(context.Background())
		m.view.invalidateLive()
		m.updateLayout()

	case core.EventApprovalCancelled:
		if m.activeApproval != nil && m.activeApproval.Request.ID == event.ApprovalID {
			m.activeApproval = nil
			m.updateLayout()
		}

	case core.EventUsage:
		if m.run.running || !m.usage.seededSessionUsage {
			m.usage.addEventUsage(event.Usage)
			if strings.TrimSpace(event.AgentID) != "" || isLeadMainUsage(event.AgentID, event.UsagePurpose) {
				m.recordAgentUsage(event)
			}
			if isLeadMainUsage(event.AgentID, event.UsagePurpose) && event.Usage.OutputTokens > 0 {
				var elapsed float64
				if !m.run.streamFirstTokenTime.IsZero() {
					elapsed = time.Since(m.run.streamFirstTokenTime).Seconds()
				} else if !m.run.turnStreamStartTime.IsZero() {
					elapsed = time.Since(m.run.turnStreamStartTime).Seconds()
				}
				if elapsed >= 0.05 {
					m.usage.setSpeed(float64(event.Usage.OutputTokens) / elapsed)
				}
				m.run.streamFirstTokenTime = time.Time{}
			}
		}
		if isLeadMainUsage(event.AgentID, event.UsagePurpose) {
			m.usage.applyLeadRequest(event.Usage.InputTokens, event.Usage.InputTokensDetails.CachedTokens,
				event.EstimatedInputTokens, event.ContextWindow, event.RequestID, event.RequestAttempt, event.RequestChanges)
		}

	case core.EventModelRecoveryRequired:
		m.run.thinking = false
		m.run.running = false
		m.checkPendingState(context.Background())
		m.view.invalidateLive()
		m.updateLayout()
		if m.activeRecovery != nil && !m.activeRecovery.RetryNotBefore.IsZero() {
			return m, tickClock()
		}

	case core.EventToolRecoveryRequired, core.EventExecutionBlocked:
		m.run.thinking = false
		m.run.running = false
		m.checkPendingState(context.Background())
		m.view.invalidateLive()
		m.updateLayout()

	case core.EventRuntimeStreamFailed:
		m.run.thinking = false
		m.run.running = false
		m.run.taskActivity = ""
		if event.Err != "" {
			m.addSystemEntry("Runtime stream error: " + event.Err)
		}
		m.view.invalidateLive()
		m.updateLayout()
		return m, m.reconnectRuntimeEventStream()

	case core.EventModelRecoveryResumed:
		m.activeRecovery = nil
		m.pausedCheckpoint = nil
		m.run.running = true
		m.view.invalidateLive()
		m.updateLayout()

	case core.EventTaskFinished:
		m.finishTurnEvent(event)
		return m, m.waitForExecution(event.ExecutionID)

	case core.EventTaskDiscarded:
		m.run.thinking = false
		m.run.running = false
		m.run.taskActivity = ""
		main := m.ensureAgentMetrics("lead")
		main.Status = "Cancelled"
		main.Activity = "Cancelled"
		if !main.StartedAt.IsZero() {
			main.Duration = time.Since(main.StartedAt)
		}
		m.pausedCheckpoint = nil
		m.activeRecovery = nil
		m.activeToolRecovery = nil
		m.run.resetStream()
		m.run.resetReasoning()
		m.view.invalidateLive()
		m.markRunningSubagentsAndToolsDone()
		m.view.invalidateLive()
		m.updateLayout()
	}

	return m, nil
}
