package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/luneCore"
)

type fakeResponder struct {
	inputs           []core.TurnRequest
	firstErr         error
	responses        int
	pending          bool
	resumes          int
	discarded        int
	compact          int
	compactErr       error
	mode             core.PermissionMode
	permissionErr    error
	contextDefaults  core.ContextDefaults
	attachmentLimits core.AttachmentLimits
	sessions         map[string]core.Session
	last             string
	sequence         int
	mcpServers       []core.MCPServerStatus
	mcpActions       []string
	mcpActionErr     error
	openMCPURLs      []string
	interactionIDs   []string
	interactionJSON  []json.RawMessage
	interactionReady bool
	runtimeMu        sync.Mutex
	runtimes         map[string]*fakeSessionRuntime
}

type blockingResponder struct {
	*fakeResponder
	started chan struct{}
}

type fakeResumableResponder = fakeResponder

func (f *fakeResponder) Workspace() string { return "/workspace" }

func (f *fakeResponder) runTask(_ context.Context, request core.TurnRequest) (json.RawMessage, error) {
	f.inputs = append(f.inputs, request)
	f.responses++
	if f.responses == 1 && f.firstErr != nil {
		return nil, f.firstErr
	}
	return json.RawMessage(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"reply"}]}]}`), nil
}

func (f *blockingResponder) runTask(ctx context.Context, _ core.TurnRequest) (json.RawMessage, error) {
	close(f.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakeResponder) ResumeTask(context.Context, string) (json.RawMessage, error) {
	f.resumes++
	f.pending = false
	return json.RawMessage(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"resumed"}]}]}`), nil
}

func (f *fakeResponder) HasPending(context.Context, string) (bool, error) { return f.pending, nil }
func (f *fakeResponder) PendingHistory(context.Context, string) (json.RawMessage, error) {
	return nil, nil
}
func (f *fakeResponder) PendingPlanMode(context.Context, string) (bool, error) { return false, nil }

func (f *fakeResponder) DiscardTask(context.Context, string) (bool, error) {
	if !f.pending {
		return false, nil
	}
	f.pending = false
	f.discarded++
	return true, nil
}

func (f *fakeResponder) Compact(context.Context, string) (int, error) {
	if f.compactErr != nil {
		return 0, f.compactErr
	}
	return f.compact, nil
}

func (f *fakeResponder) CreateSession(_ context.Context, history json.RawMessage) (core.Session, error) {
	f.sequence++
	id := fmt.Sprintf("session-%d", f.sequence)
	saved := core.Session{
		ID: id, Workspace: f.Workspace(), History: append(json.RawMessage(nil), history...),
		Settings: core.SessionSettings{ModelProfile: "lead"},
	}
	if f.sessions == nil {
		f.sessions = make(map[string]core.Session)
	}
	f.sessions[id] = saved
	f.last = id
	return saved, nil
}

func (f *fakeResponder) LatestSession(context.Context) (core.Session, bool, error) {
	saved, ok := f.sessions[f.last]
	if ok && !saved.Archived {
		return saved, true, nil
	}
	for _, candidate := range f.sessions {
		if !candidate.Archived {
			return candidate, true, nil
		}
	}
	return core.Session{}, false, nil
}

func (f *fakeResponder) ListSessions(_ context.Context, _ int, includeArchived bool) ([]core.Session, error) {
	sessions := make([]core.Session, 0, len(f.sessions))
	for _, saved := range f.sessions {
		if saved.Archived && !includeArchived {
			continue
		}
		sessions = append(sessions, saved)
	}
	return sessions, nil
}

func (f *fakeResponder) GetSession(_ context.Context, id string) (core.Session, error) {
	saved, ok := f.sessions[id]
	if !ok {
		return core.Session{}, fmt.Errorf("unknown session %q", id)
	}
	return saved, nil
}

func (f *fakeResponder) ForkSession(ctx context.Context, id string) (core.Session, error) {
	source, err := f.GetSession(ctx, id)
	if err != nil {
		return core.Session{}, err
	}
	branch, err := f.CreateSession(ctx, source.History)
	if err != nil {
		return core.Session{}, err
	}
	branch.Settings = source.Settings
	f.sessions[branch.ID] = branch
	return branch, nil
}

