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

	"github.com/afterlune/lune/core"
)

type fakeTUICompactor struct {
	fakeResponder
	compactedTurns int
	compactErr     error
	compactCalls   int
}

func (f *fakeTUICompactor) Compact(context.Context, string) (int, error) {
	f.compactCalls++
	return f.compactedTurns, f.compactErr
}

func TestTUIEnterSubmitsMultilineJSONInput(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
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
	if len(client.inputs) != 1 {
		t.Fatalf("inputs = %#v, want one input item", client.inputs)
	}
	if !strings.Contains(string(client.inputs[0].Input.Raw), `"type":"input_video"`) {
		t.Fatalf("unknown modality was not preserved: %s", client.inputs[0].Input.Raw)
	}
	if !strings.Contains(finished.View().Content, "reply") {
		t.Fatalf("view = %q, want assistant response", finished.View().Content)
	}
}

func TestTUICtrlOInsertsNewline(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.input.SetValue("first")
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'o', Mod: tea.ModCtrl}))
	if !strings.Contains(ui.input.Value(), "\n") {
		t.Fatalf("input = %q, Ctrl+O should insert a newline", ui.input.Value())
	}
}

func TestTUIEscapeCancelsActiveTask(t *testing.T) {
	client := &blockingResponder{fakeResponder: &fakeResponder{}, started: make(chan struct{})}
	ui := newTUIModel(client, false)
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
	ui := newTUIModel(&fakeResponder{}, false)
	ui.Update(toolActivityMsg{event: core.ToolCall{ID: "internal-call-id", Name: "read_file", Started: true}})
	ui.Update(toolActivityMsg{event: core.ToolCall{ID: "internal-call-id", Name: "read_file", Duration: 25, Failed: true}})
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
	history := json.RawMessage(`[{"type":"function_call","call_id":"call-1","name":"read_file","arguments":"{\"path\":\"private.txt\"}"},{"type":"function_call_output","call_id":"call-1","output":"` + toolOutput + `"}]`)
	entries := transcriptFromHistory(history)
	if len(entries) != 1 || entries[0].Text != "Reading private.txt · done" {
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
	view := ui.View().Content
	if strings.Contains(view, "read_file") || !strings.Contains(view, "Reading private.txt") {
		t.Fatalf("legacy transcript exposes internal tool name: %q", view)
	}
}

func TestTUIResumeAndDiscardCommands(t *testing.T) {
	client := &fakeResumableResponder{
		pending:  true,
		sessions: map[string]core.Session{"session-1": {ID: "session-1", Settings: core.SessionSettings{ModelProfile: "lead"}}},
		last:     "session-1",
	}
	ui := newTUIModel(client, true)
	ui.sessionID = "session-1"
	ui.input.SetValue("/resume")
	updated, command := ui.submit()
	finished := updated.(*tuiModel)
	if command != nil || !finished.sessionPicker {
		t.Fatal("/resume should open the session picker")
	}
	finished.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	finished.input.SetValue("/continue")
	_, command = finished.submit()
	if command == nil {
		t.Fatal("/continue did not start the paused task")
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
	ui := newTUIModel(client, false)
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	if len(ui.commandSuggestions) != len(tuiCommands) {
		t.Fatalf("suggestions = %#v, want all available commands", ui.commandSuggestions)
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
	if ui.input.Value() != "/continue" {
		t.Fatalf("Tab completion = %q, want /continue", ui.input.Value())
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

func TestTUIModelAndEffortPickerCommandsPersistSessionSettings(t *testing.T) {
	client := &fakeResponder{sessions: map[string]core.Session{
		"session-1": {ID: "session-1", Settings: core.SessionSettings{ModelProfile: "lead"}},
	}, last: "session-1"}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-1"
	ui.sessionSettings = core.SessionSettings{ModelProfile: "lead"}
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	if !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/model") || !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/effort") {
		t.Fatalf("suggestions = %#v, want model and effort commands", ui.commandSuggestions)
	}

	ui.input.SetValue("/model")
	ui.submit()
	if !ui.modelPicker || !strings.Contains(ui.View().Content, "review-model") {
		t.Fatalf("model picker did not show configured profiles: %q", ui.View().Content)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if ui.modelPicker || ui.sessionSettings.ModelProfile != "review" {
		t.Fatalf("model selection = %#v, picker open=%v", ui.sessionSettings, ui.modelPicker)
	}
	if saved := client.sessions[ui.sessionID]; saved.Settings.ModelProfile != "review" {
		t.Fatalf("persisted model profile = %#v", saved.Settings)
	}

	ui.input.SetValue("/effort high")
	ui.submit()
	if ui.sessionSettings.ReasoningEffort != "high" {
		t.Fatalf("direct effort command settings = %#v", ui.sessionSettings)
	}
	ui.input.SetValue("/effort")
	ui.submit()
	if !ui.effortPicker {
		t.Fatal("bare /effort should open the effort picker")
	}
	for ui.effortIndex > 0 {
		ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyUp}))
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if ui.effortPicker || ui.sessionSettings.ReasoningEffort != "" {
		t.Fatalf("default effort selection = %#v, picker open=%v", ui.sessionSettings, ui.effortPicker)
	}
	if !strings.Contains(ui.renderFooter(120), "effort default") || !strings.Contains(ui.renderFooter(120), "review") {
		t.Fatalf("footer does not show current settings: %q", ui.renderFooter(120))
	}
}

func TestPausedSessionBranchCopiesModelSettings(t *testing.T) {
	client := &fakeResponder{sessions: map[string]core.Session{
		"paused": {ID: "paused", History: json.RawMessage("null"), Settings: core.SessionSettings{ModelProfile: "review", ReasoningEffort: "high"}},
	}, last: "paused"}
	ui := newTUIModel(client, true)
	ui.sessionID = "paused"
	if err := ui.branchPausedSession(); err != nil {
		t.Fatalf("branchPausedSession: %v", err)
	}
	if got := client.sessions[ui.sessionID].Settings; got.ModelProfile != "review" || got.ReasoningEffort != "high" {
		t.Fatalf("branch settings = %#v", got)
	}
}

func TestSessionPickerArchivesRenamesAndDeletesCurrentSession(t *testing.T) {
	client := &fakeResponder{sessions: map[string]core.Session{
		"current": {ID: "current", Title: "Review", Settings: core.SessionSettings{ModelProfile: "lead"}},
		"other":   {ID: "other", Title: "Other", Settings: core.SessionSettings{ModelProfile: "lead"}},
	}, last: "current"}
	ui := newTUIModel(client, false)
	ui.sessionID = "current"
	ui.openSessionPicker()
	if len(ui.sessionList) != 2 || !strings.Contains(ui.View().Content, "Review") {
		t.Fatalf("initial session picker = %#v, view=%q", ui.sessionList, ui.View().Content)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'a'}))
	if !client.sessions["current"].Archived || len(ui.sessionList) != 1 {
		t.Fatalf("archive toggle did not hide current session: sessions=%#v list=%#v", client.sessions, ui.sessionList)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if !ui.showArchived || len(ui.sessionList) != 2 || !strings.Contains(ui.View().Content, "[archived]") {
		t.Fatalf("archived list = %#v, view=%q", ui.sessionList, ui.View().Content)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'r'}))
	if !ui.renamingSession || ui.input.Value() != "Review" {
		t.Fatalf("rename state=%v input=%q", ui.renamingSession, ui.input.Value())
	}
	ui.input.SetValue("Renamed review")
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if client.sessions["current"].Title != "Renamed review" || ui.renamingSession {
		t.Fatalf("renamed session = %#v, editing=%v", client.sessions["current"], ui.renamingSession)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'd'}))
	if !ui.deleteConfirm {
		t.Fatal("d should request delete confirmation")
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'y'}))
	if _, exists := client.sessions["current"]; exists {
		t.Fatal("confirmed delete left the current session in storage")
	}
	if ui.sessionID == "current" || ui.sessionPicker {
		t.Fatalf("current session was not replaced after deletion: id=%q picker=%v", ui.sessionID, ui.sessionPicker)
	}
}

