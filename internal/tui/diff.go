package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
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

// computeDiffStats calculates total additions and deletions across all files.
func computeDiffStats(files []DiffFile) (totalAdditions, totalDeletions int) {
	for _, f := range files {
		totalAdditions += f.Additions
		totalDeletions += f.Deletions
	}
	return
}

// renderDiffTabBar constructs a horizontally scrollable tab row for changed files.
func renderDiffTabBar(files []DiffFile, activeIdx int, width int) string {
	if len(files) <= 1 {
		return ""
	}

	th := activeTheme()
	type tabItem struct {
		text     string
		width    int
		isActive bool
	}

	var items []tabItem
	for i, f := range files {
		base := filepath.Base(f.Path)
		isActive := (i == activeIdx)
		stat := fmt.Sprintf("+%d/-%d", f.Additions, f.Deletions)
		label := fmt.Sprintf("%d.%s %s", i+1, base, stat)

		var rendered string
		if isActive {
			pillStyle := lipgloss.NewStyle().
				Background(lipgloss.Color(th.SelectBg)).
				Foreground(lipgloss.Color(th.SelectFg)).
				Bold(true).
				Padding(0, 1)
			rendered = pillStyle.Render(label)
		} else {
			pillStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color(th.TextMuted)).
				Padding(0, 1)
			rendered = pillStyle.Render(label)
		}

		items = append(items, tabItem{
			text:     rendered,
			width:    lipgloss.Width(rendered),
			isActive: isActive,
		})
	}

	// Calculate visible window around activeIdx that fits within width
	start := 0
	end := len(items)

	totalW := 0
	for _, it := range items {
		totalW += it.width + 1
	}

	if totalW > width {
		currentW := items[activeIdx].width
		start = activeIdx
		end = activeIdx + 1

		for {
			expanded := false
			if start > 0 {
				needed := items[start-1].width + 1
				if currentW+needed <= width-4 {
					start--
					currentW += needed
					expanded = true
				}
			}
			if end < len(items) {
				needed := items[end].width + 1
				if currentW+needed <= width-4 {
					currentW += needed
					end++
					expanded = true
				}
			}
			if !expanded {
				break
			}
		}
	}

	var parts []string
	if start > 0 {
		parts = append(parts, styleTextMuted().Render("‹"))
	}
	for i := start; i < end; i++ {
		parts = append(parts, items[i].text)
	}
	if end < len(items) {
		parts = append(parts, styleTextMuted().Render("›"))
	}

	return strings.Join(parts, " ")
}

// draftCommitMessage synthesizes a conventional commit message from parsed diff files.
func draftCommitMessage(files []DiffFile) string {
	if len(files) == 0 {
		return "chore: update workspace files"
	}

	scopeCounts := make(map[string]int)
	totalAdditions := 0
	totalDeletions := 0
	isAllTests := true

	for _, f := range files {
		totalAdditions += f.Additions
		totalDeletions += f.Deletions
		if !strings.HasSuffix(f.Path, "_test.go") && !strings.Contains(f.Path, "test") {
			isAllTests = false
		}

		parts := strings.Split(f.Path, "/")
		if len(parts) >= 2 && parts[0] == "internal" {
			scopeCounts[parts[1]]++
		} else if len(parts) >= 2 {
			scopeCounts[parts[0]]++
		} else {
			scopeCounts["root"]++
		}
	}

	// Find most prominent scope
	primaryScope := "app"
	maxCount := 0
	for scope, count := range scopeCounts {
		if count > maxCount {
			maxCount = count
			primaryScope = scope
		}
	}
	if primaryScope == "root" {
		primaryScope = "core"
	}

	commitType := "feat"
	if isAllTests {
		commitType = "test"
	} else if totalAdditions > 0 && totalDeletions == 0 {
		commitType = "feat"
	} else if totalDeletions > totalAdditions*2 {
		commitType = "refactor"
	}

	// Synthesize a brief description based on files
	var fileBaseNames []string
	for _, f := range files {
		base := filepath.Base(f.Path)
		base = strings.TrimSuffix(base, filepath.Ext(base))
		base = strings.TrimSuffix(base, "_test")
		found := false
		for _, existing := range fileBaseNames {
			if existing == base {
				found = true
				break
			}
		}
		if !found && len(fileBaseNames) < 3 {
			fileBaseNames = append(fileBaseNames, base)
		}
	}

	desc := "update workspace components"
	if len(fileBaseNames) > 0 {
		desc = fmt.Sprintf("update %s", strings.Join(fileBaseNames, ", "))
	}

	return fmt.Sprintf("%s(%s): %s", commitType, primaryScope, desc)
}
