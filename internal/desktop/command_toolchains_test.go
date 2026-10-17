package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectCommandToolchainsOnlyLooksUpCandidates(t *testing.T) {
	root := t.TempDir()
	goRoot := filepath.Join(root, "Go")
	gitRoot := filepath.Join(root, "Git")
	for _, dir := range []string{filepath.Join(goRoot, "bin"), filepath.Join(gitRoot, "cmd")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	goExe := filepath.Join(goRoot, "bin", "go.exe")
	gitExe := filepath.Join(gitRoot, "cmd", "git.exe")
	for _, path := range []string{goExe, gitExe} {
		if err := os.WriteFile(path, []byte("placeholder"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	lookups := map[string]string{"go": goExe, "git": gitExe}
	var requested []string
	candidates := detectCommandToolchains(func(name string) (string, error) {
		requested = append(requested, name)
		path, ok := lookups[name]
		if !ok {
			return "", errors.New("not found")
		}
		return path, nil
	})
	if len(requested) != len(commandToolchainNames) {
		t.Fatalf("looked up %v; want all common tool names %v", requested, commandToolchainNames)
	}
	if len(candidates) != 2 {
		t.Fatalf("detected %d candidates: %#v", len(candidates), candidates)
	}
	if candidates[0].Name != "go" || candidates[0].Root != goRoot || candidates[0].Executable != goExe {
		t.Fatalf("Go candidate = %#v", candidates[0])
	}
	if candidates[1].Name != "git" || candidates[1].Root != gitRoot {
		t.Fatalf("Git candidate = %#v", candidates[1])
	}
}

func TestNormalizeCommandToolchainPathsRequiresDirectoriesAndDeduplicates(t *testing.T) {
	directory := t.TempDir()
	duplicate := directory
	if runtime.GOOS == "windows" {
		duplicate = filepath.ToSlash(directory)
	}
	normalized, err := normalizeCommandToolchainPaths([]string{directory, duplicate})
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 1 {
		t.Fatalf("normalized paths = %#v, want one path", normalized)
	}
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeCommandToolchainPaths([]string{file}); err == nil {
		t.Fatal("file path was accepted as a toolchain directory")
	}
	if _, err := normalizeCommandToolchainPaths([]string{filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("missing path was accepted as a toolchain directory")
	}
}