func TestSessionPickerCanOpenArchivedOnlySessionWithoutUnarchiving(t *testing.T) {
	client := &fakeResponder{sessions: map[string]core.Session{
		"archived": {ID: "archived", Title: "Old work", Archived: true, Settings: core.SessionSettings{ModelProfile: "lead"}},
	}, last: "archived"}
	ui := newTUIModel(client, false)
	ui.sessionID = "archived"
	ui.openSessionPicker()
	if !ui.sessionPicker || len(ui.sessionList) != 0 {
		t.Fatalf("picker should remain open with no active sessions: open=%v list=%#v", ui.sessionPicker, ui.sessionList)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if !ui.showArchived || len(ui.sessionList) != 1 {
		t.Fatalf("Tab did not expose archived session: show=%v list=%#v", ui.showArchived, ui.sessionList)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if ui.sessionID != "archived" || !client.sessions["archived"].Archived {
		t.Fatalf("restoring archived session changed its archive state: id=%q session=%#v", ui.sessionID, client.sessions["archived"])
	}
}

func TestTUIForkAndRewindCommandsAreSuggestedAndDispatched(t *testing.T) {
	client := &fakeResponder{sessions: map[string]core.Session{
		"session": {ID: "session", History: json.RawMessage(`[]`), Settings: core.SessionSettings{ModelProfile: "review", ReasoningEffort: "high"}},
	}, last: "session"}
	ui := newTUIModel(client, false)
	ui.sessionID = "session"
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	if !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/fork") || !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/rewind") {
		t.Fatalf("suggestions = %#v", ui.commandSuggestions)
	}
	ui.input.SetValue("/fork")
	ui.submit()
	if ui.sessionID == "session" || ui.sessionSettings.ModelProfile != "review" || ui.sessionSettings.ReasoningEffort != "high" {
		t.Fatalf("fork state id=%q settings=%#v", ui.sessionID, ui.sessionSettings)
	}
	ui.input.SetValue("/rewind 2")
	ui.submit()
	if ui.status != "Rewound 0 user turn(s)." {
		t.Fatalf("rewind status = %q", ui.status)
	}
}

func TestTUIShiftTabCyclesPermissionModesAndKeepsDraft(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	if client.PermissionMode() != core.PermissionWorkspace {
		t.Fatalf("initial mode = %q, want workspace", client.PermissionMode())
	}
	ui.input.SetValue("keep this draft")

	for _, mode := range []core.PermissionMode{
		core.PermissionAsk,
		core.PermissionFullAccess,
		core.PermissionReadOnly,
		core.PermissionWorkspace,
	} {
		ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
		if got := client.PermissionMode(); got != mode {
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
	if client.PermissionMode() != core.PermissionWorkspace {
		t.Fatalf("mode changed while task was running: %q", client.PermissionMode())
	}
}

func TestTUIPlanCommandRemainsSeparateFromPermissionMode(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	initialPermissionMode := client.PermissionMode()

	ui.input.SetValue("/plan")
	updated, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if command != nil {
		t.Fatal("/plan should toggle locally without starting a model request")
	}
	finished := updated.(*tuiModel)
	if !finished.planMode {
		t.Fatalf("plan mode=%v, want enabled", finished.planMode)
	}
	if client.PermissionMode() != initialPermissionMode {
		t.Fatalf("/plan changed permission mode to %q", client.PermissionMode())
	}
	if finished.input.Value() != "" {
		t.Fatalf("/plan input = %q, want cleared", finished.input.Value())
	}

	finished.input.SetValue("/plan")
	finished.submit()
	if finished.planMode {
		t.Fatalf("second /plan should return to build mode: plan=%v", finished.planMode)
	}
}

func TestTUIPlanModeExplainsPermissionScope(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	ui.planMode = true

	footer := ui.renderFooter(200)
	if !strings.Contains(footer, "PLAN · read-only tools") || !strings.Contains(footer, "Workspace") {
		t.Fatalf("footer should show Plan's read-only cap and active permission: %q", footer)
	}
	ui.permissionMenu = true
	menu := ui.renderPermissionMenu()
	if !strings.Contains(menu, "Plan caps tools at read-only") || !strings.Contains(menu, "approval for visible tools") {
		t.Fatalf("Plan permission menu should explain the effective tool cap: %q", menu)
	}
	if got, want := ui.commandSuggestionHeight(), len(tuiPermissionOptions)+3; got != want {
		t.Fatalf("permission menu height = %d, want %d with Plan hint", got, want)
	}

	ui.permissionMenu = false
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if !ui.planMode || client.PermissionMode() != core.PermissionAsk {
		t.Fatalf("Plan/permission state after Shift+Tab: plan=%v permission=%q", ui.planMode, client.PermissionMode())
	}
	if !strings.Contains(ui.status, "Plan still caps tools at read-only") {
		t.Fatalf("permission status should explain Plan's tool cap: %q", ui.status)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if !ui.planMode || client.PermissionMode() != core.PermissionFullAccess {
		t.Fatalf("full-access selection should preserve Plan mode: plan=%v permission=%q", ui.planMode, client.PermissionMode())
	}
	if !strings.Contains(ui.status, "Plan still caps tools at read-only") {
		t.Fatalf("full-access status should explain Plan's tool cap: %q", ui.status)
	}
}

func TestTUIEnterRunsHighlightedSlashCommand(t *testing.T) {
	t.Run("runs default highlighted command", func(t *testing.T) {
		client := &fakeTUICompactor{compactedTurns: 2}
		ui := newTUIModel(client, false)
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
		ui := newTUIModel(client, true)
		ui.input.SetValue("/")
		ui.updateCommandSuggestions()
		ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
		ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))

		ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		if client.discarded != 1 || client.pending {
			t.Fatalf("discarded = %d, pending = %v; want selected /discard to execute", client.discarded, client.pending)
		}
	})
}

func TestTUICompactCommandAndPendingGuard(t *testing.T) {
	client := &fakeTUICompactor{compactedTurns: 2}
	ui := newTUIModel(client, false)
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
	ui := newTUIModel(&fakeResponder{}, false)
	ui.Update(modelUsageMsg{usage: core.Usage{
		InputTokens:         100,
		InputTokensDetails:  core.TokenInputDetails{CachedTokens: 25},
		OutputTokens:        40,
		TotalTokens:         140,
		OutputTokensDetails: core.TokenOutputDetails{ReasoningTokens: 12},
	}})
	ui.Update(modelUsageMsg{usage: core.Usage{
		InputTokens:        50,
		InputTokensDetails: core.TokenInputDetails{CachedTokens: 5},
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
		ui := newTUIModel(&fakeResponder{}, false)
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
	ui := newTUIModel(&fakeResponder{}, false)
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
	ui := newTUIModel(&fakeResponder{}, false)
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
	ui := newTUIModel(&fakeResponder{}, false)
	ui.workspace = workspace
	ui.preferences = uiPreferences{Theme: themeAnime, AnimeLayout: layoutStudio}
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	ui.Update(toolActivityMsg{event: core.ToolCall{
		ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"internal/tool/html.go"}`), Started: true,
	}})
	ui.Update(toolActivityMsg{event: core.ToolCall{
		ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":"internal/tool/html.go"}`), Duration: 12,
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
	read := formatToolApproval(core.ApprovalRequest{
		Tool: "read_file", Arguments: json.RawMessage(`{"path":"notes.txt"}`),
	}, workspace)
	if !strings.Contains(read, "Reading "+filepath.ToSlash(filepath.Join(workspace, "notes.txt"))) || strings.Contains(read, "read_file") || strings.Contains(read, `"path"`) {
		t.Fatalf("read approval = %q; want a human-readable path without internal payload", read)
	}
	command := formatToolApproval(core.ApprovalRequest{
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
	ui := newTUIModel(&fakeResponder{}, false)
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
	text, err := responseDisplayText(json.RawMessage(`{"output":[{"type":"function_call","call_id":"call-1","name":"read_file","arguments":"{\"path\":\"secret.txt\"}"},{"type":"custom_tool_call","call_id":"call-2","name":"internal_tool","input":"secret input"}]}`))
	if err != nil || text != "" {
		t.Fatalf("tool response display = %q, %v; want no raw tool details", text, err)
	}
}
