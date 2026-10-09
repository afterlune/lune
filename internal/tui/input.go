package tui

import "github.com/afterlune/lune/core"

func parseInputLine(line string) (core.TaskInput, error) {
	return core.ParseTaskInput(line)
}
