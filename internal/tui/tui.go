package tui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/afterlune/lune/internal/model"
	"github.com/afterlune/lune/internal/session"
	"github.com/afterlune/lune/internal/tool"
)

type tuiEntry struct {
	role       string
	text       string
	err        bool
	activityID string
}

type savedTUIEntry struct {
	Role       string `json:"role"`
	Text       string `json:"text"`
	Error      bool   `json:"error,omitempty"`
	ActivityID string `json:"activity_id,omitempty"`
}

type savedTUISession struct {
	History       *model.ResponseInput `json:"history,omitempty"`
	Entries       []savedTUIEntry      `json:"entries,omitempty"`
	Activities    []savedActivity      `json:"activities,omitempty"`
	UsageRequests []savedUsageRecord   `json:"usage_requests,omitempty"`
	TaskUsage     savedUsageTotals     `json:"task_usage,omitempty"`
	SessionUsage  savedUsageTotals     `json:"session_usage,omitempty"`
}

type savedActivity struct {
	CallID   string `json:"call_id,omitempty"`
	Name     string `json:"name,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Status   string `json:"status"`
	Duration int64  `json:"duration,omitempty"`
}

type savedUsageRecord struct {
	Request   int                 `json:"request"`
	Usage     model.ResponseUsage `json:"usage"`
	Available bool                `json:"available"`
}

type savedUsageTotals struct {
	Requests  int   `json:"requests"`
	Measured  int   `json:"measured"`
	Input     int64 `json:"input"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"`
	Total     int64 `json:"total"`
	Cached    int64 `json:"cached"`
}

type queuedInput struct {
	text     string
	display  string
	input    *model.ResponseInput
	planMode bool
}

type tuiActivity struct {
	callID   string
	summary  string
	status   string
	duration time.Duration
}

type toolActivityMsg struct{ event tool.ToolEvent }

type toolApprovalRequestMsg struct {
	request  tool.PermissionRequest
	response chan bool
}

type toolApprovalCancelledMsg struct{ response chan bool }

type taskFinishedMsg struct {
	result taskResult
}

type taskResult struct {
	response *model.Response
	notice   string
	err      error
}

type tuiModel struct {
	client      Responder
	runtime     ResumableResponder
	permissions *tool.Registry
	sessions    *session.Store
	sessionID   string
	workspace   string
	input       textarea.Model
	chat        viewport.Model
	activity    viewport.Model

	entries            []tuiEntry
	activities         []tuiActivity
	commandSuggestions []string
	suggestionIndex    int
	permissionMenu     bool
	permissionIndex    int
	pendingApproval    *toolApprovalRequestMsg
	sessionPicker      bool
	sessionList        []session.Snapshot
	sessionIndex       int
	queued             []queuedInput
	detailsOpen        bool
	planMode           bool
	planReview         bool
	taskPlanMode       bool
	vimMode            bool
	vimNormal          bool
	vimPendingDelete   bool
	queuePicker        bool
	queueIndex         int
	taskUsage          tokenUsageTotals
	sessionUsage       tokenUsageTotals
	usageRequests      []tokenUsageRecord
	width              int
	height             int
	pending            bool
	running            bool
	cancel             context.CancelFunc
	cancelled          bool
	abandonOnFinish    bool
	status             string
	preferences        uiPreferences
	preferencesPath    string
	colorProfile       colorprofile.Profile
}

type compactionResponder interface {
	Compact(context.Context) (int, error)
}

type modelUsageMsg struct{ usage model.ResponseUsage }

type tokenUsageRecord struct {
	request   int
	usage     model.ResponseUsage
	available bool
}

type tokenUsageTotals struct {
	requests  int
	measured  int
	input     int64
	output    int64
	reasoning int64
	total     int64
	cached    int64
}

var tuiCommands = []string{"/compact", "/continue", "/discard", "/exit", "/new", "/permissions", "/plan", "/queue", "/resume", "/theme", "/vim"}
var legacyTUICommands = []string{"/compact", "/discard", "/exit", "/permissions", "/plan", "/resume", "/theme"}

type tuiPermissionOption struct {
	mode        tool.PermissionMode
	label       string
	description string
}

var tuiPermissionOptions = []tuiPermissionOption{
	{mode: tool.PermissionModeReadOnly, label: "Read-only", description: "Read workspace files; ask before web; block writes and shell"},
	{mode: tool.PermissionModeWorkspace, label: "Workspace", description: "Workspace file access; shell/web need approval (shell can reach outside)"},
	{mode: tool.PermissionModeAsk, label: "Ask", description: "Ask before writes, shell, web, and other non-read tools"},
	{mode: tool.PermissionModeFullAccess, label: "Full access", description: "Allow all tools; shell can access files outside the workspace"},
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

func Run(client Responder, registry *tool.Registry) error {
	runtime, _ := client.(ResumableResponder)
	workspace, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get workspace for sessions: %w", err)
	}
	stateDir := filepath.Join(workspace, ".luneagent")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return fmt.Errorf("create session directory: %w", err)
	}
	sessions, err := session.Open(filepath.Join(stateDir, "sessions.db"))
	if err != nil {
		return err
	}
	defer sessions.Close()

	latest, err := sessions.Latest(context.Background(), workspace)
	if err != nil {
		return fmt.Errorf("load latest session: %w", err)
	}
	sessionID := ""
	var saved savedTUISession
	if latest != nil {
		sessionID = latest.ID
		if err := json.Unmarshal(latest.Data, &saved); err != nil {
			return fmt.Errorf("decode latest session: %w", err)
		}
	}
	pending := false
	if latest == nil && runtime != nil {
		pending, err = runtime.HasPending()
		if err != nil {
			return fmt.Errorf("inspect interrupted task: %w", err)
		}
	}
	if sessionID == "" {
		if pending {
			// Adopt the original single-checkpoint id for upgrades from older builds.
			sessionID = legacyRunID
		} else if sessionID, err = newSessionID(); err != nil {
			return err
		}
	}
	if latest == nil && pending {
		if historyLoader, ok := client.(interface {
			PendingHistory() (*model.ResponseInput, error)
		}); ok {
			fullHistory, err := historyLoader.PendingHistory()
			if err != nil {
				return fmt.Errorf("restore interrupted session history: %w", err)
			}
			saved.Entries = transcriptFromHistory(fullHistory, workspace)
			saved.History = historyBeforePendingTurn(fullHistory)
		}
	}
	if setter, ok := client.(interface {
		SetSession(string, *model.ResponseInput)
	}); ok {
		setter.SetSession(sessionID, saved.History)
	}
	if runtime != nil {
		pending, err = runtime.HasPending()
		if err != nil {
			return fmt.Errorf("inspect session task: %w", err)
		}
	}
	planMode := false
	if pending {
		if loader, ok := client.(interface{ PendingPlanMode() (bool, error) }); ok {
			planMode, err = loader.PendingPlanMode()
			if err != nil {
				return fmt.Errorf("inspect paused task mode: %w", err)
			}
		}
	}
	ui := newTUIModel(client, runtime, pending)
	preferences, preferencesPath, preferencesErr := loadUserPreferences()
	ui.preferences = preferences
	ui.preferencesPath = preferencesPath
	applyTextareaTheme(&ui.input, preferences.Theme)
	if preferencesErr != nil {
		ui.status = "UI preferences could not be loaded; using defaults."
	}
	ui.permissions = registry
	ui.sessions = sessions
	ui.sessionID = sessionID
	ui.workspace = workspace
	ui.planMode = planMode
	ui.setRuntimePlanMode(planMode)
	ui.restoreSessionState(saved)
	ui.updateLayout()
	ui.refreshPanels()
	ui.persistSession()
	program := tea.NewProgram(ui, tea.WithInput(os.Stdin), tea.WithOutput(os.Stdout))
	if saver, ok := client.(interface {
		SetHistorySaver(func(string, *model.ResponseInput) error)
	}); ok {
		saver.SetHistorySaver(func(id string, history *model.ResponseInput) error {
			data, err := json.Marshal(history)
			if err != nil {
				return fmt.Errorf("encode session history: %w", err)
			}
			return sessions.UpdateHistory(context.Background(), id, workspace, data)
		})
		defer saver.SetHistorySaver(nil)
	}
	if registry != nil {
		registry.SetPermissionApprovalHandler(func(ctx context.Context, request tool.PermissionRequest) bool {
			response := make(chan bool, 1)
			program.Send(toolApprovalRequestMsg{request: request, response: response})
			select {
			case approved := <-response:
				return approved
			case <-ctx.Done():
				program.Send(toolApprovalCancelledMsg{response: response})
				return false
			}
		})
		defer registry.SetPermissionApprovalHandler(nil)
	}
	if observer, ok := client.(interface {
		SetUsageObserver(func(model.ResponseUsage))
	}); ok {
		observer.SetUsageObserver(func(usage model.ResponseUsage) {
			program.Send(modelUsageMsg{usage: usage})
		})
		defer observer.SetUsageObserver(nil)
	}
	if registry != nil {
		registry.SetToolEventObserver(func(event tool.ToolEvent) {
			program.Send(toolActivityMsg{event: event})
		})
		defer registry.SetToolEventObserver(nil)
	}
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("run TUI: %w", err)
	}
	return nil
}

