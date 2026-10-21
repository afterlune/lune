package tui

import (
	"strings"

	"github.com/afterlune/lune/internal/tui/markdown"
	"github.com/charmbracelet/x/ansi"
)

func renderMarkdown(text string, width int) string {
	return renderMarkdownWithAccent(text, width, activeTheme().AccentBlue)
}

func renderPlanMarkdown(text string, width int) string {
	theme := activeTheme()
	return markdown.RenderPlan(text, width, markdown.Palette{
		TextPrimary:  theme.TextPrimary,
		TextMuted:    theme.TextMuted,
		AccentBlue:   theme.AccentSoft,
		InlineCodeFg: theme.InlineCodeFg,
		CodeBlockBg:  theme.CodeBlockBg,
		Border:       theme.Border,
	})
}

// renderPlanSurface paints a complete rectangle after Markdown layout. Keep
// geometry separate from ANSI styling: a styled outer box would wrap and pad
// the already-rendered body again.
func renderPlanSurface(rendered string, width int) string {
	const padding = 2
	width = max(20, width)
	innerWidth := width - 2*padding
	lines := strings.Split(ansi.Hardwrap(rendered, innerWidth, true), "\n")
	rows := make([]string, 0, len(lines)+2)
	empty := strings.Repeat(" ", width)
	rows = append(rows, empty)
	for _, line := range lines {
		rows = append(rows, strings.Repeat(" ", padding)+line+
			strings.Repeat(" ", width-padding-ansi.StringWidth(line)))
	}
	rows = append(rows, empty)
	th := activeTheme()
	return applySurfaceBg(strings.Join(rows, "\n"), th.CardBg, th.TextPrimary)
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
