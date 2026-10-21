package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tuiMarkdown "github.com/afterlune/lune/internal/tui/markdown"
	core "github.com/afterlune/luneCore"
)

// transcriptFromHistory restores conversation transcript entries from stored JSON history.
func transcriptFromHistory(history json.RawMessage) []TranscriptEntry {
	if len(history) == 0 || string(history) == "null" {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(history, &items); err != nil {
		return nil
	}
	var entries []TranscriptEntry
	for i, item := range items {
		var value struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			Name      string          `json:"name"`
			CallID    string          `json:"call_id"`
			Arguments json.RawMessage `json:"arguments"`
			Content   json.RawMessage `json:"content"`
			Output    json.RawMessage `json:"output"`
		}
		if err := json.Unmarshal(item, &value); err != nil {
			continue
		}
		switch value.Type {
		case "message":
			text := messageContentText(value.Content)
			if text == "" {
				continue
			}
			kind := KindAssistant
			if value.Role == "user" {
				kind = KindUser
			}
			if kind == KindAssistant && strings.Contains(text, "<proposed_plan>") {
				planText, surrounding := tuiMarkdown.ExtractProposedPlan(text)
				if surrounding != "" {
					entries = append(entries, TranscriptEntry{
						ID:        fmt.Sprintf("hist-%d", i),
						Kind:      KindAssistant,
						Text:      surrounding,
						Timestamp: time.Now(),
					})
				}
				if planText != "" {
					entries = append(entries, TranscriptEntry{
						ID:        fmt.Sprintf("hist-plan-%d", i),
						Kind:      KindPlan,
						Text:      planText,
						Timestamp: time.Now(),
					})
				}
				continue
			}
			if kind == KindAssistant {
				text = tuiMarkdown.CleanProposedPlanTags(text)
			}
			entries = append(entries, TranscriptEntry{
				ID:        fmt.Sprintf("hist-%d", i),
				Kind:      kind,
				Text:      text,
				Timestamp: time.Now(),
			})
		case "function_call", "custom_tool_call":
			if strings.EqualFold(value.Name, "select_tool_category") {
				continue
			}
			args := string(value.Arguments)
			if len(args) > 0 && args[0] == '"' {
				var unquoted string
				if json.Unmarshal(value.Arguments, &unquoted) == nil {
					args = unquoted
				}
			}
			callID := value.CallID
			if callID == "" {
				callID = fmt.Sprintf("hist-tool-%d", i)
			}
			entries = append(entries, TranscriptEntry{
				ID:          fmt.Sprintf("hist-tool-%d", i),
				Kind:        KindTool,
				ToolCallID:  callID,
				ToolName:    value.Name,
				ToolArgs:    args,
				ToolCommand: formatToolSummary(value.Name, args, ""),
				Timestamp:   time.Now(),
			})
		case "function_call_output", "custom_tool_call_output":
			outText := extractToolOutputString(value.Output)
			found := false
			if value.CallID != "" {
				for j := len(entries) - 1; j >= 0; j-- {
					if entries[j].Kind == KindTool && entries[j].ToolCallID == value.CallID {
						entries[j].ToolOutput = outText
						found = true
						break
					}
				}
			}
			if !found && len(entries) > 0 {
				for j := len(entries) - 1; j >= 0; j-- {
					if entries[j].Kind == KindTool && entries[j].ToolOutput == "" {
						entries[j].ToolOutput = outText
						break
					}
				}
			}
		}
	}
	return entries
}

func isTrivialGreeting(text string) bool {
	s := strings.ToLower(strings.TrimSpace(text))
	s = strings.Trim(s, " \t\r\n!?.。,，~`'\"！？；：、～…")
	switch s {
	case "你好", "您好", "hi", "hello", "hey", "halo", "hola", "hi there", "hello there",
		"早上好", "中午好", "下午好", "晚上好", "早", "早安", "午安", "晚安",
		"在吗", "在不在", "hi lune", "hello lune", "你好lune", "你好 lune":
		return true
	}
	return false
}

func deriveSessionTitleFromEntries(entries []TranscriptEntry) string {
	var fallback string
	for _, entry := range entries {
		if entry.Kind == KindUser && strings.TrimSpace(entry.Text) != "" {
			title := deriveSessionTitleText(entry.Text)
			if title == "" {
				continue
			}
			if !isTrivialGreeting(entry.Text) {
				return title
			}
			if fallback == "" {
				fallback = title
			}
		}
	}
	return fallback
}

func deriveSessionTitleText(text string) string {
	line := strings.TrimSpace(text)
	if idx := strings.IndexAny(line, "\r\n"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	if line == "" {
		return ""
	}
	return truncateString(line, 40)
}

func sessionDisplayTitle(s core.Session) string {
	if t := strings.TrimSpace(s.Title); t != "" && !isTrivialGreeting(t) {
		return t
	}
	if len(s.History) > 0 && string(s.History) != "null" {
		if t := deriveSessionTitleFromEntries(transcriptFromHistory(s.History)); t != "" {
			return t
		}
	}
	if t := strings.TrimSpace(s.Title); t != "" {
		return t
	}
	return "<untitled>"
}

func extractResponseText(response json.RawMessage) string {
	if len(response) == 0 || string(response) == "null" {
		return ""
	}
	var envelope struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return ""
	}
	var parts []string
	for _, item := range envelope.Output {
		var value struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(item, &value); err != nil {
			continue
		}
		if value.Type == "message" && (value.Role == "" || value.Role == "assistant") {
			if text := strings.TrimSpace(messageContentText(value.Content)); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

func extractToolOutputString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return str
	}
	var parts []json.RawMessage
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			var part struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(p, &part) == nil && part.Text != "" {
				b.WriteString(part.Text)
			}
		}
		if b.Len() > 0 {
			return b.String()
		}
	}
	var obj struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil && obj.Text != "" {
		return obj.Text
	}
	return string(raw)
}

func messageContentText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var parts []json.RawMessage
	if json.Unmarshal(content, &parts) != nil {
		return ""
	}
	var out strings.Builder
	for _, raw := range parts {
		var part struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(raw, &part) == nil && (part.Type == "input_text" || part.Type == "output_text") {
			out.WriteString(part.Text)
		}
	}
	return out.String()
}
