package tui

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	reBold       = regexp.MustCompile(`\*\*(.*?)\*\*`)
	reInlineCode = regexp.MustCompile("`([^`]+)`")
	reFileRef    = regexp.MustCompile(`\b([a-zA-Z0-9_\-\.\/\\]+\.(?:go|rs|py|ts|js|json|md|html|css|toml|ya?ml|svelte)(?::\d+)?)\b`)
	reBullet     = regexp.MustCompile(`^(\s*)[-*+]\s+`)
	reNumbered   = regexp.MustCompile(`^(\s*\d+\.)\s+`)
)

const (
	escapedPipePlaceholder = "\x00PIPE\x00"
	wrapBreakpoints        = " ,.:;!?、。，；！？"
)

func isBoxDrawingLine(s string) bool {
	for _, r := range s {
		switch r {
		case '┌', '┐', '└', '┘', '│', '─', '┬', '┴', '├', '┤', '┼', '▲', '▼', '╭', '╮', '╯', '╰':
			return true
		}
	}
	return false
}

// splitCodeComment separates a code line into its active command/code part and trailing # or // comment.
func splitCodeComment(line string) (string, string) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
		return "", line
	}
	inSingle := false
	inDouble := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if ch == '\\' && (inSingle || inDouble) {
			i++
			continue
		}
		if ch == '\'' && !inDouble {
			inSingle = !inSingle
			continue
		}
		if ch == '"' && !inSingle {
			inDouble = !inDouble
			continue
		}
		if !inSingle && !inDouble && i > 0 && (line[i-1] == ' ' || line[i-1] == '\t') {
			if ch == '#' {
				return line[:i], line[i:]
			}
			if ch == '/' && i+1 < len(line) && line[i+1] == '/' {
				return line[:i], line[i:]
			}
		}
	}
	return line, ""
}

func renderCodeBlockRow(line string, blockWidth int, th Theme) string {
	codeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextPrimary))
	if th.CodeBlockBg != "" {
		codeStyle = codeStyle.Background(lipgloss.Color(th.CodeBlockBg))
	}
	commentStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextMuted)).
		Italic(true)
	if th.CodeBlockBg != "" {
		commentStyle = commentStyle.Background(lipgloss.Color(th.CodeBlockBg))
	}

	codePart, commentPart := splitCodeComment(line)

	var b strings.Builder
	b.WriteString("  ")
	if codePart != "" {
		b.WriteString(codeStyle.Render(codePart))
	}
	if commentPart != "" {
		b.WriteString(commentStyle.Render(commentPart))
	}
	return b.String()
}

