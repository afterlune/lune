package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	core "github.com/afterlune/luneCore"
)

func (m *tuiModel) startRuntimeEvents() {
	if m.runtime != nil {
		m.runtime.SetUserInteractionAvailable(true)
	}
}

func (m *tuiModel) subscribeRuntime() tea.Cmd {
	if m.runtime == nil || m.runtimeSessionID != m.sessionID {
		return nil
	}
	if m.runtimeCancel != nil {
		m.runtimeCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.runtimeCancel = cancel
	runtime := m.runtime
	cursor := m.eventCursor
	return func() tea.Msg {
		events, err := runtime.SubscribeEvents(ctx, cursor)
		return runtimeStreamReadyMsg{events: events, err: err}
	}
}

func nextRuntimeEvent(events <-chan core.Event) tea.Cmd {
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return runtimeEventMsg{events: nil}
		}
		return runtimeEventMsg{event: event, events: events}
	}
}

func waitForTurn(runtime SessionRuntime, executionID string) tea.Cmd {
	if runtime == nil || executionID == "" {
		return nil
	}
	return func() tea.Msg {
		result, err := runtime.WaitTurn(context.Background(), executionID)
		return taskFinishedMsg{result: taskResult{response: result.Response, coreResult: result, err: err}}
	}
}

func (m *tuiModel) submitTurn(input core.TaskInput, mode core.SubmissionMode, resetUsage bool) (tea.Model, tea.Cmd) {
	if m.runtime == nil || m.runtimeSessionID != m.sessionID {
		if err := m.bindSessionRuntime(context.Background(), m.sessionID); err != nil {
			m.status = "Could not open the session runtime."
			m.addEntry("error", err.Error(), true)
			return m, nil
		}
	}
	if mode == core.StartIfIdle {
		m.resetModelStream()
		m.modelStreamFirstTokenLatency = 0
		m.modelStreamFirstTokenRecorded = false
		if resetUsage {
			m.taskUsage = tokenUsageTotals{}
		}
		m.running = true
		m.runtimeTask = true
		m.cancelled = false
		m.status = "Working… Press Esc to interrupt."
	} else {
		m.status = "Turn added to the Core queue."
	}
	runtime := m.runtime
	request := core.TurnRequest{Input: input}
	return m, func() tea.Msg {
		submission, err := runtime.SubmitTurn(context.Background(), request, mode)
		return turnSubmittedMsg{submission: submission, err: err, resetUsage: resetUsage}
	}
}

func (m *tuiModel) bindSessionRuntime(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	if m.runtimeCancel != nil {
		m.runtimeCancel()
		m.runtimeCancel = nil
	}
	if m.runtime != nil {
		m.runtime.SetUserInteractionAvailable(false)
	}
	runtime, err := m.client.OpenSessionRuntime(ctx, sessionID)
	if err != nil {
		return err
	}
	m.runtime = runtime
	m.runtimeSessionID = sessionID
	m.sessionID = sessionID
	m.eventCursor = 0
	m.runtime.SetUserInteractionAvailable(true)
	m.activeExecutions = make(map[string]bool)
	state, err := runtime.State(ctx)
	if err != nil {
		return err
	}
	m.queued = append(m.queued[:0], state.Queue...)
	m.running = state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued
	m.pending = state.Task.Status == core.TaskPaused
	m.runningExecutionID = ""
	if state.Task.ExecutionID != "" {
		m.runningExecutionID = state.Task.ExecutionID
		if m.running {
			m.activeExecutions[state.Task.ExecutionID] = true
		}
	}
	for _, queued := range state.Queue {
		if queued.ExecutionID != "" {
			m.activeExecutions[queued.ExecutionID] = true
		}
	}
	return nil
}

func (m *tuiModel) refreshRuntimeProjection() error {
	if m.runtime == nil || m.runtimeSessionID != m.sessionID {
		if err := m.bindSessionRuntime(context.Background(), m.sessionID); err != nil {
			return err
		}
	}
	state, err := m.runtime.State(context.Background())
	if err != nil {
		return err
	}
	m.queued = append(m.queued[:0], state.Queue...)
	m.pending = state.Task.Status == core.TaskPaused
	m.running = state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued
	if m.running && state.Task.ExecutionID != "" {
		m.runningExecutionID = state.Task.ExecutionID
		m.activeExecutions[state.Task.ExecutionID] = true
	}
	return nil
}

