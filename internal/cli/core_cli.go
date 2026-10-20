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

	"github.com/afterlune/lune/internal/workspacereview"
	core "github.com/afterlune/luneCore"
	"github.com/afterlune/luneCore/memory"
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
	ResolveUserQuestion(context.Context, core.UserQuestionAnswer) error
	ResolveToolRecovery(context.Context, string, core.ToolRecoveryDecision) error
	SetUserInteractionAvailable(bool)
	ListCommands(context.Context) ([]core.CommandInfo, error)
	ReadCommand(context.Context, string, core.CommandReadOptions) (core.CommandOutput, error)
	StopCommand(context.Context, string) (core.CommandOutput, error)
}

type cliCore interface {
	Workspace() string
	LatestSession(context.Context) (core.Session, bool, error)
	CreateSession(context.Context, json.RawMessage) (core.Session, error)
	GetSession(context.Context, string) (core.Session, error)
	ListSessions(context.Context, int, bool) ([]core.Session, error)
	ForkSession(context.Context, string) (core.Session, error)
	DeleteSession(context.Context, string) error
	RewindSession(context.Context, string, int) (int, error)
	SetSessionTitle(context.Context, string, string) error
	OpenSessionRuntime(context.Context, string) (cliRuntime, error)
	Compact(context.Context, string) (int, error)
	PermissionMode() core.PermissionMode
	SetPermissionMode(core.PermissionMode) error
	ProjectContextDefaults() core.ContextDefaults
	ListModelProfiles() []core.ModelOption
	ModelForSession(context.Context, string) string
	SetSessionSettings(context.Context, string, core.SessionSettings) error
	MCPServers() []core.MCPServerStatus
	EnableMCPServer(string) error
	DisableMCPServer(string) error
	AuthenticateMCPServer(context.Context, string) error
	ListMemories(context.Context, int) ([]memory.Item, error)
	SearchMemories(context.Context, string, int) ([]memory.SearchResult, error)
	AddMemory(context.Context, memory.Input) (memory.Item, error)
	DeleteMemory(context.Context, string) error
	ClearMemories(context.Context) error
}

type cliCommandHelp struct {
	name        string
	usage       string
	description string
}

func applyCLISessionModelProfile(ctx context.Context, client cliCore, sessionID, profile string) error {
	saved, err := client.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	settings := saved.Settings
	settings.ModelProfile = profile
	return client.SetSessionSettings(ctx, sessionID, settings)
}

var cliCommands = []cliCommandHelp{
	{name: "/clear", usage: "/clear", description: "Clear terminal screen."},
	{name: "/compact", usage: "/compact", description: "Compact older conversation history."},
	{name: "/context", usage: "/context", description: "Show context window budget, system rules, and capabilities."},
	{name: "/detail", usage: "/detail [index]", description: "View full output of a tool call."},
	{name: "/diff", usage: "/diff [path]", description: "Inspect git code changes in workspace."},
	{name: "/discard", usage: "/discard", description: "Discard the paused task in this session."},
	{name: "/drop", usage: "/drop [id|prefix]", description: "Delete a saved session."},
	{name: "/exit", usage: "/exit", description: "Exit the chat."},
	{name: "/export", usage: "/export [path]", description: "Export session conversation transcript to a markdown file."},
	{name: "/fork", usage: "/fork", description: "Fork the current session."},
	{name: "/help", usage: "/help", description: "Show available commands."},
	{name: "/logs", usage: "/logs [id|prefix] [--tail N]", description: "View logs of a background process."},
	{name: "/mcp", usage: "/mcp [list|enable|disable|auth]", description: "Manage external MCP servers."},
	{name: "/memory", usage: "/memory [list|add|remove|clear]", description: "View and manage workspace long-term memories."},
	{name: "/model", usage: "/model [profile]", description: "View or change active model profile."},
	{name: "/new", usage: "/new", description: "Start a new session."},
	{name: "/open", usage: "/open [id|prefix]", description: "Switch to an existing session."},
	{name: "/permissions", usage: "/permissions [mode]", description: "Show or set mode: read-only, workspace, ask, full-access."},
	{name: "/plan", usage: "/plan", description: "Toggle Plan mode (read-only exploration)."},
	{name: "/ps", usage: "/ps", description: "List background processes and daemon commands."},
	{name: "/resume", usage: "/resume", description: "Resume the paused task in this session."},
	{name: "/review", usage: "/review [path]", description: "Run a read-only model review of workspace changes in this session."},
	{name: "/rewind", usage: "/rewind [N]", description: "Rewind conversation to a previous turn."},
	{name: "/sessions", usage: "/sessions [query]", description: "List or search recent sessions in this workspace."},
	{name: "/status", usage: "/status", description: "Show session, model, and token status."},
	{name: "/stop", usage: "/stop [id|all]", description: "Stop running background processes."},
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
	return runCoreChatLoopWithOptions(client, reader, stdout, stderr, watchKeys, LaunchOptions{})
}

