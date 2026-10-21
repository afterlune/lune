package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

type AgentEvent struct {
	Kind            string       `json:"kind"`
	SessionID       string       `json:"sessionId"`
	TaskID          string       `json:"taskId"`
	ExecutionID     string       `json:"executionId"`
	Status          string       `json:"status"`
	Err             string       `json:"error,omitempty"`
	CommandID       string       `json:"commandId,omitempty"`
	CommandStatus   string       `json:"commandStatus,omitempty"`
	CommandExitCode *int         `json:"commandExitCode,omitempty"`
	Tool            ToolEvent    `json:"tool"`
	Stream          *StreamEvent `json:"stream,omitempty"`
	ApprovalID      string       `json:"approvalId,omitempty"`
}

type ToolEvent struct {
	ID           string                    `json:"id"`
	InvocationID string                    `json:"invocationId"`
	AgentID      string                    `json:"agentId,omitempty"`
	Name         string                    `json:"name"`
	Arguments    json.RawMessage           `json:"arguments,omitempty"`
	Details      *ToolExecutionDetailsView `json:"details,omitempty"`
	Started      bool                      `json:"started"`
	Failed       bool                      `json:"failed"`
	Canceled     bool                      `json:"canceled"`
	Duration     int64                     `json:"durationMs"`
	Truncated    bool                      `json:"truncated,omitempty"`
}

type StreamEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

func (s *DesktopService) subscribeEventsLocked(sessionContext *desktopSessionContext, replayFromStart bool) {
	cursor := int64(0)
	if !replayFromStart {
		cursor = latestEventCursor(sessionContext.runtime)
	}
	base := s.workspaceContext
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	sessionContext.eventCancel = cancel
	done := make(chan struct{})
	sessionContext.eventDone = done
	events, err := sessionContext.runtime.SubscribeEvents(ctx, cursor)
	if err != nil {
		cancel()
		close(done)
		s.emit("agent-error", map[string]string{"message": err.Error(), "sessionId": sessionContext.sessionID})
		return
	}
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				if s.persistActivityEvent(event) {
					continue
				}
				s.emit("agent-event", toAgentEvent(event))
				if pendingRequestChanged(event.Kind) {
					s.emit("pending-changed", sessionContext.sessionID)
				}
			}
		}
	}()
}

func (s *DesktopService) persistActivityEvent(event core.Event) bool {
	s.mu.Lock()
	if _, suppressed := s.suppressedActivity[event.ExecutionID]; suppressed {
		s.mu.Unlock()
		return true
	}
	store := s.activityStore
	s.mu.Unlock()
	if store == nil || event.SessionID == "" || event.ExecutionID == "" {
		return false
	}
	ctx := context.Background()
	switch event.Kind {
	case core.EventTaskQueued:
		if err := store.UpsertExecution(ctx, event.SessionID, event.ExecutionID, nil, "queued"); err != nil {
			slog.Warn("save queued desktop execution activity", "error", err)
		}
		if err := store.UpdateExecutionMessageStatus(ctx, event.SessionID, event.ExecutionID, "queued", 0); err != nil {
			slog.Warn("save queued desktop message status", "error", err)
		}
	case core.EventTaskStarted:
		if err := store.UpsertExecution(ctx, event.SessionID, event.ExecutionID, nil, "running"); err != nil {
			slog.Warn("save desktop execution activity", "error", err)
		}
		if err := store.UpdateExecutionMessageStatus(ctx, event.SessionID, event.ExecutionID, "running", 0); err != nil {
			slog.Warn("save running desktop message status", "error", err)
		}
	case core.EventTaskFinished:
		status := activityStatus(string(event.Status))
		if err := store.UpsertExecution(ctx, event.SessionID, event.ExecutionID, nil, status); err != nil {
			slog.Warn("save desktop execution activity status", "error", err)
		}
		if err := store.UpdateExecutionMessageStatus(ctx, event.SessionID, event.ExecutionID, status, 0); err != nil {
			slog.Warn("save finished desktop message status", "error", err)
		}
	case core.EventTaskDiscarded:
		if err := store.UpsertExecution(ctx, event.SessionID, event.ExecutionID, nil, "interrupted"); err != nil {
			slog.Warn("save interrupted execution activity", "error", err)
		}
		if err := store.UpdateExecutionMessageStatus(ctx, event.SessionID, event.ExecutionID, "interrupted", 0); err != nil {
			slog.Warn("remove discarded desktop message receipt", "error", err)
		}
	case core.EventToolDetails:
		if err := store.UpsertExecution(ctx, event.SessionID, event.ExecutionID, nil, "running"); err != nil {
			slog.Warn("save desktop execution activity", "error", err)
		}
		tool := toolActivityView(event)
		if err := store.UpsertTool(ctx, event.SessionID, event.ExecutionID, tool); err != nil {
			slog.Warn("save desktop tool activity", "error", err)
		}
	}
	return false
}

