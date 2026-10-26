package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/afterlune/lune/internal/a2a"
	"github.com/afterlune/lune/internal/cli"
	"github.com/afterlune/lune/internal/settings"
	"github.com/afterlune/lune/internal/toolchains"
	"github.com/afterlune/lune/internal/tui"
	core "github.com/afterlune/luneCore"
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
	if !isOneShot && flags.A2AListen == "" {
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

	client, err := newCoreWithDiagnostics(workspace, stderr, flags.NoSandbox)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, client.Close()) }()

	if flags.A2AListen != "" {
		srv := a2a.NewServer(a2a.NewCoreBackend(client), os.Getenv("LUNE_A2A_TOKEN"))
		fmt.Fprintf(stderr, "Lune A2A 1.0 server listening on %s\n", flags.A2AListen)
		return http.ListenAndServe(flags.A2AListen, srv)
	}
	if flags.PermissionMode == "" {
		if err := client.SetPermissionMode(core.PermissionFullAccess); err != nil {
			return fmt.Errorf("set default terminal permission mode: %w", err)
		}
	}

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

func isSandboxDisabledEnv() bool {
	v := strings.ToLower(os.Getenv("LUNE_SANDBOX"))
	if v == "off" || v == "0" || v == "false" || v == "no" {
		return true
	}
	v2 := strings.ToLower(os.Getenv("LUNE_NO_SANDBOX"))
	if v2 == "1" || v2 == "true" || v2 == "yes" || v2 == "on" {
		return true
	}
	return false
}

func newCore(workspace string, noSandbox ...bool) (*core.Core, error) {
	return newCoreWithDiagnostics(workspace, os.Stderr, noSandbox...)
}

func newCoreWithDiagnostics(workspace string, diagnostics io.Writer, noSandbox ...bool) (*core.Core, error) {
	projectConfig, configPath, err := loadUserProjectConfigWithDiagnostics(diagnostics)
	if err != nil {
		return nil, err
	}
	helperPath := findSandboxHelper(workspace)
	allowLocalCommand := isSandboxDisabledEnv()
	if len(noSandbox) > 0 && noSandbox[0] {
		allowLocalCommand = true
	}
	var toolchainRoots []string
	userStore, storeErr := settings.OpenUser()
	if storeErr == nil {
		if err := userStore.TouchWorkspace(workspace); err != nil {
			slog.Warn("record recent workspace", "workspace", workspace, "error", err)
		}
		if stored, found, err := userStore.LoadWorkspaceCommandToolchainPaths(workspace); err == nil && found {
			toolchainRoots = toolchains.AvailablePaths(stored)
		} else {
			toolchainRoots = toolchains.AutoDetectRoots()
			if len(toolchainRoots) > 0 {
				_ = userStore.SaveWorkspaceCommandToolchainPaths(workspace, toolchainRoots)
			}
		}
		defer userStore.Close()
	} else {
		toolchainRoots = toolchains.AutoDetectRoots()
	}

	baseCfg := core.Config{
		Workspace:             workspace,
		Logger:                slog.Default(),
		SandboxHelperPath:     helperPath,
		AllowLocalCommand:     allowLocalCommand,
		ProjectConfig:         &projectConfig,
		CommandToolchainPaths: toolchainRoots,
	}

	client, err := core.New(baseCfg)
	if err != nil {
		return nil, fmt.Errorf("initialize Lune with user config %s: %w", configPath, err)
	}
	return client, nil
}

func loadUserProjectConfig() (core.ProjectConfig, string, error) {
	return loadUserProjectConfigWithDiagnostics(os.Stderr)
}

func loadUserProjectConfigWithDiagnostics(diagnostics io.Writer) (core.ProjectConfig, string, error) {
	configDirectory, err := settings.UserSettingsDirectory()
	if err != nil {
		return core.ProjectConfig{}, "", fmt.Errorf("find user configuration directory: %w", err)
	}
	configPath := filepath.Join(configDirectory, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return core.ProjectConfig{}, configPath, fmt.Errorf("read user config %s: %w; copy config.example.json there and edit it", configPath, err)
	}
	document, err := prepareValidatedUserConfig(configPath, data, diagnostics)
	if err != nil {
		return core.ProjectConfig{}, configPath, fmt.Errorf("prepare user config %s: %w", configPath, err)
	}
	var projectConfig core.ProjectConfig
	if err := json.Unmarshal(document, &projectConfig); err != nil {
		return core.ProjectConfig{}, configPath, fmt.Errorf("parse user config %s: %w", configPath, err)
	}
	return projectConfig, configPath, nil
}

func findSandboxHelper(workspace string) string {
	helperName := "lune-sandbox"
	if runtime.GOOS == "windows" {
		helperName += ".exe"
	}
	if executablePath, err := os.Executable(); err == nil {
		installedPath := filepath.Join(filepath.Dir(executablePath), helperName)
		if info, err := os.Stat(installedPath); err == nil && !info.IsDir() {
			return installedPath
		}
	}
	workspacePath := filepath.Join(workspace, "bin", helperName)
	if info, err := os.Stat(workspacePath); err == nil && !info.IsDir() {
		return workspacePath
	}
	return ""
}
