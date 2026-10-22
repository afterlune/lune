package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

type builtinCommandSpec struct {
	Command             string
	Description         string
	AvailableDuringTask bool
}

var builtinCommands = []builtinCommandSpec{
	{Command: "/new", Description: "start a new chat during a conversation"},
	{Command: "/clear", Description: "clear transcript history from the current view"},
	{Command: "/plan", Description: "toggle Plan mode (read-only exploration)"},
	{Command: "/permissions", Description: "choose a permission mode: read-only, workspace, ask, full-access", AvailableDuringTask: true},
	{Command: "/model", Description: "choose an active model profile", AvailableDuringTask: true},
	{Command: "/effort", Description: "choose the reasoning effort level interactively", AvailableDuringTask: true},
	{Command: "/theme", Description: "choose a persisted theme: auto, dark, light", AvailableDuringTask: true},
	{Command: "/memory", Description: "view and manage workspace long-term memories", AvailableDuringTask: true},
	{Command: "/agents", Description: "inspect live agents and their context, cache, speed, and usage", AvailableDuringTask: true},
	{Command: "/ps", Description: "list background processes and daemon commands in session", AvailableDuringTask: true},
	{Command: "/stop", Description: "choose a running process to stop, or use /stop <id|all>", AvailableDuringTask: true},
	{Command: "/logs", Description: "choose a process to inspect, or use /logs <id>", AvailableDuringTask: true},
	{Command: "/sessions", Description: "list or search recent sessions in this workspace", AvailableDuringTask: true},
	{Command: "/resume", Description: "switch to a saved session or open the session switcher (Ctrl+O)"},
	{Command: "/drop", Description: "choose a saved session to delete with confirmation"},
	{Command: "/detail", Description: "open tool output pager modal: /detail [index] (Ctrl+T)", AvailableDuringTask: true},
	{Command: "/title", Description: "view or set custom title for this session", AvailableDuringTask: true},
	{Command: "/export", Description: "export session transcript: /export [md|html|jsonl] [file]"},
	{Command: "/context", Description: "show context window budget, system rules, and capabilities", AvailableDuringTask: true},
	{Command: "/diff", Description: "inspect git code changes in workspace modal (Ctrl+D)", AvailableDuringTask: true},
	{Command: "/review", Description: "run a read-only model review of workspace changes"},
	{Command: "/mcp", Description: "list and interactively manage external MCP servers", AvailableDuringTask: true},
	{Command: "/cancel-queue", Description: "cancel the most recently queued task", AvailableDuringTask: true},
	{Command: "/compact", Description: "compact older conversation history"},
	{Command: "/fork", Description: "fork the current chat"},
	{Command: "/rewind", Description: "rewind session history: /rewind [N] or /rewind preview [N]"},
	{Command: "/status", Description: "show session, model and token status", AvailableDuringTask: true},
	{Command: "/continue", Description: "continue the paused task in this session"},
	{Command: "/discard", Description: "discard the paused task in this session"},
	{Command: "/shortcuts", Description: "display keyboard shortcuts help", AvailableDuringTask: true},
	{Command: "/thought", Description: "toggle visibility of latest thinking/reasoning block (Ctrl+R)", AvailableDuringTask: true},
	{Command: "/help", Description: "list all available slash commands", AvailableDuringTask: true},
	{Command: "/exit", Description: "exit the chat session"},
}

func filterCommands(prefix string) []AutocompleteItem {
	prefix = strings.ToLower(prefix)
	var matches []AutocompleteItem
	for _, item := range builtinCommands {
		if strings.HasPrefix(strings.ToLower(item.Command), prefix) {
			matches = append(matches, AutocompleteItem{Command: item.Command, Description: item.Description})
		}
	}
	return matches
}

func (m *tuiModel) commandsForAutocomplete(prefix string) []AutocompleteItem {
	items := filterCommands(prefix)
	if !m.taskBusy() {
		return items
	}
	var available []AutocompleteItem
	for _, item := range items {
		if item.Command == "/memory" || item.Command == "/mcp" {
			available = append(available, item)
			continue
		}
		for _, spec := range builtinCommands {
			if spec.Command == item.Command && spec.AvailableDuringTask {
				available = append(available, item)
				break
			}
		}
	}
	return available
}

func (m *tuiModel) taskBusy() bool {
	return m.running || len(m.queuedTurns) > 0
}

