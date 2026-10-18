package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	core "github.com/afterlune/luneCore"
)

const maxWorktreeApplyPatchBytes = 128 << 20

type ApplyWorktreeChangesResult struct {
	Files          []string `json:"files"`
	Destination    string   `json:"destination"`
	AlreadyPresent bool     `json:"alreadyPresent"`
}

// ApplyWorktreeChanges applies a selected set of changes from one stopped
// Worktree session to the original project working tree without staging them.
func (s *DesktopService) ApplyWorktreeChanges(sessionID string, paths []string) (ApplyWorktreeChangesResult, error) {
	sessionContext, release, err := s.sessionOperation(sessionID)
	if err != nil {
		return ApplyWorktreeChangesResult{}, err
	}
	defer release()
	if sessionContext.mode != workspaceModeWorktree {
		return ApplyWorktreeChangesResult{}, errors.New("请选择一个已打开的 Worktree 会话")
	}
	if err := validateWorktreeApplyPermission(core.PermissionMode(s.controlsFor(sessionContext).EffectivePermissionMode)); err != nil {
		return ApplyWorktreeChangesResult{}, err
	}
	state, err := sessionContext.runtime.State(context.Background())
	if err != nil {
		return ApplyWorktreeChangesResult{}, fmt.Errorf("读取 Worktree 会话状态失败: %w", err)
	}
	requests, err := sessionContext.runtime.PendingRequests(context.Background())
	if err != nil {
		return ApplyWorktreeChangesResult{}, fmt.Errorf("读取 Worktree 待处理请求失败: %w", err)
	}
	if err := validateWorktreeApplyTaskState(state.Task.Status, len(state.Queue), len(requests)); err != nil {
		return ApplyWorktreeChangesResult{}, err
	}
	if len(paths) == 0 {
		return ApplyWorktreeChangesResult{}, errors.New("请先选择要应用的文件")
	}
	if s.workspace == "" {
		return ApplyWorktreeChangesResult{}, errors.New("原项目工作区已关闭")
	}
	if sessionContext.worktreePath == "" || sessionContext.repositoryRoot == "" {
		return ApplyWorktreeChangesResult{}, errors.New("Worktree 缺少原项目路径信息")
	}

	worktreeRoot, err := canonicalExistingDirectory(sessionContext.worktreePath)
	if err != nil {
		return ApplyWorktreeChangesResult{}, fmt.Errorf("Worktree 目录不可用: %w", err)
	}
	worktreeWorkspace, err := canonicalExistingDirectory(sessionContext.workspace)
	if err != nil {
		return ApplyWorktreeChangesResult{}, fmt.Errorf("Worktree 会话目录不可用: %w", err)
	}
	worktreeInfo, err := inspectGitWorkspace(worktreeWorkspace)
	if err != nil || !samePath(worktreeInfo.Root, worktreeRoot) {
		return ApplyWorktreeChangesResult{}, errors.New("Worktree 会话目录不属于其登记的 Git 工作树")
	}
	repositoryRoot, err := canonicalExistingDirectory(sessionContext.repositoryRoot)
	if err != nil {
		return ApplyWorktreeChangesResult{}, fmt.Errorf("原 Git 仓库不可用: %w", err)
	}
	workspaceRelative, err := filepath.Rel(worktreeRoot, worktreeWorkspace)
	if err != nil || workspaceRelative == ".." || strings.HasPrefix(workspaceRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(workspaceRelative) {
		return ApplyWorktreeChangesResult{}, errors.New("Worktree 会话目录无效")
	}
	destination := repositoryRoot
	if workspaceRelative != "." {
		destination = filepath.Join(repositoryRoot, workspaceRelative)
	}
	activeProject, err := canonicalExistingDirectory(s.workspace)
	if err != nil {
		return ApplyWorktreeChangesResult{}, fmt.Errorf("原项目工作区不可用: %w", err)
	}
	if !samePath(activeProject, destination) {
		return ApplyWorktreeChangesResult{}, errors.New("当前工作区与此 Worktree 的原项目不一致，请重新打开原项目")
	}

	result, err := applySelectedWorktreeChanges(worktreeWorkspace, repositoryRoot, paths)
	if err != nil {
		return ApplyWorktreeChangesResult{}, err
	}
	result.Destination = destination
	return result, nil
}

func validateWorktreeApplyPermission(mode core.PermissionMode) error {
	switch mode {
	case core.PermissionReadOnly:
		return errors.New("只读权限下不能将 Worktree 改动写回原项目")
	case core.PermissionWorkspace, core.PermissionAsk, core.PermissionFullAccess:
		return nil
	default:
		return fmt.Errorf("未知的权限档位 %q，无法应用 Worktree 改动", mode)
	}
}

func validateWorktreeApplyTaskState(status core.TaskStatus, queued, pending int) error {
	if status == core.TaskRunning || status == core.TaskQueued || status == core.TaskPaused || queued > 0 || pending > 0 {
		return errors.New("Worktree 任务正在运行、排队或等待处理，请停止并处理完当前请求后重试")
	}
	return nil
}

func applySelectedWorktreeChanges(workspace, destinationRoot string, requestedPaths []string) (ApplyWorktreeChangesResult, error) {
	listed, err := listWorkspaceChangesAt(workspace)
	if err != nil {
		return ApplyWorktreeChangesResult{}, err
	}
	if !listed.Available {
		return ApplyWorktreeChangesResult{}, errors.New(listed.Message)
	}
	gitInfo, err := inspectGitWorkspace(workspace)
	if err != nil {
		return ApplyWorktreeChangesResult{}, errors.New("Worktree 已不在可用的 Git 仓库中")
	}
	targetInfo, err := inspectGitWorkspace(destinationRoot)
	if err != nil || !samePath(targetInfo.Root, destinationRoot) {
		return ApplyWorktreeChangesResult{}, errors.New("原项目目录不再是可用的 Git 仓库")
	}

	changesByPath := make(map[string]WorkspaceChangeView, len(listed.Files))
	for _, change := range listed.Files {
		changesByPath[change.Path] = change
	}
	selected := make([]WorkspaceChangeView, 0, len(requestedPaths))
	seen := make(map[string]struct{}, len(requestedPaths))
	pathspecs := make([]string, 0, len(requestedPaths)*2)
	seenPathspecs := make(map[string]struct{}, len(requestedPaths)*2)
	for _, requested := range requestedPaths {
		if _, duplicate := seen[requested]; duplicate {
			continue
		}
		seen[requested] = struct{}{}
		change, found := changesByPath[requested]
		if !found {
			return ApplyWorktreeChangesResult{}, fmt.Errorf("文件 %q 已不在当前 Worktree 变更列表中，请刷新后重试", requested)
		}
		selected = append(selected, change)
		repositoryPath, err := repositoryPathForWorkspaceFile(gitInfo.RelativePath, change.Path)
		if err != nil {
			return ApplyWorktreeChangesResult{}, err
		}
		addGitPathspec(&pathspecs, seenPathspecs, repositoryPath)
		if change.originalRepositoryPath != "" {
			original, err := cleanRepositoryRelativePath(change.originalRepositoryPath)
			if err != nil {
				return ApplyWorktreeChangesResult{}, fmt.Errorf("重命名来源路径无效: %w", err)
			}
			addGitPathspec(&pathspecs, seenPathspecs, original)
		}
	}
	if len(selected) == 0 {
		return ApplyWorktreeChangesResult{}, errors.New("请先选择要应用的文件")
	}

	patch, err := buildWorktreeChangePatch(gitInfo.Root, pathspecs)
	if err != nil {
		return ApplyWorktreeChangesResult{}, err
	}
	if len(patch) == 0 {
		return ApplyWorktreeChangesResult{}, errors.New("所选文件已没有可应用的差异，请刷新列表")
	}
	files := make([]string, len(selected))
	for index, change := range selected {
		files[index] = change.Path
	}
	if err := runGitInput(destinationRoot, patch, "apply", "--reverse", "--check", "--binary", "--whitespace=nowarn", "-"); err == nil {
		return ApplyWorktreeChangesResult{Files: files, AlreadyPresent: true}, nil
	}
	if err := runGitInput(destinationRoot, patch, "apply", "--check", "--binary", "--whitespace=nowarn", "-"); err != nil {
		return ApplyWorktreeChangesResult{}, fmt.Errorf("原项目存在冲突，未应用任何文件: %w", err)
	}
	if err := runGitInput(destinationRoot, patch, "apply", "--binary", "--whitespace=nowarn", "-"); err != nil {
		return ApplyWorktreeChangesResult{}, fmt.Errorf("应用 Worktree 改动失败: %w", err)
	}

	return ApplyWorktreeChangesResult{Files: files}, nil
}

func buildWorktreeChangePatch(repository string, pathspecs []string) ([]byte, error) {
	base, err := gitBaseRevision(repository)
	if err != nil {
		return nil, err
	}
	indexRoot, err := os.MkdirTemp("", "lune-worktree-apply-")
	if err != nil {
		return nil, fmt.Errorf("创建临时 Git 索引失败: %w", err)
	}
	defer os.RemoveAll(indexRoot)
	indexPath := filepath.Join(indexRoot, "index")
	env := withEnvironment(os.Environ(), "GIT_INDEX_FILE", indexPath)
	if _, err := runGitOutputWithEnv(repository, env, "read-tree", base); err != nil {
		return nil, fmt.Errorf("准备 Worktree 变更索引失败: %w", err)
	}
	addArgs := []string{"add", "-A", "--"}
	addArgs = append(addArgs, pathspecs...)
	if _, err := runGitOutputWithEnv(repository, env, addArgs...); err != nil {
		return nil, fmt.Errorf("读取 Worktree 文件改动失败: %w", err)
	}
	diffArgs := []string{"diff", "--cached", "--binary", "--no-ext-diff", "--no-color", "--no-renames", base, "--"}
	diffArgs = append(diffArgs, pathspecs...)
	patch, truncated, err := runGitOutputLimitedWithEnv(repository, maxWorktreeApplyPatchBytes, env, diffArgs...)
	if err != nil {
		return nil, fmt.Errorf("生成 Worktree 改动补丁失败: %w", err)
	}
	if truncated {
		return nil, fmt.Errorf("所选改动补丁超过 %d MiB，无法一次应用", maxWorktreeApplyPatchBytes/(1<<20))
	}
	return patch, nil
}

func repositoryPathForWorkspaceFile(workspaceRelative, filePath string) (string, error) {
	filePath, err := cleanRepositoryRelativePath(filePath)
	if err != nil {
		return "", fmt.Errorf("Worktree 文件路径无效: %w", err)
	}
	if workspaceRelative == "" || workspaceRelative == "." {
		return filePath, nil
	}
	joined, err := cleanRepositoryRelativePath(filepath.ToSlash(filepath.Join(filepath.FromSlash(workspaceRelative), filepath.FromSlash(filePath))))
	if err != nil {
		return "", fmt.Errorf("Worktree 文件路径无效: %w", err)
	}
	return joined, nil
}

func cleanRepositoryRelativePath(path string) (string, error) {
	if path == "" || filepath.IsAbs(filepath.FromSlash(path)) || filepath.VolumeName(filepath.FromSlash(path)) != "" {
		return "", errors.New("Git 路径必须是仓库内的相对路径")
	}
	clean := filepath.Clean(filepath.FromSlash(path))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return "", errors.New("Git 路径超出仓库目录")
	}
	return filepath.ToSlash(clean), nil
}

