package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/afterlune/lune/internal/goalcmd"
	core "github.com/afterlune/luneCore"
)

type cliGoalRuntime interface {
	cliRuntime
	goalcmd.Runtime
	SubscribeEventsWithReplayComplete(context.Context, int64) (<-chan core.Event, error)
}

func handleCoreCLIGoal(runtime cliRuntime, workspace string, command goalcmd.Command, watchKeys keyEventWatcher, stdout, stderr io.Writer) error {
	manager, ok := runtime.(cliGoalRuntime)
	if !ok {
		fmt.Fprintln(stderr, "Goal management is unavailable.")
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if command.Action != "create" && command.Action != "resume" {
		if command.Action == "edit" && command.Objective == "" {
			fmt.Fprintln(stderr, "Usage: /goal edit <objective>")
			return nil
		}
		goal, err := goalcmd.Apply(ctx, manager, command, watchKeys == nil)
		if err != nil {
			fmt.Fprintln(stderr, "error: goal:", err)
			return nil
		}
		if command.Action == "clear" {
			_, err = fmt.Fprintln(stdout, "Goal cleared.")
		} else {
			_, err = fmt.Fprintln(stdout, goalcmd.Summary(goal))
		}
		return err
	}
	// Subscribe before starting work and discard only the completed historical
	// replay, so resume does not print results from previous goal turns again.
	events, err := manager.SubscribeEventsWithReplayComplete(ctx, 0)
	if err != nil {
		return err
	}
	for {
		event, ok := <-events
		if !ok {
			return errors.New("goal event stream closed during replay")
		}
		if event.Kind == core.EventRuntimeStreamFailed {
			return fmt.Errorf("goal event replay: %s", event.Err)
		}
		if event.Kind == core.EventRuntimeReplayComplete {
			break
		}
	}
	var keys <-chan cliInputEvent
	if watchKeys != nil {
		var stop func()
		keys, stop, err = watchKeys()
		if stop != nil {
			defer stop()
		}
		if err != nil {
			fmt.Fprintln(stderr, "warning: keyboard input unavailable:", err)
			keys = nil
		}
	}
	goal, err := goalcmd.Apply(ctx, manager, command, keys == nil)
	if err != nil {
		fmt.Fprintln(stderr, "error: goal:", err)
		return nil
	}
	_, _ = fmt.Fprintln(stdout, goalcmd.Summary(goal))
	for {
		select {
		case key, ok := <-keys:
			if !ok {
				keys = nil
				continue
			}
			if key.escape {
				paused, pauseErr := manager.PauseGoal(ctx)
				if pauseErr != nil {
					return pauseErr
				}
				_, err = fmt.Fprintln(stdout, goalcmd.Summary(paused)+"\nUse /goal resume to continue.")
				return err
			}
		case event, ok := <-events:
			if !ok {
				return errors.New("goal event stream closed")
			}
			if event.Kind == core.EventRuntimeStreamFailed {
				return fmt.Errorf("goal event stream: %s", event.Err)
			}
			if event.Kind != core.EventTaskStarted || event.GoalID != goal.ID {
				continue
			}
			result, callErr, interrupted := awaitCoreExecution(manager, workspace, event.ExecutionID, events, keys, stderr)
			if err := printCoreTaskResult(stdout, stderr, result.Response, callErr, interrupted); err != nil {
				return err
			}
			current, err := manager.GetGoal(ctx)
			if err != nil {
				return err
			}
			if current == nil || current.ID != goal.ID {
				return nil
			}
			if current.Status != core.GoalActive || result.Status == core.TaskPaused || interrupted {
				_, err = fmt.Fprintln(stdout, goalcmd.Summary(current))
				if result.Status == core.TaskPaused {
					_, _ = fmt.Fprintln(stdout, "Resolve pending decisions, then use /goal resume to continue.")
				}
				return err
			}
		}
	}
}