func newTUIModel(client Responder, runtime ResumableResponder, pending bool) *tuiModel {
	input := textarea.New()
	input.Placeholder = "Ask anything, or paste ResponseInput JSON..."
	input.Prompt = "┃ "
	input.ShowLineNumbers = false
	input.DynamicHeight = true
	input.MinHeight = 2
	input.MaxHeight = 6
	input.MaxContentHeight = 10000
	input.SetVirtualCursor(false)
	input.Focus()
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "newline"))
	applyTextareaTheme(&input, themeDark)

	model := &tuiModel{
		client:      client,
		runtime:     runtime,
		input:       input,
		chat:        viewport.New(viewport.WithWidth(1), viewport.WithHeight(1)),
		activity:    viewport.New(viewport.WithWidth(1), viewport.WithHeight(1)),
		width:       100,
		height:      30,
		pending:     pending,
		status:      "Ready",
		preferences: defaultUIPreferences(),
	}
	model.chat.SoftWrap = true
	model.activity.SoftWrap = true
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
	case toolActivityMsg:
		m.updateToolActivity(message.event)
		m.persistSession()
		m.refreshPanels()
		return m, nil
	case toolApprovalRequestMsg:
		m.pendingApproval = &message
		m.status = "Permission required: press Y to approve once or N to deny."
		m.addEntry("permission", formatToolApproval(message.request, m.workspace), true)
		return m, nil
	case toolApprovalCancelledMsg:
		if m.pendingApproval != nil && m.pendingApproval.response == message.response {
			m.pendingApproval = nil
			m.status = "Tool approval cancelled."
			m.refreshPanels()
		}
		return m, nil
	case modelUsageMsg:
		m.updateModelUsage(message.usage)
		m.persistSession()
		m.refreshPanels()
		return m, nil
	case taskFinishedMsg:
		m.finishTask(message.result)
		m.refreshPanels()
		if len(m.queued) > 0 && !m.planReview {
			return m.startQueuedInput()
		}
		return m, nil
	case tea.MouseWheelMsg:
		if m.detailsOpen {
			m.activity, _ = m.activity.Update(message)
		} else {
			m.chat, _ = m.chat.Update(message)
		}
		return m, nil
	case tea.MouseClickMsg:
		return m, nil
	case tea.KeyPressMsg:
		if m.detailsOpen {
			switch message.String() {
			case "ctrl+c":
				if m.running {
					m.detailsOpen = false
					m.interrupt()
					return m, nil
				}
				return m, tea.Quit
			case "esc", "ctrl+b":
				m.detailsOpen = false
				m.updateLayout()
				return m, nil
			case "pgup", "up", "ctrl+up":
				m.activity.HalfPageUp()
				return m, nil
			case "pgdown", "down", "ctrl+down":
				m.activity.HalfPageDown()
				return m, nil
			default:
				return m, nil
			}
		}
		if !(m.running && message.String() == "esc") {
			if handled, command := m.handleVimKey(message); handled {
				m.updateLayout()
				return m, command
			}
		}
		switch message.String() {
		case "enter":
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
				return m.selectSession()
			}
			if m.permissionMenu {
				return m.selectPermissionMode()
			}
			if m.pendingApproval != nil {
				return m, nil
			}
			if m.planReview && strings.TrimSpace(m.input.Value()) == "" {
				return m.acceptPlan()
			}
			if len(m.commandSuggestions) > 0 {
				index := min(m.suggestionIndex, len(m.commandSuggestions)-1)
				m.input.SetValue(m.commandSuggestions[index])
			}
			return m.submit()
		case "esc":
			if m.queuePicker {
				m.queuePicker = false
				m.status = "Queue selection closed."
				m.updateLayout()
				return m, nil
			}
			if m.planReview {
				m.planReview = false
				m.planMode = false
				m.setRuntimePlanMode(false)
				m.clearQueue()
				m.status = "Plan cancelled."
				m.updateLayout()
				return m, nil
			}
			if m.sessionPicker {
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
			m.interrupt()
			return m, nil
		case "ctrl+c":
			if m.running {
				m.interrupt()
				return m, nil
			}
			return m, tea.Quit
		case "ctrl+b":
			m.detailsOpen = true
			m.updateLayout()
			return m, nil
		case "shift+tab":
			if !m.running && !m.permissionMenu && !m.sessionPicker && !m.queuePicker && !m.detailsOpen {
				return m.cyclePermissionMode()
			}
			return m, nil
		case "ctrl+enter":
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
		case "pgup":
			m.chat.HalfPageUp()
			return m, nil
		case "pgdown":
			m.chat.HalfPageDown()
			return m, nil
		case "ctrl+up":
			m.activity.HalfPageUp()
			return m, nil
		case "ctrl+down":
			m.activity.HalfPageDown()
			return m, nil
		}
		if m.permissionMenu {
			switch message.String() {
			case "up":
				if m.permissionIndex > 0 {
					m.permissionIndex--
				}
			case "down":
				if m.permissionIndex+1 < len(tuiPermissionOptions) {
					m.permissionIndex++
				}
			}
			return m, nil
		}
		if m.sessionPicker {
			switch message.String() {
			case "up":
				if m.sessionIndex > 0 {
					m.sessionIndex--
				}
			case "down":
				if m.sessionIndex+1 < len(m.sessionList) {
					m.sessionIndex++
				}
			}
			m.updateLayout()
			return m, nil
		}
		if m.queuePicker {
			switch message.String() {
			case "up":
				if m.queueIndex > 0 {
					m.queueIndex--
				}
			case "down":
				if m.queueIndex+1 < len(m.queued) {
					m.queueIndex++
				}
			case "delete", "backspace":
				m.deleteQueuedInput()
			}
			m.updateLayout()
			return m, nil
		}
		if m.pendingApproval != nil && m.running {
			switch strings.ToLower(message.String()) {
			case "y":
				m.resolveToolApproval(true)
			case "n":
				m.resolveToolApproval(false)
			}
			return m, nil
		}
		if len(m.commandSuggestions) > 0 {
			switch message.String() {
			case "up":
				if m.suggestionIndex > 0 {
					m.suggestionIndex--
				}
				return m, nil
			case "down":
				if m.suggestionIndex+1 < len(m.commandSuggestions) {
					m.suggestionIndex++
				}
				return m, nil
			case "tab":
				m.input.SetValue(m.commandSuggestions[m.suggestionIndex])
				m.updateCommandSuggestions()
				m.updateLayout()
				return m, nil
			}
		}
		var command tea.Cmd
		m.input, command = m.input.Update(message)
		m.updateCommandSuggestions()
		m.updateLayout()
		return m, command
	default:
		var command tea.Cmd
		m.input, command = m.input.Update(message)
		m.updateCommandSuggestions()
		m.updateLayout()
		return m, command
	}
}

