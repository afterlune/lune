package tui

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/afterlune/luneCore"
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
	ui.sessionID = "session-1"
	input := "[\n  {\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"inspect\"},{\"type\":\"input_video\",\"video_url\":\"clip\"}]}\n]"
	ui.input.SetValue(input)
	updated, command := ui.update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if command == nil {
		t.Fatal("Enter did not start a task")
	}
	finished, ok := updated.(*tuiModel)
	if !ok {
		t.Fatalf("model type = %T, want *tuiModel", updated)
	}
	runTUICommands(finished, command)
	if len(client.inputs) != 1 {
		t.Fatalf("inputs = %#v, want one input item", client.inputs)
	}
	if !strings.Contains(string(client.inputs[0].Input.Raw), `"type":"input_video"`) {
		t.Fatalf("unknown modality was not preserved: %s", client.inputs[0].Input.Raw)
	}
	if len(finished.entries) == 0 || !strings.Contains(finished.entries[len(finished.entries)-1].text, "reply") {
		t.Fatalf("transcript entries = %#v, want assistant response", finished.entries)
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

func TestTUIInputSupportsSelectAllUndoRedoAndCopy(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.input.SetValue("first\nsecond")
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'a', Mod: tea.ModCtrl}))
	if !ui.input.HasSelection() || ui.input.SelectedText() != "first\nsecond" {
		t.Fatalf("selection = %q, want entire multiline input", ui.input.SelectedText())
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Text: "x"}))
	if ui.input.Value() != "x" {
		t.Fatalf("input after replacing selection = %q, want x", ui.input.Value())
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'z', Mod: tea.ModCtrl}))
	if ui.input.Value() != "first\nsecond" {
		t.Fatalf("input after undo = %q, want original multiline text", ui.input.Value())
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Mod: tea.ModCtrl}))
	if ui.input.Value() != "x" {
		t.Fatalf("input after redo = %q, want x", ui.input.Value())
	}

	ui.input.SetValue("copy me")
	ui.input.SelectAll()
	originalWriter := writeClipboard
	t.Cleanup(func() { writeClipboard = originalWriter })
	var copied string
	writeClipboard = func(value string) error {
		copied = value
		return nil
	}
	_, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: 'c', Mod: tea.ModCtrl}))
	if command != nil || copied != "copy me" || ui.input.Value() != "copy me" {
		t.Fatalf("Ctrl+C copy: command=%v copied=%q input=%q", command != nil, copied, ui.input.Value())
	}
}

func TestTUIInputCutOnlyDeletesAfterClipboardWrite(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.input.SetValue("keep until clipboard works")
	ui.input.SelectAll()
	originalWriter := writeClipboard
	t.Cleanup(func() { writeClipboard = originalWriter })
	writeClipboard = func(string) error { return errors.New("clipboard unavailable") }
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}))
	if ui.input.Value() != "keep until clipboard works" {
		t.Fatalf("cut removed input after clipboard failure: %q", ui.input.Value())
	}
	writeClipboard = func(string) error { return nil }
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}))
	if ui.input.Value() != "" {
		t.Fatalf("input after successful cut = %q, want empty", ui.input.Value())
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'z', Mod: tea.ModCtrl}))
	if ui.input.Value() != "keep until clipboard works" {
		t.Fatalf("input after undoing cut = %q", ui.input.Value())
	}
}

func TestTUIKeymapEditorCapturesPersistsAndResetsBindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings", "ui.json")
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferencesPath = path
	ui.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	ui.input.SetValue("/keymap")
	ui.submit()
	if !ui.keymapOpen || !strings.Contains(ui.View().Content, "Key bindings") {
		t.Fatal("/keymap did not open the key binding editor")
	}
	if height := lipgloss.Height(ui.View().Content); height > 24 {
		t.Fatalf("keymap editor height = %d, want it to fit in 24 rows", height)
	}

	ui.keymapIndex = keymapActionIndex("toggle_activity")
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'g', Mod: tea.ModCtrl}))
	if ui.keymapCapture != "" || ui.keymapDraft["toggle_activity"] != "ctrl+g" {
		t.Fatalf("captured binding = %q, capture=%q", ui.keymapDraft["toggle_activity"], ui.keymapCapture)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	if ui.keymapOpen || ui.preferences.Keymap["toggle_activity"] != "ctrl+g" {
		t.Fatalf("keymap was not applied on close: open=%v binding=%q", ui.keymapOpen, ui.preferences.Keymap["toggle_activity"])
	}
	loaded, err := loadUIPreferences(path)
	if err != nil || loaded.Keymap["toggle_activity"] != "ctrl+g" {
		t.Fatalf("persisted keymap = %#v, %v", loaded.Keymap, err)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'g', Mod: tea.ModCtrl}))
	if !ui.detailsOpen {
		t.Fatal("custom activity-panel binding was not active")
	}

	ui.input.SetValue("/keymap reset")
	ui.submit()
	if ui.preferences.Keymap["toggle_activity"] != "ctrl+b" {
		t.Fatalf("reset keymap binding = %q, want ctrl+b", ui.preferences.Keymap["toggle_activity"])
	}
	loaded, err = loadUIPreferences(path)
	if err != nil || loaded.Keymap["toggle_activity"] != "ctrl+b" {
		t.Fatalf("reset keymap was not persisted: %#v, %v", loaded.Keymap, err)
	}
}

