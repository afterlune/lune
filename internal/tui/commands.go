package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	core "github.com/afterlune/luneCore"
	"github.com/afterlune/luneCore/memory"
)

var builtinCommands = []AutocompleteItem{
	{Command: "/new", Description: "start a new chat during a conversation"},
	{Command: "/clear", Description: "clear transcript history from the current view"},
	{Command: "/plan", Description: "switch to Plan mode (read-only exploration)"},
	{Command: "/permissions", Description: "show or set mode: read-only, workspace, ask, full-access"},
	{Command: "/model", Description: "choose an active model profile"},
	{Command: "/effort", Description: "choose the reasoning effort level interactively"},
	{Command: "/reasoning", Description: "alias for /effort"},
	{Command: "/theme", Description: "change or inspect theme: dark, light, auto"},
	{Command: "/memory", Description: "view and manage workspace long-term memories"},
	{Command: "/agents", Description: "inspect live agents and their context, cache, speed, and usage"},
	{Command: "/ps", Description: "list background processes and daemon commands in session"},
	{Command: "/stop", Description: "stop running background process: /stop <id|all>"},
	{Command: "/logs", Description: "view output logs of a background process: /logs <id>"},
	{Command: "/sessions", Description: "open session switcher modal or search: /sessions [query]"},
	{Command: "/open", Description: "open session switcher modal or switch by ID (Ctrl+O)"},
	{Command: "/drop", Description: "delete a saved session: /drop <id|prefix>"},
	{Command: "/delete", Description: "alias for /drop"},
	{Command: "/detail", Description: "open tool output pager modal: /detail [index] (Ctrl+T)"},
	{Command: "/title", Description: "view or set custom title for this session"},
	{Command: "/export", Description: "export session transcript: /export [md|html|jsonl] [file]"},
	{Command: "/context", Description: "show context window budget, system rules, and capabilities"},
	{Command: "/diff", Description: "inspect git code changes in workspace modal (Ctrl+D)"},
	{Command: "/review", Description: "summarize and review workspace changes"},
	{Command: "/mcp", Description: "manage external MCP servers: list, enable, disable, auth"},
	{Command: "/cancel-queue", Description: "cancel waiting queued tasks"},
	{Command: "/compact", Description: "compact older conversation history"},
	{Command: "/fork", Description: "fork the current chat"},
	{Command: "/rewind", Description: "rewind session history: /rewind [N] or /rewind preview [N]"},
	{Command: "/status", Description: "show session, model and token status"},
	{Command: "/resume", Description: "resume paused task or switch session: /resume [id]"},
	{Command: "/discard", Description: "discard the paused task in this session"},
	{Command: "/shortcuts", Description: "display keyboard shortcuts help"},
	{Command: "/thought", Description: "toggle visibility of latest thinking/reasoning block (Ctrl+R)"},
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
		m.invalidateEntryCache()
		m.updateLayout()
		return m, nil

	case "/thought":
		return m.toggleReasoningExpansion()

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
		if m.running {
			m.addSystemEntry("Cannot switch sessions while a task is running. Cancel or wait for it to finish.")
			return m, nil
		}
		if m.client == nil {
			m.addSystemEntry("Session management unavailable.")
			return m, nil
		}
		if len(parts) < 2 {
			return m.openSessionsModal()
		}
		query := strings.TrimSpace(strings.Join(parts[1:], " "))
		ctx := context.Background()
		targetID := ""
		if _, err := m.client.GetSession(ctx, query); err == nil {
			targetID = query
		} else {
			sessions, err := m.client.ListSessions(ctx, 100, false)
			if err == nil {
				var prefixMatches []core.Session
				for _, s := range sessions {
					if strings.HasPrefix(strings.ToLower(s.ID), strings.ToLower(query)) {
						prefixMatches = append(prefixMatches, s)
					}
				}
				if len(prefixMatches) == 1 {
					targetID = prefixMatches[0].ID
				}
			}
		}
		if targetID != "" {
			if targetID == m.sessionID {
				m.addSystemEntry("Already in session: " + targetID)
				return m, nil
			}
			return m.switchToSession(targetID)
		}
		return m.openSessionsModalWithFilter(query)

	case "/open":
		return m.handleOpenCommand(parts)

	case "/drop", "/delete":
		return m.handleDropCommand(parts)

	case "/detail":
		target := -1
		if len(parts) > 1 {
			if n, err := strconv.Atoi(parts[1]); err == nil && n > 0 {
				target = n - 1
			}
		}
		return m.openToolPager(target)

	case "/rewind":
		if m.running {
			m.addSystemEntry("Cannot rewind while a task is running.")
			return m, nil
		}
		if m.client == nil {
			m.addSystemEntry("Core client unavailable.")
			return m, nil
		}

		isPreview := false
		turns := 1
		for _, arg := range parts[1:] {
			lower := strings.ToLower(arg)
			if lower == "preview" || lower == "-p" || lower == "--preview" {
				isPreview = true
			} else if n, err := strconv.Atoi(arg); err == nil && n > 0 {
				turns = n
			} else {
				m.addSystemEntry(fmt.Sprintf("Invalid argument %q. Usage: `/rewind [N]` or `/rewind preview [N]`", arg))
				return m, nil
			}
		}

		if isPreview {
			return m.handleRewindPreview(turns)
		}
		return m.handleRewindExecute(turns)

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
			m.syncTerminalTitle()
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
		targetPath := ""
		if len(parts) > 1 {
			targetPath = strings.Join(parts[1:], " ")
		}
		return m.openDiffModal(targetPath)

	case "/review":
		if len(parts) > 1 {
			targetPath := strings.Join(parts[1:], " ")
			return m.openDiffModal(targetPath)
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
		lines = append(lines, "\n*Tip: Press `Ctrl+D` or use `/diff [path]` to inspect full diff in the interactive diff viewer.*")
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
		if len(parts) != 1 {
			m.addSystemEntry("Usage: `/plan` to toggle Plan mode.")
			return m, nil
		}
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
			// Prefer an exact profile ID match; model IDs may be shared by profiles.
			for _, p := range profiles {
				if strings.EqualFold(p.Profile, targetProfile) {
					valid = true
					targetProfile = p.Profile
					break
				}
			}
			if !valid {
				for _, p := range profiles {
					if strings.EqualFold(p.Model, targetProfile) {
						valid = true
						targetProfile = p.Profile
						break
					}
				}
			}
			if valid {
				err := m.mutateSessionSettings(func(settings *core.SessionSettings) {
					settings.ModelProfile = targetProfile
				})
				if err != nil {
					m.addSystemEntry("Failed to set model: " + err.Error())
				} else {
					m.currentModelProfile = targetProfile
					m.currentModel = m.client.ModelForSession(context.Background(), m.sessionID)
					m.refreshMainContextWindow()
					m.addSystemEntry(fmt.Sprintf("Active model switched to `%s`.", m.currentModelDisplayName()))
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
			activeProfile := m.currentModelProfile
			if strings.TrimSpace(activeProfile) == "" {
				activeProfile = m.currentModel
			}
			var lines []string
			lines = append(lines, fmt.Sprintf("**Current model:** `%s` (`%s`)", m.currentModelDisplayName(), activeProfile))
			lines = append(lines, "\n**Available model profiles:**")
			for _, p := range profiles {
				marker := "  "
				if strings.EqualFold(p.Profile, activeProfile) {
					marker = "* "
				}
				nameDesc := p.Model
				if p.DisplayName != "" && p.DisplayName != p.Model {
					nameDesc = fmt.Sprintf("%s / %s", p.Model, p.DisplayName)
				}
				lines = append(lines, fmt.Sprintf("%s`%s` (%s / %s)", marker, p.Profile, p.Provider, nameDesc))
			}
			lines = append(lines, "\nUsage: `/model <profile>` to switch.")
			m.addSystemEntry(strings.Join(lines, "\n"))
		}
		return m, nil

	case "/export":
		targetPath := ""
		if len(parts) > 1 {
			targetPath = strings.TrimSpace(strings.Join(parts[1:], " "))
		}
		resultText, err := m.exportTranscript(targetPath)
		if err != nil {
			m.addSystemEntry("Export failed: " + err.Error())
		} else {
			m.addSystemEntry(resultText)
		}
		return m, nil

	case "/memory":
		return m.handleMemoryCommand(parts)

	case "/ps", "/process":
		return m.handlePsCommand(parts)

	case "/stop":
		return m.handleStopCommand(parts)

	case "/logs":
		return m.handleLogsCommand(parts)

	case "/agents":
		return m.handleAgentsCommand(parts)

	case "/effort", "/reasoning":
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

	case "/resume":
		if len(parts) > 1 {
			return m.handleOpenCommand(parts)
		}
		if m.running {
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
		return m.openSessionsModal()

	case "/discard":
		if m.runtime != nil {
			m.pausedCheckpoint = nil
			m.activeRecovery = nil
			m.activeToolRecovery = nil
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

	effort := m.currentReasoningEffort()
	if effort == "" {
		effort = "default"
	}

	lines := []string{
		fmt.Sprintf("Model:          %s", modelName),
		fmt.Sprintf("Reasoning Effort: %s", effort),
		fmt.Sprintf("Directory:      %s", m.workspace),
		fmt.Sprintf("Permissions:    %s", permStr),
		fmt.Sprintf("Thread name:    %s", title),
		fmt.Sprintf("Session:        %s", m.sessionID),
		fmt.Sprintf("Theme:          %s", CurrentThemeMode()),
	}

	if m.contextWindow > 0 {
		var inputLabel string
		var usedCtx int64
		if m.lastInputTokens > 0 {
			usedCtx = m.lastInputTokens
			inputLabel = "active prompt"
		} else {
			usedCtx = m.estimatedInputTokens
			inputLabel = "est. input"
		}
		pct := 0.0
		if usedCtx > 0 {
			pct = float64(usedCtx) / float64(m.contextWindow) * 100.0
		}
		lines = append(lines, fmt.Sprintf("Context Window: %s tokens (%s: %s, %.1f%% full)",
			formatTokens(m.contextWindow), inputLabel, formatTokens(usedCtx), pct))
	}
	if m.totalInputTokens > 0 || m.totalOutputTokens > 0 {
		lines = append(lines, fmt.Sprintf("Total Tokens:   in: %s (cached: %s) · out: %s (reasoning: %s)",
			formatTokens(m.totalInputTokens), formatTokens(m.cachedTokens),
			formatTokens(m.totalOutputTokens), formatTokens(m.reasoningTokens)))
	}

	if m.runtime != nil {
		if cmds, err := m.runtime.ListCommands(context.Background()); err == nil && len(cmds) > 0 {
			active := 0
			for _, c := range cmds {
				if c.Status.Active() {
					active++
				}
			}
			lines = append(lines, fmt.Sprintf("Background Ps:  %d active (%d total)", active, len(cmds)))
		}
	}

	totalSubs := m.totalSubagentsCount()
	activeSubs := m.activeSubagentsCount()
	lines = append(lines, fmt.Sprintf("Subagents:      %d active (%d in session); %s", activeSubs, totalSubs, m.delegationLimitsLabel()))

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
			m.invalidateEntryCache()
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

// handleRewindPreview displays a non-destructive summary of which turns would be rewound.
func (m *tuiModel) handleRewindPreview(turns int) (tea.Model, tea.Cmd) {
	var userIndices []int
	for i, entry := range m.entries {
		if entry.Kind == KindUser && entry.Text != "" {
			userIndices = append(userIndices, i)
		}
	}
	if len(userIndices) == 0 {
		m.addSystemEntry("No user turns in session history to rewind.")
		return m, nil
	}

	actualTurns := turns
	if actualTurns > len(userIndices) {
		actualTurns = len(userIndices)
	}

	startUserIdx := userIndices[len(userIndices)-actualTurns]
	affectedEntries := m.entries[startUserIdx:]

	userCount := 0
	assistantCount := 0
	toolCount := 0
	var userPrompts []string

	for _, entry := range affectedEntries {
		switch entry.Kind {
		case KindUser:
			userCount++
			userPrompts = append(userPrompts, entry.Text)
		case KindAssistant:
			assistantCount++
		case KindTool:
			toolCount++
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("**Rewind Preview (%d turn(s) would be removed):**\n\n", userCount))
	for i, p := range userPrompts {
		turnNum := len(userIndices) - userCount + i + 1
		sb.WriteString(fmt.Sprintf("- **Turn %d:** %q\n", turnNum, truncateString(p, 60)))
	}
	sb.WriteString(fmt.Sprintf("\n↳ Impact: %d assistant response(s), %d tool execution(s) would be undone.\n", assistantCount, toolCount))
	sb.WriteString(fmt.Sprintf("Run `/rewind %d` to execute this rewind.", userCount))

	m.addSystemEntry(sb.String())
	return m, nil
}

// handleRewindExecute performs the rewind of turns, reloads session history, and restores the rewound prompt to the input composer.
func (m *tuiModel) handleRewindExecute(turns int) (tea.Model, tea.Cmd) {
	var lastRewoundPrompt string
	for i := len(m.entries) - 1; i >= 0; i-- {
		if m.entries[i].Kind == KindUser && m.entries[i].Text != "" {
			lastRewoundPrompt = m.entries[i].Text
			break
		}
	}

	ctx := context.Background()
	removed, err := m.client.RewindSession(ctx, m.sessionID, turns)
	if err != nil {
		m.addSystemEntry("Rewind failed: " + err.Error())
		return m, nil
	}
	if removed == 0 {
		m.addSystemEntry("No turns were rewound (session history is already at the beginning).")
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
	m.promptHistory = nil
	for _, entry := range m.entries {
		if entry.Kind == KindUser && entry.Text != "" {
			m.promptHistory = append(m.promptHistory, entry.Text)
		}
	}
	m.historyIndex = len(m.promptHistory)
	m.historyDraft = ""
	m.currentStreamText = ""
	m.scrolledUp = false
	m.invalidateEntryCache()
	m.refreshViewportContent()

	if lastRewoundPrompt != "" {
		m.composer.SetValue(lastRewoundPrompt)
		m.composer.CursorEnd()
		m.addSystemEntry(fmt.Sprintf("Rewound %d turn(s). Restored prompt to input composer: %q", removed, truncateString(lastRewoundPrompt, 40)))
	} else {
		m.addSystemEntry(fmt.Sprintf("Rewound %d turn(s).", removed))
	}
	m.updateLayout()
	return m, nil
}

// ExportFormat defines the output file format for session transcript export.
type ExportFormat int

const (
	ExportFormatMarkdown ExportFormat = iota
	ExportFormatHTML
	ExportFormatJSONL
)

func determineExportFormatAndPath(raw string, sessionID string, workspace string) (ExportFormat, string) {
	raw = strings.TrimSpace(raw)
	shortID := sessionID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	if shortID == "" {
		shortID = "current"
	}
	timestamp := time.Now().Format("20060102-150405")

	format := ExportFormatMarkdown
	path := ""

	parts := strings.Fields(raw)
	if len(parts) == 0 {
		return ExportFormatMarkdown, filepath.Join(workspace, fmt.Sprintf("export-%s-%s.md", shortID, timestamp))
	}

	first := strings.ToLower(parts[0])
	switch first {
	case "html", "htm":
		format = ExportFormatHTML
		if len(parts) > 1 {
			path = strings.Join(parts[1:], " ")
		} else {
			path = fmt.Sprintf("export-%s-%s.html", shortID, timestamp)
		}
	case "jsonl", "json":
		format = ExportFormatJSONL
		if len(parts) > 1 {
			path = strings.Join(parts[1:], " ")
		} else {
			path = fmt.Sprintf("export-%s-%s.jsonl", shortID, timestamp)
		}
	case "md", "markdown":
		format = ExportFormatMarkdown
		if len(parts) > 1 {
			path = strings.Join(parts[1:], " ")
		} else {
			path = fmt.Sprintf("export-%s-%s.md", shortID, timestamp)
		}
	default:
		path = raw
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".html", ".htm":
			format = ExportFormatHTML
		case ".jsonl", ".json":
			format = ExportFormatJSONL
		default:
			format = ExportFormatMarkdown
		}
	}

	if !filepath.IsAbs(path) && workspace != "" {
		path = filepath.Join(workspace, path)
	}
	return format, path
}

func (m *tuiModel) exportTranscript(targetPath string) (string, error) {
	title := m.sessionTitle
	if title == "" {
		title = "<untitled session>"
	}
	modelName := m.currentModel
	if modelName == "" && m.client != nil {
		modelName = m.client.ModelForSession(context.Background(), m.sessionID)
	}
	if modelName == "" {
		modelName = "<default>"
	}

	userTurns := 0
	assistantCount := 0
	toolCount := 0
	for _, entry := range m.entries {
		switch entry.Kind {
		case KindUser:
			userTurns++
		case KindAssistant:
			assistantCount++
		case KindTool:
			toolCount++
		}
	}

	format, finalPath := determineExportFormatAndPath(targetPath, m.sessionID, m.workspace)

	switch format {
	case ExportFormatHTML:
		return m.exportHTML(finalPath, title, modelName, userTurns, assistantCount, toolCount)
	case ExportFormatJSONL:
		return m.exportJSONL(finalPath, title, modelName, userTurns, assistantCount, toolCount)
	default:
		return m.exportMarkdown(finalPath, title, modelName, userTurns, assistantCount, toolCount)
	}
}

func (m *tuiModel) exportMarkdown(targetPath, title, modelName string, userTurns, assistantCount, toolCount int) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Session Transcript: %s\n\n", title))
	sb.WriteString(fmt.Sprintf("- **Session ID:** `%s`\n", m.sessionID))
	sb.WriteString(fmt.Sprintf("- **Workspace:** `%s`\n", m.workspace))
	sb.WriteString(fmt.Sprintf("- **Model:** `%s`\n", modelName))
	sb.WriteString(fmt.Sprintf("- **Exported At:** %s\n", time.Now().Format(time.RFC3339)))
	if m.totalInputTokens > 0 || m.totalOutputTokens > 0 {
		sb.WriteString(fmt.Sprintf("- **Token Usage:** %s in (cached: %s) · %s out (reasoning: %s)\n",
			formatTokens(m.totalInputTokens), formatTokens(m.cachedTokens),
			formatTokens(m.totalOutputTokens), formatTokens(m.reasoningTokens)))
	}
	sb.WriteString(fmt.Sprintf("- **Statistics:** %d user turn(s) · %d assistant message(s) · %d tool execution(s)\n",
		userTurns, assistantCount, toolCount))
	sb.WriteString("\n---\n\n")

	entryCount := 0
	for _, entry := range m.entries {
		switch entry.Kind {
		case KindUser:
			entryCount++
			sb.WriteString(fmt.Sprintf("### 👤 User (%s)\n\n%s\n\n", entry.Timestamp.Format("15:04:05"), entry.Text))
		case KindAssistant:
			entryCount++
			sb.WriteString(fmt.Sprintf("### 🤖 Assistant (%s)\n\n%s\n\n", entry.Timestamp.Format("15:04:05"), entry.Text))
		case KindTool:
			entryCount++
			status := "Completed"
			if entry.ToolExitCode != nil && *entry.ToolExitCode != 0 {
				status = fmt.Sprintf("Failed (exit code %d)", *entry.ToolExitCode)
			}
			toolName := entry.ToolName
			if toolName == "" {
				toolName = "tool"
			}
			sb.WriteString(fmt.Sprintf("<details>\n<summary>Tool: <code>%s</code> (%s)</summary>\n\n", toolName, status))
			if entry.ToolCommand != "" {
				sb.WriteString(fmt.Sprintf("**Command:**\n```sh\n%s\n```\n\n", entry.ToolCommand))
			}
			if entry.ToolArgs != "" && entry.ToolArgs != "null" {
				sb.WriteString(fmt.Sprintf("**Arguments:**\n```json\n%s\n```\n\n", entry.ToolArgs))
			}
			if entry.ToolOutput != "" {
				sb.WriteString(fmt.Sprintf("**Output:**\n```\n%s\n```\n\n", strings.TrimSpace(entry.ToolOutput)))
			}
			sb.WriteString("</details>\n\n")
		case KindPlan:
			entryCount++
			sb.WriteString(fmt.Sprintf("### 📋 Plan\n\n%s\n\n", entry.Text))
		case KindSystem:
			sb.WriteString(fmt.Sprintf("> *System: %s*\n\n", entry.Text))
		}
	}

	content := []byte(sb.String())
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(targetPath, content, 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Exported session transcript to `%s` (%s bytes, %d turns, %d tools).", targetPath, formatTokens(int64(len(content))), userTurns, toolCount), nil
}

func (m *tuiModel) exportJSONL(targetPath, title, modelName string, userTurns, assistantCount, toolCount int) (string, error) {
	var lines []string

	meta := map[string]interface{}{
		"type":        "metadata",
		"session_id":  m.sessionID,
		"title":       title,
		"workspace":   m.workspace,
		"model":       modelName,
		"exported_at": time.Now().Format(time.RFC3339),
		"token_usage": map[string]int64{
			"input_tokens":     m.totalInputTokens,
			"cached_tokens":    m.cachedTokens,
			"output_tokens":    m.totalOutputTokens,
			"reasoning_tokens": m.reasoningTokens,
		},
		"statistics": map[string]int{
			"user_turns":         userTurns,
			"assistant_messages": assistantCount,
			"tool_calls":         toolCount,
		},
	}
	metaBytes, err := json.Marshal(meta)
	if err != nil {
		return "", err
	}
	lines = append(lines, string(metaBytes))

	for _, entry := range m.entries {
		rec := map[string]interface{}{
			"timestamp": entry.Timestamp.Format(time.RFC3339),
		}
		switch entry.Kind {
		case KindUser:
			rec["type"] = "message"
			rec["role"] = "user"
			rec["content"] = entry.Text
		case KindAssistant:
			rec["type"] = "message"
			rec["role"] = "assistant"
			rec["content"] = entry.Text
		case KindTool:
			rec["type"] = "tool_call"
			rec["role"] = "tool"
			rec["tool_name"] = entry.ToolName
			if entry.ToolCommand != "" {
				rec["command"] = entry.ToolCommand
			}
			if entry.ToolArgs != "" && entry.ToolArgs != "null" {
				var parsedArgs interface{}
				if err := json.Unmarshal([]byte(entry.ToolArgs), &parsedArgs); err == nil {
					rec["arguments"] = parsedArgs
				} else {
					rec["arguments"] = entry.ToolArgs
				}
			}
			rec["output"] = entry.ToolOutput
			if entry.ToolExitCode != nil {
				rec["exit_code"] = *entry.ToolExitCode
			}
		case KindPlan:
			rec["type"] = "message"
			rec["role"] = "plan"
			rec["content"] = entry.Text
		case KindSystem:
			rec["type"] = "message"
			rec["role"] = "system"
			rec["content"] = entry.Text
		}
		b, err := json.Marshal(rec)
		if err == nil {
			lines = append(lines, string(b))
		}
	}

	content := []byte(strings.Join(lines, "\n") + "\n")
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(targetPath, content, 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Exported session transcript to `%s` (%s bytes, %d records).", targetPath, formatTokens(int64(len(content))), len(lines)), nil
}

func (m *tuiModel) exportHTML(targetPath, title, modelName string, userTurns, assistantCount, toolCount int) (string, error) {
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	sb.WriteString("<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	sb.WriteString(fmt.Sprintf("<title>%s - Lune Transcript</title>\n", html.EscapeString(title)))
	sb.WriteString(`<style>
:root {
  --bg: #0d1117;
  --card-bg: #161b22;
  --border: #30363d;
  --text-primary: #e6edf3;
  --text-muted: #8b949e;
  --accent: #58a6ff;
  --user-bg: #1a2233;
  --tool-bg: #121820;
  --success: #3fb950;
  --error: #f85149;
  --code-bg: #0b0e14;
}
@media (prefers-color-scheme: light) {
  :root {
    --bg: #ffffff;
    --card-bg: #f6f8fa;
    --border: #d0d7de;
    --text-primary: #1f2328;
    --text-muted: #656d76;
    --accent: #0969da;
    --user-bg: #eef3fc;
    --tool-bg: #f3f5f8;
    --success: #1a7f37;
    --error: #cf222e;
    --code-bg: #e8ecf1;
  }
}
* { box-sizing: border-box; }
body {
  background-color: var(--bg);
  color: var(--text-primary);
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
  line-height: 1.6;
  margin: 0;
  padding: 32px 16px;
}
.container { max-width: 900px; margin: 0 auto; }
.header-card {
  background: var(--card-bg);
  border: 1px solid var(--border);
  border-radius: 8px;
  padding: 24px;
  margin-bottom: 24px;
}
.header-card h1 { margin: 0 0 16px 0; font-size: 1.5rem; color: var(--text-primary); }
.meta-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
  gap: 10px 20px;
  font-size: 0.875rem;
  color: var(--text-muted);
}
.meta-item strong { color: var(--text-primary); }
.message {
  margin-bottom: 20px;
  border-radius: 8px;
  padding: 16px 20px;
  border: 1px solid var(--border);
}
.message-user {
  background: var(--user-bg);
  border-color: var(--accent);
}
.message-assistant {
  background: var(--card-bg);
}
.message-plan {
  background: var(--card-bg);
  border-left: 4px solid var(--accent);
}
.message-system {
  background: transparent;
  border-left: 3px solid var(--text-muted);
  font-style: italic;
  color: var(--text-muted);
  padding: 8px 16px;
  margin-bottom: 16px;
}
.message-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 10px;
  font-size: 0.85rem;
  font-weight: 600;
}
.role-user { color: var(--accent); }
.role-assistant { color: var(--success); }
.timestamp { color: var(--text-muted); font-weight: normal; font-size: 0.8rem; }
.content { white-space: pre-wrap; word-break: break-word; }
details.tool-card {
  background: var(--tool-bg);
  border: 1px solid var(--border);
  border-radius: 6px;
  margin-bottom: 16px;
  padding: 10px 14px;
}
details.tool-card summary {
  cursor: pointer;
  font-weight: 600;
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  font-size: 0.875rem;
}
.badge {
  display: inline-block;
  padding: 2px 6px;
  border-radius: 4px;
  font-size: 0.75rem;
  font-weight: 600;
  margin-left: 8px;
}
.badge-success { background: var(--success); color: #ffffff; }
.badge-error { background: var(--error); color: #ffffff; }
pre {
  background: var(--code-bg);
  border: 1px solid var(--border);
  border-radius: 6px;
  padding: 12px;
  overflow-x: auto;
  font-family: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, monospace;
  font-size: 0.85rem;
  margin: 8px 0;
}
</style>
</head>
<body>
<div class="container">
`)

	sb.WriteString("<div class=\"header-card\">\n")
	sb.WriteString(fmt.Sprintf("<h1>%s</h1>\n", html.EscapeString(title)))
	sb.WriteString("<div class=\"meta-grid\">\n")
	sb.WriteString(fmt.Sprintf("<div class=\"meta-item\"><strong>Session ID:</strong> <code>%s</code></div>\n", html.EscapeString(m.sessionID)))
	sb.WriteString(fmt.Sprintf("<div class=\"meta-item\"><strong>Model:</strong> %s</div>\n", html.EscapeString(modelName)))
	sb.WriteString(fmt.Sprintf("<div class=\"meta-item\"><strong>Workspace:</strong> <code>%s</code></div>\n", html.EscapeString(m.workspace)))
	sb.WriteString(fmt.Sprintf("<div class=\"meta-item\"><strong>Exported:</strong> %s</div>\n", time.Now().Format("2006-01-02 15:04:05")))
	if m.totalInputTokens > 0 || m.totalOutputTokens > 0 {
		sb.WriteString(fmt.Sprintf("<div class=\"meta-item\"><strong>Token Usage:</strong> %s in · %s out</div>\n",
			formatTokens(m.totalInputTokens), formatTokens(m.totalOutputTokens)))
	}
	sb.WriteString(fmt.Sprintf("<div class=\"meta-item\"><strong>Turns / Tools:</strong> %d turns · %d tools</div>\n",
		userTurns, toolCount))
	sb.WriteString("</div>\n</div>\n\n")

	for _, entry := range m.entries {
		ts := entry.Timestamp.Format("15:04:05")
		switch entry.Kind {
		case KindUser:
			sb.WriteString("<div class=\"message message-user\">\n")
			sb.WriteString(fmt.Sprintf("<div class=\"message-header\"><span class=\"role-user\">👤 User</span><span class=\"timestamp\">%s</span></div>\n", ts))
			sb.WriteString(fmt.Sprintf("<div class=\"content\">%s</div>\n", html.EscapeString(entry.Text)))
			sb.WriteString("</div>\n")
		case KindAssistant:
			sb.WriteString("<div class=\"message message-assistant\">\n")
			sb.WriteString(fmt.Sprintf("<div class=\"message-header\"><span class=\"role-assistant\">🤖 Assistant</span><span class=\"timestamp\">%s</span></div>\n", ts))
			sb.WriteString(fmt.Sprintf("<div class=\"content\">%s</div>\n", html.EscapeString(entry.Text)))
			sb.WriteString("</div>\n")
		case KindTool:
			statusClass := "badge-success"
			statusText := "Completed"
			if entry.ToolExitCode != nil && *entry.ToolExitCode != 0 {
				statusClass = "badge-error"
				statusText = fmt.Sprintf("Failed (%d)", *entry.ToolExitCode)
			}
			toolName := entry.ToolName
			if toolName == "" {
				toolName = "tool"
			}
			sb.WriteString("<details class=\"tool-card\">\n")
			sb.WriteString(fmt.Sprintf("<summary>🔧 %s <span class=\"badge %s\">%s</span> <span class=\"timestamp\">%s</span></summary>\n",
				html.EscapeString(toolName), statusClass, statusText, ts))
			if entry.ToolCommand != "" {
				sb.WriteString(fmt.Sprintf("<p><strong>Command:</strong></p><pre><code>%s</code></pre>\n", html.EscapeString(entry.ToolCommand)))
			}
			if entry.ToolArgs != "" && entry.ToolArgs != "null" {
				sb.WriteString(fmt.Sprintf("<p><strong>Arguments:</strong></p><pre><code>%s</code></pre>\n", html.EscapeString(entry.ToolArgs)))
			}
			if entry.ToolOutput != "" {
				sb.WriteString(fmt.Sprintf("<p><strong>Output:</strong></p><pre><code>%s</code></pre>\n", html.EscapeString(strings.TrimSpace(entry.ToolOutput))))
			}
			sb.WriteString("</details>\n")
		case KindPlan:
			sb.WriteString("<div class=\"message message-plan\">\n")
			sb.WriteString(fmt.Sprintf("<div class=\"message-header\"><span>📋 Plan</span><span class=\"timestamp\">%s</span></div>\n", ts))
			sb.WriteString(fmt.Sprintf("<div class=\"content\">%s</div>\n", html.EscapeString(entry.Text)))
			sb.WriteString("</div>\n")
		case KindSystem:
			sb.WriteString(fmt.Sprintf("<div class=\"message-system\">ℹ️ %s</div>\n", html.EscapeString(entry.Text)))
		}
	}

	sb.WriteString("</div>\n</body>\n</html>\n")

	content := []byte(sb.String())
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(targetPath, content, 0644); err != nil {
		return "", err
	}
	return fmt.Sprintf("Exported session transcript to `%s` (%s bytes, %d turns, %d tools).",
		targetPath, formatTokens(int64(len(content))), userTurns, toolCount), nil
}

func (m *tuiModel) renderContextDiagnostics() string {
	var lines []string
	lines = append(lines, "**Context Status & Diagnostics**\n")

	// 1. Workspace & Rules
	ws := m.workspace
	if ws == "" && m.client != nil {
		ws = m.client.Workspace()
	}
	permStr := string(m.permissionMode)
	if m.planMode {
		permStr += " (Plan mode locked)"
	}
	lines = append(lines, "**Workspace & Permissions:**")
	lines = append(lines, fmt.Sprintf("- Directory: `%s`", ws))
	lines = append(lines, fmt.Sprintf("- Permissions: `%s`", permStr))

	ruleFiles := []string{"AGENTS.md", "CLAUDE.md"}
	var foundRules []string
	if ws != "" {
		for _, rf := range ruleFiles {
			p := filepath.Join(ws, rf)
			if fi, err := os.Stat(p); err == nil {
				foundRules = append(foundRules, fmt.Sprintf("`%s`: Active (%s bytes)", rf, formatTokens(fi.Size())))
			}
		}
	}
	if len(foundRules) > 0 {
		lines = append(lines, fmt.Sprintf("- Rules: %s", strings.Join(foundRules, ", ")))
	} else {
		lines = append(lines, "- Rules: None detected in workspace root")
	}

	// 2. Session & Model
	modelName := m.currentModel
	if modelName == "" && m.client != nil {
		modelName = m.client.ModelForSession(context.Background(), m.sessionID)
	}
	if modelName == "" {
		modelName = "<default>"
	}
	title := m.sessionTitle
	if title == "" {
		title = "<none>"
	}
	lines = append(lines, "\n**Session & Model:**")
	lines = append(lines, fmt.Sprintf("- Active Model: `%s`", modelName))
	lines = append(lines, fmt.Sprintf("- Session ID: `%s`", m.sessionID))
	lines = append(lines, fmt.Sprintf("- Thread: `%s`", title))

	// 3. Capabilities & Tools
	lines = append(lines, "\n**Capabilities & Tools:**")
	mcpServerCount := 0
	mcpToolCount := 0
	if m.client != nil {
		for _, s := range m.client.MCPServers() {
			if s.Enabled {
				mcpServerCount++
				mcpToolCount += s.ToolCount
			}
		}
	}
	if mcpServerCount > 0 {
		lines = append(lines, fmt.Sprintf("- MCP Servers: %d active (%d tools registered)", mcpServerCount, mcpToolCount))
	} else {
		lines = append(lines, "- MCP Servers: None active")
	}
	if m.planMode {
		lines = append(lines, "- Plan Mode: Active (restricted to read-only tools)")
	} else {
		lines = append(lines, "- Plan Mode: Inactive (all permitted tools available)")
	}

	// 4. Context Window & Token Utilization
	lines = append(lines, "\n**Context Window Utilization:**")
	if m.contextWindow > 0 {
		ratio := 0.0
		if m.estimatedInputTokens > 0 {
			ratio = float64(m.estimatedInputTokens) / float64(m.contextWindow)
		}
		pct := ratio * 100.0
		progressBar := renderProgressBar(ratio, 20)
		lines = append(lines, fmt.Sprintf("- Budget: %s %.1f%% (%s / %s tokens)",
			progressBar, pct, formatTokens(m.estimatedInputTokens), formatTokens(m.contextWindow)))
	} else {
		lines = append(lines, "- Budget: Standard context window (no provider limit reported yet)")
	}
	lines = append(lines, fmt.Sprintf("- Cumulative Tokens: in: %s (cached: %s) · out: %s (reasoning: %s)",
		formatTokens(m.totalInputTokens), formatTokens(m.cachedTokens),
		formatTokens(m.totalOutputTokens), formatTokens(m.reasoningTokens)))

	// 5. Conversation Metrics
	userTurns := 0
	asstTurns := 0
	toolTurns := 0
	for _, e := range m.entries {
		switch e.Kind {
		case KindUser:
			userTurns++
		case KindAssistant:
			asstTurns++
		case KindTool:
			toolTurns++
		}
	}
	lines = append(lines, fmt.Sprintf("- Conversation turns: %d total (%d user, %d assistant, %d tool calls)",
		len(m.entries), userTurns, asstTurns, toolTurns))
	totalSubs := m.totalSubagentsCount()
	activeSubs := m.activeSubagentsCount()
	lines = append(lines, fmt.Sprintf("- Subagents: %d active (%d total in session); %s; use `/agents` to inspect", activeSubs, totalSubs, m.delegationLimitsLabel()))

	// 6. Workspace Memory
	lines = append(lines, "\n**Workspace Memory:**")
	if m.client != nil {
		if items, err := m.client.ListMemories(context.Background(), 100); err == nil {
			if len(items) > 0 {
				lines = append(lines, fmt.Sprintf("- Stored Memories: %d active items (use `/memory` to inspect or manage)", len(items)))
			} else {
				lines = append(lines, "- Stored Memories: None yet (use `/memory add <text>` to record conventions)")
			}
		} else {
			lines = append(lines, fmt.Sprintf("- Memory Engine: unavailable (%v)", err))
		}
	} else {
		lines = append(lines, "- Stored Memories: Unavailable (no client)")
	}

	// 7. Background Processes
	lines = append(lines, "\n**Background Processes:**")
	if m.runtime != nil {
		if cmds, err := m.runtime.ListCommands(context.Background()); err == nil {
			active := 0
			for _, c := range cmds {
				if c.Status.Active() {
					active++
				}
			}
			if len(cmds) > 0 {
				lines = append(lines, fmt.Sprintf("- Processes: %d active (%d total, use `/ps` to inspect)", active, len(cmds)))
			} else {
				lines = append(lines, "- Processes: None in current session")
			}
		} else {
			lines = append(lines, fmt.Sprintf("- Process Manager: unavailable (%v)", err))
		}
	} else {
		lines = append(lines, "- Processes: None in current session")
	}

	return strings.Join(lines, "\n")
}

func (m *tuiModel) handleMemoryCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.addSystemEntry("Memory management unavailable (no active client).")
		return m, nil
	}

	ctx := context.Background()
	subcmd := "list"
	if len(parts) > 1 {
		subcmd = strings.ToLower(parts[1])
	}

	switch subcmd {
	case "list":
		query := ""
		if len(parts) > 2 {
			query = strings.TrimSpace(strings.Join(parts[2:], " "))
		}
		if query != "" {
			results, err := m.client.SearchMemories(ctx, query, 20)
			if err != nil {
				m.addSystemEntry("Failed to search memories: " + err.Error())
				return m, nil
			}
			if len(results) == 0 {
				m.addSystemEntry(fmt.Sprintf("No workspace memories matching %q found.", query))
				return m, nil
			}
			var lines []string
			lines = append(lines, fmt.Sprintf("**Workspace Memories (%d matches for %q):**", len(results), query))
			for _, r := range results {
				shortID := r.Item.ID
				if len(shortID) > 8 {
					shortID = shortID[:8]
				}
				timeStr := r.Item.UpdatedAt.Format("Jan 02 15:04")
				lines = append(lines, fmt.Sprintf("- `[%s]` **%s**: %s *(%s)*", shortID, r.Item.Category, r.Item.Content, timeStr))
			}
			m.addSystemEntry(strings.Join(lines, "\n"))
			return m, nil
		}

		items, err := m.client.ListMemories(ctx, 50)
		if err != nil {
			m.addSystemEntry("Failed to list memories: " + err.Error())
			return m, nil
		}
		if len(items) == 0 {
			m.addSystemEntry("**Workspace Memory:**\nNo workspace memories stored yet.\n\nUse `/memory add <text>` to record conventions, technical preferences, or rules for this workspace.")
			return m, nil
		}
		var lines []string
		lines = append(lines, fmt.Sprintf("**Workspace Memories (%d items):**", len(items)))
		for _, item := range items {
			shortID := item.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			timeStr := item.UpdatedAt.Format("Jan 02 15:04")
			lines = append(lines, fmt.Sprintf("- `[%s]` **%s**: %s *(%s)*", shortID, item.Category, item.Content, timeStr))
		}
		lines = append(lines, "\n*Use `/memory add <text>` to record, `/memory remove <id>` to delete.*")
		m.addSystemEntry(strings.Join(lines, "\n"))
		return m, nil

	case "add":
		if len(parts) < 3 {
			m.addSystemEntry("Usage: `/memory add [category] <content>`\nCategories: `preference`, `convention`, `decision`, `correction`, `verified_fact` (default: `preference`)")
			return m, nil
		}
		cat := memory.CategoryPreference
		contentParts := parts[2:]
		firstLower := strings.ToLower(parts[2])
		switch memory.Category(firstLower) {
		case memory.CategoryPreference, memory.CategoryConvention, memory.CategoryDecision, memory.CategoryCorrection, memory.CategoryVerifiedFact:
			cat = memory.Category(firstLower)
			contentParts = parts[3:]
		}
		content := strings.TrimSpace(strings.Join(contentParts, " "))
		if content == "" {
			m.addSystemEntry("Memory content cannot be empty.")
			return m, nil
		}
		item, err := m.client.AddMemory(ctx, memory.Input{
			Category:   cat,
			Content:    content,
			Importance: 3,
		})
		if err != nil {
			m.addSystemEntry("Failed to add memory: " + err.Error())
			return m, nil
		}
		shortID := item.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		m.addSystemEntry(fmt.Sprintf("Added workspace memory `[%s]` (**%s**): %s", shortID, item.Category, item.Content))
		return m, nil

	case "remove", "delete":
		if len(parts) < 3 {
			m.addSystemEntry("Usage: `/memory remove <id|prefix>`")
			return m, nil
		}
		target := parts[2]
		items, err := m.client.ListMemories(ctx, 200)
		if err != nil {
			m.addSystemEntry("Failed to query memories: " + err.Error())
			return m, nil
		}
		var matches []memory.Item
		for _, it := range items {
			if strings.HasPrefix(strings.ToLower(it.ID), strings.ToLower(target)) {
				matches = append(matches, it)
			}
		}
		if len(matches) == 0 {
			m.addSystemEntry(fmt.Sprintf("Memory item %q not found.", target))
			return m, nil
		}
		if len(matches) > 1 {
			var ids []string
			for _, match := range matches {
				s := match.ID
				if len(s) > 8 {
					s = s[:8]
				}
				ids = append(ids, s)
			}
			m.addSystemEntry(fmt.Sprintf("Memory ID prefix %q is ambiguous (%d matches: %s).", target, len(matches), strings.Join(ids, ", ")))
			return m, nil
		}
		match := matches[0]
		if err := m.client.DeleteMemory(ctx, match.ID); err != nil {
			m.addSystemEntry("Failed to delete memory: " + err.Error())
			return m, nil
		}
		shortID := match.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		m.addSystemEntry(fmt.Sprintf("Deleted workspace memory `[%s]`: %s", shortID, match.Content))
		return m, nil

	case "clear":
		hasForce := false
		for _, p := range parts[2:] {
			if p == "--force" || p == "confirm" || p == "-f" {
				hasForce = true
				break
			}
		}
		if !hasForce {
			m.addSystemEntry("Are you sure you want to clear all workspace memories?\nRun `/memory clear --force` to confirm.")
			return m, nil
		}
		if err := m.client.ClearMemories(ctx); err != nil {
			m.addSystemEntry("Failed to clear memories: " + err.Error())
			return m, nil
		}
		m.addSystemEntry("Cleared all workspace memories.")
		return m, nil

	case "help":
		lines := []string{
			"**Workspace Memory Commands:**",
			"- `/memory` or `/memory list`: List all workspace memories",
			"- `/memory list <query>`: Search memories by text",
			"- `/memory add [category] <text>`: Add a workspace convention or preference",
			"- `/memory remove <id|prefix>`: Remove a memory item by ID or prefix",
			"- `/memory clear --force`: Clear all workspace memories",
			"\n*Categories: `preference`, `convention`, `decision`, `correction`, `verified_fact`*",
		}
		m.addSystemEntry(strings.Join(lines, "\n"))
		return m, nil

	default:
		// If unknown subcmd, treat as a query search
		query := strings.TrimSpace(strings.Join(parts[1:], " "))
		results, err := m.client.SearchMemories(ctx, query, 20)
		if err != nil {
			m.addSystemEntry("Failed to search memories: " + err.Error())
			return m, nil
		}
		if len(results) == 0 {
			m.addSystemEntry(fmt.Sprintf("No memories matching %q found.", query))
			return m, nil
		}
		var lines []string
		lines = append(lines, fmt.Sprintf("**Workspace Memories (%d matches for %q):**", len(results), query))
		for _, r := range results {
			shortID := r.Item.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			timeStr := r.Item.UpdatedAt.Format("Jan 02 15:04")
			lines = append(lines, fmt.Sprintf("- `[%s]` **%s**: %s *(%s)*", shortID, r.Item.Category, r.Item.Content, timeStr))
		}
		m.addSystemEntry(strings.Join(lines, "\n"))
		return m, nil
	}
}

func renderProgressBar(ratio float64, width int) string {
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio * float64(width))
	if filled > width {
		filled = width
	}
	empty := width - filled
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", empty) + "]"
}

func (m *tuiModel) handlePsCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		m.addSystemEntry("Background process management unavailable (no active session runtime).")
		return m, nil
	}

	ctx := context.Background()
	commands, err := m.runtime.ListCommands(ctx)
	if err != nil {
		m.addSystemEntry("Failed to list background processes: " + err.Error())
		return m, nil
	}

	if len(commands) == 0 {
		m.addSystemEntry("No background processes in current session.")
		return m, nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("**Background Processes (%d):**", len(commands)))
	for _, cmd := range commands {
		shortID := cmd.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		statusStr := string(cmd.Status)
		timeInfo := ""
		if !cmd.FinishedAt.IsZero() {
			timeInfo = fmt.Sprintf(" *(duration %s)*", formatDuration(cmd.FinishedAt.Sub(cmd.StartedAt)))
		} else if !cmd.StartedAt.IsZero() {
			timeInfo = fmt.Sprintf(" *(started %s ago)*", formatDuration(time.Since(cmd.StartedAt)))
		}
		exitStr := ""
		if cmd.ExitCode != nil {
			exitStr = fmt.Sprintf(" `exit %d`", *cmd.ExitCode)
		}
		lines = append(lines, fmt.Sprintf("- `[%s]` **%s**%s · `%s`%s", shortID, statusStr, exitStr, cmd.Command, timeInfo))
	}
	lines = append(lines, "\n*Use `/stop <id|all>` to terminate, `/logs <id>` to inspect output.*")
	m.addSystemEntry(strings.Join(lines, "\n"))
	return m, nil
}

func (m *tuiModel) handleStopCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		m.addSystemEntry("Process termination unavailable (no active session runtime).")
		return m, nil
	}

	if len(parts) < 2 {
		m.addSystemEntry("Usage: `/stop <id|prefix|all>`")
		return m, nil
	}

	target := strings.ToLower(parts[1])
	ctx := context.Background()
	commands, err := m.runtime.ListCommands(ctx)
	if err != nil {
		m.addSystemEntry("Failed to list processes: " + err.Error())
		return m, nil
	}

	if target == "all" {
		var active []core.CommandInfo
		for _, cmd := range commands {
			if cmd.Status.Active() {
				active = append(active, cmd)
			}
		}
		if len(active) == 0 {
			m.addSystemEntry("No active background processes to stop.")
			return m, nil
		}
		var stopped []string
		for _, cmd := range active {
			if _, err := m.runtime.StopCommand(ctx, cmd.ID); err == nil {
				shortID := cmd.ID
				if len(shortID) > 8 {
					shortID = shortID[:8]
				}
				stopped = append(stopped, shortID)
			}
		}
		m.addSystemEntry(fmt.Sprintf("Stopped %d active background process(es): %s", len(stopped), strings.Join(stopped, ", ")))
		return m, nil
	}

	var matches []core.CommandInfo
	for _, cmd := range commands {
		if strings.HasPrefix(strings.ToLower(cmd.ID), target) {
			matches = append(matches, cmd)
		}
	}

	if len(matches) == 0 {
		m.addSystemEntry(fmt.Sprintf("Process %q not found.", target))
		return m, nil
	}

	if len(matches) > 1 {
		var ids []string
		for _, match := range matches {
			s := match.ID
			if len(s) > 8 {
				s = s[:8]
			}
			ids = append(ids, s)
		}
		m.addSystemEntry(fmt.Sprintf("Process ID prefix %q is ambiguous (%d matches: %s).", target, len(matches), strings.Join(ids, ", ")))
		return m, nil
	}

	match := matches[0]
	if _, err := m.runtime.StopCommand(ctx, match.ID); err != nil {
		m.addSystemEntry("Failed to stop process: " + err.Error())
		return m, nil
	}

	shortID := match.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	m.addSystemEntry(fmt.Sprintf("Stopped background process `[%s]`: `%s`", shortID, match.Command))
	return m, nil
}

