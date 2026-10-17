package tui

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

var (
	reBold       = regexp.MustCompile(`\*\*(.*?)\*\*`)
	reInlineCode = regexp.MustCompile("`([^`]+)`")
	reFileRef    = regexp.MustCompile(`\b([a-zA-Z0-9_\-\.\/\\]+\.(?:go|rs|py|ts|js|json|md|html|css|toml|ya?ml)(?::\d+)?)\b`)
	reBullet     = regexp.MustCompile(`^(\s*)[-*+]\s+`)
	reNumbered   = regexp.MustCompile(`^(\s*\d+\.)\s+`)
)

// RenderMarkdown renders markdown text using terminal styles.
func RenderMarkdown(text string, width int) string {
	lines := strings.Split(text, "\n")
	var result []string
	inCodeBlock := false

	codeBlockStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme().CardBg)).
		Foreground(lipgloss.Color(activeTheme().TextPrimary))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}

		if inCodeBlock {
			pad := max(0, width-lipgloss.Width(line)-2)
			result = append(result, codeBlockStyle.Render("  "+line+strings.Repeat(" ", pad)))
			continue
		}

		if strings.HasPrefix(trimmed, "# ") {
			title := strings.TrimPrefix(trimmed, "# ")
			style := lipgloss.NewStyle().Bold(true).Underline(true).Foreground(lipgloss.Color(activeTheme().TextPrimary))
			result = append(result, style.Render(title))
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			title := strings.TrimPrefix(trimmed, "## ")
			style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(activeTheme().TextPrimary))
			result = append(result, style.Render(title))
			continue
		}
		if strings.HasPrefix(trimmed, "### ") {
			title := strings.TrimPrefix(trimmed, "### ")
			style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(activeTheme().TextMuted))
			result = append(result, style.Render(title))
			continue
		}

		// Inline formatting
		formatted := formatInline(line)
		result = append(result, formatted)
	}

	return strings.Join(result, "\n")
}

func formatInline(line string) string {
	// Inline code `...`
	line = reInlineCode.ReplaceAllStringFunc(line, func(m string) string {
		content := strings.Trim(m, "`")
		style := lipgloss.NewStyle().
			Background(lipgloss.Color(activeTheme().CardBg)).
			Foreground(lipgloss.Color(activeTheme().TextPrimary))
		return style.Render(" " + content + " ")
	})

	// Bold **...**
	line = reBold.ReplaceAllStringFunc(line, func(m string) string {
		content := strings.Trim(m, "*")
		style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(activeTheme().TextPrimary))
		return style.Render(content)
	})

	// File references e.g. server.py:672
	line = reFileRef.ReplaceAllStringFunc(line, func(m string) string {
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue)).Underline(true)
		return style.Render(m)
	})

	return line
}
