package tool

import (
	"encoding/json"
	"net/url"
	"path/filepath"
	"strings"
)

// DisplaySummary returns a short, safe description for UI activity. It avoids
// exposing tool arguments, command text, search queries, or tool output.
func DisplaySummary(name string, raw json.RawMessage) string {
	var args struct {
		Path    string `json:"path"`
		URL     string `json:"url"`
	}
	_ = json.Unmarshal(raw, &args)
	path := safeDisplayPath(args.Path)
	switch name {
	case "read_file":
		return summaryWithPath("Reading", path)
	case "write_file":
		return summaryWithPath("Writing", path)
	case "edit_file":
		return summaryWithPath("Editing", path)
	case "list_dir":
		return summaryWithPath("Listing", path)
	case "glob":
		return "Finding project files"
	case "grep":
		if path != "" {
			return "Searching " + path
		}
		return "Searching project files"
	case "web_search":
		return "Searching the web"
	case "web_fetch":
		if parsed, err := url.Parse(args.URL); err == nil && parsed.Hostname() != "" {
			return "Fetching " + parsed.Hostname()
		}
		return "Fetching a web page"
	case "command":
		return "Running a shell command"
	case "html":
		return "Processing web content"
	default:
		return "Working on a tool request"
	}
}

func safeDisplayPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return ""
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(clean)
}

func summaryWithPath(action, path string) string {
	if path == "" {
		return action + " a workspace file"
	}
	return action + " " + path
}
