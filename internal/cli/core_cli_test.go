package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	core "github.com/afterlune/luneCore"
)

type fakeCLICore struct {
	inputs     []core.TurnRequest
	firstErr   error
	responses  int
	pending    bool
	resumes    int
	discarded  int
	compact    int
	mode       core.PermissionMode
	latest         core.Session
	initialTitle   string
	initialHistory json.RawMessage
	sessions       []core.Session
	sequence       int
	resolved   []bool
	resolveErr error
	runtime    *fakeCLIRuntime
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
	title := f.initialTitle
	if title == "" {
		title = fmt.Sprintf("Session session-%d", f.sequence)
	}
	hist := history
	if (len(hist) == 0 || string(hist) == "null") && len(f.initialHistory) > 0 {
		hist = f.initialHistory
	}
	f.latest = core.Session{ID: fmt.Sprintf("session-%d", f.sequence), Title: title, History: append(json.RawMessage(nil), hist...)}
	return f.latest, nil
}

func (f *fakeCLICore) Compact(context.Context, string) (int, error) { return f.compact, nil }

func (f *fakeCLICore) Workspace() string { return `C:\workspace` }

func (f *fakeCLICore) OpenSessionRuntime(_ context.Context, sessionID string) (cliRuntime, error) {
	if f.runtime == nil || f.runtime.sessionID != sessionID {
		f.runtime = newFakeCLIRuntime(f, sessionID, nil)
	}
	return f.runtime, nil
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

func (f *fakeCLICore) GetSession(_ context.Context, id string) (core.Session, error) {
	if f.latest.ID == id {
		return f.latest, nil
	}
	if id == "non-existent" {
		return core.Session{}, errors.New("not found")
	}
	return core.Session{ID: id, Title: "Session " + id}, nil
}

func (f *fakeCLICore) ListSessions(_ context.Context, limit int, reverse bool) ([]core.Session, error) {
	if len(f.sessions) > 0 {
		return f.sessions, nil
	}
	if f.latest.ID != "" {
		return []core.Session{f.latest}, nil
	}
	return []core.Session{{ID: "sess-1", Title: "First Session"}}, nil
}

func (f *fakeCLICore) ForkSession(_ context.Context, sessionID string) (core.Session, error) {
	f.sequence++
	f.latest = core.Session{ID: fmt.Sprintf("session-%d", f.sequence), Title: "Fork of " + sessionID}
	return f.latest, nil
}

func (f *fakeCLICore) RewindSession(_ context.Context, sessionID string, turns int) (int, error) {
	return turns, nil
}

func (f *fakeCLICore) SetSessionTitle(_ context.Context, sessionID, title string) error {
	f.latest.Title = title
	return nil
}

func (f *fakeCLICore) ListModelProfiles() []core.ModelOption {
	return []core.ModelOption{
		{Profile: "primary", Provider: "mock", Model: "mock-model"},
		{Profile: "fast", Provider: "mock", Model: "mock-fast"},
	}
}

func (f *fakeCLICore) ModelForSession(_ context.Context, sessionID string) string {
	return "mock-model"
}

func (f *fakeCLICore) SetSessionSettings(_ context.Context, sessionID string, settings core.SessionSettings) error {
	return nil
}

func (f *fakeCLICore) MCPServers() []core.MCPServerStatus {
	return []core.MCPServerStatus{
		{Name: "test-mcp", Transport: "stdio", Command: "test-cmd", Enabled: true, Connected: true, ToolCount: 3},
	}
}

func (f *fakeCLICore) EnableMCPServer(name string) error {
	return nil
}

func (f *fakeCLICore) DisableMCPServer(name string) error {
	return nil
}

func (f *fakeCLICore) AuthenticateMCPServer(_ context.Context, name string) error {
	return nil
}

func cliResponse(text string) json.RawMessage {
	encoded, _ := json.Marshal(map[string]any{"output": []any{map[string]any{
		"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}},
	}}})
	return encoded
}

func (f *approvalCLICore) OpenSessionRuntime(_ context.Context, sessionID string) (cliRuntime, error) {
	return newFakeCLIRuntime(f.fakeCLICore, sessionID, f), nil
}

type fakeCLIRuntime struct {
	client    *fakeCLICore
	sessionID string
	approval  *approvalCLICore
	mu        sync.Mutex
	sequence  int
	active    string
	status    core.TaskStatus
	cancels   map[string]context.CancelFunc
	turns     map[string]fakeCLITurn
	events    chan core.Event
	runner    func(context.Context, core.TurnRequest) (json.RawMessage, error)
}

type fakeCLITurn struct {
	done   chan struct{}
	result core.TurnResult
	err    error
}

func newFakeCLIRuntime(client *fakeCLICore, sessionID string, approval *approvalCLICore) *fakeCLIRuntime {
	return &fakeCLIRuntime{client: client, sessionID: sessionID, approval: approval,
		cancels: make(map[string]context.CancelFunc), turns: make(map[string]fakeCLITurn)}
}

func (r *fakeCLIRuntime) SessionID() string { return r.sessionID }

func (r *fakeCLIRuntime) SubmitTurn(_ context.Context, request core.TurnRequest, _ core.SubmissionMode) (core.TurnSubmission, error) {
	r.client.inputs = append(r.client.inputs, request)
	r.client.responses++
	r.mu.Lock()
	r.sequence++
	executionID := fmt.Sprintf("execution-%d", r.sequence)
	ctx, cancel := context.WithCancel(context.Background())
	turn := fakeCLITurn{done: make(chan struct{})}
	r.turns[executionID] = turn
	r.cancels[executionID] = cancel
	r.active, r.status = executionID, core.TaskRunning
	r.mu.Unlock()
	go func() {
		var response json.RawMessage
		var err error
		paused := false
		switch {
		case r.approval != nil && request.NoInteractiveDecisions:
			response = cliResponse("approved=false")
			r.client.resolved = append(r.client.resolved, false)
			paused = true
			if r.events != nil {
				r.events <- core.Event{Kind: core.EventExecutionBlocked, ExecutionID: executionID, BlockerTool: r.approval.request.Tool}
			}
		case r.approval != nil:
			if r.events != nil {
				r.events <- core.Event{Kind: core.EventApprovalRequired, ExecutionID: executionID, Approval: &r.approval.request}
			}
			close(r.approval.emitted)
			select {
			case allowed := <-r.approval.results:
				response = cliResponse(fmt.Sprintf("approved=%t", allowed))
			case <-ctx.Done():
				err = ctx.Err()
			}
		case r.runner != nil:
			response, err = r.runner(ctx, request)
		case r.client.responses == 1 && r.client.firstErr != nil:
			err = r.client.firstErr
		default:
			response = cliResponse("reply")
		}
		r.mu.Lock()
		turn := r.turns[executionID]
		status := core.TaskCompleted
		if paused {
			status = core.TaskPaused
		} else if err != nil {
			status = core.TaskFailed
		}
		turn.result, turn.err = core.TurnResult{ExecutionID: executionID, Status: status, Response: response}, err
		r.turns[executionID] = turn
		r.active, r.status = "", status
		close(turn.done)
		r.mu.Unlock()
		cancel()
	}()
	return core.TurnSubmission{Status: core.SubmissionAccepted, ExecutionID: executionID}, nil
}

func (r *fakeCLIRuntime) WaitTurn(ctx context.Context, id string) (core.TurnResult, error) {
	r.mu.Lock()
	turn, ok := r.turns[id]
	r.mu.Unlock()
	if !ok {
		return core.TurnResult{}, core.ErrTurnNotFound
	}
	select {
	case <-turn.done:
		r.mu.Lock()
		turn = r.turns[id]
		r.mu.Unlock()
		return turn.result, turn.err
	case <-ctx.Done():
		return core.TurnResult{}, ctx.Err()
	}
}

func (r *fakeCLIRuntime) SubscribeEvents(ctx context.Context, _ int64) (<-chan core.Event, error) {
	events := make(chan core.Event, 4)
	r.events = events
	go func() { <-ctx.Done(); close(events) }()
	return events, nil
}

func (r *fakeCLIRuntime) State(context.Context) (core.RuntimeState, error) {
	r.mu.Lock()
	status, active := r.status, r.active
	r.mu.Unlock()
	if r.client.pending {
		status = core.TaskPaused
	}
	return core.RuntimeState{Task: core.TaskState{SessionID: r.sessionID, Status: status, ExecutionID: active}}, nil
}

func (r *fakeCLIRuntime) Interrupt(id string) bool {
	r.mu.Lock()
	cancel := r.cancels[id]
	r.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (r *fakeCLIRuntime) Recover(context.Context, ...core.ExecutionOptions) (core.TurnSubmission, error) {
	r.client.resumes++
	r.client.pending = false
	r.mu.Lock()
	r.sequence++
	id := fmt.Sprintf("execution-%d", r.sequence)
	turn := fakeCLITurn{done: make(chan struct{}), result: core.TurnResult{ExecutionID: id, Status: core.TaskCompleted, Response: cliResponse("resumed")}}
	r.turns[id] = turn
	r.status, r.active = core.TaskCompleted, ""
	close(turn.done)
	r.mu.Unlock()
	return core.TurnSubmission{Status: core.SubmissionAccepted, ExecutionID: id}, nil
}

func (r *fakeCLIRuntime) DiscardPaused(context.Context) (bool, error) {
	if !r.client.pending {
		return false, nil
	}
	r.client.pending = false
	r.client.discarded++
	return true, nil
}

func (*fakeCLIRuntime) SetUserInteractionAvailable(bool) {}
func (*fakeCLIRuntime) PendingRequests(context.Context) ([]core.PendingRequest, error) {
	return nil, nil
}
func (r *fakeCLIRuntime) ResolveApproval(id string, allowed bool) error {
	if r.client.resolveErr != nil {
		return r.client.resolveErr
	}
	if r.approval != nil && id != r.approval.request.ID {
		return fmt.Errorf("unexpected approval id %q", id)
	}
	r.client.resolved = append(r.client.resolved, allowed)
	if r.approval != nil {
		r.approval.results <- allowed
	}
	return nil
}

func (f *blockingCLICore) OpenSessionRuntime(_ context.Context, sessionID string) (cliRuntime, error) {
	runtime := newFakeCLIRuntime(f.fakeCLICore, sessionID, nil)
	runtime.runner = func(ctx context.Context, _ core.TurnRequest) (json.RawMessage, error) {
		close(f.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return runtime, nil
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
	handleCoreCLIPermissions(client, false, "/permissions ask", &stdout, &stderr)
	if client.PermissionMode() != core.PermissionAsk {
		t.Fatalf("permission mode = %q, want ask", client.PermissionMode())
	}
	if !strings.Contains(stdout.String(), "ask") || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q, want Ask mode confirmation", stdout.String(), stderr.String())
	}

	stdout.Reset()
	handleCoreCLIPermissions(client, false, "/permissions", &stdout, &stderr)
	for _, mode := range []string{"read-only", "workspace", "ask", "full-access"} {
		if !strings.Contains(stdout.String(), mode) {
			t.Errorf("permission list %q does not contain %q", stdout.String(), mode)
		}
	}

	stderr.Reset()
	handleCoreCLIPermissions(client, true, "/permissions full-access", &stdout, &stderr)
	if !strings.Contains(stderr.String(), "Cannot change permissions while Plan mode is active") {
		t.Fatalf("stderr=%q, want Plan mode lock error", stderr.String())
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
	if !strings.Contains(stderr, "Press Esc to interrupt") || strings.Contains(stderr, "context canceled") {
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
				"operating system sandbox",
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
			for _, expected := range []string{"Task paused for execution_blocked"} {
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
	if !strings.Contains(stdout, "approved=false") || !strings.Contains(stderr, "keyboard input became unavailable") || !strings.Contains(stderr, "Denied Writing notes.txt") {
		t.Fatalf("stdout=%q stderr=%q, want a denied call and clear input failure", stdout, stderr)
	}
}

func TestCLIApprovalDoesNotApproveExpiredRequest(t *testing.T) {
	client := &fakeCLICore{resolveErr: errors.New("request is no longer pending")}
	runtime, err := client.OpenSessionRuntime(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	keys := make(chan cliInputEvent, 1)
	keys <- cliInputEvent{rune: 'y'}
	close(keys)
	resolveCLIApproval(runtime, core.ApprovalRequest{ID: "expired", Tool: "command"}, "", keys, &stderr)
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
	stdout, _ := runCLI(t, client, "/resume\n/exit\n", nil)
	if client.resumes != 1 || client.pending {
		t.Fatalf("resume calls=%d pending=%v, want one resume and no pending task", client.resumes, client.pending)
	}
	if !strings.Contains(stdout, "resumed") {
		t.Fatalf("stdout = %q, want resumed response", stdout)
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

func TestCLICleanLaunchCreatesNewSession(t *testing.T) {
	client := &fakeCLICore{
		latest: core.Session{ID: "old-session-123", Title: "Old Session"},
	}
	stdout, stderr := runCLI(t, client, "/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	_ = stdout
	// sequence must be 1 because CreateSession was called on startup instead of reusing old-session-123
	if client.sequence != 1 {
		t.Fatalf("client.sequence = %d, want 1 (new session created on launch)", client.sequence)
	}
	if client.latest.ID == "old-session-123" {
		t.Fatalf("latest session ID = %q, want new session created", client.latest.ID)
	}
}

func TestCLIStatusAndModel(t *testing.T) {
	client := &fakeCLICore{}
	stdout, stderr := runCLI(t, client, "/status\n/model\n/model fast\n/status\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Session ID:") || !strings.Contains(stdout, "Workspace:") {
		t.Fatalf("stdout = %q, want status details", stdout)
	}
	if !strings.Contains(stdout, "Available model profiles:") || !strings.Contains(stdout, "mock-model") {
		t.Fatalf("stdout = %q, want model profiles list", stdout)
	}
	if !strings.Contains(stdout, "Active model set to") {
		t.Fatalf("stdout = %q, want model switched message", stdout)
	}
}

func TestCLIPlanMode(t *testing.T) {
	client := &fakeCLICore{}
	stdout, stderr := runCLI(t, client, "/plan\n/permissions workspace\n/plan\n/exit\n", nil)
	if !strings.Contains(stdout, "Switched to Plan mode (permissions locked to read-only).") {
		t.Fatalf("stdout = %q, want Plan mode enter message", stdout)
	}
	if !strings.Contains(stderr, "Cannot change permissions while Plan mode is active.") {
		t.Fatalf("stderr = %q, want Plan mode lock error", stderr)
	}
	if !strings.Contains(stdout, "Exited Plan mode (permissions restored).") {
		t.Fatalf("stdout = %q, want Plan mode exit message", stdout)
	}
}

func TestCLISessionsAndOpen(t *testing.T) {
	client := &fakeCLICore{}
	stdout, stderr := runCLI(t, client, "/sessions\n/open session-1\n/open non-existent\n/exit\n", nil)
	if !strings.Contains(stdout, "Recent sessions:") {
		t.Fatalf("stdout = %q, want recent sessions list", stdout)
	}
	if !strings.Contains(stdout, "Already in session session-1.") {
		t.Fatalf("stdout = %q, want already in session", stdout)
	}
	if !strings.Contains(stderr, "No session found matching prefix") {
		t.Fatalf("stderr = %q, want no session found", stderr)
	}
}

func TestCLITitleAndRewind(t *testing.T) {
	client := &fakeCLICore{}
	stdout, stderr := runCLI(t, client, "/title My Work\n/title\n/rewind 2\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, `Session title set to "My Work".`) {
		t.Fatalf("stdout = %q, want title set confirmation", stdout)
	}
	if !strings.Contains(stdout, "Current session title: My Work") {
		t.Fatalf("stdout = %q, want title inspection", stdout)
	}
	if !strings.Contains(stdout, "Rewound 2 turn(s).") {
		t.Fatalf("stdout = %q, want rewind confirmation", stdout)
	}
}

func TestCLIMCPCommands(t *testing.T) {
	client := &fakeCLICore{}
	stdout, stderr := runCLI(t, client, "/mcp\n/mcp enable test-mcp\n/mcp disable test-mcp\n/mcp auth test-mcp\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "MCP Servers:") || !strings.Contains(stdout, "test-mcp") {
		t.Fatalf("stdout = %q, want mcp list", stdout)
	}
	if !strings.Contains(stdout, `MCP server "test-mcp" enabled.`) {
		t.Fatalf("stdout = %q, want mcp enable confirmation", stdout)
	}
	if !strings.Contains(stdout, `MCP server "test-mcp" disabled.`) {
		t.Fatalf("stdout = %q, want mcp disable confirmation", stdout)
	}
	if !strings.Contains(stdout, `MCP server "test-mcp" authentication completed.`) {
		t.Fatalf("stdout = %q, want mcp auth confirmation", stdout)
	}
}

func TestCLIClearAndFork(t *testing.T) {
	client := &fakeCLICore{}
	stdout, stderr := runCLI(t, client, "/clear\n/fork\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "\033[H\033[2J") {
		t.Fatalf("stdout does not contain clear escape sequence")
	}
	if !strings.Contains(stdout, "Forked to new session: session-2") {
		t.Fatalf("stdout = %q, want forked session confirmation", stdout)
	}
}

func TestCLISessionsFilter(t *testing.T) {
	client := &fakeCLICore{
		sessions: []core.Session{
			{ID: "sess-abc-1", Title: "Fix parser bug"},
			{ID: "sess-def-2", Title: "Add unit tests"},
			{ID: "sess-ghi-3", Title: "Fix lexer bug"},
		},
	}

	// 1. Filter with "bug"
	stdout, stderr := runCLI(t, client, "/sessions bug\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Matching sessions (2 found for \"bug\"):") {
		t.Fatalf("stdout = %q, want 2 matching sessions", stdout)
	}
	if !strings.Contains(stdout, "Fix parser bug") || !strings.Contains(stdout, "Fix lexer bug") {
		t.Fatalf("stdout = %q, want bug sessions", stdout)
	}
	if strings.Contains(stdout, "Add unit tests") {
		t.Fatalf("stdout = %q, did not want unit tests session", stdout)
	}

	// 2. Filter with no match
	stdout2, _ := runCLI(t, client, "/sessions non-existent\n/exit\n", nil)
	if !strings.Contains(stdout2, `No sessions matching "non-existent".`) {
		t.Fatalf("stdout2 = %q, want no sessions matching", stdout2)
	}
}

func TestCLIExport(t *testing.T) {
	tmpDir := t.TempDir()
	exportFile := filepath.Join(tmpDir, "exported.md")

	client := &fakeCLICore{
		initialTitle: "CLI Export Test",
		initialHistory: json.RawMessage(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello cli"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello user"}]}
		]`),
	}

	stdout, stderr := runCLI(t, client, fmt.Sprintf("/export %s\n/exit\n", exportFile), nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Exported session transcript to") {
		t.Fatalf("stdout = %q, want export confirmation", stdout)
	}

	content, err := os.ReadFile(exportFile)
	if err != nil {
		t.Fatalf("failed to read exported file: %v", err)
	}
	contentStr := string(content)
	if !strings.Contains(contentStr, "# Session Transcript: CLI Export Test") {
		t.Errorf("expected title in exported file, got: %s", contentStr)
	}
	if !strings.Contains(contentStr, "hello cli") || !strings.Contains(contentStr, "hello user") {
		t.Errorf("expected conversation in exported file, got: %s", contentStr)
	}
}

func TestCLIContext(t *testing.T) {
	client := &fakeCLICore{
		latest: core.Session{
			ID:    "sess-ctx-1",
			Title: "Context CLI Test",
		},
	}

	stdout, stderr := runCLI(t, client, "/context\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	for _, want := range []string{"Context Status & Diagnostics", "Workspace & Permissions:", "Session & Model:", "Capabilities & Tools:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, missing %q", stdout, want)
		}
	}
}
