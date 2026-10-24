package toolchains

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectWithOnlyLooksUpCandidates(t *testing.T) {
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
	candidates := DetectWith(func(name string) (string, error) {
		requested = append(requested, name)
		path, ok := lookups[name]
		if !ok {
			return "", errors.New("not found")
		}
		return path, nil
	})
	if len(requested) != len(CommonToolchainNames) {
		t.Fatalf("looked up %v; want all common tool names %v", requested, CommonToolchainNames)
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

func TestNormalizePathsRequiresDirectoriesAndDeduplicates(t *testing.T) {
	directory := t.TempDir()
	duplicate := directory
	if runtime.GOOS == "windows" {
		duplicate = filepath.ToSlash(directory)
	}
	normalized, err := NormalizePaths([]string{directory, duplicate})
	if err != nil {
		t.Fatal(err)
	}
	if len(normalized) != 1 {
		t.Fatalf("NormalizePaths returned %d entries; want deduplicated single entry", len(normalized))
	}
	file := filepath.Join(directory, "plain.txt")
	if err := os.WriteFile(file, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NormalizePaths([]string{file}); err == nil {
		t.Fatal("NormalizePaths succeeded on a file")
	}
	if _, err := NormalizePaths([]string{filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("NormalizePaths succeeded on a missing directory")
	}
}
