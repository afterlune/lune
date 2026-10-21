package markdown

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Palette contains the theme colors used by terminal Markdown rendering.
type Palette struct {
	TextPrimary  string
	TextMuted    string
	AccentBlue   string
	InlineCodeFg string
	CodeBlockBg  string
	Border       string
}

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

func itoaByte(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [3]byte
	i := len(buf)
	for v > 0 && i > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

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

func renderCodeBlockRow(line string, blockWidth int, th Palette) string {
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
	return fillCodeBlockRow(b.String(), blockWidth, th)
}

func fillCodeBlockRow(line string, blockWidth int, th Palette) string {
	if th.CodeBlockBg == "" {
		return line
	}
	padding := max(0, blockWidth-lipgloss.Width(line))
	if padding == 0 {
		return line
	}
	fillStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(th.TextPrimary)).
		Background(lipgloss.Color(th.CodeBlockBg))
	return line + fillStyle.Render(strings.Repeat(" ", padding))
}

// Render renders ordinary Markdown text with terminal styles, wrapping lines to fit width.
func Render(text string, width int, palette Palette) string {
	return render(text, width, palette, false)
}

// RenderPlan renders Markdown using the more spacious, source-preserving
// typography used inside the proposed-plan block.
func RenderPlan(text string, width int, palette Palette) string {
	return render(text, width, palette, true)
}