func runCoreChatLoopWithOptions(client cliCore, reader *bufio.Reader, stdout, stderr io.Writer, watchKeys keyEventWatcher, opts LaunchOptions) error {
	ctx := context.Background()

	var sessionID string
	var sessionTitle string
	var workspace string

	if opts.SessionID != "" {
		targetID := ""
		if s, err := client.GetSession(ctx, opts.SessionID); err == nil {
			targetID = s.ID
			sessionTitle = s.Title
			workspace = s.Workspace
		} else {
			sessions, err := client.ListSessions(ctx, 100, false)
			if err != nil {
				return fmt.Errorf("list sessions: %w", err)
			}
			var matches []core.Session
			for _, s := range sessions {
				if strings.HasPrefix(strings.ToLower(s.ID), strings.ToLower(opts.SessionID)) {
					matches = append(matches, s)
				}
			}
			if len(matches) == 0 {
				return fmt.Errorf("session not found: %q", opts.SessionID)
			}
			if len(matches) > 1 {
				return fmt.Errorf("session prefix %q is ambiguous (%d matches)", opts.SessionID, len(matches))
			}
			targetID = matches[0].ID
			sessionTitle = matches[0].Title
			workspace = matches[0].Workspace
		}
		sessionID = targetID
	} else {
		saved, err := client.CreateSession(ctx, json.RawMessage("null"))
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		sessionID = saved.ID
		sessionTitle = saved.Title
		workspace = saved.Workspace
	}

	// Apply model profile if provided
	if opts.ModelProfile != "" {
		if err := applyCLISessionModelProfile(ctx, client, sessionID, opts.ModelProfile); err != nil {
			return fmt.Errorf("set model profile: %w", err)
		}
	}

	// Apply permission mode if provided
	savedPermissionMode := client.PermissionMode()
	if opts.PermissionMode != "" {
		if err := client.SetPermissionMode(opts.PermissionMode); err != nil {
			return fmt.Errorf("set permission mode: %w", err)
		}
		savedPermissionMode = opts.PermissionMode
	}

	planMode := opts.PlanMode
	if planMode {
		if err := client.SetPermissionMode(core.PermissionReadOnly); err != nil {
			return fmt.Errorf("set Plan mode permissions: %w", err)
		}
	}

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
		case line == "/exit":
			return nil
		case line == "/clear":
			fmt.Fprint(stdout, "\033[H\033[2J")
		case line == "/new":
			created, err := client.CreateSession(ctx, json.RawMessage("null"))
			if err != nil {
				fmt.Fprintf(stderr, "error: create session: %v\n", err)
			} else {
				newRuntime, openErr := client.OpenSessionRuntime(ctx, created.ID)
				if openErr != nil {
					fmt.Fprintf(stderr, "error: open session runtime: %v\n", openErr)
					if cleanupErr := client.DeleteSession(ctx, created.ID); cleanupErr != nil {
						fmt.Fprintf(stderr, "error: remove unused session %s: %v\n", created.ID, cleanupErr)
					}
				} else {
					sessionID, sessionTitle, workspace, runtime = created.ID, created.Title, created.Workspace, newRuntime
					fmt.Fprintln(stdout, "New session.")
				}
			}
		case line == "/plan":
			if !planMode {
				previousMode := client.PermissionMode()
				if err := client.SetPermissionMode(core.PermissionReadOnly); err != nil {
					fmt.Fprintf(stderr, "error: enter Plan mode: %v\n", err)
					continue
				}
				savedPermissionMode = previousMode
				planMode = true
				fmt.Fprintln(stdout, "Switched to Plan mode (permissions locked to read-only).")
			} else {
				if err := client.SetPermissionMode(savedPermissionMode); err != nil {
					fmt.Fprintf(stderr, "error: exit Plan mode: %v\n", err)
					continue
				}
				planMode = false
				fmt.Fprintln(stdout, "Exited Plan mode (permissions restored).")
			}
		case line == "/status":
			handleCoreCLIStatus(client, sessionID, sessionTitle, planMode, stdout)
		case line == "/context":
			handleCoreCLIContext(client, sessionID, sessionTitle, planMode, stdout)
		case line == "/memory" || strings.HasPrefix(line, "/memory "):
			handleCoreCLIMemory(client, line, stdout, stderr)
		case line == "/ps" || strings.HasPrefix(line, "/ps "):
			handleCoreCLIPS(runtime, stdout, stderr)
		case line == "/stop" || strings.HasPrefix(line, "/stop "):
			handleCoreCLIStop(runtime, line, stdout, stderr)
		case line == "/logs" || strings.HasPrefix(line, "/logs "):
			handleCoreCLILogs(runtime, line, stdout, stderr)
		case line == "/export" || strings.HasPrefix(line, "/export "):
			handleCoreCLIExport(client, sessionID, line, stdout, stderr)
		case line == "/detail" || strings.HasPrefix(line, "/detail "):
			handleCoreCLIDetail(client, runtime, sessionID, line, stdout, stderr)
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
		case line == "/drop" || strings.HasPrefix(line, "/drop "):
			handleCoreCLIDrop(client, sessionID, line, stdout, stderr)
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
			if err := handleCoreCLIReview(runtime, targetWorkspace, line, watchKeys, stdout, stderr); err != nil {
				return err
			}
		case line == "/mcp" || strings.HasPrefix(line, "/mcp "):
			handleCoreCLIMCP(client, line, stdout, stderr)
		case line == "/fork":
			newSession, err := client.ForkSession(ctx, sessionID)
			if err != nil {
				fmt.Fprintf(stderr, "error: fork session: %v\n", err)
			} else {
				newRuntime, openErr := client.OpenSessionRuntime(ctx, newSession.ID)
				if openErr != nil {
					fmt.Fprintf(stderr, "error: open session runtime: %v\n", openErr)
					if cleanupErr := client.DeleteSession(ctx, newSession.ID); cleanupErr != nil {
						fmt.Fprintf(stderr, "error: remove unused fork %s: %v\n", newSession.ID, cleanupErr)
					}
				} else {
					sessionID, sessionTitle, workspace, runtime = newSession.ID, newSession.Title, newSession.Workspace, newRuntime
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
				result, callErr, interrupted := resumePausedTaskCLI(runtime, reader, workspace, requests, watchKeys, stdout, stderr)
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
	if saved, err := client.GetSession(ctx, sessionID); err == nil {
		usage := saved.SessionUsage
		fmt.Fprintf(stdout, "Session tokens: %d input (%d cached), %d output; %d model requests\n", usage.Input, usage.Cached, usage.Output, usage.Requests)
	}
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
	if err := applyCLISessionModelProfile(ctx, client, sessionID, target); err != nil {
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

	// Session token usage and the last model request's context budget.
	if s, err := client.GetSession(ctx, sessionID); err == nil {
		contextWindow := configuredContextWindow(client, s, model)
		if contextWindow > 0 {
			fmt.Fprintf(stdout, "\nContext Window:\n  Configured:  %s tokens\n", compactCLITokens(contextWindow))
		} else {
			fmt.Fprintln(stdout, "\nContext Window:\n  Configured:  unavailable")
		}
		if last, ok := latestMainUsage(s); ok {
			input := last.Usage.InputTokens
			if input == 0 && last.EstimatedInputTokens > 0 {
				input = last.EstimatedInputTokens
				fmt.Fprintf(stdout, "  Last prompt: %s input tokens (estimated)\n", compactCLITokens(input))
			} else {
				fmt.Fprintf(stdout, "  Last prompt: %s input tokens (%s cached)\n", compactCLITokens(input), compactCLITokens(last.Usage.InputTokensDetails.CachedTokens))
			}
		}
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "Token Usage:")
		fmt.Fprintf(stdout, "  Input:       %d (cached: %d)\n", s.SessionUsage.Input, s.SessionUsage.Cached)
		fmt.Fprintf(stdout, "  Output:      %d (reasoning: %d)\n", s.SessionUsage.Output, s.SessionUsage.Reasoning)
	}

	if items, err := client.ListMemories(ctx, 100); err == nil {
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "Workspace Memory:")
		if len(items) > 0 {
			fmt.Fprintf(stdout, "  Stored:      %d active items (use '/memory' to view or manage)\n", len(items))
		} else {
			fmt.Fprintln(stdout, "  Stored:      None yet (use '/memory add <text>' to record conventions)")
		}
	}
}

func configuredContextWindow(client cliCore, saved core.Session, model string) int64 {
	if saved.Settings.ContextWindow != nil {
		return *saved.Settings.ContextWindow
	}
	if configured := client.ProjectContextDefaults().ContextWindow; configured > 0 {
		return configured
	}
	for _, profile := range client.ListModelProfiles() {
		if profile.Profile == saved.Settings.ModelProfile || profile.Model == model {
			if profile.ContextWindow > 0 {
				return profile.ContextWindow
			}
		}
	}
	if last, ok := latestMainUsage(saved); ok {
		return last.ContextWindow
	}
	return 0
}

func latestMainUsage(saved core.Session) (core.UsageRecord, bool) {
	for i := len(saved.UsageRequests) - 1; i >= 0; i-- {
		request := saved.UsageRequests[i]
		if request.Purpose == "" || request.Purpose == core.UsagePurposeMain {
			if request.AgentID == "" || request.AgentID == "lead" {
				return request, true
			}
		}
	}
	return core.UsageRecord{}, false
}

func handleCoreCLIMemory(client cliCore, line string, stdout, stderr io.Writer) {
	ctx := context.Background()
	parts := strings.Fields(line)
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
			results, err := client.SearchMemories(ctx, query, 20)
			if err != nil {
				fmt.Fprintf(stderr, "error: search memories: %v\n", err)
				return
			}
			if len(results) == 0 {
				fmt.Fprintf(stdout, "No workspace memories matching %q found.\n", query)
				return
			}
			fmt.Fprintf(stdout, "Workspace Memories (%d matches for %q):\n", len(results), query)
			for _, r := range results {
				shortID := r.Item.ID
				if len(shortID) > 8 {
					shortID = shortID[:8]
				}
				timeStr := r.Item.UpdatedAt.Format("Jan 02 15:04")
				fmt.Fprintf(stdout, "  [%s] %-12s %s (%s)\n", shortID, r.Item.Category, r.Item.Content, timeStr)
			}
			return
		}

		items, err := client.ListMemories(ctx, 50)
		if err != nil {
			fmt.Fprintf(stderr, "error: list memories: %v\n", err)
			return
		}
		if len(items) == 0 {
			fmt.Fprintln(stdout, "No workspace memories stored yet.")
			fmt.Fprintln(stdout, "Use '/memory add <text>' to record conventions, technical preferences, or rules.")
			return
		}
		fmt.Fprintf(stdout, "Workspace Memories (%d items):\n", len(items))
		for _, item := range items {
			shortID := item.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			timeStr := item.UpdatedAt.Format("Jan 02 15:04")
			fmt.Fprintf(stdout, "  [%s] %-12s %s (%s)\n", shortID, item.Category, item.Content, timeStr)
		}
		fmt.Fprintln(stdout, "\nUse '/memory add <text>' to record, '/memory remove <id>' to delete.")

	case "add":
		if len(parts) < 3 {
			fmt.Fprintln(stderr, "Usage: /memory add [category] <content>")
			fmt.Fprintln(stderr, "Categories: preference, convention, decision, correction, verified_fact (default: preference)")
			return
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
			fmt.Fprintln(stderr, "error: memory content cannot be empty")
			return
		}
		item, err := client.AddMemory(ctx, memory.Input{
			Category:   cat,
			Content:    content,
			Importance: 3,
		})
		if err != nil {
			fmt.Fprintf(stderr, "error: add memory: %v\n", err)
			return
		}
		shortID := item.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		fmt.Fprintf(stdout, "Added workspace memory [%s] (%s): %s\n", shortID, item.Category, item.Content)

	case "remove":
		if len(parts) < 3 {
			fmt.Fprintln(stderr, "Usage: /memory remove <id|prefix>")
			return
		}
		target := parts[2]
		items, err := client.ListMemories(ctx, 200)
		if err != nil {
			fmt.Fprintf(stderr, "error: query memories: %v\n", err)
			return
		}
		var matches []memory.Item
		for _, it := range items {
			if strings.HasPrefix(strings.ToLower(it.ID), strings.ToLower(target)) {
				matches = append(matches, it)
			}
		}
		if len(matches) == 0 {
			fmt.Fprintf(stderr, "error: memory item %q not found\n", target)
			return
		}
		if len(matches) > 1 {
			var ids []string
			for _, m := range matches {
				s := m.ID
				if len(s) > 8 {
					s = s[:8]
				}
				ids = append(ids, s)
			}
			fmt.Fprintf(stderr, "error: memory ID prefix %q is ambiguous (%d matches: %s)\n", target, len(matches), strings.Join(ids, ", "))
			return
		}
		match := matches[0]
		if err := client.DeleteMemory(ctx, match.ID); err != nil {
			fmt.Fprintf(stderr, "error: delete memory: %v\n", err)
			return
		}
		shortID := match.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		fmt.Fprintf(stdout, "Deleted workspace memory [%s]: %s\n", shortID, match.Content)

	case "clear":
		hasForce := false
		for _, p := range parts[2:] {
			if p == "--force" {
				hasForce = true
				break
			}
		}
		if !hasForce {
			fmt.Fprintln(stderr, "Are you sure you want to clear all workspace memories?")
			fmt.Fprintln(stderr, "Run '/memory clear --force' to confirm.")
			return
		}
		if err := client.ClearMemories(ctx); err != nil {
			fmt.Fprintf(stderr, "error: clear memories: %v\n", err)
			return
		}
		fmt.Fprintln(stdout, "Cleared all workspace memories.")

	case "help":
		fmt.Fprintln(stdout, "Workspace Memory Commands:")
		fmt.Fprintln(stdout, "  /memory or /memory list           List recent workspace memories")
		fmt.Fprintln(stdout, "  /memory list <query>              Search memories by text")
		fmt.Fprintln(stdout, "  /memory add [category] <content>  Add a workspace convention or preference")
		fmt.Fprintln(stdout, "  /memory remove <id|prefix>        Remove a memory item by ID or prefix")
		fmt.Fprintln(stdout, "  /memory clear --force             Clear all workspace memories")
		fmt.Fprintln(stdout, "\nCategories: preference, convention, decision, correction, verified_fact")

	default:
		// Unknown subcmd: treat as query search
		query := strings.TrimSpace(strings.Join(parts[1:], " "))
		results, err := client.SearchMemories(ctx, query, 20)
		if err != nil {
			fmt.Fprintf(stderr, "error: search memories: %v\n", err)
			return
		}
		if len(results) == 0 {
			fmt.Fprintf(stdout, "No workspace memories matching %q found.\n", query)
			return
		}
		fmt.Fprintf(stdout, "Workspace Memories (%d matches for %q):\n", len(results), query)
		for _, r := range results {
			shortID := r.Item.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			timeStr := r.Item.UpdatedAt.Format("Jan 02 15:04")
			fmt.Fprintf(stdout, "  [%s] %-12s %s (%s)\n", shortID, r.Item.Category, r.Item.Content, timeStr)
		}
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm %ds", m, s)
}

