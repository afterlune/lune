package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
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
	if !strings.Contains(stripped, "LuneAgent") {
		t.Errorf("expected LuneAgent in header, got: %s", stripped)
	}
	if !strings.Contains(stripped, "(v0.1.0)") {
		t.Errorf("expected version (v0.1.0) in header, got: %s", stripped)
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

	// 1. Reasoning delta should set thinking=true and NOT add to currentStreamText
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

	// 2. Text delta should append to currentStreamText and reset thinking
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

	// 3. select_tool_category should be ignored and not add to entries
	m.handleRuntimeEvent(core.Event{
		Kind: core.EventToolStarted,
		Tool: core.ToolCall{
			Name: "select_tool_category",
		},
	})
	if len(m.entries) != 0 {
		t.Errorf("expected select_tool_category to be ignored, got %d entries", len(m.entries))
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

func TestShortcutsPanelRendering(t *testing.T) {
	renderedWide := renderShortcutsPanel(100)
	strippedWide := ansi.Strip(renderedWide)
	if !strings.Contains(strippedWide, "Keyboard Shortcuts") {
		t.Errorf("expected title 'Keyboard Shortcuts', got: %s", strippedWide)
	}
	if !strings.Contains(strippedWide, "Compose") || !strings.Contains(strippedWide, "Session") || !strings.Contains(strippedWide, "Transcript") {
		t.Errorf("expected 3 column titles, got: %s", strippedWide)
	}
	if !strings.Contains(strippedWide, "Shift+Enter") || !strings.Contains(strippedWide, "Shift+Tab") || !strings.Contains(strippedWide, "Mouse Drag") {
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

func TestBatchedQuestionNavigationAndResolution(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.width = 100
	m.height = 30

	m.activeQuestion = &ActiveQuestion{
		Request: core.UserQuestionRequest{
			ID: "req-batch-1",
			Questions: []core.UserQuestionItem{
				{
					ID:       "q1",
					Question: "First Question?",
					Options: []core.UserQuestionOption{
						{Label: "Choice 1A", Description: "Desc 1A"},
						{Label: "Choice 1B", Description: "Desc 1B"},
					},
				},
				{
					ID:       "q2",
					Question: "Second Question?",
					Options: []core.UserQuestionOption{
						{Label: "Choice 2A", Description: "Desc 2A"},
						{Label: "Choice 2B", Description: "Desc 2B"},
					},
				},
				{
					ID:       "q3",
					Question: "Third Question?",
					Options: []core.UserQuestionOption{
						{Label: "Choice 3A", Description: "Desc 3A"},
					},
				},
			},
		},
		Selected: 0,
	}

	// 1. Initial view: Question 1/3 (3 unanswered)
	rendered := renderQuestionPanel(m.activeQuestion, 100)
	if !strings.Contains(rendered, "Question 1/3 (3 unanswered)") {
		t.Fatalf("expected 'Question 1/3 (3 unanswered)', got: %s", rendered)
	}
	if !strings.Contains(rendered, "First Question?") {
		t.Fatalf("expected 'First Question?', got: %s", rendered)
	}

	// Down arrow to select Choice 1B
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mod.(*tuiModel)
	if m.activeQuestion.Selected != 1 {
		t.Fatalf("expected Selected=1 for Choice 1B, got %d", m.activeQuestion.Selected)
	}

	// Press Enter to submit answer for question 1
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)

	// Question 1 should be recorded in transcript entries
	if len(m.entries) != 1 || m.entries[0].Kind != KindQuestionAnswered || m.entries[0].AnswerText != "Choice 1B" {
		t.Fatalf("expected Question 1 answered with Choice 1B, got %+v", m.entries)
	}

	// Active question should now be at Question 2/3 (2 unanswered)
	if m.activeQuestion == nil || m.activeQuestion.CurrentIndex != 1 {
		t.Fatalf("expected ActiveQuestion at CurrentIndex=1, got %+v", m.activeQuestion)
	}
	rendered2 := renderQuestionPanel(m.activeQuestion, 100)
	if !strings.Contains(rendered2, "Question 2/3 (2 unanswered)") {
		t.Fatalf("expected 'Question 2/3 (2 unanswered)', got: %s", rendered2)
	}
	if !strings.Contains(rendered2, "Second Question?") {
		t.Fatalf("expected 'Second Question?', got: %s", rendered2)
	}

	// Press Enter to submit choice 2A for question 2
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)
	if len(m.entries) != 2 || m.entries[1].AnswerText != "Choice 2A" {
		t.Fatalf("expected Question 2 answered with Choice 2A, got %+v", m.entries)
	}

	// Active question should now be at Question 3/3 (1 unanswered)
	if m.activeQuestion == nil || m.activeQuestion.CurrentIndex != 2 {
		t.Fatalf("expected ActiveQuestion at CurrentIndex=2, got %+v", m.activeQuestion)
	}
	rendered3 := renderQuestionPanel(m.activeQuestion, 100)
	if !strings.Contains(rendered3, "Question 3/3 (1 unanswered)") {
		t.Fatalf("expected 'Question 3/3 (1 unanswered)', got: %s", rendered3)
	}

	// Press Enter to submit choice 3A for question 3
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)

	// All 3 answered, activeQuestion is closed, task marked running to await Core response
	if m.activeQuestion != nil {
		t.Fatalf("expected activeQuestion to be nil after all questions answered")
	}
	if len(m.entries) != 3 {
		t.Fatalf("expected 3 entries in transcript, got %d", len(m.entries))
	}
}

func TestTurnFinishedPausedClearsRunningState(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.running = true
	m.thinking = true

	mod, _ := m.Update(turnFinishedMsg{
		sessionID: "test-sess",
		result: core.TurnResult{
			Status: core.TaskPaused,
		},
	})
	m = mod.(*tuiModel)

	if m.running {
		t.Errorf("expected m.running to be false when turn is paused")
	}
	if m.thinking {
		t.Errorf("expected m.thinking to be false when turn is paused")
	}
}

func TestEscCancelsActiveQuestion(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.activeQuestion = &ActiveQuestion{
		Request: core.UserQuestionRequest{
			ID: "req-1",
			Questions: []core.UserQuestionItem{
				{ID: "q1", Question: "Test?"},
			},
		},
	}

	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)

	if m.activeQuestion != nil {
		t.Errorf("expected activeQuestion to be nil after Esc")
	}
	foundCancelled := false
	for _, entry := range m.entries {
		if strings.Contains(entry.Text, "Question cancelled") {
			foundCancelled = true
			break
		}
	}
	if !foundCancelled {
		t.Errorf("expected 'Question cancelled' system message in entries, got %+v", m.entries)
	}
}

