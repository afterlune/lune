import type {
  LiveAgentRun,
  LiveAgentToolStep,
  ModelOption,
  RuntimeState,
  SessionSummary,
  TimelineEntry,
  UsageRecord,
  UsageTotals,
} from './harness-types'
import type { LiveContextOverride } from './usage-metrics'

export type AgentStatus = 'idle' | 'queued' | 'running' | 'paused' | 'completed' | 'failed' | 'cancelled'

export interface AgentSnapshot {
  id: string
  name: string
  role: string
  isLead: boolean
  depth: number
  status: AgentStatus
  activity: string
  task: string
  teamRunId?: string
  contextMode?: string
  provider?: string
  modelProfile?: string
  model?: string
  currentTool?: string
  toolCount: number
  tools: LiveAgentToolStep[]
  startedAt?: number
  durationMs?: number
  error?: string
  reasoningText: string
  outputText: string
  preview: string
  tokensPerSecond?: number
  inputTokens: number
  outputTokens: number
  cachedTokens: number
  cachePct: number
  reasoningTokens: number
  totalTokens: number
  usedContext: number
  contextWindow: number
  contextPct: number
}

export interface HistoricalSubagentResult {
  id: string
  name: string
  role: string
  task: string
  modelProfile?: string
  model?: string
  contextMode?: string
  status: 'completed' | 'failed' | 'cancelled'
  outputText: string
  error?: string
}

function compactSingleLine(text: string, maxLen = 220): string {
  const collapsed = text.replace(/\s+/g, ' ').trim()
  if (collapsed.length <= maxLen) return collapsed
  return collapsed.slice(0, Math.max(1, maxLen - 1)).trimEnd() + '…'
}

function latestLinePreview(text: string): string {
  const lines = text.split('\n')
  for (let i = lines.length - 1; i >= 0; i--) {
    const trimmed = lines[i].trim()
    if (trimmed && !trimmed.startsWith('```')) {
      return compactSingleLine(trimmed, 160)
    }
  }
  return ''
}

function isLeadRecord(agentId?: string, purpose?: string): boolean {
  const agent = (agentId ?? '').trim()
  if (agent !== '' && agent !== 'lead') return false
  const p = (purpose ?? '').trim()
  return p === '' || p === 'main'
}

export function parseDelegateTeamHistory(timeline: TimelineEntry[]): HistoricalSubagentResult[] {
  const byId = new Map<string, HistoricalSubagentResult>()

  for (const entry of timeline) {
    if (entry.kind !== 'tool_round') continue
    for (const call of entry.calls) {
      if (call.name !== 'delegate_team') continue

      const taskMap = new Map<string, { task: string; model?: string }>()
      try {
        const args = JSON.parse(call.arguments || '{}') as {
          tasks?: Array<{ id?: unknown; model?: unknown; task?: unknown }>
        }
        if (Array.isArray(args.tasks)) {
          for (const item of args.tasks) {
            const memberId = typeof item.id === 'string' ? item.id.trim() : ''
            if (!memberId) continue
            taskMap.set(memberId, {
              task: typeof item.task === 'string' ? item.task : '',
              model: typeof item.model === 'string' ? item.model.trim() : undefined,
            })
          }
        }
      } catch {
        /* malformed arguments JSON; output alone can still describe members */
      }

      const rawOutput = (call.output ?? '').trim()
      const cleaned = rawOutput.replace(/\nDelegation remaining for this agent:[^\n]*$/, '').trim()
      if (cleaned.startsWith('Team results:')) {
        const body = cleaned.slice('Team results:'.length).trim()
        const sections = body ? body.split(/\n\n## /) : []
        for (let idx = 0; idx < sections.length; idx++) {
          const section = idx === 0 && sections[idx].startsWith('## ')
            ? sections[idx].slice(3)
            : sections[idx]
          const nl = section.indexOf('\n')
          const header = (nl < 0 ? section : section.slice(0, nl)).trim()
          let rest = nl < 0 ? '' : section.slice(nl + 1).trim()
          const headerMatch = /^(.+?)\s+\(model\s+([^)]*)\)$/.exec(header)
          const memberId = (headerMatch ? headerMatch[1] : header).trim()
          if (!memberId) continue
          const reportedModel = headerMatch ? headerMatch[2].trim() : undefined

          let contextMode: string | undefined
          if (rest.startsWith('Context: forked')) {
            contextMode = 'forked'
            rest = rest.slice('Context: forked'.length).trim()
          } else if (rest.startsWith('Context: task-only')) {
            contextMode = 'task-only'
            rest = rest.slice('Context: task-only'.length).trim()
          }

          let status: 'completed' | 'failed' | 'cancelled' = 'completed'
          let error: string | undefined
          let outputText = rest
          if (rest.startsWith('Failed:')) {
            error = rest.slice('Failed:'.length).trim()
            status = /cancel/i.test(error) ? 'cancelled' : 'failed'
            outputText = ''
          }

          const meta = taskMap.get(memberId)
          const fullId = memberId.startsWith('lead/') ? memberId : `lead/${memberId}`
          byId.set(fullId, {
            id: fullId,
            name: memberId,
            role: 'worker',
            task: meta?.task ?? '',
            modelProfile: meta?.model,
            model: reportedModel || meta?.model,
            contextMode,
            status,
            outputText,
            error,
          })
        }
      }

      for (const [memberId, meta] of taskMap) {
        const fullId = memberId.startsWith('lead/') ? memberId : `lead/${memberId}`
        if (!byId.has(fullId)) {
          byId.set(fullId, {
            id: fullId,
            name: memberId,
            role: 'worker',
            task: meta.task,
            modelProfile: meta.model,
            model: meta.model,
            status: call.failed ? 'failed' : 'completed',
            outputText: rawOutput,
            error: call.failed ? rawOutput || 'Delegation failed' : undefined,
          })
        }
      }
    }
  }

  return [...byId.values()]
}

