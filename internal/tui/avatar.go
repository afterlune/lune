package tui

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	_ "image/png"
	"os"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

//go:embed assets/assistant-states.png
var assistantAtlas []byte

type visualState int

const (
	stateIdle visualState = iota
	stateThinking
	stateWorking
	stateApproval
	stateResult
	stateWarning
)

var (
	decodeAssistantAtlas sync.Once
	decodedAtlas         image.Image
	atlasDecodeErr       error
)

func stateName(state visualState) string {
	switch state {
	case stateThinking:
		return "Thinking"
	case stateWorking:
		return "Working"
	case stateApproval:
		return "Needs approval"
	case stateResult:
		return "Result ready"
	case stateWarning:
		return "Paused"
	default:
		return "Ready"
	}
}

func stateGlyph(state visualState) string {
	switch state {
	case stateThinking:
		return "…"
	case stateWorking:
		return "↻"
	case stateApproval:
		return "?"
	case stateResult:
		return "✓"
	case stateWarning:
		return "!"
	default:
		return "●"
	}
}

func (m *tuiModel) visualState() visualState {
	if m.pendingApproval != nil {
		return stateApproval
	}
	if m.running {
		for index := len(m.activities) - 1; index >= 0; index-- {
			if m.activities[index].status == "running" {
				return stateWorking
			}
		}
		return stateThinking
	}
	status := strings.ToLower(m.status)
	if m.pending || strings.Contains(status, "failed") || strings.Contains(status, "paused") || strings.Contains(status, "cancelled") {
		return stateWarning
	}
	if strings.Contains(strings.ToLower(m.status), "completed") || m.planReview {
		return stateResult
	}
	return stateIdle
}

func (m *tuiModel) canRenderColorArt() bool {
	_, noColor := os.LookupEnv("NO_COLOR")
	return m.preferences.Theme == themeAnime && !noColor && m.colorProfile == colorprofile.TrueColor
}

func (m *tuiModel) renderAvatar(width, height int) string {
	state := m.visualState()
	if m.canRenderColorArt() {
		if art := assistantSprite(state, width, height, colorsFor(themeAnime).Background); art != "" {
			return art
		}
	}
	return m.styled(colorsFor(m.preferences.Theme).Accent).Bold(true).Render(stateGlyph(state))
}

func backgroundColor(value string) color.Color {
	rgb := parseHexColor(value)
	return color.RGBA{R: rgb[0], G: rgb[1], B: rgb[2], A: 0xff}
}

func assistantSprite(state visualState, width, height int, background string) string {
	if width <= 0 || height <= 0 {
		return ""
	}
	decodeAssistantAtlas.Do(func() {
		decodedAtlas, _, atlasDecodeErr = image.Decode(bytes.NewReader(assistantAtlas))
	})
	if atlasDecodeErr != nil || decodedAtlas == nil {
		return ""
	}
	bounds := decodedAtlas.Bounds()
	frameWidth, frameHeight := bounds.Dx()/3, bounds.Dy()/2
	if frameWidth <= 0 || frameHeight <= 0 {
		return ""
	}
	frame := image.Rect(bounds.Min.X+(int(state)%3)*frameWidth, bounds.Min.Y+(int(state)/3)*frameHeight,
		bounds.Min.X+(int(state)%3+1)*frameWidth, bounds.Min.Y+(int(state)/3+1)*frameHeight)
	return renderHalfBlock(decodedAtlas, frame, width, height, background)
}

func renderHalfBlock(source image.Image, crop image.Rectangle, width, height int, background string) string {
	if source == nil || width <= 0 || height <= 0 || crop.Empty() {
		return ""
	}
	bg := parseHexColor(background)
	var lines []string
	for y := 0; y < height; y++ {
		var line strings.Builder
		for x := 0; x < width; x++ {
			top := samplePixel(source, crop, x, y*2, width, height*2, bg)
			bottom := samplePixel(source, crop, x, y*2+1, width, height*2, bg)
			line.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(rgbHex(top))).
				Background(lipgloss.Color(rgbHex(bottom))).Render("▀"))
		}
		lines = append(lines, line.String())
	}
	return strings.Join(lines, "\n")
}

func samplePixel(source image.Image, crop image.Rectangle, x, y, width, height int, background [3]uint8) [3]uint8 {
	sx := crop.Min.X + min(crop.Dx()-1, x*crop.Dx()/width)
	sy := crop.Min.Y + min(crop.Dy()-1, y*crop.Dy()/height)
	red, green, blue, alpha := source.At(sx, sy).RGBA()
	a := uint32(alpha >> 8)
	inv := uint32(255) - a
	r, g, b := uint32(red>>8), uint32(green>>8), uint32(blue>>8)
	return [3]uint8{
		uint8((r*255 + uint32(background[0])*inv) / 255),
		uint8((g*255 + uint32(background[1])*inv) / 255),
		uint8((b*255 + uint32(background[2])*inv) / 255),
	}
}

func parseHexColor(value string) [3]uint8 {
	var out [3]uint8
	if len(value) != 7 || value[0] != '#' {
		return out
	}
	_, _ = fmt.Sscanf(value, "#%02x%02x%02x", &out[0], &out[1], &out[2])
	return out
}

func rgbHex(value [3]uint8) string {
	return fmt.Sprintf("#%02x%02x%02x", value[0], value[1], value[2])
}