func TestClearCommand(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.entries = []TranscriptEntry{
		{Kind: KindUser, Text: "User msg 1"},
		{Kind: KindAssistant, Text: "Assistant answer 1"},
	}
	// Active interactive modal should NOT be wiped by /clear
	m.activeQuestion = &ActiveQuestion{
		Request: core.UserQuestionRequest{ID: "q-preserve"},
	}

	m.handleSlashCommand("/clear")

	if len(m.entries) != 0 {
		t.Errorf("expected entries to be cleared, got %d", len(m.entries))
	}
	if strings.TrimSpace(m.viewport.View()) != "" {
		t.Errorf("expected viewport to be empty, got: %q", m.viewport.View())
	}
	if m.activeQuestion == nil || m.activeQuestion.Request.ID != "q-preserve" {
		t.Errorf("expected activeQuestion modal to be preserved across /clear")
	}
}

func TestThemeSwitching(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")

	// /theme dark
	m.handleSlashCommand("/theme dark")
	if CurrentThemeMode() != "dark" {
		t.Errorf("expected dark theme mode, got: %s", CurrentThemeMode())
	}

	// /theme light
	m.handleSlashCommand("/theme light")
	if CurrentThemeMode() != "light" {
		t.Errorf("expected light theme mode, got: %s", CurrentThemeMode())
	}

	// /theme auto
	m.handleSlashCommand("/theme auto")
	if CurrentThemeMode() != "auto" {
		t.Errorf("expected auto theme mode, got: %s", CurrentThemeMode())
	}

	// /theme status inspect
	m.handleSlashCommand("/theme")
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Current theme mode: `auto`") {
		t.Errorf("expected theme inspect message, got: %s", lastSys)
	}
}

func TestPasteMsgSafety(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	pasted := "Line 1 of multiline text\nLine 2 of multiline text\nLine 3"

	mod, _ := m.Update(tea.PasteMsg{Content: pasted})
	m = mod.(*tuiModel)

	if m.running {
		t.Errorf("pasting multiline text must NOT start task execution automatically")
	}
	if m.composer.Value() != pasted {
		t.Errorf("expected composer to contain pasted text, got: %q", m.composer.Value())
	}
}