function contextWindowForProfile(models: ModelOption[], profile?: string): number {
  if (!profile) return 0
  const needle = profile.trim().toLowerCase()
  return models.find((m) => m.profile.trim().toLowerCase() === needle)?.contextWindow ?? 0
}

function emptyAgentUsage(): UsageTotals {
  return { requests: 0, measured: 0, input: 0, estimated_input: 0, output: 0, reasoning: 0, total: 0, cached: 0 }
}

function accumulateRecordUsage(acc: UsageTotals, rec: UsageRecord): UsageTotals {
  const input = rec.usage?.input_tokens ?? 0
  const output = rec.usage?.output_tokens ?? 0
  const cached = rec.usage?.input_tokens_details?.cached_tokens ?? 0
  const reasoning = rec.usage?.output_tokens_details?.reasoning_tokens ?? 0
  const total = rec.usage?.total_tokens ?? (input + output)
  const est = input > 0 ? 0 : (rec.estimated_input_tokens ?? 0)
  return {
    requests: acc.requests + 1,
    measured: acc.measured + (rec.available ? 1 : 0),
    input: acc.input + input,
    estimated_input: (acc.estimated_input ?? 0) + est,
    output: acc.output + output,
    reasoning: acc.reasoning + reasoning,
    total: acc.total + total,
    cached: acc.cached + cached,
  }
}

