package tui

import (
	"context"
	"encoding/json"

	core "github.com/afterlune/luneCore"
)

// CoreClient contains host level operations. Task execution and user decisions
// belong to a session-bound SessionRuntime.
type CoreClient interface {
	Workspace() string
	OpenSessionRuntime(context.Context, string) (SessionRuntime, error)
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
	Compact(context.Context, string) (int, error)
	MCPServers() []core.MCPServerStatus
	EnableMCPServer(string) error
	DisableMCPServer(string) error
	AuthenticateMCPServer(context.Context, string) error
	OpenMCPURL(string, string) error
}

// SessionRuntime is the session-scoped task, event, and interaction API.
type SessionRuntime interface {
	SessionID() string
	SubmitTurn(context.Context, core.TurnRequest, core.SubmissionMode) (core.TurnSubmission, error)
	WaitTurn(context.Context, string) (core.TurnResult, error)
	SubscribeEvents(context.Context, int64) (<-chan core.Event, error)
	State(context.Context) (core.RuntimeState, error)
	Interrupt(string) bool
	Recover(context.Context, ...core.ExecutionOptions) (core.TurnSubmission, error)
	DiscardPaused(context.Context) (bool, error)
	SetUserInteractionAvailable(bool)
	PendingRequests(context.Context) ([]core.PendingRequest, error)
	ResolveApproval(string, bool) error
	ResolveUserQuestion(context.Context, core.UserQuestionAnswer) error
	ResolveUserInteraction(string, json.RawMessage) error
	ResolveToolRecovery(context.Context, string, core.ToolRecoveryDecision) error
	ImplementPlan(context.Context, string, core.PlanImplementationMode, ...core.PlanAuthorization) (core.PlanImplementation, error)
}
