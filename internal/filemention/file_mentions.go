package filemention

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// File represents a project file available for @ mentions.
type File struct {
	Path       string // Relative slash-separated path, e.g. "internal/tui/tui.go"
	pathLower  string // Pre-lowercased path for zero-allocation fuzzy matching
	Name       string // Base file name, e.g. "tui.go"
	nameLower  string // Pre-lowercased name for zero-allocation fuzzy matching
	Size       int64  // File size in bytes
	ModTime    time.Time
	IsModified bool // True if modified/untracked in git status
}

// Scanner scans and caches workspace files for fuzzy matching.
type Scanner struct {
	workspace string
	ttl       time.Duration
	maxFiles  int

	mu       sync.RWMutex
	files    []File
	cachedAt time.Time
}

// NewScanner creates a scanner for the given workspace root.
func NewScanner(workspace string) *Scanner {
	return &Scanner{
		workspace: workspace,
		ttl:       8 * time.Second,
		maxFiles:  5000,
	}
}

// Files returns the cached list of workspace files, refreshing if expired.
func (s *Scanner) Files() []File {
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
func (s *Scanner) Search(query string, limit int) []File {
	files := s.Files()
	return FuzzyMatch(files, query, limit)
}

// isIgnoredDirectory checks if a directory should be skipped during scanning.
func isIgnoredDirectory(name string) bool {
	switch strings.ToLower(name) {
	case ".git", ".hg", ".svn", "node_modules", "bin", "dist", "build", ".luneagent",
		".cache", ".vite", ".next", ".turbo", ".nuxt", ".output", "out",
		"coverage", "tmp", ".idea", ".vscode", "target", "vendor", "__pycache__", ".gradle", ".terraform":
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

// gitModifiedFiles returns a set of relative file paths currently modified according to git status.
func (s *Scanner) gitModifiedFiles() map[string]bool {
	if s.workspace == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "status", "--porcelain")
	cmd.Dir = s.workspace
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	modified := make(map[string]bool)
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if len(line) < 4 {
			continue
		}
		rawPath := strings.TrimSpace(line[3:])
		rawPath = strings.Trim(rawPath, "\"")
		if parts := strings.Split(rawPath, " -> "); len(parts) == 2 {
			rawPath = parts[1]
		}
		slashPath := filepath.ToSlash(rawPath)
		modified[slashPath] = true
	}
	return modified
}

// scanWorkspace walks the workspace directory and collects matching files.
func (s *Scanner) scanWorkspace() []File {
	if s.workspace == "" {
		return nil
	}

	modifiedMap := s.gitModifiedFiles()
	var results []File
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
		results = append(results, File{
			Path:       slashPath,
			pathLower:  strings.ToLower(slashPath),
			Name:       d.Name(),
			nameLower:  strings.ToLower(d.Name()),
			Size:       info.Size(),
			ModTime:    info.ModTime(),
			IsModified: modifiedMap[slashPath],
		})

		if s.maxFiles > 0 && len(results) >= s.maxFiles {
			return fs.SkipAll
		}
		return nil
	})

	return results
}

type scoredFile struct {
	file  File
	score int
}

