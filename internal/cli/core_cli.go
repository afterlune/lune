package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	core "github.com/afterlune/luneCore"
)

type cliRuntime interface {
	SubmitTurn(context.Context, core.TurnRequest, core.SubmissionMode) (core.TurnSubmission, error)
	WaitTurn(context.Context, string) (core.TurnResult, error)
	SubscribeEvents(context.Context, int64) (<-chan core.Event, error)
	State(context.Context) (core.RuntimeState, error)
	Interrupt(string) bool
	Recover(context.Context, ...core.ExecutionOptions) (core.TurnSubmission, error)
	DiscardPaused(context.Context) (bool, error)
	PendingRequests(context.Context) ([]core.PendingRequest, error)
	ResolveApproval(string, bool) error
	SetUserInteractionAvailable(bool)
}

type cliCore interface {
	Workspace() string
	LatestSession(context.Context) (core.Session, bool, error)
	CreateSession(context.Context, json.RawMessage) (core.Session, error)
	GetSession(context.Context, string) (core.Session, error)
	ListSessions(context.Context, int, bool) ([]core.Session, error)
	ForkSession(context.Context, string) (core.Session, error)
	RewindSession(context.Context, string, int) (int, error)
	SetSessionTitle(context.Context, string, string) error
	OpenSessionRuntime(context.Context, string) (cliRuntime, error)
	Compact(context.Context, string) (int, error)
	PermissionMode() core.PermissionMode
	SetPermissionMode(core.PermissionMode) error
	ListModelProfiles() []core.ModelOption
	ModelForSession(context.Context, string) string
	SetSessionSettings(context.Context, string, core.SessionSettings) error
	MCPServers() []core.MCPServerStatus
	EnableMCPServer(string) error
	DisableMCPServer(string) error
	AuthenticateMCPServer(context.Context, string) error
}

type cliCommandHelp struct {
	name        string
	usage       string
	description string
}

var cliCommands = []cliCommandHelp{
	{name: "/clear", usage: "/clear", description: "Clear terminal screen."},
	{name: "/compact", usage: "/compact", description: "Compact older conversation history."},
	{name: "/context", usage: "/context", description: "Show context window budget, system rules, and capabilities."},
	{name: "/diff", usage: "/diff [path]", description: "Inspect git code changes in workspace."},
	{name: "/discard", usage: "/discard", description: "Discard the paused task in this session."},
	{name: "/exit", usage: "/exit", description: "Exit the chat."},
	{name: "/export", usage: "/export [path]", description: "Export session conversation transcript to a markdown file."},
	{name: "/fork", usage: "/fork", description: "Fork the current session."},
	{name: "/help", usage: "/help", description: "Show available commands."},
	{name: "/mcp", usage: "/mcp [list|enable|disable|auth]", description: "Manage external MCP servers."},
	{name: "/model", usage: "/model [profile]", description: "View or change active model profile."},
	{name: "/new", usage: "/new", description: "Start a new session."},
	{name: "/open", usage: "/open [id|prefix]", description: "Switch to an existing session."},
	{name: "/permissions", usage: "/permissions [mode]", description: "Show or set mode: read-only, workspace, ask, full-access."},
	{name: "/plan", usage: "/plan", description: "Toggle Plan mode (read-only exploration)."},
	{name: "/resume", usage: "/resume", description: "Resume the paused task in this session."},
	{name: "/review", usage: "/review [path]", description: "Summarize and review workspace changes."},
	{name: "/rewind", usage: "/rewind [N]", description: "Rewind conversation to a previous turn."},
	{name: "/sessions", usage: "/sessions [query]", description: "List or search recent sessions in this workspace."},
	{name: "/status", usage: "/status", description: "Show session, model, and token status."},
	{name: "/title", usage: "/title [name]", description: "View or set custom title for this session."},
}

func cliCommandNames() []string {
	names := make([]string, 0, len(cliCommands))
	for _, command := range cliCommands {
		names = append(names, command.name)
	}
	return names
}

func cliHelpText() string {
	lines := []string{"Available commands:"}
	for _, command := range cliCommands {
		lines = append(lines, fmt.Sprintf("  %-24s %s", command.usage, command.description))
	}
	return strings.Join(lines, "\n") + "\n"
}

type cliInputEvent struct {
	rune   rune
	escape bool
	err    error
}

type keyEventWatcher func() (<-chan cliInputEvent, func(), error)

