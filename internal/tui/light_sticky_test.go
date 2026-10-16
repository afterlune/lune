package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestLightStickyPromptFollowsTheVisibleTranscriptTurn(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.preferences.Theme = themeLight
	ui.Update(tea.WindowSizeMsg{Width: 100, Height: 14})
	ui.entries = []tuiEntry{
		{role: "you", text: "First user request"},
		{role: "assistant", text: strings.Repeat("First response line. ", 15)},
		{role: "you", text: "Second user request"},
		{role: "assistant", text: strings.Repeat("Second response line. ", 15)},
	}
	ui.refreshPanels()
	ui.chat.SetYOffset(10)
	ui.updateLayout()

	sticky := ui.lightStickyPrompt()
	if sticky != "› First user request" {
		t.Fatalf("sticky prompt=%q, offset=%d", sticky, ui.chat.YOffset())
	}
	view := ansi.Strip(ui.View().Content)
	first := strings.Index(view, "› First user request")
	second := strings.Index(view, "› Second user request")
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("sticky and visible prompts are out of order: %q", view)
	}
}
