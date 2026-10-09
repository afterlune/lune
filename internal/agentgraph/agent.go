// Package agentgraph runs the existing model and tool interfaces on luneGraph.
package agentgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/afterlune/lune/internal/model"
	graph "github.com/afterlune/luneGraph"
)

const (
	modelNode = "model_request"
	toolNode  = "execute_tool"
	machineID = "lune-agent-v1"
)

// Requester sends one Responses API request without managing history or tools.
type Requester interface {
	CreateResponse(context.Context, model.ResponseRequest) (*model.Response, error)
}

// State is the resumable state for one agent task. History uses ResponseInput's
// custom JSON decoder so typed and unknown multimodal input items survive clone
// and checkpoint round trips.
type State struct {
	History          model.ResponseInput        `json:"history"`
	PlanMode         bool                       `json:"plan_mode,omitempty"`
	SelectedCategory string                     `json:"selected_category,omitempty"`
	PendingCalls     []model.OutputFunctionCall `json:"pending_calls,omitempty"`
	PendingIndex     int                        `json:"pending_index,omitempty"`
	VisibleTools     []string                   `json:"visible_tools,omitempty"`
	ToolRounds       int                        `json:"tool_rounds,omitempty"`
	MaxToolRounds    int                        `json:"max_tool_rounds,omitempty"`
	TaskError        string                     `json:"task_error,omitempty"`
	FinalResponse    *model.Response            `json:"final_response,omitempty"`
}

// TaskOptions configures one user task. A zero tool-round limit is unlimited.
type TaskOptions struct {
	MaxToolRounds int
	PlanMode      bool
}

// Agent runs one model request or one tool call per graph step.
type Agent struct {
	runner      *graph.Runner[State]
	requester   Requester
	tools       model.ToolExecutor
	categorized model.CategorizedToolExecutor
	categories  []model.ToolCategory
}

var _ Requester = (*model.Client)(nil)

func New(requester Requester, tools model.ToolExecutor) (*Agent, error) {
	if requester == nil {
		return nil, errors.New("requester is required")
	}
	if tools == nil {
		return nil, errors.New("tool executor is required")
	}

	agent := &Agent{requester: requester, tools: tools}
	if categorized, ok := tools.(model.CategorizedToolExecutor); ok {
		agent.categories = categorized.Categories()
		if len(agent.categories) > 0 {
			agent.categorized = categorized
		}
	}

	workflow := graph.New[State](modelNode)
	if err := workflow.AddNode(graph.NodeSpec[State]{Name: modelNode, Run: agent.requestModel}); err != nil {
		return nil, fmt.Errorf("add model node: %w", err)
	}
	if err := workflow.AddNode(graph.NodeSpec[State]{Name: toolNode, Run: agent.executeTool}); err != nil {
		return nil, fmt.Errorf("add tool node: %w", err)
	}
	if err := workflow.AddEdge(modelNode, toolNode); err != nil {
		return nil, fmt.Errorf("add model-to-tool edge: %w", err)
	}
	if err := workflow.AddEdge(toolNode, toolNode); err != nil {
		return nil, fmt.Errorf("add tool-to-tool edge: %w", err)
	}
	if err := workflow.AddEdge(toolNode, modelNode); err != nil {
		return nil, fmt.Errorf("add tool-to-model edge: %w", err)
	}
	runner, err := workflow.Compile(graph.Config[State]{MachineID: machineID, Clone: graph.JSONClone[State]})
	if err != nil {
		return nil, fmt.Errorf("compile agent graph: %w", err)
	}
	agent.runner = runner
	return agent, nil
}

func (a *Agent) Start(ctx context.Context, runID string, history, input *model.ResponseInput, taskOptions TaskOptions, options graph.Options[State]) (graph.Result[State], error) {
	if taskOptions.MaxToolRounds < 0 {
		return graph.Result[State]{Status: graph.StatusFailed}, errors.New("MaxToolRounds must not be negative")
	}
	state, err := initialState(history, input)
	if err != nil {
		return graph.Result[State]{Status: graph.StatusFailed}, err
	}
	state.MaxToolRounds = taskOptions.MaxToolRounds
	state.PlanMode = taskOptions.PlanMode
	return a.runner.Start(ctx, runID, state, options)
}

func (a *Agent) Resume(ctx context.Context, checkpoint graph.Checkpoint[State], inputs []graph.ResumeInput, options graph.Options[State]) (graph.Result[State], error) {
	return a.runner.Resume(ctx, checkpoint, inputs, options)
}

func (a *Agent) Recover(ctx context.Context, runID string, inputs []graph.ResumeInput, options graph.Options[State]) (graph.Result[State], error) {
	return a.runner.Recover(ctx, runID, inputs, options)
}

