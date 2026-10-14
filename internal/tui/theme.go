package tui

import "charm.land/lipgloss/v2"

type themeColors struct {
	Background string
	Foreground string
	Muted      string
	Accent     string
	Secondary  string
	Success    string
	Warning    string
	Error      string
	User       string
	Assistant  string
	Tool       string
	Code       string
}

func colorsFor(theme themeName) themeColors {
	switch theme {
	case themeLight:
		return themeColors{
			Background: "#f5f1e8", Foreground: "#2e3338", Muted: "#6a665f",
			Accent: "#925526", Secondary: "#805232", Success: "#4f6f52",
			Warning: "#895d22", Error: "#a3473e", User: "#8a4d25",
			Assistant: "#2e3338", Tool: "#6a5e53", Code: "#7c532c",
		}
	default:
		return themeColors{
			Background: "#0b1117", Foreground: "#e7dfd3", Muted: "#948f88",
			Accent: "#d29a5a", Secondary: "#b8793f", Success: "#9ab378",
			Warning: "#e0ae60", Error: "#d17a6e", User: "#e2b987",
			Assistant: "#e7dfd3", Tool: "#c7b39a", Code: "#d4ad7a",
		}
	}
}

func (m *tuiModel) styled(color string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
}
