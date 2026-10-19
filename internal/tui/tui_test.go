package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
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
	if len(m.entries) != 1 || m.entries[0].Kind != KindQuestionAnswered || len(m.entries[0].AnsweredQuestions) != 1 || m.entries[0].AnsweredQuestions[0].Answer != "Choice 1B" {
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
	if len(m.entries) != 1 || len(m.entries[0].AnsweredQuestions) != 2 || m.entries[0].AnsweredQuestions[1].Answer != "Choice 2A" {
		t.Fatalf("expected Question 2 merged into batch entry with Choice 2A, got %+v", m.entries)
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
	if len(m.entries) != 1 || len(m.entries[0].AnsweredQuestions) != 3 {
		t.Fatalf("expected 1 grouped question batch entry with 3 answers, got %+v", m.entries)
	}
	renderedAnswered := ansi.Strip(renderEntry(m.entries[0], 100))
	if !strings.Contains(renderedAnswered, "Questions 3/3 answered") ||
		!strings.Contains(renderedAnswered, "Choice 1B") ||
		!strings.Contains(renderedAnswered, "Choice 2A") ||
		!strings.Contains(renderedAnswered, "Choice 3A") {
		t.Fatalf("expected grouped Questions 3/3 answered card, got:\n%s", renderedAnswered)
	}
}

func TestTurnFinishedPausedClearsRunningState(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.running = true
	m.runningExecution = "exec-paused"
	m.thinking = true

	mod, _ := m.Update(turnFinishedMsg{
		sessionID: "test-sess",
		result: core.TurnResult{
			ExecutionID: "exec-paused",
			Status:      core.TaskPaused,
		},
	})
	m = mod.(*tuiModel)

	if m.running {
		t.Errorf("expected m.running to be false when turn is paused")
	}
	if m.thinking {
		t.Errorf("expected m.thinking to be false when turn is paused")
	}
	if m.runningExecution != "exec-paused" {
		t.Errorf("expected paused execution id to remain available for resolution, got %q", m.runningExecution)
	}
}

func TestRestoreRuntimeSnapshotIgnoresFinishedExecution(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.restoreRuntimeSnapshot(core.RuntimeState{Task: core.TaskState{
		ExecutionID: "finished-exec",
		Status:      core.TaskCompleted,
	}})
	if m.running || m.runningExecution != "" {
		t.Fatalf("finished execution must not be treated as active: running=%v execution=%q", m.running, m.runningExecution)
	}
}

func TestTaskStartedEventDoesNotReviveHistory(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	_, cmd := m.processRuntimeEventNoRefresh(core.Event{
		Kind:        core.EventTaskStarted,
		ExecutionID: "old-exec",
	})
	if m.running || m.runningExecution != "" || cmd != nil {
		t.Fatalf("historical task start must not revive an idle session: running=%v execution=%q cmd=%v", m.running, m.runningExecution, cmd != nil)
	}
}

func TestTurnCompletionEventAndWaiterDeduplicate(t *testing.T) {
	response := json.RawMessage(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Canonical response"}]}]}`)
	completion := core.Event{Kind: core.EventTaskFinished, ExecutionID: "exec-complete", Status: core.TaskCompleted}
	result := turnFinishedMsg{
		sessionID:   "test-sess",
		executionID: "exec-complete",
		result: core.TurnResult{
			ExecutionID: "exec-complete",
			Status:      core.TaskCompleted,
			Response:    response,
		},
	}

	for _, eventFirst := range []bool{true, false} {
		name := "waiter first"
		if eventFirst {
			name = "event first"
		}
		t.Run(name, func(t *testing.T) {
			m := newTUIModel(nil, nil, "test-sess")
			m.running = true
			m.runningExecution = "exec-complete"
			m.taskStartedAt = time.Now().Add(-time.Second)
			m.entries = append(m.entries,
				TranscriptEntry{ID: "user-current", Kind: KindUser, Text: "answer"},
				TranscriptEntry{ID: "assistant-partial", ExecutionID: "exec-complete", Kind: KindAssistant, Text: "Old partial"},
			)
			m.currentStreamText = "Latest draft"

			if eventFirst {
				m.finishTurnEvent(completion)
				m.finishTurn(result)
			} else {
				m.finishTurn(result)
				m.finishTurnEvent(completion)
			}
			m.finishTurnEvent(completion)
			m.finishTurn(result)

			var assistant []TranscriptEntry
			for _, entry := range m.entries {
				if entry.Kind == KindAssistant && entry.ExecutionID == "exec-complete" {
					assistant = append(assistant, entry)
				}
			}
			if len(assistant) != 1 || assistant[0].Text != "Canonical response" {
				t.Fatalf("expected one canonical assistant response, got %+v", assistant)
			}
			if m.running || m.currentStreamText != "" {
				t.Fatalf("expected completion to clear live turn state, running=%v stream=%q", m.running, m.currentStreamText)
			}
		})
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
	m.queuedTurns = append(m.queuedTurns, QueuedItem{ExecutionID: "exec-q2"})
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

func TestToolRecoveryModal(t *testing.T) {
	rec := &ActiveToolRecovery{
		InvocationID: "inv-1",
		CallID:       "call-1",
		ToolName:     "run_command",
		Arguments:    `{"command":"npm test"}`,
		Selected:     0,
	}

	// 1. Render test
	rendered := renderToolRecoveryPanel(rec, 80)
	stripped := ansi.Strip(rendered)
	if !strings.Contains(stripped, "Tool Failure Recovery Required") {
		t.Errorf("expected title in modal, got: %s", stripped)
	}
	if !strings.Contains(stripped, "run_command") {
		t.Errorf("expected tool name in modal, got: %s", stripped)
	}
	if !strings.Contains(stripped, "1. Retry tool execution") || !strings.Contains(stripped, "2. Skip this tool call") {
		t.Errorf("expected retry and skip options, got: %s", stripped)
	}

	// 2. Key navigation
	m := newTUIModel(nil, nil, "test-sess")
	m.width = 80
	m.height = 24
	m.activeToolRecovery = rec
	m.handleKeyPress(tea.KeyPressMsg{Code: 'j'}) // down
	if m.activeToolRecovery.Selected != 1 {
		t.Errorf("expected Selected to be 1 after down key, got: %d", m.activeToolRecovery.Selected)
	}
	m.handleKeyPress(tea.KeyPressMsg{Code: 'k'}) // up
	if m.activeToolRecovery.Selected != 0 {
		t.Errorf("expected Selected to be 0 after up key, got: %d", m.activeToolRecovery.Selected)
	}

	// 3. View integration
	viewStr := ansi.Strip(m.View().Content)
	if !strings.Contains(viewStr, "Tool Failure Recovery Required") {
		t.Errorf("expected View to include tool recovery panel, got: %s", viewStr)
	}
}

func TestExportCommand(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess-export")
	tmpDir := t.TempDir()
	m.workspace = tmpDir
	m.sessionTitle = "Export Test Session"
	m.entries = []TranscriptEntry{
		{
			ID:        "u1",
			Kind:      KindUser,
			Text:      "Show me current files",
			Timestamp: time.Now(),
		},
		{
			ID:          "t1",
			Kind:        KindTool,
			ToolName:    "run_command",
			ToolCommand: "ls -la",
			ToolOutput:  "file1.txt\nfile2.txt",
			Timestamp:   time.Now(),
		},
		{
			ID:        "a1",
			Kind:      KindAssistant,
			Text:      "Found 2 files: file1.txt, file2.txt",
			Timestamp: time.Now(),
		},
	}

	exportPath := filepath.Join(tmpDir, "exported_transcript.md")
	m.handleSlashCommand("/export " + exportPath)

	lastEntry := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastEntry, "Exported session transcript to") {
		t.Fatalf("expected export success message, got: %s", lastEntry)
	}

	content, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("failed to read exported file: %v", err)
	}
	contentStr := string(content)
	if !strings.Contains(contentStr, "# Session Transcript: Export Test Session") {
		t.Errorf("expected title in exported file, got: %s", contentStr)
	}
	if !strings.Contains(contentStr, "Show me current files") {
		t.Errorf("expected user message in exported file, got: %s", contentStr)
	}
	if !strings.Contains(contentStr, "Tool: <code>run_command</code>") {
		t.Errorf("expected tool details in exported file, got: %s", contentStr)
	}
	if !strings.Contains(contentStr, "Found 2 files") {
		t.Errorf("expected assistant text in exported file, got: %s", contentStr)
	}
}

func TestContextCommand(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess-context")
	tmpDir := t.TempDir()
	m.workspace = tmpDir

	// Create AGENTS.md in tmpDir
	agentsFile := filepath.Join(tmpDir, "AGENTS.md")
	_ = os.WriteFile(agentsFile, []byte("# Test Rules\nDo something good."), 0644)

	m.totalInputTokens = 15000
	m.contextWindow = 128000
	m.estimatedInputTokens = 12000

	m.handleSlashCommand("/context")
	lastEntry := m.entries[len(m.entries)-1].Text

	if !strings.Contains(lastEntry, "Context Status & Diagnostics") {
		t.Errorf("expected Context Status title, got: %s", lastEntry)
	}
	if !strings.Contains(lastEntry, "AGENTS.md`: Active") {
		t.Errorf("expected AGENTS.md active detection, got: %s", lastEntry)
	}
	if !strings.Contains(lastEntry, "Budget:") {
		t.Errorf("expected Budget progress bar, got: %s", lastEntry)
	}
	if !strings.Contains(lastEntry, "12,000 / 128,000 tokens") {
		t.Errorf("expected tokens in context diagnostics, got: %s", lastEntry)
	}
	if !strings.Contains(lastEntry, "Workspace Memory:") {
		t.Errorf("expected Workspace Memory in context diagnostics, got: %s", lastEntry)
	}
	if !strings.Contains(lastEntry, "Background Processes:") {
		t.Errorf("expected Background Processes in context diagnostics, got: %s", lastEntry)
	}
}

func TestSessionsCommandAndAutocomplete(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")

	// Autocomplete verification
	matches := filterCommands("/exp")
	if len(matches) == 0 || matches[0].Command != "/export" {
		t.Errorf("expected /export in autocomplete, got: %#v", matches)
	}
	matchesCtx := filterCommands("/cont")
	if len(matchesCtx) == 0 || matchesCtx[0].Command != "/context" {
		t.Errorf("expected /context in autocomplete, got: %#v", matchesCtx)
	}
	matchesMem := filterCommands("/mem")
	if len(matchesMem) == 0 || matchesMem[0].Command != "/memory" {
		t.Errorf("expected /memory in autocomplete, got: %#v", matchesMem)
	}
	matchesPS := filterCommands("/ps")
	if len(matchesPS) == 0 || matchesPS[0].Command != "/ps" {
		t.Errorf("expected /ps in autocomplete, got: %#v", matchesPS)
	}
	matchesStop := filterCommands("/stop")
	if len(matchesStop) == 0 || matchesStop[0].Command != "/stop" {
		t.Errorf("expected /stop in autocomplete, got: %#v", matchesStop)
	}
	matchesLogs := filterCommands("/log")
	if len(matchesLogs) == 0 || matchesLogs[0].Command != "/logs" {
		t.Errorf("expected /logs in autocomplete, got: %#v", matchesLogs)
	}

	// Sessions without client
	m.handleSlashCommand("/sessions myquery")
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Session management unavailable") {
		t.Errorf("expected unavailable message, got: %s", lastSys)
	}
}

func TestFileMentionAutocomplete(t *testing.T) {
	tempDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tempDir, "internal", "tui"), 0755)
	_ = os.MkdirAll(filepath.Join(tempDir, "cmd"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "internal", "tui", "tui.go"), []byte("package tui"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "cmd", "main.go"), []byte("package main"), 0644)

	m := &tuiModel{
		workspace:   tempDir,
		fileScanner: NewWorkspaceFileScanner(tempDir),
	}
	m.composer = textarea.New()
	m.composer.Focus()

	// 1. Trigger @
	m.composer.SetValue("@")
	m.composer.CursorEnd()
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 't', Text: "t"}))
	m = mod.(*tuiModel)
	if !m.autocompleteOpen {
		t.Fatalf("expected autocomplete to open for @t")
	}
	if m.autocompleteMode != AutocompleteModeFile {
		t.Errorf("expected AutocompleteModeFile, got: %v", m.autocompleteMode)
	}

	// 2. Select with Enter
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)
	if m.autocompleteOpen {
		t.Errorf("expected autocomplete to close after Enter")
	}
	if !strings.Contains(m.composer.Value(), "@internal/tui/tui.go ") {
		t.Errorf("expected composer to contain '@internal/tui/tui.go ', got: %q", m.composer.Value())
	}

	// 3. Email address test: should not open autocomplete
	m.composer.SetValue("contact alice@example.com")
	m.composer.CursorEnd()
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: ' ', Text: " "}))
	m = mod.(*tuiModel)
	if m.autocompleteOpen {
		t.Errorf("expected autocomplete to remain closed for email address")
	}

	// 4. Esc closes autocomplete
	m.composer.SetValue("review @c")
	m.composer.CursorEnd()
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'm', Text: "m"}))
	m = mod.(*tuiModel)
	if !m.autocompleteOpen {
		t.Fatalf("expected autocomplete to open for @cm")
	}
	// 5. Trigger @ alone in empty composer
	m.composer.SetValue("")
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '@', Text: "@"}))
	m = mod.(*tuiModel)
	if !m.autocompleteOpen {
		t.Fatalf("expected autocomplete to open for single '@'")
	}
	if len(m.autocompleteItems) != 2 {
		t.Fatalf("expected 2 files in autocomplete items, got: %d", len(m.autocompleteItems))
	}

	// 6. Select with Tab
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = mod.(*tuiModel)
	if m.autocompleteOpen {
		t.Errorf("expected autocomplete to close after Tab")
	}
	if !strings.HasPrefix(m.composer.Value(), "@") || !strings.HasSuffix(m.composer.Value(), " ") {
		t.Errorf("expected completed file mention with space, got: %q", m.composer.Value())
	}

	// 7. Completion with trailing text avoids double spaces
	m.composer.SetValue("inspect @t for issues")
	// Position cursor after @t (offset 10)
	m.setCursorTo(10)
	// Trigger re-detect
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'u', Text: "u"}))
	m = mod.(*tuiModel)
	if !m.autocompleteOpen {
		t.Fatalf("expected autocomplete to open for 'inspect @tu for issues'")
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = mod.(*tuiModel)
	expectedVal := "inspect @internal/tui/tui.go for issues"
	if m.composer.Value() != expectedVal {
		t.Errorf("expected %q, got %q", expectedVal, m.composer.Value())
	}

	// 8. Submit turn with @ mention: verify attachments and visual chip
	m.submitTurn(m.composer.Value())
	if len(m.entries) == 0 {
		t.Fatalf("expected user entry after submission")
	}
	lastEntry := m.entries[len(m.entries)-1]
	if lastEntry.Kind != KindUser {
		t.Errorf("expected KindUser entry, got %v", lastEntry.Kind)
	}
	if len(lastEntry.AttachedFiles) != 1 {
		t.Fatalf("expected 1 attached file badge, got %d", len(lastEntry.AttachedFiles))
	}
	if !strings.Contains(lastEntry.AttachedFiles[0], "internal/tui/tui.go") {
		t.Errorf("expected badge to contain 'internal/tui/tui.go', got: %s", lastEntry.AttachedFiles[0])
	}
	renderedUser := renderEntry(lastEntry, 80)
	if !strings.Contains(renderedUser, "📎 internal/tui/tui.go") {
		t.Errorf("expected rendered user entry to contain attachment chip, got: %s", renderedUser)
	}

	// 9. Autocomplete popup rendering test
	popupItems := []AutocompleteItem{
		{Command: "@internal/tui/tui.go", Description: "1.2 KB · modified"},
		{Command: "@cmd/main.go", Description: "512 B"},
	}
	renderedPopup := renderAutocompletePopup(popupItems, 0, 80)
	if !strings.Contains(ansi.Strip(renderedPopup), "@internal/tui/tui.go") || !strings.Contains(renderedPopup, "modified") {
		t.Errorf("expected popup to render file path and modified tag, got: %s", renderedPopup)
	}
}

