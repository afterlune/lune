package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/afterlune/luneCore"
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

type flushScrollbackMsg struct{}
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

	entries                       []tuiEntry
	printedEntryCount             int
	scrollbackRedrawPending       bool
	activities                    []tuiActivity
	teamActivities                []tuiTeamActivity
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
	taskPlanMode                  bool
	permissionMode                core.PermissionMode
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
	modelStreamStartedAt          time.Time
	modelStreamFirstTokenLatency  time.Duration
	modelStreamFirstTokenRecorded bool
	modelStreamClosed             bool
	modelStreamPending            []core.Event
	modelStreamTick               bool
	modelStreamToken              uint64
	modelStreamItems              map[string]int
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
	terminalColorsReady           bool
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

const (
	sessionRailMinWidth   = 24
	sessionRailMaxWidth   = 30
	sessionRailHeaderRows = 2
	sessionRailMaxItems   = 6
	maxCommandSuggestions = 8
)

type tuiCommandSpec struct {
	name        string
	usage       string
	description string
}

var tuiCommands = []tuiCommandSpec{
	{name: "/autocompact", usage: "/autocompact [on|off|threshold|reset]", description: "Configure automatic context compaction for future tasks."},
	{name: "/compact", usage: "/compact", description: "Compact older conversation history."},
	{name: "/context", usage: "/context [window <tokens>|reserve <tokens>|reset]", description: "View or set session context values."},
	{name: "/continue", usage: "/continue", description: "Continue the paused task in this session."},
	{name: "/discard", usage: "/discard", description: "Discard the paused task in this session."},
	{name: "/effort", usage: "/effort [value]", description: "Choose reasoning effort."},
	{name: "/exit", usage: "/exit", description: "Exit the chat."},
	{name: "/details", usage: "/details [n]", description: "Show a long message or activity entry in terminal history."},
	{name: "/fork", usage: "/fork", description: "Start a new session from completed history."},
	{name: "/goal", usage: "/goal [text|clear]", description: "Show, set, or clear the goal for future tasks."},
	{name: "/help", usage: "/help", description: "Show available commands."},
	{name: "/keymap", usage: "/keymap [reset]", description: "Edit key bindings or restore defaults."},
	{name: "/model", usage: "/model [profile]", description: "Choose a model profile."},
	{name: "/mcp", usage: "/mcp <list|enable|disable|auth> [name]", description: "Manage configured Model Context Protocol servers."},
	{name: "/new", usage: "/new", description: "Start a new session."},
	{name: "/permissions", usage: "/permissions [mode]", description: "Show or set the permission mode."},
	{name: "/plan", usage: "/plan", description: "Switch Core to read-only plan mode or review a plan."},
	{name: "/queue", usage: "/queue", description: "Review or cancel turns in the Core queue."},
	{name: "/resume", usage: "/resume", description: "Choose a session to resume."},
	{name: "/rewind", usage: "/rewind <n>", description: "Remove the latest n user turns."},
	{name: "/theme", usage: "/theme [auto|dark|light]", description: "Follow terminal colors or choose a fixed theme."},
	{name: "/usage", usage: "/usage", description: "Show token usage, cache hits, and activity."},
	{name: "/vim", usage: "/vim", description: "Toggle Vim input mode."},
}

func tuiCommandNames() []string {
	names := make([]string, 0, len(tuiCommands))
	for _, command := range tuiCommands {
		names = append(names, command.name)
	}
	return names
}

func tuiHelpText() string {
	lines := []string{"Available commands:"}
	for _, command := range tuiCommands {
		lines = append(lines, fmt.Sprintf("  %-23s %s", command.usage, command.description))
	}
	lines = append(lines, "Use /keymap to view or change keyboard shortcuts.")
	return strings.Join(lines, "\n")
}

var reasoningEffortOptions = []string{"", "none", "minimal", "low", "medium", "high", "xhigh", "max"}

type tuiPermissionOption struct {
	mode        core.PermissionMode
	label       string
	description string
}

var tuiPermissionOptions = []tuiPermissionOption{
	{mode: core.PermissionReadOnly, label: "Read-only", description: "Read workspace files; ask before web; block writes and shell"},
	{mode: core.PermissionWorkspace, label: "Workspace", description: "Workspace file operations and sandboxed commands; network needs plan approval"},
	{mode: core.PermissionAsk, label: "Ask", description: "Read workspace files; approve other tool operations in the plan"},
	{mode: core.PermissionFullAccess, label: "Full access", description: "Current-user files and network stay in the OS sandbox; high-risk commands need plan approval"},
}

func UseTUI(stdin, stdout *os.File) bool {
	return isTerminal(stdin) && isTerminal(stdout)
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

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
	ui.refreshSessionRail(saved.ID)
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
	input.MinHeight = 2
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
		width:               100,
		height:              30,
		pending:             pending,
		status:              "Ready",
		preferences:         defaultUIPreferences(),
		terminalColorsReady: true,
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
	commands = append(commands, m.flushScrollback())
	if m.running && m.runningExecutionID != "" {
		commands = append(commands, waitForTurn(m.runtime, m.runningExecutionID))
	}
	return tea.Batch(commands...)
}

func (m *tuiModel) startRuntimeEvents() {
	if m.runtime != nil {
		m.runtime.SetUserInteractionAvailable(true)
	}
}

func (m *tuiModel) subscribeRuntime() tea.Cmd {
	if m.runtime == nil || m.runtimeSessionID != m.sessionID {
		return nil
	}
	if m.runtimeCancel != nil {
		m.runtimeCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.runtimeCancel = cancel
	runtime := m.runtime
	cursor := m.eventCursor
	return func() tea.Msg {
		events, err := runtime.SubscribeEvents(ctx, cursor)
		return runtimeStreamReadyMsg{events: events, err: err}
	}
}

func nextRuntimeEvent(events <-chan core.Event) tea.Cmd {
	if events == nil {
		return nil
	}
	return func() tea.Msg {
		event, ok := <-events
		if !ok {
			return runtimeEventMsg{events: nil}
		}
		return runtimeEventMsg{event: event, events: events}
	}
}

func waitForTurn(runtime SessionRuntime, executionID string) tea.Cmd {
	if runtime == nil || executionID == "" {
		return nil
	}
	return func() tea.Msg {
		result, err := runtime.WaitTurn(context.Background(), executionID)
		return taskFinishedMsg{result: taskResult{response: result.Response, coreResult: result, err: err}}
	}
}

func (m *tuiModel) submitTurn(input core.TaskInput, mode core.SubmissionMode, resetUsage bool) (tea.Model, tea.Cmd) {
	if m.runtime == nil || m.runtimeSessionID != m.sessionID {
		if err := m.bindSessionRuntime(context.Background(), m.sessionID); err != nil {
			m.status = "Could not open the session runtime."
			m.addEntry("error", err.Error(), true)
			return m, nil
		}
	}
	if mode == core.StartIfIdle {
		m.resetModelStream()
		m.modelStreamFirstTokenLatency = 0
		m.modelStreamFirstTokenRecorded = false
		if resetUsage {
			m.taskUsage = tokenUsageTotals{}
		}
		m.running = true
		m.runtimeTask = true
		m.cancelled = false
		m.status = "Working… Press Esc to interrupt."
	} else {
		m.status = "Turn added to the Core queue."
	}
	runtime := m.runtime
	request := core.TurnRequest{Input: input}
	return m, func() tea.Msg {
		submission, err := runtime.SubmitTurn(context.Background(), request, mode)
		return turnSubmittedMsg{submission: submission, err: err, resetUsage: resetUsage}
	}
}

func (m *tuiModel) bindSessionRuntime(ctx context.Context, sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("session id is required")
	}
	if m.runtimeCancel != nil {
		m.runtimeCancel()
		m.runtimeCancel = nil
	}
	if m.runtime != nil {
		m.runtime.SetUserInteractionAvailable(false)
	}
	runtime, err := m.client.OpenSessionRuntime(ctx, sessionID)
	if err != nil {
		return err
	}
	m.runtime = runtime
	m.runtimeSessionID = sessionID
	m.sessionID = sessionID
	m.eventCursor = 0
	m.runtime.SetUserInteractionAvailable(true)
	m.activeExecutions = make(map[string]bool)
	state, err := runtime.State(ctx)
	if err != nil {
		return err
	}
	m.queued = append(m.queued[:0], state.Queue...)
	m.running = state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued
	m.pending = state.Task.Status == core.TaskPaused
	m.runningExecutionID = ""
	if state.Task.ExecutionID != "" {
		m.runningExecutionID = state.Task.ExecutionID
		if m.running {
			m.activeExecutions[state.Task.ExecutionID] = true
		}
	}
	for _, queued := range state.Queue {
		if queued.ExecutionID != "" {
			m.activeExecutions[queued.ExecutionID] = true
		}
	}
	return nil
}

func (m *tuiModel) refreshRuntimeProjection() error {
	if m.runtime == nil || m.runtimeSessionID != m.sessionID {
		if err := m.bindSessionRuntime(context.Background(), m.sessionID); err != nil {
			return err
		}
	}
	state, err := m.runtime.State(context.Background())
	if err != nil {
		return err
	}
	m.queued = append(m.queued[:0], state.Queue...)
	m.pending = state.Task.Status == core.TaskPaused
	m.running = state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued
	if m.running && state.Task.ExecutionID != "" {
		m.runningExecutionID = state.Task.ExecutionID
		m.activeExecutions[state.Task.ExecutionID] = true
	}
	return nil
}

func (m *tuiModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := message.(flushScrollbackMsg); ok {
		m.scrollbackRedrawPending = false
	}
	transientBeforeUpdate := m.hasTransientPanel()
	terminalColorsWereReady := m.terminalColorsReady
	model, command := m.update(message)
	terminalColorsResolved := !terminalColorsWereReady && m.terminalColorsReady
	if (transientBeforeUpdate && !m.hasTransientPanel() || terminalColorsResolved) && !m.scrollbackRedrawPending {
		// Let Bubble Tea render the dismissed panel before inserting scrollback above the view.
		m.scrollbackRedrawPending = true
		redraw := tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg { return flushScrollbackMsg{} })
		return model, tea.Batch(command, redraw)
	}
	scrollback := m.flushScrollback()
	if scrollback == nil {
		return model, command
	}
	if command == nil {
		return model, scrollback
	}
	return model, tea.Sequence(scrollback, command)
}

