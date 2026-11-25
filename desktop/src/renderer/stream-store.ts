import type { TimelineEntry } from './harness-types'

export interface StreamSnapshot {
  text: string
  reasoning: string
  isCatchingUp: boolean
}

export interface CapturedReasoningEntry {
  text: string
  durationMs?: number
}

const REASONING_STORAGE_PREFIX = 'lune:reasoning:'
const MAX_STORED_REASONING_ENTRIES = 30
const MAX_STORED_REASONING_CHARS = 200_000

const EMPTY_SNAPSHOT: StreamSnapshot = {
  text: '',
  reasoning: '',
  isCatchingUp: false,
}

export function readCapturedReasoning(sessionId: string): Record<string, CapturedReasoningEntry> {
  if (!sessionId || typeof localStorage === 'undefined') return {}
  try {
    const raw = localStorage.getItem(`${REASONING_STORAGE_PREFIX}${sessionId}`)
    if (!raw) return {}
    const parsed = JSON.parse(raw) as unknown
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {}
    const result: Record<string, CapturedReasoningEntry> = {}
    for (const [key, val] of Object.entries(parsed)) {
      if (val && typeof val === 'object' && !Array.isArray(val)) {
        const rec = val as Record<string, unknown>
        if (typeof rec.text === 'string' && rec.text.trim()) {
          result[key] = {
            text: rec.text,
            ...(typeof rec.durationMs === 'number' && Number.isFinite(rec.durationMs) ? { durationMs: rec.durationMs } : {}),
          }
        }
      }
    }
    return result
  } catch {
    return {}
  }
}

export function writeCapturedReasoning(sessionId: string, map: Record<string, CapturedReasoningEntry>): void {
  if (!sessionId || typeof localStorage === 'undefined') return
  try {
    const keys = Object.keys(map)
    if (keys.length === 0) {
      localStorage.removeItem(`${REASONING_STORAGE_PREFIX}${sessionId}`)
      return
    }
    const keptKeys = keys.slice(-MAX_STORED_REASONING_ENTRIES)
    const pruned: Record<string, CapturedReasoningEntry> = {}
    let totalChars = 0
    for (let i = keptKeys.length - 1; i >= 0; i--) {
      const k = keptKeys[i]
      const entry = map[k]
      if (!entry?.text?.trim()) continue
      if (totalChars + entry.text.length > MAX_STORED_REASONING_CHARS && Object.keys(pruned).length > 0) break
      totalChars += entry.text.length
      pruned[k] = entry
    }
    // Preserve insertion order of kept keys
    const ordered: Record<string, CapturedReasoningEntry> = {}
    for (const k of keptKeys) {
      if (Object.hasOwn(pruned, k)) ordered[k] = pruned[k]
    }
    localStorage.setItem(`${REASONING_STORAGE_PREFIX}${sessionId}`, JSON.stringify(ordered))
  } catch {
    /* ignore storage quota errors */
  }
}

export function clearCapturedReasoning(sessionId: string): void {
  if (!sessionId || typeof localStorage === 'undefined') return
  try {
    localStorage.removeItem(`${REASONING_STORAGE_PREFIX}${sessionId}`)
  } catch {
    /* ignore */
  }
}

/**
 * Injects locally captured reasoning blocks before the corresponding assistant/tool turn
 * when Core's portable Transcript omits reasoning items, preserving chronological order.
 */
export function mergeCapturedReasoning(
  entries: TimelineEntry[],
  captured: Record<string, CapturedReasoningEntry>,
): TimelineEntry[] {
  const capturedKeys = Object.keys(captured)
  if (capturedKeys.length === 0 || entries.length === 0) return entries

  const out: TimelineEntry[] = []
  let turnStartIdx = 0
  let sawReasoningInTurn = false

  for (const entry of entries) {
    if (entry.kind === 'message' && entry.role === 'user') {
      out.push(entry)
      turnStartIdx = out.length
      sawReasoningInTurn = false
      continue
    }
    if (entry.kind === 'reasoning') {
      sawReasoningInTurn = true
      out.push(entry)
      continue
    }
    if (!sawReasoningInTurn && Object.hasOwn(captured, entry.id)) {
      const rec = captured[entry.id]
      if (rec?.text?.trim()) {
        out.splice(turnStartIdx, 0, {
          kind: 'reasoning',
          id: `captured-${entry.id}`,
          text: rec.text,
          ...(rec.durationMs !== undefined ? { durationMs: rec.durationMs } : {}),
        })
        sawReasoningInTurn = true
      }
    }
    out.push(entry)
  }

  return out
}

function advanceSlice(target: string, displayed: string, dt: number): string {
  if (displayed.length >= target.length) return displayed
  const remaining = target.length - displayed.length
  const alpha = 1 - Math.exp(-26 * dt)
  const step = Math.max(1, Math.ceil(remaining * alpha))
  let nextIdx = Math.min(target.length, displayed.length + step)
  // Never split a UTF-16 surrogate pair (emoji / extended CJK) across frames.
  if (nextIdx > 0 && nextIdx < target.length) {
    const code = target.charCodeAt(nextIdx - 1)
    if (code >= 0xd800 && code <= 0xdbff) {
      nextIdx += 1
    }
  }
  return target.slice(0, nextIdx)
}

