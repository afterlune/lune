package tui

import (
	"context"
	"encoding/json"

	"github.com/afterlune/luneCore"
)

type Client interface {
	Workspace() string
	SetEventHandler(core.EventHandler)
	RunTask(context.Context, core.TaskRequest) (json.RawMessage, error)
	ResumeTask(context.Context, string) (json.RawMessage, error)
	HasPending(context.Context, string) (bool, error)
	PendingHistory(context.Context, string) (json.RawMessage, error)
	PendingPlanMode(context.Context, string) (bool, error)
	DiscardTask(context.Context, string) (bool, error)
	Compact(context.Context, string) (int, error)
	CreateSession(context.Context, json.RawMessage) (core.Session, error)
	LatestSession(context.Context) (core.Session, bool, error)
	ListSessions(context.Context, int, bool) ([]core.Session, error)
	GetSession(context.Context, string) (core.Session, error)
	ForkSession(context.Context, string) (core.Session, error)
	SetSessionTitle(context.Context, string, string) error
	SetSessionArchived(context.Context, string, bool) error
	DeleteSession(context.Context, string) error
	RewindSession(context.Context, string, int) (int, error)
	ListModelProfiles() []core.ModelOption
	SetSessionSettings(context.Context, string, core.SessionSettings) error
	ProjectContextDefaults() core.ContextDefaults
	ProjectAttachmentLimits() core.AttachmentLimits
	PermissionMode() core.PermissionMode
	SetPermissionMode(core.PermissionMode) error
	ResolveApproval(string, bool) error
	MCPServers() []core.MCPServerStatus
	EnableMCPServer(string) error
	DisableMCPServer(string) error
	AuthenticateMCPServer(context.Context, string) error
	OpenMCPURL(string, string) error
	SetUserInteractionAvailable(bool)
	ResolveUserInteraction(string, json.RawMessage) error
}
