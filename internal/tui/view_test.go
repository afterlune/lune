package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
	"github.com/charmbracelet/x/ansi"
)

func TestEmptyStateClean(t *testing.T) {
	m := &tuiModel{}
	m.viewport = viewport.New(viewport.WithWidth(80), viewport.WithHeight(24))
	m.refreshViewportContent()
	view := m.viewport.View()
	if strings.Contains(view, "⣿") || strings.Contains(view, "⣠") {
		t.Errorf("expected empty state to not contain dot-matrix/Braille characters, got: %s", view)
	}
	if strings.TrimSpace(view) != "" {
		t.Errorf("expected empty state to be completely clean blank, got: %q", view)
	}
}

func TestHeaderRendering(t *testing.T) {
	header := renderHeader("D:\\Git\\test-repo")
	stripped := ansi.Strip(header)
	if !strings.Contains(stripped, ">_ lune") {
		t.Errorf("expected '>_ lune' in header, got: %s", stripped)
	}
	if strings.Contains(stripped, "v0.1.0") || strings.Contains(stripped, "LuneAgent") {
		t.Errorf("expected no version or old LuneAgent brand in header, got: %s", stripped)
	}
	if !strings.Contains(stripped, "D:\\Git\\test-repo") {
		t.Errorf("expected workspace in header, got: %s", stripped)
	}
}

func TestRenderingRemovesUntrustedTerminalControls(t *testing.T) {
	unsafe := "before\x1b]52;c;clipboard\a\u009b31m after"
	rendered := renderEntry(TranscriptEntry{Kind: KindAssistant, Text: unsafe}, 80)
	if strings.Contains(rendered, "\x1b]52") || strings.ContainsRune(rendered, '\a') || strings.ContainsRune(rendered, '\u009b') {
		t.Fatalf("assistant rendering retained terminal control sequence: %q", rendered)
	}
	if plain := ansi.Strip(rendered); !strings.Contains(plain, "before") || !strings.Contains(plain, "after") {
		t.Fatalf("assistant rendering lost printable text: %q", plain)
	}
	toolRendered := renderEntry(TranscriptEntry{Kind: KindTool, ToolName: "run_command", ToolOutput: unsafe}, 80)
	if strings.Contains(toolRendered, "\x1b]52") || strings.ContainsRune(toolRendered, '\a') || strings.ContainsRune(toolRendered, '\u009b') {
		t.Fatalf("tool output rendering retained terminal control sequence: %q", toolRendered)
	}

	question := &ActiveQuestion{Request: core.UserQuestionRequest{Questions: []core.UserQuestionItem{{
		Question: unsafe,
		Options:  []core.UserQuestionOption{{Label: unsafe, Description: unsafe}},
	}}}}
	panel := renderQuestionPanel(question, 80)
	if strings.Contains(panel, "\x1b]52") || strings.ContainsRune(panel, '\a') || strings.ContainsRune(panel, '\u009b') {
		t.Fatalf("question panel retained terminal control sequence: %q", panel)
	}
}

func TestEntryRendering(t *testing.T) {
	// User entry
	userEntry := TranscriptEntry{
		Kind: KindUser,
		Text: "Hello Lune",
	}
	uRendered := renderEntry(userEntry, 80)
	if !strings.Contains(uRendered, "›") || !strings.Contains(uRendered, "Hello Lune") {
		t.Errorf("expected user entry with prompt prefix, got: %s", uRendered)
	}

	// Assistant entry
	asstEntry := TranscriptEntry{
		Kind: KindAssistant,
		Text: "Here is your plan:\n- Step 1\n- Step 2",
	}
	aRendered := renderEntry(asstEntry, 80)
	if !strings.Contains(aRendered, "•") || !strings.Contains(aRendered, "Here is your plan") {
		t.Errorf("expected assistant bullet and text, got: %s", aRendered)
	}

	// Tool execution Ran
	zero := 0
	toolRan := TranscriptEntry{
		Kind:         KindTool,
		ToolName:     "run_command",
		ToolCommand:  "go test ./...",
		ToolOutput:   "PASS\nok  test\n",
		ToolExitCode: &zero,
	}
	tRanRendered := renderEntry(toolRan, 80)
	if !strings.Contains(tRanRendered, "Ran") || !strings.Contains(tRanRendered, "go test ./...") || !strings.Contains(tRanRendered, "└") {
		t.Errorf("expected Ran tool entry with branch indicator, got: %s", tRanRendered)
	}

	// Tool execution Failed
	exitOne := 1
	toolFailed := TranscriptEntry{
		Kind:         KindTool,
		ToolName:     "run_command",
		ToolCommand:  "make build",
		ToolOutput:   "error: file not found",
		ToolExitCode: &exitOne,
	}
	tFailRendered := renderEntry(toolFailed, 80)
	if !strings.Contains(tFailRendered, "Failed (exit 1)") || !strings.Contains(tFailRendered, "make build") {
		t.Errorf("expected Failed tool entry, got: %s", tFailRendered)
	}
}

func TestQuestionPanelRendering(t *testing.T) {
	q := &ActiveQuestion{
		Request: core.UserQuestionRequest{
			Questions: []core.UserQuestionItem{
				{
					ID:       "q1",
					Question: "这次分析希望回答什么问题？",
					Options: []core.UserQuestionOption{
						{Label: "整体代码审查", Description: "梳理架构并找高优先级问题", Recommended: true},
						{Label: "定位运行故障", Description: "追查具体报错"},
					},
				},
			},
		},
	}

	rendered := renderQuestionPanel(q, 80)
	if !strings.Contains(rendered, "Question 1/1") {
		t.Errorf("expected question counter meta, got: %s", rendered)
	}
	if !strings.Contains(rendered, "这次分析希望回答什么问题？") {
		t.Errorf("expected question title, got: %s", rendered)
	}
	if !strings.Contains(rendered, "整体代码审查  [Recommended]") {
		t.Errorf("expected options list, got: %s", rendered)
	}
	if !strings.Contains(rendered, "tab to add notes | enter to confirm answer | esc to stop task") {
		t.Errorf("expected question footer controls, got: %s", rendered)
	}
}

func TestQuestionPanelUsesSoftAccentAndPrimaryInputs(t *testing.T) {
	previous := CurrentThemeMode()
	SetThemeMode(ThemeModeDark)
	defer SetThemeMode(previous)

	q := &ActiveQuestion{
		Drafts: []questionDraft{{
			Selected: 1,
			Custom:   "custom answer",
			NoteText: "a note",
		}},
		Request: core.UserQuestionRequest{Questions: []core.UserQuestionItem{{
			Question: "Which scope?",
			Options:  []core.UserQuestionOption{{Label: "One"}},
		}}},
	}
	rendered := renderQuestionPanel(q, 100)
	r, g, b, ok := parseHexRGB(DarkTheme.AccentSoft)
	if !ok || !strings.Contains(rendered, fmt.Sprintf("38;2;%d;%d;%dm", r, g, b)) {
		t.Fatalf("question title did not use the soft accent: %q", rendered)
	}
	pr, pg, pb, ok := parseHexRGB(DarkTheme.TextPrimary)
	if !ok || !strings.Contains(rendered, fmt.Sprintf("38;2;%d;%d;%dm", pr, pg, pb)) || !strings.Contains(ansi.Strip(rendered), "Custom answer: custom answer█") {
		t.Fatalf("custom answer did not use primary text: %q", rendered)
	}
	if !strings.Contains(ansi.Strip(rendered), "Notes: a note") {
		t.Fatalf("notes did not use primary text: %q", rendered)
	}
}

func TestPlanMarkdownUsesSoftAccent(t *testing.T) {
	previous := CurrentThemeMode()
	SetThemeMode(ThemeModeDark)
	defer SetThemeMode(previous)

	rendered := renderPlanMarkdown("See internal/tui/view.go:100", 100)
	r, g, b, ok := parseHexRGB(DarkTheme.AccentSoft)
	if !ok || !strings.Contains(rendered, fmt.Sprintf("38;2;%d;%d;%dm", r, g, b)) {
		t.Fatalf("plan markdown did not use the soft accent: %q", rendered)
	}
}

func TestQuestionPanelAlignsDescriptionsByTerminalCellWidth(t *testing.T) {
	q := &ActiveQuestion{
		Request: core.UserQuestionRequest{Questions: []core.UserQuestionItem{{
			ID: "alignment",
			Options: []core.UserQuestionOption{
				{Label: "短", Description: "ALIGN_DESC_SHORT"},
				{Label: "较长 mixed 标签 123", Description: "ALIGN_DESC_LONG"},
				{Label: "无说明"},
			},
		}}},
	}

	var expectedShort, expectedLong int
	for selected := range q.Request.Questions[0].Options {
		q.currentDraft().Selected = selected
		view := ansi.Strip(renderQuestionPanel(q, 100))
		columns := map[string]int{}
		for _, line := range strings.Split(view, "\n") {
			for _, description := range []string{"ALIGN_DESC_SHORT", "ALIGN_DESC_LONG"} {
				if index := strings.Index(line, description); index >= 0 {
					columns[description] = lipgloss.Width(line[:index])
				}
			}
		}
		shortColumn, shortOK := columns["ALIGN_DESC_SHORT"]
		longColumn, longOK := columns["ALIGN_DESC_LONG"]
		if !shortOK || !longOK {
			t.Fatalf("question panel lost an option description:\n%s", view)
		}
		if shortColumn != longColumn {
			t.Fatalf("option descriptions do not share a display column: short=%d long=%d\n%s", shortColumn, longColumn, view)
		}
		if selected == 0 {
			expectedShort, expectedLong = shortColumn, longColumn
		} else if shortColumn != expectedShort || longColumn != expectedLong {
			t.Fatalf("description columns changed with selected option: got %d/%d, want %d/%d", shortColumn, longColumn, expectedShort, expectedLong)
		}
	}

	if !questionDescriptionStyle(false).GetFaint() || !questionDescriptionStyle(true).GetFaint() {
		t.Fatal("option descriptions should use faint styling for selected and unselected rows")
	}
}

