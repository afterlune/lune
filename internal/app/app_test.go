package app

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/afterlune/lune/internal/settings"
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

func TestA2AModeReachesListenerWithoutTTYOrPrompt(t *testing.T) {
	writeUserProjectConfigForTest(t, `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{}}`)
	// An invalid numeric port exercises server-mode selection and Core setup
	// without binding a socket or leaving a server running after the test.
	err := RunWithStreams([]string{"-C", t.TempDir(), "--a2a-listen", "127.0.0.1:99999"}, nil, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "invalid port") {
		t.Fatalf("expected the A2A listener error, got %v", err)
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
	if !strings.Contains(stdout.String(), "lune v") {
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

func newModelProfileTestCore(t *testing.T) *core.Core {
	t.Helper()
	workspace := t.TempDir()
	config := `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test"}},"profiles":{"primary":{"provider":"host","model":"primary-model"},"fast":{"provider":"host","model":"fast-model"}},"defaultProfile":"primary"},"runtime":{}}`
	writeUserProjectConfigForTest(t, config)
	client, err := newCore(workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(context.Background()); err != nil {
			t.Errorf("close Core: %v", err)
		}
	})
	return client
}

func writeUserProjectConfigForTest(t *testing.T, config string) {
	t.Helper()
	userConfigRoot := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", userConfigRoot)
	case "darwin":
		t.Setenv("HOME", userConfigRoot)
	default:
		t.Setenv("XDG_CONFIG_HOME", userConfigRoot)
	}
	configDirectory, err := settings.UserSettingsDirectory()
	if err != nil {
		t.Fatalf("get test user config directory: %v", err)
	}
	if err := os.MkdirAll(configDirectory, 0700); err != nil {
		t.Fatalf("create test user config directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "config.json"), []byte(config), 0600); err != nil {
		t.Fatalf("write user config.json: %v", err)
	}
}

func TestConfiguredEngramConnectionFailureDoesNotBlockCore(t *testing.T) {
	writeUserProjectConfigForTest(t, `{"models":{"providers":{"host":{"protocol":"responses","baseURL":"https://example.invalid","apiKey":"test"}},"profiles":{"primary":{"provider":"host","model":"test-model"}},"defaultProfile":"primary"},"runtime":{},"mcpServers":{"engram":{"transport":"stdio","command":"/definitely-missing/lune-test-engram","args":["mcp","--tools=agent"]}}}`)

	var diagnostics strings.Builder
	client, err := newCoreWithDiagnostics(t.TempDir(), &diagnostics)
	if err != nil {
		t.Fatalf("newCoreWithDiagnostics() error = %v", err)
	}
	defer func() {
		if err := client.Close(context.Background()); err != nil {
			t.Errorf("close Core: %v", err)
		}
	}()

	if !strings.Contains(diagnostics.String(), "continuing without automatic memory") {
		t.Fatalf("diagnostics = %q, want non-fatal Engram warning", diagnostics.String())
	}
}

func TestRunOneShotRejectsUnknownModelProfile(t *testing.T) {
	client := newModelProfileTestCore(t)
	err := RunOneShot(client, "do not send this prompt", CLIFlags{ModelProfile: "missing"}, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), `unknown model profile "missing"`) {
		t.Fatalf("RunOneShot error = %v, want unknown model profile error", err)
	}
}

func TestOneShotModelProfilePreservesSessionSettings(t *testing.T) {
	client := newModelProfileTestCore(t)
	ctx := context.Background()
	session, err := client.CreateSession(ctx, json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	window := int64(1_000_000)
	if err := client.SetSessionSettings(ctx, session.ID, core.SessionSettings{
		ModelProfile: "primary", ReasoningEffort: "high", ContextWindow: &window,
	}); err != nil {
		t.Fatal(err)
	}
	if err := applyOneShotModelProfile(ctx, client, session.ID, "fast"); err != nil {
		t.Fatal(err)
	}
	saved, err := client.GetSession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Settings.ModelProfile != "fast" || saved.Settings.ReasoningEffort != "high" || saved.Settings.ContextWindow == nil || *saved.Settings.ContextWindow != window {
		t.Fatalf("model profile update lost session settings: %+v", saved.Settings)
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
	writeUserProjectConfigForTest(t, cfg)
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
	// Keep this parser test isolated from any user-installed Engram binary.
	tmpData = strings.ReplaceAll(tmpData, `"command": "engram"`, `"command": "/definitely-missing/lune-test-engram"`)
	tmpDir := t.TempDir()
	writeUserProjectConfigForTest(t, tmpData)
	coreClient, err := newCoreWithDiagnostics(tmpDir, io.Discard)
	if err != nil {
		t.Fatalf("newCore failed with config.example.json (with keys populated): %v", err)
	}
	defer coreClient.Close(context.Background())
}
