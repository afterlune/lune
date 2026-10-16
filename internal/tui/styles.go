package tui

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// LightTheme contains the palette and surface colors used by the Codex-style
// light transcript. Auto mode uses these accents while leaving the canvas to
// the terminal.
type LightTheme struct {
	Canvas      string
	Surface     string
	Primary     string
	Muted       string
	Subtle      string
	Success     string
	CodeSurface string
	CodeText    string
	Flag        string
	PillSurface string
	PillText    string
	Mode        string
	TaskSummary string
	Error       string
}

var codexLight = LightTheme{
	Canvas: "#FFFFFF", Surface: "#F3F4F6", Primary: "#1F2937", Muted: "#6B7280",
	Subtle: "#9CA3AF", Success: "#16A34A", CodeSurface: "#E6F4EA", CodeText: "#15803D",
	Flag: "#B91C1C", PillSurface: "#E0F2FE", PillText: "#0284C7", Mode: "#C026D3",
	TaskSummary: "#B45309", Error: "#B91C1C",
}

func wrapAndPadLines(text, indent string, totalWidth int, textStyle lipgloss.Style, bg color.Color) []string {
	totalWidth = max(1, totalWidth)
	indentWidth := lipgloss.Width(indent)
	maxIndentWidth := max(0, totalWidth-1)
	if indentWidth > maxIndentWidth {
		indent = ansi.Truncate(indent, maxIndentWidth, "")
		indentWidth = lipgloss.Width(indent)
	}
	rightPad := 0
	contentWidth := max(1, totalWidth-indentWidth-rightPad)
	background := lipgloss.NewStyle().Background(bg)
	whitespace := lipgloss.WithWhitespaceStyle(background)
	styledIndent := background.Render(indent)

	var rows []string
	for _, paragraph := range strings.Split(text, "\n") {
		plain, ranges := parseQuestionMarkdown(paragraph, bg)
		for _, segment := range wrapTextSegments(plain, contentWidth, questionAtomicRanges(ranges)) {
			content := renderQuestionTextRange(plain, segment.start, segment.end, textStyle.Background(bg), ranges)
			styled := styledIndent + content
			rows = append(rows, lipgloss.PlaceHorizontal(totalWidth, lipgloss.Left, styled, whitespace))
		}
	}
	return rows
}

func renderFullWidthBlankLine(width int, bg color.Color) string {
	background := lipgloss.NewStyle().Background(bg)
	return lipgloss.PlaceHorizontal(max(1, width), lipgloss.Left, "", lipgloss.WithWhitespaceStyle(background))
}

func inlineCodeStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.CodeText)).Background(lipgloss.Color(codexLight.CodeSurface))
}

func lightTextStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Primary))
}

func lightMutedStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Muted))
}

func lightSubtleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Subtle))
}

func renderLightToolActivity(title string, output []string, expanded bool, width int, marker string) string {
	width = max(1, width)
	if marker == "" {
		marker = "•"
	}
	lines := make([]string, 0, min(len(output), 3)+2)
	markerColor := codexLight.Success
	if strings.HasPrefix(title, "Failed ") || strings.Contains(title, " · failed") {
		markerColor = codexLight.Error
	} else if strings.HasPrefix(title, "Run ") && marker == "•" {
		marker = titleSpinner[0]
	}
	lines = append(lines, ansi.Truncate(renderLightToolHeader(marker, title, markerColor), width, "…"))
	visible := len(output)
	if !expanded {
		visible = min(3, len(output))
	}
	for index := 0; index < visible; index++ {
		guide := "    "
		if index == 0 {
			guide = "  " + lightSubtleStyle().Render("└ ")
		}
		lines = append(lines, guide+lightMutedStyle().Render(fitTerminalText(output[index], max(1, width-4))))
	}
	if remaining := len(output) - visible; remaining > 0 {
		lines = append(lines, "    "+lightSubtleStyle().Render("+ "+itoa(remaining)+" lines (ctrl+t to expand)"))
	}
	return strings.Join(lines, "\n")
}

