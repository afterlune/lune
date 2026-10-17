package desktop

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxWorkspacePreviewBytes = 1 << 20
	maxWorkspaceSearchHits   = 200
)

type WorkspaceFileEntry struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size"`
}

type WorkspaceFilePreview struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

type WorkspaceFileSearchResult struct {
	Files     []WorkspaceFileEntry `json:"files"`
	Truncated bool                 `json:"truncated"`
}

var skippedWorkspaceDirectories = map[string]struct{}{
	".git": {}, ".luneagent": {}, "node_modules": {}, "vendor": {},
	"dist": {}, "build": {}, ".venv": {},
}

func (s *DesktopService) ListWorkspaceFiles(relativePath string) ([]WorkspaceFileEntry, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return nil, err
	}
	directory, err := resolveWorkspacePath(root, relativePath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(directory)
	if err != nil {
		return nil, fmt.Errorf("读取工作区目录失败: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("目标不是文件夹")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("读取工作区目录失败: %w", err)
	}
	result := make([]WorkspaceFileEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Type()&fs.ModeSymlink != 0 || entry.IsDir() && skippedWorkspaceDirectory(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		fullPath := filepath.Join(directory, entry.Name())
		relative, err := filepath.Rel(root, fullPath)
		if err != nil {
			continue
		}
		result = append(result, WorkspaceFileEntry{
			Name: entry.Name(), Path: filepath.ToSlash(relative), Directory: entry.IsDir(), Size: info.Size(),
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Directory != result[j].Directory {
			return result[i].Directory
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result, nil
}

func (s *DesktopService) SearchWorkspaceFiles(query string) (WorkspaceFileSearchResult, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return WorkspaceFileSearchResult{}, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return WorkspaceFileSearchResult{Files: []WorkspaceFileEntry{}}, nil
	}
	result := WorkspaceFileSearchResult{Files: make([]WorkspaceFileEntry, 0)}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() && path != root && skippedWorkspaceDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.Contains(strings.ToLower(entry.Name()), query) {
			return nil
		}
		if len(result.Files) == maxWorkspaceSearchHits {
			result.Truncated = true
			return fs.SkipAll
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		result.Files = append(result.Files, WorkspaceFileEntry{
			Name: entry.Name(), Path: filepath.ToSlash(relative), Size: info.Size(),
		})
		return nil
	})
	if err != nil {
		return WorkspaceFileSearchResult{}, fmt.Errorf("搜索工作区文件失败: %w", err)
	}
	sort.Slice(result.Files, func(i, j int) bool {
		return strings.ToLower(result.Files[i].Path) < strings.ToLower(result.Files[j].Path)
	})
	return result, nil
}

func (s *DesktopService) ReadWorkspaceFile(relativePath string) (WorkspaceFilePreview, error) {
	root, err := s.workspaceRoot()
	if err != nil {
		return WorkspaceFilePreview{}, err
	}
	path, err := resolveWorkspacePath(root, relativePath)
	if err != nil {
		return WorkspaceFilePreview{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return WorkspaceFilePreview{}, fmt.Errorf("读取文件失败: %w", err)
	}
	if !info.Mode().IsRegular() {
		return WorkspaceFilePreview{}, errors.New("只能预览普通文件")
	}
	file, err := os.Open(path)
	if err != nil {
		return WorkspaceFilePreview{}, fmt.Errorf("打开文件失败: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxWorkspacePreviewBytes+1))
	if err != nil {
		return WorkspaceFilePreview{}, fmt.Errorf("读取文件失败: %w", err)
	}
	if bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0 {
		return WorkspaceFilePreview{}, errors.New("二进制文件无法预览")
	}
	truncated := len(data) > maxWorkspacePreviewBytes
	if truncated {
		data = data[:maxWorkspacePreviewBytes]
	}
	return WorkspaceFilePreview{
		Path: workspaceRelativePath(root, path), Content: strings.ToValidUTF8(string(data), "�"), Truncated: truncated,
	}, nil
}

func (s *DesktopService) OpenWorkspaceFile(relativePath string) error {
	root, err := s.workspaceRoot()
	if err != nil {
		return err
	}
	path, err := resolveWorkspacePath(root, relativePath)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("打开文件失败: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("只能打开普通文件")
	}
	s.mu.Lock()
	app := s.app
	s.mu.Unlock()
	if app == nil || app.Browser == nil {
		return errors.New("桌面窗口尚未就绪")
	}
	return app.Browser.OpenFile(path)
}

func (s *DesktopService) workspaceRoot() (string, error) {
	s.mu.Lock()
	root := s.activeWorkspace
	if root == "" {
		root = s.workspace
	}
	s.mu.Unlock()
	if root == "" {
		return "", errors.New("请先打开工作区")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("解析工作区路径失败: %w", err)
	}
	return filepath.EvalSymlinks(absolute)
}

func resolveWorkspacePath(root, relative string) (string, error) {
	relative = strings.TrimSpace(relative)
	if relative == "" {
		relative = "."
	}
	if filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" {
		resolved, err := filepath.EvalSymlinks(filepath.Clean(relative))
		if err != nil || !workspaceContains(root, resolved) {
			return "", errors.New("路径必须位于当前工作区内")
		}
		return resolved, nil
	}
	clean := filepath.Clean(filepath.FromSlash(relative))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", errors.New("路径必须位于当前工作区内")
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", fmt.Errorf("工作区路径不可用: %w", err)
	}
	if !workspaceContains(root, resolved) {
		return "", errors.New("路径必须位于当前工作区内")
	}
	return resolved, nil
}

func workspaceContains(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func workspaceRelativePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

func skippedWorkspaceDirectory(name string) bool {
	_, skip := skippedWorkspaceDirectories[strings.ToLower(name)]
	return skip
}
