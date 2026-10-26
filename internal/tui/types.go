package tui

import (
	"time"

	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
)

// EntryKind identifies the type of conversation item.
type EntryKind string

const (
	KindUser             EntryKind = "user"
	KindAssistant        EntryKind = "assistant"
	KindTool             EntryKind = "tool"
	KindQuestionAnswered EntryKind = "question_answered"
	KindPlan             EntryKind = "plan"
	KindSystem           EntryKind = "system"
	KindQueued           EntryKind = "queued"
	KindReasoning        EntryKind = "reasoning"
	KindSubagent         EntryKind = "subagent"
)

// entryLineRange tracks the rendered line range of a TranscriptEntry in the viewport.
type entryLineRange struct {
	EntryIndex    int
	StartLine     int
	EndLine       int
	IsRoundHeader bool
	// IsRoundFooter marks the trailing "Show less" row of an expanded tool round.
	IsRoundFooter bool
}

// QueuedItem tracks an input waiting in FIFO queue while another task runs.
type QueuedItem struct {
	ExecutionID string
}

// TranscriptEntry represents a single visual block in the conversation transcript.
type TranscriptEntry struct {
	ID          string
	ExecutionID string
	Kind        EntryKind
	Text        string
	Timestamp   time.Time

	// Round grouping & turn duration fields
	RoundExpanded bool
	TurnDuration  time.Duration

	// Tool specific fields
	ToolCallID    string
	ToolName      string
	ToolArgs      string
	ToolCommand   string
	ToolStatus    publictool.CommandStatus
	ToolExitCode  *int
	ToolRunning   bool
	ToolStartedAt time.Time
	ToolFailed    bool
	ToolDuration  time.Duration
	ToolChanges   []publictool.FileChange
	ToolOutput    string
	ToolExpanded  bool

	// Subagent specific fields
	AgentID          string
	AgentName        string
	AgentRole        string
	AgentTask        string
	SubagentRunning  bool
	SubagentPreview  string
	SubagentDuration time.Duration
	SubagentExpanded bool
	SubagentTools    int
	SubagentLastTool string

	// Question answered specific fields
	QuestionBatchID   string
	QuestionsTotal    int
	AnsweredQuestions []AnsweredQuestionItem

	// Reasoning specific fields
	ReasoningDuration time.Duration
	ReasoningExpanded bool
	ReasoningLive     bool

	// File mentions attachments
	AttachedFiles []string
}

type agentRuntimeMetrics struct {
	ID                     string
	Name                   string
	Role                   string
	Task                   string
	Activity               string
	Status                 string
	ModelProfile           string
	Provider               string
	Model                  string
	ContextWindow          int64
	InputTokens            int64
	EstimatedInputTokens   int64
	OutputTokens           int64
	CachedTokens           int64
	ReasoningTokens        int64
	ToolCount              int
	ContextInputTokens     int64
	TokensPerSecond        float64
	StreamStartedAt        time.Time
	StreamGenerationTokens int64
	StartedAt              time.Time
	Duration               time.Duration
}

// AnsweredQuestionItem represents a single question-answer pair in a resolved question batch.
type AnsweredQuestionItem struct {
	Question string
	Answer   string
}

// AutocompleteItem represents a slash command suggestion.
type AutocompleteItem struct {
	Command     string
	Description string
}

// ActiveQuestion holds the state of a pending clarification question.
type ActiveQuestion struct {
	Submitting    bool
	Request       core.UserQuestionRequest
	CurrentIndex  int
	Drafts        []questionDraft
	Error         string
	draftRevision uint64
	savedRevision uint64
	saveInFlight  bool
}

// questionDraft is the only source of answer state for an individual question.
type questionDraft struct {
	Selected  int
	Custom    string
	NotesOpen bool
	NoteText  string
	Confirmed bool
}

// ActivePlanReview holds the state of a pending plan review.
type ActivePlanReview struct {
	ExecutionID string
	Text        string
	Selected    int // 0: implement, 1: clear & implement, 2: cancel/stay
}

// ReasoningEffortOption describes one selectable reasoning effort level.
type ReasoningEffortOption struct {
	Value       string // empty means provider default (omit effort)
	Label       string
	Description string
}

// ActiveEffortPicker holds the state of the interactive reasoning-effort picker.
type ActiveEffortPicker struct {
	Selected int
}

// ActiveApproval holds the state of a pending tool execution approval.
type ActiveApproval struct {
	Request  core.ApprovalRequest
	Selected int // 0: allow_once, 1: allow_session, 2: deny
}

// ActiveRecovery holds the state of a pending model recovery request.
type ActiveRecovery struct {
	RunID          string
	ExecutionID    string
	InvocationID   string
	CallID         string
	Reason         string
	RetryNotBefore time.Time
	Selected       int // 0: retry/resume, 1: discard
}

// PausedCheckpoint holds execution metadata for an interrupted or paused task.
type PausedCheckpoint struct {
	ExecutionID  string
	ToolCount    int
	LastToolName string
}

// ActiveToolRecovery holds the state of a pending tool recovery decision.
type ActiveToolRecovery struct {
	ExecutionID  string
	InvocationID string
	CallID       string
	ToolName     string
	Arguments    string
	ErrorOutput  string
	Selected     int // 0: retry, 1: skip
}
