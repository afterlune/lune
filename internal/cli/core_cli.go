package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/afterlune/lune/internal/filemention"
	"github.com/afterlune/lune/internal/goalcmd"
	"github.com/afterlune/lune/internal/slashcmd"
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
	Compact(context.Context, string) (core.CompactionResult, error)
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
	CommandToolchainPaths() []string
	SetCommandToolchainPaths([]string) error
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

// validateCLIModelProfile fails fast for a `-m` profile that does not exist, even
// while the session is still a draft.
func validateCLIModelProfile(client cliCore, profile string) error {
	for _, candidate := range client.ListModelProfiles() {
		if strings.EqualFold(candidate.Profile, profile) {
			return nil
		}
	}
	return fmt.Errorf("unknown model profile %q", profile)
}

// cliHelpText renders the slash command table from the shared registry. The list
// is alphabetical so the registry can keep the TUI's semantic order.
func cliHelpText() string {
	commands := slashcmd.For(slashcmd.TargetCLI)
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	lines := []string{"Available commands:"}
	for _, command := range commands {
		description := command.Description
		if strings.TrimSpace(description) == "" {
			description = command.Summary
		}
		lines = append(lines, fmt.Sprintf("  %-24s %s", command.Usage, description))
	}
	return strings.Join(lines, "\n") + "\n"
}

// cliCommandNeedsSession reports slash commands that operate on a persisted
// session. The CLI stays in a draft until the first prompt creates the session.
func cliCommandNeedsSession(line string) bool {
	command, ok := slashcmd.Lookup(line)
	return ok && command.NeedsSession
}