function prefersReducedMotion(): boolean {
  return typeof window !== 'undefined' && Boolean(window.matchMedia?.('(prefers-reduced-motion: reduce)').matches)
}

export class StreamStore {
  private targetText = ''
  private targetReasoning = ''
  private displayedText = ''
  private displayedReasoning = ''
  private snapshot: StreamSnapshot = EMPTY_SNAPSHOT
  private listeners = new Set<() => void>()
  private rafId: number | null = null
  private lastTs = 0
  private active = false
  private onActiveChange?: (active: boolean) => void

  constructor(onActiveChange?: (active: boolean) => void) {
    this.onActiveChange = onActiveChange
  }

  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener)
    return () => {
      this.listeners.delete(listener)
    }
  }

  getSnapshot = (): StreamSnapshot => this.snapshot

  getTargetReasoning(): string {
    return this.targetReasoning
  }

  appendReasoningBreak(): void {
    if (this.targetReasoning.trim() && !this.targetReasoning.endsWith('\n\n')) {
      this.appendReasoning('\n\n')
    }
  }

  appendText(delta: string): void {
    if (!delta) return
    this.targetText += delta
    this.notifyActive(true)
    if (typeof document !== 'undefined' && (document.hidden || prefersReducedMotion())) {
      this.flushImmediate()
      return
    }
    this.kick()
  }

  appendReasoning(delta: string): void {
    if (!delta) return
    this.targetReasoning += delta
    this.notifyActive(true)
    if (typeof document !== 'undefined' && (document.hidden || prefersReducedMotion())) {
      this.flushImmediate()
      return
    }
    this.kick()
  }

  flushImmediate(): void {
    this.cancelRaf()
    if (this.displayedText === this.targetText && this.displayedReasoning === this.targetReasoning) {
      if (this.snapshot.isCatchingUp) this.emit()
      return
    }
    this.displayedText = this.targetText
    this.displayedReasoning = this.targetReasoning
    this.emit()
  }

  clear(): void {
    this.cancelRaf()
    const hadAnything =
      this.active ||
      this.targetText.length > 0 ||
      this.targetReasoning.length > 0 ||
      this.displayedText.length > 0 ||
      this.displayedReasoning.length > 0
    this.targetText = ''
    this.targetReasoning = ''
    this.displayedText = ''
    this.displayedReasoning = ''
    if (hadAnything) {
      this.snapshot = EMPTY_SNAPSHOT
      this.broadcast()
    }
    this.notifyActive(false)
  }

  dispose(): void {
    this.cancelRaf()
    this.listeners.clear()
  }

  private notifyActive(next: boolean): void {
    if (this.active === next) return
    this.active = next
    this.onActiveChange?.(next)
  }

  private cancelRaf(): void {
    if (this.rafId !== null && typeof window !== 'undefined') {
      window.cancelAnimationFrame(this.rafId)
    }
    this.rafId = null
    this.lastTs = 0
  }

  private kick(): void {
    if (this.rafId !== null) return
    if (typeof window === 'undefined' || typeof window.requestAnimationFrame !== 'function') {
      this.flushImmediate()
      return
    }
    this.lastTs = 0
    this.rafId = window.requestAnimationFrame(this.tick)
  }

  private tick = (ts: number): void => {
    this.rafId = null
    if (typeof document !== 'undefined' && (document.hidden || prefersReducedMotion())) {
      this.displayedText = this.targetText
      this.displayedReasoning = this.targetReasoning
      this.lastTs = 0
      this.emit()
      return
    }

    const dt = this.lastTs ? Math.min(0.05, Math.max(0.001, (ts - this.lastTs) / 1000)) : 1 / 120
    this.lastTs = ts

    const nextReasoning = advanceSlice(this.targetReasoning, this.displayedReasoning, dt)
    const nextText = advanceSlice(this.targetText, this.displayedText, dt)
    const stillPending = nextReasoning.length < this.targetReasoning.length || nextText.length < this.targetText.length

    if (stillPending) {
      this.rafId = window.requestAnimationFrame(this.tick)
    } else {
      this.lastTs = 0
    }

    if (nextReasoning !== this.displayedReasoning || nextText !== this.displayedText || this.snapshot.isCatchingUp !== stillPending) {
      this.displayedReasoning = nextReasoning
      this.displayedText = nextText
      this.emit()
    }
  }

  private emit(): void {
    if (!this.displayedText && !this.displayedReasoning && this.rafId === null) {
      this.snapshot = EMPTY_SNAPSHOT
    } else {
      this.snapshot = {
        text: this.displayedText,
        reasoning: this.displayedReasoning,
        isCatchingUp: this.rafId !== null,
      }
    }
    this.broadcast()
  }

  private broadcast(): void {
    for (const listener of this.listeners) {
      listener()
    }
  }
}
