package model

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	DeepSeekFlashModel = "deepseek-flash"

	deepSeekResponsesEndpoint = "https://api.deepseek.com/responses"
	configFile                = "config.json"
	requestTimeout            = 2 * time.Minute
	maxResponseBodyBytes      = 8 * 1024 * 1024
)

var ErrToolCallLimit = errors.New("maximum tool-call rounds reached")

// RespondOptions configures one call to RespondWithOptions.
type RespondOptions struct {
	// MaxToolRounds limits batches of tool calls. Zero means unlimited.
	MaxToolRounds int
}

// ToolExecutor is implemented by the tool registry. Keeping this interface in
// model avoids importing the tool package and creating an import cycle.
type ToolExecutor interface {
	Definitions() []ResponseTool
	ExecuteCall(context.Context, OutputFunctionCall) InputFunctionCallOutput
}

// ToolCategory describes a group of tools that can be revealed on demand.
type ToolCategory struct {
	Name        string
	Description string
}

// CategorizedToolExecutor optionally exposes tools one category at a time.
// Executors that only implement ToolExecutor retain the original behavior.
type CategorizedToolExecutor interface {
	ToolExecutor
	Categories() []ToolCategory
	DefinitionsForCategory(string) []ResponseTool
}

const categorySelectorName = "select_tool_category"

type Client struct {
	mu         sync.Mutex
	apiKey     string
	endpoint   string
	httpClient *http.Client
	tools      ToolExecutor
	history    []InputItem
}

type clientConfig struct {
	APIKey string `json:"apikey"`
}

// NewResponseInput encodes any JSON-serializable Responses input value. Pass a
// string for simple text or an item slice for structured and multimodal input.
func NewResponseInput[T any](value T) (*ResponseInput, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode input: %w", err)
	}
	var input ResponseInput
	if err := json.Unmarshal(data, &input); err != nil {
		return nil, fmt.Errorf("decode input: %w", err)
	}
	if input.Text == nil && input.Items == nil {
		return nil, errors.New("input must be a string or an item list")
	}
	return &input, nil
}

// NewClient reads apikey from config.json in the current working directory.
func NewClient(tools ToolExecutor) (*Client, error) {
	if tools == nil {
		return nil, errors.New("tool executor is required")
	}
	apiKey, err := readAPIKey(configFile)
	if err != nil {
		return nil, err
	}
	return newClient(apiKey, deepSeekResponsesEndpoint, &http.Client{Timeout: requestTimeout}, tools), nil
}

// Respond sends input to DeepSeek and runs any returned function calls without
// imposing a tool-call round limit. It keeps conversation history on this
// Client, so use one Client per conversation.
func (c *Client) Respond(ctx context.Context, input *ResponseInput) (*Response, error) {
	return c.RespondWithOptions(ctx, input, RespondOptions{})
}