func renderLightToolHeader(marker, title, markerColor string) string {
	title = strings.TrimSuffix(title, " · done")
	markerRun := lipgloss.NewStyle().Foreground(lipgloss.Color(markerColor)).Render(marker)
	space := " "
	if marker == "›" {
		markerRun = lightTextStyle().Render(marker)
	}
	verb := ""
	command := title
	for _, candidate := range []string{"Ran ", "Run ", "Failed ", "Read ", "Wrote ", "Edited ", "Listed ", "Found ", "Searched ", "Fetched ", "Processed "} {
		if strings.HasPrefix(title, candidate) {
			verb, command = candidate, strings.TrimPrefix(title, candidate)
			break
		}
	}
	if verb == "" {
		return markerRun + space + lightTextStyle().Render(title)
	}
	verbStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#111827")).Bold(true)
	if strings.HasPrefix(verb, "Failed") {
		verbStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Error)).Bold(true)
	}
	status := ""
	if strings.HasPrefix(verb, "Failed") && strings.HasPrefix(command, "(exit ") {
		if close := strings.Index(command, ") "); close >= 0 {
			status = command[:close+1]
			command = command[close+2:]
		}
	}
	activityStatus := ""
	for _, suffix := range []string{" · failed", " · cancelled", " · canceled", " · timed out", " · interrupted", " · running", " · stopping"} {
		if index := strings.LastIndex(command, suffix); index >= 0 {
			activityStatus = command[index:]
			command = command[:index]
			break
		}
	}

	var target string
	if status != "" {
		target = lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Error)).Render(status) + " " + renderLightCommand(command)
	} else if strings.HasPrefix(verb, "Ran") || strings.HasPrefix(verb, "Run") || strings.HasPrefix(verb, "Failed") {
		target = renderLightCommand(command)
	} else {
		target = renderLightActionTarget(verb, command)
	}
	header := markerRun + space + verbStyle.Render(strings.TrimSpace(verb)) + " " + target
	if activityStatus != "" {
		statusColor := codexLight.Muted
		if strings.Contains(activityStatus, "failed") {
			statusColor = codexLight.Error
		}
		header += lipgloss.NewStyle().Foreground(lipgloss.Color(statusColor)).Render(activityStatus)
	}
	return header
}

func renderLightActionTarget(verb, target string) string {
	details := ""
	if strings.HasPrefix(verb, "Edited") {
		if index := strings.LastIndex(target, " (+"); index >= 0 && strings.HasSuffix(target, ")") {
			details = target[index:]
			target = strings.TrimSpace(target[:index])
		}
	}
	style := lightTextStyle()
	fields := strings.Fields(target)
	if len(fields) > 0 && actionTargetIsPath(verb, target) {
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("#15803D")).Background(lipgloss.Color(codexLight.CodeSurface))
	}
	return style.Render(target) + lightTextStyle().Render(details)
}

func actionTargetIsPath(verb, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	switch strings.TrimSpace(verb) {
	case "Read", "Wrote", "Edited", "Listed":
		switch strings.ToLower(target) {
		case "a workspace file", "a directory", "files":
			return false
		default:
			return true
		}
	default:
		fields := strings.Fields(target)
		return len(fields) > 0 && isPathLike(fields[0], target)
	}
}

func isPathLike(arg, fullRest string) bool {
	arg = strings.TrimSpace(arg)
	fullRest = strings.TrimSpace(fullRest)
	if arg == "" || fullRest == "" {
		return false
	}
	firstToken := strings.Fields(fullRest)[0]
	if strings.ContainsAny(fullRest, " \t\r\n") && !strings.ContainsAny(firstToken, `/\\.`) {
		return false
	}
	if strings.ContainsAny(arg, `/\\.`) {
		return true
	}
	return strings.EqualFold(arg, "Makefile") || strings.EqualFold(arg, "Dockerfile")
}

func renderLightCommand(command string) string {
	styled := lightTextStyle().Render(command)
	return lipgloss.StyleRanges(styled, lightCommandStyleRanges(command)...)
}

type lightCommandStyleRange struct {
	start int
	end   int
	style lipgloss.Style
}

func lightCommandStyleRanges(command string) []lipgloss.Range {
	var ranges []lipgloss.Range
	for _, segment := range lightCommandSegments(command) {
		ranges = append(ranges, lipgloss.NewRange(
			lipgloss.Width(command[:segment.start]), lipgloss.Width(command[:segment.end]), segment.style,
		))
	}
	return ranges
}

