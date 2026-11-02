package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/afterlune/lune/internal/engram"
)

func (m *tuiModel) handleMemoryCommand(parts []string) (tea.Model, tea.Cmd) {
	message := engram.StatusMessage(nil)
	if m.client != nil {
		message = engram.StatusMessage(m.client.MCPServers())
	}
	m.addSystemEntry(message)
	m.updateLayout()
	return m, nil
}
