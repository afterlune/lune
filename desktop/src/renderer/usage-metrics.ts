import type { ModelOption, RuntimeEvent, RuntimeState, SessionSummary, UsageRecord } from './harness-types'

export function formatTokenCount(value: number | undefined | null): string {
  if (value === undefined || value === null || !Number.isFinite(value) || value <= 0) return '0'
  const n = Math.max(0, value)
  if (n < 1_000) return String(Math.round(n))
  if (n >= 999_950) {
    const m = Math.round((n / 1_000_000) * 10) / 10
    return `${Number.isInteger(m) ? m.toFixed(0) : m.toFixed(1)}M`
  }
  const k = Math.round((n / 1_000) * 10) / 10
  return `${Number.isInteger(k) ? k.toFixed(0) : k.toFixed(1)}K`
}

export function isLeadMainUsage(agentId?: string, purpose?: string): boolean {
  const agent = (agentId ?? '').trim()
  if (agent !== '' && agent !== 'lead') return false
  const p = (purpose ?? '').trim()
  return p === '' || p === 'main'
}

export function latestLeadUsageRecord(records?: UsageRecord[]): UsageRecord | undefined {
  if (!records) return undefined
  for (let i = records.length - 1; i >= 0; i--) {
    const record = records[i]
    if (isLeadMainUsage(record.agent_id, record.purpose)) return record
  }
  return undefined
}

function extractEventUsageNumbers(event: RuntimeEvent) {
  const u = event.usage ?? {}
  const input = u.input_tokens ?? u.input ?? 0
  const output = u.output_tokens ?? u.output ?? 0
  const cached = u.input_tokens_details?.cached_tokens ?? u.cached ?? 0
  const cacheCreation = u.input_tokens_details?.cache_creation_tokens ?? u.cache_creation ?? 0
  const reasoning = u.output_tokens_details?.reasoning_tokens ?? u.reasoning ?? 0
  const rawTotal = u.total_tokens ?? u.total ?? 0
  const total = rawTotal > 0 ? rawTotal : input + output
  const estimatedInput = input > 0 ? 0 : Math.max(0, event.estimatedInputTokens ?? 0)
  return { input, output, cached, cacheCreation, reasoning, total, estimatedInput, available: input > 0 || output > 0 || total > 0 }
}

export function usageEventDedupKey(event: RuntimeEvent): string | null {
  if (event.requestId) return `req:${event.requestId}:${event.requestAttempt ?? 0}:${event.agentId ?? ''}:${event.usagePurpose ?? ''}`
  if (event.cursor && event.cursor > 0) return `cursor:${event.cursor}`
  return null
}

export function seedAppliedUsageKeys(session: SessionSummary): Set<string> {
  const keys = new Set<string>()
  for (const r of session.usageRequests ?? []) {
    if (r.request_id) keys.add(`req:${r.request_id}:${r.request_attempt ?? 0}:${r.agent_id ?? ''}:${r.purpose ?? ''}`)
  }
  return keys
}

export function applyUsageEvent(session: SessionSummary, event: RuntimeEvent): SessionSummary {
  const nums = extractEventUsageNumbers(event)
  const cur = session.sessionUsage ?? { requests: 0, measured: 0, input: 0, output: 0, reasoning: 0, total: 0, cached: 0 }
  const sessionUsage = {
    ...cur,
    requests: cur.requests + 1,
    measured: cur.measured + (nums.available ? 1 : 0),
    input: cur.input + nums.input,
    estimated_input: (cur.estimated_input ?? 0) + nums.estimatedInput,
    output: cur.output + nums.output,
    reasoning: cur.reasoning + nums.reasoning,
    total: (cur.total || (cur.input + cur.output)) + nums.total,
    cached: cur.cached + nums.cached,
    cache_creation: (cur.cache_creation ?? 0) + nums.cacheCreation,
  }
  const record: UsageRecord = {
    request_id: event.requestId,
    request_attempt: event.requestAttempt,
    request_changes: event.requestChanges,
    request: (session.usageRequests?.length ?? 0) + 1,
    usage: {
      input_tokens: nums.input,
      input_tokens_details: {
        cached_tokens: nums.cached,
        ...(nums.cacheCreation > 0 ? { cache_creation_tokens: nums.cacheCreation } : {}),
      },
      output_tokens: nums.output,
      output_tokens_details: { reasoning_tokens: nums.reasoning },
      total_tokens: nums.total,
    },
    estimated_input_tokens: nums.estimatedInput || undefined,
    available: nums.available,
    context_window: event.contextWindow && event.contextWindow > 0 ? event.contextWindow : undefined,
    agent_id: event.agentId,
    purpose: event.usagePurpose,
  }
  return { ...session, sessionUsage, usageRequests: [...(session.usageRequests ?? []), record] }
}

export interface LiveContextOverride {
  afterTokens?: number
  compacting: boolean
}

export interface SessionUsageMetrics {
  hasUsage: boolean
  input: number
  output: number
  cached: number
  cachePct: number
  reasoning: number
  total: number
  usedContext: number
  contextWindow: number
  contextPct: number
  compacting: boolean
}

export function deriveSessionUsageMetrics(params: {
  session: SessionSummary | null
  runtime: RuntimeState
  modelOption?: ModelOption
  liveContext?: LiveContextOverride
}): SessionUsageMetrics {
  const { session, runtime, modelOption, liveContext } = params
  const usage = session?.sessionUsage
  const input = usage?.input ?? 0
  const output = usage?.output ?? 0
  const cached = usage?.cached ?? 0
  const reasoning = usage?.reasoning ?? 0
  const total = usage ? (usage.total > 0 ? usage.total : input + output) : 0
  const cachePct = input > 0 ? Math.min(100, Math.round((cached / input) * 100)) : 0
  const latestLead = latestLeadUsageRecord(session?.usageRequests)
  const leadInput = latestLead
    ? ((latestLead.usage?.input_tokens ?? 0) > 0 ? (latestLead.usage?.input_tokens ?? 0) : (latestLead.estimated_input_tokens ?? 0))
    : 0
  let usedContext = 0
  if (liveContext?.afterTokens && liveContext.afterTokens > 0) usedContext = liveContext.afterTokens
  else if (latestLead) usedContext = leadInput
  else if (usage && usage.requests === 1 && usage.input > 0) usedContext = usage.input
  else if (usage && (usage.estimated_input ?? 0) > 0) usedContext = usage.estimated_input ?? 0
  const contextWindow = latestLead?.context_window || runtime.task.contextWindow || session?.settings.contextWindow || modelOption?.contextWindow || 0
  const contextPct = contextWindow > 0 ? Math.min(100, Math.round((usedContext / contextWindow) * 100)) : 0
  const compacting = Boolean(liveContext?.compacting)
  const hasUsage = Boolean(usage && (usage.requests > 0 || input > 0 || output > 0 || total > 0 || cached > 0)) || usedContext > 0 || compacting
  return { hasUsage, input, output, cached, cachePct, reasoning, total, usedContext, contextWindow, contextPct, compacting }
}