// RespondWithOptions sends input to DeepSeek with per-call execution options.
// A positive MaxToolRounds limits batches of tool calls; zero means unlimited.
func (c *Client) RespondWithOptions(ctx context.Context, input *ResponseInput, options RespondOptions) (*Response, error) {
	if c == nil {
		return nil, errors.New("model client is nil")
	}
	if input == nil {
		return nil, errors.New("input is nil")
	}
	if c.tools == nil {
		return nil, errors.New("tool executor is required")
	}
	if options.MaxToolRounds < 0 {
		return nil, errors.New("MaxToolRounds must not be negative")
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	newItems, err := initialInputHistory(input)
	if err != nil {
		return nil, err
	}
	history := append(append([]InputItem(nil), c.history...), newItems...)

	categorized, isCategorized := c.tools.(CategorizedToolExecutor)
	var categories []ToolCategory
	if isCategorized {
		categories = categorized.Categories()
	}
	if len(categories) == 0 {
		isCategorized = false
	}
	selectedCategory := ""

	toolRounds := 0
	requestNumber := 0
	for {
		requestNumber++
		requestStarted := time.Now()
		slog.Info("model request started",
			"model", DeepSeekFlashModel,
			"request", requestNumber,
			"tool_rounds_completed", toolRounds,
		)
		var visibleTools []ResponseTool
		if isCategorized {
			selector, err := categorySelectorDefinition(categories)
			if err != nil {
				return nil, fmt.Errorf("build category selector: %w", err)
			}
			visibleTools = []ResponseTool{selector}
			if selectedCategory != "" {
				visibleTools = append(visibleTools, categorized.DefinitionsForCategory(selectedCategory)...)
			}
		} else {
			visibleTools = c.tools.Definitions()
		}
		request := ResponseRequest{
			Model: DeepSeekFlashModel,
			Input: NewResponseInputItems(history...),
			Tools: visibleTools,
		}
		response, err := c.createResponse(ctx, request)
		if err != nil {
			slog.Warn("model request failed",
				"model", DeepSeekFlashModel,
				"request", requestNumber,
				"duration", time.Since(requestStarted),
				"canceled", ctx.Err() != nil,
				"error_type", fmt.Sprintf("%T", err),
			)
			if ctx.Err() != nil {
				c.saveInterruptedHistory(history, "The task was interrupted while waiting for the model response.")
				return nil, ctx.Err()
			}
			return nil, err
		}
		calls := functionCalls(response.Output)
		slog.Info("model response received",
			"model", DeepSeekFlashModel,
			"request", requestNumber,
			"duration", time.Since(requestStarted),
			"status", response.Status,
			"tool_calls", len(calls),
		)
		if response.Status == "failed" {
			slog.Warn("model response failed", "model", DeepSeekFlashModel, "request", requestNumber)
			if response.Error != nil {
				return response, fmt.Errorf("DeepSeek response failed (%s): %s", response.Error.Code, response.Error.Message)
			}
			return response, errors.New("DeepSeek response failed")
		}

		if len(calls) == 0 {
			history, err = appendResponseHistory(history, response.Output)
			if err != nil {
				return response, err
			}
			c.history = history
			return response, nil
		}

		// DeepSeek Responses is stateless, so append the whole assistant response
		// and tool outputs to input and resend that history on the next request.
		history, err = appendResponseHistory(history, response.Output)
		if err != nil {
			return response, err
		}
		if options.MaxToolRounds > 0 && toolRounds >= options.MaxToolRounds {
			slog.Warn("tool round limit reached",
				"limit", options.MaxToolRounds,
				"tool_rounds_completed", toolRounds,
				"skipped_tool_calls", len(calls),
			)
			appendSkippedToolCalls(&history, calls, "Skipped because the tool-call round limit was reached.")
			c.saveInterruptedHistory(history, "The task stopped after reaching its tool-call round limit.")
			return response, fmt.Errorf("%w: %d", ErrToolCallLimit, options.MaxToolRounds)
		}

		visibleToolNames := responseToolNames(visibleTools)
		for i, call := range calls {
			if ctx.Err() != nil {
				slog.Warn("tool batch interrupted",
					"tool_round", toolRounds+1,
					"completed_tool_calls", i,
					"skipped_tool_calls", len(calls)-i,
				)
				appendSkippedToolCalls(&history, calls[i:], "Skipped because the task was interrupted before execution.")
				c.saveInterruptedHistory(history, "The task was interrupted while running tools. Completed tool calls and their results are recorded above.")
				return response, ctx.Err()
			}
			if isCategorized && call.Name == categorySelectorName {
				category, err := selectedToolCategory(call.Arguments, categories)
				output := ""
				if err != nil {
					output = "Category was not changed: " + err.Error()
				} else {
					selectedCategory = category
					output = "Category selected. Its tools will be available in the next request."
				}
				history = append(history, InputFunctionCallOutput{
					CallID: call.CallID,
					Output: NewToolOutputText(output),
				})
			} else if _, visible := visibleToolNames[call.Name]; !visible {
				history = append(history, InputFunctionCallOutput{
					CallID: call.CallID,
					Output: NewToolOutputText("Tool is not available in the current request."),
				})
			} else {
				history = append(history, c.tools.ExecuteCall(ctx, call))
			}
			if ctx.Err() != nil {
				slog.Warn("tool batch interrupted",
					"tool_round", toolRounds+1,
					"completed_tool_calls", i+1,
					"skipped_tool_calls", len(calls)-i-1,
				)
				appendSkippedToolCalls(&history, calls[i+1:], "Skipped because the task was interrupted before execution.")
				c.saveInterruptedHistory(history, "The task was interrupted while running tools. Completed tool calls and their results are recorded above.")
				return response, ctx.Err()
			}
		}
		toolRounds++
	}
}

func categorySelectorDefinition(categories []ToolCategory) (FunctionTool, error) {
	names := make([]string, 0, len(categories))
	descriptions := make([]string, 0, len(categories))
	for _, category := range categories {
		names = append(names, category.Name)
		descriptions = append(descriptions, category.Name+": "+category.Description)
	}
	parameters, err := json.Marshal(struct {
		Type                 string   `json:"type"`
		Properties           any      `json:"properties"`
		Required             []string `json:"required"`
		AdditionalProperties bool     `json:"additionalProperties"`
	}{
		Type: "object",
		Properties: map[string]any{
			"category": map[string]any{"type": "string", "enum": names},
		},
		Required:             []string{"category"},
		AdditionalProperties: false,
	})
	if err != nil {
		return FunctionTool{}, err
	}
	strict := true
	return FunctionTool{
		Name:        categorySelectorName,
		Description: "Choose a tool category to reveal its tools. Available categories: " + strings.Join(descriptions, "; "),
		Parameters:  parameters,
		Strict:      &strict,
	}, nil
}

func selectedToolCategory(arguments string, categories []ToolCategory) (string, error) {
	var input struct {
		Category string `json:"category"`
	}
	decoder := json.NewDecoder(strings.NewReader(arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return "", fmt.Errorf("invalid category selection: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return "", errors.New("invalid category selection: expected one JSON object")
	}
	for _, category := range categories {
		if input.Category == category.Name {
			return input.Category, nil
		}
	}
	return "", fmt.Errorf("unknown category %q", input.Category)
}

func responseToolNames(tools []ResponseTool) map[string]struct{} {
	names := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if function, ok := tool.(FunctionTool); ok {
			names[function.Name] = struct{}{}
		}
	}
	return names
}

func appendSkippedToolCalls(history *[]InputItem, calls []OutputFunctionCall, reason string) {
	for _, call := range calls {
		*history = append(*history, InputFunctionCallOutput{
			CallID: call.CallID,
			Output: NewToolOutputText(reason),
		})
	}
}

func (c *Client) saveInterruptedHistory(history []InputItem, message string) {
	history = append(history, InputMessage{
		Role:    RoleAssistant,
		Content: NewMessageText(message),
	})
	c.history = history
}

func newClient(apiKey, endpoint string, httpClient *http.Client, tools ToolExecutor) *Client {
	return &Client{
		apiKey:     apiKey,
		endpoint:   endpoint,
		httpClient: httpClient,
		tools:      tools,
	}
}

func readAPIKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var config clientConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("decode %s: %w", path, err)
	}
	apiKey := strings.TrimSpace(config.APIKey)
	if apiKey == "" {
		return "", fmt.Errorf("%s must contain a non-empty apikey", path)
	}
	return apiKey, nil
}

