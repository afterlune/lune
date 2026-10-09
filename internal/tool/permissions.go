package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// PermissionMode controls how registered tools are authorized.
type PermissionMode string

const (
	PermissionModeReadOnly   PermissionMode = "read-only"
	PermissionModeWorkspace  PermissionMode = "workspace"
	PermissionModeAsk        PermissionMode = "ask"
	PermissionModeFullAccess PermissionMode = "full-access"
)

// PermissionModes returns the supported permission modes in UI order.
func PermissionModes() []PermissionMode {
	return []PermissionMode{
		PermissionModeReadOnly,
		PermissionModeWorkspace,
		PermissionModeAsk,
		PermissionModeFullAccess,
	}
}

// PermissionModeDescription returns the user-facing explanation for a mode.
func PermissionModeDescription(mode PermissionMode) string {
	switch mode {
	case PermissionModeReadOnly:
		return "Read workspace files; ask before web; block writes and shell"
	case PermissionModeWorkspace:
		return "Workspace file operations; commands and web need approval"
	case PermissionModeAsk:
		return "Read workspace files; Ask before every other tool"
	case PermissionModeFullAccess:
		return "Allow all tools; shell can access paths outside the workspace"
	default:
		return ""
	}
}

// PermissionRequest contains the exact tool input shown before one-time approval.
type PermissionRequest struct {
	Tool      string
	Arguments json.RawMessage
}

// PermissionApprovalHandler asks the user to allow one tool call.
type PermissionApprovalHandler func(context.Context, PermissionRequest) bool

type permissionSettings struct {
	mu      sync.RWMutex
	mode    PermissionMode
	approve PermissionApprovalHandler
}

func newPermissionSettings(mode PermissionMode) *permissionSettings {
	return &permissionSettings{mode: mode}
}

func (p *permissionSettings) modeValue() PermissionMode {
	if p == nil {
		return PermissionModeFullAccess
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}

func (p *permissionSettings) setMode(mode PermissionMode) error {
	if p == nil {
		return errors.New("permission settings are unavailable")
	}
	switch mode {
	case PermissionModeReadOnly, PermissionModeWorkspace, PermissionModeAsk, PermissionModeFullAccess:
	default:
		return fmt.Errorf("unknown permission mode %q", mode)
	}
	p.mu.Lock()
	p.mode = mode
	p.mu.Unlock()
	return nil
}

func (p *permissionSettings) setApprovalHandler(handler PermissionApprovalHandler) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.approve = handler
	p.mu.Unlock()
}

func (p *permissionSettings) authorize(ctx context.Context, request PermissionRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil {
		return nil
	}
	p.mu.RLock()
	mode, approve := p.mode, p.approve
	p.mu.RUnlock()

	switch mode {
	case PermissionModeReadOnly:
		if isWorkspaceReadTool(request.Tool) {
			break
		}
		if !isNetworkTool(request.Tool) {
			return fmt.Errorf("tool %q is disabled in read-only mode", request.Tool)
		}
		return requestApproval(ctx, approve, request)
	case PermissionModeWorkspace:
		if request.Tool == "command" || isNetworkTool(request.Tool) {
			return requestApproval(ctx, approve, request)
		}
	case PermissionModeAsk:
		if !isWorkspaceReadTool(request.Tool) {
			return requestApproval(ctx, approve, request)
		}
	case PermissionModeFullAccess:
		// Full access intentionally skips tool approval. CommandTool itself is
		// unrestricted by the workspace boundary, so the UI describes this risk.
	default:
		return fmt.Errorf("unknown permission mode %q", mode)
	}
	return ctx.Err()
}

func requestApproval(ctx context.Context, approve PermissionApprovalHandler, request PermissionRequest) error {
	if approve == nil {
		return fmt.Errorf("tool %q requires approval, but no interactive approval handler is available; use /permissions full-access to allow it without prompting", request.Tool)
	}
	approved := approve(ctx, request)
	if err := ctx.Err(); err != nil {
		return err
	}
	if !approved {
		return fmt.Errorf("permission denied for tool %q", request.Tool)
	}
	return ctx.Err()
}

func isNetworkTool(name string) bool {
	return name == "web_search" || name == "web_fetch"
}

func isWorkspaceReadTool(name string) bool {
	switch name {
	case "read_file", "list_dir", "glob", "grep":
		return true
	default:
		return false
	}
}
