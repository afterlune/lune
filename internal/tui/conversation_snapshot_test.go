package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/afterlune/lune/core"
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

func TestTUIFoldCommandSVGsMatchDeterministicFixtures(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		theme := theme
		t.Run(string(theme), func(t *testing.T) {
			ui := newConversationFixture(theme, "short")
			ui.entries = []tuiEntry{
				{role: "you", text: strings.Repeat("First long user request. ", 32)},
				{role: "assistant", text: "I will preserve the separate long requests."},
				{role: "you", text: strings.Repeat("Second long user request. ", 32)},
				{role: "you", text: strings.Repeat("Third long user request. ", 32)},
			}
			ui.refreshPanels()
			ui.input.SetValue("/fold")
			ui.submit()
			name := fmt.Sprintf("%s-fold-list.svg", theme)
			got := renderTUIScreenshotSVG(ui.View().Content, ui.width, ui.height, theme)
			assertTUIScreenshotSVG(t, name, got)
		})
	}
}

func TestTUIContextCommandSVGsMatchDeterministicFixtures(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		theme := theme
		t.Run(string(theme), func(t *testing.T) {
			client := &fakeResponder{contextDefaults: core.ContextDefaults{ContextWindow: 64000, ReserveOutputTokens: 4096}}
			ui := newTUIModel(client, false)
			ui.preferences.Theme = theme
			ui.colorProfile = colorprofile.TrueColor
			ui.sessionID = "session-current"
			ui.workspace = "D:/work/project"
			ui.sessionSettings.ModelProfile = "lead"
			applyTextareaTheme(&ui.input, theme)
			ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
			ui.input.SetValue("/context")
			ui.submit()
			name := fmt.Sprintf("%s-context-settings.svg", theme)
			got := renderTUIScreenshotSVG(ui.View().Content, ui.width, ui.height, theme)
			assertTUIScreenshotSVG(t, name, got)
		})
	}
}

func TestTUICompactionStatusSVGsMatchDeterministicFixtures(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		for _, stage := range []string{"started", "completed"} {
			theme, stage := theme, stage
			t.Run(string(theme)+"-"+stage, func(t *testing.T) {
				ui := newConversationFixture(theme, "short")
				ui.sessionID = "session-current"
				details := &core.ContextCompactionDetails{Stage: stage, Mode: "local", BeforeTokens: 48000, AfterTokens: 22000}
				ui.Update(coreEventMsg{event: core.Event{
					Kind: core.EventContextCompaction, SessionID: ui.sessionID, Compaction: details,
				}})
				name := fmt.Sprintf("%s-compaction-%s.svg", theme, stage)
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
			if len(lines) < 3 || !strings.HasPrefix(lines[0], "User") || !strings.HasPrefix(lines[2], "Lune") {
				t.Fatalf("wide timeline = %#v, want left-aligned User and Lune events", lines)
			}
			if lipgloss.Width(lines[0]) > wide.chat.Width() || lipgloss.Width(lines[2]) > wide.chat.Width() {
				t.Fatalf("wide event widths = %d/%d, exceed chat width %d", lipgloss.Width(lines[0]), lipgloss.Width(lines[2]), wide.chat.Width())
			}
			if strings.Index(lines[0], "Please review") != conversationRoleWidth+1 {
				t.Fatalf("user body does not begin after the role gutter: %q", lines[0])
			}
			if !strings.HasSuffix(lines[2], "The assistant response stays on the left.") {
				t.Fatalf("assistant event = %q, want left-aligned text", lines[2])
			}
			assertModernConversationRoleStyles(t, conversation, theme)

			narrow := newConversationFixture(theme, "narrow")
			narrowText, _, _ := narrow.renderConversation()
			narrowLines := strings.Split(tuiScreenshotANSI.ReplaceAllString(narrowText, ""), "\n")
			if !strings.HasPrefix(narrowLines[0], "User") || !strings.HasSuffix(narrowLines[0], "Please review the config loading.") {
				t.Fatalf("narrow event should keep its left role gutter: %#v", narrowLines)
			}

			collapsed, _, _ := newConversationFixture(theme, "long-collapsed").renderConversation()
			collapsedLines := strings.Split(tuiScreenshotANSI.ReplaceAllString(collapsed, ""), "\n")
			if len(collapsedLines) < 4 || !strings.Contains(collapsedLines[3], "Show more ·") {
				t.Fatalf("collapsed transcript should show three preview lines and Show more: %#v", collapsedLines)
			}
			expanded, _, _ := newConversationFixture(theme, "long-expanded").renderConversation()
			if !strings.Contains(tuiScreenshotANSI.ReplaceAllString(expanded, ""), "Show less") {
				t.Fatal("expanded transcript is missing the Show less affordance")
			}
		})
	}
}

