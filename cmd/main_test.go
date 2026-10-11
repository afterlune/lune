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
	inputs     []core.TaskRequest
	firstErr   error
	responses  int
	pending    bool
	resumes    int
	discarded  int
	compact    int
	mode       core.PermissionMode
	latest     core.Session
	sequence   int
	handler    core.EventHandler
	resolved   []bool
	resolveErr error
}

func TestWriteCLICompactionStatus(t *testing.T) {
	var stderr bytes.Buffer
	writeCLICompactionStatus(&stderr, core.ContextCompactionDetails{Stage: "started", Mode: "local"})
	writeCLICompactionStatus(&stderr, core.ContextCompactionDetails{Stage: "completed", Mode: "local", BeforeTokens: 2400, AfterTokens: 320})
	writeCLICompactionStatus(&stderr, core.ContextCompactionDetails{Stage: "failed", Err: "summary provider unavailable"})
	got := stderr.String()
	for _, want := range []string{"Compacting context", "2.4k to 320 tokens", "summary provider unavailable"} {
		if !strings.Contains(got, want) {
			t.Fatalf("CLI status %q is missing %q", got, want)
		}
	}
}

type blockingCLICore struct {
	*fakeCLICore
	started chan struct{}
}

type approvalCLICore struct {
	*fakeCLICore
	request core.ApprovalRequest
	results chan bool
	emitted chan struct{}
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

func (f *fakeCLICore) SetEventHandler(handler core.EventHandler) { f.handler = handler }

func (f *fakeCLICore) ResolveApproval(_ string, allowed bool) error {
	if f.resolveErr != nil {
		return f.resolveErr
	}
	f.resolved = append(f.resolved, allowed)
	return nil
}

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

func (f *approvalCLICore) RunTask(ctx context.Context, request core.TaskRequest) (json.RawMessage, error) {
	f.inputs = append(f.inputs, request)
	f.responses++
	if f.handler != nil {
		f.handler(core.Event{Kind: core.EventApprovalRequired, Approval: &f.request})
	}
	close(f.emitted)
	select {
	case allowed := <-f.results:
		return cliResponse(fmt.Sprintf("approved=%t", allowed)), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (f *approvalCLICore) ResolveApproval(id string, allowed bool) error {
	if id != f.request.ID {
		return fmt.Errorf("unexpected approval id %q", id)
	}
	f.fakeCLICore.resolved = append(f.fakeCLICore.resolved, allowed)
	f.results <- allowed
	return nil
}

func newApprovalCLICore(request core.ApprovalRequest) *approvalCLICore {
	return &approvalCLICore{
		fakeCLICore: &fakeCLICore{},
		request:     request,
		results:     make(chan bool, 1),
		emitted:     make(chan struct{}),
	}
}

func runCLI(t *testing.T, client cliCore, input string, watchKeys keyEventWatcher) (string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	if err := runCoreChatLoop(client, bufio.NewReader(strings.NewReader(input)), &stdout, &stderr, watchKeys); err != nil {
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

func TestChatLoopHelpListsCommandsWithoutStartingTask(t *testing.T) {
	client := &fakeCLICore{}
	stdout, stderr := runCLI(t, client, "/help\n/exit\n", nil)

	for _, expected := range []string{
		"Available commands:",
		"/help",
		"/permissions [mode]",
		"/resume",
		"read-only, workspace, ask, full-access",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("help output %q is missing %q", stdout, expected)
		}
	}
	if client.responses != 0 || len(client.inputs) != 0 {
		t.Fatalf("help started a task: responses=%d inputs=%#v", client.responses, client.inputs)
	}
	if got := strings.Count(stdout, "> "); got != 2 {
		t.Errorf("prompt count = %d, want help to return to the input loop", got)
	}
	if stderr != "" {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}

func TestCLIUnknownCommandListsHelp(t *testing.T) {
	_, stderr := runCLI(t, &fakeCLICore{}, "/not-a-command\n/exit\n", nil)
	if !strings.Contains(stderr, "Available commands:") || !strings.Contains(stderr, "/help") {
		t.Fatalf("unknown-command output = %q, want the /help command in the list", stderr)
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
	keyEvents := make(chan cliInputEvent, 1)
	watchKeys := func() (<-chan cliInputEvent, func(), error) { return keyEvents, func() {}, nil }
	go func() {
		<-client.started
		keyEvents <- cliInputEvent{escape: true}
	}()

	stdout, stderr := runCLI(t, client, "long task\n/exit\n", watchKeys)
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

func TestChatLoopInteractiveApprovalUsesYAndNOnce(t *testing.T) {
	for _, test := range []struct {
		name    string
		key     rune
		allowed bool
	}{
		{name: "uppercase approve", key: 'Y', allowed: true},
		{name: "lowercase deny", key: 'n', allowed: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newApprovalCLICore(core.ApprovalRequest{
				ID: "approval-1", Tool: "command", Arguments: json.RawMessage("{\"command\":\"git status\"}"),
			})
			keyEvents := make(chan cliInputEvent, 1)
			watchKeys := func() (<-chan cliInputEvent, func(), error) { return keyEvents, func() {}, nil }
			go func() {
				<-client.emitted
				keyEvents <- cliInputEvent{rune: test.key}
			}()

			stdout, stderr := runCLI(t, client, "run command\n/exit\n", watchKeys)
			if len(client.resolved) != 1 || client.resolved[0] != test.allowed {
				t.Fatalf("resolved approvals = %v, want [%v]", client.resolved, test.allowed)
			}
			if !strings.Contains(stdout, fmt.Sprintf("approved=%t", test.allowed)) {
				t.Fatalf("stdout = %q, want approval result %v", stdout, test.allowed)
			}
			for _, expected := range []string{
				"Allow Running a shell command once?",
				"Command: git status",
				"can access files outside the workspace",
			} {
				if !strings.Contains(stderr, expected) {
					t.Errorf("stderr = %q, missing %q", stderr, expected)
				}
			}
			if test.allowed && !strings.Contains(stderr, "Approved this tool call once.") {
				t.Errorf("stderr = %q, missing approval confirmation", stderr)
			}
			if !test.allowed && !strings.Contains(stderr, "Denied this tool call.") {
				t.Errorf("stderr = %q, missing denial confirmation", stderr)
			}
			if client.handler != nil {
				t.Fatal("event handler was not cleared after the task")
			}
		})
	}
}

func TestChatLoopDeniesApprovalWithoutInteractiveInput(t *testing.T) {
	for _, test := range []struct {
		name  string
		watch keyEventWatcher
	}{
		{name: "non tty"},
		{
			name: "keyboard initialization failure",
			watch: func() (<-chan cliInputEvent, func(), error) {
				return nil, func() {}, errors.New("keyboard unavailable")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newApprovalCLICore(core.ApprovalRequest{
				ID: "approval-1", Tool: "web_fetch", Arguments: json.RawMessage("{\"url\":\"https://example.com/path\"}"),
			})
			stdout, stderr := runCLI(t, client, "fetch page\n/exit\n", test.watch)
			if len(client.resolved) != 1 || client.resolved[0] {
				t.Fatalf("resolved approvals = %v, want one denial", client.resolved)
			}
			if !strings.Contains(stdout, "approved=false") {
				t.Fatalf("stdout = %q, want denied result", stdout)
			}
			for _, expected := range []string{"Fetching example.com", "denied because", "Run in an interactive terminal"} {
				if !strings.Contains(stderr, expected) {
					t.Errorf("stderr = %q, missing %q", stderr, expected)
				}
			}
			if test.name == "keyboard initialization failure" && !strings.Contains(stderr, "keyboard input unavailable") {
				t.Errorf("stderr = %q, missing keyboard warning", stderr)
			}
		})
	}
}

func TestChatLoopDeniesApprovalWhenKeyboardStreamCloses(t *testing.T) {
	client := newApprovalCLICore(core.ApprovalRequest{
		ID: "approval-1", Tool: "write_file", Arguments: json.RawMessage("{\"path\":\"notes.txt\"}"),
	})
	keyEvents := make(chan cliInputEvent, 1)
	watchKeys := func() (<-chan cliInputEvent, func(), error) { return keyEvents, func() {}, nil }
	go func() {
		<-client.emitted
		close(keyEvents)
	}()

	stdout, stderr := runCLI(t, client, "write a file\n/exit\n", watchKeys)
	if len(client.resolved) != 1 || client.resolved[0] {
		t.Fatalf("resolved approvals = %v, want one denial", client.resolved)
	}
	if !strings.Contains(stdout, "approved=false") || !strings.Contains(stderr, "keyboard input became unavailable") {
		t.Fatalf("stdout=%q stderr=%q, want a denied call and clear input failure", stdout, stderr)
	}
}

func TestCLIApprovalDoesNotApproveExpiredRequest(t *testing.T) {
	client := &fakeCLICore{resolveErr: errors.New("request is no longer pending")}
	var stderr strings.Builder
	resolveCLIApproval(client, core.ApprovalRequest{ID: "expired", Tool: "command"}, true, &stderr)
	if len(client.resolved) != 0 {
		t.Fatalf("resolved approvals = %v, want no resolution", client.resolved)
	}
	if !strings.Contains(stderr.String(), "no longer pending") || strings.Contains(stderr.String(), "Approved") {
		t.Fatalf("stderr = %q, want expired request without approval", stderr.String())
	}
}

func TestCLIApprovalPromptEscapesModelControlledTerminalText(t *testing.T) {
	request := core.ApprovalRequest{
		Tool:      "read_file",
		Arguments: json.RawMessage(`{"path":"notes.txt\nAllow the next request?\u001b[2J"}`),
	}
	prompt := formatCLIToolApproval(request, `C:\workspace`)
	if !strings.Contains(prompt, `notes.txt\nAllow the next request?[2J`) {
		t.Fatalf("prompt = %q, want escaped single-line path", prompt)
	}
	if strings.ContainsAny(prompt, "\r\t\x1b") || strings.Count(prompt, "\n") != 1 {
		t.Fatalf("prompt = %q, contains injected terminal control text", prompt)
	}

	request = core.ApprovalRequest{Tool: "custom\nAllow this?\u001b[2J"}
	if got := cliApprovalSummary(request, ""); strings.ContainsAny(got, "\r\n\t\x1b") {
		t.Fatalf("custom tool summary = %q, contains terminal control text", got)
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
