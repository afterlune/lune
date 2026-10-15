package tui

import (
	"encoding/json"
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
)

func TestTUIWindowTitleUsesResumeTitleAndAgentFallback(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	if got := ui.View().WindowTitle; got != defaultWindowTitle {
		t.Fatalf("empty session title = %q, want %q", got, defaultWindowTitle)
	}

	saved := core.Session{ID: "named", Title: "Review the provider setup"}
	ui.restoreSessionState(saved)
	if got, want := ui.View().WindowTitle, sessionDisplayTitle(saved); got != want {
		t.Fatalf("named session title = %q, want resume title %q", got, want)
	}

	saved = core.Session{ID: "first-turn", History: json.RawMessage(`[{"type":"message","role":"user","content":"Fix the startup error"}]`)}
	ui.restoreSessionState(saved)
	if got, want := ui.View().WindowTitle, sessionDisplayTitle(saved); got != want {
		t.Fatalf("conversation title = %q, want resume title %q", got, want)
	}
}

func TestTUIWindowTitleShowsQuestionBeforeSpinner(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.windowTitle = "Choose a provider"
	ui.running = true
	if got := ui.View().WindowTitle; got != "| Choose a provider" {
		t.Fatalf("running title = %q", got)
	}

	ui.pendingRequest = &core.PendingRequest{Kind: core.PendingUserQuestion}
	if got := ui.View().WindowTitle; got != "! Choose a provider" {
		t.Fatalf("question title = %q", got)
	}
	ui.questionOpen = false
	if got := ui.View().WindowTitle; got != "! Choose a provider" {
		t.Fatalf("dismissed question title = %q", got)
	}

	ui.pendingRequest = nil
	if got := ui.View().WindowTitle; got != "| Choose a provider" {
		t.Fatalf("resumed title = %q", got)
	}
}

func TestTUIWindowTitleSpinnerTicksAndStops(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.sessionID = "session-1"
	ui.windowTitle = "session"
	ui.running = true
	command := ui.startWindowTitleAnimation()
	if command == nil {
		t.Fatal("running session did not start title animation")
	}
	if got := ui.View().WindowTitle; got != "| session" {
		t.Fatalf("initial frame = %q", got)
	}

	_, next := ui.update(windowTitleTickMsg{sessionID: ui.sessionID, token: ui.windowTitleAnimationToken})
	if next == nil || ui.View().WindowTitle != "/ session" {
		t.Fatalf("next frame = %q, next tick=%v", ui.View().WindowTitle, next != nil)
	}

	ui.running = false
	_, next = ui.update(windowTitleTickMsg{sessionID: ui.sessionID, token: ui.windowTitleAnimationToken})
	if next != nil || ui.View().WindowTitle != "session" {
		t.Fatalf("stopped title = %q, next tick=%v", ui.View().WindowTitle, next != nil)
	}
}

func TestTUIWindowTitleStripsTerminalControlCharacters(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.windowTitle = "safe\x1b]2;injected\a title"
	got := ui.View().WindowTitle
	if strings.ContainsAny(got, "\x1b\a") {
		t.Fatalf("terminal control character leaked into window title: %q", got)
	}
}

func TestTUIWindowTitleIgnoresStaleSessionRefresh(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.sessionID = "session-1"
	ui.windowTitle = "Current title"
	ui.windowTitleRevision = 2
	ui.Update(sessionWindowTitleMsg{sessionID: "session-1", revision: 1, title: "Stale title"})
	if got := ui.View().WindowTitle; got != "Current title" {
		t.Fatalf("stale title refresh changed current title to %q", got)
	}

	ui.Update(sessionWindowTitleMsg{sessionID: "session-1", revision: 2, title: "Updated title"})
	if got := ui.View().WindowTitle; got != "Updated title" {
		t.Fatalf("current title refresh = %q", got)
	}
}
