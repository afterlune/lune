package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

func TestBindSessionRuntimeRestoresSnapshotAndKeepsOldStateOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	projectConfig := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: tmpDir})
	if err != nil {
		t.Fatalf("failed to create core: %v", err)
	}
	defer client.Close(context.Background())
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
	m.activeQuestion = &ActiveQuestion{}
	m.activeApproval = &ActiveApproval{}
	m.activePlanReview = &ActivePlanReview{}

	if err := m.bindSessionRuntime(ctx, second.ID); err != nil {
		t.Fatalf("failed to switch session: %v", err)
	}
	if m.sessionID != second.ID || m.sessionTitle != "Second session" {
		t.Fatalf("session metadata was not restored: id=%q title=%q", m.sessionID, m.sessionTitle)
	}
	if got, want := m.desiredTerminalTitle(), "Second session | "+filepath.Base(m.workspace); got != want {
		t.Fatalf("session switch did not update the terminal title: got %q, want %q", got, want)
	}
	if len(m.input.history) != 1 || m.input.history[0] != "Second session prompt" {
		t.Fatalf("session prompt history was not restored: %+v", m.input.history)
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

func TestSessionSnapshotUsesConfiguredContextBeforeUsageHistory(t *testing.T) {
	tmpDir := t.TempDir()
	projectConfig := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{"contextWindow":1000000}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: tmpDir})
	if err != nil {
		t.Fatalf("failed to create core: %v", err)
	}
	defer client.Close(context.Background())

	ctx := context.Background()
	session, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	m := newTUIModel(client, nil, session.ID)
	saved, err := client.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	saved.UsageRequests = []core.UsageRecord{{ContextWindow: 200000}}
	m.loadSessionSnapshot(saved)
	if m.usage.contextWindow != 1000000 {
		t.Fatalf("project context default must override stale usage history, got %d", m.usage.contextWindow)
	}
	if summary := formatContextAndTokenSummary(m, 120); !strings.Contains(summary, "/1M") {
		t.Fatalf("footer should show the configured context window before usage events, got %q", summary)
	}

	sessionOverride := int64(1200000)
	saved.Settings.ContextWindow = &sessionOverride
	m.loadSessionSnapshot(saved)
	if m.usage.contextWindow != sessionOverride {
		t.Fatalf("session context override must win over the project default, got %d", m.usage.contextWindow)
	}
	m.loadSessionSnapshot(saved)
	m.restoreRuntimeSnapshot(core.RuntimeState{Task: core.TaskState{
		Status:      core.TaskPaused,
		ExecutionID: "queued-paused-execution",
		QueueID:     "queue-id",
	}})
	if m.usage.contextWindow != sessionOverride {
		t.Fatalf("queued paused state without a task snapshot must keep session settings, got %d", m.usage.contextWindow)
	}
	m.restoreRuntimeSnapshot(core.RuntimeState{Task: core.TaskState{
		Status:        core.TaskRunning,
		TaskID:        "task-id",
		ExecutionID:   "active-execution",
		ContextWindow: 900000,
	}})
	if m.usage.contextWindow != 900000 {
		t.Fatalf("active execution must retain its pinned context window, got %d", m.usage.contextWindow)
	}
}