func (m *tuiModel) update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case flushScrollbackMsg:
		return m, nil
	case terminalColorTimeoutMsg:
		if m.terminalColorQueryPending {
			m.terminalColorQueryPending = false
			m.terminalColorsReady = true
			if m.preferences.Theme == themeAuto {
				m.applyResolvedTheme()
			}
		}
		return m, nil
	case tea.BackgroundColorMsg:
		m.terminalBackground = message.Color
		if m.preferences.Theme == themeAuto {
			m.terminalBackgroundReceived = true
			m.finishTerminalColorQueryIfReady()
			m.applyResolvedTheme()
		}
		return m, nil
	case tea.ForegroundColorMsg:
		m.terminalForeground = message.Color
		if m.preferences.Theme == themeAuto {
			m.terminalForegroundReceived = true
			m.finishTerminalColorQueryIfReady()
			m.applyResolvedTheme()
		}
		return m, nil
	case tea.ColorProfileMsg:
		m.colorProfile = message.Profile
		m.updateLayout()
		m.refreshPanels()
		return m, nil
	case tea.WindowSizeMsg:
		m.width, m.height = message.Width, message.Height
		m.updateLayout()
		m.refreshPanels()
		return m, nil
	case coreEventMsg:
		return m, m.handleRuntimeEvent(message.event)
	case runtimeStreamReadyMsg:
		if message.err != nil {
			m.status = "Could not subscribe to session events: " + message.err.Error()
			return m, nil
		}
		return m, nextRuntimeEvent(message.events)
	case runtimeEventMsg:
		if message.events == nil {
			if m.runtimeCancel != nil {
				m.status = "Session event stream ended."
			}
			return m, nil
		}
		if message.event.Cursor > 0 {
			if message.event.Cursor <= m.eventCursor {
				return m, nextRuntimeEvent(message.events)
			}
			m.eventCursor = message.event.Cursor
		}
		if message.event.ExecutionID != "" && !m.activeExecutions[message.event.ExecutionID] {
			return m, nextRuntimeEvent(message.events)
		}
		return m, tea.Batch(m.handleRuntimeEvent(message.event), nextRuntimeEvent(message.events))
	case mcpCommandResultMsg:
		m.finishMCPCommand(message)
		m.refreshPanels()
		return m, nil
	case mcpOpenURLResultMsg:
		return m.finishMCPURLOpen(message)
	case modelStreamFlushMsg:
		if message.token != m.modelStreamToken || !m.modelStreamTick {
			return m, nil
		}
		if m.flushModelStreamEvents() {
			m.refreshPanels()
		}
		return m, nil
	case toolActivityMsg:
		m.updateToolActivity(message.event)
		m.refreshPanels()
		return m, nil
	case toolApprovalRequestMsg:
		m.pendingApproval = &message
		m.status = "Permission required: " + m.keyLabel("approve_tool") + " approve, " + m.keyLabel("deny_tool") + " deny."
		m.addEntry("permission", formatToolApprovalWithKeys(message.request, m.workspace, m.keyLabel("approve_tool"), m.keyLabel("deny_tool")), true)
		return m, nil
	case toolApprovalCancelledMsg:
		if m.pendingApproval != nil && m.pendingApproval.request.ID == message.requestID {
			m.pendingApproval = nil
			m.status = "Tool approval cancelled."
			m.refreshPanels()
		}
		return m, nil
	case modelUsageMsg:
		m.updateModelUsage(message.usage, message.agentID, message.profile, message.provider, message.model,
			message.contextWindow, message.estimatedInputTokens)
		m.refreshPanels()
		return m, nil
	case turnSubmittedMsg:
		if message.err != nil {
			m.running = false
			m.runtimeTask = false
			m.addEntry("error", "Could not submit turn: "+message.err.Error(), true)
			m.status = "Turn was not accepted."
			return m, nil
		}
		if message.submission.ExecutionID != "" {
			m.activeExecutions[message.submission.ExecutionID] = true
		}
		if message.submission.Status == core.SubmissionQueued {
			m.status = fmt.Sprintf("Turn queued at position %d.", message.submission.Position)
			if err := m.refreshRuntimeProjection(); err != nil {
				m.addEntry("error", err.Error(), true)
			}
			m.refreshPanels()
			return m, nil
		}
		m.running = true
		m.runtimeTask = true
		m.runningExecutionID = message.submission.ExecutionID
		return m, waitForTurn(m.runtime, message.submission.ExecutionID)
	case taskFinishedMsg:
		m.runtimeTask = false
		m.runningExecutionID = ""
		m.finishTask(message.result)
		m.refreshSessionRail(m.sessionID)
		m.refreshPanels()
		if err := m.refreshRuntimeProjection(); err != nil {
			m.addEntry("error", "Could not refresh task state: "+err.Error(), true)
		}
		if m.running && m.runningExecutionID != "" {
			return m, waitForTurn(m.runtime, m.runningExecutionID)
		}
		if m.exitAfterTask {
			return m, tea.Quit
		}
		return m, nil
	case imageInputReadyMsg:
		m.applyImageInputResult(message)
		m.updateCommandSuggestions()
		m.updateLayout()
		m.refreshPanels()
		return m, nil
	case tea.PasteMsg:
		if m.pendingMCPInteraction != nil {
			return m, m.updateMCPInteractionInput(message)
		}
		if m.canAttachImages() {
			if paths, ok := parsePastedImagePaths(message.Content, m.workspace); ok {
				return m, m.loadPastedImagePaths(paths, message.Content)
			}
		}
		command := m.updateInput(message)
		m.updateCommandSuggestions()
		m.updateLayout()
		return m, command
	case tea.MouseWheelMsg:
		if m.pendingMCPInteraction != nil {
			return m, nil
		}
		if m.detailsOpen {
			m.activity, _ = m.activity.Update(message)
		} else {
			m.chat, _ = m.chat.Update(message)
		}
		return m, nil
	case tea.MouseClickMsg:
		if m.pendingMCPInteraction != nil {
			return m, nil
		}
		if updated, command, handled := m.handleSessionRailClick(message); handled {
			return updated, command
		}
		if updated, command, handled := m.handleNarrowSessionSwitcherClick(message); handled {
			return updated, command
		}
		return m, nil
	case tea.KeyPressMsg:
		if m.exitConfirm {
			switch message.String() {
			case "esc":
				m.exitConfirm = false
				m.status = "Exit cancelled."
				return m, nil
			case "i":
				return m.confirmExit(true)
			case "w":
				return m.confirmExit(false)
			default:
				return m, nil
			}
		}
		if m.pendingMCPInteraction != nil {
			return m.handleMCPInteractionKey(message)
		}
		if m.mcpAuthCancel != nil && message.String() == "esc" {
			m.mcpAuthCancel()
			m.status = "Cancelling MCP authorization…"
			return m, nil
		}
		if m.keymapOpen {
			return m.handleKeymapKey(message)
		}
		if m.detailsOpen {
			switch {
			case m.matchesKey("interrupt_or_copy", message):
				if m.input.HasSelection() {
					if err := writeClipboard(m.input.SelectedText()); err != nil {
						m.status = "Selection could not be copied."
					} else {
						m.status = "Selection copied."
					}
					return m, nil
				}
				if m.running {
					m.detailsOpen = false
					m.interrupt()
					return m, nil
				}
				return m, tea.Quit
			case message.String() == "esc", m.matchesKey("toggle_activity", message):
				m.detailsOpen = false
				m.refreshPanels()
				m.updateLayout()
				return m, nil
			case m.matchesKey("chat_scroll_up", message), m.matchesKey("activity_scroll_up", message), m.matchesKey("details_scroll_up", message):
				m.activity.HalfPageUp()
				return m, nil
			case m.matchesKey("chat_scroll_down", message), m.matchesKey("activity_scroll_down", message), m.matchesKey("details_scroll_down", message):
				m.activity.HalfPageDown()
				return m, nil
			default:
				return m, nil
			}
		}
		if m.matchesKey("jump_chat_bottom", message) && !m.sessionPicker && !m.modelPicker &&
			!m.effortPicker && !m.queuePicker && !m.permissionMenu && m.pendingApproval == nil {
			m.chat.GotoBottom()
			return m, nil
		}
		if !(m.running && message.String() == "esc") && !m.sessionPicker && !m.modelPicker && !m.effortPicker && !m.queuePicker && !m.permissionMenu {
			if handled, command := m.handleVimKey(message); handled {
				m.updateLayout()
				return m, command
			}
		}
		if m.matchesKey("send", message) {
			if m.queuePicker {
				return m.editQueuedInput()
			}
			if m.running && strings.TrimSpace(m.input.Value()) == "/queue" {
				return m.openQueuePicker()
			}
			if m.running && (m.pendingApproval == nil || strings.TrimSpace(m.input.Value()) != "") {
				return m.enqueueInput()
			}
			if m.sessionPicker {
				if m.deleteConfirm {
					return m, nil
				}
				return m.selectSession()
			}
			if m.permissionMenu {
				return m.selectPermissionMode()
			}
			if m.modelPicker {
				return m.selectModelProfile()
			}
			if m.effortPicker {
				return m.selectReasoningEffort()
			}
			if m.pendingApproval != nil {
				return m, nil
			}
			if m.planReview && strings.TrimSpace(m.input.Value()) == "" {
				return m.acceptPlan()
			}
			if len(m.commandSuggestions) > 0 {
				index := min(m.suggestionIndex, len(m.commandSuggestions)-1)
				m.setInputValue(m.commandSuggestions[index])
			}
			return m.submit()
		}
		if message.String() == "esc" {
			if m.queuePicker {
				m.queuePicker = false
				m.status = "Queue selection closed."
				m.updateLayout()
				return m, nil
			}
			if m.planReview {
				m.planReview = false
				m.planMode = false
				m.clearQueue()
				m.status = "Plan cancelled."
				m.updateLayout()
				return m, nil
			}
			if m.sessionPicker {
				if m.renamingSession {
					m.renamingSession = false
					m.input.Placeholder = defaultInputPlaceholder
					m.status = "Session rename cancelled."
					m.resetInput()
					m.updateLayout()
					return m, nil
				}
				if m.deleteConfirm {
					m.deleteConfirm = false
					m.status = "Session deletion cancelled."
					m.updateLayout()
					return m, nil
				}
				m.sessionPicker = false
				m.status = "Session selection cancelled."
				m.updateLayout()
				return m, nil
			}
			if m.permissionMenu {
				m.permissionMenu = false
				m.status = "Permission selection cancelled."
				m.updateLayout()
				return m, nil
			}
			if m.modelPicker {
				m.modelPicker = false
				m.status = "Model selection cancelled."
				m.updateLayout()
				return m, nil
			}
			if m.effortPicker {
				m.effortPicker = false
				m.status = "Effort selection cancelled."
				m.updateLayout()
				return m, nil
			}
			m.interrupt()
			return m, nil
		}
		if m.matchesKey("interrupt_or_copy", message) {
			if m.input.HasSelection() && (!m.sessionPicker || m.renamingSession) {
				if err := writeClipboard(m.input.SelectedText()); err != nil {
					m.status = "Selection could not be copied."
				} else {
					m.status = "Selection copied."
				}
				return m, nil
			}
			if m.running {
				m.interrupt()
				return m, nil
			}
			return m, tea.Quit
		}
		if m.matchesKey("toggle_activity", message) {
			m.detailsOpen = true
			m.refreshPanels()
			m.updateLayout()
			return m, nil
		}
		if m.matchesKey("cycle_permissions", message) {
			if !m.running && !m.permissionMenu && !m.sessionPicker && !m.queuePicker && !m.modelPicker && !m.effortPicker && !m.detailsOpen {
				return m.cyclePermissionMode()
			}
			return m, nil
		}
		if m.matchesKey("interrupt_send", message) {
			if m.running {
				if strings.TrimSpace(m.input.Value()) != "" {
					return m.enqueueInput()
				}
				m.interrupt()
				return m, nil
			}
			return m.submit()
		}
		if m.matchesKey("chat_scroll_up", message) {
			m.chat.HalfPageUp()
			return m, nil
		}
		if m.matchesKey("chat_scroll_down", message) {
			m.chat.HalfPageDown()
			return m, nil
		}
		if m.matchesKey("activity_scroll_up", message) {
			if m.modernConversation() && !m.wideConversation() {
				m.chat.HalfPageUp()
			} else {
				m.activity.HalfPageUp()
			}
			return m, nil
		}
		if m.matchesKey("activity_scroll_down", message) {
			if m.modernConversation() && !m.wideConversation() {
				m.chat.HalfPageDown()
			} else {
				m.activity.HalfPageDown()
			}
			return m, nil
		}
		if m.permissionMenu {
			switch {
			case m.matchesKey("picker_previous", message):
				if m.permissionIndex > 0 {
					m.permissionIndex--
				}
			case m.matchesKey("picker_next", message):
				if m.permissionIndex+1 < len(tuiPermissionOptions) {
					m.permissionIndex++
				}
			}
			return m, nil
		}
		if m.sessionPicker {
			if m.renamingSession {
				if handled, command := m.handleInputEdit(message); handled {
					m.updateLayout()
					return m, command
				}
				command := m.updateInput(message)
				m.updateLayout()
				return m, command
			}
			if m.deleteConfirm {
				switch {
				case m.matchesKey("session_confirm_delete", message):
					return m.deleteSelectedSession()
				case m.matchesKey("session_cancel_delete", message):
					m.deleteConfirm = false
					m.status = "Session deletion cancelled."
					m.updateLayout()
				}
				return m, nil
			}
			switch {
			case m.matchesKey("picker_previous", message):
				if m.sessionIndex > 0 {
					m.sessionIndex--
				}
			case m.matchesKey("picker_next", message):
				if m.sessionIndex+1 < len(m.sessionList) {
					m.sessionIndex++
				}
			case m.matchesKey("session_toggle_archived", message):
				m.showArchived = !m.showArchived
				if err := m.reloadSessionList(m.sessionID); err != nil {
					m.showArchived = !m.showArchived
					m.status = "Could not update the session list."
					m.addEntry("error", err.Error(), true)
					return m, nil
				}
				if m.showArchived {
					m.status = "Showing archived sessions; " + m.keyLabel("session_toggle_archived") + " hides them."
				} else {
					m.status = "Showing active sessions; " + m.keyLabel("session_toggle_archived") + " shows archived sessions."
				}
			case m.matchesKey("session_archive", message):
				return m.toggleSelectedArchive()
			case m.matchesKey("session_rename", message):
				return m.beginSessionRename()
			case m.matchesKey("session_delete", message):
				if m.sessionIndex >= 0 && m.sessionIndex < len(m.sessionList) {
					m.deleteConfirm = true
					m.status = "Delete this session and all saved task state? Press " + m.keyLabel("session_confirm_delete") + " to confirm or " + m.keyLabel("session_cancel_delete") + "/Esc to cancel."
				}
			}
			m.updateLayout()
			return m, nil
		}
		if m.queuePicker {
			switch {
			case m.matchesKey("picker_previous", message):
				if m.queueIndex > 0 {
					m.queueIndex--
				}
			case m.matchesKey("picker_next", message):
				if m.queueIndex+1 < len(m.queued) {
					m.queueIndex++
				}
			case m.matchesKey("queue_remove", message):
				m.deleteQueuedInput()
			}
			m.updateLayout()
			return m, nil
		}
		if m.modelPicker {
			switch {
			case m.matchesKey("picker_previous", message):
				if m.modelIndex > 0 {
					m.modelIndex--
				}
			case m.matchesKey("picker_next", message):
				if m.modelIndex+1 < len(m.modelOptions) {
					m.modelIndex++
				}
			}
			m.updateLayout()
			return m, nil
		}
		if m.effortPicker {
			switch {
			case m.matchesKey("picker_previous", message):
				if m.effortIndex > 0 {
					m.effortIndex--
				}
			case m.matchesKey("picker_next", message):
				if m.effortIndex+1 < len(reasoningEffortOptions) {
					m.effortIndex++
				}
			}
			m.updateLayout()
			return m, nil
		}
		if m.pendingApproval != nil && m.running {
			switch {
			case m.matchesKey("approve_tool", message):
				m.resolveToolApproval(true)
			case m.matchesKey("deny_tool", message):
				m.resolveToolApproval(false)
			}
			return m, nil
		}
		if m.matchesKey("input_paste", message) && m.canAttachImages() {
			return m, m.readClipboardInput()
		}
		if len(m.commandSuggestions) > 0 {
			switch {
			case m.matchesKey("suggestion_previous", message):
				if m.suggestionIndex > 0 {
					m.suggestionIndex--
				}
				return m, nil
			case m.matchesKey("suggestion_next", message):
				if m.suggestionIndex+1 < len(m.commandSuggestions) {
					m.suggestionIndex++
				}
				return m, nil
			case m.matchesKey("suggestion_complete", message):
				m.setInputValue(m.commandSuggestions[m.suggestionIndex])
				m.updateCommandSuggestions()
				m.updateLayout()
				return m, nil
			}
		}
		if handled, command := m.handleInputEdit(message); handled {
			m.updateCommandSuggestions()
			m.updateLayout()
			return m, command
		}
		command := m.updateInput(message)
		m.updateCommandSuggestions()
		m.updateLayout()
		return m, command
	default:
		command := m.updateInput(message)
		m.updateCommandSuggestions()
		m.updateLayout()
		return m, command
	}
}

func (m *tuiModel) handleRuntimeEvent(event core.Event) tea.Cmd {
	if event.SessionID != "" && event.SessionID != m.sessionID {
		return nil
	}
	var command tea.Cmd
	switch event.Kind {
	case core.EventTaskStarted:
		if event.ExecutionID == m.runningExecutionID {
			m.startModelStream(event)
			m.contextWindow = event.ContextWindow
			m.contextInput = 0
			m.contextEstimated = false
		}
	case core.EventTaskQueued:
		m.status = "Turn queued."
	case core.EventTaskFinished:
		m.finishModelStreamEvent(event)
		if event.ExecutionID != "" {
			delete(m.activeExecutions, event.ExecutionID)
		}
	case core.EventModelStream:
		return m.queueModelStreamEvent(event)
	case core.EventToolStarted, core.EventToolFinished:
		m.updateToolActivity(event.Tool)
	case core.EventAgentStarted, core.EventAgentFinished:
		m.updateTeamActivity(event)
	case core.EventApprovalRequired:
		if event.Approval != nil {
			request := toolApprovalRequestMsg{request: *event.Approval}
			m.pendingApproval = &request
			m.status = "Permission required: " + m.keyLabel("approve_tool") + " approve, " + m.keyLabel("deny_tool") + " deny."
			m.addEntry("permission", formatToolApprovalWithKeys(request.request, m.workspace, m.keyLabel("approve_tool"), m.keyLabel("deny_tool")), true)
		}
	case core.EventApprovalCancelled:
		if m.pendingApproval != nil && m.pendingApproval.request.ID == event.ApprovalID {
			m.pendingApproval = nil
			m.status = "Tool approval cancelled."
		}
	case core.EventUserInteractionRequired:
		if event.Interaction != nil {
			command = m.beginMCPInteraction(*event.Interaction)
		}
	case core.EventUserInteractionCancelled:
		if m.pendingMCPInteraction != nil && m.pendingMCPInteraction.request.ID == event.InteractionID {
			m.finishMCPInteraction("MCP interaction cancelled because its task ended.", false)
		}
	case core.EventUsage:
		m.updateModelUsage(event.Usage, event.AgentID, event.ModelProfile,
			event.Provider, event.Model, event.ContextWindow, event.EstimatedInputTokens)
	case core.EventContextCompaction:
		m.updateContextCompaction(event)
	case core.EventRuntimeStreamFailed:
		m.status = "Session event stream failed: " + event.Err
	case core.EventAcceptanceRequired:
		m.status = "Acceptance decision required. Use /continue to review it."
	case core.EventExecutionBlocked:
		m.status = "Execution blocked. Review the blocker and create a new read-only plan."
	case core.EventModelRecoveryRequired, core.EventToolRecoveryRequired:
		m.status = "Recovery decision required. Use /continue to review it."
	}
	m.refreshPanels()
	return command
}

func (m *tuiModel) submit() (tea.Model, tea.Cmd) {
	raw := m.input.Value()
	line := strings.TrimSpace(raw)
	if m.pendingRequest != nil && m.pendingRequest.Kind == core.PendingUserQuestion && line != "" && !strings.HasPrefix(line, "/") {
		return m.answerUserQuestion(line)
	}
	if strings.HasPrefix(line, "/continue ") {
		return m.continueTask(line)
	}
	if strings.HasPrefix(line, "/plan replan") {
		return m.replanAfterBlocker(line)
	}
	if model, command, handled := m.updatePlanAuthorization(line); handled {
		return model, command
	}
	if len(m.attachments) > 0 && strings.HasPrefix(line, "/") {
		m.status = "Remove attached images before running a slash command."
		return m, nil
	}
	if m.mcpCommandPending && line != "" && !strings.HasPrefix(line, "/") {
		m.status = "Wait for the MCP operation to finish before starting a task."
		return m, nil
	}
	if m.planReview && line != "" && !strings.HasPrefix(line, "/") {
		m.planReview = false
	}
	switch line {
	case "":
		if len(m.attachments) == 0 {
			m.status = "Enter a message or command."
			return m, nil
		}
	case "/help":
		m.addEntry("notice", tuiHelpText(), false)
		m.resetInput()
		return m, nil
	case "/exit":
		return m.requestExit()
	case "/exit confirm":
		return m.confirmExit(true)
	case "/exit wait":
		return m.confirmExit(false)
	case "/resume":
		return m.openSessionPicker()
	case "/fork":
		return m.forkSession()
	case "/details":
		return m.showMessageDetails(line)
	case "/goal":
		return m.showGoal()
	case "/rewind":
		m.status = "Use /rewind <n> to remove the latest n user turns."
		return m, nil
	case "/queue":
		return m.openQueuePicker()
	case "/continue":
		return m.continueTask(line)
	case "/new":
		return m.newSession()
	case "/discard":
		return m.discard()
	case "/compact":
		return m.compact()
	case "/autocompact":
		return m.showAutoCompactSettings()
	case "/context":
		return m.showContextSettings()
	case "/permissions":
		return m.openPermissionMenu()
	case "/model":
		return m.openModelPicker()
	case "/mcp":
		return m.handleMCPCommand("/mcp")
	case "/effort":
		return m.openEffortPicker()
	case "/plan":
		return m.togglePlanMode()
	case "/usage":
		m.detailsOpen = !m.detailsOpen
		m.refreshPanels()
		m.status = "Usage details toggled."
		m.resetInput()
		return m, nil
	case "/vim":
		return m.toggleVimMode()
	case "/theme":
		m.status = fmt.Sprintf("Current theme: %s. Use /theme auto, /theme dark, or /theme light to change it.", m.themePreferenceLabel())
		m.resetInput()
		return m, nil
	case "/keymap":
		return m.openKeymapEditor()
	case "/keymap reset":
		return m.resetKeymap()
	case "/":
		m.updateCommandSuggestions()
		m.status = "Choose a command; use " + m.keyLabel("suggestion_previous") + "/" + m.keyLabel("suggestion_next") + " to select and " + m.keyLabel("send") + " to run."
		m.updateLayout()
		return m, nil
	}
	if strings.HasPrefix(line, "/permissions ") || strings.HasPrefix(line, "/permissions\t") {
		return m.setPermissionModeFromCommand(line)
	}
	if strings.HasPrefix(line, "/context ") || strings.HasPrefix(line, "/context\t") {
		return m.setContextSettingsFromCommand(line)
	}
	if strings.HasPrefix(line, "/autocompact ") || strings.HasPrefix(line, "/autocompact\t") {
		return m.setAutoCompactFromCommand(line)
	}
	if strings.HasPrefix(line, "/goal ") || strings.HasPrefix(line, "/goal\t") {
		return m.setGoalFromCommand(line)
	}
	if strings.HasPrefix(line, "/details ") || strings.HasPrefix(line, "/details\t") {
		return m.showMessageDetails(line)
	}
	if strings.HasPrefix(line, "/model ") || strings.HasPrefix(line, "/model\t") {
		return m.setModelFromCommand(line)
	}
	if strings.HasPrefix(line, "/mcp ") || strings.HasPrefix(line, "/mcp\t") {
		return m.handleMCPCommand(line)
	}
	if strings.HasPrefix(line, "/effort ") || strings.HasPrefix(line, "/effort\t") {
		return m.setEffortFromCommand(line)
	}
	if strings.HasPrefix(line, "/rewind ") || strings.HasPrefix(line, "/rewind\t") {
		return m.rewindSession(line)
	}
	if strings.HasPrefix(line, "/theme ") || strings.HasPrefix(line, "/theme\t") {
		return m.setThemeFromCommand(line)
	}
	if strings.HasPrefix(line, "/keymap ") || strings.HasPrefix(line, "/keymap\t") {
		m.status = "Use /keymap to edit bindings or /keymap reset to restore defaults."
		return m, nil
	}
	if strings.HasPrefix(line, "/") {
		m.status = "Unknown command."
		m.addEntry("error", fmt.Sprintf("Unknown command %q. Available commands: %s", line, strings.Join(tuiCommandNames(), " ")), true)
		return m, nil
	}

	input, err := draftTaskInput(raw, m.attachments)
	if err != nil {
		m.addEntry("error", err.Error(), true)
		m.status = "Input was not accepted."
		return m, nil
	}
	if pending, err := m.hasPending(); err != nil {
		m.addEntry("error", err.Error(), true)
		m.status = "Could not inspect paused task."
		return m, nil
	} else if pending {
		m.status = "A task is paused. Use /continue or /discard before starting another turn."
		m.addEntry("notice", m.status, false)
		return m, nil
	}
	m.addEntry("you", inputTranscriptText(input, raw), false)
	m.resetInput()
	m.cancelled = false
	return m.submitTurn(input, core.StartIfIdle, true)
}

func (m *tuiModel) requestExit() (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		return m, tea.Quit
	}
	state, err := m.runtime.State(context.Background())
	if err != nil {
		m.status = "Could not inspect running turns before exit."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued || len(state.Queue) > 0 {
		m.exitConfirm = true
		m.status = fmt.Sprintf("Active work and %d queued turn(s) · /exit confirm interrupts and cancels queued turns · /exit wait finishes them · Esc returns.", len(state.Queue))
		m.resetInput()
		return m, nil
	}
	return m, tea.Quit
}