func TestActiveRecoveryModalNavigationAndEsc(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.activeRecovery = &ActiveRecovery{
		Reason:   "rate_limit (status=429, attempts=3)",
		Selected: 0,
	}

	// Render panel
	rendered := renderRecoveryPanel(m.activeRecovery, 80)
	if !strings.Contains(rendered, "Model Recovery Required") {
		t.Errorf("expected title in recovery panel, got: %s", rendered)
	}
	if !strings.Contains(rendered, "rate_limit") {
		t.Errorf("expected reason in recovery panel, got: %s", rendered)
	}
	if !strings.Contains(rendered, "1. Retry model request") {
		t.Errorf("expected retry option in recovery panel, got: %s", rendered)
	}

	// Down arrow moves selection to discard
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mod.(*tuiModel)
	if m.activeRecovery == nil || m.activeRecovery.Selected != 1 {
		t.Errorf("expected selection 1 after down arrow, got %+v", m.activeRecovery)
	}

	// Up arrow moves selection back to retry
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mod.(*tuiModel)
	if m.activeRecovery == nil || m.activeRecovery.Selected != 0 {
		t.Errorf("expected selection 0 after up arrow, got %+v", m.activeRecovery)
	}

	// Esc discards recovery
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.activeRecovery != nil {
		t.Errorf("expected activeRecovery to be nil after Esc")
	}
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Model recovery discarded") {
		t.Errorf("expected 'Model recovery discarded' system entry, got: %s", lastSys)
	}
}

func TestStatusTableUsageFormatting(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.contextWindow = 128000
	m.estimatedInputTokens = 4200
	m.totalInputTokens = 12450
	m.cachedTokens = 8192
	m.totalOutputTokens = 3210
	m.reasoningTokens = 512

	table := m.renderStatusTable()
	if !strings.Contains(table, "Context Window: 128,000 tokens") {
		t.Errorf("expected formatted context window, got: %s", table)
	}
	if !strings.Contains(table, "est. input: 4,200") {
		t.Errorf("expected formatted estimated input, got: %s", table)
	}
	if !strings.Contains(table, "Total Tokens:   in: 12,450 (cached: 8,192)") {
		t.Errorf("expected formatted token totals, got: %s", table)
	}
	if !strings.Contains(table, "out: 3,210 (reasoning: 512)") {
		t.Errorf("expected formatted output & reasoning tokens, got: %s", table)
	}
}

func TestRewindAndOpenRunningGuards(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.running = true

	// /rewind while running
	m.handleSlashCommand("/rewind")
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Cannot rewind while a task is running") {
		t.Errorf("expected rewind guard error, got: %s", lastSys)
	}

	// /open while running
	m.handleSlashCommand("/open other-sess")
	lastSys2 := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys2, "Cannot switch sessions while a task is running") {
		t.Errorf("expected open guard error, got: %s", lastSys2)
	}

	// /new while running
	m.handleSlashCommand("/new")
	lastSys3 := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys3, "Cannot start a new session while a task is running") {
		t.Errorf("expected new session guard error, got: %s", lastSys3)
	}
}