func TestQuestionPanelWrapsDescriptionsBelowLabelsWhenNarrow(t *testing.T) {
	const width = 48
	q := &ActiveQuestion{
		Request: core.UserQuestionRequest{Questions: []core.UserQuestionItem{{
			ID: "narrow",
			Options: []core.UserQuestionOption{{
				Label:       "较长的选项标签 mixed 123456789",
				Description: "NARROW_DESCRIPTION wraps below the label and remains visible on the narrow panel",
			}},
		}}},
	}

	view := ansi.Strip(renderQuestionPanel(q, width))
	lines := strings.Split(view, "\n")
	labelLine, descriptionLine := -1, -1
	for i, line := range lines {
		if strings.Contains(line, q.Request.Questions[0].Options[0].Label) {
			labelLine = i
		}
		if strings.Contains(line, "NARROW_DESCRIPTION") {
			descriptionLine = i
		}
		if lineWidth := lipgloss.Width(line); lineWidth > width {
			t.Fatalf("question panel line exceeds its width (%d > %d): %q", lineWidth, width, line)
		}
	}
	if labelLine < 0 || descriptionLine <= labelLine {
		t.Fatalf("narrow panel should put the description below its label:\n%s", view)
	}
	if !strings.Contains(view, "remains visible") || !strings.Contains(view, "panel") {
		t.Fatalf("narrow panel truncated the option description:\n%s", view)
	}
}

func TestPlanReviewPanelRendering(t *testing.T) {
	review := &ActivePlanReview{
		ExecutionID: "exec-1",
		Text:        "Plan body",
		Selected:    0,
	}
	rendered := renderPlanReviewPanel(review, 80)
	if !strings.Contains(rendered, "Implement this plan?") {
		t.Errorf("expected implement plan title, got: %s", rendered)
	}
	if !strings.Contains(rendered, "1. Yes, implement this plan") {
		t.Errorf("expected option 1, got: %s", rendered)
	}
}

func TestScrollPillAppearsBetweenPlanAndReviewOptions(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.width = 120
	m.height = 32
	m.entries = []TranscriptEntry{{Kind: KindAssistant, Text: "plan-body-marker"}}
	m.activePlanReview = &ActivePlanReview{Text: "plan", Selected: 0}
	m.scrolledUp = true
	m.view.invalidateContent()
	m.updateLayout()

	view := ansi.Strip(m.View().Content)
	planIndex := strings.Index(view, "plan-body-marker")
	pillIndex := strings.Index(view, "Back to bottom")
	optionsIndex := strings.Index(view, "Implement this plan?")
	if planIndex < 0 || pillIndex < 0 || optionsIndex < 0 {
		t.Fatalf("expected plan, scroll pill, and review options in view:\n%s", view)
	}
	if !(planIndex < pillIndex && pillIndex < optionsIndex) {
		t.Fatalf("scroll pill should separate plan content from review options (indexes %d, %d, %d):\n%s", planIndex, pillIndex, optionsIndex, view)
	}

	expectedHeight := lipgloss.Height(renderHeader(m.workspace)) + m.viewport.Height() +
		lipgloss.Height(renderPlanReviewPanel(m.activePlanReview, max(20, m.width-4))) + 2 +
		lipgloss.Height(renderComposerBand(m.input.composer.View(), max(20, m.width-4))) +
		lipgloss.Height(renderFooter(m, max(20, m.width-4)))
	if got := lipgloss.Height(view); got != expectedHeight {
		t.Fatalf("moving the scroll pill changed the layout height: got %d, want %d", got, expectedHeight)
	}
}

func TestAutocompleteFiltering(t *testing.T) {
	items := filterCommands("/p")
	if len(items) == 0 {
		t.Fatalf("expected matches for /p, got none")
	}
	foundPlan := false
	foundPermissions := false
	for _, it := range items {
		if it.Command == "/plan" {
			foundPlan = true
		}
		if it.Command == "/permissions" {
			foundPermissions = true
		}
	}
	if !foundPlan || !foundPermissions {
		t.Errorf("expected /plan and /permissions, got %+v", items)
	}
}