func (m *tuiModel) confirmExit(cancelQueued bool) (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		return m, tea.Quit
	}
	state, err := m.runtime.State(context.Background())
	if err != nil {
		m.addEntry("error", "Could not inspect the Core queue: "+err.Error(), true)
		return m, nil
	}
	if cancelQueued {
		for _, queued := range state.Queue {
			m.runtime.Interrupt(queued.ExecutionID)
		}
	}
	if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued {
		if cancelQueued {
			m.runtime.Interrupt(state.Task.ExecutionID)
		}
		m.exitConfirm = false
		m.exitAfterTask = true
		m.running = true
		m.runtimeTask = true
		m.runningExecutionID = state.Task.ExecutionID
		m.activeExecutions[state.Task.ExecutionID] = true
		m.status = "Waiting for Core to stop before exit…"
		return m, waitForTurn(m.runtime, state.Task.ExecutionID)
	}
	return m, tea.Quit
}

func (m *tuiModel) showGoal() (tea.Model, tea.Cmd) {
	goal := strings.TrimSpace(m.sessionSettings.Goal)
	if goal == "" {
		m.addEntry("notice", "No task goal is set. Use /goal <text> to set one.", false)
	} else {
		m.addEntry("notice", "Task goal:\n"+goal, false)
	}
	m.status = "Goal applies to future tasks in this session."
	m.resetInput()
	return m, nil
}

func (m *tuiModel) showContextSettings() (tea.Model, tea.Cmd) {
	defaults := m.projectContextDefaults()
	window, windowSource := defaults.ContextWindow, "project default"
	reserve, reserveSource := defaults.ReserveOutputTokens, "project default"
	if m.sessionSettings.ContextWindow != nil {
		window, windowSource = *m.sessionSettings.ContextWindow, "session override"
	}
	if m.sessionSettings.ReserveOutputTokens != nil {
		reserve, reserveSource = *m.sessionSettings.ReserveOutputTokens, "session override"
	}
	windowLabel := "unknown"
	if window > 0 {
		windowLabel = compactTokens(window)
	} else if windowSource == "session override" {
		windowLabel = "disabled"
	}
	reserveLabel := "none"
	if reserve > 0 {
		reserveLabel = compactTokens(reserve)
	}
	message := fmt.Sprintf("Context window: %s (%s)\nOutput reserve: %s (%s)", windowLabel, windowSource, reserveLabel, reserveSource)
	message += "\nUse /context <tokens>, /context window <tokens|reset>, /context reserve <tokens|reset>, or /context reset."
	message += "\nWhen automatic compaction is enabled, the context window sets its trigger budget."
	message += "\nThe output reserve leaves room for the model's response when compaction is triggered."
	m.addEntry("notice", message, false)
	m.status = "Context settings are session overrides; project defaults remain unchanged."
	m.resetInput()
	return m, nil
}

func (m *tuiModel) projectContextDefaults() core.ContextDefaults {
	if m.client == nil {
		return core.ContextDefaults{}
	}
	return m.client.ProjectContextDefaults()
}

func (m *tuiModel) setContextSettingsFromCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	settings := m.sessionSettings
	message := ""
	switch {
	case len(fields) == 2 && fields[1] == "reset":
		settings.ContextWindow = nil
		settings.ReserveOutputTokens = nil
		message = "Context window and output reserve overrides cleared."
	case len(fields) == 3 && fields[1] == "window" && fields[2] == "reset":
		settings.ContextWindow = nil
		message = "Context window override cleared."
	case len(fields) == 3 && fields[1] == "reserve" && fields[2] == "reset":
		settings.ReserveOutputTokens = nil
		message = "Output reserve override cleared."
	case len(fields) == 2:
		value, ok := parseContextTokenCount(fields[1])
		if !ok {
			return m.contextCommandUsage()
		}
		settings.ContextWindow = &value
		if value == 0 {
			message = "Context window cleared. Automatic compaction will wait for a positive window."
		} else {
			message = "Context window saved. Automatic compaction can use it on future tasks."
		}
	case len(fields) == 3 && fields[1] == "window":
		value, ok := parseContextTokenCount(fields[2])
		if !ok {
			return m.contextCommandUsage()
		}
		settings.ContextWindow = &value
		if value == 0 {
			message = "Context window cleared. Automatic compaction will wait for a positive window."
		} else {
			message = "Context window saved. Automatic compaction can use it on future tasks."
		}
	case len(fields) == 3 && fields[1] == "reserve":
		value, ok := parseContextTokenCount(fields[2])
		if !ok {
			return m.contextCommandUsage()
		}
		settings.ReserveOutputTokens = &value
		message = "Output reserve saved for future tasks. It reduces the input budget available before compaction."
	default:
		return m.contextCommandUsage()
	}
	if err := m.client.SetSessionSettings(context.Background(), m.sessionID, settings); err != nil {
		m.status = "Context settings could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.sessionSettings = settings
	defaults := m.projectContextDefaults()
	m.contextWindow = defaults.ContextWindow
	if settings.ContextWindow != nil {
		m.contextWindow = *settings.ContextWindow
	}
	if m.pending {
		message += " The paused task keeps its original context settings."
	}
	m.status = message
	m.addEntry("notice", message, false)
	m.resetInput()
	return m, nil
}

func (m *tuiModel) showAutoCompactSettings() (tea.Model, tea.Cmd) {
	defaults := m.projectContextDefaults()
	enabled, enabledSource := defaults.AutoCompact, "project default"
	threshold, thresholdSource := defaults.AutoCompactThreshold, "project default"
	if m.sessionSettings.AutoCompact != nil {
		enabled, enabledSource = *m.sessionSettings.AutoCompact, "session override"
	}
	if m.sessionSettings.AutoCompactThreshold != nil {
		threshold, thresholdSource = *m.sessionSettings.AutoCompactThreshold, "session override"
	}
	if threshold == 0 {
		threshold = core.DefaultAutoCompactThreshold
		thresholdSource = "default when enabled"
	}
	state := "off"
	if enabled {
		state = "on"
	}
	message := fmt.Sprintf("Automatic compaction: %s (%s)\nThreshold: %.0f%% (%s)", state, enabledSource, threshold*100, thresholdSource)
	message += "\nAutomatic compaction runs before model requests when the configured threshold is reached."
	window := defaults.ContextWindow
	if m.sessionSettings.ContextWindow != nil {
		window = *m.sessionSettings.ContextWindow
	}
	if enabled && window <= 0 {
		message += "\nSet a context window with /context window <tokens> to activate automatic compaction."
	}
	message += "\nUse /autocompact on|off, /autocompact threshold <0..1>, or /autocompact reset."
	m.addEntry("notice", message, false)
	m.status = "Auto-compaction settings apply to future tasks."
	m.resetInput()
	return m, nil
}

func (m *tuiModel) setAutoCompactFromCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	settings := m.sessionSettings
	message := ""
	switch {
	case len(fields) == 2 && fields[1] == "reset":
		settings.AutoCompact = nil
		settings.AutoCompactThreshold = nil
		message = "Auto-compaction overrides cleared."
	case len(fields) == 2 && (fields[1] == "on" || fields[1] == "off"):
		enabled := fields[1] == "on"
		settings.AutoCompact = &enabled
		message = fmt.Sprintf("Auto-compaction setting saved as %s.", fields[1])
	case len(fields) == 3 && fields[1] == "threshold" && fields[2] == "reset":
		settings.AutoCompactThreshold = nil
		message = "Auto-compaction threshold override cleared."
	case len(fields) == 3 && fields[1] == "threshold":
		threshold, err := strconv.ParseFloat(fields[2], 64)
		if err != nil || math.IsNaN(threshold) || math.IsInf(threshold, 0) || threshold <= 0 || threshold > 1 {
			m.status = "Threshold must be greater than 0 and at most 1."
			m.addEntry("error", m.status, true)
			return m, nil
		}
		settings.AutoCompactThreshold = &threshold
		message = fmt.Sprintf("Auto-compaction threshold saved as %.0f%%.", threshold*100)
	default:
		m.status = "Use /autocompact on|off, /autocompact threshold <0..1>, or /autocompact reset."
		return m, nil
	}
	if err := m.client.SetSessionSettings(context.Background(), m.sessionID, settings); err != nil {
		m.status = "Auto-compaction settings could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.sessionSettings = settings
	message += " This setting applies to future tasks."
	if m.pending {
		message += " The paused task keeps its original settings."
	}
	m.status = message
	m.addEntry("notice", message, false)
	m.resetInput()
	return m, nil
}

func (m *tuiModel) contextCommandUsage() (tea.Model, tea.Cmd) {
	m.status = "Use /context <tokens>, /context window <tokens|reset>, /context reserve <tokens|reset>, or /context reset."
	return m, nil
}

func parseContextTokenCount(value string) (int64, bool) {
	tokens, err := strconv.ParseInt(value, 10, 64)
	return tokens, err == nil && tokens >= 0
}

func (m *tuiModel) setGoalFromCommand(line string) (tea.Model, tea.Cmd) {
	goal := strings.TrimSpace(line[len("/goal"):])
	clear := strings.EqualFold(goal, "clear")
	if clear {
		goal = ""
	}
	settings := m.sessionSettings
	settings.Goal = goal
	if err := m.client.SetSessionSettings(context.Background(), m.sessionID, settings); err != nil {
		m.status = "Task goal could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.sessionSettings = settings
	switch {
	case clear:
		m.status = "Task goal cleared."
	case goal == "":
		m.status = "Use /goal <text> to set a task goal, or /goal clear to remove it."
	default:
		m.status = "Task goal saved for future tasks."
	}
	if m.pending {
		m.status += " The paused task keeps its original goal."
	}
	m.addEntry("notice", m.status, false)
	m.resetInput()
	return m, nil
}

func (m *tuiModel) showMessageDetails(line string) (tea.Model, tea.Cmd) {
	var messages []tuiEntry
	for _, entry := range m.entries {
		if entry.role == "you" {
			messages = append(messages, entry)
		}
	}
	fields := strings.Fields(line)
	if len(fields) == 1 {
		if len(messages) == 0 {
			m.status = "No user messages to show."
			m.addEntry("notice", m.status, false)
		} else {
			lines := []string{"User messages · use /details <n> to show one in terminal history:"}
			for index, entry := range messages {
				preview := strings.Join(strings.Fields(entry.text), " ")
				if runes := []rune(preview); len(runes) > 72 {
					preview = string(runes[:69]) + "…"
				}
				lines = append(lines, fmt.Sprintf("%d · %s", index+1, preview))
			}
			m.addEntry("notice", strings.Join(lines, "\n"), false)
			m.status = "Choose a message with /details <n>."
		}
		m.resetInput()
		return m, nil
	}
	if len(fields) != 2 {
		m.status = "Use /details <n> to show a user message in terminal history."
		return m, nil
	}
	index, err := strconv.Atoi(fields[1])
	if err != nil || index < 1 || index > len(messages) {
		m.status = "Unknown message number. Use /details to list user messages."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	m.addEntry("notice", fmt.Sprintf("User message %d:\n%s", index, messages[index-1].text), false)
	m.status = fmt.Sprintf("Displayed user message %d in terminal history.", index)
	m.resetInput()
	return m, nil
}

func (m *tuiModel) openModelPicker() (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.status = "Model selection is unavailable."
		return m, nil
	}
	options := m.client.ListModelProfiles()
	if len(options) == 0 {
		m.status = "No model profiles are configured."
		return m, nil
	}
	m.modelOptions = options
	m.modelIndex = 0
	for index, option := range options {
		if option.Profile == m.sessionSettings.ModelProfile {
			m.modelIndex = index
			break
		}
	}
	m.modelPicker = true
	m.resetInput()
	m.status = "Choose a model profile; " + m.keyLabel("picker_previous") + "/" + m.keyLabel("picker_next") + " select, " + m.keyLabel("send") + " apply, Esc cancel."
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) openEffortPicker() (tea.Model, tea.Cmd) {
	m.effortIndex = 0
	for index, value := range reasoningEffortOptions {
		if value == m.sessionSettings.ReasoningEffort {
			m.effortIndex = index
			break
		}
	}
	m.effortPicker = true
	m.resetInput()
	m.status = "Choose reasoning effort; " + m.keyLabel("picker_previous") + "/" + m.keyLabel("picker_next") + " select, " + m.keyLabel("send") + " apply, Esc cancel."
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) selectModelProfile() (tea.Model, tea.Cmd) {
	if m.modelIndex < 0 || m.modelIndex >= len(m.modelOptions) {
		m.modelPicker = false
		return m, nil
	}
	settings := m.sessionSettings
	settings.ModelProfile = m.modelOptions[m.modelIndex].Profile
	if err := m.client.SetSessionSettings(context.Background(), m.sessionID, settings); err != nil {
		m.status = "Model profile could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.sessionSettings = settings
	m.modelPicker = false
	m.status = "Model profile set to " + settings.ModelProfile + "; it applies to future tasks."
	if m.pending {
		m.status += " /continue keeps the paused task's original model."
	}
	m.resetInput()
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) selectReasoningEffort() (tea.Model, tea.Cmd) {
	if m.effortIndex < 0 || m.effortIndex >= len(reasoningEffortOptions) {
		m.effortPicker = false
		return m, nil
	}
	settings := m.sessionSettings
	settings.ReasoningEffort = reasoningEffortOptions[m.effortIndex]
	if err := m.client.SetSessionSettings(context.Background(), m.sessionID, settings); err != nil {
		m.status = "Reasoning effort could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.sessionSettings = settings
	m.effortPicker = false
	m.status = "Reasoning effort set to " + reasoningEffortLabel(settings.ReasoningEffort) + "; it applies to future tasks."
	if m.pending {
		m.status += " /continue keeps the paused task's original effort."
	}
	m.resetInput()
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) setModelFromCommand(line string) (tea.Model, tea.Cmd) {
	profile := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "/model"), "\t"))
	profile = strings.TrimSpace(strings.TrimPrefix(profile, " "))
	for _, option := range m.client.ListModelProfiles() {
		if option.Profile != profile {
			continue
		}
		settings := m.sessionSettings
		settings.ModelProfile = profile
		if err := m.client.SetSessionSettings(context.Background(), m.sessionID, settings); err != nil {
			m.status = "Model profile could not be saved."
			m.addEntry("error", err.Error(), true)
			return m, nil
		}
		m.sessionSettings = settings
		m.status = "Model profile set to " + profile + "; it applies to future tasks."
		if m.pending {
			m.status += " /continue keeps the paused task's original model."
		}
		m.resetInput()
		return m, nil
	}
	m.status = "Unknown model profile. Use /model to list profiles."
	m.addEntry("error", m.status, true)
	return m, nil
}

func (m *tuiModel) setEffortFromCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		m.status = "Use /effort <default|none|minimal|low|medium|high|xhigh|max>."
		return m, nil
	}
	effort := strings.ToLower(fields[1])
	if effort == "default" {
		effort = ""
	}
	valid := false
	for _, option := range reasoningEffortOptions {
		if option == effort {
			valid = true
			break
		}
	}
	if !valid {
		m.status = "Unknown reasoning effort. Use /effort to list values."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	settings := m.sessionSettings
	settings.ReasoningEffort = effort
	if err := m.client.SetSessionSettings(context.Background(), m.sessionID, settings); err != nil {
		m.status = "Reasoning effort could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.sessionSettings = settings
	m.status = "Reasoning effort set to " + reasoningEffortLabel(effort) + "; it applies to future tasks."
	if m.pending {
		m.status += " /continue keeps the paused task's original effort."
	}
	m.resetInput()
	return m, nil
}

func reasoningEffortLabel(value string) string {
	if value == "" {
		return "Default"
	}
	return value
}

func (m *tuiModel) setThemeFromCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		m.status = "Use /theme auto, /theme dark, or /theme light."
		return m, nil
	}
	theme := themeName(strings.ToLower(fields[1]))
	if theme != themeAuto && theme != themeDark && theme != themeLight {
		m.status = "Theme must be auto, dark, or light."
		return m, nil
	}
	m.preferences.Theme = theme
	if err := saveUIPreferences(m.preferencesPath, m.preferences); err != nil {
		m.status = "Theme changed for this session; preference could not be saved."
	} else {
		m.status = "Theme set to " + string(theme) + "."
	}
	if theme == themeAuto {
		m.terminalBackgroundReceived = false
		m.terminalForegroundReceived = false
		m.terminalColorQueryPending = true
		m.terminalColorsReady = false
	} else {
		m.terminalColorQueryPending = false
		m.terminalColorsReady = true
	}
	m.applyResolvedTheme()
	m.resetInput()
	if theme == themeAuto {
		return m, m.requestTerminalColorCommands()
	}
	return m, nil
}

func (m *tuiModel) themePreferenceLabel() string {
	if m.preferences.Theme == themeAuto {
		return "auto (" + string(m.resolvedTheme()) + ")"
	}
	return string(m.preferences.Theme)
}

func (m *tuiModel) requestTerminalColors() tea.Cmd {
	m.terminalBackgroundReceived = false
	m.terminalForegroundReceived = false
	m.terminalColorQueryPending = true
	m.terminalColorsReady = false
	return m.requestTerminalColorCommands()
}

func (m *tuiModel) requestTerminalColorCommands() tea.Cmd {
	return tea.Batch(
		tea.RequestBackgroundColor,
		tea.RequestForegroundColor,
		tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return terminalColorTimeoutMsg{} }),
	)
}

func (m *tuiModel) finishTerminalColorQueryIfReady() {
	if m.terminalColorQueryPending && m.terminalBackgroundReceived && m.terminalForegroundReceived {
		m.terminalColorQueryPending = false
		m.terminalColorsReady = true
	}
}

