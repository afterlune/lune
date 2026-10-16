package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

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
	if m.planMode {
		if err := m.setPlanMode(true); err != nil {
			m.status = "Could not lock permissions to Read-only: " + err.Error()
			m.addEntry("error", m.status, true)
			return m, nil
		}
	}
	if m.runtime == nil || m.runtimeSessionID != m.sessionID {
		if err := m.bindSessionRuntime(context.Background(), m.sessionID); err != nil {
			m.status = "Could not open the session runtime."
			m.addEntry("error", err.Error(), true)
			return m, nil
		}
	}
	if mode == core.StartIfIdle {
		m.taskCompleted = false
		m.resetModelStream()
		m.modelStreamFirstTokenLatency = 0
		m.modelStreamFirstTokenRecorded = false
		if resetUsage {
			m.taskUsage = tokenUsageTotals{}
		}
		m.running = true
		m.executionStarted = false
		m.runtimeTask = true
		m.cancelled = false
		m.status = "Working… Press Esc to interrupt."
		m.taskStartedAt = time.Now()
	} else {
		m.status = "Turn added to the Core queue."
	}
	runtime := m.runtime
	request := core.TurnRequest{Input: input, PlanMode: m.planMode}
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
	m.executionStarted = state.Task.Status == core.TaskRunning
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
	m.executionStarted = state.Task.Status == core.TaskRunning
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
			m.executionStarted = true
			command = m.startWindowTitleAnimation()
			m.startModelStream(event)
			m.contextWindow = event.ContextWindow
			m.contextInput = 0
			m.contextEstimated = false
		}
	case core.EventTaskQueued:
		m.status = "Turn queued."
	case core.EventTaskFinished:
		if event.ExecutionID == m.runningExecutionID {
			m.executionStarted = false
			command = m.startWindowTitleAnimation()
		}
		m.finishModelStreamEvent(event)
		if event.ExecutionID != "" {
			delete(m.activeExecutions, event.ExecutionID)
		}
	case core.EventModelStream:
		return m.queueModelStreamEvent(event)
	case core.EventCommandStarted, core.EventCommandFinished:
		command = m.readCommandDetails(event.CommandID)
	case core.EventToolStarted, core.EventToolFinished, core.EventToolDetails:
		m.updateToolActivity(event.Tool)
	case core.EventAgentStarted, core.EventAgentFinished:
		m.updateTeamActivity(event)
	case core.EventUserQuestionRequired:
		if event.UserQuestion != nil {
			request := *event.UserQuestion
			m.pendingRequest = &core.PendingRequest{Kind: core.PendingUserQuestion, UserQuestion: &request}
			m.pending = true
			m.openQuestion(request)
		}
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
			m.pendingRequests = nil
			if m.pendingRequest != nil && m.pendingRequest.Kind == core.PendingApproval &&
				m.pendingRequest.Approval != nil && m.pendingRequest.Approval.ID == event.ApprovalID {
				m.pendingRequest = nil
				m.pending = false
			}
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
		m.question = nil
		m.questionOpen = false
		m.pendingRequest = nil
		m.updateLayout()
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
			m.openQuestion(*request.UserQuestion)
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
		if err := m.setPlanMode(true); err != nil {
			m.status = "Execution blocked; could not lock permissions to Read-only: " + err.Error()
			m.addEntry("error", m.status, true)
		} else {
			m.status = "Execution blocked: use /plan replan [feedback] or /discard."
		}
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

func (m *tuiModel) inspectPausedTask(budgetReason core.BudgetReason) tea.Cmd {
	if m.runtime == nil {
		return nil
	}
	runtime := m.runtime
	sessionID := m.sessionID
	return func() tea.Msg {
		ctx := context.Background()
		state, err := runtime.State(ctx)
		if err != nil {
			return pausedTaskDetailsMsg{sessionID: sessionID, paused: true, budgetReason: budgetReason, err: err}
		}
		if state.Task.Status != core.TaskPaused {
			return pausedTaskDetailsMsg{sessionID: sessionID, state: state.Task}
		}
		requests, err := runtime.PendingRequests(ctx)
		return pausedTaskDetailsMsg{sessionID: sessionID, paused: true, requests: requests, budgetReason: budgetReason, err: err}
	}
}

