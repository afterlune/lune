package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
)

type tuiEntry struct {
	role       string
	text       string
	err        bool
	incomplete bool
	activityID string
}

type transcriptEntry struct {
	Role string
	Text string
}

type inputSnapshot struct {
	text        string
	attachments []imageAttachment
}

type tuiActivity struct {
	details  *publictool.ExecutionDetails
	callID   string
	summary  string
	status   string
	duration time.Duration
}

type toolActivityMsg struct{ event core.ToolCall }

type coreEventMsg struct{ event core.Event }

type runtimeEventMsg struct {
	event  core.Event
	events <-chan core.Event
}

type runtimeStreamReadyMsg struct {
	events <-chan core.Event
	err    error
}

type toolApprovalRequestMsg struct {
	request core.ApprovalRequest
}

type toolApprovalCancelledMsg struct{ requestID string }

type taskFinishedMsg struct {
	result taskResult
}

type pausedTaskDetailsMsg struct {
	state        core.TaskState
	sessionID    string
	paused       bool
	requests     []core.PendingRequest
	budgetReason core.BudgetReason
	err          error
}

type terminalColorTimeoutMsg struct{}

type turnSubmittedMsg struct {
	submission core.TurnSubmission
	err        error
	resetUsage bool
}

type taskResult struct {
	response        json.RawMessage
	coreResult      core.TaskResult
	notice          string
	targetSessionID string
	err             error
}

type tuiModel struct {
	client              CoreClient
	runtime             SessionRuntime
	runtimeSessionID    string
	runtimeCancel       context.CancelFunc
	eventCursor         int64
	runningExecutionID  string
	sessionID           string
	workspace           string
	input               textarea.Model
	attachments         []imageAttachment
	attachmentLimits    core.AttachmentLimits
	clipboardReader     clipboardImageReader
	clipboardTextReader func() (string, error)
	nextAttachmentID    int
	chat                viewport.Model
	activity            viewport.Model
	activityExpanded    map[string]bool
	activityFocus       bool
	activityFocusIndex  int

	entries                       []tuiEntry
	activities                    []tuiActivity
	teamActivities                []tuiTeamActivity
	lastTranscriptSessionID       string
	lastRenderedEntryCount        int
	unreadEntries                 int
	transcriptSelection           transcriptSelection
	lastTranscriptClickAt         time.Time
	lastTranscriptClickX          int
	lastTranscriptClickY          int
	transcriptClickCount          int
	searchOpen                    bool
	searchQuery                   string
	searchMatches                 [][]int
	searchExpandedState           map[string]bool
	commandSuggestions            []string
	suggestionIndex               int
	keymapOpen                    bool
	keymapIndex                   int
	keymapCapture                 string
	keymapDraft                   map[string]string
	undoStack                     []inputSnapshot
	redoStack                     []inputSnapshot
	permissionMenu                bool
	permissionIndex               int
	modelPicker                   bool
	modelOptions                  []core.ModelOption
	modelIndex                    int
	effortPicker                  bool
	effortIndex                   int
	sessionSettings               core.SessionSettings
	pendingApproval               *toolApprovalRequestMsg
	pendingRequest                *core.PendingRequest
	question                      *questionPanel
	questionOpen                  bool
	finishedTasks                 map[string]bool
	taskStartedAt                 time.Time
	pendingMCPInteraction         *mcpInteractionState
	mcpCommandPending             bool
	mcpAuthCancel                 context.CancelFunc
	sessionPicker                 bool
	showArchived                  bool
	deleteConfirm                 bool
	renamingSession               bool
	sessionList                   []core.Session
	sessionIndex                  int
	queued                        []core.QueuedTurnState
	detailsOpen                   bool
	planMode                      bool
	planReview                    bool
	planReviewIndex               int
	permissionMode                core.PermissionMode
	permissionModeBeforePlan      core.PermissionMode
	vimMode                       bool
	vimNormal                     bool
	vimPendingDelete              bool
	queuePicker                   bool
	queueIndex                    int
	taskUsage                     tokenUsageTotals
	sessionUsage                  tokenUsageTotals
	agentUsage                    map[string]agentUsageSummary
	agentUsageComplete            bool
	usageRequests                 []tokenUsageRecord
	contextWindow                 int64
	contextInput                  int64
	contextEstimated              bool
	modelStreamTaskID             string
	modelStreamRequestID          string
	modelStreamStartedAt          time.Time
	modelStreamFirstTokenLatency  time.Duration
	modelStreamFirstTokenRecorded bool
	modelStreamClosed             bool
	modelStreamPending            []core.Event
	modelStreamTick               bool
	modelStreamToken              uint64
	modelStreamItems              map[string]int
	modelStreamTagFilters         map[string]*proposedPlanTagStreamFilter
	modelStreamTagFilterOrder     []string
	modelStreamRawPending         map[string]string
	modelStreamRawEntries         map[int]string
	modelStreamCurrent            []int
	modelStreamLast               []int
	activeExecutions              map[string]bool
	pendingRequests               []core.PendingRequest
	proposedPlan                  *core.ProposedPlan
	planSourceExecutionID         string
	planAuthorization             core.PlanAuthorization
	exitConfirm                   bool
	exitAfterTask                 bool
	runtimeTask                   bool
	width                         int
	height                        int
	pending                       bool
	running                       bool
	windowTitle                   string
	windowTitleFrame              int
	windowTitleAnimationToken     uint64
	windowTitleRevision           uint64
	cancel                        context.CancelFunc
	cancelled                     bool
	status                        string
	preferences                   uiPreferences
	preferencesPath               string
	terminalBackground            color.Color
	terminalForeground            color.Color
	terminalBackgroundReceived    bool
	terminalForegroundReceived    bool
	terminalColorQueryPending     bool
	colorProfile                  colorprofile.Profile
}

