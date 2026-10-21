package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const AppName = "lune"

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
	// Softer accent used by question cards and plan content
	AccentSoft string
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
	// Inline code & code block tokens
	InlineCodeBg string
	InlineCodeFg string
	CodeBlockBg  string
}

// LightTheme returns the light theme palette.
var LightTheme = Theme{
	Canvas:       "#FFFFFF",
	CardBg:       "#F6F8FA",
	TextPrimary:  "#24292F",
	TextMuted:    "#6E7781",
	AccentBlue:   "#0969DA",
	AccentSoft:   "#245EA8",
	SelectBg:     "#DCEBFA",
	SelectFg:     "#174A7E",
	PlanPurple:   "#8250DF",
	Border:       "#D8DEE4",
	SuccessGreen: "#1A7F37",
	FailureRed:   "#CF222E",
	WarningAmber: "#9A6700",
	ExploredGray: "#57606A",
	DiffAddBg:    "#DAFBE1",
	DiffDelBg:    "#FFEBE9",
	InlineCodeBg: "",
	InlineCodeFg: "#0550AE",
	CodeBlockBg:  "#EFF3F7",
}

// DarkTheme returns the dark equivalent for terminals with dark backgrounds.
var DarkTheme = Theme{
	Canvas:       "#0D1117",
	CardBg:       "#161B22",
	TextPrimary:  "#E6EDF3",
	TextMuted:    "#8B949E",
	AccentBlue:   "#58A6FF",
	AccentSoft:   "#8AB4F8",
	SelectBg:     "#26364D",
	SelectFg:     "#C9D9F0",
	PlanPurple:   "#BC8CFF",
	Border:       "#30363D",
	SuccessGreen: "#3FB950",
	FailureRed:   "#F85149",
	WarningAmber: "#D29922",
	ExploredGray: "#8B949E",
	DiffAddBg:    "#12261E",
	DiffDelBg:    "#2D1619",
	InlineCodeBg: "",
	InlineCodeFg: "#79C0FF",
	CodeBlockBg:  "#1C2128",
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
	cachedStylePlan     lipgloss.Style
	cachedStyleSelected lipgloss.Style
)

func init() {
	recalculateThemeStyles()
}

func recalculateThemeStyles() {
	th := currentTheme
	cachedStylePrimary = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextPrimary))
	cachedStyleMuted = lipgloss.NewStyle().Foreground(lipgloss.Color(th.TextMuted))
	cachedStylePlan = lipgloss.NewStyle().Foreground(lipgloss.Color(th.PlanPurple))
	cachedStyleSelected = lipgloss.NewStyle().
		Background(lipgloss.Color(th.SelectBg)).
		Foreground(lipgloss.Color(th.SelectFg)).
		Bold(true)
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

func stylePlanPurple() lipgloss.Style {
	return cachedStylePlan
}

func styleSelectedRow() lipgloss.Style {
	return cachedStyleSelected
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

func panelCardStyle(width int) lipgloss.Style {
	th := activeTheme()
	return lipgloss.NewStyle().
		Background(lipgloss.Color(th.CardBg)).
		Foreground(lipgloss.Color(th.TextPrimary)).
		Padding(1, 2).
		Width(max(20, width))
}

func renderSurfaceCard(content string, width, paddingX, paddingY int) string {
	width = max(1, width)
	paddingX = max(0, min(paddingX, (width-1)/2))
	innerWidth := max(1, width-2*paddingX)
	rows := make([]string, 0, strings.Count(content, "\n")+2*paddingY+1)
	emptyRow := strings.Repeat(" ", width)
	for range paddingY {
		rows = append(rows, emptyRow)
	}
	for _, sourceLine := range strings.Split(content, "\n") {
		for _, line := range strings.Split(ansi.Hardwrap(sourceLine, innerWidth, true), "\n") {
			row := strings.Repeat(" ", paddingX) + line
			row += strings.Repeat(" ", max(0, width-ansi.StringWidth(row)))
			rows = append(rows, row)
		}
	}
	for range paddingY {
		rows = append(rows, emptyRow)
	}
	th := activeTheme()
	return applySurfaceBg(strings.Join(rows, "\n"), th.CardBg, th.TextPrimary)
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
