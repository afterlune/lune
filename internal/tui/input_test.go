package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

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
	if len(finished.entries) == 0 || !containsEntryText(finished.entries, "assistant", "reply") {
		t.Fatalf("transcript entries = %#v, want assistant response", finished.entries)
	}
}

func containsEntryText(entries []tuiEntry, role, text string) bool {
	for _, entry := range entries {
		if entry.role == role && strings.Contains(entry.text, text) {
			return true
		}
	}
	return false
}

func TestTUIShiftEnterInsertsNewline(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.input.SetValue("first")
	ui.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModShift}))
	if !strings.Contains(ui.input.Value(), "\n") {
		t.Fatalf("input = %q, Shift+Enter should insert a newline", ui.input.Value())
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
	go func() {
		msg := waitCommand()
		if batch, ok := msg.(tea.BatchMsg); ok {
			msg = batch[0]()
		}
		done <- msg
	}()
	<-client.started
	finished.update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape}))
	finished.update(<-done)
	if finished.running || finished.status != "Task cancelled." {
		t.Fatalf("running=%v status=%q, want completed cancellation", finished.running, finished.status)
	}
}
