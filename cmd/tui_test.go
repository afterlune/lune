package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/afterlune/lune/internal/tool"
)

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
	if !strings.Contains(view, "read_file") || !strings.Contains(view, "failed") {
		t.Fatalf("view = %q, want tool name and failure status", view)
	}
	if strings.Contains(view, "internal-call-id") || strings.Contains(view, "arguments") || strings.Contains(view, "output") {
		t.Fatalf("view contains internal or tool payload data: %q", view)
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

func TestUseTUIRequiresBothStreamsToBeTerminals(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer output.Close()
	if useTUI(input, output) {
		t.Fatal("piped streams should use the line interface")
	}
}

func TestTUILayoutFitsWideAndNarrowTerminals(t *testing.T) {
	for _, size := range []struct{ width, height int }{{width: 120, height: 30}, {width: 80, height: 24}} {
		ui := newTUIModel(&fakeResponder{}, nil, false)
		ui.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
		view := ui.View().Content
		if got := lipgloss.Width(view); got > size.width {
			t.Errorf("layout width = %d, terminal width = %d", got, size.width)
		}
		if got := lipgloss.Height(view); got > size.height {
			t.Errorf("layout height = %d, terminal height = %d", got, size.height)
		}
	}
}