// RenderMarkdown renders markdown text using terminal styles, wrapping lines to fit width.
func RenderMarkdown(text string, width int) string {
	if width <= 0 {
		width = 80
	}
	proseWidth := width
	codeWidth := width

	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	inCodeBlock := false

	th := activeTheme()
	h1Style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.TextPrimary))
	h2Style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.TextPrimary))
	h3Style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.TextMuted))
	mutedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))

	inlineCodeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.InlineCodeFg))
	boldStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.TextPrimary))
	fileRefStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue))

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSuffix(lines[i], "\r")
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if !inCodeBlock {
				inCodeBlock = true
				lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				if lang != "" {
					headerStyle := lipgloss.NewStyle().
						Foreground(lipgloss.Color(th.TextMuted)).
						Bold(true)
					result = append(result, "  "+headerStyle.Render(lang))
				}
			} else {
				inCodeBlock = false
			}
			continue
		}

		if inCodeBlock {
			expanded := strings.ReplaceAll(line, "\t", "    ")
			if isBoxDrawingLine(expanded) {
				if lipgloss.Width(expanded) > codeWidth-2 {
					expanded = ansi.Truncate(expanded, max(10, codeWidth-2), "…")
				}
				result = append(result, renderCodeBlockRow(expanded, codeWidth, th))
				continue
			}
			if lipgloss.Width(expanded) > codeWidth-4 {
				wrapped := ansi.Wrap(expanded, max(10, codeWidth-4), " .,()[]{}<>;=+-*/\\\"'")
				for _, wl := range strings.Split(wrapped, "\n") {
					result = append(result, renderCodeBlockRow(wl, codeWidth, th))
				}
			} else {
				result = append(result, renderCodeBlockRow(expanded, codeWidth, th))
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
				Render(strings.Repeat("─", min(proseWidth, 48)))
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
				if len(result) > 0 && result[len(result)-1] != "" {
					result = append(result, "")
				}
				title := formatInlineWithStyles(strings.TrimSpace(trimmed[hashes+1:]), inlineCodeStyle, boldStyle, fileRefStyle)
				wrapped := ansi.Wrap(title, proseWidth, wrapBreakpoints)
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
			wrapWidth := max(10, proseWidth-barWidth)
			wrapped := ansi.Wrap(formatted, wrapWidth, wrapBreakpoints)
			for _, wl := range strings.Split(wrapped, "\n") {
				result = append(result, bar+wl)
			}
			continue
		}

		// Bullet list (- item, * item, + item)
		expandedLine := strings.ReplaceAll(line, "\t", "    ")
		if isBoxDrawingLine(expandedLine) {
			if lipgloss.Width(expandedLine) > width {
				expandedLine = ansi.Truncate(expandedLine, width, "…")
			}
			result = append(result, expandedLine)
			continue
		}

		bulletMatch := reBullet.FindStringSubmatch(expandedLine)
		if len(bulletMatch) > 0 {
			indentSpaces := bulletMatch[1]
			bulletPart := expandedLine[len(indentSpaces):len(bulletMatch[0])]
			bulletChar := strings.TrimSpace(bulletPart)
			if bulletChar == "-" || bulletChar == "*" || bulletChar == "+" {
				bulletChar = "•"
			}
			rest := expandedLine[len(bulletMatch[0]):]
			formatted := formatInlineWithStyles(rest, inlineCodeStyle, boldStyle, fileRefStyle)

			rawPrefix := indentSpaces + bulletChar + " "
			styledPrefix := indentSpaces + mutedStyle.Render(bulletChar) + " "
			prefixWidth := lipgloss.Width(rawPrefix)
			wrapWidth := max(10, proseWidth-prefixWidth)
			indent := strings.Repeat(" ", prefixWidth)

			wrapped := ansi.Wrap(formatted, wrapWidth, wrapBreakpoints)
			for j, wl := range strings.Split(wrapped, "\n") {
				if j == 0 {
					result = append(result, styledPrefix+wl)
				} else {
					result = append(result, indent+wl)
				}
			}
			continue
		}

		// Numbered list (1. item, 2. item)
		numMatch := reNumbered.FindStringSubmatch(expandedLine)
		if len(numMatch) > 0 {
			rawPrefix := numMatch[0]
			numToken := numMatch[1]
			leadSpaces := numToken[:len(numToken)-len(strings.TrimLeft(numToken, " "))]
			numStr := strings.TrimSpace(numToken)
			styledPrefix := leadSpaces + mutedStyle.Render(numStr) + " "
			rest := expandedLine[len(rawPrefix):]
			formatted := formatInlineWithStyles(rest, inlineCodeStyle, boldStyle, fileRefStyle)

			prefixWidth := lipgloss.Width(rawPrefix)
			wrapWidth := max(10, proseWidth-prefixWidth)
			indent := strings.Repeat(" ", prefixWidth)

			wrapped := ansi.Wrap(formatted, wrapWidth, wrapBreakpoints)
			for j, wl := range strings.Split(wrapped, "\n") {
				if j == 0 {
					result = append(result, styledPrefix+wl)
				} else {
					result = append(result, indent+wl)
				}
			}
			continue
		}

		// Normal paragraph with inline formatting and word wrap
		formatted := formatInlineWithStyles(expandedLine, inlineCodeStyle, boldStyle, fileRefStyle)
		if lipgloss.Width(formatted) > proseWidth {
			wrapped := ansi.Wrap(formatted, proseWidth, wrapBreakpoints)
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
	inlineCodeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.InlineCodeFg))
	boldStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.TextPrimary))
	fileRefStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue))
	return formatInlineWithStyles(line, inlineCodeStyle, boldStyle, fileRefStyle)
}

