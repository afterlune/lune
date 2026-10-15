package tui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
)

type keymapAction struct {
	id         string
	label      string
	defaultKey string
	contexts   []string
}

var keymapActions = []keymapAction{
	{id: "question_previous", label: "Previous answer", defaultKey: "up", contexts: []string{"question"}},
	{id: "question_next", label: "Next answer", defaultKey: "down", contexts: []string{"question"}},
	{id: "question_notes", label: "Toggle answer notes", defaultKey: "tab", contexts: []string{"question", "question_input"}},
	{id: "send", label: "Send / confirm", defaultKey: "enter", contexts: allKeymapContexts()},
	{id: "interrupt_or_copy", label: "Copy selection / interrupt or exit", defaultKey: "ctrl+c", contexts: allKeymapContexts()},
	{id: "transcript_copy", label: "Copy transcript selection", defaultKey: "ctrl+shift+c", contexts: []string{"main", "running", "activity_focus", "plan_review"}},
	{id: "toggle_activity", label: "Show activity and usage", defaultKey: "ctrl+b", contexts: allKeymapContexts()},
	{id: "search_open", label: "Search transcript", defaultKey: "f3", contexts: []string{"main", "input", "suggestions", "vim_normal", "running", "plan_review"}},
	{id: "search_previous", label: "Previous transcript match", defaultKey: "ctrl+p", contexts: []string{"transcript_search"}},
	{id: "search_next", label: "Next transcript match", defaultKey: "ctrl+n", contexts: []string{"transcript_search"}},
	{id: "focus_activity", label: "Focus tool activity", defaultKey: "f4", contexts: []string{"main", "input", "suggestions", "vim_normal", "running", "plan_review"}},
	{id: "activity_previous", label: "Previous tool activity", defaultKey: "up", contexts: []string{"activity_focus"}},
	{id: "activity_next", label: "Next tool activity", defaultKey: "down", contexts: []string{"activity_focus"}},
	{id: "activity_toggle", label: "Expand / collapse tool activity", defaultKey: "enter", contexts: []string{"activity_focus"}},
	{id: "interrupt_send", label: "Interrupt and send", defaultKey: "ctrl+enter", contexts: allKeymapContexts()},
	{id: "cycle_permissions", label: "Cycle permission mode", defaultKey: "shift+tab", contexts: []string{"main", "input", "suggestions", "vim_normal", "plan_review"}},
	{id: "chat_scroll_up", label: "Scroll chat up", defaultKey: "pgup", contexts: allKeymapContexts()},
	{id: "chat_scroll_down", label: "Scroll chat down", defaultKey: "pgdown", contexts: allKeymapContexts()},
	{id: "activity_scroll_up", label: "Scroll activity up", defaultKey: "ctrl+up", contexts: allKeymapContexts()},
	{id: "activity_scroll_down", label: "Scroll activity down", defaultKey: "ctrl+down", contexts: allKeymapContexts()},
	{id: "jump_chat_bottom", label: "Return to latest message", defaultKey: "alt+end", contexts: []string{"main", "input", "suggestions", "vim_normal", "plan_review", "running"}},
	{id: "details_scroll_up", label: "Scroll activity panel up", defaultKey: "up", contexts: []string{"details"}},
	{id: "details_scroll_down", label: "Scroll activity panel down", defaultKey: "down", contexts: []string{"details"}},
	{id: "picker_previous", label: "Previous option", defaultKey: "up", contexts: []string{"session_picker", "permission_menu", "model_picker", "effort_picker", "queue_picker", "plan_review"}},
	{id: "picker_next", label: "Next option", defaultKey: "down", contexts: []string{"session_picker", "permission_menu", "model_picker", "effort_picker", "queue_picker", "plan_review"}},
	{id: "suggestion_previous", label: "Previous command suggestion", defaultKey: "up", contexts: []string{"suggestions"}},
	{id: "suggestion_next", label: "Next command suggestion", defaultKey: "down", contexts: []string{"suggestions"}},
	{id: "suggestion_complete", label: "Complete command suggestion", defaultKey: "tab", contexts: []string{"suggestions"}},
	{id: "session_toggle_archived", label: "Toggle archived sessions", defaultKey: "tab", contexts: []string{"session_picker"}},
	{id: "session_archive", label: "Archive / unarchive session", defaultKey: "a", contexts: []string{"session_picker"}},
	{id: "session_rename", label: "Rename session", defaultKey: "r", contexts: []string{"session_picker"}},
	{id: "session_delete", label: "Delete session", defaultKey: "d", contexts: []string{"session_picker"}},
	{id: "session_confirm_delete", label: "Confirm session deletion", defaultKey: "y", contexts: []string{"delete_confirm"}},
	{id: "session_cancel_delete", label: "Cancel session deletion", defaultKey: "n", contexts: []string{"delete_confirm"}},
	{id: "queue_remove", label: "Remove queued message", defaultKey: "delete", contexts: []string{"queue_picker"}},
	{id: "approve_tool", label: "Approve tool call", defaultKey: "y", contexts: []string{"approval"}},
	{id: "deny_tool", label: "Deny tool call", defaultKey: "n", contexts: []string{"approval"}},
	{id: "mcp_field_next", label: "Next MCP form field", defaultKey: "tab", contexts: []string{"mcp_form"}},
	{id: "mcp_field_previous", label: "Previous MCP form field", defaultKey: "shift+tab", contexts: []string{"mcp_form"}},
	{id: "mcp_form_decline", label: "Decline MCP form", defaultKey: "ctrl+n", contexts: []string{"mcp_form"}},
	{id: "mcp_url_open", label: "Open MCP elicitation URL", defaultKey: "y", contexts: []string{"mcp_url"}},
	{id: "mcp_url_decline", label: "Decline MCP URL elicitation", defaultKey: "n", contexts: []string{"mcp_url"}},
	{id: "input_character_backward", label: "Move cursor left", defaultKey: "left", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_character_forward", label: "Move cursor right", defaultKey: "right", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_line_previous", label: "Move cursor up", defaultKey: "up", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_line_next", label: "Move cursor down", defaultKey: "down", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_word_backward", label: "Move cursor one word left", defaultKey: "ctrl+left", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_word_forward", label: "Move cursor one word right", defaultKey: "ctrl+right", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_line_start", label: "Move to line start", defaultKey: "home", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_line_end", label: "Move to line end", defaultKey: "end", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_begin", label: "Move to input start", defaultKey: "ctrl+home", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_end", label: "Move to input end", defaultKey: "ctrl+end", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_select_character_backward", label: "Select character left", defaultKey: "shift+left", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_select_character_forward", label: "Select character right", defaultKey: "shift+right", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_select_word_backward", label: "Select word left", defaultKey: "ctrl+shift+left", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_select_word_forward", label: "Select word right", defaultKey: "ctrl+shift+right", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_select_line_up", label: "Select line up", defaultKey: "shift+up", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_select_line_down", label: "Select line down", defaultKey: "shift+down", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_select_all", label: "Select all input", defaultKey: "ctrl+a", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_copy", label: "Copy selected input", defaultKey: "ctrl+shift+c", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_cut", label: "Cut selected input", defaultKey: "ctrl+x", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_paste", label: "Paste", defaultKey: "ctrl+v", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_undo", label: "Undo input edit", defaultKey: "ctrl+z", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_redo", label: "Redo input edit", defaultKey: "ctrl+y", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_delete_character_backward", label: "Delete character left", defaultKey: "backspace", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_delete_character_forward", label: "Delete character right", defaultKey: "delete", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_delete_word_backward", label: "Delete word left", defaultKey: "ctrl+backspace", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_delete_word_forward", label: "Delete word right", defaultKey: "ctrl+delete", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_delete_after_cursor", label: "Delete to line end", defaultKey: "ctrl+k", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_delete_before_cursor", label: "Delete to line start", defaultKey: "ctrl+u", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_uppercase_word", label: "Uppercase next word", defaultKey: "alt+u", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_lowercase_word", label: "Lowercase next word", defaultKey: "alt+l", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_capitalize_word", label: "Capitalize next word", defaultKey: "alt+c", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_transpose_character", label: "Transpose characters", defaultKey: "ctrl+t", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "input_newline", label: "Insert newline", defaultKey: "shift+enter", contexts: []string{"input", "session_rename", "question_input"}},
	{id: "vim_insert", label: "Vim: insert mode", defaultKey: "i", contexts: []string{"vim_normal"}},
	{id: "vim_append", label: "Vim: append mode", defaultKey: "a", contexts: []string{"vim_normal"}},
	{id: "vim_left", label: "Vim: move left", defaultKey: "h", contexts: []string{"vim_normal"}},
	{id: "vim_down", label: "Vim: move down", defaultKey: "j", contexts: []string{"vim_normal"}},
	{id: "vim_up", label: "Vim: move up", defaultKey: "k", contexts: []string{"vim_normal"}},
	{id: "vim_right", label: "Vim: move right", defaultKey: "l", contexts: []string{"vim_normal"}},
	{id: "vim_line_start", label: "Vim: line start", defaultKey: "0", contexts: []string{"vim_normal"}},
	{id: "vim_line_end", label: "Vim: line end", defaultKey: "$", contexts: []string{"vim_normal"}},
	{id: "vim_delete_line", label: "Vim: delete line", defaultKey: "d", contexts: []string{"vim_normal"}},
}

func allKeymapContexts() []string {
	return []string{
		"main", "input", "suggestions", "vim_normal", "session_picker", "session_rename",
		"permission_menu", "model_picker", "effort_picker", "queue_picker", "delete_confirm", "plan_review", "approval", "details", "running", "mcp_form", "mcp_url", "question", "question_input",
	}
}

func defaultKeymap() map[string]string {
	bindings := make(map[string]string, len(keymapActions))
	for _, action := range keymapActions {
		bindings[action.id] = action.defaultKey
	}
	return bindings
}

func cloneKeymap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for id, chord := range source {
		cloned[id] = chord
	}
	return cloned
}