// cliSessionLabel renders the session identifier for status output.
func cliSessionLabel(sessionID string) string {
	if sessionID == "" {
		return "(pending — created with your first message)"
	}
	return sessionID
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
	}

	// A draft session is created with the first prompt so launching the CLI never
	// persists an empty session.
	pendingModelProfile := ""
	if opts.ModelProfile != "" {
		if sessionID == "" {
			// Fail fast for an unknown profile; it is applied when the session exists.
			if err := validateCLIModelProfile(client, opts.ModelProfile); err != nil {
				return err
			}
			pendingModelProfile = opts.ModelProfile
		} else if err := applyCLISessionModelProfile(ctx, client, sessionID, opts.ModelProfile); err != nil {
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

	var runtime cliRuntime
	if sessionID != "" {
		opened, err := client.OpenSessionRuntime(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("open session runtime: %w", err)
		}
		runtime = opened
	}
	defer func() {
		if runtime != nil {
			_ = runtime.Close()
		}
	}()

	ensureSession := func() error {
		if sessionID != "" {
			return nil
		}
		created, err := client.CreateSession(ctx, json.RawMessage("null"))
		if err != nil {
			return err
		}
		opened, err := client.OpenSessionRuntime(ctx, created.ID)
		if err != nil {
			if cleanupErr := client.DeleteSession(ctx, created.ID); cleanupErr != nil {
				return fmt.Errorf("%w (cleanup failed: %v)", err, cleanupErr)
			}
			return err
		}
		sessionID, sessionTitle, workspace, runtime = created.ID, created.Title, created.Workspace, opened
		if pendingModelProfile != "" {
			if err := applyCLISessionModelProfile(ctx, client, sessionID, pendingModelProfile); err != nil {
				return err
			}
			pendingModelProfile = ""
		}
		return nil
	}

	submitPrompt := func(text string) error {
		if err := ensureSession(); err != nil {
			fmt.Fprintf(stderr, "error: create session: %v\n", err)
			return nil
		}
		state, err := runtime.State(ctx)
		if err != nil {
			fmt.Fprintf(stderr, "error: inspect pending task: %v\n", err)
			return nil
		}
		if state.Task.Status == core.TaskRunning || state.Task.Status == core.TaskQueued {
			fmt.Fprintln(stderr, "A turn is already running or queued in this session.")
			return nil
		}
		effectiveWorkspace := workspace
		if effectiveWorkspace == "" {
			effectiveWorkspace = client.Workspace()
		}
		_, augmented := filemention.Expand(text, effectiveWorkspace)
		input, err := core.ParseTaskInput(augmented)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return nil
		}
		if state.Task.Status == core.TaskPaused {
			if _, err := runtime.DiscardPaused(ctx); err != nil {
				fmt.Fprintf(stderr, "error: discard paused task checkpoint: %v\n", err)
				return nil
			}
			fmt.Fprintln(stdout, "Discarded the paused checkpoint; starting a new turn.")
		}
		result, callErr, interrupted := respondCoreWithKeys(runtime, workspace, core.TurnRequest{Input: input, PlanMode: planMode}, false, watchKeys, stderr)
		return printCoreTaskResult(stdout, stderr, result.Response, callErr, interrupted)
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
		if sessionID == "" && cliCommandNeedsSession(line) {
			fmt.Fprintln(stderr, "No session yet. Send a message first.")
			continue
		}
		switch {
		case goalcmd.IsCommand(line):
			command, err := goalcmd.Parse(line)
			if err != nil {
				fmt.Fprintln(stderr, "error:", err)
				continue
			}
			if planMode && !command.AllowedInPlan() {
				fmt.Fprintln(stderr, "Exit Plan mode before starting or changing a goal.")
				continue
			}
			if sessionID == "" {
				if command.Action != "create" {
					fmt.Fprintln(stdout, goalcmd.Summary(nil))
					continue
				}
				if err := ensureSession(); err != nil {
					fmt.Fprintln(stderr, "error: create session:", err)
					continue
				}
			}
			if err := handleCoreCLIGoal(runtime, workspace, command, watchKeys, stdout, stderr); err != nil {
				return err
			}
		case line == "/help":
			fmt.Fprint(stdout, cliHelpText())
		case line == "/exit":
			return nil
		case line == "/clear":
			fmt.Fprint(stdout, "\033[H\033[2J")
		case line == "/new":
			if runtime != nil {
				_ = runtime.Close()
				runtime = nil
			}
			sessionID, sessionTitle, workspace = "", "", ""
			fmt.Fprintln(stdout, "Started a fresh session; it is saved with your first message.")
		case line == "/plan":
			if !planMode {
				if manager, ok := runtime.(goalcmd.Runtime); ok {
					if goal, err := manager.GetGoal(ctx); err == nil && goal != nil && goal.Status == core.GoalActive {
						if _, err := manager.PauseGoal(ctx); err != nil {
							fmt.Fprintln(stderr, "error: pause goal:", err)
							continue
						}
					}
				}
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
			handleCoreCLIStatus(client, cliSessionLabel(sessionID), sessionTitle, planMode, stdout)
			if manager, ok := runtime.(goalcmd.Runtime); ok {
				if goal, err := manager.GetGoal(ctx); err == nil && goal != nil {
					fmt.Fprintln(stdout, goalcmd.Summary(goal))
				}
			}
		case line == "/context":
			handleCoreCLIContext(client, cliSessionLabel(sessionID), sessionTitle, planMode, stdout)
			if manager, ok := runtime.(goalcmd.Runtime); ok {
				if goal, err := manager.GetGoal(ctx); err == nil && goal != nil {
					fmt.Fprintln(stdout, goalcmd.Summary(goal))
				}
			}
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
		case line == "/resume" || strings.HasPrefix(line, "/resume "):
			newID, newRuntime, resumeErr := handleCoreCLIResume(client, sessionID, line, stdout, stderr)
			if resumeErr != nil {
				fmt.Fprintf(stderr, "error: resume session: %v\n", resumeErr)
			} else if newRuntime != nil {
				if runtime != nil {
					_ = runtime.Close()
				}
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
					if runtime != nil {
						_ = runtime.Close()
					}
					sessionID, sessionTitle, workspace, runtime = newSession.ID, newSession.Title, newSession.Workspace, newRuntime
					fmt.Fprintf(stdout, "Forked to new session: %s\n", sessionID)
				}
			}
		case line == "/permissions" || strings.HasPrefix(line, "/permissions "):
			handleCoreCLIPermissions(client, planMode, line, stdout, stderr)
		case line == "/toolchains" || strings.HasPrefix(line, "/toolchains "):
			handleCoreCLIToolchains(client, line, stdout, stderr)
		case line == "/discard":
			discarded, err := runtime.DiscardPaused(ctx)
			if err != nil {
				fmt.Fprintf(stderr, "error: discard task: %v\n", err)
			} else if discarded {
				fmt.Fprintln(stdout, "Interrupted task discarded.")
			} else {
				fmt.Fprintln(stdout, "No interrupted task to discard.")
			}
		case line == "/continue":
			state, err := runtime.State(ctx)
			if err != nil {
				fmt.Fprintf(stderr, "error: inspect pending task: %v\n", err)
			} else if state.Goal != nil && state.Task.GoalID == state.Goal.ID {
				fmt.Fprintln(stdout, "Use /goal resume to continue this goal and its checkpoint.")
			} else if state.Task.Status != core.TaskPaused {
				fmt.Fprintln(stdout, "No paused task to continue.")
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
			fmt.Fprintln(stderr, "Compacting context...")
			result, err := client.Compact(ctx, sessionID)
			if err != nil {
				fmt.Fprintf(stderr, "error: compact conversation: %v\n", err)
			} else if !result.Compacted || result.Turns == 0 {
				fmt.Fprintln(stdout, "No older conversation to compact.")
			} else {
				fmt.Fprintf(stdout, "Compacted %d older user turn(s), %s → %s tokens.\n",
					result.Turns, compactCLITokens(result.Before), compactCLITokens(result.After))
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
		default:
			// Anything that is not a registered command, including text that merely
			// starts with "/" (for example a pasted absolute path), is a prompt.
			if err := submitPrompt(line); err != nil {
				return err
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
