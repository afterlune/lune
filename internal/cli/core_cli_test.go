package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/afterlune/lune/internal/slashcmd"
	core "github.com/afterlune/luneCore"
	"github.com/afterlune/luneCore/memory"
	publictool "github.com/afterlune/luneCore/tool"
)

type fakeCLICore struct {
	workspace         string
	inputs            []core.TurnRequest
	firstErr          error
	responses         int
	pending           bool
	resumes           int
	discarded         int
	compact           int
	mode              core.PermissionMode
	latest            core.Session
	initialTitle      string
	initialHistory    json.RawMessage
	initialSettings   core.SessionSettings
	initialAgentUsage map[string]core.UsageTotals
	sessions          []core.Session
	sequence          int
	resolved          []bool
	resolveErr        error
	runtime           *fakeCLIRuntime
	memories          []memory.Item
	memSeq            int
	settingsErr       error
	permissionErr     error
	pendingRequests   []core.PendingRequest
	questionAnswers   []core.UserQuestionAnswer
	toolDecisions     []core.ToolRecoveryDecision
	toolchainPaths    []string
}

func TestWriteCLICompactionStatus(t *testing.T) {
	var stderr bytes.Buffer
	writeCLICompactionStatus(&stderr, core.ContextCompactionDetails{Stage: "started", Mode: "local"})
	writeCLICompactionStatus(&stderr, core.ContextCompactionDetails{Stage: "completed", Mode: "local", BeforeTokens: 2400, AfterTokens: 320})
	writeCLICompactionStatus(&stderr, core.ContextCompactionDetails{Stage: "completed", Mode: "server", BeforeTokens: 2400, AfterTokens: 320})
	writeCLICompactionStatus(&stderr, core.ContextCompactionDetails{Stage: "fallback", Mode: "server", Err: "provider rejected context_management"})
	writeCLICompactionStatus(&stderr, core.ContextCompactionDetails{Stage: "fallback", Mode: "local", Err: "local summary remained above target"})
	writeCLICompactionStatus(&stderr, core.ContextCompactionDetails{Stage: "failed", Err: "summary provider unavailable"})
	got := stderr.String()
	for _, want := range []string{
		"Compacting context", "2.4k to 320 tokens", "Server compaction unavailable; using local compaction: provider rejected context_management",
		"Local compaction did not reach its target; keeping the best summary: local summary remained above target",
		"summary provider unavailable",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("CLI status %q is missing %q", got, want)
		}
	}
	if strings.Contains(got, "Provider compacted the context (estimated input") || !strings.Contains(got, "Provider compacted the context (previous input 2.4k tokens)") {
		t.Fatalf("server compaction status reported an unsupported after-token count: %q", got)
	}
}

type failingCLIWriter struct{}

