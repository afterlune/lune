package cli

import (
	"context"
	"fmt"
	core "github.com/afterlune/luneCore"
	"github.com/afterlune/luneCore/memory"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func handleCoreCLIPermissions(client cliCore, planMode bool, line string, stdout, stderr io.Writer) {
	fields := strings.Fields(line)
	if len(fields) == 1 {
		mode := client.PermissionMode()
		fmt.Fprintf(stdout, "Permission mode: %s\nAvailable modes: read-only, workspace, ask, full-access\n", mode)
		return
	}
	if len(fields) != 2 {
		fmt.Fprintln(stderr, "usage: /permissions [read-only|workspace|ask|full-access]")
		return
	}
	if planMode {
		fmt.Fprintln(stderr, "Cannot change permissions while Plan mode is active.")
		return
	}
	mode := core.PermissionMode(fields[1])
	if err := client.SetPermissionMode(mode); err != nil {
		fmt.Fprintln(stderr, err)
		return
	}
	fmt.Fprintf(stdout, "Permission mode set to %s.\n", mode)
}

func handleCoreCLIStatus(client cliCore, sessionID, sessionTitle string, planMode bool, stdout io.Writer) {
	ctx := context.Background()
	workspace := client.Workspace()
	model := client.ModelForSession(ctx, sessionID)
	mode := client.PermissionMode()
	if sessionTitle == "" {
		if s, err := client.GetSession(ctx, sessionID); err == nil && s.Title != "" {
			sessionTitle = s.Title
		} else {
			sessionTitle = "<untitled>"
		}
	}
	fmt.Fprintf(stdout, "Session ID:  %s\n", sessionID)
	fmt.Fprintf(stdout, "Title:       %s\n", sessionTitle)
	fmt.Fprintf(stdout, "Model:       %s\n", model)
	fmt.Fprintf(stdout, "Workspace:   %s\n", workspace)
	fmt.Fprintf(stdout, "Permissions: %s (Plan mode: %v)\n", mode, planMode)
	if saved, err := client.GetSession(ctx, sessionID); err == nil {
		usage := saved.SessionUsage
		fmt.Fprintf(stdout, "Session tokens: %d input (%d cached), %d output; %d model requests\n", usage.Input, usage.Cached, usage.Output, usage.Requests)
	}
}

func handleCoreCLIModel(client cliCore, sessionID, line string, stdout, stderr io.Writer) {
	fields := strings.Fields(line)
	ctx := context.Background()
	profiles := client.ListModelProfiles()
	if len(fields) == 1 {
		current := client.ModelForSession(ctx, sessionID)
		fmt.Fprintf(stdout, "Current model: %s\n", current)
		if len(profiles) > 0 {
			fmt.Fprintln(stdout, "Available model profiles:")
			for _, p := range profiles {
				fmt.Fprintf(stdout, "  - %-16s (%s / %s)\n", p.Profile, p.Provider, p.Model)
			}
		}
		return
	}
	target := fields[1]
	valid := false
	for _, p := range profiles {
		if strings.EqualFold(p.Profile, target) || strings.EqualFold(p.Model, target) {
			target = p.Profile
			valid = true
			break
		}
	}
	if !valid {
		var names []string
		for _, p := range profiles {
			names = append(names, p.Profile)
		}
		fmt.Fprintf(stderr, "Unknown model profile %q. Available profiles: %s\n", fields[1], strings.Join(names, ", "))
		return
	}
	if err := applyCLISessionModelProfile(ctx, client, sessionID, target); err != nil {
		fmt.Fprintf(stderr, "error: set model: %v\n", err)
		return
	}
	fmt.Fprintf(stdout, "Active model set to %s.\n", client.ModelForSession(ctx, sessionID))
}

func handleCoreCLISessions(client cliCore, currentSessionID, line string, stdout, stderr io.Writer) {
	fields := strings.Fields(line)
	query := ""
	if len(fields) > 1 {
		query = strings.ToLower(strings.Join(fields[1:], " "))
	}
	ctx := context.Background()
	limit := 20
	if query != "" {
		limit = 100
	}
	sessions, err := client.ListSessions(ctx, limit, false)
	if err != nil {
		fmt.Fprintf(stderr, "error: list sessions: %v\n", err)
		return
	}
	if len(sessions) == 0 {
		fmt.Fprintln(stdout, "No sessions found.")
		return
	}
	var filtered []core.Session
	for _, s := range sessions {
		if query == "" {
			filtered = append(filtered, s)
		} else {
			if strings.Contains(strings.ToLower(s.ID), query) || strings.Contains(strings.ToLower(s.Title), query) {
				filtered = append(filtered, s)
			}
		}
	}
	if len(filtered) == 0 {
		fmt.Fprintf(stdout, "No sessions matching %q.\n", query)
		return
	}
	if query != "" {
		fmt.Fprintf(stdout, "Matching sessions (%d found for %q):\n", len(filtered), query)
	} else {
		fmt.Fprintln(stdout, "Recent sessions:")
	}
	for _, s := range filtered {
		prefix := "  "
		if s.ID == currentSessionID {
			prefix = "> "
		}
		shortID := s.ID
		if len(shortID) > 12 {
			shortID = shortID[:12]
		}
		title := s.Title
		if title == "" {
			title = "<untitled>"
		}
		timeStr := s.UpdatedAt.Format("2006-01-02 15:04:05")
		fmt.Fprintf(stdout, "%s%-14s %-25s %s\n", prefix, shortID, title, timeStr)
	}
	fmt.Fprintln(stdout, "Use /resume <session-id|prefix> to restore a session.")
}

func handleCoreCLIContext(client cliCore, sessionID, sessionTitle string, planMode bool, stdout io.Writer) {
	ctx := context.Background()
	workspace := client.Workspace()
	model := client.ModelForSession(ctx, sessionID)
	mode := client.PermissionMode()
	if sessionTitle == "" {
		if s, err := client.GetSession(ctx, sessionID); err == nil && s.Title != "" {
			sessionTitle = s.Title
		} else {
			sessionTitle = "<untitled>"
		}
	}

	fmt.Fprintln(stdout, "Context Status & Diagnostics")
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Workspace & Permissions:")
	fmt.Fprintf(stdout, "  Directory:   %s\n", workspace)
	permStr := string(mode)
	if planMode {
		permStr += " (Plan mode locked)"
	}
	fmt.Fprintf(stdout, "  Permissions: %s\n", permStr)

	ruleFiles := []string{"AGENTS.md", "CLAUDE.md"}
	var foundRules []string
	if workspace != "" {
		for _, rf := range ruleFiles {
			p := filepath.Join(workspace, rf)
			if fi, err := os.Stat(p); err == nil {
				foundRules = append(foundRules, fmt.Sprintf("%s (%d bytes)", rf, fi.Size()))
			}
		}
	}
	if len(foundRules) > 0 {
		fmt.Fprintf(stdout, "  Rules:       %s\n", strings.Join(foundRules, ", "))
	} else {
		fmt.Fprintln(stdout, "  Rules:       None detected in workspace root")
	}

	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Session & Model:")
	fmt.Fprintf(stdout, "  Model:       %s\n", model)
	fmt.Fprintf(stdout, "  Session ID:  %s\n", sessionID)
	fmt.Fprintf(stdout, "  Title:       %s\n", sessionTitle)

	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Capabilities & Tools:")
	mcpServers := client.MCPServers()
	activeServers := 0
	activeTools := 0
	for _, s := range mcpServers {
		if s.Enabled {
			activeServers++
			activeTools += s.ToolCount
		}
	}
	if activeServers > 0 {
		fmt.Fprintf(stdout, "  MCP Servers: %d active (%d tools registered)\n", activeServers, activeTools)
	} else {
		fmt.Fprintln(stdout, "  MCP Servers: None active")
	}
	if planMode {
		fmt.Fprintln(stdout, "  Plan Mode:   Active (restricted to read-only tools)")
	} else {
		fmt.Fprintln(stdout, "  Plan Mode:   Inactive (all permitted tools available)")
	}

	// Session token usage and the last model request's context budget.
	if s, err := client.GetSession(ctx, sessionID); err == nil {
		contextWindow := configuredContextWindow(client, s, model)
		if contextWindow > 0 {
			fmt.Fprintf(stdout, "\nContext Window:\n  Configured:  %s tokens\n", compactCLITokens(contextWindow))
		} else {
			fmt.Fprintln(stdout, "\nContext Window:\n  Configured:  unavailable")
		}
		if last, ok := latestMainUsage(s); ok {
			input := last.Usage.InputTokens
			if input == 0 && last.EstimatedInputTokens > 0 {
				input = last.EstimatedInputTokens
				fmt.Fprintf(stdout, "  Last prompt: %s input tokens (estimated)\n", compactCLITokens(input))
			} else {
				cached, missed, hitRate := promptCacheBreakdown(input, last.Usage.InputTokensDetails.CachedTokens)
				fmt.Fprintf(stdout, "  Last prompt: %s input tokens (%s cached, %s uncached; %.1f%% hit)\n",
					compactCLITokens(input), compactCLITokens(cached), compactCLITokens(missed), hitRate)
			}
		}
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "Token Usage:")
		fmt.Fprintf(stdout, "  Input:       %d (cached: %d)\n", s.SessionUsage.Input, s.SessionUsage.Cached)
		fmt.Fprintf(stdout, "  Output:      %d (reasoning: %d)\n", s.SessionUsage.Output, s.SessionUsage.Reasoning)
	}

	if items, err := client.ListMemories(ctx, 100); err == nil {
		fmt.Fprintln(stdout, "")
		fmt.Fprintln(stdout, "Workspace Memory:")
		if len(items) > 0 {
			fmt.Fprintf(stdout, "  Stored:      %d active items (use '/memory' to view or manage)\n", len(items))
		} else {
			fmt.Fprintln(stdout, "  Stored:      None yet (use '/memory add <text>' to record conventions)")
		}
	}
}

