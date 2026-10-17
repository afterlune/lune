package tui

import (
	"strings"
)

// DiffFile represents the diff output and statistics for a single changed file.
type DiffFile struct {
	Path      string   // Relative file path, e.g. "internal/tui/model.go"
	Additions int      // Count of added lines (+)
	Deletions int      // Count of deleted lines (-)
	IsBinary  bool     // True if file is binary
	RawDiff   string   // Full unified diff text for this file
	Lines     []string // Individual lines for scrolling and syntax styling
}

// parseGitDiff parses a raw unified git diff string into structured DiffFile slices.
func parseGitDiff(raw string) []DiffFile {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	// Split by git diff file header
	const diffHeaderPrefix = "diff --git "
	var chunks []string

	if strings.HasPrefix(trimmed, diffHeaderPrefix) {
		parts := strings.Split(trimmed, "\n"+diffHeaderPrefix)
		for i, p := range parts {
			if i == 0 {
				chunks = append(chunks, p)
			} else {
				chunks = append(chunks, diffHeaderPrefix+p)
			}
		}
	} else {
		chunks = []string{trimmed}
	}

	var files []DiffFile
	for _, chunk := range chunks {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}

		rawLines := strings.Split(chunk, "\n")
		var lines []string
		for _, l := range rawLines {
			lines = append(lines, strings.TrimRight(l, "\r"))
		}

		filePath := ""
		additions := 0
		deletions := 0
		isBinary := false

		// Parse file path and count changes
		for _, line := range lines {
			if strings.HasPrefix(line, diffHeaderPrefix) {
				// e.g. "diff --git a/path/to/file b/path/to/file"
				headerParts := strings.Fields(line)
				if len(headerParts) >= 4 {
					bPart := headerParts[3]
					filePath = strings.TrimPrefix(bPart, "b/")
				}
			} else if strings.HasPrefix(line, "+++ b/") {
				filePath = strings.TrimPrefix(line, "+++ b/")
			} else if strings.HasPrefix(line, "--- a/") && filePath == "" {
				filePath = strings.TrimPrefix(line, "--- a/")
			} else if strings.Contains(line, "Binary files") && strings.Contains(line, "differ") {
				isBinary = true
			} else if strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++") {
				additions++
			} else if strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---") {
				deletions++
			}
		}

		if filePath == "" {
			filePath = "unknown"
		}

		files = append(files, DiffFile{
			Path:      filePath,
			Additions: additions,
			Deletions: deletions,
			IsBinary:  isBinary,
			RawDiff:   chunk,
			Lines:     lines,
		})
	}

	return files
}