func (m *tuiModel) slashCommandUnavailable(raw string) string {
	if !m.taskBusy() {
		return ""
	}
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return ""
	}
	name := strings.ToLower(parts[0])
	var spec *builtinCommandSpec
	for i := range builtinCommands {
		if builtinCommands[i].Command == name {
			spec = &builtinCommands[i]
			break
		}
	}
	if spec == nil || spec.AvailableDuringTask {
		switch name {
		case "/mcp":
			if len(parts) == 1 || strings.EqualFold(parts[1], "list") {
				return ""
			}
			return "Only `/mcp list` is available while a task is running."
		case "/memory":
			if len(parts) == 1 {
				return ""
			}
			switch strings.ToLower(parts[1]) {
			case "add", "remove", "clear":
				return "Workspace memory changes are unavailable while a task is running."
			default:
				return ""
			}
		}
	}
	if spec != nil && spec.AvailableDuringTask {
		return ""
	}
	switch name {
	case "/rewind":
		return "Cannot rewind while a task is running."
	case "/resume":
		return "Cannot switch sessions while a task is running. Cancel or wait for it to finish."
	case "/drop":
		return "Cannot delete sessions while a task is running."
	case "/new":
		return "Cannot start a new session while a task is running."
	case "/fork":
		return "Cannot fork a session while a task is running."
	case "/continue":
		return "A task is already running."
	case "/discard":
		return "Cannot discard a paused task while another task is running."
	case "/exit":
		return "Interrupt the active task before exiting."
	}
	return fmt.Sprintf("`%s` is unavailable while a task is running. Interrupt the task or wait for it to finish.", name)
}

// handleSlashCommand executes slash commands entered by the user.
func (m *tuiModel) handleSlashCommand(raw string) (tea.Model, tea.Cmd) {
	if reason := m.slashCommandUnavailable(raw); reason != "" {
		m.addSystemEntry(reason)
		return m, nil
	}
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return m, nil
	}
	cmdName := strings.ToLower(parts[0])

	switch cmdName {
	case "/exit":
		m.cancelMCPAuthentication(false)
		return m, tea.Quit

	case "/help":
		var lines []string
		lines = append(lines, "**Available slash commands:**")
		for _, item := range builtinCommands {
			lines = append(lines, fmt.Sprintf("- `%s`: %s", item.Command, item.Description))
		}
		lines = append(lines, "\nPress `?` when composer is empty to view the keyboard shortcut cheat sheet.")
		m.addSystemEntry(strings.Join(lines, "\n"))
		return m, nil

	case "/new":
		if m.taskBusy() {
			m.addSystemEntry("Cannot start a new session while a task is running.")
			return m, nil
		}
		return m, m.startNewSession()

	case "/clear":
		m.entries = nil
		m.currentStreamText = ""
		m.viewport.SetContent("")
		m.scrolledUp = false
		m.isSelecting = false
		m.selectedText = ""
		m.selectStartX = 0
		m.selectEndX = 0
		m.selectStartY = 0
		m.selectEndY = 0
		m.invalidateEntryCache()
		m.updateLayout()
		return m, nil

	case "/thought":
		return m.toggleReasoningExpansion()

	case "/theme":
		if len(parts) > 1 {
			target := strings.ToLower(parts[1])
			m.applyThemeMode(ThemeMode(target))
		} else {
			m.openThemePicker()
		}
		return m, nil

	case "/sessions":
		return m.handleSessionsCommand(parts)
	case "/resume":
		return m.handleResumeCommand(parts)

	case "/drop":
		return m.handleDropCommand(parts)

	case "/detail":
		target := -1
		if len(parts) > 1 {
			n, err := strconv.Atoi(parts[1])
			if err != nil || n < 1 || len(parts) > 2 {
				m.addSystemEntry("Usage: `/detail [positive-index]`.")
				return m, nil
			}
			target = n - 1
		}
		return m.openToolPager(target)

	case "/rewind":
		return m.handleRewindCommand(parts)
	case "/title":
		return m.handleTitleCommand(parts)
	case "/diff":
		targetPath := ""
		if len(parts) > 1 {
			targetPath = strings.Join(parts[1:], " ")
		}
		return m.openDiffModal(targetPath)

	case "/review":
		return m.handleReviewCommand(parts)
	case "/mcp":
		return m.handleMCPCommand(parts)
	case "/cancel-queue":
		return m.cancelQueuedTurn()

	case "/plan":
		return m.handlePlanCommand(parts)
	case "/permissions":
		return m.handlePermissionsCommand(parts)
	case "/model":
		return m.handleModelCommand(parts)
	case "/export":
		return m.handleExportCommand(parts)
	case "/memory":
		return m.handleMemoryCommand(parts)

	case "/ps":
		return m.handlePsCommand(parts)

	case "/stop":
		return m.handleStopCommand(parts)

	case "/logs":
		return m.handleLogsCommand(parts)

	case "/agents":
		return m.handleAgentsCommand(parts)

	case "/effort":
		return m.handleEffortCommand(parts)

	case "/context":
		contextText := m.renderContextDiagnostics()
		m.addSystemEntry(contextText)
		return m, nil

	case "/status":
		statusText := m.renderStatusTable()
		m.addSystemEntry(statusText)
		return m, nil

	case "/compact":
		return m, func() tea.Msg {
			n, err := m.client.Compact(context.Background(), m.sessionID)
			return compactDoneMsg{trimmed: n, err: err}
		}

	case "/fork":
		return m, func() tea.Msg {
			session, err := m.client.ForkSession(context.Background(), m.sessionID)
			return sessionForkedMsg{session: session, err: err}
		}

	case "/continue":
		if len(parts) > 1 {
			m.addSystemEntry("Usage: `/continue` continues the paused task in this session.")
			return m, nil
		}
		if m.taskBusy() {
			m.addSystemEntry("A task is already running.")
			return m, nil
		}
		if m.runtime != nil {
			state, err := m.runtime.State(context.Background())
			if (err == nil && state.Task.Status == core.TaskPaused) || m.hasPausedTask() {
				m.running = true
				m.taskStartedAt = time.Now()
				m.pausedCheckpoint = nil
				m.activeRecovery = nil
				m.activeToolRecovery = nil
				m.addSystemEntry("Resuming execution from checkpoint...")
				m.updateLayout()
				sessionID := m.sessionID
				return m, func() tea.Msg {
					sub, err := m.runtime.Recover(context.Background())
					return turnSubmittedMsg{sessionID: sessionID, submission: sub, err: err}
				}
			}
		}
		m.addSystemEntry("No paused task to continue. Use `/resume` to switch sessions.")
		return m, nil

	case "/discard":
		return m.discardPausedTask(discardSourceCommand)

	case "/shortcuts":
		m.shortcutsOpen = true
		m.updateLayout()
		return m, nil
	}

	m.addSystemEntry(fmt.Sprintf("Unknown command: `%s`. Type `/help` for options.", raw))
	return m, nil
}

