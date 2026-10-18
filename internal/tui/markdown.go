package tui

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"
)

var (
	reBold       = regexp.MustCompile(`\*\*(.*?)\*\*`)
	reInlineCode = regexp.MustCompile("`([^`]+)`")
	reFileRef    = regexp.MustCompile(`\b([a-zA-Z0-9_\-\.\/\\]+\.(?:go|rs|py|ts|js|json|md|html|css|toml|ya?ml)(?::\d+)?)\b`)
	reBullet     = regexp.MustCompile(`^(\s*)[-*+]\s+`)
	reNumbered   = regexp.MustCompile(`^(\s*\d+\.)\s+`)
)

const (
	escapedPipePlaceholder = "\x00PIPE\x00"
	wrapBreakpoints        = " ,.:;!?、。，；！？"
)

// RenderMarkdown renders markdown text using terminal styles, wrapping lines to fit width.
func RenderMarkdown(text string, width int) string {
	if width <= 0 {
		width = 80
	}
	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	inCodeBlock := false

	th := activeTheme()
	codeBlockStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.CardBg)).
		Foreground(lipgloss.Color(th.TextPrimary))
	h1Style := lipgloss.NewStyle().Bold(true).Underline(true).Foreground(lipgloss.Color(th.TextPrimary))
	h2Style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.TextPrimary))
	h3Style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.TextMuted))

	inlineCodeStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.CardBg)).
		Foreground(lipgloss.Color(th.TextPrimary))
	boldStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.TextPrimary))
	fileRefStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Underline(true)

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSuffix(lines[i], "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if !inCodeBlock {
				inCodeBlock = true
				lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				if lang != "" {
					headerText := "  " + lang
					pad := max(0, width-lipgloss.Width(headerText))
					headerStyle := lipgloss.NewStyle().
						Background(lipgloss.Color(th.CardBg)).
						Foreground(lipgloss.Color(th.TextMuted)).
						Bold(true)
					result = append(result, headerStyle.Render(headerText+strings.Repeat(" ", pad)))
				}
			} else {
				inCodeBlock = false
			}
			continue
		}

		if inCodeBlock {
			expanded := strings.ReplaceAll(line, "\t", "    ")
			if lipgloss.Width(expanded) > width-4 {
				wrapped := ansi.Wrap(expanded, max(10, width-4), " .,()[]{}<>;=+-*/\\\"'")
				for _, wl := range strings.Split(wrapped, "\n") {
					pad := max(0, width-lipgloss.Width(wl)-2)
					result = append(result, codeBlockStyle.Render("  "+wl+strings.Repeat(" ", pad)))
				}
			} else {
				pad := max(0, width-lipgloss.Width(expanded)-2)
				result = append(result, codeBlockStyle.Render("  "+expanded+strings.Repeat(" ", pad)))
			}
			continue
		}

		// Empty line
		if trimmed == "" {
			result = append(result, "")
			continue
		}

		// Horizontal rule
		if trimmed == "---" || trimmed == "***" || trimmed == "___" {
			hr := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border)).
				Render(strings.Repeat("─", min(width, 40)))
			result = append(result, hr)
			continue
		}

		// Headings (# through ######)
		if strings.HasPrefix(trimmed, "#") {
			hashes := 0
			for hashes < len(trimmed) && trimmed[hashes] == '#' {
				hashes++
			}
			if hashes >= 1 && hashes <= 6 && hashes < len(trimmed) && trimmed[hashes] == ' ' {
				title := formatInlineWithStyles(strings.TrimSpace(trimmed[hashes+1:]), inlineCodeStyle, boldStyle, fileRefStyle)
				wrapped := ansi.Wrap(title, width, wrapBreakpoints)
				var style lipgloss.Style
				switch hashes {
				case 1:
					style = h1Style
				case 2:
					style = h2Style
				default:
					style = h3Style
				}
				for _, wl := range strings.Split(wrapped, "\n") {
					result = append(result, style.Render(wl))
				}
				continue
			}
		}

		// Markdown Table: header line followed by delimiter line
		if strings.Contains(line, "|") && i+1 < len(lines) && isTableDelimiterRow(lines[i+1]) {
			headerLine := line
			i += 2 // skip header and delimiter
			var rowLines []string
			for i < len(lines) {
				curr := strings.TrimSpace(lines[i])
				if curr == "" || !strings.Contains(curr, "|") {
					i-- // step back so outer loop processes this line
					break
				}
				rowLines = append(rowLines, lines[i])
				i++
			}
			tableLines := renderMarkdownTable(headerLine, rowLines, width)
			result = append(result, tableLines...)
			continue
		}

		// Blockquotes
		if strings.HasPrefix(trimmed, ">") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
			formatted := formatInlineWithStyles(rest, inlineCodeStyle, boldStyle, fileRefStyle)
			bar := lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Render("│ ")
			barWidth := 2
			wrapWidth := max(10, width-barWidth)
			wrapped := ansi.Wrap(formatted, wrapWidth, wrapBreakpoints)
			for _, wl := range strings.Split(wrapped, "\n") {
				result = append(result, bar+wl)
			}
			continue
		}

		// Bullet list (- item, * item, + item)
		expandedLine := strings.ReplaceAll(line, "\t", "    ")
		bulletMatch := reBullet.FindStringSubmatch(expandedLine)
		if len(bulletMatch) > 0 {
			indentSpaces := bulletMatch[1]
			bulletPart := expandedLine[len(indentSpaces):len(bulletMatch[0])]
			bulletChar := strings.TrimSpace(bulletPart)
			rest := expandedLine[len(bulletMatch[0]):]
			formatted := formatInlineWithStyles(rest, inlineCodeStyle, boldStyle, fileRefStyle)

			prefix := indentSpaces + bulletChar + " "
			prefixWidth := lipgloss.Width(prefix)
			wrapWidth := max(10, width-prefixWidth)
			indent := strings.Repeat(" ", prefixWidth)

			wrapped := ansi.Wrap(formatted, wrapWidth, wrapBreakpoints)
			for j, wl := range strings.Split(wrapped, "\n") {
				if j == 0 {
					result = append(result, prefix+wl)
				} else {
					result = append(result, indent+wl)
				}
			}
			continue
		}

		// Numbered list (1. item, 2. item)
		numMatch := reNumbered.FindStringSubmatch(expandedLine)
		if len(numMatch) > 0 {
			prefix := numMatch[0]
			rest := expandedLine[len(prefix):]
			formatted := formatInlineWithStyles(rest, inlineCodeStyle, boldStyle, fileRefStyle)

			prefixWidth := lipgloss.Width(prefix)
			wrapWidth := max(10, width-prefixWidth)
			indent := strings.Repeat(" ", prefixWidth)

			wrapped := ansi.Wrap(formatted, wrapWidth, wrapBreakpoints)
			for j, wl := range strings.Split(wrapped, "\n") {
				if j == 0 {
					result = append(result, prefix+wl)
				} else {
					result = append(result, indent+wl)
				}
			}
			continue
		}

		// Normal paragraph with inline formatting and word wrap
		formatted := formatInlineWithStyles(expandedLine, inlineCodeStyle, boldStyle, fileRefStyle)
		if lipgloss.Width(formatted) > width {
			wrapped := ansi.Wrap(formatted, width, wrapBreakpoints)
			for _, wl := range strings.Split(wrapped, "\n") {
				result = append(result, wl)
			}
		} else {
			result = append(result, formatted)
		}
	}

	return strings.Join(result, "\n")
}

