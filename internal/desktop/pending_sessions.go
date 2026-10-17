package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	core "github.com/afterlune/luneCore"
)

type PendingSessionSummary struct {
	ID               string                  `json:"id"`
	Title            string                  `json:"title"`
	Mode             string                  `json:"mode"`
	WorkingDirectory string                  `json:"workingDirectory"`
	UpdatedAt        string                  `json:"updatedAt"`
	Requests         []PendingRequestSummary `json:"requests"`
}

type PendingRequestSummary struct {
	Kind string `json:"kind"`
	Tool string `json:"tool,omitempty"`
}

// ListPendingSessions returns a transient, content-free index for every session
// runtime attached to the active Desktop workspace.
func (s *DesktopService) ListPendingSessions() ([]PendingSessionSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := []PendingSessionSummary{}
	if s.workspace == "" {
		return result, nil
	}
	for _, sessionContext := range s.sessionContexts {
		if sessionContext == nil || sessionContext.core == nil || sessionContext.runtime == nil {
			continue
		}
		requests, err := sessionContext.runtime.PendingRequests(context.Background())
		if err != nil {
			return nil, fmt.Errorf("读取会话 %q 的待处理请求失败: %w", sessionContext.sessionID, err)
		}
		summaries := summarizePendingRequests(requests)
		if len(summaries) == 0 {
			continue
		}
		saved, err := sessionContext.core.GetSession(context.Background(), sessionContext.sessionID)
		if err != nil {
			return nil, fmt.Errorf("读取会话 %q 的标题失败: %w", sessionContext.sessionID, err)
		}
		mode := sessionContext.mode
		if mode == "" {
			mode = workspaceModeLocal
		}
		result = append(result, PendingSessionSummary{
			ID: sessionContext.sessionID, Title: saved.Title, Mode: mode,
			WorkingDirectory: sessionContext.workspace, UpdatedAt: saved.UpdatedAt.Format("2006-01-02T15:04:05.999999999Z07:00"),
			Requests: summaries,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt > result[j].UpdatedAt })
	return result, nil
}

func summarizePendingRequests(requests []core.PendingRequest) []PendingRequestSummary {
	result := make([]PendingRequestSummary, 0, len(requests))
	for _, request := range requests {
		var summary PendingRequestSummary
		switch request.Kind {
		case core.PendingApproval:
			if request.Approval == nil {
				continue
			}
			summary = PendingRequestSummary{Kind: string(request.Kind), Tool: request.Approval.Tool}
		case core.PendingUserQuestion:
			if request.UserQuestion == nil {
				continue
			}
			summary.Kind = string(request.Kind)
		case core.PendingUserInteraction:
			if request.Interaction == nil {
				continue
			}
			summary = PendingRequestSummary{Kind: string(request.Kind), Tool: request.Interaction.ServerName}
		case core.PendingToolRecovery:
			if request.ToolRecovery == nil {
				continue
			}
			summary = PendingRequestSummary{Kind: string(request.Kind), Tool: request.ToolRecovery.Tool}
		case core.PendingModelRecoveryRequest:
			if request.ModelRecovery == nil {
				continue
			}
			summary.Kind = string(request.Kind)
		case core.PendingExecutionBlocker:
			if request.Blocker == nil {
				continue
			}
			summary = PendingRequestSummary{Kind: string(request.Kind), Tool: request.Blocker.Tool}
		default:
			continue
		}
		result = append(result, summary)
	}
	return result
}

func (s *DesktopService) ResolveUserInteraction(id, responseJSON string) error {
	if id == "" || !json.Valid([]byte(responseJSON)) {
		return errors.New("MCP 交互响应无效")
	}
	s.mu.Lock()
	runtime := s.runtime
	s.mu.Unlock()
	if runtime == nil {
		return errors.New("会话已关闭")
	}
	return runtime.ResolveUserInteraction(id, json.RawMessage(responseJSON))
}

func (s *DesktopService) OpenMCPURL(serverName, target string) error {
	s.mu.Lock()
	activeCore := s.core
	s.mu.Unlock()
	if activeCore == nil {
		return errors.New("没有打开会话")
	}
	return activeCore.OpenMCPURL(serverName, target)
}
