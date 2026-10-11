package tui

import "github.com/afterlune/luneCore"

func parseInputLine(line string) (core.TaskInput, error) {
	return core.ParseTaskInput(line)
}
