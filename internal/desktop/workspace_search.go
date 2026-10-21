package desktop

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	core "github.com/afterlune/luneCore"
)

type SessionSearchResult struct {
	SessionID        string `json:"sessionId"`
	SessionTitle     string `json:"sessionTitle"`
	WorkingDirectory string `json:"workingDirectory"`
	UpdatedAt        string `json:"updatedAt"`
	MatchKind        string `json:"matchKind"`
	MessageID        string `json:"messageId,omitempty"`
	MessageIndex     int    `json:"messageIndex"`
	Role             string `json:"role,omitempty"`
	SnippetBefore    string `json:"snippetBefore"`
	SnippetMatch     string `json:"snippetMatch"`
	SnippetAfter     string `json:"snippetAfter"`
	TitleMatched     bool   `json:"titleMatched,omitempty"`
}

type sessionSearchCandidate struct {
	result    SessionSearchResult
	updatedAt time.Time
}

const (
	sessionSearchPageSize      = 100
	sessionSearchMaxResults    = 100
	sessionSearchMaxPerSession = 3
)

func (s *DesktopService) SearchWorkspaceConversation(query string) ([]SessionSearchResult, error) {
	s.workspaceMu.RLock()
	defer s.workspaceMu.RUnlock()
	query = strings.Join(strings.Fields(query), " ")
	if query == "" {
		return []SessionSearchResult{}, nil
	}
	if len([]rune(query)) > 256 {
		return nil, errors.New("搜索内容不能超过 256 个字符")
	}

	s.mu.Lock()
	localCore, workspace := s.localCore, s.workspace
	if localCore == nil {
		localCore = s.activeCoreLocked()
	}
	s.mu.Unlock()
	if localCore == nil || workspace == "" {
		return nil, errors.New("请先打开工作区")
	}

	candidates := make([]sessionSearchCandidate, 0)
	ctx := context.Background()
	for offset := 0; ; offset += sessionSearchPageSize {
		page, err := localCore.ListSessionPage(ctx, offset, sessionSearchPageSize, false)
		if err != nil {
			return nil, fmt.Errorf("读取本地会话历史: %w", err)
		}
		for _, saved := range page {
			candidates = append(candidates, searchSession(saved, workspace, query)...)
		}
		if len(page) < sessionSearchPageSize {
			break
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if !left.updatedAt.Equal(right.updatedAt) {
			return left.updatedAt.After(right.updatedAt)
		}
		if left.result.SessionID == right.result.SessionID && left.result.MessageIndex != right.result.MessageIndex {
			return left.result.MessageIndex > right.result.MessageIndex
		}
		if left.result.SessionID != right.result.SessionID {
			return left.result.SessionID < right.result.SessionID
		}
		return left.result.MatchKind < right.result.MatchKind
	})
	if len(candidates) > sessionSearchMaxResults {
		candidates = candidates[:sessionSearchMaxResults]
	}
	results := make([]SessionSearchResult, 0, len(candidates))
	for _, candidate := range candidates {
		results = append(results, candidate.result)
	}
	return results, nil
}

func searchSession(saved core.Session, workingDirectory, query string) []sessionSearchCandidate {
	title := strings.TrimSpace(saved.Title)
	titleBefore, titleMatch, titleAfter, titleMatched := searchSnippet(title, query)
	messages := transcriptMessages(saved.History)
	results := make([]sessionSearchCandidate, 0, sessionSearchMaxPerSession)
	for index := len(messages) - 1; index >= 0 && len(results) < sessionSearchMaxPerSession; index-- {
		message := messages[index]
		before, match, after, ok := searchSnippet(message.Text, query)
		if !ok {
			continue
		}
		results = append(results, sessionSearchCandidate{
			result: SessionSearchResult{
				SessionID: saved.ID, SessionTitle: title, WorkingDirectory: workingDirectory,
				UpdatedAt: saved.UpdatedAt.Format(time.RFC3339), MatchKind: "message", MessageID: message.ID,
				MessageIndex: index, Role: message.Role, SnippetBefore: before, SnippetMatch: match, SnippetAfter: after,
				TitleMatched: titleMatched,
			},
			updatedAt: saved.UpdatedAt,
		})
	}
	if len(results) == 0 && titleMatched {
		results = append(results, sessionSearchCandidate{
			result: SessionSearchResult{
				SessionID: saved.ID, SessionTitle: title, WorkingDirectory: workingDirectory,
				UpdatedAt: saved.UpdatedAt.Format(time.RFC3339), MatchKind: "title", MessageIndex: -1,
				SnippetBefore: titleBefore, SnippetMatch: titleMatch, SnippetAfter: titleAfter,
			},
			updatedAt: saved.UpdatedAt,
		})
	}
	return results
}

func searchSnippet(text, query string) (before, match, after string, ok bool) {
	text = strings.Join(strings.Fields(text), " ")
	textRunes := []rune(text)
	lowerText := []rune(strings.ToLower(text))
	lowerQuery := []rune(strings.ToLower(query))
	if len(lowerQuery) == 0 || len(lowerQuery) > len(lowerText) {
		return "", "", "", false
	}
	start := -1
	for index := 0; index+len(lowerQuery) <= len(lowerText); index++ {
		matched := true
		for queryIndex := range lowerQuery {
			if lowerText[index+queryIndex] != lowerQuery[queryIndex] {
				matched = false
				break
			}
		}
		if matched {
			start = index
			break
		}
	}
	if start < 0 {
		return "", "", "", false
	}
	end := start + len(lowerQuery)
	const snippetRunes = 42
	beforeStart := max(0, start-snippetRunes)
	afterEnd := min(len(textRunes), end+snippetRunes)
	before = string(textRunes[beforeStart:start])
	match = string(textRunes[start:end])
	after = string(textRunes[end:afterEnd])
	if beforeStart > 0 {
		before = "…" + before
	}
	if afterEnd < len(textRunes) {
		after += "…"
	}
	return before, match, after, true
}
