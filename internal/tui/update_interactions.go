package tui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
)

func (m *tuiModel) resolveActiveQuestion() (tea.Model, tea.Cmd) {
	q := m.activeQuestion
	if q == nil || q.Submitting || len(q.Request.Questions) == 0 {
		return m, nil
	}

	q.currentDraft().Confirmed = true
	q.Error = ""
	m.recordQuestionAnswers(q)

	// Browse in request order, wrapping to skipped questions before submission.
	for offset := 1; offset < len(q.Request.Questions); offset++ {
		next := (q.CurrentIndex + offset) % len(q.Request.Questions)
		if !q.Drafts[next].Confirmed {
			q.CurrentIndex = next
			m.updateLayout()
			return m, nil
		}
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
		return m.cancelActivePlanReview()
	}

	permissionMode := m.permissionMode
	if m.planMode {
		permissionMode = m.savedMode
		if err := m.client.SetPermissionMode(permissionMode); err != nil {
			m.addSystemEntry("Failed to restore permissions; plan implementation was not started: " + err.Error())
			m.updateLayout()
			return m, nil
		}
	}
	m.activePlanReview = nil
	m.planMode = false
	m.permissionMode = permissionMode
	m.updateLayout()

	runtime := m.runtime
	return m, func() tea.Msg {
		impl, err := runtime.ImplementPlan(context.Background(), review.ExecutionID, core.PlanImplementationOptions{Mode: mode, PermissionMode: permissionMode})
		return planImplementedMsg{impl: impl, planText: review.Text, err: err}
	}
}

func (m *tuiModel) cancelActivePlanReview() (tea.Model, tea.Cmd) {
	if m.activePlanReview == nil {
		return m, nil
	}
	m.activePlanReview = nil
	m.addSystemEntry("Plan review closed. The plan remains in the conversation. Continue discussing or revising it here.")
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) resolveActiveApproval() (tea.Model, tea.Cmd) {
	appr := m.activeApproval
	if appr == nil {
		return m, nil
	}
	var approvalDecision publictool.ApprovalDecision
	var decisionText string
	switch appr.Selected {
	case 0:
		approvalDecision = publictool.ApprovalAllowOnce
		decisionText = "Allowed once"
	case 1:
		approvalDecision = publictool.ApprovalAllowSession
		decisionText = "Allowed for session"
	default:
		approvalDecision = publictool.ApprovalDeny
		decisionText = "Denied"
	}
	apprID := appr.Request.ID
	toolName := appr.Request.Tool
	m.activeApproval = nil
	m.run.running = true
	m.run.thinking = false
	m.run.taskStartedAt = time.Now()
	m.scrolledUp = false
	m.refreshViewportContent()
	m.viewport.GotoBottom()
	m.updateLayout()

	m.addSystemEntry(fmt.Sprintf("%s tool execution for `%s`.", decisionText, toolName))

	runtime := m.runtime
	executionID := m.run.runningExecution
	return m, tea.Batch(
		tickClock(),
		m.waitForExecution(executionID),
		func() tea.Msg {
			return approvalResolvedMsg{approvalID: apprID, err: runtime.ResolveApprovalDecision(apprID, approvalDecision)}
		},
	)
}

// approvalResolvedMsg reports the outcome of an approval decision sent to Core.
type approvalResolvedMsg struct {
	approvalID string
	err        error
}

// finishApprovalResolution surfaces a failed decision instead of swallowing it:
// the card comes back and the run state stops claiming a task Core never resumed.
func (m *tuiModel) finishApprovalResolution(msg approvalResolvedMsg) tea.Cmd {
	if msg.err == nil {
		return nil
	}
	m.run.running = false
	m.run.thinking = false
	m.addSystemEntry(fmt.Sprintf("Failed to resolve the tool approval %s: %v. Choose again below.", msg.approvalID, msg.err))
	m.checkPendingState(context.Background())
	m.updateLayout()
	return nil
}

// approveWithoutCard resolves an approval without a card when the active
// permission mode already grants every tool. It reports whether Core accepted the
// decision; a rejection must show the card, otherwise the task would stay paused
// with nothing on screen.
func (m *tuiModel) approveWithoutCard(request core.ApprovalRequest) bool {
	if m.permissionMode != core.PermissionFullAccess || m.runtime == nil || request.ID == "" {
		return false
	}
	if _, failed := m.run.autoApprovalFailures[request.ID]; failed {
		return false
	}
	if err := m.runtime.ResolveApproval(request.ID, true); err != nil {
		slog.Warn("auto-approve tool call in full-access mode", "tool", request.Tool, "error", err)
		m.noteAutoApprovalFailure(request.ID)
		return false
	}
	return true
}

