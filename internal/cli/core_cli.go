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
	"path/filepath"
	"strings"
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
	OpenSessionRuntime(context.Context, string) (cliRuntime, error)
	Compact(context.Context, string) (int, error)
	PermissionMode() core.PermissionMode
	SetPermissionMode(core.PermissionMode) error
}

type cliCommandHelp struct {
	name        string
	usage       string
	description string
}

var cliCommands = []cliCommandHelp{
	{name: "/compact", usage: "/compact", description: "Compact older conversation history."},
	{name: "/discard", usage: "/discard", description: "Discard the paused task in this session."},
	{name: "/exit", usage: "/exit", description: "Exit the chat."},
	{name: "/help", usage: "/help", description: "Show available commands."},
	{name: "/new", usage: "/new", description: "Start a new session."},
	{name: "/permissions", usage: "/permissions [mode]", description: "Show or set mode: read-only, workspace, ask, full-access."},
	{name: "/resume", usage: "/resume", description: "Resume the paused task in this session."},
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
	saved, found, err := client.LatestSession(ctx)
	if err != nil {
		return fmt.Errorf("load latest session: %w", err)
	}
	if !found {
		saved, err = client.CreateSession(ctx, json.RawMessage("null"))
		if err != nil {
			return fmt.Errorf("create session: %w", err)
		}
	}
	sessionID := saved.ID
	workspace := saved.Workspace
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
		case line == "/new":
			saved, err = client.CreateSession(ctx, json.RawMessage("null"))
			if err != nil {
				fmt.Fprintf(stderr, "error: create session: %v\n", err)
			} else {
				sessionID = saved.ID
				workspace = saved.Workspace
				runtime, err = client.OpenSessionRuntime(ctx, sessionID)
				if err != nil {
					fmt.Fprintf(stderr, "error: open session runtime: %v\n", err)
				} else {
					fmt.Fprintln(stdout, "New session.")
				}
			}
		case line == "/permissions" || strings.HasPrefix(line, "/permissions "):
			handleCoreCLIPermissions(client, line, stdout, stderr)
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

func handleCoreCLIPermissions(client cliCore, line string, stdout, stderr io.Writer) {
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
	mode := core.PermissionMode(fields[1])
	if err := client.SetPermissionMode(mode); err != nil {
		fmt.Fprintln(stderr, err)
		return
	}
	fmt.Fprintf(stdout, "Permission mode set to %s.\n", mode)
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
