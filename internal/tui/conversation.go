package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

const (
	wideConversationWidth  = 112
	wideConversationHeight = 24
)

func (m *tuiModel) modernConversation() bool {
	return m.preferences.Theme == themeDark || m.preferences.Theme == themeLight
}

func (m *tuiModel) wideConversation() bool {
	return m.modernConversation() && m.width >= wideConversationWidth && m.height >= wideConversationHeight
}

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
	colors := colorsFor(theme)
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