func handleCoreCLIPS(runtime cliRuntime, stdout, stderr io.Writer) {
	if runtime == nil {
		fmt.Fprintln(stderr, "error: runtime unavailable")
		return
	}
	ctx := context.Background()
	commands, err := runtime.ListCommands(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "error: list processes: %v\n", err)
		return
	}
	if len(commands) == 0 {
		fmt.Fprintln(stdout, "No background processes in current session.")
		return
	}
	fmt.Fprintf(stdout, "Background Processes (%d):\n", len(commands))
	for _, cmd := range commands {
		shortID := cmd.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		statusStr := string(cmd.Status)
		timeInfo := ""
		if !cmd.FinishedAt.IsZero() {
			timeInfo = fmt.Sprintf(" (duration %s)", formatDuration(cmd.FinishedAt.Sub(cmd.StartedAt)))
		} else if !cmd.StartedAt.IsZero() {
			timeInfo = fmt.Sprintf(" (started %s ago)", formatDuration(time.Since(cmd.StartedAt)))
		}
		exitStr := ""
		if cmd.ExitCode != nil {
			exitStr = fmt.Sprintf(" (exit %d)", *cmd.ExitCode)
		}
		fmt.Fprintf(stdout, "  [%s] %-12s %s%s%s\n", shortID, statusStr, cmd.Command, exitStr, timeInfo)
	}
	fmt.Fprintln(stdout, "\nUse '/stop <id|all>' to terminate, '/logs <id>' to inspect output.")
}

