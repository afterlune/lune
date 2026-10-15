package tui

import (
	"encoding/json"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

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
	if got := ui.View().WindowTitle; got != "Renamed review" {
		t.Fatalf("renamed current session window title = %q", got)
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
	if got := ui.View().WindowTitle; got != defaultWindowTitle {
		t.Fatalf("new session window title = %q, want %q", got, defaultWindowTitle)
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
	if got := ui.View().WindowTitle; got != "Old work" {
		t.Fatalf("restored session window title = %q", got)
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
