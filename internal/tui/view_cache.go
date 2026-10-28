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
	cachedBlocks            []string
	cachedEntryCount        int
	cachedWidth             int
	cachedThemeBg           string
	cachedDirty             bool
	cachedHasTrailingRound  bool
	cachedTrailingRunning   bool
	cachedTrailingTextEmpty bool
	dirtyEntries            map[int]struct{}
	transcriptDirty         bool
	liveDirty               bool
	liveContent             string
	transcriptLines         []string
	entryLineRanges         []entryLineRange
	toolReveal              *toolRoundReveal
	toolRevealGeneration    uint64
	toolRevealPending       bool
	toolRevealWindowed      bool
	toolRevealWindowStart   int
	animateTools            bool
	// windowedTail reports that the viewport holds only the trailing screenful of
	// a live transcript. The complete document stays in transcriptLines and is
	// pushed back before any interaction that measures absolute rows.
	windowedTail bool
	// liveTailOnly reports that liveContent was rendered from the retained tail
	// rather than the whole message, so restoring must re-render it in full.
	liveTailOnly bool
	// liveForceFull makes the next live render use the complete text.
	liveForceFull bool
	// needsSoftWrap accumulates whether any rendered block exceeds the content
	// width, so the transcript never has to re-measure every block per refresh.
	needsSoftWrap bool
	// ruleLine memoizes the full-width transcript rule for one width.
	ruleLine  string
	ruleWidth int
	// roundBlocks memoizes rendered tool rounds by their first entry index. A round
	// is rendered again only when its own entries, expansion, live state, width or
	// theme change, so a long tool history is not re-rendered on every stream delta.
	roundBlocks map[int]cachedRound
}

// cachedRound is one rendered tool round plus the relative line ranges that
// appendRendered rebases onto the current document offset.
type cachedRound struct {
	end      int
	expanded bool
	live     bool
	width    int
	themeBg  string
	block    string
	ranges   []entryLineRange
}

// roundBlock returns a cached round render when it is still valid. The animated
// trailing round is owned by the reveal animation and is never cached.
func (c *viewCache) roundBlock(start, end int, expanded, live bool, width int, themeBg string, animated bool) (cachedRound, bool) {
	if animated || c.roundBlocks == nil {
		return cachedRound{}, false
	}
	round, ok := c.roundBlocks[start]
	if !ok || round.end != end || round.expanded != expanded || round.live != live ||
		round.width != width || round.themeBg != themeBg {
		return cachedRound{}, false
	}
	return round, true
}

func (c *viewCache) storeRoundBlock(start, end int, expanded, live bool, width int, themeBg, block string, ranges []entryLineRange) {
	if c.roundBlocks == nil {
		c.roundBlocks = make(map[int]cachedRound)
	}
	c.roundBlocks[start] = cachedRound{
		end: end, expanded: expanded, live: live, width: width, themeBg: themeBg,
		block: block, ranges: ranges,
	}
}

// invalidateContent marks only the composed viewport text as stale.
func (c *viewCache) invalidateContent() {
	c.transcriptDirty = true
	c.liveDirty = true
}

// invalidateEntry marks one transcript entry stale, re-rendering its cached
// block when applicable. Tool and subagent status changes use this for updates.
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
	for start, round := range c.roundBlocks {
		if index >= start && index < round.end {
			delete(c.roundBlocks, start)
		}
	}
}

// invalidateLive marks only the independently rendered live output area as stale.
// Historical transcript measurement is unaffected by token deltas.
func (c *viewCache) invalidateLive() {
	c.liveDirty = true
}

// invalidateBlocks marks the entry blocks and the composed text as stale.
func (c *viewCache) invalidateBlocks() {
	c.toolReveal = nil
	c.toolRevealGeneration++
	c.toolRevealPending = false
	c.toolRevealWindowed = false
	c.toolRevealWindowStart = 0
	c.animateTools = false
	c.cachedDirty = true
	c.dirtyEntries = nil
	c.roundBlocks = nil
	c.transcriptDirty = true
	c.liveDirty = true
}
