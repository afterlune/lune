package tui

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/luneCore"
)

func TestTUIStreamsLeadTextBeforeTaskFinishesAndDoesNotDuplicateFinal(t *testing.T) {
	ui := newStreamingTUI()
	streamModelText(ui, "task-1", "lead", "response-1", "Hello, ")
	streamModelText(ui, "task-1", "lead", "response-1", "world!")
	if strings.Contains(plainTUIView(ui), "Hello, world!") {
		t.Fatal("stream text became visible before its batched refresh")
	}
	ui.Update(modelStreamFlushMsg{token: ui.modelStreamToken})
	if !strings.Contains(plainTUIView(ui), "Hello, world!") {
		t.Fatal("lead text deltas were not rendered while the task was running")
	}
	if got := countAssistantEntries(ui.entries); got != 1 {
		t.Fatalf("assistant stream entries = %d, want one live response", got)
	}

	streamModelBoundary(ui, "task-1", "completed")
	ui.finishTask(taskResult{response: json.RawMessage(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello, world!"}]}]}`)})
	if got := countAssistantEntries(ui.entries); got != 1 {
		t.Fatalf("assistant entries after final response = %d, want streamed response deduplicated", got)
	}
	if ui.entries[len(ui.entries)-1].text != "Hello, world!" {
		t.Fatalf("final assistant text = %q", ui.entries[len(ui.entries)-1].text)
	}
}

func TestTUIRecordsFirstTokenLatencyWhenTextBecomesVisible(t *testing.T) {
	ui := newStreamingTUI()
	ui.modelStreamStartedAt = time.Now().Add(-275 * time.Millisecond)
	streamModelText(ui, "task-1", "lead", "response-1", "first visible text")
	if ui.modelStreamFirstTokenRecorded {
		t.Fatal("first-token latency was recorded before the text flush")
	}
	ui.Update(modelStreamFlushMsg{token: ui.modelStreamToken})
	if !ui.modelStreamFirstTokenRecorded || ui.modelStreamFirstTokenLatency < 250*time.Millisecond {
		t.Fatalf("first-token latency = %s, recorded=%v", ui.modelStreamFirstTokenLatency, ui.modelStreamFirstTokenRecorded)
	}
	firstLatency := ui.modelStreamFirstTokenLatency
	if details := ui.activityDetailsContent(); !strings.Contains(details, "First token · ") {
		t.Fatalf("Activity/Usage details missing first-token latency: %q", details)
	}
	if footer := ui.renderFooter(120); !strings.Contains(footer, "first token ") {
		t.Fatalf("running footer missing first-token latency: %q", footer)
	}
	streamModelText(ui, "task-1", "lead", "response-1", "more text")
	ui.Update(modelStreamFlushMsg{token: ui.modelStreamToken})
	if ui.modelStreamFirstTokenLatency != firstLatency {
		t.Fatalf("first-token latency changed after subsequent text: first=%s later=%s", firstLatency, ui.modelStreamFirstTokenLatency)
	}
}

func TestTUIStreamKeepsPartialMarkdownAndNormalizesSplitLineEndings(t *testing.T) {
	ui := newStreamingTUI()
	for _, delta := range []string{
		"Status: **working",
		"\r",
		"\n```go\r\n",
		"fmt.Println(\"ok\")",
		"\r",
		"\n```\r\nTail ends here\nInline `fmt.Pri",
	} {
		streamModelText(ui, "task-1", "lead", "response-1", delta)
		ui.Update(modelStreamFlushMsg{token: ui.modelStreamToken})
	}
	view := plainTUIView(ui)
	for _, expected := range []string{"Status: working", "fmt.Println(\"ok\")", "Tail ends here", "Inline fmt.Pri"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("stream view lost partial Markdown content %q: %q", expected, view)
		}
	}
	if got := plainConversationText("one\r\ntwo\rthree"); got != "one\ntwo\nthree" {
		t.Fatalf("normalized conversation text = %q", got)
	}
}

func TestTUIStreamSeparatesModelResponsesAndReconcilesOnlyTheLast(t *testing.T) {
	ui := newStreamingTUI()
	streamModelText(ui, "task-1", "lead", "response-1", "Checking configuration.")
	streamModelBoundary(ui, "task-1", "completed")
	streamModelText(ui, "task-1", "lead", "response-2", "Configuration is valid.")
	streamModelBoundary(ui, "task-1", "completed")
	ui.finishTask(taskResult{response: json.RawMessage(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Configuration is valid."}]}]}`)})
	if got := countAssistantEntries(ui.entries); got != 2 {
		t.Fatalf("assistant entries = %d, want one entry per model response", got)
	}
	if ui.entries[0].text != "Checking configuration." || ui.entries[1].text != "Configuration is valid." {
		t.Fatalf("streamed responses = %#v", ui.entries)
	}
}

func TestTUIStreamReconciliationPreservesQueuedEntries(t *testing.T) {
	ui := newStreamingTUI()
	streamModelText(ui, "task-1", "lead", "response-part-1", "The final ")
	ui.Update(modelStreamFlushMsg{token: ui.modelStreamToken})
	ui.addEntry("queued", "A follow-up task", false)
	streamModelText(ui, "task-1", "lead", "response-part-2", "response is ready.")
	streamModelBoundary(ui, "task-1", "completed")
	ui.finishTask(taskResult{response: json.RawMessage(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The final response is ready."}]}]}`)})
	if got := countAssistantEntries(ui.entries); got != 1 {
		t.Fatalf("assistant entries = %d, want one reconciled response", got)
	}
	if ui.entries[0].text != "The final response is ready." || ui.entries[1].role != "queued" {
		t.Fatalf("entries after reconciliation = %#v", ui.entries)
	}
}

func TestTUIInterruptedStreamIsMarkedPartial(t *testing.T) {
	ui := newStreamingTUI()
	streamModelText(ui, "task-1", "lead", "response-1", "The answer is still being generated")
	ui.Update(modelStreamFlushMsg{token: ui.modelStreamToken})
	ui.client.(*fakeResponder).pending = true
	ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventTaskFinished, SessionID: ui.sessionID, TaskID: "task-1", Status: core.TaskPaused,
	}})
	partialText := ui.entries[0].text
	streamModelText(ui, "task-1", "lead", "response-1", " late event")
	if ui.entries[0].text != partialText {
		t.Fatal("late stream event changed a task that had already finished")
	}
	ui.cancelled = true
	ui.finishTask(taskResult{err: context.Canceled})
	if len(ui.entries) == 0 || !ui.entries[0].incomplete {
		t.Fatalf("interrupted assistant entry = %#v, want partial marker", ui.entries)
	}
	if !strings.Contains(plainTUIView(ui), "Partial") || !strings.Contains(ui.status, "Task paused") {
		t.Fatalf("interrupted stream view/status = %q / %q", plainTUIView(ui), ui.status)
	}
	printed := tuiScreenshotANSI.ReplaceAllString(formatScrollbackEntry(ui.entries[0], 80, themeDark), "")
	if !strings.Contains(printed, "Partial · The answer is still being generated") {
		t.Fatalf("interrupted scrollback entry = %q, want Partial marker", printed)
	}
}

func TestTUIResumeKeepsInterruptedTextSeparateFromNewStream(t *testing.T) {
	ui := newStreamingTUI()
	streamModelText(ui, "task-1", "lead", "response-1", "The previous attempt stopped halfway")
	ui.Update(modelStreamFlushMsg{token: ui.modelStreamToken})
	ui.client.(*fakeResponder).pending = true
	ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventTaskFinished, SessionID: ui.sessionID, TaskID: "task-1", Status: core.TaskPaused,
	}})
	ui.cancelled = true
	ui.finishTask(taskResult{err: context.Canceled})
	if len(ui.entries) == 0 || !ui.entries[0].incomplete {
		t.Fatalf("interrupted entry = %#v, want to remain visibly partial", ui.entries)
	}

	ui.client.(*fakeResponder).pending = false
	ui.running = true
	ui.status = "Working… Press Esc to interrupt."
	ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventTaskStarted, SessionID: ui.sessionID, TaskID: "task-2", Status: core.TaskRunning,
	}})
	streamModelText(ui, "task-2", "lead", "response-2", "The complete resumed answer.")
	ui.Update(modelStreamFlushMsg{token: ui.modelStreamToken})
	streamModelBoundary(ui, "task-2", "completed")
	ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventTaskFinished, SessionID: ui.sessionID, TaskID: "task-2", Status: core.TaskCompleted,
	}})
	ui.finishTask(taskResult{response: json.RawMessage(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"The complete resumed answer."}]}]}`)})

	if got := countAssistantEntries(ui.entries); got != 2 {
		t.Fatalf("assistant entries after resume = %d, want partial plus resumed response: %#v", got, ui.entries)
	}
	if !ui.entries[0].incomplete || ui.entries[1].incomplete || ui.entries[1].text != "The complete resumed answer." {
		t.Fatalf("resumed transcript = %#v", ui.entries)
	}
	view := plainTUIView(ui)
	if !strings.Contains(view, "Partial") || !strings.Contains(view, "The complete resumed answer.") {
		t.Fatalf("view lost partial/resumed distinction: %q", view)
	}
}

func TestTUIStreamIgnoresOtherAgentsSessionsAndTasks(t *testing.T) {
	ui := newStreamingTUI()
	for _, event := range []core.Event{
		streamModelEvent("session-current", "task-1", "worker-1", "response-1", "private worker output"),
		streamModelEvent("another-session", "task-1", "lead", "response-1", "another session"),
		streamModelEvent("session-current", "stale-task", "lead", "response-1", "stale task"),
		streamModelEvent("session-current", "task-1", "lead", "response-1", "reasoning only", "reasoning_delta"),
	} {
		if command := ui.queueModelStreamEvent(event); command != nil {
			t.Fatal("ignored stream event scheduled a redraw")
		}
	}
	if len(ui.modelStreamPending) != 0 || len(ui.entries) != 0 {
		t.Fatalf("ignored stream events changed state: pending=%d entries=%#v", len(ui.modelStreamPending), ui.entries)
	}
}

func newStreamingTUI() *tuiModel {
	client := &fakeResponder{}
	ui := newTUIModel(client, false)
	ui.preferences.Theme = themeDark
	ui.sessionID = "session-current"
	runtime, _ := client.OpenSessionRuntime(context.Background(), ui.sessionID)
	ui.runtime = runtime
	ui.runtimeSessionID = ui.sessionID
	ui.running = true
	ui.status = "Working… Press Esc to interrupt."
	ui.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	ui.Update(coreEventMsg{event: core.Event{
		Kind: core.EventTaskStarted, SessionID: ui.sessionID, TaskID: "task-1", Status: core.TaskRunning,
	}})
	return ui
}

func streamModelText(ui *tuiModel, taskID, agentID, itemID, delta string) {
	ui.Update(coreEventMsg{event: streamModelEvent(ui.sessionID, taskID, agentID, itemID, delta, "text_delta")})
}

func streamModelBoundary(ui *tuiModel, taskID, eventType string) {
	ui.Update(coreEventMsg{event: streamModelEvent(ui.sessionID, taskID, "lead", "", "", eventType)})
	ui.Update(modelStreamFlushMsg{token: ui.modelStreamToken})
}

func streamModelEvent(sessionID, taskID, agentID, itemID, delta string, eventTypes ...string) core.Event {
	eventType := "text_delta"
	if len(eventTypes) > 0 {
		eventType = eventTypes[0]
	}
	return core.Event{
		Kind: core.EventModelStream, SessionID: sessionID, TaskID: taskID, AgentID: agentID,
		Stream: &core.ModelStreamEvent{Type: eventType, ItemID: itemID, Delta: delta},
	}
}

func countAssistantEntries(entries []tuiEntry) int {
	count := 0
	for _, entry := range entries {
		if entry.role == "assistant" {
			count++
		}
	}
	return count
}
