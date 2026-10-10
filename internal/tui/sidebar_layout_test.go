package tui

import (
	"bytes"
	"flag"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

var updateTUISidebarScreenshots = flag.Bool("update-tui-screenshots", false, "update TUI sidebar SVG screenshots")

var tuiScreenshotANSI = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestTUIWideSidebarDoesNotMoveWithConversationLength(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		t.Run(string(theme), func(t *testing.T) {
			short := newSidebarFixture(theme, "short")
			long := newSidebarFixture(theme, "long")
			if !short.wideConversation() || !long.wideConversation() {
				t.Fatal("120x30 fixture should use the wide conversation layout")
			}
			if short.chat.Height() != long.chat.Height() || short.activity.Height() != long.activity.Height() {
				t.Fatalf("viewport sizes differ: chat %dx%d vs %dx%d, activity %dx%d vs %dx%d",
					short.chat.Width(), short.chat.Height(), long.chat.Width(), long.chat.Height(),
					short.activity.Width(), short.activity.Height(), long.activity.Width(), long.activity.Height())
			}
			shortSidebar := rightSidebarLines(short)
			longSidebar := rightSidebarLines(long)
			if !reflect.DeepEqual(shortSidebar, longSidebar) {
				t.Fatalf("right sidebar changed with left conversation length:\nshort: %#v\nlong: %#v", shortSidebar, longSidebar)
			}
			assertWideSidebarSeparatorColumn(t, short)
			assertWideSidebarSeparatorColumn(t, long)
			if got, want := findSidebarRow(shortSidebar, "Usage"), findSidebarRow(longSidebar, "Usage"); got != want {
				t.Fatalf("Usage row moved: short=%d long=%d", got, want)
			}
		})
	}
}

func TestTUIActivityScrollDoesNotMoveUsage(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		t.Run(string(theme), func(t *testing.T) {
			top := newSidebarFixture(theme, "activity-top")
			bottom := newSidebarFixture(theme, "activity-bottom")
			if top.activity.TotalLineCount() <= top.activity.Height() {
				t.Fatalf("activity content lines=%d, viewport height=%d; want overflow",
					top.activity.TotalLineCount(), top.activity.Height())
			}
			if !top.activity.AtTop() || !bottom.activity.AtBottom() {
				t.Fatalf("activity scroll positions: top=%v bottom=%v", top.activity.AtTop(), bottom.activity.AtBottom())
			}
			topSidebar, bottomSidebar := rightSidebarLines(top), rightSidebarLines(bottom)
			if topRow, bottomRow := findSidebarRow(topSidebar, "Usage"), findSidebarRow(bottomSidebar, "Usage"); topRow != bottomRow {
				t.Fatalf("Usage row moved during Activity scrolling: top=%d bottom=%d", topRow, bottomRow)
			}
			if !reflect.DeepEqual(sidebarUsageRows(topSidebar), sidebarUsageRows(bottomSidebar)) {
				t.Fatal("Usage block changed while Activity scrolled")
			}

			scrolling := newSidebarFixture(theme, "activity-top")
			chatOffset, activityOffset := scrolling.chat.YOffset(), scrolling.activity.YOffset()
			scrolling.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown, Mod: tea.ModCtrl}))
			if scrolling.activity.YOffset() <= activityOffset {
				t.Fatal("Ctrl+Down did not scroll the Activity viewport")
			}
			if scrolling.chat.YOffset() != chatOffset {
				t.Fatal("scrolling Activity also moved the chat viewport")
			}
			if !reflect.DeepEqual(sidebarUsageRows(topSidebar), sidebarUsageRows(rightSidebarLines(scrolling))) {
				t.Fatal("Usage block moved after the Activity scroll key")
			}
		})
	}
}