func handleCoreCLIStop(runtime cliRuntime, line string, stdout, stderr io.Writer) {
	if runtime == nil {
		fmt.Fprintln(stderr, "error: runtime unavailable")
		return
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		fmt.Fprintln(stderr, "Usage: /stop <id|prefix|all>")
		return
	}
	target := strings.ToLower(parts[1])
	ctx := context.Background()
	commands, err := runtime.ListCommands(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "error: list processes: %v\n", err)
		return
	}

	if target == "all" {
		var active []core.CommandInfo
		for _, cmd := range commands {
			if cmd.Status.Active() {
				active = append(active, cmd)
			}
		}
		if len(active) == 0 {
			fmt.Fprintln(stdout, "No active background processes to stop.")
			return
		}
		var stopped []string
		var failures []string
		for _, cmd := range active {
			shortID := cmd.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			if _, err := runtime.StopCommand(ctx, cmd.ID); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", shortID, err))
				continue
			}
			stopped = append(stopped, shortID)
		}
		fmt.Fprintf(stdout, "Stopped %d of %d active background process(es): %s\n", len(stopped), len(active), strings.Join(stopped, ", "))
		for _, failure := range failures {
			fmt.Fprintf(stderr, "error: stop process [%s]\n", failure)
		}
		return
	}

	var matches []core.CommandInfo
	for _, cmd := range commands {
		if strings.HasPrefix(strings.ToLower(cmd.ID), target) {
			matches = append(matches, cmd)
		}
	}
	if len(matches) == 0 {
		fmt.Fprintf(stderr, "error: process %q not found\n", target)
		return
	}
	if len(matches) > 1 {
		var ids []string
		for _, m := range matches {
			s := m.ID
			if len(s) > 8 {
				s = s[:8]
			}
			ids = append(ids, s)
		}
		fmt.Fprintf(stderr, "error: process ID prefix %q is ambiguous (%d matches: %s)\n", target, len(matches), strings.Join(ids, ", "))
		return
	}
	match := matches[0]
	if _, err := runtime.StopCommand(ctx, match.ID); err != nil {
		fmt.Fprintf(stderr, "error: stop process: %v\n", err)
		return
	}
	shortID := match.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	fmt.Fprintf(stdout, "Stopped background process [%s]: %s\n", shortID, match.Command)
}

func handleCoreCLILogs(runtime cliRuntime, line string, stdout, stderr io.Writer) {
	if runtime == nil {
		fmt.Fprintln(stderr, "error: runtime unavailable")
		return
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		fmt.Fprintln(stderr, "Usage: /logs <id|prefix> [--tail N]")
		return
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
	commands, err := runtime.ListCommands(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "error: list processes: %v\n", err)
		return
	}
	var matches []core.CommandInfo
	for _, cmd := range commands {
		if strings.HasPrefix(strings.ToLower(cmd.ID), target) {
			matches = append(matches, cmd)
		}
	}
	if len(matches) == 0 {
		fmt.Fprintf(stderr, "error: process %q not found\n", target)
		return
	}
	if len(matches) > 1 {
		var ids []string
		for _, m := range matches {
			s := m.ID
			if len(s) > 8 {
				s = s[:8]
			}
			ids = append(ids, s)
		}
		fmt.Fprintf(stderr, "error: process ID prefix %q is ambiguous (%d matches: %s)\n", target, len(matches), strings.Join(ids, ", "))
		return
	}
	match := matches[0]
	out, err := runtime.ReadCommand(ctx, match.ID, core.CommandReadOptions{})
	if err != nil {
		fmt.Fprintf(stderr, "error: read process output: %v\n", err)
		return
	}
	shortID := match.ID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	content := strings.TrimSpace(out.Output)
	if content == "" {
		fmt.Fprintf(stdout, "Process [%s] (%s) produced no output yet.\n", shortID, match.Command)
		return
	}
	rawLines := strings.Split(content, "\n")
	totalLines := len(rawLines)
	if len(rawLines) > tailLines {
		rawLines = rawLines[len(rawLines)-tailLines:]
	}
	fmt.Fprintf(stdout, "Logs for [%s] (%s, %s)", shortID, match.Command, match.Status)
	if totalLines > tailLines {
		fmt.Fprintf(stdout, " (showing last %d of %d lines):\n", tailLines, totalLines)
	} else {
		fmt.Fprintln(stdout, ":")
	}
	for _, l := range rawLines {
		fmt.Fprintln(stdout, l)
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

type cliToolEntry struct {
	Name    string
	Args    string
	Command string
	Output  string
	CallID  string
}

func cliExtractToolOutputString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return str
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			var part struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(p, &part) == nil && part.Text != "" {
				b.WriteString(part.Text)
			}
		}
		if b.Len() > 0 {
			return b.String()
		}
	}
	var obj struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Text != "" {
		return obj.Text
	}
	return string(raw)
}

