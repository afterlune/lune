package tui

import (
	"context"
	"fmt"
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
	if m.transcriptDirty || m.cachedDirty {
		m.refreshViewportContent()
	}
	return m, cmd
}

func (m *tuiModel) processRuntimeEventNoRefresh(event core.Event) (tea.Model, tea.Cmd) {
	switch event.Kind {
	case core.EventTaskStarted:
		execID := event.ExecutionID
		if execID == "" {
			return m, nil
		}
		queued := len(m.queuedTurns) > 0 && m.queuedTurns[0].ExecutionID == execID
		if queued {
			m.queuedTurns = m.queuedTurns[1:]
			for i := range m.entries {
				if m.entries[i].Kind == KindQueued && (m.entries[i].ToolCallID == execID || m.entries[i].ToolCallID == "") {
					m.entries[i].Kind = KindUser
					m.entries[i].ToolCallID = execID
					m.invalidateEntryCache()
					break
				}
			}
			m.currentStreamText = ""
			m.currentReasoningText = ""
		}
		if !queued && ((!m.running && m.runningExecution != execID) || (m.runningExecution != "" && m.runningExecution != execID)) {
			return m, nil
		}
		if m.runningExecution == execID && m.taskSnapshot.TaskID == event.TaskID && m.running {
			return m, m.waitForExecution(execID)
		}
		limits := core.ExecutionOptions{}
		if m.client != nil {
			limits = m.client.ExecutionDefaults()
		}
		nextTask := core.TaskState{TaskID: event.TaskID, ExecutionID: execID, Status: core.TaskRunning, Execution: limits}
		if m.runtime != nil {
			if state, err := m.runtime.State(context.Background()); err == nil && state.Task.ExecutionID == execID {
				nextTask = state.Task
				if state.Task.TaskID != event.TaskID || state.Task.Status != core.TaskRunning {
					m.restoreRuntimeSnapshot(state)
					m.checkPendingState(context.Background())
					switch state.Task.Status {
					case core.TaskCompleted, core.TaskFailed, core.TaskCancelled, core.TaskDiscarded:
						m.runningExecution = execID
						return m, m.awaitExecutionResult(execID)
					}
					return m, m.waitForExecution(m.runningExecution)
				}
			}
		}
		if m.taskSnapshot.TaskID != "" && m.taskSnapshot.TaskID != event.TaskID {
			delete(m.awaitingTurns, execID)
			delete(m.terminalEvents, execID)
		}
		m.taskSnapshot = nextTask
		m.running = true
		m.runningExecution = execID
		m.taskStartedAt = time.Now()
		main := m.ensureAgentMetrics("lead")
		main.Status = "Running"
		main.Activity = "Starting"
		main.StartedAt = m.taskStartedAt
		main.Duration = 0
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].Kind == KindUser {
				main.Task = compactAgentTask(m.entries[i].Text)
				break
			}
		}
		if event.ContextWindow > 0 {
			main.ContextWindow = event.ContextWindow
			m.contextWindow = event.ContextWindow
		}
		if event.ModelProfile != "" {
			main.ModelProfile = event.ModelProfile
			m.currentModelProfile = event.ModelProfile
		}
		if event.Model != "" {
			main.Model = event.Model
		}
		m.thinking = false
		m.transcriptDirty = true
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
			if m.reasoningStartTime.IsZero() {
				m.reasoningStartTime = time.Now()
			}
			if m.streamFirstTokenTime.IsZero() {
				m.streamFirstTokenTime = time.Now()
				m.streamGenerationTokens = 0
			}
			m.thinking = true
			m.currentReasoningText += event.Stream.Delta
			if event.Stream.Delta != "" {
				toks := estimateDeltaTokens(event.Stream.Delta)
				m.streamGenerationTokens += toks
				elapsed := time.Since(m.streamFirstTokenTime).Seconds()
				if elapsed >= 0.05 {
					m.lastTokensPerSec = float64(m.streamGenerationTokens) / elapsed
				}
			}
			m.transcriptDirty = true

		case "text_delta", "":
			if m.currentReasoningText != "" {
				m.flushReasoningEntry()
			}
			if m.streamFirstTokenTime.IsZero() {
				m.streamFirstTokenTime = time.Now()
				m.streamGenerationTokens = 0
			}
			if event.Stream.Delta != "" {
				m.thinking = false
				m.currentStreamText += event.Stream.Delta
				toks := estimateDeltaTokens(event.Stream.Delta)
				m.streamGenerationTokens += toks
				elapsed := time.Since(m.streamFirstTokenTime).Seconds()
				if elapsed >= 0.05 {
					m.lastTokensPerSec = float64(m.streamGenerationTokens) / elapsed
				}
				m.transcriptDirty = true
			}
		}

	case core.EventAgentStarted:
		m.updateAgentEvent(event, true)
		m.upsertSubagentEntry(event.AgentID, event.AgentName, event.AgentRole, true, 0)

	case core.EventAgentFinished:
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
		m.streamFirstTokenTime = time.Time{}
		m.streamGenerationTokens = 0
		m.recordAgentToolStarted("lead", event.Tool.Name)
		m.flushReasoningEntry()
		m.flushStreamTextEntry()
		m.thinking = false

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
					if m.running {
						m.entries[i].ToolRunning = true
					}
					m.invalidateEntryCache()
					return m, nil
				}
			}
		}
		entry := TranscriptEntry{
			ID:          callID,
			Kind:        KindTool,
			ToolCallID:  callID,
			ToolName:    event.Tool.Name,
			Timestamp:   time.Now(),
			ToolCommand: cmdText,
			ToolRunning: m.running,
			ToolChanges: changes,
			ToolOutput:  outText,
		}
		m.entries = append(m.entries, entry)
		m.invalidateEntryCache()

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
			} else if event.Tool.Duration > 0 || event.Tool.Details != nil {
				m.entries[i].ToolRunning = false
			}
			if event.Tool.Duration > 0 {
				m.entries[i].ToolDuration = event.Tool.Duration
			}
			m.invalidateEntryCache()
			found = true
			break
		}
		if !found {
			entry := TranscriptEntry{
				ID:          targetID,
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
			m.entries = append(m.entries, entry)
			m.invalidateEntryCache()
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
				m.invalidateEntryCache()
				break
			}
		}

	case core.EventContextCompaction:
		if event.Compaction != nil && m.running {
			switch event.Compaction.Stage {
			case "completed":
				if event.Compaction.BeforeTokens > 0 && event.Compaction.AfterTokens > 0 {
					m.addSystemEntry(fmt.Sprintf("Context compacted: %d → %d tokens.", event.Compaction.BeforeTokens, event.Compaction.AfterTokens))
				}
			case "failed":
				if event.Compaction.Err != "" {
					m.addSystemEntry("Context compaction failed: " + event.Compaction.Err)
				}
			}
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
			if m.questionSubmissions[m.questionKey(request)] != 0 {
				return m, nil
			}
		}
		m.checkPendingState(context.Background())
		if m.activeQuestion != nil && !m.activeQuestion.Submitting {
			m.thinking = false
			m.running = false
		}
		m.transcriptDirty = true
		m.updateLayout()

	case core.EventUserInteractionRequired:
		m.thinking = false
		m.running = false
		m.checkPendingState(context.Background())
		m.transcriptDirty = true
		m.updateLayout()

	case core.EventApprovalRequired:
		m.thinking = false
		if event.Approval != nil && m.permissionMode == core.PermissionFullAccess {
			_ = m.runtime.ResolveApproval(event.Approval.ID, true)
		} else {
			m.checkPendingState(context.Background())
			m.transcriptDirty = true
			m.updateLayout()
		}

	case core.EventApprovalCancelled:
		if m.activeApproval != nil && m.activeApproval.Request.ID == event.ApprovalID {
			m.activeApproval = nil
			m.updateLayout()
		}

	case core.EventUsage:
		if m.running || !m.seededSessionUsage {
			m.totalInputTokens += event.Usage.InputTokens
			m.totalOutputTokens += event.Usage.OutputTokens
			m.cachedTokens += event.Usage.InputTokensDetails.CachedTokens
			m.reasoningTokens += event.Usage.OutputTokensDetails.ReasoningTokens
			if strings.TrimSpace(event.AgentID) != "" || isLeadMainUsage(event.AgentID, event.UsagePurpose) {
				m.recordAgentUsage(event)
			}
			if isLeadMainUsage(event.AgentID, event.UsagePurpose) && event.Usage.OutputTokens > 0 {
				var elapsed float64
				if !m.streamFirstTokenTime.IsZero() {
					elapsed = time.Since(m.streamFirstTokenTime).Seconds()
				} else if !m.turnStreamStartTime.IsZero() {
					elapsed = time.Since(m.turnStreamStartTime).Seconds()
				}
				if elapsed >= 0.05 {
					m.lastTokensPerSec = float64(event.Usage.OutputTokens) / elapsed
				}
				m.streamFirstTokenTime = time.Time{}
			}
		}
		if isLeadMainUsage(event.AgentID, event.UsagePurpose) {
			if event.ContextWindow > 0 {
				m.contextWindow = event.ContextWindow
			}
			m.lastInputTokens = event.Usage.InputTokens
			m.lastCachedTokens = event.Usage.InputTokensDetails.CachedTokens
			m.lastRequestID, m.lastRequestAttempt = event.RequestID, event.RequestAttempt
			m.lastRequestChanges = append([]string(nil), event.RequestChanges...)
			m.estimatedInputTokens = event.EstimatedInputTokens
		}

	case core.EventModelRecoveryRequired:
		m.thinking = false
		m.running = false
		m.checkPendingState(context.Background())
		m.transcriptDirty = true
		m.updateLayout()
		if m.activeRecovery != nil && !m.activeRecovery.RetryNotBefore.IsZero() {
			return m, tickClock()
		}

	case core.EventToolRecoveryRequired, core.EventExecutionBlocked:
		m.thinking = false
		m.running = false
		m.checkPendingState(context.Background())
		m.transcriptDirty = true
		m.updateLayout()

	case core.EventRuntimeStreamFailed:
		m.thinking = false
		m.running = false
		if event.Err != "" {
			m.addSystemEntry("Runtime stream error: " + event.Err)
		}
		m.transcriptDirty = true
		m.updateLayout()

	case core.EventModelRecoveryResumed:
		m.activeRecovery = nil
		m.pausedCheckpoint = nil
		m.running = true
		m.transcriptDirty = true
		m.updateLayout()

	case core.EventTaskFinished:
		m.finishTurnEvent(event)
		return m, m.waitForExecution(event.ExecutionID)

	case core.EventTaskDiscarded:
		m.thinking = false
		m.running = false
		main := m.ensureAgentMetrics("lead")
		main.Status = "Cancelled"
		main.Activity = "Cancelled"
		if !main.StartedAt.IsZero() {
			main.Duration = time.Since(main.StartedAt)
		}
		m.pausedCheckpoint = nil
		m.activeRecovery = nil
		m.activeToolRecovery = nil
		m.currentStreamText = ""
		m.currentReasoningText = ""
		m.markRunningSubagentsAndToolsDone()
		m.transcriptDirty = true
		m.updateLayout()
	}

	return m, nil
}
