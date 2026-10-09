package tui

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/afterlune/lune/internal/model"
)

func parseInputLine(line string) (*model.ResponseInput, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, errors.New("input is empty")
	}
	if json.Valid([]byte(line)) {
		var value any
		if err := json.Unmarshal([]byte(line), &value); err == nil {
			switch value.(type) {
			case string, []any:
				return model.NewResponseInput(json.RawMessage(line))
			}
		}
	}
	return model.NewResponseInput(line)
}

func responseHistoryItems(history *model.ResponseInput) []model.InputItem {
	if history == nil {
		return nil
	}
	if history.Items != nil {
		return append([]model.InputItem(nil), history.Items...)
	}
	if history.Text != nil {
		return []model.InputItem{model.InputMessage{
			Role:    model.RoleUser,
			Content: model.NewMessageText(*history.Text),
		}}
	}
	return nil
}
