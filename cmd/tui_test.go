package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/afterlune/lune/internal/model"
	"github.com/afterlune/lune/internal/tool"
)

type fakeTUICompactor struct {
	fakeResumableResponder
	compactedTurns int
	compactErr     error
	compactCalls   int
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

func TestTUISlashCommandSuggestionsAndUnknownCommand(t *testing.T) {
	client := &fakeResponder{}
	ui := newTUIModel(client, nil, false)
	ui.input.SetValue("/")
	ui.updateCommandSuggestions()
	if len(ui.commandSuggestions) != 5 {
		t.Fatalf("suggestions = %#v, want all five commands", ui.commandSuggestions)
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
	for _, expected := range []string{"#1 100/40/12/140/25", "#2 50/10/0/60/5", "Task 150/50/12/200/30", "Session 150/50/12/200/30", "20.0%"} {
		if !strings.Contains(view, expected) {
			t.Errorf("view missing %q: %q", expected, view)
		}
	}

	ui.Update(modelUsageMsg{})
	if !strings.Contains(ui.View().Content, "usage unavailable") {
		t.Fatalf("zero usage should be marked unavailable: %q", ui.View().Content)
	}
	if ui.sessionUsage.requests != 3 || ui.sessionUsage.measured != 2 {
		t.Fatalf("session usage = %#v, want three requests and two metered", ui.sessionUsage)
	}

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
		ui.input.SetValue("/")
		ui.updateCommandSuggestions()
		ui.updateLayout()
		view = ui.View().Content
		if got := lipgloss.Height(view); got > size.height {
			t.Errorf("layout with command suggestions height = %d, terminal height = %d", got, size.height)
		}
	}
}