func handleCoreCLIDetail(client cliCore, runtime cliRuntime, sessionID, line string, stdout, stderr io.Writer) {
	ctx := context.Background()
	saved, err := client.GetSession(ctx, sessionID)
	if err != nil {
		fmt.Fprintf(stderr, "error: get session: %v\n", err)
		return
	}

	var tools []cliToolEntry
	if len(saved.History) > 0 && string(saved.History) != "null" {
		var items []json.RawMessage
		if json.Unmarshal(saved.History, &items) == nil {
			for i, item := range items {
				var val struct {
					Type      string          `json:"type"`
					Name      string          `json:"name"`
					CallID    string          `json:"call_id"`
					Arguments json.RawMessage `json:"arguments"`
					Output    json.RawMessage `json:"output"`
				}
				if json.Unmarshal(item, &val) != nil {
					continue
				}
				switch val.Type {
				case "function_call", "custom_tool_call":
					args := string(val.Arguments)
					if len(args) > 0 && args[0] == '"' {
						var unquoted string
						if json.Unmarshal(val.Arguments, &unquoted) == nil {
							args = unquoted
						}
					}
					callID := val.CallID
					if callID == "" {
						callID = fmt.Sprintf("call-%d", i)
					}
					tools = append(tools, cliToolEntry{
						Name:    val.Name,
						Args:    args,
						Command: val.Name + " " + args,
						CallID:  callID,
					})
				case "function_call_output", "custom_tool_call_output":
					outText := cliExtractToolOutputString(val.Output)
					found := false
					if val.CallID != "" {
						for j := len(tools) - 1; j >= 0; j-- {
							if tools[j].CallID == val.CallID {
								tools[j].Output = outText
								found = true
								break
							}
						}
					}
					if !found && len(tools) > 0 {
						for j := len(tools) - 1; j >= 0; j-- {
							if tools[j].Output == "" {
								tools[j].Output = outText
								break
							}
						}
					}
				}
			}
		}
	}

	if len(tools) == 0 {
		fmt.Fprintln(stdout, "No tool executions found in current session.")
		return
	}

	parts := strings.Fields(line)
	target := len(tools) - 1
	if len(parts) > 1 {
		idx, err := strconv.Atoi(parts[1])
		if err != nil || idx < 1 || idx > len(tools) {
			fmt.Fprintf(stderr, "error: tool index %s out of range (1-%d)\n", parts[1], len(tools))
			return
		}
		target = idx - 1
	}

	entry := tools[target]
	cmdStr := entry.Command
	if cmdStr == "" {
		cmdStr = entry.Name
		if entry.Args != "" {
			cmdStr += " " + entry.Args
		}
	}

	fmt.Fprintf(stdout, "--- Tool %d of %d: %s ---\n", target+1, len(tools), cmdStr)
	outTrimmed := strings.TrimSpace(entry.Output)
	if outTrimmed == "" {
		fmt.Fprintln(stdout, "(no output)")
	} else {
		fmt.Fprintln(stdout, outTrimmed)
	}

	if len(tools) > 1 && len(parts) <= 1 {
		fmt.Fprintf(stdout, "\n(Showing latest tool %d of %d. Use '/detail <index>' to inspect earlier tools.)\n", target+1, len(tools))
	}
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

func handleCoreCLIDrop(client cliCore, currentSessionID string, line string, stdout, stderr io.Writer) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		fmt.Fprintln(stderr, "usage: /drop [id|prefix]")
		return
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
			return
		}
		var matches []core.Session
		for _, s := range sessions {
			if strings.HasPrefix(strings.ToLower(s.ID), strings.ToLower(targetPrefix)) {
				matches = append(matches, s)
			}
		}
		if len(matches) == 0 {
			fmt.Fprintf(stderr, "No session found matching prefix %q.\n", targetPrefix)
			return
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
			return
		}
		targetID = matches[0].ID
	}
	if targetID == currentSessionID {
		fmt.Fprintln(stderr, "Cannot delete currently active session. Switch to another session first with /open.")
		return
	}
	if err := client.DeleteSession(ctx, targetID); err != nil {
		fmt.Fprintf(stderr, "error: delete session: %v\n", err)
		return
	}
	fmt.Fprintf(stdout, "Deleted session %s.\n", targetID)
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
	pathArgs := []string{}
	if path := strings.TrimSpace(strings.TrimPrefix(line, "/diff")); path != "" {
		pathArgs = append(pathArgs, "--")
		pathArgs = append(pathArgs, path)
	}
	var sections []string
	for _, diff := range []struct {
		name string
		args []string
	}{{"Unstaged changes", append([]string{"diff", "--no-ext-diff"}, pathArgs...)}, {"Staged changes", append([]string{"diff", "--cached", "--no-ext-diff"}, pathArgs...)}} {
		cmd := exec.CommandContext(context.Background(), "git", diff.args...)
		cmd.Dir = workspace
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(stderr, "error: git %s: %v\n", diff.args[0], err)
			return
		}
		if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
			sections = append(sections, "### "+diff.name+"\n"+trimmed)
		}
	}
	if len(sections) == 0 {
		fmt.Fprintln(stdout, "No staged or unstaged changes in workspace.")
		return
	}
	fmt.Fprintln(stdout, strings.Join(sections, "\n\n"))
}