func runCoreChatLoop(client cliCore, reader *bufio.Reader, stdout, stderr io.Writer, watchKeys keyEventWatcher) error {
	ctx := context.Background()
	saved, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	sessionID := saved.ID
	sessionTitle := saved.Title
	workspace := saved.Workspace
	planMode := false
	savedPermissionMode := client.PermissionMode()
	runtime, err := client.OpenSessionRuntime(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("open session runtime: %w", err)
	}
	for {
		if _, err := fmt.Fprint(stdout, "> "); err != nil {
			return fmt.Errorf("write prompt: %w", err)
		}
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("read input: %w", readErr)
		}
		line = strings.TrimSpace(line)
		switch {
		case line == "/help":
			fmt.Fprint(stdout, cliHelpText())
		case line == "/exit" || line == "/quit":
			return nil
		case line == "/clear":
			fmt.Fprint(stdout, "\033[H\033[2J")
		case line == "/new":
			saved, err = client.CreateSession(ctx, json.RawMessage("null"))
			if err != nil {
				fmt.Fprintf(stderr, "error: create session: %v\n", err)
			} else {
				sessionID = saved.ID
				sessionTitle = saved.Title
				workspace = saved.Workspace
				runtime, err = client.OpenSessionRuntime(ctx, sessionID)
				if err != nil {
					fmt.Fprintf(stderr, "error: open session runtime: %v\n", err)
				} else {
					fmt.Fprintln(stdout, "New session.")
				}
			}
		case line == "/plan":
			planMode = !planMode
			if planMode {
				savedPermissionMode = client.PermissionMode()
				_ = client.SetPermissionMode(core.PermissionReadOnly)
				fmt.Fprintln(stdout, "Switched to Plan mode (permissions locked to read-only).")
			} else {
				_ = client.SetPermissionMode(savedPermissionMode)
				fmt.Fprintln(stdout, "Exited Plan mode (permissions restored).")
			}
		case line == "/status":
			handleCoreCLIStatus(client, sessionID, sessionTitle, planMode, stdout)
		case line == "/context":
			handleCoreCLIContext(client, sessionID, sessionTitle, planMode, stdout)
		case line == "/export" || strings.HasPrefix(line, "/export "):
			handleCoreCLIExport(client, sessionID, line, stdout, stderr)
		case line == "/model" || strings.HasPrefix(line, "/model "):
			handleCoreCLIModel(client, sessionID, line, stdout, stderr)
		case line == "/sessions" || strings.HasPrefix(line, "/sessions "):
			handleCoreCLISessions(client, sessionID, line, stdout, stderr)
		case line == "/open" || strings.HasPrefix(line, "/open "):
			newID, newRuntime, openErr := handleCoreCLIOpen(client, sessionID, line, stdout, stderr)
			if openErr != nil {
				fmt.Fprintf(stderr, "error: open session: %v\n", openErr)
			} else if newRuntime != nil {
				sessionID = newID
				runtime = newRuntime
				if s, err := client.GetSession(ctx, sessionID); err == nil {
					sessionTitle = s.Title
					workspace = s.Workspace
				}
			}
		case line == "/title" || strings.HasPrefix(line, "/title "):
			sessionTitle = handleCoreCLITitle(client, sessionID, line, stdout, stderr)
		case line == "/rewind" || strings.HasPrefix(line, "/rewind "):
			handleCoreCLIRewind(client, sessionID, line, stdout, stderr)
		case line == "/diff" || strings.HasPrefix(line, "/diff "):
			targetWorkspace := workspace
			if targetWorkspace == "" {
				targetWorkspace = client.Workspace()
			}
			handleCoreCLIDiff(targetWorkspace, line, stdout, stderr)
		case line == "/review" || strings.HasPrefix(line, "/review "):
			targetWorkspace := workspace
			if targetWorkspace == "" {
				targetWorkspace = client.Workspace()
			}
			handleCoreCLIReview(targetWorkspace, line, stdout, stderr)
		case line == "/mcp" || strings.HasPrefix(line, "/mcp "):
			handleCoreCLIMCP(client, line, stdout, stderr)
		case line == "/fork":
			newSession, err := client.ForkSession(ctx, sessionID)
			if err != nil {
				fmt.Fprintf(stderr, "error: fork session: %v\n", err)
			} else {
				sessionID = newSession.ID
				sessionTitle = newSession.Title
				workspace = newSession.Workspace
				runtime, err = client.OpenSessionRuntime(ctx, sessionID)
				if err != nil {
					fmt.Fprintf(stderr, "error: open session runtime: %v\n", err)
				} else {
					fmt.Fprintf(stdout, "Forked to new session: %s\n", sessionID)
				}
			}
		case line == "/permissions" || strings.HasPrefix(line, "/permissions "):
			handleCoreCLIPermissions(client, planMode, line, stdout, stderr)
		case line == "/discard":
			discarded, err := runtime.DiscardPaused(ctx)
			if err != nil {
				fmt.Fprintf(stderr, "error: discard task: %v\n", err)
			} else if discarded {
				fmt.Fprintln(stdout, "Interrupted task discarded.")
			} else {
				fmt.Fprintln(stdout, "No interrupted task to discard.")
			}
		case line == "/resume":
			state, err := runtime.State(ctx)
			if err != nil {
				fmt.Fprintf(stderr, "error: inspect pending task: %v\n", err)
			} else if state.Task.Status != core.TaskPaused {
				fmt.Fprintln(stdout, "No interrupted task to resume.")
			} else {
				requests, requestErr := runtime.PendingRequests(ctx)
				if requestErr != nil {
					fmt.Fprintf(stderr, "error: inspect task decisions: %v\n", requestErr)
					continue
				}
				if len(requests) > 0 {
					fmt.Fprintf(stderr, "Task is paused for %s. Use the TUI to resolve it.\n", requests[0].Kind)
					continue
				}
				result, callErr, interrupted := respondCoreWithKeys(runtime, workspace, core.TurnRequest{}, true, watchKeys, stderr)
				if err := printCoreTaskResult(stdout, stderr, result.Response, callErr, interrupted); err != nil {
					return err
				}
			}
		case line == "/compact":
			turns, err := client.Compact(ctx, sessionID)
			if err != nil {
				fmt.Fprintf(stderr, "error: compact conversation: %v\n", err)
			} else if turns == 0 {
				fmt.Fprintln(stdout, "No older conversation to compact.")
			} else {
				fmt.Fprintf(stdout, "Compacted %d older user turn(s).\n", turns)
			}
		case line == "":
		case strings.HasPrefix(line, "/"):
			fmt.Fprintf(stderr, "Unknown command %q. Available commands: %s\n", line, strings.Join(cliCommandNames(), " "))
		default:
			state, err := runtime.State(ctx)
			if err != nil {
				fmt.Fprintf(stderr, "error: inspect pending task: %v\n", err)
			} else if state.Task.Status == core.TaskPaused {
				fmt.Fprintln(stderr, "An interrupted task is pending. Use /resume or /discard first.")
			} else if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued {
				fmt.Fprintln(stderr, "A turn is already running or queued in this session.")
			} else {
				input, err := core.ParseTaskInput(line)
				if err != nil {
					fmt.Fprintf(stderr, "error: %v\n", err)
				} else {
					result, callErr, interrupted := respondCoreWithKeys(runtime, workspace, core.TurnRequest{Input: input}, false, watchKeys, stderr)
					if err := printCoreTaskResult(stdout, stderr, result.Response, callErr, interrupted); err != nil {
						return err
					}
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
	}
}

func handleCoreCLIPermissions(client cliCore, planMode bool, line string, stdout, stderr io.Writer) {
	fields := strings.Fields(line)
	if len(fields) == 1 {
		mode := client.PermissionMode()
		fmt.Fprintf(stdout, "Permission mode: %s\nAvailable modes: read-only, workspace, ask, full-access\n", mode)
		return
	}
	if len(fields) != 2 {
		fmt.Fprintln(stderr, "usage: /permissions [read-only|workspace|ask|full-access]")
		return
	}
	if planMode {
		fmt.Fprintln(stderr, "Cannot change permissions while Plan mode is active.")
		return
	}
	mode := core.PermissionMode(fields[1])
	if err := client.SetPermissionMode(mode); err != nil {
		fmt.Fprintln(stderr, err)
		return
	}
	fmt.Fprintf(stdout, "Permission mode set to %s.\n", mode)
}

func handleCoreCLIStatus(client cliCore, sessionID, sessionTitle string, planMode bool, stdout io.Writer) {
	ctx := context.Background()
	workspace := client.Workspace()
	model := client.ModelForSession(ctx, sessionID)
	mode := client.PermissionMode()
	if sessionTitle == "" {
		if s, err := client.GetSession(ctx, sessionID); err == nil && s.Title != "" {
			sessionTitle = s.Title
		} else {
			sessionTitle = "<untitled>"
		}
	}
	fmt.Fprintf(stdout, "Session ID:  %s\n", sessionID)
	fmt.Fprintf(stdout, "Title:       %s\n", sessionTitle)
	fmt.Fprintf(stdout, "Model:       %s\n", model)
	fmt.Fprintf(stdout, "Workspace:   %s\n", workspace)
	fmt.Fprintf(stdout, "Permissions: %s (Plan mode: %v)\n", mode, planMode)
}

func handleCoreCLIModel(client cliCore, sessionID, line string, stdout, stderr io.Writer) {
	fields := strings.Fields(line)
	ctx := context.Background()
	profiles := client.ListModelProfiles()
	if len(fields) == 1 {
		current := client.ModelForSession(ctx, sessionID)
		fmt.Fprintf(stdout, "Current model: %s\n", current)
		if len(profiles) > 0 {
			fmt.Fprintln(stdout, "Available model profiles:")
			for _, p := range profiles {
				fmt.Fprintf(stdout, "  - %-16s (%s / %s)\n", p.Profile, p.Provider, p.Model)
			}
		}
		return
	}
	target := fields[1]
	valid := false
	for _, p := range profiles {
		if strings.EqualFold(p.Profile, target) || strings.EqualFold(p.Model, target) {
			target = p.Profile
			valid = true
			break
		}
	}
	if !valid {
		var names []string
		for _, p := range profiles {
			names = append(names, p.Profile)
		}
		fmt.Fprintf(stderr, "Unknown model profile %q. Available profiles: %s\n", fields[1], strings.Join(names, ", "))
		return
	}
	if err := client.SetSessionSettings(ctx, sessionID, core.SessionSettings{ModelProfile: target}); err != nil {
		fmt.Fprintf(stderr, "error: set model: %v\n", err)
		return
	}
	fmt.Fprintf(stdout, "Active model set to %s.\n", client.ModelForSession(ctx, sessionID))
}

func handleCoreCLISessions(client cliCore, currentSessionID, line string, stdout, stderr io.Writer) {
	fields := strings.Fields(line)
	query := ""
	if len(fields) > 1 {
		query = strings.ToLower(strings.Join(fields[1:], " "))
	}
	ctx := context.Background()
	limit := 20
	if query != "" {
		limit = 100
	}
	sessions, err := client.ListSessions(ctx, limit, false)
	if err != nil {
		fmt.Fprintf(stderr, "error: list sessions: %v\n", err)
		return
	}
	if len(sessions) == 0 {
		fmt.Fprintln(stdout, "No sessions found.")
		return
	}
	var filtered []core.Session
	for _, s := range sessions {
		if query == "" {
			filtered = append(filtered, s)
		} else {
			if strings.Contains(strings.ToLower(s.ID), query) || strings.Contains(strings.ToLower(s.Title), query) {
				filtered = append(filtered, s)
			}
		}
	}
	if len(filtered) == 0 {
		fmt.Fprintf(stdout, "No sessions matching %q.\n", query)
		return
	}
	if query != "" {
		fmt.Fprintf(stdout, "Matching sessions (%d found for %q):\n", len(filtered), query)
	} else {
		fmt.Fprintln(stdout, "Recent sessions:")
	}
	for _, s := range filtered {
		prefix := "  "
		if s.ID == currentSessionID {
			prefix = "> "
		}
		shortID := s.ID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		title := s.Title
		if title == "" {
			title = "<untitled>"
		}
		timeStr := s.UpdatedAt.Format("2006-01-02 15:04:05")
		fmt.Fprintf(stdout, "%s%-14s %-25s %s\n", prefix, shortID, title, timeStr)
	}
}

func handleCoreCLIContext(client cliCore, sessionID, sessionTitle string, planMode bool, stdout io.Writer) {
	ctx := context.Background()
	workspace := client.Workspace()
	model := client.ModelForSession(ctx, sessionID)
	mode := client.PermissionMode()
	if sessionTitle == "" {
		if s, err := client.GetSession(ctx, sessionID); err == nil && s.Title != "" {
			sessionTitle = s.Title
		} else {
			sessionTitle = "<untitled>"
		}
	}

	fmt.Fprintln(stdout, "Context Status & Diagnostics")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Workspace & Permissions:")
	fmt.Fprintf(stdout, "  Directory:   %s\n", workspace)
	permStr := string(mode)
	if planMode {
		permStr += " (Plan mode locked)"
	}
	fmt.Fprintf(stdout, "  Permissions: %s\n", permStr)

	ruleFiles := []string{"AGENTS.md", "CLAUDE.md"}
	var foundRules []string
	if workspace != "" {
		for _, rf := range ruleFiles {
			p := filepath.Join(workspace, rf)
			if fi, err := os.Stat(p); err == nil {
				foundRules = append(foundRules, fmt.Sprintf("%s (%d bytes)", rf, fi.Size()))
			}
		}
	}
	if len(foundRules) > 0 {
		fmt.Fprintf(stdout, "  Rules:       %s\n", strings.Join(foundRules, ", "))
	} else {
		fmt.Fprintln(stdout, "  Rules:       None detected in workspace root")
	}

	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Session & Model:")
	fmt.Fprintf(stdout, "  Model:       %s\n", model)
	fmt.Fprintf(stdout, "  Session ID:  %s\n", sessionID)
	fmt.Fprintf(stdout, "  Title:       %s\n", sessionTitle)

	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Capabilities & Tools:")
	mcpServers := client.MCPServers()
	activeServers := 0
	activeTools := 0
	for _, s := range mcpServers {
		if s.Enabled {
			activeServers++
			activeTools += s.ToolCount
		}
	}
	if activeServers > 0 {
		fmt.Fprintf(stdout, "  MCP Servers: %d active (%d tools registered)\n", activeServers, activeTools)
	} else {
		fmt.Fprintln(stdout, "  MCP Servers: None active")
	}
	if planMode {
		fmt.Fprintln(stdout, "  Plan Mode:   Active (restricted to read-only tools)")
	} else {
		fmt.Fprintln(stdout, "  Plan Mode:   Inactive (all permitted tools available)")
	}

	// Session token usage
	if s, err := client.GetSession(ctx, sessionID); err == nil {
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "Token Usage:")
		fmt.Fprintf(stdout, "  Input:       %d (cached: %d)\n", s.SessionUsage.Input, s.SessionUsage.Cached)
		fmt.Fprintf(stdout, "  Output:      %d (reasoning: %d)\n", s.SessionUsage.Output, s.SessionUsage.Reasoning)
	}
}

func handleCoreCLIExport(client cliCore, sessionID, line string, stdout, stderr io.Writer) {
	ctx := context.Background()
	saved, err := client.GetSession(ctx, sessionID)
	if err != nil {
		fmt.Fprintf(stderr, "error: get session: %v\n", err)
		return
	}
	fields := strings.Fields(line)
	targetPath := ""
	if len(fields) > 1 {
		targetPath = strings.TrimSpace(strings.Join(fields[1:], " "))
	}
	if targetPath == "" {
		shortID := sessionID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		if shortID == "" {
			shortID = "current"
		}
		targetPath = fmt.Sprintf("export-%s-%s.md", shortID, time.Now().Format("20060102-150405"))
	}
	workspace := client.Workspace()
	if !filepath.IsAbs(targetPath) && workspace != "" {
		targetPath = filepath.Join(workspace, targetPath)
	}

	title := saved.Title
	if title == "" {
		title = "<untitled session>"
	}
	model := client.ModelForSession(ctx, sessionID)
	if model == "" {
		model = "<default>"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Session Transcript: %s\n\n", title))
	sb.WriteString(fmt.Sprintf("- **Session ID:** `%s`\n", sessionID))
	sb.WriteString(fmt.Sprintf("- **Workspace:** `%s`\n", workspace))
	sb.WriteString(fmt.Sprintf("- **Model:** `%s`\n", model))
	sb.WriteString(fmt.Sprintf("- **Exported At:** %s\n", time.Now().Format(time.RFC3339)))
	if saved.SessionUsage.Input > 0 || saved.SessionUsage.Output > 0 {
		sb.WriteString(fmt.Sprintf("- **Token Usage:** %d in (cached: %d) · %d out (reasoning: %d)\n",
			saved.SessionUsage.Input, saved.SessionUsage.Cached,
			saved.SessionUsage.Output, saved.SessionUsage.Reasoning))
	}
	sb.WriteString("\n---\n\n")

	entryCount := 0
	if len(saved.History) > 0 && string(saved.History) != "null" {
		var items []json.RawMessage
		if json.Unmarshal(saved.History, &items) == nil {
			for _, item := range items {
				var val struct {
					Type      string          `json:"type"`
					Role      string          `json:"role"`
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
					Content   json.RawMessage `json:"content"`
				}
				if json.Unmarshal(item, &val) != nil {
					continue
				}
				switch val.Type {
				case "message":
					text := cliExtractMessageContent(val.Content)
					if text == "" {
						continue
					}
					entryCount++
					role := "User"
					if val.Role == "assistant" {
						role = "Assistant"
					}
					sb.WriteString(fmt.Sprintf("### %s\n\n%s\n\n", role, text))
				case "function_call":
					entryCount++
					args := string(val.Arguments)
					if len(args) > 0 && args[0] == '"' {
						var unquoted string
						if json.Unmarshal(val.Arguments, &unquoted) == nil {
							args = unquoted
						}
					}
					sb.WriteString(fmt.Sprintf("<details>\n<summary>Tool: <code>%s</code></summary>\n\n", val.Name))
					if args != "" && args != "null" {
						sb.WriteString(fmt.Sprintf("**Arguments:**\n```json\n%s\n```\n\n", args))
					}
					sb.WriteString("</details>\n\n")
				}
			}
		}
	}

	content := []byte(sb.String())
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		fmt.Fprintf(stderr, "error: create directory: %v\n", err)
		return
	}
	if err := os.WriteFile(targetPath, content, 0644); err != nil {
		fmt.Fprintf(stderr, "error: write file: %v\n", err)
		return
	}
	fmt.Fprintf(stdout, "Exported session transcript to %s (%d bytes, %d items).\n", targetPath, len(content), entryCount)
}

func cliExtractMessageContent(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var parts []json.RawMessage
	if json.Unmarshal(content, &parts) != nil {
		return ""
	}
	var out strings.Builder
	for _, raw := range parts {
		var part struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &part) == nil && (part.Type == "input_text" || part.Type == "output_text") {
			out.WriteString(part.Text)
		}
	}
	return out.String()
}

