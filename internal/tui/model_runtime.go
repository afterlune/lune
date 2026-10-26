package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	core "github.com/afterlune/luneCore"
)

// bindSessionRuntime loads a complete replacement snapshot before switching session state.
func (m *tuiModel) bindSessionRuntime(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	if m.client == nil {
		return fmt.Errorf("core client is unavailable")
	}
	if err := m.flushQuestionDraft(ctx); err != nil {
		return fmt.Errorf("save question draft before switching sessions: %w", err)
	}
	rt, err := m.client.OpenSessionRuntime(ctx, sessionID)
	if err != nil {
		return err
	}
	keepRuntime := false
	defer func() {
		if !keepRuntime {
			_ = rt.Close()
		}
	}()
	saved, err := m.client.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	state, err := rt.State(ctx)
	if err != nil {
		return err
	}
	pending, err := rt.PendingRequests(ctx)
	if err != nil {
		return err
	}
	if m.run.runtimeCancel != nil {
		m.run.runtimeCancel()
		m.run.runtimeCancel = nil
	}
	m.replaceSessionBinding()
	if m.runtime != nil {
		// Stop interactive prompts on the outgoing runtime before closing it. A
		// failed close must not abort the switch: the model would otherwise stay
		// bound to a closed runtime with no way back.
		m.runtime.SetUserInteractionAvailable(false)
		if err := m.runtime.Close(); err != nil {
			slog.Warn("close previous session runtime", "error", err)
		}
	}
	m.resetSessionState()
	m.runtime = rt
	keepRuntime = true
	m.sessionID = sessionID
	m.currentModel = m.client.ModelForSession(ctx, sessionID)
	m.loadSessionSnapshot(saved)
	m.restoreRuntimeSnapshot(state)
	m.applyPendingRequests(pending)
	rt.SetUserInteractionAvailable(true)
	m.beginSessionRestore()
	m.updateLayout()
	return nil
}

// beginSessionRestore freezes footer metrics and transcript rebuilds until the
// runtime's durable event replay for this session settles.
func (m *tuiModel) beginSessionRestore() {
	m.run.beginRestore()
}

// finishSessionRestore re-syncs live state from Core and re-renders the transcript
// once replay settles, using the latest persisted usage snapshot as the baseline.
func (m *tuiModel) finishSessionRestore() {
	if !m.run.finishRestore() {
		return
	}
	if m.runtime != nil {
		ctx := context.Background()
		if state, err := m.runtime.State(ctx); err == nil && !m.run.running && !m.hasPausedTask() {
			// Adopt Core's state when the model is idle; an in-flight local turn or a
			// paused checkpoint stays authoritative.
			m.restoreRuntimeSnapshot(state)
		}
		m.checkPendingState(ctx)
	}
	m.refreshSessionUsageSnapshot(context.Background())
	m.refreshViewportContent()
	m.updateLayout()
}

func (m *tuiModel) refreshSessionUsageSnapshot(ctx context.Context) {
	if m.client == nil || m.sessionID == "" {
		return
	}
	saved, err := m.client.GetSession(ctx, m.sessionID)
	if err != nil {
		return
	}
	m.usage.applyTotals(saved.SessionUsage)
	m.usage.clearLastRequest()

	if len(saved.AgentUsage) > 0 {
		for agentID, totals := range saved.AgentUsage {
			stats := m.ensureAgentMetrics(agentID)
			stats.InputTokens = totals.Input
			stats.OutputTokens = totals.Output
			stats.CachedTokens = totals.Cached
			stats.ReasoningTokens = totals.Reasoning
		}
	} else if m.usage.seededSessionUsage {
		stats := m.ensureAgentMetrics("lead")
		stats.InputTokens = saved.SessionUsage.Input
		stats.OutputTokens = saved.SessionUsage.Output
		stats.CachedTokens = saved.SessionUsage.Cached
		stats.ReasoningTokens = saved.SessionUsage.Reasoning
	}

	last, hasLast := latestLeadUsageRecord(saved.UsageRequests)
	if hasLast {
		window := int64(0)
		switch {
		case m.run.running && m.run.taskSnapshot.ContextWindow > 0:
			window = m.run.taskSnapshot.ContextWindow
		case saved.Settings.ContextWindow != nil && *saved.Settings.ContextWindow > 0:
			window = *saved.Settings.ContextWindow
		default:
			window = last.ContextWindow
		}
		m.usage.applyLeadRequest(last.Usage.InputTokens, last.Usage.InputTokensDetails.CachedTokens,
			last.EstimatedInputTokens, window, last.RequestID, last.RequestAttempt, last.RequestChanges)
		stats := m.ensureAgentMetrics("lead")
		stats.ContextInputTokens = last.Usage.InputTokens
		if stats.ContextInputTokens <= 0 {
			stats.ContextInputTokens = last.EstimatedInputTokens
		}
		stats.EstimatedInputTokens = last.EstimatedInputTokens
		m.applyAgentModelContext(stats, last.ModelProfile, last.ContextWindow)
	}
}

// ensureSession creates the draft session on first use so launching the TUI or
// running /new never persists an empty session.
func (m *tuiModel) ensureSession(ctx context.Context) error {
	if m.sessionID != "" {
		return nil
	}
	if m.client == nil {
		return fmt.Errorf("core client is unavailable")
	}

	// bindSessionRuntime resets per-session state, so capture the deferred choices first.
	title := m.pendingTitle
	profile := m.pendingModelProfile
	effort := m.reasoningEffort

	created, err := m.client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		return err
	}
	if err := m.bindSessionRuntime(ctx, created.ID); err != nil {
		_ = m.client.DeleteSession(ctx, created.ID)
		return err
	}

	if title != "" {
		if err := m.client.SetSessionTitle(ctx, created.ID, title); err != nil {
			return err
		}
		m.sessionTitle = title
		m.pendingTitle = ""
	}
	if profile != "" {
		if err := applySessionModelProfile(ctx, m.client, created.ID, profile); err != nil {
			return err
		}
		m.pendingModelProfile = ""
		m.currentModelProfile = profile
		m.currentModel = m.client.ModelForSession(ctx, created.ID)
	}
	if effort != "" {
		if err := m.mutateSessionSettings(func(settings *core.SessionSettings) {
			settings.ReasoningEffort = effort
		}); err != nil {
			return err
		}
		m.reasoningEffort = effort
	}
	m.refreshViewportContent()
	return nil
}

