package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

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
	projectConfig := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{}}`
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
	projectConfig := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{}}`
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
