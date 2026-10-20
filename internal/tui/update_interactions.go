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
