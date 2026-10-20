package app

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"strings"

	core "github.com/afterlune/luneCore"
)

// AppVersion represents the current application release version.
const AppVersion = "v0.1.0"

// CLIFlags contains parsed command-line flags and prompt parameters.
type CLIFlags struct {
	Workspace      string
	ModelProfile   string
	PermissionMode core.PermissionMode
	SessionID      string
	PlanMode       bool
	NoSandbox      bool
	ForceTUI       bool
	ForceCLI       bool
	Help           bool
	Version        bool
	Prompt         string
}

// ParseFlags parses command line arguments (e.g. os.Args[1:]) into CLIFlags.
func ParseFlags(args []string) (CLIFlags, error) {
	var flags CLIFlags
	var promptParts []string
	passThrough := false

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if passThrough {
			promptParts = append(promptParts, arg)
			continue
		}

		if arg == "--" {
			passThrough = true
			continue
		}

		if arg == "-h" || arg == "--help" {
			flags.Help = true
			continue
		}

		if arg == "-v" || arg == "--version" {
			flags.Version = true
			continue
		}

		if arg == "--tui" {
			flags.ForceTUI = true
			continue
		}

		if arg == "--cli" {
			flags.ForceCLI = true
			continue
		}

		if arg == "--plan" {
			flags.PlanMode = true
			continue
		}

		if arg == "--no-sandbox" {
			flags.NoSandbox = true
			continue
		}

		// Workspace flag (-C / --workspace)
		if arg == "-C" || arg == "--workspace" {
			if i+1 >= len(args) {
				return flags, fmt.Errorf("flag %s requires a directory argument", arg)
			}
			i++
			flags.Workspace = args[i]
			continue
		}
		if strings.HasPrefix(arg, "-C=") || strings.HasPrefix(arg, "--workspace=") {
			parts := strings.SplitN(arg, "=", 2)
			flags.Workspace = parts[1]
			continue
		}

		// Model flag (-m / --model)
		if arg == "-m" || arg == "--model" {
			if i+1 >= len(args) {
				return flags, fmt.Errorf("flag %s requires a model profile argument", arg)
			}
			i++
			flags.ModelProfile = args[i]
			continue
		}
		if strings.HasPrefix(arg, "-m=") || strings.HasPrefix(arg, "--model=") {
			parts := strings.SplitN(arg, "=", 2)
			flags.ModelProfile = parts[1]
			continue
		}

		// Permission flag (-p / --permission)
		if arg == "-p" || arg == "--permission" {
			if i+1 >= len(args) {
				return flags, fmt.Errorf("flag %s requires a permission mode argument", arg)
			}
			i++
			flags.PermissionMode = core.PermissionMode(strings.ToLower(args[i]))
			continue
		}
		if strings.HasPrefix(arg, "-p=") || strings.HasPrefix(arg, "--permission=") {
			parts := strings.SplitN(arg, "=", 2)
			flags.PermissionMode = core.PermissionMode(strings.ToLower(parts[1]))
			continue
		}

		// Session flag (-s / --session)
		if arg == "-s" || arg == "--session" {
			if i+1 >= len(args) {
				return flags, fmt.Errorf("flag %s requires a session ID argument", arg)
			}
			i++
			flags.SessionID = args[i]
			continue
		}
		if strings.HasPrefix(arg, "-s=") || strings.HasPrefix(arg, "--session=") {
			parts := strings.SplitN(arg, "=", 2)
			flags.SessionID = parts[1]
			continue
		}

		// Prompt flag (--prompt)
		if arg == "--prompt" {
			if i+1 >= len(args) {
				return flags, fmt.Errorf("flag %s requires a prompt argument", arg)
			}
			i++
			promptParts = append(promptParts, args[i])
			continue
		}
		if strings.HasPrefix(arg, "--prompt=") {
			parts := strings.SplitN(arg, "=", 2)
			promptParts = append(promptParts, parts[1])
			continue
		}

		// Unknown flag
		if strings.HasPrefix(arg, "-") && arg != "-" {
			return flags, fmt.Errorf("unknown flag: %s (run 'lune --help' for usage)", arg)
		}

		// Non-flag arguments are prompt tokens
		promptParts = append(promptParts, arg)
	}

	// Validate mutual exclusivity
	if flags.ForceTUI && flags.ForceCLI {
		return flags, errors.New("--tui and --cli cannot be used together")
	}

	// Validate permission mode if specified
	if flags.PermissionMode != "" {
		switch flags.PermissionMode {
		case core.PermissionReadOnly, core.PermissionWorkspace, core.PermissionAsk, core.PermissionFullAccess:
			// valid
		default:
			return flags, fmt.Errorf("invalid permission mode %q (must be 'read-only', 'workspace', 'ask', or 'full-access')", flags.PermissionMode)
		}
	}

	// Normalize workspace if provided
	if flags.Workspace != "" {
		absPath, err := filepath.Abs(flags.Workspace)
		if err == nil {
			flags.Workspace = absPath
		}
	}

	if len(promptParts) > 0 {
		flags.Prompt = strings.TrimSpace(strings.Join(promptParts, " "))
	}

	return flags, nil
}

// PrintHelp outputs the full command-line usage guide to w.
func PrintHelp(w io.Writer) {
	fmt.Fprintf(w, `LuneAgent - Autonomous AI coding agent (%s)

Usage:
  lune [flags] [prompt]

Flags:
  -C, --workspace <dir>      Set working directory (default: current directory)
  -m, --model <profile>      Set model profile for session
  -p, --permission <mode>    Set permission mode: read-only, workspace, ask, full-access
  -s, --session <id|prefix>  Resume or open an existing session
      --prompt <text>        Instruction prompt (can also be passed as positional arguments)
      --plan                 Start in Plan mode (read-only exploration)
      --no-sandbox           Disable OS sandbox and run commands directly
      --tui                  Force full-screen interactive TUI mode
      --cli                  Force classic streaming CLI mode
  -v, --version              Print version information and exit
  -h, --help                 Print this help message and exit

Execution Modes:
  Interactive Mode:
    lune                     Start full-screen TUI (or CLI when piped)
    lune -C D:\MyProject     Start in specific workspace directory
    lune -s sess-123         Resume existing session

  One-Shot / Script Mode:
    lune "prompt..."         Run task non-interactively, stream output, and exit
    lune -m fast "diff..."   Run single-turn task with specified model profile

  Pipeline / Stdin Stream:
    cat file.go | lune "explain this code"   Read stdin as context, append to prompt, run one-shot
    git diff | lune "review changes"         Read diff from stdin, stream review to stdout
    cat prompt.txt | lune                    Read prompt from stdin, execute and stream output
    lune "prompt..." > output.md             Stream response to redirected file (status to stderr)

`, AppVersion)
}

// PrintVersion outputs the application version and architecture to w.
func PrintVersion(w io.Writer) {
	fmt.Fprintf(w, "LuneAgent %s (%s/%s)\n", AppVersion, runtime.GOOS, runtime.GOARCH)
}
