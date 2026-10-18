package desktop

import (
	"context"
	"errors"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

func (s *DesktopService) ResolveApproval(sessionID string, id string, allowed bool) error {
	sessionContext, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return err
	}
	defer release()
	runtime := sessionContext.runtime
	if runtime == nil {
		return errors.New("会话已关闭")
	}
	return runtime.ResolveApproval(id, allowed)
}

type QuestionAnswerInput struct {
	ExecutionID  string                             `json:"executionId"`
	RunID        string                             `json:"runId"`
	InvocationID string                             `json:"invocationId"`
	CallID       string                             `json:"callId"`
	Answers      []publicmodel.UserQuestionResponse `json:"answers"`
}

func (s *DesktopService) AnswerQuestion(sessionID string, input QuestionAnswerInput) error {
	sessionContext, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return err
	}
	defer release()
	runtime := sessionContext.runtime
	if runtime == nil {
		return errors.New("会话已关闭")
	}
	return runtime.ResolveUserQuestion(context.Background(), core.UserQuestionAnswer{
		ExecutionID: input.ExecutionID, RunID: input.RunID, InvocationID: input.InvocationID,
		CallID: input.CallID, Answers: input.Answers,
	})
}

func (s *DesktopService) ResolveToolRecovery(sessionID string, invocationID, decision string) error {
	sessionContext, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return err
	}
	defer release()
	runtime := sessionContext.runtime
	if runtime == nil {
		return errors.New("会话已关闭")
	}
	return runtime.ResolveToolRecovery(context.Background(), invocationID, core.ToolRecoveryDecision(decision))
}

func (s *DesktopService) RecoverModel(sessionID string) error {
	sessionContext, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return err
	}
	defer release()
	runtime := sessionContext.runtime
	if runtime == nil {
		return errors.New("会话已关闭")
	}
	submission, err := runtime.Recover(context.Background())
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.watchTurnLocked(runtime, runtime.SessionID(), submission.ExecutionID)
	s.mu.Unlock()
	return nil
}

type PlanResult struct {
	SessionID   string `json:"sessionId"`
	ExecutionID string `json:"executionId"`
}

func (s *DesktopService) ImplementPlan(sessionID, executionID, mode string) (PlanResult, error) {
	c, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return PlanResult{}, err
	}
	defer release()
	sourceContext := c
	runtime := c.runtime
	controls := s.controlsFor(c)
	implementation, err := runtime.ImplementPlan(context.Background(), executionID, core.PlanImplementationOptions{Mode: core.PlanImplementationMode(mode), PermissionMode: core.PermissionMode(controls.PermissionMode)})
	if err != nil {
		return PlanResult{}, err
	}
	s.mu.Lock()
	sourceContext.planMode = false
	targetContext := s.sessionContexts[implementation.SessionID]
	s.mu.Unlock()
	if targetContext == nil {
		if sourceContext == nil {
			return PlanResult{}, errors.New("无法关联方案实施会话")
		}
		targetContext = &desktopSessionContext{
			permissionMode: core.PermissionMode(controls.PermissionMode), core: sourceContext.core, sessionID: implementation.SessionID, workspace: sourceContext.workspace,
		}
		if err := s.attachSessionRuntime(targetContext, true); err != nil {
			return PlanResult{}, err
		}
	}
	s.mu.Lock()
	s.watchTurnLocked(targetContext.runtime, implementation.SessionID, implementation.Submission.ExecutionID)
	s.mu.Unlock()
	return PlanResult{SessionID: implementation.SessionID, ExecutionID: implementation.Submission.ExecutionID}, nil
}

func (s *DesktopService) RestorePlanMode(sessionID string) (SessionControlsView, error) {
	return s.SetPlanMode(sessionID, true)
}

func pendingRequestViews(requests []core.PendingRequest) []PendingRequestView {
	var result []PendingRequestView
	for _, request := range requests {
		view := pendingRequestView(request)
		if view.Kind != "" {
			result = append(result, view)
		}
	}
	return result
}

func pendingRequestView(request core.PendingRequest) PendingRequestView {
	switch request.Kind {
	case core.PendingApproval:
		if request.Approval == nil {
			break
		}
		return PendingRequestView{Kind: string(request.Kind), ID: request.Approval.ID, Tool: request.Approval.Tool, Arguments: request.Approval.Arguments}
	case core.PendingUserQuestion:
		if request.UserQuestion == nil {
			break
		}
		q := request.UserQuestion
		return PendingRequestView{Kind: string(request.Kind), ID: q.ID, ExecutionID: q.ExecutionID, RunID: q.RunID, InvocationID: q.InvocationID, CallID: q.CallID, Questions: q.Questions}
	case core.PendingToolRecovery:
		if request.ToolRecovery == nil {
			break
		}
		r := request.ToolRecovery
		return PendingRequestView{Kind: string(request.Kind), ID: r.InvocationID, InvocationID: r.InvocationID, CallID: r.CallID, Tool: r.Tool, Arguments: r.Arguments}
	case core.PendingModelRecoveryRequest:
		if request.ModelRecovery == nil {
			break
		}
		r := request.ModelRecovery
		return PendingRequestView{Kind: string(request.Kind), ID: r.CallID, CallID: r.CallID, ExecutionID: r.ExecutionID, InvocationID: r.InvocationID, RunID: r.RunID, Reason: string(r.Failure.Category)}
	case core.PendingExecutionBlocker:
		if request.Blocker == nil {
			break
		}
		return PendingRequestView{Kind: string(request.Kind), ID: request.Blocker.ID, ExecutionID: request.Blocker.ExecutionID, Tool: request.Blocker.Tool, Reason: request.Blocker.Reason}
	case core.PendingUserInteraction:
		if request.Interaction == nil {
			break
		}
		return PendingRequestView{Kind: string(request.Kind), ID: request.Interaction.ID, Tool: request.Interaction.ServerName, Arguments: request.Interaction.Request}
	}
	return PendingRequestView{}
}