func (m *tuiModel) cancelQueuedTurn() (tea.Model, tea.Cmd) {
	if len(m.queuedTurns) == 0 {
		m.addSystemEntry("No queued tasks to cancel.")
		return m, nil
	}
	if m.cancelingQueuedExecution != "" {
		m.addSystemEntry("A queued-task cancellation is already in progress.")
		return m, nil
	}
	target := m.queuedTurns[len(m.queuedTurns)-1]
	if m.runtime == nil || target.ExecutionID == "" {
		m.addSystemEntry("This queued task is not ready to cancel yet.")
		return m, nil
	}
	m.cancelingQueuedExecution = target.ExecutionID
	m.addSystemEntry("Cancelling the most recently queued task…")
	m.updateLayout()
	runtime := m.runtime
	executionID := target.ExecutionID
	return m, func() tea.Msg {
		err := runtime.CancelQueuedTurn(context.Background(), executionID)
		return queuedTurnCancelledMsg{executionID: executionID, err: err}
	}
}

func (m *tuiModel) discardPausedTask(source discardSource) (tea.Model, tea.Cmd) {
	if m.discardInFlight {
		return m, nil
	}
	if m.runtime == nil {
		m.addSystemEntry("Paused-task management is unavailable.")
		m.updateLayout()
		return m, nil
	}
	m.discardInFlight = true
	m.addSystemEntry("Discarding paused task…")
	m.updateLayout()
	runtime := m.runtime
	return m, func() tea.Msg {
		discarded, err := runtime.DiscardPaused(context.Background())
		return taskDiscardedMsg{source: source, discarded: discarded, err: err}
	}
}

func (m *tuiModel) settleQueuedTurnCancellation(msg queuedTurnCancelledMsg) {
	if m.cancelingQueuedExecution == msg.executionID {
		m.cancelingQueuedExecution = ""
	}

	removeQueuedTurn := func() {
		for i, item := range m.queuedTurns {
			if item.ExecutionID == msg.executionID {
				m.queuedTurns = append(m.queuedTurns[:i], m.queuedTurns[i+1:]...)
				break
			}
		}
	}
	updateEntry := func(kind EntryKind, suffix string) {
		for i := len(m.entries) - 1; i >= 0; i-- {
			if m.entries[i].Kind == KindQueued && m.entries[i].ToolCallID == msg.executionID {
				m.entries[i].Kind = kind
				m.entries[i].ToolCallID = msg.executionID
				if suffix != "" {
					m.entries[i].Text += suffix
				}
				m.invalidateEntryCache()
				break
			}
		}
	}

	switch {
	case msg.err == nil:
		removeQueuedTurn()
		updateEntry(KindQueued, " (cancelled)")
		m.addSystemEntry("Cancelled the most recently queued task.")
	case errors.Is(msg.err, core.ErrTurnNotFound):
		removeQueuedTurn()
		updateEntry(KindUser, "")
		m.addSystemEntry("The queued task was already removed from Core.")
	case errors.Is(msg.err, core.ErrQueuedTurnStarted):
		m.addSystemEntry("The task has already started; `/cancel-queue` only cancels waiting tasks.")
	default:
		m.addSystemEntry("Failed to cancel queued task: " + msg.err.Error())
	}
	m.refreshViewportContent()
	m.updateLayout()
}

func (m *tuiModel) addSystemEntry(text string) {
	m.entries = append(m.entries, TranscriptEntry{
		ID:        fmt.Sprintf("sys-%d", time.Now().UnixNano()),
		Kind:      KindSystem,
		Text:      text,
		Timestamp: time.Now(),
	})
	m.refreshViewportContent()
}

const (
	ExportFormatMarkdown ExportFormat = iota
	ExportFormatHTML
	ExportFormatJSONL
)
