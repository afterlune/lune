package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/lune/internal/terminaltext"
	"github.com/afterlune/luneCore/capability"
)

func (m *tuiModel) handleCapabilityCommand(parts []string) (*tuiModel, tea.Cmd) {
	if m.client == nil {
		m.addSystemEntry("Generated tool management is unavailable (no active client).")
		return m, nil
	}
	command := "list"
	if len(parts) > 1 {
		command = strings.ToLower(parts[1])
	}
	items, err := m.client.ListGeneratedTools(context.Background(), 500, true)
	if err != nil {
		m.addSystemEntry("Could not load generated tools: " + err.Error())
		return m, nil
	}
	switch command {
	case "list":
		query := strings.ToLower(strings.TrimSpace(strings.Join(parts[2:], " ")))
		var filtered []capability.GeneratedTool
		for _, item := range items {
			if query == "" || strings.Contains(strings.ToLower(item.Name+" "+item.Description+" "+string(item.Purpose)+" "+strings.Join(item.Tags, " ")), query) {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) == 0 {
			m.addSystemEntry("**Generated Tools:**\nNo saved generated tools found.")
			return m, nil
		}
		var builder strings.Builder
		fmt.Fprintf(&builder, "**Generated Tools (%d):**\n", len(filtered))
		for _, item := range filtered {
			status := "enabled"
			if !item.Enabled {
				status = "disabled"
			}
			id := item.ID
			if len(id) > 8 {
				id = id[:8]
			}
			fmt.Fprintf(&builder, "- [%s] **%s** · %s · %s · tools: %s\n  %s\n", id,
				capabilityMarkdownInline(item.Name), item.Purpose, status,
				capabilityMarkdownInline(strings.Join(item.Dependencies, ", ")), capabilityMarkdownInline(item.Description))
		}
		builder.WriteString("\nUse `/capability enable <id>`, `/capability disable <id>`, or `/capability delete <id>`.")
		m.addSystemEntry(builder.String())
	case "enable", "disable", "delete":
		if len(parts) < 3 {
			m.addSystemEntry(fmt.Sprintf("Usage: `/capability %s <id|prefix>`", command))
			return m, nil
		}
		target := strings.ToLower(strings.TrimSpace(parts[2]))
		var matches []capability.GeneratedTool
		for _, item := range items {
			if strings.HasPrefix(strings.ToLower(item.ID), target) {
				matches = append(matches, item)
			}
		}
		if len(matches) == 0 {
			m.addSystemEntry(fmt.Sprintf("Generated tool `%s` was not found.", parts[2]))
			return m, nil
		}
		if len(matches) > 1 {
			m.addSystemEntry(fmt.Sprintf("Generated tool ID prefix `%s` is ambiguous (%d matches).", parts[2], len(matches)))
			return m, nil
		}
		item := matches[0]
		switch command {
		case "enable", "disable":
			enabled := command == "enable"
			if err := m.client.SetGeneratedToolEnabled(context.Background(), item.ID, enabled); err != nil {
				m.addSystemEntry("Could not update generated tool: " + err.Error())
				return m, nil
			}
			status := "enabled"
			if !enabled {
				status = "disabled"
			}
			m.addSystemEntry(fmt.Sprintf("Generated tool **%s** is now %s.", item.Name, status))
		case "delete":
			if err := m.client.DeleteGeneratedTool(context.Background(), item.ID); err != nil {
				m.addSystemEntry("Could not delete generated tool: " + err.Error())
				return m, nil
			}
			m.addSystemEntry(fmt.Sprintf("Deleted generated tool **%s**.", item.Name))
		}
	case "help":
		m.addSystemEntry("**Generated Tool Commands:**\n- `/capability list [query]` — list or search generated tools\n- `/capability enable <id>` — enable a generated tool\n- `/capability disable <id>` — disable a generated tool\n- `/capability delete <id>` — delete a generated tool")
	default:
		m.addSystemEntry(fmt.Sprintf("Unknown generated tool command `%s`. Use `/capability help`.", command))
	}
	return m, nil
}

func capabilityMarkdownInline(value string) string {
	value = terminaltext.Sanitize(strings.Join(strings.Fields(value), " "))
	value = strings.NewReplacer("\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "<", "\\<", ">", "\\>").Replace(value)
	return value
}
