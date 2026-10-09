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
			Background: "#f4f6f8", Foreground: "#202a35", Muted: "#657384",
			Accent: "#176b87", Secondary: "#b54e3c", Success: "#287a55",
			Warning: "#986500", Error: "#b33843", User: "#4f6476",
			Assistant: "#176b87", Tool: "#b54e3c", Code: "#344c60",
		}
	case themeAnime:
		return themeColors{
			Background: "#101c24", Foreground: "#f0f4e8", Muted: "#9db2b9",
			Accent: "#5bd4c4", Secondary: "#ff9c72", Success: "#88d68a",
			Warning: "#ffd16e", Error: "#ff7b83", User: "#b7cad1",
			Assistant: "#79e2d0", Tool: "#ffad82", Code: "#d8e8a7",
		}
	default:
		return themeColors{
			Background: "#111318", Foreground: "#e7e9ed", Muted: "#8c929d",
			Accent: "#8ab4f8", Secondary: "#c4a7e7", Success: "#78c99e",
			Warning: "#e8bd6d", Error: "#ef7d87", User: "#a9b0bb",
			Assistant: "#e7e9ed", Tool: "#c4a7e7", Code: "#d2d8df",
		}
	}
}

func (m *tuiModel) styled(color string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
}
