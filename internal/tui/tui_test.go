package tui

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/afterlune/lune/internal/model"
	"github.com/afterlune/lune/internal/tool"
)

type fakeTUICompactor struct {
	fakeResumableResponder
	compactedTurns int
	compactErr     error
	compactCalls   int
}

type planModeResponder struct {
	fakeResponder
	planModes []bool
}

func (f *planModeResponder) SetPlanMode(enabled bool) {
	f.planModes = append(f.planModes, enabled)
}

func (f *fakeTUICompactor) Compact(context.Context) (int, error) {
	f.compactCalls++
	return f.compactedTurns, f.compactErr
}

func TestTUIEnterSubmitsMultilineJSONInput(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, nil, false)
	input := "[\n  {\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"inspect\"},{\"type\":\"input_video\",\"video_url\":\"clip\"}]}\n]"
	ui.input.SetValue(input)
	updated, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if command == nil {
		t.Fatal("Enter did not start a task")
	}
	finished, ok := updated.(*tuiModel)
	if !ok {
		t.Fatalf("model type = %T, want *tuiModel", updated)
	}
	finished.Update(command())
	if len(client.inputs) != 1 || len(client.inputs[0].Items) != 1 {
		t.Fatalf("inputs = %#v, want one input item", client.inputs)
	}
	encoded, err := json.Marshal(client.inputs[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"type":"input_video"`) {
		t.Fatalf("unknown modality was not preserved: %s", encoded)
	}
	if !strings.Contains(finished.View().Content, "reply") {
		t.Fatalf("view = %q, want assistant response", finished.View().Content)
	}
}

func TestTUICtrlOInsertsNewline(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, nil, false)
	ui.input.SetValue("first")
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	if !strings.Contains(ui.input.Value(), "\n") {
		t.Fatalf("input = %q, Ctrl+O should insert a newline", ui.input.Value())
	}
}

func TestTUIEscapeCancelsActiveTask(t *testing.T) {
	client := &blockingResponder{started: make(chan struct{})}
	ui := newTUIModel(client, nil, false)
	ui.input.SetValue("long task")
	updated, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	finished, ok := updated.(*tuiModel)
	if !ok || command == nil {
		t.Fatalf("updated=%T command=%v, want task to start", updated, command != nil)
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- command() }()
	<-client.started
	finished.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	finished.Update(<-done)
	if finished.running || finished.status != "Task cancelled." {
		t.Fatalf("running=%v status=%q, want completed cancellation", finished.running, finished.status)
	}
}

func TestTUIToolTimelineShowsOnlySanitizedLifecycle(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, nil, false)
	ui.Update(toolActivityMsg{event: tool.ToolEvent{CallID: "internal-call-id", Name: "read_file", Started: true}})
	ui.Update(toolActivityMsg{event: tool.ToolEvent{CallID: "internal-call-id", Name: "read_file", Duration: 25, Failed: true}})
	view := ui.View().Content
	if !strings.Contains(view, "Reading a workspace file") || !strings.Contains(view, "failed") {
		t.Fatalf("view = %q, want humanized tool and failure status", view)
	}
	if strings.Contains(view, "read_file") || strings.Contains(view, "internal-call-id") || strings.Contains(view, "arguments") || strings.Contains(view, "output") {
		t.Fatalf("view contains internal or tool payload data: %q", view)
	}
}

func TestTUIHistoryTranscriptHidesToolArgumentsAndOutput(t *testing.T) {
	toolOutput := "secret tool output"
	history := model.NewResponseInputItems(
		model.InputFunctionCall{CallID: "call-1", Name: "read_file", Arguments: `{"path":"private.txt"}`},
		model.InputFunctionCallOutput{CallID: "call-1", Output: model.ToolOutput{Text: &toolOutput}},
	)
	entries := transcriptFromHistory(history)
	if len(entries) != 1 || entries[0].Text != "Reading private.txt · done" {
		t.Fatalf("transcript entries = %#v, want one sanitized tool lifecycle", entries)
	}
	if strings.Contains(entries[0].Text, "read_file") || strings.Contains(entries[0].Text, toolOutput) {
		t.Fatalf("transcript exposes tool payload: %#v", entries[0])
	}
}

func TestTUIRestoresLegacyToolNamesAsHumanSummaries(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, nil, false)
	ui.restoreSessionState(savedTUISession{
		Entries:    []savedTUIEntry{{Role: "tool", Text: "read_file · done"}},
		Activities: []savedActivity{{Name: "read_file", Status: "done"}},
	})
	ui.refreshPanels()
	view := ui.View().Content
	if strings.Contains(view, "read_file") || !strings.Contains(view, "Reading a workspace file") {
		t.Fatalf("legacy transcript exposes internal tool name: %q", view)
	}
}

func TestTUIResumeAndDiscardCommands(t *testing.T) {
	client := &fakeResumableResponder{pending: true}
	ui := newTUIModel(client, client, true)
	ui.input.SetValue("/resume")
	updated, command := ui.submit()
	finished := updated.(*tuiModel)
	if command == nil {
		t.Fatal("/resume did not start the task")
	}
	finished.Update(command())
	if client.resumes != 1 || client.pending {
		t.Fatalf("resume calls=%d pending=%v, want resumed once", client.resumes, client.pending)
	}
	client.pending = true
	finished.input.SetValue("/discard")
	finished.submit()
	if client.discarded != 1 || client.pending || finished.pending {
		t.Fatalf("discarded=%d responderPending=%v uiPending=%v", client.discarded, client.pending, finished.pending)
	}
}

func TestTUISlashCommandSuggestionsAndUnknownCommand(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, nil, false)
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	if len(ui.commandSuggestions) != 7 {
		t.Fatalf("suggestions = %#v, want all seven legacy commands", ui.commandSuggestions)
	}
	if !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/plan") {
		t.Fatalf("suggestions = %#v, want /plan", ui.commandSuggestions)
	}
	if !strings.Contains(ui.View().Content, "/compact") {
		t.Fatalf("view = %q, want command suggestions", ui.View().Content)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if ui.suggestionIndex != 1 {
		t.Fatalf("selected suggestion = %d, want index 1", ui.suggestionIndex)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if ui.input.Value() != "/discard" {
		t.Fatalf("Tab completion = %q, want /discard", ui.input.Value())
	}

	ui.input.SetValue("/not-a-command")
	ui.updateCommandSuggestions()
	ui.submit()
	if client.responses != 0 || len(client.inputs) != 0 {
		t.Fatalf("unknown command was sent as a prompt: responses=%d inputs=%#v", client.responses, client.inputs)
	}
	if !strings.Contains(ui.entries[len(ui.entries)-1].text, "Unknown command") {
		t.Fatalf("last entry = %#v, want unknown-command error", ui.entries[len(ui.entries)-1])
	}
}

func TestTUIShiftTabCyclesPermissionModesAndKeepsDraft(t *testing.T) {
	registry, err := tool.NewDefaultRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ui := newTUIModel(&fakeResponder{}, nil, false)
	ui.permissions = registry
	if registry.PermissionMode() != tool.PermissionModeWorkspace {
		t.Fatalf("initial mode = %q, want workspace", registry.PermissionMode())
	}
	ui.input.SetValue("keep this draft")

	for _, mode := range []tool.PermissionMode{
		tool.PermissionModeAsk,
		tool.PermissionModeFullAccess,
		tool.PermissionModeReadOnly,
		tool.PermissionModeWorkspace,
	} {
		ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
		if got := registry.PermissionMode(); got != mode {
			t.Fatalf("mode = %q, want %q", got, mode)
		}
		if ui.input.Value() != "keep this draft" {
			t.Fatalf("mode switch cleared input draft: %q", ui.input.Value())
		}
	}
	if !strings.Contains(ui.View().Content, "Workspace") || !strings.Contains(ui.status, "Workspace") {
		t.Fatalf("view/status should show active mode: view=%q status=%q", ui.View().Content, ui.status)
	}

	ui.running = true
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if registry.PermissionMode() != tool.PermissionModeWorkspace {
		t.Fatalf("mode changed while task was running: %q", registry.PermissionMode())
	}
}

func TestTUIPlanCommandRemainsSeparateFromPermissionMode(t *testing.T) {
	client := &planModeResponder{}
	registry, err := tool.NewDefaultRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ui := newTUIModel(client, nil, false)
	ui.permissions = registry
	initialPermissionMode := registry.PermissionMode()

	ui.input.SetValue("/plan")
	updated, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if command != nil {
		t.Fatal("/plan should toggle locally without starting a model request")
	}
	finished := updated.(*tuiModel)
	if !finished.planMode || len(client.planModes) != 1 || !client.planModes[0] {
		t.Fatalf("plan mode=%v updates=%v, want enabled", finished.planMode, client.planModes)
	}
	if registry.PermissionMode() != initialPermissionMode {
		t.Fatalf("/plan changed permission mode to %q", registry.PermissionMode())
	}
	if finished.input.Value() != "" {
		t.Fatalf("/plan input = %q, want cleared", finished.input.Value())
	}

	finished.input.SetValue("/plan")
	finished.submit()
	if finished.planMode || len(client.planModes) != 2 || client.planModes[1] {
		t.Fatalf("second /plan should return to build mode: plan=%v updates=%v", finished.planMode, client.planModes)
	}
}

func TestTUIEnterRunsHighlightedSlashCommand(t *testing.T) {
	t.Run("runs default highlighted command", func(t *testing.T) {
		client := &fakeTUICompactor{compactedTurns: 2}
		ui := newTUIModel(client, client, false)
		ui.input.SetValue("/")
		ui.updateCommandSuggestions()

		updated, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		finished := updated.(*tuiModel)
		if command == nil {
			t.Fatal("Enter did not execute the highlighted /compact command")
		}
		finished.Update(command())
		if client.compactCalls != 1 {
			t.Fatalf("compact calls = %d, want 1", client.compactCalls)
		}
	})

	t.Run("runs command selected with arrows", func(t *testing.T) {
		client := &fakeResumableResponder{pending: true}
		ui := newTUIModel(client, client, true)
		ui.input.SetValue("/")
		ui.updateCommandSuggestions()
		ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))

		ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		if client.discarded != 1 || client.pending {
			t.Fatalf("discarded = %d, pending = %v; want selected /discard to execute", client.discarded, client.pending)
		}
	})
}

func TestTUICompactCommandAndPendingGuard(t *testing.T) {
	client := &fakeTUICompactor{compactedTurns: 2}
	ui := newTUIModel(client, client, false)
	ui.input.SetValue("/compact")
	updated, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	finished := updated.(*tuiModel)
	if command == nil {
		t.Fatal("/compact did not start an operation")
	}
	finished.Update(command())
	if client.compactCalls != 1 || !strings.Contains(finished.entries[len(finished.entries)-1].text, "Compacted 2 older") {
		t.Fatalf("compact calls=%d entries=%#v", client.compactCalls, finished.entries)
	}

	client.pending = true
	finished.input.SetValue("/compact")
	_, command = finished.submit()
	if command != nil || client.compactCalls != 1 {
		t.Fatalf("pending task allowed compaction: command=%v calls=%d", command != nil, client.compactCalls)
	}
}

func TestTUITokenUsageShowsRequestDetailsAndTaskSessionTotals(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, nil, false)
	ui.Update(modelUsageMsg{usage: model.ResponseUsage{
		InputTokens:         100,
		InputTokensDetails:  model.TokenInputDetails{CachedTokens: 25},
		OutputTokens:        40,
		TotalTokens:         140,
		OutputTokensDetails: model.TokenOutputDetails{ReasoningTokens: 12},
	}})
	ui.Update(modelUsageMsg{usage: model.ResponseUsage{
		InputTokens:        50,
		InputTokensDetails: model.TokenInputDetails{CachedTokens: 5},
		OutputTokens:       10,
		TotalTokens:        60,
	}})
	if ui.taskUsage.requests != 2 || ui.taskUsage.input != 150 || ui.taskUsage.output != 50 || ui.taskUsage.reasoning != 12 || ui.taskUsage.total != 200 || ui.taskUsage.cached != 30 {
		t.Fatalf("task usage = %#v", ui.taskUsage)
	}
	view := ui.View().Content
	for _, expected := range []string{"in 150", "out 50", "reason 12", "20.0%"} {
		if !strings.Contains(view, expected) {
			t.Errorf("compact footer missing %q: %q", expected, view)
		}
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'b', Mod: tea.ModCtrl}))
	view = ui.View().Content
	for _, expected := range []string{
		"#1 · in 100 · out 40 · reason 12 · total 140 · cached 25",
		"#2 · in 50 · out 10 · reason 0 · total 60 · cached 5",
		"Task · in 150 · out 50 · reason 12 · total 200 · cache 30",
		"Session · in 150 · out 50 · reason 12 · total 200 · cache 30",
	} {
		if !strings.Contains(view, expected) {
			t.Errorf("details overlay missing %q: %q", expected, view)
		}
	}

	ui.Update(modelUsageMsg{})
	if !strings.Contains(ui.View().Content, "usage unavailable") {
		t.Fatalf("zero usage should be marked unavailable: %q", ui.View().Content)
	}
	if ui.sessionUsage.requests != 3 || ui.sessionUsage.measured != 2 {
		t.Fatalf("session usage = %#v, want three requests and two metered", ui.sessionUsage)
	}

	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	ui.input.SetValue("next task")
	updated, command := ui.submit()
	if command == nil {
		t.Fatal("next task did not start")
	}
	finished := updated.(*tuiModel)
	if finished.taskUsage.requests != 0 || finished.sessionUsage.requests != 3 || len(finished.usageRequests) != 3 {
		t.Fatalf("task usage=%#v session usage=%#v request details=%d after new task", finished.taskUsage, finished.sessionUsage, len(finished.usageRequests))
	}
	finished.Update(command())
}

func TestUseTUIRequiresBothStreamsToBeTerminals(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer output.Close()
	if UseTUI(input, output) {
		t.Fatal("piped streams should use the line interface")
	}
}

func TestTUILayoutFitsWideAndNarrowTerminals(t *testing.T) {
	for _, size := range []struct{ width, height int }{{width: 120, height: 30}, {width: 100, height: 30}, {width: 80, height: 24}} {
		ui := newTUIModel(&fakeResponder{}, nil, false)
		ui.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		view := ui.View().Content
		if got := lipgloss.Width(view); got > size.width {
			t.Errorf("layout width = %d, terminal width = %d", got, size.width)
		}
		if got := lipgloss.Height(view); got > size.height {
			t.Errorf("layout height = %d, terminal height = %d", got, size.height)
		}
		ui.input.SetValue("cursor alignment")
		ui.updateLayout()
		cursorView := ui.View()
		inputCursor := ui.input.Cursor()
		if cursorView.Cursor == nil || inputCursor == nil {
			t.Fatal("focused composer did not expose a terminal cursor")
		}
		if cursorView.Cursor.X != inputCursor.X || cursorView.Cursor.Y != inputCursor.Y+1+ui.chat.Height()+1 {
			t.Errorf("cursor = (%d,%d), composer expects (%d,%d)", cursorView.Cursor.X, cursorView.Cursor.Y,
				inputCursor.X, inputCursor.Y+1+ui.chat.Height()+1)
		}
		ui.input.SetValue("/")
		ui.updateCommandSuggestions()
		ui.updateLayout()
		view = ui.View().Content
		if got := lipgloss.Height(view); got > size.height {
			t.Errorf("layout with command suggestions height = %d, terminal height = %d", got, size.height)
		}
	}
	ui := newTUIModel(&fakeResponder{}, nil, false)
	ui.preferences = uiPreferences{Theme: themeAnime, AnimeLayout: layoutFocus}
	ui.colorProfile = colorprofile.TrueColor
	ui.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	ui.input.SetValue("cursor alignment")
	ui.updateLayout()
	view := ui.View()
	if got := lipgloss.Height(view.Content); got > 30 {
		t.Errorf("anime focus layout height = %d, want <= 30", got)
	}
	inputCursor := ui.input.Cursor()
	if view.Cursor == nil || inputCursor == nil || view.Cursor.Y != inputCursor.Y+lipgloss.Height(ui.renderHeader(100))+ui.chat.Height()+1 {
		t.Errorf("anime avatar shifted cursor unexpectedly: cursor=%v header=%d chat=%d", view.Cursor, lipgloss.Height(ui.renderHeader(100)), ui.chat.Height())
	}
}

func TestTUIActivityDetailsOverlayOpensAndCloses(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, nil, false)
	ui.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	ui.Update(toolActivityMsg{event: tool.ToolEvent{CallID: "call-1", Name: "read_file", Started: true}})
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

func TestUIPreferencesLoadSaveAndFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui.json")
	prefs, err := loadUIPreferences(path)
	if err != nil || prefs != defaultUIPreferences() {
		t.Fatalf("missing preferences = %#v, %v; want defaults", prefs, err)
	}
	want := uiPreferences{Theme: themeAnime, AnimeLayout: layoutStudio}
	if err := saveUIPreferences(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadUIPreferences(path)
	if err != nil || got != want {
		t.Fatalf("loaded preferences = %#v, %v; want %#v", got, err, want)
	}
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = loadUIPreferences(path)
	if err == nil || got != defaultUIPreferences() {
		t.Fatalf("invalid preferences = %#v, %v; want defaults and an error", got, err)
	}
}

func TestTUIThemeCommandPersistsAndKeepsAnimeCommandsHidden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings", "ui.json")
	ui := newTUIModel(&fakeResponder{}, nil, false)
	ui.preferencesPath = path
	ui.input.SetValue("/theme")
	ui.submit()
	if strings.Contains(ui.status, "anime") {
		t.Fatalf("bare theme command revealed hidden theme variants: %q", ui.status)
	}
	ui.input.SetValue("/theme anime studio")
	ui.submit()
	if ui.preferences != (uiPreferences{Theme: themeAnime, AnimeLayout: layoutStudio}) {
		t.Fatalf("preferences = %#v, want anime studio", ui.preferences)
	}
	loaded, err := loadUIPreferences(path)
	if err != nil || loaded != ui.preferences {
		t.Fatalf("persisted preferences = %#v, %v", loaded, err)
	}
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	if !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/theme") || strings.Contains(strings.Join(ui.commandSuggestions, " "), "anime") {
		t.Fatalf("theme command suggestions expose anime variants: %#v", ui.commandSuggestions)
	}
	ui.input.SetValue("/theme a")
	ui.updateCommandSuggestions()
	if len(ui.commandSuggestions) != 0 {
		t.Fatalf("anime subcommands should stay hidden from suggestions: %#v", ui.commandSuggestions)
	}
	ui.input.SetValue("/theme light")
	ui.submit()
	if ui.preferences.Theme != themeLight {
		t.Fatalf("theme = %q, want light", ui.preferences.Theme)
	}
}

func TestTUIActivityUsesWorkspacePathsAndStudioShowsOnlyRealState(t *testing.T) {
	workspace := t.TempDir()
	file := filepath.ToSlash(filepath.Join(workspace, "internal", "tool", "html.go"))
	ui := newTUIModel(&fakeResponder{}, nil, false)
	ui.workspace = workspace
	ui.preferences = uiPreferences{Theme: themeAnime, AnimeLayout: layoutStudio}
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	ui.Update(toolActivityMsg{event: tool.ToolEvent{
		CallID: "call-1", Name: "read_file", DisplaySummary: "Reading internal/tool/html.go", Started: true,
	}})
	ui.Update(toolActivityMsg{event: tool.ToolEvent{
		CallID: "call-1", Name: "read_file", DisplaySummary: "Reading internal/tool/html.go", Duration: 12,
	}})
	view := ui.View().Content
	if len(ui.activities) != 1 || ui.activities[0].summary != "Reading "+file || !strings.Contains(view, "single agent") || !strings.Contains(view, "Model usage") {
		t.Fatalf("studio view is missing real activity, agent status, or usage: %q", view)
	}
	if strings.Contains(view, "read_file") || strings.Contains(view, "subagent") || strings.Contains(view, "artifact") || strings.Contains(view, "0%") {
		t.Fatalf("studio view includes internal names or fabricated state: %q", view)
	}
	if got := lipgloss.Width(view); got > 120 {
		t.Errorf("wide studio width = %d, want <= 120", got)
	}
	if got := lipgloss.Height(view); got > 30 {
		t.Errorf("wide studio height = %d, want <= 30", got)
	}

	ui.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view = ui.View().Content
	if !strings.Contains(view, "STUDIO") || lipgloss.Width(view) > 80 || lipgloss.Height(view) > 24 {
		t.Fatalf("narrow studio did not stack within the terminal: %q", view)
	}
}

func TestToolApprovalUsesHumanTextAndSafeCommandPreview(t *testing.T) {
	workspace := t.TempDir()
	read := formatToolApproval(tool.PermissionRequest{
		Tool: "read_file", Arguments: json.RawMessage(`{"path":"notes.txt"}`),
	}, workspace)
	if !strings.Contains(read, "Reading "+filepath.ToSlash(filepath.Join(workspace, "notes.txt"))) || strings.Contains(read, "read_file") || strings.Contains(read, `"path"`) {
		t.Fatalf("read approval = %q; want a human-readable path without internal payload", read)
	}
	command := formatToolApproval(tool.PermissionRequest{
		Tool: "command", Arguments: json.RawMessage(`{"command":"echo hi\nrm file\u001b[31m"}`),
	})
	if !strings.Contains(command, `echo hi\nrm file[31m`) || !strings.Contains(command, "outside the workspace") {
		t.Fatalf("command approval = %q; want visible escaped preview and warning", command)
	}
}

func TestAnimeSpriteUsesHalfBlockCellsAndStateFallback(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 4))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	img.Set(0, 1, color.RGBA{G: 255, A: 255})
	img.Set(1, 0, color.RGBA{B: 255, A: 255})
	img.Set(1, 1, color.RGBA{A: 255})
	rendered := renderHalfBlock(img, img.Bounds(), 2, 1, "#000000")
	if lipgloss.Width(rendered) != 2 || lipgloss.Height(rendered) != 1 {
		t.Fatalf("half-block size = %dx%d, want 2x1", lipgloss.Width(rendered), lipgloss.Height(rendered))
	}
	if sprite := assistantSprite(stateThinking, 8, 4, colorsFor(themeAnime).Background); sprite == "" || lipgloss.Width(sprite) != 8 || lipgloss.Height(sprite) != 4 {
		t.Fatalf("embedded sprite dimensions = %dx%d, want 8x4", lipgloss.Width(sprite), lipgloss.Height(sprite))
	}
	ui := newTUIModel(&fakeResponder{}, nil, false)
	ui.preferences.Theme = themeAnime
	ui.colorProfile = colorprofile.TrueColor
	t.Setenv("NO_COLOR", "1")
	if ui.canRenderColorArt() {
		t.Fatal("NO_COLOR should disable the true-color sprite")
	}
	if stateName(ui.visualState()) != "Ready" {
		t.Fatalf("initial state = %q, want Ready", stateName(ui.visualState()))
	}
	ui.running = true
	if got := ui.visualState(); got != stateThinking {
		t.Fatalf("running without tool activity state = %v, want thinking", got)
	}
	ui.pendingApproval = &toolApprovalRequestMsg{}
	if got := ui.visualState(); got != stateApproval {
		t.Fatalf("pending approval state = %v, want approval", got)
	}
}

func TestResponseDisplayHidesRawToolCalls(t *testing.T) {
	text, err := responseDisplayText(&model.Response{Output: []model.ResponseOutputItem{
		model.OutputFunctionCall{CallID: "call-1", Name: "read_file", Arguments: `{"path":"secret.txt"}`},
		model.OutputCustomToolCall{CallID: "call-2", Name: "internal_tool", Input: "secret input"},
	}})
	if err != nil || text != "" {
		t.Fatalf("tool response display = %q, %v; want no raw tool details", text, err)
	}
}