func TestQueuedTurnWorkflowAndCancellation(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.running = true
	m.runningExecution = "exec-active"

	// 1. Submit turn while running -> should enter KindQueued
	m.submitTurn("Follow-up task while model works")
	if len(m.entries) == 0 {
		t.Fatalf("expected queued entry in transcript")
	}
	lastEntry := m.entries[len(m.entries)-1]
	if lastEntry.Kind != KindQueued || !strings.Contains(lastEntry.Text, "Follow-up task") {
		t.Errorf("expected KindQueued entry, got %+v", lastEntry)
	}
	rendered := renderEntry(lastEntry, 80)
	if !strings.Contains(rendered, "Queued ›") || !strings.Contains(rendered, "Follow-up task") {
		t.Errorf("expected queued badge in rendered view, got: %s", rendered)
	}

	// 2. Simulate Core responding with SubmissionQueued
	mod, _ := m.Update(turnSubmittedMsg{
		sessionID: "test-sess",
		submission: core.TurnSubmission{
			Status:      core.SubmissionQueued,
			ExecutionID: "exec-queued-1",
			QueueID:     "q-1",
		},
	})
	m = mod.(*tuiModel)
	if len(m.queuedTurns) != 1 || m.queuedTurns[0].ExecutionID != "exec-queued-1" {
		t.Fatalf("expected queued turn registered in m.queuedTurns, got %+v", m.queuedTurns)
	}

	// 3. Simulate Core promoting queued turn via EventTaskStarted
	mod, _ = m.handleRuntimeEvent(core.Event{
		Kind:        core.EventTaskStarted,
		ExecutionID: "exec-queued-1",
	})
	m = mod.(*tuiModel)
	if len(m.queuedTurns) != 0 {
		t.Errorf("expected queued turns to be empty after task started, got %d", len(m.queuedTurns))
	}
	if m.runningExecution != "exec-queued-1" {
		t.Errorf("expected runningExecution to be exec-queued-1, got %s", m.runningExecution)
	}
	// Check the entry was converted to KindUser
	promoted := false
	for _, e := range m.entries {
		if e.ToolCallID == "exec-queued-1" && e.Kind == KindUser {
			promoted = true
			break
		}
	}
	if !promoted {
		t.Errorf("expected queued entry to be promoted to KindUser")
	}

	// 4. Test cancel queued turn
	m.queuedTurns = append(m.queuedTurns, QueuedItem{ExecutionID: "exec-q2", Text: "Cancel me"})
	m.entries = append(m.entries, TranscriptEntry{Kind: KindQueued, ToolCallID: "exec-q2", Text: "Cancel me"})
	m.cancelQueuedTurn()
	if len(m.queuedTurns) != 0 {
		t.Errorf("expected queued turns to be empty after cancellation")
	}
	cancelledFound := false
	for _, e := range m.entries {
		if e.ToolCallID == "exec-q2" && strings.Contains(e.Text, "cancelled") {
			cancelledFound = true
			break
		}
	}
	if !cancelledFound {
		t.Errorf("expected entry to be marked as cancelled")
	}
}

func TestDiffRendering(t *testing.T) {
	diffContent := `diff --git a/main.go b/main.go
index 1234567..89abcdef 100644
--- a/main.go
+++ b/main.go
@@ -10,3 +10,4 @@ func main() {
-	fmt.Println("old")
+	fmt.Println("new line")
+	fmt.Println("added line")
 }`
	entry := TranscriptEntry{
		Kind: KindDiff,
		Text: diffContent,
	}
	rendered := renderEntry(entry, 80)
	if !strings.Contains(rendered, "Workspace Diff") {
		t.Errorf("expected 'Workspace Diff' title, got: %s", rendered)
	}
	if !strings.Contains(rendered, "diff --git a/main.go b/main.go") {
		t.Errorf("expected file header in rendered diff, got: %s", rendered)
	}
	if !strings.Contains(rendered, "+") || !strings.Contains(rendered, "added line") {
		t.Errorf("expected addition line in rendered diff, got: %s", rendered)
	}
	if !strings.Contains(rendered, "-") || !strings.Contains(rendered, "old") {
		t.Errorf("expected deletion line in rendered diff, got: %s", rendered)
	}
}

func TestTitleCommand(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")

	// 1. Inspect initial title (should be untitled or none)
	m.handleSlashCommand("/title")
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Current session title") {
		t.Errorf("expected current session title message, got: %s", lastSys)
	}

	// 2. Set new title
	m.handleSlashCommand("/title Refactor Architecture")
	if m.sessionTitle != "Refactor Architecture" {
		t.Errorf("expected sessionTitle to be 'Refactor Architecture', got: %s", m.sessionTitle)
	}
	lastSys2 := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys2, "Session title updated to `Refactor Architecture`") {
		t.Errorf("expected update notification, got: %s", lastSys2)
	}
}

func TestMCPCommand(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.handleSlashCommand("/mcp")
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "MCP management is unavailable") {
		t.Errorf("expected unavailable message when client is nil, got: %s", lastSys)
	}

	// Unknown subcommand
	m.handleSlashCommand("/mcp unknown-sub")
	lastSys2 := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys2, "MCP management is unavailable") {
		t.Errorf("expected unavailable message, got: %s", lastSys2)
	}
}

func TestReviewCommand(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.workspace = "."

	// Non-existent file review
	m.handleSlashCommand("/review non-existent-file-xyz.txt")
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "No changes found for: non-existent-file-xyz.txt") {
		t.Errorf("expected no changes message for non-existent file, got: %s", lastSys)
	}

	// Full workspace review
	m.handleSlashCommand("/review")
	lastSys2 := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys2, "Workspace Changes Review") && !strings.Contains(lastSys2, "No unstaged or uncommitted changes in workspace") {
		t.Errorf("expected review summary or no-changes message, got: %s", lastSys2)
	}
}



