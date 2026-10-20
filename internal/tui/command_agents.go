package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func (m *tuiModel) handleAgentsCommand(parts []string) (tea.Model, tea.Cmd) {
	target := ""
	if len(parts) > 1 {
		target = strings.TrimSpace(parts[1])
	}
	if !m.openAgentsPanel(target) {
		m.addSystemEntry(fmt.Sprintf("Agent `%s` not found. Use `/agents` to list agents in this session.", target))
		return m, nil
	}
	m.updateLayout()
	return m, nil
}