func configuredContextWindow(client cliCore, saved core.Session, model string) int64 {
	if saved.Settings.ContextWindow != nil {
		return *saved.Settings.ContextWindow
	}
	if configured := client.ProjectContextDefaults().ContextWindow; configured > 0 {
		return configured
	}
	for _, profile := range client.ListModelProfiles() {
		if profile.Profile == saved.Settings.ModelProfile || profile.Model == model {
			if profile.ContextWindow > 0 {
				return profile.ContextWindow
			}
		}
	}
	if last, ok := latestMainUsage(saved); ok {
		return last.ContextWindow
	}
	return 0
}

func latestMainUsage(saved core.Session) (core.UsageRecord, bool) {
	for i := len(saved.UsageRequests) - 1; i >= 0; i-- {
		request := saved.UsageRequests[i]
		if request.Purpose == "" || request.Purpose == core.UsagePurposeMain {
			if request.AgentID == "" || request.AgentID == "lead" {
				return request, true
			}
		}
	}
	return core.UsageRecord{}, false
}

func promptCacheBreakdown(inputTokens, cachedTokens int64) (int64, int64, float64) {
	if cachedTokens < 0 {
		cachedTokens = 0
	}
	if inputTokens <= 0 {
		return cachedTokens, 0, 0
	}
	if cachedTokens > inputTokens {
		cachedTokens = inputTokens
	}
	return cachedTokens, inputTokens - cachedTokens, float64(cachedTokens) * 100 / float64(inputTokens)
}

