package app

import (
	"bytes"
	"strings"
	"testing"

	core "github.com/afterlune/luneCore"
)

func TestParseFlagsHelpAndVersion(t *testing.T) {
	flags, err := ParseFlags([]string{"--help"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !flags.Help {
		t.Errorf("expected Help to be true")
	}

	flags, err = ParseFlags([]string{"-v"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !flags.Version {
		t.Errorf("expected Version to be true")
	}
}

func TestParseFlagsA2AListen(t *testing.T) {
	for _, args := range [][]string{
		{"--a2a-listen", "127.0.0.1:18091"},
		{"--a2a-listen=127.0.0.1:18091"},
	} {
		flags, err := ParseFlags(args)
		if err != nil || flags.A2AListen != "127.0.0.1:18091" || flags.Prompt != "" {
			t.Fatalf("ParseFlags(%v) = %+v, %v", args, flags, err)
		}
	}
	if _, err := ParseFlags([]string{"--a2a-listen"}); err == nil {
		t.Fatal("missing A2A listen address was accepted")
	}
}

func TestParseFlagsWorkspace(t *testing.T) {
	flags, err := ParseFlags([]string{"-C", "test-dir"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(flags.Workspace, "test-dir") {
		t.Errorf("expected Workspace to end with test-dir, got: %s", flags.Workspace)
	}

	flags, err = ParseFlags([]string{"--workspace=my-ws"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasSuffix(flags.Workspace, "my-ws") {
		t.Errorf("expected Workspace to end with my-ws, got: %s", flags.Workspace)
	}

	_, err = ParseFlags([]string{"-C"})
	if err == nil {
		t.Errorf("expected error for missing workspace argument")
	}
}

func TestParseFlagsModelAndPermission(t *testing.T) {
	flags, err := ParseFlags([]string{"-m", "gemini-2.5-pro", "-p", "full-access"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags.ModelProfile != "gemini-2.5-pro" {
		t.Errorf("expected model profile 'gemini-2.5-pro', got: %s", flags.ModelProfile)
	}
	if flags.PermissionMode != core.PermissionFullAccess {
		t.Errorf("expected permission mode 'full-access', got: %s", flags.PermissionMode)
	}

	// Invalid permission mode
	_, err = ParseFlags([]string{"-p", "super-admin"})
	if err == nil {
		t.Errorf("expected error for invalid permission mode")
	}
}

func TestParseFlagsSessionAndPlan(t *testing.T) {
	flags, err := ParseFlags([]string{"-s", "session-xyz", "--plan", "--no-sandbox"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags.SessionID != "session-xyz" {
		t.Errorf("expected session 'session-xyz', got: %s", flags.SessionID)
	}
	if !flags.PlanMode {
		t.Errorf("expected PlanMode to be true")
	}
	if !flags.NoSandbox {
		t.Errorf("expected NoSandbox to be true")
	}
}

func TestParseFlagsTUIAndCLIConflict(t *testing.T) {
	flags, err := ParseFlags([]string{"--tui"})
	if err != nil || !flags.ForceTUI {
		t.Errorf("expected ForceTUI true")
	}

	flags, err = ParseFlags([]string{"--cli"})
	if err != nil || !flags.ForceCLI {
		t.Errorf("expected ForceCLI true")
	}

	_, err = ParseFlags([]string{"--tui", "--cli"})
	if err == nil {
		t.Errorf("expected conflict error for --tui and --cli")
	}
}

func TestParseFlagsPromptAndPassThrough(t *testing.T) {
	// Simple prompt
	flags, err := ParseFlags([]string{"check", "if", "tests", "pass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags.Prompt != "check if tests pass" {
		t.Errorf("expected prompt 'check if tests pass', got: %q", flags.Prompt)
	}

	// Flags with prompt
	flags, err = ParseFlags([]string{"-m", "fast", "run", "benchmark"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags.ModelProfile != "fast" || flags.Prompt != "run benchmark" {
		t.Errorf("expected model 'fast' and prompt 'run benchmark', got: %s, %q", flags.ModelProfile, flags.Prompt)
	}

	// Double-dash passthrough
	flags, err = ParseFlags([]string{"--", "-not-a-flag", "--also-text"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags.Prompt != "-not-a-flag --also-text" {
		t.Errorf("expected passthrough prompt, got: %q", flags.Prompt)
	}

	// Explicit --prompt and --prompt=
	flags, err = ParseFlags([]string{"--prompt", "optimize performance"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags.Prompt != "optimize performance" {
		t.Errorf("expected prompt 'optimize performance', got: %q", flags.Prompt)
	}

	flags, err = ParseFlags([]string{"--prompt=fix memory leak"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if flags.Prompt != "fix memory leak" {
		t.Errorf("expected prompt 'fix memory leak', got: %q", flags.Prompt)
	}

	_, err = ParseFlags([]string{"--prompt"})
	if err == nil {
		t.Errorf("expected error for missing --prompt argument")
	}
}

func TestPrintHelpAndVersion(t *testing.T) {
	var bufHelp bytes.Buffer
	PrintHelp(&bufHelp)
	helpStr := bufHelp.String()
	if !strings.Contains(helpStr, "Lune - Autonomous AI coding agent") || !strings.Contains(helpStr, "--workspace") {
		t.Errorf("expected help output, got: %s", helpStr)
	}
	if !strings.Contains(helpStr, "Pipeline / Stdin Stream") || !strings.Contains(helpStr, "--prompt") {
		t.Errorf("expected pipeline section in help output, got: %s", helpStr)
	}

	var bufVer bytes.Buffer
	PrintVersion(&bufVer)
	verStr := bufVer.String()
	if !strings.Contains(verStr, "lune v0.1.0") {
		t.Errorf("expected version output, got: %s", verStr)
	}
}
