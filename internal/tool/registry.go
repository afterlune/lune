package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	eventObserver  ToolEventObserver
	permissions    *permissionSettings
}

// ToolEvent describes the start or completion of a tool execution. It never
// contains tool arguments, output, or error text.
type ToolEvent struct {
	CallID         string
	Name           string
	DisplaySummary string
	Started        bool
	Duration       time.Duration
	Failed         bool
	Canceled       bool
}

// ToolEventObserver receives tool lifecycle events. Implementations should
// return promptly because tool execution calls the observer synchronously.
type ToolEventObserver func(ToolEvent)

var _ model.ToolExecutor = (*Registry)(nil)
var _ model.CategorizedToolExecutor = (*Registry)(nil)

// ToolRegistration associates a tool with a category for staged discovery.
type ToolRegistration struct {
	Tool     Tool
	Category model.ToolCategory
}

func NewRegistry(tools ...Tool) (*Registry, error) {
	registry := &Registry{
		tools:       make(map[string]Tool, len(tools)),
		permissions: newPermissionSettings(PermissionModeReadOnly),
	}
	for _, registeredTool := range tools {
		if err := registry.Register(registeredTool); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// NewCategorizedRegistry creates a registry whose tools are revealed by category.
func NewCategorizedRegistry(registrations ...ToolRegistration) (*Registry, error) {
	registry := &Registry{permissions: newPermissionSettings(PermissionModeReadOnly)}
	for _, registration := range registrations {
		if err := registry.RegisterInCategory(registration.Category, registration.Tool); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

// SetToolEventObserver installs an observer for tool start and finish events.
func (r *Registry) SetToolEventObserver(observer ToolEventObserver) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.eventObserver = observer
	r.mu.Unlock()
}

func (r *Registry) PermissionMode() PermissionMode {
	if r == nil {
		return PermissionModeFullAccess
	}
	r.mu.RLock()
	permissions := r.permissions
	r.mu.RUnlock()
	return permissions.modeValue()
}

func (r *Registry) SetPermissionMode(mode PermissionMode) error {
	if r == nil {
		return errors.New("tool registry is nil")
	}
	r.mu.RLock()
	permissions := r.permissions
	r.mu.RUnlock()
	if permissions == nil {
		return errors.New("permission settings are unavailable")
	}
	return permissions.setMode(mode)
}

func (r *Registry) SetPermissionApprovalHandler(handler PermissionApprovalHandler) {
	if r == nil {
		return
	}
	r.mu.RLock()
	permissions := r.permissions
	r.mu.RUnlock()
	permissions.setApprovalHandler(handler)
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
	if category.Name != "" && definition.Name == model.CategorySelectorName {
		return errors.New("tool name is reserved: " + model.CategorySelectorName)
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
	registeredTool, err := r.authorizedTool(ctx, name, arguments)
	if err != nil {
		return Result{Err: err}
	}
	return registeredTool.Run(ctx, arguments)
}

func (r *Registry) authorizedTool(ctx context.Context, name string, arguments json.RawMessage) (Tool, error) {
	if r == nil {
		return nil, errors.New("tool registry is nil")
	}
	r.mu.RLock()
	registeredTool, exists := r.tools[name]
	permissions := r.permissions
	r.mu.RUnlock()
	if !exists {
		return nil, errors.New("unknown tool: " + name)
	}
	if err := permissions.authorize(ctx, PermissionRequest{Tool: name, Arguments: append(json.RawMessage(nil), arguments...)}); err != nil {
		return nil, err
	}
	return registeredTool, nil
}

// ExecuteCall runs a function call from a Responses API result and returns the
// matching function_call_output input item for the next request.
func (r *Registry) ExecuteCall(ctx context.Context, call model.OutputFunctionCall) model.InputFunctionCallOutput {
	started := time.Now()
	displaySummary := DisplaySummary(call.Name, json.RawMessage(call.Arguments))
	registeredTool, permissionErr := r.authorizedTool(ctx, call.Name, json.RawMessage(call.Arguments))
	var result Result
	if permissionErr != nil {
		result = Result{Err: permissionErr}
	} else {
		slog.Info("tool execution started", "tool", call.Name, "call_id", call.CallID)
		r.observeToolEvent(ToolEvent{CallID: call.CallID, Name: call.Name, DisplaySummary: displaySummary, Started: true})
		result = registeredTool.Run(ctx, json.RawMessage(call.Arguments))
	}
	duration := time.Since(started)
	attributes := []any{
		"tool", call.Name,
		"call_id", call.CallID,
		"duration", duration,
		"failed", result.Err != nil,
		"canceled", errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded),
	}
	if result.Err != nil {
		slog.Warn("tool execution finished", attributes...)
	} else {
		slog.Info("tool execution finished", attributes...)
	}
	r.observeToolEvent(ToolEvent{
		CallID: call.CallID, Name: call.Name, DisplaySummary: displaySummary, Duration: duration,
		Failed:   result.Err != nil,
		Canceled: errors.Is(result.Err, context.Canceled) || errors.Is(result.Err, context.DeadlineExceeded),
	})
	return model.InputFunctionCallOutput{
		CallID: call.CallID,
		Output: model.NewToolOutputText(result.Text()),
	}
}

func (r *Registry) observeToolEvent(event ToolEvent) {
	if r == nil {
		return
	}
	r.mu.RLock()
	observer := r.eventObserver
	r.mu.RUnlock()
	if observer == nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Warn("tool event observer panicked", "panic_type", fmt.Sprintf("%T", recovered))
		}
	}()
	observer(event)
}

// NewDefaultRegistry creates the built-in workspace and web tools.
func NewDefaultRegistry(workspace string) (*Registry, error) {
	root, err := canonicalWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	registry, err := NewCategorizedRegistry(
		ToolRegistration{Tool: ListDirTool{Workspace: root}, Category: model.ToolCategory{Name: "files", Description: "Explore, read, write, and edit workspace files."}},
		ToolRegistration{Tool: GlobTool{Workspace: root}, Category: model.ToolCategory{Name: "files", Description: "Explore, read, write, and edit workspace files."}},
		ToolRegistration{Tool: GrepTool{Workspace: root}, Category: model.ToolCategory{Name: "files", Description: "Explore, read, write, and edit workspace files."}},
		ToolRegistration{Tool: ReadFileTool{Workspace: root}, Category: model.ToolCategory{Name: "files", Description: "Explore, read, write, and edit workspace files."}},
		ToolRegistration{Tool: WriteFileTool{Workspace: root}, Category: model.ToolCategory{Name: "files", Description: "Explore, read, write, and edit workspace files."}},
		ToolRegistration{Tool: EditFileTool{Workspace: root}, Category: model.ToolCategory{Name: "files", Description: "Explore, read, write, and edit workspace files."}},
		ToolRegistration{Tool: CommandTool{Workspace: root}, Category: model.ToolCategory{Name: "command", Description: "Run a command in the workspace."}},
		ToolRegistration{Tool: WebSearchTool{httpClient: defaultWebHTTPClient}, Category: model.ToolCategory{Name: "web", Description: "Search the web and fetch web pages."}},
		ToolRegistration{Tool: WebFetchTool{httpClient: defaultWebHTTPClient}, Category: model.ToolCategory{Name: "web", Description: "Search the web and fetch web pages."}},
	)
	if err != nil {
		return nil, err
	}
	if err := registry.SetPermissionMode(PermissionModeWorkspace); err != nil {
		return nil, err
	}
	return registry, nil
}
