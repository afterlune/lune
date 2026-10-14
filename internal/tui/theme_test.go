package tui

import (
	"math"
	"testing"
)

func TestThemeSemanticColorsStayReadableAndSubdued(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		t.Run(string(theme), func(t *testing.T) {
			colors := colorsFor(theme)
			semantic := map[string]string{
				"foreground": colors.Foreground, "muted": colors.Muted, "accent": colors.Accent,
				"secondary": colors.Secondary, "success": colors.Success, "warning": colors.Warning,
				"error": colors.Error, "user": colors.User, "assistant": colors.Assistant,
				"tool": colors.Tool, "code": colors.Code,
			}
			for role, foreground := range semantic {
				if ratio := themeContrastRatio(foreground, colors.Background); ratio < 4.5 {
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
