package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

var builtinCommands = []AutocompleteItem{
	{Command: "/new", Description: "start a new chat during a conversation"},
	{Command: "/plan", Description: "switch to Plan mode (read-only exploration)"},
	{Command: "/permissions", Description: "show or set mode: read-only, workspace, ask, full-access"},
	{Command: "/model", Description: "choose an active model profile"},
	{Command: "/compact", Description: "compact older conversation history"},
	{Command: "/fork", Description: "fork the current chat"},
	{Command: "/rewind", Description: "rewind the conversation to a previous turn"},
	{Command: "/status", Description: "show session, model and token status"},
	{Command: "/resume", Description: "resume the paused task in this session"},
	{Command: "/discard", Description: "discard the paused task in this session"},
	{Command: "/help", Description: "list all available slash commands"},
	{Command: "/exit", Description: "exit the chat session"},
}

func filterCommands(prefix string) []AutocompleteItem {
	prefix = strings.ToLower(prefix)
	var matches []AutocompleteItem
	for _, item := range builtinCommands {
		if strings.HasPrefix(strings.ToLower(item.Command), prefix) {
			matches = append(matches, item)
		}
	}
	return matches
}

// handleSlashCommand executes slash commands entered by the user.
func (m *tuiModel) handleSlashCommand(raw string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return m, nil
	}
	cmdName := strings.ToLower(parts[0])

	switch cmdName {
	case "/exit", "/quit":
		return m, tea.Quit

	case "/help":
		var lines []string
		lines = append(lines, "**Available slash commands:**")
		for _, item := range builtinCommands {
			lines = append(lines, fmt.Sprintf("- `%s`: %s", item.Command, item.Description))
		}
		m.addSystemEntry(strings.Join(lines, "\n"))
		return m, nil

	case "/new":
		return m, m.startNewSession()

	case "/plan":
		m.planMode = !m.planMode
		if m.planMode {
			m.savedMode = m.permissionMode
			_ = m.client.SetPermissionMode(core.PermissionReadOnly)
			m.permissionMode = core.PermissionReadOnly
			m.addSystemEntry("Switched to **Plan mode** (permissions locked to Read-only).")
		} else {
			_ = m.client.SetPermissionMode(m.savedMode)
			m.permissionMode = m.savedMode
			m.addSystemEntry("Exited **Plan mode** (permissions restored).")
		}
		m.updateLayout()
		return m, nil

	case "/permissions":
		if len(parts) > 1 {
			mode := core.PermissionMode(strings.ToLower(parts[1]))
			switch mode {
			case core.PermissionReadOnly, core.PermissionWorkspace, core.PermissionAsk, core.PermissionFullAccess:
				if m.planMode {
					m.addSystemEntry("Cannot change permissions while **Plan mode** is active.")
				} else {
					if err := m.client.SetPermissionMode(mode); err != nil {
						m.addSystemEntry("Failed to set permission mode: " + err.Error())
					} else {
						m.permissionMode = mode
						m.addSystemEntry(fmt.Sprintf("Permission mode set to `%s`.", mode))
					}
				}
			default:
				m.addSystemEntry("Unknown permission mode. Choose: `read-only`, `workspace`, `ask`, `full-access`.")
			}
		} else {
			m.addSystemEntry(fmt.Sprintf("Current permission mode: `%s` (Plan mode: %v)", m.permissionMode, m.planMode))
		}
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

	case "/resume":
		if m.runtime != nil {
			m.running = true
			m.taskStartedAt = time.Now()
			m.updateLayout()
			sessionID := m.sessionID
			return m, func() tea.Msg {
				sub, err := m.runtime.Recover(context.Background())
				return turnSubmittedMsg{sessionID: sessionID, submission: sub, err: err}
			}
		}

	case "/discard":
		if m.runtime != nil {
			return m, func() tea.Msg {
				ok, err := m.runtime.DiscardPaused(context.Background())
				return taskDiscardedMsg{discarded: ok, err: err}
			}
		}
	}

	m.addSystemEntry(fmt.Sprintf("Unknown command: `%s`. Type `/help` for options.", raw))
	return m, nil
}

// renderStatusTable renders a status table matching Screenshot 12.
func (m *tuiModel) renderStatusTable() string {
	modelName := m.currentModel
	if modelName == "" {
		modelName = "GPT-6-Luna (reasoning max, summaries auto)"
	}
	permStr := string(m.permissionMode)
	if m.planMode {
		permStr += " (Plan mode locked)"
	}
	title := m.sessionTitle
	if title == "" {
		title = "<none>"
	}

	lines := []string{
		fmt.Sprintf("Model:          %s", modelName),
		fmt.Sprintf("Directory:      %s", m.workspace),
		fmt.Sprintf("Permissions:    %s", permStr),
		fmt.Sprintf("Thread name:    %s", title),
		fmt.Sprintf("Session:        %s", m.sessionID),
	}
	return strings.Join(lines, "\n")
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

func (m *tuiModel) startNewSession() tea.Cmd {
	return func() tea.Msg {
		session, err := m.client.CreateSession(context.Background(), json.RawMessage("null"))
		return sessionCreatedMsg{session: session, err: err}
	}
}

type compactDoneMsg struct {
	trimmed int
	err     error
}

type sessionForkedMsg struct {
	session core.Session
	err     error
}

type sessionCreatedMsg struct {
	session core.Session
	err     error
}

type taskDiscardedMsg struct {
	discarded bool
	err       error
}
