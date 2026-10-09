package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/afterlune/lune/core"
)

type fakeCLICore struct {
	inputs    []core.TaskRequest
	firstErr  error
	responses int
	pending   bool
	resumes   int
	discarded int
	compact   int
	mode      core.PermissionMode
	latest    core.Session
	sequence  int
}

type blockingCLICore struct {
	*fakeCLICore
	started chan struct{}
}

func (f *fakeCLICore) LatestSession(context.Context) (core.Session, bool, error) {
	return f.latest, f.latest.ID != "", nil
}

func (f *fakeCLICore) CreateSession(_ context.Context, history json.RawMessage) (core.Session, error) {
	f.sequence++
	f.latest = core.Session{ID: fmt.Sprintf("session-%d", f.sequence), History: append(json.RawMessage(nil), history...)}
	return f.latest, nil
}

func (f *fakeCLICore) RunTask(_ context.Context, request core.TaskRequest) (json.RawMessage, error) {
	f.inputs = append(f.inputs, request)
	f.responses++
	if f.responses == 1 && f.firstErr != nil {
		return nil, f.firstErr
	}
	return cliResponse("reply"), nil
}

func (f *blockingCLICore) RunTask(ctx context.Context, _ core.TaskRequest) (json.RawMessage, error) {
	close(f.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeCLICore) ResumeTask(context.Context, string) (json.RawMessage, error) {
	f.resumes++
	f.pending = false
	return cliResponse("resumed"), nil
}

func (f *fakeCLICore) HasPending(context.Context, string) (bool, error) { return f.pending, nil }

func (f *fakeCLICore) DiscardTask(context.Context, string) (bool, error) {
	if !f.pending {
		return false, nil
	}
	f.pending = false
	f.discarded++
	return true, nil
}

func (f *fakeCLICore) Compact(context.Context, string) (int, error) { return f.compact, nil }

func (f *fakeCLICore) PermissionMode() core.PermissionMode {
	if f.mode == "" {
		return core.PermissionWorkspace
	}
	return f.mode
}

func (f *fakeCLICore) SetPermissionMode(mode core.PermissionMode) error {
	switch mode {
	case core.PermissionReadOnly, core.PermissionWorkspace, core.PermissionAsk, core.PermissionFullAccess:
		f.mode = mode
		return nil
	default:
		return errors.New("invalid permission mode")
	}
}

func cliResponse(text string) json.RawMessage {
	encoded, _ := json.Marshal(map[string]any{"output": []any{map[string]any{
		"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}},
	}}})
	return encoded
}

func runCLI(t *testing.T, client cliCore, input string, watchEscape escapeWatcher) (string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	if err := runCoreChatLoop(client, bufio.NewReader(strings.NewReader(input)), &stdout, &stderr, watchEscape); err != nil {
		t.Fatalf("runCoreChatLoop: %v", err)
	}
	return stdout.String(), stderr.String()
}

func TestCLIPermissionsCommandSupportsAskMode(t *testing.T) {
	client := &fakeCLICore{}
	var stdout, stderr bytes.Buffer
	handleCoreCLIPermissions(client, "/permissions ask", &stdout, &stderr)
	if client.PermissionMode() != core.PermissionAsk {
		t.Fatalf("permission mode = %q, want ask", client.PermissionMode())
	}
	if !strings.Contains(stdout.String(), "ask") || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q, want Ask mode confirmation", stdout.String(), stderr.String())
	}

	stdout.Reset()
	handleCoreCLIPermissions(client, "/permissions", &stdout, &stderr)
	for _, mode := range []string{"read-only", "workspace", "ask", "full-access"} {
		if !strings.Contains(stdout.String(), mode) {
			t.Errorf("permission list %q does not contain %q", stdout.String(), mode)
		}
	}
}