func (m *tuiModel) handleRuntimeEvent(event core.Event) tea.Cmd {
	if event.SessionID != "" && event.SessionID != m.sessionID {
		return nil
	}
	var command tea.Cmd
	switch event.Kind {
	case core.EventTaskStarted:
		if event.ExecutionID == m.runningExecutionID {
			m.startModelStream(event)
			m.contextWindow = event.ContextWindow
			m.contextInput = 0
			m.contextEstimated = false
		}
	case core.EventTaskQueued:
		m.status = "Turn queued."
	case core.EventTaskFinished:
		m.finishModelStreamEvent(event)
		if event.ExecutionID != "" {
			delete(m.activeExecutions, event.ExecutionID)
		}
	case core.EventModelStream:
		return m.queueModelStreamEvent(event)
	case core.EventToolStarted, core.EventToolFinished:
		m.updateToolActivity(event.Tool)
	case core.EventAgentStarted, core.EventAgentFinished:
		m.updateTeamActivity(event)
	case core.EventApprovalRequired:
		if event.Approval != nil {
			request := toolApprovalRequestMsg{request: *event.Approval}
			m.pendingApproval = &request
			m.status = "Permission required: " + m.keyLabel("approve_tool") + " approve, " + m.keyLabel("deny_tool") + " deny."
			m.addEntry("permission", formatToolApprovalWithKeys(request.request, m.workspace, m.keyLabel("approve_tool"), m.keyLabel("deny_tool")), true)
		}
	case core.EventApprovalCancelled:
		if m.pendingApproval != nil && m.pendingApproval.request.ID == event.ApprovalID {
			m.pendingApproval = nil
			m.status = "Tool approval cancelled."
		}
	case core.EventUserInteractionRequired:
		if event.Interaction != nil {
			command = m.beginMCPInteraction(*event.Interaction)
		}
	case core.EventUserInteractionCancelled:
		if m.pendingMCPInteraction != nil && m.pendingMCPInteraction.request.ID == event.InteractionID {
			m.finishMCPInteraction("MCP interaction cancelled because its task ended.", false)
		}
	case core.EventUsage:
		m.updateModelUsage(event.Usage, event.AgentID, event.ModelProfile,
			event.Provider, event.Model, event.ContextWindow, event.EstimatedInputTokens)
	case core.EventContextCompaction:
		m.updateContextCompaction(event)
	case core.EventRuntimeStreamFailed:
		m.status = "Session event stream failed: " + event.Err
	case core.EventAcceptanceRequired:
		m.status = "Acceptance decision required. Use /continue to review it."
	case core.EventExecutionBlocked:
		m.status = "Execution blocked. Review the blocker and create a new read-only plan."
	case core.EventModelRecoveryRequired, core.EventToolRecoveryRequired:
		m.status = "Recovery decision required. Use /continue to review it."
	}
	m.refreshPanels()
	return command
}