func (f *fakeResponder) SetSessionTitle(_ context.Context, id, title string) error {
	saved, ok := f.sessions[id]
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	saved.Title = title
	f.sessions[id] = saved
	return nil
}

func (f *fakeResponder) SetSessionArchived(_ context.Context, id string, archived bool) error {
	saved, ok := f.sessions[id]
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	saved.Archived = archived
	f.sessions[id] = saved
	return nil
}

func (f *fakeResponder) DeleteSession(_ context.Context, id string) error {
	if _, ok := f.sessions[id]; !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	delete(f.sessions, id)
	if f.last == id {
		f.last = ""
	}
	return nil
}

func (f *fakeResponder) RewindSession(context.Context, string, int) (int, error) { return 0, nil }

func (f *fakeResponder) ListModelProfiles() []core.ModelOption {
	return []core.ModelOption{
		{Profile: "lead", Provider: "test", Model: "lead-model"},
		{Profile: "review", Provider: "test", Model: "review-model"},
	}
}

func (f *fakeResponder) SetSessionSettings(_ context.Context, id string, settings core.SessionSettings) error {
	saved, ok := f.sessions[id]
	if !ok {
		return fmt.Errorf("unknown session %q", id)
	}
	saved.Settings = settings
	f.sessions[id] = saved
	return nil
}

func (f *fakeResponder) ProjectContextDefaults() core.ContextDefaults { return f.contextDefaults }

func (f *fakeResponder) ProjectAttachmentLimits() core.AttachmentLimits {
	if f.attachmentLimits.MaxFiles == 0 && f.attachmentLimits.MaxFileBytes == 0 && f.attachmentLimits.MaxTotalBytes == 0 {
		return core.AttachmentLimits{MaxFiles: 5, MaxFileBytes: 10 << 20, MaxTotalBytes: 20 << 20}
	}
	return f.attachmentLimits
}

func (f *fakeResponder) PermissionMode() core.PermissionMode {
	if f.mode == "" {
		return core.PermissionWorkspace
	}
	return f.mode
}

func (f *fakeResponder) SetPermissionMode(mode core.PermissionMode) error {
	if f.permissionErr != nil {
		return f.permissionErr
	}
	f.mode = mode
	return nil
}
func (f *fakeResponder) ResolveApproval(string, bool) error { return nil }
func (f *fakeResponder) MCPServers() []core.MCPServerStatus {
	return append([]core.MCPServerStatus(nil), f.mcpServers...)
}
func (f *fakeResponder) EnableMCPServer(name string) error {
	f.mcpActions = append(f.mcpActions, "enable:"+name)
	return f.mcpActionErr
}
func (f *fakeResponder) DisableMCPServer(name string) error {
	f.mcpActions = append(f.mcpActions, "disable:"+name)
	return f.mcpActionErr
}
func (f *fakeResponder) AuthenticateMCPServer(_ context.Context, name string) error {
	f.mcpActions = append(f.mcpActions, "auth:"+name)
	return f.mcpActionErr
}
func (f *fakeResponder) OpenMCPURL(serverName, target string) error {
	f.openMCPURLs = append(f.openMCPURLs, serverName+" "+target)
	return f.mcpActionErr
}
func (f *fakeResponder) SetUserInteractionAvailable(available bool) { f.interactionReady = available }
func (f *fakeResponder) ResolveUserInteraction(id string, response json.RawMessage) error {
	f.interactionIDs = append(f.interactionIDs, id)
	f.interactionJSON = append(f.interactionJSON, append(json.RawMessage(nil), response...))
	return f.mcpActionErr
}