func TestChatLoopReadsTextAndJSONUntilExit(t *testing.T) {
	client := &fakeCLICore{}
	input := "hello\n" + `[{"type":"message","role":"user","content":[{"type":"input_text","text":"inspect"},{"type":"input_video","video_url":"clip"}]}]` + "\n/exit\nignored\n"
	stdout, stderr := runCLI(t, client, input, nil)
	if len(client.inputs) != 2 {
		t.Fatalf("got %d calls, want 2", len(client.inputs))
	}
	if string(client.inputs[0].Input.Raw) != `"hello"` {
		t.Fatalf("first input = %s, want encoded text hello", client.inputs[0].Input.Raw)
	}
	if !strings.Contains(string(client.inputs[1].Input.Raw), `"type":"input_video"`) {
		t.Fatalf("unknown modality was not preserved: %s", client.inputs[1].Input.Raw)
	}
	if got := strings.Count(stdout, "reply\n"); got != 2 {
		t.Fatalf("printed replies = %d, want 2", got)
	}
	if stderr != "" {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}

func TestChatLoopExitsAtEOFAndHandlesFinalLine(t *testing.T) {
	client := &fakeCLICore{}
	runCLI(t, client, "last input", nil)
	if len(client.inputs) != 1 || string(client.inputs[0].Input.Raw) != `"last input"` {
		t.Fatalf("unexpected inputs: %#v", client.inputs)
	}
}

func TestChatLoopContinuesAfterRequestError(t *testing.T) {
	client := &fakeCLICore{firstErr: errors.New("temporary API error")}
	stdout, stderr := runCLI(t, client, "first\nsecond\n/exit\n", nil)
	if len(client.inputs) != 2 {
		t.Fatalf("calls = %d, want 2", len(client.inputs))
	}
	if !strings.Contains(stderr, "temporary API error") || !strings.Contains(stdout, "reply") {
		t.Fatalf("stderr=%q stdout=%q", stderr, stdout)
	}
}

func TestChatLoopEscapeCancelsActiveTaskAndReturnsToPrompt(t *testing.T) {
	previous := slog.Default()
	logs := &bytes.Buffer{}
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	client := &blockingCLICore{fakeCLICore: &fakeCLICore{}, started: make(chan struct{})}
	escape := make(chan struct{}, 1)
	watchEscape := func() (<-chan struct{}, func(), error) { return escape, func() {}, nil }
	go func() {
		<-client.started
		escape <- struct{}{}
	}()

	stdout, stderr := runCLI(t, client, "long task\n/exit\n", watchEscape)
	if got := strings.Count(stdout, "> "); got != 2 {
		t.Fatalf("prompt count = %d, want 2 after returning to prompt: %q", got, stdout)
	}
	if !strings.Contains(stdout, "Task interrupted.") {
		t.Fatalf("stdout = %q, want interruption status", stdout)
	}
	if !strings.Contains(stderr, "Press Esc to cancel") || strings.Contains(stderr, "context canceled") {
		t.Fatalf("stderr = %q, want Esc status without cancellation error", stderr)
	}
	if !strings.Contains(logs.String(), "task interrupted by user") || !strings.Contains(logs.String(), "source=escape") {
		t.Fatalf("logs = %q, want the Esc interruption event", logs.String())
	}
}

func TestChatLoopResumesExplicitlyAndWarnsAboutReplay(t *testing.T) {
	client := &fakeCLICore{pending: true}
	stdout, stderr := runCLI(t, client, "/resume\n/exit\n", nil)
	if client.resumes != 1 || client.pending {
		t.Fatalf("resume calls=%d pending=%v, want one resume and no pending task", client.resumes, client.pending)
	}
	if !strings.Contains(stdout, "resumed") {
		t.Fatalf("stdout = %q, want resumed response", stdout)
	}
	if !strings.Contains(stderr, "may repeat a model request or tool call") {
		t.Fatalf("stderr = %q, want replay warning", stderr)
	}
}

func TestChatLoopBlocksNewInputUntilPendingTaskIsResumedOrDiscarded(t *testing.T) {
	client := &fakeCLICore{pending: true}
	stdout, stderr := runCLI(t, client, "must not run\n/discard\nnew task\n/exit\n", nil)
	if client.discarded != 1 || client.pending {
		t.Fatalf("discarded=%d pending=%v, want discarded once", client.discarded, client.pending)
	}
	if len(client.inputs) != 1 || string(client.inputs[0].Input.Raw) != `"new task"` {
		t.Fatalf("responder inputs = %#v, want only new task after discard", client.inputs)
	}
	if !strings.Contains(stderr, "Use /resume or /discard first") {
		t.Fatalf("stderr = %q, want pending-task instruction", stderr)
	}
	if !strings.Contains(stdout, "Interrupted task discarded.") {
		t.Fatalf("stdout = %q, want discard confirmation", stdout)
	}
}