func TestTUIUsageSidebarAlwaysHasFixedRows(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	for _, usage := range []tokenUsageTotals{
		{},
		{requests: 2},
		{requests: 2, measured: 2, input: 1200, output: 300, reasoning: 600, total: 2100, cached: 400},
	} {
		ui.sessionUsage = usage
		lines := strings.Split(tuiScreenshotANSI.ReplaceAllString(ui.usageSidebarContent(36), ""), "\n")
		if len(lines) != wideSidebarUsageRows {
			t.Fatalf("usage rows = %d, want %d: %#v", len(lines), wideSidebarUsageRows, lines)
		}
	}
}

func TestTUISidebarSVGsMatchDeterministicFixtures(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate screenshot test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	screenshotDirectory := filepath.Join(repositoryRoot, "docs", "design", "tui", "verification")
	if *updateTUISidebarScreenshots {
		if err := os.MkdirAll(screenshotDirectory, 0755); err != nil {
			t.Fatalf("create screenshot directory: %v", err)
		}
	}

	scenarios := []string{"empty", "short", "long", "activity-top", "activity-bottom", "narrow"}
	for _, theme := range []themeName{themeDark, themeLight} {
		for _, scenario := range scenarios {
			name := fmt.Sprintf("%s-%s.svg", theme, scenario)
			t.Run(name, func(t *testing.T) {
				ui := newSidebarFixture(theme, scenario)
				got := renderTUIScreenshotSVG(ui.View().Content, ui.width, ui.height, theme)
				path := filepath.Join(screenshotDirectory, name)
				if *updateTUISidebarScreenshots {
					if err := os.WriteFile(path, []byte(got), 0644); err != nil {
						t.Fatalf("write screenshot %s: %v", path, err)
					}
					return
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read screenshot %s: %v; refresh with go test ./internal/tui -run TestTUISidebarSVGsMatchDeterministicFixtures -args -update-tui-screenshots", path, err)
				}
				if !bytes.Equal([]byte(got), want) {
					t.Fatalf("screenshot %s differs; refresh with go test ./internal/tui -run TestTUISidebarSVGsMatchDeterministicFixtures -args -update-tui-screenshots", path)
				}
			})
		}
	}
}

func newSidebarFixture(theme themeName, scenario string) *tuiModel {
	width, height := 120, 30
	if scenario == "narrow" {
		width, height = 80, 24
	}
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = theme
	ui.colorProfile = colorprofile.TrueColor
	applyTextareaTheme(&ui.input, theme)
	ui.Update(tea.WindowSizeMsg{Width: width, Height: height})

	switch scenario {
	case "short":
		ui.entries = []tuiEntry{
			{role: "you", text: "Please review the config loading."},
			{role: "assistant", text: "The merge order is clear and predictable."},
		}
		ui.activities = sidebarFixtureActivities(5)
		ui.sessionUsage = measuredSidebarUsage()
	case "long":
		ui.entries = []tuiEntry{
			{role: "you", text: "Please review the config loading."},
			{role: "assistant", text: "I reviewed defaults and project settings across the config flow.\nThe environment overrides are applied after the file values.\nProvider fields and optional settings are validated before use.\nThe longer response fills the chat viewport without moving the side panel.\nActivity and Usage keep their positions while the conversation scrolls."},
		}
		ui.activities = sidebarFixtureActivities(5)
		ui.sessionUsage = measuredSidebarUsage()
	case "activity-top", "activity-bottom":
		ui.entries = []tuiEntry{{role: "you", text: "Review the recent activity."}}
		ui.activities = sidebarFixtureActivities(28)
		ui.sessionUsage = measuredSidebarUsage()
	case "narrow":
		ui.entries = []tuiEntry{
			{role: "you", text: "Review the current task."},
			{role: "assistant", text: "The activity summary stays compact on narrow terminals."},
		}
		ui.activities = sidebarFixtureActivities(8)
		ui.sessionUsage = measuredSidebarUsage()
	case "empty":
	default:
		panic("unknown sidebar fixture: " + scenario)
	}

	ui.refreshPanels()
	switch scenario {
	case "activity-top":
		ui.activity.GotoTop()
	case "activity-bottom":
		ui.activity.GotoBottom()
	}
	return ui
}