func handleCoreCLIReview(runtime cliRuntime, workspace, line string, watchKeys keyEventWatcher, stdout, stderr io.Writer) error {
	path := strings.TrimSpace(strings.TrimPrefix(line, "/review"))
	materials, changed, err := workspacereview.CollectMaterials(workspace, path)
	if err != nil {
		fmt.Fprintf(stderr, "error: prepare workspace review: %v\n", err)
		return nil
	}
	if !changed {
		fmt.Fprintln(stdout, "No text changes to review. Git-ignored and binary files are excluded.")
		return nil
	}
	input, err := core.ParseTaskInput(workspacereview.Prompt(materials, path))
	if err != nil {
		fmt.Fprintf(stderr, "error: parse review request: %v\n", err)
		return nil
	}
	result, callErr, interrupted := respondCoreWithKeys(runtime, workspace, core.TurnRequest{Input: input, PermissionMode: core.PermissionReadOnly}, false, watchKeys, stderr)
	return printCoreTaskResult(stdout, stderr, result.Response, callErr, interrupted)
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

	var events <-chan core.Event
	stopStream := func() {}
	var err error
	if resume {
		state, stateErr := runtime.State(context.Background())
		if stateErr != nil {
			return core.TurnResult{}, stateErr, false
		}
		if state.Task.ExecutionID != "" {
			events, stopStream, err = subscribeAfterPausedExecution(runtime, state.Task.ExecutionID)
		} else {
			streamCtx, cancel := context.WithCancel(context.Background())
			stopStream = cancel
			events, err = runtime.SubscribeEvents(streamCtx, 0)
		}
	} else {
		streamCtx, cancel := context.WithCancel(context.Background())
		stopStream = cancel
		events, err = runtime.SubscribeEvents(streamCtx, 0)
	}
	defer stopStream()
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
	return awaitCoreExecution(runtime, workspace, submission.ExecutionID, events, keyEvents, stderr)
}

