package tui

// viewCache owns the rendered transcript blocks and the two stale markers that
// decide how much of them has to be rebuilt. The flags mean different work and
// are therefore kept apart:
//
//   - transcriptDirty: the historical viewport text must be re-composed, but
//     the per-entry blocks are still valid.
//   - cachedDirty: every per-entry block must be rendered again.
//   - dirtyEntries: selected blocks must be rendered again, then the transcript
//     is re-composed around them.
type viewCache struct {
	cachedBlocks     []string
	cachedEntryCount int
	cachedWidth      int
	cachedThemeBg    string
	cachedDirty      bool
	dirtyEntries     map[int]struct{}
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

// invalidateEntry marks one transcript entry stale, re-rendering its cached
// block when applicable. Tool and subagent events use this for in-place updates.
func (c *viewCache) invalidateEntry(index int) {
	if index < 0 {
		return
	}
	c.invalidateContent()
	if c.cachedDirty {
		return
	}
	if c.dirtyEntries == nil {
		c.dirtyEntries = make(map[int]struct{})
	}
	c.dirtyEntries[index] = struct{}{}
}

// invalidateLive marks only the small, independently rendered live response
// area as stale. Historical transcript measurement is unaffected by token deltas.
func (c *viewCache) invalidateLive() {
	c.liveDirty = true
}

// invalidateBlocks marks the entry blocks and the composed text as stale.
func (c *viewCache) invalidateBlocks() {
	c.cachedDirty = true
	c.dirtyEntries = nil
	c.transcriptDirty = true
	c.liveDirty = true
}