func initialState(history, input *model.ResponseInput) (State, error) {
	if input == nil {
		return State{}, errors.New("input is nil")
	}
	items, err := responseInputItems(input)
	if err != nil {
		return State{}, err
	}
	var previous []model.InputItem
	if history != nil {
		if history.Text != nil && history.Items != nil {
			return State{}, errors.New("conversation history cannot contain both text and items")
		}
		if history.Text != nil {
			previous = append(previous, model.InputMessage{
				Role:    model.RoleUser,
				Content: model.NewMessageText(*history.Text),
			})
		} else {
			previous = append(previous, history.Items...)
		}
	}
	if len(items) == 0 {
		return State{}, errors.New("input item list is empty")
	}
	previous = append(previous, items...)
	return State{History: *model.NewResponseInputItems(previous...)}, nil
}

func responseInputItems(input *model.ResponseInput) ([]model.InputItem, error) {
	if input.Text != nil && input.Items != nil {
		return nil, errors.New("responses input cannot contain both text and items")
	}
	if input.Text != nil {
		return []model.InputItem{model.InputMessage{
			Role:    model.RoleUser,
			Content: model.NewMessageText(*input.Text),
		}}, nil
	}
	if len(input.Items) == 0 {
		return nil, errors.New("input item list is empty")
	}
	return append([]model.InputItem(nil), input.Items...), nil
}

func (a *Agent) requestModel(ctx context.Context, _ graph.CallInfo, state State) (graph.Transition[State], error) {
	visibleTools, visibleNames, err := a.visibleTools(state.SelectedCategory, state.PlanMode)
	if err != nil {
		return failTask(state, err)
	}
	request := model.ResponseRequest{
		Model: model.DeepSeekFlashModel,
		Input: model.NewResponseInputItems(state.History.Items...),
		Tools: visibleTools,
	}
	if state.PlanMode {
		request.Instructions = "You are in plan mode. Inspect and reason about the requested work, then present a concise implementation plan for user review. You may use read-only tools only. Do not claim to have made changes."
	}
	slog.InfoContext(ctx, "agent model request started",
		"model", model.DeepSeekFlashModel,
		"request", state.ToolRounds+1,
		"tool_rounds_completed", state.ToolRounds,
		"plan_mode", state.PlanMode,
		"selected_category", state.SelectedCategory,
		"visible_tools", visibleNames,
	)
	response, err := a.requester.CreateResponse(ctx, request)
	if err != nil {
		if ctx.Err() != nil {
			return graph.Transition[State]{}, err
		}
		return failTask(state, err)
	}
	if response == nil {
		return failTask(state, errors.New("requester returned a nil response"))
	}
	if response.Status == "failed" {
		if response.Error != nil {
			return failTask(state, fmt.Errorf("model response failed (%s): %s", response.Error.Code, response.Error.Message))
		}
		return failTask(state, errors.New("model response failed"))
	}

	state.History, err = appendResponseHistory(state.History, response.Output)
	if err != nil {
		return failTask(state, err)
	}
	calls := responseFunctionCalls(response.Output)
	if len(calls) == 0 {
		state.FinalResponse = response
		return graph.EndExecution(state), nil
	}
	if state.MaxToolRounds > 0 && state.ToolRounds >= state.MaxToolRounds {
		for _, call := range calls {
			state.History.Items = append(state.History.Items, model.InputFunctionCallOutput{
				CallID: call.CallID,
				Output: model.NewToolOutputText("Skipped because the tool-call round limit was reached."),
			})
		}
		state.FinalResponse = response
		state.TaskError = fmt.Sprintf("%v: %d", model.ErrToolCallLimit, state.MaxToolRounds)
		return graph.EndExecution(state), nil
	}
	state.PendingCalls = calls
	state.PendingIndex = 0
	state.VisibleTools = visibleNames
	return graph.To(state, toolNode), nil
}

func failTask(state State, err error) (graph.Transition[State], error) {
	state.TaskError = err.Error()
	return graph.EndExecution(state), nil
}