func (m *tuiModel) handleLogsCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.runtime == nil {
		m.addSystemEntry("Process logs unavailable (no active session runtime).")
		return m, nil
	}

	if len(parts) < 2 {
		m.addSystemEntry("Usage: `/logs <id|prefix> [--tail N]`")
		return m, nil
	}

	target := strings.ToLower(parts[1])
	tailLines := 50
	for i := 2; i < len(parts); i++ {
		if (parts[i] == "--tail" || parts[i] == "-n") && i+1 < len(parts) {
			if n, err := strconv.Atoi(parts[i+1]); err == nil && n > 0 {
				tailLines = n
				i++
			}
		}
	}

	ctx := context.Background()
	commands, err := m.runtime.ListCommands(ctx)
	if err != nil {
		m.addSystemEntry("Failed to list processes: " + err.Error())
		return m, nil
	}

	var matches []core.CommandInfo
	for _, cmd := range commands {
		if strings.HasPrefix(strings.ToLower(cmd.ID), target) {
			matches = append(matches, cmd)
		}
	}

	if len(matches) == 0 {
		m.addSystemEntry(fmt.Sprintf("Process %q not found.", target))
		return m, nil
	}

	if len(matches) > 1 {
		var ids []string
		for _, match := range matches {
			s := match.ID
			if len(s) > 8 {
				s = s[:8]
			}
			ids = append(ids, s)
		}
		m.addSystemEntry(fmt.Sprintf("Process ID prefix %q is ambiguous (%d matches: %s).", target, len(matches), strings.Join(ids, ", ")))
		return m, nil
	}

	match := matches[0]
	out, err := m.runtime.ReadCommand(ctx, match.ID, core.CommandReadOptions{})
	if err != nil {
		m.addSystemEntry("Failed to read process output: " + err.Error())
		return m, nil
	}

	shortID := match.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	content := strings.TrimSpace(out.Output)
	if content == "" {
		m.addSystemEntry(fmt.Sprintf("Process `[%s]` (`%s`) produced no output yet.", shortID, match.Command))
		return m, nil
	}

	rawLines := strings.Split(content, "\n")
	totalLines := len(rawLines)
	if len(rawLines) > tailLines {
		rawLines = rawLines[len(rawLines)-tailLines:]
	}

	header := fmt.Sprintf("**Logs for `[%s]` (`%s`, %s):**", shortID, match.Command, match.Status)
	if totalLines > tailLines {
		header += fmt.Sprintf(" *(showing last %d of %d lines)*", tailLines, totalLines)
	}

	logBlock := "```\n" + strings.Join(rawLines, "\n") + "\n```"
	m.addSystemEntry(header + "\n" + logBlock)
	return m, nil
}