func (c *Client) createResponse(ctx context.Context, requestBody ResponseRequest) (*Response, error) {
	body, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("encode Responses request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create Responses request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call DeepSeek Responses API: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read DeepSeek response: %w", err)
	}
	if len(responseBody) > maxResponseBodyBytes {
		return nil, fmt.Errorf("DeepSeek response exceeds %d bytes", maxResponseBodyBytes)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(responseBody))
		if message == "" {
			message = response.Status
		}
		return nil, fmt.Errorf("DeepSeek Responses API returned %s: %s", response.Status, message)
	}
	var decoded Response
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return nil, fmt.Errorf("decode DeepSeek response: %w", err)
	}
	return &decoded, nil
}

func initialInputHistory(input *ResponseInput) ([]InputItem, error) {
	if input.Text != nil && input.Items != nil {
		return nil, errors.New("responses input cannot contain both text and items")
	}
	if input.Text != nil {
		return []InputItem{InputMessage{Role: RoleUser, Content: NewMessageText(*input.Text)}}, nil
	}
	if len(input.Items) == 0 {
		return nil, errors.New("input item list is empty")
	}
	return append([]InputItem(nil), input.Items...), nil
}

func appendResponseHistory(history []InputItem, output []ResponseOutputItem) ([]InputItem, error) {
	for _, item := range output {
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("encode response history: %w", err)
		}
		history = append(history, UnknownInputItem{Raw: encoded})
	}
	return history, nil
}

func functionCalls(items []ResponseOutputItem) []OutputFunctionCall {
	calls := make([]OutputFunctionCall, 0)
	for _, item := range items {
		if call, ok := item.(OutputFunctionCall); ok {
			calls = append(calls, call)
		}
	}
	return calls
}