func (m *tuiModel) continueTask(line string) (tea.Model, tea.Cmd) {
	if m.runtime == nil || m.runtimeSessionID != m.sessionID {
		if err := m.bindSessionRuntime(context.Background(), m.sessionID); err != nil {
			m.status = "Task recovery is unavailable."
			m.addEntry("error", err.Error(), true)
			return m, nil
		}
	}
	requests, err := m.runtime.PendingRequests(context.Background())
	if err != nil {
		m.status = "Could not inspect pending task decisions."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.pendingRequests = requests
	if len(requests) == 0 {
		state, stateErr := m.runtime.State(context.Background())
		if stateErr != nil {
			m.addEntry("error", stateErr.Error(), true)
			return m, nil
		}
		if state.Task.Status != core.TaskPaused {
			m.pending = false
			m.status = "No paused task in this session."
			m.resetInput()
			return m, nil
		}
		return m.startRecovery(nil)
	}
	request := requests[0]
	if m.pendingRequest != nil && m.pendingRequest.Kind == request.Kind {
		request = *m.pendingRequest
	}
	m.pendingRequest = &request
	m.pending = true
	switch request.Kind {
	case core.PendingApproval:
		if request.Approval != nil {
			m.pendingApproval = &toolApprovalRequestMsg{request: *request.Approval}
			m.status = "Approve or deny the pending tool call."
			m.addEntry("permission", formatToolApprovalWithKeys(*request.Approval, m.workspace, m.keyLabel("approve_tool"), m.keyLabel("deny_tool")), true)
		}
	case core.PendingUserQuestion:
		if request.UserQuestion != nil {
			question := request.UserQuestion
			text := question.Question
			if len(question.Options) > 0 {
				text += "\nOptions: " + strings.Join(question.Options, " · ")
			}
			m.status = "Answer the question, then press Enter."
			m.addEntry("question", text, true)
		}
	case core.PendingAcceptanceRequest:
		if request.Acceptance != nil {
			m.status = "Acceptance decision required: /continue confirm or /continue revise."
			m.addEntry("notice", acceptanceSummary(*request.Acceptance), true)
		}
	case core.PendingToolRecovery:
		m.status = "A tool may have run before interruption: /continue retry or /continue skip."
		for _, pending := range requests {
			if pending.ToolRecovery != nil {
				m.addEntry("notice", fmt.Sprintf("Tool recovery: %s · invocation %s", pending.ToolRecovery.Tool, pending.ToolRecovery.InvocationID), true)
			}
		}
	case core.PendingModelRecoveryRequest:
		m.status = "Model request recovery required: /continue retry when the provider is ready."
		if request.ModelRecovery != nil {
			failure := request.ModelRecovery.Failure
			detail := string(failure.Category)
			if failure.Code != "" {
				detail += " · " + failure.Code
			}
			if failure.StatusCode != 0 {
				detail += fmt.Sprintf(" · HTTP %d", failure.StatusCode)
			}
			if !failure.RetryNotBefore.IsZero() {
				detail += " · retry after " + failure.RetryNotBefore.Local().Format("15:04:05")
			}
			m.addEntry("notice", "Model request failed: "+detail, true)
		}
	case core.PendingExecutionBlocker:
		m.planMode = true
		m.status = "Execution blocked: use /plan replan [feedback] or /discard."
		if request.Blocker != nil {
			m.addEntry("error", request.Blocker.Tool+": "+request.Blocker.Reason, true)
		}
	case core.PendingUserInteraction:
		if request.Interaction != nil {
			return m, m.beginMCPInteraction(*request.Interaction)
		}
	default:
		m.status = "This task is waiting for a Core decision."
	}
	m.resetInput()
	return m, nil
}

func (m *tuiModel) answerUserQuestion(answer string) (tea.Model, tea.Cmd) {
	if m.pendingRequest == nil || m.pendingRequest.UserQuestion == nil || m.runtime == nil {
		return m, nil
	}
	question := *m.pendingRequest.UserQuestion
	if len(question.Options) > 0 && !containsString(question.Options, answer) {
		m.status = "Choose one of the listed options or enter a matching answer."
		return m, nil
	}
	if err := m.runtime.ResolveUserQuestion(context.Background(), core.UserQuestionAnswer{
		RunID: question.RunID, InvocationID: question.InvocationID, CallID: question.CallID, Answer: answer,
	}); err != nil {
		m.status = "Could not submit the answer."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.addEntry("you", answer, false)
	m.pendingRequest = nil
	m.pending = false
	m.resetInput()
	return m.waitForRecoveredExecution(question.ExecutionID)
}

func (m *tuiModel) startRecovery(options *core.ExecutionOptions) (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		return m, nil
	}
	runtime := m.runtime
	var command tea.Cmd = func() tea.Msg {
		var submission core.TurnSubmission
		var err error
		if options == nil {
			submission, err = runtime.Recover(context.Background())
		} else {
			submission, err = runtime.Recover(context.Background(), *options)
		}
		if err != nil {
			return taskFinishedMsg{result: taskResult{err: err}}
		}
		return turnSubmittedMsg{submission: submission}
	}
	m.running = true
	m.runtimeTask = true
	m.status = "Recovering the paused task…"
	m.resetInput()
	return m, command
}

func (m *tuiModel) waitForRecoveredExecution(executionID string) (tea.Model, tea.Cmd) {
	if executionID == "" {
		return m, nil
	}
	m.running = true
	m.runtimeTask = true
	m.runningExecutionID = executionID
	m.activeExecutions[executionID] = true
	m.status = "Continuing after your answer…"
	return m, waitForTurn(m.runtime, executionID)
}

func acceptanceSummary(pending core.PendingAcceptance) string {
	var lines []string
	if pending.Candidate != "" {
		lines = append(lines, pending.Candidate)
	}
	for _, criterion := range pending.Criteria {
		lines = append(lines, "• "+criterion.Description)
	}
	return strings.Join(lines, "\n")
}

func (m *tuiModel) replanAfterBlocker(line string) (tea.Model, tea.Cmd) {
	if m.pendingRequest == nil || m.pendingRequest.Blocker == nil || m.runtime == nil {
		m.status = "There is no blocked execution to re-plan."
		return m, nil
	}
	feedback := strings.TrimSpace(strings.TrimPrefix(line, "/plan replan"))
	if err := m.client.SetPermissionMode(core.PermissionReadOnly); err != nil {
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if _, err := m.runtime.DiscardPaused(context.Background()); err != nil {
		m.addEntry("error", "Could not discard the blocked execution: "+err.Error(), true)
		return m, nil
	}
	m.pendingRequest = nil
	m.pending = false
	m.planMode = true
	if feedback == "" {
		feedback = "Create a revised plan that avoids the blocked operation."
	}
	input, err := core.NewTaskInput(feedback)
	if err != nil {
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.addEntry("you", feedback, false)
	return m.submitTurn(input, core.StartIfIdle, true)
}

func (m *tuiModel) compact() (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.status = "Conversation compaction is unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	if pending, err := m.hasPending(); err != nil {
		m.status = "Could not inspect interrupted task."
		m.addEntry("error", err.Error(), true)
		return m, nil
	} else if pending {
		m.pending = true
		m.status = "A task is paused in this session; use /continue or start a new message branch."
		m.addEntry("notice", m.status, true)
		return m, nil
	}
	m.resetInput()
	return m.startTask(func(ctx context.Context) taskResult {
		compactedTurns, err := m.client.Compact(ctx, m.sessionID)
		if err != nil {
			return taskResult{err: err}
		}
		if compactedTurns == 0 {
			return taskResult{notice: "No older conversation to compact; the latest four user turns are already retained."}
		}
		return taskResult{notice: fmt.Sprintf("Compacted %d older user turn(s); kept the latest four.", compactedTurns)}
	}, true)
}

func (m *tuiModel) discard() (tea.Model, tea.Cmd) {
	if m.runtime == nil && m.sessionID != "" {
		if err := m.bindSessionRuntime(context.Background(), m.sessionID); err != nil {
			m.status = "Task recovery is unavailable."
			m.addEntry("error", err.Error(), true)
			return m, nil
		}
	}
	if m.runtime == nil {
		m.status = "Task recovery is unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	discarded, err := m.runtime.DiscardPaused(context.Background())
	if err != nil {
		m.status = "Could not discard interrupted task."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.pending = false
	m.status = "No interrupted task to discard."
	if discarded {
		m.status = "Interrupted task discarded."
	}
	m.addEntry("notice", m.status, false)
	m.resetInput()
	m.pendingRequest = nil
	_ = m.refreshRuntimeProjection()
	return m, nil
}

func (m *tuiModel) hasPending() (bool, error) {
	if m.runtime == nil && m.sessionID != "" {
		if err := m.bindSessionRuntime(context.Background(), m.sessionID); err != nil {
			return false, err
		}
	}
	if m.runtime == nil {
		return false, nil
	}
	state, err := m.runtime.State(context.Background())
	if err != nil {
		return false, err
	}
	m.pending = state.Task.Status == core.TaskPaused
	return m.pending, nil
}

func (m *tuiModel) startTask(operation func(context.Context) taskResult, resetUsage bool) (tea.Model, tea.Cmd) {
	m.resetModelStream()
	m.modelStreamFirstTokenLatency = 0
	m.modelStreamFirstTokenRecorded = false
	if resetUsage {
		m.taskUsage = tokenUsageTotals{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.running = true
	m.cancelled = false
	m.taskPlanMode = m.planMode
	m.status = "Working… Press Esc to interrupt."
	return m, func() tea.Msg {
		return taskFinishedMsg{result: operation(ctx)}
	}
}

func (m *tuiModel) interrupt() {
	if m.running && m.runtimeTask && m.runtime != nil && m.runningExecutionID != "" && !m.cancelled {
		m.cancelled = true
		m.status = "Interrupt requested…"
		if !m.runtime.Interrupt(m.runningExecutionID) {
			m.status = "Core could not interrupt the current turn."
		}
		return
	}
	if m.running && m.cancel != nil && !m.cancelled {
		m.cancelled = true
		m.status = "Cancelling task…"
		m.cancel()
	}
}

func (m *tuiModel) finishTask(result taskResult) {
	m.running = false
	m.pendingApproval = nil
	m.runtimeTask = false
	if executionID := result.coreResult.ExecutionID; executionID != "" {
		delete(m.activeExecutions, executionID)
		if m.runningExecutionID == executionID {
			m.runningExecutionID = ""
		}
	}
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}

	var responseText string
	var responseErr error
	if result.response != nil {
		responseText, responseErr = responseDisplayText(result.response)
	}
	taskFailed := result.coreResult.Status == core.TaskFailed
	taskCancelled := m.cancelled || result.coreResult.Status == core.TaskCancelled
	streamedFinal := m.finishModelStream(responseText, result.err == nil && !taskFailed && !taskCancelled)
	if responseErr != nil {
		m.addEntry("error", responseErr.Error(), true)
	} else if responseText != "" && !streamedFinal {
		m.addEntry("assistant", responseText, false)
	}
	if result.notice != "" {
		m.addEntry("notice", result.notice, false)
	}
	if result.targetSessionID != "" && result.targetSessionID != m.sessionID {
		saved, err := m.client.GetSession(context.Background(), result.targetSessionID)
		if err != nil {
			m.addEntry("error", "Plan started in a new session, but the session could not be loaded: "+err.Error(), true)
		} else if err := m.bindSessionRuntime(context.Background(), saved.ID); err != nil {
			m.addEntry("error", "Plan started in a new session, but its runtime could not be opened: "+err.Error(), true)
		} else {
			m.restoreSessionState(saved)
			m.status = "Plan implementation moved to a new session."
		}
	}

	pending, pendingErr := m.hasPending()
	if pendingErr != nil {
		m.addEntry("error", "Could not inspect interrupted task: "+pendingErr.Error(), true)
	}
	if taskCancelled {
		if pending {
			m.status = "Task paused. Use /continue to resume or type a new message to branch."
		} else {
			m.status = "Task cancelled."
		}
	} else if result.err != nil || taskFailed {
		m.status = "Task failed."
		if failure := result.coreResult.ModelFailure; failure != nil {
			m.addEntry("error", modelFailureSummary(failure), true)
		} else if result.err != nil {
			m.addEntry("error", result.err.Error(), true)
		} else {
			m.addEntry("error", "Task failed. Core did not provide a failure reason.", true)
		}
	} else if result.coreResult.Status == core.TaskPaused {
		m.status = "Task paused and waiting for a decision. Use /continue to review it."
	} else {
		m.status = "Completed."
	}
	if !taskCancelled && !taskFailed && result.err == nil && result.coreResult.ProposedPlan != nil {
		m.planReview = true
		m.proposedPlan = result.coreResult.ProposedPlan
		m.planSourceExecutionID = result.coreResult.ExecutionID
		m.planAuthorization = core.PlanAuthorization{}
		m.status = "Plan ready · /plan allow-* grants capabilities · /plan implement starts it · type feedback to revise."
	}
	m.cancelled = false
}

func modelFailureSummary(failure *core.ModelFailure) string {
	if failure == nil {
		return "Model request failed."
	}
	detail := "Model request failed: " + string(failure.Category)
	if failure.Code != "" {
		detail += " · " + failure.Code
	}
	if failure.StatusCode != 0 {
		detail += fmt.Sprintf(" · HTTP %d", failure.StatusCode)
	}
	return detail
}
