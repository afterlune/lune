package cli

import (
	"context"
	"fmt"
	"github.com/afterlune/lune/internal/workspacereview"
	core "github.com/afterlune/luneCore"
	"io"
	"os/exec"
	"strings"
)

func handleCoreCLIDiff(workspace, line string, stdout, stderr io.Writer) {
	pathArgs := []string{}
	if path := strings.TrimSpace(strings.TrimPrefix(line, "/diff")); path != "" {
		pathArgs = append(pathArgs, "--")
		pathArgs = append(pathArgs, path)
	}
	var sections []string
	for _, diff := range []struct {
		name string
		args []string
	}{{"Unstaged changes", append([]string{"diff", "--no-ext-diff"}, pathArgs...)}, {"Staged changes", append([]string{"diff", "--cached", "--no-ext-diff"}, pathArgs...)}} {
		cmd := exec.CommandContext(context.Background(), "git", diff.args...)
		cmd.Dir = workspace
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(stderr, "error: git %s: %v\n", diff.args[0], err)
			return
		}
		if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
			sections = append(sections, "### "+diff.name+"\n"+trimmed)
		}
	}
	if len(sections) == 0 {
		fmt.Fprintln(stdout, "No staged or unstaged changes in workspace.")
		return
	}
	fmt.Fprintln(stdout, strings.Join(sections, "\n\n"))
}

func handleCoreCLIReview(runtime cliRuntime, workspace, line string, watchKeys keyEventWatcher, stdout, stderr io.Writer) error {
	path := strings.TrimSpace(strings.TrimPrefix(line, "/review"))
	materials, changed, err := workspacereview.CollectMaterials(workspace, path)
	if err != nil {
		fmt.Fprintf(stderr, "error: prepare workspace review: %v\n", err)
		return nil
	}
	if !changed {
		fmt.Fprintln(stdout, "No text changes to review. Git-ignored and binary files are excluded.")
		return nil
	}
	input, err := core.ParseTaskInput(workspacereview.Prompt(materials, path))
	if err != nil {
		fmt.Fprintf(stderr, "error: parse review request: %v\n", err)
		return nil
	}
	result, callErr, interrupted := respondCoreWithKeys(runtime, workspace, core.TurnRequest{Input: input, PermissionMode: core.PermissionReadOnly}, false, watchKeys, stderr)
	return printCoreTaskResult(stdout, stderr, result.Response, callErr, interrupted)
}

func handleCoreCLIMCP(client cliCore, line string, stdout, stderr io.Writer) {
	fields := strings.Fields(line)
	subcommand := "list"
	if len(fields) > 1 {
		subcommand = strings.ToLower(fields[1])
	}
	ctx := context.Background()
	switch subcommand {
	case "list":
		servers := client.MCPServers()
		if len(servers) == 0 {
			fmt.Fprintln(stdout, "No MCP servers configured.")
			return
		}
		fmt.Fprintln(stdout, "MCP Servers:")
		for _, s := range servers {
			status := "Disabled"
			if s.Enabled {
				status = "Enabled"
			}
			conn := "Disconnected"
			if s.Connected {
				conn = "Connected"
			}
			endpoint := s.Endpoint
			if endpoint == "" {
				endpoint = s.Command
			}
			errInfo := ""
			if s.LastError != "" {
				errInfo = fmt.Sprintf(" (Error: %s)", s.LastError)
			}
			fmt.Fprintf(stdout, "  - %-16s [%s, %s] %s (%d tools)%s\n", cleanCLIInlineText(s.Name), status, conn, cleanCLIInlineText(endpoint), s.ToolCount, cleanCLIInlineText(errInfo))
		}
	case "enable":
		if len(fields) < 3 {
			fmt.Fprintln(stderr, "usage: /mcp enable <server-name>")
			return
		}
		name := fields[2]
		if err := client.EnableMCPServer(name); err != nil {
			fmt.Fprintf(stderr, "error: enable MCP server %q: %s\n", cleanCLIInlineText(name), cleanCLIInlineText(err.Error()))
			return
		}
		fmt.Fprintf(stdout, "MCP server %q enabled.\n", cleanCLIInlineText(name))
	case "disable":
		if len(fields) < 3 {
			fmt.Fprintln(stderr, "usage: /mcp disable <server-name>")
			return
		}
		name := fields[2]
		if err := client.DisableMCPServer(name); err != nil {
			fmt.Fprintf(stderr, "error: disable MCP server %q: %s\n", cleanCLIInlineText(name), cleanCLIInlineText(err.Error()))
			return
		}
		fmt.Fprintf(stdout, "MCP server %q disabled.\n", cleanCLIInlineText(name))
	case "auth":
		if len(fields) < 3 {
			fmt.Fprintln(stderr, "usage: /mcp auth <server-name>")
			return
		}
		name := fields[2]
		if err := client.AuthenticateMCPServer(ctx, name); err != nil {
			fmt.Fprintf(stderr, "error: authenticate MCP server %q: %s\n", cleanCLIInlineText(name), cleanCLIInlineText(err.Error()))
			return
		}
		fmt.Fprintf(stdout, "MCP server %q authentication completed.\n", cleanCLIInlineText(name))
	default:
		fmt.Fprintf(stderr, "Unknown MCP subcommand %q. Usage: /mcp [list|enable|disable|auth]\n", cleanCLIInlineText(subcommand))
	}
}