func handleCoreCLIMemory(client cliCore, line string, stdout, stderr io.Writer) {
	ctx := context.Background()
	parts := strings.Fields(line)
	subcmd := "list"
	if len(parts) > 1 {
		subcmd = strings.ToLower(parts[1])
	}

	switch subcmd {
	case "list":
		query := ""
		if len(parts) > 2 {
			query = strings.TrimSpace(strings.Join(parts[2:], " "))
		}
		if query != "" {
			results, err := client.SearchMemories(ctx, query, 20)
			if err != nil {
				fmt.Fprintf(stderr, "error: search memories: %v\n", err)
				return
			}
			if len(results) == 0 {
				fmt.Fprintf(stdout, "No workspace memories matching %q found.\n", query)
				return
			}
			fmt.Fprintf(stdout, "Workspace Memories (%d matches for %q):\n", len(results), query)
			for _, r := range results {
				shortID := r.Item.ID
				if len(shortID) > 8 {
					shortID = shortID[:8]
				}
				timeStr := r.Item.UpdatedAt.Format("Jan 02 15:04")
				fmt.Fprintf(stdout, "  [%s] %-12s %s (%s)\n", shortID, r.Item.Category, r.Item.Content, timeStr)
			}
			return
		}

		items, err := client.ListMemories(ctx, 50)
		if err != nil {
			fmt.Fprintf(stderr, "error: list memories: %v\n", err)
			return
		}
		if len(items) == 0 {
			fmt.Fprintln(stdout, "No workspace memories stored yet.")
			fmt.Fprintln(stdout, "Use '/memory add <text>' to record conventions, technical preferences, or rules.")
			return
		}
		fmt.Fprintf(stdout, "Workspace Memories (%d items):\n", len(items))
		for _, item := range items {
			shortID := item.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			timeStr := item.UpdatedAt.Format("Jan 02 15:04")
			fmt.Fprintf(stdout, "  [%s] %-12s %s (%s)\n", shortID, item.Category, item.Content, timeStr)
		}
		fmt.Fprintln(stdout, "\nUse '/memory add <text>' to record, '/memory remove <id>' to delete.")

	case "add":
		if len(parts) < 3 {
			fmt.Fprintln(stderr, "Usage: /memory add [category] <content>")
			fmt.Fprintln(stderr, "Categories: preference, convention, decision, correction, verified_fact (default: preference)")
			return
		}
		cat := memory.CategoryPreference
		contentParts := parts[2:]
		firstLower := strings.ToLower(parts[2])
		switch memory.Category(firstLower) {
		case memory.CategoryPreference, memory.CategoryConvention, memory.CategoryDecision, memory.CategoryCorrection, memory.CategoryVerifiedFact:
			cat = memory.Category(firstLower)
			contentParts = parts[3:]
		}
		content := strings.TrimSpace(strings.Join(contentParts, " "))
		if content == "" {
			fmt.Fprintln(stderr, "error: memory content cannot be empty")
			return
		}
		item, err := client.AddMemory(ctx, memory.Input{
			Category:   cat,
			Content:    content,
			Importance: 3,
		})
		if err != nil {
			fmt.Fprintf(stderr, "error: add memory: %v\n", err)
			return
		}
		shortID := item.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		fmt.Fprintf(stdout, "Added workspace memory [%s] (%s): %s\n", shortID, item.Category, item.Content)

	case "remove":
		if len(parts) < 3 {
			fmt.Fprintln(stderr, "Usage: /memory remove <id|prefix>")
			return
		}
		target := parts[2]
		items, err := client.ListMemories(ctx, 200)
		if err != nil {
			fmt.Fprintf(stderr, "error: query memories: %v\n", err)
			return
		}
		var matches []memory.Item
		for _, it := range items {
			if strings.HasPrefix(strings.ToLower(it.ID), strings.ToLower(target)) {
				matches = append(matches, it)
			}
		}
		if len(matches) == 0 {
			fmt.Fprintf(stderr, "error: memory item %q not found\n", target)
			return
		}
		if len(matches) > 1 {
			var ids []string
			for _, m := range matches {
				s := m.ID
				if len(s) > 8 {
					s = s[:8]
				}
				ids = append(ids, s)
			}
			fmt.Fprintf(stderr, "error: memory ID prefix %q is ambiguous (%d matches: %s)\n", target, len(matches), strings.Join(ids, ", "))
			return
		}
		match := matches[0]
		if err := client.DeleteMemory(ctx, match.ID); err != nil {
			fmt.Fprintf(stderr, "error: delete memory: %v\n", err)
			return
		}
		shortID := match.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		fmt.Fprintf(stdout, "Deleted workspace memory [%s]: %s\n", shortID, match.Content)

	case "clear":
		hasForce := false
		for _, p := range parts[2:] {
			if p == "--force" {
				hasForce = true
				break
			}
		}
		if !hasForce {
			fmt.Fprintln(stderr, "Are you sure you want to clear all workspace memories?")
			fmt.Fprintln(stderr, "Run '/memory clear --force' to confirm.")
			return
		}
		if err := client.ClearMemories(ctx); err != nil {
			fmt.Fprintf(stderr, "error: clear memories: %v\n", err)
			return
		}
		fmt.Fprintln(stdout, "Cleared all workspace memories.")

	case "help":
		fmt.Fprintln(stdout, "Workspace Memory Commands:")
		fmt.Fprintln(stdout, "  /memory or /memory list           List recent workspace memories")
		fmt.Fprintln(stdout, "  /memory list <query>              Search memories by text")
		fmt.Fprintln(stdout, "  /memory add [category] <content>  Add a workspace convention or preference")
		fmt.Fprintln(stdout, "  /memory remove <id|prefix>        Remove a memory item by ID or prefix")
		fmt.Fprintln(stdout, "  /memory clear --force             Clear all workspace memories")
		fmt.Fprintln(stdout, "\nCategories: preference, convention, decision, correction, verified_fact")

	default:
		// Unknown subcmd: treat as query search
		query := strings.TrimSpace(strings.Join(parts[1:], " "))
		results, err := client.SearchMemories(ctx, query, 20)
		if err != nil {
			fmt.Fprintf(stderr, "error: search memories: %v\n", err)
			return
		}
		if len(results) == 0 {
			fmt.Fprintf(stdout, "No workspace memories matching %q found.\n", query)
			return
		}
		fmt.Fprintf(stdout, "Workspace Memories (%d matches for %q):\n", len(results), query)
		for _, r := range results {
			shortID := r.Item.ID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			timeStr := r.Item.UpdatedAt.Format("Jan 02 15:04")
			fmt.Fprintf(stdout, "  [%s] %-12s %s (%s)\n", shortID, r.Item.Category, r.Item.Content, timeStr)
		}
	}
}

