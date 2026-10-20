package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

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
		if m.running {
			m.addSystemEntry("Cannot change MCP servers while a task is running.")
			return m, nil
		}
		if len(parts) < 3 {
			m.addSystemEntry("Usage: `/mcp enable <server-name>`")
			return m, nil
		}
		name := parts[2]
		if err := m.client.EnableMCPServer(name); err != nil {
			m.addSystemEntry(fmt.Sprintf("Failed to enable MCP server `%s`: %v", name, err))
		} else {
			m.addSystemEntry(fmt.Sprintf("MCP server `%s` enabled and tools registered.", name))
		}
	case "disable":
		if m.running {
			m.addSystemEntry("Cannot change MCP servers while a task is running.")
			return m, nil
		}
		if len(parts) < 3 {
			m.addSystemEntry("Usage: `/mcp disable <server-name>`")
			return m, nil
		}
		name := parts[2]
		if err := m.client.DisableMCPServer(name); err != nil {
			m.addSystemEntry(fmt.Sprintf("Failed to disable MCP server `%s`: %v", name, err))
		} else {
			m.addSystemEntry(fmt.Sprintf("MCP server `%s` disabled and disconnected.", name))
		}
	case "auth":
		if m.running {
			m.addSystemEntry("Cannot authorize an MCP server while a task is running.")
			return m, nil
		}
		if len(parts) < 3 {
			m.addSystemEntry("Usage: `/mcp auth <server-name>`")
			return m, nil
		}
		name := parts[2]
		if err := m.client.AuthenticateMCPServer(context.Background(), name); err != nil {
			m.addSystemEntry(fmt.Sprintf("Failed to authenticate MCP server `%s`: %v", name, err))
		} else {
			m.addSystemEntry(fmt.Sprintf("MCP server `%s` authorized successfully.", name))
		}
	default:
		m.addSystemEntry(fmt.Sprintf("Unknown MCP subcommand `%s`. Usage: `/mcp [list|enable|disable|auth]`", subcommand))
	}
	return m, nil
}
