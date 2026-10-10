// Package core exposes the agent runtime without terminal presentation details.
package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/afterlune/lune/internal/agentgraph"
	"github.com/afterlune/lune/internal/model"
	"github.com/afterlune/lune/internal/session"
	"github.com/afterlune/lune/internal/tool"
	"github.com/afterlune/luneGraph/checkpoint"
	"github.com/afterlune/luneGraph/checkpoint/sqlite"
)

const legacyRunID = "lune-cli-active"

type PermissionMode string

const (
	PermissionReadOnly   PermissionMode = "read-only"
	PermissionWorkspace  PermissionMode = "workspace"
	PermissionAsk        PermissionMode = "ask"
	PermissionFullAccess PermissionMode = "full-access"
)

type TaskStatus string

const (
	TaskIdle      TaskStatus = "idle"
	TaskRunning   TaskStatus = "running"
	TaskPaused    TaskStatus = "paused"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
)

type EventKind string

const (
	EventTaskStarted       EventKind = "task_started"
	EventTaskFinished      EventKind = "task_finished"
	EventToolStarted       EventKind = "tool_started"
	EventToolFinished      EventKind = "tool_finished"
	EventUsage             EventKind = "usage"
	EventApprovalRequired  EventKind = "approval_required"
	EventApprovalCancelled EventKind = "approval_cancelled"
	EventAgentStarted      EventKind = "agent_started"
	EventAgentFinished     EventKind = "agent_finished"
	EventModelStream       EventKind = "model_stream"
)

// Event is a structured runtime notification. Arguments and response payloads
// are transient; clients should not persist or log them without sanitizing.
type Event struct {
	Kind         EventKind
	TaskID       string
	SessionID    string
	Status       TaskStatus
	Tool         ToolCall
	Usage        Usage
	Approval     *ApprovalRequest
	ApprovalID   string
	Err          string
	AgentID      string
	AgentName    string
	AgentRole    string
	Provider     string
	ModelProfile string
	Model        string
	Stream       *ModelStreamEvent
}

type ModelStreamEvent struct {
	Type          string
	ItemID        string
	OutputIndex   *int
	Delta         string
	ToolCallID    string
	ToolName      string
	ToolArguments string
}

type ToolCall struct {
	ID        string
	AgentID   string
	Name      string
	Arguments json.RawMessage
	Duration  time.Duration
	Started   bool
	Failed    bool
	Canceled  bool
}

type ApprovalRequest struct {
	ID        string
	AgentID   string
	Tool      string
	Arguments json.RawMessage
}

type EventHandler func(Event)

type Usage struct {
	InputTokens         int64              `json:"input_tokens"`
	InputTokensDetails  TokenInputDetails  `json:"input_tokens_details"`
	OutputTokens        int64              `json:"output_tokens"`
	OutputTokensDetails TokenOutputDetails `json:"output_tokens_details"`
	TotalTokens         int64              `json:"total_tokens"`
}

type TokenInputDetails struct {
	CachedTokens        int64 `json:"cached_tokens"`
	CacheCreationTokens int64 `json:"cache_creation_tokens,omitempty"`
}
type TokenOutputDetails struct {
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

type UsageRecord struct {
	Request      int    `json:"request"`
	Usage        Usage  `json:"usage"`
	Available    bool   `json:"available"`
	AgentID      string `json:"agent_id,omitempty"`
	Provider     string `json:"provider,omitempty"`
	ModelProfile string `json:"model_profile,omitempty"`
	Model        string `json:"model,omitempty"`
}

type UsageTotals struct {
	Requests      int   `json:"requests"`
	Measured      int   `json:"measured"`
	Input         int64 `json:"input"`
	Output        int64 `json:"output"`
	Reasoning     int64 `json:"reasoning"`
	Total         int64 `json:"total"`
	Cached        int64 `json:"cached"`
	CacheCreation int64 `json:"cache_creation,omitempty"`
}

// SessionSettings are the defaults applied to new tasks in a session. A task
// already in progress keeps the settings recorded in its checkpoint.
type SessionSettings struct {
	ModelProfile    string `json:"model_profile"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// ModelOption is a model profile exposed to clients without leaking the
// internal model package through Core's public API.
type ModelOption struct {
	Profile  string `json:"profile"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// Session contains durable conversation state, not TUI transcript caches.
type Session struct {
	ID            string          `json:"id"`
	Workspace     string          `json:"workspace"`
	UpdatedAt     time.Time       `json:"updated_at"`
	Settings      SessionSettings `json:"settings"`
	History       json.RawMessage `json:"history,omitempty"`
	UsageRequests []UsageRecord   `json:"usage_requests,omitempty"`
	TaskUsage     UsageTotals     `json:"task_usage,omitempty"`
	SessionUsage  UsageTotals     `json:"session_usage,omitempty"`
}

type TaskInput struct{ Raw json.RawMessage }

func NewTaskInput[T any](value T) (TaskInput, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return TaskInput{}, fmt.Errorf("encode task input: %w", err)
	}
	return TaskInput{Raw: raw}, nil
}

func ParseTaskInput(line string) (TaskInput, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return TaskInput{}, errors.New("input is empty")
	}
	if json.Valid([]byte(line)) {
		var value any
		if err := json.Unmarshal([]byte(line), &value); err == nil {
			switch value.(type) {
			case string, []any:
				return TaskInput{Raw: json.RawMessage(line)}, nil
			}
		}
	}
	return NewTaskInput(line)
}

