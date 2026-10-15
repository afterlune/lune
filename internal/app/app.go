package app

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/afterlune/lune/internal/cli"
	"github.com/afterlune/lune/internal/tui"
	core "github.com/afterlune/luneCore"
)

func Run() (runErr error) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	workspace, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get workspace: %w", err)
	}

	interactive := useTUI(os.Stdin, os.Stdout)
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

	if interactive {
		return tui.Run(client)
	}
	return cli.Run(client)
}

func useTUI(stdin, stdout *os.File) bool {
	return isTerminal(stdin) && isTerminal(stdout)
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
