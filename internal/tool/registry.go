package tool

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/afterlune/lune/internal/model"
)

// Registry stores tools by their function name.
type Registry struct {
	mu             sync.RWMutex
	tools          map[string]Tool
	toolOrder      []string
	toolCategories map[string]string
	categories     map[string]model.ToolCategory
	categoryOrder  []string
	uncategorized  int
}

var _ model.ToolExecutor = (*Registry)(nil)
var _ model.CategorizedToolExecutor = (*Registry)(nil)

// ToolRegistration associates a tool with a category for staged discovery.
type ToolRegistration struct {
	Tool     Tool
	Category model.ToolCategory
}

func NewRegistry(tools ...Tool) (*Registry, error) {
	registry := &Registry{tools: make(map[string]Tool, len(tools))}
	for _, registeredTool := range tools {
		if err := registry.Register(registeredTool); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// NewCategorizedRegistry creates a registry whose tools are revealed by category.
func NewCategorizedRegistry(registrations ...ToolRegistration) (*Registry, error) {
	registry := &Registry{}
	for _, registration := range registrations {
		if err := registry.RegisterInCategory(registration.Category, registration.Tool); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

func (r *Registry) Register(registeredTool Tool) error {
	return r.register(model.ToolCategory{}, registeredTool)
}

// RegisterInCategory registers a tool and makes it available under category.
func (r *Registry) RegisterInCategory(category model.ToolCategory, registeredTool Tool) error {
	if strings.TrimSpace(category.Name) == "" || category.Name != strings.TrimSpace(category.Name) {
		return errors.New("tool category name must not be empty or padded with whitespace")
	}
	if strings.TrimSpace(category.Description) == "" {
		return errors.New("tool category description must not be empty")
	}
	return r.register(category, registeredTool)
}

func (r *Registry) register(category model.ToolCategory, registeredTool Tool) error {
	if registeredTool == nil {
		return errors.New("cannot register a nil tool")
	}
	definition := registeredTool.Definition()
	if definition.Name == "" {
		return errors.New("tool name is empty")
	}
	if category.Name != "" && definition.Name == "select_tool_category" {
		return errors.New("tool name is reserved: select_tool_category")
	}
	if len(definition.Parameters) == 0 {
		return errors.New("tool parameters schema is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if category.Name == "" && len(r.categories) > 0 {
		return errors.New("cannot add an uncategorized tool to a categorized registry")
	}
	if category.Name != "" && r.uncategorized > 0 {
		return errors.New("cannot categorize tools in a registry with uncategorized tools")
	}
	if category.Name != "" {
		if existing, exists := r.categories[category.Name]; exists && existing.Description != category.Description {
			return errors.New("tool category description does not match existing category: " + category.Name)
		}
	}
	if r.tools == nil {
		r.tools = make(map[string]Tool)
	}
	if _, exists := r.tools[definition.Name]; exists {
		return errors.New("tool is already registered: " + definition.Name)
	}
	r.tools[definition.Name] = registeredTool
	r.toolOrder = append(r.toolOrder, definition.Name)
	if category.Name != "" {
		if r.categories == nil {
			r.categories = make(map[string]model.ToolCategory)
		}
		if r.toolCategories == nil {
			r.toolCategories = make(map[string]string)
		}
		if _, exists := r.categories[category.Name]; !exists {
			r.categories[category.Name] = category
			r.categoryOrder = append(r.categoryOrder, category.Name)
		}
		r.toolCategories[definition.Name] = category.Name
	} else {
		r.uncategorized++
	}
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

// Categories returns categories in registration order.
func (r *Registry) Categories() []model.ToolCategory {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	categories := make([]model.ToolCategory, 0, len(r.categoryOrder))
	for _, name := range r.categoryOrder {
		categories = append(categories, r.categories[name])
	}
	return categories
}

// DefinitionsForCategory returns function definitions in registration order.
func (r *Registry) DefinitionsForCategory(category string) []model.ResponseTool {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	registeredTools := make([]Tool, 0)
	for _, name := range r.toolOrder {
		if r.toolCategories[name] == category {
			registeredTools = append(registeredTools, r.tools[name])
		}
	}
	r.mu.RUnlock()
	definitions := make([]model.ResponseTool, 0, len(registeredTools))
	for _, registeredTool := range registeredTools {
		definitions = append(definitions, registeredTool.Definition())
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
	started := time.Now()
	slog.Info("tool execution started", "tool", call.Name, "call_id", call.CallID)
	result := r.Run(ctx, call.Name, json.RawMessage(call.Arguments))
	attributes := []any{
		"tool", call.Name,
		"call_id", call.CallID,
		"duration", time.Since(started),
		"failed", result.Err != nil,
		"canceled", errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded),
	}
	if result.Err != nil {
		slog.Warn("tool execution finished", attributes...)
	} else {
		slog.Info("tool execution finished", attributes...)
	}
	return model.InputFunctionCallOutput{
		CallID: call.CallID,
		Output: model.NewToolOutputText(result.Text()),
	}
}

// NewDefaultRegistry creates the built-in workspace and web tools.
func NewDefaultRegistry(workspace string) (*Registry, error) {
	root, err := canonicalWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	return NewCategorizedRegistry(
		ToolRegistration{Tool: ReadFileTool{Workspace: root}, Category: model.ToolCategory{Name: "files", Description: "Read, write, and edit workspace files."}},
		ToolRegistration{Tool: WriteFileTool{Workspace: root}, Category: model.ToolCategory{Name: "files", Description: "Read, write, and edit workspace files."}},
		ToolRegistration{Tool: EditFileTool{Workspace: root}, Category: model.ToolCategory{Name: "files", Description: "Read, write, and edit workspace files."}},
		ToolRegistration{Tool: CommandTool{Workspace: root}, Category: model.ToolCategory{Name: "command", Description: "Run a command in the workspace."}},
		ToolRegistration{Tool: WebSearchTool{httpClient: defaultWebHTTPClient}, Category: model.ToolCategory{Name: "web", Description: "Search the web and fetch web pages."}},
		ToolRegistration{Tool: WebFetchTool{httpClient: defaultWebHTTPClient}, Category: model.ToolCategory{Name: "web", Description: "Search the web and fetch web pages."}},
	)
}