type TaskRequest struct {
	SessionID     string
	Input         TaskInput
	PlanMode      bool
	MaxToolRounds int
}

type TaskState struct {
	Status          TaskStatus
	SessionID       string
	TaskID          string
	PlanMode        bool
	ModelProfile    string
	Provider        string
	Model           string
	ReasoningEffort string
}

type Config struct {
	Workspace string
	StateDir  string
}

type Core struct {
	workspace   string
	registry    *tool.Registry
	models      *model.ProviderCatalog
	teamRuns    *teamStore
	team        *teamExecutor
	runtime     *graphResponder
	checkpoints *sqlite.Store[agentgraph.State]
	sessions    *session.Store

	mu              sync.RWMutex
	usageMu         sync.Mutex
	approvalQueueMu sync.Mutex
	active          TaskState
	handler         EventHandler
	approvals       map[string]chan bool
	closed          bool
}

func New(config Config) (_ *Core, err error) {
	workspace := config.Workspace
	if workspace == "" {
		workspace, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get workspace: %w", err)
		}
	}
	workspace, err = filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}
	stateDir := config.StateDir
	if stateDir == "" {
		stateDir = filepath.Join(workspace, ".luneagent")
	}
	stateDir, err = filepath.Abs(stateDir)
	if err != nil {
		return nil, fmt.Errorf("resolve state directory: %w", err)
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	registry, err := tool.NewDefaultRegistry(workspace)
	if err != nil {
		return nil, err
	}
	modelConfig, err := model.LoadConfig(filepath.Join(workspace, model.ConfigFileName))
	if err != nil {
		return nil, err
	}
	models, err := model.NewProviderCatalog(modelConfig)
	if err != nil {
		return nil, err
	}
	checkpoints, err := sqlite.Open[agentgraph.State](context.Background(), filepath.Join(stateDir, "checkpoints.db"), checkpoint.JSON[agentgraph.State]{})
	if err != nil {
		return nil, fmt.Errorf("open task checkpoints: %w", err)
	}
	closeCheckpoints := true
	defer func() {
		if closeCheckpoints {
			_ = checkpoints.Close()
		}
	}()
	sessions, err := session.Open(filepath.Join(stateDir, "sessions.db"))
	if err != nil {
		return nil, err
	}
	closeSessions := true
	defer func() {
		if closeSessions {
			_ = sessions.Close()
		}
	}()
	teamRuns, err := openTeamStore(filepath.Join(stateDir, "team_runs.db"))
	if err != nil {
		return nil, err
	}
	closeTeamRuns := true
	defer func() {
		if closeTeamRuns {
			_ = teamRuns.Close()
		}
	}()
	c := &Core{
		workspace:   workspace,
		registry:    registry,
		models:      models,
		teamRuns:    teamRuns,
		checkpoints: checkpoints,
		sessions:    sessions,
		active:      TaskState{Status: TaskIdle},
		approvals:   make(map[string]chan bool),
	}
	team := &teamExecutor{core: c, models: models, tools: registry, checkpoints: checkpoints, store: teamRuns}
	c.team = team
	c.runtime = &graphResponder{store: checkpoints}
	registry.SetToolEventObserver(c.onToolEvent)
	registry.SetPermissionApprovalHandler(c.requestApproval)
	if err := c.migrateSessionRecords(context.Background()); err != nil {
		return nil, err
	}
	if err := c.migrateLegacySession(context.Background()); err != nil {
		return nil, err
	}
	closeCheckpoints, closeSessions, closeTeamRuns = false, false, false
	return c, nil
}

