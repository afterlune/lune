package tui

import (
	tea "charm.land/bubbletea/v2"
)

func (m *tuiModel) handleMemoryCommand(parts []string) (tea.Model, tea.Cmd) {
	m.addSystemEntry("Workspace memory has been decoupled from Core. Engram MCP is configured by default; run `/mcp` to manage external memory servers.")
	m.updateLayout()
	return m, nil
}
