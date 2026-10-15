package tui

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/rivo/uniseg"
	clip "golang.design/x/clipboard"
)

type transcriptSelection struct {
	start      int
	end        int
	anchorFrom int
	anchorTo   int
	originX    int
	originY    int
	active     bool
	dragging   bool
}

type transcriptLine struct {
	text string
	role string
}

type transcriptVisualLine struct {
	text  string
	start int
	end   int
}

type toolActivityGroup struct {
	key   string
	start int
	end   int
}

func (m *tuiModel) transcriptLines() []transcriptLine {
	lines := make([]transcriptLine, 0, len(m.entries)*2)
	groupNumber := 0
	appendText := func(value, role string) {
		for _, line := range strings.Split(value, "\n") {
			lines = append(lines, transcriptLine{text: line, role: role})
		}
	}
	appendEntry := func(entry tuiEntry) {
		text := renderConversationText(entry.text, max(1, m.chat.Width()-2))
		if text == "" {
			return
		}
		prefix := ""
		switch entry.role {
		case "you":
			prefix = "› "
		case "notice", "queued":
			prefix = "· "
		case "error":
			prefix = "! "
		case "permission":
			prefix = "! "
		case "question":
			prefix = "? "
		case "assistant":
			if entry.incomplete {
				prefix = "Partial · "
			}
		}
		parts := strings.Split(text, "\n")
		parts[0] = prefix + parts[0]
		appendText(strings.Join(parts, "\n"), entry.role)
	}
	appendSpacer := func() {
		if len(lines) > 0 && lines[len(lines)-1].text != "" {
			lines = append(lines, transcriptLine{})
		}
	}

	for index := 0; index < len(m.entries); {
		entry := m.entries[index]
		if entry.role != "tool" {
			appendEntry(entry)
			index++
			if index < len(m.entries) {
				appendSpacer()
			}
			continue
		}

		selected := m.activityFocus && groupNumber == m.activityFocusIndex
		groupNumber++
		key := toolActivityGroupKey(m.entries, index)
		lines = m.appendToolTranscript(lines, entry, selected, m.activityExpanded[key])
		index++
		if index < len(m.entries) {
			appendSpacer()
		}
	}
	return lines
}

func (m *tuiModel) collapsedToolGroupLabel(group toolActivityGroup) string {
	count := group.end - group.start
	if count == 1 {
		return m.entries[group.start].text
	}
	labels := make([]string, 0, min(count, 3))
	for index := group.start; index < group.end && len(labels) < 3; index++ {
		labels = append(labels, m.entries[index].text)
	}
	label := fmt.Sprintf("Tool activity (%d) · %s", count, strings.Join(labels, "; "))
	return fitTerminalText(label, 180)
}

func toolActivityGroupKey(entries []tuiEntry, start int) string {
	if start >= 0 && start < len(entries) && entries[start].activityID != "" {
		return entries[start].activityID
	}
	return fmt.Sprintf("tool-group-%d", start)
}

func transcriptContent(lines []transcriptLine) (string, []string) {
	contentLines := make([]string, len(lines))
	roles := make([]string, len(lines))
	for index, line := range lines {
		contentLines[index] = line.text
		roles[index] = line.role
	}
	return strings.Join(contentLines, "\n"), roles
}

func (m *tuiModel) transcriptContent() (string, []string) {
	return transcriptContent(m.transcriptLines())
}

func (m *tuiModel) toolActivityGroups() []toolActivityGroup {
	var groups []toolActivityGroup
	for index := 0; index < len(m.entries); {
		if m.entries[index].role != "tool" {
			index++
			continue
		}
		start := index
		index++
		groups = append(groups, toolActivityGroup{
			key: toolActivityGroupKey(m.entries, start), start: start, end: index,
		})
	}
	return groups
}