func (c *Core) buildAgent(profileID string) (*agentgraph.Agent, *usageTrackingRequester, model.ModelProfile, error) {
	profile, ok := c.models.Profile(profileID)
	if !ok {
		return nil, nil, model.ModelProfile{}, fmt.Errorf("unknown model profile %q", profileID)
	}
	client, err := c.models.NewClient(profileID, c.registry)
	if err != nil {
		return nil, nil, model.ModelProfile{}, err
	}
	requester := &usageTrackingRequester{
		inner: client, agentID: "lead", profile: profileID,
		providerName: profile.Provider, modelName: profile.Model,
		observer: c.onAgentUsage,
	}
	agent, err := agentgraph.NewWithOptions(requester, c.registry, agentgraph.AgentOptions{
		ID: "lead", Name: "Lead", Profile: profileID, Provider: profile.Provider, Model: profile.Model,
		Team: c.team,
		StreamObserver: func(event model.ModelStreamEvent) {
			c.onModelStream("lead", "Lead", profileID, profile.Provider, profile.Model, event)
		},
	})
	if err != nil {
		return nil, nil, model.ModelProfile{}, err
	}
	return agent, requester, profile, nil
}

func (c *Core) configureRuntime(profileID, reasoningEffort string) (model.ModelProfile, error) {
	agent, requester, profile, err := c.buildAgent(profileID)
	if err != nil {
		return model.ModelProfile{}, err
	}
	c.runtime.SetRunModel(agent, requester, profile.Model, reasoningEffort)
	return profile, nil
}

func (c *Core) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	if c.active.Status == TaskRunning {
		c.mu.Unlock()
		return errors.New("cannot close core while a task is running")
	}
	c.closed = true
	for id, response := range c.approvals {
		delete(c.approvals, id)
		select {
		case response <- false:
		default:
		}
	}
	c.mu.Unlock()
	return errors.Join(c.checkpoints.Close(), c.sessions.Close(), c.teamRuns.Close())
}

func (c *Core) SetEventHandler(handler EventHandler) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.handler = handler
	c.mu.Unlock()
}

func (c *Core) State() TaskState {
	if c == nil {
		return TaskState{Status: TaskFailed}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.active
}

func (c *Core) Workspace() string {
	if c == nil {
		return ""
	}
	return c.workspace
}

// ListModelProfiles returns configured profiles in stable name order.
func (c *Core) ListModelProfiles() []ModelOption {
	if c == nil || c.models == nil {
		return nil
	}
	profiles := c.models.Profiles()
	options := make([]ModelOption, 0, len(profiles))
	for id, profile := range profiles {
		options = append(options, ModelOption{Profile: id, Provider: profile.Provider, Model: profile.Model})
	}
	sort.Slice(options, func(i, j int) bool { return options[i].Profile < options[j].Profile })
	return options
}

// SetSessionSettings replaces the defaults used by future tasks in a session.
// A paused task continues with the profile and effort saved in its checkpoint.
func (c *Core) SetSessionSettings(ctx context.Context, sessionID string, settings SessionSettings) error {
	if c == nil || ctx == nil || sessionID == "" {
		return errors.New("core, context, and session id are required")
	}
	settings, err := c.normalizeSessionSettings(settings)
	if err != nil {
		return err
	}
	// Usage writes and settings writes both replace the JSON session snapshot.
	// Serialize them so neither update can overwrite the other's fields.
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	saved, err := c.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	saved.Settings = settings
	saved.UpdatedAt = time.Now()
	return c.saveSession(ctx, saved)
}

func (c *Core) normalizeSessionSettings(settings SessionSettings) (SessionSettings, error) {
	if settings.ModelProfile == "" {
		settings.ModelProfile = c.models.DefaultModel()
	}
	if _, ok := c.models.Profile(settings.ModelProfile); !ok {
		return SessionSettings{}, fmt.Errorf("unknown model profile %q", settings.ModelProfile)
	}
	effort := strings.TrimSpace(settings.ReasoningEffort)
	if effort == "default" {
		effort = ""
	}
	switch effort {
	case "", model.ReasoningEffortNone, model.ReasoningEffortMinimal, model.ReasoningEffortLow,
		model.ReasoningEffortMedium, model.ReasoningEffortHigh, model.ReasoningEffortXHigh, model.ReasoningEffortMax:
	default:
		return SessionSettings{}, fmt.Errorf("unsupported reasoning effort %q", settings.ReasoningEffort)
	}
	settings.ReasoningEffort = effort
	return settings, nil
}

func (c *Core) PermissionMode() PermissionMode {
	if c == nil {
		return ""
	}
	return PermissionMode(c.registry.PermissionMode())
}

func (c *Core) SetPermissionMode(mode PermissionMode) error {
	if c == nil {
		return errors.New("core is nil")
	}
	return c.registry.SetPermissionMode(tool.PermissionMode(mode))
}

func (c *Core) ResolveApproval(id string, allowed bool) error {
	if c == nil {
		return errors.New("core is nil")
	}
	c.mu.Lock()
	response, ok := c.approvals[id]
	if ok {
		delete(c.approvals, id)
	}
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("approval request %q is no longer pending", id)
	}
	response <- allowed
	return nil
}

