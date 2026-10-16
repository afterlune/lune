package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
)

func TestTUIViewsUseColorAndMarkersWithoutBold(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		t.Run(string(theme), func(t *testing.T) {
			ui := newTUIModel(&fakeResponder{}, false)
			ui.preferences.Theme = theme
			ui.sessionID = "session-current"
			ui.sessionList = workspaceFixtureSessions()
			ui.sessionIndex = 1
			ui.queued = []core.QueuedTurnState{{ExecutionID: "queued-1", Position: 1, Status: "queued"}}
			ui.queueIndex = 0
			ui.commandSuggestions = []string{"/help", "/model"}
			ui.suggestionIndex = 0
			ui.modelOptions = []core.ModelOption{{Profile: "lead", Provider: "deepseek", Model: "deepseek-main"}}
			ui.modelIndex = 0
			ui.effortIndex = 1
			ui.permissionIndex = 1
			ui.entries = []tuiEntry{{role: "you", text: "Review the config."}, {role: "assistant", text: "The selected option is marked by its arrow and color."}}
			views := []string{
				ui.renderQueuePicker(), ui.renderPlanReview(), ui.renderCommandSuggestions(),
				ui.renderKeymapEditor(), ui.renderSessionPicker(), ui.renderModelPicker(), ui.renderEffortPicker(),
				ui.renderPermissionMenu(), ui.View().Content,
			}
			combined := strings.Join(views, "\n")
			if !strings.Contains(tuiScreenshotANSI.ReplaceAllString(combined, ""), "›") {
				t.Fatal("selected rows lost their structural arrow marker")
			}
			for _, line := range parseTUIScreenshotLines(combined, colorsFor(theme).Foreground) {
				for _, run := range line {
					if run.bold {
						t.Fatalf("TUI %s view contains bold text %q", theme, run.text)
					}
				}
			}
		})
	}
}

func TestTUILayoutFitsWideAndNarrowTerminals(t *testing.T) {
	for _, size := range []struct{ width, height int }{{width: 120, height: 30}, {width: 100, height: 30}, {width: 80, height: 24}} {
		ui := newTUIModel(&fakeResponder{}, false)
		ui.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		initialView := ui.View()
		if !initialView.AltScreen {
			t.Errorf("%dx%d view did not request the alternate screen", size.width, size.height)
		}
		if ui.input.Height() != 1 {
			t.Errorf("%dx%d empty composer height = %d, want 1", size.width, size.height, ui.input.Height())
		}
		view := initialView.Content
		if got := lipgloss.Width(view); got > size.width {
			t.Errorf("layout width = %d, terminal width = %d", got, size.width)
		}
		if got := lipgloss.Height(view); got > size.height {
			t.Errorf("layout height = %d, terminal height = %d", got, size.height)
		}
		ui.input.SetValue("cursor alignment")
		ui.updateLayout()
		cursorView := ui.View()
		inputCursor := ui.input.Cursor()
		if cursorView.Cursor == nil || inputCursor == nil {
			t.Fatal("focused composer did not expose a terminal cursor")
		}
		expectedX := inputCursor.X
		if ui.lightLayout() {
			expectedX++
		}
		expectedY := lipgloss.Height(ui.renderSessionWorkspaceBody(size.width)) + 1 + inputCursor.Y
		if cursorView.Cursor.X != expectedX || cursorView.Cursor.Y != expectedY {
			t.Errorf("cursor = (%d,%d), composer expects (%d,%d)", cursorView.Cursor.X, cursorView.Cursor.Y,
				expectedX, expectedY)
		}
		ui.input.SetValue("/")
		ui.updateCommandSuggestions()
		ui.updateLayout()
		view = ui.View().Content
		if got := lipgloss.Height(view); got > size.height {
			t.Errorf("layout with command suggestions height = %d, terminal height = %d", got, size.height)
		}
		if got := lipgloss.Height(view); got != size.height {
			t.Errorf("layout height with suggestions = %d, want fixed terminal height %d", got, size.height)
		}
		if ui.input.Height() != 1 {
			t.Errorf("single-line draft height = %d, want 1", ui.input.Height())
		}
	}
}

func TestTUICommandSuggestionsScrollWithinBoundedHeight(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	ui.suggestionIndex = len(ui.commandSuggestions) - 1
	start, end := commandSuggestionRange(ui.suggestionIndex, len(ui.commandSuggestions))
	if end-start > maxCommandSuggestions || start == 0 {
		t.Fatalf("suggestion range = %d:%d for selected index %d", start, end, ui.suggestionIndex)
	}
	view := ui.renderCommandSuggestions()
	if !strings.Contains(view, ui.commandSuggestions[ui.suggestionIndex]) {
		t.Fatalf("suggestions do not show selected command %q: %q", ui.commandSuggestions[ui.suggestionIndex], view)
	}
	if got := lipgloss.Height(view); got > maxCommandSuggestions+1 {
		t.Fatalf("suggestion height = %d, want at most %d", got, maxCommandSuggestions+1)
	}
}

func TestCommandSuggestionsOverlayKeepsTranscriptAndComposerGeometry(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	ui.entries = []tuiEntry{{role: "assistant", text: "Latest transcript answer."}}
	ui.refreshPanels()
	before := ui.View()
	bodyHeight := ui.chat.Height()
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	ui.updateLayout()
	after := ui.View()
	if !after.AltScreen || lipgloss.Height(after.Content) != lipgloss.Height(before.Content) || ui.chat.Height() != bodyHeight {
		t.Fatalf("command popup changed geometry: screen %d→%d, body %d→%d", lipgloss.Height(before.Content), lipgloss.Height(after.Content), bodyHeight, ui.chat.Height())
	}
	plain := tuiScreenshotANSI.ReplaceAllString(after.Content, "")
	if !strings.Contains(plain, "Latest transcript answer.") {
		t.Fatalf("transcript disappeared while command popup was open: %q", plain)
	}
	if selected := ui.commandSuggestions[ui.suggestionIndex]; !strings.Contains(plain, selected) || strings.Count(plain, selected) != 1 {
		t.Fatalf("selected command should appear exactly once in the overlay: %q / %q", selected, plain)
	}
	if got := ui.commandSuggestionHeight(); got != 0 {
		t.Fatalf("command popup reserved %d rows instead of overlaying transcript", got)
	}
	ui.taskUsage = tokenUsageTotals{requests: 1, measured: 1, input: 1200, output: 300, total: 1500, cached: 240}
	ui.contextWindow, ui.contextInput = 64000, 1200
	ui.refreshPanels()
	ui.updateLayout()
	withUsage := ui.View()
	if lipgloss.Height(withUsage.Content) != lipgloss.Height(before.Content) || ui.chat.Height() != bodyHeight {
		t.Fatal("updating usage changed the fixed composer or transcript geometry")
	}
	ui.resetInput()
	ui.updateLayout()
	closed := tuiScreenshotANSI.ReplaceAllString(ui.View().Content, "")
	if strings.Contains(closed, "/autocompact") || !strings.Contains(closed, "Latest transcript answer.") {
		t.Fatalf("closing command popup left rows behind or hid transcript: %q", closed)
	}
}
