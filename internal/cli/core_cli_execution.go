package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/afterlune/lune/internal/assistanttext"
	"github.com/afterlune/lune/internal/terminaltext"
	core "github.com/afterlune/luneCore"
)

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
	var stopStream context.CancelFunc
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
				fmt.Fprintf(stderr, "Task paused for %s. Use /continue to recover it, or /resume <id> to switch sessions.\n", event.Kind)
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
				fmt.Fprintf(stdout, "Waiting %s for the model retry window (Esc cancels)...\n", delay.Round(time.Second))
				// Watch the keyboard during the wait so Esc can abandon the retry
				// instead of blocking until the window opens.
				startWatching()
				timer := time.NewTimer(delay)
				waiting := true
				for waiting {
					select {
					case <-timer.C:
						waiting = false
					case key, ok := <-keyEvents:
						if !ok || key.err != nil {
							keyEvents = nil
							continue
						}
						if key.escape {
							timer.Stop()
							stopWatching()
							stopStream()
							fmt.Fprintln(stdout, "Retry cancelled; the task stays paused.")
							return core.TurnResult{ExecutionID: executionID, Status: core.TaskPaused}, core.ErrTaskPaused, false
						}
						// Nothing else is actionable while the retry window is closed.
					}
				}
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
		recommended := -1
		for optionIndex, option := range item.Options {
			fmt.Fprintf(stdout, "  %d. %s", optionIndex+1, cleanCLIInlineText(option.Label))
			if option.Recommended {
				recommended = optionIndex
				fmt.Fprint(stdout, " [Recommended]")
			}
			if option.Description != "" {
				fmt.Fprintf(stdout, " — %s", cleanCLIInlineText(option.Description))
			}
			fmt.Fprintln(stdout)
		}
		for {
			prompt := "Answer with an option number or your own text: "
			if recommended >= 0 {
				prompt = fmt.Sprintf("Answer with an option number or your own text (default: %s): ", cleanCLIInlineText(item.Options[recommended].Label))
			}
			fmt.Fprint(stdout, prompt)
			answer, err := readCLIRecoveryLine(reader)
			if err != nil {
				return core.UserQuestionAnswer{}, err
			}
			if answer == "" && recommended >= 0 {
				answer = item.Options[recommended].Label
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
	if result.Backend == "server" {
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
		if _, err := fmt.Fprintln(stdout, "Task interrupted. Use /continue to recover, or /discard to abandon."); err != nil {
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