func (m *tuiModel) toggleFocusedActivity() {
	groups := m.toolActivityGroups()
	if len(groups) == 0 {
		return
	}
	m.activityFocusIndex = max(0, min(m.activityFocusIndex, len(groups)-1))
	group := groups[m.activityFocusIndex]
	m.activityExpanded[group.key] = !m.activityExpanded[group.key]
	m.refreshPanels()
}

func (m *tuiModel) applyTranscriptHighlights() {
	m.chat.ClearHighlights()
	selection := m.transcriptSelection
	if selection.active && selection.start < selection.end {
		m.chat.SetHighlights([][]int{{selection.start, selection.end}})
		return
	}
	if len(m.searchMatches) > 0 {
		m.chat.SetHighlights(m.searchMatches)
	}
}

func findTranscriptMatches(content, query string) [][]int {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	pattern := regexp.MustCompile("(?i)" + regexp.QuoteMeta(query))
	return pattern.FindAllStringIndex(content, -1)
}

func (m *tuiModel) openTranscriptSearch() {
	if m.searchOpen {
		if len(m.searchMatches) > 0 {
			m.chat.HighlightNext()
		}
		return
	}
	m.clearTranscriptSelection()
	m.searchOpen = true
	m.searchExpandedState = cloneExpansionState(m.activityExpanded)
	for _, group := range m.toolActivityGroups() {
		m.activityExpanded[group.key] = true
	}
	m.refreshPanels()
	m.updateTranscriptSearch()
	m.updateLayout()
}

func cloneExpansionState(source map[string]bool) map[string]bool {
	cloned := make(map[string]bool, len(source))
	for key, expanded := range source {
		cloned[key] = expanded
	}
	return cloned
}

func (m *tuiModel) updateTranscriptSearch() {
	content, _ := m.transcriptContent()
	m.searchMatches = findTranscriptMatches(content, m.searchQuery)
	m.chat.ClearHighlights()
	if len(m.searchMatches) > 0 {
		m.chat.SetHighlights(m.searchMatches)
	}
}

func (m *tuiModel) handleTranscriptSearchKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case message.String() == "esc":
		m.closeTranscriptSearch(false)
		m.status = "Transcript search closed."
	case message.String() == "enter":
		count := len(m.searchMatches)
		m.closeTranscriptSearch(true)
		if count == 0 {
			m.status = "No transcript matches."
		} else {
			m.status = fmt.Sprintf("Found %d transcript match(es). F3 resumes search.", count)
		}
	case m.matchesKey("search_previous", message):
		if len(m.searchMatches) > 0 {
			m.chat.HighlightPrevious()
		}
	case m.matchesKey("search_next", message):
		if len(m.searchMatches) > 0 {
			m.chat.HighlightNext()
		}
	case message.String() == "ctrl+u":
		m.searchQuery = ""
		m.updateTranscriptSearch()
	case message.String() == "backspace":
		if m.searchQuery != "" {
			runes := []rune(m.searchQuery)
			m.searchQuery = string(runes[:len(runes)-1])
			m.updateTranscriptSearch()
		}
	default:
		key := message.Key()
		if key.Text != "" && !key.Mod.Contains(tea.ModCtrl|tea.ModAlt) {
			m.searchQuery += key.Text
			m.updateTranscriptSearch()
		}
	}
	m.updateLayout()
	return m, nil
}

func (m *tuiModel) closeTranscriptSearch(keepMatch bool) {
	m.searchOpen = false
	if keepMatch {
		m.searchExpandedState = nil
		m.applyTranscriptHighlights()
		return
	}
	if m.searchExpandedState != nil {
		m.activityExpanded = m.searchExpandedState
		m.searchExpandedState = nil
	}
	m.searchQuery = ""
	m.searchMatches = nil
	m.refreshPanels()
}

