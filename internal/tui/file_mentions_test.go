package tui

import (
	"os"
	"path/filepath"
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
	files := []WorkspaceFile{
		{Path: "internal/tui/tui.go", Name: "tui.go", Size: 1024},
		{Path: "internal/tui/model.go", Name: "model.go", Size: 2048},
		{Path: "internal/tui/update.go", Name: "update.go", Size: 3072},
		{Path: "internal/app/app.go", Name: "app.go", Size: 4096},
		{Path: "cmd/main.go", Name: "main.go", Size: 512},
	}

	// 1. Exact file name match
	matches := FuzzyMatchFiles(files, "model.go", 5)
	if len(matches) == 0 || matches[0].Name != "model.go" {
		t.Fatalf("expected model.go to rank #1, got: %#v", matches)
	}

	// 2. Prefix match
	matches = FuzzyMatchFiles(files, "tu", 5)
	if len(matches) == 0 || matches[0].Name != "tui.go" {
		t.Fatalf("expected tui.go to rank #1 for 'tu', got: %#v", matches)
	}

	// 3. Path substring match
	matches = FuzzyMatchFiles(files, "internal/app", 5)
	if len(matches) == 0 || matches[0].Path != "internal/app/app.go" {
		t.Fatalf("expected app.go to rank #1 for 'internal/app', got: %#v", matches)
	}

	// 4. Subsequence match "itt" -> internal/tui/tui.go
	matches = FuzzyMatchFiles(files, "itt", 5)
	if len(matches) == 0 || matches[0].Path != "internal/tui/tui.go" {
		t.Fatalf("expected internal/tui/tui.go for 'itt', got: %#v", matches)
	}

	// 5. Empty query returns files sorted by path
	matches = FuzzyMatchFiles(files, "", 3)
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

	scanner := NewWorkspaceFileScanner(tempDir)
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
