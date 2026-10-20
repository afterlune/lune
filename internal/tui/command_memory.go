package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/luneCore/memory"
)

func (m *tuiModel) handleMemoryCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.addSystemEntry("Memory management unavailable (no active client).")
		return m, nil
	}

	ctx := context.Background()
	subcmd := "list"
	if len(parts) > 1 {
		subcmd = strings.ToLower(parts[1])
	}

	switch subcmd {
	case "list":
		query := ""
		if len(parts) > 2 {
			query = strings.TrimSpace(strings.Join(parts[2:], " "))
		}
		if query != "" {
			results, err := m.client.SearchMemories(ctx, query, 20)
			if err != nil {
				m.addSystemEntry("Failed to search memories: " + err.Error())
				return m, nil
			}
			if len(results) == 0 {
				m.addSystemEntry(fmt.Sprintf("No workspace memories matching %q found.", query))
				return m, nil
			}
			var lines []string
			lines = append(lines, fmt.Sprintf("**Workspace Memories (%d matches for %q):**", len(results), query))
			for _, r := range results {
				shortID := r.Item.ID
				if len(shortID) > 8 {
					shortID = shortID[:8]
				}
				timeStr := r.Item.UpdatedAt.Format("Jan 02 15:04")
				lines = append(lines, fmt.Sprintf("- `[%s]` **%s**: %s *(%s)*", shortID, r.Item.Category, r.Item.Content, timeStr))
			}
			m.addSystemEntry(strings.Join(lines, "\n"))
			return m, nil
		}

		items, err := m.client.ListMemories(ctx, 50)
		if err != nil {
			m.addSystemEntry("Failed to list memories: " + err.Error())
			return m, nil
		}
		if len(items) == 0 {
			m.addSystemEntry("**Workspace Memory:**\nNo workspace memories stored yet.\n\nUse `/memory add <text>` to record conventions, technical preferences, or rules for this workspace.")
			return m, nil
		}
		var lines []string
		lines = append(lines, fmt.Sprintf("**Workspace Memories (%d items):**", len(items)))
		for _, item := range items {
			shortID := item.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			timeStr := item.UpdatedAt.Format("Jan 02 15:04")
			lines = append(lines, fmt.Sprintf("- `[%s]` **%s**: %s *(%s)*", shortID, item.Category, item.Content, timeStr))
		}
		lines = append(lines, "\n*Use `/memory add <text>` to record, `/memory remove <id>` to delete.*")
		m.addSystemEntry(strings.Join(lines, "\n"))
		return m, nil

	case "add":
		if len(parts) < 3 {
			m.addSystemEntry("Usage: `/memory add [category] <content>`\nCategories: `preference`, `convention`, `decision`, `correction`, `verified_fact` (default: `preference`)")
			return m, nil
		}
		cat := memory.CategoryPreference
		contentParts := parts[2:]
		firstLower := strings.ToLower(parts[2])
		switch memory.Category(firstLower) {
		case memory.CategoryPreference, memory.CategoryConvention, memory.CategoryDecision, memory.CategoryCorrection, memory.CategoryVerifiedFact:
			cat = memory.Category(firstLower)
			contentParts = parts[3:]
		}
		content := strings.TrimSpace(strings.Join(contentParts, " "))
		if content == "" {
			m.addSystemEntry("Memory content cannot be empty.")
			return m, nil
		}
		item, err := m.client.AddMemory(ctx, memory.Input{
			Category:   cat,
			Content:    content,
			Importance: 3,
		})
		if err != nil {
			m.addSystemEntry("Failed to add memory: " + err.Error())
			return m, nil
		}
		shortID := item.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		m.addSystemEntry(fmt.Sprintf("Added workspace memory `[%s]` (**%s**): %s", shortID, item.Category, item.Content))
		return m, nil

	case "remove", "delete":
		if len(parts) < 3 {
			m.addSystemEntry("Usage: `/memory remove <id|prefix>`")
			return m, nil
		}
		target := parts[2]
		items, err := m.client.ListMemories(ctx, 200)
		if err != nil {
			m.addSystemEntry("Failed to query memories: " + err.Error())
			return m, nil
		}
		var matches []memory.Item
		for _, it := range items {
			if strings.HasPrefix(strings.ToLower(it.ID), strings.ToLower(target)) {
				matches = append(matches, it)
			}
		}
		if len(matches) == 0 {
			m.addSystemEntry(fmt.Sprintf("Memory item %q not found.", target))
			return m, nil
		}
		if len(matches) > 1 {
			var ids []string
			for _, match := range matches {
				s := match.ID
				if len(s) > 8 {
					s = s[:8]
				}
				ids = append(ids, s)
			}
			m.addSystemEntry(fmt.Sprintf("Memory ID prefix %q is ambiguous (%d matches: %s).", target, len(matches), strings.Join(ids, ", ")))
			return m, nil
		}
		match := matches[0]
		if err := m.client.DeleteMemory(ctx, match.ID); err != nil {
			m.addSystemEntry("Failed to delete memory: " + err.Error())
			return m, nil
		}
		shortID := match.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		m.addSystemEntry(fmt.Sprintf("Deleted workspace memory `[%s]`: %s", shortID, match.Content))
		return m, nil

	case "clear":
		hasForce := false
		for _, p := range parts[2:] {
			if p == "--force" || p == "confirm" || p == "-f" {
				hasForce = true
				break
			}
		}
		if !hasForce {
			m.addSystemEntry("Are you sure you want to clear all workspace memories?\nRun `/memory clear --force` to confirm.")
			return m, nil
		}
		if err := m.client.ClearMemories(ctx); err != nil {
			m.addSystemEntry("Failed to clear memories: " + err.Error())
			return m, nil
		}
		m.addSystemEntry("Cleared all workspace memories.")
		return m, nil

	case "help":
		lines := []string{
			"**Workspace Memory Commands:**",
			"- `/memory` or `/memory list`: List all workspace memories",
			"- `/memory list <query>`: Search memories by text",
			"- `/memory add [category] <text>`: Add a workspace convention or preference",
			"- `/memory remove <id|prefix>`: Remove a memory item by ID or prefix",
			"- `/memory clear --force`: Clear all workspace memories",
			"\n*Categories: `preference`, `convention`, `decision`, `correction`, `verified_fact`*",
		}
		m.addSystemEntry(strings.Join(lines, "\n"))
		return m, nil

	default:
		// If unknown subcmd, treat as a query search
		query := strings.TrimSpace(strings.Join(parts[1:], " "))
		results, err := m.client.SearchMemories(ctx, query, 20)
		if err != nil {
			m.addSystemEntry("Failed to search memories: " + err.Error())
			return m, nil
		}
		if len(results) == 0 {
			m.addSystemEntry(fmt.Sprintf("No memories matching %q found.", query))
			return m, nil
		}
		var lines []string
		lines = append(lines, fmt.Sprintf("**Workspace Memories (%d matches for %q):**", len(results), query))
		for _, r := range results {
			shortID := r.Item.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			timeStr := r.Item.UpdatedAt.Format("Jan 02 15:04")
			lines = append(lines, fmt.Sprintf("- `[%s]` **%s**: %s *(%s)*", shortID, r.Item.Category, r.Item.Content, timeStr))
		}
		m.addSystemEntry(strings.Join(lines, "\n"))
		return m, nil
	}
}
