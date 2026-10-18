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
	// Diff row background colors
	DiffAddBg string
	DiffDelBg string
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
	DiffAddBg:    "#DAFBE1",
	DiffDelBg:    "#FFEBE9",
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
	DiffAddBg:    "#12261E",
	DiffDelBg:    "#2D1619",
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

	cachedStylePrimary  lipgloss.Style
	cachedStyleMuted    lipgloss.Style
	cachedStyleAccent   lipgloss.Style
	cachedStylePlan     lipgloss.Style
	cachedStyleSuccess  lipgloss.Style
	cachedStyleFailure  lipgloss.Style
	cachedStyleWarning  lipgloss.Style
	cachedStyleExplored lipgloss.Style
	cachedStyleSelected lipgloss.Style
	cachedStyleCard     lipgloss.Style
)

func init() {
	recalculateThemeStyles()
}

func recalculateThemeStyles() {
	th := currentTheme
	cachedStylePrimary = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary))
	cachedStyleMuted = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
	cachedStyleAccent = lipgloss.NewStyle().Foreground(lipgloss.Color(th.AccentBlue))
	cachedStylePlan = lipgloss.NewStyle().Foreground(lipgloss.Color(th.PlanPurple))
	cachedStyleSuccess = lipgloss.NewStyle().Foreground(lipgloss.Color(th.SuccessGreen))
	cachedStyleFailure = lipgloss.NewStyle().Foreground(lipgloss.Color(th.FailureRed))
	cachedStyleWarning = lipgloss.NewStyle().Foreground(lipgloss.Color(th.WarningAmber))
	cachedStyleExplored = lipgloss.NewStyle().Foreground(lipgloss.Color(th.ExploredGray))
	cachedStyleSelected = lipgloss.NewStyle().
		Background(lipgloss.Color(th.SelectBg)).
		Foreground(lipgloss.Color(th.SelectFg)).
		Bold(true)
	cachedStyleCard = lipgloss.NewStyle().
		Background(lipgloss.Color(th.CardBg)).
		Foreground(lipgloss.Color(th.TextPrimary))
}

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
	recalculateThemeStyles()
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
		recalculateThemeStyles()
	}
}

// CurrentThemeMode returns the active theme mode setting.
func CurrentThemeMode() ThemeMode {
	return currentThemeMode
}

func styleTextPrimary() lipgloss.Style {
	return cachedStylePrimary
}

func styleTextMuted() lipgloss.Style {
	return cachedStyleMuted
}

func styleAccentBlue() lipgloss.Style {
	return cachedStyleAccent
}

func stylePlanPurple() lipgloss.Style {
	return cachedStylePlan
}

func styleSuccess() lipgloss.Style {
	return cachedStyleSuccess
}

func styleFailure() lipgloss.Style {
	return cachedStyleFailure
}

func styleWarning() lipgloss.Style {
	return cachedStyleWarning
}

func styleExplored() lipgloss.Style {
	return cachedStyleExplored
}

func styleSelectedRow() lipgloss.Style {
	return cachedStyleSelected
}

func styleCard() lipgloss.Style {
	return cachedStyleCard
}

func parseHexRGB(hex string) (int, int, int, bool) {
	if len(hex) == 7 && hex[0] == '#' {
		hex = hex[1:]
	}
	if len(hex) != 6 {
		return 0, 0, 0, false
	}
	val := 0
	for i := 0; i < 6; i++ {
		c := hex[i]
		var digit int
		switch {
		case c >= '0' && c <= '9':
			digit = int(c - '0')
		case c >= 'a' && c <= 'f':
			digit = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			digit = int(c-'A') + 10
		default:
			return 0, 0, 0, false
		}
		val = (val << 4) | digit
	}
	return (val >> 16) & 0xFF, (val >> 8) & 0xFF, val & 0xFF, true
}

// applySurfaceBg ensures inner ANSI resets (\x1b[0m or \x1b[m) immediately restore
// the card's background and primary foreground colors so trailing spaces and padding
// never bleed the terminal's default canvas color.
func applySurfaceBg(content, bgHex, fgHex string) string {
	br, bg, bb, okBg := parseHexRGB(bgHex)
	if !okBg {
		return content
	}
	fr, fg, fb, okFg := parseHexRGB(fgHex)
	var restore string
	if okFg {
		restore = "\x1b[48;2;" + itoaByte(br) + ";" + itoaByte(bg) + ";" + itoaByte(bb) +
			";38;2;" + itoaByte(fr) + ";" + itoaByte(fg) + ";" + itoaByte(fb) + "m"
	} else {
		restore = "\x1b[48;2;" + itoaByte(br) + ";" + itoaByte(bg) + ";" + itoaByte(bb) + "m"
	}

	var out []byte
	out = make([]byte, 0, len(content)+128)
	out = append(out, restore...)
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			out = append(out, "\x1b[0m\n"...)
			out = append(out, restore...)
			continue
		}
		if content[i] == '\x1b' && i+2 < len(content) && content[i+1] == '[' {
			if content[i+2] == 'm' {
				out = append(out, "\x1b[0m"...)
				out = append(out, restore...)
				i += 2
				continue
			}
			if content[i+2] == '0' && i+3 < len(content) && content[i+3] == 'm' {
				out = append(out, "\x1b[0m"...)
				out = append(out, restore...)
				i += 3
				continue
			}
		}
		out = append(out, content[i])
	}
	out = append(out, "\x1b[0m"...)
	return string(out)
}

func renderCardSurface(cardStyle lipgloss.Style, content string) string {
	th := activeTheme()
	return cardStyle.Render(applySurfaceBg(content, th.CardBg, th.TextPrimary))
}

func itoaByte(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [3]byte
	i := len(buf)
	for v > 0 && i > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
