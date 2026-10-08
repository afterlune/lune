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

	"github.com/afterlune/lune/internal/model"
	"github.com/afterlune/lune/internal/tool"
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
	if err := chatLoop(client, bufio.NewReader(os.Stdin), os.Stdout, os.Stderr); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func chatLoop(client responder, reader *bufio.Reader, stdout, stderr io.Writer) error {
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
				response, callErr := client.Respond(context.Background(), input)
				if response != nil {
					if err := printResponse(stdout, response); err != nil {
						return err
					}
				}
				if callErr != nil {
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