func addGitPathspec(pathspecs *[]string, seen map[string]struct{}, repositoryPath string) {
	pathspec := ":(literal)" + repositoryPath
	if _, exists := seen[pathspec]; exists {
		return
	}
	seen[pathspec] = struct{}{}
	*pathspecs = append(*pathspecs, pathspec)
}

func canonicalExistingDirectory(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("目标不是文件夹")
	}
	return resolved, nil
}

func runGitOutputWithEnv(repository string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := newGitCommand(ctx, args...)
	command.Dir = repository
	command.Env = append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "PAGER=cat")
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, gitCommandError(output, err)
	}
	return output, nil
}

func runGitOutputLimitedWithEnv(repository string, limit int, env []string, args ...string) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := newGitCommand(ctx, args...)
	command.Dir = repository
	command.Env = append(env, "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "PAGER=cat")
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return nil, false, err
	}
	output, readErr := readLimitedAndDrain(stdout, limit)
	waitErr := command.Wait()
	if readErr != nil {
		return nil, false, readErr
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

func readLimitedAndDrain(reader io.Reader, limit int) ([]byte, error) {
	output, readErr := io.ReadAll(io.LimitReader(reader, int64(limit+1)))
	if readErr != nil {
		return nil, readErr
	}
	if _, err := io.Copy(io.Discard, reader); err != nil {
		return nil, err
	}
	return output, nil
}

func runGitInput(repository string, input []byte, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := newGitCommand(ctx, args...)
	command.Dir = repository
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "PAGER=cat")
	command.Stdin = bytes.NewReader(input)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return gitCommandError(output.Bytes(), err)
	}
	return nil
}

func withEnvironment(env []string, name, value string) []string {
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		key, _, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(key, name) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, name+"="+value)
}
