package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/model"
	"github.com/afterlune/lune/internal/tool"
)

type fakeResponder struct {
	inputs    []*model.ResponseInput
	firstErr  error
	responses int
}

type blockingResponder struct {
	started chan struct{}
}

type fakeResumableResponder struct {
	fakeResponder
	pending   bool
	resumes   int
	discarded int
}

func TestCLIPermissionsCommandSupportsAskMode(t *testing.T) {
	registry, err := tool.NewDefaultRegistry(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	handleCLIPermissionsCommand(registry, "/permissions ask", &stdout, &stderr)
	if registry.PermissionMode() != tool.PermissionModeAsk {
		t.Fatalf("permission mode = %q, want ask", registry.PermissionMode())
	}
	if !strings.Contains(stdout.String(), "Ask") || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q, want Ask mode confirmation", stdout.String(), stderr.String())
	}

	stdout.Reset()
	handleCLIPermissionsCommand(registry, "/permissions", &stdout, &stderr)
	for _, mode := range []string{"read-only", "workspace", "ask", "full-access"} {
		if !strings.Contains(stdout.String(), mode) {
			t.Errorf("permission list %q does not contain %q", stdout.String(), mode)
		}
	}
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

func (f *fakeResumableResponder) HasPending() (bool, error) { return f.pending, nil }

func (f *fakeResumableResponder) Resume(context.Context) (*model.Response, error) {
	f.resumes++
	f.pending = false
	return &model.Response{Output: []model.ResponseOutputItem{
		model.OutputMessage{Role: model.RoleAssistant, Content: []model.OutputContentPart{model.OutputText{Text: "resumed"}}},
	}}, nil
}

func (f *fakeResumableResponder) Discard(context.Context) (bool, error) {
	if !f.pending {
		return false, nil
	}
	f.pending = false
	f.discarded++
	return true, nil
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
	previous := slog.Default()
	logs := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

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
	if !strings.Contains(logs.String(), "task interrupted by user") || !strings.Contains(logs.String(), "source=escape") {
		t.Fatalf("logs = %q, want the Esc interruption event", logs.String())
	}
}

func TestChatLoopResumesExplicitlyAndWarnsAboutReplay(t *testing.T) {
	client := &fakeResumableResponder{pending: true}
	var stdout, stderr strings.Builder
	if err := chatLoopWithEscape(client, bufio.NewReader(strings.NewReader("/resume\n/exit\n")), &stdout, &stderr, nil); err != nil {
		t.Fatalf("chatLoopWithEscape: %v", err)
	}
	if client.resumes != 1 || client.pending {
		t.Fatalf("resume calls=%d pending=%v, want one resume and no pending task", client.resumes, client.pending)
	}
	if !strings.Contains(stdout.String(), "resumed") {
		t.Fatalf("stdout = %q, want resumed response", stdout.String())
	}
	if !strings.Contains(stderr.String(), "may repeat a model request or tool call") {
		t.Fatalf("stderr = %q, want replay warning", stderr.String())
	}
}

func TestChatLoopBlocksNewInputUntilPendingTaskIsResumedOrDiscarded(t *testing.T) {
	client := &fakeResumableResponder{pending: true}
	var stdout, stderr strings.Builder
	input := "must not run\n/discard\nnew task\n/exit\n"
	if err := chatLoopWithEscape(client, bufio.NewReader(strings.NewReader(input)), &stdout, &stderr, nil); err != nil {
		t.Fatalf("chatLoopWithEscape: %v", err)
	}
	if client.discarded != 1 || client.pending {
		t.Fatalf("discarded=%d pending=%v, want discarded once", client.discarded, client.pending)
	}
	if len(client.inputs) != 1 || client.inputs[0].Text == nil || *client.inputs[0].Text != "new task" {
		t.Fatalf("responder inputs = %#v, want only new task after discard", client.inputs)
	}
	if !strings.Contains(stderr.String(), "Use /resume or /discard first") {
		t.Fatalf("stderr = %q, want pending-task instruction", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Interrupted task discarded.") {
		t.Fatalf("stdout = %q, want discard confirmation", stdout.String())
	}
}
