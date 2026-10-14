package tui

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
)

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

func (m *tuiModel) resolvedTheme() themeName {
	switch m.preferences.Theme {
	case themeLight, themeDark:
		return m.preferences.Theme
	default:
		if m.terminalBackground != nil {
			if !colorIsDark(m.terminalBackground) {
				return themeLight
			}
		} else if m.terminalForeground != nil && colorIsDark(m.terminalForeground) {
			return themeLight
		}
		return themeDark
	}
}

func (m *tuiModel) themeColors() themeColors {
	colors := colorsFor(m.resolvedTheme())
	if m.preferences.Theme == themeAuto {
		if m.terminalBackground != nil {
			colors.Background = colorHex(m.terminalBackground)
		}
		if m.terminalForeground != nil {
			colors.Foreground = colorHex(m.terminalForeground)
			colors.Assistant = colors.Foreground
		}
	}
	return colors
}

func (m *tuiModel) applyResolvedTheme() {
	applyTextareaTheme(&m.input, m.resolvedTheme())
	m.applyMCPInteractionTheme(m.resolvedTheme())
	m.updateLayout()
	m.refreshPanels()
}

func colorIsDark(value color.Color) bool {
	if value == nil {
		return true
	}
	r, g, b, _ := value.RGBA()
	return (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) < 0x8000
}

func colorHex(value color.Color) string {
	if value == nil {
		return ""
	}
	r, g, b, _ := value.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

func (m *tuiModel) styled(color string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
}