func TestMemoryCommand(t *testing.T) {
	// 1. Without client
	m := newTUIModel(nil, nil, "test-sess")
	m.handleSlashCommand("/memory")
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Memory management unavailable") {
		t.Errorf("expected unavailable message without client, got: %s", lastSys)
	}

	// 2. With real core client in tempDir
	tmpDir := t.TempDir()
	projectConfig := `{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"models":{"primary":{"provider":"host","model":"test-model"}},"defaultModel":"primary"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: tmpDir})
	if err != nil {
		t.Fatalf("failed to create core: %v", err)
	}
	defer client.Close()
	mWithClient := newTUIModel(client, nil, "test-sess")

	// Initially empty
	mWithClient.handleSlashCommand("/memory")
	lastSys = mWithClient.entries[len(mWithClient.entries)-1].Text
	if !strings.Contains(lastSys, "No workspace memories stored yet") {
		t.Errorf("expected empty message, got: %s", lastSys)
	}

	// Add convention
	mWithClient.handleSlashCommand("/memory add convention Always verify tests")
	lastSys = mWithClient.entries[len(mWithClient.entries)-1].Text
	if !strings.Contains(lastSys, "Added workspace memory") || !strings.Contains(lastSys, "convention") {
		t.Errorf("expected added message, got: %s", lastSys)
	}

	// Add preference
	mWithClient.handleSlashCommand("/memory add Keep responses concise")
	lastSys = mWithClient.entries[len(mWithClient.entries)-1].Text
	if !strings.Contains(lastSys, "Added workspace memory") || !strings.Contains(lastSys, "preference") {
		t.Errorf("expected added preference, got: %s", lastSys)
	}

	// List
	mWithClient.handleSlashCommand("/memory list")
	lastSys = mWithClient.entries[len(mWithClient.entries)-1].Text
	if !strings.Contains(lastSys, "Workspace Memories (2 items)") {
		t.Errorf("expected 2 items, got: %s", lastSys)
	}

	// Search
	mWithClient.handleSlashCommand("/memory list verify")
	lastSys = mWithClient.entries[len(mWithClient.entries)-1].Text
	if !strings.Contains(lastSys, "Workspace Memories") || !strings.Contains(lastSys, "verify") {
		t.Errorf("expected search match for verify, got: %s", lastSys)
	}

	// Remove by prefix or exact ID
	// Extract short ID from list output: `[mem-...` or similar
	items, err := client.ListMemories(context.Background(), 10)
	if err != nil || len(items) == 0 {
		t.Fatalf("expected memories in store, got error: %v", err)
	}
	targetID := items[0].ID
	mWithClient.handleSlashCommand("/memory remove " + targetID[:6])
	lastSys = mWithClient.entries[len(mWithClient.entries)-1].Text
	if !strings.Contains(lastSys, "Deleted workspace memory") {
		t.Errorf("expected deleted confirmation, got: %s", lastSys)
	}

	// Clear requires confirmation
	mWithClient.handleSlashCommand("/memory clear")
	lastSys = mWithClient.entries[len(mWithClient.entries)-1].Text
	if !strings.Contains(lastSys, "Are you sure you want to clear all workspace memories?") {
		t.Errorf("expected confirmation warning, got: %s", lastSys)
	}

	// Clear with force
	mWithClient.handleSlashCommand("/memory clear --force")
	lastSys = mWithClient.entries[len(mWithClient.entries)-1].Text
	if !strings.Contains(lastSys, "Cleared all workspace memories.") {
		t.Errorf("expected cleared confirmation, got: %s", lastSys)
	}

	// List after clear
	mWithClient.handleSlashCommand("/memory list")
	lastSys = mWithClient.entries[len(mWithClient.entries)-1].Text
	if !strings.Contains(lastSys, "No workspace memories stored yet") {
		t.Errorf("expected empty list after clear, got: %s", lastSys)
	}
}

func TestProcessCommands(t *testing.T) {
	// 1. Without runtime
	m := newTUIModel(nil, nil, "test-sess")
	m.handleSlashCommand("/ps")
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Background process management unavailable") {
		t.Errorf("expected unavailable message without runtime, got: %s", lastSys)
	}

	m.handleSlashCommand("/stop all")
	lastSys = m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Process termination unavailable") {
		t.Errorf("expected unavailable message without runtime, got: %s", lastSys)
	}

	m.handleSlashCommand("/logs cmd-1")
	lastSys = m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Process logs unavailable") {
		t.Errorf("expected unavailable message without runtime, got: %s", lastSys)
	}

	// 2. With real core client and session runtime
	tmpDir := t.TempDir()
	projectConfig := `{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"models":{"primary":{"provider":"host","model":"test-model"}},"defaultModel":"primary"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: tmpDir})
	if err != nil {
		t.Fatalf("failed to create core: %v", err)
	}
	defer client.Close()

	session, err := client.CreateSession(context.Background(), json.RawMessage("null"))
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	runtime, err := client.OpenSessionRuntime(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("failed to open session runtime: %v", err)
	}
	mWithRuntime := newTUIModel(client, runtime, session.ID)

	// Initially empty ps
	mWithRuntime.handleSlashCommand("/ps")
	lastSys = mWithRuntime.entries[len(mWithRuntime.entries)-1].Text
	if !strings.Contains(lastSys, "No background processes in current session.") {
		t.Errorf("expected empty processes message, got: %s", lastSys)
	}

	// Usage error for /stop
	mWithRuntime.handleSlashCommand("/stop")
	lastSys = mWithRuntime.entries[len(mWithRuntime.entries)-1].Text
	if !strings.Contains(lastSys, "Usage: `/stop <id|prefix|all>`") {
		t.Errorf("expected usage message, got: %s", lastSys)
	}

	// Stop all when none running
	mWithRuntime.handleSlashCommand("/stop all")
	lastSys = mWithRuntime.entries[len(mWithRuntime.entries)-1].Text
	if !strings.Contains(lastSys, "No active background processes to stop.") {
		t.Errorf("expected no active processes message, got: %s", lastSys)
	}

	// Usage error for /logs
	mWithRuntime.handleSlashCommand("/logs")
	lastSys = mWithRuntime.entries[len(mWithRuntime.entries)-1].Text
	if !strings.Contains(lastSys, "Usage: `/logs <id|prefix> [--tail N]`") {
		t.Errorf("expected usage message, got: %s", lastSys)
	}

	// Logs for non-existent process
	mWithRuntime.handleSlashCommand("/logs non-existent")
	lastSys = mWithRuntime.entries[len(mWithRuntime.entries)-1].Text
	if !strings.Contains(lastSys, "Process \"non-existent\" not found.") {
		t.Errorf("expected not found message, got: %s", lastSys)
	}
}

