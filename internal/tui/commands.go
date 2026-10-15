package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	core "github.com/afterlune/luneCore"
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
	{name: "/details", usage: "/details [n]", description: "Show a user message in the session transcript."},
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

func (m *tuiModel) resetInput() {
	m.input.Reset()
	m.attachments = nil
	m.nextAttachmentID = 1
	m.undoStack = nil
	m.redoStack = nil
	m.updateCommandSuggestions()
	m.updateLayout()
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
