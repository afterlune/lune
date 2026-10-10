package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

func TestTUIConversationSVGsMatchDeterministicFixtures(t *testing.T) {
	scenarios := []string{"short", "long-collapsed", "long-expanded", "narrow"}
	for _, theme := range []themeName{themeDark, themeLight} {
		for _, scenario := range scenarios {
			theme, scenario := theme, scenario
			name := fmt.Sprintf("%s-transcript-%s.svg", theme, scenario)
			t.Run(name, func(t *testing.T) {
				ui := newConversationFixture(theme, scenario)
				got := renderTUIScreenshotSVG(ui.View().Content, ui.width, ui.height, theme)
				assertTUIScreenshotSVG(t, name, got)
			})
		}
	}
}

func TestTUIConversationAlignmentStylesAndCollapse(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		t.Run(string(theme), func(t *testing.T) {
			wide := newConversationFixture(theme, "short")
			conversation, _, _ := wide.renderConversation()
			lines := strings.Split(tuiScreenshotANSI.ReplaceAllString(conversation, ""), "\n")
			if len(lines) < 4 || !strings.Contains(lines[0], "▸ YOU") {
				t.Fatalf("wide transcript = %#v, want user marker, body, spacing, and assistant", lines)
			}
			if lipgloss.Width(lines[0]) != wide.chat.Width() || lipgloss.Width(lines[1]) != wide.chat.Width() {
				t.Fatalf("wide user marker/body widths = %d/%d, want chat width %d", lipgloss.Width(lines[0]), lipgloss.Width(lines[1]), wide.chat.Width())
			}
			if !strings.HasSuffix(lines[1], "Please review the config loading.") {
				t.Fatalf("wide user body is not aligned to the right edge: %q", lines[1])
			}
			if lines[3] != "The assistant response stays on the left." {
				t.Fatalf("assistant line = %q, want left-aligned plain text", lines[3])
			}
			assertOnlyUserMarkerIsBold(t, conversation, theme)

			narrow := newConversationFixture(theme, "narrow")
			narrowText, _, _ := narrow.renderConversation()
			narrowLines := strings.Split(tuiScreenshotANSI.ReplaceAllString(narrowText, ""), "\n")
			if narrowLines[0] != "▸ YOU" || narrowLines[1] != "Please review the config loading." {
				t.Fatalf("narrow user block should be left aligned: %#v", narrowLines)
			}

			collapsed, _, _ := newConversationFixture(theme, "long-collapsed").renderConversation()
			collapsedLines := strings.Split(tuiScreenshotANSI.ReplaceAllString(collapsed, ""), "\n")
			if len(collapsedLines) < 5 || !strings.Contains(collapsedLines[4], "Show more ·") {
				t.Fatalf("collapsed transcript should show three preview lines and Show more: %#v", collapsedLines)
			}
			expanded, _, _ := newConversationFixture(theme, "long-expanded").renderConversation()
			if !strings.Contains(tuiScreenshotANSI.ReplaceAllString(expanded, ""), "Show less") {
				t.Fatal("expanded transcript is missing the Show less affordance")
			}
		})
	}
}

func TestTUIConversationFoldThresholds(t *testing.T) {
	sixLines := strings.Join([]string{"one", "two", "three", "four", "five", "six"}, "\n")
	sevenLines := sixLines + "\nseven"
	for _, test := range []struct {
		name string
		text string
		want bool
	}{
		{name: "six lines", text: sixLines},
		{name: "seven lines", text: sevenLines, want: true},
		{name: "600 runes", text: strings.Repeat("x", messageCollapseRunes)},
		{name: "601 runes", text: strings.Repeat("x", messageCollapseRunes+1), want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := conversationMessageFoldable(test.text); got != test.want {
				t.Fatalf("foldable = %v, want %v", got, test.want)
			}
		})
	}
}

func newConversationFixture(theme themeName, scenario string) *tuiModel {
	width, height := 120, 30
	if scenario == "narrow" {
		width, height = 80, 24
	}
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = theme
	ui.colorProfile = colorprofile.TrueColor
	ui.sessionID = "conversation-snapshot"
	applyTextareaTheme(&ui.input, theme)
	ui.Update(tea.WindowSizeMsg{Width: width, Height: height})

	userText := "Please review the config loading."
	assistantText := "The assistant response stays on the left."
	if scenario == "long-collapsed" || scenario == "long-expanded" {
		userText = strings.Join([]string{
			"Please review the complete configuration lifecycle and confirm the user request remains readable in the conversation transcript.",
			"Check how the output wraps across lines while the marker and message body keep their expected alignment on a wide terminal.",
			"The collapsed preview should preserve the original wording without bolding or recoloring the message body text.",
			"The Activity and Usage panels should remain steady as the long request grows and the assistant response appears below it.",
			"Expanding the request should reveal every line and retain the Show less control without shifting neighboring content.",
			"This final sentence makes the fixture long enough to exercise both the line and character collapse thresholds.",
		}, "\n")
	}
	ui.entries = []tuiEntry{{role: "you", text: userText}, {role: "assistant", text: assistantText}}
	ui.ensureMessageCollapseKeys()
	if scenario == "long-expanded" {
		ui.preferences.ExpandedMessages = map[string]bool{ui.entries[0].collapseKey: true}
	}
	ui.refreshPanels()
	return ui
}

func assertOnlyUserMarkerIsBold(t *testing.T, conversation string, theme themeName) {
	t.Helper()
	lines := parseTUIScreenshotLines(conversation, colorsFor(theme).Foreground)
	foundMarker := false
	for _, line := range lines {
		for _, run := range line {
			if !run.bold {
				continue
			}
			if run.text != "▸ YOU" {
				t.Fatalf("unexpected bold transcript text %q", run.text)
			}
			if run.foreground != colorsFor(theme).User {
				t.Fatalf("user marker color = %s, want %s", run.foreground, colorsFor(theme).User)
			}
			foundMarker = true
		}
	}
	if !foundMarker {
		t.Fatal("transcript user marker is not bold")
	}
}

func conversationMessageFoldable(text string) bool {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = themeDark
	ui.width = 300
	ui.chat.SetWidth(240)
	ui.entries = []tuiEntry{{role: "you", text: text, collapseKey: "fixture"}}
	_, _, foldable := ui.renderConversation()
	return len(foldable) > 0
}