func (m *tuiModel) enqueueInput() (tea.Model, tea.Cmd) {
	raw := m.input.Value()
	line := strings.TrimSpace(raw)
	if line == "" && len(m.attachments) == 0 {
		m.status = "Type a message before queueing it."
		return m, nil
	}
	if strings.HasPrefix(line, "/") {
		m.status = "Commands cannot be queued while a task is running."
		return m, nil
	}
	input, err := draftTaskInput(raw, m.attachments)
	if err != nil {
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	display := inputTranscriptText(input, raw)
	m.addEntry("you", display, false)
	m.status = "Queueing this turn in Core…"
	m.resetInput()
	return m.submitTurn(input, core.QueueInput, false)
}

func (m *tuiModel) openQueuePicker() (tea.Model, tea.Cmd) {
	if err := m.refreshRuntimeProjection(); err != nil {
		m.status = "Could not load the Core queue."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if len(m.queued) == 0 {
		m.status = "No queued messages."
		m.resetInput()
		return m, nil
	}
	m.queuePicker = true
	m.queueIndex = min(m.queueIndex, len(m.queued)-1)
	m.status = "Core queue · " + m.keyLabel("picker_previous") + "/" + m.keyLabel("picker_next") + " select · " + m.keyLabel("send") + " details · " + m.keyLabel("queue_remove") + " cancel."
	m.resetInput()
	return m, nil
}

func (m *tuiModel) editQueuedInput() (tea.Model, tea.Cmd) {
	if m.queueIndex < 0 || m.queueIndex >= len(m.queued) {
		m.queuePicker = false
		return m, nil
	}
	queued := m.queued[m.queueIndex]
	m.addEntry("notice", fmt.Sprintf("Queued turn %d · %s · execution %s", queued.Position, queued.Status, queued.ExecutionID), false)
	m.status = "Queued input is managed by Core and cannot be edited here."
	return m, nil
}

func (m *tuiModel) deleteQueuedInput() {
	if m.queueIndex < 0 || m.queueIndex >= len(m.queued) {
		return
	}
	queued := m.queued[m.queueIndex]
	if m.runtime == nil || !m.runtime.Interrupt(queued.ExecutionID) {
		m.status = "Core could not cancel this queued turn."
		return
	}
	if err := m.refreshRuntimeProjection(); err != nil {
		m.status = "Turn cancellation was requested, but the queue could not be refreshed."
		return
	}
	if len(m.queued) == 0 {
		m.queuePicker = false
		m.status = "Queued turn cancelled."
	} else {
		m.queueIndex = min(m.queueIndex, len(m.queued)-1)
		m.status = fmt.Sprintf("Queued turn cancelled · %d remain.", len(m.queued))
	}
	m.refreshPanels()
}

func (m *tuiModel) clearQueue() {
	m.queued = nil
	m.queuePicker = false
	m.queueIndex = 0
	m.refreshPanels()
}

func (m *tuiModel) acceptPlan() (tea.Model, tea.Cmd) {
	return m.implementProposedPlan(core.PlanImplementSameContext)
}

func (m *tuiModel) implementProposedPlan(mode core.PlanImplementationMode) (tea.Model, tea.Cmd) {
	if m.runtime == nil || m.proposedPlan == nil || m.planSourceExecutionID == "" {
		m.status = "There is no completed plan to implement."
		return m, nil
	}
	runtime := m.runtime
	client := m.client
	sourceID := m.planSourceExecutionID
	authorization := m.planAuthorization
	m.planReview = false
	m.resetInput()
	m.running = true
	m.runtimeTask = true
	m.status = "Starting the approved plan…"
	return m, func() tea.Msg {
		implementation, err := runtime.ImplementPlan(context.Background(), sourceID, mode, authorization)
		if err != nil {
			return taskFinishedMsg{result: taskResult{err: err}}
		}
		waitRuntime := runtime
		if implementation.SessionID != runtime.SessionID() {
			waitRuntime, err = client.OpenSessionRuntime(context.Background(), implementation.SessionID)
			if err != nil {
				return taskFinishedMsg{result: taskResult{targetSessionID: implementation.SessionID, err: err}}
			}
		}
		result, err := waitRuntime.WaitTurn(context.Background(), implementation.Submission.ExecutionID)
		return taskFinishedMsg{result: taskResult{response: result.Response, coreResult: result, targetSessionID: implementation.SessionID, err: err}}
	}
}

func (m *tuiModel) updatePlanAuthorization(line string) (tea.Model, tea.Cmd, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 || fields[0] != "/plan" || !m.planReview {
		return m, nil, false
	}
	switch {
	case len(fields) == 2 && fields[1] == "implement":
		model, command := m.implementProposedPlan(core.PlanImplementSameContext)
		return model, command, true
	case len(fields) == 3 && fields[1] == "implement" && fields[2] == "new":
		model, command := m.implementProposedPlan(core.PlanImplementNewContext)
		return model, command, true
	case len(fields) == 3 && fields[1] == "implement" && fields[2] == "same":
		model, command := m.implementProposedPlan(core.PlanImplementSameContext)
		return model, command, true
	case len(fields) == 2 && fields[1] == "stay":
		m.planReview = false
		m.proposedPlan = nil
		m.status = "Plan saved in the conversation; permission remains read-only."
	case len(fields) == 2 && fields[1] == "allow-network":
		m.planAuthorization.AllowNetwork = !m.planAuthorization.AllowNetwork
		m.status = fmt.Sprintf("Plan network access: %t.", m.planAuthorization.AllowNetwork)
	case len(fields) == 3 && fields[1] == "allow-tool":
		m.planAuthorization.AllowedTools = appendUnique(m.planAuthorization.AllowedTools, fields[2])
		m.status = "Allowed tool added to this plan."
	case len(fields) >= 3 && fields[1] == "allow-command":
		command := strings.TrimSpace(strings.TrimPrefix(line, "/plan allow-command"))
		m.planAuthorization.ApprovedHighRiskCommands = appendUnique(m.planAuthorization.ApprovedHighRiskCommands, command)
		m.status = "Exact high-risk command added to this plan."
	case len(fields) >= 3 && fields[1] == "allow-path":
		path := fields[2]
		if !filepath.IsAbs(path) {
			m.status = "Approved paths must be absolute."
			return m, nil, true
		}
		access := publictool.PathReadWrite
		if len(fields) >= 4 && fields[3] == "read" {
			access = publictool.PathRead
		} else if len(fields) >= 4 && fields[3] != "read_write" {
			m.status = "Path access must be read or read_write."
			return m, nil, true
		}
		m.planAuthorization.Paths = append(m.planAuthorization.Paths, publictool.PathGrant{Path: path, Access: access})
		m.status = "Path grant added to this plan."
	default:
		m.status = "Plan options: /plan allow-network, allow-path <absolute-path> [read|read_write], allow-tool <name>, allow-command <exact>, implement [same|new], stay."
	}
	m.resetInput()
	return m, nil, true
}

func appendUnique(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func (m *tuiModel) togglePlanMode() (tea.Model, tea.Cmd) {
	if m.running {
		m.status = "Plan mode can only be changed while idle."
		m.addEntry("notice", m.status, false)
		return m, nil
	}
	mode := m.currentPermissionMode()
	coreMode := mode
	if !m.planMode {
		coreMode = core.PermissionReadOnly
	} else if coreMode == "" {
		coreMode = core.PermissionWorkspace
	}
	if err := m.client.SetPermissionMode(coreMode); err != nil {
		m.status = "Could not change Core permission mode."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.planMode = !m.planMode
	if m.planMode {
		m.status = "Plan mode · tools are restricted to read-only."
	} else {
		m.status = "Plan mode off · " + permissionModeLabel(mode) + " permissions restored."
	}
	m.addEntry("notice", m.status, false)
	m.resetInput()
	return m, nil
}

func (m *tuiModel) toggleVimMode() (tea.Model, tea.Cmd) {
	m.vimMode = !m.vimMode
	m.vimNormal = false
	m.vimPendingDelete = false
	if m.vimMode {
		m.status = "Vim mode · insert (Esc normal, " + m.keyLabel("vim_insert") + " insert, " + m.keyLabel("vim_left") + "/" + m.keyLabel("vim_down") + "/" + m.keyLabel("vim_up") + "/" + m.keyLabel("vim_right") + " move)."
	} else {
		m.status = "Vim mode disabled."
	}
	m.resetInput()
	return m, nil
}

func (m *tuiModel) handleVimKey(message tea.KeyPressMsg) (bool, tea.Cmd) {
	if !m.vimMode {
		return false, nil
	}
	if m.sessionPicker || m.permissionMenu || m.queuePicker {
		return false, nil
	}
	keyName := message.String()
	if keyName == "esc" && m.planReview {
		return false, nil
	}
	if !m.vimNormal && keyName == "esc" {
		m.vimNormal = true
		m.status = "Vim normal mode · " + m.keyLabel("vim_insert") + " insert · " + m.keyLabel("vim_left") + "/" + m.keyLabel("vim_down") + "/" + m.keyLabel("vim_up") + "/" + m.keyLabel("vim_right") + " move · " + m.keyLabel("send") + " sends."
		return true, nil
	}
	if !m.vimNormal {
		return false, nil
	}
	switch {
	case m.matchesKey("vim_insert", message):
		m.vimNormal = false
		m.vimPendingDelete = false
		m.status = "Vim insert mode."
		return true, nil
	case m.matchesKey("vim_append", message):
		command := m.updateInput(tea.KeyPressMsg{Code: tea.KeyRight})
		m.vimNormal = false
		m.status = "Vim insert mode."
		return true, command
	case m.matchesKey("vim_left", message):
		command := m.updateInput(tea.KeyPressMsg{Code: tea.KeyLeft})
		return true, command
	case m.matchesKey("vim_down", message):
		m.input.CursorDown()
		return true, nil
	case m.matchesKey("vim_up", message):
		m.input.CursorUp()
		return true, nil
	case m.matchesKey("vim_line_start", message):
		m.input.CursorStart()
		return true, nil
	case m.matchesKey("vim_line_end", message):
		m.input.CursorEnd()
		return true, nil
	case m.matchesKey("vim_delete_line", message):
		if m.vimPendingDelete {
			m.deleteCurrentLine()
			m.vimPendingDelete = false
		} else {
			m.vimPendingDelete = true
		}
		return true, nil
	case keyName == "esc":
		m.vimPendingDelete = false
		return true, nil
	}
	if message.Text != "" {
		return true, nil
	}
	return false, nil
}

func (m *tuiModel) deleteCurrentLine() {
	cursor := m.input.Cursor()
	if cursor == nil {
		return
	}
	lines := strings.Split(m.input.Value(), "\n")
	line := min(max(cursor.Y, 0), len(lines)-1)
	lines = append(lines[:line], lines[line+1:]...)
	before := m.input.Value()
	m.input.SetValue(strings.Join(lines, "\n"))
	m.recordInputChange(before, m.input.Value())
}

func (m *tuiModel) openPermissionMenu() (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.status = "Permission settings are unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	m.permissionIndex = 0
	for index, option := range tuiPermissionOptions {
		if option.mode == m.currentPermissionMode() {
			m.permissionIndex = index
			break
		}
	}
	m.permissionMenu = true
	m.resetInput()
	m.status = "Choose a permission mode; Enter applies it, Esc cancels."
	m.refreshPanels()
	return m, nil
}

func (m *tuiModel) cyclePermissionMode() (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.status = "Permission settings are unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	currentIndex := -1
	for index, option := range tuiPermissionOptions {
		if option.mode == m.currentPermissionMode() {
			currentIndex = index
			break
		}
	}
	if currentIndex < 0 {
		currentIndex = 0
	}
	next := tuiPermissionOptions[(currentIndex+1)%len(tuiPermissionOptions)]
	return m.changePermissionMode(next.mode, false)
}

func (m *tuiModel) selectPermissionMode() (tea.Model, tea.Cmd) {
	if m.permissionIndex < 0 || m.permissionIndex >= len(tuiPermissionOptions) {
		m.permissionMenu = false
		return m, nil
	}
	option := tuiPermissionOptions[m.permissionIndex]
	return m.applyPermissionMode(option.mode)
}

func (m *tuiModel) setPermissionModeFromCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		m.status = "Usage: /permissions [read-only|workspace|ask|full-access]"
		m.addEntry("error", m.status, true)
		return m, nil
	}
	return m.applyPermissionMode(core.PermissionMode(fields[1]))
}

func (m *tuiModel) applyPermissionMode(mode core.PermissionMode) (tea.Model, tea.Cmd) {
	return m.changePermissionMode(mode, true)
}

func (m *tuiModel) changePermissionMode(mode core.PermissionMode, resetInput bool) (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.status = "Permission settings are unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	valid := false
	for _, option := range tuiPermissionOptions {
		if option.mode == mode {
			valid = true
			break
		}
	}
	if !valid {
		m.status = "Unknown permission mode."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	coreMode := mode
	if m.planMode {
		coreMode = core.PermissionReadOnly
	}
	if err := m.client.SetPermissionMode(coreMode); err != nil {
		m.status = err.Error()
		m.addEntry("error", m.status, true)
		return m, nil
	}
	m.permissionMode = mode
	m.permissionMenu = false
	if resetInput {
		m.resetInput()
	} else {
		m.updateCommandSuggestions()
		m.updateLayout()
	}
	m.status = fmt.Sprintf("Permission mode: %s.", permissionModeLabel(mode))
	if m.planMode {
		m.status += " Plan mode still limits tools to read-only."
	}
	notice := fmt.Sprintf("Permission mode: %s. %s", permissionModeLabel(mode), permissionModeDescription(mode))
	m.addEntry("notice", notice, false)
	m.refreshPanels()
	return m, nil
}

func (m *tuiModel) currentPermissionMode() core.PermissionMode {
	if m.permissionMode != "" {
		return m.permissionMode
	}
	if m.client != nil {
		return m.client.PermissionMode()
	}
	return core.PermissionReadOnly
}

func permissionModeLabel(mode core.PermissionMode) string {
	for _, option := range tuiPermissionOptions {
		if option.mode == mode {
			return option.label
		}
	}
	return string(mode)
}

func permissionModeDescription(mode core.PermissionMode) string {
	switch mode {
	case core.PermissionReadOnly:
		return "Read workspace files; ask before web; block writes and shell"
	case core.PermissionWorkspace:
		return "Workspace file operations and sandboxed commands; network and external paths need plan approval"
	case core.PermissionAsk:
		return "Read workspace files; approve other tool operations in the plan"
	case core.PermissionFullAccess:
		return "Current-user files and network stay in the OS sandbox; high-risk commands need plan approval"
	default:
		return ""
	}
}

func (m *tuiModel) resolveToolApproval(approved bool) {
	if m.pendingApproval == nil {
		return
	}
	request := m.pendingApproval
	m.pendingApproval = nil
	if m.runtime != nil {
		if err := m.runtime.ResolveApproval(request.request.ID, approved); err != nil {
			m.status = "Could not resolve tool approval: " + err.Error()
			m.addEntry("error", m.status, true)
			return
		}
	}
	if approved {
		m.status = "Tool call approved once."
		m.addEntry("notice", "Approved this tool call once.", false)
	} else {
		m.status = "Tool call denied."
		m.addEntry("notice", "Denied this tool call.", false)
	}
}

func formatToolApproval(request core.ApprovalRequest, workspace ...string) string {
	root := ""
	if len(workspace) > 0 {
		root = workspace[0]
	}
	return formatToolApprovalWithKeys(request, root, "Y", "N")
}

func formatToolApprovalWithKeys(request core.ApprovalRequest, workspace, approveKey, denyKey string) string {
	display := humanizedToolSummary(request.Tool, request.Arguments, workspace)
	warning := ""
	details := ""
	if request.Tool == "command" {
		var args struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(request.Arguments, &args)
		details = "\nCommand: " + cleanInlineText(args.Command)
		warning = "\nApproved shell commands run as your user and can access files outside the workspace."
	}
	return fmt.Sprintf("Allow %s once?%s%s\nPress %s to approve once, %s to deny, or Esc to interrupt the task.", display, details, warning, approveKey, denyKey)
}

func (m *tuiModel) openSessionPicker() (tea.Model, tea.Cmd) {
	if m.running || len(m.queued) > 0 {
		m.status = "Interrupt the active turn and clear queued work before switching sessions."
		return m, nil
	}
	m.showArchived = false
	if err := m.reloadSessionList(m.sessionID); err != nil {
		m.status = "Could not load sessions."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if len(m.sessionList) == 0 {
		m.sessionPicker = true
		m.status = "No active sessions; press " + m.keyLabel("session_toggle_archived") + " to show archived sessions."
		m.resetInput()
		m.updateLayout()
		return m, nil
	}
	m.sessionPicker = true
	m.status = "Choose a session · " + m.keyLabel("picker_previous") + "/" + m.keyLabel("picker_next") + " select · " + m.keyLabel("send") + " restore · " + m.keyLabel("session_archive") + " archive · " + m.keyLabel("session_rename") + " rename · " + m.keyLabel("session_delete") + " delete · " + m.keyLabel("session_toggle_archived") + " archived."
	m.resetInput()
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) reloadSessionList(preferredID string) error {
	sessions, err := m.client.ListSessions(context.Background(), 30, m.showArchived)
	if err != nil {
		return err
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if !sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
		}
		return sessions[i].ID < sessions[j].ID
	})
	m.sessionList = sessions
	if len(sessions) == 0 {
		m.sessionIndex = 0
		return nil
	}
	m.sessionIndex = min(max(m.sessionIndex, 0), len(sessions)-1)
	for index, saved := range sessions {
		if saved.ID == preferredID {
			m.sessionIndex = index
			return nil
		}
	}
	return nil
}

func (m *tuiModel) refreshSessionRail(preferredID string) {
	if m.client == nil {
		return
	}
	if err := m.reloadSessionList(preferredID); err != nil {
		m.sessionList = nil
	}
}

func (m *tuiModel) sessionRailWidth() int {
	return min(sessionRailMaxWidth, max(sessionRailMinWidth, max(1, m.width)/5))
}

func (m *tuiModel) sessionRailRange(bodyHeight int) (int, int) {
	visible := min(sessionRailMaxItems, max(0, bodyHeight-sessionRailHeaderRows-1))
	start := max(0, m.sessionIndex-visible/2)
	end := min(len(m.sessionList), start+visible)
	if end == len(m.sessionList) {
		start = max(0, end-visible)
	}
	return start, end
}

func (m *tuiModel) handleSessionRailClick(message tea.MouseClickMsg) (tea.Model, tea.Cmd, bool) {
	if !m.wideConversation() || message.Button != tea.MouseLeft || m.running || m.pendingApproval != nil ||
		m.detailsOpen || m.sessionPicker || m.modelPicker || m.effortPicker || m.queuePicker || m.permissionMenu || m.keymapOpen {
		return m, nil, false
	}
	if message.X >= m.sessionRailWidth() {
		return m, nil, false
	}
	bodyStartY := lipgloss.Height(m.renderHeader(max(1, m.width))) + 1
	row := message.Y - bodyStartY
	itemOffset := row - sessionRailHeaderRows
	if itemOffset < 0 {
		return m, nil, false
	}
	bodyHeight := m.availableBodyHeight(max(1, m.width), 1, 2)
	start, end := m.sessionRailRange(bodyHeight)
	index := start + itemOffset
	if index < start || index >= end {
		return m, nil, false
	}
	if m.sessionList[index].ID == m.sessionID {
		m.status = "Session already active."
		return m, nil, true
	}
	m.sessionIndex = index
	model, command := m.selectSession()
	return model, command, true
}

func (m *tuiModel) handleNarrowSessionSwitcherClick(message tea.MouseClickMsg) (tea.Model, tea.Cmd, bool) {
	if !m.modernConversation() || m.wideConversation() || message.Button != tea.MouseLeft || message.Y >= lipgloss.Height(m.renderHeader(max(1, m.width))) ||
		message.X >= max(1, m.width)/2 || m.running || m.pendingApproval != nil || m.detailsOpen || m.sessionPicker {
		return m, nil, false
	}
	model, command := m.openSessionPicker()
	return model, command, true
}

func (m *tuiModel) renderSessionRail(width, height int) string {
	colors := m.themeColors()
	lines := []string{
		lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render("Sessions"),
		lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render("click · /resume"),
	}
	if len(m.sessionList) == 0 {
		lines = append(lines,
			lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render("No recent sessions"),
			fitTerminalText(m.currentSessionTitle(), width),
		)
		return lipgloss.NewStyle().Width(width).Height(height).Render(strings.Join(lines, "\n"))
	}
	start, end := m.sessionRailRange(height)
	for index := start; index < end; index++ {
		saved := m.sessionList[index]
		title := sessionDisplayTitle(saved)
		if strings.TrimSpace(title) == "" {
			title = "Untitled session"
		}
		mark, color := "○", colors.Muted
		if saved.ID == m.sessionID {
			mark, color = "●", colors.Accent
		}
		line := fitTerminalText(mark+" "+title, width)
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(line))
	}
	if end < len(m.sessionList) {
		lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render("…"))
	}
	return lipgloss.NewStyle().Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

func (m *tuiModel) currentSessionTitle() string {
	for _, saved := range m.sessionList {
		if saved.ID == m.sessionID {
			if title := strings.TrimSpace(sessionDisplayTitle(saved)); title != "" {
				return title
			}
		}
	}
	if m.sessionID == "" {
		return "New session"
	}
	return "Session " + m.sessionID[:min(8, len(m.sessionID))]
}

func activityDrawerContentHeight(bodyHeight int) int {
	if bodyHeight < 5 {
		return 0
	}
	return min(max(4, bodyHeight/3), bodyHeight-2)
}

func (m *tuiModel) selectSession() (tea.Model, tea.Cmd) {
	if m.renamingSession {
		return m.saveSessionRename()
	}
	if len(m.sessionList) == 0 {
		m.status = "No session to restore; press Tab to show archived sessions."
		return m, nil
	}
	if m.sessionIndex < 0 || m.sessionIndex >= len(m.sessionList) {
		m.sessionPicker = false
		return m, nil
	}
	if m.running || len(m.queued) > 0 {
		m.status = "Interrupt the active turn and clear queued work before switching sessions."
		return m, nil
	}
	saved := m.sessionList[m.sessionIndex]
	if err := m.bindSessionRuntime(context.Background(), saved.ID); err != nil {
		m.sessionPicker = false
		m.status = "Could not open the selected session."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.restoreSessionState(saved)
	m.planReview = false
	m.sessionPicker = false
	m.status = "Session restored."
	m.resetInput()
	m.refreshPanels()
	m.updateLayout()
	commands := []tea.Cmd{m.subscribeRuntime()}
	if m.running && m.runningExecutionID != "" {
		commands = append(commands, waitForTurn(m.runtime, m.runningExecutionID))
	}
	return m, tea.Batch(commands...)
}

func (m *tuiModel) beginSessionRename() (tea.Model, tea.Cmd) {
	if m.sessionIndex < 0 || m.sessionIndex >= len(m.sessionList) {
		return m, nil
	}
	m.renamingSession = true
	m.input.Placeholder = "Session title · empty clears"
	m.setInputValue(m.sessionList[m.sessionIndex].Title)
	m.status = "Edit title · Enter save · empty clears · Esc cancel."
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) saveSessionRename() (tea.Model, tea.Cmd) {
	if m.sessionIndex < 0 || m.sessionIndex >= len(m.sessionList) {
		m.renamingSession = false
		return m, nil
	}
	saved := m.sessionList[m.sessionIndex]
	title := m.input.Value()
	if err := m.client.SetSessionTitle(context.Background(), saved.ID, title); err != nil {
		m.status = "Session title could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.renamingSession = false
	m.input.Placeholder = defaultInputPlaceholder
	m.resetInput()
	if err := m.reloadSessionList(saved.ID); err != nil {
		m.status = "Title saved, but the session list could not be refreshed."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.status = "Session title saved."
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) toggleSelectedArchive() (tea.Model, tea.Cmd) {
	if m.sessionIndex < 0 || m.sessionIndex >= len(m.sessionList) {
		return m, nil
	}
	saved := m.sessionList[m.sessionIndex]
	archived := !saved.Archived
	if err := m.client.SetSessionArchived(context.Background(), saved.ID, archived); err != nil {
		m.status = "Session archive state could not be saved."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if err := m.reloadSessionList(saved.ID); err != nil {
		m.status = "Archive state saved, but the session list could not be refreshed."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if archived {
		m.status = "Session archived."
	} else {
		m.status = "Session unarchived."
	}
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) deleteSelectedSession() (tea.Model, tea.Cmd) {
	if m.sessionIndex < 0 || m.sessionIndex >= len(m.sessionList) {
		m.deleteConfirm = false
		return m, nil
	}
	saved := m.sessionList[m.sessionIndex]
	if err := m.client.DeleteSession(context.Background(), saved.ID); err != nil {
		m.status = "Session could not be deleted."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.deleteConfirm = false
	if saved.ID == m.sessionID {
		m.sessionPicker = false
		updated, command := m.newSession()
		if finished, ok := updated.(*tuiModel); ok {
			if finished.sessionID == saved.ID {
				finished.sessionID = ""
				finished.showArchived = false
				finished.sessionPicker = true
				if err := finished.reloadSessionList(""); err != nil {
					finished.addEntry("error", err.Error(), true)
				}
				finished.status = "Session deleted, but a new session could not be created."
				finished.updateLayout()
			} else {
				finished.status = "Current session deleted; started a new session."
			}
		}
		return updated, command
	}
	if err := m.reloadSessionList(m.sessionID); err != nil {
		m.status = "Session deleted, but the session list could not be refreshed."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if len(m.sessionList) == 0 {
		m.sessionPicker = false
		m.status = "Session deleted."
	} else {
		m.status = "Session deleted."
	}
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) continueTask(line string) (tea.Model, tea.Cmd) {
	if m.runtime == nil || m.runtimeSessionID != m.sessionID {
		if err := m.bindSessionRuntime(context.Background(), m.sessionID); err != nil {
			m.status = "Task recovery is unavailable."
			m.addEntry("error", err.Error(), true)
			return m, nil
		}
	}
	requests, err := m.runtime.PendingRequests(context.Background())
	if err != nil {
		m.status = "Could not inspect pending task decisions."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.pendingRequests = requests
	if len(requests) == 0 {
		state, stateErr := m.runtime.State(context.Background())
		if stateErr != nil {
			m.addEntry("error", stateErr.Error(), true)
			return m, nil
		}
		if state.Task.Status != core.TaskPaused {
			m.pending = false
			m.status = "No paused task in this session."
			m.resetInput()
			return m, nil
		}
		return m.startRecovery(nil)
	}
	request := requests[0]
	if m.pendingRequest != nil && m.pendingRequest.Kind == request.Kind {
		request = *m.pendingRequest
	}
	m.pendingRequest = &request
	m.pending = true
	switch request.Kind {
	case core.PendingApproval:
		if request.Approval != nil {
			m.pendingApproval = &toolApprovalRequestMsg{request: *request.Approval}
			m.status = "Approve or deny the pending tool call."
			m.addEntry("permission", formatToolApprovalWithKeys(*request.Approval, m.workspace, m.keyLabel("approve_tool"), m.keyLabel("deny_tool")), true)
		}
	case core.PendingUserQuestion:
		if request.UserQuestion != nil {
			question := request.UserQuestion
			text := question.Question
			if len(question.Options) > 0 {
				text += "\nOptions: " + strings.Join(question.Options, " · ")
			}
			m.status = "Answer the question, then press Enter."
			m.addEntry("question", text, true)
		}
	case core.PendingAcceptanceRequest:
		if request.Acceptance != nil {
			m.status = "Acceptance decision required: /continue confirm or /continue revise."
			m.addEntry("notice", acceptanceSummary(*request.Acceptance), true)
		}
	case core.PendingToolRecovery:
		m.status = "A tool may have run before interruption: /continue retry or /continue skip."
		for _, pending := range requests {
			if pending.ToolRecovery != nil {
				m.addEntry("notice", fmt.Sprintf("Tool recovery: %s · invocation %s", pending.ToolRecovery.Tool, pending.ToolRecovery.InvocationID), true)
			}
		}
	case core.PendingModelRecoveryRequest:
		m.status = "Model request recovery required: /continue retry when the provider is ready."
		if request.ModelRecovery != nil {
			failure := request.ModelRecovery.Failure
			detail := string(failure.Category)
			if failure.Code != "" {
				detail += " · " + failure.Code
			}
			if failure.StatusCode != 0 {
				detail += fmt.Sprintf(" · HTTP %d", failure.StatusCode)
			}
			if !failure.RetryNotBefore.IsZero() {
				detail += " · retry after " + failure.RetryNotBefore.Local().Format("15:04:05")
			}
			m.addEntry("notice", "Model request failed: "+detail, true)
		}
	case core.PendingExecutionBlocker:
		m.planMode = true
		m.status = "Execution blocked: use /plan replan [feedback] or /discard."
		if request.Blocker != nil {
			m.addEntry("error", request.Blocker.Tool+": "+request.Blocker.Reason, true)
		}
	case core.PendingUserInteraction:
		if request.Interaction != nil {
			return m, m.beginMCPInteraction(*request.Interaction)
		}
	default:
		m.status = "This task is waiting for a Core decision."
	}
	m.resetInput()
	return m, nil
}

func (m *tuiModel) answerUserQuestion(answer string) (tea.Model, tea.Cmd) {
	if m.pendingRequest == nil || m.pendingRequest.UserQuestion == nil || m.runtime == nil {
		return m, nil
	}
	question := *m.pendingRequest.UserQuestion
	if len(question.Options) > 0 && !containsString(question.Options, answer) {
		m.status = "Choose one of the listed options or enter a matching answer."
		return m, nil
	}
	if err := m.runtime.ResolveUserQuestion(context.Background(), core.UserQuestionAnswer{
		RunID: question.RunID, InvocationID: question.InvocationID, CallID: question.CallID, Answer: answer,
	}); err != nil {
		m.status = "Could not submit the answer."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.addEntry("you", answer, false)
	m.pendingRequest = nil
	m.pending = false
	m.resetInput()
	return m.waitForRecoveredExecution(question.ExecutionID)
}

func (m *tuiModel) startRecovery(options *core.ExecutionOptions) (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		return m, nil
	}
	runtime := m.runtime
	var command tea.Cmd = func() tea.Msg {
		var submission core.TurnSubmission
		var err error
		if options == nil {
			submission, err = runtime.Recover(context.Background())
		} else {
			submission, err = runtime.Recover(context.Background(), *options)
		}
		if err != nil {
			return taskFinishedMsg{result: taskResult{err: err}}
		}
		return turnSubmittedMsg{submission: submission}
	}
	m.running = true
	m.runtimeTask = true
	m.status = "Recovering the paused task…"
	m.resetInput()
	return m, command
}

func (m *tuiModel) waitForRecoveredExecution(executionID string) (tea.Model, tea.Cmd) {
	if executionID == "" {
		return m, nil
	}
	m.running = true
	m.runtimeTask = true
	m.runningExecutionID = executionID
	m.activeExecutions[executionID] = true
	m.status = "Continuing after your answer…"
	return m, waitForTurn(m.runtime, executionID)
}

func acceptanceSummary(pending core.PendingAcceptance) string {
	var lines []string
	if pending.Candidate != "" {
		lines = append(lines, pending.Candidate)
	}
	for _, criterion := range pending.Criteria {
		lines = append(lines, "• "+criterion.Description)
	}
	return strings.Join(lines, "\n")
}

func (m *tuiModel) replanAfterBlocker(line string) (tea.Model, tea.Cmd) {
	if m.pendingRequest == nil || m.pendingRequest.Blocker == nil || m.runtime == nil {
		m.status = "There is no blocked execution to re-plan."
		return m, nil
	}
	feedback := strings.TrimSpace(strings.TrimPrefix(line, "/plan replan"))
	if err := m.client.SetPermissionMode(core.PermissionReadOnly); err != nil {
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if _, err := m.runtime.DiscardPaused(context.Background()); err != nil {
		m.addEntry("error", "Could not discard the blocked execution: "+err.Error(), true)
		return m, nil
	}
	m.pendingRequest = nil
	m.pending = false
	m.planMode = true
	if feedback == "" {
		feedback = "Create a revised plan that avoids the blocked operation."
	}
	input, err := core.NewTaskInput(feedback)
	if err != nil {
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.addEntry("you", feedback, false)
	return m.submitTurn(input, core.StartIfIdle, true)
}

func (m *tuiModel) newSession() (tea.Model, tea.Cmd) {
	saved, err := m.client.CreateSession(context.Background(), json.RawMessage("null"))
	if err != nil {
		m.status = "Could not create a session."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if err := m.bindSessionRuntime(context.Background(), saved.ID); err != nil {
		m.status = "Session was created, but its runtime could not be opened."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.sessionSettings = saved.Settings
	m.clearQueue()
	m.entries = nil
	m.activities = nil
	m.usageRequests = nil
	m.agentUsage = make(map[string]agentUsageSummary)
	m.agentUsageComplete = saved.AgentUsageComplete
	m.modelStreamFirstTokenLatency = 0
	m.modelStreamFirstTokenRecorded = false
	m.contextWindow = 0
	if saved.Settings.ContextWindow != nil {
		m.contextWindow = *saved.Settings.ContextWindow
	}
	m.contextInput = 0
	m.contextEstimated = false
	m.taskUsage = tokenUsageTotals{}
	m.sessionUsage = tokenUsageTotals{}
	m.planReview = false
	m.pending = false
	m.status = "New session."
	m.refreshSessionRail(m.sessionID)
	m.resetInput()
	m.refreshPanels()
	m.updateLayout()
	return m, m.subscribeRuntime()
}

func (m *tuiModel) forkSession() (tea.Model, tea.Cmd) {
	branch, err := m.client.ForkSession(context.Background(), m.sessionID)
	if err != nil {
		m.status = "Could not fork this session."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if err := m.bindSessionRuntime(context.Background(), branch.ID); err != nil {
		m.status = "Session was forked, but its runtime could not be opened."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.restoreSessionState(branch)
	m.clearQueue()
	m.planReview = false
	m.pending = false
	m.status = "Forked session history into a new session."
	m.refreshSessionRail(m.sessionID)
	m.resetInput()
	m.refreshPanels()
	m.updateLayout()
	return m, m.subscribeRuntime()
}

func (m *tuiModel) rewindSession(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		m.status = "Use /rewind <n> to remove the latest n user turns."
		return m, nil
	}
	turns, err := strconv.Atoi(fields[1])
	if err != nil || turns <= 0 {
		m.status = "Rewind count must be a positive integer."
		return m, nil
	}
	removed, err := m.client.RewindSession(context.Background(), m.sessionID, turns)
	if err != nil {
		m.status = "Could not rewind this session."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	saved, err := m.client.GetSession(context.Background(), m.sessionID)
	if err != nil {
		m.status = "History was rewound, but the session could not be reloaded."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.restoreSessionState(saved)
	m.status = fmt.Sprintf("Rewound %d user turn(s).", removed)
	m.resetInput()
	m.refreshPanels()
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) restoreSessionState(saved core.Session) {
	m.sessionSettings = saved.Settings
	m.entries = transcriptEntries(saved.History, m.workspace)
	m.printedEntryCount = 0
	m.activities = nil
	m.teamActivities = nil
	m.modelStreamFirstTokenLatency = 0
	m.modelStreamFirstTokenRecorded = false
	m.contextWindow = 0
	m.contextInput = 0
	m.contextEstimated = false
	if saved.Settings.ContextWindow != nil {
		m.contextWindow = *saved.Settings.ContextWindow
	}
	m.usageRequests = make([]tokenUsageRecord, 0, len(saved.UsageRequests))
	for _, record := range saved.UsageRequests {
		m.usageRequests = append(m.usageRequests, tokenUsageRecord{
			request: record.Request, usage: record.Usage, available: record.Available,
			agentID: record.AgentID, profile: record.ModelProfile, provider: record.Provider, model: record.Model,
			contextWindow: record.ContextWindow, estimatedInputTokens: record.EstimatedInputTokens,
		})
	}
	m.agentUsage = make(map[string]agentUsageSummary, len(saved.AgentUsage))
	for agentID, totals := range saved.AgentUsage {
		m.agentUsage[agentID] = usageSummaryFromTotals(totals)
	}
	m.agentUsageComplete = saved.AgentUsageComplete
	for index := len(m.usageRequests) - 1; index >= 0; index-- {
		record := m.usageRequests[index]
		if record.agentID != "" && record.agentID != "lead" {
			continue
		}
		if record.contextWindow > 0 {
			m.contextWindow = record.contextWindow
			if record.usage.InputTokens > 0 {
				m.contextInput = record.usage.InputTokens
				m.contextEstimated = false
			} else if record.estimatedInputTokens > 0 {
				m.contextInput = record.estimatedInputTokens
				m.contextEstimated = true
			} else {
				m.contextInput = 0
				m.contextEstimated = false
			}
		} else if saved.Settings.ContextWindow == nil {
			m.contextWindow = 0
			m.contextInput = 0
			m.contextEstimated = false
		}
		break
	}
	m.taskUsage = loadUsageTotals(saved.TaskUsage)
	m.sessionUsage = loadUsageTotals(saved.SessionUsage)
}

func loadUsageTotals(totals core.UsageTotals) tokenUsageTotals {
	return tokenUsageTotals{requests: totals.Requests, measured: totals.Measured, input: totals.Input, estimatedInput: totals.EstimatedInput,
		output: totals.Output, reasoning: totals.Reasoning, total: totals.Total, cached: totals.Cached}
}

func transcriptFromHistory(history json.RawMessage, workspace ...string) []transcriptEntry {
	items := rawHistoryItems(history)
	var entries []transcriptEntry
	for _, item := range items {
		var value struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Content   json.RawMessage `json:"content"`
		}
		if json.Unmarshal(item, &value) != nil {
			continue
		}
		switch value.Type {
		case "message":
			role := "notice"
			if value.Role == "user" {
				role = "you"
			} else if value.Role == "assistant" {
				role = "assistant"
			}
			if text := messageContentText(value.Content); text != "" {
				entries = append(entries, transcriptEntry{Role: role, Text: text})
			}
		case "function_call":
			arguments := value.Arguments
			if len(arguments) > 0 && arguments[0] == '"' {
				var text string
				_ = json.Unmarshal(arguments, &text)
				arguments = json.RawMessage(text)
			}
			entries = append(entries, transcriptEntry{Role: "tool", Text: humanizedToolSummary(value.Name, arguments, workspace...) + " · done"})
		}
	}
	return entries
}

func transcriptEntries(history json.RawMessage, workspace ...string) []tuiEntry {
	saved := transcriptFromHistory(history, workspace...)
	entries := make([]tuiEntry, 0, len(saved))
	for _, entry := range saved {
		entries = append(entries, tuiEntry{role: entry.Role, text: entry.Text})
	}
	return entries
}

func rawHistoryItems(history json.RawMessage) []json.RawMessage {
	if len(history) == 0 || string(history) == "null" {
		return nil
	}
	var text string
	if json.Unmarshal(history, &text) == nil {
		encoded, _ := json.Marshal(map[string]any{"type": "message", "role": "user", "content": text})
		return []json.RawMessage{encoded}
	}
	var items []json.RawMessage
	_ = json.Unmarshal(history, &items)
	return items
}

func sessionPreview(history json.RawMessage) string {
	for _, entry := range transcriptFromHistory(history) {
		if entry.Role != "you" {
			continue
		}
		preview := strings.Join(strings.Fields(entry.Text), " ")
		if len([]rune(preview)) > 44 {
			preview = string([]rune(preview)[:41]) + "..."
		}
		if preview != "" {
			return preview
		}
	}
	return "empty session"
}

func messageContentText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var parts []json.RawMessage
	if json.Unmarshal(content, &parts) != nil {
		return "[multimodal content]"
	}
	var out strings.Builder
	images := 0
	for _, raw := range parts {
		var part struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		_ = json.Unmarshal(raw, &part)
		switch part.Type {
		case "input_text", "output_text":
			out.WriteString(part.Text)
		case "input_image":
			images++
			if strings.Count(out.String(), "[Image #") < images {
				out.WriteString("[image]")
			}
		case "reasoning_text":
		default:
			out.WriteString("[multimodal content]")
		}
	}
	return out.String()
}

func inputTranscriptText(input core.TaskInput, fallback string) string {
	if len(input.Raw) == 0 {
		return fallback
	}
	var text string
	if json.Unmarshal(input.Raw, &text) == nil {
		return text
	}
	var items []json.RawMessage
	if json.Unmarshal(input.Raw, &items) != nil {
		return "[structured input]"
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		var value struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Name    string          `json:"name"`
			Content json.RawMessage `json:"content"`
		}
		_ = json.Unmarshal(item, &value)
		if value.Type == "message" {
			if content := messageContentText(value.Content); content != "" {
				parts = append(parts, content)
			} else {
				parts = append(parts, "[multimodal content]")
			}
		} else if value.Type == "function_call" {
			parts = append(parts, value.Name+"(tool call)")
		} else {
			parts = append(parts, "[structured input item]")
		}
	}
	if len(parts) == 0 {
		return "[structured input]"
	}
	return strings.Join(parts, "\n")
}

func (m *tuiModel) compact() (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.status = "Conversation compaction is unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	if pending, err := m.hasPending(); err != nil {
		m.status = "Could not inspect interrupted task."
		m.addEntry("error", err.Error(), true)
		return m, nil
	} else if pending {
		m.pending = true
		m.status = "A task is paused in this session; use /continue or start a new message branch."
		m.addEntry("notice", m.status, true)
		return m, nil
	}
	m.resetInput()
	return m.startTask(func(ctx context.Context) taskResult {
		compactedTurns, err := m.client.Compact(ctx, m.sessionID)
		if err != nil {
			return taskResult{err: err}
		}
		if compactedTurns == 0 {
			return taskResult{notice: "No older conversation to compact; the latest four user turns are already retained."}
		}
		return taskResult{notice: fmt.Sprintf("Compacted %d older user turn(s); kept the latest four.", compactedTurns)}
	}, true)
}

func (m *tuiModel) discard() (tea.Model, tea.Cmd) {
	if m.runtime == nil && m.sessionID != "" {
		if err := m.bindSessionRuntime(context.Background(), m.sessionID); err != nil {
			m.status = "Task recovery is unavailable."
			m.addEntry("error", err.Error(), true)
			return m, nil
		}
	}
	if m.runtime == nil {
		m.status = "Task recovery is unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	discarded, err := m.runtime.DiscardPaused(context.Background())
	if err != nil {
		m.status = "Could not discard interrupted task."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.pending = false
	m.status = "No interrupted task to discard."
	if discarded {
		m.status = "Interrupted task discarded."
	}
	m.addEntry("notice", m.status, false)
	m.resetInput()
	m.pendingRequest = nil
	_ = m.refreshRuntimeProjection()
	return m, nil
}

func (m *tuiModel) resetInput() {
	m.input.Reset()
	m.attachments = nil
	m.nextAttachmentID = 1
	m.undoStack = nil
	m.redoStack = nil
	m.updateCommandSuggestions()
	m.updateLayout()
}

func (m *tuiModel) hasPending() (bool, error) {
	if m.runtime == nil && m.sessionID != "" {
		if err := m.bindSessionRuntime(context.Background(), m.sessionID); err != nil {
			return false, err
		}
	}
	if m.runtime == nil {
		return false, nil
	}
	state, err := m.runtime.State(context.Background())
	if err != nil {
		return false, err
	}
	m.pending = state.Task.Status == core.TaskPaused
	return m.pending, nil
}

func (m *tuiModel) startTask(operation func(context.Context) taskResult, resetUsage bool) (tea.Model, tea.Cmd) {
	m.resetModelStream()
	m.modelStreamFirstTokenLatency = 0
	m.modelStreamFirstTokenRecorded = false
	if resetUsage {
		m.taskUsage = tokenUsageTotals{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.running = true
	m.cancelled = false
	m.taskPlanMode = m.planMode
	m.status = "Working… Press Esc to interrupt."
	return m, func() tea.Msg {
		return taskFinishedMsg{result: operation(ctx)}
	}
}

func (m *tuiModel) interrupt() {
	if m.running && m.runtimeTask && m.runtime != nil && m.runningExecutionID != "" && !m.cancelled {
		m.cancelled = true
		m.status = "Interrupt requested…"
		if !m.runtime.Interrupt(m.runningExecutionID) {
			m.status = "Core could not interrupt the current turn."
		}
		return
	}
	if m.running && m.cancel != nil && !m.cancelled {
		m.cancelled = true
		m.status = "Cancelling task…"
		m.cancel()
	}
}

func (m *tuiModel) finishTask(result taskResult) {
	m.running = false
	m.pendingApproval = nil
	m.runtimeTask = false
	if executionID := result.coreResult.ExecutionID; executionID != "" {
		delete(m.activeExecutions, executionID)
		if m.runningExecutionID == executionID {
			m.runningExecutionID = ""
		}
	}
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}

	var responseText string
	var responseErr error
	if result.response != nil {
		responseText, responseErr = responseDisplayText(result.response)
	}
	taskFailed := result.coreResult.Status == core.TaskFailed
	taskCancelled := m.cancelled || result.coreResult.Status == core.TaskCancelled
	streamedFinal := m.finishModelStream(responseText, result.err == nil && !taskFailed && !taskCancelled)
	if responseErr != nil {
		m.addEntry("error", responseErr.Error(), true)
	} else if responseText != "" && !streamedFinal {
		m.addEntry("assistant", responseText, false)
	}
	if result.notice != "" {
		m.addEntry("notice", result.notice, false)
	}
	if result.targetSessionID != "" && result.targetSessionID != m.sessionID {
		saved, err := m.client.GetSession(context.Background(), result.targetSessionID)
		if err != nil {
			m.addEntry("error", "Plan started in a new session, but the session could not be loaded: "+err.Error(), true)
		} else if err := m.bindSessionRuntime(context.Background(), saved.ID); err != nil {
			m.addEntry("error", "Plan started in a new session, but its runtime could not be opened: "+err.Error(), true)
		} else {
			m.restoreSessionState(saved)
			m.status = "Plan implementation moved to a new session."
		}
	}

	pending, pendingErr := m.hasPending()
	if pendingErr != nil {
		m.addEntry("error", "Could not inspect interrupted task: "+pendingErr.Error(), true)
	}
	if taskCancelled {
		if pending {
			m.status = "Task paused. Use /continue to resume or type a new message to branch."
		} else {
			m.status = "Task cancelled."
		}
	} else if result.err != nil || taskFailed {
		m.status = "Task failed."
		if failure := result.coreResult.ModelFailure; failure != nil {
			m.addEntry("error", modelFailureSummary(failure), true)
		} else if result.err != nil {
			m.addEntry("error", result.err.Error(), true)
		} else {
			m.addEntry("error", "Task failed. Core did not provide a failure reason.", true)
		}
	} else if result.coreResult.Status == core.TaskPaused {
		m.status = "Task paused and waiting for a decision. Use /continue to review it."
	} else {
		m.status = "Completed."
	}
	if !taskCancelled && !taskFailed && result.err == nil && result.coreResult.ProposedPlan != nil {
		m.planReview = true
		m.proposedPlan = result.coreResult.ProposedPlan
		m.planSourceExecutionID = result.coreResult.ExecutionID
		m.planAuthorization = core.PlanAuthorization{}
		m.status = "Plan ready · /plan allow-* grants capabilities · /plan implement starts it · type feedback to revise."
	}
	m.cancelled = false
}

func modelFailureSummary(failure *core.ModelFailure) string {
	if failure == nil {
		return "Model request failed."
	}
	detail := "Model request failed: " + string(failure.Category)
	if failure.Code != "" {
		detail += " · " + failure.Code
	}
	if failure.StatusCode != 0 {
		detail += fmt.Sprintf(" · HTTP %d", failure.StatusCode)
	}
	return detail
}

func (m *tuiModel) updateCommandSuggestions() {
	m.commandSuggestions = nil
	m.suggestionIndex = 0
	raw := m.input.Value()
	if strings.ContainsAny(raw, "\r\n") {
		return
	}
	line := strings.TrimSpace(raw)
	if !strings.HasPrefix(line, "/") {
		return
	}
	for _, command := range tuiCommands {
		// Typing "/h" offers /help for completion; the renderer scrolls the
		// full bare-slash list inside a bounded number of rows.
		if line == "/" && command.name == "/help" {
			continue
		}
		if strings.HasPrefix(command.name, line) {
			m.commandSuggestions = append(m.commandSuggestions, command.name)
		}
	}
}

func (m *tuiModel) updateContextCompaction(event core.Event) {
	if event.Compaction == nil {
		return
	}
	details := event.Compaction
	switch details.Stage {
	case "started":
		if details.Mode == "server" {
			m.status = "Provider is compacting the model context…"
		} else {
			m.status = "Compacting context: clearing older tool output and summarizing…"
		}
	case "completed":
		if details.AfterTokens > 0 {
			m.status = fmt.Sprintf("Context compacted: %s → %s estimated tokens.", compactTokens(details.BeforeTokens), compactTokens(details.AfterTokens))
		} else {
			m.status = "Provider compacted the context."
		}
	case "fallback":
		m.status = "Server compaction unavailable; using local compaction."
	case "skipped":
		if strings.Contains(strings.ToLower(event.Err), "contextwindow is unset") {
			m.status = "Auto-compaction needs a context window. Set it with /context window <tokens>."
		}
	case "failed":
		m.status = "Context compaction failed: " + event.Err
	}
}

func (m *tuiModel) updateModelUsage(usage core.Usage, agentID, profile, provider, model string, contextWindow, estimatedInputTokens int64) {
	available := usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.OutputTokensDetails.ReasoningTokens != 0 ||
		usage.TotalTokens != 0 || usage.InputTokensDetails.CachedTokens != 0
	requestNumber := m.sessionUsage.requests + 1
	estimatedInput := int64(0)
	if usage.InputTokens <= 0 && estimatedInputTokens > 0 {
		estimatedInput = estimatedInputTokens
	}
	record := tokenUsageRecord{
		request: requestNumber, usage: usage, available: available,
		agentID: agentID, profile: profile, provider: provider, model: model,
		contextWindow:        contextWindow,
		estimatedInputTokens: estimatedInput,
	}
	if agentID == "" || agentID == "lead" {
		m.contextWindow = contextWindow
		if usage.InputTokens > 0 {
			m.contextInput = usage.InputTokens
			m.contextEstimated = false
		} else if record.estimatedInputTokens > 0 {
			m.contextInput = record.estimatedInputTokens
			m.contextEstimated = true
		} else {
			m.contextInput = 0
			m.contextEstimated = false
		}
	}
	m.taskUsage.add(usage, available)
	m.sessionUsage.add(usage, available)
	m.taskUsage.estimatedInput += record.estimatedInputTokens
	m.sessionUsage.estimatedInput += record.estimatedInputTokens
	m.usageRequests = append(m.usageRequests, record)
	if len(m.usageRequests) > 200 {
		m.usageRequests = append([]tokenUsageRecord(nil), m.usageRequests[len(m.usageRequests)-200:]...)
	}
	if m.agentUsage == nil {
		m.agentUsage = make(map[string]agentUsageSummary)
	}
	summary := m.agentUsage[agentID]
	summary.requests++
	if available {
		summary.measured++
		summary.input += usage.InputTokens
		summary.output += usage.OutputTokens
		summary.reasoning += usage.OutputTokensDetails.ReasoningTokens
		summary.total += usage.TotalTokens
		summary.cached += usage.InputTokensDetails.CachedTokens
	}
	m.agentUsage[agentID] = summary
}

func (totals *tokenUsageTotals) add(usage core.Usage, available bool) {
	totals.requests++
	if !available {
		return
	}
	totals.measured++
	totals.input += usage.InputTokens
	totals.output += usage.OutputTokens
	totals.reasoning += usage.OutputTokensDetails.ReasoningTokens
	totals.total += usage.TotalTokens
	totals.cached += usage.InputTokensDetails.CachedTokens
}

func (record tokenUsageRecord) String() string {
	label := fmt.Sprintf("#%d", record.request)
	var attribution []string
	if record.agentID != "" {
		attribution = append(attribution, record.agentID)
	}
	if record.profile != "" && record.profile != record.agentID {
		attribution = append(attribution, "profile="+record.profile)
	}
	model := record.model
	if record.provider != "" {
		if model == "" {
			model = record.provider
		} else {
			model = record.provider + "/" + model
		}
	}
	if model != "" {
		attribution = append(attribution, model)
	}
	if len(attribution) > 0 {
		label += " · " + strings.Join(attribution, " · ")
	}
	if !record.available {
		label += " · usage unavailable"
		if record.estimatedInputTokens > 0 {
			label += " · input ~" + compactTokens(record.estimatedInputTokens)
		}
		return label
	}
	inputTokens := compactTokens(record.usage.InputTokens)
	if record.usage.InputTokens <= 0 && record.estimatedInputTokens > 0 {
		inputTokens = "~" + compactTokens(record.estimatedInputTokens)
	}
	return fmt.Sprintf("%s · in %s · out %s · reason %s · total %s · cached %s (%s)",
		label,
		inputTokens,
		compactTokens(record.usage.OutputTokens),
		compactTokens(record.usage.OutputTokensDetails.ReasoningTokens),
		compactTokens(record.usage.TotalTokens),
		compactTokens(record.usage.InputTokensDetails.CachedTokens),
		cacheHitRate(record.usage.InputTokens, record.usage.InputTokensDetails.CachedTokens),
	)
}

func (totals tokenUsageTotals) String(label string) string {
	if totals.requests == 0 {
		return fmt.Sprintf("%s: no model requests", label)
	}
	if totals.measured == 0 {
		if totals.estimatedInput > 0 {
			return fmt.Sprintf("%s: usage unavailable (%d request(s)); input estimate ~%s", label, totals.requests, compactTokens(totals.estimatedInput))
		}
		return fmt.Sprintf("%s: usage unavailable (%d request(s))", label, totals.requests)
	}
	result := fmt.Sprintf("%s · in %s · out %s · reason %s · total %s · cache %s (%s)",
		label,
		compactTokens(totals.input),
		compactTokens(totals.output),
		compactTokens(totals.reasoning),
		compactTokens(totals.total),
		compactTokens(totals.cached),
		cacheHitRate(totals.input, totals.cached),
	)
	if totals.estimatedInput > 0 {
		result += " · est input ~" + compactTokens(totals.estimatedInput)
	}
	return result
}

func compactTokens(tokens int64) string {
	if tokens >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1000000)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return fmt.Sprint(tokens)
}

func activityBadge(status string) string {
	switch status {
	case "running":
		return "◉"
	case "done":
		return "✓"
	case "failed":
		return "×"
	case "cancelled":
		return "–"
	default:
		return "·"
	}
}

func cacheHitRate(inputTokens, cachedTokens int64) string {
	if inputTokens <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", float64(cachedTokens)*100/float64(inputTokens))
}

func (m *tuiModel) updateToolActivity(event core.ToolCall) {
	summary := humanizedToolSummary(event.Name, event.Arguments, m.workspace)
	index := -1
	for i := len(m.activities) - 1; i >= 0; i-- {
		if event.ID != "" && m.activities[i].callID == event.ID {
			index = i
			break
		}
	}
	if event.Started {
		activity := tuiActivity{callID: event.ID, summary: summary, status: "running"}
		if index >= 0 {
			m.activities[index] = activity
		} else {
			m.activities = append(m.activities, activity)
			index = len(m.activities) - 1
		}
	} else {
		if index < 0 {
			m.activities = append(m.activities, tuiActivity{callID: event.ID, summary: summary})
			index = len(m.activities) - 1
		}
		activity := &m.activities[index]
		if summary != "" {
			activity.summary = summary
		}
		activity.duration = event.Duration
		switch {
		case event.Canceled:
			activity.status = "cancelled"
		case event.Failed:
			activity.status = "failed"
		default:
			activity.status = "done"
		}
	}

	activity := m.activities[index]
	entryText := activity.summary + " · " + activity.status
	if activity.status != "running" && activity.duration > 0 {
		entryText += " · " + activity.duration.Round(time.Millisecond).String()
	}
	entryIndex := -1
	if event.ID != "" {
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].activityID == event.ID {
				entryIndex = i
				break
			}
		}
	}
	if activity.status != "running" && entryIndex < 0 {
		m.entries = append(m.entries, tuiEntry{
			role: "tool", text: entryText, err: activity.status == "failed", activityID: event.ID,
		})
	}
	if len(m.activities) > 200 {
		m.activities = append([]tuiActivity(nil), m.activities[len(m.activities)-200:]...)
	}
}

func humanizedToolSummary(name string, arguments json.RawMessage, workspace ...string) string {
	var args struct {
		Path string `json:"path"`
		URL  string `json:"url"`
	}
	_ = json.Unmarshal(arguments, &args)
	path := safeDisplayPath(args.Path)
	summary := "Working on a tool request"
	switch name {
	case "read_file":
		summary = summaryWithPath("Reading", path)
	case "write_file":
		summary = summaryWithPath("Writing", path)
	case "edit_file":
		summary = summaryWithPath("Editing", path)
	case "list_dir":
		summary = summaryWithPath("Listing", path)
	case "glob":
		summary = "Finding project files"
	case "grep":
		if path != "" {
			summary = "Searching " + path
		} else {
			summary = "Searching project files"
		}
	case "web_search":
		summary = "Searching the web"
	case "web_fetch":
		if parsed, err := url.Parse(args.URL); err == nil && parsed.Hostname() != "" {
			summary = "Fetching " + parsed.Hostname()
		} else {
			summary = "Fetching a web page"
		}
	case "command":
		summary = "Running a shell command"
	case "html":
		summary = "Processing web content"
	}
	if len(workspace) > 0 {
		summary = absoluteToolSummary(summary, workspace[0])
	}
	return summary
}

func safeDisplayPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) {
		return ""
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(clean)
}

func summaryWithPath(action, path string) string {
	if path == "" {
		return action + " a workspace file"
	}
	return action + " " + path
}

func absoluteToolSummary(summary, workspace string) string {
	if workspace == "" {
		return summary
	}
	for _, prefix := range []string{"Reading ", "Writing ", "Editing ", "Listing ", "Searching "} {
		if !strings.HasPrefix(summary, prefix) {
			continue
		}
		relative := strings.TrimSpace(strings.TrimPrefix(summary, prefix))
		if relative == "" || relative == "a workspace file" || relative == "project files" || filepath.IsAbs(relative) {
			return summary
		}
		absolute := filepath.Clean(filepath.Join(workspace, filepath.FromSlash(relative)))
		inside, err := filepath.Rel(workspace, absolute)
		if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
			return summary
		}
		return prefix + filepath.ToSlash(absolute)
	}
	return summary
}

func cleanInlineText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\\r\\n")
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, "\r", "\\r")
	value = strings.ReplaceAll(value, "\t", "\\t")
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, value)
}

func (m *tuiModel) addEntry(role, text string, isError bool) {
	if role == "notice" && len(m.entries) > 0 {
		last := m.entries[len(m.entries)-1]
		if last.role == "notice" && last.text == text {
			return
		}
	}
	m.entries = append(m.entries, tuiEntry{role: role, text: text, err: isError})
	m.refreshPanels()
}

func (m *tuiModel) refreshPanels() {
	m.activity.SetContent(m.activityDetailsContent())
}

func (m *tuiModel) activityDetailsContent() string {
	colors := m.themeColors()
	var activities strings.Builder
	if len(m.activities) == 0 {
		activities.WriteString("No tool activity yet.")
	}
	for i, activity := range m.activities {
		if i > 0 {
			activities.WriteByte('\n')
		}
		icon := activityBadge(activity.status)
		line := lipgloss.NewStyle().Foreground(lipgloss.Color(activityColor(activity.status, colors))).Render(icon) + " " + activity.summary + " · " + activity.status
		if activity.status != "running" && activity.duration > 0 {
			line += "  " + activity.duration.Round(time.Millisecond).String()
		}
		activities.WriteString(line)
	}
	activities.WriteString("\n\nLead agent · " + stateName(m.visualState()))
	if m.modelStreamFirstTokenRecorded {
		activities.WriteString("\nFirst token · " + m.modelStreamFirstTokenLatency.Round(time.Millisecond).String())
	}
	if teamDetails := m.teamActivityDetails(colors); teamDetails != "" {
		activities.WriteString("\n\n" + teamDetails)
	}
	if contextLabel := contextUsageLabel(m.contextInput, m.contextWindow, m.contextEstimated); contextLabel != "" {
		activities.WriteString("\n\nContext · " + contextLabel)
		if m.contextEstimated {
			activities.WriteString(" · estimate; image contents excluded")
		}
	}
	paths := m.recentWorkspacePaths(5)
	if len(paths) > 0 {
		activities.WriteString("\nRecent workspace paths")
		for _, path := range paths {
			activities.WriteString("\n  " + path)
		}
	}
	activities.WriteString("\n\nModel usage · input / output / reasoning / total / cached")
	if len(m.usageRequests) == 0 {
		activities.WriteString("\nNo model requests yet.")
	} else {
		for _, record := range m.usageRequests[max(0, len(m.usageRequests)-6):] {
			activities.WriteByte('\n')
			activities.WriteString(record.String())
		}
	}
	if byAgent := m.usageByAgentDetails(); byAgent != "" {
		activities.WriteString("\n\n")
		activities.WriteString(byAgent)
	}
	activities.WriteByte('\n')
	activities.WriteString(m.taskUsage.String("Task"))
	activities.WriteByte('\n')
	activities.WriteString(m.sessionUsage.String("Session"))
	activities.WriteString(fmt.Sprintf("\nMetered requests: task %d/%d · session %d/%d", m.taskUsage.measured, m.taskUsage.requests, m.sessionUsage.measured, m.sessionUsage.requests))
	if m.client != nil {
		activities.WriteString("\nPermissions: " + permissionModeLabel(m.currentPermissionMode()))
	}
	return activities.String()
}

func activityColor(status string, colors themeColors) string {
	switch status {
	case "running":
		return colors.Accent
	case "done":
		return colors.Success
	case "failed", "cancelled":
		return colors.Error
	default:
		return colors.Muted
	}
}

func (m *tuiModel) recentWorkspacePaths(limit int) []string {
	seen := make(map[string]struct{})
	var paths []string
	for index := len(m.activities) - 1; index >= 0 && len(paths) < limit; index-- {
		path := activityPath(m.activities[index].summary)
		if path == "" {
			continue
		}
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func activityPath(summary string) string {
	for _, prefix := range []string{"Reading ", "Writing ", "Editing ", "Listing ", "Searching "} {
		if strings.HasPrefix(summary, prefix) {
			path := strings.TrimSpace(strings.TrimPrefix(summary, prefix))
			if filepath.IsAbs(path) {
				return path
			}
		}
	}
	return ""
}

func (m *tuiModel) updateLayout() {
	if m.width <= 0 {
		m.width = 80
	}
	if m.height <= 0 {
		m.height = 24
	}
	contentWidth := max(1, m.width)
	if m.modernConversation() {
		wide := m.wideConversation()
		inputWidth, footerHeight, dividerCount := contentWidth, 2, 1
		if wide {
			inputWidth = max(1, contentWidth-m.sessionRailWidth()-1)
			footerHeight, dividerCount = 1, 2
		}
		m.input.SetWidth(inputWidth)
		m.updateMCPInteractionWidth(inputWidth)
		bodyHeight := m.availableBodyHeight(contentWidth, footerHeight, dividerCount)
		m.chat.SoftWrap = false
		m.chat.SetWidth(inputWidth)
		drawerHeight := 0
		if m.detailsOpen {
			drawerHeight = activityDrawerContentHeight(bodyHeight)
		}
		chatHeight := bodyHeight
		if drawerHeight > 0 {
			chatHeight = max(1, bodyHeight-drawerHeight-1)
		}
		m.chat.SetHeight(chatHeight)
		m.activity.SetWidth(inputWidth)
		if drawerHeight > 0 {
			m.activity.SetHeight(drawerHeight - 1)
		} else {
			m.activity.SetHeight(max(1, bodyHeight))
		}
		return
	}
	m.input.SetWidth(contentWidth)
	m.updateMCPInteractionWidth(contentWidth)
	bodyHeight := m.availableBodyHeight(contentWidth, 2, 1)
	if m.detailsOpen {
		m.chat.SetWidth(contentWidth)
		m.chat.SetHeight(bodyHeight)
		m.activity.SetWidth(max(1, contentWidth-2))
		m.activity.SetHeight(max(1, bodyHeight-4))
		return
	}
	m.chat.SetWidth(contentWidth)
	m.chat.SetHeight(bodyHeight)
	m.activity.SetWidth(max(1, contentWidth-2))
	m.activity.SetHeight(max(1, bodyHeight-4))
}

func (m *tuiModel) availableBodyHeight(contentWidth, footerHeight, dividerCount int) int {
	bodyHeight := max(1, m.height-lipgloss.Height(m.renderHeader(contentWidth))-dividerCount-m.input.Height()-m.commandSuggestionHeight()-footerHeight)
	if m.pendingMCPInteraction != nil {
		bodyHeight = max(1, bodyHeight-lipgloss.Height(m.renderMCPInteraction()))
	}
	if m.keymapOpen {
		bodyHeight = max(1, bodyHeight-m.keymapEditorHeight())
	}
	return bodyHeight
}

func applyTextareaTheme(input *textarea.Model, theme themeName) {
	colors := colorsFor(theme)
	styles := textarea.DefaultStyles(theme != themeLight)
	apply := func(state *textarea.StyleState) {
		state.Base = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Foreground)).Background(lipgloss.Color(colors.Background))
		state.Text = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Foreground))
		state.LineNumber = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))
		state.CursorLineNumber = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))
		state.CursorLine = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Foreground)).Background(lipgloss.Color(colors.Background))
		state.EndOfBuffer = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))
		state.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))
		state.Prompt = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent))
		state.Selection = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Background)).Background(lipgloss.Color(colors.Secondary))
	}
	apply(&styles.Focused)
	apply(&styles.Blurred)
	styles.Cursor.Color = lipgloss.Color(colors.Accent)
	input.SetStyles(styles)
}

