package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/afterlune/lune/internal/model"
	"github.com/afterlune/lune/internal/tool"
	"github.com/eiannone/keyboard"
)

type responder interface {
	Respond(context.Context, *model.ResponseInput) (*model.Response, error)
}

func main() {
	registry, err := tool.NewDefaultRegistry(".")
	if err != nil {
		fatal(err)
	}
	client, err := model.NewClient(registry)
	if err != nil {
		fatal(err)
	}
	if err := chatLoopWithEscape(client, bufio.NewReader(os.Stdin), os.Stdout, os.Stderr, startKeyboardEscapeWatcher); err != nil {
		fatal(err)
	}
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
		if line != "" {
			input, err := parseInputLine(line)
			if err != nil {
				if _, writeErr := fmt.Fprintf(stderr, "error: %v\n", err); writeErr != nil {
					return fmt.Errorf("write error: %w", writeErr)
				}
			} else {
				response, callErr, interrupted := respondWithEscape(client, input, watchEscape, stderr)
				if response != nil {
					if err := printResponse(stdout, response); err != nil {
						return err
					}
				}
				if interrupted {
					if _, writeErr := fmt.Fprintln(stdout, "Task interrupted."); writeErr != nil {
						return fmt.Errorf("write interruption status: %w", writeErr)
					}
				}
				if callErr != nil && !(interrupted && errors.Is(callErr, context.Canceled)) {
					if _, writeErr := fmt.Fprintf(stderr, "error: %v\n", callErr); writeErr != nil {
						return fmt.Errorf("write error: %w", writeErr)
					}
				}
			}
		}
		if errors.Is(readErr, io.EOF) {
			return nil
		}
	}
}

func respondWithEscape(client responder, input *model.ResponseInput, watchEscape escapeWatcher, stderr io.Writer) (*model.Response, error, bool) {
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
		response, err := client.Respond(ctx, input)
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
		response, err := client.Respond(ctx, input)
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
		result := <-done
		return result.response, result.err, true
	}
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
