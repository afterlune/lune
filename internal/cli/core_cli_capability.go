package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/afterlune/lune/internal/terminaltext"
	"github.com/afterlune/luneCore/capability"
)

type generatedToolManager interface {
	ListGeneratedTools(context.Context, int, bool) ([]capability.GeneratedTool, error)
	SetGeneratedToolEnabled(context.Context, string, bool) error
	DeleteGeneratedTool(context.Context, string) error
}

func handleCoreCLICapability(client cliCore, line string, stdout, stderr io.Writer) {
	manager, ok := client.(generatedToolManager)
	if !ok {
		fmt.Fprintln(stderr, "error: generated tool management is unavailable")
		return
	}
	parts := strings.Fields(line)
	command := "list"
	if len(parts) > 1 {
		command = strings.ToLower(parts[1])
	}
	ctx := context.Background()
	items, err := manager.ListGeneratedTools(ctx, 500, true)
	if err != nil {
		fmt.Fprintf(stderr, "error: list generated tools: %v\n", err)
		return
	}
	switch command {
	case "list":
		query := strings.ToLower(strings.TrimSpace(strings.Join(parts[2:], " ")))
		var filtered []capability.GeneratedTool
		for _, item := range items {
			if query == "" || strings.Contains(strings.ToLower(item.Name+" "+item.Description+" "+string(item.Purpose)+" "+strings.Join(item.Tags, " ")), query) {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) == 0 {
			fmt.Fprintln(stdout, "No generated tools found.")
			return
		}
		sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Name < filtered[j].Name })
		fmt.Fprintf(stdout, "Generated Tools (%d):\n", len(filtered))
		for _, item := range filtered {
			fmt.Fprintf(stdout, "  [%s] %-8s %-20s %s\n", shortGeneratedToolID(item.ID), generatedToolEnabledLabel(item.Enabled), item.Purpose, terminaltext.Sanitize(item.Name))
			fmt.Fprintf(stdout, "      %s\n      tools: %s\n", terminaltext.Sanitize(item.Description), terminaltext.Sanitize(strings.Join(item.Dependencies, ", ")))
		}
	case "enable", "disable", "delete":
		if len(parts) < 3 {
			fmt.Fprintf(stderr, "Usage: /capability %s <id|prefix>\n", command)
			return
		}
		item, err := resolveGeneratedTool(items, parts[2])
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return
		}
		switch command {
		case "enable", "disable":
			enabled := command == "enable"
			if err := manager.SetGeneratedToolEnabled(ctx, item.ID, enabled); err != nil {
				fmt.Fprintf(stderr, "error: update generated tool: %v\n", err)
				return
			}
			fmt.Fprintf(stdout, "Generated tool [%s] %s: %s\n", shortGeneratedToolID(item.ID), generatedToolEnabledLabel(enabled), terminaltext.Sanitize(item.Name))
		case "delete":
			if err := manager.DeleteGeneratedTool(ctx, item.ID); err != nil {
				fmt.Fprintf(stderr, "error: delete generated tool: %v\n", err)
				return
			}
			fmt.Fprintf(stdout, "Deleted generated tool [%s]: %s\n", shortGeneratedToolID(item.ID), terminaltext.Sanitize(item.Name))
		}
	case "help":
		writeGeneratedToolHelp(stdout)
	default:
		fmt.Fprintf(stderr, "Unknown generated tool command %q. Use /capability help.\n", command)
	}
}

func resolveGeneratedTool(items []capability.GeneratedTool, target string) (capability.GeneratedTool, error) {
	var matches []capability.GeneratedTool
	for _, item := range items {
		if strings.HasPrefix(strings.ToLower(item.ID), strings.ToLower(strings.TrimSpace(target))) {
			matches = append(matches, item)
		}
	}
	if len(matches) == 0 {
		return capability.GeneratedTool{}, fmt.Errorf("generated tool %q was not found", target)
	}
	if len(matches) > 1 {
		return capability.GeneratedTool{}, fmt.Errorf("generated tool ID prefix %q is ambiguous (%d matches)", target, len(matches))
	}
	return matches[0], nil
}

func shortGeneratedToolID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func generatedToolEnabledLabel(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func writeGeneratedToolHelp(out io.Writer) {
	fmt.Fprintln(out, "Generated Tool Commands:")
	fmt.Fprintln(out, "  /capability list [query]  List or search saved generated tools")
	fmt.Fprintln(out, "  /capability enable <id>   Enable a generated tool")
	fmt.Fprintln(out, "  /capability disable <id>  Disable a generated tool")
	fmt.Fprintln(out, "  /capability delete <id>   Delete a generated tool")
}