func handleCoreCLIOpen(client cliCore, currentSessionID, line string, stdout, stderr io.Writer) (string, cliRuntime, error) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		fmt.Fprintln(stderr, "usage: /open <session-id|prefix>")
		return currentSessionID, nil, nil
	}
	targetPrefix := fields[1]
	ctx := context.Background()
	targetID := ""
	if _, err := client.GetSession(ctx, targetPrefix); err == nil {
		targetID = targetPrefix
	} else {
		sessions, err := client.ListSessions(ctx, 100, false)
		if err != nil {
			fmt.Fprintf(stderr, "error: query sessions: %v\n", err)
			return currentSessionID, nil, nil
		}
		var matches []core.Session
		for _, s := range sessions {
			if strings.HasPrefix(strings.ToLower(s.ID), strings.ToLower(targetPrefix)) {
				matches = append(matches, s)
			}
		}
		if len(matches) == 0 {
			fmt.Fprintf(stderr, "No session found matching prefix %q.\n", targetPrefix)
			return currentSessionID, nil, nil
		} else if len(matches) > 1 {
			var ids []string
			for _, match := range matches {
				short := match.ID
				if len(short) > 12 {
					short = short[:12]
				}
				ids = append(ids, short)
			}
			fmt.Fprintf(stderr, "Prefix %q is ambiguous. Matches: %s\n", targetPrefix, strings.Join(ids, ", "))
			return currentSessionID, nil, nil
		}
		targetID = matches[0].ID
	}
	if targetID == currentSessionID {
		fmt.Fprintf(stdout, "Already in session %s.\n", targetID)
		return currentSessionID, nil, nil
	}
	rt, err := client.OpenSessionRuntime(ctx, targetID)
	if err != nil {
		fmt.Fprintf(stderr, "error: open session runtime: %v\n", err)
		return currentSessionID, nil, nil
	}
	fmt.Fprintf(stdout, "Switched to session %s.\n", targetID)
	return targetID, rt, nil
}