func (m *tuiModel) commandSuggestionHeight() int {
	if m.planReview {
		return 2
	}
	if m.queuePicker {
		return min(len(m.queued), 8) + 1
	}
	if m.sessionPicker {
		if m.renamingSession || m.deleteConfirm {
			return 2
		}
		return min(len(m.sessionList), 8) + 2
	}
	if m.modelPicker {
		return min(len(m.modelOptions), 8) + 2
	}
	if m.effortPicker {
		return len(reasoningEffortOptions) + 2
	}
	if m.permissionMenu {
		extraLines := 0
		if m.planMode {
			extraLines = 1
		}
		return len(tuiPermissionOptions) + 2 + extraLines
	}
	if len(m.commandSuggestions) == 0 {
		return 0
	}
	return min(len(m.commandSuggestions), maxCommandSuggestions) + 1
}

func (m *tuiModel) View() tea.View {
	width := max(1, m.width)
	colors := m.themeColors()
	var before []string
	if len(m.entries) == 0 && !m.running && strings.TrimSpace(m.input.Value()) == "" {
		before = append(before, lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render("Lune"))
		before = append(before, "Ready to work in "+m.workspace+".")
		before = append(before, lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render("Use /help to see commands."))
	}
	if live := m.liveOutput(); live != "" {
		limit := max(2, m.height-9)
		before = append(before, tailTerminalLines(live, limit))
	}
	if m.detailsOpen {
		before = append(before, "Activity & usage\n"+tailTerminalLines(m.activityDetailsContent(), min(16, max(4, m.height/2))))
	}
	if auxiliary := m.renderAuxiliaryPanel(m.renderCommandSuggestions()); auxiliary != "" {
		before = append(before, auxiliary)
	}
	input := m.renderInputView()
	content := strings.Join(before, "\n")
	if content != "" {
		content += "\n"
	}
	content += input
	footer := m.renderInlineFooter(width, colors)
	content += "\n" + footer
	view := tea.NewView(content)
	if m.preferences.Theme == themeAuto {
		view.BackgroundColor = m.terminalBackground
		view.ForegroundColor = m.terminalForeground
	} else {
		view.BackgroundColor = backgroundColor(colors.Background)
		view.ForegroundColor = backgroundColor(colors.Foreground)
	}
	if cursor := m.activeCursor(); cursor != nil && !m.keymapOpen && (!m.sessionPicker || m.renamingSession) && !m.queuePicker && !m.permissionMenu && !m.modelPicker && !m.effortPicker {
		if len(before) > 0 {
			cursor.Y += lipgloss.Height(strings.Join(before, "\n")) + 1
		}
		view.Cursor = cursor
	}
	return view
}