func (m *tuiModel) transcriptPointAt(x, y int) (int, int, bool) {
	if m.detailsOpen || y < 0 || y >= m.chat.Height() {
		return 0, 0, false
	}
	content, _ := m.transcriptContent()
	lines := wrapTranscriptContent(content, m.chat.Width())
	lineIndex := m.chat.YOffset() + y
	if lineIndex < 0 || lineIndex >= len(lines) {
		return 0, 0, false
	}
	line := lines[lineIndex]
	if x <= 0 {
		return line.start, min(len(content), line.start+firstGraphemeBytes(line.text)), true
	}
	column := 0
	graphemes := uniseg.NewGraphemes(line.text)
	for graphemes.Next() {
		clusterStart, clusterEnd := graphemes.Positions()
		start := line.start + clusterStart
		end := line.start + clusterEnd
		width := max(1, graphemes.Width())
		if x < column+width {
			return start, end, true
		}
		column += width
	}
	return line.end, line.end, true
}

func firstGraphemeBytes(value string) int {
	graphemes := uniseg.NewGraphemes(value)
	if !graphemes.Next() {
		return 0
	}
	_, end := graphemes.Positions()
	return end
}

func wrapTranscriptContent(content string, width int) []transcriptVisualLine {
	width = max(1, width)
	var lines []transcriptVisualLine
	for lineStart := 0; lineStart <= len(content); {
		lineEnd := strings.IndexByte(content[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(content)
		} else {
			lineEnd += lineStart
		}
		logicalLine := content[lineStart:lineEnd]
		if logicalLine == "" {
			lines = append(lines, transcriptVisualLine{start: lineStart, end: lineEnd})
		} else {
			graphemes := uniseg.NewGraphemes(logicalLine)
			segmentStart, segmentEnd, columns := 0, 0, 0
			for graphemes.Next() {
				start, end := graphemes.Positions()
				clusterWidth := max(1, graphemes.Width())
				if columns > 0 && columns+clusterWidth > width {
					lines = append(lines, transcriptVisualLine{
						text: logicalLine[segmentStart:segmentEnd], start: lineStart + segmentStart, end: lineStart + segmentEnd,
					})
					segmentStart, columns = start, 0
				}
				segmentEnd = end
				columns += clusterWidth
			}
			lines = append(lines, transcriptVisualLine{
				text: logicalLine[segmentStart:segmentEnd], start: lineStart + segmentStart, end: lineStart + segmentEnd,
			})
		}
		if lineEnd == len(content) {
			break
		}
		lineStart = lineEnd + 1
	}
	return lines
}

func wordSelection(content string, start, end int) (int, int) {
	runes := []rune(content)
	byteOffsets := make([]int, len(runes)+1)
	for index, r := range runes {
		byteOffsets[index+1] = byteOffsets[index] + len(string(r))
	}
	runeAt := 0
	for runeAt < len(runes) && byteOffsets[runeAt+1] <= start {
		runeAt++
	}
	if runeAt >= len(runes) {
		return start, end
	}
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' }
	left, right := runeAt, runeAt
	if !isWord(runes[runeAt]) {
		return start, end
	}
	for left > 0 && isWord(runes[left-1]) {
		left--
	}
	for right+1 < len(runes) && isWord(runes[right+1]) {
		right++
	}
	return byteOffsets[left], byteOffsets[right+1]
}

func lineSelection(content string, offset int) (int, int) {
	start := strings.LastIndex(content[:max(0, min(offset, len(content)))], "\n") + 1
	end := strings.IndexByte(content[min(offset, len(content)):], '\n')
	if end < 0 {
		end = len(content)
	} else {
		end += min(offset, len(content))
	}
	return start, end
}

func (m *tuiModel) handleTranscriptMouseClick(message tea.MouseClickMsg) bool {
	if message.Button != tea.MouseLeft || m.searchOpen || m.detailsOpen || m.activityFocus {
		return false
	}
	start, end, ok := m.transcriptPointAt(message.X, message.Y)
	if !ok {
		return false
	}
	now := time.Now()
	if message.X == m.lastTranscriptClickX && message.Y == m.lastTranscriptClickY && now.Sub(m.lastTranscriptClickAt) <= 500*time.Millisecond {
		m.transcriptClickCount = m.transcriptClickCount%3 + 1
	} else {
		m.transcriptClickCount = 1
	}
	m.lastTranscriptClickAt, m.lastTranscriptClickX, m.lastTranscriptClickY = now, message.X, message.Y
	selection := transcriptSelection{originX: message.X, originY: message.Y, dragging: true}
	if m.transcriptClickCount == 2 {
		content, _ := m.transcriptContent()
		selection.start, selection.end = wordSelection(content, start, end)
		selection.anchorFrom, selection.anchorTo = selection.start, selection.end
		selection.active = selection.start < selection.end
	} else if m.transcriptClickCount >= 3 {
		content, _ := m.transcriptContent()
		selection.start, selection.end = lineSelection(content, start)
		selection.anchorFrom, selection.anchorTo = selection.start, selection.end
		selection.active = selection.start < selection.end
	} else {
		selection.anchorFrom, selection.anchorTo = start, end
	}
	m.transcriptSelection = selection
	m.applyTranscriptHighlights()
	return true
}

func (m *tuiModel) handleTranscriptMouseMotion(message tea.MouseMotionMsg) bool {
	selection := &m.transcriptSelection
	if !selection.dragging || message.Button != tea.MouseLeft {
		return false
	}
	start, end, ok := m.transcriptPointAt(message.X, message.Y)
	if !ok {
		return true
	}
	if message.X != selection.originX || message.Y != selection.originY {
		selection.active = true
	}
	if selection.active {
		selection.start = min(selection.anchorFrom, start)
		selection.end = max(selection.anchorTo, end)
		if start > selection.anchorFrom {
			selection.end = max(selection.anchorTo, end)
		} else if start < selection.anchorFrom {
			selection.start = min(start, selection.anchorFrom)
			selection.end = selection.anchorTo
		}
		m.applyTranscriptHighlights()
	}
	return true
}

func (m *tuiModel) handleTranscriptMouseRelease(message tea.MouseReleaseMsg) bool {
	selection := &m.transcriptSelection
	if message.Button != tea.MouseLeft || !selection.dragging {
		return false
	}
	selection.dragging = false
	if selection.active {
		m.copyTranscriptSelection(true)
	} else {
		m.clearTranscriptSelection()
	}
	return true
}

func (m *tuiModel) clearTranscriptSelection() {
	if !m.transcriptSelection.active && !m.transcriptSelection.dragging {
		return
	}
	m.transcriptSelection = transcriptSelection{}
	m.applyTranscriptHighlights()
}

func (m *tuiModel) selectedTranscriptText() string {
	selection := m.transcriptSelection
	content, _ := m.transcriptContent()
	start := max(0, min(selection.start, len(content)))
	end := max(start, min(selection.end, len(content)))
	return content[start:end]
}

func (m *tuiModel) autoCopySelectionEnabled() bool {
	switch m.preferences.CopyOnSelect {
	case "always":
		return true
	case "never":
		return false
	default:
		return !terminalCopiesSelectionNatively()
	}
}

func terminalCopiesSelectionNatively() bool {
	if os.Getenv("TMUX") != "" || os.Getenv("STY") != "" || os.Getenv("ZELLIJ") != "" {
		return false
	}
	if os.Getenv("WT_SESSION") != "" {
		return true
	}
	switch strings.ToLower(os.Getenv("TERM_PROGRAM")) {
	case "windows_terminal", "windowsterminal":
		return true
	case "vscode":
		return runtime.GOOS == "windows"
	case "kitty":
		return runtime.GOOS == "darwin"
	case "ghostty":
		return ghosttyForwardsCopyShortcut(os.Getenv("TERM_PROGRAM_VERSION"))
	default:
		return false
	}
}

func ghosttyForwardsCopyShortcut(version string) bool {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".")
	if len(parts) < 2 {
		return false
	}
	major, majorErr := strconv.Atoi(parts[0])
	minor, minorErr := strconv.Atoi(parts[1])
	return majorErr == nil && minorErr == nil && (major > 1 || major == 1 && minor >= 2)
}

