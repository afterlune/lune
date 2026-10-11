package tui

import (
	"context"
	"encoding/json"
	"fmt"
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

	"github.com/afterlune/lune/core"
)

type tuiEntry struct {
	role        string
	text        string
	err         bool
	incomplete  bool
	activityID  string
	collapseKey string
}

type transcriptEntry struct {
	Role string
	Text string
}

type queuedInput struct {
	text        string
	display     string
	attachments []imageAttachment
	planMode    bool
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

type toolApprovalRequestMsg struct {
	request core.ApprovalRequest
}

type toolApprovalCancelledMsg struct{ requestID string }

type taskFinishedMsg struct {
	result taskResult
}

type taskResult struct {
	response json.RawMessage
	notice   string
	err      error
}

type tuiModel struct {
	client              Client
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
	activities                    []tuiActivity
	teamActivities                []tuiTeamActivity
	chatClickTargets              map[int]string
	foldableMessages              []string
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
	pendingMCPInteraction         *mcpInteractionState
	mcpCommandPending             bool
	mcpAuthCancel                 context.CancelFunc
	sessionPicker                 bool
	showArchived                  bool
	deleteConfirm                 bool
	renamingSession               bool
	sessionList                   []core.Session
	sessionIndex                  int
	queued                        []queuedInput
	detailsOpen                   bool
	planMode                      bool
	planReview                    bool
	taskPlanMode                  bool
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
	width                         int
	height                        int
	pending                       bool
	running                       bool
	cancel                        context.CancelFunc
	cancelled                     bool
	abandonOnFinish               bool
	status                        string
	preferences                   uiPreferences
	preferencesPath               string
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

const defaultInputPlaceholder = "Ask anything, or paste ResponseInput JSON..."

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
	{name: "/fold", usage: "/fold [n]", description: "List or toggle a long user message."},
	{name: "/fork", usage: "/fork", description: "Start a new session from completed history."},
	{name: "/goal", usage: "/goal [text|clear]", description: "Show, set, or clear the goal for future tasks."},
	{name: "/help", usage: "/help", description: "Show available commands."},
	{name: "/keymap", usage: "/keymap [reset]", description: "Edit key bindings or restore defaults."},
	{name: "/model", usage: "/model [profile]", description: "Choose a model profile."},
	{name: "/mcp", usage: "/mcp <list|enable|disable|auth> [name]", description: "Manage configured Model Context Protocol servers."},
	{name: "/new", usage: "/new", description: "Start a new session."},
	{name: "/permissions", usage: "/permissions [mode]", description: "Show or set the permission mode."},
	{name: "/plan", usage: "/plan", description: "Toggle Plan mode."},
	{name: "/queue", usage: "/queue", description: "Review or edit queued messages."},
	{name: "/resume", usage: "/resume", description: "Choose a session to resume."},
	{name: "/rewind", usage: "/rewind <n>", description: "Remove the latest n user turns."},
	{name: "/theme", usage: "/theme [value]", description: "Show or change the theme."},
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
	{mode: core.PermissionWorkspace, label: "Workspace", description: "Workspace file access; shell/web need approval (shell can reach outside)"},
	{mode: core.PermissionAsk, label: "Ask", description: "Ask before writes, shell, web, and other non-read tools"},
	{mode: core.PermissionFullAccess, label: "Full access", description: "Allow all tools; shell can access files outside the workspace"},
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

func Run(client Client) error {
	workspace := client.Workspace()
	ctx := context.Background()
	saved, found, err := client.LatestSession(ctx)
	if err != nil {
		return fmt.Errorf("load latest session: %w", err)
	}
	if !found {
		saved, err = client.CreateSession(ctx, json.RawMessage("null"))
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
	}
	pending, err := client.HasPending(ctx, saved.ID)
	if err != nil {
		return fmt.Errorf("inspect session task: %w", err)
	}
	var pendingHistory json.RawMessage
	if pending {
		pendingHistory, err = client.PendingHistory(ctx, saved.ID)
		if err != nil {
			return fmt.Errorf("restore interrupted session history: %w", err)
		}
	}
	planMode := false
	if pending {
		planMode, err = client.PendingPlanMode(ctx, saved.ID)
		if err != nil {
			return fmt.Errorf("inspect paused task mode: %w", err)
		}
	}
	ui := newTUIModel(client, pending)
	preferences, preferencesPath, preferencesErr := loadUserPreferences()
	ui.preferences = preferences
	ui.preferencesPath = preferencesPath
	applyTextareaTheme(&ui.input, preferences.Theme)
	ui.applyMCPInteractionTheme(preferences.Theme)
	ui.applyKeymap()
	if preferencesErr != nil {
		ui.status = "Some UI preferences were invalid; defaults were used where needed."
	}
	ui.sessionID = saved.ID
	ui.workspace = workspace
	ui.planMode = planMode
	ui.restoreSessionState(saved)
	ui.refreshSessionRail(saved.ID)
	if pending && len(pendingHistory) > 0 {
		ui.entries = transcriptEntries(pendingHistory, workspace)
	}
	ui.updateLayout()
	ui.refreshPanels()
	program := tea.NewProgram(ui, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	client.SetEventHandler(func(event core.Event) { program.Send(coreEventMsg{event: event}) })
	client.SetUserInteractionAvailable(true)
	defer func() {
		if ui.mcpAuthCancel != nil {
			ui.mcpAuthCancel()
		}
		if ui.pendingMCPInteraction != nil {
			_ = client.ResolveUserInteraction(ui.pendingMCPInteraction.request.ID, json.RawMessage(`{"action":"cancel"}`))
		}
		client.SetUserInteractionAvailable(false)
		client.SetEventHandler(nil)
	}()
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}
	return nil
}

func newTUIModel(client Client, pending bool) *tuiModel {
	input := textarea.New()
	input.Placeholder = defaultInputPlaceholder
	input.Prompt = "┃ "
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
		agentUsage:          make(map[string]agentUsageSummary), agentUsageComplete: true,
	}
	if client != nil {
		model.attachmentLimits = normalizeAttachmentLimits(client.ProjectAttachmentLimits())
	}
	model.chat.SoftWrap = true
	model.activity.SoftWrap = true
	model.applyKeymap()
	model.updateLayout()
	model.refreshPanels()
	return model
}

func (m *tuiModel) Init() tea.Cmd { return textarea.Blink }

func (m *tuiModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
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
		var command tea.Cmd
		switch message.event.Kind {
		case core.EventTaskStarted:
			if message.event.SessionID == m.sessionID {
				m.startModelStream(message.event)
				m.contextWindow = message.event.ContextWindow
				m.contextInput = 0
				m.contextEstimated = false
			}
		case core.EventTaskFinished:
			m.finishModelStreamEvent(message.event)
		case core.EventModelStream:
			return m, m.queueModelStreamEvent(message.event)
		case core.EventToolStarted, core.EventToolFinished:
			m.updateToolActivity(message.event.Tool)
		case core.EventAgentStarted, core.EventAgentFinished:
			m.updateTeamActivity(message.event)
		case core.EventApprovalRequired:
			if message.event.Approval != nil {
				request := toolApprovalRequestMsg{request: *message.event.Approval}
				m.pendingApproval = &request
				m.status = "Permission required: " + m.keyLabel("approve_tool") + " approve, " + m.keyLabel("deny_tool") + " deny."
				m.addEntry("permission", formatToolApprovalWithKeys(request.request, m.workspace, m.keyLabel("approve_tool"), m.keyLabel("deny_tool")), true)
			}
		case core.EventApprovalCancelled:
			if m.pendingApproval != nil && m.pendingApproval.request.ID == message.event.ApprovalID {
				m.pendingApproval = nil
				m.status = "Tool approval cancelled."
			}
		case core.EventUserInteractionRequired:
			if message.event.Interaction != nil {
				command = m.beginMCPInteraction(*message.event.Interaction)
			}
		case core.EventUserInteractionCancelled:
			if m.pendingMCPInteraction != nil && m.pendingMCPInteraction.request.ID == message.event.InteractionID {
				m.finishMCPInteraction("MCP interaction cancelled because its task ended.", false)
			}
		case core.EventUsage:
			m.updateModelUsage(message.event.Usage, message.event.AgentID, message.event.ModelProfile,
				message.event.Provider, message.event.Model, message.event.ContextWindow, message.event.EstimatedInputTokens)
		case core.EventContextCompaction:
			if message.event.SessionID == "" || message.event.SessionID == m.sessionID {
				m.updateContextCompaction(message.event)
			}
		}
		m.refreshPanels()
		return m, command
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
	case taskFinishedMsg:
		m.finishTask(message.result)
		m.refreshSessionRail(m.sessionID)
		m.refreshPanels()
		if len(m.queued) > 0 && !m.planReview {
			return m.startQueuedInput()
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
		if m.handleConversationClick(message) {
			return m, nil
		}
		return m, nil
	case tea.KeyPressMsg:
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
		if m.matchesKey("input_toggle_message_fold", message) && !m.sessionPicker && !m.modelPicker &&
			!m.effortPicker && !m.queuePicker && !m.permissionMenu && m.pendingApproval == nil {
			m.toggleLatestLongMessage()
			return m, nil
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
					queuedBefore := len(m.queued)
					m.enqueueInput()
					if len(m.queued) == queuedBefore {
						return m, nil
					}
				}
				m.addEntry("notice", "Abandoning the current task; completed tool side effects are not rolled back.", true)
				m.abandonOnFinish = true
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

func (m *tuiModel) submit() (tea.Model, tea.Cmd) {
	raw := m.input.Value()
	line := strings.TrimSpace(raw)
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
		return m, tea.Quit
	case "/resume":
		return m.openSessionPicker()
	case "/fork":
		return m.forkSession()
	case "/fold":
		return m.showFoldableMessages()
	case "/goal":
		return m.showGoal()
	case "/rewind":
		m.status = "Use /rewind <n> to remove the latest n user turns."
		return m, nil
	case "/queue":
		return m.openQueuePicker()
	case "/continue":
		return m.continueTask()
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
	case "/vim":
		return m.toggleVimMode()
	case "/theme":
		m.status = fmt.Sprintf("Current theme: %s%s.",
			m.preferences.Theme, animeLayoutLabel(m.preferences))
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
	if strings.HasPrefix(line, "/fold ") || strings.HasPrefix(line, "/fold\t") {
		return m.toggleLongMessageFromCommand(line)
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
		if err := m.branchPausedSession(); err != nil {
			m.addEntry("error", err.Error(), true)
			m.status = "Could not start a new session branch."
			return m, nil
		}
	}
	m.addEntry("you", inputTranscriptText(input, raw), false)
	m.resetInput()
	m.cancelled = false
	return m.startTask(func(ctx context.Context) taskResult {
		response, err := m.client.RunTask(ctx, core.TaskRequest{SessionID: m.sessionID, Input: input, PlanMode: m.planMode})
		return taskResult{response: response, err: err}
	}, true)
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

func (m *tuiModel) showFoldableMessages() (tea.Model, tea.Cmd) {
	m.refreshPanels()
	if len(m.foldableMessages) == 0 {
		m.status = "No long user messages to fold."
		m.addEntry("notice", m.status, false)
		m.resetInput()
		return m, nil
	}
	lines := []string{"Long user messages:"}
	for index, key := range m.foldableMessages {
		state, preview := "collapsed", ""
		if m.preferences.ExpandedMessages[key] {
			state = "expanded"
		}
		for _, entry := range m.entries {
			if entry.collapseKey != key {
				continue
			}
			preview = strings.Join(strings.Fields(entry.text), " ")
			if runes := []rune(preview); len(runes) > 72 {
				preview = string(runes[:69]) + "…"
			}
			break
		}
		lines = append(lines, fmt.Sprintf("%d · %s · %s", index+1, state, preview))
	}
	lines = append(lines, "Use /fold <n> to toggle a message.")
	m.addEntry("notice", strings.Join(lines, "\n"), false)
	m.status = "Choose a long user message with /fold <n>."
	m.resetInput()
	return m, nil
}

func (m *tuiModel) toggleLongMessageFromCommand(line string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		m.status = "Use /fold <n> to toggle a listed long user message."
		return m, nil
	}
	index, err := strconv.Atoi(fields[1])
	if err != nil || index < 1 || index > len(m.foldableMessages) {
		m.status = "Unknown long user message. Use /fold to list available messages."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	m.toggleMessageExpansion(m.foldableMessages[index-1])
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
	if len(fields) < 2 || len(fields) > 3 {
		m.status = "Theme selection was not recognized."
		return m, nil
	}
	theme := themeName(strings.ToLower(fields[1]))
	switch theme {
	case themeDark, themeLight:
		if len(fields) != 2 {
			m.status = "Theme selection was not recognized."
			return m, nil
		}
		m.preferences.Theme = theme
	case themeAnime:
		if len(fields) == 3 {
			layout := animeLayout(strings.ToLower(fields[2]))
			if layout != layoutFocus && layout != layoutStudio {
				m.status = "Anime layout must be focus or studio."
				return m, nil
			}
			m.preferences.AnimeLayout = layout
		}
		m.preferences.Theme = themeAnime
	default:
		m.status = "Theme selection was not recognized."
		return m, nil
	}
	if err := saveUIPreferences(m.preferencesPath, m.preferences); err != nil {
		m.status = "Theme changed for this session; preference could not be saved."
	} else {
		m.status = "Theme set to " + string(m.preferences.Theme) + animeLayoutLabel(m.preferences) + "."
	}
	applyTextareaTheme(&m.input, m.preferences.Theme)
	m.updateLayout()
	m.refreshPanels()
	return m, nil
}

func animeLayoutLabel(preferences uiPreferences) string {
	if preferences.Theme != themeAnime {
		return ""
	}
	return " · " + string(preferences.AnimeLayout)
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
	m.queued = append(m.queued, queuedInput{
		text: raw, display: display, attachments: shallowCopyImageAttachments(m.attachments), planMode: m.planMode,
	})
	m.addEntry("queued", display, false)
	m.status = fmt.Sprintf("Queued %d message(s). %s adds another · %s interrupts and sends.", len(m.queued), m.keyLabel("send"), m.keyLabel("interrupt_send"))
	m.resetInput()
	return m, nil
}

func (m *tuiModel) openQueuePicker() (tea.Model, tea.Cmd) {
	if len(m.queued) == 0 {
		m.status = "No queued messages."
		m.resetInput()
		return m, nil
	}
	m.queuePicker = true
	m.queueIndex = min(m.queueIndex, len(m.queued)-1)
	m.status = "Queue · " + m.keyLabel("picker_previous") + "/" + m.keyLabel("picker_next") + " select · " + m.keyLabel("send") + " edit · " + m.keyLabel("queue_remove") + " removes · Esc closes."
	m.resetInput()
	return m, nil
}

func (m *tuiModel) editQueuedInput() (tea.Model, tea.Cmd) {
	if m.queueIndex < 0 || m.queueIndex >= len(m.queued) {
		m.queuePicker = false
		return m, nil
	}
	queued := m.queued[m.queueIndex]
	m.queued = append(m.queued[:m.queueIndex], m.queued[m.queueIndex+1:]...)
	m.removeQueuedEntry(queued.display)
	m.setInputValue(queued.text)
	m.attachments = shallowCopyImageAttachments(queued.attachments)
	m.nextAttachmentID = nextImageAttachmentID(m.attachments)
	m.planMode = queued.planMode
	m.queuePicker = false
	m.status = "Editing a queued message; Enter resubmits it."
	m.updateCommandSuggestions()
	m.updateLayout()
	m.refreshPanels()
	return m, nil
}

func (m *tuiModel) deleteQueuedInput() {
	if m.queueIndex < 0 || m.queueIndex >= len(m.queued) {
		return
	}
	text := m.queued[m.queueIndex].display
	m.queued = append(m.queued[:m.queueIndex], m.queued[m.queueIndex+1:]...)
	m.removeQueuedEntry(text)
	if len(m.queued) == 0 {
		m.queuePicker = false
		m.status = "Queue is empty."
	} else {
		m.queueIndex = min(m.queueIndex, len(m.queued)-1)
		m.status = fmt.Sprintf("%d message(s) remain queued.", len(m.queued))
	}
	m.refreshPanels()
}

func (m *tuiModel) removeQueuedEntry(text string) {
	for index := len(m.entries) - 1; index >= 0; index-- {
		if m.entries[index].role == "queued" && m.entries[index].text == text {
			m.entries = append(m.entries[:index], m.entries[index+1:]...)
			return
		}
	}
}

func (m *tuiModel) clearQueue() {
	m.queued = nil
	m.queuePicker = false
	m.queueIndex = 0
	kept := m.entries[:0]
	for _, entry := range m.entries {
		if entry.role != "queued" {
			kept = append(kept, entry)
		}
	}
	m.entries = kept
	m.refreshPanels()
}

func (m *tuiModel) startQueuedInput() (tea.Model, tea.Cmd) {
	if len(m.queued) == 0 {
		return m, nil
	}
	queued := m.queued[0]
	m.queued = m.queued[1:]
	input, inputErr := draftTaskInput(queued.text, queued.attachments)
	if inputErr != nil {
		m.queued = append([]queuedInput{queued}, m.queued...)
		m.addEntry("error", "Queued image message could not be prepared: "+inputErr.Error(), true)
		m.refreshPanels()
		if len(m.queued) > 0 {
			return m.startQueuedInput()
		}
		return m, nil
	}
	if pending, err := m.hasPending(); err != nil {
		m.queued = append([]queuedInput{queued}, m.queued...)
		m.addEntry("error", "Could not inspect paused task: "+err.Error(), true)
		return m, nil
	} else if pending {
		if m.abandonOnFinish {
			if _, err := m.client.DiscardTask(context.Background(), m.sessionID); err != nil {
				m.queued = append([]queuedInput{queued}, m.queued...)
				m.addEntry("error", "Could not discard interrupted task: "+err.Error(), true)
				return m, nil
			}
			m.pending = false
			m.abandonOnFinish = false
		} else if err := m.branchPausedSession(); err != nil {
			m.queued = append([]queuedInput{queued}, m.queued...)
			m.addEntry("error", "Could not branch paused task: "+err.Error(), true)
			return m, nil
		}
	}
	for index := len(m.entries) - 1; index >= 0; index-- {
		if m.entries[index].role == "queued" && m.entries[index].text == queued.display {
			m.entries[index].role = "you"
			break
		}
	}
	m.planMode = queued.planMode
	m.cancelled = false
	m.resetInput()
	return m.startTask(func(ctx context.Context) taskResult {
		response, err := m.client.RunTask(ctx, core.TaskRequest{SessionID: m.sessionID, Input: input, PlanMode: queued.planMode})
		return taskResult{response: response, err: err}
	}, true)
}

func (m *tuiModel) acceptPlan() (tea.Model, tea.Cmd) {
	m.planReview = false
	m.planMode = false
	m.addEntry("you", "Implement the approved plan.", false)
	m.resetInput()
	input, _ := core.NewTaskInput("Implement the approved plan.")
	return m.startTask(func(ctx context.Context) taskResult {
		response, err := m.client.RunTask(ctx, core.TaskRequest{SessionID: m.sessionID, Input: input, PlanMode: false})
		return taskResult{response: response, err: err}
	}, true)
}

func (m *tuiModel) togglePlanMode() (tea.Model, tea.Cmd) {
	if m.running {
		m.status = "Plan mode can only be changed while idle."
		m.addEntry("notice", m.status, false)
		return m, nil
	}
	m.planMode = !m.planMode
	if m.planMode {
		m.status = "Plan mode · read-only tools only; permission still controls approval for visible tools."
	} else {
		m.status = "Build mode."
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
		if option.mode == m.client.PermissionMode() {
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
		if option.mode == m.client.PermissionMode() {
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
	if err := m.client.SetPermissionMode(mode); err != nil {
		m.status = err.Error()
		m.addEntry("error", m.status, true)
		return m, nil
	}
	m.permissionMenu = false
	if resetInput {
		m.resetInput()
	} else {
		m.updateCommandSuggestions()
		m.updateLayout()
	}
	m.status = fmt.Sprintf("Permission mode: %s.", permissionModeLabel(mode))
	notice := fmt.Sprintf("Permission mode: %s. %s", permissionModeLabel(mode), permissionModeDescription(mode))
	if m.planMode {
		m.status += " Plan still caps tools at read-only."
		notice += " Plan mode only exposes read-only tools; this setting controls approval for visible tools such as web."
	}
	m.addEntry("notice", notice, false)
	m.refreshPanels()
	return m, nil
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
		return "Workspace file operations; commands and web need approval"
	case core.PermissionAsk:
		return "Read workspace files; ask before every other tool"
	case core.PermissionFullAccess:
		return "Allow all tools; shell can access paths outside the workspace"
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
	if m.client != nil {
		if err := m.client.ResolveApproval(request.request.ID, approved); err != nil {
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
	colors := colorsFor(m.preferences.Theme)
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
	saved := m.sessionList[m.sessionIndex]
	m.sessionID = saved.ID
	m.clearQueue()
	m.restoreSessionState(saved)
	m.planMode = false
	m.planReview = false
	m.pending = false
	if pending, err := m.hasPending(); err != nil {
		m.sessionPicker = false
		m.status = "Could not inspect paused task."
		m.addEntry("error", err.Error(), true)
		return m, nil
	} else {
		m.pending = pending
	}
	if m.pending {
		planMode, err := m.client.PendingPlanMode(context.Background(), m.sessionID)
		if err != nil {
			m.sessionPicker = false
			m.status = "Could not inspect paused task mode."
			m.addEntry("error", err.Error(), true)
			return m, nil
		}
		m.planMode = planMode
	}
	if m.pending {
		if history, err := m.client.PendingHistory(context.Background(), m.sessionID); err == nil && len(history) > 0 {
			m.entries = transcriptEntries(history, m.workspace)
		}
	}
	m.sessionPicker = false
	m.status = "Session restored."
	m.resetInput()
	m.refreshPanels()
	m.updateLayout()
	return m, nil
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
	m.forgetExpandedMessages(saved.ID)
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

func (m *tuiModel) continueTask() (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.status = "Task recovery is unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	pending, err := m.client.HasPending(context.Background(), m.sessionID)
	if err != nil {
		m.status = "Could not inspect interrupted task."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if !pending {
		m.pending = false
		m.status = "No paused task in this session."
		m.addEntry("notice", m.status, false)
		m.resetInput()
		return m, nil
	}
	m.pending = true
	planMode, err := m.client.PendingPlanMode(context.Background(), m.sessionID)
	if err != nil {
		m.status = "Could not inspect paused task mode."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.planMode = planMode
	m.addEntry("notice", "Continuing the paused task; a model request or tool call may repeat.", true)
	m.resetInput()
	m.cancelled = false
	return m.startTask(func(ctx context.Context) taskResult {
		response, err := m.client.ResumeTask(ctx, m.sessionID)
		return taskResult{response: response, err: err}
	}, false)
}

func (m *tuiModel) newSession() (tea.Model, tea.Cmd) {
	saved, err := m.client.CreateSession(context.Background(), json.RawMessage("null"))
	if err != nil {
		m.status = "Could not create a session."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.sessionID = saved.ID
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
	m.planMode = false
	m.planReview = false
	m.pending = false
	m.status = "New session."
	m.refreshSessionRail(m.sessionID)
	m.resetInput()
	m.refreshPanels()
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) forkSession() (tea.Model, tea.Cmd) {
	branch, err := m.client.ForkSession(context.Background(), m.sessionID)
	if err != nil {
		m.status = "Could not fork this session."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	m.sessionID = branch.ID
	m.restoreSessionState(branch)
	m.clearQueue()
	m.planMode = false
	m.planReview = false
	m.pending = false
	m.status = "Forked session history into a new session."
	m.refreshSessionRail(m.sessionID)
	m.resetInput()
	m.refreshPanels()
	m.updateLayout()
	return m, nil
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

func (m *tuiModel) branchPausedSession() error {
	branch, err := m.client.ForkSession(context.Background(), m.sessionID)
	if err != nil {
		return err
	}
	m.sessionID = branch.ID
	m.restoreSessionState(branch)
	m.refreshSessionRail(m.sessionID)
	m.pending = false
	m.status = "Started a new branch from the last completed turn."
	m.addEntry("notice", "Started a new branch from completed history; the paused task remains in its original session.", false)
	return nil
}

func (m *tuiModel) restoreSessionState(saved core.Session) {
	m.sessionSettings = saved.Settings
	m.entries = transcriptEntries(saved.History, m.workspace)
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
	if m.client == nil {
		m.status = "Task recovery is unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	discarded, err := m.client.DiscardTask(context.Background(), m.sessionID)
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
	if len(m.queued) > 0 {
		return m.startQueuedInput()
	}
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
	if m.client == nil {
		return false, nil
	}
	pending, err := m.client.HasPending(context.Background(), m.sessionID)
	m.pending = pending
	return pending, err
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
	if m.running && m.cancel != nil && !m.cancelled {
		m.cancelled = true
		m.status = "Cancelling task…"
		m.cancel()
	}
}

func (m *tuiModel) finishTask(result taskResult) {
	m.running = false
	m.pendingApproval = nil
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}

	var responseText string
	var responseErr error
	if result.response != nil {
		responseText, responseErr = responseDisplayText(result.response)
	}
	streamedFinal := m.finishModelStream(responseText, result.err == nil && !m.cancelled)
	if responseErr != nil {
		m.addEntry("error", responseErr.Error(), true)
	} else if responseText != "" && !streamedFinal {
		m.addEntry("assistant", responseText, false)
	}
	if result.notice != "" {
		m.addEntry("notice", result.notice, false)
	}

	pending, pendingErr := m.hasPending()
	if pendingErr != nil {
		m.addEntry("error", "Could not inspect interrupted task: "+pendingErr.Error(), true)
	}
	if m.abandonOnFinish {
		if pending && m.client != nil {
			if _, err := m.client.DiscardTask(context.Background(), m.sessionID); err != nil {
				m.addEntry("error", "Could not discard interrupted task: "+err.Error(), true)
			} else {
				pending = false
				m.pending = false
			}
		}
		m.abandonOnFinish = false
	}
	if m.cancelled {
		if pending {
			m.status = "Task paused. Use /continue to resume or type a new message to branch."
		} else {
			m.status = "Task cancelled."
		}
	} else if result.err != nil {
		m.status = "Task failed."
		m.addEntry("error", result.err.Error(), true)
	} else {
		m.status = "Completed."
	}
	if m.taskPlanMode && !m.cancelled && result.err == nil && result.response != nil {
		m.planReview = true
		m.status = "Plan ready · Enter accepts · type feedback to revise · Esc cancels."
	} else if m.taskPlanMode && result.err != nil {
		m.planMode = false
	}
	m.cancelled = false
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
	if entryIndex >= 0 {
		m.entries[entryIndex].text = entryText
		m.entries[entryIndex].err = activity.status == "failed"
	} else {
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
	chatAtBottom := m.chat.AtBottom()
	activityAtBottom := m.activity.AtBottom()
	m.ensureMessageCollapseKeys()
	conversation, clickTargets, foldable := m.renderConversation()
	m.chat.SoftWrap = false // Conversation lines are wrapped before they reach the viewport.
	m.chat.SetContent(conversation)
	m.chatClickTargets = clickTargets
	m.foldableMessages = foldable
	m.activity.SetContent(m.activityDetailsContent())
	if m.modernConversation() {
		m.updateLayout()
	}
	if chatAtBottom {
		m.chat.GotoBottom()
	} else {
		m.chat.SetYOffset(m.chat.YOffset())
	}
	if activityAtBottom {
		m.activity.GotoBottom()
	} else {
		m.activity.SetYOffset(m.activity.YOffset())
	}
}

func (m *tuiModel) activityDetailsContent() string {
	colors := colorsFor(m.preferences.Theme)
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
		activities.WriteString("\nPermissions: " + permissionModeLabel(m.client.PermissionMode()))
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
	if m.studioLayout() && m.studioWide() {
		sidebarWidth := 36
		m.chat.SetWidth(max(1, contentWidth-sidebarWidth-1))
		m.chat.SetHeight(bodyHeight)
		artHeight := m.studioArtHeight(bodyHeight)
		m.activity.SetWidth(sidebarWidth)
		m.activity.SetHeight(max(1, bodyHeight-artHeight))
		return
	}
	if m.studioLayout() {
		chatHeight := max(1, (bodyHeight-2)*2/3)
		m.chat.SetWidth(contentWidth)
		m.chat.SetHeight(chatHeight)
		m.activity.SetWidth(contentWidth)
		m.activity.SetHeight(max(1, bodyHeight-chatHeight-2))
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

func (m *tuiModel) studioLayout() bool {
	return m.preferences.Theme == themeAnime && m.preferences.AnimeLayout == layoutStudio
}

func (m *tuiModel) studioWide() bool {
	return m.studioLayout() && m.width >= 120 && m.height >= 30
}

func (m *tuiModel) studioArtHeight(bodyHeight int) int {
	if m.studioWide() && m.canRenderColorArt() && bodyHeight >= 14 {
		return 8
	}
	return 0
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
	if m.modernConversation() && m.wideConversation() {
		return m.renderSessionWorkspace(width)
	}
	header := m.renderHeader(width)
	colors := colorsFor(m.preferences.Theme)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(strings.Repeat("─", width))
	body := m.renderBody(width)
	suggestions := m.renderCommandSuggestions()
	footer := m.renderFooter(width)
	parts := []string{header, body, divider, m.renderInputView()}
	if auxiliary := m.renderAuxiliaryPanel(suggestions); auxiliary != "" {
		parts = append(parts, auxiliary)
	}
	parts = append(parts, footer)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	view := tea.NewView(content)
	view.BackgroundColor = backgroundColor(colors.Background)
	view.ForegroundColor = backgroundColor(colors.Foreground)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	if cursor := m.activeCursor(); cursor != nil && !m.keymapOpen && (!m.sessionPicker || m.renamingSession) && !m.queuePicker && !m.permissionMenu && !m.modelPicker && !m.effortPicker {
		cursor.Y += lipgloss.Height(header) + lipgloss.Height(body) + lipgloss.Height(divider)
		view.Cursor = cursor
	}
	return view
}

func (m *tuiModel) renderAuxiliaryPanel(suggestions string) string {
	switch {
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
	colors := colorsFor(m.preferences.Theme)
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
	colors := colorsFor(m.preferences.Theme)
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
	colors := colorsFor(m.preferences.Theme)
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
	if m.preferences.Theme == themeAnime {
		headerText += " · Anime / " + string(m.preferences.AnimeLayout)
	}
	headerText += " · " + stateName(m.visualState())
	if m.sessionID != "" {
		headerText += "  ·  " + m.sessionID[:min(8, len(m.sessionID))]
	}
	header := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(headerText)
	if m.preferences.Theme == themeAnime && !m.studioLayout() && width >= 80 && m.height >= 24 {
		avatar := m.renderAvatar(8, 4)
		if m.canRenderColorArt() {
			return lipgloss.JoinHorizontal(lipgloss.Center, header, "   ", avatar)
		}
		header = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(
			stateGlyph(m.visualState()) + " " + headerText)
	}
	return header
}

func (m *tuiModel) renderFooter(width int) string {
	colors := colorsFor(m.preferences.Theme)
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
		if len(m.foldableMessages) > 0 && !m.detailsOpen {
			status += " · " + m.keyLabel("input_toggle_message_fold") + " fold latest long message"
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
	metrics += " · " + string(m.preferences.Theme) + animeLayoutLabel(m.preferences)
	if m.client != nil {
		metrics += " · " + permissionModeLabel(m.client.PermissionMode())
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
	colors := colorsFor(m.preferences.Theme)
	lines := []string{"Queued messages · " + m.keyLabel("picker_previous") + "/" + m.keyLabel("picker_next") + " select · " + m.keyLabel("send") + " edit · " + m.keyLabel("queue_remove") + " removes · Esc closes"}
	start := max(0, m.queueIndex-4)
	end := min(len(m.queued), start+8)
	if end == len(m.queued) {
		start = max(0, end-8)
	}
	for index, item := range m.queued[start:end] {
		index += start
		preview := strings.Join(strings.Fields(item.display), " ")
		if len([]rune(preview)) > 64 {
			preview = string([]rune(preview)[:61]) + "..."
		}
		line := fmt.Sprintf("  %d. %s", index+1, preview)
		if index == m.queueIndex {
			line = "›" + strings.TrimPrefix(line, " ")
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderPlanReview() string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(colorsFor(m.preferences.Theme).Warning)).Render(
		"Plan review · Enter accepts · type feedback and Enter to revise · Esc cancels",
	)
}

func (m *tuiModel) renderCommandSuggestions() string {
	colors := colorsFor(m.preferences.Theme)
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
	colors := colorsFor(m.preferences.Theme)
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
	colors := colorsFor(m.preferences.Theme)
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
	colors := colorsFor(m.preferences.Theme)
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
	colors := colorsFor(m.preferences.Theme)
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
	colors := colorsFor(m.preferences.Theme)
	mode := core.PermissionReadOnly
	if m.client != nil {
		mode = m.client.PermissionMode()
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
	colors := colorsFor(m.preferences.Theme)
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
	if m.studioWide() {
		sidebar := m.activity.View()
		if artHeight := m.studioArtHeight(m.chat.Height()); artHeight > 0 {
			art := m.renderAvatar(20, artHeight)
			sidebar = lipgloss.JoinVertical(lipgloss.Left, art, sidebar)
		}
		separator := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(
			strings.TrimSuffix(strings.Repeat("│\n", max(1, m.chat.Height())), "\n"))
		return lipgloss.JoinHorizontal(lipgloss.Top, m.chat.View(), separator, sidebar)
	}
	if m.studioLayout() {
		separator := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(strings.Repeat("─", width))
		label := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).
			Render("STUDIO · " + stateName(m.visualState()) + " · single agent")
		return lipgloss.JoinVertical(lipgloss.Left, m.chat.View(), separator, label, m.activity.View())
	}
	return m.chat.View()
}

func (m *tuiModel) renderSessionWorkspaceBody(width int) string {
	colors := colorsFor(m.preferences.Theme)
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
	colors := colorsFor(m.preferences.Theme)
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
