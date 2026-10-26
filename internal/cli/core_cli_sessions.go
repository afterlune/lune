package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/afterlune/lune/internal/settings"
	"github.com/afterlune/lune/internal/terminaltext"
	core "github.com/afterlune/luneCore"
)

func handleCoreCLIExport(client cliCore, sessionID, line string, stdout, stderr io.Writer) {
	ctx := context.Background()
	saved, err := client.GetSession(ctx, sessionID)
	if err != nil {
		fmt.Fprintf(stderr, "error: get session: %v\n", err)
		return
	}
	fields := strings.Fields(line)
	format := "md"
	targetPath := ""
	if len(fields) > 1 {
		first := strings.ToLower(fields[1])
		switch first {
		case "html", "htm":
			format = "html"
			if len(fields) > 2 {
				targetPath = strings.TrimSpace(strings.Join(fields[2:], " "))
			}
		case "jsonl", "json":
			format = "jsonl"
			if len(fields) > 2 {
				targetPath = strings.TrimSpace(strings.Join(fields[2:], " "))
			}
		case "md", "markdown":
			format = "md"
			if len(fields) > 2 {
				targetPath = strings.TrimSpace(strings.Join(fields[2:], " "))
			}
		default:
			targetPath = strings.TrimSpace(strings.Join(fields[1:], " "))
			ext := strings.ToLower(filepath.Ext(targetPath))
			switch ext {
			case ".html", ".htm":
				format = "html"
			case ".jsonl", ".json":
				format = "jsonl"
			default:
				format = "md"
			}
		}
	}
	shortID := sessionID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	if shortID == "" {
		shortID = "current"
	}
	timestamp := time.Now().Format("20060102-150405")
	if targetPath == "" {
		targetPath = fmt.Sprintf("export-%s-%s.%s", shortID, timestamp, format)
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

	var content []byte
	entryCount := 0

	switch format {
	case "jsonl":
		var lines []string
		if len(saved.History) > 0 && string(saved.History) != "null" {
			var items []json.RawMessage
			if json.Unmarshal(saved.History, &items) == nil {
				for _, item := range items {
					lines = append(lines, string(item))
					entryCount++
				}
			}
		}
		content = []byte(strings.Join(lines, "\n") + "\n")

	case "html":
		content = cliGenerateHTMLExport(saved, sessionID, title, model, workspace, &entryCount)

	default: // "md"
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
		content = []byte(sb.String())
	}

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

func cliGenerateHTMLExport(saved core.Session, sessionID, title, model, workspace string, entryCount *int) []byte {
	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n")
	sb.WriteString("<meta charset=\"UTF-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1.0\">\n")
	sb.WriteString(fmt.Sprintf("<title>%s</title>\n", html.EscapeString(title)))
	sb.WriteString(`<style>
:root { --bg: #0f141c; --card: #18202c; --text: #e6edf3; --muted: #8b949e; --border: #30363d; }
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; background: var(--bg); color: var(--text); margin: 0; padding: 24px; }
.container { max-width: 900px; margin: 0 auto; }
.header { background: var(--card); border: 1px solid var(--border); border-radius: 8px; padding: 20px; margin-bottom: 24px; }
h1 { margin: 0 0 12px 0; font-size: 24px; }
.meta { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 12px; font-size: 13px; color: var(--muted); }
.message { background: var(--card); border: 1px solid var(--border); border-radius: 8px; padding: 16px; margin-bottom: 16px; }
.role { font-weight: bold; margin-bottom: 8px; }
.role-user { color: #58a6ff; }
.role-assistant { color: #3fb950; }
pre { background: #0d1117; padding: 12px; border-radius: 6px; overflow-x: auto; font-size: 13px; }
code { font-family: ui-monospace, SFMono-Regular, SF Mono, Menlo, Consolas, Liberation Mono, monospace; }
details { background: #161b22; border: 1px solid var(--border); border-radius: 6px; padding: 8px 12px; margin-bottom: 12px; }
summary { cursor: pointer; color: var(--muted); font-size: 13px; }
</style>
</head>
<body>
<div class="container">
`)
	sb.WriteString("<div class=\"header\">\n")
	sb.WriteString(fmt.Sprintf("<h1>%s</h1>\n<div class=\"meta\">\n", html.EscapeString(title)))
	sb.WriteString(fmt.Sprintf("<div><strong>Session ID:</strong> <code>%s</code></div>\n", html.EscapeString(sessionID)))
	sb.WriteString(fmt.Sprintf("<div><strong>Workspace:</strong> <code>%s</code></div>\n", html.EscapeString(workspace)))
	sb.WriteString(fmt.Sprintf("<div><strong>Model:</strong> %s</div>\n", html.EscapeString(model)))
	sb.WriteString(fmt.Sprintf("<div><strong>Exported:</strong> %s</div>\n", time.Now().Format("2006-01-02 15:04:05")))
	if saved.SessionUsage.Input > 0 || saved.SessionUsage.Output > 0 {
		sb.WriteString(fmt.Sprintf("<div><strong>Tokens:</strong> %d in · %d out</div>\n", saved.SessionUsage.Input, saved.SessionUsage.Output))
	}
	sb.WriteString("</div>\n</div>\n\n")

	count := 0
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
					count++
					roleClass := "role-user"
					roleName := "👤 User"
					if val.Role == "assistant" {
						roleClass = "role-assistant"
						roleName = "🤖 Assistant"
					}
					sb.WriteString("<div class=\"message\">\n")
					sb.WriteString(fmt.Sprintf("<div class=\"role %s\">%s</div>\n", roleClass, roleName))
					sb.WriteString(fmt.Sprintf("<div>%s</div>\n", html.EscapeString(text)))
					sb.WriteString("</div>\n")
				case "function_call":
					count++
					args := string(val.Arguments)
					if len(args) > 0 && args[0] == '"' {
						var unquoted string
						if json.Unmarshal(val.Arguments, &unquoted) == nil {
							args = unquoted
						}
					}
					sb.WriteString("<details>\n")
					sb.WriteString(fmt.Sprintf("<summary>🔧 Tool: <code>%s</code></summary>\n", html.EscapeString(val.Name)))
					if args != "" && args != "null" {
						sb.WriteString(fmt.Sprintf("<pre><code>%s</code></pre>\n", html.EscapeString(args)))
					}
					sb.WriteString("</details>\n")
				}
			}
		}
	}
	sb.WriteString("</div>\n</body>\n</html>\n")
	*entryCount = count
	return []byte(sb.String())
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

	fmt.Fprintf(stdout, "--- Tool %d of %d: %s ---\n", target+1, len(tools), cleanCLIInlineText(cmdStr))
	outTrimmed := strings.TrimSpace(entry.Output)
	if outTrimmed == "" {
		fmt.Fprintln(stdout, "(no output)")
	} else {
		fmt.Fprintln(stdout, terminaltext.Sanitize(outTrimmed))
	}

	if len(tools) > 1 && len(parts) <= 1 {
		fmt.Fprintf(stdout, "\n(Showing latest tool %d of %d. Use '/detail <index>' to inspect earlier tools.)\n", target+1, len(tools))
	}
}

