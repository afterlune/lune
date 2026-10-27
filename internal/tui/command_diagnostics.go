package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/afterlune/lune/internal/goalcmd"
)

// renderStatusTable renders a status table matching Screenshot 12.
func (m *tuiModel) renderStatusTable() string {
	modelName := m.currentModel
	if modelName == "" && m.client != nil {
		modelName = m.client.ModelForSession(context.Background(), m.sessionID)
	}
	if modelName == "" {
		modelName = "<default>"
	}
	permStr := string(m.permissionMode)
	title := m.sessionTitle
	if title == "" {
		title = "<none>"
	}

	effort := m.currentReasoningEffort()

	sessionLabel := m.sessionID
	if sessionLabel == "" {
		sessionLabel = "(pending — created with your first message)"
	}

	lines := []string{
		fmt.Sprintf("Model:          %s", modelName),
		fmt.Sprintf("Reasoning Effort: %s", effort),
		fmt.Sprintf("Directory:      %s", m.workspace),
		fmt.Sprintf("Permissions:    %s", permStr),
		fmt.Sprintf("Plan mode:      %t", m.planMode),
		fmt.Sprintf("Thread name:    %s", title),
		fmt.Sprintf("Session:        %s", sessionLabel),
		fmt.Sprintf("Theme:          %s", CurrentThemeMode()),
		fmt.Sprintf("Task:           %s", m.taskStatusLabel()),
		fmt.Sprintf("Queued:         %d waiting", len(m.run.queuedTurns)),
	}

	if m.run.goal != nil {
		lines = append(lines, goalcmd.Summary(m.run.goal))
	}
	if m.usage.contextWindow > 0 {
		var inputLabel string
		var usedCtx int64
		if m.usage.lastInputTokens > 0 {
			usedCtx = m.usage.lastInputTokens
			inputLabel = "active prompt"
		} else {
			usedCtx = m.usage.estimatedInputTokens
			inputLabel = "est. input"
		}
		pct := 0.0
		if usedCtx > 0 {
			pct = float64(usedCtx) / float64(m.usage.contextWindow) * 100.0
		}
		lines = append(lines, fmt.Sprintf("Context Window: %s tokens (%s: %s, %.1f%% full)",
			formatTokens(m.usage.contextWindow), inputLabel, formatTokens(usedCtx), pct))
	}
	if label := m.compactionSettingsLabel(); label != "" {
		lines = append(lines, "Auto-compaction: "+label)
	}
	if m.usage.compacting {
		lines = append(lines, "Compaction:      in progress…")
	}
	if m.usage.lastInputTokens > 0 {
		lines = append(lines, "Last Prompt Cache: "+formatPromptCacheBreakdown(m.usage.lastInputTokens, m.usage.lastCachedTokens))
	}
	if m.usage.totalInputTokens > 0 || m.usage.totalOutputTokens > 0 {
		lines = append(lines, fmt.Sprintf("Total Tokens:   in: %s (cached: %s) · out: %s (reasoning: %s)",
			formatTokens(m.usage.totalInputTokens), formatTokens(m.usage.cachedTokens),
			formatTokens(m.usage.totalOutputTokens), formatTokens(m.usage.reasoningTokens)))
	}

	if m.runtime != nil {
		if cmds, err := m.runtime.ListCommands(context.Background()); err == nil && len(cmds) > 0 {
			active := 0
			for _, c := range cmds {
				if c.Status.Active() {
					active++
				}
			}
			lines = append(lines, fmt.Sprintf("Background Ps:  %d active (%d total)", active, len(cmds)))
		}
	}

	totalSubs := m.totalSubagentsCount()
	activeSubs := m.activeSubagentsCount()
	lines = append(lines, fmt.Sprintf("Subagents:      %d active (%d in session); %s", activeSubs, totalSubs, m.delegationLimitsLabel()))

	return strings.Join(lines, "\n")
}

func formatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	in := fmt.Sprintf("%d", n)
	var out []byte
	rem := len(in) % 3
	if rem > 0 {
		out = append(out, in[:rem]...)
		if len(in) > rem {
			out = append(out, ',')
		}
	}
	for i := rem; i < len(in); i += 3 {
		out = append(out, in[i:i+3]...)
		if i+3 < len(in) {
			out = append(out, ',')
		}
	}
	return string(out)
}

func (m *tuiModel) renderContextDiagnostics() string {
	var lines []string
	lines = append(lines, "**Context Status & Diagnostics**\n")

	// 1. Workspace & Rules
	ws := m.workspace
	if ws == "" && m.client != nil {
		ws = m.client.Workspace()
	}
	permStr := string(m.permissionMode)
	lines = append(lines, "**Workspace & Permissions:**")
	lines = append(lines, fmt.Sprintf("- Directory: `%s`", ws))
	lines = append(lines, fmt.Sprintf("- Permissions: `%s`", permStr))

	ruleFiles := []string{"AGENTS.md", "CLAUDE.md"}
	var foundRules []string
	if ws != "" {
		for _, rf := range ruleFiles {
			p := filepath.Join(ws, rf)
			if fi, err := os.Stat(p); err == nil {
				foundRules = append(foundRules, fmt.Sprintf("`%s`: Active (%s bytes)", rf, formatTokens(fi.Size())))
			}
		}
	}
	if len(foundRules) > 0 {
		lines = append(lines, fmt.Sprintf("- Rules: %s", strings.Join(foundRules, ", ")))
	} else {
		lines = append(lines, "- Rules: None detected in workspace root")
	}

	// 2. Session & Model
	modelName := m.currentModel
	if modelName == "" && m.client != nil {
		modelName = m.client.ModelForSession(context.Background(), m.sessionID)
	}
	if modelName == "" {
		modelName = "<default>"
	}
	title := m.sessionTitle
	if title == "" {
		title = "<none>"
	}
	lines = append(lines, "\n**Session & Model:**")
	lines = append(lines, fmt.Sprintf("- Active Model: `%s`", modelName))
	sessionIDLabel := m.sessionID
	if sessionIDLabel == "" {
		sessionIDLabel = "(pending — created with your first message)"
	}
	lines = append(lines, fmt.Sprintf("- Session ID: `%s`", sessionIDLabel))
	lines = append(lines, fmt.Sprintf("- Thread: `%s`", title))

	// 3. Capabilities & Tools
	lines = append(lines, "\n**Capabilities & Tools:**")
	mcpServerCount := 0
	mcpToolCount := 0
	if m.client != nil {
		for _, s := range m.client.MCPServers() {
			if s.Enabled {
				mcpServerCount++
				mcpToolCount += s.ToolCount
			}
		}
	}
	if mcpServerCount > 0 {
		lines = append(lines, fmt.Sprintf("- MCP Servers: %d active (%d tools registered)", mcpServerCount, mcpToolCount))
	} else {
		lines = append(lines, "- MCP Servers: None active")
	}
	if m.planMode {
		lines = append(lines, "- Plan Mode: Active (restricted to read-only tools)")
	} else {
		lines = append(lines, "- Plan Mode: Inactive (all permitted tools available)")
	}

	// 4. Context Window & Token Utilization
	lines = append(lines, "\n**Context Window Utilization:**")
	if m.usage.contextWindow > 0 {
		ratio := 0.0
		if m.usage.estimatedInputTokens > 0 {
			ratio = float64(m.usage.estimatedInputTokens) / float64(m.usage.contextWindow)
		}
		pct := ratio * 100.0
		progressBar := renderProgressBar(ratio, 20)
		lines = append(lines, fmt.Sprintf("- Budget: %s %.1f%% (%s / %s tokens)",
			progressBar, pct, formatTokens(m.usage.estimatedInputTokens), formatTokens(m.usage.contextWindow)))
	} else {
		lines = append(lines, "- Budget: Standard context window (no provider limit reported yet)")
	}
	if label := m.compactionSettingsLabel(); label != "" {
		lines = append(lines, "- Auto-compaction: "+label)
	}
	lines = append(lines, fmt.Sprintf("- Session Cumulative Tokens: in: %s (cached: %s) · out: %s (reasoning: %s)",
		formatTokens(m.usage.totalInputTokens), formatTokens(m.usage.cachedTokens),
		formatTokens(m.usage.totalOutputTokens), formatTokens(m.usage.reasoningTokens)))
	if m.usage.lastInputTokens > 0 {
		lines = append(lines, "- Last prompt cache: "+formatPromptCacheBreakdown(m.usage.lastInputTokens, m.usage.lastCachedTokens))
		if m.usage.lastRequestID != "" {
			lines = append(lines, fmt.Sprintf("- Last main request: %s (attempt %d, input %s)", m.usage.lastRequestID, m.usage.lastRequestAttempt, formatTokens(m.usage.lastInputTokens)))
			changes := strings.Join(m.usage.lastRequestChanges, ", ")
			if changes == "" {
				changes = "no local prefix changes"
			}
			lines = append(lines, "- Local request changes: "+changes+" (not provider cache eviction reasons)")
		}
	}

	// 5. Conversation Metrics
	userTurns := 0
	asstTurns := 0
	toolTurns := 0
	for _, e := range m.entries {
		switch e.Kind {
		case KindUser:
			userTurns++
		case KindAssistant:
			asstTurns++
		case KindTool:
			toolTurns++
		}
	}
	lines = append(lines, fmt.Sprintf("- Conversation turns: %d total (%d user, %d assistant, %d tool calls)",
		len(m.entries), userTurns, asstTurns, toolTurns))
	totalSubs := m.totalSubagentsCount()
	activeSubs := m.activeSubagentsCount()
	lines = append(lines, fmt.Sprintf("- Subagents: %d active (%d total in session); %s; use `/agents` to inspect", activeSubs, totalSubs, m.delegationLimitsLabel()))

	// 6. Workspace Memory
	lines = append(lines, "\n**Workspace Memory:**")
	if m.client != nil {
		if items, err := m.client.ListMemories(context.Background(), 100); err == nil {
			if len(items) > 0 {
				lines = append(lines, fmt.Sprintf("- Stored Memories: %d active items (use `/memory` to inspect or manage)", len(items)))
			} else {
				lines = append(lines, "- Stored Memories: None yet (use `/memory add <text>` to record conventions)")
			}
		} else {
			lines = append(lines, fmt.Sprintf("- Memory Engine: unavailable (%v)", err))
		}
	} else {
		lines = append(lines, "- Stored Memories: Unavailable (no client)")
	}

	// 7. Background Processes
	lines = append(lines, "\n**Background Processes:**")
	if m.runtime != nil {
		if cmds, err := m.runtime.ListCommands(context.Background()); err == nil {
			active := 0
			for _, c := range cmds {
				if c.Status.Active() {
					active++
				}
			}
			if len(cmds) > 0 {
				lines = append(lines, fmt.Sprintf("- Processes: %d active (%d total, use `/ps` to inspect)", active, len(cmds)))
			} else {
				lines = append(lines, "- Processes: None in current session")
			}
		} else {
			lines = append(lines, fmt.Sprintf("- Process Manager: unavailable (%v)", err))
		}
	} else {
		lines = append(lines, "- Processes: None in current session")
	}

	return strings.Join(lines, "\n")
}

func formatPromptCacheBreakdown(inputTokens, cachedTokens int64) string {
	if cachedTokens < 0 {
		cachedTokens = 0
	}
	if cachedTokens > inputTokens {
		cachedTokens = inputTokens
	}
	missed := inputTokens - cachedTokens
	rate := 0.0
	if inputTokens > 0 {
		rate = float64(cachedTokens) * 100 / float64(inputTokens)
	}
	return fmt.Sprintf("%s cached, %s uncached (%.1f%% hit)", formatTokens(cachedTokens), formatTokens(missed), rate)
}

func renderProgressBar(ratio float64, width int) string {
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio * float64(width))
	if filled > width {
		filled = width
	}
	empty := width - filled
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", empty) + "]"
}
