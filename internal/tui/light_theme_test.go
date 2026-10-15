package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
	"github.com/charmbracelet/x/ansi"
)

func TestLightTranscriptUsesBandsAndIndependentPermissionStatus(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.preferences.Theme = themeLight
	ui.permissionMode = core.PermissionWorkspace
	ui.workspace = `D:\Git\luneAgent`
	ui.sessionSettings.ModelProfile = "GPT-6-Luna"
	ui.applyResolvedTheme()
	ui.entries = []tuiEntry{
		{role: "you", text: "Please inspect `responses.go`."},
		{role: "assistant", text: "I found `ResponseRequest` and will trace its defaults."},
	}
	ui.Update(tea.WindowSizeMsg{Width: 100, Height: 28})
	ui.refreshPanels()
	view := ui.View()
	plain := ansi.Strip(view.Content)
	if view.BackgroundColor == nil || colorHex(view.BackgroundColor) != "#ffffff" {
		t.Fatalf("light canvas = %s, want white", colorHex(view.BackgroundColor))
	}
	if strings.Contains(plain, "────") || !strings.Contains(plain, "› Please inspect") || !strings.Contains(plain, "• I found") {
		t.Fatalf("light transcript should be borderless and use the requested message markers: %q", plain)
	}
	if !strings.Contains(plain, "Workspace · shift+tab to cycle") || strings.Contains(plain, "Plan mode (shift+tab") {
		t.Fatalf("statusline does not distinguish permission cycling from Plan mode: %q", plain)
	}
	if !strings.Contains(view.Content, "\x1b[48;2;243;244;246m") {
		t.Fatalf("light composer/user bands did not use the surface background: %q", view.Content)
	}
	if !strings.Contains(view.Content, "48;2;236;253;245m") {
		t.Fatalf("inline code did not use the green highlight surface: %q", view.Content)
	}
	if !strings.Contains(plain, "? shortcuts") || lipgloss.Height(view.Content) != ui.height {
		t.Fatalf("light footer or fixed viewport height is missing: height=%d content=%q", lipgloss.Height(view.Content), plain)
	}

	ui.planMode = true
	footer := ansi.Strip(ui.renderFooter(100))
	if !strings.Contains(footer, "Plan mode · Read-only") || strings.Contains(footer, "shift+tab to cycle") {
		t.Fatalf("Plan mode status should be independent from Shift+Tab: %q", footer)
	}
}

func TestLightComposerCursorAndViewportFitNarrowTerminals(t *testing.T) {
	for _, size := range []struct{ width, height int }{{width: 120, height: 30}, {width: 80, height: 24}} {
		ui := newTUIModel(nil, false)
		ui.preferences.Theme = themeLight
		ui.applyResolvedTheme()
		ui.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		view := ui.View()
		if lipgloss.Width(view.Content) > size.width || lipgloss.Height(view.Content) != size.height {
			t.Errorf("%dx%d light viewport dimensions = %dx%d", size.width, size.height, lipgloss.Width(view.Content), lipgloss.Height(view.Content))
		}
		cursor := ui.input.Cursor()
		wantY := lipgloss.Height(ui.renderSessionWorkspaceBody(size.width)) + 1 + cursor.Y
		if view.Cursor == nil || view.Cursor.X != cursor.X || view.Cursor.Y != wantY {
			t.Errorf("%dx%d composer cursor = %#v, want (%d,%d)", size.width, size.height, view.Cursor, cursor.X, wantY)
		}
		ui.input.SetValue("first line\nsecond line")
		ui.updateLayout()
		view = ui.View()
		cursor = ui.input.Cursor()
		wantY = lipgloss.Height(ui.renderSessionWorkspaceBody(size.width)) + 1 + cursor.Y
		if view.Cursor == nil || view.Cursor.X != cursor.X || view.Cursor.Y != wantY || lipgloss.Height(view.Content) > size.height {
			t.Errorf("%dx%d multiline composer cursor/layout = %#v / %d rows, want (%d,%d) within viewport", size.width, size.height, view.Cursor, lipgloss.Height(view.Content), cursor.X, wantY)
		}
	}
}

