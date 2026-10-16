package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

func TestLogoEmptyState(t *testing.T) {
	rendered := RenderLogoEmptyState(80, 24)
	if !strings.Contains(rendered, "⣠⣴⣶⣿⣿⣷⣶⣤") {
		t.Errorf("expected Braille logo in empty state, got:\n%s", rendered)
	}
}

func TestHeaderRendering(t *testing.T) {
	header := renderHeader("D:\\Git\\test-repo")
	stripped := ansi.Strip(header)
	if !strings.Contains(stripped, "OpenAI Codex") {
		t.Errorf("expected OpenAI Codex in header, got: %s", stripped)
	}
	if !strings.Contains(stripped, "D:\\Git\\test-repo") {
		t.Errorf("expected workspace in header, got: %s", stripped)
	}
}

func TestEntryRendering(t *testing.T) {
	// User entry
	userEntry := TranscriptEntry{
		Kind: KindUser,
		Text: "Hello Codex",
	}
	uRendered := renderEntry(userEntry, 80)
	if !strings.Contains(uRendered, "›") || !strings.Contains(uRendered, "Hello Codex") {
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

func TestWorkingIndicator(t *testing.T) {
	working := renderWorking(40*time.Second, false)
	if !strings.Contains(working, "Working (40s · esc to interrupt)") {
		t.Errorf("expected Working title, got: %s", working)
	}
	if !strings.Contains(working, "Tip:") {
		t.Errorf("expected Tip line, got: %s", working)
	}

	thinking := renderWorking(15*time.Second, true)
	if !strings.Contains(thinking, "Thinking (15s · esc to interrupt)") {
		t.Errorf("expected Thinking title, got: %s", thinking)
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

