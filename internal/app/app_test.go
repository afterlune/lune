package app

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
)

func TestUseTUIRequiresBothStreamsToBeTerminals(t *testing.T) {
	input, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer output.Close()
	if useTUI(input, output) {
		t.Fatal("piped streams should use the line interface")
	}
}

func TestRunWithStreamsHelp(t *testing.T) {
	var stdout, stderr strings.Builder
	err := RunWithStreams([]string{"--help"}, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Errorf("expected help output in stdout, got: %q", stdout.String())
	}
}

func TestRunWithStreamsVersion(t *testing.T) {
	var stdout, stderr strings.Builder
	err := RunWithStreams([]string{"-v"}, nil, &stdout, &stderr)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
	if !strings.Contains(stdout.String(), "LuneAgent") {
		t.Errorf("expected version output in stdout, got: %q", stdout.String())
	}
}

func TestRunWithStreamsInvalidFlags(t *testing.T) {
	var stdout, stderr strings.Builder
	err := RunWithStreams([]string{"--invalid-flag"}, nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for invalid flag, got nil")
	}

	err = RunWithStreams([]string{"--tui", "--cli"}, nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for conflicting flags --tui and --cli, got nil")
	}

	err = RunWithStreams([]string{"-p", "invalid-perm"}, nil, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for invalid permission, got nil")
	}
}

func TestCombinePromptAndStdin(t *testing.T) {
	tests := []struct {
		name     string
		prompt   string
		stdin    string
		expected string
	}{
		{
			name:     "both empty",
			prompt:   "",
			stdin:    "",
			expected: "",
		},
		{
			name:     "prompt only",
			prompt:   "what is this?",
			stdin:    "",
			expected: "what is this?",
		},
		{
			name:     "stdin only",
			prompt:   "",
			stdin:    "package main",
			expected: "package main",
		},
		{
			name:     "both provided with whitespace trimming",
			prompt:   "  review changes  \n",
			stdin:    "\n  diff --git a b  \n",
			expected: "review changes\n\n---\ndiff --git a b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := combinePromptAndStdin(tt.prompt, tt.stdin)
			if got != tt.expected {
				t.Errorf("combinePromptAndStdin(%q, %q) = %q; want %q", tt.prompt, tt.stdin, got, tt.expected)
			}
		})
	}
}