var writePrimaryClipboard = writeTranscriptPrimaryClipboard

func writeTranscriptPrimaryClipboard(value string) error {
	clipboardInitOnce.Do(func() { clipboardInitErr = clip.Init() })
	if clipboardInitErr != nil {
		return clipboardInitErr
	}
	_, err := clip.Write(context.Background(), clip.FmtText, []byte(value), clip.FromPrimary())
	return err
}

func (m *tuiModel) copyTranscriptSelection(auto bool) bool {
	if !m.transcriptSelection.active {
		return false
	}
	text := m.selectedTranscriptText()
	if text == "" {
		return false
	}
	primaryErr := error(nil)
	if auto && runtime.GOOS == "linux" {
		primaryErr = writePrimaryClipboard(text)
	}
	if auto && !m.autoCopySelectionEnabled() {
		if primaryErr != nil {
			m.status = "Selection retained; press Ctrl+C to copy it."
		} else if runtime.GOOS == "linux" {
			m.status = "Selection sent to the primary clipboard."
		} else {
			m.status = "Selection retained; press Ctrl+C to copy it."
		}
		return true
	}
	err := writeClipboard(text)
	if err != nil {
		m.status = "Selection is available, but the clipboard write failed."
		return true
	}
	if auto {
		m.status = "Selection copied."
	} else {
		m.status = "Transcript selection copied."
	}
	if m.preferences.ClearSelectionOnCopy {
		m.clearTranscriptSelection()
	}
	return true
}

