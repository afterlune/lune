// Package engram integrates the configured Engram MCP server with Lune's
// automatic memory workflow.
package engram

import core "github.com/afterlune/luneCore"

const ServerName = "engram"

const memoryInstructions = `Use the configured Engram MCP server for durable project memory.

At the beginning of a new Lune conversation, before substantive work, call mem_current_project and mem_context to confirm the current project and load prior context. Call mem_search when earlier decisions may affect the current task.

After a substantive task succeeds, call mem_save only for durable, verified decisions, discoveries, conventions, constraints, or useful next steps. Use concise, searchable content and a stable topic key. Do not save routine exchanges, raw transcripts, transient status, credentials, or secrets. If the task produced no durable knowledge, do not save anything.

If Engram tools are not visible, discover the mcp tool category before using them. Follow the active tool permissions and Plan mode: do not perform memory writes while planning or after a denied operation. Engram failures must not prevent the user's task from completing.`

// MCPController is the part of Core needed to enable a configured MCP server.
type MCPController interface {
	MCPServers() []core.MCPServerStatus
	EnableMCPServer(string) error
}

// Configured reports whether the project's MCP configuration includes Engram.
func Configured(servers map[string]core.MCPServerConfig) bool {
	_, ok := servers[ServerName]
	return ok
}

// Instructions returns the automatic memory protocol only when Engram is
// configured for this Core.
func Instructions(servers map[string]core.MCPServerConfig) string {
	if !Configured(servers) {
		return ""
	}
	return memoryInstructions
}

// EnsureEnabled connects the configured Engram server. A restored connection
// is left alone; a restored but disconnected server is retried.
func EnsureEnabled(client MCPController, servers map[string]core.MCPServerConfig) error {
	if !Configured(servers) {
		return nil
	}
	for _, status := range client.MCPServers() {
		if status.Name == ServerName && status.Enabled && status.Connected {
			return nil
		}
	}
	return client.EnableMCPServer(ServerName)
}

// StatusMessage describes the memory integration for the /memory command.
func StatusMessage(servers []core.MCPServerStatus) string {
	for _, server := range servers {
		if server.Name != ServerName {
			continue
		}
		if server.Enabled && server.Connected {
			return "Engram MCP is connected. Lune asks the agent to load relevant project context at the start of a conversation and save durable findings after significant tasks. Routine exchanges are not saved. Run `/mcp disable engram` to stop it for this runtime, or remove `mcpServers.engram` from the config to keep it off."
		}
		return "Engram MCP is configured but not connected. Lune can continue without automatic memory; check `/mcp list` or run `/mcp enable engram` to retry. Remove `mcpServers.engram` from the config to keep it off."
	}
	return "Engram MCP is not configured, so automatic memory is off. Add `mcpServers.engram` to the config to enable it."
}