func (s *DesktopService) suppressExecutionActivity(executionIDs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(executionIDs) == 0 {
		return
	}
	if s.suppressedActivity == nil {
		s.suppressedActivity = make(map[string]struct{}, len(executionIDs))
	}
	for _, executionID := range executionIDs {
		if executionID != "" {
			s.suppressedActivity[executionID] = struct{}{}
		}
	}
}

func activityStatus(status string) string {
	switch strings.ToLower(status) {
	case "completed":
		return "completed"
	case "failed":
		return "failed"
	case "cancelled", "canceled":
		return "cancelled"
	case "discarded":
		return "interrupted"
	case "paused":
		return "paused"
	default:
		return "running"
	}
}

func toolActivityView(event core.Event) ToolActivityView {
	tool := event.Tool
	status := "done"
	if tool.Started {
		status = "running"
	} else if tool.Failed {
		status = "failed"
	} else if tool.Canceled {
		status = "cancelled"
	}
	view := ToolActivityView{
		ID: tool.ID, InvocationID: tool.InvocationID, AgentID: tool.AgentID, Name: tool.Name,
		Arguments: append(json.RawMessage(nil), tool.Arguments...), Status: status, DurationMs: tool.Duration.Milliseconds(),
	}
	if tool.Details != nil {
		details := &ToolExecutionDetailsView{Output: tool.Details.Output}
		if command := tool.Details.Command; command != nil {
			details.Command = &ActivityCommandOutput{
				Text: command.Command.Command, Output: command.Output, Status: string(command.Command.Status),
				ExitCode: command.Command.ExitCode, Truncated: command.Truncated, Error: command.Error,
			}
		}
		for _, change := range tool.Details.Changes {
			converted := ActivityFileChange{Path: change.Path, Added: change.Added, Removed: change.Removed, Truncated: change.Truncated}
			for _, line := range change.Lines {
				converted.Lines = append(converted.Lines, ActivityDiffLine{Kind: line.Kind, OldLine: line.OldLine, NewLine: line.NewLine, Text: line.Text})
			}
			details.Changes = append(details.Changes, converted)
		}
		view.Details = details
	}
	if payload, err := boundedToolActivityPayload(view); err == nil {
		_ = json.Unmarshal(payload, &view)
	}
	return view
}

func pendingRequestChanged(kind core.EventKind) bool {
	switch kind {
	case core.EventApprovalRequired,
		core.EventApprovalCancelled,
		core.EventUserQuestionRequired,
		core.EventUserInteractionRequired,
		core.EventUserInteractionCancelled,
		core.EventExecutionBlocked,
		core.EventModelRecoveryRequired,
		core.EventModelRecoveryResumed,
		core.EventToolRecoveryRequired:
		return true
	default:
		return false
	}
}

func latestEventCursor(runtime *core.SessionRuntime) int64 {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events, err := runtime.SubscribeEvents(ctx, 0)
	if err != nil {
		return 0
	}
	var cursor int64
	idle := time.NewTimer(30 * time.Millisecond)
	defer idle.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return cursor
			}
			if event.Cursor > cursor {
				cursor = event.Cursor
				if !idle.Stop() {
					select {
					case <-idle.C:
					default:
					}
				}
				idle.Reset(30 * time.Millisecond)
			}
		case <-idle.C:
			return cursor
		case <-ctx.Done():
			return cursor
		}
	}
}

func toAgentEvent(event core.Event) AgentEvent {
	tool := toolActivityView(event)
	result := AgentEvent{
		Kind: string(event.Kind), SessionID: event.SessionID, TaskID: event.TaskID, ExecutionID: event.ExecutionID,
		Status: string(event.Status), Err: event.Err, ApprovalID: event.ApprovalID,
		CommandID: event.CommandID, CommandStatus: string(event.CommandStatus), CommandExitCode: event.CommandExitCode,
		Tool: ToolEvent{ID: event.Tool.ID, InvocationID: event.Tool.InvocationID, AgentID: event.Tool.AgentID,
			Name: event.Tool.Name, Arguments: tool.Arguments, Details: tool.Details,
			Started: event.Tool.Started, Failed: event.Tool.Failed, Canceled: event.Tool.Canceled,
			Duration: event.Tool.Duration.Milliseconds(), Truncated: tool.Truncated},
	}
	if event.Stream != nil {
		result.Stream = &StreamEvent{Type: event.Stream.Type, Delta: event.Stream.Delta}
	}
	return result
}

