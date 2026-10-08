package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/model"
)

type fakeResponder struct {
	inputs    []*model.ResponseInput
	firstErr  error
	responses int
}

type blockingResponder struct {
	started chan struct{}
}

func (f *blockingResponder) Respond(ctx context.Context, _ *model.ResponseInput) (*model.Response, error) {
	close(f.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeResponder) Respond(_ context.Context, input *model.ResponseInput) (*model.Response, error) {
	f.inputs = append(f.inputs, input)
	f.responses++
	if f.responses == 1 && f.firstErr != nil {
		return nil, f.firstErr
	}
	return &model.Response{Output: []model.ResponseOutputItem{
		model.OutputMessage{
			Role:    model.RoleAssistant,
			Content: []model.OutputContentPart{model.OutputText{Text: "reply"}},
		},
	}}, nil
}

func TestChatLoopReadsTextAndJSONUntilExit(t *testing.T) {
	client := &fakeResponder{}
	input := "hello\n" + `[{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect"},{"type":"input_video","video_url":"clip"}]}]` + "\n/exit\nignored\n"
	var stdout, stderr strings.Builder
	if err := chatLoop(client, bufio.NewReader(strings.NewReader(input)), &stdout, &stderr); err != nil {
		t.Fatalf("chatLoop: %v", err)
	}
	if len(client.inputs) != 2 {
		t.Fatalf("got %d calls, want 2", len(client.inputs))
	}
	if client.inputs[0].Text == nil || *client.inputs[0].Text != "hello" {
		t.Fatalf("first input = %#v, want text hello", client.inputs[0])
	}
	if len(client.inputs[1].Items) != 1 {
		t.Fatalf("JSON input items = %d, want 1", len(client.inputs[1].Items))
	}
	encoded, err := json.Marshal(client.inputs[1])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"type":"input_video"`) {
		t.Fatalf("unknown modality was not preserved: %s", encoded)
	}
	if got := strings.Count(stdout.String(), "reply\n"); got != 2 {
		t.Fatalf("printed replies = %d, want 2", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestChatLoopExitsAtEOFAndHandlesFinalLine(t *testing.T) {
	client := &fakeResponder{}
	var stdout, stderr strings.Builder
	if err := chatLoop(client, bufio.NewReader(strings.NewReader("last input")), &stdout, &stderr); err != nil {
		t.Fatalf("chatLoop: %v", err)
	}
	if len(client.inputs) != 1 || client.inputs[0].Text == nil || *client.inputs[0].Text != "last input" {
		t.Fatalf("unexpected inputs: %#v", client.inputs)
	}
}

func TestChatLoopContinuesAfterRequestError(t *testing.T) {
	client := &fakeResponder{firstErr: errors.New("temporary API error")}
	var stdout, stderr strings.Builder
	input := "first\nsecond\n/exit\n"
	if err := chatLoop(client, bufio.NewReader(strings.NewReader(input)), &stdout, &stderr); err != nil {
		t.Fatalf("chatLoop: %v", err)
	}
	if len(client.inputs) != 2 {
		t.Fatalf("calls = %d, want 2", len(client.inputs))
	}
	if !strings.Contains(stderr.String(), "temporary API error") || !strings.Contains(stdout.String(), "reply") {
		t.Fatalf("stderr=%q stdout=%q", stderr.String(), stdout.String())
	}
}

func TestChatLoopEscapeCancelsActiveTaskAndReturnsToPrompt(t *testing.T) {
	client := &blockingResponder{started: make(chan struct{})}
	escape := make(chan struct{}, 1)
	watchEscape := func() (<-chan struct{}, func(), error) {
		return escape, func() {}, nil
	}
	go func() {
		<-client.started
		escape <- struct{}{}
	}()

	var stdout, stderr strings.Builder
	input := "long task\n/exit\n"
	if err := chatLoopWithEscape(client, bufio.NewReader(strings.NewReader(input)), &stdout, &stderr, watchEscape); err != nil {
		t.Fatalf("chatLoopWithEscape: %v", err)
	}
	if got := strings.Count(stdout.String(), "> "); got != 2 {
		t.Fatalf("prompt count = %d, want 2 after returning to prompt: %q", got, stdout.String())
	}
	if !strings.Contains(stdout.String(), "Task interrupted.") {
		t.Fatalf("stdout = %q, want interruption status", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Press Esc to cancel") || strings.Contains(stderr.String(), "context canceled") {
		t.Fatalf("stderr = %q, want Esc status without cancellation error", stderr.String())
	}
}