func TestTUIKeymapRejectsOverlappingAndAllowsExclusiveContexts(t *testing.T) {
	bindings := defaultKeymap()
	bindings["input_select_all"] = "ctrl+x"
	if _, err := normalizeKeymap(bindings); err == nil {
		t.Fatal("overlapping select-all and cut bindings should conflict")
	}
	bindings = defaultKeymap()
	bindings["input_cut"] = "shift+x"
	if _, err := normalizeKeymap(bindings); err == nil {
		t.Fatal("printable input keys should require Ctrl/Alt or a non-text key")
	}

	bindings = defaultKeymap()
	bindings["vim_append"] = "q"
	bindings["session_archive"] = "q"
	if _, err := normalizeKeymap(bindings); err != nil {
		t.Fatalf("mutually exclusive Vim/session contexts should allow a shared binding: %v", err)
	}

	ui := newTUIModel(&fakeResponder{}, false)
	ui.keymapOpen = true
	ui.keymapDraft = defaultKeymap()
	ui.keymapIndex = keymapActionIndex("input_select_all")
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'x', Mod: tea.ModCtrl}))
	if ui.keymapCapture != "input_select_all" || ui.keymapDraft["input_select_all"] != "ctrl+a" {
		t.Fatalf("conflicting capture changed binding: capture=%q key=%q", ui.keymapCapture, ui.keymapDraft["input_select_all"])
	}
}

func TestTUIEscapeCancelsActiveTask(t *testing.T) {
	client := &blockingResponder{fakeResponder: &fakeResponder{}, started: make(chan struct{})}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-1"
	ui.input.SetValue("long task")
	updated, command := ui.update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	finished, ok := updated.(*tuiModel)
	if !ok || command == nil {
		t.Fatalf("updated=%T command=%v, want task to start", updated, command != nil)
	}
	submission := command()
	updatedModel, waitCommand := finished.update(submission)
	finished = updatedModel.(*tuiModel)
	if waitCommand == nil {
		t.Fatal("accepted turn did not start waiting for Core")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- waitCommand() }()
	<-client.started
	finished.update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	finished.update(<-done)
	if finished.running || finished.status != "Task cancelled." {
		t.Fatalf("running=%v status=%q, want completed cancellation", finished.running, finished.status)
	}
}