func awaitCoreExecution(runtime cliRuntime, workspace, executionID string, events <-chan core.Event, keyEvents <-chan cliInputEvent, stderr io.Writer) (core.TurnResult, error, bool) {
	if keyEvents != nil {
		if _, err := fmt.Fprintln(stderr, "Working... Press Esc to interrupt."); err != nil {
			runtime.Interrupt(executionID)
			return core.TurnResult{}, err, true
		}
	}
	type waitResult struct {
		result core.TurnResult
		err    error
	}
	done := make(chan waitResult, 1)
	go func() {
		result, waitErr := runtime.WaitTurn(context.Background(), executionID)
		done <- waitResult{result: result, err: waitErr}
	}()
	interrupted := false
	var pendingKey *cliInputEvent
	for {
		select {
		case result := <-done:
			pendingKey = nil
			if result.result.Status == core.TaskPaused && result.err == nil {
				result.err = core.ErrTaskPaused
			} else if result.result.Status == core.TaskFailed && result.err == nil {
				if result.result.ModelFailure != nil {
					reason := string(result.result.ModelFailure.Category)
					if result.result.ModelFailure.Code != "" {
						reason += " (" + result.result.ModelFailure.Code + ")"
					} else if result.result.ModelFailure.StatusCode > 0 {
						reason += fmt.Sprintf(" (HTTP %d)", result.result.ModelFailure.StatusCode)
					}
					if reason == "" {
						reason = "model request failed"
					}
					result.err = fmt.Errorf("task failed: %s", reason)
				} else if result.result.BudgetReason != "" {
					result.err = fmt.Errorf("task stopped: budget limit reached (%s)", result.result.BudgetReason)
				} else {
					result.err = errors.New("task failed")
				}
			}
			return result.result, result.err, interrupted
		case event, ok := <-events:
			if !ok {
				return core.TurnResult{}, errors.New("session event stream closed"), interrupted
			}
			if event.ExecutionID != "" && event.ExecutionID != executionID {
				continue
			}
			if event.Kind != core.EventApprovalRequired {
				pendingKey = nil
			}
			switch event.Kind {
			case core.EventContextCompaction:
				if event.Compaction != nil {
					writeCLICompactionStatus(stderr, *event.Compaction)
				}
			case core.EventToolStarted:
				if strings.TrimSpace(event.Tool.Name) != "" && !strings.EqualFold(event.Tool.Name, "select_tool_category") {
					summary := cliApprovalSummary(core.ApprovalRequest{
						Tool:      event.Tool.Name,
						Arguments: event.Tool.Arguments,
					}, workspace)
					_, _ = fmt.Fprintf(stderr, "• %s\n", summary)
				}
			case core.EventModelRetry:
				attempt := 1
				if event.ModelFailure != nil && event.ModelFailure.Attempts > 0 {
					attempt = event.ModelFailure.Attempts
				}
				reason := cleanCLIInlineText(event.Err)
				if reason == "" && event.ModelFailure != nil {
					reason = string(event.ModelFailure.Category)
				}
				if reason != "" {
					_, _ = fmt.Fprintf(stderr, "Retrying model request (attempt %d): %s\n", attempt, reason)
				} else {
					_, _ = fmt.Fprintf(stderr, "Retrying model request (attempt %d)...\n", attempt)
				}
			case core.EventAgentStarted:
				name := strings.TrimSpace(event.AgentName)
				if name == "" {
					name = strings.TrimSpace(event.AgentID)
				}
				if name != "" && name != "lead" {
					_, _ = fmt.Fprintf(stderr, "• Subagent started: %s\n", cleanCLIInlineText(name))
				}
			case core.EventAgentFinished:
				name := strings.TrimSpace(event.AgentName)
				if name == "" {
					name = strings.TrimSpace(event.AgentID)
				}
				if name != "" && name != "lead" {
					_, _ = fmt.Fprintf(stderr, "• Subagent finished: %s\n", cleanCLIInlineText(name))
				}
			case core.EventApprovalRequired:
				if event.Approval != nil {
					if keyEvents == nil {
						if err := runtime.ResolveApproval(event.Approval.ID, false); err != nil {
							fmt.Fprintf(stderr, "error: deny approval: %v\n", err)
							runtime.Interrupt(executionID)
						} else {
							fmt.Fprintf(stderr, "Denied %s because interactive keyboard input is unavailable.\n", cliApprovalSummary(*event.Approval, workspace))
						}
						continue
					}
					if _, err := fmt.Fprintln(stderr, formatCLIToolApproval(*event.Approval, workspace)); err != nil {
						runtime.Interrupt(executionID)
						continue
					}
					if pendingKey != nil {
						handled, err := handleCLIApprovalKey(runtime, *event.Approval, workspace, *pendingKey, stderr)
						pendingKey = nil
						if err != nil {
							runtime.Interrupt(executionID)
							continue
						}
						if handled {
							continue
						}
					}
					if err := resolveCLIApprovalKeys(runtime, *event.Approval, workspace, keyEvents, stderr); err != nil {
						runtime.Interrupt(executionID)
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
				interrupted = runtime.Interrupt(executionID)
				if interrupted {
					slog.Warn("task interrupted by user", "source", "escape")
				}
				pendingKey = nil
				continue
			}
			// The approval event and a fast Y/N key can become ready together.
			// Hold the key so the approval handler can consume it instead of
			// letting one side of the select wait forever.
			buffered := key
			pendingKey = &buffered
		}
	}
}

func subscribeAfterPausedExecution(runtime cliRuntime, executionID string) (<-chan core.Event, context.CancelFunc, error) {
	replayCtx, stopReplay := context.WithCancel(context.Background())
	replay, err := runtime.SubscribeEvents(replayCtx, 0)
	if err != nil {
		stopReplay()
		return nil, func() {}, err
	}
	var cursor int64
	found := false
	for event := range replay {
		if event.Cursor > cursor {
			cursor = event.Cursor
		}
		if event.Kind == core.EventTaskFinished && event.Status == core.TaskPaused && event.ExecutionID == executionID {
			found = true
			break
		}
	}
	stopReplay()
	if !found {
		return nil, func() {}, fmt.Errorf("could not locate the pause point for execution %s", executionID)
	}
	streamCtx, stopStream := context.WithCancel(context.Background())
	events, err := runtime.SubscribeEvents(streamCtx, cursor)
	if err != nil {
		stopStream()
		return nil, func() {}, err
	}
	return events, stopStream, nil
}

func resumePausedTaskCLI(runtime cliRuntime, reader *bufio.Reader, workspace string, requests []core.PendingRequest, watchKeys keyEventWatcher, stdout, stderr io.Writer) (core.TurnResult, error, bool) {
	ctx := context.Background()
	for attempts := 0; attempts < 8; attempts++ {
		if len(requests) == 0 {
			result, err, interrupted := respondCoreWithKeys(runtime, workspace, core.TurnRequest{}, true, watchKeys, stderr)
			if result.Status != core.TaskPaused {
				return result, err, interrupted
			}
			requests, err = runtime.PendingRequests(ctx)
			if err != nil {
				return result, fmt.Errorf("inspect resumed task: %w", err), interrupted
			}
			if len(requests) == 0 {
				return result, errors.New("task remains paused without a recoverable request; use the TUI for diagnostics"), interrupted
			}
			continue
		}

		state, err := runtime.State(ctx)
		if err != nil {
			return core.TurnResult{}, fmt.Errorf("inspect paused execution: %w", err), false
		}
		executionID := state.Task.ExecutionID
		if executionID == "" {
			return core.TurnResult{}, errors.New("paused task has no execution ID"), false
		}
		request := requests[0]
		if request.Kind == core.PendingUserInteraction || request.Kind == core.PendingExecutionBlocker {
			fmt.Fprintf(stderr, "This paused task requires %s, which the CLI cannot resolve. Resume it in the TUI or use /discard.\n", request.Kind)
			return core.TurnResult{ExecutionID: executionID, Status: core.TaskPaused}, core.ErrTaskPaused, false
		}

		events, stopStream, err := subscribeAfterPausedExecution(runtime, executionID)
		if err != nil {
			return core.TurnResult{}, fmt.Errorf("watch paused execution: %w", err), false
		}
		var keyEvents <-chan cliInputEvent
		stopWatching := func() {}
		watchingStarted := false
		startWatching := func() {
			if watchKeys == nil || watchingStarted {
				return
			}
			watchingStarted = true
			keyEvents, stopWatching, err = watchKeys()
			if stopWatching == nil {
				stopWatching = func() {}
			}
			if err != nil {
				fmt.Fprintf(stderr, "warning: keyboard input unavailable: %v\n", err)
				keyEvents = nil
			}
		}

		switch request.Kind {
		case core.PendingUserQuestion:
			if request.UserQuestion == nil {
				stopWatching()
				stopStream()
				return core.TurnResult{}, errors.New("pending user question has no payload"), false
			}
			answer, readErr := readCLIUserQuestion(reader, *request.UserQuestion, stdout)
			if readErr != nil {
				stopWatching()
				stopStream()
				return core.TurnResult{}, readErr, false
			}
			runtime.SetUserInteractionAvailable(false)
			startWatching()
			if err := runtime.ResolveUserQuestion(ctx, answer); err != nil {
				stopWatching()
				stopStream()
				return core.TurnResult{}, fmt.Errorf("submit user answers: %w", err), false
			}
			executionID = request.UserQuestion.ExecutionID
		case core.PendingModelRecoveryRequest:
			if request.ModelRecovery == nil {
				stopWatching()
				stopStream()
				return core.TurnResult{}, errors.New("pending model recovery has no payload"), false
			}
			failure := request.ModelRecovery.Failure
			fmt.Fprintf(stdout, "Model request failed (%s). Retry after %s? [r]etry / [d]iscard: ", failure.Category, failure.RetryNotBefore.Format(time.RFC3339))
			choice, readErr := readCLIRecoveryLine(reader)
			if readErr != nil {
				stopWatching()
				stopStream()
				return core.TurnResult{}, readErr, false
			}
			if !strings.EqualFold(choice, "r") && !strings.EqualFold(choice, "retry") {
				if strings.EqualFold(choice, "d") || strings.EqualFold(choice, "discard") {
					_, discardErr := runtime.DiscardPaused(ctx)
					stopWatching()
					stopStream()
					if discardErr != nil {
						return core.TurnResult{}, fmt.Errorf("discard paused task: %w", discardErr), false
					}
					fmt.Fprintln(stdout, "Interrupted task discarded.")
					return core.TurnResult{ExecutionID: executionID, Status: core.TaskCancelled}, nil, false
				}
				stopWatching()
				stopStream()
				return core.TurnResult{ExecutionID: executionID, Status: core.TaskPaused}, core.ErrTaskPaused, false
			}
			if delay := time.Until(failure.RetryNotBefore); delay > 0 {
				fmt.Fprintf(stdout, "Waiting %s for the model retry window...\n", delay.Round(time.Second))
				timer := time.NewTimer(delay)
				<-timer.C
			}
			runtime.SetUserInteractionAvailable(false)
			startWatching()
			var submission core.TurnSubmission
			submission, err = runtime.Recover(ctx)
			if err == nil {
				executionID = submission.ExecutionID
			}
		case core.PendingToolRecovery:
			if request.ToolRecovery == nil {
				stopWatching()
				stopStream()
				return core.TurnResult{}, errors.New("pending tool recovery has no payload"), false
			}
			tool := cleanCLIInlineText(request.ToolRecovery.Tool)
			fmt.Fprintf(stdout, "The outcome of %s is unknown. [r]etry / [s]kip: ", tool)
			choice, readErr := readCLIRecoveryLine(reader)
			if readErr != nil {
				stopWatching()
				stopStream()
				return core.TurnResult{}, readErr, false
			}
			decision := core.ToolRecoverySkip
			if strings.EqualFold(choice, "r") || strings.EqualFold(choice, "retry") {
				decision = core.ToolRecoveryRetry
			} else if !strings.EqualFold(choice, "s") && !strings.EqualFold(choice, "skip") {
				stopWatching()
				stopStream()
				return core.TurnResult{ExecutionID: executionID, Status: core.TaskPaused}, core.ErrTaskPaused, false
			}
			runtime.SetUserInteractionAvailable(false)
			startWatching()
			if err = runtime.ResolveToolRecovery(ctx, request.ToolRecovery.InvocationID, decision); err == nil {
				var submission core.TurnSubmission
				submission, err = runtime.Recover(ctx)
				if err == nil {
					executionID = submission.ExecutionID
				}
			}
		case core.PendingApproval:
			if request.Approval == nil {
				stopWatching()
				stopStream()
				return core.TurnResult{}, errors.New("pending approval has no payload"), false
			}
			fmt.Fprintf(stdout, "%s\nAllow this tool call? [y/N]: ", formatCLIToolApproval(*request.Approval, workspace))
			choice, readErr := readCLIRecoveryLine(reader)
			if readErr != nil {
				stopWatching()
				stopStream()
				return core.TurnResult{}, readErr, false
			}
			allowed := strings.EqualFold(choice, "y") || strings.EqualFold(choice, "yes")
			runtime.SetUserInteractionAvailable(false)
			startWatching()
			err = runtime.ResolveApproval(request.Approval.ID, allowed)
		default:
			stopWatching()
			stopStream()
			return core.TurnResult{}, fmt.Errorf("unsupported paused request %q", request.Kind), false
		}
		if err != nil {
			stopWatching()
			stopStream()
			return core.TurnResult{}, fmt.Errorf("resolve paused request: %w", err), false
		}
		result, waitErr, interrupted := awaitCoreExecution(runtime, workspace, executionID, events, keyEvents, stderr)
		stopWatching()
		stopStream()
		if result.Status != core.TaskPaused {
			return result, waitErr, interrupted
		}
		requests, err = runtime.PendingRequests(ctx)
		if err != nil {
			return result, fmt.Errorf("inspect resumed task decisions: %w", err), interrupted
		}
	}
	return core.TurnResult{}, errors.New("paused task exceeded the CLI recovery step limit"), false
}

func readCLIUserQuestion(reader *bufio.Reader, question core.UserQuestionRequest, stdout io.Writer) (core.UserQuestionAnswer, error) {
	answers := make([]core.UserQuestionResponse, 0, len(question.Questions))
	for index, item := range question.Questions {
		fmt.Fprintf(stdout, "Question %d/%d: %s\n", index+1, len(question.Questions), cleanCLIInlineText(item.Question))
		for optionIndex, option := range item.Options {
			fmt.Fprintf(stdout, "  %d. %s", optionIndex+1, cleanCLIInlineText(option.Label))
			if option.Description != "" {
				fmt.Fprintf(stdout, " — %s", cleanCLIInlineText(option.Description))
			}
			fmt.Fprintln(stdout)
		}
		for {
			fmt.Fprint(stdout, "Answer with an option number or your own text: ")
			answer, err := readCLIRecoveryLine(reader)
			if err != nil {
				return core.UserQuestionAnswer{}, err
			}
			if choice, parseErr := strconv.Atoi(answer); parseErr == nil && choice > 0 && choice <= len(item.Options) {
				answer = item.Options[choice-1].Label
			}
			if strings.TrimSpace(answer) == "" {
				fmt.Fprintln(stdout, "An answer is required.")
				continue
			}
			answers = append(answers, core.UserQuestionResponse{QuestionID: item.ID, Answer: answer})
			break
		}
	}
	return core.UserQuestionAnswer{
		ExecutionID: question.ExecutionID, RunID: question.RunID, InvocationID: question.InvocationID,
		CallID: question.CallID, Answers: answers,
	}, nil
}

func readCLIRecoveryLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read recovery input: %w", err)
	}
	if errors.Is(err, io.EOF) && strings.TrimSpace(line) == "" {
		return "", io.EOF
	}
	return strings.TrimSpace(line), nil
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
	if tokens >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d", tokens)
}