func (m *tuiModel) submit() (tea.Model, tea.Cmd) {
	raw := m.input.Value()
	line := strings.TrimSpace(raw)
	if m.planReview && line != "" && !strings.HasPrefix(line, "/") {
		m.planReview = false
	}
	switch line {
	case "":
		m.status = "Enter a message or command."
		return m, nil
	case "/exit":
		return m, tea.Quit
	case "/resume":
		return m.openSessionPicker()
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
	case "/permissions":
		return m.openPermissionMenu()
	case "/plan":
		return m.togglePlanMode()
	case "/vim":
		return m.toggleVimMode()
	case "/theme":
		m.status = fmt.Sprintf("Current theme: %s%s.",
			m.preferences.Theme, animeLayoutLabel(m.preferences))
		return m, nil
	case "/":
		m.updateCommandSuggestions()
		m.status = "Choose a command; use ↑/↓ to select and Enter to run."
		m.updateLayout()
		return m, nil
	}
	if strings.HasPrefix(line, "/permissions ") || strings.HasPrefix(line, "/permissions\t") {
		return m.setPermissionModeFromCommand(line)
	}
	if strings.HasPrefix(line, "/theme ") || strings.HasPrefix(line, "/theme\t") {
		return m.setThemeFromCommand(line)
	}
	if strings.HasPrefix(line, "/") {
		m.status = "Unknown command."
		m.addEntry("error", fmt.Sprintf("Unknown command %q. Available commands: %s", line, strings.Join(tuiCommands, " ")), true)
		return m, nil
	}

	input, err := parseInputLine(raw)
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
		response, err := m.client.Respond(ctx, input)
		return taskResult{response: response, err: err}
	}, true)
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
	if line == "" {
		m.status = "Type a message before queueing it."
		return m, nil
	}
	if strings.HasPrefix(line, "/") {
		m.status = "Commands cannot be queued while a task is running."
		return m, nil
	}
	input, err := parseInputLine(raw)
	if err != nil {
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	display := inputTranscriptText(input, raw)
	m.queued = append(m.queued, queuedInput{text: raw, display: display, input: input, planMode: m.planMode})
	m.addEntry("queued", display, false)
	m.status = fmt.Sprintf("Queued %d message(s). Enter adds another · Ctrl+Enter interrupts and sends.", len(m.queued))
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
	m.status = "Queue · ↑/↓ select · Enter edit · Delete removes · Esc closes."
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
	m.input.SetValue(queued.text)
	m.planMode = queued.planMode
	m.setRuntimePlanMode(queued.planMode)
	m.queuePicker = false
	m.status = "Editing a queued message; Enter resubmits it."
	m.updateCommandSuggestions()
	m.updateLayout()
	m.persistSession()
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
	m.persistSession()
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
	if pending, err := m.hasPending(); err != nil {
		m.addEntry("error", "Could not inspect paused task: "+err.Error(), true)
		return m, nil
	} else if pending {
		if m.abandonOnFinish {
			if _, err := m.runtime.Discard(context.Background()); err != nil {
				m.addEntry("error", "Could not discard interrupted task: "+err.Error(), true)
				return m, nil
			}
			m.pending = false
			m.abandonOnFinish = false
		} else if err := m.branchPausedSession(); err != nil {
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
	m.setRuntimePlanMode(queued.planMode)
	m.planMode = queued.planMode
	m.cancelled = false
	m.persistSession()
	m.resetInput()
	return m.startTask(func(ctx context.Context) taskResult {
		response, err := m.client.Respond(ctx, queued.input)
		return taskResult{response: response, err: err}
	}, true)
}

func (m *tuiModel) acceptPlan() (tea.Model, tea.Cmd) {
	m.planReview = false
	m.planMode = false
	m.setRuntimePlanMode(false)
	m.addEntry("you", "Implement the approved plan.", false)
	m.resetInput()
	input := model.NewResponseInputText("Implement the approved plan.")
	return m.startTask(func(ctx context.Context) taskResult {
		response, err := m.client.Respond(ctx, input)
		return taskResult{response: response, err: err}
	}, true)
}

func (m *tuiModel) setRuntimePlanMode(enabled bool) {
	if setter, ok := m.client.(interface{ SetPlanMode(bool) }); ok {
		setter.SetPlanMode(enabled)
	}
}

func (m *tuiModel) togglePlanMode() (tea.Model, tea.Cmd) {
	if m.running {
		m.status = "Plan mode can only be changed while idle."
		m.addEntry("notice", m.status, false)
		return m, nil
	}
	m.planMode = !m.planMode
	m.setRuntimePlanMode(m.planMode)
	if m.planMode {
		m.status = "Plan mode · read-only tools are available."
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
		m.status = "Vim mode · insert mode (Esc normal, i insert, h/j/k/l move)."
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
		m.status = "Vim normal mode · i insert · h/j/k/l move · Enter sends."
		return true, nil
	}
	if !m.vimNormal {
		return false, nil
	}
	switch keyName {
	case "i":
		m.vimNormal = false
		m.vimPendingDelete = false
		m.status = "Vim insert mode."
		return true, nil
	case "a":
		var command tea.Cmd
		m.input, command = m.input.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		m.vimNormal = false
		m.status = "Vim insert mode."
		return true, command
	case "h":
		var command tea.Cmd
		m.input, command = m.input.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
		return true, command
	case "j":
		m.input.CursorDown()
		return true, nil
	case "k":
		m.input.CursorUp()
		return true, nil
	case "0":
		m.input.CursorStart()
		return true, nil
	case "$":
		m.input.CursorEnd()
		return true, nil
	case "d":
		if m.vimPendingDelete {
			m.deleteCurrentLine()
			m.vimPendingDelete = false
		} else {
			m.vimPendingDelete = true
		}
		return true, nil
	case "esc":
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
	m.input.SetValue(strings.Join(lines, "\n"))
	if m.input.Value() == "" {
		m.input.SetValue("")
	}
}

func (m *tuiModel) openPermissionMenu() (tea.Model, tea.Cmd) {
	if m.permissions == nil {
		m.status = "Permission settings are unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	m.permissionIndex = 0
	for index, option := range tuiPermissionOptions {
		if option.mode == m.permissions.PermissionMode() {
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
	if m.permissions == nil {
		m.status = "Permission settings are unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	currentIndex := -1
	for index, option := range tuiPermissionOptions {
		if option.mode == m.permissions.PermissionMode() {
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
	return m.applyPermissionMode(tool.PermissionMode(fields[1]))
}

func (m *tuiModel) applyPermissionMode(mode tool.PermissionMode) (tea.Model, tea.Cmd) {
	return m.changePermissionMode(mode, true)
}

func (m *tuiModel) changePermissionMode(mode tool.PermissionMode, resetInput bool) (tea.Model, tea.Cmd) {
	if m.permissions == nil {
		m.status = "Permission settings are unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	if err := m.permissions.SetPermissionMode(mode); err != nil {
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
	m.addEntry("notice", fmt.Sprintf("Permission mode: %s. %s", permissionModeLabel(mode), permissionModeDescription(mode)), false)
	m.refreshPanels()
	return m, nil
}

func permissionModeLabel(mode tool.PermissionMode) string {
	for _, option := range tuiPermissionOptions {
		if option.mode == mode {
			return option.label
		}
	}
	return string(mode)
}

func permissionModeDescription(mode tool.PermissionMode) string {
	return tool.PermissionModeDescription(mode)
}

func (m *tuiModel) resolveToolApproval(approved bool) {
	if m.pendingApproval == nil {
		return
	}
	request := m.pendingApproval
	m.pendingApproval = nil
	request.response <- approved
	if approved {
		m.status = "Tool call approved once."
		m.addEntry("notice", "Approved this tool call once.", false)
	} else {
		m.status = "Tool call denied."
		m.addEntry("notice", "Denied this tool call.", false)
	}
}

func formatToolApproval(request tool.PermissionRequest, workspace ...string) string {
	display := humanizedToolSummary(request.Tool, request.Arguments, workspace...)
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
	return fmt.Sprintf("Allow %s once?%s%s\nPress Y to approve once, N to deny, or Esc to interrupt the task.", display, details, warning)
}

func (m *tuiModel) openSessionPicker() (tea.Model, tea.Cmd) {
	if m.sessions == nil {
		return m.continueTask()
	}
	sessions, err := m.sessions.List(context.Background(), m.workspace, 30)
	if err != nil {
		m.status = "Could not load sessions."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if len(sessions) == 0 {
		m.status = "No saved sessions yet."
		m.addEntry("notice", m.status, false)
		m.resetInput()
		return m, nil
	}
	m.sessionList = sessions
	m.sessionIndex = 0
	for index, saved := range sessions {
		if saved.ID == m.sessionID {
			m.sessionIndex = index
			break
		}
	}
	m.sessionPicker = true
	m.status = "Choose a session to restore; Enter selects, Esc cancels."
	m.resetInput()
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) selectSession() (tea.Model, tea.Cmd) {
	if m.sessionIndex < 0 || m.sessionIndex >= len(m.sessionList) {
		m.sessionPicker = false
		return m, nil
	}
	saved := m.sessionList[m.sessionIndex]
	var state savedTUISession
	if err := json.Unmarshal(saved.Data, &state); err != nil {
		m.sessionPicker = false
		m.status = "Could not restore session."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if setter, ok := m.client.(interface {
		SetSession(string, *model.ResponseInput)
	}); ok {
		setter.SetSession(saved.ID, state.History)
	}
	m.sessionID = saved.ID
	m.clearQueue()
	m.restoreSessionState(state)
	m.planMode = false
	m.planReview = false
	m.setRuntimePlanMode(false)
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
		if loader, ok := m.client.(interface{ PendingPlanMode() (bool, error) }); ok {
			planMode, err := loader.PendingPlanMode()
			if err != nil {
				m.sessionPicker = false
				m.status = "Could not inspect paused task mode."
				m.addEntry("error", err.Error(), true)
				return m, nil
			}
			m.planMode = planMode
			m.setRuntimePlanMode(m.planMode)
		}
	}
	m.sessionPicker = false
	m.status = "Session restored."
	m.resetInput()
	m.persistSession()
	m.refreshPanels()
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) continueTask() (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		m.status = "Task recovery is unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	pending, err := m.runtime.HasPending()
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
	if loader, ok := m.client.(interface{ PendingPlanMode() (bool, error) }); ok {
		planMode, err := loader.PendingPlanMode()
		if err != nil {
			m.status = "Could not inspect paused task mode."
			m.addEntry("error", err.Error(), true)
			return m, nil
		}
		m.planMode = planMode
		m.setRuntimePlanMode(planMode)
	}
	m.addEntry("notice", "Continuing the paused task; a model request or tool call may repeat.", true)
	m.resetInput()
	m.cancelled = false
	return m.startTask(func(ctx context.Context) taskResult {
		response, err := m.runtime.Resume(ctx)
		return taskResult{response: response, err: err}
	}, false)
}

func (m *tuiModel) newSession() (tea.Model, tea.Cmd) {
	id, err := newSessionID()
	if err != nil {
		m.status = "Could not create a session."
		m.addEntry("error", err.Error(), true)
		return m, nil
	}
	if setter, ok := m.client.(interface {
		SetSession(string, *model.ResponseInput)
	}); ok {
		setter.SetSession(id, nil)
	}
	m.sessionID = id
	m.clearQueue()
	m.entries = nil
	m.activities = nil
	m.usageRequests = nil
	m.taskUsage = tokenUsageTotals{}
	m.sessionUsage = tokenUsageTotals{}
	m.planMode = false
	m.planReview = false
	m.setRuntimePlanMode(false)
	m.pending = false
	m.status = "New session."
	m.resetInput()
	m.persistSession()
	m.refreshPanels()
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) branchPausedSession() error {
	id, err := newSessionID()
	if err != nil {
		return err
	}
	var history *model.ResponseInput
	if source, ok := m.client.(interface{ SessionHistory() *model.ResponseInput }); ok {
		history = source.SessionHistory()
	}
	if setter, ok := m.client.(interface {
		SetSession(string, *model.ResponseInput)
	}); ok {
		setter.SetSession(id, history)
	}
	m.sessionID = id
	m.pending = false
	m.status = "Started a new branch from the last completed turn."
	m.addEntry("notice", "Started a new branch from completed history; the paused task remains in its original session.", false)
	m.persistSession()
	return nil
}

func (m *tuiModel) persistSession() {
	if m.sessions == nil || m.sessionID == "" || m.workspace == "" {
		return
	}
	state := savedTUISession{}
	if source, ok := m.client.(interface{ SessionHistory() *model.ResponseInput }); ok {
		state.History = source.SessionHistory()
	}
	if len(m.entries) > 500 {
		m.entries = append([]tuiEntry(nil), m.entries[len(m.entries)-500:]...)
	}
	state.Entries = make([]savedTUIEntry, 0, len(m.entries))
	for _, entry := range m.entries {
		if entry.role == "queued" {
			continue
		}
		state.Entries = append(state.Entries, savedTUIEntry{
			Role: entry.role, Text: entry.text, Error: entry.err, ActivityID: entry.activityID,
		})
	}
	state.Activities = make([]savedActivity, 0, len(m.activities))
	for _, activity := range m.activities {
		state.Activities = append(state.Activities, savedActivity{
			CallID: activity.callID, Summary: activity.summary, Status: activity.status, Duration: int64(activity.duration),
		})
	}
	state.UsageRequests = make([]savedUsageRecord, 0, len(m.usageRequests))
	for _, record := range m.usageRequests {
		state.UsageRequests = append(state.UsageRequests, savedUsageRecord{
			Request: record.request, Usage: record.usage, Available: record.available,
		})
	}
	state.TaskUsage = saveUsageTotals(m.taskUsage)
	state.SessionUsage = saveUsageTotals(m.sessionUsage)
	data, err := json.Marshal(state)
	if err == nil {
		err = m.sessions.Save(context.Background(), session.Snapshot{
			ID: m.sessionID, Workspace: m.workspace, Data: data,
		})
	}
	if err != nil {
		m.status = "Session save failed: " + err.Error()
	}
}

func (m *tuiModel) persistSessionHistory() {
	if m.sessions == nil || m.sessionID == "" || m.workspace == "" {
		return
	}
	source, ok := m.client.(interface{ SessionHistory() *model.ResponseInput })
	if !ok {
		return
	}
	data, err := json.Marshal(source.SessionHistory())
	if err == nil {
		err = m.sessions.UpdateHistory(context.Background(), m.sessionID, m.workspace, data)
	}
	if err != nil {
		m.status = "Session history save failed: " + err.Error()
	}
}

func (m *tuiModel) restoreSessionState(state savedTUISession) {
	m.entries = make([]tuiEntry, 0, len(state.Entries))
	for _, entry := range state.Entries {
		text := entry.Text
		if entry.Role == "tool" {
			text = humanizeSavedToolEntry(text)
		}
		m.entries = append(m.entries, tuiEntry{
			role: entry.Role, text: text, err: entry.Error, activityID: entry.ActivityID,
		})
	}
	m.activities = make([]tuiActivity, 0, len(state.Activities))
	for _, activity := range state.Activities {
		summary := activity.Summary
		if summary == "" {
			summary = tool.DisplaySummary(activity.Name, nil)
		}
		summary = absoluteToolSummary(summary, m.workspace)
		m.activities = append(m.activities, tuiActivity{
			callID: activity.CallID, summary: summary, status: activity.Status, duration: time.Duration(activity.Duration),
		})
	}
	m.usageRequests = make([]tokenUsageRecord, 0, len(state.UsageRequests))
	for _, record := range state.UsageRequests {
		m.usageRequests = append(m.usageRequests, tokenUsageRecord{
			request: record.Request, usage: record.Usage, available: record.Available,
		})
	}
	m.taskUsage = loadUsageTotals(state.TaskUsage)
	m.sessionUsage = loadUsageTotals(state.SessionUsage)
}

func humanizeSavedToolEntry(text string) string {
	name, status, found := strings.Cut(text, " · ")
	if !found || !strings.Contains(name, "_") {
		return text
	}
	return tool.DisplaySummary(name, nil) + " · " + status
}

func saveUsageTotals(totals tokenUsageTotals) savedUsageTotals {
	return savedUsageTotals{
		Requests: totals.requests, Measured: totals.measured, Input: totals.input,
		Output: totals.output, Reasoning: totals.reasoning, Total: totals.total, Cached: totals.cached,
	}
}

func loadUsageTotals(totals savedUsageTotals) tokenUsageTotals {
	return tokenUsageTotals{
		requests: totals.Requests, measured: totals.Measured, input: totals.Input,
		output: totals.Output, reasoning: totals.Reasoning, total: totals.Total, cached: totals.Cached,
	}
}

func newSessionID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("create session id: %w", err)
	}
	return hex.EncodeToString(random[:]), nil
}

func historyBeforePendingTurn(history *model.ResponseInput) *model.ResponseInput {
	items := responseHistoryItems(history)
	cut := len(items)
	for index := len(items) - 1; index >= 0; index-- {
		message, ok := items[index].(model.InputMessage)
		if ok && message.Role == model.RoleUser {
			cut = index
			break
		}
	}
	return model.NewResponseInputItems(items[:cut]...)
}

func transcriptFromHistory(history *model.ResponseInput, workspace ...string) []savedTUIEntry {
	var entries []savedTUIEntry
	for _, item := range responseHistoryItems(history) {
		switch value := item.(type) {
		case model.InputMessage:
			role := "notice"
			switch value.Role {
			case model.RoleUser:
				role = "you"
			case model.RoleAssistant:
				role = "assistant"
			}
			if text := messageContentText(value.Content); text != "" {
				entries = append(entries, savedTUIEntry{Role: role, Text: text})
			}
		case model.InputFunctionCall:
			entries = append(entries, savedTUIEntry{Role: "tool", Text: humanizedToolSummary(value.Name, json.RawMessage(value.Arguments), workspace...) + " · done"})
		case model.InputFunctionCallOutput:
			// Tool output may contain file contents or command output. Keep the
			// conversation transcript limited to the sanitized call lifecycle.
		}
	}
	return entries
}

func messageContentText(content model.MessageContent) string {
	if content.Text != nil {
		return *content.Text
	}
	var text strings.Builder
	for _, part := range content.Parts {
		switch value := part.(type) {
		case model.InputText:
			text.WriteString(value.Text)
		case model.OutputText:
			text.WriteString(value.Text)
		case model.InputImage:
			text.WriteString("[image]")
		case model.ReasoningText:
			continue
		default:
			text.WriteString("[multimodal content]")
		}
	}
	return text.String()
}

func inputTranscriptText(input *model.ResponseInput, fallback string) string {
	if input == nil {
		return fallback
	}
	if input.Text != nil {
		return *input.Text
	}
	var parts []string
	for _, item := range input.Items {
		switch value := item.(type) {
		case model.InputMessage:
			if text := messageContentText(value.Content); text != "" {
				parts = append(parts, text)
			} else {
				parts = append(parts, "[multimodal content]")
			}
		case model.InputFunctionCall:
			parts = append(parts, fmt.Sprintf("%s(%s)", value.Name, value.Arguments))
		default:
			parts = append(parts, "[structured input item]")
		}
	}
	if len(parts) == 0 {
		return "[structured input]"
	}
	return strings.Join(parts, "\n")
}

func toolOutputText(output model.ToolOutput) string {
	if output.Text != nil {
		return *output.Text
	}
	var text strings.Builder
	for _, part := range output.Parts {
		switch value := part.(type) {
		case model.InputText:
			text.WriteString(value.Text)
		case model.OutputText:
			text.WriteString(value.Text)
		}
	}
	return text.String()
}

func (m *tuiModel) compact() (tea.Model, tea.Cmd) {
	runtime, ok := m.client.(compactionResponder)
	if !ok {
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
		compactedTurns, err := runtime.Compact(ctx)
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
	if m.runtime == nil {
		m.status = "Task recovery is unavailable."
		m.addEntry("error", m.status, true)
		return m, nil
	}
	discarded, err := m.runtime.Discard(context.Background())
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
	m.updateCommandSuggestions()
	m.updateLayout()
}

func (m *tuiModel) hasPending() (bool, error) {
	if m.runtime == nil {
		return false, nil
	}
	pending, err := m.runtime.HasPending()
	m.pending = pending
	return pending, err
}

func (m *tuiModel) startTask(operation func(context.Context) taskResult, resetUsage bool) (tea.Model, tea.Cmd) {
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

	if result.response != nil {
		text, err := responseDisplayText(result.response)
		if err != nil {
			m.addEntry("error", err.Error(), true)
		} else if text != "" {
			m.addEntry("assistant", text, false)
		}
	}
	if result.notice != "" {
		m.addEntry("notice", result.notice, false)
	}

	pending, pendingErr := m.hasPending()
	if pendingErr != nil {
		m.addEntry("error", "Could not inspect interrupted task: "+pendingErr.Error(), true)
	}
	if m.abandonOnFinish {
		if pending && m.runtime != nil {
			if _, err := m.runtime.Discard(context.Background()); err != nil {
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
		m.setRuntimePlanMode(false)
	}
	m.cancelled = false
	m.persistSession()
	m.persistSessionHistory()
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
	commands := tuiCommands
	if m.sessions == nil {
		commands = legacyTUICommands
	}
	for _, command := range commands {
		if strings.HasPrefix(command, line) {
			m.commandSuggestions = append(m.commandSuggestions, command)
		}
	}
}

func (m *tuiModel) updateModelUsage(usage model.ResponseUsage) {
	available := usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.OutputTokensDetails.ReasoningTokens != 0 ||
		usage.TotalTokens != 0 || usage.InputTokensDetails.CachedTokens != 0
	requestNumber := m.sessionUsage.requests + 1
	record := tokenUsageRecord{request: requestNumber, usage: usage, available: available}
	m.taskUsage.add(usage, available)
	m.sessionUsage.add(usage, available)
	m.usageRequests = append(m.usageRequests, record)
	if len(m.usageRequests) > 200 {
		m.usageRequests = append([]tokenUsageRecord(nil), m.usageRequests[len(m.usageRequests)-200:]...)
	}
}

func (totals *tokenUsageTotals) add(usage model.ResponseUsage, available bool) {
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
	if !record.available {
		return fmt.Sprintf("#%d usage unavailable", record.request)
	}
	return fmt.Sprintf("#%d · in %s · out %s · reason %s · total %s · cached %s (%s)",
		record.request,
		compactTokens(record.usage.InputTokens),
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
		return fmt.Sprintf("%s: usage unavailable (%d request(s))", label, totals.requests)
	}
	return fmt.Sprintf("%s · in %s · out %s · reason %s · total %s · cache %s (%s)",
		label,
		compactTokens(totals.input),
		compactTokens(totals.output),
		compactTokens(totals.reasoning),
		compactTokens(totals.total),
		compactTokens(totals.cached),
		cacheHitRate(totals.input, totals.cached),
	)
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

func entryBadge(role string) string {
	switch role {
	case "you":
		return "YOU"
	case "assistant":
		return "LUNE"
	case "tool":
		return "TOOL"
	case "queued":
		return "QUEUED"
	case "permission":
		return "PERMISSION"
	case "error":
		return "ERROR"
	case "notice":
		return "NOTE"
	default:
		return strings.ToUpper(role)
	}
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

func renderTerminalMarkdown(markdown string, colors themeColors) string {
	var rendered strings.Builder
	inCode := false
	for _, line := range strings.Split(markdown, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCode = !inCode
			continue
		}
		switch {
		case inCode:
			rendered.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Code)).Render("  │ " + line))
		case strings.HasPrefix(trimmed, "### "):
			rendered.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colors.Accent)).Render(strings.TrimPrefix(trimmed, "### ")))
		case strings.HasPrefix(trimmed, "## "):
			rendered.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colors.Accent)).Render(strings.TrimPrefix(trimmed, "## ")))
		case strings.HasPrefix(trimmed, "# "):
			rendered.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colors.Accent)).Render(strings.TrimPrefix(trimmed, "# ")))
		case strings.HasPrefix(trimmed, "> "):
			rendered.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render("│ " + strings.TrimPrefix(trimmed, "> ")))
		case strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* "):
			rendered.WriteString("• " + trimmed[2:])
		default:
			rendered.WriteString(line)
		}
		rendered.WriteByte('\n')
	}
	return strings.TrimSuffix(rendered.String(), "\n")
}

