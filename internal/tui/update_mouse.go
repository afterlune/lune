package tui

import "charm.land/lipgloss/v2"

func (m *tuiModel) composerMouseOffset() (int, int) {
	contentWidth := max(20, m.width-4)
	const headerHeight = 3
	const composerGapHeight = 1
	const composerTopPadding = 1
	x := 1 // renderComposerBand adds one leading card-padding column.
	_, overlayHeight := m.overlaySection(contentWidth)
	y := headerHeight + m.viewport.Height() + overlayHeight + composerGapHeight + composerTopPadding
	return x, y
}

func (m *tuiModel) isComposerMousePoint(x, y int) bool {
	offsetX, offsetY := m.composerMouseOffset()
	// Width() excludes the prompt; include it to match the rendered view width.
	composerViewWidth := m.input.composer.Width() + lipgloss.Width(m.input.composer.Prompt)
	return x >= offsetX && x < offsetX+composerViewWidth &&
		y >= offsetY && y < offsetY+m.input.composer.Height()
}

func (m *tuiModel) composerMousePosition(x, y int) (int, int) {
	offsetX, offsetY := m.composerMouseOffset()
	return x - offsetX, y - offsetY
}