func TestModelUpdateKeys(t *testing.T) {
	workspace := t.TempDir()
	projectConfig := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{}}`
	if err := os.WriteFile(filepath.Join(workspace, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: workspace})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	m := newTUIModel(client, nil, "test-sess")
	m.workspace = client.Workspace()
	m.width = 100
	m.height = 30
	m.updateLayout()

	// Shift+Tab toggles Plan mode while preserving permissions.
	normalMode := m.permissionMode
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	m = mod.(*tuiModel)
	if !m.planMode || m.permissionMode != normalMode {
		t.Fatalf("expected Shift+Tab to preserve permissions in Plan mode, plan=%v permission=%s", m.planMode, m.permissionMode)
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	m = mod.(*tuiModel)
	if m.planMode || m.permissionMode != normalMode {
		t.Fatalf("expected Shift+Tab to exit Plan mode and keep %s, plan=%v permission=%s", normalMode, m.planMode, m.permissionMode)
	}

	// Autocomplete trigger
	m.input.composer.SetValue("/pl")
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"}))
	m = mod.(*tuiModel)
	if !m.input.autocompleteOpen {
		t.Errorf("expected autocomplete to open for /plan")
	}

	// Shift+Enter inserts newline
	m.input.composer.SetValue("line1")
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModShift}))
	m = mod.(*tuiModel)
	if m.input.composer.Value() != "line1\n" {
		t.Errorf("expected composer to have newline after Shift+Enter, got: %q", m.input.composer.Value())
	}

	// Composer band covers at least 3 rows
	renderedBand := renderComposerBand(m.input.composer.View(), 80)
	bandLines := strings.Split(renderedBand, "\n")
	if len(bandLines) < 3 {
		t.Errorf("expected composer band to have at least 3 lines, got: %d", len(bandLines))
	}
}

func TestComposerBandRendering(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.width = 80
	m.height = 24
	m.updateLayout()
	rendered := renderComposerBand(m.input.composer.View(), 76)
	lines := strings.Split(rendered, "\n")
	for i, l := range lines {
		t.Logf("Line %d: %q", i, l)
	}
}

func TestComposerBandRowsMatchSurfaceWidth(t *testing.T) {
	for _, width := range []int{20, 40, 80, 120} {
		for _, tc := range []struct {
			name  string
			value string
		}{
			{name: "empty"},
			{name: "short", value: "short prompt"},
			{name: "wrapped", value: strings.Repeat("多行输入 composer 宽度 ", 10)},
		} {
			t.Run(fmt.Sprintf("width=%d/%s", width, tc.name), func(t *testing.T) {
				m := newTUIModel(nil, nil, "test-sess")
				m.width = width + 4
				m.height = 30
				m.input.composer.SetValue(tc.value)
				m.updateLayout()

				rendered := ansi.Strip(renderComposerBand(m.input.composer.View(), width))
				lines := strings.Split(rendered, "\n")
				if len(lines) < 3 {
					t.Fatalf("expected at least 3 composer rows, got %d:\n%s", len(lines), rendered)
				}
				for i, line := range lines {
					if got := ansi.StringWidth(line); got != width {
						t.Fatalf("composer row %d width = %d, want %d: %q", i, got, width, line)
					}
				}
			})
		}
	}
}

func TestComposerMouseHitAreaMatchesRenderedWidth(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.width = 80
	m.height = 24
	m.updateLayout()

	_, y := m.composerMouseOffset()
	composerViewWidth := m.input.composer.Width() + lipgloss.Width(m.input.composer.Prompt)
	if composerViewWidth <= 0 {
		t.Fatalf("expected a positive composer width, got %d", composerViewWidth)
	}
	if got := lipgloss.Width(m.input.composer.View()); got != composerViewWidth {
		t.Fatalf("composer hit width = %d, rendered width = %d", composerViewWidth, got)
	}
	if !m.isComposerMousePoint(1, y) {
		t.Fatal("expected the first composer column to be interactive")
	}
	if !m.isComposerMousePoint(1+composerViewWidth-1, y) {
		t.Fatalf("expected the last rendered composer column %d to be interactive", 1+composerViewWidth-1)
	}
	if m.isComposerMousePoint(1+composerViewWidth, y) {
		t.Fatalf("column %d lies outside the rendered composer and must not be interactive", 1+composerViewWidth)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d        time.Duration
		expected string
	}{
		{0, "0s"},
		{5 * time.Second, "5s"},
		{40 * time.Second, "40s"},
		{60 * time.Second, "1m"},
		{1000 * time.Second, "16m 40s"},
		{3600 * time.Second, "1h 0m"},
		{3665 * time.Second, "1h 1m 5s"},
	}

	for _, tt := range tests {
		actual := formatDuration(tt.d)
		if actual != tt.expected {
			t.Errorf("formatDuration(%v) = %q, expected %q", tt.d, actual, tt.expected)
		}
	}
}

func TestWorkingIndicator(t *testing.T) {
	working := renderWorking(40*time.Second, false)
	if !strings.Contains(working, "Working (40s • esc to interrupt)") {
		t.Errorf("expected Working title, got: %s", working)
	}
	if !strings.Contains(working, "Tip:") {
		t.Errorf("expected Tip line, got: %s", working)
	}

	thinking := renderWorking(15*time.Second, true)
	if !strings.Contains(thinking, "Thinking (15s • esc to interrupt)") {
		t.Errorf("expected Thinking title, got: %s", thinking)
	}

	longer := renderWorking(1000*time.Second, false)
	if !strings.Contains(longer, "Working (16m 40s • esc to interrupt)") {
		t.Errorf("expected Working 16m 40s, got: %s", longer)
	}
}

func TestStreamingFiltersReasoningDelta(t *testing.T) {
	m := &tuiModel{}

	// 1. Reasoning delta should set thinking=true, accumulate currentReasoningText, and NOT add to currentStreamText
	m.handleRuntimeEvent(core.Event{
		Kind: core.EventModelStream,
		Stream: &core.ModelStreamEvent{
			Type:  "reasoning_delta",
			Delta: "Internal model thoughts...",
		},
	})
	if !m.run.thinking {
		t.Errorf("expected m.run.thinking to be true during reasoning_delta")
	}
	if m.run.currentStreamText != "" {
		t.Errorf("expected currentStreamText to be empty, got: %q", m.run.currentStreamText)
	}
	if m.run.currentReasoningText != "Internal model thoughts..." {
		t.Errorf("expected currentReasoningText to be 'Internal model thoughts...', got: %q", m.run.currentReasoningText)
	}

	// 2. Text delta should commit reasoning to entries as KindReasoning, append to currentStreamText, and reset thinking
	m.handleRuntimeEvent(core.Event{
		Kind: core.EventModelStream,
		Stream: &core.ModelStreamEvent{
			Type:  "text_delta",
			Delta: "Hello user",
		},
	})
	if m.run.thinking {
		t.Errorf("expected m.run.thinking to be false after text_delta")
	}
	if m.run.currentStreamText != "Hello user" {
		t.Errorf("expected currentStreamText to be 'Hello user', got: %q", m.run.currentStreamText)
	}
	if len(m.entries) != 1 || m.entries[0].Kind != KindReasoning {
		t.Fatalf("expected 1 KindReasoning entry committed, got %d", len(m.entries))
	}
	if m.entries[0].Text != "Internal model thoughts..." {
		t.Errorf("expected reasoning text 'Internal model thoughts...', got: %q", m.entries[0].Text)
	}

	// 3. select_tool_category should be ignored and not add to entries
	prevCount := len(m.entries)
	m.handleRuntimeEvent(core.Event{
		Kind: core.EventToolStarted,
		Tool: core.ToolCall{
			Name: "select_tool_category",
		},
	})
	if len(m.entries) != prevCount {
		t.Errorf("expected select_tool_category to be ignored, got %d entries", len(m.entries))
	}
}

func TestReasoningRenderingAndToggle(t *testing.T) {
	m := &tuiModel{
		width: 80,
	}

	entry := TranscriptEntry{
		Kind:              KindReasoning,
		Text:              "Step 1: check files\nStep 2: fix bug",
		ReasoningDuration: 12 * time.Second,
		ReasoningExpanded: false,
	}
	m.entries = append(m.entries, entry)

	// 1. Collapsed rendering: contains Thought for 12s and (click to expand)
	collapsed := renderReasoningEntry(m.entries[0], 80)
	if !strings.Contains(collapsed, "Thought for 12s") || !strings.Contains(collapsed, "(click to expand)") {
		t.Errorf("Unexpected collapsed rendering: %s", collapsed)
	}
	if strings.Contains(collapsed, "Step 1: check files") {
		t.Errorf("Collapsed rendering should not contain body text: %s", collapsed)
	}

	// 2. Toggle via the /thought command path
	_, _ = m.toggleReasoningExpansion()
	if !m.entries[0].ReasoningExpanded {
		t.Errorf("Expected ReasoningExpanded to be true after toggle")
	}

	// 3. Expanded rendering: contains body text and (click to collapse)
	expanded := renderReasoningEntry(m.entries[0], 80)
	if !strings.Contains(expanded, "(click to collapse)") {
		t.Errorf("Unexpected expanded rendering header: %s", expanded)
	}
	if !strings.Contains(expanded, "Step 1: check files") {
		t.Errorf("Expanded rendering should contain body text: %s", expanded)
	}

	// 4. Live streaming rendering (ReasoningLive = true)
	live := TranscriptEntry{
		Kind:              KindReasoning,
		Text:              "Currently thinking about logic...",
		ReasoningDuration: 3 * time.Second,
		ReasoningExpanded: true,
		ReasoningLive:     true,
	}
	renderedLive := renderReasoningEntry(live, 80)
	if !strings.Contains(renderedLive, "Thinking... (3s)") {
		t.Errorf("Expected live header to have Thinking... (3s), got: %s", renderedLive)
	}
	if !strings.Contains(renderedLive, "Currently thinking about logic...") {
		t.Errorf("Expected live body to contain text, got: %s", renderedLive)
	}
}

func TestMouseWheelScrolling(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.width = 80
	m.height = 24
	m.viewport.SetWidth(80)
	m.viewport.SetHeight(10)
	// Add many lines of content
	m.viewport.SetContent(strings.Repeat("line\n", 50))
	m.viewport.GotoBottom()
	if m.scrolledUp {
		t.Errorf("expected scrolledUp to be false at bottom")
	}

	// Scroll up with mouse wheel
	mod, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	m = mod.(*tuiModel)
	if !m.scrolledUp {
		t.Errorf("expected scrolledUp to be true after MouseWheelUp")
	}

	// Click to jump back to bottom
	mod, _ = m.Update(tea.MouseClickMsg{Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	if m.scrolledUp {
		t.Errorf("expected scrolledUp to be false after clicking back to bottom")
	}
}

func TestApprovalPanelRendering(t *testing.T) {
	appr := &ActiveApproval{
		Request: core.ApprovalRequest{
			ID:        "appr-1",
			Tool:      "run_command",
			Arguments: []byte(`{"command":"rm -rf /"}`),
		},
		Selected: 0,
	}
	rendered := renderApprovalPanel(appr, 80)
	if !strings.Contains(rendered, "Tool Approval Required") {
		t.Errorf("expected approval title, got: %s", rendered)
	}
	if !strings.Contains(rendered, "run_command") {
		t.Errorf("expected tool name, got: %s", rendered)
	}
	if !strings.Contains(rendered, "1. Allow execution") {
		t.Errorf("expected allow option, got: %s", rendered)
	}
}

func TestApprovalPanelExplainsExternalDirectoryGrant(t *testing.T) {
	appr := &ActiveApproval{Request: core.ApprovalRequest{
		ID: "grant-approval", Tool: "read_file",
		PathGrant: &publictool.PathGrant{Path: `/home/user/private`, Access: publictool.PathReadWrite},
	}}
	rendered := renderApprovalPanel(appr, 90)
	for _, expected := range []string{"Session grant", "read and write", "Directory:", "/home", "private", "Allow this call once", "Grant for this session"} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("approval panel is missing %q: %s", expected, rendered)
		}
	}
}

func TestTranscriptFromHistory(t *testing.T) {
	history := []byte(`[
		{"type":"message","role":"user","content":"Hello from past"},
		{"type":"function_call","name":"list_dir","arguments":"{\"path\":\".\"}"},
		{"type":"message","role":"assistant","content":"Done listing files"}
	]`)
	entries := transcriptFromHistory(history)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	if entries[0].Kind != KindUser || entries[0].Text != "Hello from past" {
		t.Errorf("unexpected entry 0: %+v", entries[0])
	}
	if entries[1].Kind != KindTool || entries[1].ToolName != "list_dir" {
		t.Errorf("unexpected entry 1: %+v", entries[1])
	}
	if entries[2].Kind != KindAssistant || entries[2].Text != "Done listing files" {
		t.Errorf("unexpected entry 2: %+v", entries[2])
	}
}

func TestSystemMarkdownRendering(t *testing.T) {
	entry := TranscriptEntry{
		Kind: KindSystem,
		Text: "Switched to **Plan mode** (read-only tools).",
	}
	rendered := renderEntry(entry, 80)
	if strings.Contains(rendered, "**") {
		t.Errorf("expected bold formatting without asterisks, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Plan mode") {
		t.Errorf("expected Plan mode text, got: %s", rendered)
	}
}

func TestMouseDragSelectionAndCopy(t *testing.T) {
	m := &tuiModel{
		viewport: newTUIModel(nil, nil, "test-sess").viewport,
	}
	m.input.clipboardWriter = func(string) error { return nil }
	m.width = 80
	m.height = 24
	m.viewport.SetWidth(80)
	m.viewport.SetHeight(10)
	m.viewport.SetContent("Line 0: Hello World\nLine 1: Second line of text\nLine 2: Third line")

	// Click at (8, 3) -> row 0 in viewport, col 8 ("Hello World")
	mod, _ := m.Update(tea.MouseClickMsg{
		X:      8,
		Y:      3,
		Button: tea.MouseLeft,
	})
	m = mod.(*tuiModel)
	if !m.input.selecting {
		t.Fatalf("expected isSelecting to be true")
	}

	// Drag to (13, 3) -> "Hello"
	mod, _ = m.Update(tea.MouseMotionMsg{
		X: 13,
		Y: 3,
	})
	m = mod.(*tuiModel)
	if m.input.selectEndX != 13 || m.input.selectEndY != 3 {
		t.Fatalf("expected selectEnd to be (13, 3), got (%d, %d)", m.input.selectEndX, m.input.selectEndY)
	}

	// Release mouse
	mod, _ = m.Update(tea.MouseReleaseMsg{
		X:      13,
		Y:      3,
		Button: tea.MouseLeft,
	})
	m = mod.(*tuiModel)
	if m.input.selecting {
		t.Fatalf("expected isSelecting to be false after release")
	}
	if m.input.selectedText != "Hello" {
		t.Fatalf("expected selected text to be 'Hello', got %q", m.input.selectedText)
	}
	if !strings.Contains(m.input.copyFeedback, "Copied 5 chars to host clipboard") {
		t.Fatalf("expected copyFeedback with 5 chars, got %q", m.input.copyFeedback)
	}

	// Esc clears selection
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.input.selectedText != "" {
		t.Fatalf("expected selectedText to be cleared on Esc, got %q", m.input.selectedText)
	}
}

func TestMouseScrollSelectionAcrossPagesAndCopy(t *testing.T) {
	m := &tuiModel{
		viewport: newTUIModel(nil, nil, "test-sess").viewport,
	}
	var copiedText string
	m.input.clipboardWriter = func(s string) error {
		copiedText = s
		return nil
	}
	m.width = 80
	m.height = 24
	m.viewport.SetWidth(80)
	m.viewport.SetHeight(10)

	var sb strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&sb, "Row %02d: content of the row\n", i)
	}
	m.viewport.SetContent(strings.TrimRight(sb.String(), "\n"))
	m.viewport.SetYOffset(0)

	// 1. Mouse left down at row 2 of viewport (Y=5, X=0) -> "Row 02"
	mod, _ := m.Update(tea.MouseClickMsg{
		X:      0,
		Y:      5,
		Button: tea.MouseLeft,
	})
	m = mod.(*tuiModel)
	if !m.input.selecting {
		t.Fatal("expected selecting to be true")
	}
	if m.input.selectStartDocRow != 2 {
		t.Fatalf("expected selectStartDocRow to be 2, got %d", m.input.selectStartDocRow)
	}

	// 2. Mouse wheel down multiple times to scroll across pages
	for i := 0; i < 10; i++ {
		mod, _ = m.Update(tea.MouseWheelMsg{
			X:      20,
			Y:      5,
			Button: tea.MouseWheelDown,
		})
		m = mod.(*tuiModel)
	}

	// Viewport should have scrolled down
	if m.viewport.YOffset() <= 10 {
		t.Fatalf("expected viewport to have scrolled down significantly, yoffset=%d", m.viewport.YOffset())
	}
	// EndDocRow should have extended with the scroll
	if m.input.selectEndDocRow <= 10 {
		t.Fatalf("expected selectEndDocRow to follow scroll, got %d", m.input.selectEndDocRow)
	}

	// 3. Mouse release at Y=5, X=26
	mod, _ = m.Update(tea.MouseReleaseMsg{
		X:      26,
		Y:      5,
		Button: tea.MouseLeft,
	})
	m = mod.(*tuiModel)
	if m.input.selecting {
		t.Fatal("expected selecting to be false after release")
	}

	// Verify text was copied across the scrolled pages
	if !strings.Contains(copiedText, "Row 02") {
		t.Fatalf("expected copied text to start with Row 02, got:\n%s", copiedText)
	}
	// Should contain lines from the middle pages that were scrolled past
	if !strings.Contains(copiedText, "Row 15") {
		t.Fatalf("expected copied text to contain scrolled-past Row 15, got:\n%s", copiedText)
	}
	if !strings.Contains(copiedText, fmt.Sprintf("Row %02d", m.input.selectEndDocRow)) {
		t.Fatalf("expected copied text to contain end row %d, got:\n%s", m.input.selectEndDocRow, copiedText)
	}
}

func TestMouseDragToEdgeAutoScroll(t *testing.T) {
	m := &tuiModel{
		viewport: newTUIModel(nil, nil, "test-sess").viewport,
	}
	m.input.clipboardWriter = func(string) error { return nil }
	m.width = 80
	m.height = 24
	m.viewport.SetWidth(80)
	m.viewport.SetHeight(10)

	var sb strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&sb, "Line %02d: text\n", i)
	}
	m.viewport.SetContent(strings.TrimRight(sb.String(), "\n"))
	m.viewport.SetYOffset(0)

	// Click at viewport top
	mod, _ := m.Update(tea.MouseClickMsg{X: 0, Y: 3, Button: tea.MouseLeft})
	m = mod.(*tuiModel)

	// Drag below viewport bottom (vpEnd = 3 + 10 = 13, drag to Y=15)
	mod, _ = m.Update(tea.MouseMotionMsg{X: 10, Y: 15})
	m = mod.(*tuiModel)

	if m.viewport.YOffset() == 0 {
		t.Fatal("expected auto-scroll down when dragged below viewport")
	}
	if m.input.selectEndDocRow <= 0 {
		t.Fatalf("expected selectEndDocRow to advance beyond 0, got %d", m.input.selectEndDocRow)
	}
}

func TestComposerMouseDragSelectionAndCopy(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.input.clipboardWriter = func(string) error { return nil }
	m.width = 80
	m.height = 24
	m.input.composer.SetValue("copy me")
	m.updateLayout()

	x, y := m.composerMouseOffset()
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if y >= len(lines) || !strings.HasPrefix(lines[y], " "+ansi.Strip(m.input.composer.View())) {
		t.Fatalf("composer mouse offset (%d, %d) does not point to rendered composer", x, y)
	}

	textStart := x + lipgloss.Width(m.input.composer.Prompt)
	mod, _ := m.Update(tea.MouseClickMsg{X: textStart, Y: y, Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	if !m.input.composerSelecting {
		t.Fatal("expected a composer selection to start")
	}

	mod, _ = m.Update(tea.MouseMotionMsg{X: textStart + 4, Y: y})
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.MouseReleaseMsg{X: textStart + 4, Y: y, Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	if m.input.composerSelecting {
		t.Fatal("expected composer selection to end on mouse release")
	}
	if got := m.input.composer.SelectedText(); got != "copy" {
		t.Fatalf("expected composer selection %q, got %q", "copy", got)
	}

	mod, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = mod.(*tuiModel)
	if cmd == nil {
		t.Fatal("expected Ctrl+C to schedule copy feedback")
	}
	if got := m.input.copyFeedback; got != "Copied 4 chars to host clipboard" {
		t.Fatalf("expected copy feedback for composer selection, got %q", got)
	}

	mod, _ = m.Update(tea.MouseClickMsg{X: 10, Y: 3, Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	if m.input.composer.HasSelection() {
		t.Fatal("expected clicking outside the composer to clear its selection")
	}
}

func TestComposerMouseSelectionAcrossLinesAndSoftWrap(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.width = 80
	m.height = 24
	m.input.composer.SetValue("第一行\nsecond line")
	m.updateLayout()

	x, y := m.composerMouseOffset()
	textStart := x + lipgloss.Width(m.input.composer.Prompt)
	mod, _ := m.Update(tea.MouseClickMsg{X: textStart, Y: y, Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.MouseMotionMsg{X: textStart + 3, Y: y + 1})
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.MouseReleaseMsg{X: textStart + 3, Y: y + 1, Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	if got := m.input.composer.SelectedText(); got != "第一行\nsec" {
		t.Fatalf("expected multiline selection %q, got %q", "第一行\nsec", got)
	}

	m = newTUIModel(nil, nil, "test-sess")
	m.width = 25 // The composer is 20 columns wide after the band's left padding, forcing this line to wrap.
	m.height = 24
	m.input.composer.SetValue("abcdefghijklmnopqrst")
	m.updateLayout()
	x, y = m.composerMouseOffset()
	textStart = x + lipgloss.Width(m.input.composer.Prompt)
	mod, _ = m.Update(tea.MouseClickMsg{X: textStart + 16, Y: y, Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	if !m.input.composerSelecting {
		t.Fatal("expected soft-wrap composer selection to start")
	}
	mod, _ = m.Update(tea.MouseMotionMsg{X: textStart + 2, Y: y + 1})
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.MouseReleaseMsg{X: textStart + 2, Y: y + 1, Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	if got := m.input.composer.SelectedText(); got != "qrst" {
		t.Fatalf("expected selection across soft wrap %q, got %q", "qrst", got)
	}
}

func TestComposerKeyboardSelectionAndCtrlCPrecedence(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.input.clipboardWriter = func(string) error { return nil }
	m.input.composer.SetValue("hello")
	m.input.composer.CursorStart()
	for range 2 {
		mod, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
		m = mod.(*tuiModel)
	}
	if got := m.input.composer.SelectedText(); got != "he" {
		t.Fatalf("expected keyboard selection %q, got %q", "he", got)
	}

	// The composer is the active copy target when both selection stores happen
	// to contain text.
	m.input.selectedText = "transcript selection"
	mod, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = mod.(*tuiModel)
	if cmd == nil || m.input.copyFeedback != "Copied 2 chars to host clipboard" {
		t.Fatalf("Ctrl+C did not copy the composer selection: cmd=%v feedback=%q", cmd != nil, m.input.copyFeedback)
	}

	m.input.composer.ClearSelection()
	m.input.composer.CursorStart()
	mod, _ = m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = mod.(*tuiModel)
	if got := m.input.composer.SelectedText(); got != "hello" {
		t.Fatalf("expected Ctrl+G to select all input, got %q", got)
	}
	mod, cmd = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl | tea.ModShift})
	m = mod.(*tuiModel)
	if cmd == nil || m.input.copyFeedback != "Copied 5 chars to host clipboard" {
		t.Fatalf("Ctrl+Shift+C did not use the safe composer copy path: cmd=%v feedback=%q", cmd != nil, m.input.copyFeedback)
	}
	mod, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = mod.(*tuiModel)
	if m.input.composer.HasSelection() || m.input.composer.Value() != "hello" {
		t.Fatalf("Esc should clear the input selection without changing its content: value=%q selected=%t", m.input.composer.Value(), m.input.composer.HasSelection())
	}
}

func TestCtrlCWithoutComposerSelectionKeepsQuitBehavior(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	mod, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if mod != m || cmd == nil {
		t.Fatal("Ctrl+C without a selection should keep the existing quit command")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Fatalf("expected Ctrl+C to quit without a selection, got %T", msg)
	}
}

func TestShortcutsPanelRendering(t *testing.T) {
	renderedWide := renderShortcutsPanel(100)
	strippedWide := ansi.Strip(renderedWide)
	if !strings.Contains(strippedWide, "Keyboard Shortcuts") {
		t.Errorf("expected title 'Keyboard Shortcuts', got: %s", strippedWide)
	}
	if !strings.Contains(strippedWide, "Compose") || !strings.Contains(strippedWide, "Session") || !strings.Contains(strippedWide, "Transcript") {
		t.Errorf("expected 3 column titles, got: %s", strippedWide)
	}
	if !strings.Contains(strippedWide, "Shift+Enter") || !strings.Contains(strippedWide, "Shift+Tab") || !strings.Contains(strippedWide, "Mouse Drag") || !strings.Contains(strippedWide, "Select all input") {
		t.Errorf("expected shortcut keys, got: %s", strippedWide)
	}
	if !strings.Contains(strippedWide, "Toggle Plan mode") || !strings.Contains(strippedWide, "Switch permission mode") {
		t.Errorf("expected Plan and permission shortcut entries, got: %s", strippedWide)
	}
	if !strings.Contains(strippedWide, "esc or ? to close") {
		t.Errorf("expected close hint, got: %s", strippedWide)
	}

	renderedNarrow := renderShortcutsPanel(60)
	strippedNarrow := ansi.Strip(renderedNarrow)
	if !strings.Contains(strippedNarrow, "Keyboard Shortcuts") || !strings.Contains(strippedNarrow, "Compose") {
		t.Errorf("expected narrow rendering to include content, got: %s", strippedNarrow)
	}
}

func TestShortcutsModalInteractions(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.width = 80
	m.height = 24

	// 1. Press '?' when composer is empty opens shortcuts
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '?', Text: "?"}))
	m = mod.(*tuiModel)
	if !m.modals.shortcuts {
		t.Fatalf("expected shortcutsOpen to be true after typing '?'")
	}

	// 2. Press '?' again closes shortcuts
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '?', Text: "?"}))
	m = mod.(*tuiModel)
	if m.modals.shortcuts {
		t.Fatalf("expected shortcutsOpen to be false after typing '?' again")
	}

	// 3. /shortcuts slash command opens shortcuts
	m.input.composer.SetValue("/shortcuts")
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)
	if !m.modals.shortcuts {
		t.Fatalf("expected shortcutsOpen to be true after /shortcuts command")
	}

	// 4. Esc closes shortcuts
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.modals.shortcuts {
		t.Fatalf("expected shortcutsOpen to be false after Esc")
	}

	// 5. Typing '?' when composer has text does NOT open shortcuts
	m.input.composer.SetValue("Is this a test?")
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '?', Text: "?"}))
	m = mod.(*tuiModel)
	if m.modals.shortcuts {
		t.Fatalf("expected shortcutsOpen to remain false when typing '?' with existing text")
	}
}

func TestPromptHistoryNavigationAndEscSemantics(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.input.history = []string{"First prompt", "Second prompt", "Third prompt"}
	m.input.historyIndex = len(m.input.history)

	// User types an unsent draft
	m.input.composer.SetValue("My draft")

	// Up key saves draft and loads last history prompt ("Third prompt")
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mod.(*tuiModel)
	if m.input.composer.Value() != "Third prompt" {
		t.Fatalf("expected 'Third prompt', got %q", m.input.composer.Value())
	}
	if m.input.historyDraft != "My draft" {
		t.Fatalf("expected historyDraft to be 'My draft', got %q", m.input.historyDraft)
	}

	// Up key again loads "Second prompt"
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mod.(*tuiModel)
	if m.input.composer.Value() != "Second prompt" {
		t.Fatalf("expected 'Second prompt', got %q", m.input.composer.Value())
	}

	// Up key again loads "First prompt"
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mod.(*tuiModel)
	if m.input.composer.Value() != "First prompt" {
		t.Fatalf("expected 'First prompt', got %q", m.input.composer.Value())
	}

	// Down key moves forward to "Second prompt"
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mod.(*tuiModel)
	if m.input.composer.Value() != "Second prompt" {
		t.Fatalf("expected 'Second prompt', got %q", m.input.composer.Value())
	}

	// Down key moves forward to "Third prompt"
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mod.(*tuiModel)
	if m.input.composer.Value() != "Third prompt" {
		t.Fatalf("expected 'Third prompt', got %q", m.input.composer.Value())
	}

	// Down key restores original draft "My draft"
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mod.(*tuiModel)
	if m.input.composer.Value() != "My draft" {
		t.Fatalf("expected restored draft 'My draft', got %q", m.input.composer.Value())
	}

	// Esc does NOT clear composer or restore history; draft remains intact
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.input.composer.Value() != "My draft" {
		t.Fatalf("expected Esc to leave composer draft intact, got %q", m.input.composer.Value())
	}

	// Esc when composer is empty steps back to edit last message ("Third prompt")
	m.input.composer.SetValue("")
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.input.composer.Value() != "Third prompt" {
		t.Fatalf("expected Esc on empty composer to restore 'Third prompt', got %q", m.input.composer.Value())
	}

	// Esc when text is present leaves composer text intact
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.input.composer.Value() != "Third prompt" {
		t.Fatalf("expected Esc on non-empty composer to leave text intact, got %q", m.input.composer.Value())
	}

	// Up key navigates to previous message ("Second prompt")
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mod.(*tuiModel)
	if m.input.composer.Value() != "Second prompt" {
		t.Fatalf("expected Up to load 'Second prompt', got %q", m.input.composer.Value())
	}
}

func TestToolExpandAndCollapseHint(t *testing.T) {
	toolEntry := TranscriptEntry{
		Kind:         KindTool,
		ToolName:     "run_command",
		ToolCommand:  "git status",
		ToolOutput:   "line1\nline2\nline3\nline4\nline5\nline6",
		ToolExpanded: false,
	}

	renderedCollapsed := renderEntry(toolEntry, 80)
	if !strings.Contains(renderedCollapsed, "+ 3 lines (click to expand · /detail for full output)") {
		t.Errorf("expected expand hint, got: %s", renderedCollapsed)
	}

	toolEntry.ToolExpanded = true
	renderedExpanded := renderEntry(toolEntry, 80)
	if !strings.Contains(renderedExpanded, "collapse 6 lines (click)") {
		t.Errorf("expected collapse hint, got: %s", renderedExpanded)
	}
}

func TestRoundToolPreviewUsesHeadForSuccessAndTailForFailure(t *testing.T) {
	success := TranscriptEntry{
		Kind:        KindTool,
		ToolName:    "run_command",
		ToolCommand: "git status --short",
		ToolOutput:  "line1\nline2\nline3\nline4\nline5",
	}
	rendered := ansi.Strip(renderRoundChildEntry(success, 100))
	if !strings.Contains(rendered, "line1") || !strings.Contains(rendered, "line3") {
		t.Fatalf("expected successful tool preview to show its first lines, got:\n%s", rendered)
	}
	if strings.Contains(rendered, "line4") || strings.Contains(rendered, "line5") {
		t.Fatalf("expected successful tool preview to stop after three lines, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "+ 2 lines (click to expand · /detail for full output)") {
		t.Fatalf("expected a three-line expand hint, got:\n%s", rendered)
	}

	exitOne := 1
	failed := success
	failed.ToolExitCode = &exitOne
	failedRendered := ansi.Strip(renderRoundChildEntry(failed, 100))
	if strings.Contains(failedRendered, "line1") || !strings.Contains(failedRendered, "line5") {
		t.Fatalf("expected failed tool preview to show its last lines, got:\n%s", failedRendered)
	}

	success.ToolExpanded = true
	expanded := ansi.Strip(renderRoundChildEntry(success, 100))
	if !strings.Contains(expanded, "line5") || !strings.Contains(expanded, "- collapse 5 lines (click)") {
		t.Fatalf("expected expanded tool output and collapse hint, got:\n%s", expanded)
	}
}

func TestRoundFileChangePreviewShowsThreeDiffLines(t *testing.T) {
	entry := TranscriptEntry{
		Kind:     KindTool,
		ToolName: "write_file",
		ToolChanges: []publictool.FileChange{{
			Path:  "internal/tui/view.go",
			Added: 5,
			Lines: []publictool.DiffLine{
				{Kind: "add", NewLine: 1, Text: "line one"},
				{Kind: "add", NewLine: 2, Text: "line two"},
				{Kind: "add", NewLine: 3, Text: "line three"},
				{Kind: "add", NewLine: 4, Text: "line four"},
				{Kind: "add", NewLine: 5, Text: "line five"},
			},
		}},
	}

	preview := ansi.Strip(renderRoundChildEntry(entry, 100))
	if !strings.Contains(preview, "Added internal/tui/view.go (+5 -0)") ||
		!strings.Contains(preview, "line one") ||
		!strings.Contains(preview, "line three") ||
		!strings.Contains(preview, "+ 2 lines (click to expand)") {
		t.Fatalf("expected a three-line file diff preview and hint, got:\n%s", preview)
	}
	if strings.Contains(preview, "line four") || strings.Contains(preview, "line five") {
		t.Fatalf("expected the file diff preview to stop after three lines, got:\n%s", preview)
	}

	entry.ToolExpanded = true
	expanded := ansi.Strip(renderRoundChildEntry(entry, 100))
	if !strings.Contains(expanded, "line five") || !strings.Contains(expanded, "Hide details") {
		t.Fatalf("expected the full file diff after expansion, got:\n%s", expanded)
	}
}

func TestViewportAutoStickToBottomOnMultiTurn(t *testing.T) {
	m := &tuiModel{}
	m.width = 80
	m.height = 24
	m.viewport = viewport.New(viewport.WithWidth(80), viewport.WithHeight(10))

	// Turn 1: Add some entries
	m.entries = append(m.entries, TranscriptEntry{
		Kind: KindUser,
		Text: "Question 1",
	})
	m.entries = append(m.entries, TranscriptEntry{
		Kind: KindAssistant,
		Text: "Answer 1 with multiple lines\nLine 2\nLine 3\nLine 4\nLine 5\nLine 6\nLine 7\nLine 8\nLine 9\nLine 10",
	})
	m.refreshViewportContent()

	if !m.viewport.AtBottom() {
		t.Errorf("expected viewport to be at bottom after turn 1, atBottom=%v, yoffset=%d", m.viewport.AtBottom(), m.viewport.YOffset())
	}

	// Turn 2: User sends second message while running
	m.entries = append(m.entries, TranscriptEntry{
		Kind: KindUser,
		Text: "Question 2",
	})
	m.run.running = true
	m.run.taskStartedAt = time.Now().Add(-5 * time.Second)
	m.refreshViewportContent()

	if !m.viewport.AtBottom() {
		t.Errorf("expected viewport to remain at bottom when running in turn 2, atBottom=%v, yoffset=%d", m.viewport.AtBottom(), m.viewport.YOffset())
	}

	// Live activity renders beside the historical viewport so streamed changes
	// do not rebuild or remeasure every transcript entry.
	if !strings.Contains(m.view.liveContent, "Working") {
		t.Errorf("expected working indicator in live content, got:\n%s", m.view.liveContent)
	}
}

func TestShortOverlayKeepsSelectedChoiceVisible(t *testing.T) {
	for rows := 1; rows <= 3; rows++ {
		panel := fitOverlayHeight("Question\nPrompt\n  1. First\n› 2. Selected\n  3. Third\nHint", rows)
		if !strings.Contains(panel, "› 2. Selected") || len(strings.Split(panel, "\n")) > rows {
			t.Fatalf("short overlay with %d rows lost the selected option or overflowed: %q", rows, panel)
		}
	}
}

func TestQuestionOverlayFitsSmallTerminalHeights(t *testing.T) {
	options := make([]core.UserQuestionOption, 12)
	for i := range options {
		options[i] = core.UserQuestionOption{Label: fmt.Sprintf("Option %d", i+1)}
	}
	m := newTUIModel(nil, nil, "small-terminal")
	m.width = 80
	m.entries = []TranscriptEntry{{Kind: KindUser, Text: "choose"}}
	m.activeQuestion = newActiveQuestion(core.UserQuestionRequest{Questions: []core.UserQuestionItem{{
		ID: "q", Question: "Choose an option", Options: options,
	}}})
	m.activeQuestion.currentDraft().Selected = len(options) - 1
	for _, height := range []int{10, 14, 18, 24} {
		m.height = height
		m.updateLayout()
		view := ansi.Strip(m.View().Content)
		lines := strings.Split(view, "\n")
		if len(lines) > height {
			t.Fatalf("question overlay rendered %d rows in a %d-row terminal", len(lines), height)
		}
		if height >= 14 && !strings.Contains(view, "› 12. Option 12") {
			t.Fatalf("selected option disappeared at height %d:\n%s", height, view)
		}
	}
}

func TestRenderCardSurfacePreservesBackground(t *testing.T) {
	inner := "Before " + styleTextMuted().Render("code.go") + " After"
	card := lipgloss.NewStyle().Background(lipgloss.Color(activeTheme().CardBg)).Foreground(lipgloss.Color(activeTheme().TextPrimary)).Width(50)
	rendered := renderCardSurface(card, inner)
	if strings.Contains(rendered, "\x1b[0m After") || strings.Contains(rendered, "\x1b[m After") {
		t.Errorf("expected card background SGR to be re-asserted after inner reset, got %q", rendered)
	}
}

func TestUserMessageCardHasConsistentPaddingAndWidth(t *testing.T) {
	const width = 64
	rendered := renderEntry(TranscriptEntry{
		Kind:          KindUser,
		Text:          strings.Repeat("A deliberately long user message stays readable when it wraps. ", 2),
		AttachedFiles: []string{"internal/tui/view.go"},
	}, width)
	lines := strings.Split(ansi.Strip(rendered), "\n")
	if len(lines) < 5 || !strings.Contains(rendered, "› ") || !strings.Contains(rendered, "internal/tui/view.go") {
		t.Fatalf("expected a padded, wrapped user card with its attachment, got:\n%s", rendered)
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got != width {
			t.Fatalf("user card row %d width = %d, want %d: %q", i, got, width, line)
		}
	}
}

func TestComposerBandUsesThemeCardBackground(t *testing.T) {
	previousMode := CurrentThemeMode()
	t.Cleanup(func() { SetThemeMode(previousMode) })

	for _, test := range []struct {
		mode ThemeMode
		bg   string
	}{
		{mode: ThemeModeLight, bg: LightTheme.CardBg},
		{mode: ThemeModeDark, bg: DarkTheme.CardBg},
	} {
		SetThemeMode(test.mode)
		m := newTUIModel(nil, nil, "test-sess")
		m.input.composer.SetWidth(40)
		rendered := renderComposerBand(m.input.composer.View(), 40)
		r, g, b, ok := parseHexRGB(test.bg)
		if !ok {
			t.Fatalf("invalid test background %q", test.bg)
		}
		want := fmt.Sprintf("48;2;%d;%d;%d", r, g, b)
		if !strings.Contains(rendered, want) {
			t.Errorf("%s composer band does not contain its theme background %q: %q", test.mode, test.bg, rendered)
		}
	}
}

func TestMarkdownInlineCodeProtectsFileRefsAndBold(t *testing.T) {
	md := "Check `internal/tui/view.go:100` and `__init__.py` in `cmd/main.go`."
	rendered := renderMarkdown(md, 80)
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "internal/tui/view.go:100") {
		t.Errorf("expected inline code file path to remain intact, got %q", stripped)
	}
	if !strings.Contains(stripped, "__init__.py") {
		t.Errorf("expected underscores inside inline code not to be stripped as bold, got %q", stripped)
	}
}

func TestSanitizeAndTruncateLineUTF8(t *testing.T) {
	cjk := "这是一段包含中文字符和\t制表符的很长文本，用于测试按显示宽度安全截断不会产生乱码"
	truncated := sanitizeAndTruncateLine(cjk, 24)
	if strings.Contains(truncated, "\t") {
		t.Errorf("expected tabs to be expanded, got %q", truncated)
	}
	if strings.ContainsRune(truncated, '\ufffd') {
		t.Errorf("expected valid UTF-8 without replacement char, got %q", truncated)
	}
	if ansi.StringWidth(truncated) > 24 {
		t.Errorf("expected display width <= 24, got %d (%q)", ansi.StringWidth(truncated), truncated)
	}
}

func TestGrokToolRoundCollapseAndTwoLevelClickExpand(t *testing.T) {
	m := newTUIModel(nil, nil, "grok-round-sess")
	m.width = 100
	m.height = 44
	m.updateLayout()

	// 1. Simulate an active (live) tool round while m.run.running == true and currentStreamText == ""
	m.run.running = true
	m.run.taskStartedAt = time.Now().Add(-46 * time.Second)
	m.entries = []TranscriptEntry{
		{
			Kind: KindUser,
			Text: "检查一下这个项目",
		},
		{
			Kind:              KindReasoning,
			Text:              "First let's list the root directory.",
			ReasoningDuration: 1600 * time.Millisecond,
		},
		{
			Kind:        KindTool,
			ToolName:    "list_dir",
			ToolCommand: "List D:\\Git\\luneAgent",
			ToolOutput:  "cmd/\ninternal/\ngo.mod",
		},
		{
			Kind:              KindQuestionAnswered,
			QuestionsTotal:    1,
			AnsweredQuestions: []AnsweredQuestionItem{{Question: "这次分析希望回答什么问题？", Answer: "整体代码审查 (推荐)"}},
		},
		{
			Kind:        KindTool,
			ToolName:    "read_file",
			ToolCommand: "Read go.mod",
			ToolOutput:  "module github.com/afterlune/lune",
		},
		{
			Kind:        KindTool,
			ToolName:    "run_command",
			ToolCommand: "git status --short",
			ToolOutput:  " M internal/tui/view.go\n M internal/tui/model.go\n M internal/tui/view_tools.go\n M internal/tui/view_footer.go\n M internal/tui/update_keys.go\n M internal/tui/command_model.go",
		},
	}

	m.view.invalidateBlocks()
	m.refreshViewportContent()
	liveView := ansi.Strip(m.viewport.View())

	// The completed pre-question round is independent from the standalone Q&A;
	// only the currently running post-question round expands live.
	if !strings.Contains(liveView, "› Explored 1 directory") ||
		!strings.Contains(liveView, "⠋ Explored 1 file, ran 1 command") {
		t.Fatalf("expected separate pre-question and live round summaries, got:\n%s", liveView)
	}
	if !strings.Contains(liveView, "Questions 1/1 answered") ||
		!strings.Contains(liveView, "这次分析希望回答什么问题？") ||
		!strings.Contains(liveView, "整体代码审查 (推荐)") ||
		strings.Contains(liveView, "│ › Ask 这次分析希望回答什么问题？") ||
		!strings.Contains(liveView, "│ › Read go.mod") ||
		!strings.Contains(liveView, "│ › Ran git status --short") {
		t.Fatalf("expected standalone Q&A and post-question round children, got:\n%s", liveView)
	}
	if !strings.Contains(liveView, "└ module github.com/afterlune/lune") ||
		!strings.Contains(liveView, "M internal/tui/view.go") {
		t.Fatalf("expected live round tool output previews, got:\n%s", liveView)
	}

	// 2. Complete the turn via finalizeTurnOutput -> round auto-collapses into 1-line summary bar + Worked for 46s
	m.run.currentStreamText = "这是一个多形态 AI 编程 Agent 宿主仓库。"
	m.finalizeTurnOutput("", nil, "exec-grok-1")
	m.run.running = false
	m.refreshViewportContent()

	collapsedView := ansi.Strip(m.viewport.View())
	if !strings.Contains(collapsedView, "› Explored 1 directory") ||
		!strings.Contains(collapsedView, "› Explored 1 file, ran 1 command") {
		t.Fatalf("expected separate collapsed execution summaries, got:\n%s", collapsedView)
	}
	if strings.Contains(collapsedView, "│ › Read go.mod") {
		t.Fatalf("expected child steps to be hidden when round is auto-collapsed, got:\n%s", collapsedView)
	}
	if !strings.Contains(collapsedView, "Questions 1/1 answered") ||
		!strings.Contains(collapsedView, "整体代码审查 (推荐)") ||
		strings.Contains(collapsedView, "Ask 这次分析希望回答什么问题？") {
		t.Fatalf("expected the answered question to remain a standalone card, got:\n%s", collapsedView)
	}
	if !strings.Contains(collapsedView, "Worked for 46s") {
		t.Fatalf("expected 'Worked for 46s' on completed assistant turn, got:\n%s", collapsedView)
	}

	// 3. Level 1 Click: Click on the collapsed round header bar -> expands the round tree (⌄ Explored ...)
	var roundHeaderLine int = -1
	for _, rng := range m.view.entryLineRanges {
		if rng.IsRoundHeader && rng.EntryIndex == 4 {
			roundHeaderLine = rng.StartLine
			break
		}
	}
	if roundHeaderLine < 0 {
		t.Fatalf("expected post-question round header range for EntryIndex 4, got %+v", m.view.entryLineRanges)
	}

	clickLine := func(absLine int) {
		vpY := absLine - m.viewport.YOffset() + 3 // headerHeight == 3
		mod, _ := m.Update(tea.MouseClickMsg{X: 5, Y: vpY, Button: tea.MouseLeft})
		m = mod.(*tuiModel)
		mod, _ = m.Update(tea.MouseReleaseMsg{X: 5, Y: vpY, Button: tea.MouseLeft})
		m = mod.(*tuiModel)
	}

	questionLine := -1
	for _, rng := range m.view.entryLineRanges {
		if rng.EntryIndex == 3 && !rng.IsRoundHeader {
			questionLine = rng.StartLine
			break
		}
	}
	if questionLine < 0 {
		t.Fatalf("expected standalone question range for EntryIndex 3, got %+v", m.view.entryLineRanges)
	}
	clickLine(questionLine)
	if m.entries[3].ToolExpanded || m.entries[1].RoundExpanded || m.entries[4].RoundExpanded {
		t.Fatal("clicking the standalone question must not expand an execution round")
	}

	clickLine(roundHeaderLine)
	if !m.entries[4].RoundExpanded {
		t.Fatalf("expected entries[4].RoundExpanded to be true after clicking round header")
	}
	expandedRoundView := ansi.Strip(m.viewport.View())
	if !strings.Contains(expandedRoundView, "⌄ Explored 1 file, ran 1 command") ||
		!strings.Contains(expandedRoundView, "│ › Ran git status --short") {
		t.Fatalf("expected expanded round tree with ⌄ header and │ › children, got:\n%s", expandedRoundView)
	}
	if !strings.Contains(expandedRoundView, "Questions 1/1 answered") ||
		strings.Contains(expandedRoundView, "│ › Ask 这次分析希望回答什么问题？") {
		t.Fatalf("expanded execution round must leave the Q&A card outside its tree, got:\n%s", expandedRoundView)
	}
	if !strings.Contains(expandedRoundView, "M internal/tui/view.go") ||
		!strings.Contains(expandedRoundView, "+ 3 lines (click to expand · /detail for full output)") {
		t.Fatalf("expected a three-line child command preview and expand hint, got:\n%s", expandedRoundView)
	}
	if strings.Contains(expandedRoundView, "M internal/tui/view_footer.go") {
		t.Fatalf("expected only the first three command output lines before clicking, got:\n%s", expandedRoundView)
	}
	if !strings.Contains(expandedRoundView, "⌃ Show less") {
		t.Fatalf("expected a bottom Show less row in the expanded round, got:\n%s", expandedRoundView)
	}

	// 4. Level 2 Click: Click on the child step "│ › Ran git status --short" (EntryIndex 5) -> expands its output inline
	var childCmdLine int = -1
	for _, rng := range m.view.entryLineRanges {
		if !rng.IsRoundHeader && rng.EntryIndex == 5 {
			childCmdLine = rng.StartLine
			break
		}
	}
	if childCmdLine < 0 {
		t.Fatalf("expected child range for EntryIndex 5, got %+v", m.view.entryLineRanges)
	}
	clickLine(childCmdLine)
	if !m.entries[5].ToolExpanded {
		t.Fatalf("expected entries[5].ToolExpanded to be true after clicking child step")
	}
	childExpandedView := ansi.Strip(m.viewport.View())
	if !strings.Contains(childExpandedView, "│ ⌄ Ran git status --short") ||
		!strings.Contains(childExpandedView, "M internal/tui/view.go") ||
		!strings.Contains(childExpandedView, "M internal/tui/view_footer.go") ||
		!strings.Contains(childExpandedView, "│   - collapse 6 lines (click)") {
		t.Fatalf("expected full inline command output inside expanded round tree, got:\n%s", childExpandedView)
	}

	// 5. Click the round header (⌄ Explored ...) again -> collapses the entire round back to 1 line
	clickLine(roundHeaderLine)
	if m.entries[4].RoundExpanded {
		t.Fatalf("expected entries[4].RoundExpanded to be false after clicking ⌄ header")
	}
	if !m.entries[5].ToolExpanded {
		t.Fatalf("expected the expanded child step to survive a header collapse")
	}
}

func TestExpandedToolRoundShowLessRow(t *testing.T) {
	m := newTUIModel(nil, nil, "show-less-sess")
	m.width = 100
	m.height = 40
	m.entries = []TranscriptEntry{
		{Kind: KindUser, Text: "run checks"},
		{Kind: KindTool, ToolName: "run_command", ToolCommand: "go build ./...", ToolOutput: "ok"},
		{Kind: KindTool, ToolName: "read_file", ToolCommand: "Read go.mod", ToolOutput: "module github.com/afterlune/lune"},
	}
	m.entries[1].RoundExpanded = true
	m.entries[2].ToolExpanded = true
	m.updateLayout()
	m.refreshViewportContent()

	expandedView := ansi.Strip(m.viewport.View())
	if !strings.Contains(expandedView, "⌃ Show less") {
		t.Fatalf("expected a bottom Show less row in the expanded round, got:\n%s", expandedView)
	}
	if !strings.Contains(expandedView, "│ ⌄ Read go.mod") {
		t.Fatalf("expected the expanded child step to stay expanded, got:\n%s", expandedView)
	}

	footerLine := -1
	for _, rng := range m.view.entryLineRanges {
		if rng.IsRoundFooter && rng.EntryIndex == 1 {
			footerLine = rng.StartLine
			break
		}
	}
	if footerLine < 0 {
		t.Fatalf("expected a round footer range for EntryIndex 1, got %+v", m.view.entryLineRanges)
	}

	vpY := footerLine - m.viewport.YOffset() + 3
	mod, _ := m.Update(tea.MouseClickMsg{X: 5, Y: vpY, Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.MouseReleaseMsg{X: 5, Y: vpY, Button: tea.MouseLeft})
	m = mod.(*tuiModel)

	if m.entries[1].RoundExpanded {
		t.Fatalf("expected entries[1].RoundExpanded to be false after clicking Show less")
	}
	if !m.entries[2].ToolExpanded {
		t.Fatalf("expected a block-level collapse to leave sub-step expansion untouched")
	}
	collapsedView := ansi.Strip(m.viewport.View())
	if strings.Contains(collapsedView, "⌃ Show less") {
		t.Fatalf("expected the Show less row to disappear once collapsed, got:\n%s", collapsedView)
	}
}

func TestCtrlOTogglesAllToolRounds(t *testing.T) {
	m := newTUIModel(nil, nil, "toggle-all-rounds")
	m.width = 100
	m.height = 30
	m.entries = []TranscriptEntry{
		{Kind: KindUser, Text: "first"},
		{Kind: KindTool, ToolName: "run_command", ToolCommand: "go test ./...", ToolOutput: "ok"},
		{Kind: KindTool, ToolName: "read_file", ToolCommand: "Read go.mod", ToolOutput: "module github.com/afterlune/lune"},
		{Kind: KindAssistant, Text: "first done"},
		{Kind: KindUser, Text: "second"},
		{Kind: KindTool, ToolName: "run_command", ToolCommand: "git status --short", ToolOutput: "clean"},
	}
	m.updateLayout()

	starts := m.toolRoundStarts()
	if len(starts) != 2 || starts[0] != 1 || starts[1] != 5 {
		t.Fatalf("expected round starts at 1 and 5, got %v", starts)
	}

	pressCtrlO := func() {
		mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
		m = mod.(*tuiModel)
	}

	// Every round is collapsed -> one press expands all of them.
	pressCtrlO()
	for _, start := range starts {
		if !m.entries[start].RoundExpanded {
			t.Fatalf("expected round %d to expand after Ctrl+O", start)
		}
	}

	// All expanded -> next press collapses all of them.
	pressCtrlO()
	for _, start := range starts {
		if m.entries[start].RoundExpanded {
			t.Fatalf("expected round %d to collapse after second Ctrl+O", start)
		}
	}

	// One expanded round plus one collapsed round -> the press expands everything again.
	m.entries[1].RoundExpanded = true
	m.entries[5].RoundExpanded = false
	pressCtrlO()
	for _, start := range starts {
		if !m.entries[start].RoundExpanded {
			t.Fatalf("expected round %d to expand after Ctrl+O with one round expanded", start)
		}
	}

	// Block-level toggling never rewrites sub-step expansion flags.
	m.entries[2].ToolExpanded = true
	pressCtrlO()
	if !m.entries[2].ToolExpanded {
		t.Fatalf("expected Ctrl+O to leave sub-step expansion untouched")
	}
}

func TestCtrlOCollapsesRunningRound(t *testing.T) {
	m := newTUIModel(nil, nil, "live-toggle")
	m.width = 100
	m.height = 30
	m.run.running = true
	m.run.taskStartedAt = time.Now()
	m.entries = []TranscriptEntry{
		{Kind: KindUser, Text: "sleep for a while"},
		{Kind: KindTool, ToolName: "run_command", ToolCommand: "sleep 5", ToolRunning: true, ToolStartedAt: time.Now()},
	}
	m.updateLayout()

	if !strings.Contains(ansi.Strip(m.viewport.View()), "│ ⠋ Running Ran sleep 5") {
		t.Fatalf("expected the running round expanded by default, got:\n%s", ansi.Strip(m.viewport.View()))
	}

	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if !m.roundsCollapsedByUser {
		t.Fatalf("expected Ctrl+O to collapse the running round")
	}
	collapsed := ansi.Strip(m.viewport.View())
	if strings.Contains(collapsed, "│ ⠋ Running Ran sleep 5") {
		t.Fatalf("expected the running round collapsed, got:\n%s", collapsed)
	}
	if !strings.Contains(collapsed, "Ran 1 command") {
		t.Fatalf("expected the collapsed summary bar to keep the command count, got:\n%s", collapsed)
	}

	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if m.roundsCollapsedByUser {
		t.Fatalf("expected the second Ctrl+O to expand the running round again")
	}
	if !strings.Contains(ansi.Strip(m.viewport.View()), "│ ⠋ Running Ran sleep 5") {
		t.Fatalf("expected the running round expanded again, got:\n%s", ansi.Strip(m.viewport.View()))
	}
}

func TestQueuedTurnsStayPinnedAboveComposer(t *testing.T) {
	m := newTUIModel(nil, nil, "queue-band")
	m.width = 100
	m.height = 30
	m.run.running = true
	m.entries = []TranscriptEntry{
		{Kind: KindUser, Text: "first task"},
		{Kind: KindTool, ToolName: "run_command", ToolCommand: "ls", ToolOutput: "a\nb", ToolRunning: true},
		{Kind: KindQueued, Text: "follow-up task"},
	}
	m.updateLayout()

	if got := ansi.Strip(m.viewport.View()); strings.Contains(got, "Queued ›") {
		t.Fatalf("queued turns must not scroll with the transcript, got:\n%s", got)
	}
	band := ansi.Strip(renderQueuedBand(m, 96))
	if !strings.Contains(band, "Queued › follow-up task") {
		t.Fatalf("expected the queued band to show the waiting task, got %q", band)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "Queued › follow-up task") {
		t.Fatalf("expected the queued band inside the rendered frame")
	}

	// The band takes one row from the viewport so the frame never overflows.
	bandHeight := lipgloss.Height(renderQueuedBand(m, 96))
	withQueue := m.viewport.Height()
	m.entries = m.entries[:2]
	m.updateLayout()
	if bandHeight != 1 || m.viewport.Height() != withQueue+bandHeight {
		t.Fatalf("expected the queued band to reserve %d row(s), viewport %d -> %d", bandHeight, withQueue, m.viewport.Height())
	}
}

func TestRemovedCtrlRAndCtrlTHotkeys(t *testing.T) {
	m := newTUIModel(nil, nil, "removed-hotkeys")
	m.width = 80
	m.height = 24
	m.entries = []TranscriptEntry{
		{Kind: KindTool, ToolName: "run_command", ToolCommand: "ls", ToolOutput: "a\nb\nc\nd"},
		{Kind: KindReasoning, Text: "plan the work"},
	}
	m.updateLayout()
	entryCount := len(m.entries)

	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'r', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if m.entries[1].ReasoningExpanded {
		t.Fatalf("expected Ctrl+R to no longer toggle reasoning")
	}
	if len(m.entries) != entryCount {
		t.Fatalf("expected Ctrl+R to stay silent, got %d entries", len(m.entries))
	}

	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 't', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if m.modals.toolPager.open {
		t.Fatalf("expected Ctrl+T to no longer open the tool output pager")
	}

	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if m.modals.sessions.open {
		t.Fatalf("expected Ctrl+O to no longer open the session switcher")
	}
}

func TestVisualComfortMarkdownAndPlanTypography(t *testing.T) {
	// Wide terminal prose wraps to fit the terminal width naturally without artificial cutoff.
	longProse := strings.Repeat("这是一段非常长的技术方案说明文字，用于测试在超宽两百列终端下自动根据宽度自然排版换行。", 4)
	renderedProse := renderMarkdown(longProse, 220)
	for _, line := range strings.Split(renderedProse, "\n") {
		if w := ansi.StringWidth(line); w > 220 {
			t.Fatalf("expected prose line width <= 220 on 220-col terminal, got %d: %q", w, ansi.Strip(line))
		}
	}

	// KindPlan preserves heading levels and code blocks in the response flow.
	planEntry := TranscriptEntry{
		Kind: KindPlan,
		Text: "# 验证\n\n1. **运行测试**\n\n```powershell\ngofmt -l internal/desktop # 期望无输出\n```",
	}
	rawRenderedPlan := renderEntry(planEntry, 160)
	renderedPlan := ansi.Strip(rawRenderedPlan)
	if !strings.Contains(renderedPlan, "• Proposed Plan") {
		t.Fatalf("expected Proposed Plan header, got:\n%s", renderedPlan)
	}
	cardR, cardG, cardB, ok := parseHexRGB(activeTheme().CardBg)
	if !ok || strings.Contains(rawRenderedPlan, fmt.Sprintf("48;2;%d;%d;%d", cardR, cardG, cardB)) {
		t.Fatalf("expected the plan body to drop the card surface background, got: %q", rawRenderedPlan)
	}
	if !strings.Contains(renderedPlan, "# 验证") {
		t.Fatalf("expected visible plan heading marker, got:\n%s", renderedPlan)
	}
	if !strings.Contains(renderedPlan, "  # 验证") {
		t.Fatalf("plan body should align with assistant prose, got:\n%s", renderedPlan)
	}
	if !strings.Contains(renderedPlan, "gofmt -l internal/desktop # 期望无输出") {
		t.Fatalf("expected code block text in KindPlan, got:\n%s", renderedPlan)
	}
	for _, line := range strings.Split(renderedPlan, "\n") {
		if w := ansi.StringWidth(line); w > 160 {
			t.Fatalf("plan card line exceeds width 160 (%d): %q", w, line)
		}
	}
}

