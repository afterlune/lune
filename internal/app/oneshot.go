package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/afterlune/lune/internal/filemention"
	"github.com/afterlune/lune/internal/terminaltext"
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
		if err := client.SetPermissionMode(core.PermissionReadOnly); err != nil {
			return fmt.Errorf("set Plan mode permissions: %w", err)
		}
	}

	// Open session runtime
	runtime, err := client.OpenSessionRuntime(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("open session runtime: %w", err)
	}
	defer runtime.Close()
	runtime.SetUserInteractionAvailable(false)

	streamCtx, stopStream := context.WithCancel(ctx)
	defer stopStream()

	events, err := runtime.SubscribeEvents(streamCtx, 0)
	if err != nil {
		return fmt.Errorf("subscribe events: %w", err)
	}

	_, augmented := filemention.Expand(prompt, client.Workspace())
	input, err := core.ParseTaskInput(augmented)
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
		if errors.Is(err, core.ErrTaskPaused) {
			return oneShotPausedTaskError(sessionID)
		}
		return fmt.Errorf("submit task: %w", err)
	}

	return awaitOneShotTurn(ctx, runtime, sessionID, submission.ExecutionID, events, stopStream, stdout, stderr)
}

// Keep the completion loop independent of session setup so event/result races
// and slow output can be exercised deterministically.
type oneShotTurnRuntime interface {
	WaitTurn(context.Context, string) (core.TurnResult, error)
	Interrupt(string) bool
}

func awaitOneShotTurn(ctx context.Context, runtime oneShotTurnRuntime, sessionID, executionID string, events <-chan core.Event, stopStream context.CancelFunc, stdout, stderr io.Writer) error {
	waitCtx, cancelWait := context.WithCancel(ctx)
	defer cancelWait()
	type waitResult struct {
		result core.TurnResult
		err    error
	}
	done := make(chan waitResult, 1)
	go func() {
		res, err := runtime.WaitTurn(waitCtx, executionID)
		done <- waitResult{res, err}
	}()
	abort := func(err error) error {
		// Release blocked stream producers before joining the interrupted turn.
		stopStream()
		runtime.Interrupt(executionID)
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, cleanupErr := runtime.WaitTurn(cleanupCtx, executionID)
		return errors.Join(err, cleanupErr)
	}
	writeText := func(text string) error {
		n, err := io.WriteString(stdout, text)
		if err == nil && n != len(text) {
			return io.ErrShortWrite
		}
		return err
	}
	var result core.TurnResult
	var streamedText strings.Builder
	var streamRequestID string
	haveResult, finished, streamed, endsWithNewline := false, false, false, false
	for !haveResult || !finished {
		select {
		case res := <-done:
			if res.err != nil {
				return abort(res.err)
			}
			result, haveResult, done = res.result, true, nil
		case event, ok := <-events:
			if !ok {
				if !finished {
					return abort(errors.New("runtime event stream closed before task completion"))
				}
				events = nil
				continue
			}
			if event.ExecutionID != "" && event.ExecutionID != executionID {
				continue
			}
			var writeErr error
			switch event.Kind {
			case core.EventRuntimeStreamFailed:
				return abort(fmt.Errorf("runtime event stream failed: %s", event.Err))
			case core.EventTaskFinished:
				if event.ExecutionID == executionID {
					finished = true
				}
			case core.EventModelStream:
				if !oneShotAssistantDelta(event) {
					continue
				}
				if requestID := event.Stream.RequestID; requestID != "" && requestID != streamRequestID {
					streamRequestID = requestID
					streamedText.Reset()
				}
				delta := terminaltext.Sanitize(event.Stream.Delta)
				if delta != "" {
					writeErr = writeText(delta)
					streamedText.WriteString(delta)
					streamed, endsWithNewline = true, strings.HasSuffix(delta, "\n")
				}
			case core.EventToolStarted:
				if !strings.EqualFold(event.Tool.Name, "select_tool_category") {
					_, writeErr = fmt.Fprintf(stderr, "• Running %s...\n", terminaltext.Sanitize(event.Tool.Name))
				}
			case core.EventToolFinished:
				if !strings.EqualFold(event.Tool.Name, "select_tool_category") {
					status := "done"
					if event.CommandStatus == core.CommandFailed {
						status = "failed"
					}
					_, writeErr = fmt.Fprintf(stderr, "• %s (%s)\n", terminaltext.Sanitize(event.Tool.Name), status)
				}
			case core.EventApprovalRequired:
				if event.Approval != nil {
					_, writeErr = fmt.Fprintf(stderr, "warning: tool approval required for %s in non-interactive mode; if the task pauses, continue with `lune --session %s --cli` and use /continue\n", terminaltext.Sanitize(event.Approval.Tool), terminaltext.Sanitize(sessionID))
				}
			}
			if writeErr != nil {
				return abort(fmt.Errorf("write task output: %w", writeErr))
			}
		case <-ctx.Done():
			return abort(ctx.Err())
		}
	}
	if result.Status == core.TaskPaused {
		return oneShotPausedTaskError(sessionID)
	}
	if result.Status == core.TaskFailed {
		if result.ModelFailure != nil {
			return fmt.Errorf("task execution failed: %s (code=%s)", result.ModelFailure.Category, result.ModelFailure.Code)
		}
		return errors.New("task execution failed")
	}
	if result.Status != core.TaskCompleted {
		return fmt.Errorf("task ended with status %s", result.Status)
	}
	if len(result.Response) > 0 {
		text, err := coreExtractText(result.Response)
		if err != nil {
			return err
		}
		text = terminaltext.Sanitize(text)
		if text != "" && !streamed {
			return writeText(text + "\n")
		}
		// Core's live deltas are best effort. Complete a streamed prefix from the
		// authoritative response, or print the full answer if a gap cannot be
		// repaired in output that has already been written.
		if text != "" && !strings.HasSuffix(streamedText.String(), text) {
			if strings.HasPrefix(text, streamedText.String()) {
				if err := writeText(text[streamedText.Len():]); err != nil {
					return err
				}
			} else {
				if _, err := fmt.Fprintln(stderr, "warning: streamed output was incomplete; the complete answer follows."); err != nil {
					return err
				}
				if err := writeText("\n" + text); err != nil {
					return err
				}
			}
			endsWithNewline = strings.HasSuffix(text, "\n")
		}
	}
	if streamed && !endsWithNewline {
		return writeText("\n")
	}
	return nil
}

func oneShotAssistantDelta(event core.Event) bool {
	if event.Stream == nil || event.Stream.Type != "text_delta" || event.Stream.Delta == "" {
		return false
	}
	agent := strings.TrimSpace(event.AgentID)
	purpose := strings.TrimSpace(event.Stream.Purpose)
	return (agent == "" || agent == "lead") && (purpose == "" || purpose == string(core.UsagePurposeMain))
}

func oneShotPausedTaskError(sessionID string) error {
	return fmt.Errorf("task paused for approval or a decision in session %s; continue with `lune --session %s --cli` and use /continue", sessionID, sessionID)
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