func TestBindSessionRuntimeRestoresSnapshotAndKeepsOldStateOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	projectConfig := `{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"models":{"primary":{"provider":"host","model":"test-model"}},"defaultModel":"primary"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: tmpDir})
	if err != nil {
		t.Fatalf("failed to create core: %v", err)
	}
	defer client.Close()
	ctx := context.Background()

	first, err := client.CreateSession(ctx, json.RawMessage(`[{"type":"message","role":"user","content":[{"type":"input_text","text":"First session prompt"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetSessionTitle(ctx, first.ID, "First session"); err != nil {
		t.Fatal(err)
	}
	second, err := client.CreateSession(ctx, json.RawMessage(`[{"type":"message","role":"user","content":[{"type":"input_text","text":"Second session prompt"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	if err := client.SetSessionTitle(ctx, second.ID, "Second session"); err != nil {
		t.Fatal(err)
	}
	runtime, err := client.OpenSessionRuntime(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := newTUIModel(client, runtime, first.ID)
	var terminalOutput strings.Builder
	m.output = &terminalOutput
	m.activeQuestion = &ActiveQuestion{}
	m.activeApproval = &ActiveApproval{}
	m.activePlanReview = &ActivePlanReview{}

	if err := m.bindSessionRuntime(ctx, second.ID); err != nil {
		t.Fatalf("failed to switch session: %v", err)
	}
	if m.sessionID != second.ID || m.sessionTitle != "Second session" {
		t.Fatalf("session metadata was not restored: id=%q title=%q", m.sessionID, m.sessionTitle)
	}
	if len(m.promptHistory) != 1 || m.promptHistory[0] != "Second session prompt" {
		t.Fatalf("session prompt history was not restored: %+v", m.promptHistory)
	}
	if len(m.entries) != 1 || m.entries[0].Text != "Second session prompt" {
		t.Fatalf("session transcript was not replaced from snapshot: %+v", m.entries)
	}
	if m.activeQuestion != nil || m.activeApproval != nil || m.activePlanReview != nil {
		t.Fatal("pending UI state from the previous session must not leak into the new session")
	}

	previousRuntime := m.runtime
	previousEntries := append([]TranscriptEntry(nil), m.entries...)
	if err := m.bindSessionRuntime(ctx, "missing-session"); err == nil {
		t.Fatal("expected binding a missing session to fail")
	}
	if m.sessionID != second.ID || m.runtime != previousRuntime || len(m.entries) != len(previousEntries) || m.entries[0].Text != previousEntries[0].Text {
		t.Fatal("failed session preflight must leave the active session intact")
	}
}

func TestSessionsModalInteractions(t *testing.T) {
	tmpDir := t.TempDir()
	projectConfig := `{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"models":{"primary":{"provider":"host","model":"test-model"}},"defaultModel":"primary"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: tmpDir})
	if err != nil {
		t.Fatalf("failed to create core: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	s1, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	_ = client.SetSessionTitle(ctx, s1.ID, "Frontend UI feature")

	s2, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	_ = client.SetSessionTitle(ctx, s2.ID, "Backend API bugfix")

	s3, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	_ = client.SetSessionTitle(ctx, s3.ID, "Database migration")

	rt1, err := client.OpenSessionRuntime(ctx, s1.ID)
	if err != nil {
		t.Fatal(err)
	}

	m := newTUIModel(client, rt1, s1.ID)
	m.width = 100
	m.height = 30
	m.updateLayout()

	// 1. Guard: Cannot open modal when running
	m.running = true
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if m.sessionsModalOpen {
		t.Fatalf("expected sessions modal NOT to open while task is running")
	}
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Cannot switch sessions while a task is running") {
		t.Fatalf("expected running guard warning, got: %s", lastSys)
	}
	m.running = false

	// 2. Open modal via Ctrl+O
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if !m.sessionsModalOpen {
		t.Fatalf("expected sessions modal to open on Ctrl+O")
	}
	if len(m.sessionsItems) != 3 {
		t.Fatalf("expected 3 sessions in modal, got %d", len(m.sessionsItems))
	}
	if len(m.sessionsFiltered) != 3 {
		t.Fatalf("expected 3 filtered sessions, got %d", len(m.sessionsFiltered))
	}
	// Verify view rendering
	viewStr := ansi.Strip(m.View().Content)
	if !strings.Contains(viewStr, "Switch Session") || !strings.Contains(viewStr, "Frontend UI feature") || !strings.Contains(viewStr, "• active") {
		t.Fatalf("expected modal view with session details, got:\n%s", viewStr)
	}

	// 3. Navigation with Down and Up
	initialIdx := m.sessionsIndex
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mod.(*tuiModel)
	if m.sessionsIndex == initialIdx && len(m.sessionsFiltered) > 1 {
		t.Fatalf("expected sessionsIndex to change on Down")
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mod.(*tuiModel)
	if m.sessionsIndex != initialIdx {
		t.Fatalf("expected sessionsIndex to return to initialIdx on Up")
	}

	// 4. Typing to filter
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'b', Text: "b"}))
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'u', Text: "u"}))
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'g', Text: "g"}))
	m = mod.(*tuiModel)
	if m.sessionsFilter != "bug" {
		t.Fatalf("expected filter to be 'bug', got %q", m.sessionsFilter)
	}
	if len(m.sessionsFiltered) != 1 || m.sessionsFiltered[0].ID != s2.ID {
		t.Fatalf("expected filtered to contain only s2, got %d items", len(m.sessionsFiltered))
	}

	// 5. Backspace removes last char
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	m = mod.(*tuiModel)
	if m.sessionsFilter != "bu" {
		t.Fatalf("expected filter to be 'bu' after backspace, got %q", m.sessionsFilter)
	}

	// 6. Dismiss with Esc
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.sessionsModalOpen {
		t.Fatalf("expected sessions modal to close on Esc")
	}
	if m.sessionsFilter != "" {
		t.Fatalf("expected filter to reset on Esc")
	}

	// 7. Open with /open command (without arguments)
	mod, _ = m.handleSlashCommand("/open")
	m = mod.(*tuiModel)
	if !m.sessionsModalOpen {
		t.Fatalf("expected /open without args to open modal")
	}

	// 8. Filter to "data" and press Enter to switch
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'd', Text: "d"}))
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"}))
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 't', Text: "t"}))
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Text: "a"}))
	m = mod.(*tuiModel)
	if len(m.sessionsFiltered) != 1 || m.sessionsFiltered[0].ID != s3.ID {
		t.Fatalf("expected filtered to match s3, got %d items", len(m.sessionsFiltered))
	}

	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)
	if m.sessionsModalOpen {
		t.Fatalf("expected modal to close after Enter")
	}
	if m.sessionID != s3.ID {
		t.Fatalf("expected session to switch to s3 (%s), got %s", s3.ID, m.sessionID)
	}

	// 9. Pressing Ctrl+O while modal is open toggles it closed
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if !m.sessionsModalOpen {
		t.Fatalf("expected modal to open on Ctrl+O")
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if m.sessionsModalOpen {
		t.Fatalf("expected modal to toggle closed on second Ctrl+O")
	}

	// 10. Verify shortcuts panel includes Ctrl+O
	shortcutsStr := renderShortcutsPanel(100)
	if !strings.Contains(shortcutsStr, "Ctrl+O") || !strings.Contains(shortcutsStr, "Switch session") {
		t.Fatalf("expected shortcuts panel to list Ctrl+O Switch session, got:\n%s", shortcutsStr)
	}
}

func TestSessionsModalDeleteAndDropCommand(t *testing.T) {
	tmpDir := t.TempDir()
	projectConfig := `{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"models":{"primary":{"provider":"host","model":"test-model"}},"defaultModel":"primary"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: tmpDir})
	if err != nil {
		t.Fatalf("failed to create core: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	s1, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	_ = client.SetSessionTitle(ctx, s1.ID, "Session 1 Active")

	s2, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	_ = client.SetSessionTitle(ctx, s2.ID, "Session 2 Disposable")

	s3, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	_ = client.SetSessionTitle(ctx, s3.ID, "Session 3 Extra")

	rt1, err := client.OpenSessionRuntime(ctx, s1.ID)
	if err != nil {
		t.Fatal(err)
	}

	m := newTUIModel(client, rt1, s1.ID)
	m.width = 100
	m.height = 30
	m.updateLayout()

	// 1. Open sessions modal
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if !m.sessionsModalOpen {
		t.Fatalf("expected modal to open on Ctrl+O")
	}

	// 2. Select active session (s1) and try Ctrl+D -> blocked
	for i, s := range m.sessionsFiltered {
		if s.ID == s1.ID {
			m.sessionsIndex = i
			break
		}
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if m.sessionsConfirmDelete {
		t.Fatalf("expected deletion confirmation NOT to trigger for active session")
	}
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Cannot delete currently active session") {
		t.Fatalf("expected active session guard warning, got: %s", lastSys)
	}

	// 3. Select disposable session (s2) and press Ctrl+D -> enter confirmation
	for i, s := range m.sessionsFiltered {
		if s.ID == s2.ID {
			m.sessionsIndex = i
			break
		}
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if !m.sessionsConfirmDelete || m.sessionsDeletingID != s2.ID {
		t.Fatalf("expected sessionsConfirmDelete for s2, got %v (id: %s)", m.sessionsConfirmDelete, m.sessionsDeletingID)
	}
	// Verify view rendering contains confirmation warning
	viewStr := ansi.Strip(m.View().Content)
	if !strings.Contains(viewStr, "Delete session") || !strings.Contains(viewStr, "y to confirm") {
		t.Fatalf("expected confirmation banner in view, got:\n%s", viewStr)
	}

	// 4. Press 'n' to cancel
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"}))
	m = mod.(*tuiModel)
	if m.sessionsConfirmDelete {
		t.Fatalf("expected sessionsConfirmDelete to cancel on 'n'")
	}

	// 5. Press Delete key (tea.KeyDelete) on s2 and press 'y' to confirm
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDelete}))
	m = mod.(*tuiModel)
	if !m.sessionsConfirmDelete {
		t.Fatalf("expected sessionsConfirmDelete on Delete key")
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	m = mod.(*tuiModel)
	if m.sessionsConfirmDelete {
		t.Fatalf("expected confirmation to clear after 'y'")
	}
	// Verify s2 is gone from sessionsItems
	for _, s := range m.sessionsItems {
		if s.ID == s2.ID {
			t.Fatalf("expected s2 to be deleted from sessionsItems")
		}
	}
	lastSys2 := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys2, "Deleted session: "+s2.ID) {
		t.Fatalf("expected deletion notice, got: %s", lastSys2)
	}

	// Close modal with Esc
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)

	// 6. Test /drop command
	// Usage error
	m.handleSlashCommand("/drop")
	if !strings.Contains(m.entries[len(m.entries)-1].Text, "Usage: `/drop") {
		t.Fatalf("expected usage error for /drop")
	}

	// Cannot delete active session
	m.handleSlashCommand("/drop " + s1.ID)
	if !strings.Contains(m.entries[len(m.entries)-1].Text, "Cannot delete currently active session") {
		t.Fatalf("expected active session guard for /drop")
	}

	// Delete s3 via prefix
	s3Prefix := s3.ID[:8]
	m.handleSlashCommand("/drop " + s3Prefix)
	if !strings.Contains(m.entries[len(m.entries)-1].Text, "Deleted session: "+s3.ID) {
		t.Fatalf("expected deleted session notice for s3, got: %s", m.entries[len(m.entries)-1].Text)
	}

	// 7. Test running guard for /drop
	m.running = true
	m.handleSlashCommand("/drop non-existent")
	if !strings.Contains(m.entries[len(m.entries)-1].Text, "Cannot delete sessions while a task is running") {
		t.Fatalf("expected running guard warning for /drop")
	}
}