var validCLIEffortLevels = []string{"default", "none", "minimal", "low", "medium", "high", "xhigh", "max"}

func normalizeCLIEffort(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "default", "unset", "clear", "reset", "off", "inherit":
		return "", true
	case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		return strings.ToLower(strings.TrimSpace(raw)), true
	}
	return "", false
}

func handleCoreCLIEffort(client cliCore, sessionID, line string, stdout, stderr io.Writer) {
	fields := strings.Fields(line)
	ctx := context.Background()
	saved, err := client.GetSession(ctx, sessionID)
	if err != nil {
		fmt.Fprintf(stderr, "error: get session: %v\n", err)
		return
	}
	current := saved.Settings.ReasoningEffort
	if current == "" {
		current = "default"
	}
	if len(fields) == 1 {
		fmt.Fprintf(stdout, "Current reasoning effort: %s\nAvailable levels: %s\n", current, strings.Join(validCLIEffortLevels, ", "))
		return
	}
	target, ok := normalizeCLIEffort(fields[1])
	if !ok {
		fmt.Fprintf(stderr, "Invalid reasoning effort %q. Available levels: %s\n", fields[1], strings.Join(validCLIEffortLevels, ", "))
		return
	}
	settings := saved.Settings
	settings.ReasoningEffort = target
	if err := client.SetSessionSettings(ctx, sessionID, settings); err != nil {
		fmt.Fprintf(stderr, "error: set reasoning effort: %v\n", err)
		return
	}
	display := target
	if display == "" {
		display = "default"
	}
	fmt.Fprintf(stdout, "Reasoning effort set to %s.\n", display)
}

