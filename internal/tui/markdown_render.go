package tui

import "github.com/afterlune/lune/internal/tui/markdown"

func renderMarkdown(text string, width int) string {
	theme := activeTheme()
	return markdown.Render(text, width, markdown.Palette{
		TextPrimary:  theme.TextPrimary,
		TextMuted:    theme.TextMuted,
		AccentBlue:   theme.AccentBlue,
		InlineCodeFg: theme.InlineCodeFg,
		CodeBlockBg:  theme.CodeBlockBg,
		Border:       theme.Border,
	})
}
