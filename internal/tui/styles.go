package tui

import (
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
	Subtle: "#9CA3AF", Success: "#16A34A", CodeSurface: "#ECFDF5", CodeText: "#15803D",
	Flag: "#B91C1C", PillSurface: "#E0F2FE", PillText: "#0284C7", Mode: "#C026D3",
	TaskSummary: "#B45309", Error: "#B91C1C",
}

func fullWidthBand(content string, width int, background string) string {
	width = max(1, width)
	lines := strings.Split(content, "\n")
	for index, line := range lines {
		lineWidth := lipgloss.Width(line)
		if lineWidth > width {
			line = fitTerminalText(line, width)
			lineWidth = lipgloss.Width(line)
		}
		lines[index] = line + strings.Repeat(" ", max(0, width-lineWidth))
	}
	return lipgloss.NewStyle().Width(width).Background(lipgloss.Color(background)).Render(strings.Join(lines, "\n"))
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
	if strings.HasPrefix(title, "Failed ") {
		markerColor = codexLight.Error
	} else if strings.HasPrefix(title, "Running ") && marker == "•" {
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
	markerRun := lipgloss.NewStyle().Foreground(lipgloss.Color(markerColor)).Render(marker)
	space := " "
	if marker == "›" {
		markerRun = lightTextStyle().Render(marker)
	}
	verb := ""
	command := title
	for _, candidate := range []string{"Ran ", "Running ", "Failed ", "Edited ", "Reading ", "Writing ", "Searching ", "Fetching ", "Listing "} {
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
	if status != "" {
		return markerRun + space + verbStyle.Render(strings.TrimSpace(verb)) + " " +
			lipgloss.NewStyle().Foreground(lipgloss.Color(codexLight.Error)).Render(status) + " " + renderLightCommand(command)
	}
	return markerRun + space + verbStyle.Render(strings.TrimSpace(verb)) + " " + renderLightCommand(command)
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
	if !strings.HasPrefix(cmd, "Ran ") && !strings.HasPrefix(cmd, "Running ") && !strings.HasPrefix(cmd, "Failed ") &&
		!strings.HasPrefix(cmd, "Edited ") && !strings.HasPrefix(cmd, "Reading ") && !strings.HasPrefix(cmd, "Writing ") &&
		!strings.HasPrefix(cmd, "Searching ") && !strings.HasPrefix(cmd, "Fetching ") && !strings.HasPrefix(cmd, "Listing ") {
		cmd = "Ran " + cmd
	}
	return renderLightToolActivity(cmd, output, expanded, width, "•")
}
