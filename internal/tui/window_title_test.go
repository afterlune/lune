package tui

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	core "github.com/afterlune/luneCore"
)

func TestTUIWindowTitleUsesLatestTaskAndSessionFallback(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	if got, want := ui.View().WindowTitle, "lune | luneAgent"; got != want {
		t.Fatalf("empty session title = %q, want %q", got, want)
	}

	saved := core.Session{ID: "named", Title: "Review the provider setup"}
	ui.restoreSessionState(saved)
	if got, want := ui.View().WindowTitle, "Review the provider setup | luneAgent"; got != want {
		t.Fatalf("named session title = %q, want %q", got, want)
	}

	saved = core.Session{ID: "first-turn", History: json.RawMessage(`[{"type":"message","role":"user","content":"Fix the startup error"}]`)}
	ui.restoreSessionState(saved)
	if got, want := ui.View().WindowTitle, "Fix the startup error | luneAgent"; got != want {
		t.Fatalf("conversation task title = %q, want %q", got, want)
	}
}

func TestTUIWindowTitleReflectsRunningQuestionAndApprovalStates(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.windowTitle = "Old session title"
	ui.setTaskTitle("梳理当前工具调用")
	ui.running = true
	ui.modelStreamTaskID = "task-1"
	if got, want := ui.View().WindowTitle, "⠋ Thinking... · 梳理当前工具调用"; got != want {
		t.Fatalf("running title = %q, want %q", got, want)
	}

	question := core.UserQuestionRequest{Question: "下一步优先做哪条线？"}
	ui.pendingRequest = &core.PendingRequest{Kind: core.PendingUserQuestion, UserQuestion: &question}
	if got, want := ui.View().WindowTitle, "⚠ 待回答: 下一步优先做哪条线？ | 梳理当前工具调用"; got != want {
		t.Fatalf("question title = %q, want %q", got, want)
	}

	ui.pendingRequest = nil
	ui.question = nil
	ui.pendingApproval = &toolApprovalRequestMsg{request: core.ApprovalRequest{
		Tool: "command", Arguments: json.RawMessage(`{"command":"git push"}`),
	}}
	if got, want := ui.View().WindowTitle, "⚠ 待审批: git push | 梳理当前工具调用"; got != want {
		t.Fatalf("approval title = %q, want %q", got, want)
	}
}

func TestTUIWindowTitleSpinnerTicksAt100Milliseconds(t *testing.T) {
	if windowTitleTickPeriod != 100*time.Millisecond {
		t.Fatalf("window title tick = %s, want 100ms", windowTitleTickPeriod)
	}
	ui := newTUIModel(&fakeResponder{}, false)
	ui.setTaskTitle("Inspect the tool stream")
	ui.running = true
	command := ui.startWindowTitleAnimation()
	if command == nil || !strings.HasPrefix(ui.View().WindowTitle, "⠋ ") {
		t.Fatalf("initial spinner title/command = %q / %v", ui.View().WindowTitle, command != nil)
	}
	_, next := ui.Update(windowTitleTickMsg{sessionID: ui.sessionID, token: ui.windowTitleAnimationToken})
	if next == nil || !strings.HasPrefix(ui.View().WindowTitle, "⠙ ") {
		t.Fatalf("next spinner title/command = %q / %v", ui.View().WindowTitle, next != nil)
	}
}

func TestTUIWindowTitleCompletionClearsOnFocusOrInput(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.setTaskTitle("梳理当前项目")
	ui.taskCompleted = true
	if got, want := ui.View().WindowTitle, "✔ 已完成 · 梳理当前项目 | luneAgent"; got != want {
		t.Fatalf("completed title = %q, want %q", got, want)
	}
	ui.Update(tea.FocusMsg{})
	if got, want := ui.View().WindowTitle, "梳理当前项目 | luneAgent"; got != want {
		t.Fatalf("focused title = %q, want %q", got, want)
	}
	ui.taskCompleted = true
	ui.Update(tea.KeyPressMsg(tea.Key{Code: 'h'}))
	if ui.taskCompleted {
		t.Fatal("typing did not clear the completed title")
	}
}

