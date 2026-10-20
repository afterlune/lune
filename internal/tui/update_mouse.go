package tui

import "charm.land/lipgloss/v2"

func (m *tuiModel) composerMouseOffset() (int, int) {
	contentWidth := max(20, m.width-4)
	const headerHeight = 3
	const composerGapHeight = 1
	const composerTopPadding = 1
	x := 1 // renderComposerBand adds one leading card-padding column.
	y := headerHeight + m.viewport.Height() + m.modalHeight(contentWidth) + composerGapHeight + composerTopPadding
	return x, y
}

func (m *tuiModel) isComposerMousePoint(x, y int) bool {
	offsetX, offsetY := m.composerMouseOffset()
	composerViewWidth := m.composer.Width() + lipgloss.Width(m.composer.Prompt)
	return x >= offsetX && x < offsetX+composerViewWidth &&
		y >= offsetY && y < offsetY+m.composer.Height()
}

func (m *tuiModel) composerMousePosition(x, y int) (int, int) {
	offsetX, offsetY := m.composerMouseOffset()
	return x - offsetX, y - offsetY
}
