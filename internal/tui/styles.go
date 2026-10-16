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
	Subtle: "#9CA3AF", Success: "#16A34A", CodeSurface: "#E6F4EA", CodeText: "#137333",
	Flag: "#B91C1C", PillSurface: "#E0F2FE", PillText: "#0284C7", Mode: "#C026D3",
	TaskSummary: "#B45309", Error: "#B91C1C",
}

func renderFullWidthBand(lines []string, width int, bg color.Color, padX, padY int) string {
	width = max(1, width)
	padY = max(0, padY)
	padX = max(0, min(padX, (width-1)/2))
	contentWidth := max(1, width-2*padX)
	background := lipgloss.NewStyle().Background(bg)
	whitespace := lipgloss.WithWhitespaceStyle(background)
	emptyRow := func() string {
		return lipgloss.PlaceHorizontal(width, lipgloss.Left, "", whitespace)
	}

	rows := make([]string, 0, len(lines)+padY*2)
	for range padY {
		rows = append(rows, emptyRow())
	}
	for _, line := range lines {
		for _, paragraphLine := range strings.Split(line, "\n") {
			wrapped := ansi.Wrap(paragraphLine, contentWidth, "")
			for _, wrappedLine := range strings.Split(wrapped, "\n") {
				content := background.Render(strings.Repeat(" ", padX)) + wrappedLine
				rows = append(rows, lipgloss.PlaceHorizontal(width, lipgloss.Left, content, whitespace))
			}
		}
	}
	for range padY {
		rows = append(rows, emptyRow())
	}
	return strings.Join(rows, "\n")
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
		marker = windowTitleSpinner[0]
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
	verbStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Primary)).Bold(true)
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
	if isLightPathTarget(target) {
		style = lipgloss.NewStyle().Foreground(lipgloss.Color("#15803D")).Background(lipgloss.Color(codexLight.CodeSurface))
	}
	return style.Render(target) + lightTextStyle().Render(details)
}

func isLightPathTarget(target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if strings.ContainsAny(target, `/\\.`) {
		return true
	}
	// A lone target is commonly an extensionless filename (for example Makefile).
	// Multi-word fallback summaries such as "a workspace file" remain prose.
	return !strings.ContainsAny(target, " \t\r\n")
}

func renderLightCommand(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return lightTextStyle().Render(command)
	}
	var out strings.Builder
	commandStyle := inlineCodeStyle()
	flagStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Flag))
	for index, field := range fields {
		if index > 0 {
			out.WriteByte(' ')
		}
		switch {
		case strings.HasPrefix(field, "-"):
			out.WriteString(flagStyle.Render(field))
		case index == 0:
			out.WriteString(commandStyle.Render(field))
		default:
			out.WriteString(lightTextStyle().Render(field))
		}
	}
	return out.String()
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