// FuzzyMatch filters and ranks workspace files against a search query.
func FuzzyMatch(files []File, query string, limit int) []File {
	if limit <= 0 {
		limit = 8
	}

	trimmed := strings.TrimSpace(query)
	q := strings.ToLower(trimmed)

	if q == "" {
		// No query: return modified files first, then sorted alphabetically by path
		result := make([]File, len(files))
		copy(result, files)
		sort.Slice(result, func(i, j int) bool {
			if result[i].IsModified != result[j].IsModified {
				return result[i].IsModified // Modified files rank first!
			}
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
			if f.IsModified {
				score += 250 // Relevance boost for modified files
			}
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
	out := make([]File, count)
	for i := 0; i < count; i++ {
		out[i] = scored[i].file
	}
	return out
}

func calculateMatchScore(f File, q string) int {
	nameLower := f.nameLower
	if nameLower == "" {
		nameLower = strings.ToLower(f.Name)
	}
	pathLower := f.pathLower
	if pathLower == "" {
		pathLower = strings.ToLower(f.Path)
	}

	// If query contains a slash, prioritize matching against full path
	if strings.Contains(q, "/") {
		if pathLower == q {
			return 1000
		}
		if strings.HasPrefix(pathLower, q) {
			return 800 - len(pathLower)
		}
		if strings.Contains(pathLower, q) {
			return 600 - len(pathLower)
		}
		if isSubsequence(q, pathLower) {
			return 200 - len(pathLower)
		}
		return 0
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

var (
	// Matches #L10-L20, #L10-20, #10-20, #L10, #10
	hashLineRegex = regexp.MustCompile(`(?i)#(?:L)?(\d+)(?:-(?:L)?(\d+))?$`)
	// Matches :10-20, :10
	colonLineRegex = regexp.MustCompile(`:(\d+)(?:-(\d+))?$`)
)

func parseLineRangeSuffix(token string) (cleanPath string, startLine, endLine int) {
	if m := hashLineRegex.FindStringSubmatchIndex(token); len(m) >= 4 {
		cleanPath = token[:m[0]]
		startStr := token[m[2]:m[3]]
		s, _ := strconv.Atoi(startStr)
		startLine = s
		endLine = s
		if m[4] != -1 && m[5] != -1 {
			endStr := token[m[4]:m[5]]
			if e, err := strconv.Atoi(endStr); err == nil && e >= startLine {
				endLine = e
			}
		}
		return cleanPath, startLine, endLine
	}

	if m := colonLineRegex.FindStringSubmatchIndex(token); len(m) >= 4 {
		cleanPath = token[:m[0]]
		startStr := token[m[2]:m[3]]
		s, _ := strconv.Atoi(startStr)
		startLine = s
		endLine = s
		if m[4] != -1 && m[5] != -1 {
			endStr := token[m[4]:m[5]]
			if e, err := strconv.Atoi(endStr); err == nil && e >= startLine {
				endLine = e
			}
		}
		return cleanPath, startLine, endLine
	}

	return token, 0, 0
}

// Attachment represents a workspace file referenced via @mention with its loaded content.
type Attachment struct {
	Path      string // Relative slash path, e.g. "internal/tui/view.go"
	StartLine int    // 1-indexed start line, 0 if full file
	EndLine   int    // 1-indexed end line, 0 if full file
	Size      int64  // File size in bytes
	Content   string // File content or line slice
	Label     string // Short human-readable badge text, e.g. "internal/tui/view.go (lines 20-50)"
}

// maxAttachmentSizeBytes is the limit per file to prevent blowing up the context window.
const maxAttachmentSizeBytes = 120 * 1024 // 120 KB

// Expand parses @workspace/path mentions from the input prompt, verifies their
// existence within workspace, extracts content (and line ranges if specified), and returns
// both the attachments and the augmented prompt text with structured context blocks.
func Expand(text string, workspace string) ([]Attachment, string) {
	if workspace == "" || !strings.Contains(text, "@") {
		return nil, text
	}

	var attachments []Attachment
	seen := make(map[string]bool)

	// Scan through text for @ tokens
	for i := 0; i < len(text); i++ {
		if text[i] != '@' {
			continue
		}

		// Ensure '@' is at start or preceded by whitespace or common punctuation
		if i > 0 {
			prevRune, _ := utf8.DecodeLastRuneInString(text[:i])
			if !unicode.IsSpace(prevRune) && prevRune != '(' && prevRune != '[' && prevRune != '{' && prevRune != '"' && prevRune != '\'' && prevRune != '`' {
				continue // e.g. user@example.com
			}
		}

		// Read token after '@' up to next whitespace or delimiter
		tokenEnd := i + 1
		for tokenEnd < len(text) {
			r, size := utf8.DecodeRuneInString(text[tokenEnd:])
			if unicode.IsSpace(r) || r == ')' || r == ']' || r == '}' || r == '"' || r == '\'' || r == '`' || r == ',' || r == ';' {
				break
			}
			tokenEnd += size
		}

		rawToken := text[i+1 : tokenEnd]
		i = tokenEnd - 1 // Move cursor forward
		if rawToken == "" {
			continue
		}

		cleanPath, startLine, endLine := parseLineRangeSuffix(rawToken)
		if cleanPath == "" || strings.Contains(cleanPath, "..") {
			continue
		}

		cleanPath = filepath.ToSlash(filepath.Clean(cleanPath))
		if strings.HasPrefix(cleanPath, "/") || strings.HasPrefix(cleanPath, "./") {
			cleanPath = strings.TrimPrefix(cleanPath, "./")
			cleanPath = strings.TrimPrefix(cleanPath, "/")
		}

		dedupKey := fmt.Sprintf("%s:%d-%d", cleanPath, startLine, endLine)
		if seen[dedupKey] {
			continue
		}

		absPath := filepath.Join(workspace, filepath.FromSlash(cleanPath))
		rel, err := filepath.Rel(workspace, absPath)
		if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
			continue // Prevent traversal outside workspace
		}

		info, err := os.Stat(absPath)
		if err != nil || info.IsDir() {
			continue
		}

		// Skip binary files
		ext := filepath.Ext(cleanPath)
		if isIgnoredExtension(ext) {
			continue
		}

		// Read file content
		data, err := os.ReadFile(absPath)
		if err != nil {
			continue
		}

		// Enforce size limit
		if int64(len(data)) > maxAttachmentSizeBytes {
			data = data[:maxAttachmentSizeBytes]
		}

		content := string(data)
		var label string
		if startLine > 0 && endLine >= startLine {
			lines := strings.Split(content, "\n")
			totalLines := len(lines)
			if startLine > totalLines {
				startLine = totalLines
			}
			if endLine > totalLines {
				endLine = totalLines
			}
			if startLine <= endLine && startLine > 0 {
				sliced := lines[startLine-1 : endLine]
				content = strings.Join(sliced, "\n")
				if startLine == endLine {
					label = fmt.Sprintf("%s (line %d)", cleanPath, startLine)
				} else {
					label = fmt.Sprintf("%s (lines %d-%d)", cleanPath, startLine, endLine)
				}
			} else {
				label = fmt.Sprintf("%s (%s)", cleanPath, FormatFileSize(info.Size()))
			}
		} else {
			label = fmt.Sprintf("%s (%s)", cleanPath, FormatFileSize(info.Size()))
		}

		seen[dedupKey] = true
		attachments = append(attachments, Attachment{
			Path:      cleanPath,
			StartLine: startLine,
			EndLine:   endLine,
			Size:      info.Size(),
			Content:   content,
			Label:     label,
		})
	}

	if len(attachments) == 0 {
		return nil, text
	}

	// Build augmented prompt with context blocks
	var b strings.Builder
	b.WriteString(text)
	b.WriteString("\n\n---")
	for _, att := range attachments {
		lang := fileExtensionToLanguage(att.Path)
		header := fmt.Sprintf("\n[Attached File Context: %s]", att.Label)
		b.WriteString(header)
		b.WriteString("\n```")
		b.WriteString(lang)
		b.WriteString("\n")
		b.WriteString(att.Content)
		if !strings.HasSuffix(att.Content, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n")
	}

	return attachments, b.String()
}

func fileExtensionToLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".mts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".jsx":
		return "jsx"
	case ".py":
		return "python"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".md":
		return "markdown"
	case ".html":
		return "html"
	case ".css":
		return "css"
	case ".sh", ".bash":
		return "bash"
	case ".rs":
		return "rust"
	case ".c", ".h":
		return "c"
	case ".cpp", ".hpp", ".cc":
		return "cpp"
	case ".sql":
		return "sql"
	default:
		return ""
	}
}