func render(text string, width int, palette Palette, planStyle bool) string {
	if width <= 0 {
		width = 80
	}
	proseWidth := width
	codeWidth := width

	lines := strings.Split(text, "\n")
	result := make([]string, 0, len(lines))
	inCodeBlock := false

	th := palette
	h1Style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.AccentBlue))
	h2Style := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(th.TextPrimary))
	h3Style := lipgloss.NewStyle().Bold(true).Italic(true).Foreground(lipgloss.Color(th.TextMuted))
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
					if th.CodeBlockBg != "" {
						headerStyle = headerStyle.Background(lipgloss.Color(th.CodeBlockBg))
					}
					result = append(result, fillCodeBlockRow("  "+headerStyle.Render(lang), codeWidth, th))
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
				if planStyle {
					title = strings.Repeat("#", hashes) + " " + title
				}
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
				if i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" {
					result = append(result, "")
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
			tableLines := renderMarkdownTable(headerLine, rowLines, width, th)
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
			wrappedLines := strings.Split(wrapped, "\n")
			for j, wl := range wrappedLines {
				if j == 0 {
					result = append(result, styledPrefix+wl)
				} else {
					result = append(result, indent+wl)
				}
			}
			if planStyle && len(wrappedLines) > 1 {
				result = append(result, "")
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
			wrappedLines := strings.Split(wrapped, "\n")
			for j, wl := range wrappedLines {
				if j == 0 {
					result = append(result, styledPrefix+wl)
				} else {
					result = append(result, indent+wl)
				}
			}
			if planStyle && len(wrappedLines) > 1 {
				result = append(result, "")
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

func formatInline(line string, th Palette) string {
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
func renderMarkdownTable(headerLine string, rowLines []string, width int, th Palette) []string {
	headers := splitTableRow(headerLine)
	numCols := len(headers)
	if numCols == 0 {
		return nil
	}
	for i := range headers {
		headers[i] = formatInline(headers[i], th)
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
			rowCells[i] = formatInline(rowCells[i], th)
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
	reProposedPlanTags  = regexp.MustCompile(`(?i)</?proposed_plan>|<proposed_plan_end/>`)
	reProposedPlanBlock = regexp.MustCompile(`(?is)<proposed_plan>.*?(?:</proposed_plan>|<proposed_plan_end/>)`)
)

const (
	proposedPlanOpenTag  = "<proposed_plan>"
	proposedPlanCloseTag = "</proposed_plan>"
	// Keep the live renderer aligned with Core's observed provider alias.
	proposedPlanCompatCloseTag = "<proposed_plan_end/>"
)

// ProposedPlanStream is the live rendering split for an assistant response.
// Before and After are ordinary assistant text; Plan is rendered with the
// proposed-plan style while the model is still streaming.
type ProposedPlanStream struct {
	Before   string
	Plan     string
	After    string
	Found    bool
	Complete bool
}

// CleanProposedPlanTags strips any raw proposed-plan tags from text,
// but preserves inline code mentions like `<proposed_plan>`.
func CleanProposedPlanTags(s string) string {
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

// StripProposedPlan removes complete proposed-plan blocks
// (their content is rendered separately in the plan review card) and any dangling
// standalone tags, while preserving inline code mentions like `<proposed_plan>`.
func StripProposedPlan(text string) string {
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
	return CleanProposedPlanTags(stripped)
}

// ExtractProposedPlan extracts one complete, non-empty proposed-plan block and
// the remaining assistant text. A dangling open tag is not a plan. Inline mentions
// like `<proposed_plan>` are preserved.
func ExtractProposedPlan(text string) (string, string) {
	if !strings.Contains(strings.ToLower(text), "proposed_plan") {
		return "", CleanProposedPlanTags(text)
	}
	var codeTokens []string
	protected := reInlineCode.ReplaceAllStringFunc(text, func(m string) string {
		idx := len(codeTokens)
		codeTokens = append(codeTokens, m)
		return "\x00CODE_" + itoaByte(idx) + "\x00"
	})

	start := strings.Index(protected, proposedPlanOpenTag)
	if start < 0 {
		return "", CleanProposedPlanTags(text)
	}
	innerStart := start + len(proposedPlanOpenTag)
	relEnd, closeLength := proposedPlanCloseIndex(protected[innerStart:])
	if relEnd < 0 {
		// Unclosed tag: keep the prose after the tag, but do not produce a plan.
		return "", StripProposedPlan(text)
	}
	end := innerStart + relEnd + closeLength
	rawPlan := protected[innerStart : innerStart+relEnd]
	rawPlan = restoreProposedPlanCodeTokens(rawPlan, codeTokens)
	planText := CleanProposedPlanTags(rawPlan)
	if planText == "" || strings.Contains(protected[innerStart:], proposedPlanOpenTag) || hasProposedPlanClose(protected[end:]) {
		return "", StripProposedPlan(text)
	}

	restProtected := protected[:start] + protected[end:]
	restProtected = restoreProposedPlanCodeTokens(restProtected, codeTokens)
	return planText, CleanProposedPlanTags(restProtected)
}

// SplitProposedPlanStream separates a live response into ordinary text and a
// plan segment. An unclosed opening tag is a live plan immediately; inline-code
// mentions remain protected and therefore do not start a plan.
func SplitProposedPlanStream(text string) ProposedPlanStream {
	if !strings.Contains(strings.ToLower(text), "proposed_plan") {
		return ProposedPlanStream{Before: strings.TrimSpace(text)}
	}
	codeTokens := []string{}
	protected := reInlineCode.ReplaceAllStringFunc(text, func(m string) string {
		idx := len(codeTokens)
		codeTokens = append(codeTokens, m)
		return "\x00CODE_" + itoaByte(idx) + "\x00"
	})
	start := strings.Index(protected, proposedPlanOpenTag)
	if start < 0 {
		return ProposedPlanStream{Before: CleanProposedPlanTags(text)}
	}
	innerStart := start + len(proposedPlanOpenTag)
	relEnd, closeLength := proposedPlanCloseIndex(protected[innerStart:])
	complete := relEnd >= 0
	end := len(protected)
	planEnd := len(protected)
	if complete {
		planEnd = innerStart + relEnd
		end = innerStart + relEnd + closeLength
	}
	rawPlan := restoreProposedPlanCodeTokens(protected[innerStart:planEnd], codeTokens)
	result := ProposedPlanStream{
		Before:   CleanProposedPlanTags(restoreProposedPlanCodeTokens(protected[:start], codeTokens)),
		Plan:     CleanProposedPlanTags(rawPlan),
		After:    CleanProposedPlanTags(restoreProposedPlanCodeTokens(protected[end:], codeTokens)),
		Found:    true,
		Complete: complete,
	}
	return result
}

func proposedPlanCloseIndex(text string) (int, int) {
	canonical := strings.Index(text, proposedPlanCloseTag)
	compat := strings.Index(text, proposedPlanCompatCloseTag)
	switch {
	case canonical < 0 && compat < 0:
		return -1, 0
	case canonical < 0:
		return compat, len(proposedPlanCompatCloseTag)
	case compat < 0:
		return canonical, len(proposedPlanCloseTag)
	case canonical < compat:
		return canonical, len(proposedPlanCloseTag)
	default:
		return compat, len(proposedPlanCompatCloseTag)
	}
}

func hasProposedPlanClose(text string) bool {
	index, _ := proposedPlanCloseIndex(text)
	return index >= 0
}

func restoreProposedPlanCodeTokens(text string, tokens []string) string {
	for idx, token := range tokens {
		text = strings.ReplaceAll(text, "\x00CODE_"+itoaByte(idx)+"\x00", token)
	}
	return text
}
