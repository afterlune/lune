package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func newWorktreeID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("创建 Worktree 标识: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

type gitWorkspace struct {
	Root          string
	WorkspacePath string
	RelativePath  string
}

func inspectGitWorkspace(workspace string) (gitWorkspace, error) {
	command := newGitCommand(context.Background(), "-C", workspace, "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err != nil {
		return gitWorkspace{}, errors.New("Worktree 模式需要可用的 Git 仓库")
	}
	root := strings.TrimSpace(string(output))
	if root == "" {
		return gitWorkspace{}, errors.New("无法读取 Git 仓库根目录")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return gitWorkspace{}, fmt.Errorf("解析 Git 仓库根目录: %w", err)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return gitWorkspace{}, fmt.Errorf("解析工作区路径: %w", err)
	}
	workspace = resolvedWorkspace
	relative, err := filepath.Rel(root, workspace)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return gitWorkspace{}, errors.New("工作区不在 Git 仓库目录内")
	}
	if relative == "." {
		relative = ""
	}
	return gitWorkspace{Root: root, WorkspacePath: workspace, RelativePath: relative}, nil
}

func createGitWorktree(workspace, destination string) (gitWorkspace, string, error) {
	info, err := inspectGitWorkspace(workspace)
	if err != nil {
		return gitWorkspace{}, "", err
	}
	if _, err := os.Stat(destination); err == nil {
		return gitWorkspace{}, "", fmt.Errorf("Worktree 目录已存在: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return gitWorkspace{}, "", fmt.Errorf("检查 Worktree 目录: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return gitWorkspace{}, "", fmt.Errorf("创建 Worktree 父目录: %w", err)
	}
	command := newGitCommand(context.Background(), "-C", info.Root, "worktree", "add", "--detach", destination, "HEAD")
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		if _, statErr := os.Stat(destination); statErr == nil {
			if cleanupErr := removeGitWorktree(info.Root, destination); cleanupErr != nil {
				if removeErr := os.Remove(destination); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					cleanupErr = errors.Join(cleanupErr, removeErr)
				}
				return gitWorkspace{}, "", errors.Join(fmt.Errorf("创建 Git Worktree 失败: %s", message), cleanupErr)
			}
		}
		return gitWorkspace{}, "", fmt.Errorf("创建 Git Worktree 失败: %s", message)
	}
	workingDirectory := destination
	if info.RelativePath != "" {
		workingDirectory = filepath.Join(destination, info.RelativePath)
	}
	dirInfo, statErr := os.Stat(workingDirectory)
	if statErr != nil || !dirInfo.IsDir() {
		_ = removeGitWorktree(info.Root, destination)
		return gitWorkspace{}, "", errors.New("Worktree 中找不到所选工作区子目录")
	}
	return info, workingDirectory, nil
}

func removeGitWorktree(repository, destination string) error {
	command := newGitCommand(context.Background(), "-C", repository, "worktree", "remove", destination)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("清理未完成的 Worktree 失败: %s", message)
	}
	return nil
}
