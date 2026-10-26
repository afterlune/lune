package tui

// viewCache owns the rendered transcript blocks and the two stale markers that
// decide how much of them has to be rebuilt. The flags mean different work and
// are therefore kept apart:
//
//   - transcriptDirty: the historical viewport text must be re-composed, but
//     the per-entry blocks are still valid.
//   - cachedDirty: the per-entry blocks themselves must be rendered again
//     (entries were added, replaced or mutated), which also re-composes.
type viewCache struct {
	cachedBlocks     []string
	cachedEntryCount int
	cachedWidth      int
	cachedThemeBg    string
	cachedDirty      bool
	transcriptDirty  bool
	liveDirty        bool
	liveContent      string
	transcriptLines  []string
	entryLineRanges  []entryLineRange
}

// invalidateContent marks only the composed viewport text as stale.
func (c *viewCache) invalidateContent() {
	c.transcriptDirty = true
	c.liveDirty = true
}

// invalidateLive marks only the small, independently rendered live response
// area as stale. Historical transcript measurement is unaffected by token deltas.
func (c *viewCache) invalidateLive() {
	c.liveDirty = true
}

// invalidateBlocks marks the entry blocks and the composed text as stale.
func (c *viewCache) invalidateBlocks() {
	c.cachedDirty = true
	c.transcriptDirty = true
	c.liveDirty = true
}