func (m *tuiModel) handleAgentsCommand(parts []string) (tea.Model, tea.Cmd) {
	target := ""
	if len(parts) > 1 {
		target = strings.TrimSpace(parts[1])
	}
	if !m.openAgentsPanel(target) {
		m.addSystemEntry(fmt.Sprintf("Agent `%s` not found. Use `/agents` to list agents in this session.", target))
		return m, nil
	}
	m.updateLayout()
	return m, nil
}

// handleOpenCommand handles switching to a session by ID/prefix or opening the modal.
func (m *tuiModel) handleOpenCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.running {
		m.addSystemEntry("Cannot switch sessions while a task is running. Cancel or wait for it to finish.")
		return m, nil
	}
	if m.client == nil {
		m.addSystemEntry("Session management unavailable.")
		return m, nil
	}
	if len(parts) < 2 {
		return m.openSessionsModal()
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

	return m.switchToSession(targetID)
}

// openSessionsModalWithFilter queries workspace sessions and opens the modal with an optional pre-set filter.
func (m *tuiModel) openSessionsModalWithFilter(filter string) (tea.Model, tea.Cmd) {
	if m.client == nil {
		m.addSystemEntry("Session management unavailable.")
		return m, nil
	}
	sessions, err := m.client.ListSessions(context.Background(), 100, false)
	if err != nil {
		m.addSystemEntry("Failed to query sessions: " + err.Error())
		return m, nil
	}
	m.sessionsModalOpen = true
	m.shortcutsOpen = false
	m.autocompleteOpen = false
	m.sessionsConfirmDelete = false
	m.sessionsDeletingID = ""
	m.sessionsItems = sessions
	m.sessionsFilter = strings.TrimSpace(filter)
	m.filterSessions()
	m.sessionsIndex = 0
	for i, s := range m.sessionsFiltered {
		if s.ID == m.sessionID {
			m.sessionsIndex = i
			break
		}
	}
	m.updateLayout()
	return m, nil
}

