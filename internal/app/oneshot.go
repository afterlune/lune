package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	core "github.com/afterlune/luneCore"
)

// RunOneShot executes a single prompt non-interactively, streaming events to stdout/stderr.
func RunOneShot(client *core.Core, prompt string, flags CLIFlags, stdout, stderr io.Writer) error {
	ctx := context.Background()

	sessionID, err := resolveSessionID(ctx, client, flags.SessionID)
	if err != nil {
		return err
	}

	// Apply model profile if provided
	if flags.ModelProfile != "" {
		if err := applyOneShotModelProfile(ctx, client, sessionID, flags.ModelProfile); err != nil {
			return fmt.Errorf("set model profile: %w", err)
		}
	}

	// Apply permission mode if provided
	if flags.PermissionMode != "" {
		if err := client.SetPermissionMode(flags.PermissionMode); err != nil {
			return fmt.Errorf("set permission mode: %w", err)
		}
	}

	// Apply plan mode if provided
	if flags.PlanMode {
		_ = client.SetPermissionMode(core.PermissionReadOnly)
	}

	// Open session runtime
	runtime, err := client.OpenSessionRuntime(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("open session runtime: %w", err)
	}
	runtime.SetUserInteractionAvailable(false)

	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()

	events, err := runtime.SubscribeEvents(streamCtx, 0)
	if err != nil {
		return fmt.Errorf("subscribe events: %w", err)
	}

	input, err := core.ParseTaskInput(prompt)
	if err != nil {
		return fmt.Errorf("parse prompt input: %w", err)
	}

	turnReq := core.TurnRequest{
		Input:                  input,
		PlanMode:               flags.PlanMode,
		NoInteractiveDecisions: true,
	}

	submission, err := runtime.SubmitTurn(ctx, turnReq, core.StartIfIdle)
	if err != nil {
		return fmt.Errorf("submit task: %w", err)
	}

	type waitResult struct {
		result core.TurnResult
		err    error
	}

	done := make(chan waitResult, 1)
	go func() {
		res, wErr := runtime.WaitTurn(ctx, submission.ExecutionID)
		done <- waitResult{result: res, err: wErr}
	}()

	var streamText strings.Builder
	for {
		select {
		case res := <-done:
			if res.err != nil {
				return res.err
			}
			if res.result.Status == core.TaskFailed {
				if res.result.ModelFailure != nil {
					return fmt.Errorf("task execution failed: %s (code=%s)", res.result.ModelFailure.Category, res.result.ModelFailure.Code)
				}
				return errors.New("task execution failed")
			}
			if res.result.Status == core.TaskPaused {
				return errors.New("task paused for user approval or decision; run interactively with 'lune' or use '--permission full-access'")
			}
			// Print final response if not streamed as deltas
			if streamText.Len() == 0 && len(res.result.Response) > 0 {
				respText, _ := coreExtractText(res.result.Response)
				if respText != "" {
					fmt.Fprintln(stdout, respText)
				}
			} else if streamText.Len() > 0 && !strings.HasSuffix(streamText.String(), "\n") {
				fmt.Fprintln(stdout)
			}
			return nil

		case event, ok := <-events:
			if !ok {
				return errors.New("runtime event stream closed unexpectedly")
			}
			if event.ExecutionID != "" && event.ExecutionID != submission.ExecutionID {
				continue
			}
			switch event.Kind {
			case core.EventModelStream:
				if event.Stream != nil && event.Stream.Type != "reasoning_delta" && event.Stream.Delta != "" {
					streamText.WriteString(event.Stream.Delta)
					fmt.Fprint(stdout, event.Stream.Delta)
				}
			case core.EventToolStarted:
				if !strings.EqualFold(event.Tool.Name, "select_tool_category") {
					fmt.Fprintf(stderr, "• Running %s...\n", event.Tool.Name)
				}
			case core.EventToolFinished:
				if !strings.EqualFold(event.Tool.Name, "select_tool_category") {
					status := "done"
					if event.CommandStatus == core.CommandFailed {
						status = "failed"
					}
					fmt.Fprintf(stderr, "• %s (%s)\n", event.Tool.Name, status)
				}
			case core.EventApprovalRequired:
				if event.Approval != nil {
					fmt.Fprintf(stderr, "warning: tool approval required for %s in non-interactive mode; use --permission full-access to permit execution without prompt\n", event.Approval.Tool)
				}
			}
		}
	}
}

func applyOneShotModelProfile(ctx context.Context, client *core.Core, sessionID, profile string) error {
	saved, err := client.GetSession(ctx, sessionID)
	if err != nil {
		return err
	}
	settings := saved.Settings
	settings.ModelProfile = profile
	return client.SetSessionSettings(ctx, sessionID, settings)
}

func resolveSessionID(ctx context.Context, client *core.Core, requestedID string) (string, error) {
	if requestedID == "" {
		saved, err := client.CreateSession(ctx, json.RawMessage("null"))
		if err != nil {
			return "", fmt.Errorf("create session: %w", err)
		}
		return saved.ID, nil
	}
	if s, err := client.GetSession(ctx, requestedID); err == nil {
		return s.ID, nil
	}
	sessions, err := client.ListSessions(ctx, 100, false)
	if err != nil {
		return "", fmt.Errorf("list sessions: %w", err)
	}
	var matches []core.Session
	for _, s := range sessions {
		if strings.HasPrefix(strings.ToLower(s.ID), strings.ToLower(requestedID)) {
			matches = append(matches, s)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("session not found: %q", requestedID)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("session prefix %q is ambiguous (%d matches)", requestedID, len(matches))
	}
	return matches[0].ID, nil
}

func coreExtractText(response json.RawMessage) (string, error) {
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
			continue
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