func TestSessionSnapshotRestoresLeadContextUsage(t *testing.T) {
	tests := []struct {
		name              string
		records           []core.UsageRecord
		wantInput         int64
		wantEstimated     int64
		wantContextWindow int64
	}{
		{
			name: "latest team request does not replace lead context",
			records: []core.UsageRecord{
				{AgentID: "lead", Purpose: core.UsagePurposeMain, Usage: core.Usage{InputTokens: 42000}, ContextWindow: 1000000},
				{AgentID: "lead/researcher", Purpose: core.UsagePurposeTeam, Usage: core.Usage{InputTokens: 180000}, EstimatedInputTokens: 180000},
			},
			wantInput: 42000, wantContextWindow: 1000000,
		},
		{
			name: "lead estimate is restored when measured input is absent",
			records: []core.UsageRecord{
				{AgentID: "lead", Purpose: core.UsagePurposeMain, EstimatedInputTokens: 55000, ContextWindow: 800000},
				{AgentID: "lead/reviewer", Purpose: core.UsagePurposeTeam, EstimatedInputTokens: 90000},
			},
			wantEstimated: 55000, wantContextWindow: 800000,
		},
		{
			name: "legacy record without agent metadata",
			records: []core.UsageRecord{
				{Usage: core.Usage{InputTokens: 30000}, ContextWindow: 500000},
			},
			wantInput: 30000, wantContextWindow: 500000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTUIModel(nil, nil, "test-sess")
			m.loadSessionSnapshot(core.Session{UsageRequests: tt.records})
			if m.usage.lastInputTokens != tt.wantInput || m.usage.estimatedInputTokens != tt.wantEstimated || m.usage.contextWindow != tt.wantContextWindow {
				t.Fatalf("restored usage input=%d estimated=%d context=%d, want input=%d estimated=%d context=%d",
					m.usage.lastInputTokens, m.usage.estimatedInputTokens, m.usage.contextWindow, tt.wantInput, tt.wantEstimated, tt.wantContextWindow)
			}
		})
	}
}

func TestSessionsModalInteractions(t *testing.T) {
	tmpDir := t.TempDir()
	projectConfig := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: tmpDir})
	if err != nil {
		t.Fatalf("failed to create core: %v", err)
	}
	defer client.Close(context.Background())

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
	m.run.running = true
	mod, _ := m.handleSlashCommand("/resume")
	m = mod.(*tuiModel)
	if m.modals.sessions.open {
		t.Fatalf("expected sessions modal NOT to open while task is running")
	}
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Cannot switch sessions while a task is running") {
		t.Fatalf("expected running guard warning, got: %s", lastSys)
	}
	m.run.running = false

	// 2. Open modal via /resume
	mod, _ = m.handleSlashCommand("/resume")
	m = mod.(*tuiModel)
	if !m.modals.sessions.open {
		t.Fatalf("expected sessions modal to open via /resume")
	}
	if len(m.modals.sessions.items) != 3 {
		t.Fatalf("expected 3 sessions in modal, got %d", len(m.modals.sessions.items))
	}
	if len(m.modals.sessions.filtered) != 3 {
		t.Fatalf("expected 3 filtered sessions, got %d", len(m.modals.sessions.filtered))
	}
	// Verify view rendering
	viewStr := ansi.Strip(m.View().Content)
	if !strings.Contains(viewStr, "Switch Session") || !strings.Contains(viewStr, "Frontend UI feature") || !strings.Contains(viewStr, "• active") {
		t.Fatalf("expected modal view with session details, got:\n%s", viewStr)
	}

	// 3. Navigation with Down and Up
	initialIdx := m.modals.sessions.index
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m = mod.(*tuiModel)
	if m.modals.sessions.index == initialIdx && len(m.modals.sessions.filtered) > 1 {
		t.Fatalf("expected sessionsIndex to change on Down")
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mod.(*tuiModel)
	if m.modals.sessions.index != initialIdx {
		t.Fatalf("expected sessionsIndex to return to initialIdx on Up")
	}

	// 4. Typing to filter
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'b', Text: "b"}))
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'u', Text: "u"}))
	m = mod.(*tuiModel)
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'g', Text: "g"}))
	m = mod.(*tuiModel)
	if m.modals.sessions.filter != "bug" {
		t.Fatalf("expected filter to be 'bug', got %q", m.modals.sessions.filter)
	}
	if len(m.modals.sessions.filtered) != 1 || m.modals.sessions.filtered[0].ID != s2.ID {
		t.Fatalf("expected filtered to contain only s2, got %d items", len(m.modals.sessions.filtered))
	}

	// 5. Backspace removes last char
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	m = mod.(*tuiModel)
	if m.modals.sessions.filter != "bu" {
		t.Fatalf("expected filter to be 'bu' after backspace, got %q", m.modals.sessions.filter)
	}

	// 6. Dismiss with Esc
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.modals.sessions.open {
		t.Fatalf("expected sessions modal to close on Esc")
	}
	if m.modals.sessions.filter != "" {
		t.Fatalf("expected filter to reset on Esc")
	}

	// 7. Open with /resume command (without arguments)
	mod, _ = m.handleSlashCommand("/resume")
	m = mod.(*tuiModel)
	if !m.modals.sessions.open {
		t.Fatalf("expected /resume without args to open modal")
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
	if len(m.modals.sessions.filtered) != 1 || m.modals.sessions.filtered[0].ID != s3.ID {
		t.Fatalf("expected filtered to match s3, got %d items", len(m.modals.sessions.filtered))
	}

	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)
	if m.modals.sessions.open {
		t.Fatalf("expected modal to close after Enter")
	}
	if m.sessionID != s3.ID {
		t.Fatalf("expected session to switch to s3 (%s), got %s", s3.ID, m.sessionID)
	}

	// 9. Reopening via /resume and closing with Esc
	mod, _ = m.handleSlashCommand("/resume")
	m = mod.(*tuiModel)
	if !m.modals.sessions.open {
		t.Fatalf("expected modal to open via /resume")
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.modals.sessions.open {
		t.Fatalf("expected modal to close on Esc")
	}

	// 10. Verify the shortcuts panel lists /resume instead of a session hotkey
	shortcutsStr := renderShortcutsPanel(100)
	if !strings.Contains(shortcutsStr, "/resume") || !strings.Contains(shortcutsStr, "Switch session") {
		t.Fatalf("expected shortcuts panel to list /resume Switch session, got:\n%s", shortcutsStr)
	}
	if strings.Contains(shortcutsStr, "Ctrl+T") || strings.Contains(shortcutsStr, "Ctrl+R") {
		t.Fatalf("expected removed Ctrl+T/Ctrl+R hotkeys to be absent, got:\n%s", shortcutsStr)
	}
	if !strings.Contains(shortcutsStr, "Ctrl+O") || !strings.Contains(shortcutsStr, "Expand/collapse all rounds") {
		t.Fatalf("expected Ctrl+O to be listed for round expansion, got:\n%s", shortcutsStr)
	}
}