func (m *tuiModel) liveOutput() string {
	var lines []string
	indices := m.modelStreamCurrent
	if len(indices) == 0 && m.runtimeTask {
		indices = m.modelStreamLast
	}
	seen := make(map[int]bool, len(indices))
	for _, index := range indices {
		if index >= 0 && index < len(m.entries) && !seen[index] {
			seen[index] = true
			lines = append(lines, plainConversationText(m.entries[index].text))
		}
	}
	if len(lines) > 0 {
		return strings.Join(lines, "\n")
	}
	for index := len(m.activities) - 1; index >= 0; index-- {
		if m.activities[index].status == "running" {
			return m.activities[index].summary + "…"
		}
	}
	return ""
}

func tailTerminalLines(content string, limit int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > limit {
		lines = append([]string{"…"}, lines[len(lines)-limit+1:]...)
	}
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderInlineFooter(width int, colors themeColors) string {
	model := m.sessionSettings.ModelProfile
	if model == "" {
		model = "default model"
	}
	effort := reasoningEffortLabel(m.sessionSettings.ReasoningEffort)
	mode := permissionModeLabel(core.PermissionReadOnly)
	if m.client != nil {
		mode = permissionModeLabel(m.currentPermissionMode())
	}
	left := model + " · " + effort + " · " + mode
	if context := contextUsageLabel(m.contextInput, m.contextWindow, m.contextEstimated); context != "" {
		left += " · " + context
	}
	status := m.status
	if status == "Ready" {
		status = m.keyLabel("send") + " send · " + m.keyLabel("input_newline") + " newline · /plan · /permissions"
	} else if m.running {
		status = "Working · " + m.keyLabel("interrupt_or_copy") + " interrupt · /queue"
	} else if m.pending {
		status = "Paused · /continue to review · /discard"
	}
	usage := "tokens — · cache —"
	if m.taskUsage.requests > 0 {
		usage = fmt.Sprintf("in %d · out %d · reason %d · total %d · cache %s",
			m.taskUsage.input, m.taskUsage.output, m.taskUsage.reasoning, m.taskUsage.total,
			cacheHitRate(m.taskUsage.input, m.taskUsage.cached))
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted))
	return style.Render(fitTerminalText(left+"   "+status, width) + "\n" + fitTerminalText(usage, width))
}

