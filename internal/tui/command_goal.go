package tui

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/lune/internal/goalcmd"
	core "github.com/afterlune/luneCore"
)

type goalActionMsg struct {
	sessionID         string
	bindingGeneration uint64
	action            string
	err               error
}

func (m *tuiModel) handleGoalCommand(raw string) (tea.Model, tea.Cmd) {
	command, err := goalcmd.Parse(raw)
	if err != nil {
		m.addSystemEntry("Goal: " + err.Error())
		return m, nil
	}
	if m.planMode && !command.AllowedInPlan() {
		m.addSystemEntry("Exit Plan mode before starting or changing a goal.")
		return m, nil
	}
	if m.client == nil {
		m.addSystemEntry("Goal management is unavailable without a Core client.")
		return m, nil
	}
	if m.sessionID == "" && command.Action != "create" {
		m.addSystemEntry(goalcmd.Summary(nil))
		return m, nil
	}
	if command.Action == "show" {
		m.refreshGoal()
		m.addSystemEntry(goalcmd.Summary(m.run.goal))
		return m, nil
	}
	if command.Action == "edit" && command.Objective == "" {
		m.refreshGoal()
		if m.run.goal == nil {
			m.addSystemEntry("No goal is set.")
			return m, nil
		}
		if m.run.goal.Status == core.GoalActive {
			m.addSystemEntry("Pause the goal before editing it.")
			return m, nil
		}
		m.input.composer.SetValue("/goal edit " + m.run.goal.Objective)
		m.input.composer.CursorEnd()
		return m, nil
	}
	var subscription tea.Cmd
	if m.sessionID == "" {
		if err := m.ensureSession(context.Background()); err != nil {
			m.addSystemEntry("Failed to create goal session: " + err.Error())
			return m, nil
		}
		subscription = m.startRuntimeEventSubscription(true)
	}
	runtime, sessionID := m.runtime, m.sessionID
	bindingGeneration := m.bindingGeneration
	action := func() tea.Msg {
		_, err := goalcmd.Apply(context.Background(), runtime, command, false)
		return goalActionMsg{sessionID: sessionID, bindingGeneration: bindingGeneration, action: command.Action, err: err}
	}
	return m, tea.Batch(subscription, action)
}

func (m *tuiModel) refreshGoal() {
	if m.runtime != nil {
		if goal, err := m.runtime.GetGoal(context.Background()); err == nil {
			m.run.goal = goal
		}
	}
}

func (m *tuiModel) finishGoalAction(msg goalActionMsg) (tea.Model, tea.Cmd) {
	if msg.sessionID != m.sessionID || !m.matchesBinding(msg.bindingGeneration) {
		return m, nil
	}
	m.refreshGoal()
	if msg.err != nil {
		m.addSystemEntry("Goal: " + msg.err.Error())
		return m, nil
	}
	if msg.action == "clear" {
		m.addSystemEntry("Goal cleared.")
	} else {
		m.addSystemEntry(goalcmd.Summary(m.run.goal))
	}
	var wait tea.Cmd
	if m.runtime != nil {
		if state, err := m.runtime.State(context.Background()); err == nil {
			m.restoreRuntimeSnapshot(state)
			wait = m.waitForExecution(m.run.runningExecution)
		}
		m.checkPendingState(context.Background())
	}
	m.updateLayout()
	return m, tea.Batch(wait, tickClock())
}

func (m *tuiModel) goalFooter() string {
	if m.run.goal == nil {
		return ""
	}
	goal := m.run.goal
	text := "goal " + string(goal.Status)
	if goal.TokenBudget != nil {
		text += fmt.Sprintf(" %s/%s", formatCompactTokens(goal.TokensUsed), formatCompactTokens(*goal.TokenBudget))
	}
	if m.run.running && m.run.taskSnapshot.GoalID == goal.ID {
		text += " · esc to pause"
	} else {
		text += " · /goal"
	}
	return text
}
