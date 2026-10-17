package tui

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// WorkspaceFile represents a project file available for @ mentions.
type WorkspaceFile struct {
	Path      string // Relative slash-separated path, e.g. "internal/tui/tui.go"
	PathLower string // Pre-lowercased path for zero-allocation fuzzy matching
	Name      string // Base file name, e.g. "tui.go"
	NameLower string // Pre-lowercased name for zero-allocation fuzzy matching
	Size      int64  // File size in bytes
	ModTime   time.Time
}

// WorkspaceFileScanner scans and caches workspace files for fuzzy matching.
type WorkspaceFileScanner struct {
	workspace string
	ttl       time.Duration
	maxFiles  int

	mu       sync.RWMutex
	files    []WorkspaceFile
	cachedAt time.Time
}

// NewWorkspaceFileScanner creates a scanner for the given workspace root.
func NewWorkspaceFileScanner(workspace string) *WorkspaceFileScanner {
	return &WorkspaceFileScanner{
		workspace: workspace,
		ttl:       8 * time.Second,
		maxFiles:  5000,
	}
}

// Files returns the cached list of workspace files, refreshing if expired.
func (s *WorkspaceFileScanner) Files() []WorkspaceFile {
	if s == nil || s.workspace == "" {
		return nil
	}

	s.mu.RLock()
	if time.Since(s.cachedAt) < s.ttl && s.files != nil {
		cached := s.files
		s.mu.RUnlock()
		return cached
	}
	s.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	// Double check after acquiring write lock
	if time.Since(s.cachedAt) < s.ttl && s.files != nil {
		return s.files
	}

	s.files = s.scanWorkspace()
	s.cachedAt = time.Now()
	return s.files
}

// Search returns up to limit files matching the query, ranked by relevance.
func (s *WorkspaceFileScanner) Search(query string, limit int) []WorkspaceFile {
	files := s.Files()
	return FuzzyMatchFiles(files, query, limit)
}

// isIgnoredDirectory checks if a directory should be skipped during scanning.
func isIgnoredDirectory(name string) bool {
	switch strings.ToLower(name) {
	case ".git", "node_modules", "bin", "dist", "build", ".luneagent",
		".cache", ".vite", ".next", ".turbo", ".nuxt", ".output",
		"coverage", "tmp", ".idea", ".vscode", "target", "vendor", "__pycache__":
		return true
	default:
		return false
	}
}

// isIgnoredExtension checks if a file extension represents a binary or non-text asset.
func isIgnoredExtension(ext string) bool {
	switch strings.ToLower(ext) {
	case ".exe", ".dll", ".so", ".dylib", ".bin", ".zip", ".tar", ".gz", ".7z",
		".png", ".jpg", ".jpeg", ".gif", ".ico", ".webp", ".pdf", ".mp4", ".mp3",
		".wasm", ".db", ".sqlite", ".lock":
		return true
	default:
		return false
	}
}

// scanWorkspace walks the workspace directory and collects matching files.
func (s *WorkspaceFileScanner) scanWorkspace() []WorkspaceFile {
	if s.workspace == "" {
		return nil
	}

	var results []WorkspaceFile
	root := s.workspace

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		if d.IsDir() {
			if path != root && isIgnoredDirectory(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip binary files and oversized files (> 20MB)
		ext := filepath.Ext(d.Name())
		if isIgnoredExtension(ext) {
			return nil
		}

		info, err := d.Info()
		if err != nil || info.Size() > 20*1024*1024 {
			return nil
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		slashPath := filepath.ToSlash(relPath)
		results = append(results, WorkspaceFile{
			Path:      slashPath,
			PathLower: strings.ToLower(slashPath),
			Name:      d.Name(),
			NameLower: strings.ToLower(d.Name()),
			Size:      info.Size(),
			ModTime:   info.ModTime(),
		})

		if s.maxFiles > 0 && len(results) >= s.maxFiles {
			return fs.SkipAll
		}
		return nil
	})

	return results
}

type scoredFile struct {
	file  WorkspaceFile
	score int
}

// FuzzyMatchFiles filters and ranks workspace files against a search query.
func FuzzyMatchFiles(files []WorkspaceFile, query string, limit int) []WorkspaceFile {
	if limit <= 0 {
		limit = 8
	}

	trimmed := strings.TrimSpace(query)
	q := strings.ToLower(trimmed)

	if q == "" {
		// No query: return the first `limit` files sorted by path
		result := make([]WorkspaceFile, len(files))
		copy(result, files)
		sort.Slice(result, func(i, j int) bool {
			return result[i].Path < result[j].Path
		})
		if len(result) > limit {
			result = result[:limit]
		}
		return result
	}

	var scored []scoredFile
	for _, f := range files {
		score := calculateMatchScore(f, q)
		if score > 0 {
			scored = append(scored, scoredFile{file: f, score: score})
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score // Higher score first
		}
		if len(scored[i].file.Path) != len(scored[j].file.Path) {
			return len(scored[i].file.Path) < len(scored[j].file.Path) // Shorter path first
		}
		return scored[i].file.Path < scored[j].file.Path
	})

	count := min(limit, len(scored))
	out := make([]WorkspaceFile, count)
	for i := 0; i < count; i++ {
		out[i] = scored[i].file
	}
	return out
}