// openSessionsModal queries workspace sessions and displays the interactive session switcher modal.
func (m *tuiModel) openSessionsModal() (tea.Model, tea.Cmd) {
	return m.openSessionsModalWithFilter("")
}

// handleDropCommand deletes a saved session matching the target prefix or ID.
func (m *tuiModel) handleDropCommand(parts []string) (tea.Model, tea.Cmd) {
	if m.running {
		m.addSystemEntry("Cannot delete sessions while a task is running.")
		return m, nil
	}
	if len(parts) < 2 {
		m.addSystemEntry("Usage: `/drop <session-id|prefix>` (or `/delete`).")
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
		m.addSystemEntry("Cannot delete currently active session. Switch to another session first with `/open`.")
		return m, nil
	}

	if err := m.client.DeleteSession(ctx, targetID); err != nil {
		m.addSystemEntry("Failed to delete session: " + err.Error())
		return m, nil
	}
	m.addSystemEntry("Deleted session: " + targetID)
	return m, nil
}

// filterSessions updates sessionsFiltered according to the current sessionsFilter.
func (m *tuiModel) filterSessions() {
	query := strings.ToLower(strings.TrimSpace(m.sessionsFilter))
	if query == "" {
		m.sessionsFiltered = m.sessionsItems
	} else {
		var matches []core.Session
		for _, s := range m.sessionsItems {
			if strings.Contains(strings.ToLower(s.ID), query) || strings.Contains(strings.ToLower(s.Title), query) {
				matches = append(matches, s)
			}
		}
		m.sessionsFiltered = matches
	}
	if m.sessionsIndex >= len(m.sessionsFiltered) {
		m.sessionsIndex = max(0, len(m.sessionsFiltered)-1)
	}
}