func (m *tuiModel) showPausedTaskDetails(message pausedTaskDetailsMsg) {
	if message.sessionID != m.sessionID || !message.paused || !m.pending {
		return
	}
	if message.err != nil {
		m.addEntry("error", "Could not inspect the paused task: "+message.err.Error(), true)
	}

	for _, request := range message.requests {
		if request.Kind == core.PendingUserQuestion {
			copy := request
			m.pendingRequest = &copy
			if copy.UserQuestion != nil {
				m.openQuestion(*copy.UserQuestion)
			}
			return
		}
	}
	m.question = nil
	m.questionOpen = false
	m.pendingRequest = nil
	m.updateLayout()
	m.addEntry("notice", pausedTaskSummary(message.budgetReason, message.requests), false)
}

func pausedTaskSummary(reason core.BudgetReason, requests []core.PendingRequest) string {
	lines := make([]string, 0, len(requests)+2)
	if reason != "" {
		lines = append(lines, "Task paused: "+budgetPauseDescription(reason)+".")
	}
	for _, request := range requests {
		if detail := pendingRequestSummary(request); detail != "" {
			lines = append(lines, detail)
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "Task paused, but no specific reason was reported.")
	}
	lines = append(lines, "Use /continue to review or resume, or /discard to abandon the paused task.")
	return strings.Join(lines, "\n")
}

func budgetPauseDescription(reason core.BudgetReason) string {
	switch reason {
	case core.BudgetMaxSteps:
		return "the step limit was reached"
	case core.BudgetMaxInvocations:
		return "the tool invocation limit was reached"
	case core.BudgetMaxTokens:
		return "the token budget was reached"
	default:
		return "the execution limit was reached (" + string(reason) + ")"
	}
}

func pendingRequestSummary(request core.PendingRequest) string {
	switch request.Kind {
	case core.PendingApproval:
		if request.Approval != nil {
			return "Waiting for approval of " + request.Approval.Tool + ". Use /continue to review it."
		}
		return "Waiting for a tool approval. Use /continue to review it."
	case core.PendingUserQuestion:
		if request.UserQuestion != nil {
			question := ""
			if len(request.UserQuestion.Questions) > 0 {
				question = request.UserQuestion.Questions[0].Question
			}
			if question != "" {
				text := "Waiting for your answer: " + question
				if len(request.UserQuestion.Questions[0].Options) >= 2 {
					text += "\n" + formatUserQuestionOptions(request.UserQuestion.Questions[0].Options)
				}
				return text + "\nEnter an answer to continue."
			}
		}
		return "Waiting for your answer. Use /continue to review the question."
	case core.PendingToolRecovery:
		if request.ToolRecovery != nil && request.ToolRecovery.Tool != "" {
			return "Tool " + request.ToolRecovery.Tool + " may have run before the pause. Use /continue retry or /continue skip."
		}
		return "A tool may have run before the pause. Use /continue retry or /continue skip."
	case core.PendingModelRecoveryRequest:
		if request.ModelRecovery != nil {
			detail := strings.TrimPrefix(modelFailureSummary(&request.ModelRecovery.Failure), "Model request failed: ")
			return "The model request failed (" + detail + "). Use /continue retry when ready."
		}
		return "A model request failed. Use /continue retry when ready."
	case core.PendingExecutionBlocker:
		if request.Blocker != nil {
			return "Execution blocked by " + request.Blocker.Tool + ": " + request.Blocker.Reason + ". Use /plan replan or /discard."
		}
		return "Execution is blocked. Use /plan replan or /discard."
	case core.PendingUserInteraction:
		return "Waiting for an external sign-in or confirmation. Use /continue to review it."
	default:
		return "Task is waiting for a Core decision. Use /continue to review it."
	}
}

func formatUserQuestionOptions(options []core.UserQuestionOption) string {
	if len(options) < 2 {
		return ""
	}
	lines := make([]string, 1, len(options)+1)
	lines[0] = "Options:"
	for index, option := range options {
		lines = append(lines, fmt.Sprintf("%d. %s", index+1, option.Label))
	}
	return strings.Join(lines, "\n")
}

