package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
)

func TestTUIToolTimelineShowsOnlySanitizedLifecycle(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.Update(toolActivityMsg{event: core.ToolCall{ID: "internal-call-id", Name: "read_file", Started: true}})
	if len(ui.entries) != 1 || ui.entries[0].text != "Read a workspace file" {
		t.Fatalf("starting tool call should create one semantic transcript row: %#v", ui.entries)
	}
	ui.Update(toolActivityMsg{event: core.ToolCall{ID: "internal-call-id", Name: "read_file", Duration: 25, Failed: true}})
	if len(ui.entries) != 1 || !strings.Contains(ui.entries[0].text, "failed") {
		t.Fatalf("finished tool call should update its live transcript row: %#v", ui.entries)
	}
	view := ui.activityDetailsContent()
	if !strings.Contains(view, "Read a workspace file") || !strings.Contains(view, "failed") {
		t.Fatalf("view = %q, want humanized tool and failure status", view)
	}
	if strings.Contains(view, "read_file") || strings.Contains(view, "internal-call-id") || strings.Contains(view, "arguments") || strings.Contains(view, "secret tool output") {
		t.Fatalf("view contains internal or tool payload data: %q", view)
	}
}

func TestTUIHistoryTranscriptHidesToolArgumentsAndOutput(t *testing.T) {
	toolOutput := "secret tool output"
	history := json.RawMessage(`[{"type":"function_call","call_id":"call-1","name":"read_file","arguments":"{\"path\":\"private.txt\"}"},{"type":"function_call_output","call_id":"call-1","output":"` + toolOutput + `"}]`)
	entries := transcriptFromHistory(history)
	if len(entries) != 1 || entries[0].Text != "Read private.txt · done" {
		t.Fatalf("transcript entries = %#v, want one sanitized tool lifecycle", entries)
	}
	if strings.Contains(entries[0].Text, "read_file") || strings.Contains(entries[0].Text, toolOutput) {
		t.Fatalf("transcript exposes tool payload: %#v", entries[0])
	}
}

func TestTUIRestoresLegacyToolNamesAsHumanSummaries(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.restoreSessionState(core.Session{History: json.RawMessage(`[{"type":"function_call","call_id":"call-1","name":"read_file","arguments":"{\"path\":\"private.txt\"}"}]`)})
	ui.refreshPanels()
	view := ui.activityDetailsContent() + "\n" + strings.Join(entryTexts(ui.entries), "\n")
	if strings.Contains(view, "read_file") || !strings.Contains(view, "Read private.txt") {
		t.Fatalf("legacy transcript exposes internal tool name: %q", view)
	}
}

func TestSessionHeaderStaysSeparateFromFilteredToolOutput(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = themeLight
	ui.applyResolvedTheme()
	ui.workspace = `D:\Git\luneAgent`
	ui.windowTitle = "lune"
	ui.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	ui.updateToolActivity(core.ToolCall{
		ID: "read-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"internal/tool/html.go"}`),
		Details: &publictool.ExecutionDetails{Output: "visible result\n[end of results]\n[next_start_line=4]"},
	})
	group := ui.toolActivityGroups()[0]
	ui.activityExpanded[group.key] = true
	ui.refreshPanels()
	lines := strings.Split(plainTUIView(ui), "\n")
	if len(lines) == 0 || !strings.HasPrefix(strings.TrimSpace(lines[0]), "›_ luneAgent") || strings.Contains(lines[0], "visible result") {
		t.Fatalf("session header absorbed tool output: %#v", lines)
	}
	plain := strings.Join(lines, "\n")
	if !strings.Contains(plain, "visible result") || strings.Contains(plain, "[end of results]") || strings.Contains(plain, "[next_start_line=") {
		t.Fatalf("tool output preview or metadata filtering is wrong: %q", plain)
	}
}

func TestTUIRestoredHistoryCollapsesToolCallsByUserTurn(t *testing.T) {
	history := json.RawMessage(`[
		{"type":"message","role":"user","content":"Inspect the project."},
		{"type":"function_call","call_id":"call-1","name":"read_file","arguments":"{\"path\":\"main.go\"}"},
		{"type":"function_call","call_id":"call-2","name":"grep","arguments":"{\"pattern\":\"TODO\"}"},
		{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Found two TODOs."}]}
	]`)
	ui := newTUIModel(&fakeResponder{}, false)
	ui.entries = transcriptEntries(history)
	content, _ := ui.transcriptContent()
	if len(ui.toolActivityGroups()) != 1 || strings.Contains(content, "Read main.go") || !strings.Contains(content, "Searched TODO") || !strings.Contains(content, "Found two TODOs.") {
		t.Fatalf("restored collapsed transcript = groups:%d content:%q", len(ui.toolActivityGroups()), content)
	}
	ui.activityFocus = true
	ui.toggleFocusedActivity()
	content, _ = ui.transcriptContent()
	if !strings.Contains(content, "Read main.go · done") || !strings.Contains(content, "Searched TODO · done") {
		t.Fatalf("restored expanded transcript = %q", content)
	}
}