func (c *Core) RunTask(ctx context.Context, request TaskRequest) (json.RawMessage, error) {
	if len(request.Input.Raw) == 0 || !json.Valid(request.Input.Raw) {
		return nil, errors.New("task input must be valid JSON")
	}
	input, err := model.NewResponseInput(request.Input.Raw)
	if err != nil {
		return nil, err
	}
	return c.run(ctx, request.SessionID, request.PlanMode, request.MaxToolRounds, false, input)
}

func (c *Core) ResumeTask(ctx context.Context, sessionID string) (json.RawMessage, error) {
	return c.run(ctx, sessionID, false, 0, true, nil)
}

func (c *Core) run(ctx context.Context, sessionID string, planMode bool, maxRounds int, resume bool, input *model.ResponseInput) (json.RawMessage, error) {
	if c == nil || ctx == nil {
		return nil, errors.New("core and context are required")
	}
	if sessionID == "" {
		return nil, errors.New("session id is required")
	}
	if maxRounds < 0 {
		return nil, errors.New("maximum tool rounds must not be negative")
	}
	saved, err := c.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	settings := saved.Settings
	if settings.ModelProfile == "" {
		settings.ModelProfile = c.models.DefaultModel()
	}
	var pendingState agentgraph.State
	if resume {
		var ok bool
		pendingState, ok, err = c.runtime.PendingStateFor(ctx, sessionID)
		if err != nil {
			return nil, fmt.Errorf("inspect paused task settings: %w", err)
		}
		if !ok {
			return nil, errors.New("no paused task exists in this session")
		}
		if pendingState.ModelProfile != "" {
			settings.ModelProfile = pendingState.ModelProfile
		}
		settings.ReasoningEffort = pendingState.ReasoningEffort
		planMode = pendingState.PlanMode
	}
	profile, ok := c.models.Profile(settings.ModelProfile)
	if !ok {
		return nil, fmt.Errorf("session model profile %q is not configured", settings.ModelProfile)
	}
	if resume && ((pendingState.Provider != "" && pendingState.Provider != profile.Provider) ||
		(pendingState.Model != "" && pendingState.Model != profile.Model)) {
		return nil, fmt.Errorf("paused task model profile %q changed in config; it remains pinned to provider %q and model %q", settings.ModelProfile, pendingState.Provider, pendingState.Model)
	}
	settings, err = c.normalizeSessionSettings(settings)
	if err != nil {
		return nil, err
	}
	profile, _ = c.models.Profile(settings.ModelProfile)
	var history *model.ResponseInput
	if len(saved.History) > 0 && string(saved.History) != "null" {
		var decoded model.ResponseInput
		if err := json.Unmarshal(saved.History, &decoded); err != nil {
			return nil, fmt.Errorf("decode session history: %w", err)
		}
		history = &decoded
	}
	if _, err := c.beginTask(sessionID, planMode, settings, profile); err != nil {
		return nil, err
	}
	defer c.endTask()
	if _, err := c.configureRuntime(settings.ModelProfile, settings.ReasoningEffort); err != nil {
		c.finishTaskState(sessionID, TaskFailed, err)
		return nil, err
	}
	if !resume {
		if err := c.resetTaskUsage(ctx, sessionID); err != nil {
			c.finishTaskState(sessionID, TaskFailed, err)
			return nil, err
		}
	}
	c.runtime.SetSession(sessionID, history)
	c.runtime.SetPlanMode(planMode)
	c.runtime.SetMaxToolRounds(maxRounds)
	c.runtime.SetHistorySaver(func(id string, history *model.ResponseInput) error {
		encoded, err := json.Marshal(history)
		if err != nil {
			return err
		}
		return c.SetSessionHistory(context.Background(), id, encoded)
	})
	defer c.runtime.SetHistorySaver(nil)
	var response *model.Response
	if resume {
		response, err = c.runtime.Resume(ctx)
	} else {
		response, err = c.runtime.Respond(ctx, input)
	}
	if response != nil {
		_ = c.saveHistoryUsage(ctx, sessionID)
	}
	status := TaskCompleted
	if err != nil {
		status = TaskFailed
		if ctx.Err() != nil {
			status = TaskPaused
		}
	}
	if pending, pendingErr := c.HasPending(ctx, sessionID); pendingErr == nil && pending && status != TaskCompleted {
		status = TaskPaused
	}
	c.finishTaskState(sessionID, status, err)
	if status != TaskPaused && c.teamRuns != nil {
		if cleanupErr := c.teamRuns.DeleteParent(c.runtime.runID()); cleanupErr != nil {
			slog.Warn("delete completed team runs", "error", cleanupErr)
		}
	}
	if response == nil {
		return nil, err
	}
	encoded, encodeErr := json.Marshal(response)
	if encodeErr != nil {
		return nil, errors.Join(err, fmt.Errorf("encode task response: %w", encodeErr))
	}
	return encoded, err
}