func handleCoreCLIAgents(client cliCore, runtime cliRuntime, sessionID, line string, stdout, stderr io.Writer) {
	ctx := context.Background()
	saved, err := client.GetSession(ctx, sessionID)
	if err != nil {
		fmt.Fprintf(stderr, "error: get session: %v\n", err)
		return
	}
	fields := strings.Fields(line)
	targetAgent := ""
	if len(fields) > 1 {
		targetAgent = strings.ToLower(fields[1])
	}

	taskStatus := "idle"
	if runtime != nil {
		if state, stateErr := runtime.State(ctx); stateErr == nil && state.Task.Status != "" {
			taskStatus = string(state.Task.Status)
		}
	}

	leadUsage := saved.SessionUsage
	if val, ok := saved.AgentUsage["lead"]; ok {
		leadUsage = val
	}

	if targetAgent != "" && targetAgent != "lead" {
		usage, ok := saved.AgentUsage[targetAgent]
		if !ok {
			fmt.Fprintf(stderr, "Agent %q not found in this session.\n", fields[1])
			return
		}
		fmt.Fprintf(stdout, "Agent [%s]:\n", targetAgent)
		fmt.Fprintf(stdout, "  Role:      worker\n")
		fmt.Fprintf(stdout, "  Requests:  %d\n", usage.Requests)
		fmt.Fprintf(stdout, "  Tokens:    %d in (%d cached), %d out (%d reasoning)\n",
			usage.Input, usage.Cached, usage.Output, usage.Reasoning)
		return
	}

	fmt.Fprintf(stdout, "Session Agents (current task: %s):\n", taskStatus)
	fmt.Fprintf(stdout, "  - [lead] Main Agent (role: lead, requests: %d, in: %d, out: %d)\n",
		leadUsage.Requests, leadUsage.Input, leadUsage.Output)

	for id, usage := range saved.AgentUsage {
		if id == "lead" {
			continue
		}
		fmt.Fprintf(stdout, "  - [%s] Subagent (requests: %d, in: %d, out: %d)\n",
			id, usage.Requests, usage.Input, usage.Output)
	}
}