func (s *DesktopService) watchTurnLocked(runtime *core.SessionRuntime, sessionID, executionID string) {
	if executionID == "" || s.waiting[executionID] {
		return
	}
	if s.waiting == nil {
		s.waiting = make(map[string]bool)
	}
	s.waiting[executionID] = true
	base := s.workspaceContext
	if base == nil {
		base = context.Background()
	}
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		defer func() {
			s.mu.Lock()
			delete(s.waiting, executionID)
			s.mu.Unlock()
			s.releaseInactiveSessionRuntime(sessionID)
		}()
		ctx, cancel := context.WithTimeout(base, 24*time.Hour)
		defer cancel()
		result, err := runtime.WaitTurn(ctx, executionID)
		if err != nil {
			if ctx.Err() == nil && !s.isExecutionActivitySuppressed(executionID) {
				s.emit("turn-result", TurnResultView{SessionID: sessionID, ExecutionID: executionID, Error: err.Error()})
			}
			return
		}
		plan := ""
		if result.ProposedPlan != nil {
			plan = result.ProposedPlan.Text
		}
		text := responseText(result.Response)
		if plan != "" {
			text = stripProposedPlan(text)
		}
		if ctx.Err() == nil && !s.isExecutionActivitySuppressed(executionID) {
			s.emit("turn-result", TurnResultView{SessionID: sessionID, ExecutionID: executionID, Status: string(result.Status), Text: text, Plan: plan})
		}
	}()
}

func (s *DesktopService) isExecutionActivitySuppressed(executionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, suppressed := s.suppressedActivity[executionID]
	return suppressed
}

type TurnResultView struct {
	SessionID   string `json:"sessionId"`
	ExecutionID string `json:"executionId"`
	Status      string `json:"status"`
	Text        string `json:"text"`
	Plan        string `json:"plan,omitempty"`
	Error       string `json:"error,omitempty"`
}

func responseText(raw json.RawMessage) string {
	var response publicmodel.ModelResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return ""
	}
	var parts []string
	for _, item := range response.Output {
		message, ok := item.(publicmodel.OutputMessage)
		if !ok {
			continue
		}
		for _, part := range message.Content {
			if output, ok := part.(publicmodel.OutputText); ok {
				parts = append(parts, output.Text)
			}
		}
	}
	return strings.Join(parts, "")
}

func stripProposedPlan(text string) string {
	const openTag = "<proposed_plan>"
	start := strings.Index(text, openTag)
	if start < 0 {
		return text
	}
	innerStart := start + len(openTag)
	canonical := strings.Index(text[innerStart:], "</proposed_plan>")
	compat := strings.Index(text[innerStart:], "<proposed_plan_end/>")
	end, closeLength := -1, 0
	switch {
	case canonical < 0 && compat < 0:
		return text
	case canonical < 0:
		end, closeLength = compat, len("<proposed_plan_end/>")
	case compat < 0 || canonical < compat:
		end, closeLength = canonical, len("</proposed_plan>")
	default:
		end, closeLength = compat, len("<proposed_plan_end/>")
	}
	if end < 0 {
		return text
	}
	end += innerStart + closeLength
	return strings.TrimSpace(text[:start] + text[end:])
}

func transcriptMessages(raw json.RawMessage) []ConversationMessage {
	var input publicmodel.ResponseInput
	if len(raw) == 0 || json.Unmarshal(raw, &input) != nil {
		return nil
	}
	if input.Text != nil {
		if *input.Text == "" {
			return nil
		}
		return []ConversationMessage{{ID: "history-0", Role: "user", Text: *input.Text}}
	}
	var result []ConversationMessage
	for index, item := range input.Items {
		message, ok := item.(publicmodel.InputMessage)
		if !ok || (message.Role != publicmodel.RoleUser && message.Role != publicmodel.RoleAssistant) {
			continue
		}
		view := ConversationMessage{ID: message.ID, Role: string(message.Role)}
		if view.ID == "" {
			view.ID = fmt.Sprintf("history-%d", index)
		}
		if message.Content.Text != nil {
			view.Text = *message.Content.Text
		}
		for _, part := range message.Content.Parts {
			switch value := part.(type) {
			case publicmodel.InputText:
				view.Text += value.Text
			case publicmodel.OutputText:
				view.Text += value.Text
			case publicmodel.InputImage:
				if strings.HasPrefix(value.ImageURL, "data:image/") {
					view.Images = append(view.Images, value.ImageURL)
				}
			}
		}
		if view.Role == string(publicmodel.RoleAssistant) {
			view.Text = stripProposedPlan(view.Text)
		}
		if strings.TrimSpace(view.Text) != "" || len(view.Images) > 0 {
			result = append(result, view)
		}
	}
	return result
}

func (s *DesktopService) emit(name string, data ...any) {
	s.mu.Lock()
	app := s.app
	s.mu.Unlock()
	if app != nil {
		app.Event.Emit(name, data...)
	}
}
