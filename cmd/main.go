package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/afterlune/lune/internal/agentgraph"
	"github.com/afterlune/lune/internal/model"
	"github.com/afterlune/lune/internal/tool"
	"github.com/afterlune/luneGraph/checkpoint"
	"github.com/afterlune/luneGraph/checkpoint/sqlite"
	"github.com/eiannone/keyboard"
)

type responder interface {
	Respond(context.Context, *model.ResponseInput) (*model.Response, error)
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(); err != nil {
		fatal(err)
	}
}

func run() (runErr error) {
	registry, err := tool.NewDefaultRegistry(".")
	if err != nil {
		return err
	}
	client, err := model.NewClient(registry)
	if err != nil {
		return err
	}
	workspace, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}
	stateDir := filepath.Join(workspace, ".luneagent")
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return fmt.Errorf("create agent state directory: %w", err)
	}
	store, err := sqlite.Open[agentgraph.State](context.Background(), filepath.Join(stateDir, "checkpoints.db"), checkpoint.JSON[agentgraph.State]{})
	if err != nil {
		return fmt.Errorf("open agent checkpoints: %w", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close agent checkpoints: %w", err))
		}
	}()
	requester := &usageTrackingRequester{inner: client}
	agent, err := agentgraph.New(requester, registry)
	if err != nil {
		return err
	}
	runtime := &graphResponder{agent: agent, store: store, requester: requester}
	if useTUI(os.Stdin, os.Stdout) {
		logFile, err := os.OpenFile(filepath.Join(stateDir, "agent.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("open TUI log: %w", err)
		}
		defer logFile.Close()
		previousLogger := slog.Default()
		defer slog.SetDefault(previousLogger)
		slog.SetDefault(slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo})))
		return runTUI(runtime, registry)
	}
	return chatLoopWithEscape(runtime, bufio.NewReader(os.Stdin), os.Stdout, os.Stderr, startKeyboardEscapeWatcher)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func chatLoop(client responder, reader *bufio.Reader, stdout, stderr io.Writer) error {
	return chatLoopWithEscape(client, reader, stdout, stderr, nil)
}

type escapeWatcher func() (<-chan struct{}, func(), error)

