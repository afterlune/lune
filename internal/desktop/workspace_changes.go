package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxWorkspaceDiffBytes = 8 << 20
	emptyGitTree          = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
)

type WorkspaceChangeView struct {
	Path                   string `json:"path"`
	OriginalPath           string `json:"originalPath,omitempty"`
	Status                 string `json:"status"`
	Added                  int    `json:"added"`
	Removed                int    `json:"removed"`
	Binary                 bool   `json:"binary"`
	originalRepositoryPath string
}

type WorkspaceChangesView struct {
	Available bool                  `json:"available"`
	Message   string                `json:"message"`
	Files     []WorkspaceChangeView `json:"files"`
	Added     int                   `json:"added"`
	Removed   int                   `json:"removed"`
	Refreshed string                `json:"refreshedAt"`
}

type WorkspaceDiffView struct {
	Path      string `json:"path"`
	Status    string `json:"status"`
	Content   string `json:"content"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
}

type gitStatusEntry struct {
	path         string
	originalPath string
	status       string
}

type gitNumstat struct {
	path    string
	oldPath string
	added   int
	removed int
	binary  bool
}

func (s *DesktopService) ListWorkspaceChanges() (WorkspaceChangesView, error) {
	workspace, err := s.workspaceRoot()
	if err != nil {
		return WorkspaceChangesView{}, err
	}
	return listWorkspaceChangesAt(workspace)
}

func listWorkspaceChangesAt(workspace string) (WorkspaceChangesView, error) {
	result := WorkspaceChangesView{Files: []WorkspaceChangeView{}, Refreshed: time.Now().UTC().Format(time.RFC3339Nano)}
	if _, err := exec.LookPath("git"); err != nil {
		result.Message = "找不到 Git，请安装 Git 后重试。"
		return result, nil
	}
	gitInfo, err := inspectGitWorkspace(workspace)
	if err != nil {
		result.Message = "当前会话目录不在可用的 Git 仓库中。"
		return result, nil
	}
	base, err := gitBaseRevision(gitInfo.Root)
	if err != nil {
		return WorkspaceChangesView{}, err
	}
	pathspec := workspaceGitPathspec(gitInfo.RelativePath)
	statusArgs := []string{"status", "--porcelain=v1", "-z", "--untracked-files=all"}
	if pathspec != "" {
		statusArgs = append(statusArgs, "--", pathspec)
	}
	statusOutput, err := runGitOutput(gitInfo.Root, statusArgs...)
	if err != nil {
		return WorkspaceChangesView{}, fmt.Errorf("读取 Git 工作区状态失败: %w", err)
	}
	entries := parseGitStatusZ(statusOutput)
	numstatArgs := []string{"diff", "--no-ext-diff", "--no-color", "--numstat", "-z", "--find-renames", base}
	if pathspec != "" {
		numstatArgs = append(numstatArgs, "--", pathspec)
	}
	numstatOutput, err := runGitOutput(gitInfo.Root, numstatArgs...)
	if err != nil {
		return WorkspaceChangesView{}, fmt.Errorf("读取 Git 差异统计失败: %w", err)
	}
	numstats := parseGitNumstatZ(numstatOutput)
	result.Available = true
	for _, entry := range entries {
		path, inScope := pathWithinGitWorkspace(entry.path, gitInfo.RelativePath)
		if !inScope {
			continue
		}
		change := WorkspaceChangeView{Path: path, Status: entry.status, originalRepositoryPath: entry.originalPath}
		if entry.originalPath != "" {
			change.OriginalPath, _ = pathWithinGitWorkspace(entry.originalPath, gitInfo.RelativePath)
			if change.OriginalPath == "" {
				change.OriginalPath = filepath.ToSlash(entry.originalPath)
			}
		}
		stats, found := numstats[entry.path]
		if !found && entry.originalPath != "" {
			stats, found = numstats[entry.originalPath]
		}
		if found {
			change.Added, change.Removed, change.Binary = stats.added, stats.removed, stats.binary
		}
		if entry.status == "??" {
			added, binary, statErr := countUntrackedLines(workspace, path)
			if statErr != nil {
				return WorkspaceChangesView{}, statErr
			}
			change.Added, change.Binary = added, binary
		}
		result.Added += change.Added
		result.Removed += change.Removed
		result.Files = append(result.Files, change)
	}
	sort.Slice(result.Files, func(i, j int) bool {
		return strings.ToLower(result.Files[i].Path) < strings.ToLower(result.Files[j].Path)
	})
	return result, nil
}

func (s *DesktopService) ReadWorkspaceDiff(relativePath string) (WorkspaceDiffView, error) {
	listed, err := s.ListWorkspaceChanges()
	if err != nil {
		return WorkspaceDiffView{}, err
	}
	if !listed.Available {
		return WorkspaceDiffView{}, errors.New(listed.Message)
	}
	var selected *WorkspaceChangeView
	for index := range listed.Files {
		if listed.Files[index].Path == relativePath {
			selected = &listed.Files[index]
			break
		}
	}
	if selected == nil {
		return WorkspaceDiffView{}, errors.New("所选文件已不在当前 Git 变更列表中，请刷新后重试")
	}
	workspace, err := s.workspaceRoot()
	if err != nil {
		return WorkspaceDiffView{}, err
	}
	gitInfo, err := inspectGitWorkspace(workspace)
	if err != nil {
		return WorkspaceDiffView{}, errors.New("当前会话目录不在可用的 Git 仓库中")
	}
	base, err := gitBaseRevision(gitInfo.Root)
	if err != nil {
		return WorkspaceDiffView{}, err
	}
	repoPath := filepath.ToSlash(filepath.Join(gitInfo.RelativePath, filepath.FromSlash(relativePath)))
	repoPath = strings.TrimPrefix(repoPath, "./")
	if selected.Status == "??" {
		content, binary, truncated, err := untrackedDiff(workspace, relativePath)
		if err != nil {
			return WorkspaceDiffView{}, err
		}
		return WorkspaceDiffView{Path: relativePath, Status: selected.Status, Content: content, Binary: binary, Truncated: truncated}, nil
	}
	output, truncated, err := runGitOutputLimited(gitInfo.Root, maxWorkspaceDiffBytes,
		"diff", "--no-ext-diff", "--no-color", "--unified=3", "--find-renames", base, "--", ":(literal)"+repoPath)
	if err != nil {
		return WorkspaceDiffView{}, fmt.Errorf("读取 Git 文件差异失败: %w", err)
	}
	return WorkspaceDiffView{
		Path: relativePath, Status: selected.Status, Content: string(output),
		Binary: selected.Binary, Truncated: truncated,
	}, nil
}

func gitBaseRevision(repository string) (string, error) {
	output, err := runGitOutput(repository, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return emptyGitTree, nil
	}
	base := strings.TrimSpace(string(output))
	if base == "" {
		return "", errors.New("Git 返回了空 HEAD")
	}
	return base, nil
}

func workspaceGitPathspec(relative string) string {
	if relative == "" || relative == "." {
		return ""
	}
	return ":(literal)" + filepath.ToSlash(relative) + "/"
}

func pathWithinGitWorkspace(repositoryPath, workspaceRelative string) (string, bool) {
	repositoryPath = filepath.ToSlash(repositoryPath)
	workspaceRelative = strings.Trim(filepath.ToSlash(workspaceRelative), "/")
	if workspaceRelative == "" || workspaceRelative == "." {
		return repositoryPath, repositoryPath != ""
	}
	if repositoryPath == workspaceRelative {
		return filepath.Base(repositoryPath), true
	}
	prefix := workspaceRelative + "/"
	if !strings.HasPrefix(repositoryPath, prefix) {
		return "", false
	}
	return strings.TrimPrefix(repositoryPath, prefix), true
}

func parseGitStatusZ(output []byte) []gitStatusEntry {
	fields := bytes.Split(output, []byte{0})
	entries := make([]gitStatusEntry, 0, len(fields))
	for index := 0; index < len(fields); index++ {
		field := fields[index]
		if len(field) < 4 || field[2] != ' ' {
			continue
		}
		status := string(field[:2])
		entry := gitStatusEntry{status: status, path: string(field[3:])}
		if strings.ContainsAny(status, "RC") && index+1 < len(fields) {
			index++
			entry.originalPath = string(fields[index])
		}
		entries = append(entries, entry)
	}
	return entries
}

func parseGitNumstatZ(output []byte) map[string]gitNumstat {
	result := make(map[string]gitNumstat)
	for len(output) > 0 {
		firstTab := bytes.IndexByte(output, '\t')
		if firstTab < 0 {
			break
		}
		secondTabRelative := bytes.IndexByte(output[firstTab+1:], '\t')
		if secondTabRelative < 0 {
			break
		}
		secondTab := firstTab + 1 + secondTabRelative
		added := string(output[:firstTab])
		removed := string(output[firstTab+1 : secondTab])
		rest := output[secondTab+1:]
		if len(rest) == 0 {
			break
		}
		if rest[0] == 0 {
			rest = rest[1:]
			oldPath, rest := consumeNULField(rest)
			newPath, tail := consumeNULField(rest)
			output = tail
			entry := makeGitNumstat(newPath, oldPath, added, removed)
			result[newPath] = entry
			result[oldPath] = entry
			continue
		}
		path, tail := consumeNULField(rest)
		output = tail
		result[path] = makeGitNumstat(path, "", added, removed)
	}
	return result
}

func consumeNULField(input []byte) (string, []byte) {
	index := bytes.IndexByte(input, 0)
	if index < 0 {
		return string(input), nil
	}
	return string(input[:index]), input[index+1:]
}

func makeGitNumstat(path, oldPath, added, removed string) gitNumstat {
	entry := gitNumstat{path: path, oldPath: oldPath, binary: added == "-" || removed == "-"}
	if !entry.binary {
		entry.added, _ = strconv.Atoi(added)
		entry.removed, _ = strconv.Atoi(removed)
	}
	return entry
}

func countUntrackedLines(workspace, relativePath string) (int, bool, error) {
	path, err := safeWorkspaceGitPath(workspace, relativePath)
	if err != nil {
		return 0, false, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return 0, false, fmt.Errorf("读取未跟踪文件失败: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return 0, false, fmt.Errorf("读取符号链接失败: %w", err)
		}
		return textLineCount([]byte(target)), false, nil
	} else if info.Mode().IsRegular() {
		file, err := os.Open(path)
		if err != nil {
			return 0, false, fmt.Errorf("读取未跟踪文件失败: %w", err)
		}
		defer file.Close()
		buffer := make([]byte, 32*1024)
		lines, total, lastByte := 0, 0, byte(0)
		for {
			count, readErr := file.Read(buffer)
			if count > 0 {
				chunk := buffer[:count]
				if bytes.IndexByte(chunk, 0) >= 0 {
					return 0, true, nil
				}
				lines += bytes.Count(chunk, []byte{'\n'})
				total += count
				lastByte = chunk[count-1]
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return 0, false, fmt.Errorf("读取未跟踪文件失败: %w", readErr)
			}
		}
		if total > 0 && lastByte != '\n' {
			lines++
		}
		return lines, false, nil
	} else {
		return 0, false, errors.New("未跟踪变更不是普通文件")
	}
	return 0, false, nil
}

func textLineCount(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	count := bytes.Count(data, []byte{'\n'})
	if data[len(data)-1] != '\n' {
		count++
	}
	return count
}

func untrackedDiff(workspace, relativePath string) (string, bool, bool, error) {
	path, err := safeWorkspaceGitPath(workspace, relativePath)
	if err != nil {
		return "", false, false, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", false, false, fmt.Errorf("读取未跟踪文件失败: %w", err)
	}
	var data []byte
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return "", false, false, fmt.Errorf("读取符号链接失败: %w", err)
		}
		data = []byte(target)
	} else if info.Mode().IsRegular() {
		file, err := os.Open(path)
		if err != nil {
			return "", false, false, fmt.Errorf("读取未跟踪文件失败: %w", err)
		}
		data, err = io.ReadAll(io.LimitReader(file, maxWorkspaceDiffBytes+1))
		closeErr := file.Close()
		if err != nil {
			return "", false, false, fmt.Errorf("读取未跟踪文件失败: %w", err)
		}
		if closeErr != nil {
			return "", false, false, fmt.Errorf("关闭未跟踪文件失败: %w", closeErr)
		}
	} else {
		return "", false, false, errors.New("未跟踪变更不是普通文件")
	}
	truncated := len(data) > maxWorkspaceDiffBytes
	if truncated {
		data = data[:maxWorkspaceDiffBytes]
	}
	binary := bytes.IndexByte(data, 0) >= 0
	if binary {
		return "二进制文件，无法显示文本差异。", true, truncated, nil
	}
	content := formatAddedFileDiff(relativePath, data, info.Mode())
	return content, false, truncated, nil
}

func safeWorkspaceGitPath(workspace, relativePath string) (string, error) {
	if relativePath == "" || filepath.IsAbs(filepath.FromSlash(relativePath)) || filepath.VolumeName(filepath.FromSlash(relativePath)) != "" {
		return "", errors.New("Git 文件路径无效")
	}
	clean := filepath.Clean(filepath.FromSlash(relativePath))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("Git 文件路径超出当前会话目录")
	}
	path := filepath.Join(workspace, clean)
	relative, err := filepath.Rel(workspace, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", errors.New("Git 文件路径超出当前会话目录")
	}
	return path, nil
}

func formatAddedFileDiff(path string, data []byte, mode os.FileMode) string {
	path = filepath.ToSlash(path)
	if strings.ContainsAny(path, "\t\n\r\"\\") {
		path = strconv.Quote(path)
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	trailingNewline := bytes.HasSuffix(data, []byte{'\n'})
	if trailingNewline {
		lines = lines[:len(lines)-1]
	}
	if len(data) == 0 {
		lines = nil
	}
	modeText := "100644"
	if mode&os.ModeSymlink != 0 {
		modeText = "120000"
	} else if mode.Perm()&0o111 != 0 {
		modeText = "100755"
	}
	var result strings.Builder
	fmt.Fprintf(&result, "diff --git a/%s b/%s\nnew file mode %s\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", path, path, modeText, path, len(lines))
	for _, line := range lines {
		result.WriteByte('+')
		result.WriteString(line)
		result.WriteByte('\n')
	}
	if len(lines) > 0 && !trailingNewline {
		result.WriteString("\\ No newline at end of file\n")
	}
	return result.String()
}

func runGitOutput(repository string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = repository
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "PAGER=cat")
	output, err := command.Output()
	if err != nil {
		return nil, gitCommandError(output, err)
	}
	return output, nil
}

func runGitOutputLimited(repository string, limit int, args ...string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = repository
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "PAGER=cat")
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return nil, false, err
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, int64(limit+1)))
	_, drainErr := io.Copy(io.Discard, stdout)
	waitErr := command.Wait()
	if readErr != nil {
		return nil, false, readErr
	}
	if drainErr != nil {
		return nil, false, drainErr
	}
	if waitErr != nil {
		return nil, false, gitCommandError(stderr.Bytes(), waitErr)
	}
	truncated := len(output) > limit
	if truncated {
		output = output[:limit]
	}
	return output, truncated, nil
}

func gitCommandError(output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return err
	}
	return fmt.Errorf("%s: %w", message, err)
}
