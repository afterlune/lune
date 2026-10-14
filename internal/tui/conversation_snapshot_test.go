package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/luneCore"
	"github.com/charmbracelet/colorprofile"
)

func TestTUIImageAttachmentComposerSVGsMatchDeterministicFixtures(t *testing.T) {
	scenarios := []string{"empty", "single", "multiple", "removed", "narrow", "error", "limit"}
	for _, theme := range []themeName{themeDark, themeLight} {
		for _, scenario := range scenarios {
			theme, scenario := theme, scenario
			name := fmt.Sprintf("%s-image-attachments-%s.svg", theme, scenario)
			t.Run(name, func(t *testing.T) {
				ui := newConversationFixture(theme, "short")
				first := screenshotImageAttachment(t, 1, 16, 9)
				second := screenshotImageAttachment(t, 2, 11, 7)
				switch scenario {
				case "single":
					ui.attachments = []imageAttachment{first}
					ui.input.SetValue("Compare this view: " + first.Token)
				case "multiple":
					ui.attachments = []imageAttachment{first, second}
					ui.input.SetValue("Compare these states: " + first.Token + " " + second.Token + " and fix the shifting column.")
				case "removed":
					ui.input.SetValue("Compare the final screenshot.")
					ui.status = "Image removed · Ctrl+Z restores the draft."
				case "narrow":
					ui.attachments = []imageAttachment{first, second}
					ui.input.SetValue("Compare these states: " + first.Token + " " + second.Token + " and fix the shifting column.")
					ui.Update(tea.WindowSizeMsg{Width: 72, Height: 24})
				case "error":
					ui.input.SetValue(`D:\screenshots\missing.png`)
					ui.status = "Could not open image; the pasted path remains in the draft."
				case "limit":
					ui.attachments = []imageAttachment{first}
					ui.input.SetValue("Compare this view: " + first.Token)
					ui.status = "Image limit reached · attachments.maxFiles allows 1."
				}
				if scenario != "narrow" {
					ui.nextAttachmentID = 3
				}
				ui.updateLayout()
				got := renderTUIScreenshotSVG(ui.View().Content, ui.width, ui.height, theme)
				assertTUIScreenshotSVG(t, name, got)
			})
		}
	}
}

func screenshotImageAttachment(t *testing.T, index, width, height int) imageAttachment {
	t.Helper()
	attachment, err := imageAttachmentFromBytes(testPNG(t, width, height), "screen.png", core.AttachmentLimits{})
	if err != nil {
		t.Fatalf("build screenshot image attachment: %v", err)
	}
	attachment.Token = imageAttachmentToken(index, attachment)
	return attachment
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

func TestScrollbackEntriesShowPartialResponsesWithoutFolding(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		t.Run(string(theme), func(t *testing.T) {
			entry := tuiEntry{role: "assistant", text: "The answer stopped here.", incomplete: true}
			got := tuiScreenshotANSI.ReplaceAllString(formatScrollbackEntry(entry, 80, theme), "")
			if !strings.Contains(got, "Partial · The answer stopped here.") {
				t.Fatalf("partial scrollback entry = %q", got)
			}
			if strings.Contains(got, "Show more") || strings.Contains(got, "Show less") {
				t.Fatalf("scrollback entry contains an obsolete fold control: %q", got)
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
	ui.entries = []tuiEntry{{role: "you", text: userText}, {role: "assistant", text: assistantText}}
	ui.refreshPanels()
	return ui
}
