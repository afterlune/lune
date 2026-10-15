package tui

import (
	"bytes"
	"flag"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

var updateTUIScreenshots = flag.Bool("update-tui-screenshots", false, "update TUI test snapshot SVGs")

var tuiScreenshotANSI = regexp.MustCompile("\x1b\\[[0-9;]*m")
var tuiScreenshotSGR = regexp.MustCompile("\x1b\\[([0-9;]*)m")

func TestTUIScreenshotSVGPreservesForegroundAndBold(t *testing.T) {
	content := "\x1b[38;2;167;177;186;1m▸ YOU\x1b[22m muted marker tail\x1b[39m <body>&\x1b[0m"
	svg := renderTUIScreenshotSVG(content, 40, 2, themeDark)
	for _, expected := range []string{
		`fill="#101010"`,
		`fill="#e7dfd3"`,
		`<tspan fill="#a7b1ba" font-weight="bold">▸ YOU</tspan>`,
		`<tspan fill="#a7b1ba"> muted marker tail</tspan>`,
		`&lt;body&gt;&amp;`,
	} {
		if !strings.Contains(svg, expected) {
			t.Errorf("SVG is missing %q: %s", expected, svg)
		}
	}
}

func assertTUIScreenshotSVG(t *testing.T, name, got string) {
	t.Helper()
	writeOrCompareTUIScreenshot(t, filepath.Join("testdata", "verification"), name, got)
}

func writeOrCompareTUIScreenshot(t *testing.T, screenshotDirectory, name, got string) {
	t.Helper()
	path := filepath.Join(screenshotDirectory, name)
	if *updateTUIScreenshots {
		if err := os.MkdirAll(screenshotDirectory, 0755); err != nil {
			t.Fatalf("create screenshot directory: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0644); err != nil {
			t.Fatalf("write screenshot %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read screenshot %s: %v; refresh with go test ./internal/tui -run 'TestTUI(SessionWorkspace|Conversation)SVGsMatchDeterministicFixtures' -args -update-tui-screenshots", path, err)
	}
	if !bytes.Equal([]byte(got), want) {
		gotBytes := []byte(got)
		index := 0
		for index < len(gotBytes) && index < len(want) && gotBytes[index] == want[index] {
			index++
		}
		start := max(index-32, 0)
		gotEnd := min(index+64, len(gotBytes))
		wantEnd := min(index+64, len(want))
		t.Fatalf("screenshot %s differs at byte %d; got %q, want %q; refresh with go test ./internal/tui -run 'TestTUI(SessionWorkspace|Conversation)SVGsMatchDeterministicFixtures' -args -update-tui-screenshots",
			path, index, gotBytes[start:gotEnd], want[start:wantEnd])
	}
}

func renderTUIScreenshotSVG(content string, width, height int, theme themeName) string {
	colors := colorsFor(theme)
	colors.Background = terminalScreenshotBackground(theme)
	content = strings.TrimRight(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	lines := parseTUIScreenshotLinesWithBackground(content, colors.Foreground, colors.Background)
	const cellWidth, lineHeight, margin = 9, 18, 12
	pixelWidth := width*cellWidth + 2*margin
	pixelHeight := height*lineHeight + 2*margin
	var svg strings.Builder
	fmt.Fprintf(&svg, "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"%d\" height=\"%d\" viewBox=\"0 0 %d %d\">", pixelWidth, pixelHeight, pixelWidth, pixelHeight)
	fmt.Fprintf(&svg, "<rect width=\"100%%\" height=\"100%%\" fill=\"%s\"/>", colors.Background)
	for _, line := range lines {
		for _, run := range line {
			if run.background != colors.Background {
				return renderTUIScreenshotSVGWithBackground(lines, width, height, colors, cellWidth, lineHeight, margin, pixelWidth, pixelHeight)
			}
		}
	}
	fmt.Fprintf(&svg, "<g fill=\"%s\" font-family=\"Cascadia Mono, Consolas, monospace\" font-size=\"14\" style=\"white-space:pre\">", colors.Foreground)
	for row := 0; row < height; row++ {
		var line []tuiScreenshotRun
		if row < len(lines) {
			line = lines[row]
		}
		fmt.Fprintf(&svg, "<text xml:space=\"preserve\" x=\"%d\" y=\"%d\">", margin, margin+(row+1)*lineHeight-3)
		for _, run := range line {
			if run.foreground == colors.Foreground && !run.bold {
				svg.WriteString(html.EscapeString(run.text))
				continue
			}
			svg.WriteString("<tspan")
			if run.foreground != colors.Foreground {
				fmt.Fprintf(&svg, " fill=\"%s\"", html.EscapeString(run.foreground))
			}
			if run.bold {
				svg.WriteString(" font-weight=\"bold\"")
			}
			svg.WriteString(">")
			svg.WriteString(html.EscapeString(run.text))
			svg.WriteString("</tspan>")
		}
		svg.WriteString("</text>")
	}
	svg.WriteString("</g></svg>\n")
	return svg.String()
}

func terminalScreenshotBackground(theme themeName) string {
	if theme == themeLight {
		return "#ffffff"
	}
	return "#101010"
}

func renderTUIScreenshotSVGWithBackground(lines [][]tuiScreenshotRun, width, height int, colors themeColors, cellWidth, lineHeight, margin, pixelWidth, pixelHeight int) string {
	var svg strings.Builder
	fmt.Fprintf(&svg, "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"%d\" height=\"%d\" viewBox=\"0 0 %d %d\">", pixelWidth, pixelHeight, pixelWidth, pixelHeight)
	fmt.Fprintf(&svg, "<rect width=\"100%%\" height=\"100%%\" fill=\"%s\"/>", colors.Background)
	for row := 0; row < height; row++ {
		var line []tuiScreenshotRun
		if row < len(lines) {
			line = lines[row]
		}
		x := margin
		baseline := margin + (row+1)*lineHeight - 3
		for _, run := range line {
			runWidth := lipgloss.Width(run.text)
			if run.background != colors.Background && runWidth > 0 {
				fmt.Fprintf(&svg, "<rect x=\"%d\" y=\"%d\" width=\"%d\" height=\"16\" rx=\"2\" fill=\"%s\"/>",
					x, baseline-14, runWidth*cellWidth, run.background)
			}
			if run.text != "" {
				fmt.Fprintf(&svg, "<text xml:space=\"preserve\" x=\"%d\" y=\"%d\" fill=\"%s\" font-family=\"Cascadia Mono, Consolas, monospace\" font-size=\"14\"",
					x, baseline, html.EscapeString(run.foreground))
				if run.bold {
					svg.WriteString(" font-weight=\"bold\"")
				}
				svg.WriteString(">")
				svg.WriteString(html.EscapeString(run.text))
				svg.WriteString("</text>")
			}
			x += runWidth * cellWidth
		}
	}
	svg.WriteString("</svg>\n")
	return svg.String()
}

type tuiScreenshotRun struct {
	text       string
	foreground string
	background string
	bold       bool
}

type tuiScreenshotStyle struct {
	foreground string
	background string
	bold       bool
}

func parseTUIScreenshotLines(content, defaultForeground string) [][]tuiScreenshotRun {
	return parseTUIScreenshotLinesWithBackground(content, defaultForeground, "")
}

func parseTUIScreenshotLinesWithBackground(content, defaultForeground, defaultBackground string) [][]tuiScreenshotRun {
	style := tuiScreenshotStyle{foreground: defaultForeground, background: defaultBackground}
	runStyle := style
	lines := [][]tuiScreenshotRun{{}}
	var run strings.Builder
	flush := func() {
		if run.Len() == 0 {
			return
		}
		lines[len(lines)-1] = append(lines[len(lines)-1], tuiScreenshotRun{
			text: run.String(), foreground: runStyle.foreground, background: runStyle.background, bold: runStyle.bold,
		})
		run.Reset()
	}
	appendText := func(value string) {
		for _, character := range value {
			if character == '\n' {
				flush()
				lines = append(lines, []tuiScreenshotRun{})
				continue
			}
			if run.Len() > 0 && runStyle != style {
				flush()
			}
			if run.Len() == 0 {
				runStyle = style
			}
			run.WriteRune(character)
		}
	}

	cursor := 0
	for _, match := range tuiScreenshotSGR.FindAllStringSubmatchIndex(content, -1) {
		appendText(content[cursor:match[0]])
		applyTUIScreenshotSGR(content[match[2]:match[3]], &style, defaultForeground, defaultBackground)
		cursor = match[1]
	}
	appendText(content[cursor:])
	flush()
	return lines
}

func applyTUIScreenshotSGR(parameters string, style *tuiScreenshotStyle, defaultForeground, defaultBackground string) {
	codes := strings.Split(parameters, ";")
	for index := 0; index < len(codes); index++ {
		code, err := strconv.Atoi(codes[index])
		if err != nil {
			code = 0
		}
		switch code {
		case 0:
			*style = tuiScreenshotStyle{foreground: defaultForeground, background: defaultBackground}
		case 1:
			style.bold = true
		case 22:
			style.bold = false
		case 39:
			style.foreground = defaultForeground
		case 49:
			style.background = defaultBackground
		case 38:
			if index+4 >= len(codes) || codes[index+1] != "2" {
				continue
			}
			red, redErr := strconv.Atoi(codes[index+2])
			green, greenErr := strconv.Atoi(codes[index+3])
			blue, blueErr := strconv.Atoi(codes[index+4])
			if redErr == nil && greenErr == nil && blueErr == nil &&
				red >= 0 && red <= 255 && green >= 0 && green <= 255 && blue >= 0 && blue <= 255 {
				style.foreground = fmt.Sprintf("#%02x%02x%02x", red, green, blue)
			}
			index += 4
		case 48:
			if index+4 >= len(codes) || codes[index+1] != "2" {
				continue
			}
			red, redErr := strconv.Atoi(codes[index+2])
			green, greenErr := strconv.Atoi(codes[index+3])
			blue, blueErr := strconv.Atoi(codes[index+4])
			if redErr == nil && greenErr == nil && blueErr == nil &&
				red >= 0 && red <= 255 && green >= 0 && green <= 255 && blue >= 0 && blue <= 255 {
				style.background = fmt.Sprintf("#%02x%02x%02x", red, green, blue)
			}
			index += 4
		}
	}
}
