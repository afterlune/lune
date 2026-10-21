package filemention

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectAtQuery(t *testing.T) {
	tests := []struct {
		name         string
		val          string
		cursorOffset int
		wantQuery    string
		wantStart    int
		wantEnd      int
		wantOk       bool
	}{
		{
			name:         "at start of empty mention",
			val:          "@",
			cursorOffset: 1,
			wantQuery:    "",
			wantStart:    0,
			wantEnd:      1,
			wantOk:       true,
		},
		{
			name:         "at start with query",
			val:          "@tui",
			cursorOffset: 4,
			wantQuery:    "tui",
			wantStart:    0,
			wantEnd:      4,
			wantOk:       true,
		},
		{
			name:         "mid sentence after space",
			val:          "please check @internal/tui for bugs",
			cursorOffset: 26, // right after @internal/tui
			wantQuery:    "internal/tui",
			wantStart:    13,
			wantEnd:      26,
			wantOk:       true,
		},
		{
			name:         "cursor in middle of mention token",
			val:          "check @tui.go now",
			cursorOffset: 10, // right after "tui"
			wantQuery:    "tui",
			wantStart:    6,
			wantEnd:      13, // ends after ".go"
			wantOk:       true,
		},
		{
			name:         "mention inside parentheses",
			val:          "see (@model.go)",
			cursorOffset: 14,
			wantQuery:    "model.go",
			wantStart:    5,
			wantEnd:      14,
			wantOk:       true,
		},
		{
			name:         "mention on second line",
			val:          "first line\n@app.go",
			cursorOffset: 18,
			wantQuery:    "app.go",
			wantStart:    11,
			wantEnd:      18,
			wantOk:       true,
		},
		{
			name:         "email address should be rejected",
			val:          "send to alice@example.com please",
			cursorOffset: 25,
			wantOk:       false,
		},
		{
			name:         "word containing at should be rejected",
			val:          "username@hostname",
			cursorOffset: 17,
			wantOk:       false,
		},
		{
			name:         "cursor far past mention with trailing text",
			val:          "@tui was reviewed already",
			cursorOffset: 25,
			wantOk:       false, // space between @tui and cursor
		},
		{
			name:         "no at symbol present",
			val:          "just plain text",
			cursorOffset: 15,
			wantOk:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, start, end, ok := DetectAtQuery(tt.val, tt.cursorOffset)
			if ok != tt.wantOk {
				t.Fatalf("DetectAtQuery() ok = %v, want %v", ok, tt.wantOk)
			}
			if !ok {
				return
			}
			if query != tt.wantQuery {
				t.Errorf("DetectAtQuery() query = %q, want %q", query, tt.wantQuery)
			}
			if start != tt.wantStart {
				t.Errorf("DetectAtQuery() start = %d, want %d", start, tt.wantStart)
			}
			if end != tt.wantEnd {
				t.Errorf("DetectAtQuery() end = %d, want %d", end, tt.wantEnd)
			}
		})
	}
}

func TestFuzzyMatchFiles(t *testing.T) {
	files := []File{
		{Path: "internal/tui/tui.go", Name: "tui.go", Size: 1024},
		{Path: "internal/tui/model.go", Name: "model.go", Size: 2048},
		{Path: "internal/tui/update.go", Name: "update.go", Size: 3072},
		{Path: "internal/app/app.go", Name: "app.go", Size: 4096},
		{Path: "cmd/main.go", Name: "main.go", Size: 512},
	}

	// 1. Exact file name match
	matches := FuzzyMatch(files, "model.go", 5)
	if len(matches) == 0 || matches[0].Name != "model.go" {
		t.Fatalf("expected model.go to rank #1, got: %#v", matches)
	}

	// 2. Prefix match
	matches = FuzzyMatch(files, "tu", 5)
	if len(matches) == 0 || matches[0].Name != "tui.go" {
		t.Fatalf("expected tui.go to rank #1 for 'tu', got: %#v", matches)
	}

	// 3. Path substring match
	matches = FuzzyMatch(files, "internal/app", 5)
	if len(matches) == 0 || matches[0].Path != "internal/app/app.go" {
		t.Fatalf("expected app.go to rank #1 for 'internal/app', got: %#v", matches)
	}

	// 4. Subsequence match "itt" -> internal/tui/tui.go
	matches = FuzzyMatch(files, "itt", 5)
	if len(matches) == 0 || matches[0].Path != "internal/tui/tui.go" {
		t.Fatalf("expected internal/tui/tui.go for 'itt', got: %#v", matches)
	}

	// 5. Empty query returns files sorted by path
	matches = FuzzyMatch(files, "", 3)
	if len(matches) != 3 {
		t.Fatalf("expected 3 items for empty query, got %d", len(matches))
	}
	if matches[0].Path != "cmd/main.go" {
		t.Errorf("expected cmd/main.go as first alphabetical item, got %s", matches[0].Path)
	}
}