func TestSessionsModalDeleteAndDropCommand(t *testing.T) {
	tmpDir := t.TempDir()
	projectConfig := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(projectConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := core.New(core.Config{Workspace: tmpDir})
	if err != nil {
		t.Fatalf("failed to create core: %v", err)
	}
	defer client.Close(context.Background())

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
	mod, _ := m.handleSlashCommand("/resume")
	m = mod.(*tuiModel)
	if !m.modals.sessions.open {
		t.Fatalf("expected modal to open via /resume")
	}

	// 2. Select active session (s1) and try Ctrl+D -> blocked
	for i, s := range m.modals.sessions.filtered {
		if s.ID == s1.ID {
			m.modals.sessions.index = i
			break
		}
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if m.modals.sessions.confirmDelete {
		t.Fatalf("expected deletion confirmation NOT to trigger for active session")
	}
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Cannot delete currently active session") {
		t.Fatalf("expected active session guard warning, got: %s", lastSys)
	}

	// 3. Select disposable session (s2) and press Ctrl+D -> enter confirmation
	for i, s := range m.modals.sessions.filtered {
		if s.ID == s2.ID {
			m.modals.sessions.index = i
			break
		}
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'd', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if !m.modals.sessions.confirmDelete || m.modals.sessions.deletingID != s2.ID {
		t.Fatalf("expected sessionsConfirmDelete for s2, got %v (id: %s)", m.modals.sessions.confirmDelete, m.modals.sessions.deletingID)
	}
	// Verify view rendering contains confirmation warning
	viewStr := ansi.Strip(m.View().Content)
	if !strings.Contains(viewStr, "Delete session") || !strings.Contains(viewStr, "y to confirm") {
		t.Fatalf("expected confirmation banner in view, got:\n%s", viewStr)
	}

	// 4. Press 'n' to cancel
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"}))
	m = mod.(*tuiModel)
	if m.modals.sessions.confirmDelete {
		t.Fatalf("expected sessionsConfirmDelete to cancel on 'n'")
	}

	// 5. Press Delete key (tea.KeyDelete) on s2 and press 'y' to confirm
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDelete}))
	m = mod.(*tuiModel)
	if !m.modals.sessions.confirmDelete {
		t.Fatalf("expected sessionsConfirmDelete on Delete key")
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	m = mod.(*tuiModel)
	if m.modals.sessions.confirmDelete {
		t.Fatalf("expected confirmation to clear after 'y'")
	}
	// Verify s2 is gone from sessionsItems
	for _, s := range m.modals.sessions.items {
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
	// No arguments opens the interactive delete picker.
	m.handleSlashCommand("/drop")
	if !m.modals.sessions.open || !m.modals.sessions.deleteMode {
		t.Fatalf("expected /drop to open delete picker, open=%v deleteMode=%v", m.modals.sessions.open, m.modals.sessions.deleteMode)
	}
	_, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))

	// Cannot delete active session
	m.handleSlashCommand("/drop " + s1.ID)
	if !strings.Contains(m.entries[len(m.entries)-1].Text, "Cannot delete currently active session") {
		t.Fatalf("expected active session guard for /drop")
	}

	// Delete s3 via prefix
	s3Prefix := s3.ID[:8]
	m.handleSlashCommand("/drop " + s3Prefix)
	if !m.modals.sessions.open || !m.modals.sessions.confirmDelete || m.modals.sessions.deletingID != s3.ID {
		t.Fatalf("expected /drop to request confirmation for s3, open=%v confirm=%v id=%q", m.modals.sessions.open, m.modals.sessions.confirmDelete, m.modals.sessions.deletingID)
	}
	_, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"}))
	if !m.modals.sessions.open || m.modals.sessions.confirmDelete {
		t.Fatal("expected n to cancel the deletion and return to the picker")
	}
	_, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m.handleSlashCommand("/drop " + s3Prefix)
	_, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	if m.modals.sessions.confirmDelete {
		t.Fatal("expected y to complete the confirmed deletion")
	}
	lastSys = m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "Deleted session: "+s3.ID) {
		t.Fatalf("expected deleted session notice for s3, got: %s", lastSys)
	}

	// 7. Test running guard for /drop
	m.run.running = true
	m.handleSlashCommand("/drop non-existent")
	if !strings.Contains(m.entries[len(m.entries)-1].Text, "Cannot delete sessions while a task is running") {
		t.Fatalf("expected running guard warning for /drop")
	}
}