func cacheHitRate(inputTokens, cachedTokens int64) string {
	if inputTokens <= 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", float64(cachedTokens)*100/float64(inputTokens))
}

func (m *tuiModel) updateToolActivity(event tool.ToolEvent) {
	summary := event.DisplaySummary
	if summary == "" {
		summary = tool.DisplaySummary(event.Name, nil)
	}
	summary = absoluteToolSummary(summary, m.workspace)
	index := -1
	for i := len(m.activities) - 1; i >= 0; i-- {
		if event.CallID != "" && m.activities[i].callID == event.CallID {
			index = i
			break
		}
	}
	if event.Started {
		activity := tuiActivity{callID: event.CallID, summary: summary, status: "running"}
		if index >= 0 {
			m.activities[index] = activity
		} else {
			m.activities = append(m.activities, activity)
			index = len(m.activities) - 1
		}
	} else {
		if index < 0 {
			m.activities = append(m.activities, tuiActivity{callID: event.CallID, summary: summary})
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
	if event.CallID != "" {
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].activityID == event.CallID {
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
			role: "tool", text: entryText, err: activity.status == "failed", activityID: event.CallID,
		})
	}
	if len(m.activities) > 200 {
		m.activities = append([]tuiActivity(nil), m.activities[len(m.activities)-200:]...)
	}
}

