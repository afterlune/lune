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
)

// TranscriptEntry represents a single visual block in the conversation transcript.
type TranscriptEntry struct {
	ID        string
	Kind      EntryKind
	Text      string
	Timestamp time.Time

	// Tool specific fields
	ToolCallID   string
	ToolName     string
	ToolArgs     string
	ToolCommand  string
	ToolStatus   publictool.CommandStatus
	ToolExitCode *int
	ToolOutput   string
	ToolExpanded bool

	// Question answered specific fields
	QuestionText string
	AnswerText   string
}

// AutocompleteItem represents a slash command suggestion.
type AutocompleteItem struct {
	Command     string
	Description string
}

// QuestionOption represents a suggested option in an interactive clarification question.
type QuestionOption struct {
	Key         string
	Label       string
	Description string
}

// ActiveQuestion holds the state of a pending clarification question.
type ActiveQuestion struct {
	Request   core.UserQuestionRequest
	Selected  int
	Custom    string
	NotesOpen bool
	NoteText  string
	Answered  bool
	Error     string
}

// ActivePlanReview holds the state of a pending plan review.
type ActivePlanReview struct {
	ExecutionID string
	Text        string
	Selected    int // 0: implement, 1: clear & implement, 2: cancel/stay
}

// ActiveApproval holds the state of a pending tool execution approval.
type ActiveApproval struct {
	Request  core.ApprovalRequest
	Selected int // 0: allow, 1: deny
}

// ImageAttachment represents a pasted clipboard image.
type ImageAttachment struct {
	ID        int
	Name      string
	MediaType string
	Data      []byte
	Size      int64
}