func handleCoreCLITitle(client cliCore, sessionID, line string, stdout, stderr io.Writer) string {
	fields := strings.Fields(line)
	ctx := context.Background()
	if len(fields) == 1 {
		title := "<untitled>"
		if s, err := client.GetSession(ctx, sessionID); err == nil && s.Title != "" {
			title = s.Title
		}
		fmt.Fprintf(stdout, "Current session title: %s (ID: %s)\n", title, sessionID)
		return title
	}
	newTitle := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
	if err := client.SetSessionTitle(ctx, sessionID, newTitle); err != nil {
		fmt.Fprintf(stderr, "error: set session title: %v\n", err)
		return ""
	}
	fmt.Fprintf(stdout, "Session title set to %q.\n", newTitle)
	return newTitle
}

func handleCoreCLIRewind(client cliCore, sessionID, line string, stdout, stderr io.Writer) {
	fields := strings.Fields(line)
	turns := 1
	if len(fields) > 1 {
		n, err := strconv.Atoi(fields[1])
		if err != nil || n <= 0 {
			fmt.Fprintf(stderr, "usage: /rewind [N] (N must be a positive integer)\n")
			return
		}
		turns = n
	}
	ctx := context.Background()
	removed, err := client.RewindSession(ctx, sessionID, turns)
	if err != nil {
		fmt.Fprintf(stderr, "error: rewind session: %v\n", err)
		return
	}
	fmt.Fprintf(stdout, "Rewound %d turn(s).\n", removed)
}