func formatInline(line string) string {
	th := activeTheme()
	inlineCodeStyle := lipgloss.NewStyle().
		Background(lipgloss.Color(th.CardBg)).
		Foreground(lipgloss.Color(th.TextPrimary))
	boldStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.TextPrimary))
	fileRefStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue)).Underline(true)
	return formatInlineWithStyles(line, inlineCodeStyle, boldStyle, fileRefStyle)
}

func formatInlineWithStyles(line string, codeStyle, boldStyle, fileStyle lipgloss.Style) string {
	hasBacktick := strings.ContainsRune(line, '`')
	hasAsterisk := strings.Contains(line, "**")
	hasDot := strings.ContainsRune(line, '.')

	if !hasBacktick && !hasAsterisk && !hasDot {
		return line
	}

	// Tokenize inline code spans first so file-reference or bold regexes never
	// match inside inline code or corrupt its ANSI escape sequences.
	var renderedCodeSpans []string
	if hasBacktick {
		line = reInlineCode.ReplaceAllStringFunc(line, func(m string) string {
			content := strings.Trim(m, "`")
			idx := len(renderedCodeSpans)
			renderedCodeSpans = append(renderedCodeSpans, codeStyle.Render(" "+content+" "))
			return "\x00INLINECODE_" + itoaByte(idx) + "\x00"
		})
	}

	if hasAsterisk {
		line = reBold.ReplaceAllStringFunc(line, func(m string) string {
			content := strings.Trim(m, "*")
			if hasDot {
				content = reFileRef.ReplaceAllStringFunc(content, func(fm string) string {
					return fileStyle.Bold(true).Render(fm)
				})
			}
			return boldStyle.Render(content)
		})
	}

	if hasDot {
		line = reFileRef.ReplaceAllStringFunc(line, func(m string) string {
			return fileStyle.Render(m)
		})
	}

	for idx, rendered := range renderedCodeSpans {
		placeholder := "\x00INLINECODE_" + itoaByte(idx) + "\x00"
		line = strings.ReplaceAll(line, placeholder, rendered)
	}

	return line
}

func splitTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.ReplaceAll(trimmed, `\|`, escapedPipePlaceholder)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	rawParts := strings.Split(trimmed, "|")
	cells := make([]string, len(rawParts))
	for i, p := range rawParts {
		cells[i] = strings.ReplaceAll(strings.TrimSpace(p), escapedPipePlaceholder, "|")
	}
	return cells
}

func isTableDelimiterRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	if !strings.Contains(trimmed, "|") {
		return false
	}
	cells := splitTableRow(trimmed)
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		c = strings.TrimSpace(c)
		if len(c) == 0 {
			return false
		}
		for _, r := range c {
			if r != '-' && r != ':' && r != ' ' {
				return false
			}
		}
		if !strings.ContainsRune(c, '-') {
			return false
		}
	}
	return true
}

func renderMarkdownTable(headerLine string, rowLines []string, width int) []string {
	headers := splitTableRow(headerLine)
	for i := range headers {
		headers[i] = formatInline(headers[i])
	}
	var rows [][]string
	for _, rLine := range rowLines {
		rowCells := splitTableRow(rLine)
		for len(rowCells) < len(headers) {
			rowCells = append(rowCells, "")
		}
		if len(rowCells) > len(headers) {
			rowCells = rowCells[:len(headers)]
		}
		for i := range rowCells {
			rowCells[i] = formatInline(rowCells[i])
		}
		rows = append(rows, rowCells)
	}

	th := activeTheme()
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border))).
		Headers(headers...).
		Rows(rows...).
		Wrap(true)

	t.StyleFunc(func(row, col int) lipgloss.Style {
		if row == table.HeaderRow {
			return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.TextPrimary))
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary))
	})

	if width > 20 {
		t.Width(width)
	}

	rendered := t.Render()
	return strings.Split(rendered, "\n")
}

// stripProposedPlan removes <proposed_plan>...</proposed_plan> tags and content from raw streamed text.
func stripProposedPlan(text string) string {
	_, cleaned := extractProposedPlan(text)
	return cleaned
}

// extractProposedPlan extracts the inner plan text and the remaining assistant text outside <proposed_plan>...</proposed_plan>.
func extractProposedPlan(text string) (string, string) {
	const openTag, closeTag = "<proposed_plan>", "</proposed_plan>"
	start := strings.Index(text, openTag)
	if start < 0 {
		return "", text
	}
	innerStart := start + len(openTag)
	relEnd := strings.Index(text[innerStart:], closeTag)
	if relEnd < 0 {
		return strings.TrimSpace(text[innerStart:]), strings.TrimSpace(text[:start])
	}
	planText := strings.TrimSpace(text[innerStart : innerStart+relEnd])
	end := innerStart + relEnd + len(closeTag)
	cleaned := strings.TrimSpace(text[:start] + text[end:])
	return planText, cleaned
}

