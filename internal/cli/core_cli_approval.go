package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strings"
	"unicode"

	core "github.com/afterlune/luneCore"
	publictool "github.com/afterlune/luneCore/tool"
)

func resolveCLIApproval(runtime cliRuntime, request core.ApprovalRequest, workspace string, keys <-chan cliInputEvent, stderr io.Writer) error {
	if _, err := fmt.Fprintln(stderr, formatCLIToolApproval(request, workspace)); err != nil {
		return err
	}
	return resolveCLIApprovalKeys(runtime, request, workspace, keys, stderr)
}

func resolveCLIApprovalKeys(runtime cliRuntime, request core.ApprovalRequest, workspace string, keys <-chan cliInputEvent, stderr io.Writer) error {
	for key := range keys {
		handled, err := handleCLIApprovalKey(runtime, request, workspace, key, stderr)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
	}
	err := runtime.ResolveApproval(request.ID, false)
	if err == nil {
		fmt.Fprintf(stderr, "Denied %s because interactive keyboard input became unavailable.\n", cliApprovalSummary(request, workspace))
	} else {
		fmt.Fprintf(stderr, "error: resolve approval: %v\n", err)
	}
	return err
}

func handleCLIApprovalKey(runtime cliRuntime, request core.ApprovalRequest, workspace string, key cliInputEvent, stderr io.Writer) (bool, error) {
	if key.err != nil {
		err := runtime.ResolveApproval(request.ID, false)
		if err == nil {
			fmt.Fprintf(stderr, "Denied %s because interactive keyboard input is unavailable.\n", cliApprovalSummary(request, workspace))
		} else {
			fmt.Fprintf(stderr, "error: resolve approval: %v\n", err)
		}
		return true, err
	}
	if key.escape {
		return false, errors.New("approval interrupted")
	}
	var allowed bool
	switch unicode.ToLower(key.rune) {
	case 'y':
		allowed = true
	case 'n':
		allowed = false
	default:
		return false, nil
	}
	if err := runtime.ResolveApproval(request.ID, allowed); err != nil {
		fmt.Fprintf(stderr, "error: resolve approval: %v\n", err)
		return true, fmt.Errorf("resolve approval: %w", err)
	}
	if allowed {
		if request.PathGrant != nil {
			_, _ = fmt.Fprintln(stderr, "Granted directory access for this session.")
		} else {
			_, _ = fmt.Fprintln(stderr, "Approved this tool call once.")
		}
	} else {
		_, _ = fmt.Fprintln(stderr, "Denied this tool call.")
	}
	return true, nil
}

func formatCLIToolApproval(request core.ApprovalRequest, workspace string) string {
	if request.PathGrant != nil {
		access := "read-only"
		if request.PathGrant.Access == publictool.PathReadWrite {
			access = "read and write"
		}
		return fmt.Sprintf("Grant %s access to %s and its subdirectories for this session?\nPress Y to grant access, N to deny, or Esc to interrupt the task.",
			access, cleanCLIInlineText(request.PathGrant.Path))
	}
	details := ""
	warning := ""
	if request.Tool == "command" {
		var args map[string]json.RawMessage
		_ = json.Unmarshal(request.Arguments, &args)
		var command string
		_ = json.Unmarshal(args["command"], &command)
		details = "\nCommand: " + cleanCLIInlineText(command)
		warning = "\nCore runs commands inside the operating system sandbox."
	}
	return fmt.Sprintf("Allow %s once?%s%s\nPress Y to approve once, N to deny, or Esc to interrupt the task.",
		cliApprovalSummary(request, workspace), details, warning)
}

func cliApprovalSummary(request core.ApprovalRequest, workspace string) string {
	var args map[string]json.RawMessage
	_ = json.Unmarshal(request.Arguments, &args)
	var pathValue, rawURL string
	_ = json.Unmarshal(args["path"], &pathValue)
	_ = json.Unmarshal(args["url"], &rawURL)
	path := cliApprovalPath(pathValue, workspace)
	summary := "Working on a tool request"
	switch request.Tool {
	case "read_file", "read_image":
		summary = cliApprovalPathSummary("Reading", path)
	case "write_file":
		summary = cliApprovalPathSummary("Writing", path)
	case "edit_file":
		summary = cliApprovalPathSummary("Editing", path)
	case "list_dir":
		summary = cliApprovalPathSummary("Listing", path)
	case "glob":
		summary = "Finding project files"
	case "grep":
		if path == "" {
			summary = "Searching project files"
		} else {
			summary = "Searching " + path
		}
	case "web_search":
		summary = "Searching the web"
	case "web_fetch":
		if parsed, err := url.Parse(rawURL); err == nil && parsed.Hostname() != "" {
			summary = "Fetching " + parsed.Hostname()
		} else {
			summary = "Fetching a web page"
		}
	case "command":
		summary = "Running a shell command"
	default:
		if request.Tool != "" {
			summary = "Tool " + request.Tool
		}
	}
	// Tool arguments and extension tool names may originate from model output.
	// Keep their display on one terminal line and prevent ANSI control sequences.
	return cleanCLIInlineText(summary)
}

func cliApprovalPath(value, workspace string) string {
	value = strings.TrimSpace(value)
	if value == "" || filepath.IsAbs(value) {
		return ""
	}
	clean := filepath.Clean(filepath.FromSlash(value))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return ""
	}
	if workspace == "" {
		return filepath.ToSlash(clean)
	}
	absolute := filepath.Clean(filepath.Join(workspace, clean))
	relative, err := filepath.Rel(workspace, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(clean)
	}
	return filepath.ToSlash(absolute)
}

func cliApprovalPathSummary(action, path string) string {
	if path == "" {
		return action + " a workspace file"
	}
	return action + " " + path
}

func cleanCLIInlineText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\\r\\n")
	value = strings.ReplaceAll(value, "\n", "\\n")
	value = strings.ReplaceAll(value, "\r", "\\r")
	value = strings.ReplaceAll(value, "\t", "\\t")
	return strings.Map(func(char rune) rune {
		if unicode.IsControl(char) {
			return -1
		}
		return char
	}, value)
}
