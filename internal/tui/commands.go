package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/afterlune/lune/internal/slashcmd"
	core "github.com/afterlune/luneCore"
)

// builtinCommands is the TUI view of the shared slash command registry.
// Order here is the completion order users see while typing.
var builtinCommands = slashcmd.For(slashcmd.TargetTUI)

func filterCommands(prefix string) []AutocompleteItem {
	prefix = strings.ToLower(prefix)
	var matches []AutocompleteItem
	for _, item := range builtinCommands {
		if strings.HasPrefix(strings.ToLower(item.Name), prefix) {
			matches = append(matches, AutocompleteItem{Command: item.Name, Description: item.Summary})
		}
	}
	return matches
}

func (m *tuiModel) commandsForAutocomplete(prefix string) []AutocompleteItem {
	items := filterCommands(prefix)
	if !m.taskBusy() {
		return items
	}
	available := make([]AutocompleteItem, 0, len(items))
	for _, item := range items {
		if spec := lookupBuiltinCommand(item.Command); spec != nil && spec.AvailableDuringTask {
			available = append(available, item)
		}
	}
	return available
}

func (m *tuiModel) taskBusy() bool {
	return m.run.busy()
}

// lookupBuiltinCommand returns the registered command for a slash command name.
func lookupBuiltinCommand(name string) *slashcmd.Command {
	name = strings.ToLower(strings.TrimSpace(name))
	for i := range builtinCommands {
		if builtinCommands[i].Name == name {
			return &builtinCommands[i]
		}
	}
	return nil
}

// isKnownSlashCommand reports whether the first token of text is a registered command.
// Anything else that merely starts with "/" is a normal prompt, so pasted absolute
// paths are sent to the model instead of failing as an unknown command.
func isKnownSlashCommand(text string) bool {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return false
	}
	if strings.ToLower(fields[0]) == "/memory" {
		return true
	}
	return lookupBuiltinCommand(fields[0]) != nil
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
	spec := lookupBuiltinCommand(name)
	if spec != nil && spec.AvailableDuringTask {
		return subcommandUnavailableDuringTask(name, parts)
	}
	if spec != nil && spec.BlockedDuringTask != "" {
		return spec.BlockedDuringTask
	}
	return fmt.Sprintf("`%s` is unavailable while a task is running. Interrupt the task or wait for it to finish.", name)
}

// subcommandUnavailableDuringTask reports the remaining task-time restrictions of
// commands that stay available, so a read-only subcommand can run mid-task.
func subcommandUnavailableDuringTask(name string, parts []string) string {
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
		}
	}
	return ""
}

// commandNeedsSession reports whether a slash command operates on a persisted session.
// Draft sessions keep these commands unavailable until the first message is sent;
// commands marked DraftOK either defer their change to the first message or have
// nothing to act on yet.
func commandNeedsSession(name string) bool {
	spec := lookupBuiltinCommand(name)
	return spec != nil && spec.NeedsSession && !spec.DraftOK
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

	if m.sessionID == "" && commandNeedsSession(cmdName) {
		m.addSystemEntry("No session yet — send a message first.")
		return m, nil
	}

	switch cmdName {
	case "/goal":
		return m.handleGoalCommand(raw)
	case "/exit":
		m.cancelMCPAuthentication(false)
		return m, tea.Quit

	case "/help":
		var lines []string
		lines = append(lines, "**Available slash commands:**")
		for _, item := range builtinCommands {
			lines = append(lines, fmt.Sprintf("- `%s`: %s", item.Name, item.Summary))
		}
		lines = append(lines, "\nPress `?` when composer is empty to view the keyboard shortcut cheat sheet.")
		m.addSystemEntry(strings.Join(lines, "\n"))
		return m, nil

	case "/new":
		if m.taskBusy() {
			m.addSystemEntry("Cannot start a new session while a task is running.")
			return m, nil
		}
		return m.startDraftSession()

	case "/clear":
		m.entries = nil
		m.run.resetStream()
		m.run.resetReasoning()
		m.view.invalidateLive()
		m.view.windowedTail = false
		m.viewport.SetContent("")
		m.scrolledUp = false
		m.clearTranscriptSelection()
		m.view.invalidateBlocks()
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
	case "/toolchains":
		return m.handleToolchainsCommand(parts)
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
		if m.usage.compacting || m.run.compactInFlight {
			m.addSystemEntry("Compaction is already in progress.")
			return m, nil
		}
		client, sessionID, bindingGeneration := m.client, m.sessionID, m.bindingGeneration
		m.run.compactInFlight = true
		m.run.compactionQuitRequested = false
		m.usage.beginCompaction()
		m.addSystemEntry("Compacting older conversation history…")
		m.updateLayout()
		return m, func() tea.Msg {
			result, err := client.Compact(context.Background(), sessionID)
			return compactDoneMsg{result: result, err: err, sessionID: sessionID, bindingGeneration: bindingGeneration}
		}

	case "/fork":
		client, sessionID, bindingGeneration := m.client, m.sessionID, m.bindingGeneration
		ctx := m.boundContext
		if ctx == nil {
			ctx = context.Background()
		}
		return m, func() tea.Msg {
			session, err := client.ForkSession(ctx, sessionID)
			return sessionForkedMsg{session: session, sessionID: sessionID, bindingGeneration: bindingGeneration, err: err}
		}

	case "/shortcuts":
		m.modals.open()
		m.modals.shortcuts = true
		m.updateLayout()
		return m, nil
	}

	m.addSystemEntry(fmt.Sprintf("Unknown command: `%s`. Type `/help` for options.", raw))
	return m, nil
}