func (a *Agent) executeTool(ctx context.Context, _ graph.CallInfo, state State) (graph.Transition[State], error) {
	if state.PendingIndex < 0 || state.PendingIndex >= len(state.PendingCalls) {
		return graph.Transition[State]{}, errors.New("pending tool-call index is out of range")
	}
	call := state.PendingCalls[state.PendingIndex]
	var output model.InputFunctionCallOutput
	switch {
	case a.categorized != nil && call.Name == model.CategorySelectorName:
		categories := a.categories
		if state.PlanMode {
			categories = a.readOnlyCategories()
		}
		category, err := model.ParseCategorySelection(call.Arguments, categories)
		text := "Category selected. Its tools will be available in the next request."
		if err != nil {
			slog.WarnContext(ctx, "tool category selection rejected",
				"selected_category", state.SelectedCategory,
				"error_type", fmt.Sprintf("%T", err),
			)
			text = "Category was not changed: " + err.Error()
		} else {
			state.SelectedCategory = category
			slog.InfoContext(ctx, "tool category selected", "selected_category", category)
		}
		output = model.InputFunctionCallOutput{CallID: call.CallID, Output: model.NewToolOutputText(text)}
	case state.PlanMode && !isReadOnlyTool(call.Name):
		output = model.InputFunctionCallOutput{
			CallID: call.CallID,
			Output: model.NewToolOutputText("Tool is not available in plan mode."),
		}
	case !contains(state.VisibleTools, call.Name):
		expectedCategory := a.categoryForTool(call.Name)
		slog.WarnContext(ctx, "tool call is not in the visible tool set",
			"tool", call.Name,
			"selected_category", state.SelectedCategory,
			"visible_tools", state.VisibleTools,
			"expected_category", expectedCategory,
		)
		output = model.InputFunctionCallOutput{
			CallID: call.CallID,
			Output: model.NewToolOutputText(unavailableToolMessage(call.Name, expectedCategory, a.categorized != nil)),
		}
	default:
		output = a.tools.ExecuteCall(ctx, call)
	}

	state.History.Items = append(state.History.Items, output)
	state.PendingIndex++
	if state.PendingIndex < len(state.PendingCalls) {
		return graph.To(state, toolNode), nil
	}
	state.ToolRounds++
	state.PendingCalls = nil
	state.PendingIndex = 0
	state.VisibleTools = nil
	return graph.To(state, modelNode), nil
}

func (a *Agent) visibleTools(category string, planMode bool) ([]model.ResponseTool, []string, error) {
	if a.categorized == nil {
		definitions := a.tools.Definitions()
		if planMode {
			definitions = readOnlyDefinitions(definitions)
		}
		return definitions, toolNames(definitions), nil
	}
	categories := a.categories
	if planMode {
		categories = a.readOnlyCategories()
	}
	selector, err := model.CategorySelectorDefinition(categories)
	if err != nil {
		return nil, nil, fmt.Errorf("build category selector: %w", err)
	}
	definitions := []model.ResponseTool{selector}
	if category != "" {
		definitions = append(definitions, a.categorized.DefinitionsForCategory(category)...)
		if planMode {
			definitions = readOnlyDefinitions(definitions)
			definitions = append([]model.ResponseTool{selector}, definitions...)
		}
	}
	return definitions, toolNames(definitions), nil
}

func (a *Agent) readOnlyCategories() []model.ToolCategory {
	var categories []model.ToolCategory
	for _, category := range a.categories {
		if len(readOnlyDefinitions(a.categorized.DefinitionsForCategory(category.Name))) > 0 {
			categories = append(categories, category)
		}
	}
	return categories
}

func (a *Agent) categoryForTool(name string) string {
	if a.categorized == nil {
		return ""
	}
	for _, category := range a.categories {
		for _, definition := range a.categorized.DefinitionsForCategory(category.Name) {
			if function, ok := definition.(model.FunctionTool); ok && function.Name == name {
				return category.Name
			}
		}
	}
	return ""
}

func unavailableToolMessage(name, category string, categorized bool) string {
	if category != "" {
		return fmt.Sprintf("Tool %q belongs to category %q, which is not visible in this request. Call %s with category %q, then retry the tool in the next request.",
			name, category, model.CategorySelectorName, category)
	}
	if categorized {
		return fmt.Sprintf("Tool %q is not registered in a discoverable category. Call %s to review available tools, then use a listed tool.",
			name, model.CategorySelectorName)
	}
	return fmt.Sprintf("Tool %q is not available in this request. Only call tools listed in the current request.", name)
}

func readOnlyDefinitions(definitions []model.ResponseTool) []model.ResponseTool {
	filtered := make([]model.ResponseTool, 0, len(definitions))
	for _, definition := range definitions {
		function, ok := definition.(model.FunctionTool)
		if ok && isReadOnlyTool(function.Name) {
			filtered = append(filtered, definition)
		}
	}
	return filtered
}

func isReadOnlyTool(name string) bool {
	switch name {
	case "read_file", "list_dir", "glob", "grep", "web_search", "web_fetch":
		return true
	default:
		return false
	}
}

func appendResponseHistory(history model.ResponseInput, output []model.ResponseOutputItem) (model.ResponseInput, error) {
	items := append([]model.InputItem(nil), history.Items...)
	for _, item := range output {
		encoded, err := json.Marshal(item)
		if err != nil {
			return model.ResponseInput{}, fmt.Errorf("encode response history: %w", err)
		}
		items = append(items, model.UnknownInputItem{Raw: encoded})
	}
	return *model.NewResponseInputItems(items...), nil
}

func responseFunctionCalls(items []model.ResponseOutputItem) []model.OutputFunctionCall {
	calls := make([]model.OutputFunctionCall, 0)
	for _, item := range items {
		if call, ok := item.(model.OutputFunctionCall); ok {
			calls = append(calls, call)
		}
	}
	return calls
}

func toolNames(definitions []model.ResponseTool) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		if function, ok := definition.(model.FunctionTool); ok {
			names = append(names, function.Name)
		}
	}
	return names
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