func humanizedToolSummary(name string, arguments json.RawMessage, workspace ...string) string {
	summary := tool.DisplaySummary(name, arguments)
	if len(workspace) > 0 {
		summary = absoluteToolSummary(summary, workspace[0])
	}
	return summary
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
	m.persistSession()
	m.refreshPanels()
}

func (m *tuiModel) refreshPanels() {
	chatAtBottom := m.chat.AtBottom()
	activityAtBottom := m.activity.AtBottom()
	colors := colorsFor(m.preferences.Theme)
	var messages strings.Builder
	for i, entry := range m.entries {
		if i > 0 {
			messages.WriteString("\n\n")
		}
		badge := entryBadge(entry.role)
		style := lipgloss.NewStyle().Foreground(lipgloss.Color(m.entryColor(entry.role, colors))).Bold(true)
		if entry.err {
			style = style.Foreground(lipgloss.Color(colors.Error))
		}
		if entry.role == "tool" {
			messages.WriteString(style.Render("  ↳ " + entry.text))
			continue
		}
		messages.WriteString(style.Render(badge))
		messages.WriteByte('\n')
		if entry.role == "assistant" {
			messages.WriteString(renderTerminalMarkdown(entry.text, colors))
		} else {
			messages.WriteString(entry.text)
		}
	}
	if messages.Len() == 0 {
		messages.WriteString("Start a task, or use /resume to browse saved sessions.")
	}
	m.chat.SetContent(messages.String())
	if chatAtBottom {
		m.chat.GotoBottom()
	}

	var activities strings.Builder
	if len(m.activities) == 0 {
		activities.WriteString("No tool activity yet.")
	}
	for i, activity := range m.activities {
		if i > 0 {
			activities.WriteByte('\n')
		}
		icon := activityBadge(activity.status)
		line := lipgloss.NewStyle().Foreground(lipgloss.Color(activityColor(activity.status, colors))).Bold(true).Render(icon) + " " + activity.summary + " · " + activity.status
		if activity.status != "running" && activity.duration > 0 {
			line += "  " + activity.duration.Round(time.Millisecond).String()
		}
		activities.WriteString(line)
	}
	activities.WriteString("\n\nAgent · " + stateName(m.visualState()) + " · single agent")
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
	activities.WriteByte('\n')
	activities.WriteString(m.taskUsage.String("Task"))
	activities.WriteByte('\n')
	activities.WriteString(m.sessionUsage.String("Session"))
	activities.WriteString(fmt.Sprintf("\nMetered requests: task %d/%d · session %d/%d", m.taskUsage.measured, m.taskUsage.requests, m.sessionUsage.measured, m.sessionUsage.requests))
	if m.permissions != nil {
		activities.WriteString("\nPermissions: " + permissionModeLabel(m.permissions.PermissionMode()))
	}
	m.activity.SetContent(activities.String())
	if activityAtBottom {
		m.activity.GotoBottom()
	}
}

