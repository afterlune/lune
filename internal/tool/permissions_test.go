package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/model"
)

type permissionTestTool struct {
	name  string
	calls int
}

func (t *permissionTestTool) Definition() model.FunctionTool {
	return model.FunctionTool{
		Name:       t.name,
		Parameters: json.RawMessage(`{"type":"object","properties":{},"required":[],"additionalProperties":false}`),
	}
}

func (t *permissionTestTool) Run(context.Context, json.RawMessage) Result {
	t.calls++
	return Result{Output: "ran"}
}

func TestPermissionModesAuthorizeToolsAsDesigned(t *testing.T) {
	tests := []struct {
		name          string
		mode          PermissionMode
		allowed       map[string]bool
		needsApproval map[string]bool
	}{
		{
			name:          "read-only",
			mode:          PermissionModeReadOnly,
			allowed:       map[string]bool{"read_file": true, "web_fetch": true},
			needsApproval: map[string]bool{"web_fetch": true},
		},
		{
			name:          "workspace",
			mode:          PermissionModeWorkspace,
			allowed:       map[string]bool{"read_file": true, "write_file": true, "custom_tool": true, "command": true, "web_fetch": true},
			needsApproval: map[string]bool{"command": true, "web_fetch": true},
		},
		{
			name:          "ask",
			mode:          PermissionModeAsk,
			allowed:       map[string]bool{"read_file": true, "write_file": true, "custom_tool": true, "command": true, "web_fetch": true},
			needsApproval: map[string]bool{"write_file": true, "custom_tool": true, "command": true, "web_fetch": true},
		},
		{
			name:    "full-access",
			mode:    PermissionModeFullAccess,
			allowed: map[string]bool{"read_file": true, "write_file": true, "custom_tool": true, "command": true, "web_fetch": true},
		},
	}

	toolNames := []string{"read_file", "write_file", "custom_tool", "command", "web_fetch"}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tools := make(map[string]*permissionTestTool, len(toolNames))
			registered := make([]Tool, 0, len(toolNames))
			for _, name := range toolNames {
				registeredTool := &permissionTestTool{name: name}
				tools[name] = registeredTool
				registered = append(registered, registeredTool)
			}
			registry, err := NewRegistry(registered...)
			if err != nil {
				t.Fatal(err)
			}
			if err := registry.SetPermissionMode(test.mode); err != nil {
				t.Fatal(err)
			}
			approvals := make(map[string]int)
			registry.SetPermissionApprovalHandler(func(_ context.Context, request PermissionRequest) bool {
				approvals[request.Tool]++
				return true
			})

			for _, name := range toolNames {
				result := registry.Run(context.Background(), name, json.RawMessage(`{}`))
				wantAllowed := test.allowed[name]
				if wantAllowed && result.Err != nil {
					t.Errorf("%s was rejected: %v", name, result.Err)
				}
				if !wantAllowed && result.Err == nil {
					t.Errorf("%s was allowed", name)
				}
				wantCalls := 0
				if wantAllowed {
					wantCalls = 1
				}
				if tools[name].calls != wantCalls {
					t.Errorf("%s calls = %d, want %d", name, tools[name].calls, wantCalls)
				}
				wantApprovals := 0
				if test.needsApproval[name] {
					wantApprovals = 1
				}
				if approvals[name] != wantApprovals {
					t.Errorf("%s approvals = %d, want %d", name, approvals[name], wantApprovals)
				}
			}
		})
	}
}

func TestAskModeDenialAndMissingApprovalHandlerBlockTool(t *testing.T) {
	for _, test := range []struct {
		name     string
		handler  PermissionApprovalHandler
		wantText string
	}{
		{name: "denied", handler: func(context.Context, PermissionRequest) bool { return false }, wantText: "permission denied"},
		{name: "no handler", wantText: "requires approval"},
	} {
		t.Run(test.name, func(t *testing.T) {
			registeredTool := &permissionTestTool{name: "write_file"}
			registry, err := NewRegistry(registeredTool)
			if err != nil {
				t.Fatal(err)
			}
			if err := registry.SetPermissionMode(PermissionModeAsk); err != nil {
				t.Fatal(err)
			}
			registry.SetPermissionApprovalHandler(test.handler)

			result := registry.Run(context.Background(), "write_file", json.RawMessage(`{}`))
			if result.Err == nil || !strings.Contains(result.Err.Error(), test.wantText) {
				t.Fatalf("error = %v, want text %q", result.Err, test.wantText)
			}
			if registeredTool.calls != 0 {
				t.Fatalf("tool ran %d times after approval was unavailable", registeredTool.calls)
			}
		})
	}
}