func calculateMatchScore(f WorkspaceFile, q string) int {
	nameLower := f.NameLower
	if nameLower == "" {
		nameLower = strings.ToLower(f.Name)
	}
	pathLower := f.PathLower
	if pathLower == "" {
		pathLower = strings.ToLower(f.Path)
	}

	// 1. Exact file name match
	if nameLower == q {
		return 1000
	}

	// 2. File name prefix match
	if strings.HasPrefix(nameLower, q) {
		return 800 - len(nameLower)
	}

	// 3. File name contains query
	if strings.Contains(nameLower, q) {
		return 600 - len(nameLower)
	}

	// 4. Path prefix match
	if strings.HasPrefix(pathLower, q) {
		return 400 - len(pathLower)
	}

	// 5. Path contains query
	if strings.Contains(pathLower, q) {
		return 300 - len(pathLower)
	}

	// 6. Subsequence matching (e.g. "itt" in "internal/tui/tui.go")
	if isSubsequence(q, pathLower) {
		return 100 - len(pathLower)
	}

	return 0
}

func isSubsequence(sub, str string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(sub) > len(str) {
		return false
	}
	subIdx := 0
	subRunes := []rune(sub)
	for _, r := range str {
		if r == subRunes[subIdx] {
			subIdx++
			if subIdx == len(subRunes) {
				return true
			}
		}
	}
	return false
}

// DetectAtQuery inspects the input text and cursor position to detect if the user
// is actively typing an @file mention. It returns the query, the replacement range
// [startIdx, endIdx], and whether a valid mention was detected.
func DetectAtQuery(val string, cursorOffset int) (query string, startIdx int, endIdx int, ok bool) {
	if cursorOffset < 0 {
		cursorOffset = 0
	}
	if cursorOffset > len(val) {
		cursorOffset = len(val)
	}

	prefix := val[:cursorOffset]
	lastAt := strings.LastIndex(prefix, "@")
	if lastAt == -1 {
		return "", 0, 0, false
	}

	// Check character preceding '@' (must be beginning of string, space, or opening delimiter)
	if lastAt > 0 {
		prevRune, _ := utf8.DecodeLastRuneInString(val[:lastAt])
		if !unicode.IsSpace(prevRune) && prevRune != '(' && prevRune != '[' && prevRune != '{' && prevRune != '"' && prevRune != '\'' && prevRune != '`' {
			return "", 0, 0, false // e.g. user@domain.com or foo@bar
		}
	}

	// Check if there is whitespace between '@' and cursor
	tokenToCursor := val[lastAt+1 : cursorOffset]
	if strings.ContainsAny(tokenToCursor, " \t\r\n") {
		return "", 0, 0, false
	}

	// Determine end of mention token (up to next whitespace or closing delimiter)
	tokenEnd := cursorOffset
	for tokenEnd < len(val) {
		r, size := utf8.DecodeRuneInString(val[tokenEnd:])
		if unicode.IsSpace(r) || r == ')' || r == ']' || r == '}' || r == '"' || r == '\'' || r == '`' || r == ',' || r == ';' {
			break
		}
		tokenEnd += size
	}

	return tokenToCursor, lastAt, tokenEnd, true
}

// FormatFileSize returns a compact, human-readable file size string (e.g. "12 KB").
func FormatFileSize(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}