func TestReadPipedStdin(t *testing.T) {
	// 1. nil stdin
	got, err := readPipedStdin(nil)
	if err != nil || got != "" {
		t.Fatalf("expected empty string and nil error for nil stdin, got %q, %v", got, err)
	}

	// 2. pipe with content
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	testContent := "hello from pipe"
	go func() {
		w.WriteString(testContent)
		w.Close()
	}()

	got, err = readPipedStdin(r)
	if err != nil {
		t.Fatalf("unexpected error reading pipe: %v", err)
	}
	if got != testContent {
		t.Errorf("expected %q, got %q", testContent, got)
	}

	// 3. empty pipe
	r2, w2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r2.Close()
	w2.Close()

	got, err = readPipedStdin(r2)
	if err != nil {
		t.Fatalf("unexpected error reading empty pipe: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string from empty pipe, got %q", got)
	}

	// 4. oversized input exceeding limit
	r3, w3, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r3.Close()
	go func() {
		w3.WriteString("123456789012345")
		w3.Close()
	}()

	_, err = readPipedStdinWithLimit(r3, 10)
	if err == nil {
		t.Fatal("expected size limit error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds maximum allowed size") {
		t.Errorf("expected size limit error message, got: %v", err)
	}
}

func createTestWorkspace(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	cfg := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test-key"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	return tmpDir
}

func TestRunWithStreamsPipedStdin(t *testing.T) {
	var capturedPrompt string
	origRunOneShot := runOneShot
	runOneShot = func(client *core.Core, prompt string, flags CLIFlags, stdout, stderr io.Writer) error {
		capturedPrompt = prompt
		return nil
	}
	defer func() { runOneShot = origRunOneShot }()

	tmpDir := createTestWorkspace(t)

	// 1. Piped stdin with prompt
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		w.WriteString("diff --git a b\n+new line")
		w.Close()
	}()

	var stdout, stderr strings.Builder
	err = RunWithStreams([]string{"-C", tmpDir, "review these changes"}, r, &stdout, &stderr)
	r.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "review these changes\n\n---\ndiff --git a b\n+new line"
	if capturedPrompt != expected {
		t.Errorf("captured prompt mismatch:\nwant: %q\ngot:  %q", expected, capturedPrompt)
	}

	// 2. Piped stdin without prompt
	r2, w2, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		w2.WriteString("echo 'pure stdin prompt'")
		w2.Close()
	}()

	capturedPrompt = ""
	err = RunWithStreams([]string{"-C", tmpDir}, r2, &stdout, &stderr)
	r2.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedPrompt != "echo 'pure stdin prompt'" {
		t.Errorf("expected 'echo \\'pure stdin prompt\\'', got %q", capturedPrompt)
	}

	// 3. Piped stdin with '-'
	r3, w3, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		w3.WriteString("prompt via dash")
		w3.Close()
	}()

	capturedPrompt = ""
	err = RunWithStreams([]string{"-C", tmpDir, "-"}, r3, &stdout, &stderr)
	r3.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedPrompt != "prompt via dash" {
		t.Errorf("expected 'prompt via dash', got %q", capturedPrompt)
	}

	// 4. Empty stdin without prompt returns helpful error
	r4, w4, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	w4.Close()

	err = RunWithStreams([]string{"-C", tmpDir}, r4, &stdout, &stderr)
	r4.Close()
	if err == nil {
		t.Fatal("expected error for empty stdin with no prompt, got nil")
	}
	if !strings.Contains(err.Error(), "no prompt provided and standard input is empty") {
		t.Errorf("expected helpful empty error, got: %v", err)
	}
}

func TestConfigExampleIncludesAllTeamLimits(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config.example.json"))
	if err != nil {
		t.Fatalf("read config.example.json: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal config.example.json: %v", err)
	}

	runtimeConfig, ok := raw["runtime"].(map[string]any)
	if !ok {
		t.Fatal("runtime section missing from config.example.json")
	}
	if _, ok := runtimeConfig["maxTeamChildrenPerAgent"]; !ok {
		t.Error("runtime.maxTeamChildrenPerAgent missing from config.example.json")
	}
	if _, ok := runtimeConfig["maxTeamDepth"]; !ok {
		t.Error("runtime.maxTeamDepth missing from config.example.json")
	}

	var cfg struct {
		Runtime struct {
			MaxParallelAgents       int `json:"maxParallelAgents"`
			MaxTeamChildrenPerAgent int `json:"maxTeamChildrenPerAgent"`
			MaxTeamDepth            int `json:"maxTeamDepth"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config.example.json into struct: %v", err)
	}

	if cfg.Runtime.MaxParallelAgents != 4 {
		t.Errorf("expected runtime.maxParallelAgents=4, got %d", cfg.Runtime.MaxParallelAgents)
	}
	if cfg.Runtime.MaxTeamChildrenPerAgent != 4 {
		t.Errorf("expected runtime.maxTeamChildrenPerAgent=4, got %d", cfg.Runtime.MaxTeamChildrenPerAgent)
	}
	if cfg.Runtime.MaxTeamDepth != 2 {
		t.Errorf("expected runtime.maxTeamDepth=2, got %d", cfg.Runtime.MaxTeamDepth)
	}

	// Also verify that with placeholder keys populated, core.New parses and validates config.example.json without schema or configuration errors
	tmpData := strings.ReplaceAll(string(data), `"apiKey": ""`, `"apiKey": "test-key"`)
	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(tmpData), 0600); err != nil {
		t.Fatalf("write temp config.json: %v", err)
	}
	coreClient, err := newCore(tmpDir)
	if err != nil {
		t.Fatalf("newCore failed with config.example.json (with keys populated): %v", err)
	}
	defer coreClient.Close()
}
