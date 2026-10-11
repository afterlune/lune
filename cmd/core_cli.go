package main

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
	"sync/atomic"
	"unicode"

	"github.com/afterlune/luneCore"
)

type coreOperation func(context.Context) (json.RawMessage, error)

type cliCore interface {
	LatestSession(context.Context) (core.Session, bool, error)
	CreateSession(context.Context, json.RawMessage) (core.Session, error)
	RunTask(context.Context, core.TaskRequest) (json.RawMessage, error)
	ResumeTask(context.Context, string) (json.RawMessage, error)
	HasPending(context.Context, string) (bool, error)
	DiscardTask(context.Context, string) (bool, error)
	Compact(context.Context, string) (int, error)
	SetEventHandler(core.EventHandler)
	ResolveApproval(string, bool) error
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
				fmt.Fprintln(stdout, "New session.")
			}
		case line == "/permissions" || strings.HasPrefix(line, "/permissions "):
			handleCoreCLIPermissions(client, line, stdout, stderr)
		case line == "/discard":
			discarded, err := client.DiscardTask(ctx, sessionID)
			if err != nil {
				fmt.Fprintf(stderr, "error: discard task: %v\n", err)
			} else if discarded {
				fmt.Fprintln(stdout, "Interrupted task discarded.")
			} else {
				fmt.Fprintln(stdout, "No interrupted task to discard.")
			}
		case line == "/resume":
			pending, err := client.HasPending(ctx, sessionID)
			if err != nil {
				fmt.Fprintf(stderr, "error: inspect pending task: %v\n", err)
			} else if !pending {
				fmt.Fprintln(stdout, "No interrupted task to resume.")
			} else {
				fmt.Fprintln(stderr, "Warning: recovery may repeat a model request or tool call.")
				response, callErr, interrupted := respondCoreWithKeys(client, workspace, func(callCtx context.Context) (json.RawMessage, error) { return client.ResumeTask(callCtx, sessionID) }, watchKeys, stderr)
				if err := printCoreTaskResult(stdout, stderr, response, callErr, interrupted); err != nil {
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
			pending, err := client.HasPending(ctx, sessionID)
			if err != nil {
				fmt.Fprintf(stderr, "error: inspect pending task: %v\n", err)
			} else if pending {
				fmt.Fprintln(stderr, "An interrupted task is pending. Use /resume or /discard first.")
			} else {
				input, err := core.ParseTaskInput(line)
				if err != nil {
					fmt.Fprintf(stderr, "error: %v\n", err)
				} else {
					response, callErr, interrupted := respondCoreWithKeys(client, workspace, func(callCtx context.Context) (json.RawMessage, error) {
						return client.RunTask(callCtx, core.TaskRequest{SessionID: sessionID, Input: input})
					}, watchKeys, stderr)
					if err := printCoreTaskResult(stdout, stderr, response, callErr, interrupted); err != nil {
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

func respondCoreWithKeys(client cliCore, workspace string, operation coreOperation, watchKeys keyEventWatcher, stderr io.Writer) (json.RawMessage, error, bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var keyEvents <-chan cliInputEvent
	stopWatching := func() {}
	noInteractiveReason := atomic.Value{}
	noInteractiveReason.Store("stdin is not an interactive terminal")
	if watchKeys != nil {
		var err error
		keyEvents, stopWatching, err = watchKeys()
		if stopWatching == nil {
			stopWatching = func() {}
		}
		if err != nil {
			fmt.Fprintf(stderr, "warning: keyboard input unavailable: %v\n", err)
			keyEvents = nil
			noInteractiveReason.Store("keyboard input is unavailable")
		}
	}
	defer stopWatching()

	interactive := atomic.Bool{}
	interactive.Store(keyEvents != nil)
	approvalEvents := make(chan core.ApprovalRequest, 1)
	client.SetEventHandler(func(event core.Event) {
		switch event.Kind {
		case core.EventContextCompaction:
			if event.Compaction != nil {
				writeCLICompactionStatus(stderr, *event.Compaction)
			}
		case core.EventApprovalRequired:
			if event.Approval == nil {
				return
			}
			request := *event.Approval
			if !interactive.Load() {
				denyCLIApproval(client, request, workspace, noInteractiveReason.Load().(string), stderr)
				return
			}
			select {
			case approvalEvents <- request:
			case <-ctx.Done():
				_ = client.ResolveApproval(request.ID, false)
			}
		}
	})
	defer client.SetEventHandler(nil)

	if keyEvents == nil {
		response, err := operation(ctx)
		return response, err, false
	}
	if _, err := fmt.Fprintln(stderr, "Working... Press Esc to cancel."); err != nil {
		return nil, err, false
	}
	type result struct {
		response json.RawMessage
		err      error
	}
	done := make(chan result, 1)
	go func() { response, err := operation(ctx); done <- result{response: response, err: err} }()
	for {
		select {
		case result := <-done:
			return result.response, result.err, false
		case request := <-approvalEvents:
			if !interactive.Load() {
				denyCLIApproval(client, request, workspace, noInteractiveReason.Load().(string), stderr)
				continue
			}
			if _, err := fmt.Fprintln(stderr, formatCLIToolApproval(request, workspace)); err != nil {
				cancel()
				result := <-done
				return result.response, err, true
			}
			approvalAnswered := false
			for !approvalAnswered {
				select {
				case result := <-done:
					return result.response, result.err, false
				case event, ok := <-keyEvents:
					if !ok || event.err != nil {
						keyEvents = nil
						interactive.Store(false)
						noInteractiveReason.Store("keyboard input became unavailable")
						denyCLIApproval(client, request, workspace, noInteractiveReason.Load().(string), stderr)
						approvalAnswered = true
						continue
					}
					if event.escape {
						cancel()
						slog.Warn("task interrupted by user", "source", "escape")
						result := <-done
						return result.response, result.err, true
					}
					switch unicode.ToLower(event.rune) {
					case 'y':
						resolveCLIApproval(client, request, true, stderr)
						approvalAnswered = true
					case 'n':
						resolveCLIApproval(client, request, false, stderr)
						approvalAnswered = true
					}
				}
			}
		case event, ok := <-keyEvents:
			if !ok || event.err != nil {
				keyEvents = nil
				interactive.Store(false)
				noInteractiveReason.Store("keyboard input became unavailable")
				fmt.Fprintln(stderr, "warning: keyboard input ended; pending approvals will be denied")
				continue
			}
			if event.escape {
				cancel()
				slog.Warn("task interrupted by user", "source", "escape")
				result := <-done
				return result.response, result.err, true
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

func resolveCLIApproval(client cliCore, request core.ApprovalRequest, allowed bool, stderr io.Writer) {
	if err := client.ResolveApproval(request.ID, allowed); err != nil {
		fmt.Fprintln(stderr, "Approval request is no longer pending; it was not approved.")
		return
	}
	if allowed {
		fmt.Fprintln(stderr, "Approved this tool call once.")
	} else {
		fmt.Fprintln(stderr, "Denied this tool call.")
	}
}

func denyCLIApproval(client cliCore, request core.ApprovalRequest, workspace, reason string, stderr io.Writer) {
	fmt.Fprintf(stderr, "Approval required for %s; denied because %s. Run in an interactive terminal to approve, or explicitly set /permissions full-access.\n",
		cliApprovalSummary(request, workspace),
		reason,
	)
	if err := client.ResolveApproval(request.ID, false); err != nil {
		fmt.Fprintln(stderr, "Approval request is no longer pending.")
	}
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
		warning = "\nApproved shell commands run as your user and can access files outside the workspace."
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
