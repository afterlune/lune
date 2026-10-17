package app

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/afterlune/lune/internal/cli"
	"github.com/afterlune/lune/internal/tui"
	core "github.com/afterlune/luneCore"
)

// Run executes the application using os.Args[1:] and standard I/O streams.
func Run() error {
	return RunWithStreams(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
}

// RunWithStreams executes the application with custom arguments and streams.
func RunWithStreams(args []string, stdin *os.File, stdout, stderr io.Writer) (runErr error) {
	flags, err := ParseFlags(args)
	if err != nil {
		return err
	}

	if flags.Help {
		PrintHelp(stdout)
		return nil
	}

	if flags.Version {
		PrintVersion(stdout)
		return nil
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	workspace := flags.Workspace
	if workspace == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get workspace: %w", err)
		}
		workspace = cwd
	}

	// Decide execution mode:
	// 1. One-Shot mode (if Prompt is provided)
	// 2. Interactive TUI or CLI mode
	isOneShot := flags.Prompt != ""

	// Determine if interactive TUI should be used
	interactive := false
	if !isOneShot {
		if flags.ForceTUI {
			interactive = true
		} else if flags.ForceCLI {
			interactive = false
		} else {
			interactive = useTUI(stdin, stdout)
		}
	}

	if interactive {
		logPath := filepath.Join(workspace, ".luneagent", "agent.log")
		logFile, err := newRotatingLogWriter(logPath, tuiLogMaxBytes, tuiLogBackupCount)
		if err != nil {
			return fmt.Errorf("open TUI log: %w", err)
		}
		defer func() { runErr = errors.Join(runErr, logFile.Close()) }()

		previousLogger := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo})))
		defer slog.SetDefault(previousLogger)
	}

	client, err := newCore(workspace)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, client.Close()) }()

	if isOneShot {
		return RunOneShot(client, flags.Prompt, flags, stdout, stderr)
	}

	if interactive {
		return tui.RunWithOptions(client, tui.LaunchOptions{
			SessionID:      flags.SessionID,
			ModelProfile:   flags.ModelProfile,
			PermissionMode: flags.PermissionMode,
			PlanMode:       flags.PlanMode,
		})
	}

	return cli.RunWithOptions(client, cli.LaunchOptions{
		SessionID:      flags.SessionID,
		ModelProfile:   flags.ModelProfile,
		PermissionMode: flags.PermissionMode,
		PlanMode:       flags.PlanMode,
	})
}

func useTUI(stdin *os.File, stdout io.Writer) bool {
	stdoutFile, ok := stdout.(*os.File)
	if !ok {
		return false
	}
	return isTerminal(stdin) && isTerminal(stdoutFile)
}

func isTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func newCore(workspace string) (*core.Core, error) {
	helperName := "lune-sandbox"
	if runtime.GOOS == "windows" {
		helperName += ".exe"
	}
	helperPath := filepath.Join(workspace, "bin", helperName)
	if _, err := os.Stat(helperPath); err != nil {
		helperPath = ""
	}
	return core.New(core.Config{Workspace: workspace, Logger: slog.Default(), SandboxHelperPath: helperPath})
}