export function deriveAgentSnapshots(params: {
  session: SessionSummary | null
  timeline: TimelineEntry[]
  liveAgents: LiveAgentRun[]
  runtime: RuntimeState
  models: ModelOption[]
  taskIsRunning: boolean
  liveContext?: LiveContextOverride
}): AgentSnapshot[] {
  const { session, timeline, liveAgents, runtime, models, taskIsRunning, liveContext } = params
  const usageRequests = session?.usageRequests ?? []

  // Latest user message becomes Lead's active/recent task summary
  let latestUserTask = ''
  for (let i = timeline.length - 1; i >= 0; i--) {
    const entry = timeline[i]
    if (entry.kind === 'message' && entry.role === 'user' && entry.text.trim()) {
      latestUserTask = compactSingleLine(entry.text, 220)
      break
    }
  }

  // Group usageRequests by agent_id
  const requestsByAgent = new Map<string, UsageRecord[]>()
  let latestLeadReq: UsageRecord | undefined
  for (const rec of usageRequests) {
    const key = (rec.agent_id ?? '').trim() || 'lead'
    const list = requestsByAgent.get(key)
    if (list) list.push(rec)
    else requestsByAgent.set(key, [rec])
    if (isLeadRecord(rec.agent_id, rec.purpose)) {
      latestLeadReq = rec
    }
  }

  // Lead metrics
  const leadTotals = session?.agentUsage?.lead ?? (session?.sessionUsage && !Object.keys(session.agentUsage ?? {}).length ? session.sessionUsage : undefined)
  const leadInput = leadTotals?.input ?? 0
  const leadOutput = leadTotals?.output ?? 0
  const leadCached = leadTotals?.cached ?? 0
  const leadReasoning = leadTotals?.reasoning ?? 0
  const leadTotal = leadTotals ? (leadTotals.total > 0 ? leadTotals.total : leadInput + leadOutput) : 0
  const leadCachePct = leadInput > 0 ? Math.min(100, Math.round((leadCached / leadInput) * 100)) : 0
  const leadModelProfile = runtime.task.modelProfile || session?.settings.modelProfile || models[0]?.profile || ''
  const leadContextWindow =
    latestLeadReq?.context_window ||
    runtime.task.contextWindow ||
    session?.settings.contextWindow ||
    contextWindowForProfile(models, leadModelProfile) ||
    0
  let leadUsedContext = 0
  if (liveContext?.afterTokens && liveContext.afterTokens > 0) leadUsedContext = liveContext.afterTokens
  else if (latestLeadReq) leadUsedContext = (latestLeadReq.usage?.input_tokens ?? 0) > 0 ? (latestLeadReq.usage?.input_tokens ?? 0) : (latestLeadReq.estimated_input_tokens ?? 0)
  else if (leadTotals && leadTotals.requests === 1 && leadTotals.input > 0) leadUsedContext = leadTotals.input
  const leadContextPct = leadContextWindow > 0 ? Math.min(100, Math.round((leadUsedContext / leadContextWindow) * 100)) : 0

  const leadSnapshot: AgentSnapshot = {
    id: 'lead',
    name: 'Main agent',
    role: 'main',
    isLead: true,
    depth: 0,
    status: taskIsRunning
      ? 'running'
      : runtime.task.status === 'paused'
        ? 'paused'
        : runtime.task.status === 'queued' || runtime.queue.length > 0
          ? 'queued'
          : 'idle',
    activity: taskIsRunning
      ? (liveContext?.compacting ? 'Compacting' : 'Working')
      : runtime.task.status === 'paused'
        ? 'Paused'
        : runtime.task.status === 'queued' || runtime.queue.length > 0
          ? 'Queued'
          : 'Idle',
    task: latestUserTask,
    provider: runtime.task.provider || latestLeadReq?.provider,
    modelProfile: leadModelProfile,
    model: runtime.task.model || latestLeadReq?.model || models.find((m) => m.profile === leadModelProfile)?.model,
    toolCount: 0,
    tools: [],
    reasoningText: '',
    outputText: '',
    preview: latestUserTask,
    inputTokens: leadInput,
    outputTokens: leadOutput,
    cachedTokens: leadCached,
    cachePct: leadCachePct,
    reasoningTokens: leadReasoning,
    totalTokens: leadTotal,
    usedContext: leadUsedContext,
    contextWindow: leadContextWindow,
    contextPct: leadContextPct,
  }

  // Build subagent entries keyed by canonical subagent ID
  const subMap = new Map<string, AgentSnapshot>()
  const resolveSubKey = (rawId: string): string => {
    const clean = rawId.trim()
    if (!clean) return clean
    if (subMap.has(clean)) return clean
    if (subMap.has(`lead/${clean}`)) return `lead/${clean}`
    for (const existingKey of subMap.keys()) {
      if (existingKey.endsWith(`/${clean}`)) return existingKey
    }
    return clean
  }

  // 1) Historical delegate_team tool outputs
  for (const hist of parseDelegateTeamHistory(timeline)) {
    const preview = latestLinePreview(hist.outputText) || compactSingleLine(hist.error || hist.task, 160)
    subMap.set(hist.id, {
      id: hist.id,
      name: hist.name,
      role: hist.role,
      isLead: false,
      depth: Math.max(1, hist.id.split('/').length - 1),
      status: hist.status,
      activity: hist.error ? `Failed: ${compactSingleLine(hist.error, 100)}` : 'Completed',
      task: hist.task,
      contextMode: hist.contextMode,
      modelProfile: hist.modelProfile,
      model: hist.model,
      toolCount: 0,
      tools: [],
      error: hist.error,
      reasoningText: '',
      outputText: hist.outputText,
      preview,
      inputTokens: 0,
      outputTokens: 0,
      cachedTokens: 0,
      cachePct: 0,
      reasoningTokens: 0,
      totalTokens: 0,
      usedContext: 0,
      contextWindow: contextWindowForProfile(models, hist.modelProfile) || leadContextWindow,
      contextPct: 0,
    })
  }

  // 2) Overlay live running / finished agents from SSE/WS events
  for (const live of liveAgents) {
    const id = live.id.trim()
    if (!id || id === 'lead') continue
    const existing = subMap.get(resolveSubKey(id))
    const status: AgentStatus = live.status
    const outputText = live.outputText ?? existing?.outputText ?? ''
    const reasoningText = live.reasoningText ?? existing?.reasoningText ?? ''
    const activity =
      live.activity ||
      (live.currentTool ? `Running ${live.currentTool}` : status === 'running' ? 'Working' : status === 'failed' ? 'Failed' : status === 'cancelled' ? 'Cancelled' : 'Completed')
    const preview =
      latestLinePreview(outputText) ||
      latestLinePreview(reasoningText) ||
      compactSingleLine(live.error || activity || live.task || existing?.task || '', 160)

    subMap.set(existing?.id ?? id, {
      ...(existing ?? {
        role: 'worker',
        isLead: false,
        depth: Math.max(1, id.split('/').length - 1),
        task: '',
        toolCount: 0,
        tools: [],
        reasoningText: '',
        outputText: '',
        inputTokens: 0,
        outputTokens: 0,
        cachedTokens: 0,
        cachePct: 0,
        reasoningTokens: 0,
        totalTokens: 0,
        usedContext: 0,
        contextWindow: 0,
        contextPct: 0,
      }),
      id: existing?.id ?? id,
      name: live.name || existing?.name || id.split('/').at(-1) || id,
      role: live.role || existing?.role || 'worker',
      task: live.task || existing?.task || '',
      teamRunId: live.teamRunId || existing?.teamRunId,
      status,
      activity,
      currentTool: live.currentTool,
      toolCount: Math.max(live.toolCount ?? 0, live.tools?.length ?? 0, existing?.toolCount ?? 0),
      tools: live.tools && live.tools.length > 0 ? live.tools : (existing?.tools ?? []),
      startedAt: live.startedAt || existing?.startedAt,
      durationMs: live.durationMs ?? existing?.durationMs,
      error: live.error ?? existing?.error,
      provider: live.provider || existing?.provider,
      modelProfile: live.modelProfile || existing?.modelProfile,
      model: live.model || existing?.model,
      contextWindow: live.contextWindow || existing?.contextWindow || contextWindowForProfile(models, live.modelProfile || existing?.modelProfile) || leadContextWindow,
      reasoningText,
      outputText,
      preview,
      tokensPerSecond: live.tokensPerSecond ?? existing?.tokensPerSecond,
    })
  }

  // 3) Discover any subagents known only from agentUsage / usageRequests and attach usage stats
  const usageKeys = new Set<string>()
  for (const key of Object.keys(session?.agentUsage ?? {})) {
    if (key && key !== 'lead') usageKeys.add(key)
  }
  for (const key of requestsByAgent.keys()) {
    if (key && key !== 'lead') usageKeys.add(key)
  }

  for (const key of usageKeys) {
    const resolved = resolveSubKey(key)
    const reqs = requestsByAgent.get(key) ?? []
    const lastReq = reqs.at(-1)
    let totals = session?.agentUsage?.[key]
    if (!totals && reqs.length > 0) {
      totals = reqs.reduce(accumulateRecordUsage, emptyAgentUsage())
    }
    const existing = subMap.get(resolved)
    const inputTokens = totals?.input ?? existing?.inputTokens ?? 0
    const outputTokens = totals?.output ?? existing?.outputTokens ?? 0
    const cachedTokens = totals?.cached ?? existing?.cachedTokens ?? 0
    const reasoningTokens = totals?.reasoning ?? existing?.reasoningTokens ?? 0
    const totalTokens = totals ? (totals.total > 0 ? totals.total : inputTokens + outputTokens) : (existing?.totalTokens ?? 0)
    const cachePct = inputTokens > 0 ? Math.min(100, Math.round((cachedTokens / inputTokens) * 100)) : 0
    const usedContext = lastReq
      ? ((lastReq.usage?.input_tokens ?? 0) > 0 ? (lastReq.usage?.input_tokens ?? 0) : (lastReq.estimated_input_tokens ?? 0))
      : (inputTokens > 0 && totals?.requests === 1 ? inputTokens : (totals?.estimated_input ?? existing?.usedContext ?? 0))
    const profile = existing?.modelProfile || lastReq?.model_profile
    const contextWindow =
      lastReq?.context_window ||
      existing?.contextWindow ||
      contextWindowForProfile(models, profile) ||
      leadContextWindow
    const contextPct = contextWindow > 0 ? Math.min(100, Math.round((usedContext / contextWindow) * 100)) : 0

    if (existing) {
      existing.inputTokens = inputTokens
      existing.outputTokens = outputTokens
      existing.cachedTokens = cachedTokens
      existing.cachePct = cachePct
      existing.reasoningTokens = reasoningTokens
      existing.totalTokens = totalTokens
      existing.usedContext = usedContext
      existing.contextWindow = contextWindow
      existing.contextPct = contextPct
      if (!existing.provider && lastReq?.provider) existing.provider = lastReq.provider
      if (!existing.modelProfile && lastReq?.model_profile) existing.modelProfile = lastReq.model_profile
      if (!existing.model && lastReq?.model) existing.model = lastReq.model
    } else {
      const shortName = key.split('/').at(-1) || key
      subMap.set(key, {
        id: key,
        name: shortName,
        role: 'worker',
        isLead: false,
        depth: Math.max(1, key.split('/').length - 1),
        status: taskIsRunning ? 'running' : 'completed',
        activity: taskIsRunning ? 'Working' : 'Completed',
        task: '',
        provider: lastReq?.provider,
        modelProfile: lastReq?.model_profile,
        model: lastReq?.model,
        toolCount: 0,
        tools: [],
        reasoningText: '',
        outputText: '',
        preview: '',
        inputTokens,
        outputTokens,
        cachedTokens,
        cachePct,
        reasoningTokens,
        totalTokens,
        usedContext,
        contextWindow,
        contextPct,
      })
    }
  }

  // Attach request stats for subagents whose ID differed only by prefix
  for (const agent of subMap.values()) {
    if (agent.totalTokens === 0 && !agent.id.startsWith('lead/')) continue
    if (agent.totalTokens === 0 && agent.id.startsWith('lead/')) {
      const shortId = agent.id.slice('lead/'.length)
      const directReqs = requestsByAgent.get(agent.id) ?? requestsByAgent.get(shortId) ?? []
      const totals = session?.agentUsage?.[agent.id] ?? session?.agentUsage?.[shortId]
      if (totals) {
        const lastReq = directReqs.at(-1)
        agent.inputTokens = totals.input
        agent.outputTokens = totals.output
        agent.cachedTokens = totals.cached
        agent.reasoningTokens = totals.reasoning
        agent.totalTokens = totals.total > 0 ? totals.total : totals.input + totals.output
        agent.cachePct = totals.input > 0 ? Math.min(100, Math.round((totals.cached / totals.input) * 100)) : 0
        if (lastReq) {
          agent.usedContext = (lastReq.usage?.input_tokens ?? 0) > 0 ? (lastReq.usage?.input_tokens ?? 0) : (lastReq.estimated_input_tokens ?? 0)
          agent.contextWindow = lastReq.context_window || agent.contextWindow || leadContextWindow
          agent.contextPct = agent.contextWindow > 0 ? Math.min(100, Math.round((agent.usedContext / agent.contextWindow) * 100)) : 0
        }
      }
    }
  }

  const subagents = [...subMap.values()].sort((a, b) => {
    const aRunning = a.status === 'running' ? 0 : 1
    const bRunning = b.status === 'running' ? 0 : 1
    if (aRunning !== bRunning) return aRunning - bRunning
    if (a.startedAt && b.startedAt) return a.startedAt - b.startedAt
    return a.id.localeCompare(b.id)
  })

  return [leadSnapshot, ...subagents]
}