// switchToSession switches to the specified session runtime and refreshes TUI state.
func (m *tuiModel) switchToSession(targetID string) (tea.Model, tea.Cmd) {
	ctx := context.Background()
	if err := m.bindSessionRuntime(ctx, targetID); err != nil {
		m.addSystemEntry("Failed to open session: " + err.Error())
		m.updateLayout()
		return m, nil
	}
	m.addSystemEntry("Opened session: " + targetID)
	return m, m.subscribeRuntimeEvents()
}

// getToolEntryIndices returns the indices of all KindTool entries in m.entries.
func (m *tuiModel) getToolEntryIndices() []int {
	var indices []int
	for i, entry := range m.entries {
		if entry.Kind == KindTool {
			indices = append(indices, i)
		}
	}
	return indices
}

// toolPagerVisibleLines calculates the visible line capacity of the tool pager modal.
func (m *tuiModel) toolPagerVisibleLines() int {
	if m.height <= 0 {
		return 10
	}
	lines := m.height - 12
	if lines > 18 {
		lines = 18
	}
	if lines < 5 {
		lines = 5
	}
	return lines
}

// openToolPager opens the floating pager modal to inspect full tool output.
func (m *tuiModel) openToolPager(targetIndex int) (tea.Model, tea.Cmd) {
	indices := m.getToolEntryIndices()
	if len(indices) == 0 {
		m.addSystemEntry("No tool executions in current session.")
		m.updateLayout()
		return m, nil
	}

	m.shortcutsOpen = false
	m.sessionsModalOpen = false
	m.autocompleteOpen = false
	m.toolPagerOpen = true
	m.toolPagerScrollY = 0

	if targetIndex < 0 || targetIndex >= len(indices) {
		m.toolPagerIndex = len(indices) - 1
	} else {
		m.toolPagerIndex = targetIndex
	}

	m.updateLayout()
	return m, nil
}