func sidebarFixtureActivities(count int) []tuiActivity {
	activities := make([]tuiActivity, count)
	for index := range activities {
		status := "done"
		if index == count-1 {
			status = "running"
		}
		activities[index] = tuiActivity{
			callID:  fmt.Sprintf("call-%02d", index+1),
			summary: fmt.Sprintf("Reading internal/config/section-%02d/provider-settings.go", index+1),
			status:  status,
		}
	}
	return activities
}

func measuredSidebarUsage() tokenUsageTotals {
	return tokenUsageTotals{requests: 4, measured: 4, input: 1200, output: 300, reasoning: 600, total: 2100, cached: 400}
}

func rightSidebarLines(ui *tuiModel) []string {
	plain := plainTUIView(ui)
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	start := ui.chat.Width() + 3
	width := ui.activity.Width() + 1
	sidebar := make([]string, len(lines))
	for index, line := range lines {
		sidebar[index] = terminalCellSlice(line, start, width)
	}
	return sidebar
}

func assertWideSidebarSeparatorColumn(t *testing.T, ui *tuiModel) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(plainTUIView(ui), "\n"), "\n")
	bodyStart := lipgloss.Height(ui.renderHeader(max(1, ui.width)))
	separatorColumn := ui.chat.Width() + 1
	for row := bodyStart; row < bodyStart+ui.chat.Height(); row++ {
		if row >= len(lines) {
			t.Fatalf("body row %d is missing from view with %d lines", row, len(lines))
		}
		if got := terminalCellSlice(lines[row], separatorColumn, 1); got != "│" {
			t.Fatalf("sidebar separator moved at row %d: got %q at column %d", row, got, separatorColumn)
		}
	}
}

func plainTUIView(ui *tuiModel) string {
	return tuiScreenshotANSI.ReplaceAllString(ui.View().Content, "")
}

func sidebarUsageRows(lines []string) []string {
	start := findSidebarRow(lines, "Usage")
	if start < 0 {
		return nil
	}
	end := min(len(lines), start+wideSidebarUsageRows)
	return lines[start:end]
}

func findSidebarRow(lines []string, marker string) int {
	for index, line := range lines {
		if strings.Contains(line, marker) {
			return index
		}
	}
	return -1
}

func terminalCellSlice(line string, start, width int) string {
	var output strings.Builder
	column := 0
	for _, character := range line {
		characterWidth := lipgloss.Width(string(character))
		if column >= start && column < start+width {
			output.WriteRune(character)
		}
		column += characterWidth
	}
	return strings.TrimRight(output.String(), " ")
}

func renderTUIScreenshotSVG(content string, width, height int, theme themeName) string {
	background, foreground := "#111417", "#e6edf3"
	if theme == themeLight {
		background, foreground = "#ffffff", "#202b39"
	}
	plain := tuiScreenshotANSI.ReplaceAllString(content, "")
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	const cellWidth, lineHeight, margin = 9, 18, 12
	pixelWidth := width*cellWidth + 2*margin
	pixelHeight := height*lineHeight + 2*margin
	var svg strings.Builder
	fmt.Fprintf(&svg, "<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"%d\" height=\"%d\" viewBox=\"0 0 %d %d\">", pixelWidth, pixelHeight, pixelWidth, pixelHeight)
	fmt.Fprintf(&svg, "<rect width=\"100%%\" height=\"100%%\" fill=\"%s\"/>", background)
	fmt.Fprintf(&svg, "<g fill=\"%s\" font-family=\"Cascadia Mono, Consolas, monospace\" font-size=\"14\" style=\"white-space:pre\">", foreground)
	for row := 0; row < height; row++ {
		line := ""
		if row < len(lines) {
			line = lines[row]
		}
		fmt.Fprintf(&svg, "<text xml:space=\"preserve\" x=\"%d\" y=\"%d\">%s</text>", margin, margin+(row+1)*lineHeight-3, html.EscapeString(line))
	}
	svg.WriteString("</g></svg>\n")
	return svg.String()
}
