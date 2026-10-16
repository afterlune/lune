package tui

import (
	"image/color"
	"math"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestThemeSemanticColorsStayReadableAndSubdued(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		t.Run(string(theme), func(t *testing.T) {
			colors := colorsFor(theme)
			terminalBackground := terminalScreenshotBackground(theme)
			if theme == themeLight {
				if colors.Background != "#FFFFFF" || colors.Foreground != "#1F2937" || colors.Muted != "#6B7280" ||
					colors.Success != "#16A34A" || codexLight.Surface != "#F3F4F6" ||
					codexLight.CodeSurface != "#E6F4EA" || codexLight.CodeText != "#15803D" {
					t.Fatalf("light palette does not match the approved design: %#v / %#v", colors, codexLight)
				}
				if ratio := themeContrastRatio(colors.Foreground, terminalBackground); ratio < 4.5 {
					t.Errorf("light primary contrast = %.2f:1, want at least 4.5:1", ratio)
				}
				return
			}
			semantic := map[string]string{
				"foreground": colors.Foreground, "muted": colors.Muted, "accent": colors.Accent,
				"secondary": colors.Secondary, "success": colors.Success, "warning": colors.Warning,
				"error": colors.Error, "user": colors.User, "assistant": colors.Assistant,
				"tool": colors.Tool, "code": colors.Code,
			}
			for role, foreground := range semantic {
				if ratio := themeContrastRatio(foreground, terminalBackground); ratio < 4.5 {
					t.Errorf("%s contrast = %.2f:1, want at least 4.5:1", role, ratio)
				}
				if saturation := themeHSLSaturation(foreground); saturation > 0.68 {
					t.Errorf("%s saturation = %.2f, want at most 0.68", role, saturation)
				}
				components := parseHexColor(foreground)
				if max(components[0], max(components[1], components[2])) > 242 {
					t.Errorf("%s contains a near-white color channel: %s", role, foreground)
				}
			}
			if ratio := themeContrastRatio(colors.Background, colors.Secondary); ratio < 4.5 {
				t.Errorf("selected text contrast = %.2f:1, want at least 4.5:1", ratio)
			}
		})
	}
}

func TestAutoThemeUsesTerminalColorsAndManualThemesOverride(t *testing.T) {
	ui := newTUIModel(nil, false)
	if ui.preferences.Theme != themeAuto {
		t.Fatalf("default theme = %q, want auto", ui.preferences.Theme)
	}
	ui.Update(tea.BackgroundColorMsg{Color: color.RGBA{R: 255, G: 255, B: 255, A: 255}})
	ui.Update(tea.ForegroundColorMsg{Color: color.RGBA{A: 255}})
	if got := ui.resolvedTheme(); got != themeLight {
		t.Fatalf("resolved theme = %q, want light", got)
	}
	view := ui.View()
	if view.BackgroundColor != nil || colorHex(view.ForegroundColor) != "#000000" || !view.AltScreen {
		t.Fatalf("auto view colors/background/screen = %s / %s / %v, want inherited background, terminal text, and alternate screen", colorHex(view.BackgroundColor), colorHex(view.ForegroundColor), view.AltScreen)
	}
	if ui.themeColors().Assistant != "#000000" {
		t.Fatalf("assistant color = %q, want terminal foreground", ui.themeColors().Assistant)
	}

	ui.preferences.Theme = themeDark
	view = ui.View()
	if view.BackgroundColor != nil || colorHex(view.ForegroundColor) != colorsFor(themeDark).Foreground {
		t.Fatalf("manual dark view = %s / %s, want inherited background and dark foreground palette", colorHex(view.BackgroundColor), colorHex(view.ForegroundColor))
	}
	ui.preferences.Theme = themeLight
	view = ui.View()
	if colorHex(view.BackgroundColor) != "#ffffff" || !strings.EqualFold(colorHex(view.ForegroundColor), colorsFor(themeLight).Foreground) {
		t.Fatalf("manual light view = %s / %s, want white canvas and light foreground palette", colorHex(view.BackgroundColor), colorHex(view.ForegroundColor))
	}

	ui.preferences.Theme = themeAuto
	ui.terminalBackground = nil
	ui.terminalForeground = nil
	ui.Update(tea.ForegroundColorMsg{Color: color.RGBA{A: 255}})
	if got := ui.resolvedTheme(); got != themeLight {
		t.Fatalf("foreground fallback resolved theme = %q, want light", got)
	}
	view = ui.View()
	if view.BackgroundColor != nil || colorHex(view.ForegroundColor) != "#000000" {
		t.Fatalf("foreground-only fallback view colors = %s / %s, want terminal default background and black foreground", colorHex(view.BackgroundColor), colorHex(view.ForegroundColor))
	}
}

func TestAutoThemeKeepsTranscriptInAlternateScreenDuringTerminalColorQuery(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.entries = []tuiEntry{{role: "assistant", text: "Existing answer."}}
	ui.refreshPanels()
	ui.Init()
	if !ui.terminalColorQueryPending || !ui.View().AltScreen || !strings.Contains(ui.View().Content, "Existing answer.") {
		t.Fatal("TUI did not render the transcript in the alternate screen while querying terminal colors")
	}
	ui.Update(tea.BackgroundColorMsg{Color: color.White})
	if !ui.terminalColorQueryPending {
		t.Fatal("one terminal color response completed the two-color query")
	}
	ui.Update(tea.ForegroundColorMsg{Color: color.Black})
	if ui.terminalColorQueryPending || ui.View().BackgroundColor != nil || !strings.Contains(ui.View().Content, "Existing answer.") {
		t.Fatal("terminal color resolution changed the inherited background or hid the transcript")
	}

	ui = newTUIModel(nil, false)
	ui.Init()
	ui.Update(terminalColorTimeoutMsg{})
	if ui.terminalColorQueryPending || !ui.View().AltScreen || ui.View().BackgroundColor != nil {
		t.Fatal("unsupported terminal color query did not keep the TUI transparent and fullscreen")
	}
}

func themeContrastRatio(foreground, background string) float64 {
	front, back := themeRelativeLuminance(foreground), themeRelativeLuminance(background)
	return (math.Max(front, back) + 0.05) / (math.Min(front, back) + 0.05)
}

func themeRelativeLuminance(color string) float64 {
	components := parseHexColor(color)
	values := [3]float64{}
	for index, component := range components {
		value := float64(component) / 255
		if value <= 0.04045 {
			values[index] = value / 12.92
		} else {
			values[index] = math.Pow((value+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*values[0] + 0.7152*values[1] + 0.0722*values[2]
}

func themeHSLSaturation(color string) float64 {
	components := parseHexColor(color)
	red, green, blue := float64(components[0])/255, float64(components[1])/255, float64(components[2])/255
	maximum := math.Max(red, math.Max(green, blue))
	minimum := math.Min(red, math.Min(green, blue))
	delta := maximum - minimum
	lightness := (maximum + minimum) / 2
	if delta == 0 {
		return 0
	}
	return delta / (1 - math.Abs(2*lightness-1))
}
