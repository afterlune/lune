package tui

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

type lightTextSpan struct {
	start, end int
	style      lipgloss.Style
}

// lightInlineText retains style positions while removing inline delimiters.
func lightInlineText(source string) (string, []lightTextSpan) {
	var out strings.Builder
	var spans []lightTextSpan
	for len(source) > 0 {
		delimiter := ""
		style := lightTextStyle()
		if strings.HasPrefix(source, "**") {
			delimiter = "**"
			style = style.Bold(true)
		} else if strings.HasPrefix(source, "`") {
			delimiter = "`"
			style = style.Foreground(lipgloss.Color(codexLight.CodeText))
		}
		if delimiter != "" {
			if end := strings.Index(source[len(delimiter):], delimiter); end >= 0 {
				value := source[len(delimiter) : len(delimiter)+end]
				start := out.Len()
				out.WriteString(value)
				spans = append(spans, lightTextSpan{start, out.Len(), style})
				source = source[end+2*len(delimiter):]
				continue
			}
		}
		if source[0] == '[' {
			if close := strings.Index(source, "]("); close >= 0 {
				if end := strings.IndexByte(source[close+2:], ')'); end >= 0 {
					label := source[1:close]
					target := source[close+2 : close+2+end]
					start := out.Len()
					out.WriteString(label)
					style = style.Foreground(lipgloss.Color("#1C64C8"))
					if !strings.Contains(target, "://") {
						style = style.Foreground(lipgloss.Color(codexLight.CodeText))
					}
					spans = append(spans, lightTextSpan{start, out.Len(), style})
					source = source[close+3+end:]
					continue
				}
			}
		}
		_, n := utf8.DecodeRuneInString(source)
		out.WriteString(source[:n])
		source = source[n:]
	}
	return out.String(), spans
}

func lightMarkdownLines(text, role string, width int) []transcriptLine {
	source := strings.Split(strings.ReplaceAll(strings.ReplaceAll(safeToolText(text), "\r\n", "\n"), "\r", "\n"), "\n")
	var result []transcriptLine
	code := false
	for i := 0; i < len(source); i++ {
		raw := source[i]
		trim := strings.TrimSpace(raw)
		if strings.HasPrefix(trim, "```") {
			code = !code
			result = append(result, transcriptLine{text: "  ", role: role})
			continue
		}
		if !code && i+1 < len(source) && len(tableCells(raw)) > 1 && tableSeparator(source[i+1], len(tableCells(raw))) {
			cells := [][]string{tableCells(raw)}
			i += 2
			for i < len(source) && len(tableCells(source[i])) == len(cells[0]) {
				cells = append(cells, tableCells(source[i]))
				i++
			}
			i--
			for _, line := range strings.Split(layoutTable(cells, max(1, width-2)), "\n") {
				result = append(result, transcriptLine{text: "  " + line, role: role})
			}
			continue
		}
		lineRole := role
		prefix := "  "
		if strings.HasPrefix(trim, "# ") || strings.HasPrefix(trim, "## ") || strings.HasPrefix(trim, "### ") {
			lineRole = role + "_heading"
		}
		if !code && (strings.HasPrefix(trim, "- ") || strings.HasPrefix(trim, "* ") || strings.HasPrefix(trim, "+ ")) {
			raw = "• " + trim[2:]
		}
		plain, spans := raw, []lightTextSpan(nil)
		if !code {
			plain, spans = lightInlineText(raw)
		}
		if lineRole == role+"_heading" {
			spans = append([]lightTextSpan{{0, len(plain), lightTextStyle().Bold(true)}}, spans...)
		}
		if orderedPlanListItem.MatchString(strings.TrimSpace(plain)) {
			start := strings.Index(plain, strings.TrimSpace(plain))
			end := strings.IndexAny(plain[start:], ".)")
			if end >= 0 {
				spans = append(spans, lightTextSpan{start, start + end + 1, lipgloss.NewStyle().Foreground(lipgloss.Color("#1C64C8"))})
			}
		}
		for j := range spans {
			spans[j].start += len(prefix)
			spans[j].end += len(prefix)
		}
		result = append(result, transcriptLine{text: prefix + plain, role: lineRole, spans: spans})
	}
	if role == "assistant" && len(result) > 0 {
		result[0].text = "• " + strings.TrimPrefix(result[0].text, "  ")
		for j := range result[0].spans {
			result[0].spans[j].start += len("• ") - 2
			result[0].spans[j].end += len("• ") - 2
		}
	}
	return result
}

func transcriptStyleSpans(lines []transcriptLine) []lightTextSpan {
	var result []lightTextSpan
	offset := 0
	for _, line := range lines {
		for _, span := range line.spans {
			span.start += offset
			span.end += offset
			result = append(result, span)
		}
		offset += len(line.text) + 1
	}
	return result
}