// diffModalVisibleLines calculates the visible line capacity of the diff modal.
func (m *tuiModel) diffModalVisibleLines() int {
	if m.height <= 0 {
		return 10
	}
	lines := m.height - 12
	if lines > 20 {
		lines = 20
	}
	if lines < 5 {
		lines = 5
	}
	return lines
}

// openDiffModal fetches workspace git diffs and opens the floating diff viewer modal.
func (m *tuiModel) openDiffModal(targetPath string) (tea.Model, tea.Cmd) {
	ws := m.workspace
	if ws == "" && m.client != nil {
		ws = m.client.Workspace()
	}

	var diffArgs []string
	diffArgs = append(diffArgs, "diff")
	if targetPath != "" {
		diffArgs = append(diffArgs, "--", targetPath)
	}

	cmd := exec.CommandContext(context.Background(), "git", diffArgs...)
	cmd.Dir = ws
	out, err := cmd.CombinedOutput()
	if err != nil {
		m.addSystemEntry("Failed to run git diff: " + err.Error())
		m.updateLayout()
		return m, nil
	}

	diffText := strings.TrimSpace(string(out))
	if diffText == "" && targetPath == "" {
		stagedCmd := exec.CommandContext(context.Background(), "git", "diff", "--cached")
		stagedCmd.Dir = ws
		stagedOut, err := stagedCmd.CombinedOutput()
		if err == nil {
			diffText = strings.TrimSpace(string(stagedOut))
		}
	}

	if diffText == "" {
		if targetPath != "" {
			m.addSystemEntry(fmt.Sprintf("No changes found for: %s", targetPath))
		} else {
			m.addSystemEntry("No git changes detected in workspace.")
		}
		m.updateLayout()
		return m, nil
	}

	files := parseGitDiff(diffText)
	if len(files) == 0 {
		if targetPath != "" {
			m.addSystemEntry(fmt.Sprintf("No changes found for: %s", targetPath))
		} else {
			m.addSystemEntry("No git changes detected in workspace.")
		}
		m.updateLayout()
		return m, nil
	}

	m.shortcutsOpen = false
	m.sessionsModalOpen = false
	m.autocompleteOpen = false
	m.toolPagerOpen = false
	m.diffModalOpen = true
	m.diffFiles = files
	m.diffFileIndex = 0
	m.diffScrollY = 0

	m.updateLayout()
	return m, nil
}

