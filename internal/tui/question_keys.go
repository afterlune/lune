package tui

import tea "charm.land/bubbletea/v2"

// handleQuestionKeyPress owns navigation and editing while the card has focus.
func (m *tuiModel) handleQuestionKeyPress(msg tea.KeyPressMsg, key string) (handled bool, cmd tea.Cmd) {
	q := m.activeQuestion
	if q == nil || q.Submitting {
		return false, nil
	}
	draft := q.currentDraft()
	if draft == nil {
		return false, nil
	}
	switch key {
	case "left", "right":
		delta := 1
		if key == "left" {
			delta = -1
		}
		q.CurrentIndex = max(0, min(q.CurrentIndex+delta, len(q.Request.Questions)-1))
		m.updateLayout()
		return true, nil
	case "enter":
		_, cmd = m.resolveActiveQuestion()
		return true, cmd
	case "tab":
		draft.NotesOpen = !draft.NotesOpen
		m.updateLayout()
		return true, nil
	}

	before := *draft
	defer func() {
		if before.Selected != draft.Selected || before.Custom != draft.Custom || before.NoteText != draft.NoteText {
			m.questionDraftChanged(q)
		}
		if handled {
			m.updateLayout()
		}
	}()
	maxOption := len(q.Request.Questions[q.CurrentIndex].Options)
	if !draft.NotesOpen {
		switch key {
		case "up":
			draft.Selected = (draft.Selected + maxOption) % (maxOption + 1)
			return true, nil
		case "down":
			draft.Selected = (draft.Selected + 1) % (maxOption + 1)
			return true, nil
		}
	}

	if draft.NotesOpen || draft.Selected == maxOption {
		input := &draft.Custom
		if draft.NotesOpen {
			input = &draft.NoteText
		}
		switch key {
		case "backspace":
			runes := []rune(*input)
			if len(runes) > 0 {
				*input = string(runes[:len(runes)-1])
			}
		case "space":
			*input += " "
		default:
			text := msg.Key().Text
			if text == "" && msg.Key().Code >= ' ' && msg.Key().Code <= '~' && msg.Key().Mod == 0 {
				text = string(msg.Key().Code)
			}
			*input += text
		}
		return true, nil
	}

	if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
		if option := int(key[0] - '1'); option <= maxOption {
			draft.Selected = option
			return true, nil
		}
	}
	// Other text goes to the composer, including slash commands.
	return false, nil
}