func formatInlineWithStyles(line string, codeStyle, boldStyle, fileStyle lipgloss.Style) string {
	hasBacktick := strings.ContainsRune(line, '`')
	hasAsterisk := strings.Contains(line, "**")
	hasDot := strings.ContainsRune(line, '.')

	if !hasBacktick && !hasAsterisk && !hasDot {
		return line
	}

	var renderedTokens []string

	// 1. Tokenize inline code spans first: `code`
	if hasBacktick {
		line = reInlineCode.ReplaceAllStringFunc(line, func(m string) string {
			content := strings.Trim(m, "`")
			idx := len(renderedTokens)
			renderedTokens = append(renderedTokens, codeStyle.Render(content))
			return "\x00TOKEN_" + itoaByte(idx) + "\x00"
		})
	}

	// 2. Tokenize bold spans: **bold**
	if hasAsterisk {
		line = reBold.ReplaceAllStringFunc(line, func(m string) string {
			content := strings.Trim(m, "*")
			idx := len(renderedTokens)
			renderedTokens = append(renderedTokens, boldStyle.Render(content))
			return "\x00TOKEN_" + itoaByte(idx) + "\x00"
		})
	}

	// 3. For any remaining plain text, if hasDot, match file references on un-tokenized words
	if hasDot {
		line = reFileRef.ReplaceAllStringFunc(line, func(m string) string {
			idx := len(renderedTokens)
			renderedTokens = append(renderedTokens, fileStyle.Render(m))
			return "\x00TOKEN_" + itoaByte(idx) + "\x00"
		})
	}

	// 4. Restore outer tokens first. A bold token can contain an earlier
	// inline-code token, so descending order also restores nested placeholders.
	for idx := len(renderedTokens) - 1; idx >= 0; idx-- {
		placeholder := "\x00TOKEN_" + itoaByte(idx) + "\x00"
		line = strings.ReplaceAll(line, placeholder, renderedTokens[idx])
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

// renderMarkdownTable renders a clean, borderless horizontal-divider table (Codex style).
func renderMarkdownTable(headerLine string, rowLines []string, width int) []string {
	headers := splitTableRow(headerLine)
	numCols := len(headers)
	if numCols == 0 {
		return nil
	}
	for i := range headers {
		headers[i] = formatInline(headers[i])
	}

	var rows [][]string
	for _, rLine := range rowLines {
		rowCells := splitTableRow(rLine)
		for len(rowCells) < numCols {
			rowCells = append(rowCells, "")
		}
		if len(rowCells) > numCols {
			rowCells = rowCells[:numCols]
		}
		for i := range rowCells {
			rowCells[i] = formatInline(rowCells[i])
		}
		rows = append(rows, rowCells)
	}

	colWidths := make([]int, numCols)
	for c := 0; c < numCols; c++ {
		w := lipgloss.Width(headers[c])
		if w < 4 {
			w = 4
		}
		colWidths[c] = w
	}
	for _, r := range rows {
		for c := 0; c < numCols; c++ {
			if w := lipgloss.Width(r[c]); w > colWidths[c] {
				colWidths[c] = w
			}
		}
	}

	const colGap = 2
	totalGap := max(0, (numCols-1)*colGap)
	availCols := max(numCols*4, width-totalGap)

	totalNatural := 0
	for _, w := range colWidths {
		totalNatural += w
	}
	if totalNatural > availCols {
		// Preserve compact label columns (<= 16 chars) and shrink wider columns proportionally.
		fixedSum := 0
		flexSum := 0
		for _, w := range colWidths {
			if w <= 16 {
				fixedSum += w
			} else {
				flexSum += w
			}
		}
		if fixedSum < availCols/2 && flexSum > 0 {
			rem := availCols - fixedSum
			for c, w := range colWidths {
				if w > 16 {
					colWidths[c] = max(6, w*rem/flexSum)
				}
			}
		} else {
			for c, w := range colWidths {
				colWidths[c] = max(4, w*availCols/totalNatural)
			}
		}
	}

	th := activeTheme()
	borderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.Border))
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.TextPrimary))
	cellStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary))

	divParts := make([]string, numCols)
	for c := 0; c < numCols; c++ {
		divParts[c] = borderStyle.Render(strings.Repeat("─", colWidths[c]))
	}
	dividerLine := strings.Join(divParts, strings.Repeat(" ", colGap))

	renderRow := func(cells []string, isHeader bool) []string {
		wrappedCols := make([][]string, numCols)
		maxLines := 1
		for c := 0; c < numCols; c++ {
			wrapped := ansi.Wrap(cells[c], colWidths[c], wrapBreakpoints)
			lines := strings.Split(wrapped, "\n")
			wrappedCols[c] = lines
			if len(lines) > maxLines {
				maxLines = len(lines)
			}
		}
		out := make([]string, 0, maxLines)
		for lineIdx := 0; lineIdx < maxLines; lineIdx++ {
			lineParts := make([]string, numCols)
			for c := 0; c < numCols; c++ {
				cellText := ""
				if lineIdx < len(wrappedCols[c]) {
					cellText = wrappedCols[c][lineIdx]
				}
				pad := max(0, colWidths[c]-lipgloss.Width(cellText))
				if isHeader {
					lineParts[c] = headerStyle.Render(cellText) + strings.Repeat(" ", pad)
				} else {
					lineParts[c] = cellStyle.Render(cellText) + strings.Repeat(" ", pad)
				}
			}
			out = append(out, strings.Join(lineParts, strings.Repeat(" ", colGap)))
		}
		return out
	}

	var out []string
	out = append(out, renderRow(headers, true)...)
	out = append(out, dividerLine)
	for rIdx, r := range rows {
		out = append(out, renderRow(r, false)...)
		if rIdx < len(rows)-1 {
			out = append(out, dividerLine)
		}
	}
	return out
}