func (m *tuiModel) setCopySelectionFromCommand(line string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(line)
	usage := "Use /copyselect [auto|always|never] or /copyselect clear on|off."
	switch {
	case len(parts) == 1:
		m.status = fmt.Sprintf("Copy on select: %s · clear after copy: %s.", m.preferences.CopyOnSelect, onOff(m.preferences.ClearSelectionOnCopy))
	case len(parts) == 2 && (parts[1] == "auto" || parts[1] == "always" || parts[1] == "never"):
		m.preferences.CopyOnSelect = parts[1]
		m.status = "Copy on select set to " + parts[1] + "."
	case len(parts) == 3 && parts[1] == "clear" && (parts[2] == "on" || parts[2] == "off"):
		m.preferences.ClearSelectionOnCopy = parts[2] == "on"
		m.status = "Clear selection after copy " + parts[2] + "."
	default:
		m.status = usage
		return m, nil
	}
	if err := saveUIPreferences(m.preferencesPath, m.preferences); err != nil {
		m.status += " UI preference could not be saved."
	}
	m.resetInput()
	m.updateLayout()
	return m, nil
}

func onOff(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

func transcriptRoleStyle(role string, colors themeColors) lipgloss.Style {
	color := colors.Assistant
	switch role {
	case "you":
		color = colors.User
	case "tool", "notice", "queued":
		color = colors.Muted
	case "tool_done", "diff_added":
		color = colors.Success
	case "error", "diff_removed":
		color = colors.Error
	case "permission", "question":
		color = colors.Warning
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color))
}

func (m *tuiModel) toggleActivityFocus() {
	if m.detailsOpen || m.searchOpen {
		return
	}
	groups := m.toolActivityGroups()
	if len(groups) == 0 {
		m.status = "No tool activity in this transcript."
		return
	}
	m.activityFocus = !m.activityFocus
	if m.activityFocus {
		m.activityFocusIndex = min(m.activityFocusIndex, len(groups)-1)
		m.status = "Tool activity · ↑/↓ choose · Enter expand · Esc return to chat."
	} else {
		m.status = "Transcript focused."
	}
	m.refreshPanels()
	m.updateLayout()
}

func (m *tuiModel) adjustActivityFocus(delta int) {
	groups := m.toolActivityGroups()
	if len(groups) == 0 {
		m.activityFocusIndex = 0
		return
	}
	m.activityFocusIndex = max(0, min(m.activityFocusIndex+delta, len(groups)-1))
}