func TestHeaderBrandLune(t *testing.T) {
	rendered := renderHeader("/my/project")
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, ">_ lune") {
		t.Fatalf("expected '>_ lune' in header, got: %q", stripped)
	}
	if strings.Contains(stripped, "v0.1.0") || strings.Contains(stripped, "LuneAgent") {
		t.Fatalf("header must not contain version or old brand: %q", stripped)
	}
	if !strings.Contains(stripped, "/my/project") {
		t.Fatalf("header missing workspace: %q", stripped)
	}
}

func TestAssistantNarrationStyleDiffersFromReasoning(t *testing.T) {
	marker := assistantMarkerStyle()
	if marker.GetForeground() != lipgloss.Color(activeTheme().TextPrimary) || !marker.GetBold() {
		t.Fatalf("narration marker must use the primary text color and a bold weight")
	}
	if marker.GetForeground() == styleTextMuted().GetForeground() {
		t.Fatalf("narration marker must differ from the muted reasoning color")
	}

	narration := renderEntry(TranscriptEntry{Kind: KindAssistant, Text: "我会先检查基础状态。"}, 80)
	if !strings.Contains(narration, marker.Render("• ")) {
		t.Fatalf("expected the primary marker on narration, got:\n%s", narration)
	}
	if got := ansi.Strip(narration); !strings.Contains(got, "我会先检查基础状态。") || strings.Contains(got, "Thought for") {
		t.Fatalf("narration must read as model text, got:\n%s", got)
	}

	reasoning := renderEntry(TranscriptEntry{Kind: KindReasoning, Text: "先看进程与网络", ReasoningDuration: 2 * time.Second}, 80)
	if got := ansi.Strip(reasoning); !strings.Contains(got, "Thought for 2s") {
		t.Fatalf("expected the folded reasoning label, got:\n%s", got)
	}
	if strings.Contains(reasoning, marker.Render("• ")) {
		t.Fatalf("reasoning must not reuse the narration marker, got:\n%s", ansi.Strip(reasoning))
	}
}

// isExploreTool only drives presentation, so pin the current classification: a new
// read-only tool must be added here deliberately instead of drifting silently.
func TestIsExploreToolClassifiesReadOnlyTools(t *testing.T) {
	for _, name := range []string{"read_file", "read_image", "list_dir", "grep", "glob", "find_files", "search_files", " READ_FILE "} {
		if !isExploreTool(name) {
			t.Errorf("%q must render as an exploration tool", name)
		}
	}
	for _, name := range []string{"write_file", "edit_file", "command", "read_url", ""} {
		if isExploreTool(name) {
			t.Errorf("%q must not render as an exploration tool", name)
		}
	}
}