func TestToolActivityPreviewAndExpansion(t *testing.T) {
	if ranges := lightToolLineRanges("• Ran git status --short", "tool_done"); len(ranges) < 4 {
		t.Fatalf("tool header syntax ranges = %#v", ranges)
	}
	output := []string{"first", "second", "third", "fourth", "fifth"}
	collapsed := ansi.Strip(renderToolActivity("git status --short", output, false, 80))
	if !strings.Contains(collapsed, "• Ran git status --short") || !strings.Contains(collapsed, "└ first") ||
		!strings.Contains(collapsed, "+ 2 lines (ctrl+t to expand)") || strings.Contains(collapsed, "fourth") {
		t.Fatalf("collapsed tool preview = %q", collapsed)
	}
	expanded := ansi.Strip(renderToolActivity("git status --short", output, true, 80))
	if !strings.Contains(expanded, "fourth") || !strings.Contains(expanded, "fifth") || strings.Contains(expanded, "+ 2 lines") {
		t.Fatalf("expanded tool output = %q", expanded)
	}
	header := renderToolActivity("git status --short", nil, false, 80)
	if !strings.Contains(header, "48;2;236;253;245") || !strings.Contains(header, "38;2;185;28;28") {
		t.Fatal("tool header is missing command and flag syntax colors")
	}

}

func TestLightScrollPillClickAndEscapeReturnToLatest(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.preferences.Theme = themeLight
	ui.Update(tea.WindowSizeMsg{Width: 80, Height: 16})
	for index := 0; index < 40; index++ {
		ui.entries = append(ui.entries, tuiEntry{role: "assistant", text: strings.Repeat("A response with enough text to wrap. ", 3)})
	}
	ui.refreshPanels()
	ui.chat.SetYOffset(0)
	ui.updateLayout()
	if !strings.Contains(ansi.Strip(ui.View().Content), "Back to bottom") {
		t.Fatal("scrolling away from the latest message should show the scroll pill")
	}
	ui.Update(tea.MouseClickMsg{X: ui.width / 2, Y: ui.chat.Height(), Button: tea.MouseLeft})
	if !ui.chat.AtBottom() || strings.Contains(ansi.Strip(ui.View().Content), "Back to bottom") {
		t.Fatal("clicking the scroll pill should return to the latest message")
	}

	ui.chat.SetYOffset(0)
	ui.running = true
	interrupted := false
	ui.cancel = func() { interrupted = true }
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if !interrupted || !ui.chat.AtBottom() {
		t.Fatalf("Esc should return to latest and interrupt while running: interrupted=%v atBottom=%v", interrupted, ui.chat.AtBottom())
	}
}

func TestShortcutsOverlayDoesNotConsumeTypedQuestionMark(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.preferences.Theme = themeLight
	ui.Update(tea.KeyPressMsg(tea.Key{Text: "?"}))
	if !ui.shortcutsOpen || !strings.Contains(ansi.Strip(ui.View().Content), "Keyboard shortcuts") {
		t.Fatal("? should open the shortcuts overlay when the composer is empty")
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if ui.shortcutsOpen {
		t.Fatal("Esc should close the shortcuts overlay")
	}
	ui.input.SetValue("ask")
	ui.Update(tea.KeyPressMsg(tea.Key{Text: "?"}))
	if ui.shortcutsOpen || ui.input.Value() != "ask?" {
		t.Fatalf("? should remain ordinary input when the composer contains text: open=%v input=%q", ui.shortcutsOpen, ui.input.Value())
	}
}

func TestCtrlTExpandsLatestToolInvocation(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.preferences.Theme = themeLight
	ui.Update(toolActivityMsg{event: core.ToolCall{ExecutionID: "e1", ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.go"}`), Started: true}})
	ui.Update(toolActivityMsg{event: core.ToolCall{ExecutionID: "e1", ID: "call-1", Name: "read_file", Arguments: []byte(`{"path":"a.go"}`), Details: &publictool.ExecutionDetails{Output: "one\ntwo\nthree\nfour"}}})
	group := ui.toolActivityGroups()[0]
	if ui.activityExpanded[group.key] {
		t.Fatal("tool output should start collapsed")
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 't', Mod: tea.ModCtrl}))
	if !ui.activityExpanded[group.key] {
		t.Fatal("Ctrl+T should expand the most recent invocation")
	}
}

func TestCompletedTaskAddsWorkedFooter(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.taskStartedAt = time.Now().Add(-49 * time.Second)
	ui.finishTask(taskResult{coreResult: core.TaskResult{Status: core.TaskCompleted}})
	for _, entry := range ui.entries {
		if entry.role == "turn_footer" && strings.HasPrefix(entry.text, "Worked for 49s · ") {
			return
		}
	}
	t.Fatalf("completion footer missing: %#v", ui.entries)
}