func handleCoreCLIDiff(workspace, line string, stdout, stderr io.Writer) {
	fields := strings.Fields(line)
	args := []string{"diff"}
	if len(fields) > 1 {
		args = append(args, "--")
		args = append(args, fields[1:]...)
	}
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = workspace
	out, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(stderr, "error: git diff: %v\n", err)
		return
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		fmt.Fprintln(stdout, "No unstaged or uncommitted changes in workspace.")
		return
	}
	fmt.Fprintln(stdout, trimmed)
}

func handleCoreCLIReview(workspace, line string, stdout, stderr io.Writer) {
	fields := strings.Fields(line)
	if len(fields) > 1 {
		handleCoreCLIDiff(workspace, line, stdout, stderr)
		return
	}
	statusCmd := exec.CommandContext(context.Background(), "git", "status", "--short")
	statusCmd.Dir = workspace
	statusOut, err := statusCmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(stderr, "error: git status: %v\n", err)
		return
	}
	statusText := strings.TrimSpace(string(statusOut))
	if statusText == "" {
		fmt.Fprintln(stdout, "No unstaged or uncommitted changes in workspace.")
		return
	}
	diffStatCmd := exec.CommandContext(context.Background(), "git", "diff", "--stat")
	diffStatCmd.Dir = workspace
	diffStatOut, _ := diffStatCmd.CombinedOutput()
	statText := strings.TrimSpace(string(diffStatOut))

	fmt.Fprintln(stdout, "Workspace Changes Summary:")
	fmt.Fprintln(stdout, statusText)
	if statText != "" {
		fmt.Fprintln(stdout, "---")
		fmt.Fprintln(stdout, statText)
	}
	fmt.Fprintln(stdout, "\nTip: Use /review <path> or /diff <path> to view full diff for a specific file.")
}

