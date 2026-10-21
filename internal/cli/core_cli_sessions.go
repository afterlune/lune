package cli

import (
	"context"
	"encoding/json"
	"fmt"
	core "github.com/afterlune/luneCore"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func handleCoreCLIExport(client cliCore, sessionID, line string, stdout, stderr io.Writer) {
	ctx := context.Background()
	saved, err := client.GetSession(ctx, sessionID)
	if err != nil {
		fmt.Fprintf(stderr, "error: get session: %v\n", err)
		return
	}
	fields := strings.Fields(line)
	targetPath := ""
	if len(fields) > 1 {
		targetPath = strings.TrimSpace(strings.Join(fields[1:], " "))
	}
	if targetPath == "" {
		shortID := sessionID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		if shortID == "" {
			shortID = "current"
		}
		targetPath = fmt.Sprintf("export-%s-%s.md", shortID, time.Now().Format("20060102-150405"))
	}
	workspace := client.Workspace()
	if !filepath.IsAbs(targetPath) && workspace != "" {
		targetPath = filepath.Join(workspace, targetPath)
	}

	title := saved.Title
	if title == "" {
		title = "<untitled session>"
	}
	model := client.ModelForSession(ctx, sessionID)
	if model == "" {
		model = "<default>"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Session Transcript: %s\n\n", title))
	sb.WriteString(fmt.Sprintf("- **Session ID:** `%s`\n", sessionID))
	sb.WriteString(fmt.Sprintf("- **Workspace:** `%s`\n", workspace))
	sb.WriteString(fmt.Sprintf("- **Model:** `%s`\n", model))
	sb.WriteString(fmt.Sprintf("- **Exported At:** %s\n", time.Now().Format(time.RFC3339)))
	if saved.SessionUsage.Input > 0 || saved.SessionUsage.Output > 0 {
		sb.WriteString(fmt.Sprintf("- **Token Usage:** %d in (cached: %d) · %d out (reasoning: %d)\n",
			saved.SessionUsage.Input, saved.SessionUsage.Cached,
			saved.SessionUsage.Output, saved.SessionUsage.Reasoning))
	}
	sb.WriteString("\n---\n\n")

	entryCount := 0
	if len(saved.History) > 0 && string(saved.History) != "null" {
		var items []json.RawMessage
		if json.Unmarshal(saved.History, &items) == nil {
			for _, item := range items {
				var val struct {
					Type      string          `json:"type"`
					Role      string          `json:"role"`
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
					Content   json.RawMessage `json:"content"`
				}
				if json.Unmarshal(item, &val) != nil {
					continue
				}
				switch val.Type {
				case "message":
					text := cliExtractMessageContent(val.Content)
					if text == "" {
						continue
					}
					entryCount++
					role := "User"
					if val.Role == "assistant" {
						role = "Assistant"
					}
					sb.WriteString(fmt.Sprintf("### %s\n\n%s\n\n", role, text))
				case "function_call":
					entryCount++
					args := string(val.Arguments)
					if len(args) > 0 && args[0] == '"' {
						var unquoted string
						if json.Unmarshal(val.Arguments, &unquoted) == nil {
							args = unquoted
						}
					}
					sb.WriteString(fmt.Sprintf("<details>\n<summary>Tool: <code>%s</code></summary>\n\n", val.Name))
					if args != "" && args != "null" {
						sb.WriteString(fmt.Sprintf("**Arguments:**\n```json\n%s\n```\n\n", args))
					}
					sb.WriteString("</details>\n\n")
				}
			}
		}
	}

	content := []byte(sb.String())
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		fmt.Fprintf(stderr, "error: create directory: %v\n", err)
		return
	}
	if err := os.WriteFile(targetPath, content, 0644); err != nil {
		fmt.Fprintf(stderr, "error: write file: %v\n", err)
		return
	}
	fmt.Fprintf(stdout, "Exported session transcript to %s (%d bytes, %d items).\n", targetPath, len(content), entryCount)
}

func cliExtractMessageContent(content json.RawMessage) string {
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

func cliExtractToolOutputString(raw json.RawMessage) string {
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

func handleCoreCLIDetail(client cliCore, runtime cliRuntime, sessionID, line string, stdout, stderr io.Writer) {
	ctx := context.Background()
	saved, err := client.GetSession(ctx, sessionID)
	if err != nil {
		fmt.Fprintf(stderr, "error: get session: %v\n", err)
		return
	}

	var tools []cliToolEntry
	if len(saved.History) > 0 && string(saved.History) != "null" {
		var items []json.RawMessage
		if json.Unmarshal(saved.History, &items) == nil {
			for i, item := range items {
				var val struct {
					Type      string          `json:"type"`
					Name      string          `json:"name"`
					CallID    string          `json:"call_id"`
					Arguments json.RawMessage `json:"arguments"`
					Output    json.RawMessage `json:"output"`
				}
				if json.Unmarshal(item, &val) != nil {
					continue
				}
				switch val.Type {
				case "function_call", "custom_tool_call":
					args := string(val.Arguments)
					if len(args) > 0 && args[0] == '"' {
						var unquoted string
						if json.Unmarshal(val.Arguments, &unquoted) == nil {
							args = unquoted
						}
					}
					callID := val.CallID
					if callID == "" {
						callID = fmt.Sprintf("call-%d", i)
					}
					tools = append(tools, cliToolEntry{
						Name:    val.Name,
						Args:    args,
						Command: val.Name + " " + args,
						CallID:  callID,
					})
				case "function_call_output", "custom_tool_call_output":
					outText := cliExtractToolOutputString(val.Output)
					found := false
					if val.CallID != "" {
						for j := len(tools) - 1; j >= 0; j-- {
							if tools[j].CallID == val.CallID {
								tools[j].Output = outText
								found = true
								break
							}
						}
					}
					if !found && len(tools) > 0 {
						for j := len(tools) - 1; j >= 0; j-- {
							if tools[j].Output == "" {
								tools[j].Output = outText
								break
							}
						}
					}
				}
			}
		}
	}

	if len(tools) == 0 {
		fmt.Fprintln(stdout, "No tool executions found in current session.")
		return
	}

	parts := strings.Fields(line)
	target := len(tools) - 1
	if len(parts) > 1 {
		idx, err := strconv.Atoi(parts[1])
		if err != nil || idx < 1 || idx > len(tools) {
			fmt.Fprintf(stderr, "error: tool index %s out of range (1-%d)\n", parts[1], len(tools))
			return
		}
		target = idx - 1
	}

	entry := tools[target]
	cmdStr := entry.Command
	if cmdStr == "" {
		cmdStr = entry.Name
		if entry.Args != "" {
			cmdStr += " " + entry.Args
		}
	}

	fmt.Fprintf(stdout, "--- Tool %d of %d: %s ---\n", target+1, len(tools), cmdStr)
	outTrimmed := strings.TrimSpace(entry.Output)
	if outTrimmed == "" {
		fmt.Fprintln(stdout, "(no output)")
	} else {
		fmt.Fprintln(stdout, outTrimmed)
	}

	if len(tools) > 1 && len(parts) <= 1 {
		fmt.Fprintf(stdout, "\n(Showing latest tool %d of %d. Use '/detail <index>' to inspect earlier tools.)\n", target+1, len(tools))
	}
}

func handleCoreCLIOpen(client cliCore, currentSessionID, line string, stdout, stderr io.Writer) (string, cliRuntime, error) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		fmt.Fprintln(stderr, "usage: /open <session-id|prefix>")
		return currentSessionID, nil, nil
	}
	targetPrefix := fields[1]
	ctx := context.Background()
	targetID := ""
	if _, err := client.GetSession(ctx, targetPrefix); err == nil {
		targetID = targetPrefix
	} else {
		sessions, err := client.ListSessions(ctx, 100, false)
		if err != nil {
			fmt.Fprintf(stderr, "error: query sessions: %v\n", err)
			return currentSessionID, nil, nil
		}
		var matches []core.Session
		for _, s := range sessions {
			if strings.HasPrefix(strings.ToLower(s.ID), strings.ToLower(targetPrefix)) {
				matches = append(matches, s)
			}
		}
		if len(matches) == 0 {
			fmt.Fprintf(stderr, "No session found matching prefix %q.\n", targetPrefix)
			return currentSessionID, nil, nil
		} else if len(matches) > 1 {
			var ids []string
			for _, match := range matches {
				short := match.ID
				if len(short) > 12 {
					short = short[:12]
				}
				ids = append(ids, short)
			}
			fmt.Fprintf(stderr, "Prefix %q is ambiguous. Matches: %s\n", targetPrefix, strings.Join(ids, ", "))
			return currentSessionID, nil, nil
		}
		targetID = matches[0].ID
	}
	if targetID == currentSessionID {
		fmt.Fprintf(stdout, "Already in session %s.\n", targetID)
		return currentSessionID, nil, nil
	}
	rt, err := client.OpenSessionRuntime(ctx, targetID)
	if err != nil {
		fmt.Fprintf(stderr, "error: open session runtime: %v\n", err)
		return currentSessionID, nil, nil
	}
	fmt.Fprintf(stdout, "Switched to session %s.\n", targetID)
	return targetID, rt, nil
}

func handleCoreCLIDrop(client cliCore, currentSessionID string, line string, stdout, stderr io.Writer) {
	fields := strings.Fields(line)
	if len(fields) != 2 {
		fmt.Fprintln(stderr, "usage: /drop [id|prefix]")
		return
	}
	targetPrefix := fields[1]
	ctx := context.Background()
	targetID := ""
	if _, err := client.GetSession(ctx, targetPrefix); err == nil {
		targetID = targetPrefix
	} else {
		sessions, err := client.ListSessions(ctx, 100, false)
		if err != nil {
			fmt.Fprintf(stderr, "error: query sessions: %v\n", err)
			return
		}
		var matches []core.Session
		for _, s := range sessions {
			if strings.HasPrefix(strings.ToLower(s.ID), strings.ToLower(targetPrefix)) {
				matches = append(matches, s)
			}
		}
		if len(matches) == 0 {
			fmt.Fprintf(stderr, "No session found matching prefix %q.\n", targetPrefix)
			return
		} else if len(matches) > 1 {
			var ids []string
			for _, match := range matches {
				short := match.ID
				if len(short) > 12 {
					short = short[:12]
				}
				ids = append(ids, short)
			}
			fmt.Fprintf(stderr, "Prefix %q is ambiguous. Matches: %s\n", targetPrefix, strings.Join(ids, ", "))
			return
		}
		targetID = matches[0].ID
	}
	if targetID == currentSessionID {
		fmt.Fprintln(stderr, "Cannot delete currently active session. Switch to another session first with /open.")
		return
	}
	if err := client.DeleteSession(ctx, targetID); err != nil {
		fmt.Fprintf(stderr, "error: delete session: %v\n", err)
		return
	}
	fmt.Fprintf(stdout, "Deleted session %s.\n", targetID)
}

func handleCoreCLITitle(client cliCore, sessionID, line string, stdout, stderr io.Writer) string {
	fields := strings.Fields(line)
	ctx := context.Background()
	if len(fields) == 1 {
		title := "<untitled>"
		if s, err := client.GetSession(ctx, sessionID); err == nil && s.Title != "" {
			title = s.Title
		}
		fmt.Fprintf(stdout, "Current session title: %s (ID: %s)\n", title, sessionID)
		return title
	}
	newTitle := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
	if err := client.SetSessionTitle(ctx, sessionID, newTitle); err != nil {
		fmt.Fprintf(stderr, "error: set session title: %v\n", err)
		return ""
	}
	fmt.Fprintf(stdout, "Session title set to %q.\n", newTitle)
	return newTitle
}

func handleCoreCLIRewind(client cliCore, sessionID, line string, stdout, stderr io.Writer) {
	fields := strings.Fields(line)
	turns := 1
	if len(fields) > 1 {
		n, err := strconv.Atoi(fields[1])
		if err != nil || n <= 0 {
			fmt.Fprintf(stderr, "usage: /rewind [N] (N must be a positive integer)\n")
			return
		}
		turns = n
	}
	ctx := context.Background()
	removed, err := client.RewindSession(ctx, sessionID, turns)
	if err != nil {
		fmt.Fprintf(stderr, "error: rewind session: %v\n", err)
		return
	}
	fmt.Fprintf(stdout, "Rewound %d turn(s).\n", removed)
}
