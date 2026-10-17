package app

import (
	"os"
	"strings"
	"testing"
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