func (m *tuiModel) entryColor(role string, colors themeColors) string {
	switch role {
	case "you":
		return colors.User
	case "assistant":
		return colors.Assistant
	case "tool":
		return colors.Tool
	case "permission":
		return colors.Warning
	case "error":
		return colors.Error
	case "queued":
		return colors.Secondary
	default:
		return colors.Muted
	}
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
	m.input.SetWidth(contentWidth)
	bodyHeight := max(1, m.height-lipgloss.Height(m.renderHeader(contentWidth))-1-m.input.Height()-m.commandSuggestionHeight()-2)
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
		state.Selection = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Foreground)).Background(lipgloss.Color(colors.Secondary))
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
		return min(len(m.sessionList), 8) + 1
	}
	if m.permissionMenu {
		return len(tuiPermissionOptions) + 2
	}
	if len(m.commandSuggestions) == 0 {
		return 0
	}
	return len(m.commandSuggestions) + 1
}

func (m *tuiModel) View() tea.View {
	width := max(1, m.width)
	header := m.renderHeader(width)
	colors := colorsFor(m.preferences.Theme)
	divider := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render(strings.Repeat("─", width))
	body := m.renderBody(width)
	suggestions := m.renderCommandSuggestions()
	footer := m.renderFooter(width)
	parts := []string{header, body, divider, m.input.View()}
	if m.sessionPicker {
		parts = append(parts, m.renderSessionPicker())
	} else if m.queuePicker {
		parts = append(parts, m.renderQueuePicker())
	} else if m.planReview {
		parts = append(parts, m.renderPlanReview())
	} else if m.permissionMenu {
		parts = append(parts, m.renderPermissionMenu())
	} else if suggestions != "" {
		parts = append(parts, suggestions)
	}
	parts = append(parts, footer)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	view := tea.NewView(content)
	view.BackgroundColor = backgroundColor(colors.Background)
	view.ForegroundColor = backgroundColor(colors.Foreground)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	if cursor := m.input.Cursor(); cursor != nil && !m.sessionPicker && !m.queuePicker && !m.permissionMenu {
		cursor.Y += lipgloss.Height(header) + lipgloss.Height(body) + lipgloss.Height(divider)
		view.Cursor = cursor
	}
	return view
}

