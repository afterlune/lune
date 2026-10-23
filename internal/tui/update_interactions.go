package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

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
		q.Selected = recommendedQuestionOptionIndex(q.Request.Questions[q.CurrentIndex].Options)
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
		return m.cancelActivePlanReview()
	}

	permissionMode := m.savedMode
	if err := m.client.SetPermissionMode(permissionMode); err != nil {
		m.addSystemEntry("Failed to restore permissions; plan implementation was not started: " + err.Error())
		m.updateLayout()
		return m, nil
	}
	m.activePlanReview = nil
	m.planMode = false
	m.permissionMode = m.savedMode
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
	m.addSystemEntry("Plan review closed. The plan remains in the conversation, and Plan mode is still active. Continue discussing or revising it here.")
	m.updateLayout()
	return m, nil
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
	return end == len(m.entries) && m.running && strings.TrimSpace(m.currentStreamText) == ""
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
