package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
)

var builtinCommands = []AutocompleteItem{
	{Command: "/new", Description: "start a new chat during a conversation"},
	{Command: "/clear", Description: "clear transcript history from the current view"},
	{Command: "/plan", Description: "switch to Plan mode (read-only exploration)"},
	{Command: "/permissions", Description: "show or set mode: read-only, workspace, ask, full-access"},
	{Command: "/model", Description: "choose an active model profile"},
	{Command: "/theme", Description: "change or inspect theme: dark, light, auto"},
	{Command: "/sessions", Description: "list recent sessions in this workspace"},
	{Command: "/open", Description: "switch to an existing session by ID or prefix"},
	{Command: "/title", Description: "view or set custom title for this session"},
	{Command: "/diff", Description: "inspect git code changes in workspace"},
	{Command: "/review", Description: "summarize and review workspace changes"},
	{Command: "/mcp", Description: "manage external MCP servers: list, enable, disable, auth"},
	{Command: "/cancel-queue", Description: "cancel waiting queued tasks"},
	{Command: "/compact", Description: "compact older conversation history"},
	{Command: "/fork", Description: "fork the current chat"},
	{Command: "/rewind", Description: "rewind the conversation to a previous turn"},
	{Command: "/status", Description: "show session, model and token status"},
	{Command: "/resume", Description: "resume the paused task in this session"},
	{Command: "/discard", Description: "discard the paused task in this session"},
	{Command: "/shortcuts", Description: "display keyboard shortcuts help"},
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
		lines = append(lines, "\nPress `?` when composer is empty to view the keyboard shortcut cheat sheet.")
		m.addSystemEntry(strings.Join(lines, "\n"))
		return m, nil

	case "/new":
		if m.running {
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
		m.updateLayout()
		return m, nil

	case "/theme":
		if len(parts) > 1 {
			target := strings.ToLower(parts[1])
			switch target {
			case "dark", "light", "auto":
				SetThemeMode(ThemeMode(target))
				m.applyThemeStyles()
				m.refreshViewportContent()
				m.updateLayout()
				m.addSystemEntry(fmt.Sprintf("Theme mode set to `%s` (active: %s).", target, CurrentThemeMode()))
			default:
				m.addSystemEntry(fmt.Sprintf("Unknown theme mode %q. Options: `dark`, `light`, `auto`.", parts[1]))
			}
		} else {
			m.addSystemEntry(fmt.Sprintf("Current theme mode: `%s`.", CurrentThemeMode()))
		}
		return m, nil

	case "/sessions":
		if m.client == nil {
			m.addSystemEntry("Session management unavailable.")
			return m, nil
		}
		sessions, err := m.client.ListSessions(context.Background(), 20, false)
		if err != nil {
			m.addSystemEntry("Failed to list sessions: " + err.Error())
			return m, nil
		}
		if len(sessions) == 0 {
			m.addSystemEntry("No saved sessions in this workspace.")
			return m, nil
		}
		var lines []string
		lines = append(lines, "**Recent Sessions:**")
		for _, s := range sessions {
			marker := "  "
			if s.ID == m.sessionID {
				marker = "› "
			}
			title := s.Title
			if title == "" {
				title = "<untitled>"
			}
			shortID := s.ID
			if len(shortID) > 12 {
				shortID = shortID[:12]
			}
			timeStr := s.UpdatedAt.Format("Jan 02 15:04")
			lines = append(lines, fmt.Sprintf("%s`%s`  %-24s  %s", marker, shortID, title, timeStr))
		}
		lines = append(lines, "\nSwitch with `/open <id>` (supports unique prefix).")
		m.addSystemEntry(strings.Join(lines, "\n"))
		return m, nil

	case "/open":
		if m.running {
			m.addSystemEntry("Cannot switch sessions while a task is running. Cancel or wait for it to finish.")
			return m, nil
		}
		if len(parts) < 2 {
			m.addSystemEntry("Usage: `/open <session-id>` (or unique prefix).")
			return m, nil
		}
		targetPrefix := parts[1]
		ctx := context.Background()
		targetID := ""
		if _, err := m.client.GetSession(ctx, targetPrefix); err == nil {
			targetID = targetPrefix
		} else {
			sessions, err := m.client.ListSessions(ctx, 100, false)
			if err != nil {
				m.addSystemEntry("Failed to query sessions: " + err.Error())
				return m, nil
			}
			var matches []core.Session
			for _, s := range sessions {
				if strings.HasPrefix(strings.ToLower(s.ID), strings.ToLower(targetPrefix)) {
					matches = append(matches, s)
				}
			}
			if len(matches) == 0 {
				m.addSystemEntry(fmt.Sprintf("No session found matching prefix `%s`.", targetPrefix))
				return m, nil
			} else if len(matches) > 1 {
				var ids []string
				for _, match := range matches {
					short := match.ID
					if len(short) > 12 {
						short = short[:12]
					}
					ids = append(ids, short)
				}
				m.addSystemEntry(fmt.Sprintf("Prefix `%s` is ambiguous. Matches: %s", targetPrefix, strings.Join(ids, ", ")))
				return m, nil
			}
			targetID = matches[0].ID
		}

		if targetID == m.sessionID {
			m.addSystemEntry("Already in session: " + targetID)
			return m, nil
		}

		if err := m.bindSessionRuntime(ctx, targetID); err != nil {
			m.addSystemEntry("Failed to open session: " + err.Error())
			return m, nil
		}
		m.activeQuestion = nil
		m.activeApproval = nil
		m.activePlanReview = nil
		m.activeRecovery = nil
		m.running = false
		m.currentStreamText = ""
		m.updateLayout()
		m.addSystemEntry("Opened session: " + targetID)
		return m, m.subscribeRuntimeEvents()

	case "/rewind":
		if m.running {
			m.addSystemEntry("Cannot rewind while a task is running.")
			return m, nil
		}
		turns := 1
		if len(parts) > 1 {
			if n, err := strconv.Atoi(parts[1]); err == nil && n > 0 {
				turns = n
			} else {
				m.addSystemEntry(fmt.Sprintf("Invalid turn count %q. Usage: `/rewind [N]`", parts[1]))
				return m, nil
			}
		}
		if m.client == nil {
			m.addSystemEntry("Core client unavailable.")
			return m, nil
		}
		ctx := context.Background()
		removed, err := m.client.RewindSession(ctx, m.sessionID, turns)
		if err != nil {
			m.addSystemEntry("Rewind failed: " + err.Error())
			return m, nil
		}
		saved, err := m.client.GetSession(ctx, m.sessionID)
		if err != nil {
			m.addSystemEntry(fmt.Sprintf("Rewound %d turn(s), but failed to reload session: %v", removed, err))
			return m, nil
		}
		m.sessionTitle = saved.Title
		m.entries = transcriptFromHistory(saved.History)
		if m.entries == nil {
			m.entries = []TranscriptEntry{}
		}
		m.currentStreamText = ""
		m.scrolledUp = false
		m.refreshViewportContent()
		m.updateLayout()
		m.addSystemEntry(fmt.Sprintf("Rewound %d turn(s).", removed))
		return m, nil

	case "/title":
		if len(parts) > 1 {
			newTitle := strings.TrimSpace(strings.Join(parts[1:], " "))
			if m.client != nil {
				if err := m.client.SetSessionTitle(context.Background(), m.sessionID, newTitle); err != nil {
					m.addSystemEntry("Failed to set session title: " + err.Error())
					return m, nil
				}
			}
			m.sessionTitle = newTitle
			m.updateLayout()
			m.addSystemEntry(fmt.Sprintf("Session title updated to `%s`.", newTitle))
		} else {
			title := m.sessionTitle
			if title == "" {
				title = "<untitled>"
			}
			m.addSystemEntry(fmt.Sprintf("Current session title: `%s` (ID: `%s`)", title, m.sessionID))
		}
		return m, nil

	case "/diff":
		var diffArgs []string
		diffArgs = append(diffArgs, "diff")
		if len(parts) > 1 {
			diffArgs = append(diffArgs, "--")
			diffArgs = append(diffArgs, parts[1:]...)
		}
		cmd := exec.CommandContext(context.Background(), "git", diffArgs...)
		cmd.Dir = m.workspace
		out, err := cmd.CombinedOutput()
		if err != nil {
			m.addSystemEntry("Failed to run git diff: " + err.Error())
			return m, nil
		}
		diffText := strings.TrimSpace(string(out))
		if diffText == "" {
			m.addSystemEntry("No unstaged or uncommitted changes in workspace.")
			return m, nil
		}
		m.entries = append(m.entries, TranscriptEntry{
			ID:        fmt.Sprintf("diff-%d", time.Now().UnixNano()),
			Kind:      KindDiff,
			Text:      diffText,
			Timestamp: time.Now(),
		})
		m.refreshViewportContent()
		m.updateLayout()
		return m, nil

	case "/review":
		if len(parts) > 1 {
			diffArgs := append([]string{"diff", "--"}, parts[1:]...)
			cmd := exec.CommandContext(context.Background(), "git", diffArgs...)
			cmd.Dir = m.workspace
			out, err := cmd.CombinedOutput()
			if err != nil {
				m.addSystemEntry("Failed to run git diff: " + err.Error())
				return m, nil
			}
			diffText := strings.TrimSpace(string(out))
			if diffText == "" {
				m.addSystemEntry("No changes found for: " + strings.Join(parts[1:], " "))
				return m, nil
			}
			m.entries = append(m.entries, TranscriptEntry{
				ID:        fmt.Sprintf("diff-%d", time.Now().UnixNano()),
				Kind:      KindDiff,
				Text:      diffText,
				Timestamp: time.Now(),
			})
			m.refreshViewportContent()
			m.updateLayout()
			return m, nil
		}

		statusCmd := exec.CommandContext(context.Background(), "git", "status", "--short")
		statusCmd.Dir = m.workspace
		statusOut, err := statusCmd.CombinedOutput()
		if err != nil {
			m.addSystemEntry("Failed to check git status: " + err.Error())
			return m, nil
		}
		statusText := strings.TrimSpace(string(statusOut))
		if statusText == "" {
			m.addSystemEntry("No unstaged or uncommitted changes in workspace.")
			return m, nil
		}

		diffStatCmd := exec.CommandContext(context.Background(), "git", "diff", "--stat")
		diffStatCmd.Dir = m.workspace
		diffStatOut, _ := diffStatCmd.CombinedOutput()
		statText := strings.TrimSpace(string(diffStatOut))

		var lines []string
		lines = append(lines, "**Workspace Changes Review:**\n```text")
		lines = append(lines, statusText)
		lines = append(lines, "```")
		if statText != "" {
			lines = append(lines, "\n**Diff Statistics:**\n```text")
			lines = append(lines, statText)
			lines = append(lines, "```")
		}
		lines = append(lines, "\n*Tip: Use `/review <path>` or `/diff <path>` to view full diff for a specific file.*")
		m.addSystemEntry(strings.Join(lines, "\n"))
		return m, nil

	case "/mcp":
		if m.client == nil {
			m.addSystemEntry("MCP management is unavailable.")
			return m, nil
		}
		subcommand := "list"
		if len(parts) > 1 {
			subcommand = strings.ToLower(parts[1])
		}
		switch subcommand {
		case "list":
			servers := m.client.MCPServers()
			if len(servers) == 0 {
				m.addSystemEntry("No MCP servers configured in `mcpServers` (config.json).")
				return m, nil
			}
			var lines []string
			lines = append(lines, "**Configured MCP Servers:**")
			for _, s := range servers {
				status := "Disabled"
				if s.Enabled {
					status = "Enabled"
				}
				conn := "Disconnected"
				if s.Connected {
					conn = "Connected"
				}
				endpoint := s.Endpoint
				if endpoint == "" {
					endpoint = s.Command
				}
				errStr := ""
				if s.LastError != "" {
					errStr = fmt.Sprintf(" · *Error: %s*", s.LastError)
				}
				lines = append(lines, fmt.Sprintf("- **`%s`** (`%s`): %s, %s (%d tools)%s\n  Target: `%s`",
					s.Name, s.Transport, status, conn, s.ToolCount, errStr, endpoint))
			}
			m.addSystemEntry(strings.Join(lines, "\n"))
		case "enable":
			if m.running {
				m.addSystemEntry("Cannot change MCP servers while a task is running.")
				return m, nil
			}
			if len(parts) < 3 {
				m.addSystemEntry("Usage: `/mcp enable <server-name>`")
				return m, nil
			}
			name := parts[2]
			if err := m.client.EnableMCPServer(name); err != nil {
				m.addSystemEntry(fmt.Sprintf("Failed to enable MCP server `%s`: %v", name, err))
			} else {
				m.addSystemEntry(fmt.Sprintf("MCP server `%s` enabled and tools registered.", name))
			}
		case "disable":
			if m.running {
				m.addSystemEntry("Cannot change MCP servers while a task is running.")
				return m, nil
			}
			if len(parts) < 3 {
				m.addSystemEntry("Usage: `/mcp disable <server-name>`")
				return m, nil
			}
			name := parts[2]
			if err := m.client.DisableMCPServer(name); err != nil {
				m.addSystemEntry(fmt.Sprintf("Failed to disable MCP server `%s`: %v", name, err))
			} else {
				m.addSystemEntry(fmt.Sprintf("MCP server `%s` disabled and disconnected.", name))
			}
		case "auth":
			if m.running {
				m.addSystemEntry("Cannot authorize an MCP server while a task is running.")
				return m, nil
			}
			if len(parts) < 3 {
				m.addSystemEntry("Usage: `/mcp auth <server-name>`")
				return m, nil
			}
			name := parts[2]
			if err := m.client.AuthenticateMCPServer(context.Background(), name); err != nil {
				m.addSystemEntry(fmt.Sprintf("Failed to authenticate MCP server `%s`: %v", name, err))
			} else {
				m.addSystemEntry(fmt.Sprintf("MCP server `%s` authorized successfully.", name))
			}
		default:
			m.addSystemEntry(fmt.Sprintf("Unknown MCP subcommand `%s`. Usage: `/mcp [list|enable|disable|auth]`", subcommand))
		}
		return m, nil

	case "/cancel-queue":
		return m.cancelQueuedTurn()

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

	case "/model":
		if m.client == nil {
			m.addSystemEntry("Model switching is unavailable.")
			return m, nil
		}
		if len(parts) > 1 {
			targetProfile := parts[1]
			profiles := m.client.ListModelProfiles()
			valid := false
			for _, p := range profiles {
				if strings.EqualFold(p.Profile, targetProfile) || strings.EqualFold(p.Model, targetProfile) {
					valid = true
					targetProfile = p.Profile
					break
				}
			}
			if valid {
				err := m.client.SetSessionSettings(context.Background(), m.sessionID, core.SessionSettings{
					ModelProfile: targetProfile,
				})
				if err != nil {
					m.addSystemEntry("Failed to set model: " + err.Error())
				} else {
					m.currentModel = m.client.ModelForSession(context.Background(), m.sessionID)
					m.addSystemEntry(fmt.Sprintf("Active model switched to `%s`.", m.currentModel))
					m.updateLayout()
				}
			} else {
				var names []string
				for _, p := range profiles {
					names = append(names, p.Profile)
				}
				m.addSystemEntry(fmt.Sprintf("Unknown model profile `%s`. Available profiles: %s", parts[1], strings.Join(names, ", ")))
			}
		} else {
			profiles := m.client.ListModelProfiles()
			var lines []string
			lines = append(lines, fmt.Sprintf("**Current model:** `%s`", m.currentModel))
			lines = append(lines, "\n**Available model profiles:**")
			for _, p := range profiles {
				marker := "  "
				if strings.EqualFold(p.Profile, m.currentModel) || strings.EqualFold(p.Model, m.currentModel) {
					marker = "* "
				}
				lines = append(lines, fmt.Sprintf("%s`%s` (%s / %s)", marker, p.Profile, p.Provider, p.Model))
			}
			lines = append(lines, "\nUsage: `/model <profile>` to switch.")
			m.addSystemEntry(strings.Join(lines, "\n"))
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

	case "/shortcuts":
		m.shortcutsOpen = true
		m.updateLayout()
		return m, nil
	}

	m.addSystemEntry(fmt.Sprintf("Unknown command: `%s`. Type `/help` for options.", raw))
	return m, nil
}

// renderStatusTable renders a status table matching Screenshot 12.
func (m *tuiModel) renderStatusTable() string {
	modelName := m.currentModel
	if modelName == "" && m.client != nil {
		modelName = m.client.ModelForSession(context.Background(), m.sessionID)
	}
	if modelName == "" {
		modelName = "<default>"
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
		fmt.Sprintf("Theme:          %s", CurrentThemeMode()),
	}

	if m.contextWindow > 0 {
		pct := 0.0
		if m.estimatedInputTokens > 0 {
			pct = float64(m.estimatedInputTokens) / float64(m.contextWindow) * 100.0
		}
		lines = append(lines, fmt.Sprintf("Context Window: %s tokens (est. input: %s, %.1f%% full)",
			formatTokens(m.contextWindow), formatTokens(m.estimatedInputTokens), pct))
	}
	if m.totalInputTokens > 0 || m.totalOutputTokens > 0 {
		lines = append(lines, fmt.Sprintf("Total Tokens:   in: %s (cached: %s) · out: %s (reasoning: %s)",
			formatTokens(m.totalInputTokens), formatTokens(m.cachedTokens),
			formatTokens(m.totalOutputTokens), formatTokens(m.reasoningTokens)))
	}

	return strings.Join(lines, "\n")
}

func formatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	in := fmt.Sprintf("%d", n)
	var out []byte
	rem := len(in) % 3
	if rem > 0 {
		out = append(out, in[:rem]...)
		if len(in) > rem {
			out = append(out, ',')
		}
	}
	for i := rem; i < len(in); i += 3 {
		out = append(out, in[i:i+3]...)
		if i+3 < len(in) {
			out = append(out, ',')
		}
	}
	return string(out)
}

func (m *tuiModel) cancelQueuedTurn() (tea.Model, tea.Cmd) {
	if len(m.queuedTurns) == 0 {
		m.addSystemEntry("No queued tasks to cancel.")
		return m, nil
	}
	lastIdx := len(m.queuedTurns) - 1
	target := m.queuedTurns[lastIdx]
	m.queuedTurns = m.queuedTurns[:lastIdx]
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindQueued && (m.entries[i].ToolCallID == target.ExecutionID || m.entries[i].ToolCallID == "") {
			m.entries[i].Text += " (cancelled)"
			break
		}
	}
	m.refreshViewportContent()
	m.updateLayout()
	if m.runtime != nil && target.ExecutionID != "" {
		ctx := context.Background()
		_ = m.runtime.CancelQueuedTurn(ctx, target.ExecutionID)
	}
	m.addSystemEntry("Cancelled queued task.")
	return m, nil
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