func (c *Core) beginTask(sessionID string, planMode bool, settings SessionSettings, profile model.ModelProfile) (string, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return "", errors.New("core is closed")
	}
	if c.active.Status == TaskRunning {
		c.mu.Unlock()
		return "", errors.New("a task is already running")
	}
	id, err := newID()
	if err != nil {
		c.mu.Unlock()
		return "", err
	}
	c.active = TaskState{
		Status: TaskRunning, SessionID: sessionID, TaskID: id, PlanMode: planMode,
		ModelProfile: settings.ModelProfile, Provider: profile.Provider, Model: profile.Model,
		ReasoningEffort: settings.ReasoningEffort,
	}
	c.mu.Unlock()
	c.emit(Event{Kind: EventTaskStarted, TaskID: id, SessionID: sessionID, Status: TaskRunning,
		ModelProfile: settings.ModelProfile, Provider: profile.Provider, Model: profile.Model})
	return id, nil
}

func (c *Core) endTask() {
	c.mu.Lock()
	if c.active.Status == TaskRunning {
		c.active.Status = TaskIdle
	}
	c.mu.Unlock()
}

func (c *Core) setTaskStatus(status TaskStatus) {
	c.mu.Lock()
	c.active.Status = status
	c.mu.Unlock()
}

func (c *Core) finishTaskState(sessionID string, status TaskStatus, err error) {
	c.setTaskStatus(status)
	state := c.State()
	c.emit(Event{Kind: EventTaskFinished, TaskID: state.TaskID, SessionID: sessionID, Status: status, Err: errorString(err),
		ModelProfile: state.ModelProfile, Provider: state.Provider, Model: state.Model})
}

func (c *Core) emit(event Event) {
	c.mu.RLock()
	handler := c.handler
	c.mu.RUnlock()
	if handler == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Warn("core event handler panicked", "panic_type", fmt.Sprintf("%T", recovered))
		}
	}()
	handler(event)
}

func (c *Core) onToolEvent(event tool.ToolEvent) {
	kind := EventToolFinished
	if event.Started {
		kind = EventToolStarted
	}
	c.emit(Event{Kind: kind, SessionID: c.State().SessionID, TaskID: c.State().TaskID, Tool: ToolCall{
		ID: event.CallID, AgentID: event.AgentID, Name: event.Name, Arguments: event.Arguments, Started: event.Started,
		Duration: event.Duration, Failed: event.Failed, Canceled: event.Canceled,
	}})
}

func (c *Core) onModelStream(agentID, agentName, profile, provider, modelID string, event model.ModelStreamEvent) {
	stream := &ModelStreamEvent{
		Type: string(event.Type), ItemID: event.ItemID, OutputIndex: event.OutputIndex,
		Delta: event.Delta,
	}
	if event.ToolCall != nil {
		stream.ToolCallID, stream.ToolName, stream.ToolArguments = event.ToolCall.CallID, event.ToolCall.Name, event.ToolCall.Arguments
	}
	c.emit(Event{
		Kind: EventModelStream, SessionID: c.State().SessionID, TaskID: c.State().TaskID,
		AgentID: agentID, AgentName: agentName, ModelProfile: profile, Provider: provider, Model: modelID,
		Stream: stream,
	})
}