func (failingCLIWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestAwaitCoreExecutionStopsOnStreamAndProgressFailures(t *testing.T) {
	newRuntime := func() *fakeCLIRuntime {
		return &fakeCLIRuntime{turns: map[string]fakeCLITurn{"execution": {done: make(chan struct{})}}}
	}
	t.Run("runtime stream failure", func(t *testing.T) {
		events := make(chan core.Event, 1)
		events <- core.Event{Kind: core.EventRuntimeStreamFailed, Err: "journal unavailable"}
		_, err, interrupted := awaitCoreExecution(newRuntime(), t.TempDir(), "execution", events, nil, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "journal unavailable") || interrupted {
			t.Fatalf("stream failure result = %v, interrupted=%v", err, interrupted)
		}
	})
	t.Run("closed runtime stream", func(t *testing.T) {
		events := make(chan core.Event)
		close(events)
		_, err, interrupted := awaitCoreExecution(newRuntime(), t.TempDir(), "execution", events, nil, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "event stream closed") || interrupted {
			t.Fatalf("closed stream result = %v, interrupted=%v", err, interrupted)
		}
	})
	t.Run("progress write failure", func(t *testing.T) {
		events := make(chan core.Event, 1)
		events <- core.Event{Kind: core.EventModelRetry, Err: "temporary failure"}
		_, err, interrupted := awaitCoreExecution(newRuntime(), t.TempDir(), "execution", events, nil, failingCLIWriter{})
		if err == nil || !strings.Contains(err.Error(), "broken pipe") || interrupted {
			t.Fatalf("write failure result = %v, interrupted=%v", err, interrupted)
		}
	})
	t.Run("initial progress write failure is not an interruption", func(t *testing.T) {
		_, err, interrupted := awaitCoreExecution(newRuntime(), t.TempDir(), "execution", nil, make(chan cliInputEvent), failingCLIWriter{})
		if err == nil || !strings.Contains(err.Error(), "broken pipe") || interrupted {
			t.Fatalf("initial write failure result = %v, interrupted=%v", err, interrupted)
		}
	})
}

func TestFormatCLIManualCompactionResult(t *testing.T) {
	tests := []struct {
		name   string
		result core.CompactionResult
		want   string
		avoid  string
	}{
		{
			name:   "local",
			result: core.CompactionResult{Backend: "local", Turns: 2, Before: 2400, After: 320, Compacted: true},
			want:   "Compacted 2 older user turn(s) locally: 2.4k → 320 tokens.",
		},
		{
			name:   "server",
			result: core.CompactionResult{Backend: "server", Turns: 2, Before: 2400, After: 320, Compacted: true},
			want:   "Compacted 2 older user turn(s) with provider compaction (previous input 2.4k tokens).",
			avoid:  "320 tokens",
		},
		{
			name:   "fallback",
			result: core.CompactionResult{Backend: "local", Turns: 1, Before: 2400, After: 900, Fallback: true, Compacted: true},
			want:   "Fallback was applied.",
		},
		{
			name: "fallback reasons",
			result: core.CompactionResult{
				Backend: "local", Turns: 1, Before: 104000, After: 73249, Fallback: true, Compacted: true,
				FallbackReason: "provider compaction is unsupported; context remains above target",
			},
			want: "Fallback: provider compaction is unsupported; context remains above target.",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := formatCLIManualCompactionResult(test.result)
			if !strings.Contains(got, test.want) {
				t.Fatalf("formatted result = %q, want %q", got, test.want)
			}
			if test.avoid != "" && strings.Contains(got, test.avoid) {
				t.Fatalf("formatted result = %q, must omit %q", got, test.avoid)
			}
		})
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
	f.latest = core.Session{
		ID:         fmt.Sprintf("session-%d", f.sequence),
		Title:      title,
		Workspace:  f.workspace,
		History:    append(json.RawMessage(nil), hist...),
		Settings:   f.initialSettings,
		AgentUsage: f.initialAgentUsage,
	}
	return f.latest, nil
}

func (f *fakeCLICore) Compact(context.Context, string) (core.CompactionResult, error) {
	return core.CompactionResult{Turns: f.compact, Compacted: f.compact > 0}, nil
}

func (f *fakeCLICore) Workspace() string {
	if f.workspace != "" {
		return f.workspace
	}
	return `C:\workspace`
}

func (f *fakeCLICore) OpenSessionRuntime(_ context.Context, sessionID string) (cliRuntime, error) {
	if f.runtime == nil {
		f.runtime = newFakeCLIRuntime(f, sessionID, nil)
	} else {
		f.runtime.sessionID = sessionID
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
	if f.permissionErr != nil {
		return f.permissionErr
	}
	switch mode {
	case core.PermissionReadOnly, core.PermissionWorkspace, core.PermissionAuto, core.PermissionAsk, core.PermissionFullAccess:
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
	if len(f.sessions) > 0 {
		for _, s := range f.sessions {
			if s.ID == id {
				return s, nil
			}
		}
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

func (f *fakeCLICore) DeleteSession(_ context.Context, sessionID string) error {
	var updated []core.Session
	for _, s := range f.sessions {
		if s.ID != sessionID {
			updated = append(updated, s)
		}
	}
	f.sessions = updated
	if f.latest.ID == sessionID {
		f.latest = core.Session{}
	}
	return nil
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

func (f *fakeCLICore) ProjectContextDefaults() core.ContextDefaults {
	return core.ContextDefaults{}
}

func (f *fakeCLICore) SetSessionSettings(_ context.Context, sessionID string, settings core.SessionSettings) error {
	if f.settingsErr != nil {
		return f.settingsErr
	}
	if settings.ModelProfile != "" {
		valid := false
		for _, profile := range f.ListModelProfiles() {
			if profile.Profile == settings.ModelProfile {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("unknown model profile %q", settings.ModelProfile)
		}
	}
	if f.latest.ID == sessionID {
		f.latest.Settings = settings
	}
	for i := range f.sessions {
		if f.sessions[i].ID == sessionID {
			f.sessions[i].Settings = settings
		}
	}
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

func (f *fakeCLICore) ListMemories(_ context.Context, limit int) ([]memory.Item, error) {
	if limit <= 0 || limit > len(f.memories) {
		limit = len(f.memories)
	}
	res := make([]memory.Item, limit)
	copy(res, f.memories[:limit])
	return res, nil
}

func (f *fakeCLICore) SearchMemories(_ context.Context, query string, limit int) ([]memory.SearchResult, error) {
	q := strings.ToLower(query)
	var results []memory.SearchResult
	for _, item := range f.memories {
		if strings.Contains(strings.ToLower(item.Content), q) || strings.Contains(strings.ToLower(string(item.Category)), q) {
			results = append(results, memory.SearchResult{Item: item, Score: 1.0})
			if limit > 0 && len(results) >= limit {
				break
			}
		}
	}
	return results, nil
}

func (f *fakeCLICore) AddMemory(_ context.Context, in memory.Input) (memory.Item, error) {
	f.memSeq++
	id := fmt.Sprintf("mem-%04d", f.memSeq)
	now := time.Now()
	item := memory.Item{
		ID:         id,
		Category:   in.Category,
		Content:    in.Content,
		Importance: in.Importance,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	f.memories = append([]memory.Item{item}, f.memories...)
	return item, nil
}

func (f *fakeCLICore) DeleteMemory(_ context.Context, id string) error {
	for i, item := range f.memories {
		if item.ID == id {
			f.memories = append(f.memories[:i], f.memories[i+1:]...)
			return nil
		}
	}
	return errors.New("memory not found")
}

func (f *fakeCLICore) ClearMemories(_ context.Context) error {
	f.memories = nil
	return nil
}

func (f *fakeCLICore) CommandToolchainPaths() []string {
	return append([]string(nil), f.toolchainPaths...)
}

func (f *fakeCLICore) SetCommandToolchainPaths(paths []string) error {
	f.toolchainPaths = append([]string(nil), paths...)
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
	commands  []core.CommandInfo
	outputs   map[string]string
}

type fakeCLITurn struct {
	done   chan struct{}
	result core.TurnResult
	err    error
}

func newFakeCLIRuntime(client *fakeCLICore, sessionID string, approval *approvalCLICore) *fakeCLIRuntime {
	return &fakeCLIRuntime{client: client, sessionID: sessionID, approval: approval,
		cancels: make(map[string]context.CancelFunc), turns: make(map[string]fakeCLITurn),
		outputs: make(map[string]string)}
}

func (r *fakeCLIRuntime) Close() error      { return nil }
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

func (r *fakeCLIRuntime) SubscribeEvents(ctx context.Context, afterCursor int64) (<-chan core.Event, error) {
	events := make(chan core.Event, 4)
	r.events = events
	if afterCursor == 0 && r.client.pending {
		events <- core.Event{Kind: core.EventTaskFinished, ExecutionID: "execution-paused", Status: core.TaskPaused, Cursor: 1}
	}
	go func() { <-ctx.Done(); close(events) }()
	return events, nil
}

func (r *fakeCLIRuntime) State(context.Context) (core.RuntimeState, error) {
	r.mu.Lock()
	status, active := r.status, r.active
	r.mu.Unlock()
	if r.client.pending {
		status = core.TaskPaused
		active = "execution-paused"
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
	r.client.pendingRequests = nil
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
func (r *fakeCLIRuntime) PendingRequests(context.Context) ([]core.PendingRequest, error) {
	return append([]core.PendingRequest(nil), r.client.pendingRequests...), nil
}
func (r *fakeCLIRuntime) ResolveUserQuestion(_ context.Context, answer core.UserQuestionAnswer) error {
	r.client.questionAnswers = append(r.client.questionAnswers, answer)
	r.client.pending = false
	r.client.pendingRequests = nil
	turn := fakeCLITurn{done: make(chan struct{}), result: core.TurnResult{ExecutionID: answer.ExecutionID, Status: core.TaskCompleted, Response: cliResponse("answered")}}
	r.mu.Lock()
	r.turns[answer.ExecutionID] = turn
	close(turn.done)
	r.mu.Unlock()
	r.client.resumes++
	return nil
}
func (r *fakeCLIRuntime) ResolveToolRecovery(_ context.Context, _ string, decision core.ToolRecoveryDecision) error {
	r.client.toolDecisions = append(r.client.toolDecisions, decision)
	return nil
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

func (r *fakeCLIRuntime) ListCommands(_ context.Context) ([]core.CommandInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	res := make([]core.CommandInfo, len(r.commands))
	copy(res, r.commands)
	return res, nil
}

func (r *fakeCLIRuntime) ReadCommand(_ context.Context, id string, _ core.CommandReadOptions) (core.CommandOutput, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cmd := range r.commands {
		if cmd.ID == id {
			out := r.outputs[id]
			return core.CommandOutput{Command: cmd, Output: out}, nil
		}
	}
	return core.CommandOutput{}, errors.New("command not found")
}

func (r *fakeCLIRuntime) StopCommand(_ context.Context, id string) (core.CommandOutput, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, cmd := range r.commands {
		if cmd.ID == id {
			r.commands[i].Status = core.CommandCancelled
			now := time.Now()
			r.commands[i].FinishedAt = now
			return core.CommandOutput{Command: r.commands[i]}, nil
		}
	}
	return core.CommandOutput{}, errors.New("command not found")
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
	// Most CLI tests exercise in-session behavior, so seed a persisted session first
	// and reuse the fake's existing session when one is already set up.
	seed := core.Session{}
	if latest, ok, err := client.LatestSession(context.Background()); err == nil && ok {
		seed = latest
	}
	if seed.ID == "" {
		created, err := client.CreateSession(context.Background(), nil)
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		seed = created
	}
	return runCLIWithOptions(t, client, input, watchKeys, LaunchOptions{SessionID: seed.ID})
}

// runCLIDraft runs the chat loop against a draft session that has no persisted row.
func runCLIDraft(t *testing.T, client cliCore, input string, watchKeys keyEventWatcher) (string, string) {
	t.Helper()
	return runCLIWithOptions(t, client, input, watchKeys, LaunchOptions{})
}

func runCLIWithOptions(t *testing.T, client cliCore, input string, watchKeys keyEventWatcher, opts LaunchOptions) (string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	if err := runCoreChatLoopWithOptions(client, bufio.NewReader(strings.NewReader(input)), &stdout, &stderr, watchKeys, opts); err != nil {
		t.Fatalf("runCoreChatLoopWithOptions: %v", err)
	}
	return stdout.String(), stderr.String()
}

func TestCLIPermissionsCommandSupportsAutoMode(t *testing.T) {
	client := &fakeCLICore{}
	var stdout, stderr bytes.Buffer
	handleCoreCLIPermissions(client, false, "/permissions auto", &stdout, &stderr)
	if client.PermissionMode() != core.PermissionAuto {
		t.Fatalf("permission mode = %q, want auto", client.PermissionMode())
	}
	if !strings.Contains(stdout.String(), "auto") || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q, want Auto mode confirmation", stdout.String(), stderr.String())
	}

	// The legacy ask alias is rejected instead of silently mapped to auto.
	stderr.Reset()
	handleCoreCLIPermissions(client, false, "/permissions ask", &stdout, &stderr)
	if client.PermissionMode() != core.PermissionAuto {
		t.Fatalf("permission mode = %q, want auto unchanged after a rejected alias", client.PermissionMode())
	}
	if !strings.Contains(stderr.String(), "unknown permission mode") {
		t.Fatalf("stderr=%q, want an unknown-mode error", stderr.String())
	}

	// Any other value is rejected with the accepted modes instead of being
	// forwarded to Core.
	stderr.Reset()
	handleCoreCLIPermissions(client, false, "/permissions nonsense", &stdout, &stderr)
	if !strings.Contains(stderr.String(), "read-only, workspace, auto, full-access") {
		t.Fatalf("stderr=%q, want the accepted modes listed", stderr.String())
	}

	stdout.Reset()
	handleCoreCLIPermissions(client, false, "/permissions", &stdout, &stderr)
	for _, mode := range []string{"read-only", "workspace", "auto", "full-access"} {
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

func TestCLIToolchainsCommand(t *testing.T) {
	client := &fakeCLICore{workspace: t.TempDir()}
	var stdout, stderr bytes.Buffer

	handleCoreCLIToolchains(client, "/toolchains", &stdout, &stderr)
	if !strings.Contains(stdout.String(), "No command toolchains configured") {
		t.Fatalf("unexpected output for /toolchains: %s", stdout.String())
	}

	stdout.Reset()
	handleCoreCLIToolchains(client, "/toolchains detect", &stdout, &stderr)
	if !strings.Contains(stdout.String(), "Detected") && !strings.Contains(stdout.String(), "No host toolchains") {
		t.Fatalf("unexpected output for /toolchains detect: %s", stdout.String())
	}

	stdout.Reset()
	handleCoreCLIToolchains(client, "/toolchains refresh", &stdout, &stderr)
	if !strings.Contains(stdout.String(), "Refreshed toolchains") {
		t.Fatalf("unexpected output for /toolchains refresh: %s", stdout.String())
	}

	stderr.Reset()
	handleCoreCLIToolchains(client, "/toolchains invalid", &stdout, &stderr)
	if !strings.Contains(stderr.String(), "usage: /toolchains") {
		t.Fatalf("unexpected error for invalid subcommand: %s", stderr.String())
	}
}

func TestCLIPermissionsTableKeepsFrontendsOnOneParser(t *testing.T) {
	for _, raw := range []string{"ask", "nonsense", "readonly", "full_access"} {
		client := &fakeCLICore{}
		initial := client.PermissionMode()
		var stdout, stderr bytes.Buffer
		handleCoreCLIPermissions(client, false, "/permissions "+raw, &stdout, &stderr)
		if client.PermissionMode() != initial {
			t.Fatalf("/permissions %s reached Core and changed it to %q", raw, client.PermissionMode())
		}
		if !strings.Contains(stderr.String(), "unknown permission mode") {
			t.Fatalf("/permissions %s: stderr=%q, want an unknown-mode error", raw, stderr.String())
		}
	}
}

func TestCLIHelpListsEveryRegistryCommand(t *testing.T) {
	stdout, stderr := runCLI(t, &fakeCLICore{}, "/help\n/exit\n", nil)
	for _, command := range slashcmd.For(slashcmd.TargetCLI) {
		if !strings.Contains(stdout, command.Usage) {
			t.Errorf("help output is missing usage %q", command.Usage)
		}
		if !strings.Contains(stdout, command.Description) {
			t.Errorf("help output is missing description for %q", command.Name)
		}
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
}

func TestChatLoopHelpListsCommandsWithoutStartingTask(t *testing.T) {
	client := &fakeCLICore{}
	stdout, stderr := runCLI(t, client, "/help\n/exit\n", nil)

	for _, expected := range []string{
		"Available commands:",
		"/help",
		"/permissions [mode]",
		"/resume [id|prefix]",
		"/continue",
		"read-only, workspace, auto, full-access",
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("help output %q is missing %q", stdout, expected)
		}
	}
	if strings.Contains(stdout, "/open") {
		t.Fatalf("help output still exposes the removed /open command: %q", stdout)
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

func TestCLIUnknownSlashTextSubmitsPrompt(t *testing.T) {
	client := &fakeCLICore{}
	_, stderr := runCLI(t, client, "/open session-2\n/home/iacsadmin/notes.txt 看一下这个文件\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want slash-prefixed text submitted without errors", stderr)
	}
	if len(client.inputs) != 2 {
		t.Fatalf("inputs = %#v, want both slash-prefixed lines treated as prompts", client.inputs)
	}
	if got := string(client.inputs[0].Input.Raw); got != `"/open session-2"` {
		t.Fatalf("first prompt = %s, want the removed /open line kept verbatim", got)
	}
	if got := string(client.inputs[1].Input.Raw); !strings.Contains(got, "/home/iacsadmin/notes.txt") {
		t.Fatalf("second prompt = %s, want the pasted absolute path", got)
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

func TestCLIPathApprovalExplainsSessionDirectoryGrant(t *testing.T) {
	request := core.ApprovalRequest{Tool: "read_file", PathGrant: &publictool.PathGrant{Path: `C:\Users\Code\private`, Access: publictool.PathRead}}
	prompt := formatCLIToolApproval(request, `C:\workspace`)
	if !strings.Contains(prompt, "read-only access") || !strings.Contains(prompt, `C:\Users\Code\private`) || !strings.Contains(prompt, "for this session") {
		t.Fatalf("prompt = %q, missing session grant scope", prompt)
	}
	if strings.Contains(prompt, "Allow this tool call once") {
		t.Fatalf("prompt = %q, still describes a one-time approval", prompt)
	}
	request.PathGrant.Access = publictool.PathReadWrite
	if prompt := formatCLIToolApproval(request, ""); !strings.Contains(prompt, "read and write access") {
		t.Fatalf("write grant prompt = %q", prompt)
	}
}

func TestChatLoopContinuesPausedCheckpointExplicitly(t *testing.T) {
	client := &fakeCLICore{pending: true}
	stdout, _ := runCLI(t, client, "/continue\n/exit\n", nil)
	if client.resumes != 1 || client.pending {
		t.Fatalf("resume calls=%d pending=%v, want one resume and no pending task", client.resumes, client.pending)
	}
	if !strings.Contains(stdout, "resumed") {
		t.Fatalf("stdout = %q, want resumed response", stdout)
	}
}

func TestCLIResumeAnswersUserQuestionBatch(t *testing.T) {
	question := &core.UserQuestionRequest{
		ID: "question-1", RunID: "run-1", InvocationID: "invocation-1", CallID: "call-1", ExecutionID: "execution-paused",
		Questions: []core.UserQuestionItem{
			{ID: "q1", Question: "Choose a strategy", Options: []core.UserQuestionOption{{Label: "Fast"}, {Label: "Safe", Recommended: true}}},
			{ID: "q2", Question: "Any constraints?"},
		},
	}
	client := &fakeCLICore{pending: true, pendingRequests: []core.PendingRequest{{Kind: core.PendingUserQuestion, UserQuestion: question}}}
	stdout, stderr := runCLI(t, client, "/continue\n\nKeep the API stable\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if len(client.questionAnswers) != 1 {
		t.Fatalf("resolved answers = %#v, want one batch", client.questionAnswers)
	}
	answer := client.questionAnswers[0]
	if answer.ExecutionID != "execution-paused" || answer.RunID != "run-1" || answer.InvocationID != "invocation-1" || answer.CallID != "call-1" || len(answer.Answers) != 2 {
		t.Fatalf("question identity or batch was not preserved: %#v", answer)
	}
	if answer.Answers[0] != (core.UserQuestionResponse{QuestionID: "q1", Answer: "Safe"}) || answer.Answers[1] != (core.UserQuestionResponse{QuestionID: "q2", Answer: "Keep the API stable"}) {
		t.Fatalf("answers = %#v, want ordered option and free-text answers", answer.Answers)
	}
	if !strings.Contains(stdout, "answered") || !strings.Contains(stdout, "Question 2/2") {
		t.Fatalf("stdout = %q, want question prompts and resumed output", stdout)
	}
	if !strings.Contains(stdout, "[Recommended]") || !strings.Contains(stdout, "default: Safe") {
		t.Fatalf("stdout = %q, want recommendation marker and default prompt", stdout)
	}
}

func TestCLIResumeHandlesModelAndToolRecovery(t *testing.T) {
	t.Run("model", func(t *testing.T) {
		modelRecovery := &core.PendingModelRecovery{ExecutionID: "execution-paused", Failure: core.ModelFailure{RetryNotBefore: time.Now().Add(-time.Second)}}
		client := &fakeCLICore{pending: true, pendingRequests: []core.PendingRequest{{Kind: core.PendingModelRecoveryRequest, ModelRecovery: modelRecovery}}}
		stdout, stderr := runCLI(t, client, "/continue\nr\n/exit\n", nil)
		if stderr != "" || client.resumes != 1 || !strings.Contains(stdout, "resumed") {
			t.Fatalf("stdout=%q stderr=%q resumes=%d, want retry", stdout, stderr, client.resumes)
		}
	})

	t.Run("tool skip", func(t *testing.T) {
		toolRecovery := &core.ToolRecoveryRequest{InvocationID: "invocation-2", Tool: "run_command"}
		client := &fakeCLICore{pending: true, pendingRequests: []core.PendingRequest{{Kind: core.PendingToolRecovery, ToolRecovery: toolRecovery}}}
		stdout, stderr := runCLI(t, client, "/continue\ns\n/exit\n", nil)
		if stderr != "" || client.resumes != 1 || !strings.Contains(stdout, "resumed") {
			t.Fatalf("stdout=%q stderr=%q resumes=%d, want skip and recover", stdout, stderr, client.resumes)
		}
		if len(client.toolDecisions) != 1 || client.toolDecisions[0] != core.ToolRecoverySkip {
			t.Fatalf("tool decisions = %#v, want one skip", client.toolDecisions)
		}
	})
}

func TestChatLoopDiscardsPausedCheckpointWhenStartingNewPrompt(t *testing.T) {
	client := &fakeCLICore{pending: true}
	stdout, stderr := runCLI(t, client, "new task\n/exit\n", nil)
	if client.discarded != 1 || client.pending {
		t.Fatalf("discarded=%d pending=%v, want discarded once", client.discarded, client.pending)
	}
	if len(client.inputs) != 1 || string(client.inputs[0].Input.Raw) != `"new task"` {
		t.Fatalf("responder inputs = %#v, want new task after discard", client.inputs)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want no pending-task error", stderr)
	}
	if !strings.Contains(stdout, "Discarded the paused checkpoint; starting a new turn.") {
		t.Fatalf("stdout = %q, want checkpoint discard confirmation", stdout)
	}
}

func TestCLICleanLaunchKeepsDraftUntilFirstPrompt(t *testing.T) {
	client := &fakeCLICore{
		latest: core.Session{ID: "old-session-123", Title: "Old Session"},
	}
	// Launching and exiting without a prompt must not create a session.
	stdout, stderr := runCLIDraft(t, client, "/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if strings.Contains(stdout, "session-") {
		t.Fatalf("stdout = %q, want no session banner", stdout)
	}
	if client.sequence != 0 {
		t.Fatalf("client.sequence = %d, want 0 (no session created on launch)", client.sequence)
	}
	if client.latest.ID != "old-session-123" {
		t.Fatalf("latest session ID = %q, want the previous session untouched", client.latest.ID)
	}

	// The first prompt creates exactly one session and runs the turn in it.
	client.latest = core.Session{ID: "old-session-123", Title: "Old Session"}
	stdout, stderr = runCLIDraft(t, client, "hello\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if client.sequence != 1 {
		t.Fatalf("client.sequence = %d, want 1 (first prompt creates the session)", client.sequence)
	}
	if len(client.inputs) != 1 || string(client.inputs[0].Input.Raw) != `"hello"` {
		t.Fatalf("inputs = %#v, want the first prompt submitted", client.inputs)
	}
	_ = stdout
}

func TestCLINewStaysDraft(t *testing.T) {
	client := &fakeCLICore{}
	stdout, stderr := runCLIDraft(t, client, "/new\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if client.sequence != 0 {
		t.Fatalf("client.sequence = %d, want 0 (/new must not persist a session)", client.sequence)
	}
	if !strings.Contains(stdout, "Started a fresh session; it is saved with your first message.") {
		t.Fatalf("stdout = %q, want draft notice", stdout)
	}
}

func TestCLIDraftRejectsSessionCommands(t *testing.T) {
	client := &fakeCLICore{}
	_, stderr := runCLIDraft(t, client, "/rewind\n/fork\n/export\n/exit\n", nil)
	if strings.Count(stderr, "No session yet. Send a message first.") != 3 {
		t.Fatalf("stderr = %q, want three draft-session notices", stderr)
	}
	if client.sequence != 0 {
		t.Fatalf("client.sequence = %d, want 0", client.sequence)
	}
}

func TestCLIStatusOnDraftReportsPendingSession(t *testing.T) {
	client := &fakeCLICore{}
	stdout, stderr := runCLIDraft(t, client, "/status\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "(pending — created with your first message)") {
		t.Fatalf("stdout = %q, want the pending session label", stdout)
	}
	if client.sequence != 0 {
		t.Fatalf("client.sequence = %d, want 0", client.sequence)
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

func TestCLIModelProfilePreservesSessionSettings(t *testing.T) {
	window := int64(1_000_000)
	reserve := int64(32_000)
	autoCompact := true
	threshold := 0.82
	client := &fakeCLICore{latest: core.Session{ID: "session-1", Settings: core.SessionSettings{
		ModelProfile: "primary", ReasoningEffort: "high",
		ContextWindow: &window, ReserveOutputTokens: &reserve, AutoCompact: &autoCompact, AutoCompactThreshold: &threshold,
	}}}
	var stdout, stderr bytes.Buffer
	handleCoreCLIModel(client, "session-1", "/model fast", &stdout, &stderr)
	if got := client.latest.Settings; got.ModelProfile != "fast" || got.ReasoningEffort != "high" || got.ContextWindow == nil || *got.ContextWindow != window || got.ReserveOutputTokens == nil || *got.ReserveOutputTokens != reserve || got.AutoCompact == nil || !*got.AutoCompact || got.AutoCompactThreshold == nil || *got.AutoCompactThreshold != threshold {
		t.Fatalf("model switch replaced unrelated session settings: %+v", got)
	}
	if stderr.Len() != 0 || !strings.Contains(stdout.String(), "Active model set to") {
		t.Fatalf("stdout=%q stderr=%q, want successful model switch", stdout.String(), stderr.String())
	}
}

func TestCLIStartupRejectsUnknownModelProfile(t *testing.T) {
	client := &fakeCLICore{}
	var stdout, stderr bytes.Buffer
	err := runCoreChatLoopWithOptions(client, bufio.NewReader(strings.NewReader("/exit\n")), &stdout, &stderr, nil, LaunchOptions{ModelProfile: "missing"})
	if err == nil || !strings.Contains(err.Error(), `unknown model profile "missing"`) {
		t.Fatalf("runCoreChatLoopWithOptions error = %v, want unknown model profile error", err)
	}
	if client.responses != 0 {
		t.Fatalf("responses = %d, want no task started", client.responses)
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

func TestCLIPlanModePermissionFailureIsTransactional(t *testing.T) {
	client := &fakeCLICore{mode: core.PermissionAuto, permissionErr: errors.New("permission store unavailable")}
	stdout, stderr := runCLI(t, client, "/plan\n/status\n/exit\n", nil)
	if client.PermissionMode() != core.PermissionAuto {
		t.Fatalf("permission mode changed after rejected Plan transition: %s", client.PermissionMode())
	}
	if !strings.Contains(stderr, "error: enter Plan mode: permission store unavailable") {
		t.Fatalf("stderr = %q, want permission failure", stderr)
	}
	if !strings.Contains(stdout, "Permissions: auto (Plan mode: false)") {
		t.Fatalf("stdout = %q, want original mode and Plan state", stdout)
	}
}

func TestCLISessionsAndResume(t *testing.T) {
	client := &fakeCLICore{sessions: []core.Session{
		{ID: "session-1", Title: "Current session"},
		{ID: "session-2", Title: "Restored session"},
	}}
	stdout, stderr := runCLI(t, client, "/resume\n/resume session-1\n/resume session-2\n/resume non-existent\n/exit\n", nil)
	if !strings.Contains(stdout, "Recent sessions:") {
		t.Fatalf("stdout = %q, want recent sessions list", stdout)
	}
	if !strings.Contains(stdout, "Already in session session-1.") {
		t.Fatalf("stdout = %q, want already in session", stdout)
	}
	if !strings.Contains(stderr, "No session found matching prefix") {
		t.Fatalf("stderr = %q, want no session found", stderr)
	}
	if !strings.Contains(stdout, "Resumed session session-2.") || client.runtime == nil || client.runtime.sessionID != "session-2" {
		t.Fatalf("stdout=%q runtime=%v, want session-2 restored", stdout, client.runtime)
	}
	if client.responses != 0 || len(client.inputs) != 0 {
		t.Fatalf("restoring a session started a model turn: responses=%d inputs=%#v", client.responses, client.inputs)
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
	for _, want := range []string{"Context Status & Diagnostics", "Workspace & Permissions:", "Session & Model:", "Capabilities & Tools:", "Workspace Memory:"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, missing %q", stdout, want)
		}
	}

	window := int64(1_000_000)
	client.latest = core.Session{
		ID: "sess-ctx-1", Title: "Context CLI Test", Settings: core.SessionSettings{ModelProfile: "primary"},
		SessionUsage:  core.UsageTotals{Input: 900, Cached: 300, Output: 120, Requests: 2},
		UsageRequests: []core.UsageRecord{{Purpose: core.UsagePurposeMain, AgentID: "lead", ContextWindow: window, Usage: core.Usage{InputTokens: 540, InputTokensDetails: core.TokenInputDetails{CachedTokens: 200}}}},
	}
	var diagnostics bytes.Buffer
	handleCoreCLIContext(client, "sess-ctx-1", "Context CLI Test", false, &diagnostics)
	for _, want := range []string{"Configured:  1.0M tokens", "Last prompt: 540 input tokens (200 cached, 340 uncached; 37.0% hit)", "Input:       900 (cached: 300)"} {
		if !strings.Contains(diagnostics.String(), want) {
			t.Errorf("context diagnostics %q missing %q", diagnostics.String(), want)
		}
	}
}

func TestCLIReviewSubmitsReadOnlyReviewIntoCurrentSession(t *testing.T) {
	workspace := t.TempDir()
	runCLITestGit(t, workspace, "init")
	runCLITestGit(t, workspace, "config", "user.email", "cli-review@example.invalid")
	runCLITestGit(t, workspace, "config", "user.name", "CLI Review Test")
	file := filepath.Join(workspace, "reviewed.txt")
	if err := os.WriteFile(file, []byte("before\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runCLITestGit(t, workspace, "add", "reviewed.txt")
	runCLITestGit(t, workspace, "commit", "-m", "initial")
	if err := os.WriteFile(file, []byte("after\n"), 0644); err != nil {
		t.Fatal(err)
	}

	client := &fakeCLICore{}
	runtime, err := client.OpenSessionRuntime(context.Background(), "review-session")
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := handleCoreCLIReview(runtime, workspace, "/review", nil, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if len(client.inputs) != 1 || client.inputs[0].PermissionMode != core.PermissionReadOnly {
		t.Fatalf("review requests = %#v, want one read-only turn", client.inputs)
	}
	if !strings.Contains(string(client.inputs[0].Input.Raw), "reviewed.txt") || !strings.Contains(string(client.inputs[0].Input.Raw), "after") {
		t.Fatalf("review prompt does not contain changed file materials: %s", client.inputs[0].Input.Raw)
	}
	if !strings.Contains(stdout.String(), "reply") || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q, want review response", stdout.String(), stderr.String())
	}
}

func runCLITestGit(t *testing.T, workspace string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = workspace
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func TestCLIMemoryCommands(t *testing.T) {
	client := &fakeCLICore{}

	// 1. Initially empty
	stdout, _ := runCLI(t, client, "/memory list\n/exit\n", nil)
	if !strings.Contains(stdout, "No workspace memories stored yet.") {
		t.Fatalf("stdout = %q, want empty memory message", stdout)
	}

	// 2. Add preference
	stdout, _ = runCLI(t, client, "/memory add preference Always use structured errors\n/exit\n", nil)
	if !strings.Contains(stdout, "Added workspace memory [mem-0001] (preference): Always use structured errors") {
		t.Fatalf("stdout = %q, want add preference confirmation", stdout)
	}

	// 3. Add convention
	stdout, _ = runCLI(t, client, "/memory add convention Follow standard table tests\n/exit\n", nil)
	if !strings.Contains(stdout, "Added workspace memory [mem-0002] (convention): Follow standard table tests") {
		t.Fatalf("stdout = %q, want add convention confirmation", stdout)
	}

	// 4. List memories
	stdout, _ = runCLI(t, client, "/memory list\n/exit\n", nil)
	if !strings.Contains(stdout, "Workspace Memories (2 items):") || !strings.Contains(stdout, "Always use structured errors") {
		t.Fatalf("stdout = %q, want list items", stdout)
	}

	// 5. Search memories
	stdout, _ = runCLI(t, client, "/memory list structured\n/exit\n", nil)
	if !strings.Contains(stdout, "Workspace Memories (1 matches for \"structured\"):") {
		t.Fatalf("stdout = %q, want search match", stdout)
	}

	// 6. Delete memory by prefix
	stdout, _ = runCLI(t, client, "/memory remove mem-0001\n/exit\n", nil)
	if !strings.Contains(stdout, "Deleted workspace memory [mem-0001]") {
		t.Fatalf("stdout = %q, want delete confirmation", stdout)
	}

	// 7. Clear memories requires --force
	_, stderr := runCLI(t, client, "/memory clear\n/exit\n", nil)
	if !strings.Contains(stderr, "Run '/memory clear --force' to confirm.") {
		t.Fatalf("stderr = %q, want clear confirmation warning", stderr)
	}

	// 8. Clear memories with --force
	stdout, _ = runCLI(t, client, "/memory clear --force\n/exit\n", nil)
	if !strings.Contains(stdout, "Cleared all workspace memories.") {
		t.Fatalf("stdout = %q, want cleared confirmation", stdout)
	}

	// 9. Verify empty after clear
	stdout, _ = runCLI(t, client, "/memory\n/exit\n", nil)
	if !strings.Contains(stdout, "No workspace memories stored yet.") {
		t.Fatalf("stdout = %q, want empty after clear", stdout)
	}
}

func TestCLIPsAndStopCommands(t *testing.T) {
	client := &fakeCLICore{}

	// 1. Initially empty
	stdout, _ := runCLI(t, client, "/ps\n/exit\n", nil)
	if !strings.Contains(stdout, "No background processes in current session.") {
		t.Fatalf("stdout = %q, want empty processes message", stdout)
	}

	// 2. Add two background commands to runtime
	now := time.Now()
	exitZero := 0
	client.runtime = &fakeCLIRuntime{
		client: client,
		commands: []core.CommandInfo{
			{
				ID:        "cmd-1001-dev",
				Command:   "npm run dev",
				Status:    core.CommandRunning,
				StartedAt: now.Add(-30 * time.Second),
			},
			{
				ID:         "cmd-1002-test",
				Command:    "go test ./...",
				Status:     core.CommandSucceeded,
				StartedAt:  now.Add(-60 * time.Second),
				FinishedAt: now.Add(-55 * time.Second),
				ExitCode:   &exitZero,
			},
		},
		outputs: map[string]string{
			"cmd-1001-dev": "VITE v5.0.0 ready in 350 ms\nLocal: http://localhost:5173/\nNetwork: use --host to expose",
		},
	}

	// 3. /ps lists both commands
	stdout, _ = runCLI(t, client, "/ps\n/exit\n", nil)
	if !strings.Contains(stdout, "Background Processes (2):") || !strings.Contains(stdout, "npm run dev") || !strings.Contains(stdout, "go test ./...") {
		t.Fatalf("stdout = %q, want list of 2 processes", stdout)
	}

	// 4. /logs for cmd-1001-dev
	stdout, _ = runCLI(t, client, "/logs cmd-1001\n/exit\n", nil)
	if !strings.Contains(stdout, "Logs for [cmd-1001]") || !strings.Contains(stdout, "http://localhost:5173/") {
		t.Fatalf("stdout = %q, want logs for process", stdout)
	}

	// 5. /stop for specific ID prefix
	stdout, _ = runCLI(t, client, "/stop cmd-1001\n/exit\n", nil)
	if !strings.Contains(stdout, "Stopped background process [cmd-1001]: npm run dev") {
		t.Fatalf("stdout = %q, want stop confirmation", stdout)
	}

	// 6. /stop all when 0 active processes
	stdout, _ = runCLI(t, client, "/stop all\n/exit\n", nil)
	if !strings.Contains(stdout, "No active background processes to stop.") {
		t.Fatalf("stdout = %q, want no active processes message", stdout)
	}
}

func TestCLIDropCommand(t *testing.T) {
	client := &fakeCLICore{
		sessions: []core.Session{
			{ID: "sess-101-frontend", Title: "Frontend Feature"},
			{ID: "sess-102-backend", Title: "Backend Bugfix"},
			{ID: "sess-103-database", Title: "Database Migration"},
		},
	}

	// 1. Usage error when no ID provided
	_, stderr := runCLI(t, client, "/drop\n/exit\n", nil)
	if !strings.Contains(stderr, "usage: /drop [id|prefix]") {
		t.Fatalf("stderr = %q, want usage error", stderr)
	}

	// 2. Prevent deleting active session
	_, stderr = runCLI(t, client, "/resume sess-101\n/drop sess-101\n/exit\n", nil)
	if !strings.Contains(stderr, "Cannot delete currently active session") {
		t.Fatalf("stderr = %q, want active session guard error", stderr)
	}

	// 3. Delete other session via unique prefix
	stdout, stderr := runCLI(t, client, "/resume sess-101\n/drop sess-102\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("unexpected stderr: %s", stderr)
	}
	if !strings.Contains(stdout, "Deleted session sess-102-backend.") {
		t.Fatalf("stdout = %q, want deleted confirmation", stdout)
	}
	for _, s := range client.sessions {
		if s.ID == "sess-102-backend" {
			t.Fatalf("expected sess-102-backend to be removed from sessions")
		}
	}

	// 4. Removed aliases are no longer commands, so they are submitted as prompts
	// instead of deleting anything.
	before := len(client.inputs)
	stdout, stderr = runCLI(t, client, "/delete sess-103\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want the removed alias submitted as a prompt", stderr)
	}
	if len(client.inputs) != before+1 || string(client.inputs[len(client.inputs)-1].Input.Raw) != `"/delete sess-103"` {
		t.Fatalf("inputs = %#v, want the removed alias submitted as a prompt", client.inputs)
	}
	if strings.Contains(stdout, "Deleted session sess-103-database.") {
		t.Fatalf("stdout = %q, removed alias unexpectedly deleted a session", stdout)
	}
}

func TestCLIDetailCommand(t *testing.T) {
	// 1. Session without any tool executions
	clientEmpty := &fakeCLICore{
		latest: core.Session{
			ID:    "sess-empty",
			Title: "Empty Tool Session",
		},
	}
	stdout, stderr := runCLI(t, clientEmpty, "/detail\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "No tool executions found in current session.") {
		t.Fatalf("stdout = %q, want 'No tool executions found'", stdout)
	}

	// 2. Session with tool calls in history
	clientWithTools := &fakeCLICore{
		initialTitle: "Tool Detail Session",
		initialHistory: json.RawMessage(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"list and check"}]},
			{"type":"function_call","call_id":"c1","name":"list_dir","arguments":"{\"path\":\".\"}"},
			{"type":"function_call_output","call_id":"c1","output":"main.go\ngo.mod\nREADME.md"},
			{"type":"function_call","call_id":"c2","name":"run_command","arguments":"{\"command\":\"git status\"}"},
			{"type":"function_call_output","call_id":"c2","output":"On branch main\nChanges not staged for commit:\n  modified: app.go"}
		]`),
	}

	// /detail with no args defaults to latest tool (tool 2)
	stdout, stderr = runCLI(t, clientWithTools, "/detail\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "--- Tool 2 of 2: run_command") {
		t.Fatalf("stdout = %q, want tool 2 header", stdout)
	}
	if !strings.Contains(stdout, "On branch main") || !strings.Contains(stdout, "modified: app.go") {
		t.Fatalf("stdout = %q, want git status output", stdout)
	}
	if !strings.Contains(stdout, "Showing latest tool 2 of 2") {
		t.Fatalf("stdout = %q, want navigation hint", stdout)
	}

	// /detail 1 inspects the first tool (list_dir)
	stdout, stderr = runCLI(t, clientWithTools, "/detail 1\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "--- Tool 1 of 2: list_dir") {
		t.Fatalf("stdout = %q, want tool 1 header", stdout)
	}
	if !strings.Contains(stdout, "main.go") || !strings.Contains(stdout, "README.md") {
		t.Fatalf("stdout = %q, want list_dir output", stdout)
	}

	// /detail out of range
	_, stderr = runCLI(t, clientWithTools, "/detail 99\n/exit\n", nil)
	if !strings.Contains(stderr, "error: tool index 99 out of range (1-2)") {
		t.Fatalf("stderr = %q, want out of range error", stderr)
	}
}

func TestCLIEffortAndAgentsCommands(t *testing.T) {
	client := &fakeCLICore{
		initialSettings: core.SessionSettings{ReasoningEffort: "low"},
		initialAgentUsage: map[string]core.UsageTotals{
			"lead":     {Requests: 5, Input: 1000, Output: 200},
			"worker-1": {Requests: 2, Input: 400, Output: 80},
		},
	}

	// 1. /effort views current effort
	stdout, stderr := runCLI(t, client, "/effort\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Current reasoning effort: low") || !strings.Contains(stdout, "Available levels:") {
		t.Fatalf("stdout = %q, want current effort", stdout)
	}

	// 2. /effort high sets effort
	stdout, stderr = runCLI(t, client, "/effort high\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Reasoning effort set to high.") {
		t.Fatalf("stdout = %q, want effort set confirmation", stdout)
	}

	// 3. /effort with invalid level
	_, stderr = runCLI(t, client, "/effort invalid-level\n/exit\n", nil)
	if !strings.Contains(stderr, "Invalid reasoning effort \"invalid-level\"") {
		t.Fatalf("stderr = %q, want invalid effort error", stderr)
	}

	// 4. /agents lists all agents in session
	stdout, stderr = runCLI(t, client, "/agents\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Session Agents") || !strings.Contains(stdout, "Main Agent") || !strings.Contains(stdout, "worker-1") {
		t.Fatalf("stdout = %q, want agents list", stdout)
	}

	// 5. /agents for specific worker
	stdout, stderr = runCLI(t, client, "/agents worker-1\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Agent [worker-1]:") || !strings.Contains(stdout, "Requests:  2") {
		t.Fatalf("stdout = %q, want worker-1 stats", stdout)
	}
}

func TestCLIExportHTMLAndJSONL(t *testing.T) {
	tmpDir := t.TempDir()
	client := &fakeCLICore{
		workspace:    tmpDir,
		initialTitle: "Export Test",
		initialHistory: json.RawMessage(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"world"}]}
		]`),
	}

	htmlFile := filepath.Join(tmpDir, "transcript.html")
	jsonlFile := filepath.Join(tmpDir, "transcript.jsonl")

	stdout, stderr := runCLI(t, client, fmt.Sprintf("/export html %s\n/export jsonl %s\n/exit\n", htmlFile, jsonlFile), nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Exported session transcript to") {
		t.Fatalf("stdout = %q, want export confirmation", stdout)
	}

	// Verify HTML file was written and contains HTML tags
	htmlBytes, err := os.ReadFile(htmlFile)
	if err != nil {
		t.Fatalf("read HTML file: %v", err)
	}
	if !strings.Contains(string(htmlBytes), "<!DOCTYPE html>") || !strings.Contains(string(htmlBytes), "Export Test") {
		t.Fatalf("html content = %s, want valid HTML with title", string(htmlBytes))
	}

	// Verify JSONL file was written
	jsonlBytes, err := os.ReadFile(jsonlFile)
	if err != nil {
		t.Fatalf("read JSONL file: %v", err)
	}
	if !strings.Contains(string(jsonlBytes), `"type":"message"`) {
		t.Fatalf("jsonl content = %s, want JSON lines", string(jsonlBytes))
	}
}

func TestCLIRewindPreview(t *testing.T) {
	client := &fakeCLICore{
		initialTitle: "Rewind Preview Test",
		initialHistory: json.RawMessage(`[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"first turn"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"resp 1"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"second turn"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"resp 2"}]}
		]`),
	}

	// /rewind preview 1 shows preview without executing rewind
	stdout, stderr := runCLI(t, client, "/rewind preview 1\n/exit\n", nil)
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if !strings.Contains(stdout, "Rewind preview (would remove 1 turn(s)):") || !strings.Contains(stdout, "second turn") {
		t.Fatalf("stdout = %q, want rewind preview", stdout)
	}
	if !strings.Contains(stdout, "Run '/rewind 1' to execute.") {
		t.Fatalf("stdout = %q, want execute instruction", stdout)
	}
}

// TestCLITUISpecificCommandsFriendlyGuide keeps the frontend split honest: every
// command the registry lists only for the TUI is refused with the same wording,
// so a new TUI-only command cannot fall through and become a prompt.
func TestCLITUISpecificCommandsFriendlyGuide(t *testing.T) {
	client := &fakeCLICore{}
	_, stderr := runCLI(t, client, "/theme dark\n/shortcuts\n/thought\n/cancel-queue\n/exit\n", nil)
	for _, name := range []string{"/theme", "/shortcuts", "/thought", "/cancel-queue"} {
		want := "`" + name + "` is only available in the full-screen TUI"
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr = %q, want the registry notice for %s", stderr, name)
		}
	}
	if len(client.inputs) != 0 {
		t.Fatalf("inputs = %#v, want no prompt submitted for TUI-only commands", client.inputs)
	}
}

func TestCLIFileMentionExpansion(t *testing.T) {
	tmpDir := t.TempDir()
	targetFile := filepath.Join(tmpDir, "example.txt")
	if err := os.WriteFile(targetFile, []byte("file content for expansion"), 0644); err != nil {
		t.Fatal(err)
	}

	client := &fakeCLICore{
		workspace: tmpDir,
	}

	_, _ = runCLI(t, client, "please inspect @example.txt\n/exit\n", nil)
	if len(client.inputs) == 0 {
		t.Fatalf("expected at least 1 turn request")
	}
	submittedInput := string(client.inputs[0].Input.Raw)
	if !strings.Contains(submittedInput, "file content for expansion") {
		t.Fatalf("submitted input = %q, want expanded file content", submittedInput)
	}
}

func TestPrintCoreTaskResultSanitizesTerminalControls(t *testing.T) {
	var stdout, stderr bytes.Buffer
	response := json.RawMessage(`{"output":[{"type":"message","content":[{"type":"output_text","text":"before\u001b]52;c;clipboard\u000731m after"}]}]}`)
	if err := printCoreTaskResult(&stdout, &stderr, response, nil, false); err != nil {
		t.Fatalf("printCoreTaskResult: %v", err)
	}
	if strings.Contains(stdout.String(), "\x1b]52") || strings.ContainsRune(stdout.String(), '\a') || strings.ContainsRune(stdout.String(), '\u009b') {
		t.Fatalf("stdout retained terminal control sequence: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "before") || !strings.Contains(stdout.String(), "after") {
		t.Fatalf("stdout lost printable text: %q", stdout.String())
	}
}

func TestCoreCLILogsSanitizeProcessOutput(t *testing.T) {
	runtime := newFakeCLIRuntime(&fakeCLICore{}, "session-1", nil)
	runtime.commands = []core.CommandInfo{{ID: "command-1", Command: "run command"}}
	runtime.outputs["command-1"] = "before\x1b]52;c;clipboard\a\u009b31m after\rrewritten"
	var stdout, stderr bytes.Buffer

	handleCoreCLILogs(runtime, "/logs command-1", &stdout, &stderr)
	if strings.Contains(stdout.String(), "\x1b]52") || strings.ContainsRune(stdout.String(), '\a') || strings.ContainsRune(stdout.String(), '\u009b') {
		t.Fatalf("stdout retained terminal control sequence: %q", stdout.String())
	}
	if strings.ContainsRune(stdout.String(), '\r') {
		t.Fatalf("carriage return was retained: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "before") || !strings.Contains(stdout.String(), "after") {
		t.Fatalf("stdout lost printable process output: %q", stdout.String())
	}
}

func TestCLIPlanModeReachesSubmittedTurn(t *testing.T) {
	for _, startup := range []bool{false, true} {
		t.Run(fmt.Sprint(startup), func(t *testing.T) {
			client := &fakeCLICore{mode: core.PermissionAuto}
			input := "/plan\nplan this task\n/plan\nimplement this task\n/exit\n"
			if startup {
				input = "plan this task\n/plan\nimplement this task\n/exit\n"
			}
			var stdout, stderr bytes.Buffer
			err := runCoreChatLoopWithOptions(client, bufio.NewReader(strings.NewReader(input)), &stdout, &stderr, nil, LaunchOptions{PlanMode: startup})
			if err != nil {
				t.Fatal(err)
			}
			if len(client.inputs) != 2 || !client.inputs[0].PlanMode || client.inputs[1].PlanMode {
				t.Fatalf("requests=%+v", client.inputs)
			}
			if client.PermissionMode() != core.PermissionAuto {
				t.Fatalf("restored permissions=%s", client.PermissionMode())
			}
		})
	}
}