func TestTUIViewAlwaysCarriesTerminalProgressState(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	view := ui.View()
	if view.ProgressBar == nil || view.ProgressBar.State != tea.ProgressBarNone || !view.ReportFocus {
		t.Fatalf("idle view progress/focus = %#v/%v", view.ProgressBar, view.ReportFocus)
	}

	ui.running = true
	view = ui.View()
	if view.ProgressBar == nil || view.ProgressBar.State != tea.ProgressBarIndeterminate {
		t.Fatalf("running view progress = %#v", view.ProgressBar)
	}

	ui.pendingRequest = &core.PendingRequest{Kind: core.PendingUserQuestion}
	view = ui.View()
	if view.ProgressBar == nil || view.ProgressBar.State != tea.ProgressBarWarning || view.ProgressBar.Value != 100 {
		t.Fatalf("waiting view progress = %#v", view.ProgressBar)
	}
}

func TestTUIWaitingTransitionRingsOnce(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	var output bytes.Buffer
	ui.terminalOutput = &synchronizedWriter{w: &output}
	ui.running = true
	request := core.ApprovalRequest{ID: "approval-1", Tool: "command", Arguments: json.RawMessage(`{"command":"git push"}`)}
	_, command := ui.Update(toolApprovalRequestMsg{request: request})
	if command == nil {
		t.Fatal("running to waiting transition did not schedule a terminal bell")
	}
	command()
	if output.String() != "\a" {
		t.Fatalf("terminal bell output = %q, want one BEL", output.String())
	}

	_, command = ui.Update(toolApprovalRequestMsg{request: request})
	if command != nil {
		command()
	}
	if output.String() != "\a" {
		t.Fatalf("repeated waiting event rang again: %q", output.String())
	}

	initiallyWaiting := newTUIModel(&fakeResponder{}, false)
	initiallyWaiting.terminalOutput = &synchronizedWriter{w: &bytes.Buffer{}}
	initiallyWaiting.pendingRequest = &core.PendingRequest{Kind: core.PendingUserQuestion}
	_, command = initiallyWaiting.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if command != nil {
		t.Fatal("initial waiting state should not ring")
	}
}

func TestTUIWindowTitleStripsTerminalControlsAndTruncatesPrompt(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.setTaskTitle("first line\nignored second line")
	if ui.taskTitle != "first line" {
		t.Fatalf("multi-line task summary = %q, want first line only", ui.taskTitle)
	}
	ui.setTaskTitle("safe\x1b]2;injected\a " + strings.Repeat("long", 20))
	got := ui.View().WindowTitle
	if strings.ContainsAny(got, "\x1b\a\n\r") {
		t.Fatalf("terminal control character leaked into window title: %q", got)
	}
	if width := lipgloss.Width(ui.taskTitle); width > 32 {
		t.Fatalf("task title was not truncated: %q (%d columns)", ui.taskTitle, width)
	}
}

func TestTUIWindowTitleIgnoresStaleSessionRefresh(t *testing.T) {
	ui := newTUIModel(&fakeResponder{}, false)
	ui.sessionID = "session-1"
	ui.windowTitle = "Current title"
	ui.windowTitleRevision = 2
	ui.Update(sessionWindowTitleMsg{sessionID: "session-1", revision: 1, title: "Stale title"})
	if got, want := ui.View().WindowTitle, "Current title | luneAgent"; got != want {
		t.Fatalf("stale title refresh changed current title to %q", got)
	}

	ui.Update(sessionWindowTitleMsg{sessionID: "session-1", revision: 2, title: "Updated title"})
	if got, want := ui.View().WindowTitle, "Updated title | luneAgent"; got != want {
		t.Fatalf("current title refresh = %q, want %q", got, want)
	}
}
