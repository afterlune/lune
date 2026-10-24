package tui

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/lune/internal/filemention"
	"github.com/afterlune/lune/internal/settings"
	"github.com/afterlune/lune/internal/workspacereview"
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

func TestSlashCommandAvailabilityDuringTask(t *testing.T) {
	m := newTUIModel(nil, nil, "task-session")
	m.run.running = true
	allowed := []string{
		"/permissions auto", "/model", "/effort", "/theme", "/memory", "/memory list",
		"/memory list context", "/memory coding style", "/agents", "/ps", "/stop all",
		"/logs process", "/sessions", "/detail", "/title In progress", "/context", "/diff",
		"/mcp", "/mcp list", "/cancel-queue", "/status", "/shortcuts", "/thought", "/help",
	}
	for _, command := range allowed {
		if reason := m.slashCommandUnavailable(command); reason != "" {
			t.Errorf("%s was blocked during a task: %s", command, reason)
		}
	}
	blocked := []string{
		"/new", "/resume", "/drop", "/export", "/review", "/compact", "/fork", "/rewind",
		"/continue", "/discard", "/exit", "/mcp enable server", "/memory add note", "/memory remove id", "/memory clear",
	}
	for _, command := range blocked {
		if reason := m.slashCommandUnavailable(command); reason == "" {
			t.Errorf("%s was allowed during a task", command)
		}
	}
	suggested := map[string]bool{}
	for _, item := range m.commandsForAutocomplete("/") {
		suggested[item.Command] = true
	}
	for _, command := range []string{"/new", "/resume", "/review", "/export", "/continue", "/discard"} {
		if suggested[command] {
			t.Errorf("autocomplete suggested unavailable command %s during a task", command)
		}
	}
	for _, command := range []string{"/status", "/permissions", "/memory", "/mcp", "/cancel-queue"} {
		if !suggested[command] {
			t.Errorf("autocomplete omitted command %s that remains available during a task", command)
		}
	}

	m.run.running = false
	m.run.queuedTurns = []QueuedItem{{ExecutionID: "queued-task"}}
	if reason := m.slashCommandUnavailable("/new"); reason == "" {
		t.Fatal("command should remain unavailable while a queued turn is pending")
	}
	if reason := m.slashCommandUnavailable("/status"); reason != "" {
		t.Fatalf("read-only status should remain available while a queued turn is pending: %s", reason)
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

func TestMCPAuthenticationIsCancelableAndDoesNotBlockInput(t *testing.T) {
	m := newTUIModel(nil, nil, "mcp-auth")
	started := make(chan struct{})
	m.modals.mcp.authRunner = func(ctx context.Context, _ string) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	cmd := m.startMCPAuthentication("test-server")
	if cmd == nil || m.modals.mcp.authName != "test-server" {
		t.Fatal("expected MCP authorization to start with visible state")
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Press Esc to") {
		t.Fatalf("expected MCP authorization panel to show cancellation instructions, got: %q", view)
	}

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	<-started
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	m = mod.(*tuiModel)
	if m.input.composer.Value() != "x" {
		t.Fatal("composer input should remain responsive during OAuth authorization")
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.modals.mcp.authCancel != nil || m.modals.mcp.authName != "" {
		t.Fatal("Esc should cancel and clear the active OAuth state")
	}
	mod, _ = m.Update(<-done)
	m = mod.(*tuiModel)
	if strings.Contains(m.entries[len(m.entries)-1].Text, "Failed to authenticate") {
		t.Fatal("late cancellation result should not overwrite the cancellation state")
	}
	foundCancel := false
	for _, entry := range m.entries {
		if strings.Contains(entry.Text, "Cancelled MCP authorization") {
			foundCancel = true
		}
	}
	if !foundCancel {
		t.Fatal("expected a cancellation notice")
	}
}

func TestPermissionsCommandRejectsModesOutsideTheRegistry(t *testing.T) {
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

	m := newTUIModel(client, nil, "permission-table")
	for _, raw := range []string{"ask", "nonsense", "readonly", "full_access"} {
		m.handleSlashCommand("/permissions " + raw)
		last := m.entries[len(m.entries)-1].Text
		if !strings.Contains(last, "Unknown permission mode") {
			t.Fatalf("/permissions %s was accepted: %q", raw, last)
		}
		if client.PermissionMode() != core.PermissionWorkspace {
			t.Fatalf("/permissions %s changed the client to %q", raw, client.PermissionMode())
		}
	}

	m.handleSlashCommand("/permissions  AUTO ")
	if m.permissionMode != core.PermissionAuto || client.PermissionMode() != core.PermissionAuto {
		t.Fatalf("model=%q client=%q, want auto", m.permissionMode, client.PermissionMode())
	}
}

func TestReviewCommand(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")
	m.workspace = initReviewGitRepo(t)

	// An empty worktree reports that there is nothing to review without requiring a runtime.
	mod, cmd := m.handleSlashCommand("/review")
	m = mod.(*tuiModel)
	m = settleCommands(t, m, cmd, isReviewMaterials)
	lastSys := m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "No text changes to review") {
		t.Errorf("expected no-changes message, got: %s", lastSys)
	}

	// Changed workspaces need an active runtime before the model can review them.
	if err := os.WriteFile(filepath.Join(m.workspace, "tracked.txt"), []byte("working tree change\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mod, cmd = m.handleSlashCommand("/review")
	m = mod.(*tuiModel)
	m = settleCommands(t, m, cmd, isReviewMaterials)
	lastSys = m.entries[len(m.entries)-1].Text
	if !strings.Contains(lastSys, "active session runtime") {
		t.Errorf("expected runtime requirement message, got: %s", lastSys)
	}
}

func TestCollectReviewMaterialsIncludesTextChangesAndExcludesIgnoredBinary(t *testing.T) {
	workspace := initReviewGitRepo(t)
	trackedBinary := filepath.Join(workspace, "tracked.bin")
	if err := os.WriteFile(trackedBinary, []byte{0, 'b', 'i', 'n', 'a', 'r', 'y'}, 0644); err != nil {
		t.Fatal(err)
	}
	runReviewGit(t, workspace, "add", "tracked.bin")
	runReviewGit(t, workspace, "commit", "-m", "add binary fixture")
	if err := os.WriteFile(trackedBinary, []byte{0, 'b', 'i', 'n', 'a', 'r', 'y', '2'}, 0644); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(workspace, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("staged line\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runReviewGit(t, workspace, "add", "tracked.txt")
	if err := os.WriteFile(tracked, []byte("staged and unstaged line\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "new.txt"), []byte("untracked review content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "ignored.txt"), []byte("ignored secret content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "image.bin"), []byte{1, 'B', 'I', 'N', 'A', 'R', 'Y'}, 0644); err != nil {
		t.Fatal(err)
	}
	materials, changed, err := workspacereview.CollectMaterials(workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected textual changes to be reviewable")
	}
	for _, want := range []string{"staged line", "unstaged line", "untracked review content"} {
		if !strings.Contains(materials, want) {
			t.Errorf("review materials missing %q", want)
		}
	}
	for _, unwanted := range []string{"ignored secret content", "BINARY", "binary fixture"} {
		if strings.Contains(materials, unwanted) {
			t.Errorf("review materials unexpectedly include %q", unwanted)
		}
	}

	pathMaterials, pathChanged, err := workspacereview.CollectMaterials(workspace, "tracked.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !pathChanged || !strings.Contains(pathMaterials, "staged line") || strings.Contains(pathMaterials, "untracked review content") {
		t.Fatalf("path-scoped materials were not limited to tracked.txt: changed=%v materials=%q", pathChanged, pathMaterials)
	}
	if _, _, err := workspacereview.CollectMaterials(workspace, "../outside.txt"); err == nil {
		t.Fatal("review accepted a path outside the workspace")
	}
}

func initReviewGitRepo(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	runReviewGit(t, workspace, "init")
	runReviewGit(t, workspace, "config", "user.email", "review-test@example.invalid")
	runReviewGit(t, workspace, "config", "user.name", "Review Test")
	if err := os.WriteFile(filepath.Join(workspace, ".gitignore"), []byte("ignored.txt\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "tracked.txt"), []byte("initial line\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runReviewGit(t, workspace, "add", ".")
	runReviewGit(t, workspace, "commit", "-m", "initial")
	return workspace
}

func runReviewGit(t *testing.T, workspace string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = workspace
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
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

	m.usage.totalInputTokens = 15000
	m.usage.contextWindow = 128000
	m.usage.estimatedInputTokens = 12000

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

func TestContextDiagnosticsShowsLastPromptCacheBreakdown(t *testing.T) {
	model := tuiModel{usage: sessionUsage{lastInputTokens: 1000, lastCachedTokens: 320, lastRequestID: "request-main", lastRequestAttempt: 2, lastRequestChanges: []string{"tools_changed"}}}
	diagnostics := model.renderContextDiagnostics()
	if !strings.Contains(diagnostics, "Last prompt cache: 320 cached, 680 uncached (32.0% hit)") {
		t.Fatalf("context diagnostics = %q, want per-request cache hit and miss", diagnostics)
	}
	for _, expected := range []string{"request-main", "attempt 2", "tools_changed", "not provider cache eviction reasons"} {
		if !strings.Contains(diagnostics, expected) {
			t.Fatalf("missing %q in %s", expected, diagnostics)
		}
	}
}

func TestSessionsCommandAndAutocomplete(t *testing.T) {
	m := newTUIModel(nil, nil, "test-sess")

	// Autocomplete verification
	matches := filterCommands("/exp")
	var foundExport bool
	for _, match := range matches {
		foundExport = foundExport || match.Command == "/export"
	}
	if !foundExport {
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

func TestCommandAutocompleteEnterExecutesSelectedCandidate(t *testing.T) {
	for _, input := range []string{"/the", "/theme"} {
		t.Run(input, func(t *testing.T) {
			m := newTUIModel(nil, nil, "autocomplete-enter")
			m.input.composer.SetValue(input)
			m.input.composer.CursorEnd()
			m.input.autocompleteMode = AutocompleteModeCommand
			m.input.autocompleteItems = filterCommands(input)
			m.input.autocompleteOpen = true
			for i, item := range m.input.autocompleteItems {
				if item.Command == "/theme" {
					m.input.autocompleteIndex = i
					break
				}
			}

			mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
			m = mod.(*tuiModel)
			if m.modals.theme == nil {
				t.Fatal("Enter should execute the selected command, not leave it in the composer")
			}
			if m.input.composer.Value() != "" {
				t.Fatalf("composer should be cleared after command execution, got %q", m.input.composer.Value())
			}
			if m.input.autocompleteOpen {
				t.Fatal("command autocomplete should close after execution")
			}
		})
	}
}

func TestThemePickerPersistsSelection(t *testing.T) {
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	SetThemeMode(ThemeModeAuto)

	m := newTUIModel(nil, nil, "theme-picker")
	m.settingsStore = store
	m.handleSlashCommand("/theme")
	for i := 0; i < 2; i++ {
		mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
		m = mod.(*tuiModel)
	}
	_, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if CurrentThemeMode() != ThemeModeLight {
		t.Fatalf("theme mode = %q, want light", CurrentThemeMode())
	}
	if mode, found, err := store.TUITheme(); err != nil || !found || mode != "light" {
		t.Fatalf("persisted theme = %q, found %v, err %v; want light", mode, found, err)
	}
}

func TestThemeStillAppliesWhenPersistenceFails(t *testing.T) {
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	SetThemeMode(ThemeModeAuto)
	m := newTUIModel(nil, nil, "theme-unsaved")
	m.settingsStore = store
	m.handleSlashCommand("/theme dark")
	if CurrentThemeMode() != ThemeModeDark {
		t.Fatalf("theme should still apply in memory after persistence failure, got %q", CurrentThemeMode())
	}
	if !strings.Contains(m.entries[len(m.entries)-1].Text, "for this run") {
		t.Fatalf("expected persistence failure notice, got %q", m.entries[len(m.entries)-1].Text)
	}
}

func TestFileMentionAutocomplete(t *testing.T) {
	tempDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tempDir, "internal", "tui"), 0755)
	_ = os.MkdirAll(filepath.Join(tempDir, "cmd"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "internal", "tui", "tui.go"), []byte("package tui"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "cmd", "main.go"), []byte("package main"), 0644)

	// Core lives in its own workspace so the mention scan still sees only the two
	// fixture files; the model starts as a draft session.
	client := newLazySessionTestCore(t)
	m := newTUIModel(client, nil, "")
	m.workspace = tempDir
	m.input.scanner = filemention.NewScanner(tempDir)
	m.input.composer = textarea.New()
	m.input.composer.Focus()

	// 1. Trigger @
	m.input.composer.SetValue("@")
	m.input.composer.CursorEnd()
	mod, cmd := m.Update(tea.KeyPressMsg(tea.Key{Code: 't', Text: "t"}))
	m = mod.(*tuiModel)
	m = settleCommands(t, m, cmd, isMentionResults)
	if !m.input.autocompleteOpen {
		t.Fatalf("expected autocomplete to open for @t")
	}
	if m.input.autocompleteMode != AutocompleteModeFile {
		t.Errorf("expected AutocompleteModeFile, got: %v", m.input.autocompleteMode)
	}

	// 2. Select with Enter
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)
	if m.input.autocompleteOpen {
		t.Errorf("expected autocomplete to close after Enter")
	}
	if !strings.Contains(m.input.composer.Value(), "@internal/tui/tui.go ") {
		t.Errorf("expected composer to contain '@internal/tui/tui.go ', got: %q", m.input.composer.Value())
	}

	// 3. Email address test: should not open autocomplete
	m.input.composer.SetValue("contact alice@example.com")
	m.input.composer.CursorEnd()
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: ' ', Text: " "}))
	m = mod.(*tuiModel)
	if m.input.autocompleteOpen {
		t.Errorf("expected autocomplete to remain closed for email address")
	}

	// 4. Esc closes autocomplete
	m.input.composer.SetValue("review @c")
	m.input.composer.CursorEnd()
	mod, cmd = m.Update(tea.KeyPressMsg(tea.Key{Code: 'm', Text: "m"}))
	m = mod.(*tuiModel)
	m = settleCommands(t, m, cmd, isMentionResults)
	if !m.input.autocompleteOpen {
		t.Fatalf("expected autocomplete to open for @cm")
	}
	// 5. Trigger @ alone in empty composer
	m.input.composer.SetValue("")
	mod, cmd = m.Update(tea.KeyPressMsg(tea.Key{Code: '@', Text: "@"}))
	m = mod.(*tuiModel)
	m = settleCommands(t, m, cmd, isMentionResults)
	if !m.input.autocompleteOpen {
		t.Fatalf("expected autocomplete to open for single '@'")
	}
	if len(m.input.autocompleteItems) != 2 {
		t.Fatalf("expected 2 files in autocomplete items, got: %d", len(m.input.autocompleteItems))
	}

	// 6. Select with Tab
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = mod.(*tuiModel)
	if m.input.autocompleteOpen {
		t.Errorf("expected autocomplete to close after Tab")
	}
	if !strings.HasPrefix(m.input.composer.Value(), "@") || !strings.HasSuffix(m.input.composer.Value(), " ") {
		t.Errorf("expected completed file mention with space, got: %q", m.input.composer.Value())
	}

	// 7. Completion with trailing text avoids double spaces
	m.input.composer.SetValue("inspect @t for issues")
	// Position cursor after @t (offset 10)
	m.setCursorTo(10)
	// Trigger re-detect
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'u', Text: "u"}))
	m = mod.(*tuiModel)
	if !m.input.autocompleteOpen {
		t.Fatalf("expected autocomplete to open for 'inspect @tu for issues'")
	}
	mod, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m = mod.(*tuiModel)
	expectedVal := "inspect @internal/tui/tui.go for issues"
	if m.input.composer.Value() != expectedVal {
		t.Errorf("expected %q, got %q", expectedVal, m.input.composer.Value())
	}

	// 8. Submit turn with @ mention: verify attachments and visual chip
	m.submitTurn(m.input.composer.Value())
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
	if !strings.Contains(renderedUser, "@internal/tui/tui.go") {
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

	// Missing content opens a composer input flow and Enter saves it.
	mWithClient.handleSlashCommand("/memory add convention")
	if !mWithClient.modals.memory.pendingAdd || mWithClient.modals.memory.pendingCategory != "convention" {
		t.Fatal("expected missing memory content to open an input flow")
	}
	mWithClient.input.composer.SetValue("Always verify tests")
	mod, _ := mWithClient.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	mWithClient = mod.(*tuiModel)
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

	// Missing remove ID opens a selector and removes the highlighted item on Enter.
	mWithClient.handleSlashCommand("/memory remove")
	if mWithClient.modals.memory.picker == nil {
		t.Fatal("expected missing memory ID to open a selector")
	}
	mod, _ = mWithClient.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	mWithClient = mod.(*tuiModel)
	if mWithClient.modals.memory.picker != nil || !strings.Contains(mWithClient.entries[len(mWithClient.entries)-1].Text, "Deleted workspace memory") {
		t.Fatal("expected Enter to remove the selected memory")
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
	if !mWithClient.modals.memory.clearConfirm {
		t.Fatal("expected /memory clear to open confirmation")
	}
	mod, _ = mWithClient.Update(tea.KeyPressMsg(tea.Key{Code: 'n', Text: "n"}))
	mWithClient = mod.(*tuiModel)
	if mWithClient.modals.memory.clearConfirm {
		t.Fatal("expected n to cancel memory clear")
	}
	mWithClient.handleSlashCommand("/memory clear")
	mod, _ = mWithClient.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	mWithClient = mod.(*tuiModel)
	lastSys = mWithClient.entries[len(mWithClient.entries)-1].Text
	if !strings.Contains(lastSys, "Cleared all workspace memories.") {
		t.Errorf("expected confirmed clear, got: %s", lastSys)
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

	// Settings commands open real choice pickers when selected without arguments.
	mWithRuntime.handleSlashCommand("/model")
	if mWithRuntime.modals.model == nil || len(mWithRuntime.modals.model.Items) != 1 {
		t.Fatalf("expected /model to open configured model picker, got: %+v", mWithRuntime.modals.model)
	}
	_, _ = mWithRuntime.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	mWithRuntime.handleSlashCommand("/permissions")
	if mWithRuntime.modals.permission == nil {
		t.Fatal("expected /permissions to open the permission mode picker")
	}
	_, _ = mWithRuntime.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if mWithRuntime.modals.permission != nil {
		t.Fatal("Enter should apply and close the permission picker")
	}

	// Initially empty ps
	mWithRuntime.handleSlashCommand("/ps")
	lastSys = mWithRuntime.entries[len(mWithRuntime.entries)-1].Text
	if !strings.Contains(lastSys, "No background processes in current session.") {
		t.Errorf("expected empty processes message, got: %s", lastSys)
	}

	// Missing process IDs open a selector; with no active processes it reports an empty state.
	mWithRuntime.handleSlashCommand("/stop")
	lastSys = mWithRuntime.entries[len(mWithRuntime.entries)-1].Text
	if !strings.Contains(lastSys, "No active background processes to stop.") {
		t.Errorf("expected empty stop picker state, got: %s", lastSys)
	}

	// Stop all when none running
	mWithRuntime.handleSlashCommand("/stop all")
	lastSys = mWithRuntime.entries[len(mWithRuntime.entries)-1].Text
	if !strings.Contains(lastSys, "No active background processes to stop.") {
		t.Errorf("expected no active processes message, got: %s", lastSys)
	}

	// Missing process IDs open the log selector; with no processes it reports an empty state.
	mWithRuntime.handleSlashCommand("/logs")
	lastSys = mWithRuntime.entries[len(mWithRuntime.entries)-1].Text
	if !strings.Contains(lastSys, "No background processes in current session.") {
		t.Errorf("expected empty logs picker state, got: %s", lastSys)
	}

	// Logs for non-existent process
	mWithRuntime.handleSlashCommand("/logs non-existent")
	lastSys = mWithRuntime.entries[len(mWithRuntime.entries)-1].Text
	if !strings.Contains(lastSys, "Process \"non-existent\" not found.") {
		t.Errorf("expected not found message, got: %s", lastSys)
	}
}

func TestUnknownSlashTextSubmitsAsMessage(t *testing.T) {
	client := newLazySessionTestCore(t)
	m := newTUIModel(client, nil, "")
	m.input.composer.SetValue("/home/iacsadmin/cntb/屏幕截图 2026-10-22 131712.png 执行过程像这样")
	m.input.composer.CursorEnd()

	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)

	last := m.entries[len(m.entries)-1]
	if last.Kind != KindUser {
		t.Fatalf("expected a user message, got %s: %q", last.Kind, last.Text)
	}
	if !strings.HasPrefix(last.Text, "/home/iacsadmin/cntb/") {
		t.Fatalf("submitted text = %q, want the pasted path", last.Text)
	}
	for _, entry := range m.entries {
		if entry.Kind == KindSystem && strings.Contains(entry.Text, "Unknown command") {
			t.Fatalf("slash-prefixed prompt must not be rejected as a command: %q", entry.Text)
		}
	}
}

func TestKnownSlashCommandStillDispatches(t *testing.T) {
	client := newLazySessionTestCore(t)
	m := newTUIModel(client, nil, "")
	m.input.composer.SetValue("/model fast")
	m.input.composer.CursorEnd()

	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m = mod.(*tuiModel)

	if m.pendingModelProfile != "fast" {
		t.Fatalf("expected /model to dispatch as a command, got pending profile %q", m.pendingModelProfile)
	}
	for _, entry := range m.entries {
		if entry.Kind == KindUser {
			t.Fatalf("command input must not be submitted as a message: %q", entry.Text)
		}
	}
}