// reasoningEffortOptions returns the selectable reasoning-effort levels in display order.
func reasoningEffortOptions() []ReasoningEffortOption {
	return []ReasoningEffortOption{
		{Value: "", Label: "default", Description: "Provider default (omit effort)"},
		{Value: "none", Label: "none", Description: "Disable reasoning"},
		{Value: "minimal", Label: "minimal", Description: "Minimal reasoning tokens"},
		{Value: "low", Label: "low", Description: "Faster, lighter reasoning"},
		{Value: "medium", Label: "medium", Description: "Balanced reasoning"},
		{Value: "high", Label: "high", Description: "Deeper reasoning"},
		{Value: "xhigh", Label: "xhigh", Description: "Extra-deep reasoning"},
		{Value: "max", Label: "max", Description: "Maximum reasoning budget"},
	}
}

// normalizeReasoningEffort maps a user-provided level to its canonical value.
// The empty string means "provider default" (effort is omitted from the request).
func normalizeReasoningEffort(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "default", "unset", "clear", "reset", "off", "inherit":
		return "", true
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(raw)), true
	}
	return "", false
}

// openEffortPicker opens the interactive reasoning-effort selector.
func (m *tuiModel) openEffortPicker() {
	current := m.currentReasoningEffort()
	m.reasoningEffort = current
	selected := 0
	for i, opt := range reasoningEffortOptions() {
		if opt.Value == current {
			selected = i
			break
		}
	}
	m.effortPicker = &ActiveEffortPicker{Selected: selected}
	m.updateLayout()
}