func TestTUIToolTimelineShowsOnlySanitizedLifecycle(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.Update(toolActivityMsg{event: core.ToolCall{ID: "internal-call-id", Name: "read_file", Started: true}})
	ui.Update(toolActivityMsg{event: core.ToolCall{ID: "internal-call-id", Name: "read_file", Duration: 25, Failed: true}})
	view := ui.activityDetailsContent()
	if !strings.Contains(view, "Reading a workspace file") || !strings.Contains(view, "failed") {
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
	view := ui.activityDetailsContent() + "\n" + strings.Join(entryTexts(ui.entries), "\n")
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
	runTUICommands(finished, command)
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
	if len(ui.commandSuggestions) != len(tuiCommands)-1 {
		t.Fatalf("suggestions = %#v, want the existing bare-slash menu size", ui.commandSuggestions)
	}
	if !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/plan") ||
		!strings.Contains(strings.Join(ui.commandSuggestions, " "), "/details") ||
		!strings.Contains(strings.Join(ui.commandSuggestions, " "), "/goal") {
		t.Fatalf("suggestions = %#v, want /plan, /details, and /goal", ui.commandSuggestions)
	}
	ui.input.SetValue("/h")
	ui.updateCommandSuggestions()
	if !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/help") {
		t.Fatalf("suggestions = %#v, want /help for the /h prefix", ui.commandSuggestions)
	}
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	if !strings.Contains(ui.View().Content, "/compact") {
		t.Fatalf("view = %q, want command suggestions", ui.View().Content)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	if ui.suggestionIndex != 1 {
		t.Fatalf("selected suggestion = %d, want index 1", ui.suggestionIndex)
	}
	wantCompletion := ui.commandSuggestions[ui.suggestionIndex]
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	if ui.input.Value() != wantCompletion {
		t.Fatalf("Tab completion = %q, want %q", ui.input.Value(), wantCompletion)
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
	if !strings.Contains(ui.entries[len(ui.entries)-1].text, "/help") {
		t.Fatalf("unknown-command entry = %#v, want /help in the command list", ui.entries[len(ui.entries)-1])
	}
}

func TestTUIHelpCommandShowsCommandsWithoutStartingTask(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	ui.input.SetValue("/help")

	updated, command := ui.submit()
	finished := updated.(*tuiModel)
	if command != nil {
		t.Fatalf("/help returned a command: %v", command)
	}
	if client.responses != 0 || len(client.inputs) != 0 {
		t.Fatalf("/help started a task: responses=%d inputs=%#v", client.responses, client.inputs)
	}
	if finished.input.Value() != "" {
		t.Fatalf("input after /help = %q, want cleared input", finished.input.Value())
	}
	if len(finished.entries) == 0 {
		t.Fatal("/help did not add a help entry")
	}
	help := finished.entries[len(finished.entries)-1]
	if help.role != "notice" || help.err {
		t.Fatalf("help entry = %#v, want a non-error notice", help)
	}
	for _, expected := range []string{
		"Available commands:",
		"/autocompact [on|off|threshold|reset]",
		"/context [window <tokens>|reserve <tokens>|reset]",
		"/details [n]",
		"/goal [text|clear]",
		"/help",
		"/keymap [reset]",
		"/rewind <n>",
		"Use /keymap to view or change keyboard shortcuts.",
	} {
		if !strings.Contains(help.text, expected) {
			t.Errorf("help text %q is missing %q", help.text, expected)
		}
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
	if saved := client.sessions[ui.sessionID].Settings; saved.ModelProfile != "review" || saved.ReasoningEffort != "" {
		t.Fatalf("persisted default effort settings = %#v", saved)
	}
	footer := ui.renderFooter(120)
	if !strings.Contains(footer, "tokens — · cache —") || !strings.Contains(footer, "Reasoning effort set to Default") || strings.Contains(footer, "review") {
		t.Fatalf("compact footer should show usage and transient status without persistent settings: %q", footer)
	}
}

func TestTUIGoalCommandShowsPersistsAndClearsSessionGoal(t *testing.T) {
	client := &fakeResponder{sessions: map[string]core.Session{
		"session-1": {ID: "session-1", Settings: core.SessionSettings{ModelProfile: "lead"}},
	}, last: "session-1"}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-1"
	ui.sessionSettings = core.SessionSettings{ModelProfile: "lead"}

	ui.input.SetValue("/goal")
	ui.submit()
	if len(ui.entries) == 0 || !strings.Contains(ui.entries[len(ui.entries)-1].text, "No task goal is set") {
		t.Fatalf("bare /goal output = %#v", ui.entries)
	}

	ui.input.SetValue("/goal Keep the change focused on observable user behavior.")
	ui.submit()
	if got := ui.sessionSettings.Goal; got != "Keep the change focused on observable user behavior." {
		t.Fatalf("session goal = %q", got)
	}
	if got := client.sessions[ui.sessionID].Settings.Goal; got != ui.sessionSettings.Goal {
		t.Fatalf("persisted goal = %q, want %q", got, ui.sessionSettings.Goal)
	}
	ui.input.SetValue("/goal")
	ui.submit()
	if got := ui.entries[len(ui.entries)-1].text; !strings.Contains(got, ui.sessionSettings.Goal) {
		t.Fatalf("goal display = %q, want saved goal", got)
	}

	ui.pending = true
	ui.input.SetValue("/goal clear")
	ui.submit()
	if ui.sessionSettings.Goal != "" || client.sessions[ui.sessionID].Settings.Goal != "" {
		t.Fatalf("goal was not cleared: session=%#v persisted=%#v", ui.sessionSettings, client.sessions[ui.sessionID].Settings)
	}
	if !strings.Contains(ui.status, "paused task keeps its original goal") {
		t.Fatalf("status = %q, want paused-task behavior", ui.status)
	}
}

func TestTUIDetailsCommandListsAndShowsAnyUserMessage(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.sessionID = "session-details"
	ui.entries = []tuiEntry{
		{role: "you", text: strings.Repeat("first long message ", 50)},
		{role: "you", text: strings.Repeat("second long message ", 50)},
		{role: "you", text: strings.Repeat("third long message ", 50)},
	}
	ui.input.SetValue("/details")
	ui.submit()
	listing := ui.entries[len(ui.entries)-1].text
	for _, expected := range []string{"1 · first long message", "2 · second long message", "3 · third long message", "/details <n>"} {
		if !strings.Contains(listing, expected) {
			t.Errorf("details listing %q is missing %q", listing, expected)
		}
	}
	ui.input.SetValue("/details 2")
	ui.submit()
	if got := ui.entries[len(ui.entries)-1].text; !strings.Contains(got, "User message 2:") || !strings.Contains(got, strings.Repeat("second long message ", 50)) {
		t.Fatalf("/details 2 did not display the full second message: %q", got)
	}
}

func TestForkedSessionCopiesModelSettings(t *testing.T) {
	client := &fakeResponder{sessions: map[string]core.Session{
		"paused": {ID: "paused", History: json.RawMessage("null"), Settings: core.SessionSettings{ModelProfile: "review", ReasoningEffort: "high"}},
	}, last: "paused"}
	ui := newTUIModel(client, true)
	ui.sessionID = "paused"
	ui.forkSession()
	if ui.sessionID == "paused" {
		t.Fatal("fork did not switch to the new session")
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
	updated, command := ui.update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	if command != nil {
		t.Fatal("/plan should toggle locally without starting a model request")
	}
	finished := updated.(*tuiModel)
	if !finished.planMode {
		t.Fatalf("plan mode=%v, want enabled", finished.planMode)
	}
	if finished.currentPermissionMode() != initialPermissionMode || client.PermissionMode() != core.PermissionReadOnly {
		t.Fatalf("Plan/permission state = %q/%q, want independent active mode %q with Core enforcing read-only tools", finished.currentPermissionMode(), client.PermissionMode(), initialPermissionMode)
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
	if !ui.planMode || client.PermissionMode() != core.PermissionWorkspace {
		t.Fatalf("Plan and permission state = %v/%q, want Plan with Workspace", ui.planMode, client.PermissionMode())
	}
	if !strings.Contains(footer, "/plan") || !strings.Contains(footer, "permissions") {
		t.Fatalf("compact footer should retain Plan and permission shortcuts: %q", footer)
	}
	ui.permissionMenu = true
	menu := ui.renderPermissionMenu()
	if !strings.Contains(menu, "Permission mode · current: Workspace") || !strings.Contains(menu, "Plan caps tools at read-only") || !strings.Contains(menu, "approval for visible tools") {
		t.Fatalf("Plan permission menu should explain the effective tool cap: %q", menu)
	}
	if got, want := ui.commandSuggestionHeight(), len(tuiPermissionOptions)+3; got != want {
		t.Fatalf("permission menu height = %d, want %d with Plan hint", got, want)
	}

	ui.permissionMenu = false
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if !ui.planMode || ui.currentPermissionMode() != core.PermissionAsk || client.PermissionMode() != core.PermissionReadOnly {
		t.Fatalf("Plan/permission state after Shift+Tab: plan=%v active=%q core=%q", ui.planMode, ui.currentPermissionMode(), client.PermissionMode())
	}
	if !strings.Contains(ui.status, "Plan mode still limits tools to read-only") {
		t.Fatalf("permission status should explain Plan's tool cap: %q", ui.status)
	}
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab, Mod: tea.ModShift}))
	if !ui.planMode || ui.currentPermissionMode() != core.PermissionFullAccess || client.PermissionMode() != core.PermissionReadOnly {
		t.Fatalf("full-access selection should preserve Plan mode: plan=%v active=%q core=%q", ui.planMode, ui.currentPermissionMode(), client.PermissionMode())
	}
	if !strings.Contains(ui.status, "Plan mode still limits tools to read-only") {
		t.Fatalf("full-access status should explain Plan's tool cap: %q", ui.status)
	}
}

func TestTUIEnterRunsHighlightedSlashCommand(t *testing.T) {
	t.Run("runs default highlighted command", func(t *testing.T) {
		client := &fakeTUICompactor{compactedTurns: 2}
		ui := newTUIModel(client, false)
		ui.sessionID = "session-1"
		ui.input.SetValue("/compact")
		ui.updateCommandSuggestions()

		updated, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		finished := updated.(*tuiModel)
		if command == nil {
			t.Fatal("Enter did not execute the highlighted /compact command")
		}
		runTUICommands(finished, command)
		if client.compactCalls != 1 {
			t.Fatalf("compact calls = %d, want 1", client.compactCalls)
		}
	})

	t.Run("runs command selected with arrows", func(t *testing.T) {
		client := &fakeResumableResponder{pending: true}
		ui := newTUIModel(client, true)
		ui.sessionID = "session-1"
		ui.input.SetValue("/")
		ui.updateCommandSuggestions()
		for ui.commandSuggestions[ui.suggestionIndex] != "/discard" {
			ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
		}

		ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
		if client.discarded != 1 || client.pending {
			t.Fatalf("discarded = %d, pending = %v; want selected /discard to execute", client.discarded, client.pending)
		}
	})
}

func TestTUICompactCommandAndPendingGuard(t *testing.T) {
	client := &fakeTUICompactor{compactedTurns: 2}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-1"
	ui.input.SetValue("/compact")
	updated, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	finished := updated.(*tuiModel)
	if command == nil {
		t.Fatal("/compact did not start an operation")
	}
	runTUICommands(finished, command)
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
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-1"
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
	details := ui.activityDetailsContent()
	for _, expected := range []string{
		"#1 · in 100 · out 40 · reason 12 · total 140 · cached 25",
		"#2 · in 50 · out 10 · reason 0 · total 60 · cached 5",
		"Session usage by agent (all 2 requests)",
		"Unattributed · 2 request(s) · 2 metered · in 150 · out 50 · reason 12 · total 200 · cache 30 (20.0%)",
		"Task · in 150 · out 50 · reason 12 · total 200 · cache 30",
		"Session · in 150 · out 50 · reason 12 · total 200 · cache 30",
	} {
		if !strings.Contains(details, expected) {
			t.Errorf("usage details missing %q: %q", expected, details)
		}
	}
	if ui.activity.TotalLineCount() <= ui.activity.Height() {
		t.Fatalf("activity viewport lines=%d height=%d; expected scrollable usage details", ui.activity.TotalLineCount(), ui.activity.Height())
	}

	ui.Update(modelUsageMsg{})
	if !strings.Contains(ui.activityDetailsContent(), "usage unavailable") {
		t.Fatalf("zero usage should be marked unavailable: %q", ui.activityDetailsContent())
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
	runTUICommands(finished, command)
}

func TestTUIUsageDetailsPreserveAgentAndModelAttribution(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = themeDark
	ui.sessionID = "session-current"
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	usage := core.Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120}
	ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventUsage, SessionID: ui.sessionID, AgentID: "worker-1",
		ModelProfile: "review", Provider: "deepseek", Model: "deepseek-worker", Usage: usage,
	}})
	current := ui.usageRequests[0].String()
	for _, expected := range []string{"worker-1", "profile=review", "deepseek/deepseek-worker", "in 100", "out 20"} {
		if !strings.Contains(current, expected) {
			t.Fatalf("current usage row missing %q: %q", expected, current)
		}
	}

	ui.restoreSessionState(core.Session{UsageRequests: []core.UsageRecord{
		{
			Request: 7, Usage: usage, Available: true, AgentID: "worker-2", ModelProfile: "text",
			Provider: "anthropic", Model: "claude-worker",
		},
		{Request: 8, Usage: usage, Available: true, AgentID: "lead", ModelProfile: "lead", ContextWindow: 32000},
	}})
	restored := ui.usageRequests[0].String()
	for _, expected := range []string{"#7", "worker-2", "profile=text", "anthropic/claude-worker"} {
		if !strings.Contains(restored, expected) {
			t.Fatalf("restored usage row missing %q: %q", expected, restored)
		}
	}
	if ui.contextWindow != 32000 || ui.contextInput != usage.InputTokens {
		t.Fatalf("restored context usage = input %d / window %d", ui.contextInput, ui.contextWindow)
	}
	if details := ui.activityDetailsContent(); !strings.Contains(details, "Context · ctx 100/32.0k (0%)") {
		t.Fatalf("restored details missing context ratio: %q", details)
	}
}