func (m *tuiModel) renderHeader(width int) string {
	colors := colorsFor(m.preferences.Theme)
	headerText := "☾ Lune Agent"
	if m.preferences.Theme == themeAnime {
		headerText += " · Anime / " + string(m.preferences.AnimeLayout)
	}
	headerText += " · " + stateName(m.visualState())
	if m.sessionID != "" {
		headerText += "  ·  " + m.sessionID[:min(8, len(m.sessionID))]
	}
	header := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colors.Accent)).Render(headerText)
	if m.preferences.Theme == themeAnime && !m.studioLayout() && width >= 80 && m.height >= 24 {
		avatar := m.renderAvatar(8, 4)
		if m.canRenderColorArt() {
			return lipgloss.JoinHorizontal(lipgloss.Center, header, "   ", avatar)
		}
		header = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colors.Accent)).Render(
			stateGlyph(m.visualState()) + " " + headerText)
	}
	return header
}

func (m *tuiModel) renderFooter(width int) string {
	colors := colorsFor(m.preferences.Theme)
	mode := "BUILD"
	if m.planMode {
		mode = "PLAN"
	}
	metrics := mode
	metrics += " · " + string(m.preferences.Theme) + animeLayoutLabel(m.preferences)
	if m.permissions != nil {
		metrics += " · " + permissionModeLabel(m.permissions.PermissionMode())
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
		status = "Activity & usage · ↑/↓ scroll · Ctrl+B or Esc close"
	case m.pendingApproval != nil:
		status = "Approval required · Y allow once · N deny · Esc pause"
	case m.running:
		status = fmt.Sprintf("Working · %d queued · Esc pause · Ctrl+Enter interrupt/send", len(m.queued))
	case m.pending:
		status = "Paused task · /continue to resume"
	case m.status == "Ready":
		status = "/ commands · Enter send · Shift+Tab permissions · /plan planning · Ctrl+B activity"
	}
	if m.vimMode {
		vimState := "VIM INSERT"
		if m.vimNormal {
			vimState = "VIM NORMAL"
		}
		status = vimState + " · " + status
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
	lines := []string{"Queued messages · ↑/↓ select · Enter edit · Delete removes · Esc closes"}
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
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Bold(true).Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderPlanReview() string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(colorsFor(m.preferences.Theme).Warning)).Bold(true).Render(
		"Plan review · Enter accepts · type feedback and Enter to revise · Esc cancels",
	)
}

