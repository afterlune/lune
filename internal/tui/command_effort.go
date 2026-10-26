package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

// reasoningEffortOptions returns the selectable reasoning-effort levels in display order.
func reasoningEffortOptions() []ReasoningEffortOption {
	return []ReasoningEffortOption{
		{Value: "none", Label: "none", Description: "Disable reasoning"},
		{Value: "minimal", Label: "minimal", Description: "Minimal reasoning tokens"},
		{Value: "low", Label: "low", Description: "Faster, lighter reasoning"},
		{Value: "medium", Label: "medium", Description: "Balanced reasoning"},
		{Value: "high", Label: "high", Description: "Deeper reasoning"},
		{Value: "xhigh", Label: "xhigh", Description: "Extra-deep reasoning"},
		{Value: "max", Label: "max", Description: "Maximum reasoning budget"},
	}
}

// normalizeReasoningEffort accepts only explicit reasoning levels.
func normalizeReasoningEffort(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(raw)), true
	}
	return "", false
}

// openEffortPicker opens the interactive reasoning-effort selector.
func (m *tuiModel) openEffortPicker() {
	if card := m.activeCardKind(); card != cardNone && card != cardEffort {
		m.addSystemEntry("Reasoning effort picker can only be opened after the pending interaction is resolved.")
		return
	}
	current := m.currentReasoningEffort()
	m.reasoningEffort = current
	selected := 0
	for i, opt := range reasoningEffortOptions() {
		if opt.Value == current {
			selected = i
			break
		}
	}
	// The effort picker is not a modal overlay, but opening it still closes any
	// overlay so two panels never share the screen.
	m.modals.close()
	m.effortPicker = &ActiveEffortPicker{Selected: selected}
	m.updateLayout()
}

// mutateSessionSettings updates only the requested session settings, preserving
// all other fields already stored on the session.
func (m *tuiModel) mutateSessionSettings(mutate func(*core.SessionSettings)) error {
	if m.client == nil || m.sessionID == "" {
		return nil
	}
	ctx := context.Background()
	saved, err := m.client.GetSession(ctx, m.sessionID)
	if err != nil {
		return err
	}
	settings := saved.Settings
	mutate(&settings)
	return m.client.SetSessionSettings(ctx, m.sessionID, settings)
}

// applyReasoningEffort persists and applies a reasoning-effort level.
func (m *tuiModel) applyReasoningEffort(value string) (tea.Model, tea.Cmd) {
	if err := m.mutateSessionSettings(func(settings *core.SessionSettings) {
		settings.ReasoningEffort = value
	}); err != nil {
		m.addSystemEntry("Failed to update reasoning effort: " + err.Error())
		m.updateLayout()
		return m, nil
	}
	m.reasoningEffort = value
	m.addSystemEntry(fmt.Sprintf("Reasoning effort set to `%s`.", value))
	m.updateLayout()
	return m, nil
}

// handleEffortCommand opens the interactive effort picker, or sets a level directly
// when an explicit level is supplied.
func (m *tuiModel) handleEffortCommand(parts []string) (tea.Model, tea.Cmd) {
	if len(parts) == 1 {
		m.openEffortPicker()
		return m, nil
	}
	value, ok := normalizeReasoningEffort(parts[1])
	if !ok {
		m.addSystemEntry(fmt.Sprintf("Invalid reasoning effort `%s`. Valid levels: `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`.", parts[1]))
		return m, nil
	}
	return m.applyReasoningEffort(value)
}
