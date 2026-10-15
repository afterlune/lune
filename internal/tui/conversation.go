package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

func plainConversationText(markdown string) string {
	markdown = strings.ReplaceAll(markdown, "\r\n", "\n")
	markdown = strings.ReplaceAll(markdown, "\r", "\n")
	lines := strings.Split(markdown, "\n")
	inCode := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			lines[index] = ""
			continue
		}
		if inCode {
			lines[index] = "  " + line
			continue
		}
		switch {
		case strings.HasPrefix(trimmed, "### "):
			line = strings.TrimPrefix(trimmed, "### ")
		case strings.HasPrefix(trimmed, "## "):
			line = strings.TrimPrefix(trimmed, "## ")
		case strings.HasPrefix(trimmed, "# "):
			line = strings.TrimPrefix(trimmed, "# ")
		case strings.HasPrefix(trimmed, "> "):
			line = strings.TrimPrefix(trimmed, "> ")
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			line = "• " + trimmed[2:]
		}
		line = strings.ReplaceAll(line, "**", "")
		line = stripPairedAsterisks(line)
		line = strings.ReplaceAll(line, "__", "")
		line = strings.ReplaceAll(line, "~~", "")
		line = strings.ReplaceAll(line, "`", "")
		lines[index] = line
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

func stripPairedAsterisks(line string) string {
	for {
		start := strings.IndexByte(line, '*')
		if start < 0 {
			return line
		}
		relativeEnd := strings.IndexByte(line[start+1:], '*')
		if relativeEnd < 0 {
			return line
		}
		end := start + 1 + relativeEnd
		line = line[:start] + line[start+1:end] + line[end+1:]
	}
}

func formatScrollbackEntry(entry tuiEntry, width int, theme themeName) string {
	return formatScrollbackEntryWithColors(entry, width, colorsFor(theme))
}

func formatScrollbackEntryWithColors(entry tuiEntry, width int, colors themeColors) string {
	body := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Foreground)).Render(entry.text)
	prefix, color := "", colors.Muted
	switch entry.role {
	case "you":
		prefix, color = "› ", colors.Accent
	case "tool":
		prefix = "• "
	case "notice", "queued":
		prefix = "· "
	case "error":
		prefix, color = "! ", colors.Error
	case "permission":
		prefix, color = "! ", colors.Warning
	case "question":
		prefix, color = "? ", colors.Warning
	case "assistant":
		if entry.incomplete {
			prefix = "Partial · "
		}
	}
	if prefix != "" {
		body = lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(prefix) + body
	}
	return lipgloss.NewStyle().Width(max(1, width)).Render(body)
}

func formatTranscriptEntry(entry tuiEntry, colors themeColors) string {
	text := plainConversationText(entry.text)
	if text == "" {
		return ""
	}
	bodyColor := colors.Assistant
	prefix, prefixColor := "", colors.Muted
	switch entry.role {
	case "you":
		prefix, prefixColor = "› ", colors.User
	case "tool":
		prefix = "• "
	case "notice", "queued":
		prefix = "· "
	case "error":
		prefix, prefixColor = "! ", colors.Error
	case "permission":
		prefix, prefixColor = "! ", colors.Warning
	case "question":
		prefix, prefixColor = "? ", colors.Warning
	case "assistant":
		if entry.incomplete {
			prefix = "Partial · "
		}
	default:
		bodyColor = colors.Foreground
	}
	body := lipgloss.NewStyle().Foreground(lipgloss.Color(bodyColor)).Render(text)
	if prefix != "" {
		body = lipgloss.NewStyle().Foreground(lipgloss.Color(prefixColor)).Render(prefix) + body
	}
	return body
}

func formatConversationTranscript(entries []tuiEntry, colors themeColors) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		if rendered := formatTranscriptEntry(entry, colors); rendered != "" {
			lines = append(lines, rendered)
		}
	}
	return strings.Join(lines, "\n\n")
}