func runTUICommands(model tea.Model, command tea.Cmd) tea.Model {
	commands := []tea.Cmd{command}
	for steps := 0; len(commands) > 0 && steps < 128; steps++ {
		command := commands[0]
		commands = commands[1:]
		if command == nil {
			continue
		}
		message := command()
		value := reflect.ValueOf(message)
		if value.IsValid() && value.Kind() == reflect.Slice && (value.Type().Name() == "sequenceMsg" || value.Type().Name() == "BatchMsg") {
			for index := value.Len() - 1; index >= 0; index-- {
				if next, ok := value.Index(index).Interface().(tea.Cmd); ok {
					commands = append([]tea.Cmd{next}, commands...)
				}
			}
			continue
		}
		var next tea.Cmd
		model, next = model.Update(message)
		if next != nil {
			commands = append(commands, next)
		}
	}
	return model
}

func (f *fakeResponder) OpenSessionRuntime(_ context.Context, sessionID string) (SessionRuntime, error) {
	f.runtimeMu.Lock()
	defer f.runtimeMu.Unlock()
	if f.runtimes == nil {
		f.runtimes = make(map[string]*fakeSessionRuntime)
	}
	if runtime := f.runtimes[sessionID]; runtime != nil {
		return runtime, nil
	}
	runtime := newFakeSessionRuntime(f, sessionID, f.runTask)
	f.runtimes[sessionID] = runtime
	return runtime, nil
}

func (f *blockingResponder) OpenSessionRuntime(_ context.Context, sessionID string) (SessionRuntime, error) {
	return newFakeSessionRuntime(f.fakeResponder, sessionID, f.runTask), nil
}

type fakeSessionRuntime struct {
	owner              *fakeResponder
	sessionID          string
	runner             func(context.Context, core.TurnRequest) (json.RawMessage, error)
	mu                 sync.Mutex
	sequence           int
	active             string
	status             core.TaskStatus
	cancels            map[string]context.CancelFunc
	turns              map[string]fakeTurn
	queue              []core.QueuedTurnState
	pendingRequests    []core.PendingRequest
	pendingRequestsErr error
	resolvedQuestions  []core.UserQuestionAnswer
}

type fakeTurn struct {
	done   chan struct{}
	result core.TurnResult
	err    error
}

func newFakeSessionRuntime(owner *fakeResponder, sessionID string, runner func(context.Context, core.TurnRequest) (json.RawMessage, error)) *fakeSessionRuntime {
	return &fakeSessionRuntime{
		owner: owner, sessionID: sessionID, runner: runner,
		cancels: make(map[string]context.CancelFunc), turns: make(map[string]fakeTurn),
	}
}

func (r *fakeSessionRuntime) SessionID() string { return r.sessionID }

func (r *fakeSessionRuntime) SubmitTurn(_ context.Context, request core.TurnRequest, mode core.SubmissionMode) (core.TurnSubmission, error) {
	if mode == core.QueueInput {
		r.mu.Lock()
		id := fmt.Sprintf("queue-%d", len(r.queue)+1)
		position := int64(len(r.queue) + 1)
		r.owner.inputs = append(r.owner.inputs, request)
		r.queue = append(r.queue, core.QueuedTurnState{QueueID: id, ExecutionID: id, Position: int64(len(r.queue) + 1), Status: "queued"})
		r.mu.Unlock()
		return core.TurnSubmission{Status: core.SubmissionQueued, QueueID: id, ExecutionID: id, Position: position}, nil
	}
	r.mu.Lock()
	r.sequence++
	executionID := fmt.Sprintf("execution-%d", r.sequence)
	ctx, cancel := context.WithCancel(context.Background())
	turn := fakeTurn{done: make(chan struct{})}
	r.turns[executionID] = turn
	r.cancels[executionID] = cancel
	r.active = executionID
	r.status = core.TaskRunning
	r.mu.Unlock()
	go func() {
		response, err := r.runner(ctx, request)
		r.mu.Lock()
		turn := r.turns[executionID]
		turn.result = core.TurnResult{ExecutionID: executionID, Response: response, Status: core.TaskCompleted}
		turn.err = err
		if err != nil {
			turn.result.Status = core.TaskFailed
		}
		r.turns[executionID] = turn
		r.status = turn.result.Status
		if r.active == executionID {
			r.active = ""
		}
		close(turn.done)
		r.mu.Unlock()
		cancel()
	}()
	return core.TurnSubmission{Status: core.SubmissionAccepted, ExecutionID: executionID}, nil
}

