package tui

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"strings"

	core "github.com/afterlune/luneCore"
)

func isLeadMainUsage(agentID string, purpose core.UsagePurpose) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID != "" && agentID != "lead" {
		return false
	}
	return purpose == "" || purpose == core.UsagePurposeMain
}

func latestLeadUsageRecord(records []core.UsageRecord) (core.UsageRecord, bool) {
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if isLeadMainUsage(record.AgentID, record.Purpose) {
			return record, true
		}
	}
	return core.UsageRecord{}, false
}

// formatToolSummary parses raw JSON tool arguments into a concise human-readable command summary.
func formatToolSummary(toolName, rawArgs, workspace string) string {
	name := strings.TrimSpace(toolName)
	argsText := strings.TrimSpace(rawArgs)
	if argsText == "" || argsText == "{}" || argsText == "null" {
		return name
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(argsText), &args); err != nil {
		return cleanToolInline(name + " " + argsText)
	}

	strField := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := args[k]; ok {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
		return ""
	}
	intField := func(keys ...string) (int, bool) {
		for _, k := range keys {
			if v, ok := args[k]; ok {
				switch n := v.(type) {
				case float64:
					return int(n), true
				case int:
					return n, true
				}
			}
		}
		return 0, false
	}

	relPath := func(p string) string {
		if p == "" {
			return ""
		}
		if workspace != "" && filepath.IsAbs(p) {
			if rel, err := filepath.Rel(workspace, p); err == nil && !strings.HasPrefix(rel, "..") {
				return filepath.ToSlash(rel)
			}
		}
		return filepath.ToSlash(p)
	}

	switch strings.ToLower(name) {
	case "command", "run_command":
		if cmd := strField("command", "cmd"); cmd != "" {
			return cleanToolInline(cmd)
		}
	case "read_file":
		if p := relPath(strField("path", "file_path", "file")); p != "" {
			start, okStart := intField("start_line", "offset", "startLine")
			end, okEnd := intField("end_line", "endLine")
			limit, okLimit := intField("limit")
			if okStart && okEnd && end >= start && start > 0 {
				return fmt.Sprintf("Read %s (L%d-%d)", p, start, end)
			}
			if okStart && okLimit && limit > 0 && start > 0 {
				return fmt.Sprintf("Read %s (L%d-%d)", p, start, start+limit-1)
			}
			return "Read " + p
		}
	case "read_image":
		if p := relPath(strField("path", "file_path", "file")); p != "" {
			return "Viewed image " + p
		}
	case "write_file":
		if p := relPath(strField("path", "file_path", "file")); p != "" {
			return "Write " + p
		}
	case "edit_file", "replace_file_content":
		if p := relPath(strField("path", "file_path", "file")); p != "" {
			return "Edit " + p
		}
	case "list_dir":
		p := relPath(strField("path", "dir", "directory"))
		if p == "" {
			p = "."
		}
		return "List " + p
	case "glob", "find_files":
		pat := strField("pattern", "glob", "query")
		p := relPath(strField("path", "dir"))
		if pat != "" && p != "" && p != "." {
			return fmt.Sprintf("Find %s in %s", pat, p)
		}
		if pat != "" {
			return "Find " + pat
		}
	case "grep", "search_files":
		pat := strField("pattern", "query", "regex")
		p := relPath(strField("path", "dir", "include"))
		if pat != "" && p != "" && p != "." {
			return fmt.Sprintf("Search %q in %s", pat, p)
		}
		if pat != "" {
			return fmt.Sprintf("Search %q", pat)
		}
	case "web_search":
		if q := strField("query", "q"); q != "" {
			return fmt.Sprintf("Web search %q", q)
		}
	case "web_fetch":
		if rawURL := strField("url"); rawURL != "" {
			if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
				return "Fetch " + u.Host + u.Path
			}
			return "Fetch " + rawURL
		}
	case "delegate_team":
		if tasks, ok := args["tasks"].([]any); ok && len(tasks) > 0 {
			return fmt.Sprintf("Delegate %d subagent(s)", len(tasks))
		}
		return "Delegate subagent team"
	}

	return cleanToolInline(name + " " + argsText)
}

func hasToolSummaryArguments(toolName, rawArgs, summary string) bool {
	args := strings.TrimSpace(rawArgs)
	if args == "" || args == "null" || args == "{}" {
		return false
	}
	summary = strings.TrimSpace(summary)
	return summary != "" && summary != strings.TrimSpace(toolName)
}

func cleanToolInline(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	return strings.TrimSpace(s)
}

// estimateDeltaTokens estimates token count from an incremental stream delta.
func estimateDeltaTokens(delta string) int64 {
	if delta == "" {
		return 0
	}
	var count float64
	for _, r := range delta {
		if r >= 0x4e00 && r <= 0x9fff || r >= 0x3400 && r <= 0x4dbf || r >= 0x20000 && r <= 0x2a6df {
			// CJK characters in modern LLMs average 1.3 to 1.5 tokens
			count += 1.4
		} else if r > 127 {
			count += 1.0
		} else {
			count += 0.25
		}
	}
	toks := int64(math.Ceil(count))
	if toks < 1 {
		toks = 1
	}
	return toks
}