func TestToolPagerModalInteractions(t *testing.T) {
	m := newTUIModel(nil, nil, "test-tool-pager")
	m.width = 100
	m.height = 30
	m.updateLayout()

	// 1. Pressing Ctrl+T when no tool executions exist
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 't', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if m.toolPagerOpen {
		t.Fatalf("expected toolPagerOpen to be false when no tools exist")
	}
	if len(m.entries) == 0 || !strings.Contains(m.entries[len(m.entries)-1].Text, "No tool executions in current session.") {
		t.Fatalf("expected 'No tool executions' system entry, got %v", m.entries)
	}

	// 2. Populate entries with multiple tool executions
	tool1 := TranscriptEntry{
		ID:          "tool-1",
		Kind:        KindTool,
		ToolName:    "list_dir",
		ToolCommand: "list_dir .",
		ToolOutput:  "file1.go\nfile2.go\nfile3.go",
	}
	var longOutputBuilder strings.Builder
	for i := 1; i <= 50; i++ {
		longOutputBuilder.WriteString(fmt.Sprintf("log line output item #%d\n", i))
	}
	tool2 := TranscriptEntry{
		ID:          "tool-2",
		Kind:        KindTool,
		ToolName:    "run_command",
		ToolCommand: "go test ./...",
		ToolOutput:  longOutputBuilder.String(),
	}
	failExit := 1
	tool3 := TranscriptEntry{
		ID:           "tool-3",
		Kind:         KindTool,
		ToolName:     "run_command",
		ToolCommand:  "git diff --check",
		ToolOutput:   "error: trailing whitespace in file.go\nerror: missing newline at EOF",
		ToolExitCode: &failExit,
	}

	m.entries = []TranscriptEntry{
		{Kind: KindUser, Text: "Run tests and checks"},
		tool1,
		tool2,
		tool3,
	}
	m.updateLayout()

	// 3. Open pager via Ctrl+T
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 't', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if !m.toolPagerOpen {
		t.Fatalf("expected toolPagerOpen to be true after Ctrl+T")
	}
	if m.toolPagerIndex != 2 {
		t.Fatalf("expected toolPagerIndex to point to latest tool (2), got %d", m.toolPagerIndex)
	}
	if m.toolPagerScrollY != 0 {
		t.Fatalf("expected toolPagerScrollY to be 0, got %d", m.toolPagerScrollY)
	}

	// Verify modal rendering contains tool 3 info and failure badge
	rendered := renderToolPagerModal(m, 100)
	if !strings.Contains(rendered, "Tool Output Pager") || !strings.Contains(rendered, "(Tool 3 of 3)") {
		t.Fatalf("expected tool 3 header in modal, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Failed (exit 1)") || !strings.Contains(rendered, "git diff --check") {
		t.Fatalf("expected failed command in modal, got:\n%s", rendered)
	}

	// 4. Switch to previous tool using '[' or 'p'
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '[', Text: "["}))
	m = mod.(*tuiModel)
	if m.toolPagerIndex != 1 {
		t.Fatalf("expected toolPagerIndex to be 1 after '[', got %d", m.toolPagerIndex)
	}
	renderedTool2 := renderToolPagerModal(m, 100)
	if !strings.Contains(renderedTool2, "(Tool 2 of 3)") || !strings.Contains(renderedTool2, "go test ./...") {
		t.Fatalf("expected tool 2 in modal, got:\n%s", renderedTool2)
	}

	// 5. Test vertical scrolling on tool 2 (which has 50 lines)
	visible := m.toolPagerVisibleLines()
	// Scroll down with 'j'
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'j', Text: "j"}))
	m = mod.(*tuiModel)
	if m.toolPagerScrollY != 1 {
		t.Fatalf("expected scrollY to be 1 after 'j', got %d", m.toolPagerScrollY)
	}

	// Scroll down with 'pgdown'
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	m = mod.(*tuiModel)
	if m.toolPagerScrollY != 1+visible {
		t.Fatalf("expected scrollY to be %d after pgdown, got %d", 1+visible, m.toolPagerScrollY)
	}

	// Scroll to end with 'end' / 'G'
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'G', Text: "G"}))
	m = mod.(*tuiModel)
	maxScroll := max(0, 50-visible)
	if m.toolPagerScrollY != maxScroll {
		t.Fatalf("expected scrollY to be maxScroll (%d), got %d", maxScroll, m.toolPagerScrollY)
	}

	// Scroll to top with 'home' / 'g'
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'g', Text: "g"}))
	m = mod.(*tuiModel)
	if m.toolPagerScrollY != 0 {
		t.Fatalf("expected scrollY to be 0 after 'g', got %d", m.toolPagerScrollY)
	}

	// 6. Test copying tool output with 'c'
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Text: "c"}))
	m = mod.(*tuiModel)
	if !strings.Contains(m.copyFeedback, "Copied") {
		t.Fatalf("expected copy feedback, got %q", m.copyFeedback)
	}
	renderedWithCopy := renderToolPagerModal(m, 100)
	if !strings.Contains(renderedWithCopy, "Copied") {
		t.Fatalf("expected copy feedback rendered in footer, got:\n%s", renderedWithCopy)
	}

	// 7. Cycle previous to tool 0, then wrap around
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Text: "p"}))
	m = mod.(*tuiModel)
	if m.toolPagerIndex != 0 {
		t.Fatalf("expected toolPagerIndex to be 0 after 'p', got %d", m.toolPagerIndex)
	}
	// Wrap around backwards to 2
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '[', Text: "["}))
	m = mod.(*tuiModel)
	if m.toolPagerIndex != 2 {
		t.Fatalf("expected toolPagerIndex to wrap to 2, got %d", m.toolPagerIndex)
	}
	// Cycle next with ']'
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: ']', Text: "]"}))
	m = mod.(*tuiModel)
	if m.toolPagerIndex != 0 {
		t.Fatalf("expected toolPagerIndex to wrap to 0, got %d", m.toolPagerIndex)
	}

	// 8. Close with 'q'
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}))
	m = mod.(*tuiModel)
	if m.toolPagerOpen {
		t.Fatalf("expected toolPagerOpen to be false after 'q'")
	}

	// 9. Open via /detail command with specific 1-based index
	mod, _ = m.handleSlashCommand("/detail 2")
	m = mod.(*tuiModel)
	if !m.toolPagerOpen {
		t.Fatalf("expected toolPagerOpen to be true after /detail 2")
	}
	if m.toolPagerIndex != 1 {
		t.Fatalf("expected toolPagerIndex to be 1 (/detail 2), got %d", m.toolPagerIndex)
	}

	// Close with Esc
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.toolPagerOpen {
		t.Fatalf("expected toolPagerOpen to be false after Esc")
	}

	// Toggle open and close with Ctrl+T
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 't', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if !m.toolPagerOpen {
		t.Fatalf("expected toolPagerOpen to be true after Ctrl+T toggle open")
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 't', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if m.toolPagerOpen {
		t.Fatalf("expected toolPagerOpen to be false after Ctrl+T toggle close")
	}
}

func TestDiffModalInteractions(t *testing.T) {
	// 1. Test parseGitDiff parser
	sampleDiff := `diff --git a/pkg/auth/login.go b/pkg/auth/login.go
index 1111111..2222222 100644
--- a/pkg/auth/login.go
+++ b/pkg/auth/login.go
@@ -10,25 +10,27 @@ func Login() {
-	oldCheck()
+	newCheck()
+	auditLog()
+	line4()
+	line5()
+	line6()
+	line7()
+	line8()
+	line9()
+	line10()
+	line11()
+	line12()
+	line13()
+	line14()
+	line15()
+	line16()
+	line17()
+	line18()
+	line19()
+	line20()
+	line21()
+	line22()
+	line23()
+	line24()
+	line25()
 }
diff --git a/pkg/auth/token.go b/pkg/auth/token.go
index 3333333..4444444 100644
--- a/pkg/auth/token.go
+++ b/pkg/auth/token.go
@@ -5,3 +5,2 @@ func Token() {
-	deprecated()
 }`
	parsed := parseGitDiff(sampleDiff)
	if len(parsed) != 2 {
		t.Fatalf("expected 2 diff files, got %d", len(parsed))
	}
	if parsed[0].Path != "pkg/auth/login.go" || parsed[0].Additions != 24 || parsed[0].Deletions != 1 {
		t.Fatalf("file 0 parsed stats mismatch: %+v", parsed[0])
	}
	if parsed[1].Path != "pkg/auth/token.go" || parsed[1].Additions != 0 || parsed[1].Deletions != 1 {
		t.Fatalf("file 1 parsed stats mismatch: %+v", parsed[1])
	}

	// 2. Setup model with diff files
	m := newTUIModel(nil, nil, "test-sess")
	m.width = 100
	m.height = 30
	m.diffModalOpen = true
	m.diffFiles = parsed
	m.diffFileIndex = 0
	m.diffScrollY = 0
	m.updateLayout()

	// Verify renderDiffModal output
	viewContent := ansi.Strip(m.View().Content)
	if !strings.Contains(viewContent, "Workspace Diff Pager") {
		t.Errorf("expected View to contain Workspace Diff Pager, got: %s", viewContent)
	}
	if !strings.Contains(viewContent, "File 1 of 2") {
		t.Errorf("expected View to show File 1 of 2, got: %s", viewContent)
	}
	if !strings.Contains(viewContent, "pkg/auth/login.go") {
		t.Errorf("expected View to show login.go, got: %s", viewContent)
	}
	if !strings.Contains(viewContent, "+24") || !strings.Contains(viewContent, "-1") {
		t.Errorf("expected View to show +24 -1 stats, got: %s", viewContent)
	}

	// 3. Test scrolling (down/up)
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mod.(*tuiModel)
	if m.diffScrollY != 1 {
		t.Errorf("expected diffScrollY to be 1 after down arrow, got %d", m.diffScrollY)
	}

	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mod.(*tuiModel)
	if m.diffScrollY != 0 {
		t.Errorf("expected diffScrollY to be 0 after up arrow, got %d", m.diffScrollY)
	}

	// 4. Test file switching (Tab / ])
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = mod.(*tuiModel)
	if m.diffFileIndex != 1 {
		t.Errorf("expected diffFileIndex to be 1 after Tab, got %d", m.diffFileIndex)
	}
	viewContent2 := ansi.Strip(m.View().Content)
	if !strings.Contains(viewContent2, "File 2 of 2") || !strings.Contains(viewContent2, "pkg/auth/token.go") {
		t.Errorf("expected View to show File 2 of 2 token.go, got: %s", viewContent2)
	}

	// Previous file ([ / p)
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '['}))
	m = mod.(*tuiModel)
	if m.diffFileIndex != 0 {
		t.Errorf("expected diffFileIndex to wrap back to 0, got %d", m.diffFileIndex)
	}

	// 5. Test Copy (c)
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'c'}))
	m = mod.(*tuiModel)
	if !strings.Contains(m.copyFeedback, "Copied") {
		t.Errorf("expected copy feedback, got %q", m.copyFeedback)
	}

	// 6. Test Esc closes modal
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.diffModalOpen {
		t.Fatalf("expected diffModalOpen to be false after Esc")
	}

	// 7. Test /diff command with non-existent path
	m.handleSlashCommand("/diff non-existent-path-abc.txt")
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "No changes found for: non-existent-path-abc.txt") {
		t.Errorf("expected no changes notice, got: %s", lastSys)
	}
}

func BenchmarkRefreshViewportContent_Cached(b *testing.B) {
	m := newTUIModel(nil, nil, "bench-sess")
	m.width = 120
	m.height = 40

	// 50 realistic entries
	for i := 0; i < 25; i++ {
		m.entries = append(m.entries, TranscriptEntry{
			Kind: KindUser,
			Text: fmt.Sprintf("Turn %d: Analyze the performance of our database query layer", i),
		})
		m.entries = append(m.entries, TranscriptEntry{
			Kind: KindAssistant,
			Text: fmt.Sprintf("Here is the analysis for turn %d:\n- Check **indexes** on columns\n- Use `EXPLAIN ANALYZE`\n- Review connection pool settings\n\n```sql\nSELECT * FROM users WHERE active = true;\n```", i),
		})
	}
	m.refreshViewportContent()

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.currentStreamText = "Streaming token delta..."
		m.refreshViewportContent()
	}
}

func BenchmarkRefreshViewportContent_Uncached(b *testing.B) {
	m := newTUIModel(nil, nil, "bench-sess")
	m.width = 120
	m.height = 40

	for i := 0; i < 25; i++ {
		m.entries = append(m.entries, TranscriptEntry{
			Kind: KindUser,
			Text: fmt.Sprintf("Turn %d: Analyze the performance of our database query layer", i),
		})
		m.entries = append(m.entries, TranscriptEntry{
			Kind: KindAssistant,
			Text: fmt.Sprintf("Here is the analysis for turn %d:\n- Check **indexes** on columns\n- Use `EXPLAIN ANALYZE`\n- Review connection pool settings\n\n```sql\nSELECT * FROM users WHERE active = true;\n```", i),
		})
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m.invalidateEntryCache()
		m.refreshViewportContent()
	}
}

func BenchmarkRenderMarkdown(b *testing.B) {
	sample := `# System Architecture
This document outlines the **core design** of the system.
Here are the primary components:
- Storage engine with SQLite and WAL mode
- Transport layer with WebSocket and JSON-RPC
- Terminal user interface with Lipgloss styling

` + "```go\nfunc Process(ctx context.Context) error {\n\treturn nil\n}\n```\n" + `
Please refer to ` + "`internal/tui/model.go`" + ` for details.`

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = RenderMarkdown(sample, 80)
	}
}

