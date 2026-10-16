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
		{role: "you", text: "Please inspect `responses.go` and review 9124055 on origin/main."},
		{role: "assistant", text: "I found `TUI` and will run `go test ./...` on origin/main at 9124055."},
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
	transcript, _ := ui.transcriptContent()
	if strings.Contains(transcript, "`") {
		t.Fatalf("inline code delimiters should not be visible: %q", transcript)
	}
	if !strings.Contains(plain, "Workspace · shift+tab to cycle") || strings.Contains(plain, "Plan mode (shift+tab") {
		t.Fatalf("statusline does not distinguish permission cycling from Plan mode: %q", plain)
	}
	if !strings.Contains(view.Content, "\x1b[48;2;244;244;244m") {
		t.Fatalf("light composer/user bands did not use the surface background: %q", view.Content)
	}
	if strings.Contains(view.Content, "48;2;230;244;234m") || !strings.Contains(view.Content, "38;2;87;110;78") {
		t.Fatalf("inline code did not use the green text without a chip background: %q", view.Content)
	}
	assertStyledRun := func(value string) {
		t.Helper()
		for _, line := range parseTUIScreenshotLinesWithBackground(view.Content, codexLight.Primary, codexLight.Canvas) {
			for _, run := range line {
				if strings.Contains(run.text, value) {
					if !strings.EqualFold(run.foreground, codexLight.CodeText) || !strings.EqualFold(run.background, codexLight.Canvas) {
						t.Errorf("%q style = fg %s / bg %s, want %s / %s", value, run.foreground, run.background, codexLight.CodeText, codexLight.Canvas)
					}
					return
				}
			}
		}
		t.Errorf("styled run %q was not found", value)
	}
	for _, value := range []string{"TUI", "go test ./..."} {
		assertStyledRun(value)
	}
	assertFullSurfaceLine := func(value string) {
		t.Helper()
		for _, line := range parseTUIScreenshotLinesWithBackground(view.Content, codexLight.Primary, codexLight.Canvas) {
			var text strings.Builder
			width := 0
			lastBackground := ""
			hasSurface := false
			for _, run := range line {
				text.WriteString(run.text)
				width += lipgloss.Width(run.text)
				if lipgloss.Width(run.text) > 0 {
					lastBackground = run.background
				}
				if strings.EqualFold(run.background, codexLight.Surface) {
					hasSurface = true
				}
			}
			if !strings.Contains(text.String(), value) {
				continue
			}
			if width != 98 || !hasSurface || !strings.EqualFold(lastBackground, codexLight.Surface) {
				t.Errorf("%q surface row width/background = %d / %s (surface seen: %v), want 98 / %s", value, width, lastBackground, hasSurface, codexLight.Surface)
			}
			return
		}
		t.Errorf("surface row containing %q was not found", value)
	}
	assertFullSurfaceLine("Please inspect")
	assertFullSurfaceLine("Ask Codex to do anything")
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
		wantY := ui.lightGeometry().bottomY + 1 + cursor.Y
		wantX := cursor.X + ui.lightGeometry().margin + 1
		if view.Cursor == nil || view.Cursor.X != wantX || view.Cursor.Y != wantY {
			t.Errorf("%dx%d composer cursor = %#v, want (%d,%d)", size.width, size.height, view.Cursor, wantX, wantY)
		}
		ui.input.SetValue("first line\nsecond line")
		ui.updateLayout()
		view = ui.View()
		cursor = ui.input.Cursor()
		wantY = ui.lightGeometry().bottomY + 1 + cursor.Y
		wantX = cursor.X + ui.lightGeometry().margin + 1
		if view.Cursor == nil || view.Cursor.X != wantX || view.Cursor.Y != wantY || lipgloss.Height(view.Content) > size.height {
			t.Errorf("%dx%d multiline composer cursor/layout = %#v / %d rows, want (%d,%d) within viewport", size.width, size.height, view.Cursor, lipgloss.Height(view.Content), wantX, wantY)
		}
	}
}