type modelUsageMsg struct {
	usage                core.Usage
	agentID              string
	profile              string
	provider             string
	model                string
	contextWindow        int64
	estimatedInputTokens int64
}

type tokenUsageRecord struct {
	request              int
	usage                core.Usage
	available            bool
	agentID              string
	profile              string
	provider             string
	model                string
	contextWindow        int64
	estimatedInputTokens int64
}

type tokenUsageTotals struct {
	requests       int
	measured       int
	input          int64
	estimatedInput int64
	output         int64
	reasoning      int64
	total          int64
	cached         int64
}

const defaultInputPlaceholder = "Ask Lune to do anything"

func Run(client *core.Core) error {
	app := coreApplication{Core: client}
	workspace := app.Workspace()
	ctx := context.Background()
	saved, found, err := app.LatestSession(ctx)
	if err != nil {
		return fmt.Errorf("load latest session: %w", err)
	}
	if !found {
		saved, err = app.CreateSession(ctx, json.RawMessage("null"))
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
	}
	runtime, err := app.OpenSessionRuntime(ctx, saved.ID)
	if err != nil {
		return fmt.Errorf("open session runtime: %w", err)
	}
	state, err := runtime.State(ctx)
	if err != nil {
		return fmt.Errorf("inspect session runtime: %w", err)
	}
	ui := newTUIModel(app, state.Task.Status == core.TaskPaused)
	preferences, preferencesPath, preferencesErr := loadUserPreferences()
	ui.preferences = preferences
	ui.preferencesPath = preferencesPath
	applyTextareaTheme(&ui.input, ui.resolvedTheme())
	ui.applyMCPInteractionTheme(ui.resolvedTheme())
	ui.applyKeymap()
	if preferencesErr != nil {
		ui.status = "Some UI preferences were invalid; defaults were used where needed."
	}
	ui.sessionID = saved.ID
	ui.workspace = workspace
	ui.runtime = runtime
	ui.runtimeSessionID = saved.ID
	ui.pending = state.Task.Status == core.TaskPaused
	ui.running = state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued
	ui.runningExecutionID = state.Task.ExecutionID
	ui.activeExecutions = make(map[string]bool)
	if state.Task.ExecutionID != "" {
		ui.activeExecutions[state.Task.ExecutionID] = true
	}
	for _, queued := range state.Queue {
		if queued.ExecutionID != "" {
			ui.activeExecutions[queued.ExecutionID] = true
		}
	}
	ui.restoreSessionState(saved)
	ui.updateLayout()
	ui.refreshPanels()
	program := tea.NewProgram(ui, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	ui.startRuntimeEvents()
	runtime.SetUserInteractionAvailable(true)
	defer func() {
		if ui.mcpAuthCancel != nil {
			ui.mcpAuthCancel()
		}
		if ui.pendingMCPInteraction != nil {
			_ = runtime.ResolveUserInteraction(ui.pendingMCPInteraction.request.ID, json.RawMessage(`{"action":"cancel"}`))
		}
		if ui.runtimeCancel != nil {
			ui.runtimeCancel()
		}
		runtime.SetUserInteractionAvailable(false)
	}()
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}
	return nil
}

func newTUIModel(client CoreClient, pending bool) *tuiModel {
	input := textarea.New()
	input.Placeholder = defaultInputPlaceholder
	input.Prompt = "› "
	input.ShowLineNumbers = false
	input.DynamicHeight = true
	input.MinHeight = 1
	input.MaxHeight = 6
	input.MaxContentHeight = 10000
	input.SetVirtualCursor(false)
	input.Focus()
	applyTextareaTheme(&input, themeDark)

	model := &tuiModel{
		client:              client,
		input:               input,
		attachmentLimits:    defaultAttachmentLimits(),
		clipboardReader:     systemClipboardImageReader{},
		clipboardTextReader: readClipboardText,
		nextAttachmentID:    1,
		chat:                viewport.New(viewport.WithWidth(1), viewport.WithHeight(1)),
		activity:            viewport.New(viewport.WithWidth(1), viewport.WithHeight(1)),
		activityExpanded:    make(map[string]bool),
		width:               100,
		height:              30,
		pending:             pending,
		status:              "Ready",
		preferences:         defaultUIPreferences(),
		agentUsage:          make(map[string]agentUsageSummary), agentUsageComplete: true,
		activeExecutions: make(map[string]bool),
	}
	if client != nil {
		model.attachmentLimits = normalizeAttachmentLimits(client.ProjectAttachmentLimits())
		model.permissionMode = client.PermissionMode()
	}
	model.chat.SoftWrap = true
	model.activity.SoftWrap = true
	model.applyKeymap()
	model.updateLayout()
	model.refreshPanels()
	return model
}

func (m *tuiModel) Init() tea.Cmd {
	commands := []tea.Cmd{textarea.Blink, m.subscribeRuntime()}
	if m.preferences.Theme == themeAuto {
		commands = append(commands, m.requestTerminalColors())
	}
	if m.running && m.runningExecutionID != "" {
		commands = append(commands, waitForTurn(m.runtime, m.runningExecutionID), taskClock(m.sessionID))
	}
	if m.running {
		commands = append(commands, m.startWindowTitleAnimation())
	}
	if m.pending {
		if command := m.inspectPausedTask(""); command != nil {
			commands = append(commands, command)
		}
	}
	return tea.Batch(commands...)
}