// noteAutoApprovalFailure reports a failed silent approval once and stops
// retrying that request so the card stays visible.
func (m *tuiModel) noteAutoApprovalFailure(approvalID string) {
	if m.run.autoApprovalFailures == nil {
		m.run.autoApprovalFailures = map[string]struct{}{}
	}
	m.run.autoApprovalFailures[approvalID] = struct{}{}
	m.addSystemEntry("Full-access auto-approval failed; resolve the tool approval below.")
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
	m.run.running = true
	m.run.thinking = false
	m.run.taskStartedAt = time.Now()
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

// toggleReasoningExpansion toggles the expanded/collapsed state of the latest reasoning block.
func (m *tuiModel) toggleReasoningExpansion() (tea.Model, tea.Cmd) {
	if m.run.currentReasoningText != "" {
		m.run.reasoningLiveCollapsed = !m.run.reasoningLiveCollapsed
		m.refreshViewportContent()
		return m, nil
	}

	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindReasoning {
			m.entries[i].ReasoningExpanded = !m.entries[i].ReasoningExpanded
			if m.entries[i].ReasoningExpanded {
				m.entries[roundStartIndex(m.entries, i)].RoundExpanded = true
			}
			m.view.invalidateBlocks()
			m.refreshViewportContent()
			return m, nil
		}
	}
	m.addSystemEntry("No thinking/reasoning blocks found in this session.")
	return m, nil
}

// roundStartIndex returns the first entry index of the tool round that contains idx.
func roundStartIndex(entries []TranscriptEntry, idx int) int {
	start := idx
	for start > 0 && isRoundMember(entries, start-1) {
		start--
	}
	return start
}

// roundEndIndex returns the exclusive end index of the tool round starting at idx.
func (m *tuiModel) roundEndIndex(idx int) int {
	end := idx + 1
	for end < len(m.entries) && isRoundMember(m.entries, end) {
		end++
	}
	return end
}

// roundLive reports whether the round spanning entries[start:end] is still running.
func (m *tuiModel) roundLive(start, end int) bool {
	for i := start; i < end; i++ {
		entry := m.entries[i]
		if entry.ToolRunning || entry.SubagentRunning || entry.ReasoningLive {
			return true
		}
	}
	return end == len(m.entries) && m.run.running && strings.TrimSpace(m.run.currentStreamText) == ""
}

// roundExpanded reports whether a round renders expanded. The running round follows
// the user's explicit collapse so Ctrl+O and clicks work during execution, while a
// finished round follows its own flag.
func (m *tuiModel) roundExpanded(headIdx int, live bool) bool {
	if live {
		return !m.roundsCollapsedByUser
	}
	return m.entries[headIdx].RoundExpanded
}

// toggleRoundExpansion flips one round between expanded and collapsed.
func (m *tuiModel) toggleRoundExpansion(headIdx int) {
	if m.roundLive(headIdx, m.roundEndIndex(headIdx)) {
		m.roundsCollapsedByUser = !m.roundsCollapsedByUser
		return
	}
	m.entries[headIdx].RoundExpanded = !m.entries[headIdx].RoundExpanded
}

// beginRoundIfNeeded clears the running-round collapse override so a new round in
// the same turn opens expanded again.
func (m *tuiModel) beginRoundIfNeeded() {
	if n := len(m.entries); n > 0 && isRoundMember(m.entries, n-1) {
		return
	}
	m.roundsCollapsedByUser = false
}

// toolRoundStarts returns the header entry index of every tool round in the transcript.
func (m *tuiModel) toolRoundStarts() []int {
	var starts []int
	for i := range m.entries {
		if !isRoundMember(m.entries, i) {
			continue
		}
		if i > 0 && isRoundMember(m.entries, i-1) {
			continue
		}
		starts = append(starts, i)
	}
	return starts
}

// toggleAllToolRounds expands every tool round when at least one is collapsed and
// collapses all of them otherwise. Sub-step expansion flags are left untouched.
func (m *tuiModel) toggleAllToolRounds() (tea.Model, tea.Cmd) {
	starts := m.toolRoundStarts()
	if len(starts) == 0 {
		return m, nil
	}
	expand := false
	for _, start := range starts {
		if !m.roundExpanded(start, m.roundLive(start, m.roundEndIndex(start))) {
			expand = true
			break
		}
	}
	for _, start := range starts {
		m.entries[start].RoundExpanded = expand
	}
	m.roundsCollapsedByUser = !expand
	m.view.invalidateBlocks()
	m.refreshViewportContent()
	return m, nil
}