func BenchmarkFuzzyMatchFiles_5000(b *testing.B) {
	files := make([]WorkspaceFile, 5000)
	for i := 0; i < 5000; i++ {
		p := fmt.Sprintf("internal/pkg%d/submodule/service_%d_handler.go", i%20, i)
		name := fmt.Sprintf("service_%d_handler.go", i)
		files[i] = WorkspaceFile{
			Path:      p,
			PathLower: strings.ToLower(p),
			Name:      name,
			NameLower: strings.ToLower(name),
			Size:      int64(1024 + i),
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = FuzzyMatchFiles(files, "service_12", 10)
	}
}

func TestFormatRelativeTime(t *testing.T) {
	if got := formatRelativeTime(time.Time{}); got != "-" {
		t.Errorf("expected '-', got %q", got)
	}
	now := time.Now()
	if got := formatRelativeTime(now.Add(-10 * time.Second)); got != "just now" {
		t.Errorf("expected 'just now', got %q", got)
	}
	if got := formatRelativeTime(now.Add(-5 * time.Minute)); got != "5m ago" {
		t.Errorf("expected '5m ago', got %q", got)
	}
	if got := formatRelativeTime(now.Add(-2 * time.Hour)); got != "2h ago" {
		t.Errorf("expected '2h ago', got %q", got)
	}
	if got := formatRelativeTime(now.Add(-26 * time.Hour)); got != "yesterday" {
		t.Errorf("expected 'yesterday', got %q", got)
	}
	if got := formatRelativeTime(now.Add(-4 * 24 * time.Hour)); got != "4d ago" {
		t.Errorf("expected '4d ago', got %q", got)
	}
	// Older than 30 days
	oldTime := time.Date(2025, time.January, 15, 10, 0, 0, 0, time.UTC)
	if got := formatRelativeTime(oldTime); got != "Jan 15" {
		t.Errorf("expected 'Jan 15', got %q", got)
	}
}

func TestSessionsCommandAndResumeWorkflows(t *testing.T) {
	tmpDir := t.TempDir()
	projectConfig := `{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"models":{"primary":{"provider":"host","model":"test-model"}},"defaultModel":"primary"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: tmpDir})
	if err != nil {
		t.Fatalf("failed to create core: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	s1, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	_ = client.SetSessionTitle(ctx, s1.ID, "Alpha Task")

	s2, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	_ = client.SetSessionTitle(ctx, s2.ID, "Beta Bugfix")

	m := newTUIModel(client, nil, s1.ID)
	m.width = 100
	m.height = 30
	m.updateLayout()

	// 1. /sessions without arguments opens the modal
	mod, _ := m.handleSlashCommand("/sessions")
	m = mod.(*tuiModel)
	if !m.sessionsModalOpen {
		t.Fatalf("expected /sessions without args to open sessions modal")
	}
	if m.sessionsFilter != "" {
		t.Fatalf("expected filter to be empty, got %q", m.sessionsFilter)
	}

	// Close modal with Esc
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.sessionsModalOpen {
		t.Fatalf("expected modal to close on Esc")
	}

	// 2. /sessions with query that matches ambiguous/non-unique string opens modal pre-filtered
	mod, _ = m.handleSlashCommand("/sessions Beta")
	m = mod.(*tuiModel)
	if !m.sessionsModalOpen {
		t.Fatalf("expected /sessions Beta to open pre-filtered modal")
	}
	if m.sessionsFilter != "Beta" {
		t.Fatalf("expected sessionsFilter to be 'Beta', got %q", m.sessionsFilter)
	}
	if len(m.sessionsFiltered) != 1 || m.sessionsFiltered[0].ID != s2.ID {
		t.Fatalf("expected filtered to match s2, got %d items", len(m.sessionsFiltered))
	}

	// Close modal
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)

	// 3. /sessions with exact session ID or unique prefix switches directly
	s2Prefix := s2.ID
	if len(s2Prefix) > 8 {
		s2Prefix = s2Prefix[:8]
	}
	mod, _ = m.handleSlashCommand("/sessions " + s2Prefix)
	m = mod.(*tuiModel)
	if m.sessionsModalOpen {
		t.Fatalf("expected direct switch instead of opening modal for unique prefix")
	}
	if m.sessionID != s2.ID {
		t.Fatalf("expected session to switch to s2 (%s), got %s", s2.ID, m.sessionID)
	}

	// 4. /resume with session prefix switches to session
	s1Prefix := s1.ID
	if len(s1Prefix) > 8 {
		s1Prefix = s1Prefix[:8]
	}
	mod, _ = m.handleSlashCommand("/resume " + s1Prefix)
	m = mod.(*tuiModel)
	if m.sessionID != s1.ID {
		t.Fatalf("expected /resume with prefix to switch to s1 (%s), got %s", s1.ID, m.sessionID)
	}

	// 5. /resume without arguments when no paused task opens sessions modal
	mod, _ = m.handleSlashCommand("/resume")
	m = mod.(*tuiModel)
	if !m.sessionsModalOpen {
		t.Fatalf("expected /resume without args to open sessions modal when idle")
	}
}

func TestExportMultiFormat(t *testing.T) {
	tmpDir := t.TempDir()
	exitZero := 0
	m := &tuiModel{
		workspace:         tmpDir,
		sessionID:         "test-export-id-12345",
		sessionTitle:      "Multi-Format Export Test",
		currentModel:      "test-model-pro",
		totalInputTokens:  1200,
		totalOutputTokens: 600,
		entries: []TranscriptEntry{
			{
				ID:        "u1",
				Kind:      KindUser,
				Text:      "Generate a sorting function",
				Timestamp: time.Now(),
			},
			{
				ID:           "t1",
				Kind:         KindTool,
				ToolName:     "run_command",
				ToolCommand:  "go test ./...",
				ToolArgs:     `{"cmd":"go test"}`,
				ToolOutput:   "PASS\nok\n",
				ToolExitCode: &exitZero,
				Timestamp:    time.Now(),
			},
			{
				ID:        "a1",
				Kind:      KindAssistant,
				Text:      "Here is the sorting implementation.",
				Timestamp: time.Now(),
			},
		},
	}

	// 1. Export HTML
	htmlFile := filepath.Join(tmpDir, "report.html")
	m.handleSlashCommand("/export " + htmlFile)
	lastEntry := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastEntry, "Exported session transcript") {
		t.Fatalf("expected export success message, got: %s", lastEntry)
	}
	htmlBytes, err := os.ReadFile(htmlFile)
	if err != nil {
		t.Fatalf("failed to read html export: %v", err)
	}
	htmlContent := string(htmlBytes)
	if !strings.Contains(htmlContent, "<!DOCTYPE html>") || !strings.Contains(htmlContent, "Multi-Format Export Test") {
		t.Errorf("HTML export missing doctype or title: %s", htmlContent)
	}
	if !strings.Contains(htmlContent, "Generate a sorting function") || !strings.Contains(htmlContent, "run_command") {
		t.Errorf("HTML export missing user prompt or tool name")
	}

	// 2. Export JSONL
	m.entries = m.entries[:3]
	jsonlFile := filepath.Join(tmpDir, "data.jsonl")
	m.handleSlashCommand("/export " + jsonlFile)
	jsonlBytes, err := os.ReadFile(jsonlFile)
	if err != nil {
		t.Fatalf("failed to read jsonl export: %v", err)
	}
	jsonlLines := strings.Split(strings.TrimSpace(string(jsonlBytes)), "\n")
	if len(jsonlLines) != 4 { // 1 metadata + 3 entries
		t.Fatalf("expected 4 jsonl lines, got %d", len(jsonlLines))
	}
	var meta map[string]interface{}
	if err := json.Unmarshal([]byte(jsonlLines[0]), &meta); err != nil {
		t.Fatalf("failed to parse metadata json line: %v", err)
	}
	if meta["type"] != "metadata" || meta["title"] != "Multi-Format Export Test" {
		t.Errorf("unexpected metadata record: %+v", meta)
	}
	var userRec map[string]interface{}
	if err := json.Unmarshal([]byte(jsonlLines[1]), &userRec); err != nil {
		t.Fatalf("failed to parse user json line: %v", err)
	}
	if userRec["role"] != "user" || userRec["content"] != "Generate a sorting function" {
		t.Errorf("unexpected user record: %+v", userRec)
	}
}

func TestRewindPreviewAndRestoration(t *testing.T) {
	tmpDir := t.TempDir()
	projectConfig := `{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"models":{"primary":{"provider":"host","model":"test-model"}},"defaultModel":"primary"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: tmpDir})
	if err != nil {
		t.Fatalf("failed to create core: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	// Create session with 2 turns of history
	historyJSON := `[
		{"type":"message","role":"user","content":"Initial prompt turn 1"},
		{"type":"message","role":"assistant","content":"Assistant reply turn 1"},
		{"type":"message","role":"user","content":"Detailed prompt turn 2"},
		{"type":"message","role":"assistant","content":"Assistant reply turn 2"}
	]`
	sess, err := client.CreateSession(ctx, json.RawMessage(historyJSON))
	if err != nil {
		t.Fatal(err)
	}

	m := newTUIModel(client, nil, sess.ID)
	m.width = 100
	m.height = 30
	m.entries = transcriptFromHistory(json.RawMessage(historyJSON))
	for _, entry := range m.entries {
		if entry.Kind == KindUser && entry.Text != "" {
			m.promptHistory = append(m.promptHistory, entry.Text)
		}
	}
	m.historyIndex = len(m.promptHistory)
	m.updateLayout()

	// 1. Preview mode does not delete history
	mod, _ := m.handleSlashCommand("/rewind preview")
	m = mod.(*tuiModel)
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Rewind Preview") || !strings.Contains(lastSys, "Detailed prompt turn 2") {
		t.Fatalf("expected rewind preview with turn 2 details, got: %s", lastSys)
	}

	// 2. Execute rewind 1 turn: removes turn 2, restores "Detailed prompt turn 2" to composer
	mod, _ = m.handleSlashCommand("/rewind 1")
	m = mod.(*tuiModel)
	if m.composer.Value() != "Detailed prompt turn 2" {
		t.Fatalf("expected composer to restore 'Detailed prompt turn 2', got %q", m.composer.Value())
	}
	lastSys2 := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys2, "Rewound 1 turn(s)") {
		t.Fatalf("expected rewind success message, got: %s", lastSys2)
	}

	// 3. User turns left should be 1
	userTurnCount := 0
	for _, entry := range m.entries {
		if entry.Kind == KindUser {
			userTurnCount++
		}
	}
	if userTurnCount != 1 {
		t.Fatalf("expected 1 user turn remaining after rewind, got %d", userTurnCount)
	}
}