func chatLoopWithEscape(client responder, reader *bufio.Reader, stdout, stderr io.Writer, watchEscape escapeWatcher) error {
	for {
		if _, err := fmt.Fprint(stdout, "> "); err != nil {
			return fmt.Errorf("write prompt: %w", err)
		}
		line, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("read input: %w", readErr)
		}
		line = strings.TrimSpace(line)
		if line == "/exit" {
			return nil
		}
		switch line {
		case "/resume":
			runtime, ok := client.(resumableResponder)
			if !ok {
				if _, err := fmt.Fprintln(stderr, "error: task recovery is unavailable"); err != nil {
					return fmt.Errorf("write error: %w", err)
				}
				break
			}
			pending, err := runtime.HasPending()
			if err != nil {
				if _, writeErr := fmt.Fprintf(stderr, "error: inspect pending task: %v\n", err); writeErr != nil {
					return fmt.Errorf("write error: %w", writeErr)
				}
				break
			}
			if !pending {
				if _, err := fmt.Fprintln(stdout, "No interrupted task to resume."); err != nil {
					return fmt.Errorf("write status: %w", err)
				}
				break
			}
			if _, err := fmt.Fprintln(stderr, "Warning: recovery may repeat a model request or tool call."); err != nil {
				return fmt.Errorf("write warning: %w", err)
			}
			response, callErr, interrupted := respondWithEscapeOperation(runtime.Resume, watchEscape, stderr)
			if err := printTaskResult(stdout, stderr, response, callErr, interrupted); err != nil {
				return err
			}
		case "/discard":
			runtime, ok := client.(resumableResponder)
			if !ok {
				if _, err := fmt.Fprintln(stderr, "error: task recovery is unavailable"); err != nil {
					return fmt.Errorf("write error: %w", err)
				}
				break
			}
			discarded, err := runtime.Discard(context.Background())
			if err != nil {
				if _, writeErr := fmt.Fprintf(stderr, "error: discard task: %v\n", err); writeErr != nil {
					return fmt.Errorf("write error: %w", writeErr)
				}
				break
			}
			message := "No interrupted task to discard."
			if discarded {
				message = "Interrupted task discarded."
			}
			if _, err := fmt.Fprintln(stdout, message); err != nil {
				return fmt.Errorf("write status: %w", err)
			}
		case "":
			// Empty lines only display the next prompt.
		default:
			if runtime, ok := client.(resumableResponder); ok {
				pending, err := runtime.HasPending()
				if err != nil {
					if _, writeErr := fmt.Fprintf(stderr, "error: inspect pending task: %v\n", err); writeErr != nil {
						return fmt.Errorf("write error: %w", writeErr)
					}
					break
				}
				if pending {
					if _, err := fmt.Fprintln(stderr, "An interrupted task is pending. Use /resume or /discard first."); err != nil {
						return fmt.Errorf("write error: %w", err)
					}
					break
				}
			}
			input, err := parseInputLine(line)
			if err != nil {
				if _, writeErr := fmt.Fprintf(stderr, "error: %v\n", err); writeErr != nil {
					return fmt.Errorf("write error: %w", writeErr)
				}
			} else {
				response, callErr, interrupted := respondWithEscape(client, input, watchEscape, stderr)
				if err := printTaskResult(stdout, stderr, response, callErr, interrupted); err != nil {
					return err
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
	}
}

func respondWithEscape(client responder, input *model.ResponseInput, watchEscape escapeWatcher, stderr io.Writer) (*model.Response, error, bool) {
	return respondWithEscapeOperation(func(ctx context.Context) (*model.Response, error) {
		return client.Respond(ctx, input)
	}, watchEscape, stderr)
}

func respondWithEscapeOperation(operation func(context.Context) (*model.Response, error), watchEscape escapeWatcher, stderr io.Writer) (*model.Response, error, bool) {
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
			if _, writeErr := fmt.Fprintf(stderr, "warning: Esc cancellation unavailable: %v\n", err); writeErr != nil {
				return nil, fmt.Errorf("write warning: %w", writeErr), false
			}
			escape = nil
		}
	}
	defer stopWatching()

	if escape == nil {
		response, err := operation(ctx)
		return response, err, false
	}
	if _, err := fmt.Fprintln(stderr, "Working... Press Esc to cancel."); err != nil {
		return nil, fmt.Errorf("write task status: %w", err), false
	}

	type result struct {
		response *model.Response
		err      error
	}
	done := make(chan result, 1)
	go func() {
		response, err := operation(ctx)
		done <- result{response: response, err: err}
	}()

	select {
	case result := <-done:
		return result.response, result.err, false
	case <-escape:
		// Prefer a result that completed at the same time as the key event.
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

func printTaskResult(stdout, stderr io.Writer, response *model.Response, callErr error, interrupted bool) error {
	if response != nil {
		if err := printResponse(stdout, response); err != nil {
			return err
		}
	}
	if interrupted {
		if _, err := fmt.Fprintln(stdout, "Task interrupted. Use /resume to continue, or /discard to abandon."); err != nil {
			return fmt.Errorf("write interruption status: %w", err)
		}
	}
	if callErr != nil && !(interrupted && errors.Is(callErr, context.Canceled)) {
		if _, err := fmt.Fprintf(stderr, "error: %v\n", callErr); err != nil {
			return fmt.Errorf("write error: %w", err)
		}
	}
	return nil
}

func startKeyboardEscapeWatcher() (<-chan struct{}, func(), error) {
	info, err := os.Stdin.Stat()
	if err != nil {
		return nil, func() {}, fmt.Errorf("inspect stdin: %w", err)
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return nil, func() {}, nil
	}
	keys, err := keyboard.GetKeys(10)
	if err != nil {
		return nil, func() {}, err
	}

	escape := make(chan struct{}, 1)
	watcherDone := make(chan struct{})
	go func() {
		defer close(watcherDone)
		for event := range keys {
			if event.Key == keyboard.KeyEsc {
				escape <- struct{}{}
				return
			}
		}
	}()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = keyboard.Close()
			<-watcherDone
		})
	}
	return escape, stop, nil
}

func parseInputLine(line string) (*model.ResponseInput, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, errors.New("input is empty")
	}
	if json.Valid([]byte(line)) {
		var value any
		if err := json.Unmarshal([]byte(line), &value); err == nil {
			switch value.(type) {
			case string, []any:
				return model.NewResponseInput(json.RawMessage(line))
			}
		}
	}
	return model.NewResponseInput(line)
}

func printResponse(w io.Writer, response *model.Response) error {
	for _, item := range response.Output {
		message, ok := item.(model.OutputMessage)
		if !ok {
			if _, isReasoning := item.(model.OutputReasoning); isReasoning {
				continue
			}
			if err := printJSON(w, item); err != nil {
				return err
			}
			continue
		}

		var messageText strings.Builder
		for _, part := range message.Content {
			if text, ok := part.(model.OutputText); ok {
				messageText.WriteString(text.Text)
				continue
			}
			if err := printJSON(w, part); err != nil {
				return err
			}
		}
		if messageText.Len() > 0 {
			if _, err := fmt.Fprintln(w, messageText.String()); err != nil {
				return fmt.Errorf("write response: %w", err)
			}
		}
	}
	return nil
}

func printJSON(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode response output: %w", err)
	}
	if _, err := fmt.Fprintln(w, string(data)); err != nil {
		return fmt.Errorf("write response: %w", err)
	}
	return nil
}
