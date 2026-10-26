package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/afterlune/lune/internal/assistanttext"
	"github.com/afterlune/lune/internal/terminaltext"
	core "github.com/afterlune/luneCore"
)

func respondCoreWithKeys(runtime cliRuntime, workspace string, request core.TurnRequest, watchKeys keyEventWatcher, stderr io.Writer) (core.TurnResult, error, bool) {
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
	events, err := runtime.SubscribeEvents(streamCtx, 0)
	defer stopStream()
	if err != nil {
		return core.TurnResult{}, err, false
	}
	runtime.SetUserInteractionAvailable(false)
	request.NoInteractiveDecisions = keyEvents == nil
	submission, err := runtime.SubmitTurn(context.Background(), request, core.StartIfIdle)
	if err != nil {
		return core.TurnResult{}, err, false
	}
	return awaitCoreExecution(runtime, workspace, submission.ExecutionID, events, keyEvents, stderr)
}

func awaitCoreExecution(runtime cliRuntime, workspace, executionID string, events <-chan core.Event, keyEvents <-chan cliInputEvent, stderr io.Writer) (core.TurnResult, error, bool) {
	progress := &cliProgressWriter{writer: stderr, errors: make(chan error, 1)}
	stderr = progress
	if keyEvents != nil {
		if _, err := fmt.Fprintln(stderr, "Working... Press Esc to interrupt."); err != nil {
			return core.TurnResult{}, err, false
		}
	}
	type waitResult struct {
		result core.TurnResult
		err    error
	}
	done := make(chan waitResult, 1)
	waitCtx, cancelWait := context.WithCancel(context.Background())
	defer cancelWait()
	go func() {
		result, waitErr := runtime.WaitTurn(waitCtx, executionID)
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
			case core.EventRuntimeStreamFailed:
				reason := cleanCLIInlineText(event.Err)
				if reason == "" {
					reason = "unknown stream error"
				}
				return core.TurnResult{}, fmt.Errorf("runtime event stream failed: %s", reason), interrupted
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
				fmt.Fprintf(stderr, "Task paused for %s. Send a follow-up message to continue the conversation; it starts a new turn.\n", event.Kind)
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
		case writeErr := <-progress.errors:
			return core.TurnResult{}, fmt.Errorf("write CLI progress: %w", writeErr), interrupted
		}
	}
}

type cliProgressWriter struct {
	writer io.Writer
	errors chan error
}

func (w *cliProgressWriter) Write(data []byte) (int, error) {
	if w.writer == nil {
		w.writer = io.Discard
	}
	n, err := w.writer.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		select {
		case w.errors <- err:
		default:
		}
	}
	return n, err
}

func writeCLICompactionStatus(stderr io.Writer, event core.ContextCompactionDetails) {
	switch event.Stage {
	case "started":
		_, _ = fmt.Fprintln(stderr, "Compacting context...")
	case "completed":
		if event.Mode == "server" {
			if event.BeforeTokens > 0 {
				_, _ = fmt.Fprintf(stderr, "Provider compacted the context (previous input %s tokens).\n", compactCLITokens(event.BeforeTokens))
			} else {
				_, _ = fmt.Fprintln(stderr, "Provider compacted the context.")
			}
		} else if event.AfterTokens > 0 {
			_, _ = fmt.Fprintf(stderr, "Context compacted (estimated input %s to %s tokens).\n", compactCLITokens(event.BeforeTokens), compactCLITokens(event.AfterTokens))
		} else {
			_, _ = fmt.Fprintln(stderr, "Context compacted locally.")
		}
	case "fallback":
		message := "Server compaction unavailable; using local compaction"
		if event.Mode == "local" {
			message = "Local compaction did not reach its target; keeping the best summary"
		}
		if reason := compactFallbackReason(event.Err); reason != "" {
			message += ": " + reason
		}
		_, _ = fmt.Fprintln(stderr, message+".")
	case "skipped":
		if event.Err != "" {
			_, _ = fmt.Fprintf(stderr, "Automatic compaction skipped: %s\n", event.Err)
		}
	case "failed":
		_, _ = fmt.Fprintf(stderr, "Context compaction failed: %s\n", event.Err)
	}
}

func formatCLIManualCompactionResult(result core.CompactionResult) string {
	if result.Mode == "compact" {
		if result.Before > 0 {
			return fmt.Sprintf("Compacted %d older user turn(s) with provider compaction (previous input %s tokens).",
				result.Turns, compactCLITokens(result.Before))
		}
		return fmt.Sprintf("Compacted %d older user turn(s) with provider compaction.", result.Turns)
	}
	message := fmt.Sprintf("Compacted %d older user turn(s) locally", result.Turns)
	if result.Before > 0 && result.After > 0 {
		message += fmt.Sprintf(": %s → %s tokens.", compactCLITokens(result.Before), compactCLITokens(result.After))
	} else {
		message += "."
	}
	if result.Fallback {
		if reason := compactFallbackReason(result.FallbackReason); reason != "" {
			message += " Fallback: " + reason + "."
		} else {
			message += " Fallback was applied."
		}
	}
	return message
}

func compactFallbackReason(reason string) string {
	reason = strings.Join(strings.Fields(terminaltext.Sanitize(reason)), " ")
	runes := []rune(reason)
	if len(runes) > 160 {
		return string(runes[:157]) + "..."
	}
	return strings.TrimSuffix(reason, ".")
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

func printCoreTaskResult(stdout, stderr io.Writer, response json.RawMessage, callErr error, interrupted bool) error {
	if len(response) > 0 {
		text, err := coreResponseText(response)
		if err != nil {
			return err
		}
		if text != "" {
			if _, err := fmt.Fprintln(stdout, terminaltext.Sanitize(text)); err != nil {
				return err
			}
		}
	}
	if interrupted {
		if _, err := fmt.Fprintln(stdout, "Task interrupted. Send a follow-up message to continue the conversation in a new turn."); err != nil {
			return err
		}
	}
	if callErr != nil && !(interrupted && errors.Is(callErr, context.Canceled)) {
		if _, err := fmt.Fprintf(stderr, "error: %s\n", cleanCLIInlineText(callErr.Error())); err != nil {
			return err
		}
	}
	return nil
}

func coreResponseText(response json.RawMessage) (string, error) {
	text, err := assistanttext.Extract(response)
	if err != nil {
		return "", fmt.Errorf("decode response output: %w", err)
	}
	return text, nil
}
