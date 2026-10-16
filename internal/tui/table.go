package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"
)

func wrapDisplayText(text string, width int) string {
	return ansi.Wrap(text, max(1, width), "")
}

// renderConversationText keeps code literal and only lays out complete tables.
// An incomplete streamed row remains ordinary text until its separator arrives.
func renderConversationText(text string, width int) string {
	text = safeToolText(text)
	source := strings.Split(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n")
	var result []string
	var prose []string
	flush := func() {
		if len(prose) > 0 {
			result = append(result, plainConversationText(strings.Join(prose, "\n")))
			prose = nil
		}
	}
	code := false
	for i := 0; i < len(source); {
		if strings.HasPrefix(strings.TrimSpace(source[i]), "```") {
			code = !code
		}
		header := tableCells(source[i])
		if !code && len(header) > 1 && i+1 < len(source) && tableSeparator(source[i+1], len(header)) {
			flush()
			rows := [][]string{header}
			i += 2
			for i < len(source) {
				row := tableCells(source[i])
				if len(row) != len(header) {
					break
				}
				rows = append(rows, row)
				i++
			}
			result = append(result, layoutTable(rows, width))
		} else {
			prose = append(prose, source[i])
			i++
		}
	}
	flush()
	return strings.Trim(strings.Join(result, "\n"), "\n")
}

func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	if !strings.Contains(line, "|") {
		return nil
	}
	line = strings.TrimPrefix(line, "|")
	if strings.HasSuffix(line, "|") && !strings.HasSuffix(line, `\|`) {
		line = strings.TrimSuffix(line, "|")
	}
	var cells []string
	var cell strings.Builder
	escaped, code := false, false
	for _, r := range line {
		if escaped {
			if r != '|' {
				cell.WriteRune('\\')
			}
			cell.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '`' {
			code = !code
		}
		if r == '|' && !code {
			cells = append(cells, strings.TrimSpace(cell.String()))
			cell.Reset()
		} else {
			cell.WriteRune(r)
		}
	}
	if escaped {
		cell.WriteRune('\\')
	}
	return append(cells, strings.TrimSpace(cell.String()))
}

func tableSeparator(line string, count int) bool {
	cells := tableCells(line)
	if len(cells) != count {
		return false
	}
	for _, cell := range cells {
		cell = strings.Trim(cell, ":")
		if len(cell) < 3 || strings.Trim(cell, "-") != "" {
			return false
		}
	}
	return true
}

func layoutTable(rows [][]string, width int) string {
	columns := len(rows[0])
	widths := make([]int, columns)
	for ri, row := range rows {
		for ci, cell := range row {
			cell = plainConversationText(cell)
			rows[ri][ci] = cell
			widths[ci] = max(widths[ci], uniseg.StringWidth(cell))
		}
	}
	available := max(1, width) - 3*(columns-1)
	minimum := 0
	for _, w := range widths {
		minimum += min(max(1, w), 12)
	}
	if available < minimum {
		var lines []string
		for i, row := range rows[1:] {
			if i > 0 {
				lines = append(lines, "")
			}
			for j, cell := range row {
				lines = append(lines, wrapDisplayText(rows[0][j]+": "+cell, width))
			}
		}
		if len(lines) == 0 {
			return wrapDisplayText(strings.Join(rows[0], " · "), width)
		}
		return strings.Join(lines, "\n")
	}
	sum := func() int {
		total := 0
		for _, w := range widths {
			total += max(1, w)
		}
		return total
	}
	for sum() > available {
		largest := 0
		for i := range widths {
			if widths[i] > widths[largest] {
				largest = i
			}
		}
		widths[largest]--
	}
	var lines []string
	for ri, row := range rows {
		wrapped := make([][]string, columns)
		height := 1
		for ci, cell := range row {
			wrapped[ci] = strings.Split(wrapDisplayText(cell, max(1, widths[ci])), "\n")
			height = max(height, len(wrapped[ci]))
		}
		for y := 0; y < height; y++ {
			cells := make([]string, columns)
			for ci := range widths {
				cell := ""
				if y < len(wrapped[ci]) {
					cell = wrapped[ci][y]
				}
				cells[ci] = cell + strings.Repeat(" ", max(0, widths[ci]-uniseg.StringWidth(cell)))
			}
			lines = append(lines, strings.Join(cells, " │ "))
		}
		if ri == 0 {
			cells := make([]string, columns)
			for ci, w := range widths {
				cells[ci] = strings.Repeat("─", max(1, w))
			}
			lines = append(lines, strings.Join(cells, "─┼─"))
		}
	}
	return strings.Join(lines, "\n")
}