func normalizeKeymap(bindings map[string]string) (map[string]string, error) {
	defaults := defaultKeymap()
	normalized := make(map[string]string, len(defaults))
	for id := range defaults {
		normalized[id] = defaults[id]
	}
	for id, chord := range bindings {
		actionIndex := keymapActionIndex(id)
		if actionIndex < 0 {
			return nil, fmt.Errorf("unknown keymap action %q", id)
		}
		canonical, err := normalizeKeyChord(chord)
		if err != nil {
			return nil, fmt.Errorf("invalid key for %s: %w", id, err)
		}
		if requiresInputModifier(keymapActions[actionIndex], canonical) {
			return nil, fmt.Errorf("invalid key for %s: use Ctrl/Alt or a non-text key", keymapActions[actionIndex].label)
		}
		normalized[id] = canonical
	}
	if err := validateKeymapConflicts(normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

func requiresInputModifier(action keymapAction, chord string) bool {
	if !strings.HasPrefix(action.id, "input_") {
		return false
	}
	parts := strings.Split(chord, "+")
	base := parts[len(parts)-1]
	if base != "space" && utf8.RuneCountInString(base) != 1 {
		return false
	}
	for _, modifier := range parts[:len(parts)-1] {
		if modifier == "ctrl" || modifier == "alt" {
			return false
		}
	}
	return true
}

func normalizeKeyChord(chord string) (string, error) {
	chord = strings.ToLower(strings.TrimSpace(chord))
	if chord == "escape" {
		chord = "esc"
	}
	if chord == "" || strings.ContainsAny(chord, "\r\n\t ") {
		return "", fmt.Errorf("expected one key chord")
	}
	parts := strings.Split(chord, "+")
	base := parts[len(parts)-1]
	if base == "esc" {
		return "", fmt.Errorf("esc is reserved for cancelling or closing")
	}
	seen := make(map[string]bool)
	for _, modifier := range parts[:len(parts)-1] {
		if modifier != "ctrl" && modifier != "alt" && modifier != "shift" {
			return "", fmt.Errorf("unknown modifier %q", modifier)
		}
		if seen[modifier] {
			return "", fmt.Errorf("modifier %q is repeated", modifier)
		}
		seen[modifier] = true
	}
	if base == "" || !validKeyName(base) {
		return "", fmt.Errorf("unknown key %q", base)
	}
	return chord, nil
}

func validKeyName(name string) bool {
	if utf8.RuneCountInString(name) == 1 {
		return true
	}
	switch name {
	case "enter", "tab", "space", "up", "down", "left", "right", "home", "end", "pgup", "pgdown", "backspace", "delete", "insert":
		return true
	}
	if strings.HasPrefix(name, "f") {
		number, err := strconv.Atoi(strings.TrimPrefix(name, "f"))
		return err == nil && number >= 1 && number <= 24
	}
	return false
}

func validateKeymapConflicts(bindings map[string]string) error {
	for i, first := range keymapActions {
		for _, second := range keymapActions[i+1:] {
			if bindings[first.id] != bindings[second.id] || !keymapContextsOverlap(first.contexts, second.contexts) {
				continue
			}
			return fmt.Errorf("%q is assigned to both %s and %s", bindings[first.id], first.label, second.label)
		}
	}
	return nil
}

func keymapContextsOverlap(first, second []string) bool {
	for _, left := range first {
		for _, right := range second {
			if left == right {
				return true
			}
		}
	}
	return false
}

func keymapActionIndex(id string) int {
	for index, action := range keymapActions {
		if action.id == id {
			return index
		}
	}
	return -1
}

var writeClipboard = clipboard.WriteAll

const maxInputUndoSteps = 100

func (m *tuiModel) keymapBindings() map[string]string {
	if m.preferences.Keymap == nil {
		return defaultKeymap()
	}
	return m.preferences.Keymap
}

func (m *tuiModel) matchesKey(id string, message tea.KeyPressMsg) bool {
	chord := m.keymapBindings()[id]
	if chord == "" {
		chord = defaultKeymap()[id]
	}
	return message.String() == chord
}

func (m *tuiModel) keyLabel(id string) string {
	if chord := m.keymapBindings()[id]; chord != "" {
		return chord
	}
	return defaultKeymap()[id]
}

func (m *tuiModel) applyKeymap() {
	bindings, err := normalizeKeymap(m.keymapBindings())
	if err != nil {
		bindings = defaultKeymap()
	}
	m.preferences.Keymap = bindings
	keymap := textarea.DefaultKeyMap()
	bind := func(id, label string) key.Binding {
		chord := bindings[id]
		return key.NewBinding(key.WithKeys(chord), key.WithHelp(chord, label))
	}
	keymap.CharacterBackward = bind("input_character_backward", "cursor left")
	keymap.CharacterForward = bind("input_character_forward", "cursor right")
	keymap.LinePrevious = bind("input_line_previous", "cursor up")
	keymap.LineNext = bind("input_line_next", "cursor down")
	keymap.WordBackward = bind("input_word_backward", "word left")
	keymap.WordForward = bind("input_word_forward", "word right")
	keymap.LineStart = bind("input_line_start", "line start")
	keymap.LineEnd = bind("input_line_end", "line end")
	keymap.InputBegin = bind("input_begin", "input start")
	keymap.InputEnd = bind("input_end", "input end")
	keymap.SelectCharacterBackward = bind("input_select_character_backward", "select left")
	keymap.SelectCharacterForward = bind("input_select_character_forward", "select right")
	keymap.SelectWordBackward = bind("input_select_word_backward", "select word left")
	keymap.SelectWordForward = bind("input_select_word_forward", "select word right")
	keymap.SelectLineUp = bind("input_select_line_up", "select line up")
	keymap.SelectLineDown = bind("input_select_line_down", "select line down")
	keymap.SelectAll = bind("input_select_all", "select all")
	keymap.CopySelection = bind("input_copy", "copy selection")
	keymap.Paste = bind("input_paste", "paste")
	keymap.DeleteCharacterBackward = bind("input_delete_character_backward", "delete left")
	keymap.DeleteCharacterForward = bind("input_delete_character_forward", "delete right")
	keymap.DeleteWordBackward = bind("input_delete_word_backward", "delete word left")
	keymap.DeleteWordForward = bind("input_delete_word_forward", "delete word right")
	keymap.DeleteAfterCursor = bind("input_delete_after_cursor", "delete to line end")
	keymap.DeleteBeforeCursor = bind("input_delete_before_cursor", "delete to line start")
	keymap.UppercaseWordForward = bind("input_uppercase_word", "uppercase next word")
	keymap.LowercaseWordForward = bind("input_lowercase_word", "lowercase next word")
	keymap.CapitalizeWordForward = bind("input_capitalize_word", "capitalize next word")
	keymap.TransposeCharacterBackward = bind("input_transpose_character", "transpose characters")
	keymap.InsertNewline = bind("input_newline", "newline")
	keymap.PageUp = key.NewBinding()
	keymap.PageDown = key.NewBinding()
	m.input.KeyMap = keymap
}

func (m *tuiModel) openKeymapEditor() (tea.Model, tea.Cmd) {
	m.keymapDraft = cloneKeymap(m.keymapBindings())
	m.keymapIndex = 0
	m.keymapCapture = ""
	m.keymapOpen = true
	m.resetInput()
	m.status = "Key bindings · ↑/↓ choose · Enter change · Esc apply and close."
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) handleKeymapKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	pressed := message.String()
	if m.keymapCapture != "" {
		if pressed == "esc" {
			m.keymapCapture = ""
			m.status = "Key capture cancelled."
			m.updateLayout()
			return m, nil
		}
		chord, err := normalizeKeyChord(pressed)
		if err != nil {
			m.status = "That key cannot be assigned: " + err.Error()
			m.updateLayout()
			return m, nil
		}
		candidate := cloneKeymap(m.keymapDraft)
		candidate[m.keymapCapture] = chord
		normalized, err := normalizeKeymap(candidate)
		if err != nil {
			m.status = "That binding conflicts: " + err.Error()
			m.updateLayout()
			return m, nil
		}
		action := keymapActions[keymapActionIndex(m.keymapCapture)]
		m.keymapDraft = normalized
		m.keymapCapture = ""
		m.status = action.label + " set to " + chord + "; press Esc to apply changes."
		m.updateLayout()
		return m, nil
	}
	switch pressed {
	case "esc":
		return m.closeKeymapEditor()
	case "up":
		if m.keymapIndex > 0 {
			m.keymapIndex--
		}
	case "down":
		if m.keymapIndex < len(keymapActions) {
			m.keymapIndex++
		}
	case "enter":
		if m.keymapIndex == len(keymapActions) {
			m.keymapDraft = defaultKeymap()
			m.status = "Default bindings restored; press Esc to apply changes."
		} else {
			m.keymapCapture = keymapActions[m.keymapIndex].id
			m.status = "Press one key or key combination; Esc cancels capture."
		}
	}
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) closeKeymapEditor() (tea.Model, tea.Cmd) {
	bindings, err := normalizeKeymap(m.keymapDraft)
	if err != nil {
		m.status = "Key bindings were not applied: " + err.Error()
		m.keymapCapture = ""
		return m, nil
	}
	m.preferences.Keymap = bindings
	m.keymapOpen = false
	m.keymapCapture = ""
	m.applyKeymap()
	if err := saveUIPreferences(m.preferencesPath, m.preferences); err != nil {
		m.status = "Key bindings changed for this session; preference could not be saved."
	} else {
		m.status = "Key bindings saved."
	}
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) resetKeymap() (tea.Model, tea.Cmd) {
	m.preferences.Keymap = defaultKeymap()
	m.applyKeymap()
	if err := saveUIPreferences(m.preferencesPath, m.preferences); err != nil {
		m.status = "Default key bindings restored for this session; preference could not be saved."
	} else {
		m.status = "Default key bindings restored."
	}
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) handleInputEdit(message tea.KeyPressMsg) (bool, tea.Cmd) {
	switch {
	case m.matchesKey("input_cut", message):
		if !m.input.HasSelection() {
			return true, nil
		}
		selected := m.input.SelectedText()
		if err := writeClipboard(selected); err != nil {
			m.status = "Selection could not be cut because the clipboard write failed."
			return true, nil
		}
		before := m.input.Value()
		m.input.DeleteSelection()
		m.recordInputChange(before, m.input.Value())
		m.status = "Selection cut."
		return true, nil
	case m.matchesKey("input_undo", message):
		m.undoInput()
		return true, nil
	case m.matchesKey("input_redo", message):
		m.redoInput()
		return true, nil
	default:
		return false, nil
	}
}

func (m *tuiModel) updateInput(message tea.Msg) tea.Cmd {
	before := m.input.Value()
	var command tea.Cmd
	m.input, command = m.input.Update(message)
	m.recordInputChange(before, m.input.Value())
	if before != m.input.Value() {
		m.clearTranscriptSelection()
	}
	return command
}

func (m *tuiModel) recordInputChange(before, after string) {
	if before == after {
		return
	}
	m.undoStack = append(m.undoStack, inputSnapshot{text: before, attachments: shallowCopyImageAttachments(m.attachments)})
	if len(m.undoStack) > maxInputUndoSteps {
		m.undoStack = m.undoStack[len(m.undoStack)-maxInputUndoSteps:]
	}
	m.redoStack = nil
	m.reconcileDraftAttachments(after)
}

func (m *tuiModel) undoInput() {
	if len(m.undoStack) == 0 {
		m.status = "Nothing to undo."
		return
	}
	current := inputSnapshot{text: m.input.Value(), attachments: shallowCopyImageAttachments(m.attachments)}
	last := len(m.undoStack) - 1
	value := m.undoStack[last]
	m.undoStack = m.undoStack[:last]
	m.redoStack = append(m.redoStack, current)
	m.restoreInputSnapshot(value)
	m.moveInputCursorToEnd()
	m.input.ClearSelection()
	m.status = "Input edit undone."
}

func (m *tuiModel) redoInput() {
	if len(m.redoStack) == 0 {
		m.status = "Nothing to redo."
		return
	}
	current := inputSnapshot{text: m.input.Value(), attachments: shallowCopyImageAttachments(m.attachments)}
	last := len(m.redoStack) - 1
	value := m.redoStack[last]
	m.redoStack = m.redoStack[:last]
	m.undoStack = append(m.undoStack, current)
	if len(m.undoStack) > maxInputUndoSteps {
		m.undoStack = m.undoStack[len(m.undoStack)-maxInputUndoSteps:]
	}
	m.restoreInputSnapshot(value)
	m.moveInputCursorToEnd()
	m.input.ClearSelection()
	m.status = "Input edit redone."
}

func (m *tuiModel) moveInputCursorToEnd() {
	lines := strings.Count(m.input.Value(), "\n")
	for range lines {
		m.input.CursorDown()
	}
	m.input.CursorEnd()
}

func (m *tuiModel) setInputValue(value string) {
	for _, attachment := range m.attachments {
		if !strings.Contains(value, attachment.Token) {
			m.status = "Remove attached images before replacing the draft."
			return
		}
	}
	m.input.SetValue(value)
	m.undoStack = nil
	m.redoStack = nil
}
