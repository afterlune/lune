package app

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/afterlune/lune/internal/cli"
	"github.com/afterlune/lune/internal/settings"
	"github.com/afterlune/lune/internal/tui"
	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

// maxPipedStdinBytes limits the maximum data read from standard input pipe (10 MB).
const maxPipedStdinBytes = 10 * 1024 * 1024

var runOneShot = RunOneShot

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

	// Read standard input if piped (non-terminal), unless interactive --cli or --tui
	// was explicitly requested without a positional prompt.
	var pipedInput string
	if (!flags.ForceCLI && !flags.ForceTUI) || flags.Prompt != "" {
		var err error
		pipedInput, err = readPipedStdin(stdin)
		if err != nil {
			return err
		}
	}

	if flags.Prompt == "-" {
		flags.Prompt = ""
	}

	effectivePrompt := combinePromptAndStdin(flags.Prompt, pipedInput)

	// Decide execution mode:
	// 1. One-Shot mode (if prompt or piped input is provided)
	// 2. Interactive TUI or CLI mode
	isOneShot := effectivePrompt != ""

	// Determine if interactive TUI should be used
	interactive := false
	if !isOneShot {
		if flags.ForceTUI {
			interactive = true
		} else if flags.ForceCLI {
			interactive = false
		} else {
			if stdin == nil || !isTerminal(stdin) {
				return errors.New("no prompt provided and standard input is empty (run 'lune --help' for usage)")
			}
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
		return runOneShot(client, effectivePrompt, flags, stdout, stderr)
	}

	if interactive {
		return tui.RunWithOptions(client, tui.LaunchOptions{
			SessionID:      flags.SessionID,
			ModelProfile:   flags.ModelProfile,
			PermissionMode: flags.PermissionMode,
			PlanMode:       flags.PlanMode,
			Input:          stdin,
			Output:         stdout,
		})
	}

	return cli.RunWithOptions(client, cli.LaunchOptions{
		SessionID:      flags.SessionID,
		ModelProfile:   flags.ModelProfile,
		PermissionMode: flags.PermissionMode,
		PlanMode:       flags.PlanMode,
		Stdin:          stdin,
		Stdout:         stdout,
		Stderr:         stderr,
	})
}

// readPipedStdin reads standard input if it is connected to a pipe or file redirection.
// If stdin is nil or is connected to a terminal (character device), it returns empty string without reading.
func readPipedStdin(stdin *os.File) (string, error) {
	return readPipedStdinWithLimit(stdin, maxPipedStdinBytes)
}

func readPipedStdinWithLimit(stdin *os.File, limit int64) (string, error) {
	if stdin == nil || isTerminal(stdin) {
		return "", nil
	}

	reader := io.LimitReader(stdin, limit+1)
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", fmt.Errorf("read standard input: %w", err)
	}

	if int64(len(data)) > limit {
		return "", fmt.Errorf("piped standard input exceeds maximum allowed size of %d MB", limit/(1024*1024))
	}

	return string(data), nil
}

// combinePromptAndStdin combines an optional command line prompt with piped standard input.
func combinePromptAndStdin(prompt, stdinText string) string {
	prompt = strings.TrimSpace(prompt)
	stdinText = strings.TrimSpace(stdinText)

	if prompt == "" {
		return stdinText
	}
	if stdinText == "" {
		return prompt
	}

	return prompt + "\n\n---\n" + stdinText
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
	baseCfg := core.Config{Workspace: workspace, Logger: slog.Default(), SandboxHelperPath: helperPath}

	var storeModelCfg *publicmodel.Config
	var storeProjectCfg *core.ProjectConfig
	if store, err := settings.OpenUser(); err == nil {
		defer store.Close()
		_ = store.TouchWorkspace(workspace)
		if wsCfg, ok, err := store.LoadWorkspaceConfig(workspace); err == nil && ok {
			storeProjectCfg = &wsCfg
		}
		if modelCfg, err := store.LoadModelConfig(); err == nil && (len(modelCfg.Providers) > 0 || len(modelCfg.Models) > 0) {
			storeModelCfg = &modelCfg
		}
	}

	configPath := filepath.Join(workspace, "config.json")
	if _, err := os.Stat(configPath); err == nil {
		// Prefer explicit workspace config.json when present; fall back to overlaying user settings if needed.
		client, err := core.New(baseCfg)
		if err == nil || storeModelCfg == nil {
			return client, err
		}
	}

	if storeProjectCfg != nil {
		baseCfg.ProjectConfig = storeProjectCfg
	}
	if storeModelCfg != nil {
		baseCfg.ModelConfig = storeModelCfg
	}
	return core.New(baseCfg)
}
