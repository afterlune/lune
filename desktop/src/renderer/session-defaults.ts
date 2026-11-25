import type { ModelOption, SessionSummary } from './harness-types'

export const VALID_REASONING_EFFORTS = new Set([
  '',
  'none',
  'minimal',
  'low',
  'medium',
  'high',
  'xhigh',
  'max',
])

export interface LastSessionSettings {
  modelProfile?: string
  reasoningEffort?: string
  updatedAt: number
}

export type SessionDefaultsPatch = {
  modelProfile?: string
  reasoningEffort?: string
}

const STORAGE_KEY_PREFIX = 'lune:last-session-settings:v1:'
const GLOBAL_SCOPE_KEY = '__global__'

const memoryStore = new Map<string, LastSessionSettings>()

export function sessionDefaultsScopeKey(
  input: { mode?: string; workspace?: string; workspaceId?: string; url?: string } | null | undefined,
): string {
  if (!input) return GLOBAL_SCOPE_KEY
  if (input.mode === 'local' && input.workspace) {
    return `local:${input.workspace}`
  }
  if (input.mode === 'loopback') {
    return `loopback:${input.workspaceId || input.workspace || 'default'}`
  }
  if (input.mode === 'remote' && input.url) {
    return `remote:${input.url}`
  }
  return GLOBAL_SCOPE_KEY
}

export function sanitizeLastSessionSettings(raw: unknown): LastSessionSettings | null {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return null
  const record = raw as Record<string, unknown>
  const next: LastSessionSettings = {
    updatedAt: typeof record.updatedAt === 'number' && Number.isFinite(record.updatedAt) ? record.updatedAt : Date.now(),
  }
  let hasField = false

  if (typeof record.modelProfile === 'string') {
    const trimmed = record.modelProfile.trim()
    if (trimmed) {
      next.modelProfile = trimmed
      hasField = true
    }
  }

  if (Object.prototype.hasOwnProperty.call(record, 'reasoningEffort') && typeof record.reasoningEffort === 'string') {
    if (VALID_REASONING_EFFORTS.has(record.reasoningEffort)) {
      next.reasoningEffort = record.reasoningEffort
      hasField = true
    }
  }

  return hasField ? next : null
}

function readScopeRecord(scopeKey: string): LastSessionSettings | null {
  try {
    if (typeof localStorage !== 'undefined') {
      const raw = localStorage.getItem(`${STORAGE_KEY_PREFIX}${scopeKey}`)
      if (raw) {
        const parsed = sanitizeLastSessionSettings(JSON.parse(raw))
        if (parsed) {
          memoryStore.set(scopeKey, parsed)
          return parsed
        }
      }
    }
  } catch {
    // Ignore storage read errors and fall back to memoryStore.
  }
  return memoryStore.get(scopeKey) ?? null
}

function writeScopeRecord(scopeKey: string, value: LastSessionSettings): void {
  memoryStore.set(scopeKey, value)
  try {
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(`${STORAGE_KEY_PREFIX}${scopeKey}`, JSON.stringify(value))
    }
  } catch {
    // Ignore storage write errors (e.g. private browsing quota).
  }
}

function mergeScopeRecord(scopeKey: string, patch: SessionDefaultsPatch, updatedAt: number): LastSessionSettings | null {
  const existing = readScopeRecord(scopeKey)
  const merged: LastSessionSettings = {
    ...(existing?.modelProfile ? { modelProfile: existing.modelProfile } : {}),
    ...(existing && Object.prototype.hasOwnProperty.call(existing, 'reasoningEffort') && existing.reasoningEffort !== undefined
      ? { reasoningEffort: existing.reasoningEffort }
      : {}),
    updatedAt,
  }

  if (typeof patch.modelProfile === 'string') {
    const trimmed = patch.modelProfile.trim()
    if (trimmed) {
      merged.modelProfile = trimmed
    }
  }

  if (Object.prototype.hasOwnProperty.call(patch, 'reasoningEffort') && typeof patch.reasoningEffort === 'string') {
    if (VALID_REASONING_EFFORTS.has(patch.reasoningEffort)) {
      merged.reasoningEffort = patch.reasoningEffort
    }
  }

  const sanitized = sanitizeLastSessionSettings(merged)
  if (!sanitized) return null
  writeScopeRecord(scopeKey, sanitized)
  return sanitized
}

export function loadLastSessionSettings(scopeKey?: string): LastSessionSettings | null {
  const resolvedScope = scopeKey || GLOBAL_SCOPE_KEY
  const scoped = resolvedScope !== GLOBAL_SCOPE_KEY ? readScopeRecord(resolvedScope) : null
  const globalRecord = readScopeRecord(GLOBAL_SCOPE_KEY)

  if (!scoped && !globalRecord) return null
  if (!scoped) return globalRecord
  if (!globalRecord || resolvedScope === GLOBAL_SCOPE_KEY) return scoped

  const combined: LastSessionSettings = {
    updatedAt: Math.max(scoped.updatedAt, globalRecord.updatedAt),
  }
  if (scoped.modelProfile) {
    combined.modelProfile = scoped.modelProfile
  } else if (globalRecord.modelProfile) {
    combined.modelProfile = globalRecord.modelProfile
  }

  if (Object.prototype.hasOwnProperty.call(scoped, 'reasoningEffort') && scoped.reasoningEffort !== undefined) {
    combined.reasoningEffort = scoped.reasoningEffort
  } else if (Object.prototype.hasOwnProperty.call(globalRecord, 'reasoningEffort') && globalRecord.reasoningEffort !== undefined) {
    combined.reasoningEffort = globalRecord.reasoningEffort
  }

  return sanitizeLastSessionSettings(combined)
}

export function saveLastSessionSettings(
  scopeKey: string | undefined,
  patch: SessionDefaultsPatch,
): LastSessionSettings | null {
  const now = Date.now()
  const resolvedScope = scopeKey || GLOBAL_SCOPE_KEY
  const globalSaved = mergeScopeRecord(GLOBAL_SCOPE_KEY, patch, now)
  if (resolvedScope === GLOBAL_SCOPE_KEY) {
    return globalSaved
  }
  return mergeScopeRecord(resolvedScope, patch, now) ?? globalSaved
}

export function resolveNewSessionSettings(
  created: SessionSummary,
  saved: LastSessionSettings | null,
  availableModels: ModelOption[],
): { nextSettings: SessionSummary['settings']; changed: boolean } {
  const nextSettings: SessionSummary['settings'] = { ...created.settings }
  if (!saved) {
    return { nextSettings, changed: false }
  }

  if (saved.modelProfile && availableModels.some((model) => model.profile === saved.modelProfile)) {
    nextSettings.modelProfile = saved.modelProfile
  }

  if (
    Object.prototype.hasOwnProperty.call(saved, 'reasoningEffort') &&
    typeof saved.reasoningEffort === 'string' &&
    VALID_REASONING_EFFORTS.has(saved.reasoningEffort)
  ) {
    nextSettings.reasoningEffort = saved.reasoningEffort
  }

  const changed =
    nextSettings.modelProfile !== created.settings.modelProfile ||
    (nextSettings.reasoningEffort ?? '') !== (created.settings.reasoningEffort ?? '')

  return { nextSettings, changed }
}