func TestTUIContextUsageFooterUsesOnlyKnownLimits(t *testing.T) {
	for _, size := range []struct{ width, height int }{{width: 80, height: 24}, {width: 120, height: 30}} {
		ui := newTUIModel(&fakeResponder{}, false)
		ui.preferences.Theme = themeDark
		ui.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		ui.contextWindow = 64000
		ui.contextInput = 12300
		footer := tuiScreenshotANSI.ReplaceAllString(ui.renderFooter(size.width), "")
		if !strings.Contains(footer, "ctx 12.3k/64.0k (19%)") {
			t.Fatalf("%dx%d footer missing configured context ratio: %q", size.width, size.height, footer)
		}
		if got := lipgloss.Width(ui.renderFooter(size.width)); got > size.width {
			t.Fatalf("%dx%d context footer width = %d", size.width, size.height, got)
		}
		ui.contextWindow = 0
		if got := tuiScreenshotANSI.ReplaceAllString(ui.renderFooter(size.width), ""); !strings.Contains(got, "ctx 12.3k/unknown") {
			t.Fatalf("%dx%d footer should label unknown window: %q", size.width, size.height, got)
		}
		ui.contextInput = 0
		if got := tuiScreenshotANSI.ReplaceAllString(ui.renderFooter(size.width), ""); strings.Contains(got, "ctx ") {
			t.Fatalf("%dx%d footer inferred context usage without input/window data: %q", size.width, size.height, got)
		}
	}
}