func TestToolPagerModalInteractions(t *testing.T) {
	m := newTUIModel(nil, nil, "test-tool-pager")
	m.input.clipboardWriter = func(string) error { return nil }
	m.width = 100
	m.height = 30
	m.updateLayout()

	// 1. Opening the pager with no tool executions reports it instead of opening
	mod, _ := m.handleSlashCommand("/detail")
	m = mod.(*tuiModel)
	if m.modals.toolPager.open {
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

	// 3. Open pager via /detail
	mod, _ = m.handleSlashCommand("/detail")
	m = mod.(*tuiModel)
	if !m.modals.toolPager.open {
		t.Fatalf("expected toolPagerOpen to be true after /detail")
	}
	if m.modals.toolPager.index != 2 {
		t.Fatalf("expected toolPagerIndex to point to latest tool (2), got %d", m.modals.toolPager.index)
	}
	if m.modals.toolPager.scrollY != 0 {
		t.Fatalf("expected toolPagerScrollY to be 0, got %d", m.modals.toolPager.scrollY)
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
	if m.modals.toolPager.index != 1 {
		t.Fatalf("expected toolPagerIndex to be 1 after '[', got %d", m.modals.toolPager.index)
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
	if m.modals.toolPager.scrollY != 1 {
		t.Fatalf("expected scrollY to be 1 after 'j', got %d", m.modals.toolPager.scrollY)
	}

	// Scroll down with 'pgdown'
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	m = mod.(*tuiModel)
	if m.modals.toolPager.scrollY != 1+visible {
		t.Fatalf("expected scrollY to be %d after pgdown, got %d", 1+visible, m.modals.toolPager.scrollY)
	}

	// Scroll to end with 'end' / 'G'
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'G', Text: "G"}))
	m = mod.(*tuiModel)
	maxScroll := max(0, 50-visible)
	if m.modals.toolPager.scrollY != maxScroll {
		t.Fatalf("expected scrollY to be maxScroll (%d), got %d", maxScroll, m.modals.toolPager.scrollY)
	}

	// Scroll to top with 'home' / 'g'
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'g', Text: "g"}))
	m = mod.(*tuiModel)
	if m.modals.toolPager.scrollY != 0 {
		t.Fatalf("expected scrollY to be 0 after 'g', got %d", m.modals.toolPager.scrollY)
	}

	// 6. Test copying tool output with 'c'
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Text: "c"}))
	m = mod.(*tuiModel)
	if !strings.Contains(m.input.copyFeedback, "Copied") {
		t.Fatalf("expected copy feedback, got %q", m.input.copyFeedback)
	}
	renderedWithCopy := renderToolPagerModal(m, 100)
	if !strings.Contains(renderedWithCopy, "Copied") {
		t.Fatalf("expected copy feedback rendered in footer, got:\n%s", renderedWithCopy)
	}

	// 7. Cycle previous to tool 0, then wrap around
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'p', Text: "p"}))
	m = mod.(*tuiModel)
	if m.modals.toolPager.index != 0 {
		t.Fatalf("expected toolPagerIndex to be 0 after 'p', got %d", m.modals.toolPager.index)
	}
	// Wrap around backwards to 2
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '[', Text: "["}))
	m = mod.(*tuiModel)
	if m.modals.toolPager.index != 2 {
		t.Fatalf("expected toolPagerIndex to wrap to 2, got %d", m.modals.toolPager.index)
	}
	// Cycle next with ']'
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: ']', Text: "]"}))
	m = mod.(*tuiModel)
	if m.modals.toolPager.index != 0 {
		t.Fatalf("expected toolPagerIndex to wrap to 0, got %d", m.modals.toolPager.index)
	}

	// 8. Close with 'q'
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}))
	m = mod.(*tuiModel)
	if m.modals.toolPager.open {
		t.Fatalf("expected toolPagerOpen to be false after 'q'")
	}

	// 9. Open via /detail command with specific 1-based index
	mod, _ = m.handleSlashCommand("/detail 2")
	m = mod.(*tuiModel)
	if !m.modals.toolPager.open {
		t.Fatalf("expected toolPagerOpen to be true after /detail 2")
	}
	if m.modals.toolPager.index != 1 {
		t.Fatalf("expected toolPagerIndex to be 1 (/detail 2), got %d", m.modals.toolPager.index)
	}

	// Close with Esc
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.modals.toolPager.open {
		t.Fatalf("expected toolPagerOpen to be false after Esc")
	}

	// Ctrl+T is no longer bound: it must not reopen the pager
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 't', Mod: tea.ModCtrl}))
	m = mod.(*tuiModel)
	if m.modals.toolPager.open {
		t.Fatalf("expected Ctrl+T to no longer open the tool output pager")
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
	m.input.clipboardWriter = func(string) error { return nil }
	m.width = 100
	m.height = 30
	m.modals.diff.open = true
	m.modals.diff.files = parsed
	m.modals.diff.fileIndex = 0
	m.modals.diff.scrollY = 0
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
	if m.modals.diff.scrollY != 1 {
		t.Errorf("expected diffScrollY to be 1 after down arrow, got %d", m.modals.diff.scrollY)
	}

	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	m = mod.(*tuiModel)
	if m.modals.diff.scrollY != 0 {
		t.Errorf("expected diffScrollY to be 0 after up arrow, got %d", m.modals.diff.scrollY)
	}

	// 4. Test file switching (Tab / ])
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = mod.(*tuiModel)
	if m.modals.diff.fileIndex != 1 {
		t.Errorf("expected diffFileIndex to be 1 after Tab, got %d", m.modals.diff.fileIndex)
	}
	viewContent2 := ansi.Strip(m.View().Content)
	if !strings.Contains(viewContent2, "File 2 of 2") || !strings.Contains(viewContent2, "pkg/auth/token.go") {
		t.Errorf("expected View to show File 2 of 2 token.go, got: %s", viewContent2)
	}

	// Previous file ([ / p)
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: '['}))
	m = mod.(*tuiModel)
	if m.modals.diff.fileIndex != 0 {
		t.Errorf("expected diffFileIndex to wrap back to 0, got %d", m.modals.diff.fileIndex)
	}

	// 5. Test Copy (c)
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'c'}))
	m = mod.(*tuiModel)
	if !strings.Contains(m.input.copyFeedback, "Copied") {
		t.Errorf("expected copy feedback, got %q", m.input.copyFeedback)
	}

	// 6. Test Esc closes modal
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.modals.diff.open {
		t.Fatalf("expected diffModalOpen to be false after Esc")
	}

	// 7. Test /diff command with non-existent path
	mod, cmd := m.handleSlashCommand("/diff non-existent-path-abc.txt")
	m = mod.(*tuiModel)
	m = settleCommands(t, m, cmd, isDiffLoaded)
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "No changes found for: non-existent-path-abc.txt") {
		t.Errorf("expected no changes notice, got: %s", lastSys)
	}
}

