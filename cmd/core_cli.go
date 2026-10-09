package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/afterlune/lune/core"
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
	PermissionMode() core.PermissionMode
	SetPermissionMode(core.PermissionMode) error
}

type escapeWatcher func() (<-chan struct{}, func(), error)

func runCoreChatLoop(client cliCore, reader *bufio.Reader, stdout, stderr io.Writer, watchEscape escapeWatcher) error {
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
		case line == "/exit":
			return nil
		case line == "/new":
			saved, err = client.CreateSession(ctx, json.RawMessage("null"))
			if err != nil {
				fmt.Fprintf(stderr, "error: create session: %v\n", err)
			} else {
				sessionID = saved.ID
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
				response, callErr, interrupted := respondCoreWithEscape(func(callCtx context.Context) (json.RawMessage, error) { return client.ResumeTask(callCtx, sessionID) }, watchEscape, stderr)
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
			fmt.Fprintf(stderr, "Unknown command %q. Available commands: /compact /discard /exit /new /permissions /resume\n", line)
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
					response, callErr, interrupted := respondCoreWithEscape(func(callCtx context.Context) (json.RawMessage, error) {
						return client.RunTask(callCtx, core.TaskRequest{SessionID: sessionID, Input: input})
					}, watchEscape, stderr)
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

func respondCoreWithEscape(operation coreOperation, watchEscape escapeWatcher, stderr io.Writer) (json.RawMessage, error, bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var escape <-chan struct{}
	stopWatching := func() {}
	if watchEscape != nil {
		var err error
		escape, stopWatching, err = watchEscape()
		if stopWatching == nil {
			stopWatching = func() {}
		}
		if err != nil {
			fmt.Fprintf(stderr, "warning: Esc cancellation unavailable: %v\n", err)
			escape = nil
		}
	}
	defer stopWatching()
	if escape == nil {
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
	select {
	case result := <-done:
		return result.response, result.err, false
	case <-escape:
		select {
		case result := <-done:
			return result.response, result.err, false
		default:
		}
		cancel()
		slog.Warn("task interrupted by user", "source", "escape")
		result := <-done
		return result.response, result.err, true
	}
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
