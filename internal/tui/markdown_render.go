package tui

import "github.com/afterlune/lune/internal/tui/markdown"

func renderMarkdown(text string, width int) string {
	return renderMarkdownWithAccent(text, width, activeTheme().AccentBlue)
}

func renderPlanMarkdown(text string, width int) string {
	return renderMarkdownWithAccent(text, width, activeTheme().AccentSoft)
}

func renderMarkdownWithAccent(text string, width int, accent string) string {
	theme := activeTheme()
	return markdown.Render(text, width, markdown.Palette{
		TextPrimary:  theme.TextPrimary,
		TextMuted:    theme.TextMuted,
		AccentBlue:   accent,
		InlineCodeFg: theme.InlineCodeFg,
		CodeBlockBg:  theme.CodeBlockBg,
		Border:       theme.Border,
	})
}