func TestInterruptCheckpointCardAndBanner(t *testing.T) {
	tmpDir := t.TempDir()
	projectConfig := `{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"models":{"primary":{"provider":"host","model":"test-model"}},"defaultModel":"primary"}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: tmpDir})
	if err != nil {
		t.Fatalf("failed to create core: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	sess, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	rt, err := client.OpenSessionRuntime(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}

	m := newTUIModel(client, rt, sess.ID)
	m.width = 100
	m.height = 30
	m.updateLayout()

	// Simulate turn with user input and tool calls
	m.entries = append(m.entries, TranscriptEntry{
		ID:        "u1",
		Kind:      KindUser,
		Text:      "Refactor the parser",
		Timestamp: time.Now(),
	})
	m.entries = append(m.entries, TranscriptEntry{
		ID:          "t1",
		Kind:        KindTool,
		ToolCallID:  "call-read",
		ToolName:    "read_file",
		ToolCommand: "read_file parser.go",
		ToolStatus:  "success",
		Timestamp:   time.Now(),
	})
	m.entries = append(m.entries, TranscriptEntry{
		ID:          "t2",
		Kind:        KindTool,
		ToolCallID:  "call-cmd",
		ToolName:    "run_command",
		ToolCommand: "go test ./...",
		ToolStatus:  "running",
		Timestamp:   time.Now(),
	})

	m.running = true
	m.runningExecution = "exec-test-int-001"
	m.currentReasoningText = "Analyzing test failures..."
	m.reasoningStartTime = time.Now().Add(-2 * time.Second)

	// Press Esc to interrupt
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)

	if m.running {
		t.Fatalf("expected m.running to be false after Esc interrupt")
	}
	if m.pausedCheckpoint == nil {
		t.Fatalf("expected m.pausedCheckpoint to be non-nil after interrupt")
	}
	if m.pausedCheckpoint.ExecutionID != "exec-test-int-001" {
		t.Errorf("expected ExecutionID 'exec-test-int-001', got %q", m.pausedCheckpoint.ExecutionID)
	}
	if m.pausedCheckpoint.ToolCount != 2 {
		t.Errorf("expected ToolCount 2, got %d", m.pausedCheckpoint.ToolCount)
	}
	if m.pausedCheckpoint.LastToolName != "run_command" {
		t.Errorf("expected LastToolName 'run_command', got %q", m.pausedCheckpoint.LastToolName)
	}
	if !m.hasPausedTask() {
		t.Fatalf("expected hasPausedTask() to be true")
	}

	// Verify checkpoint card in transcript
	lastEntry := m.entries[len(m.entries)-1]
	if lastEntry.Kind != KindSystem {
		t.Fatalf("expected last entry to be KindSystem checkpoint card, got %v", lastEntry.Kind)
	}
	cardText := lastEntry.Text
	if !strings.Contains(cardText, "Task Execution Paused (Checkpoint Saved)") {
		t.Errorf("expected checkpoint title in card, got: %s", cardText)
	}
	if !strings.Contains(cardText, "exec-test-int-001") {
		t.Errorf("expected execution ID in card, got: %s", cardText)
	}
	if !strings.Contains(cardText, "2 tool step(s) completed") || !strings.Contains(cardText, "run_command") {
		t.Errorf("expected completed tools summary in card, got: %s", cardText)
	}
	if !strings.Contains(cardText, "/resume") || !strings.Contains(cardText, "/discard") {
		t.Errorf("expected /resume and /discard hints in card, got: %s", cardText)
	}

	// Verify View contains the paused task banner in composer gap
	viewOutput := ansi.Strip(m.View().Content)
	if !strings.Contains(viewOutput, "Task Paused (Checkpoint saved)") {
		t.Errorf("expected View() to render paused task banner, got:\n%s", viewOutput)
	}

	// Verify footer shows paused task indicator
	footerText := ansi.Strip(renderFooter(m, 100))
	if !strings.Contains(footerText, "task paused · /resume or /discard") {
		t.Errorf("expected footer to show paused task hint, got: %s", footerText)
	}

	// Discard paused task
	mod, _ = m.handleSlashCommand("/discard")
	m = mod.(*tuiModel)
	if m.pausedCheckpoint != nil {
		t.Fatalf("expected pausedCheckpoint to be nil after /discard")
	}
	if m.hasPausedTask() {
		t.Fatalf("expected hasPausedTask() to be false after /discard")
	}
}

func TestActiveRecoveryCountdownAndShortcuts(t *testing.T) {
	// 1. Recovery panel with active cool-down
	recActive := &ActiveRecovery{
		ExecutionID:    "exec-rec-1",
		CallID:         "call-rec-1",
		Reason:         "rate_limit (status=429)",
		RetryNotBefore: time.Now().Add(25 * time.Second),
		Selected:       0,
	}
	renderedActive := ansi.Strip(renderRecoveryPanel(recActive, 80))
	if !strings.Contains(renderedActive, "Model Recovery Required") {
		t.Errorf("expected title in recovery panel, got: %s", renderedActive)
	}
	if !strings.Contains(renderedActive, "Rate limit cool-down:") {
		t.Errorf("expected cool-down countdown line, got: %s", renderedActive)
	}
	if !strings.Contains(renderedActive, "[") || !strings.Contains(renderedActive, "]") {
		t.Errorf("expected countdown progress bar in recovery panel, got: %s", renderedActive)
	}
	if !strings.Contains(renderedActive, "1 retry") || !strings.Contains(renderedActive, "r / d quick action") {
		t.Errorf("expected quick action hints in recovery panel, got: %s", renderedActive)
	}

	// 2. Recovery panel with elapsed cool-down
	recElapsed := &ActiveRecovery{
		ExecutionID:    "exec-rec-2",
		CallID:         "call-rec-2",
		Reason:         "rate_limit (status=429)",
		RetryNotBefore: time.Now().Add(-5 * time.Second),
		Selected:       0,
	}
	renderedElapsed := ansi.Strip(renderRecoveryPanel(recElapsed, 80))
	if !strings.Contains(renderedElapsed, "Cool-down elapsed — Ready to resume execution") {
		t.Errorf("expected cool-down elapsed banner, got: %s", renderedElapsed)
	}

	// 3. Shortcuts testing
	m := newTUIModel(nil, nil, "sess-rec")
	m.width = 80
	m.height = 24
	m.activeRecovery = &ActiveRecovery{
		ExecutionID: "exec-rec-keys",
		CallID:      "call-rec-keys",
		Selected:    0,
	}

	// Key 'j' (down) moves to discard
	m.handleKeyPress(tea.KeyPressMsg{Code: 'j'})
	if m.activeRecovery.Selected != 1 {
		t.Errorf("expected Selected=1 after 'j', got %d", m.activeRecovery.Selected)
	}
	// Key 'k' (up) moves back to retry
	m.handleKeyPress(tea.KeyPressMsg{Code: 'k'})
	if m.activeRecovery.Selected != 0 {
		t.Errorf("expected Selected=0 after 'k', got %d", m.activeRecovery.Selected)
	}

	// Esc discards recovery
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.activeRecovery != nil {
		t.Fatalf("expected activeRecovery to be cleared after Esc")
	}
}

func TestActiveToolRecoveryDetailsAndShortcuts(t *testing.T) {
	rec := &ActiveToolRecovery{
		InvocationID: "inv-test-err",
		CallID:       "call-test-err",
		ToolName:     "run_command",
		Arguments:    `{"command":"npm run build"}`,
		ErrorOutput:  "Exit code 1\nSyntaxError: Unexpected token '<' in bundle.js\n  at build (compiler.js:42)",
		Selected:     0,
	}

	rendered := ansi.Strip(renderToolRecoveryPanel(rec, 90))
	if !strings.Contains(rendered, "Tool Failure Recovery Required") {
		t.Errorf("expected title in modal, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Tool: run_command") {
		t.Errorf("expected tool name in modal, got: %s", rendered)
	}
	if !strings.Contains(rendered, "Failure Details:") {
		t.Errorf("expected Failure Details section, got: %s", rendered)
	}
	if !strings.Contains(rendered, "SyntaxError: Unexpected token") {
		t.Errorf("expected error details in modal, got: %s", rendered)
	}
	if !strings.Contains(rendered, "1 retry") || !strings.Contains(rendered, "r / s quick action") {
		t.Errorf("expected quick action hints in modal, got: %s", rendered)
	}

	// Navigation & selection
	m := newTUIModel(nil, nil, "sess-tool-rec")
	m.width = 80
	m.height = 24
	m.activeToolRecovery = rec

	// Press 'j'
	m.handleKeyPress(tea.KeyPressMsg{Code: 'j'})
	if m.activeToolRecovery.Selected != 1 {
		t.Errorf("expected Selected=1 after 'j', got %d", m.activeToolRecovery.Selected)
	}
	// Press 'k'
	m.handleKeyPress(tea.KeyPressMsg{Code: 'k'})
	if m.activeToolRecovery.Selected != 0 {
		t.Errorf("expected Selected=0 after 'k', got %d", m.activeToolRecovery.Selected)
	}
}

func TestPlanImplementationUsesRuntimeStatus(t *testing.T) {
	m := newTUIModel(nil, nil, "plan-sess")
	m.workspace = t.TempDir()
	m.planMode = true
	m.savedMode = core.PermissionAsk
	m.activePlanReview = &ActivePlanReview{ExecutionID: "plan-review", Text: "1. Read code\n2. Run tests", Selected: 0}
	_, cmd := m.resolveActivePlanReview()
	if cmd == nil || m.activePlanReview != nil || m.planMode || m.permissionMode != core.PermissionAsk {
		t.Fatal("plan implementation must dismiss review and restore the previous permission mode")
	}
	m.Update(planImplementedMsg{impl: core.PlanImplementation{Submission: core.TurnSubmission{ExecutionID: "implementation"}}, planText: "1. Read code\n2. Run tests"})
	if !m.running || m.runningExecution != "implementation" {
		t.Fatal("implementation must use the submitted runtime execution")
	}
	m.handleRuntimeEvent(core.Event{Kind: core.EventModelStream, Stream: &core.ModelStreamEvent{Type: "text_delta", Delta: "We will consider Step 2 next."}})
	if !strings.Contains(m.currentStreamText, "Step 2") {
		t.Fatal("plan text must still stream normally")
	}
	m.Update(turnFinishedMsg{result: core.TurnResult{Status: core.TaskCompleted}})
	if m.running {
		t.Fatal("runtime completion must end the running state")
	}
	for _, entry := range m.entries {
		if strings.Contains(entry.Text, "steps completed") || strings.Contains(entry.Text, "Completed Checklist") {
			t.Fatalf("task completion must not claim inferred step completion: %s", entry.Text)
		}
	}
	files, err := os.ReadDir(filepath.Join(m.workspace, ".luneagent", "plans"))
	if err != nil || len(files) != 1 {
		t.Fatalf("approved plan must remain archived: %v", err)
	}
}

func TestDiffModalTabBarAndNavigation(t *testing.T) {
	files := []DiffFile{
		{Path: "internal/tui/diff.go", Additions: 42, Deletions: 5, Lines: []string{"+line1", "+line2"}},
		{Path: "internal/tui/view.go", Additions: 10, Deletions: 2, Lines: []string{"+view1", "-view2"}},
		{Path: "cmd/main.go", Additions: 3, Deletions: 0, Lines: []string{"+main1"}},
	}

	// 1. Test computeDiffStats
	totAdds, totDels := computeDiffStats(files)
	if totAdds != 55 || totDels != 7 {
		t.Errorf("computeDiffStats = (+%d, -%d), want (+55, -7)", totAdds, totDels)
	}

	// 2. Test renderDiffTabBar
	tabBar := renderDiffTabBar(files, 1, 80)
	strippedTab := ansi.Strip(tabBar)
	if !strings.Contains(strippedTab, "1.diff.go +42/-5") || !strings.Contains(strippedTab, "2.view.go +10/-2") {
		t.Errorf("expected tab bar to render file tabs, got: %s", strippedTab)
	}

	m := newTUIModel(nil, nil, "diff-test-sess")
	m.width = 100
	m.height = 30
	m.diffModalOpen = true
	m.diffFiles = files
	m.diffFileIndex = 0

	// Press '2' -> jumps to file index 1 (view.go)
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: '2'}))
	m = mod.(*tuiModel)
	if m.diffFileIndex != 1 {
		t.Errorf("expected diffFileIndex=1 after pressing '2', got %d", m.diffFileIndex)
	}

	// Press '3' -> jumps to file index 2 (main.go)
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '3'}))
	m = mod.(*tuiModel)
	if m.diffFileIndex != 2 {
		t.Errorf("expected diffFileIndex=2 after pressing '3', got %d", m.diffFileIndex)
	}

	// The diff viewer remains read-only when the old commit shortcut is pressed.
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'm'}))
	m = mod.(*tuiModel)
	if !m.diffModalOpen || m.composer.Value() != "" {
		t.Fatal("diff viewer must not draft a Git write command")
	}
}

func TestRemovedGitCommands(t *testing.T) {
	for _, command := range []string{"/commit", "/push"} {
		m := newTUIModel(nil, nil, "read-only")
		_, cmd := m.handleSlashCommand(command)
		if cmd != nil {
			t.Fatalf("%s must not execute a command", command)
		}
		for _, item := range builtinCommands {
			if item.Command == command {
				t.Fatalf("removed command is still advertised: %s", command)
			}
		}
	}
}

func TestMultiAgentStreamIsolation(t *testing.T) {
	m := newTUIModel(nil, nil, "multi-agent-sess")
	m.running = true

	// 1. Subagent starts and streams reasoning + text
	m.handleRuntimeEvent(core.Event{
		Kind:      core.EventAgentStarted,
		AgentID:   "agent-1",
		AgentName: "cli_tui_auditor",
		AgentRole: "TUI Auditor",
	})
	m.handleRuntimeEvent(core.Event{
		Kind:      core.EventModelStream,
		AgentID:   "agent-1",
		AgentName: "cli_tui_auditor",
		Stream: &core.ModelStreamEvent{
			Type:  "reasoning_delta",
			Delta: "Subagent secret thought",
		},
	})
	m.handleRuntimeEvent(core.Event{
		Kind:      core.EventModelStream,
		AgentID:   "agent-1",
		AgentName: "cli_tui_auditor",
		Stream: &core.ModelStreamEvent{
			Type:  "text_delta",
			Delta: "Subagent report line 1\nSubagent report line 2",
		},
	})

	// Lead buffers must remain untouched by subagent events
	if m.currentReasoningText != "" {
		t.Fatalf("expected lead currentReasoningText to be empty, got %q", m.currentReasoningText)
	}
	if m.currentStreamText != "" {
		t.Fatalf("expected lead currentStreamText to be empty, got %q", m.currentStreamText)
	}

	// 2. Background memory stream is also ignored by lead stream
	m.handleRuntimeEvent(core.Event{
		Kind:    core.EventModelStream,
		AgentID: "lead",
		Stream: &core.ModelStreamEvent{
			Purpose: string(core.UsagePurposeMemory),
			Type:    "text_delta",
			Delta:   "internal memory json",
		},
	})
	if m.currentStreamText != "" {
		t.Fatalf("expected memory purpose delta to be ignored, got %q", m.currentStreamText)
	}

	// 3. Lead agent streams its own reasoning and text
	m.handleRuntimeEvent(core.Event{
		Kind:    core.EventModelStream,
		AgentID: "lead",
		Stream: &core.ModelStreamEvent{
			Type:  "reasoning_delta",
			Delta: "Lead thought part 1. ",
		},
	})
	m.handleRuntimeEvent(core.Event{
		Kind:    core.EventModelStream,
		AgentID: "lead",
		Stream: &core.ModelStreamEvent{
			Type:  "reasoning_delta",
			Delta: "Lead thought part 2.",
		},
	})
	m.handleRuntimeEvent(core.Event{
		Kind:    core.EventModelStream,
		AgentID: "lead",
		Stream: &core.ModelStreamEvent{
			Type:  "text_delta",
			Delta: "Final lead response.",
		},
	})
	m.handleRuntimeEvent(core.Event{
		Kind:      core.EventAgentFinished,
		AgentID:   "agent-1",
		AgentName: "cli_tui_auditor",
	})
	m.finalizeTurnOutput("", nil, "exec-1")

	var subagentEntries, reasoningEntries, assistantEntries []TranscriptEntry
	for _, e := range m.entries {
		switch e.Kind {
		case KindSubagent:
			subagentEntries = append(subagentEntries, e)
		case KindReasoning:
			reasoningEntries = append(reasoningEntries, e)
		case KindAssistant:
			assistantEntries = append(assistantEntries, e)
		}
	}

	if len(subagentEntries) != 1 {
		t.Fatalf("expected 1 subagent entry, got %d", len(subagentEntries))
	}
	if subagentEntries[0].SubagentRunning {
		t.Errorf("expected subagent entry to be marked finished")
	}
	if subagentEntries[0].SubagentPreview != "Subagent report line 2" {
		t.Errorf("unexpected subagent preview: %q", subagentEntries[0].SubagentPreview)
	}
	if len(reasoningEntries) != 1 || reasoningEntries[0].Text != "Lead thought part 1. Lead thought part 2." {
		t.Errorf("expected 1 merged lead reasoning entry, got %+v", reasoningEntries)
	}
	if len(assistantEntries) != 1 || assistantEntries[0].Text != "Final lead response." {
		t.Errorf("expected 1 clean lead assistant entry, got %+v", assistantEntries)
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

func TestActiveQuestionCustomAndNotesInput(t *testing.T) {
	m := newTUIModel(nil, nil, "q-sess")
	m.activeQuestion = &ActiveQuestion{
		Request: core.UserQuestionRequest{
			ID: "q-req-1",
			Questions: []core.UserQuestionItem{
				{
					ID:       "q1",
					Question: "Choose an option",
					Options: []core.UserQuestionOption{
						{Label: "Opt A"},
					},
				},
			},
		},
		Selected: 1, // "None of the above"
	}

	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'h', Text: "h"}))
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'i', Text: "i"}))
	m = mod.(*tuiModel)
	if m.activeQuestion.Custom != "hi" {
		t.Fatalf("expected Custom answer 'hi', got %q", m.activeQuestion.Custom)
	}

	// Toggle notes with Tab and type into NoteText
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = mod.(*tuiModel)
	if !m.activeQuestion.NotesOpen {
		t.Fatalf("expected NotesOpen to be true after Tab")
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"}))
	m = mod.(*tuiModel)
	if m.activeQuestion.NoteText != "n" {
		t.Fatalf("expected NoteText 'n', got %q", m.activeQuestion.NoteText)
	}
}

func TestCodexExecutionTimelineAlignment(t *testing.T) {
	// 1. File change card: "• Added .desktop_rework.py (+3 -0)" with line-numbered diff preview and "+ Show details"
	addedEntry := TranscriptEntry{
		Kind:     KindTool,
		ToolName: "write_file",
		ToolChanges: []publictool.FileChange{
			{
				Path:  ".desktop_rework.py",
				Added: 3,
				Lines: []publictool.DiffLine{
					{Kind: "add", NewLine: 1, Text: "from pathlib import Path"},
					{Kind: "add", NewLine: 2, Text: "import re"},
					{Kind: "add", NewLine: 3, Text: "p=Path('internal/desktop/service.go')"},
				},
			},
		},
	}
	renderedAdded := ansi.Strip(renderEntry(addedEntry, 100))
	if !strings.Contains(renderedAdded, "• Added .desktop_rework.py (+3 -0)") {
		t.Fatalf("expected Codex Added file header, got:\n%s", renderedAdded)
	}
	if !strings.Contains(renderedAdded, "1 +from pathlib import Path") || !strings.Contains(renderedAdded, "+ Show details") {
		t.Fatalf("expected inline diff preview and + Show details, got:\n%s", renderedAdded)
	}

	// 2. Command tail preview: shows the LAST 3 lines of output (e.g. FAIL summary / traceback)
	exitOne := 1
	failedEntry := TranscriptEntry{
		Kind:         KindTool,
		ToolName:     "run_command",
		ToolCommand:  "go test ./internal/desktop",
		ToolOutput:   "line 1 setup\nline 2 compile\nline 3 check\ninternal\\desktop\\service.go:83:6: too many errors\nFAIL\tgithub.com/afterlune/lune/internal/desktop [build failed]\nFAIL",
		ToolExitCode: &exitOne,
	}
	renderedFailed := ansi.Strip(renderEntry(failedEntry, 100))
	if !strings.Contains(renderedFailed, "Failed (exit 1)") {
		t.Fatalf("expected Failed (exit 1), got:\n%s", renderedFailed)
	}
	if strings.Contains(renderedFailed, "line 1 setup") {
		t.Fatalf("expected collapsed preview to show tail lines rather than line 1, got:\n%s", renderedFailed)
	}
	if !strings.Contains(renderedFailed, "[build failed]") || !strings.Contains(renderedFailed, "+ 3 lines (ctrl+t to expand)") {
		t.Fatalf("expected tail error lines and expand hint, got:\n%s", renderedFailed)
	}

	// 3. Borderless horizontal-rule Markdown table
	mdTable := "| 领域 | 功能 |\n| --- | --- |\n| 会话 | 多会话并行、历史搜索 |\n| 对话 | 流式回复、消息排队 |"
	renderedTable := ansi.Strip(RenderMarkdown(mdTable, 80))
	if strings.Contains(renderedTable, "╭") || strings.Contains(renderedTable, "│") {
		t.Fatalf("expected borderless horizontal-rule table without vertical box borders, got:\n%s", renderedTable)
	}
	if !strings.Contains(renderedTable, "领域") || !strings.Contains(renderedTable, "────") || !strings.Contains(renderedTable, "多会话并行") {
		t.Fatalf("expected table headers, horizontal dividers, and cell text, got:\n%s", renderedTable)
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

func TestSubagentTrackingAndCommands(t *testing.T) {
	m := &tuiModel{
		running:       true,
		taskStartedAt: time.Now().Add(-10 * time.Second),
	}

	// 1. Subagent tool event routing: subagent tools do not mix into lead agent tools
	m.handleRuntimeEvent(core.Event{
		Kind:      core.EventAgentStarted,
		AgentID:   "researcher-1",
		AgentName: "code_researcher",
		AgentRole: "Codebase Analyst",
	})
	m.handleRuntimeEvent(core.Event{
		Kind:      core.EventToolStarted,
		AgentID:   "researcher-1",
		AgentName: "code_researcher",
		Tool: core.ToolCall{
			ID:        "call-1",
			Name:      "read_file",
			Arguments: []byte(`{"path":"internal/tui/theme.go"}`),
		},
	})
	m.handleRuntimeEvent(core.Event{
		Kind:    core.EventToolFinished,
		AgentID: "researcher-1",
		Tool: core.ToolCall{
			ID:       "call-1",
			Name:     "read_file",
			Duration: 250 * time.Millisecond,
		},
	})

	// Check that no KindTool entries were added to lead entries
	for _, e := range m.entries {
		if e.Kind == KindTool {
			t.Fatalf("expected no KindTool in lead entries from subagent event, found: %+v", e)
		}
	}

	// Check subagent entry updated properly
	subs := m.getSubagentEntries()
	if len(subs) != 1 {
		t.Fatalf("expected 1 subagent entry, got %d", len(subs))
	}
	if subs[0].SubagentTools != 1 || subs[0].SubagentLastTool != "read_file" {
		t.Fatalf("unexpected subagent tools: count=%d, last=%s", subs[0].SubagentTools, subs[0].SubagentLastTool)
	}

	// 2. Subagent summary in Working indicator
	summary := m.subagentsStatusSummary()
	if !strings.Contains(summary, "1 subagent running") {
		t.Fatalf("expected '1 subagent running', got %q", summary)
	}
	workingBlock := renderWorking(10*time.Second, false, summary)
	if !strings.Contains(workingBlock, "1 subagent running") {
		t.Fatalf("expected workingBlock to contain subagent summary, got:\n%s", workingBlock)
	}

	// 3. Subagent badge in footer
	footer := ansi.Strip(renderFooter(m, 120))
	if !strings.Contains(footer, "subagent") || !strings.Contains(footer, "/agents") {
		t.Fatalf("expected footer to show subagent count and /agents hint, got:\n%s", footer)
	}

	// 4. /agents command table view
	m.handleSlashCommand("/agents")
	lastEntry := m.entries[len(m.entries)-1]
	if lastEntry.Kind != KindSystem || !strings.Contains(lastEntry.Text, "Subagents in Session (1 total)") {
		t.Fatalf("expected /agents table in system entry, got:\n%s", lastEntry.Text)
	}

	// 5. /agents 1 detail view
	m.handleSlashCommand("/agents 1")
	lastDetail := m.entries[len(m.entries)-1]
	if !strings.Contains(lastDetail.Text, "Subagent Details: code_researcher") || !strings.Contains(lastDetail.Text, "Tools Executed: 1") {
		t.Fatalf("expected /agents detail view, got:\n%s", lastDetail.Text)
	}
}

func TestPlanTagStripping(t *testing.T) {
	raw := "Here is the proposal:\n<proposed_plan>\n1. Step One\n2. Step Two\n</proposed_plan>\nLet me know!"
	extracted, cleaned := extractProposedPlan(raw)
	if strings.Contains(extracted, "<proposed_plan>") || strings.Contains(extracted, "</proposed_plan>") {
		t.Fatalf("extracted plan contains raw tags: %q", extracted)
	}
	if !strings.Contains(extracted, "1. Step One") {
		t.Fatalf("expected plan content in extracted, got: %q", extracted)
	}
	if strings.Contains(cleaned, "<proposed_plan>") || strings.Contains(cleaned, "</proposed_plan>") {
		t.Fatalf("cleaned surrounding contains raw tags: %q", cleaned)
	}

	stripped := stripProposedPlan(raw)
	if strings.Contains(stripped, "<proposed_plan>") || strings.Contains(stripped, "</proposed_plan>") {
		t.Fatalf("stripProposedPlan leaked raw tags: %q", stripped)
	}

	// Test renderEntry for KindPlan and KindAssistant
	planEntry := TranscriptEntry{
		Kind: KindPlan,
		Text: "<proposed_plan>\nImplement auth service\n</proposed_plan>",
	}
	renderedPlan := ansi.Strip(renderEntry(planEntry, 80))
	if strings.Contains(renderedPlan, "<proposed_plan>") || strings.Contains(renderedPlan, "</proposed_plan>") {
		t.Fatalf("renderEntry(KindPlan) leaked plan tags: %q", renderedPlan)
	}
	if !strings.Contains(renderedPlan, "Implement auth service") {
		t.Fatalf("renderEntry(KindPlan) missing text: %q", renderedPlan)
	}

	asstEntry := TranscriptEntry{
		Kind: KindAssistant,
		Text: "Notice <proposed_plan> in text</proposed_plan>",
	}
	renderedAsst := ansi.Strip(renderEntry(asstEntry, 80))
	if strings.Contains(renderedAsst, "<proposed_plan>") || strings.Contains(renderedAsst, "</proposed_plan>") {
		t.Fatalf("renderEntry(KindAssistant) leaked plan tags: %q", renderedAsst)
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

func TestFooterLiveTokenDiagnostics(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.estimatedInputTokens = 12400
	m.contextWindow = 200000
	m.totalInputTokens = 15000
	m.cachedTokens = 12750 // 85% cache
	m.totalOutputTokens = 9500
	m.lastTokensPerSec = 48.5

	summary := formatContextAndTokenSummary(m, 120)
	if !strings.Contains(summary, "Context: 12.4k/200k (6.2%)") {
		t.Fatalf("expected used/total ctx in summary, got: %q", summary)
	}
	if !strings.Contains(summary, "Cache: 85%") {
		t.Fatalf("expected cache 85%% in summary, got: %q", summary)
	}
	if !strings.Contains(summary, "48.5 tok/s") {
		t.Fatalf("expected 48.5 tok/s in summary, got: %q", summary)
	}
	if !strings.Contains(summary, "Total: 24.5k tok") {
		t.Fatalf("expected Total: 24.5k tok in summary, got: %q", summary)
	}

	footer := ansi.Strip(renderFooter(m, 120))
	if !strings.Contains(footer, "Context: 12.4k/200k (6.2%)") {
		t.Fatalf("expected footer line 2 to show context metrics, got:\n%s", footer)
	}
	if !strings.Contains(footer, "48.5 tok/s") || !strings.Contains(footer, "Total: 24.5k tok") {
		t.Fatalf("expected footer line 2 to show speed and total, got:\n%s", footer)
	}
}

func TestTerminalTitleUpdates(t *testing.T) {
	var buf strings.Builder
	m := newTUIModel(nil, nil, "sess-1")
	m.output = &buf

	// 1. Initial untitled session: title is "lune"
	if got := m.desiredTerminalTitle(); got != "lune" {
		t.Fatalf("expected initial title 'lune', got: %q", got)
	}
	m.syncTerminalTitle()
	if !strings.Contains(buf.String(), "lune") {
		t.Fatalf("expected OSC sequence to contain 'lune', got: %q", buf.String())
	}

	// 2. Set title: title becomes "<title> | lune"
	buf.Reset()
	m.sessionTitle = "Refactor Architecture"
	if got := m.desiredTerminalTitle(); got != "Refactor Architecture | lune" {
		t.Fatalf("expected title 'Refactor Architecture | lune', got: %q", got)
	}
	m.syncTerminalTitle()
	if !strings.Contains(buf.String(), "Refactor Architecture | lune") {
		t.Fatalf("expected OSC sequence to contain 'Refactor Architecture | lune', got: %q", buf.String())
	}
}

func TestModelDisplayName(t *testing.T) {
	m := newTUIModel(nil, nil, "sess-1")
	m.currentModel = "claude-3-7-sonnet"

	// When no client or profiles loaded, fallback to currentModel
	if got := m.currentModelDisplayName(); got != "claude-3-7-sonnet" {
		t.Fatalf("expected fallback model name, got: %q", got)
	}
}

func TestContextMetricsUseLatestPromptNotCumulative(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.contextWindow = 1000000
	m.lastInputTokens = 42000
	m.totalInputTokens = 794000
	m.totalOutputTokens = 12000
	m.cachedTokens = 754300 // ~95%
	m.lastTokensPerSec = 20.4

	summary := formatContextAndTokenSummary(m, 120)
	if !strings.Contains(summary, "Context: 42k/1M (4.2%)") {
		t.Fatalf("expected Context to use lastInputTokens (42k/1M (4.2%%)), got: %q", summary)
	}
	if !strings.Contains(summary, "Total: 806k tok") {
		t.Fatalf("expected Total to show cumulative sum (806k tok), got: %q", summary)
	}
	if !strings.Contains(summary, "Cache: 95%") {
		t.Fatalf("expected Cache: 95%%, got: %q", summary)
	}
}

func TestEffortPickerPersistsWithoutSwitchingModelProfile(t *testing.T) {
	tmpDir := t.TempDir()
	projectConfig := `{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"models":{"alpha":{"provider":"host","model":"shared-model","displayName":"Alpha"},"beta":{"provider":"host","model":"shared-model","displayName":"Beta"}},"defaultModel":"alpha","maxParallelAgents":8}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: tmpDir})
	if err != nil {
		t.Fatalf("failed to create core: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	sess, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	m := newTUIModel(client, nil, sess.ID)

	// Both profiles run the same model ID; the footer must still identify the
	// active profile, not whichever profile happens to sort first.
	m.handleSlashCommand("/model beta")
	if m.currentModelProfile != "beta" {
		t.Fatalf("expected active profile beta, got %q", m.currentModelProfile)
	}
	if got := m.currentModelDisplayName(); got != "Beta" {
		t.Fatalf("expected display name Beta, got %q", got)
	}

	m.handleSlashCommand("/effort")
	for i := 0; i < 5; i++ {
		m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})

	saved, err := client.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Settings.ModelProfile != "beta" {
		t.Fatalf("effort change must not switch model profile, got %q", saved.Settings.ModelProfile)
	}
	if saved.Settings.ReasoningEffort != "high" {
		t.Fatalf("expected persisted effort high, got %q", saved.Settings.ReasoningEffort)
	}
	footer := ansi.Strip(renderFooter(m, 300))
	if !strings.Contains(footer, "Beta [effort: high]") {
		t.Fatalf("expected footer to show model and effort, got:\n%s", footer)
	}
	if !strings.Contains(footer, "🤖 0 used · 8 max subagents") {
		t.Fatalf("expected footer to show configured subagent limit, got:\n%s", footer)
	}
}