func (c *Core) onUsage(usage model.ResponseUsage) {
	c.onAgentUsage(usage, "", "", "", "")
}

func (c *Core) onAgentUsage(usage model.ResponseUsage, agentID, profile, provider, modelID string) {
	value := Usage{InputTokens: usage.InputTokens, InputTokensDetails: TokenInputDetails{
		CachedTokens: usage.InputTokensDetails.CachedTokens, CacheCreationTokens: usage.InputTokensDetails.CacheCreationTokens,
	},
		OutputTokens: usage.OutputTokens, OutputTokensDetails: TokenOutputDetails{ReasoningTokens: usage.OutputTokensDetails.ReasoningTokens}, TotalTokens: usage.TotalTokens}
	state := c.State()
	if state.SessionID != "" {
		if err := c.recordUsageForAgent(context.Background(), state.SessionID, value, agentID, profile, provider, modelID); err != nil {
			slog.Warn("persist token usage", "error", err)
		}
	}
	c.emit(Event{Kind: EventUsage, SessionID: state.SessionID, TaskID: state.TaskID, Usage: value,
		AgentID: agentID, ModelProfile: profile, Provider: provider, Model: modelID})
}

func (c *Core) requestApproval(ctx context.Context, request tool.PermissionRequest) bool {
	c.approvalQueueMu.Lock()
	defer c.approvalQueueMu.Unlock()
	if ctx.Err() != nil {
		return false
	}
	id, err := newID()
	if err != nil {
		return false
	}
	response := make(chan bool, 1)
	c.mu.Lock()
	if c.closed || c.handler == nil {
		c.mu.Unlock()
		return false
	}
	c.approvals[id] = response
	c.mu.Unlock()
	c.emit(Event{Kind: EventApprovalRequired, SessionID: c.State().SessionID, TaskID: c.State().TaskID,
		AgentID:  request.AgentID,
		Approval: &ApprovalRequest{ID: id, AgentID: request.AgentID, Tool: request.Tool, Arguments: append(json.RawMessage(nil), request.Arguments...)}})
	select {
	case allowed := <-response:
		return allowed
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.approvals, id)
		c.mu.Unlock()
		c.emit(Event{Kind: EventApprovalCancelled, SessionID: c.State().SessionID, TaskID: c.State().TaskID, ApprovalID: id})
		return false
	}
}

func (c *Core) CreateSession(ctx context.Context, history json.RawMessage) (Session, error) {
	if c == nil || ctx == nil {
		return Session{}, errors.New("core and context are required")
	}
	if len(history) == 0 {
		history = json.RawMessage("null")
	}
	if !json.Valid(history) {
		return Session{}, errors.New("session history must be valid JSON")
	}
	id, err := newID()
	if err != nil {
		return Session{}, err
	}
	saved := Session{
		ID: id, Workspace: c.workspace, UpdatedAt: time.Now(),
		Settings: SessionSettings{ModelProfile: c.models.DefaultModel()},
		History:  append(json.RawMessage(nil), history...),
	}
	if err := c.saveSession(ctx, saved); err != nil {
		return Session{}, err
	}
	return saved, nil
}

func (c *Core) LatestSession(ctx context.Context) (Session, bool, error) {
	if c == nil || ctx == nil {
		return Session{}, false, errors.New("core and context are required")
	}
	snapshot, err := c.sessions.Latest(ctx, c.workspace)
	if err != nil || snapshot == nil {
		return Session{}, false, err
	}
	saved, err := c.decodeSession(*snapshot)
	return saved, err == nil, err
}

func (c *Core) ListSessions(ctx context.Context, limit int) ([]Session, error) {
	if c == nil || ctx == nil {
		return nil, errors.New("core and context are required")
	}
	snapshots, err := c.sessions.List(ctx, c.workspace, limit)
	if err != nil {
		return nil, err
	}
	sessions := make([]Session, 0, len(snapshots))
	for _, snapshot := range snapshots {
		saved, err := c.decodeSession(snapshot)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, saved)
	}
	return sessions, nil
}