// mutateSessionSettings updates only the requested session settings, preserving
// all other fields already stored on the session.
func (m *tuiModel) mutateSessionSettings(mutate func(*core.SessionSettings)) error {
	if m.client == nil || m.sessionID == "" {
		return nil
	}
	ctx := context.Background()
	saved, err := m.client.GetSession(ctx, m.sessionID)
	if err != nil {
		return err
	}
	settings := saved.Settings
	mutate(&settings)
	return m.client.SetSessionSettings(ctx, m.sessionID, settings)
}

// applyReasoningEffort persists and applies a reasoning-effort level.
func (m *tuiModel) applyReasoningEffort(value string) (tea.Model, tea.Cmd) {
	if err := m.mutateSessionSettings(func(settings *core.SessionSettings) {
		settings.ReasoningEffort = value
	}); err != nil {
		m.addSystemEntry("Failed to update reasoning effort: " + err.Error())
		m.updateLayout()
		return m, nil
	}
	m.reasoningEffort = value
	display := value
	if display == "" {
		display = "default"
	}
	m.addSystemEntry(fmt.Sprintf("Reasoning effort set to `%s`.", display))
	m.updateLayout()
	return m, nil
}

// handleEffortCommand opens the interactive effort picker, or sets a level directly
// when an explicit level is supplied.
func (m *tuiModel) handleEffortCommand(parts []string) (tea.Model, tea.Cmd) {
	if len(parts) == 1 {
		m.openEffortPicker()
		return m, nil
	}
	value, ok := normalizeReasoningEffort(parts[1])
	if !ok {
		m.addSystemEntry(fmt.Sprintf("Invalid reasoning effort `%s`. Valid levels: `default`, `none`, `minimal`, `low`, `medium`, `high`, `xhigh`, `max`.", parts[1]))
		return m, nil
	}
	return m.applyReasoningEffort(value)
}