func (r *fakeSessionRuntime) WaitTurn(ctx context.Context, executionID string) (core.TurnResult, error) {
	r.mu.Lock()
	turn, ok := r.turns[executionID]
	r.mu.Unlock()
	if !ok {
		return core.TurnResult{}, core.ErrTurnNotFound
	}
	select {
	case <-turn.done:
		r.mu.Lock()
		turn = r.turns[executionID]
		r.mu.Unlock()
		return turn.result, turn.err
	case <-ctx.Done():
		return core.TurnResult{}, ctx.Err()
	}
}

func (r *fakeSessionRuntime) SubscribeEvents(ctx context.Context, _ int64) (<-chan core.Event, error) {
	events := make(chan core.Event)
	go func() {
		<-ctx.Done()
		close(events)
	}()
	return events, nil
}

func (r *fakeSessionRuntime) State(context.Context) (core.RuntimeState, error) {
	r.mu.Lock()
	status, active := r.status, r.active
	queue := append([]core.QueuedTurnState(nil), r.queue...)
	r.mu.Unlock()
	if r.owner.pending {
		status = core.TaskPaused
	}
	return core.RuntimeState{Task: core.TaskState{SessionID: r.sessionID, ExecutionID: active, Status: status}, Queue: queue}, nil
}

func (r *fakeSessionRuntime) Interrupt(executionID string) bool {
	r.mu.Lock()
	cancel := r.cancels[executionID]
	for index, queued := range r.queue {
		if queued.ExecutionID == executionID {
			r.queue = append(r.queue[:index], r.queue[index+1:]...)
			r.mu.Unlock()
			return true
		}
	}
	r.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (r *fakeSessionRuntime) Recover(_ context.Context, _ ...core.ExecutionOptions) (core.TurnSubmission, error) {
	response, err := r.owner.ResumeTask(context.Background(), r.sessionID)
	r.mu.Lock()
	r.sequence++
	executionID := fmt.Sprintf("execution-%d", r.sequence)
	turn := fakeTurn{done: make(chan struct{}), result: core.TurnResult{ExecutionID: executionID, Response: response, Status: core.TaskCompleted}, err: err}
	r.turns[executionID] = turn
	r.status, r.active = core.TaskCompleted, ""
	close(turn.done)
	r.mu.Unlock()
	return core.TurnSubmission{Status: core.SubmissionAccepted, ExecutionID: executionID}, nil
}

func (r *fakeSessionRuntime) DiscardPaused(context.Context) (bool, error) {
	discarded, err := r.owner.DiscardTask(context.Background(), r.sessionID)
	if err != nil || !discarded {
		return discarded, err
	}
	r.mu.Lock()
	r.status = core.TaskCancelled
	r.active = ""
	r.pendingRequests = nil
	r.mu.Unlock()
	return discarded, nil
}

func (r *fakeSessionRuntime) SetUserInteractionAvailable(available bool) {
	r.owner.SetUserInteractionAvailable(available)
}

func (r *fakeSessionRuntime) PendingRequests(context.Context) ([]core.PendingRequest, error) {
	return append([]core.PendingRequest(nil), r.pendingRequests...), r.pendingRequestsErr
}

func (r *fakeSessionRuntime) ResolveApproval(id string, allowed bool) error {
	return r.owner.ResolveApproval(id, allowed)
}

func (r *fakeSessionRuntime) ResolveUserQuestion(_ context.Context, answer core.UserQuestionAnswer) error {
	r.resolvedQuestions = append(r.resolvedQuestions, answer)
	return nil
}
func (r *fakeSessionRuntime) ResolveUserInteraction(id string, response json.RawMessage) error {
	return r.owner.ResolveUserInteraction(id, response)
}

func (*fakeSessionRuntime) ResolveToolRecovery(context.Context, string, core.ToolRecoveryDecision) error {
	return nil
}
func (*fakeSessionRuntime) ImplementPlan(context.Context, string, core.PlanImplementationMode, ...core.PlanAuthorization) (core.PlanImplementation, error) {
	return core.PlanImplementation{}, nil
}