func (m *tuiModel) flushScrollback() tea.Cmd {
	if m.printedEntryCount > len(m.entries) {
		m.printedEntryCount = 0
	}
	if m.scrollbackRedrawPending || m.hasTransientPanel() ||
		(m.preferences.Theme == themeAuto && !m.terminalColorsReady) {
		return nil
	}
	limit := len(m.entries)
	for _, index := range m.modelStreamCurrent {
		if index >= m.printedEntryCount && index < limit {
			limit = index
		}
	}
	if m.runtimeTask {
		for _, index := range m.modelStreamLast {
			if index >= m.printedEntryCount && index < limit {
				limit = index
			}
		}
	}
	if limit <= m.printedEntryCount {
		return nil
	}
	commands := make([]tea.Cmd, 0, limit-m.printedEntryCount)
	for _, entry := range m.entries[m.printedEntryCount:limit] {
		text := formatScrollbackEntryWithColors(entry, m.width, m.themeColors())
		commands = append(commands, tea.Println(text))
	}
	m.printedEntryCount = limit
	return tea.Sequence(commands...)
}

func (m *tuiModel) hasTransientPanel() bool {
	return m.detailsOpen || m.exitConfirm || m.keymapOpen || m.sessionPicker ||
		m.modelPicker || m.effortPicker || m.queuePicker || m.planReview ||
		m.permissionMenu || m.pendingApproval != nil || m.pendingRequest != nil ||
		m.pendingMCPInteraction != nil || len(m.commandSuggestions) > 0
}

func (m *tuiModel) renderAuxiliaryPanel(suggestions string) string {
	switch {
	case m.exitConfirm:
		return "Interrupt and cancel the queued turns [i] · wait for completion [w] · return [esc]"
	case m.pendingMCPInteraction != nil:
		return m.renderMCPInteraction()
	case m.keymapOpen:
		return m.renderKeymapEditor()
	case m.sessionPicker:
		return m.renderSessionPicker()
	case m.modelPicker:
		return m.renderModelPicker()
	case m.effortPicker:
		return m.renderEffortPicker()
	case m.queuePicker:
		return m.renderQueuePicker()
	case m.planReview:
		return m.renderPlanReview()
	case m.permissionMenu:
		return m.renderPermissionMenu()
	default:
		return suggestions
	}
}

func (m *tuiModel) renderSessionWorkspace(width int) tea.View {
	colors := m.themeColors()
	header := m.renderHeader(width)
	headerRule := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(strings.Repeat("─", width))
	body := m.renderSessionWorkspaceBody(width)
	centerWidth := max(1, m.chat.Width())
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(strings.Repeat("─", centerWidth))
	input := m.renderWorkspaceRow(m.renderInputView(), centerWidth)
	suggestions := m.renderCommandSuggestions()
	parts := []string{header, headerRule, body, m.renderWorkspaceRow(divider, centerWidth), input}
	if auxiliary := m.renderAuxiliaryPanel(suggestions); auxiliary != "" {
		parts = append(parts, m.renderWorkspaceRow(auxiliary, centerWidth))
	}
	parts = append(parts, m.renderWorkspaceRow(m.renderFooter(centerWidth), centerWidth))
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	view := tea.NewView(content)
	view.BackgroundColor = backgroundColor(colors.Background)
	view.ForegroundColor = backgroundColor(colors.Foreground)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	if cursor := m.activeCursor(); cursor != nil && !m.keymapOpen && (!m.sessionPicker || m.renamingSession) && !m.queuePicker && !m.permissionMenu && !m.modelPicker && !m.effortPicker {
		cursor.Y += lipgloss.Height(header) + lipgloss.Height(headerRule) + lipgloss.Height(body) + lipgloss.Height(divider)
		cursor.X += m.sessionRailWidth() + 1
		view.Cursor = cursor
	}
	return view
}

