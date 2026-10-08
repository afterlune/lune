package tool

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"

	"github.com/afterlune/lune/internal/model"
)

// Registry stores tools by their function name.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

var _ model.ToolExecutor = (*Registry)(nil)

func NewRegistry(tools ...Tool) (*Registry, error) {
	registry := &Registry{tools: make(map[string]Tool, len(tools))}
	for _, registeredTool := range tools {
		if err := registry.Register(registeredTool); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (r *Registry) Register(registeredTool Tool) error {
	if registeredTool == nil {
		return errors.New("cannot register a nil tool")
	}
	definition := registeredTool.Definition()
	if definition.Name == "" {
		return errors.New("tool name is empty")
	}
	if len(definition.Parameters) == 0 {
		return errors.New("tool parameters schema is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.tools == nil {
		r.tools = make(map[string]Tool)
	}
	if _, exists := r.tools[definition.Name]; exists {
		return errors.New("tool is already registered: " + definition.Name)
	}
	r.tools[definition.Name] = registeredTool
	return nil
}

// Definitions returns function-tool definitions suitable for ResponseRequest.Tools.
func (r *Registry) Definitions() []model.ResponseTool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	registeredTools := make(map[string]Tool, len(r.tools))
	for name, registeredTool := range r.tools {
		registeredTools[name] = registeredTool
	}
	r.mu.RUnlock()

	names := make([]string, 0, len(registeredTools))
	for name := range registeredTools {
		names = append(names, name)
	}
	sort.Strings(names)
	definitions := make([]model.ResponseTool, 0, len(names))
	for _, name := range names {
		definitions = append(definitions, registeredTools[name].Definition())
	}
	return definitions
}

// Run executes a named tool with its JSON arguments.
func (r *Registry) Run(ctx context.Context, name string, arguments json.RawMessage) Result {
	if r == nil {
		return Result{Err: errors.New("tool registry is nil")}
	}
	r.mu.RLock()
	registeredTool, exists := r.tools[name]
	r.mu.RUnlock()
	if !exists {
		return Result{Err: errors.New("unknown tool: " + name)}
	}
	return registeredTool.Run(ctx, arguments)
}

// ExecuteCall runs a function call from a Responses API result and returns the
// matching function_call_output input item for the next request.
func (r *Registry) ExecuteCall(ctx context.Context, call model.OutputFunctionCall) model.InputFunctionCallOutput {
	result := r.Run(ctx, call.Name, json.RawMessage(call.Arguments))
	return model.InputFunctionCallOutput{
		CallID: call.CallID,
		Output: model.NewToolOutputText(result.Text()),
	}
}

// NewDefaultRegistry creates the four built-in tools, scoped to workspace.
func NewDefaultRegistry(workspace string) (*Registry, error) {
	root, err := canonicalWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	return NewRegistry(
		CommandTool{Workspace: root},
		ReadFileTool{Workspace: root},
		WriteFileTool{Workspace: root},
		EditFileTool{Workspace: root},
	)
}