func TestTUIConversationColorsStayOnRoleLabels(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		t.Run(string(theme), func(t *testing.T) {
			ui := newConversationFixture(theme, "short")
			ui.entries = []tuiEntry{
				{role: "you", text: "USER_BODY"},
				{role: "assistant", text: "LUNE_BODY"},
				{role: "assistant", text: "PARTIAL_BODY", incomplete: true},
				{role: "tool", text: "TOOL_BODY"},
				{role: "notice", text: "NOTE_BODY"},
				{role: "permission", text: "APPROVAL_BODY"},
				{role: "error", text: "ERROR_BODY", err: true},
				{role: "queued", text: "QUEUED_BODY"},
			}
			conversation, _, _ := ui.renderConversation()
			lines := parseTUIScreenshotLines(conversation, colorsFor(theme).Foreground)
			colors := colorsFor(theme)
			roleColors := map[string]string{
				"User": colors.User, "Lune": colors.Accent, "Partial": colors.Muted,
				"Tool": colors.Tool, "Note": colors.Muted, "Approval": colors.Warning,
				"Error": colors.Error, "Queued": colors.Muted,
			}
			bodyLabels := []string{"USER_BODY", "LUNE_BODY", "PARTIAL_BODY", "TOOL_BODY", "NOTE_BODY", "APPROVAL_BODY", "ERROR_BODY", "QUEUED_BODY"}
			foundRoles := make(map[string]bool, len(roleColors))
			foundBodies := make(map[string]bool, len(bodyLabels))
			for _, line := range lines {
				for _, run := range line {
					if run.bold {
						t.Fatalf("modern timeline contains bold text %q", run.text)
					}
					if want, ok := roleColors[run.text]; ok {
						foundRoles[run.text] = true
						if run.foreground != want {
							t.Errorf("role %q color = %s, want %s", run.text, run.foreground, want)
						}
					}
					for _, body := range bodyLabels {
						if strings.Contains(run.text, body) {
							foundBodies[body] = true
							if run.foreground != colors.Foreground {
								t.Errorf("body %q color = %s, want default %s", body, run.foreground, colors.Foreground)
							}
						}
					}
				}
			}
			for role := range roleColors {
				if !foundRoles[role] {
					t.Errorf("role label %q not found", role)
				}
			}
			for _, body := range bodyLabels {
				if !foundBodies[body] {
					t.Errorf("message body %q not found", body)
				}
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
	ui.sessionID = "session-current"
	ui.sessionList = workspaceFixtureSessions()
	ui.sessionIndex = 0
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

func assertModernConversationRoleStyles(t *testing.T, conversation string, theme themeName) {
	t.Helper()
	lines := parseTUIScreenshotLines(conversation, colorsFor(theme).Foreground)
	colors := colorsFor(theme)
	roleColors := map[string]string{
		"User": colors.User, "Lune": colors.Accent, "Tool": colors.Tool,
		"Partial": colors.Muted, "Note": colors.Muted, "Approval": colors.Warning,
		"Error": colors.Error, "Queued": colors.Muted,
	}
	foundUser := false
	for _, line := range lines {
		for _, run := range line {
			if run.bold {
				t.Fatalf("modern timeline should keep role labels and bodies regular weight: %q", run.text)
			}
			if wantColor, ok := roleColors[run.text]; ok {
				if run.foreground != wantColor {
					t.Fatalf("role label %q color = %s, want %s", run.text, run.foreground, wantColor)
				}
				if run.text == "User" {
					foundUser = true
				}
			}
		}
	}
	if !foundUser {
		t.Fatal("timeline User role label is missing its theme color")
	}
	for _, line := range lines {
		for _, run := range line {
			if strings.Contains(run.text, "Please review") && run.bold {
				t.Fatal("user message body should remain regular weight")
			}
		}
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