func (m *tuiModel) answerUserQuestion(answer string) (tea.Model, tea.Cmd) {
	if m.pendingRequest == nil || m.pendingRequest.UserQuestion == nil || m.runtime == nil {
		return m, nil
	}
	question := *m.pendingRequest.UserQuestion
	numbered := !m.questionOpen
	if m.question == nil || !sameQuestion(m.question.request, question) {
		m.openQuestion(question)
	}
	if m.question.submitting {
		return m, nil
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		if len(m.question.item().Options) >= 2 {
			m.question.err = "Choose an option or enter an answer."
		} else {
			m.question.err = "Enter an answer."
		}
		return m, nil
	}
	if i, err := strconv.Atoi(answer); numbered && len(m.question.item().Options) >= 2 && err == nil && i >= 1 && i <= len(m.question.item().Options) {
		answer = m.question.item().Options[i-1].Label
		m.question.selected = i - 1
	}
	if numbered {
		matched := false
		for i, o := range m.question.item().Options {
			if o.Label == answer {
				m.question.selected = i
				matched = true
				break
			}
		}
		if !matched {
			m.question.selected = len(m.question.item().Options)
			m.question.customDraft = answer
		}
	}
	m.question.submitting = true
	m.question.err = ""
	m.question.fromComposer = numbered
	return m, m.submitQuestionAnswer(question, answer)
}

func (m *tuiModel) startRecovery(options *core.ExecutionOptions) (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		return m, nil
	}
	m.pendingRequests = nil
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
	m.executionStarted = false
	m.runtimeTask = true
	m.status = "Recovering the paused task…"
	m.resetInput()
	return m, tea.Batch(command, m.startWindowTitleAnimation())
}

func (m *tuiModel) waitForRecoveredExecution(executionID string) (tea.Model, tea.Cmd) {
	if executionID == "" {
		return m, nil
	}
	m.pendingRequests = nil
	m.running = true
	m.executionStarted = true
	m.runtimeTask = true
	m.runningExecutionID = executionID
	m.activeExecutions[executionID] = true
	m.status = "Continuing after your answer…"
	return m, tea.Batch(waitForTurn(m.runtime, executionID), taskClock(m.sessionID), m.startWindowTitleAnimation())
}

