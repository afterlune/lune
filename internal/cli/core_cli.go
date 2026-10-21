package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/afterlune/lune/internal/filemention"
	core "github.com/afterlune/luneCore"
	"github.com/afterlune/luneCore/memory"
)

type cliRuntime interface {
	Close() error
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
	{name: "/agents", usage: "/agents [id]", description: "Inspect live agents and subagents in this session."},
	{name: "/clear", usage: "/clear", description: "Clear terminal screen."},
	{name: "/compact", usage: "/compact", description: "Compact older conversation history."},
	{name: "/context", usage: "/context", description: "Show context window budget, system rules, and capabilities."},
	{name: "/detail", usage: "/detail [index]", description: "View full output of a tool call."},
	{name: "/diff", usage: "/diff [path]", description: "Inspect git code changes in workspace."},
	{name: "/discard", usage: "/discard", description: "Discard the paused task in this session."},
	{name: "/drop", usage: "/drop [id|prefix]", description: "Delete a saved session."},
	{name: "/effort", usage: "/effort [level]", description: "View or set model reasoning effort: default, none, minimal, low, medium, high, xhigh, max."},
	{name: "/exit", usage: "/exit", description: "Exit the chat."},
	{name: "/export", usage: "/export [md|html|jsonl] [path]", description: "Export session conversation transcript to a file."},
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
	{name: "/rewind", usage: "/rewind [N|preview [N]]", description: "Rewind conversation or preview turns to be removed."},
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
	defer func() { _ = runtime.Close() }()
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
					_ = runtime.Close()
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
		case line == "/effort" || strings.HasPrefix(line, "/effort "):
			handleCoreCLIEffort(client, sessionID, line, stdout, stderr)
		case line == "/agents" || strings.HasPrefix(line, "/agents "):
			handleCoreCLIAgents(client, runtime, sessionID, line, stdout, stderr)
		case line == "/model" || strings.HasPrefix(line, "/model "):
			handleCoreCLIModel(client, sessionID, line, stdout, stderr)
		case line == "/sessions" || strings.HasPrefix(line, "/sessions "):
			handleCoreCLISessions(client, sessionID, line, stdout, stderr)
		case line == "/open" || strings.HasPrefix(line, "/open "):
			newID, newRuntime, openErr := handleCoreCLIOpen(client, sessionID, line, stdout, stderr)
			if openErr != nil {
				fmt.Fprintf(stderr, "error: open session: %v\n", openErr)
			} else if newRuntime != nil {
				_ = runtime.Close()
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
					_ = runtime.Close()
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
		case line == "/theme" || strings.HasPrefix(line, "/theme "):
			fmt.Fprintln(stderr, "The /theme command is specific to full-screen interactive TUI mode (run 'lune --tui').")
		case line == "/shortcuts" || strings.HasPrefix(line, "/shortcuts "):
			fmt.Fprintln(stderr, "The /shortcuts command is specific to full-screen interactive TUI mode (run 'lune --tui').")
		case line == "/thought" || strings.HasPrefix(line, "/thought "):
			fmt.Fprintln(stderr, "The /thought command is specific to full-screen interactive TUI mode (run 'lune --tui').")
		case line == "/cancel-queue" || strings.HasPrefix(line, "/cancel-queue "):
			fmt.Fprintln(stderr, "The /cancel-queue command is specific to full-screen interactive TUI mode (run 'lune --tui').")
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
				effectiveWorkspace := workspace
				if effectiveWorkspace == "" {
					effectiveWorkspace = client.Workspace()
				}
				_, augmented := filemention.Expand(line, effectiveWorkspace)
				input, err := core.ParseTaskInput(augmented)
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

type cliToolEntry struct {
	Name    string
	Args    string
	Command string
	Output  string
	CallID  string
}