func TestTerminalTitleUpdates(t *testing.T) {
	m := newTUIModel(nil, nil, "sess-1")
	m.workspace = filepath.Join(`D:\Git`, "lune") + string(os.PathSeparator)
	m.width = 120
	m.height = 36
	m.updateLayout()

	// 1. Initial untitled session includes the workspace directory.
	if got := m.desiredTerminalTitle(); got != "lune | lune" {
		t.Fatalf("expected initial title 'lune | lune', got: %q", got)
	}

	// 2. Set title: it replaces "lune" while preserving the workspace suffix.
	m.sessionTitle = "Refactor Architecture"
	if got := m.desiredTerminalTitle(); got != "Refactor Architecture | lune" {
		t.Fatalf("expected title 'Refactor Architecture | lune', got: %q", got)
	}

	// A title derived from the conversation uses the same directory suffix.
	m.sessionTitle = ""
	m.entries = []TranscriptEntry{{Kind: KindUser, Text: "Review the latest commit"}}
	if got := m.desiredTerminalTitle(); got != "Review the latest commit | lune" {
		t.Fatalf("expected derived title with workspace suffix, got: %q", got)
	}

	// 3. An explicit untitled marker uses the app name as the session title.
	m.entries = nil
	m.sessionTitle = "<untitled>"
	if got := m.desiredTerminalTitle(); got != "lune | lune" {
		t.Fatalf("expected untitled fallback 'lune | lune', got: %q", got)
	}

	// 4. Without a usable workspace, keep the prior title behavior.
	root := string(os.PathSeparator)
	if volume := filepath.VolumeName(m.workspace); volume != "" {
		root = volume + string(os.PathSeparator)
	}
	m.workspace = root
	if got := m.desiredTerminalTitle(); got != "lune" {
		t.Fatalf("expected root-workspace fallback 'lune', got: %q", got)
	}
	m.sessionTitle = "Refactor Architecture"
	if got := m.desiredTerminalTitle(); got != "Refactor Architecture | lune" {
		t.Fatalf("expected root-workspace title 'Refactor Architecture | lune', got: %q", got)
	}
	m.workspace = ""
	if got := m.desiredTerminalTitle(); got != "Refactor Architecture | lune" {
		t.Fatalf("expected empty-workspace title 'Refactor Architecture | lune', got: %q", got)
	}
}

func TestDeriveSessionTitleSkipsGreeting(t *testing.T) {
	// Single greeting entry
	entries1 := []TranscriptEntry{
		{Kind: KindUser, Text: "你好"},
	}
	if got := deriveSessionTitleFromEntries(entries1); got != "你好" {
		t.Fatalf("expected greeting fallback '你好', got: %q", got)
	}

	// Greeting followed by substantive prompt
	entries2 := []TranscriptEntry{
		{Kind: KindUser, Text: "你好！"},
		{Kind: KindAssistant, Text: "你好！有什么我可以帮你的？"},
		{Kind: KindUser, Text: "修复编译错误"},
	}
	if got := deriveSessionTitleFromEntries(entries2); got != "修复编译错误" {
		t.Fatalf("expected substantive prompt '修复编译错误', got: %q", got)
	}

	// Multiple greetings
	entries3 := []TranscriptEntry{
		{Kind: KindUser, Text: "hello"},
		{Kind: KindUser, Text: "hi there"},
		{Kind: KindUser, Text: "实现新的用户权限模块"},
	}
	if got := deriveSessionTitleFromEntries(entries3); got != "实现新的用户权限模块" {
		t.Fatalf("expected substantive prompt '实现新的用户权限模块', got: %q", got)
	}
}