func TestEffortPickerRendering(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.reasoningEffort = "high"
	m.effortPicker = &ActiveEffortPicker{Selected: 5}
	rendered := ansi.Strip(renderEffortPicker(m, 80))
	for _, want := range []string{"Reasoning effort", "6. high", "✓ current", "enter apply"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected effort picker to contain %q, got:\n%s", want, rendered)
		}
	}
}

func TestFooterSubagentsAlwaysVisible(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	footer := ansi.Strip(renderFooter(m, 120))
	if !strings.Contains(footer, "🤖 0 used · 4 max subagents") {
		t.Fatalf("expected footer to show 🤖 0 used · 4 max subagents when idle, got:\n%s", footer)
	}
}

func TestFooterSubagentCountReportsSessionUsage(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.entries = []TranscriptEntry{
		{Kind: KindSubagent, AgentID: "a", AgentName: "researcher", SubagentRunning: false},
		{Kind: KindSubagent, AgentID: "b", AgentName: "reviewer", SubagentRunning: true},
	}
	m.width = 140
	footer := ansi.Strip(renderFooter(m, 140))
	if !strings.Contains(footer, "🤖 2 used · 4 max subagents") {
		t.Fatalf("expected footer to show 2 used · 4 max subagents, got:\n%s", footer)
	}
}