func TestTUIShowsAutomaticCompactionProgressAndFailure(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.sessionID = "session-compaction"
	model, _ := ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventContextCompaction, SessionID: ui.sessionID,
		Compaction: &core.ContextCompactionDetails{Stage: "started", Mode: "local"},
	}})
	ui = model.(*tuiModel)
	if !strings.Contains(ui.status, "Compacting context") {
		t.Fatalf("status after compaction start = %q", ui.status)
	}
	model, _ = ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventContextCompaction, SessionID: ui.sessionID,
		Compaction: &core.ContextCompactionDetails{Stage: "completed", Mode: "local", BeforeTokens: 900, AfterTokens: 120},
	}})
	ui = model.(*tuiModel)
	if !strings.Contains(ui.status, "900 → 120 estimated tokens") {
		t.Fatalf("status after compaction completion = %q", ui.status)
	}
	model, _ = ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventContextCompaction, SessionID: ui.sessionID, Err: "summary provider unavailable",
		Compaction: &core.ContextCompactionDetails{Stage: "failed", Mode: "local", Err: "summary provider unavailable"},
	}})
	ui = model.(*tuiModel)
	if !strings.Contains(ui.status, "summary provider unavailable") {
		t.Fatalf("status after compaction failure = %q", ui.status)
	}
}

func TestTUIActivityDetailsOverlayOpensAndCloses(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	ui.Update(toolActivityMsg{event: core.ToolCall{ID: "call-1", Name: "read_file", Started: true}})
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'b', Mod: tea.ModCtrl}))
	view := ui.View()
	if !ui.detailsOpen || !strings.Contains(view.Content, "Activity & usage") {
		t.Fatalf("Ctrl+B should open details overlay: open=%v view=%q", ui.detailsOpen, view.Content)
	}
	if got := lipgloss.Width(view.Content); got > 80 {
		t.Errorf("overlay width = %d, terminal width = 80", got)
	}
	if got := lipgloss.Height(view.Content); got > 24 {
		t.Errorf("overlay height = %d, terminal height = 24", got)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if ui.detailsOpen {
		t.Fatal("Esc should close details overlay")
	}
}

func TestTUIActivityUsesWorkspacePathsAndUserVisibleToolNames(t *testing.T) {
	workspace := t.TempDir()
	ui := newTUIModel(&fakeResponder{}, false)
	ui.workspace = workspace
	ui.preferences = uiPreferences{Theme: themeDark}
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	ui.Update(toolActivityMsg{event: core.ToolCall{
		ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"internal/tool/html.go"}`), Started: true,
	}})
	ui.Update(toolActivityMsg{event: core.ToolCall{
		ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"internal/tool/html.go"}`), Duration: 12,
	}})
	view := ui.activityDetailsContent() + "\n" + strings.Join(entryTexts(ui.entries), "\n")
	if len(ui.activities) != 1 || ui.activities[0].summary != "Read internal/tool/html.go" || !strings.Contains(view, "Lead agent") || !strings.Contains(view, "Model usage") {
		t.Fatalf("activity details are missing real tool activity, agent status, or usage: %q", view)
	}
	if strings.Contains(view, "read_file") || strings.Contains(view, "subagent") || strings.Contains(view, "artifact") || strings.Contains(view, "0%") {
		t.Fatalf("studio view includes internal names or fabricated state: %q", view)
	}
}

func TestToolApprovalUsesHumanTextAndSafeCommandPreview(t *testing.T) {
	workspace := t.TempDir()
	read := formatToolApproval(core.ApprovalRequest{
		Tool: "read_file", Arguments: json.RawMessage(`{"path":"notes.txt"}`),
	}, workspace)
	if !strings.Contains(read, "Action: Read notes.txt") || strings.Contains(read, "read_file") || strings.Contains(read, `"path"`) {
		t.Fatalf("read approval = %q; want a human-readable path without internal payload", read)
	}
	command := formatToolApproval(core.ApprovalRequest{
		Tool: "command", Arguments: json.RawMessage(`{"command":"echo hi\nrm file\u001b[31m"}`),
	})
	if !strings.Contains(command, `echo hi\nrm file[31m`) || !strings.Contains(command, "outside the workspace") {
		t.Fatalf("command approval = %q; want visible escaped preview and warning", command)
	}
}

func TestResponseDisplayHidesRawToolCalls(t *testing.T) {
	text, err := responseDisplayText(json.RawMessage(`{"output":[{"type":"function_call","call_id":"call-1","name":"read_file","arguments":"{\"path\":\"secret.txt\"}"},{"type":"custom_tool_call","call_id":"call-2","name":"internal_tool","input":"secret input"}]}`))
	if err != nil || text != "" {
		t.Fatalf("tool response display = %q, %v; want no raw tool details", text, err)
	}
}