func (c *Core) GetSession(ctx context.Context, id string) (Session, error) {
	if c == nil || ctx == nil || id == "" {
		return Session{}, errors.New("core, context, and session id are required")
	}
	snapshot, err := c.sessions.Get(ctx, id, c.workspace)
	if err != nil {
		return Session{}, err
	}
	return c.decodeSession(*snapshot)
}

func (c *Core) SetSessionHistory(ctx context.Context, id string, history json.RawMessage) error {
	if c == nil || ctx == nil || id == "" {
		return errors.New("core, context, and session id are required")
	}
	if len(history) == 0 || !json.Valid(history) {
		return errors.New("session history must be valid JSON")
	}
	if _, err := model.NewResponseInput(history); err != nil {
		return err
	}
	return c.sessions.UpdateHistory(ctx, id, c.workspace, history)
}

func (c *Core) HasPending(ctx context.Context, sessionID string) (bool, error) {
	if sessionID == "" {
		return false, errors.New("session id is required")
	}
	if c == nil || ctx == nil {
		return false, errors.New("core and context are required")
	}
	return c.runtime.HasPendingFor(sessionID, ctx)
}

func (c *Core) PendingHistory(ctx context.Context, sessionID string) (json.RawMessage, error) {
	if c == nil || ctx == nil || sessionID == "" {
		return nil, errors.New("core, context, and session id are required")
	}
	history, err := c.runtime.PendingHistoryFor(ctx, sessionID)
	if err != nil || history == nil {
		return nil, err
	}
	return json.Marshal(history)
}

func (c *Core) PendingPlanMode(ctx context.Context, sessionID string) (bool, error) {
	if c == nil || ctx == nil || sessionID == "" {
		return false, errors.New("core, context, and session id are required")
	}
	return c.runtime.PendingPlanModeFor(ctx, sessionID)
}