func TestFooterShowsReasoningEffortBesideModel(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.currentModel = "deepseek-flash"
	m.currentModelProfile = "deepseek-flash"
	m.reasoningEffort = "high"
	m.width = 140
	footer := ansi.Strip(renderFooter(m, 140))
	if !strings.Contains(footer, "deepseek-flash [effort: high]") {
		t.Fatalf("expected reasoning effort beside the model name, got:\n%s", footer)
	}

	m.reasoningEffort = ""
	footer = ansi.Strip(renderFooter(m, 140))
	if !strings.Contains(footer, "deepseek-flash [effort: default]") {
		t.Fatalf("expected default effort label, got:\n%s", footer)
	}
}

func TestGreetingWithDanglingPlanTagDoesNotOpenPlanReview(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.currentStreamText = "你好！我是 Plan 模式下的规划助手。你说明目标后，我会先阅读相关代码，再用 <proposed_plan> 给你完整方案。"
	m.finalizeTurnOutput("", nil, "exec-1")

	if m.activePlanReview != nil {
		t.Fatalf("expected no plan review for a greeting, got: %+v", m.activePlanReview)
	}
	var assistantText string
	for _, entry := range m.entries {
		if entry.Kind == KindPlan {
			t.Fatalf("expected no plan entry for a greeting, got: %q", entry.Text)
		}
		if entry.Kind == KindAssistant {
			assistantText = entry.Text
		}
	}
	if strings.Contains(assistantText, "<proposed_plan>") {
		t.Fatalf("expected raw plan tag to be removed, got: %q", assistantText)
	}
	if !strings.Contains(assistantText, "给你完整方案。") {
		t.Fatalf("expected the reply's prose to be preserved, got: %q", assistantText)
	}
}

func TestCompleteCoreProposedPlanOpensPlanReview(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	plan := "## 实施步骤\n1. 修改 runtime.go\n2. 运行测试"
	m.finalizeTurnOutput(plan, nil, "exec-2")

	if m.activePlanReview == nil {
		t.Fatalf("expected a plan review for a complete Core-provided plan")
	}
	if m.activePlanReview.Text != plan {
		t.Fatalf("unexpected plan review text: %q", m.activePlanReview.Text)
	}
	foundPlan := false
	for _, entry := range m.entries {
		if entry.Kind == KindPlan && entry.Text == plan {
			foundPlan = true
		}
	}
	if !foundPlan {
		t.Fatalf("expected a plan transcript entry, got: %+v", m.entries)
	}
}

func TestPlanMarkupInTextWithoutCorePlanDoesNotOpenReview(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.currentStreamText = "Here you go:\n<proposed_plan>1. Do it</proposed_plan>"
	m.finalizeTurnOutput("", nil, "exec-3")

	if m.activePlanReview != nil {
		t.Fatalf("expected no review when the Core returned no ProposedPlan, got: %+v", m.activePlanReview)
	}
	for _, entry := range m.entries {
		if entry.Kind == KindPlan {
			t.Fatalf("expected no plan entry without a Core plan, got: %q", entry.Text)
		}
		if entry.Kind == KindAssistant && strings.Contains(entry.Text, "<proposed_plan>") {
			t.Fatalf("expected raw plan markup to be removed, got: %q", entry.Text)
		}
	}
}

func TestSummarizeToolRoundRecognizesAskUserAndSubagents(t *testing.T) {
	entries := []TranscriptEntry{
		{Kind: KindTool, ToolName: "list_dir"},
		{Kind: KindTool, ToolName: "read_file"},
		{Kind: KindTool, ToolName: "grep"},
		{Kind: KindTool, ToolName: "ask_user"},
		{Kind: KindTool, ToolName: "delegate_team"},
	}
	summary := summarizeToolRound(entries)
	if !strings.Contains(summary, "Explored 1 directory, 1 file, 1 search") {
		t.Fatalf("expected file exploration in summary, got: %q", summary)
	}
	if !strings.Contains(summary, "asked 1 question") {
		t.Fatalf("expected 'asked 1 question' in summary, got: %q", summary)
	}
	if !strings.Contains(summary, "ran 1 subagent") {
		t.Fatalf("expected 'ran 1 subagent' in summary, got: %q", summary)
	}
}

func TestEffortCommandOpensInteractivePicker(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")

	// 1. Initial effort is default
	if got := m.currentReasoningEffort(); got != "" {
		t.Fatalf("expected empty effort initially, got: %q", got)
	}

	// 2. /effort without arguments opens the interactive picker instead of printing usage.
	m.handleSlashCommand("/effort")
	if m.effortPicker == nil {
		t.Fatalf("expected /effort to open the interactive effort picker")
	}
	if m.effortPicker.Selected != 0 {
		t.Fatalf("expected picker to start at current level (default), got %d", m.effortPicker.Selected)
	}
	if len(m.entries) != 0 {
		t.Fatalf("expected no system entry when opening the picker, got: %+v", m.entries)
	}

	// 3. Navigate down to "high" (index 5) and apply with Enter.
	for i := 0; i < 5; i++ {
		m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if m.effortPicker == nil || m.effortPicker.Selected != 5 {
		t.Fatalf("expected picker selection at high, got %+v", m.effortPicker)
	}
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.effortPicker != nil {
		t.Fatalf("expected picker to close after applying")
	}
	if m.reasoningEffort != "high" {
		t.Fatalf("expected reasoningEffort 'high', got: %q", m.reasoningEffort)
	}
	last := m.entries[len(m.entries)-1]
	if !strings.Contains(last.Text, "Reasoning effort set to `high`") {
		t.Fatalf("expected set confirmation, got: %q", last.Text)
	}

	// 4. Reopening the picker selects the active level.
	m.handleSlashCommand("/effort")
	if m.effortPicker == nil || m.effortPicker.Selected != 5 {
		t.Fatalf("expected picker to remember active level high, got %+v", m.effortPicker)
	}
	m.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.effortPicker != nil {
		t.Fatalf("expected Esc to close the picker")
	}

	// 5. Direct level argument still works for quick, scriptable changes.
	m.handleSlashCommand("/effort default")
	if m.reasoningEffort != "" {
		t.Fatalf("expected reasoningEffort empty, got: %q", m.reasoningEffort)
	}
	last = m.entries[len(m.entries)-1]
	if !strings.Contains(last.Text, "Reasoning effort set to `default`") {
		t.Fatalf("expected reset confirmation, got: %q", last.Text)
	}

	// 6. Invalid effort level
	m.handleSlashCommand("/effort super-turbo")
	last = m.entries[len(m.entries)-1]
	if !strings.Contains(last.Text, "Invalid reasoning effort `super-turbo`") {
		t.Fatalf("expected invalid effort error, got: %q", last.Text)
	}
}

func TestEstimateDeltaTokens(t *testing.T) {
	// Empty delta
	if got := estimateDeltaTokens(""); got != 0 {
		t.Fatalf("expected 0 tokens for empty, got %d", got)
	}

	// ASCII English: "Hello world" (11 chars) -> approx 2-5 tokens
	if got := estimateDeltaTokens("Hello world"); got < 2 || got > 5 {
		t.Fatalf("expected 2-5 tokens for 'Hello world', got %d", got)
	}

	// Chinese: "你好世界" (4 chars) -> approx 5-7 tokens (average 1.4 per char)
	if got := estimateDeltaTokens("你好世界"); got < 5 || got > 7 {
		t.Fatalf("expected 5-7 tokens for '你好世界', got %d", got)
	}
}

func TestStreamingGenerationSpeedCalculation(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.running = true
	// Simulate user submit 5 seconds ago (e.g. prompt overhead, queue, network TTFT)
	m.turnStreamStartTime = time.Now().Add(-5 * time.Second)

	// Stream first token arrives now (100 tokens generated in 0.4s)
	m.streamFirstTokenTime = time.Now().Add(-400 * time.Millisecond)

	// Usage event arrives with 100 output tokens
	usageEvent := core.Event{
		Kind: core.EventUsage,
		Usage: core.Usage{
			OutputTokens: 100,
		},
	}
	res, _ := m.Update(runtimeEventMsg{event: usageEvent})
	updated := res.(*tuiModel)

	// 100 tokens / 0.4s = 250 tok/s! Should NOT be 100 / 5.4s = 18 tok/s!
	if updated.lastTokensPerSec < 200.0 || updated.lastTokensPerSec > 300.0 {
		t.Fatalf("expected generation speed ~250 tok/s, got %.1f tok/s", updated.lastTokensPerSec)
	}
}
