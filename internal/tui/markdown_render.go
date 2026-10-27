package tui

import (
	"github.com/afterlune/lune/internal/terminaltext"
	"github.com/afterlune/lune/internal/tui/markdown"
)

func renderMarkdown(text string, width int) string {
	return renderMarkdownWithAccent(text, width, activeTheme().AccentBlue)
}

func renderMarkdownStable(text string, width int) string {
	theme := activeTheme()
	return markdown.RenderStable(terminaltext.Sanitize(text), width, markdown.Palette{
		TextPrimary:  theme.TextPrimary,
		TextMuted:    theme.TextMuted,
		AccentBlue:   theme.AccentBlue,
		InlineCodeFg: theme.InlineCodeFg,
		CodeBlockBg:  theme.CodeBlockBg,
		Border:       theme.Border,
	})
}

func renderPlanMarkdown(text string, width int) string {
	theme := activeTheme()
	return markdown.RenderPlan(terminaltext.Sanitize(text), width, markdown.Palette{
		TextPrimary:  theme.TextPrimary,
		TextMuted:    theme.TextMuted,
		AccentBlue:   theme.AccentSoft,
		InlineCodeFg: theme.InlineCodeFg,
		CodeBlockBg:  theme.CodeBlockBg,
		Border:       theme.Border,
	})
}

func renderPlanMarkdownStable(text string, width int) string {
	theme := activeTheme()
	return markdown.RenderPlanStable(terminaltext.Sanitize(text), width, markdown.Palette{
		TextPrimary:  theme.TextPrimary,
		TextMuted:    theme.TextMuted,
		AccentBlue:   theme.AccentSoft,
		InlineCodeFg: theme.InlineCodeFg,
		CodeBlockBg:  theme.CodeBlockBg,
		Border:       theme.Border,
	})
}

func renderMarkdownWithAccent(text string, width int, accent string) string {
	theme := activeTheme()
	return markdown.Render(terminaltext.Sanitize(text), width, markdown.Palette{
		TextPrimary:  theme.TextPrimary,
		TextMuted:    theme.TextMuted,
		AccentBlue:   accent,
		InlineCodeFg: theme.InlineCodeFg,
		CodeBlockBg:  theme.CodeBlockBg,
		Border:       theme.Border,
	})
}
