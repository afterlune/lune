package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type mcpAuthDoneMsg struct {
	generation uint64
	name       string
	err        error
}

type ActiveMCPServerPicker struct {
	Action   string
	Names    []string
	Selected int
}

func (m *tuiModel) openMCPServerPicker(action string) (tea.Model, tea.Cmd) {
	servers := m.client.MCPServers()
	var names []string
	for _, server := range servers {
		if action == "enable" && server.Enabled {
			continue
		}
		if action == "disable" && !server.Enabled {
			continue
		}
		names = append(names, server.Name)
	}
	if len(names) == 0 {
		m.addSystemEntry(fmt.Sprintf("No MCP servers are available to %s.", action))
		return m, nil
	}
	m.modals.open()
	m.modals.mcp.picker = &ActiveMCPServerPicker{Action: action, Names: names}
	m.input.autocompleteOpen = false
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) handleMCPServerPickerKeyPress(keyStr string) (tea.Model, tea.Cmd) {
	if m.modals.mcp.picker == nil {
		return m, nil
	}
	switch strings.ToLower(keyStr) {
	case "esc", "q":
		m.modals.mcp.picker = nil
		m.updateLayout()
		return m, nil
	case "up", "k":
		m.modals.mcp.picker.Selected = (m.modals.mcp.picker.Selected - 1 + len(m.modals.mcp.picker.Names)) % len(m.modals.mcp.picker.Names)
		m.updateLayout()
		return m, nil
	case "down", "j":
		m.modals.mcp.picker.Selected = (m.modals.mcp.picker.Selected + 1) % len(m.modals.mcp.picker.Names)
		m.updateLayout()
		return m, nil
	case "enter":
		name := m.modals.mcp.picker.Names[m.modals.mcp.picker.Selected]
		action := m.modals.mcp.picker.Action
		m.modals.mcp.picker = nil
		return m.handleMCPCommand([]string{"/mcp", action, name})
	default:
		return m, nil
	}
}

func (m *tuiModel) startMCPAuthentication(name string) tea.Cmd {
	if m.modals.mcp.authCancel != nil {
		m.addSystemEntry("An MCP authorization is already in progress. Press Esc to cancel it first.")
		return nil
	}
	if m.modals.mcp.authRunner == nil {
		m.modals.mcp.authRunner = m.client.AuthenticateMCPServer
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.modals.mcp.authGeneration++
	generation := m.modals.mcp.authGeneration
	m.modals.mcp.authName = name
	m.modals.mcp.authCancel = cancel
	m.updateLayout()
	runner := m.modals.mcp.authRunner
	return func() tea.Msg {
		return mcpAuthDoneMsg{generation: generation, name: name, err: runner(ctx, name)}
	}
}

func (m *tuiModel) cancelMCPAuthentication(report bool) {
	if m.modals.mcp.authCancel == nil {
		return
	}
	name := m.modals.mcp.authName
	m.modals.mcp.authGeneration++
	m.modals.mcp.authCancel()
	m.modals.mcp.authCancel = nil
	m.modals.mcp.authName = ""
	if report {
		m.addSystemEntry(fmt.Sprintf("Cancelled MCP authorization for `%s`.", name))
	}
	m.updateLayout()
}

// handleMCPCommand handles the /mcp command.
func (m *tuiModel) handleMCPCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.addSystemEntry("MCP management is unavailable.")
		return m, nil
	}
	subcommand := "list"
	if len(parts) > 1 {
		subcommand = strings.ToLower(parts[1])
	}
	switch subcommand {
	case "list":
		servers := m.client.MCPServers()
		if len(servers) == 0 {
			m.addSystemEntry("No MCP servers configured in `mcpServers` (config.json).")
			return m, nil
		}
		var lines []string
		lines = append(lines, "**Configured MCP Servers:**")
		for _, s := range servers {
			status := "Disabled"
			if s.Enabled {
				status = "Enabled"
			}
			conn := "Disconnected"
			if s.Connected {
				conn = "Connected"
			}
			endpoint := s.Endpoint
			if endpoint == "" {
				endpoint = s.Command
			}
			errStr := ""
			if s.LastError != "" {
				errStr = fmt.Sprintf(" · *Error: %s*", s.LastError)
			}
			lines = append(lines, fmt.Sprintf("- **`%s`** (`%s`): %s, %s (%d tools)%s\n  Target: `%s`",
				s.Name, s.Transport, status, conn, s.ToolCount, errStr, endpoint))
		}
		m.addSystemEntry(strings.Join(lines, "\n"))
	case "enable":
		if m.taskBusy() {
			m.addSystemEntry("Cannot change MCP servers while a task is running.")
			return m, nil
		}
		if len(parts) < 3 {
			return m.openMCPServerPicker("enable")
		}
		name := parts[2]
		if err := m.client.EnableMCPServer(name); err != nil {
			m.addSystemEntry(fmt.Sprintf("Failed to enable MCP server `%s`: %v", name, err))
		} else {
			m.addSystemEntry(fmt.Sprintf("MCP server `%s` enabled and tools registered.", name))
		}
	case "disable":
		if m.taskBusy() {
			m.addSystemEntry("Cannot change MCP servers while a task is running.")
			return m, nil
		}
		if len(parts) < 3 {
			return m.openMCPServerPicker("disable")
		}
		name := parts[2]
		if err := m.client.DisableMCPServer(name); err != nil {
			m.addSystemEntry(fmt.Sprintf("Failed to disable MCP server `%s`: %v", name, err))
		} else {
			m.addSystemEntry(fmt.Sprintf("MCP server `%s` disabled and disconnected.", name))
		}
	case "auth":
		if m.taskBusy() {
			m.addSystemEntry("Cannot authorize an MCP server while a task is running.")
			return m, nil
		}
		if len(parts) < 3 {
			return m.openMCPServerPicker("auth")
		}
		name := parts[2]
		m.addSystemEntry(fmt.Sprintf("Authenticating MCP server `%s`; complete the browser flow or press Esc to cancel.", name))
		return m, m.startMCPAuthentication(name)
	default:
		m.addSystemEntry(fmt.Sprintf("Unknown MCP subcommand `%s`. Usage: `/mcp [list|enable|disable|auth]`", subcommand))
	}
	return m, nil
}