func TestWorkspaceFileScanner(t *testing.T) {
	tempDir := t.TempDir()

	// Create test files and directories
	_ = os.MkdirAll(filepath.Join(tempDir, "src"), 0755)
	_ = os.MkdirAll(filepath.Join(tempDir, ".git"), 0755)
	_ = os.MkdirAll(filepath.Join(tempDir, "node_modules"), 0755)
	_ = os.MkdirAll(filepath.Join(tempDir, "bin"), 0755)

	_ = os.WriteFile(filepath.Join(tempDir, "src", "index.ts"), []byte("console.log()"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "src", "app.tsx"), []byte("<App />"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, ".git", "HEAD"), []byte("ref: refs/heads/main"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "node_modules", "pkg.json"), []byte("{}"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "bin", "tool.exe"), []byte("binary"), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "image.png"), []byte("png"), 0644)

	scanner := NewScanner(tempDir)
	files := scanner.Files()

	if len(files) != 2 {
		t.Fatalf("expected 2 files (src/index.ts, src/app.tsx), got %d: %#v", len(files), files)
	}

	paths := map[string]bool{}
	for _, f := range files {
		paths[f.Path] = true
	}
	if !paths["src/index.ts"] || !paths["src/app.tsx"] {
		t.Errorf("missing expected files in scan: %#v", paths)
	}

	// Test Search
	results := scanner.Search("index", 5)
	if len(results) != 1 || results[0].Name != "index.ts" {
		t.Errorf("expected index.ts in search results, got: %#v", results)
	}

	// Test Search with path slash
	slashResults := scanner.Search("src/", 5)
	if len(slashResults) != 2 {
		t.Errorf("expected 2 files when searching 'src/', got: %d", len(slashResults))
	}

	// Test subsequence search
	subseqResults := scanner.Search("atx", 5) // matches src/app.tsx
	if len(subseqResults) != 1 || subseqResults[0].Name != "app.tsx" {
		t.Errorf("expected app.tsx when searching 'atx', got: %#v", subseqResults)
	}

	// Test caching within TTL
	cached := scanner.Files()
	if len(cached) != 2 {
		t.Errorf("expected cached files to match")
	}
}

func TestFormatFileSize(t *testing.T) {
	if FormatFileSize(500) != "500 B" {
		t.Errorf("FormatFileSize(500) = %q, want '500 B'", FormatFileSize(500))
	}
	if FormatFileSize(1536) != "1.5 KB" {
		t.Errorf("FormatFileSize(1536) = %q, want '1.5 KB'", FormatFileSize(1536))
	}
	if FormatFileSize(2*1024*1024) != "2.0 MB" {
		t.Errorf("FormatFileSize(2MB) = %q, want '2.0 MB'", FormatFileSize(2*1024*1024))
	}
}

func TestParseLineRangeSuffix(t *testing.T) {
	tests := []struct {
		token     string
		wantPath  string
		wantStart int
		wantEnd   int
	}{
		{"internal/tui/view.go#L10-L25", "internal/tui/view.go", 10, 25},
		{"internal/tui/view.go#L10-25", "internal/tui/view.go", 10, 25},
		{"internal/tui/view.go#10-25", "internal/tui/view.go", 10, 25},
		{"internal/tui/view.go#L42", "internal/tui/view.go", 42, 42},
		{"internal/tui/view.go#42", "internal/tui/view.go", 42, 42},
		{"internal/tui/view.go:15-30", "internal/tui/view.go", 15, 30},
		{"internal/tui/view.go:50", "internal/tui/view.go", 50, 50},
		{"internal/tui/view.go", "internal/tui/view.go", 0, 0},
	}

	for _, tt := range tests {
		cleanPath, start, end := parseLineRangeSuffix(tt.token)
		if cleanPath != tt.wantPath || start != tt.wantStart || end != tt.wantEnd {
			t.Errorf("parseLineRangeSuffix(%q) = (%q, %d, %d), want (%q, %d, %d)",
				tt.token, cleanPath, start, end, tt.wantPath, tt.wantStart, tt.wantEnd)
		}
	}
}

