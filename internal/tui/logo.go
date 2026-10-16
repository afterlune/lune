package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// BrailleLogoLines is the exact Codex braille spiral logo.
var BrailleLogoLines = []string{
	"                  ⣠⣴⣶⣿⣿⣷⣶⣤⣀⣀⣀⣀⣀",
	"                ⢠⣾⣿⡿⠟⠛⠉⣻⣿⣿⣿⣿⣿⣿⣿⣿⣦⡀",
	"              ⢀⣀⣿⣿⡟⢀⣠⣶⣿⣿⠿⠛⢉⣀⣀ ⠙⢿⣿⣿⣆",
	"            ⢀⣾⣿⣿⣿⣿ ⢸⣿⡿⠋⢁⣤⣶⣿⣿⣿⣿⣦⣄⣻⣿⣿",
	"           ⢠⣿⣿⡟⢹⣿⣿ ⢸⣿⣷⣾⣿⣿⣿⣯⣀⠉⠛⢿⣿⣿⣿⣿",
	"           ⣼⣿⣿ ⢸⣿⣿ ⢸⣿⡿⠛⠁⠈⠛⢿⣿⣿⣦⣄⡈⠙⢿⣿⣷⡀",
	"           ⢸⣿⣿⡀⠸⣿⣿⣶⣼⣿⡇    ⢸⣿⣟⠿⣿⣿⡆ ⢻⣿⣧",
	"            ⢻⣿⣿⣄⡈⠙⠻⣿⣿⣷⣤⡀⢀⣤⣾⣿⣿ ⢸⣿⡇ ⣸⣿⣿",
	"             ⣿⣿⣿⣿⣷⣦⣀⠉⣻⣿⣿⣿⡿⢿⣿⣿ ⢸⣿⡇⣠⣿⣿⡟",
	"             ⢿⣿⣧⠉⠻⣿⣿⣿⣿⡿⠛⠉⣀⣼⣿⣿ ⣸⣿⣿⣿⣿⠟",
	"             ⠘⢿⣿⣷⣄⡀⠉⠉⢁⣠⣴⣿⣿⡿⠟⠋⢀⣿⣿⡟⠋⠁",
	"              ⠈⠛⢿⣿⣿⣿⣿⣿⣿⣿⣛⣉⣀⣀⣴⣿⣿⡟",
	"                 ⠈⠉⠉⠉⠙⠻⣿⣿⣿⣿⣿⣿⠿⠋",
	"                        ⠈⠉⠉",
}

// RenderLogoEmptyState renders the centered braille logo within the given width and height.
func RenderLogoEmptyState(width, height int) string {
	if height < 14 || width < 40 {
		return ""
	}
	logoHeight := len(BrailleLogoLines)
	topPadding := max(0, (height-logoHeight)/2)

	var lines []string
	for i := 0; i < topPadding; i++ {
		lines = append(lines, "")
	}

	logoStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted))
	for _, rawLine := range BrailleLogoLines {
		lineWidth := lipgloss.Width(rawLine)
		leftPad := max(0, (width-lineWidth)/2)
		line := strings.Repeat(" ", leftPad) + logoStyle.Render(rawLine)
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}