func handleCoreCLIMCP(client cliCore, line string, stdout, stderr io.Writer) {
	fields := strings.Fields(line)
	subcommand := "list"
	if len(fields) > 1 {
		subcommand = strings.ToLower(fields[1])
	}
	ctx := context.Background()
	switch subcommand {
	case "list":
		servers := client.MCPServers()
		if len(servers) == 0 {
			fmt.Fprintln(stdout, "No MCP servers configured.")
			return
		}
		fmt.Fprintln(stdout, "MCP Servers:")
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
			errInfo := ""
			if s.LastError != "" {
				errInfo = fmt.Sprintf(" (Error: %s)", s.LastError)
			}
			fmt.Fprintf(stdout, "  - %-16s [%s, %s] %s (%d tools)%s\n", s.Name, status, conn, endpoint, s.ToolCount, errInfo)
		}
	case "enable":
		if len(fields) < 3 {
			fmt.Fprintln(stderr, "usage: /mcp enable <server-name>")
			return
		}
		name := fields[2]
		if err := client.EnableMCPServer(name); err != nil {
			fmt.Fprintf(stderr, "error: enable MCP server %q: %v\n", name, err)
			return
		}
		fmt.Fprintf(stdout, "MCP server %q enabled.\n", name)
	case "disable":
		if len(fields) < 3 {
			fmt.Fprintln(stderr, "usage: /mcp disable <server-name>")
			return
		}
		name := fields[2]
		if err := client.DisableMCPServer(name); err != nil {
			fmt.Fprintf(stderr, "error: disable MCP server %q: %v\n", name, err)
			return
		}
		fmt.Fprintf(stdout, "MCP server %q disabled.\n", name)
	case "auth":
		if len(fields) < 3 {
			fmt.Fprintln(stderr, "usage: /mcp auth <server-name>")
			return
		}
		name := fields[2]
		if err := client.AuthenticateMCPServer(ctx, name); err != nil {
			fmt.Fprintf(stderr, "error: authenticate MCP server %q: %v\n", name, err)
			return
		}
		fmt.Fprintf(stdout, "MCP server %q authentication completed.\n", name)
	default:
		fmt.Fprintf(stderr, "Unknown MCP subcommand %q. Usage: /mcp [list|enable|disable|auth]\n", subcommand)
	}
}