func TestQuestionPanelUsesFullWidthLightSurfaceAndSingleRowSelection(t *testing.T) {
	ui := newTUIModel(nil, false)
	ui.preferences.Theme = themeLight
	ui.applyResolvedTheme()
	ui.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	ui.openQuestion(core.UserQuestionRequest{Questions: []core.UserQuestionItem{{ID: "q1", Question: "下一步要落在哪条线上？", Options: []core.UserQuestionOption{
		{Label: "保留结果状态", Description: "成功显示已完成，失败显示警告"},
		{Label: "全部恢复普通标题", Description: "失败或取消后也立刻恢复普通任务标题"},
	}}}})
	ui.question.selected = 1
	ui.updateLayout()
	panel := ui.renderQuestion()
	lines := parseTUIScreenshotLinesWithBackground(panel, codexLight.Primary, codexLight.Canvas)
	lineText := func(runs []tuiScreenshotRun) string {
		var text strings.Builder
		for _, run := range runs {
			text.WriteString(run.text)
		}
		return text.String()
	}
	findLine := func(needle string) []tuiScreenshotRun {
		t.Helper()
		for _, runs := range lines {
			if strings.Contains(lineText(runs), needle) {
				return runs
			}
		}
		t.Fatalf("Question panel line %q is missing: %q", needle, tuiScreenshotANSI.ReplaceAllString(panel, ""))
		return nil
	}
	assertSurfaceRow := func(needle, surface string) []tuiScreenshotRun {
		t.Helper()
		runs := findLine(needle)
		width := 0
		foundSurface := false
		for _, run := range runs {
			width += lipgloss.Width(run.text)
			foundSurface = foundSurface || strings.EqualFold(run.background, surface)
		}
		if width != ui.lightPanelWidth() || !foundSurface {
			t.Fatalf("row %q width/background = %d / %v, want panel columns filled with %s", needle, width, foundSurface, surface)
		}
		return runs
	}
	header := assertSurfaceRow("Question 1/1 (1 unanswered)", codexLight.Surface)
	for _, run := range header {
		if strings.Contains(run.text, "Question 1/1") && !strings.EqualFold(run.foreground, codexLight.Muted) {
			t.Errorf("Question heading color = %s, want %s", run.foreground, codexLight.Muted)
		}
	}
	question := assertSurfaceRow("下一步要落在哪条线上？", codexLight.Surface)
	questionBlue := false
	for _, run := range question {
		if strings.Contains(run.text, "下一步要落在哪条线上？") && strings.EqualFold(run.foreground, "#1C64C8") {
			questionBlue = true
		}
	}
	if !questionBlue {
		t.Fatal("core question was not rendered in bright blue")
	}
	selected := assertSurfaceRow("› 2. 全部恢复普通标题", "#A4CDFB")
	selectedLabel, selectedDescription := false, false
	for _, run := range selected {
		if strings.Contains(run.text, "全部恢复普通标题") {
			selectedLabel = run.bold && strings.EqualFold(run.foreground, codexLight.Primary)
		}
		if strings.Contains(run.text, "失败或取消") {
			selectedDescription = strings.EqualFold(run.foreground, "#24292F")
		}
	}
	if !selectedLabel || !selectedDescription {
		t.Fatalf("selected row colors/boldness are wrong: %#v", selected)
	}
	optionLine := findLine("1. 保留结果状态")
	if !strings.Contains(lineText(optionLine), "成功显示已完成") {
		t.Fatal("inactive option description is not on the same row")
	}
	hint := findLine("tab to add notes | enter to submit answer | esc to interrupt")
	for _, run := range hint {
		if run.text == "tab" || run.text == "enter" || run.text == "esc" {
			if !run.bold || !strings.EqualFold(run.foreground, codexLight.Primary) {
				t.Errorf("shortcut %q style = %#v", run.text, run)
			}
		}
	}
	if ui.View().Cursor != nil {
		t.Fatal("choice state exposed a cursor")
	}
}

func TestToolActivityPreviewAndExpansion(t *testing.T) {
	if ranges := lightToolLineRanges("• Ran git status --short", "tool_done"); len(ranges) < 4 {
		t.Fatalf("tool header syntax ranges = %#v", ranges)
	}
	output := []string{"first", "second", "third", "fourth", "fifth"}
	collapsed := ansi.Strip(renderToolActivity("git status --short", output, false, 80))
	lines := strings.Split(collapsed, "\n")
	if len(lines) != 5 || !strings.HasPrefix(lines[0], "• Ran git status --short") ||
		lines[1] != "  └ first" || lines[2] != "    second" || lines[3] != "    third" ||
		lines[4] != "    + 2 lines (ctrl+t to expand)" || strings.Contains(collapsed, "fourth") {
		t.Fatalf("collapsed tool preview = %q", collapsed)
	}
	expanded := ansi.Strip(renderToolActivity("git status --short", output, true, 80))
	if !strings.Contains(expanded, "fourth") || !strings.Contains(expanded, "fifth") || strings.Contains(expanded, "+ 2 lines") {
		t.Fatalf("expanded tool output = %q", expanded)
	}
	header := renderToolActivity("git status --short", nil, false, 80)
	if !strings.Contains(header, "38;2;89;111;159") || !strings.Contains(header, "38;2;154;99;104") {
		t.Fatal("tool header is missing blue command and brick-red flag syntax colors")
	}
	quoted := renderToolActivity(`Get-Content "a b.txt" -Skip 2`, nil, false, 80)
	if !strings.Contains(quoted, "38;2;87;110;78") {
		t.Fatal("quoted shell string is missing dark-green syntax color")
	}
	path := renderLightToolActivity("Read internal/tui/view.go", nil, false, 80, "•")
	for _, color := range []string{"38;2;12;134;12", "38;2;87;110;78"} {
		if !strings.Contains(path, color) {
			t.Errorf("Read activity row is missing required color %q: %q", color, path)
		}
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
	ui.Update(tea.MouseClickMsg{X: ui.width / 2, Y: ui.lightGeometry().header + ui.lightGeometry().body, Button: tea.MouseLeft})
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
		if entry.role == "turn_footer" && strings.HasPrefix(entry.text, "Worked for 49s • ") {
			return
		}
	}
	t.Fatalf("completion footer missing: %#v", ui.entries)
}