func resolveCLIApproval(runtime cliRuntime, request core.ApprovalRequest, workspace string, keys <-chan cliInputEvent, stderr io.Writer) error {
	if _, err := fmt.Fprintln(stderr, formatCLIToolApproval(request, workspace)); err != nil {
		return err
	}
	return resolveCLIApprovalKeys(runtime, request, workspace, keys, stderr)
}

func resolveCLIApprovalKeys(runtime cliRuntime, request core.ApprovalRequest, workspace string, keys <-chan cliInputEvent, stderr io.Writer) error {
	for key := range keys {
		handled, err := handleCLIApprovalKey(runtime, request, workspace, key, stderr)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
	}
	err := runtime.ResolveApproval(request.ID, false)
	if err == nil {
		fmt.Fprintf(stderr, "Denied %s because interactive keyboard input became unavailable.\n", cliApprovalSummary(request, workspace))
	} else {
		fmt.Fprintf(stderr, "error: resolve approval: %v\n", err)
	}
	return err
}

func handleCLIApprovalKey(runtime cliRuntime, request core.ApprovalRequest, workspace string, key cliInputEvent, stderr io.Writer) (bool, error) {
	if key.err != nil {
		err := runtime.ResolveApproval(request.ID, false)
		if err == nil {
			fmt.Fprintf(stderr, "Denied %s because interactive keyboard input is unavailable.\n", cliApprovalSummary(request, workspace))
		} else {
			fmt.Fprintf(stderr, "error: resolve approval: %v\n", err)
		}
		return true, err
	}
	if key.escape {
		return false, errors.New("approval interrupted")
	}
	var allowed bool
	switch unicode.ToLower(key.rune) {
	case 'y':
		allowed = true
	case 'n':
		allowed = false
	default:
		return false, nil
	}
	if err := runtime.ResolveApproval(request.ID, allowed); err != nil {
		fmt.Fprintf(stderr, "error: resolve approval: %v\n", err)
		return true, fmt.Errorf("resolve approval: %w", err)
	}
	if allowed {
		_, _ = fmt.Fprintln(stderr, "Approved this tool call once.")
	} else {
		_, _ = fmt.Fprintln(stderr, "Denied this tool call.")
	}
	return true, nil
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
