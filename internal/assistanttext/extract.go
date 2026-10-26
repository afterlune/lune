// Package assistanttext extracts user-visible assistant text from model
// Responses payloads. Control items, reasoning, tool calls, and opaque provider
// extensions are intentionally omitted.
package assistanttext

import (
	"encoding/json"
	"strings"
)

// Extract returns assistant message text from a Responses-style payload.
func Extract(response json.RawMessage) (string, error) {
	if len(response) == 0 || string(response) == "null" {
		return "", nil
	}
	var envelope struct {
		Output []json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(response, &envelope); err != nil {
		return "", err
	}
	var messages []string
	for _, item := range envelope.Output {
		var value struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(item, &value); err != nil {
			continue
		}
		if value.Type != "message" || (value.Role != "" && value.Role != "assistant") {
			continue
		}
		if text := strings.TrimSpace(ContentText(value.Content, false)); text != "" {
			messages = append(messages, text)
		}
	}
	return strings.Join(messages, "\n\n"), nil
}

// ContentText extracts text parts. includeInputText is used when restoring user
// messages from conversation history; model responses should leave it false.
func ContentText(content json.RawMessage, includeInputText bool) string {
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
		if json.Unmarshal(raw, &part) != nil {
			continue
		}
		if part.Type == "output_text" || part.Type == "text" || (includeInputText && part.Type == "input_text") {
			out.WriteString(part.Text)
		}
	}
	return out.String()
}
