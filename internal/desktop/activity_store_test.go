package desktop

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestActivityStorePersistsExecutionAndToolDetails(t *testing.T) {
	directory := t.TempDir()
	store, err := OpenActivityStore(directory)
	if err != nil {
		t.Fatalf("OpenActivityStore: %v", err)
	}
	messageIndex := 2
	if err := store.UpsertExecution(context.Background(), "session-a", "execution-a", &messageIndex, "running"); err != nil {
		t.Fatalf("UpsertExecution: %v", err)
	}
	started := ToolActivityView{
		ID: "call-a", InvocationID: "invocation-a", Name: "command", Status: "running",
		Arguments: json.RawMessage(`{"command":"git status --short"}`),
	}
	if err := store.UpsertTool(context.Background(), "session-a", "execution-a", started); err != nil {
		t.Fatalf("UpsertTool(started): %v", err)
	}
	finished := started
	finished.Status = "done"
	finished.DurationMs = 245
	finished.Details = &ToolExecutionDetailsView{
		Command: &ActivityCommandOutput{Text: "git status --short", Output: " M internal/desktop/service.go"},
	}
	if err := store.UpsertTool(context.Background(), "session-a", "execution-a", finished); err != nil {
		t.Fatalf("UpsertTool(finished): %v", err)
	}
	if err := store.UpsertExecution(context.Background(), "session-a", "execution-a", nil, "interrupted"); err != nil {
		t.Fatalf("UpsertExecution(interrupted): %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	store, err = OpenActivityStore(directory)
	if err != nil {
		t.Fatalf("reopen activity store: %v", err)
	}
	defer store.Close()

	got, err := store.ListSession(context.Background(), "session-a")
	if err != nil {
		t.Fatalf("ListSession: %v", err)
	}
	if len(got) != 1 || got[0].UserMessageIndex != 2 || got[0].Status != "interrupted" {
		t.Fatalf("execution history = %#v, want one interrupted execution at message index 2", got)
	}
	if len(got[0].Tools) != 1 || got[0].Tools[0].Details == nil || got[0].Tools[0].Details.Command == nil {
		t.Fatalf("tool history = %#v, want persisted command details", got[0].Tools)
	}
	if got[0].Tools[0].Details.Command.Output != " M internal/desktop/service.go" {
		t.Fatalf("command output = %q, want persisted result", got[0].Tools[0].Details.Command.Output)
	}
	otherSession, err := store.ListSession(context.Background(), "session-b")
	if err != nil || len(otherSession) != 0 {
		t.Fatalf("other session history = %#v, error = %v; want empty", otherSession, err)
	}
}

func TestBoundedToolActivityPayloadTruncatesLargeDetails(t *testing.T) {
	activity := ToolActivityView{
		ID: "call-large", InvocationID: "invocation-large", Name: "write_file", Status: "done",
		Arguments: json.RawMessage(`{"content":"` + strings.Repeat("x", 256<<10) + `"}`),
		Details:   &ToolExecutionDetailsView{Output: strings.Repeat("y", 256<<10)},
	}
	payload, err := boundedToolActivityPayload(activity)
	if err != nil {
		t.Fatalf("boundedToolActivityPayload: %v", err)
	}
	if len(payload) > maxStoredToolActivityBytes {
		t.Fatalf("payload is %d bytes, limit is %d", len(payload), maxStoredToolActivityBytes)
	}
	var got ToolActivityView
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode bounded payload: %v", err)
	}
	if !got.Truncated {
		t.Fatal("bounded payload is not marked truncated")
	}
}