func (c *Core) DiscardTask(ctx context.Context, sessionID string) (bool, error) {
	if c == nil || ctx == nil || sessionID == "" {
		return false, errors.New("core, context, and session id are required")
	}
	if c.State().Status == TaskRunning {
		return false, errors.New("cannot discard a task while another task is running")
	}
	err := c.checkpoints.Delete(ctx, c.runtime.checkpointID(sessionID))
	if errors.Is(err, checkpoint.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (c *Core) Compact(ctx context.Context, sessionID string) (int, error) {
	if c == nil || ctx == nil || sessionID == "" {
		return 0, errors.New("core, context, and session id are required")
	}
	saved, err := c.GetSession(ctx, sessionID)
	if err != nil {
		return 0, err
	}
	var history *model.ResponseInput
	if len(saved.History) > 0 {
		var decoded model.ResponseInput
		if err := json.Unmarshal(saved.History, &decoded); err != nil {
			return 0, err
		}
		history = &decoded
	}
	settings, err := c.normalizeSessionSettings(saved.Settings)
	if err != nil {
		return 0, err
	}
	profile, _ := c.models.Profile(settings.ModelProfile)
	if _, err := c.beginTask(sessionID, false, settings, profile); err != nil {
		return 0, err
	}
	defer c.endTask()
	if _, err := c.configureRuntime(settings.ModelProfile, settings.ReasoningEffort); err != nil {
		c.finishTaskState(sessionID, TaskFailed, err)
		return 0, err
	}
	if err := c.resetTaskUsage(ctx, sessionID); err != nil {
		c.finishTaskState(sessionID, TaskFailed, err)
		return 0, err
	}
	c.runtime.SetSession(sessionID, history)
	compacted, err := c.runtime.Compact(ctx)
	if err == nil {
		err = c.saveHistoryUsage(ctx, sessionID)
	}
	status := TaskCompleted
	if err != nil {
		status = TaskFailed
		if ctx.Err() != nil {
			status = TaskPaused
		}
	}
	c.finishTaskState(sessionID, status, err)
	if err != nil {
		return compacted, err
	}
	return compacted, nil
}

func (c *Core) resetTaskUsage(ctx context.Context, sessionID string) error {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	saved, err := c.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	saved.TaskUsage = UsageTotals{}
	saved.UpdatedAt = time.Now()
	return c.saveSession(ctx, saved)
}

func (c *Core) saveHistoryUsage(ctx context.Context, sessionID string) error {
	history := c.runtime.SessionHistory()
	if history == nil {
		return nil
	}
	encoded, err := json.Marshal(history)
	if err != nil {
		return err
	}
	return c.SetSessionHistory(ctx, sessionID, encoded)
}

func (c *Core) recordUsage(ctx context.Context, id string, usage Usage) error {
	return c.recordUsageForAgent(ctx, id, usage, "", "", "", "")
}

func (c *Core) recordUsageForAgent(ctx context.Context, id string, usage Usage, agentID, profile, provider, modelID string) error {
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	saved, err := c.GetSession(ctx, id)
	if err != nil {
		return err
	}
	available := usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.OutputTokensDetails.ReasoningTokens != 0 || usage.TotalTokens != 0 ||
		usage.InputTokensDetails.CachedTokens != 0 || usage.InputTokensDetails.CacheCreationTokens != 0
	record := UsageRecord{
		Request: saved.SessionUsage.Requests + 1, Usage: usage, Available: available,
		AgentID: agentID, ModelProfile: profile, Provider: provider, Model: modelID,
	}
	saved.UsageRequests = append(saved.UsageRequests, record)
	if len(saved.UsageRequests) > 200 {
		saved.UsageRequests = append([]UsageRecord(nil), saved.UsageRequests[len(saved.UsageRequests)-200:]...)
	}
	addUsage(&saved.SessionUsage, usage, available)
	addUsage(&saved.TaskUsage, usage, available)
	saved.UpdatedAt = time.Now()
	return c.saveSession(ctx, saved)
}

func addUsage(totals *UsageTotals, usage Usage, available bool) {
	totals.Requests++
	if !available {
		return
	}
	totals.Measured++
	totals.Input += usage.InputTokens
	totals.Output += usage.OutputTokens
	totals.Reasoning += usage.OutputTokensDetails.ReasoningTokens
	totals.Total += usage.TotalTokens
	totals.Cached += usage.InputTokensDetails.CachedTokens
	totals.CacheCreation += usage.InputTokensDetails.CacheCreationTokens
}

func (c *Core) saveSession(ctx context.Context, saved Session) error {
	if saved.UpdatedAt.IsZero() {
		saved.UpdatedAt = time.Now()
	}
	data, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	return c.sessions.Save(ctx, session.Snapshot{ID: saved.ID, Workspace: c.workspace, UpdatedAt: saved.UpdatedAt, Data: data})
}

func (c *Core) decodeSession(snapshot session.Snapshot) (Session, error) {
	var saved Session
	if err := json.Unmarshal(snapshot.Data, &saved); err != nil {
		return Session{}, fmt.Errorf("decode session %q: %w", snapshot.ID, err)
	}
	saved.ID, saved.Workspace, saved.UpdatedAt = snapshot.ID, snapshot.Workspace, snapshot.UpdatedAt
	if len(saved.History) == 0 {
		saved.History = json.RawMessage("null")
	}
	if saved.Settings.ModelProfile == "" {
		saved.Settings.ModelProfile = c.models.DefaultModel()
	}
	return saved, nil
}

func (c *Core) migrateLegacySession(ctx context.Context) error {
	latest, err := c.sessions.Latest(ctx, c.workspace)
	if err != nil || latest != nil {
		return err
	}
	if _, err := c.checkpoints.Load(ctx, legacyRunID); err != nil {
		if errors.Is(err, checkpoint.ErrNotFound) {
			return nil
		}
		return err
	}
	history, err := c.runtime.PendingHistoryFor(ctx, legacyRunID)
	if err != nil {
		return err
	}
	var encoded json.RawMessage = json.RawMessage("null")
	if history != nil {
		items := responseHistoryItems(history)
		cut := len(items)
		for index := len(items) - 1; index >= 0; index-- {
			message, ok := items[index].(model.InputMessage)
			if ok && message.Role == model.RoleUser {
				cut = index
				break
			}
		}
		prior := model.NewResponseInputItems(items[:cut]...)
		encoded, err = json.Marshal(prior)
		if err != nil {
			return err
		}
	}
	saved := Session{
		ID: legacyRunID, Workspace: c.workspace, UpdatedAt: time.Now(),
		Settings: SessionSettings{ModelProfile: c.models.DefaultModel()}, History: encoded,
	}
	return c.saveSession(ctx, saved)
}

func (c *Core) migrateSessionRecords(ctx context.Context) error {
	snapshots, err := c.sessions.All(ctx, c.workspace)
	if err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		saved, err := c.decodeSession(snapshot)
		if err != nil {
			return err
		}
		if err := c.saveSession(ctx, saved); err != nil {
			return err
		}
	}
	return nil
}

func newID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create id: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
