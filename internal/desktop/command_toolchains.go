package desktop

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// CommandToolchainCandidate is a host PATH lookup result. Detection never
// launches the executable and never adds its root to workspace permissions.
type CommandToolchainCandidate struct {
	Name       string `json:"name"`
	Executable string `json:"executable"`
	Root       string `json:"root"`
	Note       string `json:"note,omitempty"`
}

var commandToolchainNames = []string{"go", "node", "npm", "pnpm", "python", "py", "git"}

func (s *DesktopService) DetectCommandToolchains() []CommandToolchainCandidate {
	return detectCommandToolchains(exec.LookPath)
}

func detectCommandToolchains(lookPath func(string) (string, error)) []CommandToolchainCandidate {
	if lookPath == nil {
		return nil
	}
	var candidates []CommandToolchainCandidate
	for _, name := range commandToolchainNames {
		executable, err := lookPath(name)
		if err != nil {
			continue
		}
		absolute, err := filepath.Abs(executable)
		if err != nil {
			continue
		}
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			absolute = resolved
		}
		root := commandToolchainRoot(name, absolute)
		candidate := CommandToolchainCandidate{Name: name, Executable: absolute, Root: root}
		if runtime.GOOS == "windows" && strings.EqualFold(filepath.Clean(root), filepath.Clean(os.Getenv("SystemRoot"))) {
			candidate.Root = ""
			candidate.Note = "py 启动器位于 Windows 目录；请手动选择 Python 安装目录，避免授权整个系统目录。"
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func commandToolchainRoot(name, executable string) string {
	dir := filepath.Dir(executable)
	parent := filepath.Dir(dir)
	base := strings.ToLower(filepath.Base(dir))
	switch strings.ToLower(name) {
	case "go":
		if base == "bin" {
			return parent
		}
	case "git":
		if base == "cmd" || base == "bin" {
			return parent
		}
	case "npm", "pnpm", "python", "py":
		if base == "scripts" {
			return parent
		}
	}
	return dir
}

func normalizeCommandToolchainPaths(paths []string) ([]string, error) {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			return nil, errors.New("命令工具链目录不能为空")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("解析命令工具链目录 %q: %w", path, err)
		}
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return nil, fmt.Errorf("命令工具链目录不可用 %q: %w", path, err)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, fmt.Errorf("检查命令工具链目录 %q: %w", path, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("命令工具链授权路径必须是目录: %q", path)
		}
		resolved, err = filepath.Abs(resolved)
		if err != nil {
			return nil, fmt.Errorf("解析命令工具链目录 %q: %w", path, err)
		}
		key := filepath.Clean(resolved)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, resolved)
	}
	return result, nil
}

// availableCommandToolchainPaths lets Desktop reopen a workspace if a
// previously authorized directory was removed. The stale grant stays visible
// in settings so the user can remove or replace it; Core receives only paths
// that still resolve to directories.
func availableCommandToolchainPaths(paths []string) []string {
	var available []string
	for _, path := range paths {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			continue
		}
		available = append(available, resolved)
	}
	if normalized, err := normalizeCommandToolchainPaths(available); err == nil {
		return normalized
	}
	return nil
}