func TestExpandFileMentions(t *testing.T) {
	tempDir := t.TempDir()

	file1 := filepath.Join(tempDir, "main.go")
	content1 := "package main\n\nfunc main() {\n\tprintln(\"hello world\")\n}\n"
	_ = os.WriteFile(file1, []byte(content1), 0644)

	file2 := filepath.Join(tempDir, "config.json")
	content2 := `{"port": 8080, "debug": true}`
	_ = os.WriteFile(file2, []byte(content2), 0644)

	// 1. Single file mention
	p1 := "Please inspect @main.go and explain."
	atts, aug := Expand(p1, tempDir)
	if len(atts) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(atts))
	}
	if atts[0].Path != "main.go" || atts[0].Content != content1 {
		t.Errorf("attachment mismatch: %+v", atts[0])
	}
	if !strings.Contains(aug, "[Attached File Context: main.go") || !strings.Contains(aug, "package main") {
		t.Errorf("expected augmented prompt to contain attached code fence: %s", aug)
	}

	// 2. Line range mention
	p2 := "Check @main.go#L3-L4 for syntax"
	atts2, aug2 := Expand(p2, tempDir)
	if len(atts2) != 1 {
		t.Fatalf("expected 1 attachment for line range, got %d", len(atts2))
	}
	if atts2[0].StartLine != 3 || atts2[0].EndLine != 4 {
		t.Errorf("expected lines 3-4, got %d-%d", atts2[0].StartLine, atts2[0].EndLine)
	}
	if atts2[0].Content != "func main() {\n\tprintln(\"hello world\")" {
		t.Errorf("unexpected sliced content: %q", atts2[0].Content)
	}
	if !strings.Contains(aug2, "lines 3-4") {
		t.Errorf("expected header with lines 3-4 in augmented prompt: %s", aug2)
	}

	// 3. Multiple file mentions
	p3 := "Compare @main.go with @config.json please"
	atts3, _ := Expand(p3, tempDir)
	if len(atts3) != 2 {
		t.Fatalf("expected 2 attachments, got %d", len(atts3))
	}

	// 4. Email address should NOT attach
	p4 := "Contact support@example.com for issues"
	atts4, aug4 := Expand(p4, tempDir)
	if len(atts4) != 0 || aug4 != p4 {
		t.Errorf("expected email to not trigger attachment, got %d attachments", len(atts4))
	}

	// 5. Directory traversal should be rejected
	p5 := "Inspect @../secret.txt"
	atts5, _ := Expand(p5, tempDir)
	if len(atts5) != 0 {
		t.Errorf("expected path traversal to be rejected, got %d attachments", len(atts5))
	}

	// 6. Non-existent file should be skipped
	p6 := "Look at @nonexistent.go please"
	atts6, aug6 := Expand(p6, tempDir)
	if len(atts6) != 0 || aug6 != p6 {
		t.Errorf("expected non-existent file to be skipped")
	}
}

func TestGitModifiedFilesBoost(t *testing.T) {
	files := []File{
		{Path: "cmd/main.go", Name: "main.go", Size: 500, IsModified: false},
		{Path: "internal/tui/view.go", Name: "view.go", Size: 2000, IsModified: true},
		{Path: "internal/tui/model.go", Name: "model.go", Size: 1500, IsModified: false},
		{Path: "pkg/util.go", Name: "util.go", Size: 800, IsModified: true},
	}

	// Empty query: modified files must rank first
	rankedEmpty := FuzzyMatch(files, "", 4)
	if len(rankedEmpty) != 4 {
		t.Fatalf("expected 4 files, got %d", len(rankedEmpty))
	}
	if !rankedEmpty[0].IsModified || !rankedEmpty[1].IsModified {
		t.Errorf("expected first 2 files to be modified files, got: %+v, %+v", rankedEmpty[0], rankedEmpty[1])
	}

	// Query matching both: modified file gets score boost
	rankedSearch := FuzzyMatch(files, "go", 4)
	if len(rankedSearch) == 0 || !rankedSearch[0].IsModified {
		t.Errorf("expected top search result to be modified file, got: %+v", rankedSearch[0])
	}
}