func TestTUIContextUsageLabelsMissingServerInputAsEstimate(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = themeDark
	ui.sessionID = "session-estimate"
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventUsage, SessionID: ui.sessionID, AgentID: "lead", ContextWindow: 64000,
		EstimatedInputTokens: 1200, Usage: core.Usage{OutputTokens: 6, TotalTokens: 6},
	}})
	if ui.contextInput != 1200 || !ui.contextEstimated || ui.taskUsage.input != 0 || ui.taskUsage.estimatedInput != 1200 {
		t.Fatalf("context input=%d estimated=%v task totals=%#v", ui.contextInput, ui.contextEstimated, ui.taskUsage)
	}
	footer := tuiScreenshotANSI.ReplaceAllString(ui.renderFooter(120), "")
	if !strings.Contains(footer, "ctx ~1.2k/64.0k (2%)") {
		t.Fatalf("footer does not identify estimated input: %q", footer)
	}
	details := ui.activityDetailsContent()
	for _, expected := range []string{"Context · ctx ~1.2k/64.0k (2%) · estimate; image contents excluded", "in ~1.2k", "est input ~1.2k"} {
		if !strings.Contains(details, expected) {
			t.Errorf("usage details %q missing %q", details, expected)
		}
	}
}

func TestTUIContextCommandShowsPersistsAndResetsOverrides(t *testing.T) {
	defaults := core.ContextDefaults{ContextWindow: 64000, ReserveOutputTokens: 4096}
	client := &fakeResponder{
		contextDefaults: defaults,
		sessions: map[string]core.Session{
			"session-context": {ID: "session-context", Settings: core.SessionSettings{ModelProfile: "lead"}},
		},
	}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-context"
	ui.sessionSettings = core.SessionSettings{ModelProfile: "lead", Goal: "keep the goal", ReasoningEffort: "high"}

	ui.input.SetValue("/context")
	ui.submit()
	view := ui.entries[len(ui.entries)-1].text
	for _, expected := range []string{"64.0k (project default)", "4.1k (project default)", "context window sets its trigger budget", "output reserve leaves room"} {
		if !strings.Contains(strings.ToLower(view), strings.ToLower(expected)) {
			t.Fatalf("context view %q is missing %q", view, expected)
		}
	}

	ui.input.SetValue("/context window 32000")
	ui.submit()
	if ui.sessionSettings.ContextWindow == nil || *ui.sessionSettings.ContextWindow != 32000 || ui.contextWindow != 32000 {
		t.Fatalf("context window override=%v effective=%d", ui.sessionSettings.ContextWindow, ui.contextWindow)
	}
	if !strings.Contains(ui.status, "Automatic compaction can use it") {
		t.Fatalf("context window status = %q, want automatic-compaction status", ui.status)
	}
	ui.input.SetValue("/context reserve 2048")
	ui.submit()
	if ui.sessionSettings.ReserveOutputTokens == nil || *ui.sessionSettings.ReserveOutputTokens != 2048 {
		t.Fatalf("reserve override = %v", ui.sessionSettings.ReserveOutputTokens)
	}
	persisted := client.sessions[ui.sessionID].Settings
	if persisted.Goal != "keep the goal" || persisted.ReasoningEffort != "high" {
		t.Fatalf("context command replaced unrelated session settings: %#v", persisted)
	}

	ui.input.SetValue("/context window reset")
	ui.submit()
	if ui.sessionSettings.ContextWindow != nil || ui.sessionSettings.ReserveOutputTokens == nil || *ui.sessionSettings.ReserveOutputTokens != 2048 || ui.contextWindow != defaults.ContextWindow {
		t.Fatalf("window reset changed unexpected values: settings=%#v effective=%d", ui.sessionSettings, ui.contextWindow)
	}
	ui.input.SetValue("/context reset")
	ui.submit()
	if ui.sessionSettings.ContextWindow != nil || ui.sessionSettings.ReserveOutputTokens != nil {
		t.Fatalf("full context reset left overrides: %#v", ui.sessionSettings)
	}
	if settings := client.sessions[ui.sessionID].Settings; settings.ModelProfile != "lead" || settings.Goal != "keep the goal" || settings.ReasoningEffort != "high" {
		t.Fatalf("reset command replaced unrelated session settings: %#v", settings)
	}

	ui.pending = true
	ui.input.SetValue("/context window 48000")
	ui.submit()
	if !strings.Contains(ui.status, "paused task keeps its original context settings") {
		t.Fatalf("status = %q, want paused-task behavior", ui.status)
	}
}