func respondCoreWithKeys(runtime cliRuntime, workspace string, request core.TurnRequest, resume bool, watchKeys keyEventWatcher, stderr io.Writer) (core.TurnResult, error, bool) {
	var keyEvents <-chan cliInputEvent
	stopWatching := func() {}
	if watchKeys != nil {
		var err error
		keyEvents, stopWatching, err = watchKeys()
		if stopWatching == nil {
			stopWatching = func() {}
		}
		if err != nil {
			fmt.Fprintf(stderr, "warning: keyboard input unavailable: %v\n", err)
			keyEvents = nil
		}
	}
	defer stopWatching()

	streamCtx, stopStream := context.WithCancel(context.Background())
	defer stopStream()
	events, err := runtime.SubscribeEvents(streamCtx, 0)
	if err != nil {
		return core.TurnResult{}, err, false
	}
	runtime.SetUserInteractionAvailable(false)
	request.NoInteractiveDecisions = keyEvents == nil
	var submission core.TurnSubmission
	if resume {
		submission, err = runtime.Recover(context.Background())
	} else {
		submission, err = runtime.SubmitTurn(context.Background(), request, core.StartIfIdle)
	}
	if err != nil {
		return core.TurnResult{}, err, false
	}
	if keyEvents != nil {
		if _, err := fmt.Fprintln(stderr, "Working... Press Esc to interrupt."); err != nil {
			runtime.Interrupt(submission.ExecutionID)
			return core.TurnResult{}, err, true
		}
	}
	type waitResult struct {
		result core.TurnResult
		err    error
	}
	done := make(chan waitResult, 1)
	go func() {
		result, waitErr := runtime.WaitTurn(context.Background(), submission.ExecutionID)
		done <- waitResult{result: result, err: waitErr}
	}()
	interrupted := false
	for {
		select {
		case result := <-done:
			if result.result.Status == core.TaskPaused && result.err == nil {
				result.err = core.ErrTaskPaused
			}
			return result.result, result.err, interrupted
		case event, ok := <-events:
			if !ok {
				return core.TurnResult{}, errors.New("session event stream closed"), interrupted
			}
			if event.ExecutionID != "" && event.ExecutionID != submission.ExecutionID {
				continue
			}
			switch event.Kind {
			case core.EventContextCompaction:
				if event.Compaction != nil {
					writeCLICompactionStatus(stderr, *event.Compaction)
				}
			case core.EventApprovalRequired:
				if event.Approval != nil {
					if keyEvents == nil {
						if err := runtime.ResolveApproval(event.Approval.ID, false); err != nil {
							fmt.Fprintf(stderr, "error: deny approval: %v\n", err)
							runtime.Interrupt(submission.ExecutionID)
						} else {
							fmt.Fprintf(stderr, "Denied %s because interactive keyboard input is unavailable.\n", cliApprovalSummary(*event.Approval, workspace))
						}
					} else if err := resolveCLIApproval(runtime, *event.Approval, workspace, keyEvents, stderr); err != nil {
						runtime.Interrupt(submission.ExecutionID)
					}
				}
			case core.EventUserQuestionRequired, core.EventUserInteractionRequired,
				core.EventToolRecoveryRequired, core.EventModelRecoveryRequired, core.EventExecutionBlocked:
				fmt.Fprintf(stderr, "Task paused for %s. Use /resume to inspect it.\n", event.Kind)
			}
		case key, ok := <-keyEvents:
			if !ok || key.err != nil {
				keyEvents = nil
				fmt.Fprintln(stderr, "warning: keyboard input became unavailable; future approvals will be denied")
				continue
			}
			if key.escape {
				interrupted = runtime.Interrupt(submission.ExecutionID)
				if interrupted {
					slog.Warn("task interrupted by user", "source", "escape")
				}
			}
		}
	}
}

func writeCLICompactionStatus(stderr io.Writer, event core.ContextCompactionDetails) {
	switch event.Stage {
	case "started":
		_, _ = fmt.Fprintln(stderr, "Compacting context...")
	case "completed":
		if event.AfterTokens > 0 {
			_, _ = fmt.Fprintf(stderr, "Context compacted (estimated input %s to %s tokens).\n", compactCLITokens(event.BeforeTokens), compactCLITokens(event.AfterTokens))
		} else {
			_, _ = fmt.Fprintln(stderr, "Provider compacted the context.")
		}
	case "fallback":
		_, _ = fmt.Fprintf(stderr, "Server compaction unavailable; using local compaction: %s\n", event.Err)
	case "skipped":
		if event.Err != "" {
			_, _ = fmt.Fprintf(stderr, "Automatic compaction skipped: %s\n", event.Err)
		}
	case "failed":
		_, _ = fmt.Fprintf(stderr, "Context compaction failed: %s\n", event.Err)
	}
}