func (m *tuiModel) renderCommandSuggestions() string {
	colors := colorsFor(m.preferences.Theme)
	if m.permissionMenu || len(m.commandSuggestions) == 0 {
		return ""
	}
	lines := make([]string, 0, len(m.commandSuggestions)+1)
	for index, command := range m.commandSuggestions {
		line := "  " + command
		if index == m.suggestionIndex {
			line = "› " + command
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Bold(true).Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "  ↑/↓ select · Enter run")
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderSessionPicker() string {
	colors := colorsFor(m.preferences.Theme)
	lines := []string{"Saved sessions · ↑/↓ select · Enter restore · Esc cancel"}
	start := max(0, m.sessionIndex-4)
	end := min(len(m.sessionList), start+8)
	if end == len(m.sessionList) {
		start = max(0, end-8)
	}
	for index, saved := range m.sessionList[start:end] {
		index += start
		var state savedTUISession
		_ = json.Unmarshal(saved.Data, &state)
		preview := "empty session"
		for _, entry := range state.Entries {
			if entry.Role == "you" {
				preview = strings.Join(strings.Fields(entry.Text), " ")
				if len([]rune(preview)) > 44 {
					preview = string([]rune(preview)[:41]) + "..."
				}
				break
			}
		}
		line := fmt.Sprintf("  %s  %s", saved.UpdatedAt.Local().Format("01-02 15:04"), preview)
		if saved.ID == m.sessionID {
			line += "  (current)"
		}
		if index == m.sessionIndex {
			line = "›" + strings.TrimPrefix(line, " ")
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Bold(true).Render(line)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderPermissionMenu() string {
	colors := colorsFor(m.preferences.Theme)
	lines := []string{"Permission mode · current: " + permissionModeLabel(m.permissions.PermissionMode())}
	for index, option := range tuiPermissionOptions {
		line := fmt.Sprintf("  %-12s %s", option.label, option.description)
		if index == m.permissionIndex {
			line = fmt.Sprintf("› %-12s %s", option.label, option.description)
			line = lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Bold(true).Render(line)
		}
		lines = append(lines, line)
	}
	lines = append(lines, "  ↑/↓ select · Enter apply · Esc cancel")
	return strings.Join(lines, "\n")
}

func (m *tuiModel) renderBody(width int) string {
	colors := colorsFor(m.preferences.Theme)
	if m.detailsOpen {
		bodyHeight := max(1, m.chat.Height())
		if bodyHeight < 5 {
			return m.activity.View()
		}
		title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(colors.Accent)).Render("Activity & usage")
		hint := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Muted)).Render("↑/↓ scroll · Ctrl+B or Esc close")
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
		label := lipgloss.NewStyle().Foreground(lipgloss.Color(colors.Accent)).Bold(true).
			Render("STUDIO · " + stateName(m.visualState()) + " · single agent")
		return lipgloss.JoinVertical(lipgloss.Left, m.chat.View(), separator, label, m.activity.View())
	}
	return m.chat.View()
}

func responseDisplayText(response *model.Response) (string, error) {
	if response == nil {
		return "", nil
	}
	lines := make([]string, 0, len(response.Output))
	for _, item := range response.Output {
		message, ok := item.(model.OutputMessage)
		if !ok {
			if _, reasoning := item.(model.OutputReasoning); reasoning {
				continue
			}
			if _, toolCall := item.(model.OutputFunctionCall); toolCall {
				// Tool lifecycle events render a sanitized summary elsewhere.
				continue
			}
			if _, toolCall := item.(model.OutputCustomToolCall); toolCall {
				continue
			}
			encoded, err := json.Marshal(item)
			if err != nil {
				return "", fmt.Errorf("encode response output: %w", err)
			}
			lines = append(lines, string(encoded))
			continue
		}
		var messageText strings.Builder
		for _, part := range message.Content {
			if text, ok := part.(model.OutputText); ok {
				messageText.WriteString(text.Text)
			}
		}
		if messageText.Len() > 0 {
			lines = append(lines, messageText.String())
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}