func handleCoreCLIResume(client cliCore, currentSessionID, line string, stdout, stderr io.Writer) (string, cliRuntime, error) {
	fields := strings.Fields(line)
	if len(fields) == 1 {
		handleCoreCLISessions(client, currentSessionID, "/sessions", stdout, stderr)
		return currentSessionID, nil, nil
	}
	if len(fields) != 2 {
		fmt.Fprintln(stderr, "usage: /resume [session-id|prefix]")
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
	if err := settings.ApplyTerminalDefaults(ctx, client, targetID); err != nil {
		fmt.Fprintf(stderr, "error: set default reasoning effort: %v\n", err)
		return currentSessionID, nil, nil
	}
	rt, err := client.OpenSessionRuntime(ctx, targetID)
	if err != nil {
		fmt.Fprintf(stderr, "error: open session runtime: %v\n", err)
		return currentSessionID, nil, nil
	}
	fmt.Fprintf(stdout, "Resumed session %s.\n", targetID)
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
		fmt.Fprintln(stderr, "Cannot delete currently active session. Switch to another session first with /resume.")
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
	isPreview := false
	for _, arg := range fields[1:] {
		lower := strings.ToLower(arg)
		if lower == "preview" || lower == "-p" || lower == "--preview" {
			isPreview = true
		} else if n, err := strconv.Atoi(arg); err == nil && n > 0 {
			turns = n
		} else {
			fmt.Fprintf(stderr, "usage: /rewind [N] or /rewind preview [N] (N must be a positive integer)\n")
			return
		}
	}
	ctx := context.Background()
	if isPreview {
		saved, err := client.GetSession(ctx, sessionID)
		if err != nil {
			fmt.Fprintf(stderr, "error: get session: %v\n", err)
			return
		}
		var userTurns []string
		if len(saved.History) > 0 && string(saved.History) != "null" {
			var items []json.RawMessage
			if json.Unmarshal(saved.History, &items) == nil {
				for _, item := range items {
					var val struct {
						Type    string          `json:"type"`
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					}
					if json.Unmarshal(item, &val) == nil && val.Type == "message" && val.Role == "user" {
						text := cliExtractMessageContent(val.Content)
						if text != "" {
							userTurns = append(userTurns, text)
						}
					}
				}
			}
		}
		if len(userTurns) == 0 {
			fmt.Fprintln(stdout, "No user turns in session history.")
			return
		}
		if turns > len(userTurns) {
			turns = len(userTurns)
		}
		dropped := userTurns[len(userTurns)-turns:]
		fmt.Fprintf(stdout, "Rewind preview (would remove %d turn(s)):\n", len(dropped))
		for i, text := range dropped {
			summary := text
			if len(summary) > 60 {
				summary = summary[:60] + "..."
			}
			summary = strings.ReplaceAll(summary, "\n", " ")
			fmt.Fprintf(stdout, "  - Turn %d: %q\n", len(userTurns)-len(dropped)+i+1, summary)
		}
		fmt.Fprintf(stdout, "Run '/rewind %d' to execute.\n", turns)
		return
	}
	removed, err := client.RewindSession(ctx, sessionID, turns)
	if err != nil {
		fmt.Fprintf(stderr, "error: rewind session: %v\n", err)
		return
	}
	fmt.Fprintf(stdout, "Rewound %d turn(s).\n", removed)
}