var (
	reProposedPlanTags  = regexp.MustCompile(`(?i)</?proposed_plan>`)
	reProposedPlanBlock = regexp.MustCompile(`(?is)<proposed_plan>.*?</proposed_plan>`)
)

// cleanProposedPlanTags strips any raw <proposed_plan> or </proposed_plan> tags from text,
// but preserves inline code mentions like `<proposed_plan>`.
func cleanProposedPlanTags(s string) string {
	if !strings.Contains(strings.ToLower(s), "proposed_plan") {
		return strings.TrimSpace(s)
	}
	var codeTokens []string
	protected := reInlineCode.ReplaceAllStringFunc(s, func(m string) string {
		idx := len(codeTokens)
		codeTokens = append(codeTokens, m)
		return "\x00CODE_" + itoaByte(idx) + "\x00"
	})
	stripped := reProposedPlanTags.ReplaceAllString(protected, "")
	for idx, token := range codeTokens {
		stripped = strings.ReplaceAll(stripped, "\x00CODE_"+itoaByte(idx)+"\x00", token)
	}
	return strings.TrimSpace(stripped)
}

// stripProposedPlan removes complete <proposed_plan>...</proposed_plan> blocks
// (their content is rendered separately in the plan review card) and any dangling
// standalone tags, while preserving inline code mentions like `<proposed_plan>`.
func stripProposedPlan(text string) string {
	if !strings.Contains(strings.ToLower(text), "proposed_plan") {
		return strings.TrimSpace(text)
	}
	var codeTokens []string
	protected := reInlineCode.ReplaceAllStringFunc(text, func(m string) string {
		idx := len(codeTokens)
		codeTokens = append(codeTokens, m)
		return "\x00CODE_" + itoaByte(idx) + "\x00"
	})
	stripped := reProposedPlanBlock.ReplaceAllString(protected, "")
	for idx, token := range codeTokens {
		stripped = strings.ReplaceAll(stripped, "\x00CODE_"+itoaByte(idx)+"\x00", token)
	}
	return cleanProposedPlanTags(stripped)
}

// extractProposedPlan extracts one complete, non-empty <proposed_plan> block and
// the remaining assistant text. A dangling open tag is not a plan. Inline mentions
// like `<proposed_plan>` are preserved.
func extractProposedPlan(text string) (string, string) {
	if !strings.Contains(strings.ToLower(text), "proposed_plan") {
		return "", cleanProposedPlanTags(text)
	}
	var codeTokens []string
	protected := reInlineCode.ReplaceAllStringFunc(text, func(m string) string {
		idx := len(codeTokens)
		codeTokens = append(codeTokens, m)
		return "\x00CODE_" + itoaByte(idx) + "\x00"
	})

	const openTag, closeTag = "<proposed_plan>", "</proposed_plan>"
	start := strings.Index(protected, openTag)
	if start < 0 {
		return "", cleanProposedPlanTags(text)
	}
	innerStart := start + len(openTag)
	relEnd := strings.Index(protected[innerStart:], closeTag)
	if relEnd < 0 {
		// Unclosed tag: keep the prose after the tag, but do not produce a plan.
		return "", stripProposedPlan(text)
	}
	end := innerStart + relEnd + len(closeTag)
	rawPlan := protected[innerStart : innerStart+relEnd]
	for idx, token := range codeTokens {
		rawPlan = strings.ReplaceAll(rawPlan, "\x00CODE_"+itoaByte(idx)+"\x00", token)
	}
	planText := cleanProposedPlanTags(rawPlan)
	if planText == "" || strings.Contains(protected[innerStart:], openTag) || strings.Contains(protected[end:], closeTag) {
		return "", stripProposedPlan(text)
	}

	restProtected := protected[:start] + protected[end:]
	for idx, token := range codeTokens {
		restProtected = strings.ReplaceAll(restProtected, "\x00CODE_"+itoaByte(idx)+"\x00", token)
	}
	return planText, cleanProposedPlanTags(restProtected)
}
