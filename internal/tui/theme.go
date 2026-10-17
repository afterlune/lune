package tui

import (
	"charm.land/lipgloss/v2"
)

const (
	AppName    = "LuneAgent"
	AppVersion = "v0.1.0"
)

// Theme defines the color palette for the LuneAgent TUI.
type Theme struct {
	// Canvas background
	Canvas string
	// Primary text
	TextPrimary string
	// Secondary text
	TextMuted string
	// Accent / links / prompts
	AccentBlue string
	// Selected row background
	SelectBg string
	// Selected row text
	SelectFg string
	// Plan mode accent (purple/magenta)
	PlanPurple string
	// Card / Composer background
	CardBg string
	// Border / divider color
	Border string
	// Tool status colors
	SuccessGreen string
	FailureRed   string
	WarningAmber string
	ExploredGray string
}

// LightTheme returns the light theme palette.
var LightTheme = Theme{
	Canvas:       "#FFFFFF",
	CardBg:       "#F6F8FA",
	TextPrimary:  "#1F2328",
	TextMuted:    "#656D76",
	AccentBlue:   "#0969DA",
	SelectBg:     "#A4CDFB",
	SelectFg:     "#0550AE",
	PlanPurple:   "#8250DF",
	Border:       "#D0D7DE",
	SuccessGreen: "#1A7F37",
	FailureRed:   "#CF222E",
	WarningAmber: "#9A6700",
	ExploredGray: "#57606A",
}

// DarkTheme returns the dark equivalent for terminals with dark backgrounds.
var DarkTheme = Theme{
	Canvas:       "#0D1117",
	CardBg:       "#161B22",
	TextPrimary:  "#E6EDF3",
	TextMuted:    "#8B949E",
	AccentBlue:   "#58A6FF",
	SelectBg:     "#1F6FEB",
	SelectFg:     "#FFFFFF",
	PlanPurple:   "#BC8CFF",
	Border:       "#30363D",
	SuccessGreen: "#3FB950",
	FailureRed:   "#F85149",
	WarningAmber: "#D29922",
	ExploredGray: "#8B949E",
}

// ThemeMode specifies the theme preference.
type ThemeMode string

const (
	ThemeModeAuto  ThemeMode = "auto"
	ThemeModeDark  ThemeMode = "dark"
	ThemeModeLight ThemeMode = "light"
)

var (
	currentThemeMode = ThemeModeAuto
	isTerminalDark   = true
	currentTheme     = DarkTheme
)

func activeTheme() Theme {
	return currentTheme
}

// SetThemeMode updates the active theme mode and recalculates active theme.
func SetThemeMode(mode ThemeMode) {
	currentThemeMode = mode
	switch mode {
	case ThemeModeDark:
		currentTheme = DarkTheme
	case ThemeModeLight:
		currentTheme = LightTheme
	case ThemeModeAuto:
		if isTerminalDark {
			currentTheme = DarkTheme
		} else {
			currentTheme = LightTheme
		}
	}
}

// UpdateTerminalBackground informs the theme system of the terminal's detected background.
func UpdateTerminalBackground(dark bool) {
	isTerminalDark = dark
	if currentThemeMode == ThemeModeAuto {
		if dark {
			currentTheme = DarkTheme
		} else {
			currentTheme = LightTheme
		}
	}
}

// CurrentThemeMode returns the active theme mode setting.
func CurrentThemeMode() ThemeMode {
	return currentThemeMode
}

func styleTextPrimary() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextPrimary))
}

func styleTextMuted() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().TextMuted))
}

func styleAccentBlue() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().AccentBlue))
}

func stylePlanPurple() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().PlanPurple))
}

func styleSuccess() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().SuccessGreen))
}

func styleFailure() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(activeTheme().FailureRed))
}

func styleSelectedRow() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme().SelectBg)).
		Foreground(lipgloss.Color(activeTheme().SelectFg)).
		Bold(true)
}

func styleCard() lipgloss.Style {
	return lipgloss.NewStyle().
		Background(lipgloss.Color(activeTheme().CardBg)).
		Foreground(lipgloss.Color(activeTheme().TextPrimary))
}
