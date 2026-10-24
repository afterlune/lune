// Package goalcmd shares the goal command language and presentation across hosts.
package goalcmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/afterlune/lune/internal/terminaltext"
	core "github.com/afterlune/luneCore"
)

const Usage = "/goal [--tokens N] <objective> | edit <objective> | budget <N|off> | pause | resume | clear"

type Command struct { Action string; Objective string; Budget *int64 }

type Runtime interface {
	GetGoal(context.Context) (*core.Goal, error)
	CreateGoal(context.Context, core.GoalCreateOptions) (*core.Goal, error)
	EditGoal(context.Context, string) (*core.Goal, error)
	SetGoalBudget(context.Context, *int64) (*core.Goal, error)
	PauseGoal(context.Context) (*core.Goal, error)
	ResumeGoal(context.Context,...core.GoalResumeOptions) (*core.Goal, error)
	ClearGoal(context.Context) error
}

func IsCommand(line string) bool {
	fields := strings.Fields(line)
	return len(fields) > 0 && strings.EqualFold(fields[0], "/goal")
}

func split(text string) (string, string) {
	fields := strings.Fields(text)
	if len(fields) == 0 { return "", "" }
	return fields[0], strings.TrimSpace(text[len(fields[0]):])
}

func parseBudget(text string) (*int64, error) {
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n <= 0 { return nil, errors.New("token budget must be a positive integer") }
	return &n, nil
}

func Parse(line string) (Command, error) {
	if !IsCommand(line) { return Command{}, errors.New("expected /goal") }
	_, args := split(strings.TrimSpace(line))
	if args == "" { return Command{Action:"show"}, nil }
	first, rest := split(args)
	switch strings.ToLower(first) {
	case "pause", "resume", "clear":
		if rest != "" { return Command{}, fmt.Errorf("usage: /goal %s", first) }
		return Command{Action:strings.ToLower(first)}, nil
	case "edit": return Command{Action:"edit", Objective:rest}, nil
	case "budget":
		if rest == "off" { return Command{Action:"budget"}, nil }
		budget, err := parseBudget(rest)
		return Command{Action:"budget", Budget:budget}, err
	}
	command := Command{Action:"create"}
	if first == "--tokens" {
		n, tail := split(rest)
		budget, err := parseBudget(n)
		if err != nil { return Command{}, err }
		command.Budget, args = budget, tail
	}
	first, rest = split(args)
	if first == "--" { args = rest } else if strings.HasPrefix(first, "--") { return Command{}, fmt.Errorf("unknown goal option %q; use -- before literal text", first) }
	if strings.TrimSpace(args) == "" { return Command{}, errors.New("goal objective must not be empty") }
	command.Objective = args
	return command, nil
}

func (c Command) AllowedInPlan() bool { return c.Action == "show" || c.Action == "pause" || c.Action == "clear" }

func Apply(ctx context.Context, runtime Runtime, command Command, noninteractive bool) (*core.Goal, error) {
	switch command.Action {
	case "show": return runtime.GetGoal(ctx)
	case "create": return runtime.CreateGoal(ctx, core.GoalCreateOptions{Objective:command.Objective, TokenBudget:command.Budget, NoInteractiveDecisions:noninteractive})
	case "edit": return runtime.EditGoal(ctx, command.Objective)
	case "budget": return runtime.SetGoalBudget(ctx, command.Budget)
	case "pause": return runtime.PauseGoal(ctx)
	case "resume": return runtime.ResumeGoal(ctx,core.GoalResumeOptions{NoInteractiveDecisions:noninteractive})
	case "clear": return nil, runtime.ClearGoal(ctx)
	default: return nil, errors.New("unknown goal action")
	}
}

func Summary(goal *core.Goal) string {
	if goal == nil { return "No goal is set.\nUsage: "+Usage }
	budget := "unlimited"
	if goal.TokenBudget != nil { budget = strconv.FormatInt(*goal.TokenBudget,10) }
	estimate := ""
	if goal.Estimated { estimate = " (includes estimates)" }
	text := fmt.Sprintf("Goal: %s\nObjective: %s\nTokens: %d / %s%s (uncached input + output); cached input: %d\nRunning time: %s", goal.Status, goal.Objective, goal.TokensUsed, budget, estimate, goal.CachedTokens, (time.Duration(goal.RunMilliseconds)*time.Millisecond).Round(time.Second))
	if goal.StopReason != "" { text += "\n"+goal.StopReason }
	return terminaltext.Sanitize(text)
}