func (m *tuiModel) cancelQueuedTurn() (tea.Model, tea.Cmd) {
	if len(m.run.queuedTurns) == 0 {
		m.addSystemEntry("No queued tasks to cancel.")
		return m, nil
	}
	if m.run.cancelingQueuedExecution != "" {
		m.addSystemEntry("A queued-task cancellation is already in progress.")
		return m, nil
	}
	target := m.run.queuedTurns[len(m.run.queuedTurns)-1]
	if m.runtime == nil || target.ExecutionID == "" {
		m.addSystemEntry("This queued task is not ready to cancel yet.")
		return m, nil
	}
	m.run.cancelingQueuedExecution = target.ExecutionID
	m.addSystemEntry("Cancelling the most recently queued task…")
	m.updateLayout()
	runtime := m.runtime
	executionID := target.ExecutionID
	bindingGeneration := m.bindingGeneration
	ctx := m.boundContext
	if ctx == nil {
		ctx = context.Background()
	}
	return m, func() tea.Msg {
		err := runtime.CancelQueuedTurn(ctx, executionID)
		return queuedTurnCancelledMsg{executionID: executionID, bindingGeneration: bindingGeneration, err: err}
	}
}

const questionPendingNotice = "This task is waiting for your answer. Answer it, or press Esc to stop the task."

func (m *tuiModel) noteQuestionPending() (tea.Model, tea.Cmd) {
	if len(m.entries) == 0 || m.entries[len(m.entries)-1].Kind != KindSystem || m.entries[len(m.entries)-1].Text != questionPendingNotice {
		m.addSystemEntry(questionPendingNotice)
		m.updateLayout()
	}
	return m, nil
}

func (m *tuiModel) discardPausedTask(source discardSource) (tea.Model, tea.Cmd) {
	if m.run.discardInFlight {
		return m, nil
	}
	if m.runtime == nil {
		m.addSystemEntry("Paused-task management is unavailable.")
		m.updateLayout()
		return m, nil
	}
	m.run.discardInFlight = true
	m.addSystemEntry("Discarding paused task…")
	m.updateLayout()
	runtime := m.runtime
	bindingGeneration := m.bindingGeneration
	return m, func() tea.Msg {
		discarded, err := runtime.DiscardPaused(context.Background())
		return taskDiscardedMsg{source: source, discarded: discarded, err: err, bindingGeneration: bindingGeneration}
	}
}

func (m *tuiModel) settleQueuedTurnCancellation(msg queuedTurnCancelledMsg) {
	if m.run.cancelingQueuedExecution == msg.executionID {
		m.run.cancelingQueuedExecution = ""
	}

	removeQueuedTurn := func() {
		for i, item := range m.run.queuedTurns {
			if item.ExecutionID == msg.executionID {
				m.run.queuedTurns = append(m.run.queuedTurns[:i], m.run.queuedTurns[i+1:]...)
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
				m.view.invalidateBlocks()
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