func compactCLITokens(tokens int64) string {
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

func resolveCLIApproval(runtime cliRuntime, request core.ApprovalRequest, workspace string, keys <-chan cliInputEvent, stderr io.Writer) error {
	if _, err := fmt.Fprintln(stderr, formatCLIToolApproval(request, workspace)); err != nil {
		return err
	}
	for key := range keys {
		if key.err != nil {
			err := runtime.ResolveApproval(request.ID, false)
			if err == nil {
				fmt.Fprintf(stderr, "Denied %s because interactive keyboard input is unavailable.\n", cliApprovalSummary(request, workspace))
			} else {
				fmt.Fprintf(stderr, "error: resolve approval: %v\n", err)
			}
			return err
		}
		if key.escape {
			return errors.New("approval interrupted")
		}
		var allowed bool
		switch unicode.ToLower(key.rune) {
		case 'y':
			allowed = true
		case 'n':
			allowed = false
		default:
			continue
		}
		if err := runtime.ResolveApproval(request.ID, allowed); err != nil {
			fmt.Fprintf(stderr, "error: resolve approval: %v\n", err)
			return fmt.Errorf("resolve approval: %w", err)
		}
		if allowed {
			_, _ = fmt.Fprintln(stderr, "Approved this tool call once.")
		} else {
			_, _ = fmt.Fprintln(stderr, "Denied this tool call.")
		}
		return nil
	}
	err := runtime.ResolveApproval(request.ID, false)
	if err == nil {
		fmt.Fprintf(stderr, "Denied %s because interactive keyboard input became unavailable.\n", cliApprovalSummary(request, workspace))
	} else {
		fmt.Fprintf(stderr, "error: resolve approval: %v\n", err)
	}
	return err
}

func formatCLIToolApproval(request core.ApprovalRequest, workspace string) string {
	details := ""
	warning := ""
	if request.Tool == "command" {
		var args map[string]json.RawMessage
		_ = json.Unmarshal(request.Arguments, &args)
		var command string
		_ = json.Unmarshal(args["command"], &command)
		details = "\nCommand: " + cleanCLIInlineText(command)
		warning = "\nCore runs commands inside the operating system sandbox."
	}
	return fmt.Sprintf("Allow %s once?%s%s\nPress Y to approve once, N to deny, or Esc to interrupt the task.",
		cliApprovalSummary(request, workspace), details, warning)
}

func cliApprovalSummary(request core.ApprovalRequest, workspace string) string {
	var args map[string]json.RawMessage
	_ = json.Unmarshal(request.Arguments, &args)
	var pathValue, rawURL string
	_ = json.Unmarshal(args["path"], &pathValue)
	_ = json.Unmarshal(args["url"], &rawURL)
	path := cliApprovalPath(pathValue, workspace)
	summary := "Working on a tool request"
	switch request.Tool {
	case "read_file":
		summary = cliApprovalPathSummary("Reading", path)
	case "write_file":
		summary = cliApprovalPathSummary("Writing", path)
	case "edit_file":
		summary = cliApprovalPathSummary("Editing", path)
	case "list_dir":
		summary = cliApprovalPathSummary("Listing", path)
	case "glob":
		summary = "Finding project files"
	case "grep":
		if path == "" {
			summary = "Searching project files"
		} else {
			summary = "Searching " + path
		}
	case "web_search":
		summary = "Searching the web"
	case "web_fetch":
		if parsed, err := url.Parse(rawURL); err == nil && parsed.Hostname() != "" {
			summary = "Fetching " + parsed.Hostname()
		} else {
			summary = "Fetching a web page"
		}
	case "command":
		summary = "Running a shell command"
	default:
		if request.Tool != "" {
			summary = "Tool " + request.Tool
		}
	}
	// Tool arguments and extension tool names may originate from model output.
	// Keep their display on one terminal line and prevent ANSI control sequences.
	return cleanCLIInlineText(summary)
}

func cliApprovalPath(value, workspace string) string {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) {
		return ""
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return ""
	}
	if workspace == "" {
		return filepath.ToSlash(clean)
	}
	absolute := filepath.Clean(filepath.Join(workspace, clean))
	relative, err := filepath.Rel(workspace, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(clean)
	}
	return filepath.ToSlash(absolute)
}

func cliApprovalPathSummary(action, path string) string {
	if path == "" {
		return action + " a workspace file"
	}
	return action + " " + path
}

func cleanCLIInlineText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\\r\\n")
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, "\r", "\\r")
	value = strings.ReplaceAll(value, "\t", "\\t")
	return strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return -1
		}
		return char
	}, value)
}

func printCoreTaskResult(stdout, stderr io.Writer, response json.RawMessage, callErr error, interrupted bool) error {
	if len(response) > 0 {
		text, err := coreResponseText(response)
		if err != nil {
			return err
		}
		if text != "" {
			if _, err := fmt.Fprintln(stdout, text); err != nil {
				return err
			}
		}
	}
	if interrupted {
		if _, err := fmt.Fprintln(stdout, "Task interrupted. Use /resume to continue, or /discard to abandon."); err != nil {
			return err
		}
	}
	if callErr != nil && !(interrupted && errors.Is(callErr, context.Canceled)) {
		if _, err := fmt.Fprintf(stderr, "error: %v\n", callErr); err != nil {
			return err
		}
	}
	return nil
}

func coreResponseText(response json.RawMessage) (string, error) {
	var envelope struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return "", fmt.Errorf("decode response output: %w", err)
	}
	var lines []string
	for _, item := range envelope.Output {
		var value struct {
			Type    string          `json:"type"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(item, &value); err != nil {
			return "", err
		}
		if value.Type == "reasoning" || value.Type == "function_call" || value.Type == "custom_tool_call" {
			continue
		}
		if value.Type != "message" {
			lines = append(lines, string(item))
			continue
		}
		var parts []json.RawMessage
		if json.Unmarshal(value.Content, &parts) != nil {
			var text string
			if json.Unmarshal(value.Content, &text) == nil {
				lines = append(lines, text)
			}
			continue
		}
		var text strings.Builder
		for _, raw := range parts {
			var part struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			_ = json.Unmarshal(raw, &part)
			if part.Type == "output_text" {
				text.WriteString(part.Text)
			}
		}
		if text.Len() > 0 {
			lines = append(lines, text.String())
		}
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}