func TestTUIAutoCompactCommandPersistsSettingsAndShowsRuntimeBehavior(t *testing.T) {
	client := &fakeResponder{
		contextDefaults: core.ContextDefaults{AutoCompact: true, AutoCompactThreshold: 0.75},
		sessions: map[string]core.Session{
			"session-autocompact": {ID: "session-autocompact", Settings: core.SessionSettings{ModelProfile: "lead"}},
		},
	}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-autocompact"
	ui.sessionSettings = core.SessionSettings{ModelProfile: "lead", Goal: "preserve this goal"}

	ui.input.SetValue("/autocompact")
	ui.submit()
	view := ui.entries[len(ui.entries)-1].text
	for _, expected := range []string{"on (project default)", "75% (project default)", "runs before model requests", "set a context window"} {
		if !strings.Contains(strings.ToLower(view), strings.ToLower(expected)) {
			t.Fatalf("auto-compaction view %q is missing %q", view, expected)
		}
	}

	ui.input.SetValue("/autocompact off")
	ui.submit()
	if ui.sessionSettings.AutoCompact == nil || *ui.sessionSettings.AutoCompact || !strings.Contains(ui.status, "applies to future tasks") {
		t.Fatalf("off setting/status = %#v / %q", ui.sessionSettings.AutoCompact, ui.status)
	}
	ui.input.SetValue("/autocompact threshold 0.6")
	ui.submit()
	if ui.sessionSettings.AutoCompactThreshold == nil || *ui.sessionSettings.AutoCompactThreshold != 0.6 {
		t.Fatalf("threshold override = %v", ui.sessionSettings.AutoCompactThreshold)
	}
	ui.input.SetValue("/autocompact threshold 1.1")
	ui.submit()
	if ui.sessionSettings.AutoCompactThreshold == nil || *ui.sessionSettings.AutoCompactThreshold != 0.6 {
		t.Fatalf("invalid threshold changed settings: %v", ui.sessionSettings.AutoCompactThreshold)
	}
	ui.input.SetValue("/autocompact threshold NaN")
	ui.submit()
	if ui.sessionSettings.AutoCompactThreshold == nil || *ui.sessionSettings.AutoCompactThreshold != 0.6 {
		t.Fatalf("non-finite threshold changed settings: %v", ui.sessionSettings.AutoCompactThreshold)
	}
	ui.input.SetValue("/autocompact reset")
	ui.submit()
	if ui.sessionSettings.AutoCompact != nil || ui.sessionSettings.AutoCompactThreshold != nil {
		t.Fatalf("reset left session overrides: %#v", ui.sessionSettings)
	}
	if saved := client.sessions[ui.sessionID].Settings; saved.Goal != "preserve this goal" || saved.ModelProfile != "lead" {
		t.Fatalf("auto-compaction commands replaced unrelated settings: %#v", saved)
	}

	ui.pending = true
	ui.input.SetValue("/autocompact off")
	ui.submit()
	if !strings.Contains(ui.status, "paused task keeps its original settings") {
		t.Fatalf("status = %q, want paused-task behavior", ui.status)
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

func TestTUINewSessionClearsPreviousContextEstimate(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	ui.sessionID = "session-old"
	ui.contextWindow = 64000
	ui.contextInput = 1200
	ui.contextEstimated = true
	ui.newSession()
	if ui.sessionID == "session-old" || ui.contextWindow != 0 || ui.contextInput != 0 || ui.contextEstimated {
		t.Fatalf("new session retained previous context: id=%q window=%d input=%d estimated=%v",
			ui.sessionID, ui.contextWindow, ui.contextInput, ui.contextEstimated)
	}
}

func TestTUIUsageDetailsSummarizeRecentRequestsByAgent(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.usageRequests = []tokenUsageRecord{
		{request: 1, agentID: "lead", available: true, usage: core.Usage{
			InputTokens: 100, OutputTokens: 20, TotalTokens: 120, InputTokensDetails: core.TokenInputDetails{CachedTokens: 20},
		}},
		{request: 2, agentID: "research", available: true, usage: core.Usage{
			InputTokens: 50, OutputTokens: 10, TotalTokens: 60, InputTokensDetails: core.TokenInputDetails{CachedTokens: 5},
		}},
		{request: 3, agentID: "research", available: false},
		{request: 4, available: false},
	}
	details := ui.activityDetailsContent()
	for _, expected := range []string{
		"Recent usage by agent (4 requests)",
		"Unattributed · 1 request(s) · 0 metered · usage unavailable",
		"lead · 1 request(s) · 1 metered · in 100 · out 20 · reason 0 · total 120 · cache 20 (20.0%)",
		"research · 2 request(s) · 1 metered · in 50 · out 10 · reason 0 · total 60 · cache 5 (10.0%)",
	} {
		if !strings.Contains(details, expected) {
			t.Errorf("usage details missing %q: %q", expected, details)
		}
	}
}

func TestTUIUsageDetailsUsePersistedSessionAgentTotals(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.restoreSessionState(core.Session{
		UsageRequests:      []core.UsageRecord{{Request: 206, AgentID: "lead", Available: true, Usage: core.Usage{InputTokens: 1, TotalTokens: 1}}},
		AgentUsageComplete: true,
		AgentUsage: map[string]core.UsageTotals{
			"lead":     {Requests: 205, Measured: 204, Input: 32000, EstimatedInput: 1200, Output: 8000, Reasoning: 1000, Total: 40000, Cached: 6000},
			"research": {Requests: 42, Measured: 42, Input: 9000, Output: 500, Total: 9500, Cached: 0},
		},
		SessionUsage: core.UsageTotals{Requests: 247},
	})
	details := ui.activityDetailsContent()
	for _, expected := range []string{
		"Session usage by agent (all 247 requests)",
		"lead · 205 request(s) · 204 metered · est input ~1.2k · in 32.0k · out 8.0k · reason 1.0k · total 40.0k · cache 6.0k (18.8%)",
		"research · 42 request(s) · 42 metered · in 9.0k · out 500 · reason 0 · total 9.5k · cache 0 (0.0%)",
	} {
		if !strings.Contains(details, expected) {
			t.Errorf("session usage details missing %q: %q", expected, details)
		}
	}

	ui.agentUsageComplete = false
	if details := ui.usageByAgentDetails(); !strings.Contains(details, "older requests may be missing") {
		t.Fatalf("incomplete legacy totals lack a coverage note: %q", details)
	}
	ui.agentUsage = nil
	ui.usageRequests = nil
	ui.sessionUsage.requests = 5
	if details := ui.usageByAgentDetails(); !strings.Contains(details, "No retained requests are available") {
		t.Fatalf("legacy totals without retained details lack an explanation: %q", details)
	}
}

func TestTUIViewsUseColorAndMarkersWithoutBold(t *testing.T) {
	for _, theme := range []themeName{themeDark, themeLight} {
		t.Run(string(theme), func(t *testing.T) {
			ui := newTUIModel(&fakeResponder{}, false)
			ui.preferences.Theme = theme
			ui.sessionID = "session-current"
			ui.sessionList = workspaceFixtureSessions()
			ui.sessionIndex = 1
			ui.queued = []core.QueuedTurnState{{ExecutionID: "queued-1", Position: 1, Status: "queued"}}
			ui.queueIndex = 0
			ui.commandSuggestions = []string{"/help", "/model"}
			ui.suggestionIndex = 0
			ui.modelOptions = []core.ModelOption{{Profile: "lead", Provider: "deepseek", Model: "deepseek-main"}}
			ui.modelIndex = 0
			ui.effortIndex = 1
			ui.permissionIndex = 1
			ui.entries = []tuiEntry{{role: "you", text: "Review the config."}, {role: "assistant", text: "The selected option is marked by its arrow and color."}}
			views := []string{
				ui.renderHeader(120), ui.renderQueuePicker(), ui.renderPlanReview(), ui.renderCommandSuggestions(),
				ui.renderKeymapEditor(), ui.renderSessionPicker(), ui.renderModelPicker(), ui.renderEffortPicker(),
				ui.renderPermissionMenu(), ui.View().Content,
			}
			combined := strings.Join(views, "\n")
			if !strings.Contains(tuiScreenshotANSI.ReplaceAllString(combined, ""), "›") {
				t.Fatal("selected rows lost their structural arrow marker")
			}
			for _, line := range parseTUIScreenshotLines(combined, colorsFor(theme).Foreground) {
				for _, run := range line {
					if run.bold {
						t.Fatalf("TUI %s view contains bold text %q", theme, run.text)
					}
				}
			}
		})
	}
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
		expectedX := inputCursor.X
		expectedY := inputCursor.Y
		if cursorView.Cursor.X != expectedX || cursorView.Cursor.Y != expectedY {
			t.Errorf("cursor = (%d,%d), composer expects (%d,%d)", cursorView.Cursor.X, cursorView.Cursor.Y,
				expectedX, expectedY)
		}
		ui.input.SetValue("/")
		ui.updateCommandSuggestions()
		ui.updateLayout()
		view = ui.View().Content
		if got := lipgloss.Height(view); got > size.height {
			t.Errorf("layout with command suggestions height = %d, terminal height = %d", got, size.height)
		}
	}
}

func TestTUICommandSuggestionsScrollWithinBoundedHeight(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	ui.suggestionIndex = len(ui.commandSuggestions) - 1
	start, end := commandSuggestionRange(ui.suggestionIndex, len(ui.commandSuggestions))
	if end-start > maxCommandSuggestions || start == 0 {
		t.Fatalf("suggestion range = %d:%d for selected index %d", start, end, ui.suggestionIndex)
	}
	view := ui.renderCommandSuggestions()
	if !strings.Contains(view, ui.commandSuggestions[ui.suggestionIndex]) {
		t.Fatalf("suggestions do not show selected command %q: %q", ui.commandSuggestions[ui.suggestionIndex], view)
	}
	if got := lipgloss.Height(view); got > maxCommandSuggestions+1 {
		t.Fatalf("suggestion height = %d, want at most %d", got, maxCommandSuggestions+1)
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
	if err != nil || !reflect.DeepEqual(prefs, defaultUIPreferences()) {
		t.Fatalf("missing preferences = %#v, %v; want defaults", prefs, err)
	}
	want := defaultUIPreferences()
	want.Theme = themeLight
	if err := saveUIPreferences(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadUIPreferences(path)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("loaded preferences = %#v, %v; want %#v", got, err, want)
	}
	if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err = loadUIPreferences(path)
	if err == nil || !reflect.DeepEqual(got, defaultUIPreferences()) {
		t.Fatalf("invalid preferences = %#v, %v; want defaults and an error", got, err)
	}
}

func TestUIPreferencesDefaultsLegacyKeymapAndFallsBackOnInvalidBindings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ui.json")
	if err := os.WriteFile(path, []byte(`{"theme":"light","anime_layout":"studio"}`), 0600); err != nil {
		t.Fatal(err)
	}
	prefs, err := loadUIPreferences(path)
	if err != nil || prefs.Theme != themeLight || !reflect.DeepEqual(prefs.Keymap, defaultKeymap()) {
		t.Fatalf("legacy preferences = %#v, %v; want supported theme and default keymap", prefs, err)
	}

	if err := os.WriteFile(path, []byte(`{"theme":"light","keymap":{"input_select_all":"ctrl+x"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	prefs, err = loadUIPreferences(path)
	if err == nil || prefs.Theme != themeLight || !reflect.DeepEqual(prefs.Keymap, defaultKeymap()) {
		t.Fatalf("invalid keymap fallback = %#v, %v; want default bindings and preserved theme", prefs, err)
	}
}

func TestTUIThemeCommandSupportsAutoAndResetsSuccessfulInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings", "ui.json")
	ui := newTUIModel(&fakeResponder{}, false)
	ui.preferences.Theme = themeDark
	ui.preferencesPath = path
	ui.input.SetValue("/theme")
	ui.submit()
	if !strings.Contains(ui.status, "/theme auto") || !strings.Contains(ui.status, "/theme dark") || !strings.Contains(ui.status, "/theme light") {
		t.Fatalf("bare theme command should list supported themes: %q", ui.status)
	}
	if ui.input.Value() != "" {
		t.Fatalf("bare theme command left input behind: %q", ui.input.Value())
	}
	ui.input.SetValue("/theme anime studio")
	ui.submit()
	if ui.preferences.Theme != themeDark || !strings.Contains(ui.status, "Use /theme auto") || ui.input.Value() != "/theme anime studio" {
		t.Fatalf("unsupported theme changed preferences: %#v status=%q", ui.preferences, ui.status)
	}
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	if !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/theme") || !strings.Contains(strings.Join(ui.commandSuggestions, " "), "/details") || strings.Contains(strings.Join(ui.commandSuggestions, " "), "/fold") {
		t.Fatalf("theme suggestions = %#v", ui.commandSuggestions)
	}
	ui.input.SetValue("/theme a")
	ui.updateCommandSuggestions()
	if len(ui.commandSuggestions) != 0 {
		t.Fatalf("unknown theme values should not appear as commands: %#v", ui.commandSuggestions)
	}
	ui.input.SetValue("/theme light")
	ui.submit()
	if ui.preferences.Theme != themeLight {
		t.Fatalf("theme = %q, want light", ui.preferences.Theme)
	}
	if ui.input.Value() != "" || len(ui.commandSuggestions) != 0 {
		t.Fatalf("successful theme command left draft or suggestions: %q / %#v", ui.input.Value(), ui.commandSuggestions)
	}
	loaded, err := loadUIPreferences(path)
	if err != nil || loaded.Theme != themeLight {
		t.Fatalf("persisted theme = %#v, %v", loaded, err)
	}

	ui.input.SetValue("/theme auto")
	ui.submit()
	loaded, err = loadUIPreferences(path)
	if err != nil || ui.preferences.Theme != themeAuto || loaded.Theme != themeAuto || ui.input.Value() != "" {
		t.Fatalf("auto theme = %#v, loaded=%#v, input=%q, err=%v", ui.preferences, loaded, ui.input.Value(), err)
	}
}

func TestScrollbackFlushWaitsUntilTransientUICloses(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.entries = []tuiEntry{{role: "notice", text: "Permission mode changed."}}
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	if command := ui.flushScrollback(); command != nil || ui.printedEntryCount != 0 {
		t.Fatalf("flush during command predictions = (%v, %d), want deferred", command != nil, ui.printedEntryCount)
	}
	ui.resetInput()
	if command := ui.flushScrollback(); command == nil || ui.printedEntryCount != 1 {
		t.Fatalf("flush after closing predictions = (%v, %d), want one printed entry", command != nil, ui.printedEntryCount)
	}

	ui.printedEntryCount = 0
	ui.permissionMenu = true
	if command := ui.flushScrollback(); command != nil || ui.printedEntryCount != 0 {
		t.Fatalf("flush during permission menu = (%v, %d), want deferred", command != nil, ui.printedEntryCount)
	}
}

func TestUpdateDefersScrollbackUntilTransientPanelRedraws(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.entries = []tuiEntry{{role: "notice", text: "Permission mode changed."}}
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	_, command := ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	if command == nil || ui.hasTransientPanel() || ui.printedEntryCount != 0 {
		t.Fatalf("closing predictions = (command=%v, transient=%v, printed=%d), want redraw delay", command != nil, ui.hasTransientPanel(), ui.printedEntryCount)
	}
	ui.Update(flushScrollbackMsg{})
	if ui.printedEntryCount != 1 {
		t.Fatalf("printed entries after redraw = %d, want one", ui.printedEntryCount)
	}
}

func TestTUIActivityUsesWorkspacePathsAndUserVisibleToolNames(t *testing.T) {
	workspace := t.TempDir()
	file := filepath.ToSlash(filepath.Join(workspace, "internal", "tool", "html.go"))
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
	if len(ui.activities) != 1 || ui.activities[0].summary != "Reading "+file || !strings.Contains(view, "Lead agent") || !strings.Contains(view, "Model usage") {
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

func TestResponseDisplayHidesRawToolCalls(t *testing.T) {
	text, err := responseDisplayText(json.RawMessage(`{"output":[{"type":"function_call","call_id":"call-1","name":"read_file","arguments":"{\"path\":\"secret.txt\"}"},{"type":"custom_tool_call","call_id":"call-2","name":"internal_tool","input":"secret input"}]}`))
	if err != nil || text != "" {
		t.Fatalf("tool response display = %q, %v; want no raw tool details", text, err)
	}
}
