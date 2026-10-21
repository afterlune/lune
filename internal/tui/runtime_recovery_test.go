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

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	"github.com/charmbracelet/x/ansi"
)

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
	projectConfig := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{}}`
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

	// 1. /sessions without arguments lists sessions without switching modes.
	mod, _ := m.handleSlashCommand("/sessions")
	m = mod.(*tuiModel)
	if m.sessionsModalOpen || !strings.Contains(m.entries[len(m.entries)-1].Text, s2.ID[:12]) {
		t.Fatalf("expected /sessions to list the second session without opening a modal: modal=%v entry=%q", m.sessionsModalOpen, m.entries[len(m.entries)-1].Text)
	}

	// 2. /sessions with a query lists matching sessions without switching.
	mod, _ = m.handleSlashCommand("/sessions Beta")
	m = mod.(*tuiModel)
	if m.sessionsModalOpen || !strings.Contains(m.entries[len(m.entries)-1].Text, "Sessions matching \"beta\"") || m.sessionID != s1.ID {
		t.Fatalf("expected /sessions Beta to list matches without switching: modal=%v session=%s entry=%q", m.sessionsModalOpen, m.sessionID, m.entries[len(m.entries)-1].Text)
	}

	// 3. /resume with a unique prefix switches directly without starting a model turn.
	s2Prefix := s2.ID
	if len(s2Prefix) > 8 {
		s2Prefix = s2Prefix[:8]
	}
	mod, _ = m.handleSlashCommand("/resume " + s2Prefix)
	m = mod.(*tuiModel)
	if m.running {
		t.Fatal("switching sessions must not start a model turn")
	}
	if m.sessionsModalOpen {
		t.Fatalf("expected direct switch instead of opening modal for unique prefix")
	}
	if m.sessionID != s2.ID {
		t.Fatalf("expected session to switch to s2 (%s), got %s", s2.ID, m.sessionID)
	}
	mod, _ = m.handleSlashCommand("/open " + s1.ID)
	m = mod.(*tuiModel)
	if !strings.Contains(m.entries[len(m.entries)-1].Text, "Unknown command: `/open ") || m.sessionID != s2.ID {
		t.Fatalf("expected removed /open command to be rejected without switching: session=%s entry=%q", m.sessionID, m.entries[len(m.entries)-1].Text)
	}

	// 4. /resume without an ID opens the session picker.
	mod, _ = m.handleSlashCommand("/resume")
	m = mod.(*tuiModel)
	if !m.sessionsModalOpen {
		t.Fatal("expected /resume without arguments to open the session picker")
	}
	m.sessionsModalOpen = false

	// 5. Checkpoint recovery has a separate /continue command.
	s1Prefix := s1.ID
	if len(s1Prefix) > 8 {
		s1Prefix = s1Prefix[:8]
	}
	mod, _ = m.handleSlashCommand("/continue " + s1Prefix)
	m = mod.(*tuiModel)
	if !strings.Contains(m.entries[len(m.entries)-1].Text, "Usage: `/continue`") {
		t.Fatalf("expected /continue to reject session IDs, session=%s entry=%q", m.sessionID, m.entries[len(m.entries)-1].Text)
	}

	// 6. /continue without a paused task reports that no checkpoint is available.
	mod, _ = m.handleSlashCommand("/continue")
	m = mod.(*tuiModel)
	if m.sessionsModalOpen || !strings.Contains(m.entries[len(m.entries)-1].Text, "No paused task to continue") {
		t.Fatalf("expected /continue to report no paused task without opening a modal")
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
	projectConfig := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{}}`
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
	projectConfig := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{}}`
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
	if !strings.Contains(cardText, "/continue") || !strings.Contains(cardText, "/discard") {
		t.Errorf("expected /continue and /discard hints in card, got: %s", cardText)
	}

	// Verify View contains the paused task banner in composer gap
	viewOutput := ansi.Strip(m.View().Content)
	if !strings.Contains(viewOutput, "Task Paused (Checkpoint saved)") {
		t.Errorf("expected View() to render paused task banner, got:\n%s", viewOutput)
	}

	// Verify footer shows paused task indicator
	footerText := ansi.Strip(renderFooter(m, 100))
	if !strings.Contains(footerText, "task paused · /continue or /discard") {
		t.Errorf("expected footer to show paused task hint, got: %s", footerText)
	}

	// Discard paused task
	mod, cmd := m.handleSlashCommand("/discard")
	m = mod.(*tuiModel)
	if cmd == nil {
		t.Fatal("expected /discard to request a Core result")
	}
	mod, _ = m.Update(cmd())
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

	// Esc cannot discard recovery without a runtime.
	mod, _ := m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))
	m = mod.(*tuiModel)
	if m.activeRecovery == nil || !strings.Contains(m.entries[len(m.entries)-1].Text, "Paused-task management is unavailable") {
		t.Fatalf("Esc should preserve recovery and explain the missing runtime: recovery=%+v entries=%+v", m.activeRecovery, m.entries)
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
	if err := client.SetPermissionMode(core.PermissionAsk); err != nil {
		t.Fatal(err)
	}
	session, err := client.CreateSession(context.Background(), json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := client.OpenSessionRuntime(context.Background(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := newTUIModel(client, runtime, session.ID)
	m.workspace = workspace
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
