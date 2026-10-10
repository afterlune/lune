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
			Background: "#171a1e", Foreground: "#f0f1f2", Muted: "#9aa3ad",
			Accent: "#78aaa2", Secondary: "#c6a56e", Success: "#73ae8a",
			Warning: "#c6a56e", Error: "#c77a7a", User: "#a7b1ba",
			Assistant: "#f0f1f2", Tool: "#92a0a9", Code: "#cbd3db",
		}
	}
}

func (m *tuiModel) styled(color string) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
}