func (m *tuiModel) replanAfterBlocker(line string) (tea.Model, tea.Cmd) {
	if m.pendingRequest == nil || m.pendingRequest.Blocker == nil || m.runtime == nil {
		m.status = "There is no blocked execution to re-plan."
		return m, nil
	}
	if err := m.setPlanMode(true); err != nil {
		m.status = "Could not lock permissions to Read-only: " + err.Error()
		m.addEntry("error", m.status, true)
		return m, nil
	}
	feedback := strings.TrimSpace(strings.TrimPrefix(line, "/plan replan"))
	if _, err := m.runtime.DiscardPaused(context.Background()); err != nil {
		m.addEntry("error", "Could not discard the blocked execution: "+err.Error(), true)
		return m, nil
	}
	m.pendingRequest = nil
	m.pending = false
	if feedback == "" {
		feedback = "Create a revised plan that avoids the blocked operation."
	}
	input, err := core.NewTaskInput(feedback)
	if err != nil {
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.setTaskTitle(feedback)
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
	m.question = nil
	m.questionOpen = false
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
	m.executionStarted = true
	m.cancelled = false
	m.status = "Working… Press Esc to interrupt."
	m.taskStartedAt = time.Now()
	operationCmd := tea.Cmd(func() tea.Msg {
		return taskFinishedMsg{result: operation(ctx)}
	})
	return m, tea.Batch(operationCmd, m.startWindowTitleAnimation())
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
	startedAt := m.taskStartedAt
	if id := result.coreResult.ExecutionID; id != "" {
		if m.finishedTasks[id] {
			return
		}
		if result.coreResult.Status == core.TaskCompleted || result.coreResult.Status == core.TaskFailed {
			if m.finishedTasks == nil {
				m.finishedTasks = make(map[string]bool)
			}
			m.finishedTasks[id] = true
		}
	}
	m.running = false
	m.executionStarted = false
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
	m.taskCompleted = result.coreResult.Status == core.TaskCompleted && !taskFailed && !taskCancelled && result.err == nil
	proposal := result.coreResult.ProposedPlan
	if planText, ok := proposedPlanText(responseText); ok {
		if proposal == nil && result.coreResult.Status == core.TaskCompleted && !taskFailed && !taskCancelled && result.err == nil {
			proposal = &core.ProposedPlan{Text: planText}
		}
		responseText = stripProposedPlanTags(responseText)
	}
	if proposal != nil && strings.TrimSpace(responseText) == "" {
		responseText = proposal.Text
	}
	streamedFinal := m.finishModelStream(responseText, result.err == nil && !taskFailed && !taskCancelled)
	if responseErr != nil {
		m.addEntry("error", responseErr.Error(), true)
	} else if proposal != nil {
		before, after := splitPlanResponseText(responseText, proposal.Text)
		replacement := make([]tuiEntry, 0, 3)
		if before != "" {
			replacement = append(replacement, tuiEntry{role: "assistant", text: before})
		}
		replacement = append(replacement, tuiEntry{role: "plan", text: proposal.Text})
		if after != "" {
			replacement = append(replacement, tuiEntry{role: "assistant", text: after})
		}
		if updated, replaced := replaceStreamedPlanEntry(m.entries, responseText, replacement); streamedFinal && replaced {
			m.entries = updated
		} else {
			m.entries = append(m.entries, replacement...)
		}
	} else if responseText != "" && !streamedFinal {
		m.addEntry("assistant", responseText, false)
	} else if responseText == "" && !streamedFinal && result.notice == "" && result.coreResult.Status == core.TaskCompleted && proposal == nil {
		m.addEntry("notice", "Task completed, but it returned no displayable response.", false)
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
		m.status = "Task paused. See the conversation for the reason and next steps."
	} else {
		m.status = "Completed."
	}
	if !startedAt.IsZero() && (result.coreResult.Status == core.TaskCompleted || result.coreResult.Status == core.TaskFailed) {
		worked := time.Since(startedAt).Round(time.Second)
		m.addEntry("turn_footer", fmt.Sprintf("Worked for %s • %s", worked, time.Now().Local().Format("15:04")), false)
	}
	if !taskCancelled && !taskFailed && result.err == nil && result.coreResult.Status == core.TaskCompleted && proposal != nil {
		if m.searchOpen {
			m.closeTranscriptSearch(false)
		}
		m.detailsOpen = false
		m.activityFocus = false
		m.keymapOpen = false
		m.keymapCapture = ""
		m.planReview = true
		m.planReviewIndex = 0
		m.proposedPlan = proposal
		m.planSourceExecutionID = result.coreResult.ExecutionID
		m.planAuthorization = core.PlanAuthorization{}
		m.status = "Plan review · Read-only permissions."
		m.updateLayout()
	}
	m.cancelled = false
}

func splitPlanResponseText(response, plan string) (string, string) {
	response = strings.TrimSpace(response)
	plan = strings.TrimSpace(plan)
	if plan == "" {
		return response, ""
	}
	if index := strings.Index(response, plan); index >= 0 {
		return strings.TrimSpace(response[:index]), strings.TrimSpace(response[index+len(plan):])
	}
	if response == "" || response == plan {
		return "", ""
	}
	return response, ""
}

func replaceStreamedPlanEntry(entries []tuiEntry, response string, replacement []tuiEntry) ([]tuiEntry, bool) {
	for index := len(entries) - 1; index >= 0; index-- {
		if entries[index].role != "assistant" || entries[index].text != response {
			continue
		}
		updated := make([]tuiEntry, 0, len(entries)-1+len(replacement))
		updated = append(updated, entries[:index]...)
		updated = append(updated, replacement...)
		updated = append(updated, entries[index+1:]...)
		return updated, true
	}
	return entries, false
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
