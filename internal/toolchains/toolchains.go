package toolchains

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Candidate is a host PATH lookup result. Detection never
// launches the executable and never modifies file permissions.
type Candidate struct {
	Name       string `json:"name"`
	Executable string `json:"executable"`
	Root       string `json:"root"`
	Note       string `json:"note,omitempty"`
}

var CommonToolchainNames = []string{"go", "node", "npm", "pnpm", "python", "py", "git"}

// Detect discovers installed command toolchains using host PATH.
func Detect() []Candidate {
	return DetectWith(exec.LookPath)
}

// DetectWith discovers installed command toolchains with a custom lookup function.
func DetectWith(lookPath func(string) (string, error)) []Candidate {
	if lookPath == nil {
		return nil
	}
	var candidates []Candidate
	for _, name := range CommonToolchainNames {
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
		root := Root(name, absolute)
		candidate := Candidate{Name: name, Executable: absolute, Root: root}
		if runtime.GOOS == "windows" && strings.EqualFold(filepath.Clean(root), filepath.Clean(os.Getenv("SystemRoot"))) {
			candidate.Root = ""
			candidate.Note = "py 启动器位于 Windows 目录；请手动选择 Python 安装目录，避免授权整个系统目录。"
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

// Root extracts the installation root directory from an executable path.
func Root(name, executable string) string {
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

// NormalizePaths validates and canonicalizes toolchain root directories.
func NormalizePaths(paths []string) ([]string, error) {
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

// AvailablePaths filters paths to those that currently exist and resolve to directories.
func AvailablePaths(paths []string) []string {
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
	if normalized, err := NormalizePaths(available); err == nil {
		return normalized
	}
	return nil
}

// AutoDetectRoots returns normalized roots of all automatically detected host toolchains.
func AutoDetectRoots() []string {
	candidates := Detect()
	var roots []string
	for _, c := range candidates {
		if c.Root != "" {
			roots = append(roots, c.Root)
		}
	}
	if normalized, err := NormalizePaths(roots); err == nil {
		return normalized
	}
	return nil
}