func (m *tuiModel) renderWorkspaceRow(content string, centerWidth int) string {
	colors := m.themeColors()
	separator := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render("│")
	leftPadding := strings.Repeat(" ", m.sessionRailWidth())
	content = lipgloss.NewStyle().Width(centerWidth).Render(content)
	lines := strings.Split(content, "\n")
	rows := make([]string, len(lines))
	for index, line := range lines {
		if lipgloss.Width(line) < centerWidth {
			line += strings.Repeat(" ", centerWidth-lipgloss.Width(line))
		} else if lipgloss.Width(line) > centerWidth {
			line = fitTerminalText(line, centerWidth)
		}
		rows[index] = leftPadding + separator + line
	}
	return strings.Join(rows, "\n")
}

func (m *tuiModel) renderHeader(width int) string {
	colors := m.themeColors()
	if m.modernConversation() {
		state := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(stateName(m.visualState()))
		brand := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Foreground)).Render("☾ Lune")
		left := brand + " · " + state
		if !m.wideConversation() {
			left = brand + "  ‹ " + fitTerminalText(m.currentSessionTitle(), max(12, width/2)) + " › · " + state
		}
		right := filepath.ToSlash(m.workspace)
		maxRight := max(0, width-lipgloss.Width(left)-4)
		if maxRight == 0 {
			right = ""
		} else if lipgloss.Width(filepath.Base(right)) <= maxRight && lipgloss.Width(right) > maxRight {
			right = filepath.Base(right)
		} else {
			right = fitTerminalText(right, maxRight)
		}
		if right == "" {
			return left
		}
		gap := max(1, width-lipgloss.Width(left)-lipgloss.Width(right))
		return left + strings.Repeat(" ", gap) + lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(right)
	}
	headerText := "☾ Lune Agent"
	headerText += " · " + stateName(m.visualState())
	if m.sessionID != "" {
		headerText += "  ·  " + m.sessionID[:min(8, len(m.sessionID))]
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(headerText)
}

func (m *tuiModel) renderFooter(width int) string {
	colors := m.themeColors()
	if m.modernConversation() {
		metrics := ""
		if !m.wideConversation() {
			if m.taskUsage.requests > 0 {
				metrics = fmt.Sprintf("in %d · out %d · reason %d · cache %s",
					m.taskUsage.input, m.taskUsage.output, m.taskUsage.reasoning,
					cacheHitRate(m.taskUsage.input, m.taskUsage.cached))
			} else {
				metrics = "tokens — · cache —"
			}
		}
		status := m.status
		switch {
		case m.detailsOpen:
			status = "Activity & usage · " + m.keyLabel("details_scroll_up") + "/" + m.keyLabel("details_scroll_down") + " scroll · " + m.keyLabel("toggle_activity") + " or Esc close"
		case m.pendingMCPInteraction != nil:
			status = m.mcpInteractionFooter()
		case m.mcpAuthCancel != nil:
			status = m.mcpAuthFooter()
		case m.pendingApproval != nil:
			status = "Approval required · " + m.keyLabel("approve_tool") + " allow once · " + m.keyLabel("deny_tool") + " deny · Esc pause"
		case m.running:
			status = fmt.Sprintf("Working · %d queued · Esc pause · %s interrupt/send", len(m.queued), m.keyLabel("interrupt_send"))
		case m.pending:
			status = "Paused task · /continue to resume"
		case m.status == "Ready":
			status = m.keyLabel("send") + " send · " + m.keyLabel("input_newline") + " newline · " + m.keyLabel("cycle_permissions") + " permissions · /plan · " + m.keyLabel("toggle_activity") + " activity"
			if !m.wideConversation() {
				status = "/resume sessions · " + status
			}
		}
		if m.running && m.modelStreamFirstTokenRecorded {
			status += " · first token " + m.modelStreamFirstTokenLatency.Round(time.Millisecond).String()
		}
		if !m.chat.AtBottom() && !m.detailsOpen {
			status += " · " + m.keyLabel("jump_chat_bottom") + " back to latest"
		}
		if contextLabel := contextUsageLabel(m.contextInput, m.contextWindow, m.contextEstimated); contextLabel != "" {
			if m.wideConversation() {
				status = contextLabel + " · " + status
			} else {
				metrics = contextLabel + " · " + metrics
			}
		}
		if len(m.attachments) > 0 {
			status += fmt.Sprintf(" · %d image(s) · Ctrl+V add · edit a tag to remove", len(m.attachments))
		}
		status = fitTerminalText(status, width)
		if metrics == "" {
			return lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(status)
		}
		metrics = fitTerminalText(metrics, width)
		return lipgloss.JoinVertical(lipgloss.Left,
			lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(metrics),
			lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(status),
		)
	}
	mode := "BUILD"
	if m.planMode {
		mode = "PLAN · read-only tools"
	}
	metrics := mode
	metrics += " · " + m.themePreferenceLabel()
	if m.client != nil {
		metrics += " · " + permissionModeLabel(m.currentPermissionMode())
	}
	if m.sessionSettings.ModelProfile != "" {
		metrics += " · " + m.sessionSettings.ModelProfile
	}
	if m.sessionSettings.ReasoningEffort == "" {
		metrics += " · effort default"
	} else {
		metrics += " · effort " + m.sessionSettings.ReasoningEffort
	}
	if m.taskUsage.requests > 0 {
		metrics += fmt.Sprintf(" · in %d · out %d · reason %d · cache %s",
			m.taskUsage.input, m.taskUsage.output, m.taskUsage.reasoning,
			cacheHitRate(m.taskUsage.input, m.taskUsage.cached))
	} else {
		metrics += " · tokens — · cache —"
	}
	status := m.status
	switch {
	case m.detailsOpen:
		status = "Activity & usage · " + m.keyLabel("details_scroll_up") + "/" + m.keyLabel("details_scroll_down") + " scroll · " + m.keyLabel("toggle_activity") + " or Esc close"
	case m.pendingMCPInteraction != nil:
		status = m.mcpInteractionFooter()
	case m.mcpAuthCancel != nil:
		status = m.mcpAuthFooter()
	case m.pendingApproval != nil:
		status = "Approval required · " + m.keyLabel("approve_tool") + " allow once · " + m.keyLabel("deny_tool") + " deny · Esc pause"
	case m.running:
		status = fmt.Sprintf("Working · %d queued · Esc pause · %s interrupt/send", len(m.queued), m.keyLabel("interrupt_send"))
	case m.pending:
		status = "Paused task · /continue to resume"
	case m.status == "Ready":
		status = fmt.Sprintf("/ commands · %s send · %s permissions · /plan planning · %s activity", m.keyLabel("send"), m.keyLabel("cycle_permissions"), m.keyLabel("toggle_activity"))
	}
	if m.vimMode {
		vimState := "VIM INSERT"
		if m.vimNormal {
			vimState = "VIM NORMAL"
		}
		status = vimState + " · " + status
	}
	if len(m.attachments) > 0 {
		status += fmt.Sprintf(" · %d image(s) · Ctrl+V add · edit a tag to remove", len(m.attachments))
	}
	status = fitTerminalText(status, width)
	metrics = fitTerminalText(metrics, width)
	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(metrics),
		lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(status),
	)
}

func fitTerminalText(value string, width int) string {
	if width <= 0 || lipgloss.Width(value) <= width {
		return value
	}
	runes := []rune(value)
	for len(runes) > 0 && lipgloss.Width(string(runes)+"…") > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}

func (m *tuiModel) renderQueuePicker() string {
	colors := m.themeColors()
	lines := []string{"Core queue · " + m.keyLabel("picker_previous") + "/" + m.keyLabel("picker_next") + " select · " + m.keyLabel("send") + " details · " + m.keyLabel("queue_remove") + " cancel · Esc close"}
	start := max(0, m.queueIndex-4)
	end := min(len(m.queued), start+8)
	if end == len(m.queued) {
		start = max(0, end-8)
	}
	for index, item := range m.queued[start:end] {
		index += start
		line := fmt.Sprintf("  %d. %-8s execution %s", item.Position, item.Status, item.ExecutionID)
		if index == m.queueIndex {
			line = "›" + strings.TrimPrefix(line, " ")
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderPlanReview() string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(m.themeColors().Warning)).Render(
		"Plan review · Enter accepts · type feedback and Enter to revise · Esc cancels",
	)
}

func (m *tuiModel) renderCommandSuggestions() string {
	colors := m.themeColors()
	if m.permissionMenu || len(m.commandSuggestions) == 0 {
		return ""
	}
	start, end := commandSuggestionRange(m.suggestionIndex, len(m.commandSuggestions))
	lines := make([]string, 0, end-start+1)
	for index := start; index < end; index++ {
		command := m.commandSuggestions[index]
		line := "  " + command
		if index == m.suggestionIndex {
			line = "› " + command
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	hint := "  " + m.keyLabel("suggestion_previous") + "/" + m.keyLabel("suggestion_next") + " select · " + m.keyLabel("send") + " run"
	if len(m.commandSuggestions) > maxCommandSuggestions {
		hint = fmt.Sprintf("  %d–%d of %d · %s", start+1, end, len(m.commandSuggestions), strings.TrimPrefix(hint, "  "))
	}
	lines = append(lines, hint)
	return strings.Join(lines, "\n")
}

func commandSuggestionRange(index, total int) (int, int) {
	visible := min(total, maxCommandSuggestions)
	if visible <= 0 {
		return 0, 0
	}
	index = max(0, min(index, total-1))
	start := max(0, min(index-visible/2, total-visible))
	return start, start + visible
}

func (m *tuiModel) renderKeymapEditor() string {
	colors := m.themeColors()
	count := len(keymapActions) + 1
	rows := m.keymapEditorRows()
	start := max(0, m.keymapIndex-rows/2)
	end := min(count, start+rows)
	if end == count {
		start = max(0, end-rows)
	}
	heading := "Key bindings · ↑/↓ choose · Enter change · Esc apply and close"
	if m.keymapCapture != "" {
		heading = "Recording a binding · press one key combination · Esc cancels capture"
	}
	lines := []string{heading}
	for index := start; index < end; index++ {
		var label, chord string
		if index == len(keymapActions) {
			label, chord = "Restore defaults", ""
		} else {
			action := keymapActions[index]
			label = action.label
			chord = m.keymapDraft[action.id]
		}
		line := fmt.Sprintf("  %-38s %s", label, chord)
		if index == m.keymapIndex {
			line = "›" + strings.TrimPrefix(line, " ")
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	if m.keymapCapture == "" {
		lines = append(lines, "Esc is reserved for cancel/close · bindings apply when this menu closes")
	}
	return strings.Join(lines, "\n")
}

func (m *tuiModel) keymapEditorRows() int {
	return min(9, max(1, m.height-10))
}

func (m *tuiModel) keymapEditorHeight() int {
	return m.keymapEditorRows() + 2
}

func (m *tuiModel) renderSessionPicker() string {
	colors := m.themeColors()
	if m.renamingSession {
		return strings.Join([]string{"Rename session · Enter save · empty clears · Esc cancel", "Title is limited to 120 characters."}, "\n")
	}
	if m.deleteConfirm {
		if m.sessionIndex >= 0 && m.sessionIndex < len(m.sessionList) {
			return strings.Join([]string{
				"Delete “" + sessionDisplayTitle(m.sessionList[m.sessionIndex]) + "” and all saved task state?",
				m.keyLabel("session_confirm_delete") + " confirm · " + m.keyLabel("session_cancel_delete") + "/Esc cancel",
			}, "\n")
		}
		return m.keyLabel("session_confirm_delete") + " confirm · " + m.keyLabel("session_cancel_delete") + "/Esc cancel"
	}
	if len(m.sessionList) == 0 {
		if m.showArchived {
			return "No saved sessions · " + m.keyLabel("session_toggle_archived") + " toggle · Esc cancel"
		}
		return "No active sessions · " + m.keyLabel("session_toggle_archived") + " show archived · Esc cancel"
	}
	viewKind := "active"
	if m.showArchived {
		viewKind = "active + archived"
	}
	lines := []string{"Sessions · " + viewKind + " · " + m.keyLabel("picker_previous") + "/" + m.keyLabel("picker_next") + " select · " + m.keyLabel("send") + " restore · " + m.keyLabel("session_archive") + " archive · " + m.keyLabel("session_rename") + " rename · " + m.keyLabel("session_delete") + " delete · " + m.keyLabel("session_toggle_archived") + " toggle"}
	start := max(0, m.sessionIndex-4)
	end := min(len(m.sessionList), start+8)
	if end == len(m.sessionList) {
		start = max(0, end-8)
	}
	for index, saved := range m.sessionList[start:end] {
		index += start
		preview := sessionDisplayTitle(saved)
		line := fmt.Sprintf("  %s  %s", saved.UpdatedAt.Local().Format("01-02 15:04"), preview)
		if saved.Archived {
			line += "  [archived]"
		}
		if saved.ID == m.sessionID {
			line += "  (current)"
		}
		if index == m.sessionIndex {
			line = "›" + strings.TrimPrefix(line, " ")
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func sessionDisplayTitle(saved core.Session) string {
	if title := strings.TrimSpace(saved.Title); title != "" {
		return title
	}
	return sessionPreview(saved.History)
}

func (m *tuiModel) renderModelPicker() string {
	colors := m.themeColors()
	lines := []string{"Model profile · current: " + m.sessionSettings.ModelProfile}
	start := max(0, m.modelIndex-4)
	end := min(len(m.modelOptions), start+8)
	if end == len(m.modelOptions) {
		start = max(0, end-8)
	}
	for index, option := range m.modelOptions[start:end] {
		index += start
		line := fmt.Sprintf("  %-20s %s / %s", option.Profile, option.Provider, option.Model)
		if index == m.modelIndex {
			line = "›" + strings.TrimPrefix(line, " ")
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "  "+m.keyLabel("picker_previous")+"/"+m.keyLabel("picker_next")+" select · "+m.keyLabel("send")+" apply · Esc cancel")
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderEffortPicker() string {
	colors := m.themeColors()
	lines := []string{"Reasoning effort · current: " + reasoningEffortLabel(m.sessionSettings.ReasoningEffort)}
	for index, value := range reasoningEffortOptions {
		line := "  " + reasoningEffortLabel(value)
		if index == m.effortIndex {
			line = "› " + reasoningEffortLabel(value)
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "  "+m.keyLabel("picker_previous")+"/"+m.keyLabel("picker_next")+" select · "+m.keyLabel("send")+" apply · Esc cancel")
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderPermissionMenu() string {
	colors := m.themeColors()
	mode := core.PermissionReadOnly
	if m.client != nil {
		mode = m.currentPermissionMode()
	}
	lines := []string{"Permission mode · current: " + permissionModeLabel(mode)}
	if m.planMode {
		lines = append(lines, "Plan caps tools at read-only; permission controls approval for visible tools such as web.")
	}
	for index, option := range tuiPermissionOptions {
		line := fmt.Sprintf("  %-12s %s", option.label, option.description)
		if index == m.permissionIndex {
			line = fmt.Sprintf("› %-12s %s", option.label, option.description)
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "  "+m.keyLabel("picker_previous")+"/"+m.keyLabel("picker_next")+" select · "+m.keyLabel("send")+" apply · Esc cancel")
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderBody(width int) string {
	colors := m.themeColors()
	if m.modernConversation() {
		if m.wideConversation() {
			return m.renderSessionWorkspaceBody(width)
		}
		if m.detailsOpen {
			bodyHeight := m.availableBodyHeight(width, 2, 1)
			drawerHeight := activityDrawerContentHeight(bodyHeight)
			if drawerHeight == 0 {
				return m.activity.View()
			}
			divider := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(strings.Repeat("─", max(1, width)))
			return lipgloss.JoinVertical(lipgloss.Left, m.chat.View(), divider, m.activityDrawerHeading(), m.activity.View())
		}
		return m.chat.View()
	}
	if m.detailsOpen {
		bodyHeight := max(1, m.chat.Height())
		if bodyHeight < 5 {
			return m.activity.View()
		}
		title := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render("Activity & usage")
		hint := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(m.keyLabel("details_scroll_up") + "/" + m.keyLabel("details_scroll_down") + " scroll · " + m.keyLabel("toggle_activity") + " or Esc close")
		content := lipgloss.JoinVertical(lipgloss.Left, title, m.activity.View(), hint)
		return lipgloss.NewStyle().Width(max(1, width-2)).Height(bodyHeight - 2).
			Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(colors.Accent)).Render(content)
	}
	return m.chat.View()
}

func (m *tuiModel) renderSessionWorkspaceBody(width int) string {
	colors := m.themeColors()
	bodyHeight := m.availableBodyHeight(width, 1, 2)
	railWidth := m.sessionRailWidth()
	centerWidth := max(1, width-railWidth-1)
	center := m.chat.View()
	drawerHeight := activityDrawerContentHeight(bodyHeight)
	if m.detailsOpen {
		if drawerHeight == 0 {
			center = m.activity.View()
		} else {
			divider := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(strings.Repeat("─", centerWidth))
			center = lipgloss.JoinVertical(lipgloss.Left, center, divider, m.activityDrawerHeading(), m.activity.View())
		}
	}
	rail := m.renderSessionRail(railWidth, bodyHeight)
	railLines := fitWorkspaceColumn(rail, railWidth, bodyHeight)
	centerLines := fitWorkspaceColumn(center, centerWidth, bodyHeight)
	separator := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render("│")
	rows := make([]string, bodyHeight)
	for row := range rows {
		rows[row] = railLines[row] + separator + centerLines[row]
	}
	return strings.Join(rows, "\n")
}

func (m *tuiModel) activityDrawerHeading() string {
	colors := m.themeColors()
	title := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render("Activity & usage")
	hint := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(
		m.keyLabel("details_scroll_up") + "/" + m.keyLabel("details_scroll_down") + " scroll · " + m.keyLabel("toggle_activity") + " close",
	)
	return title + "  " + hint
}

func fitWorkspaceColumn(content string, width, height int) []string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	for index, line := range lines {
		if lipgloss.Width(line) > width {
			line = fitTerminalText(line, width)
		}
		lines[index] = line + strings.Repeat(" ", max(0, width-lipgloss.Width(line)))
	}
	return lines
}

func responseDisplayText(response json.RawMessage) (string, error) {
	if len(response) == 0 {
		return "", nil
	}
	var envelope struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	lines := make([]string, 0, len(envelope.Output))
	for _, item := range envelope.Output {
		var value struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(item, &value); err != nil {
			return "", err
		}
		switch value.Type {
		case "message":
			if text := messageContentText(value.Content); text != "" {
				lines = append(lines, text)
			}
		case "reasoning", "function_call", "custom_tool_call":
		default:
			lines = append(lines, string(item))
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}