func lightCommandLineRanges(line string, commandStart int, command string) []lipgloss.Range {
	var ranges []lipgloss.Range
	for _, segment := range lightCommandSegments(command) {
		lineStart, lineEnd := commandStart+segment.start, commandStart+segment.end
		if lineStart < 0 || lineEnd > len(line) || lineStart >= lineEnd {
			continue
		}
		ranges = append(ranges, lipgloss.NewRange(
			lipgloss.Width(line[:lineStart]), lipgloss.Width(line[:lineEnd]), segment.style,
		))
	}
	return ranges
}

func lightCommandSegments(command string) []lightCommandStyleRange {
	commandStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#2563EB"))
	flagStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#B91C1C"))
	quotedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#15803D"))
	var ranges []lightCommandStyleRange
	tokenIndex := 0
	for cursor := 0; cursor < len(command); {
		for cursor < len(command) && (command[cursor] == ' ' || command[cursor] == '\t') {
			cursor++
		}
		if cursor >= len(command) {
			break
		}
		tokenStart := cursor
		quotedStart := -1
		quote := byte(0)
		var quotedRanges [][2]int
		for cursor < len(command) {
			character := command[cursor]
			if quote != 0 {
				if character == '\\' && quote == '"' && cursor+1 < len(command) {
					cursor += 2
					continue
				}
				if character == quote {
					quote = 0
					quotedRanges = append(quotedRanges, [2]int{quotedStart, cursor + 1})
					quotedStart = -1
				}
				cursor++
				continue
			}
			if character == ' ' || character == '\t' {
				break
			}
			if character == '\'' || character == '"' || character == '`' {
				quote = character
				quotedStart = cursor
			}
			cursor++
		}
		if quotedStart >= 0 {
			quotedRanges = append(quotedRanges, [2]int{quotedStart, cursor})
		}
		tokenEnd := cursor
		var tokenStyle lipgloss.Style
		hasTokenStyle := false
		switch {
		case tokenIndex == 0:
			tokenStyle = commandStyle
			hasTokenStyle = true
		case strings.HasPrefix(command[tokenStart:tokenEnd], "-"):
			tokenStyle = flagStyle
			hasTokenStyle = true
		}
		segmentStart := tokenStart
		for _, quoted := range quotedRanges {
			if hasTokenStyle && segmentStart < quoted[0] {
				ranges = append(ranges, lightCommandStyleRange{start: segmentStart, end: quoted[0], style: tokenStyle})
			}
			ranges = append(ranges, lightCommandStyleRange{start: quoted[0], end: quoted[1], style: quotedStyle})
			segmentStart = quoted[1]
		}
		if hasTokenStyle && segmentStart < tokenEnd {
			ranges = append(ranges, lightCommandStyleRange{start: segmentStart, end: tokenEnd, style: tokenStyle})
		}
		tokenIndex++
	}
	return ranges
}

func renderScrollPill(width int, key string) string {
	pill := scrollPillContent(key)
	left := max(0, (width-lipgloss.Width(pill))/2)
	return strings.Repeat(" ", left) + pill
}

func scrollPillContent(key string) string {
	label := "↓ Back to bottom · " + key
	return lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.PillText)).Background(lipgloss.Color(codexLight.PillSurface)).Padding(0, 1).Render(label)
}

func itoa(value int) string {
	if value < 0 {
		value = 0
	}
	return strconv.Itoa(value)
}

// renderToolActivity returns a compact, tree-indented tool record. The first
// three output lines stay visible until the caller expands the invocation.
func renderToolActivity(cmd string, output []string, expanded bool, width int) string {
	if !strings.HasPrefix(cmd, "Ran ") && !strings.HasPrefix(cmd, "Run ") && !strings.HasPrefix(cmd, "Failed ") &&
		!strings.HasPrefix(cmd, "Read ") && !strings.HasPrefix(cmd, "Wrote ") && !strings.HasPrefix(cmd, "Edited ") &&
		!strings.HasPrefix(cmd, "Listed ") && !strings.HasPrefix(cmd, "Found ") && !strings.HasPrefix(cmd, "Searched ") &&
		!strings.HasPrefix(cmd, "Fetched ") && !strings.HasPrefix(cmd, "Processed ") {
		cmd = "Ran " + cmd
	}
	return renderLightToolActivity(cmd, output, expanded, width, "•")
}
