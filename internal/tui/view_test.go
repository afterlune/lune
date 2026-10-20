package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
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
						{Label: "整体代码审查 (推荐)", Description: "梳理架构并找高优先级问题"},
						{Label: "定位运行故障", Description: "追查具体报错"},
					},
				},
			},
		},
		Selected: 0,
	}

	rendered := renderQuestionPanel(q, 80)
	if !strings.Contains(rendered, "Question 1/1") {
		t.Errorf("expected question counter meta, got: %s", rendered)
	}
	if !strings.Contains(rendered, "这次分析希望回答什么问题？") {
		t.Errorf("expected question title, got: %s", rendered)
	}
	if !strings.Contains(rendered, "整体代码审查 (推荐)") {
		t.Errorf("expected options list, got: %s", rendered)
	}
	if !strings.Contains(rendered, "tab to add notes | enter to submit answer | esc to interrupt") {
		t.Errorf("expected question footer controls, got: %s", rendered)
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
		q.Selected = selected
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
		Selected: 0,
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
	m.transcriptDirty = true
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
		lipgloss.Height(renderPlanReviewPanel(m.activePlanReview, max(20, m.width-4))) + 1 +
		lipgloss.Height(renderComposerBand(m.composer.View(), max(20, m.width-4))) +
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
	m := &tuiModel{
		workspace:      "D:\\test",
		permissionMode: core.PermissionWorkspace,
		composer:       newTUIModel(nil, nil, "test-sess").composer,
		viewport:       newTUIModel(nil, nil, "test-sess").viewport,
	}
	m.width = 100
	m.height = 30
	m.updateLayout()

	// Shift+Tab cycles mode
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	m = mod.(*tuiModel)
	if m.permissionMode != core.PermissionAsk {
		t.Errorf("expected permissionMode to cycle to ask, got %s", m.permissionMode)
	}

	// Autocomplete trigger
	m.composer.SetValue("/pl")
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"}))
	m = mod.(*tuiModel)
	if !m.autocompleteOpen {
		t.Errorf("expected autocomplete to open for /plan")
	}

	// Shift+Enter inserts newline
	m.composer.SetValue("line1")
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModShift}))
	m = mod.(*tuiModel)
	if m.composer.Value() != "line1\n" {
		t.Errorf("expected composer to have newline after Shift+Enter, got: %q", m.composer.Value())
	}

	// Composer band covers at least 3 rows
	renderedBand := renderComposerBand(m.composer.View(), 80)
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
	rendered := renderComposerBand(m.composer.View(), 76)
	lines := strings.Split(rendered, "\n")
	for i, l := range lines {
		t.Logf("Line %d: %q", i, l)
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
	if !m.thinking {
		t.Errorf("expected m.thinking to be true during reasoning_delta")
	}
	if m.currentStreamText != "" {
		t.Errorf("expected currentStreamText to be empty, got: %q", m.currentStreamText)
	}
	if m.currentReasoningText != "Internal model thoughts..." {
		t.Errorf("expected currentReasoningText to be 'Internal model thoughts...', got: %q", m.currentReasoningText)
	}

	// 2. Text delta should commit reasoning to entries as KindReasoning, append to currentStreamText, and reset thinking
	m.handleRuntimeEvent(core.Event{
		Kind: core.EventModelStream,
		Stream: &core.ModelStreamEvent{
			Type:  "text_delta",
			Delta: "Hello user",
		},
	})
	if m.thinking {
		t.Errorf("expected m.thinking to be false after text_delta")
	}
	if m.currentStreamText != "Hello user" {
		t.Errorf("expected currentStreamText to be 'Hello user', got: %q", m.currentStreamText)
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

	// 1. Collapsed rendering: contains Thought for 12s and (ctrl+r to expand)
	collapsed := renderReasoningEntry(m.entries[0], 80)
	if !strings.Contains(collapsed, "Thought for 12s") || !strings.Contains(collapsed, "(ctrl+r to expand)") {
		t.Errorf("Unexpected collapsed rendering: %s", collapsed)
	}
	if strings.Contains(collapsed, "Step 1: check files") {
		t.Errorf("Collapsed rendering should not contain body text: %s", collapsed)
	}

	// 2. Toggle via ctrl+r
	_, _ = m.toggleReasoningExpansion()
	if !m.entries[0].ReasoningExpanded {
		t.Errorf("Expected ReasoningExpanded to be true after toggle")
	}

	// 3. Expanded rendering: contains body text and (ctrl+r to collapse)
	expanded := renderReasoningEntry(m.entries[0], 80)
	if !strings.Contains(expanded, "(ctrl+r to collapse)") {
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
	m := &tuiModel{
		viewport: newTUIModel(nil, nil, "test-sess").viewport,
	}
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
		Text: "Switched to **Plan mode** (permissions locked to Read-only).",
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
	m.clipboardWriter = func(string) error { return nil }
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
	if !m.isSelecting {
		t.Fatalf("expected isSelecting to be true")
	}

	// Drag to (13, 3) -> "Hello"
	mod, _ = m.Update(tea.MouseMotionMsg{
		X: 13,
		Y: 3,
	})
	m = mod.(*tuiModel)
	if m.selectEndX != 13 || m.selectEndY != 3 {
		t.Fatalf("expected selectEnd to be (13, 3), got (%d, %d)", m.selectEndX, m.selectEndY)
	}

	// Release mouse
	mod, _ = m.Update(tea.MouseReleaseMsg{
		X:      13,
		Y:      3,
		Button: tea.MouseLeft,
	})
	m = mod.(*tuiModel)
	if m.isSelecting {
		t.Fatalf("expected isSelecting to be false after release")
	}
	if m.selectedText != "Hello" {
		t.Fatalf("expected selected text to be 'Hello', got %q", m.selectedText)
	}
	if !strings.Contains(m.copyFeedback, "Copied 5 chars to host clipboard") {
		t.Fatalf("expected copyFeedback with 5 chars, got %q", m.copyFeedback)
	}

	// Esc clears selection
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.selectedText != "" {
		t.Fatalf("expected selectedText to be cleared on Esc, got %q", m.selectedText)
	}
}

func TestComposerMouseDragSelectionAndCopy(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.clipboardWriter = func(string) error { return nil }
	m.width = 80
	m.height = 24
	m.composer.SetValue("copy me")
	m.updateLayout()

	x, y := m.composerMouseOffset()
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	if y >= len(lines) || !strings.HasPrefix(lines[y], " "+ansi.Strip(m.composer.View())) {
		t.Fatalf("composer mouse offset (%d, %d) does not point to rendered composer", x, y)
	}

	textStart := x + lipgloss.Width(m.composer.Prompt)
	mod, _ := m.Update(tea.MouseClickMsg{X: textStart, Y: y, Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	if !m.composerMouseSelecting {
		t.Fatal("expected a composer selection to start")
	}

	mod, _ = m.Update(tea.MouseMotionMsg{X: textStart + 4, Y: y})
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.MouseReleaseMsg{X: textStart + 4, Y: y, Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	if m.composerMouseSelecting {
		t.Fatal("expected composer selection to end on mouse release")
	}
	if got := m.composer.SelectedText(); got != "copy" {
		t.Fatalf("expected composer selection %q, got %q", "copy", got)
	}

	mod, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = mod.(*tuiModel)
	if cmd == nil {
		t.Fatal("expected Ctrl+C to schedule copy feedback")
	}
	if got := m.copyFeedback; got != "Copied 4 chars to host clipboard" {
		t.Fatalf("expected copy feedback for composer selection, got %q", got)
	}

	mod, _ = m.Update(tea.MouseClickMsg{X: 10, Y: 3, Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	if m.composer.HasSelection() {
		t.Fatal("expected clicking outside the composer to clear its selection")
	}
}

func TestComposerMouseSelectionAcrossLinesAndSoftWrap(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.width = 80
	m.height = 24
	m.composer.SetValue("第一行\nsecond line")
	m.updateLayout()

	x, y := m.composerMouseOffset()
	textStart := x + lipgloss.Width(m.composer.Prompt)
	mod, _ := m.Update(tea.MouseClickMsg{X: textStart, Y: y, Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.MouseMotionMsg{X: textStart + 3, Y: y + 1})
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.MouseReleaseMsg{X: textStart + 3, Y: y + 1, Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	if got := m.composer.SelectedText(); got != "第一行\nsec" {
		t.Fatalf("expected multiline selection %q, got %q", "第一行\nsec", got)
	}

	m = newTUIModel(nil, nil, "test-sess")
	m.width = 24 // The composer is 20 columns wide, forcing this line to wrap.
	m.height = 24
	m.composer.SetValue("abcdefghijklmnopqrst")
	m.updateLayout()
	x, y = m.composerMouseOffset()
	textStart = x + lipgloss.Width(m.composer.Prompt)
	mod, _ = m.Update(tea.MouseClickMsg{X: textStart + 16, Y: y, Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	if !m.composerMouseSelecting {
		t.Fatal("expected soft-wrap composer selection to start")
	}
	mod, _ = m.Update(tea.MouseMotionMsg{X: textStart + 2, Y: y + 1})
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.MouseReleaseMsg{X: textStart + 2, Y: y + 1, Button: tea.MouseLeft})
	m = mod.(*tuiModel)
	if got := m.composer.SelectedText(); got != "qrst" {
		t.Fatalf("expected selection across soft wrap %q, got %q", "qrst", got)
	}
}

func TestComposerKeyboardSelectionAndCtrlCPrecedence(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.clipboardWriter = func(string) error { return nil }
	m.composer.SetValue("hello")
	m.composer.CursorStart()
	for range 2 {
		mod, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
		m = mod.(*tuiModel)
	}
	if got := m.composer.SelectedText(); got != "he" {
		t.Fatalf("expected keyboard selection %q, got %q", "he", got)
	}

	// The composer is the active copy target when both selection stores happen
	// to contain text.
	m.selectedText = "transcript selection"
	mod, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	m = mod.(*tuiModel)
	if cmd == nil || m.copyFeedback != "Copied 2 chars to host clipboard" {
		t.Fatalf("Ctrl+C did not copy the composer selection: cmd=%v feedback=%q", cmd != nil, m.copyFeedback)
	}

	m.composer.ClearSelection()
	m.composer.CursorStart()
	mod, _ = m.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = mod.(*tuiModel)
	if got := m.composer.SelectedText(); got != "hello" {
		t.Fatalf("expected Ctrl+G to select all input, got %q", got)
	}
	mod, cmd = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl | tea.ModShift})
	m = mod.(*tuiModel)
	if cmd == nil || m.copyFeedback != "Copied 5 chars to host clipboard" {
		t.Fatalf("Ctrl+Shift+C did not use the safe composer copy path: cmd=%v feedback=%q", cmd != nil, m.copyFeedback)
	}
	mod, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = mod.(*tuiModel)
	if m.composer.HasSelection() || m.composer.Value() != "hello" {
		t.Fatalf("Esc should clear the input selection without changing its content: value=%q selected=%t", m.composer.Value(), m.composer.HasSelection())
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
	if !m.shortcutsOpen {
		t.Fatalf("expected shortcutsOpen to be true after typing '?'")
	}

	// 2. Press '?' again closes shortcuts
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '?', Text: "?"}))
	m = mod.(*tuiModel)
	if m.shortcutsOpen {
		t.Fatalf("expected shortcutsOpen to be false after typing '?' again")
	}

	// 3. /shortcuts slash command opens shortcuts
	m.composer.SetValue("/shortcuts")
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)
	if !m.shortcutsOpen {
		t.Fatalf("expected shortcutsOpen to be true after /shortcuts command")
	}

	// 4. Esc closes shortcuts
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.shortcutsOpen {
		t.Fatalf("expected shortcutsOpen to be false after Esc")
	}

	// 5. Typing '?' when composer has text does NOT open shortcuts
	m.composer.SetValue("Is this a test?")
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '?', Text: "?"}))
	m = mod.(*tuiModel)
	if m.shortcutsOpen {
		t.Fatalf("expected shortcutsOpen to remain false when typing '?' with existing text")
	}
}

func TestPromptHistoryNavigationAndEscSemantics(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.promptHistory = []string{"First prompt", "Second prompt", "Third prompt"}
	m.historyIndex = len(m.promptHistory)

	// User types an unsent draft
	m.composer.SetValue("My draft")

	// Up key saves draft and loads last history prompt ("Third prompt")
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mod.(*tuiModel)
	if m.composer.Value() != "Third prompt" {
		t.Fatalf("expected 'Third prompt', got %q", m.composer.Value())
	}
	if m.historyDraft != "My draft" {
		t.Fatalf("expected historyDraft to be 'My draft', got %q", m.historyDraft)
	}

	// Up key again loads "Second prompt"
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mod.(*tuiModel)
	if m.composer.Value() != "Second prompt" {
		t.Fatalf("expected 'Second prompt', got %q", m.composer.Value())
	}

	// Up key again loads "First prompt"
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mod.(*tuiModel)
	if m.composer.Value() != "First prompt" {
		t.Fatalf("expected 'First prompt', got %q", m.composer.Value())
	}

	// Down key moves forward to "Second prompt"
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mod.(*tuiModel)
	if m.composer.Value() != "Second prompt" {
		t.Fatalf("expected 'Second prompt', got %q", m.composer.Value())
	}

	// Down key moves forward to "Third prompt"
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mod.(*tuiModel)
	if m.composer.Value() != "Third prompt" {
		t.Fatalf("expected 'Third prompt', got %q", m.composer.Value())
	}

	// Down key restores original draft "My draft"
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mod.(*tuiModel)
	if m.composer.Value() != "My draft" {
		t.Fatalf("expected restored draft 'My draft', got %q", m.composer.Value())
	}

	// Esc does NOT clear composer or restore history; draft remains intact
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.composer.Value() != "My draft" {
		t.Fatalf("expected Esc to leave composer draft intact, got %q", m.composer.Value())
	}

	// Esc when composer is empty steps back to edit last message ("Third prompt")
	m.composer.SetValue("")
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.composer.Value() != "Third prompt" {
		t.Fatalf("expected Esc on empty composer to restore 'Third prompt', got %q", m.composer.Value())
	}

	// Esc when text is present leaves composer text intact
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.composer.Value() != "Third prompt" {
		t.Fatalf("expected Esc on non-empty composer to leave text intact, got %q", m.composer.Value())
	}

	// Up key navigates to previous message ("Second prompt")
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mod.(*tuiModel)
	if m.composer.Value() != "Second prompt" {
		t.Fatalf("expected Up to load 'Second prompt', got %q", m.composer.Value())
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
	if !strings.Contains(renderedCollapsed, "+ 3 lines (ctrl+t to expand)") {
		t.Errorf("expected expand hint, got: %s", renderedCollapsed)
	}

	toolEntry.ToolExpanded = true
	renderedExpanded := renderEntry(toolEntry, 80)
	if !strings.Contains(renderedExpanded, "collapse 6 lines (ctrl+t)") {
		t.Errorf("expected collapse hint, got: %s", renderedExpanded)
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
	m.running = true
	m.taskStartedAt = time.Now().Add(-5 * time.Second)
	m.refreshViewportContent()

	if !m.viewport.AtBottom() {
		t.Errorf("expected viewport to remain at bottom when running in turn 2, atBottom=%v, yoffset=%d", m.viewport.AtBottom(), m.viewport.YOffset())
	}

	// Verify working indicator is rendered in the viewport visible lines
	viewText := m.viewport.View()
	if !strings.Contains(viewText, "Working") {
		t.Errorf("expected working indicator to be visible in viewport view, got:\n%s", viewText)
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
		m.composer.SetWidth(40)
		rendered := renderComposerBand(m.composer.View(), 40)
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
	rendered := RenderMarkdown(md, 80)
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
	m.height = 30
	m.updateLayout()

	// 1. Simulate an active (live) tool round while m.running == true and currentStreamText == ""
	m.running = true
	m.taskStartedAt = time.Now().Add(-46 * time.Second)
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
			ToolOutput:  " M internal/tui/view.go\n M internal/tui/model.go",
		},
	}

	m.invalidateEntryCache()
	m.refreshViewportContent()
	liveView := ansi.Strip(m.viewport.View())

	// While live, the round should be expanded with ⠋ header and │ › child steps
	if !strings.Contains(liveView, "Explored 1 directory, 1 file, ran 1 command") {
		t.Fatalf("expected live round summary header, got:\n%s", liveView)
	}
	if !strings.Contains(liveView, "│ › Thought for 1.6s") ||
		!strings.Contains(liveView, "│ › Listed D:\\Git\\luneAgent") ||
		!strings.Contains(liveView, "│ › Ask 这次分析希望回答什么问题？") ||
		!strings.Contains(liveView, "│ › Read go.mod") ||
		!strings.Contains(liveView, "│ › Ran git status --short") {
		t.Fatalf("expected live round child tree lines, got:\n%s", liveView)
	}

	// 2. Complete the turn via finalizeTurnOutput -> round auto-collapses into 1-line summary bar + Worked for 46s
	m.currentStreamText = "这是一个多形态 AI 编程 Agent 宿主仓库。"
	m.finalizeTurnOutput("", nil, "exec-grok-1")
	m.running = false
	m.refreshViewportContent()

	collapsedView := ansi.Strip(m.viewport.View())
	if !strings.Contains(collapsedView, "› Explored 1 directory, 1 file, ran 1 command") {
		t.Fatalf("expected collapsed Grok summary bar, got:\n%s", collapsedView)
	}
	if strings.Contains(collapsedView, "│ › Read go.mod") {
		t.Fatalf("expected child steps to be hidden when round is auto-collapsed, got:\n%s", collapsedView)
	}
	if !strings.Contains(collapsedView, "Worked for 46s") {
		t.Fatalf("expected 'Worked for 46s' on completed assistant turn, got:\n%s", collapsedView)
	}

	// 3. Level 1 Click: Click on the collapsed round header bar -> expands the round tree (⌄ Explored ...)
	var roundHeaderLine int = -1
	for _, rng := range m.entryLineRanges {
		if rng.IsRoundHeader && rng.EntryIndex == 1 {
			roundHeaderLine = rng.StartLine
			break
		}
	}
	if roundHeaderLine < 0 {
		t.Fatalf("expected round header range for EntryIndex 1, got %+v", m.entryLineRanges)
	}

	clickLine := func(absLine int) {
		vpY := absLine - m.viewport.YOffset() + 3 // headerHeight == 3
		mod, _ := m.Update(tea.MouseClickMsg{X: 5, Y: vpY, Button: tea.MouseLeft})
		m = mod.(*tuiModel)
		mod, _ = m.Update(tea.MouseReleaseMsg{X: 5, Y: vpY, Button: tea.MouseLeft})
		m = mod.(*tuiModel)
	}

	clickLine(roundHeaderLine)
	if !m.entries[1].RoundExpanded {
		t.Fatalf("expected entries[1].RoundExpanded to be true after clicking round header")
	}
	expandedRoundView := ansi.Strip(m.viewport.View())
	if !strings.Contains(expandedRoundView, "⌄ Explored 1 directory, 1 file, ran 1 command") ||
		!strings.Contains(expandedRoundView, "│ › Ran git status --short") {
		t.Fatalf("expected expanded round tree with ⌄ header and │ › children, got:\n%s", expandedRoundView)
	}
	if strings.Contains(expandedRoundView, "M internal/tui/view.go") {
		t.Fatalf("expected child command output to remain collapsed until child item is clicked, got:\n%s", expandedRoundView)
	}

	// 4. Level 2 Click: Click on the child step "│ › Ran git status --short" (EntryIndex 5) -> expands its output inline
	var childCmdLine int = -1
	for _, rng := range m.entryLineRanges {
		if !rng.IsRoundHeader && rng.EntryIndex == 5 {
			childCmdLine = rng.StartLine
			break
		}
	}
	if childCmdLine < 0 {
		t.Fatalf("expected child range for EntryIndex 5, got %+v", m.entryLineRanges)
	}
	clickLine(childCmdLine)
	if !m.entries[5].ToolExpanded {
		t.Fatalf("expected entries[5].ToolExpanded to be true after clicking child step")
	}
	childExpandedView := ansi.Strip(m.viewport.View())
	if !strings.Contains(childExpandedView, "│ ⌄ Ran git status --short") ||
		!strings.Contains(childExpandedView, "M internal/tui/view.go") {
		t.Fatalf("expected inline command output inside expanded round tree, got:\n%s", childExpandedView)
	}

	// 5. Click the round header (⌄ Explored ...) again -> collapses the entire round back to 1 line
	clickLine(roundHeaderLine)
	if m.entries[1].RoundExpanded {
		t.Fatalf("expected entries[1].RoundExpanded to be false after clicking ⌄ header")
	}
}

func TestVisualComfortMarkdownAndPlanTypography(t *testing.T) {
	// 1. splitCodeComment separates trailing # and // comments while respecting quotes
	code, comment := splitCodeComment("gofmt -l internal/desktop # 期望无输出")
	if code != "gofmt -l internal/desktop " || comment != "# 期望无输出" {
		t.Fatalf("unexpected splitCodeComment result: code=%q, comment=%q", code, comment)
	}
	quotedCode, quotedComment := splitCodeComment(`git commit -m "fix #123 issue" # trailing comment`)
	if quotedCode != `git commit -m "fix #123 issue" ` || quotedComment != "# trailing comment" {
		t.Fatalf("expected # inside quotes to be preserved, got code=%q, comment=%q", quotedCode, quotedComment)
	}

	// 2. Wide terminal prose wraps to fit the terminal width naturally without artificial cutoff
	longProse := strings.Repeat("这是一段非常长的技术方案说明文字，用于测试在超宽两百列终端下自动根据宽度自然排版换行。", 4)
	renderedProse := RenderMarkdown(longProse, 220)
	for _, line := range strings.Split(renderedProse, "\n") {
		if w := ansi.StringWidth(line); w > 220 {
			t.Fatalf("expected prose line width <= 220 on 220-col terminal, got %d: %q", w, ansi.Strip(line))
		}
	}

	// 3. KindPlan renders with single left rail │ and clean code block indent (no double │ │ rail)
	planEntry := TranscriptEntry{
		Kind: KindPlan,
		Text: "5. **验证**\n```powershell\ngofmt -l internal/desktop # 期望无输出\n```",
	}
	renderedPlan := ansi.Strip(renderEntry(planEntry, 160))
	if !strings.Contains(renderedPlan, "• Proposed Plan") {
		t.Fatalf("expected Proposed Plan header, got:\n%s", renderedPlan)
	}
	if strings.Contains(renderedPlan, "│ │") {
		t.Fatalf("expected no double rail '│ │' in KindPlan code block, got:\n%s", renderedPlan)
	}
	if !strings.Contains(renderedPlan, "gofmt -l internal/desktop # 期望无输出") {
		t.Fatalf("expected code block text in KindPlan, got:\n%s", renderedPlan)
	}
}

func TestInlineMarkdownTokenizationSafety(t *testing.T) {
	// Verify that bold file names like **TUI.md** do not corrupt ANSI escape codes (no '218mTUI.md')
	input := "请检查 **TUI.md** 以及 `go test -v ./...` 的输出。"
	formatted := formatInline(input)
	stripped := ansi.Strip(formatted)
	if strings.Contains(stripped, "218m") {
		t.Fatalf("corrupted ANSI sequence leaked into text: %q", stripped)
	}
	if !strings.Contains(stripped, "TUI.md") {
		t.Fatalf("expected stripped output to contain TUI.md, got %q", stripped)
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