func (m *tuiModel) restoreRuntimeSnapshot(state core.RuntimeState) {
	m.run.goal = state.Goal
	m.pausedCheckpoint = nil
	m.run.restoreFrom(state.Task)
	contextWindowKnown := state.Task.ContextWindow > 0 || state.Task.TaskID != ""
	switch state.Task.Status {
	case core.TaskRunning, core.TaskQueued:
		if contextWindowKnown {
			m.usage.setContextWindow(state.Task.ContextWindow)
		}
	case core.TaskPaused:
		if contextWindowKnown {
			m.usage.setContextWindow(state.Task.ContextWindow)
		}
		m.pausedCheckpoint = &PausedCheckpoint{ExecutionID: state.Task.ExecutionID}
	}
}

// hasPausedTask reports whether the current session has an interrupted checkpoint or pending recovery.
func (m *tuiModel) hasPausedTask() bool {
	return m.pausedCheckpoint != nil || m.activeRecovery != nil || m.activeToolRecovery != nil
}

// checkPendingState queries Core for any pending user questions or approvals.
func (m *tuiModel) checkPendingState(ctx context.Context) {
	if m.runtime == nil {
		return
	}
	reqs, err := m.runtime.PendingRequests(ctx)
	if err != nil {
		slog.Warn("inspect pending task decisions", "error", err)
		return
	}
	m.applyPendingRequests(reqs)
}

func (m *tuiModel) applyPendingRequests(reqs []core.PendingRequest) {
	var foundQuestion *core.UserQuestionRequest
	var foundApproval *core.ApprovalRequest
	var foundRecovery *core.PendingModelRecovery
	var foundToolRecovery *core.ToolRecoveryRequest
	for _, req := range reqs {
		if req.Kind == core.PendingUserQuestion && req.UserQuestion != nil && foundQuestion == nil {
			if m.run.questionSubmissions[m.questionKey(*req.UserQuestion)] == 0 {
				foundQuestion = req.UserQuestion
			}
		}
		if req.Kind == core.PendingApproval && req.Approval != nil && foundApproval == nil {
			foundApproval = req.Approval
		}
		if req.Kind == core.PendingModelRecoveryRequest && req.ModelRecovery != nil && foundRecovery == nil {
			foundRecovery = req.ModelRecovery
		}
		if req.Kind == core.PendingToolRecovery && req.ToolRecovery != nil && foundToolRecovery == nil {
			foundToolRecovery = req.ToolRecovery
		}
	}

	if m.activeQuestion != nil && m.activeQuestion.Submitting {
		// Keep selections visible until the submission command reports its result.
	} else if foundQuestion != nil {
		if m.activeQuestion == nil || m.questionKey(m.activeQuestion.Request) != m.questionKey(*foundQuestion) {
			m.activeQuestion = newActiveQuestion(*foundQuestion)
		}
	} else {
		m.activeQuestion = nil
	}

	if foundApproval != nil {
		if !m.approveWithoutCard(*foundApproval) {
			if m.activeApproval == nil || m.activeApproval.Request.ID != foundApproval.ID {
				m.activeApproval = &ActiveApproval{
					Request:  *foundApproval,
					Selected: 0,
				}
			}
		}
	} else {
		m.activeApproval = nil
	}

	if foundRecovery != nil {
		if m.activeRecovery == nil || m.activeRecovery.CallID != foundRecovery.CallID {
			reason := string(foundRecovery.Failure.Category)
			if foundRecovery.Failure.StatusCode > 0 {
				reason = fmt.Sprintf("%s (status=%d, attempts=%d)", foundRecovery.Failure.Category, foundRecovery.Failure.StatusCode, foundRecovery.Failure.Attempts)
			}
			m.activeRecovery = &ActiveRecovery{
				RunID:          foundRecovery.RunID,
				ExecutionID:    foundRecovery.ExecutionID,
				InvocationID:   foundRecovery.InvocationID,
				CallID:         foundRecovery.CallID,
				Reason:         reason,
				RetryNotBefore: foundRecovery.Failure.RetryNotBefore,
				Selected:       0,
			}
		}
	} else {
		m.activeRecovery = nil
	}

	if foundToolRecovery != nil {
		if m.activeToolRecovery == nil || m.activeToolRecovery.InvocationID != foundToolRecovery.InvocationID {
			errOutput := ""
			for i := len(m.entries) - 1; i >= 0; i-- {
				if m.entries[i].Kind == KindTool && (m.entries[i].ToolCallID == foundToolRecovery.CallID || m.entries[i].ToolName == foundToolRecovery.Tool) {
					errOutput = m.entries[i].ToolOutput
					break
				}
			}
			m.activeToolRecovery = &ActiveToolRecovery{
				InvocationID: foundToolRecovery.InvocationID,
				CallID:       foundToolRecovery.CallID,
				ToolName:     foundToolRecovery.Tool,
				Arguments:    string(foundToolRecovery.Arguments),
				ErrorOutput:  errOutput,
				Selected:     0,
			}
		}
	} else {
		m.activeToolRecovery = nil
	}
}
