export type JsonObject = Record<string, unknown>

export type GoalStatus = 'active' | 'paused' | 'blocked' | 'budget_limited' | 'complete'

export interface Goal {
  id: string
  revision: number
  objective: string
  status: GoalStatus
  token_budget?: number
  tokens_used: number
  cached_tokens: number
  estimated?: boolean
  run_milliseconds: number
  stop_reason?: string
  empty_turns?: number
  created_at: string
  updated_at: string
}

export interface UsageTotals {
  requests: number
  measured: number
  input: number
  estimated_input?: number
  output: number
  reasoning: number
  total: number
  cached: number
  cache_creation?: number
}

export interface ModelUsage {
  input_tokens?: number
  input_tokens_details?: {
    cached_tokens?: number
    cache_creation_tokens?: number
  }
  output_tokens?: number
  output_tokens_details?: {
    reasoning_tokens?: number
  }
  total_tokens?: number
}

export interface UsageRecord {
  request_id?: string
  request_attempt?: number
  request_changes?: string[]
  request: number
  usage?: ModelUsage
  estimated_input_tokens?: number
  available: boolean
  context_window?: number
  agent_id?: string
  purpose?: string
  provider?: string
  model_profile?: string
  model?: string
}

export interface SessionSummary {
  id: string
  updatedAt: string
  dataVersion?: number
  title: string
  archived: boolean
  sessionUsage?: UsageTotals
  taskUsage?: UsageTotals
  usageRequests?: UsageRecord[]
  settings: {
    modelProfile: string
    reasoningEffort?: string
    contextWindow?: number
    autoCompact?: boolean
    autoCompactThreshold?: number
  }
}

export interface ModelOption {
  profile: string
  provider: string
  model: string
  displayName?: string
  contextWindow?: number
  supportsInputImages?: boolean
}

export interface TaskState {
  goalId?: string
  goalRevision?: number
  automaticGoal?: boolean
  status: string
  contextWindow?: number
  executionId?: string
  taskId?: string
  goal?: string
  modelProfile?: string
  provider?: string
  model?: string
  reasoningEffort?: string
}

export interface RuntimeState {
  goal?: Goal | null
  task: TaskState
  queue: Array<{ queueId: string; executionId: string; position: number; status: string }>
}

export interface ModelFailure {
  category: string
  status_code?: number
  code?: string
  attempts: number
  retry_not_before?: string
  stream_started?: boolean
}

export interface UserQuestionDraftResponse {
  question_id: string
  selected_option?: number
  notes?: string
  custom_answer?: string
  confirmed?: boolean
}

export interface UserQuestionDraft {
  current_question_id?: string
  responses?: UserQuestionDraftResponse[]
  raw_payload?: unknown
}

export interface Pending {
  kind: string
  approval?: {
    id: string
    agentId?: string
    tool: string
    arguments: unknown
    pathGrant?: { path: string; access: string }
    autoReviewReason?: string
  }
  userQuestion?: {
    id: string
    executionId: string
    runId: string
    invocationId: string
    callId: string
    questions?: Array<{
      id: string
      question: string
      options?: Array<{ label: string; description: string; recommended?: boolean }>
    }>
    draft?: UserQuestionDraft
    draftPayload?: unknown
  }
  interaction?: { id: string; serverName: string; request: unknown }
  toolRecovery?: { invocationId: string; tool: string; arguments: unknown }
  modelRecovery?: { failure?: ModelFailure }
  blocker?: { id: string; tool: string; reason: string }
}

export interface RuntimeEvent {
  kind: string
  cursor?: number
  occurredAt?: string
  status?: string
  executionId?: string
  taskId?: string
  commandId?: string
  commandStatus?: string
  commandExitCode?: number
  goal?: Goal | null
  error?: string
  modelFailure?: ModelFailure
  tool?: { id?: string; invocationId?: string; agentId?: string; name?: string; arguments?: unknown; failed?: boolean; canceled?: boolean; duration?: number }
  stream?: { type?: string; purpose?: string; delta?: string; toolName?: string }
  agentId?: string
  agentName?: string
  agentRole?: string
  agentTask?: string
  teamRunId?: string
  provider?: string
  modelProfile?: string
  model?: string
  usage?: ModelUsage & Partial<UsageTotals>
  usagePurpose?: string
  requestId?: string
  requestAttempt?: number
  requestChanges?: string[]
  contextWindow?: number
  estimatedInputTokens?: number
  compaction?: {
    stage?: string
    mode?: string
    reason?: string
    strategy?: string
    beforeTokens?: number
    afterTokens?: number
    threshold?: number
    turns?: number
    error?: string
  }
}

export interface LiveAgentRun {
  id: string
  name: string
  role?: string
  task?: string
  teamRunId?: string
  status: 'running' | 'completed' | 'failed'
  currentTool?: string
  toolCount: number
  startedAt: number
  durationMs?: number
  error?: string
}

export interface Activity {
  id: string
  label: string
  detail?: string
  failed?: boolean
}

export interface LiveToolStep {
  id: string
  name: string
  status: 'running' | 'completed' | 'failed'
  detail?: string
  durationMs?: number
}

export interface CommandInfo {
  command_text: string
  id: string
  session_id: string
  execution_id: string
  agent_id: string
  invocation_id: string
  task_id?: string
  background?: boolean
  status: string
  started_at: string
  finished_at?: string
  exit_code?: number
}

export interface CommandOutput {
  command: CommandInfo
  output: string
  cursor: number
  next_cursor: number
  end_cursor: number
  truncated?: boolean
  error?: string
}

export interface MCPServerStatus {
  name: string
  transport: string
  endpoint?: string
  command?: string
  enabled: boolean
  connected: boolean
  toolCount: number
  lastError?: string
}

export interface MCPPromptArgument {
  name: string
  description?: string
  required?: boolean
}

export interface MCPPrompt {
  name: string
  title?: string
  description?: string
  arguments?: MCPPromptArgument[]
}

export interface MCPPromptResult {
  description?: string
  input?: unknown
}

export interface MCPResource {
  uri: string
  name: string
  title?: string
  description?: string
  mimeType?: string
  size?: number
}

export interface MCPResourceTemplate {
  uriTemplate: string
  name: string
  title?: string
  description?: string
  mimeType?: string
}

export type HarnessTab = 'files' | 'goal' | 'processes' | 'terminal' | 'mcp' | 'context'

export interface HistoryAttachment {
  name: string
  image?: string
  text?: string
}

export interface ToolCallItem {
  callId: string
  name: string
  arguments: string
  output?: string
  failed?: boolean
}

export type TimelineEntry =
  | {
      kind: 'message'
      id: string
      role: 'user' | 'assistant'
      text: string
      attachments?: HistoryAttachment[]
    }
  | {
      kind: 'reasoning'
      id: string
      text: string
      durationMs?: number
    }
  | {
      kind: 'tool_round'
      id: string
      calls: ToolCallItem[]
    }

export interface HistoryMessage {
  id: string
  role: 'user' | 'assistant'
  text: string
  attachments?: HistoryAttachment[]
}

export interface OptimisticMessage extends HistoryMessage {
  baselineCount: number
  sessionId: string
  executionId?: string
}

export function objectValue(value: unknown): JsonObject {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as JsonObject : {}
}

export function historyItems(history: unknown): unknown[] {
  return Array.isArray(history)
    ? history
    : Array.isArray(objectValue(history).items)
      ? objectValue(history).items as unknown[]
      : []
}

export function safeImageURL(value: unknown): string | undefined {
  return typeof value === 'string' && /^data:image\/(png|jpeg);base64,[a-zA-Z0-9+/=]+$/.test(value)
    ? value
    : undefined
}

export function contentText(value: unknown): string {
  if (typeof value === 'string') return value
  if (Array.isArray(value)) {
    return value.map((part) => {
      const item = objectValue(part)
      if (typeof item.text === 'string') return item.text
      if (item.type === 'input_image' || item.type === 'image') return '[image]'
      if (item.type === 'refusal' && typeof item.refusal === 'string') return item.refusal
      return ''
    }).filter(Boolean).join('\n')
  }
  return ''
}

function extractReasoningText(item: JsonObject): string {
  const summary = contentText(item.summary)
  if (summary.trim()) return summary.trim()
  const content = contentText(item.content)
  if (content.trim()) return content.trim()
  if (typeof item.text === 'string' && item.text.trim()) return item.text.trim()
  return ''
}

export function extractProposedPlan(text: string): { before: string; plan: string; after: string } | null {
  if (!text.toLowerCase().includes('proposed_plan')) return null
  const tokens: string[] = []
  const protectedText = text.replace(/```[\s\S]*?```|`[^`\n]+`/g, (match) => {
    const id = tokens.length
    tokens.push(match)
    return `\x00CODE_${id}\x00`
  })
  const openTag = '<proposed_plan>'
  const closeTags = ['</proposed_plan>', '<proposed_plan_end/>']
  const openIndex = protectedText.indexOf(openTag)
  if (openIndex < 0) return null
  const contentStart = openIndex + openTag.length
  if (protectedText.indexOf(openTag, contentStart) >= 0) return null

  let closeIndex = -1
  let closeLen = 0
  for (const tag of closeTags) {
    const idx = protectedText.indexOf(tag, contentStart)
    if (idx >= 0 && (closeIndex < 0 || idx < closeIndex)) {
      closeIndex = idx
      closeLen = tag.length
    }
  }
  if (closeIndex < 0) return null

  const restore = (segment: string) => segment.replace(/\x00CODE_(\d+)\x00/g, (_, idx) => tokens[Number(idx)] ?? '')
  const plan = restore(protectedText.slice(contentStart, closeIndex)).trim()
  if (!plan || /^[.\u2026\s-]+$/.test(plan)) return null
  return {
    before: restore(protectedText.slice(0, openIndex)).trim(),
    plan,
    after: restore(protectedText.slice(closeIndex + closeLen)).trim(),
  }
}

export function buildTimeline(history: unknown): TimelineEntry[] {
  const entries: TimelineEntry[] = []
  const byCallId = new Map<string, ToolCallItem>()

  for (const [index, raw] of historyItems(history).entries()) {
    const item = objectValue(raw)
    if (item.type === 'message' && (item.role === 'user' || item.role === 'assistant')) {
      const parts = item.content ?? item.parts
      const attachments: HistoryAttachment[] = []
      const textParts: unknown[] = []
      if (Array.isArray(parts)) {
        for (const rawPart of parts) {
          const part = objectValue(rawPart)
          const match = typeof part.text === 'string' && /^Attachment: ("(?:[^"\\]|\\.)*")(?:\n([\s\S]*))?$/.exec(part.text)
          if (match && item.role === 'user') {
            try {
              attachments.push({ name: JSON.parse(match[1]) as string, text: match[2] })
              continue
            } catch { /* ordinary message */ }
          }
          const image = part.type === 'input_image' ? safeImageURL(part.image_url) : undefined
          if (image) {
            const last = attachments.at(-1)
            if (last && last.text === undefined && !last.image) last.image = image
            else attachments.push({ name: 'Image', image })
          } else {
            textParts.push(rawPart)
          }
        }
      }
      const text = contentText(Array.isArray(parts) ? textParts : parts)
      if (!text.trim() && !attachments.length) continue
      entries.push({
        kind: 'message',
        id: `${index}-${String(item.role)}`,
        role: item.role,
        text,
        attachments: attachments.length ? attachments : undefined,
      })
    } else if (item.type === 'reasoning') {
      const text = extractReasoningText(item)
      if (text) entries.push({ kind: 'reasoning', id: `${index}-reasoning`, text })
    } else if ((item.type === 'function_call' || item.type === 'custom_tool_call') && typeof item.call_id === 'string') {
      const args = item.arguments ?? item.input
      const call: ToolCallItem = {
        callId: item.call_id,
        name: String(item.name ?? 'Tool'),
        arguments: typeof args === 'string' ? args : JSON.stringify(args ?? {}, null, 2),
      }
      byCallId.set(item.call_id, call)
      const previous = entries.at(-1)
      if (previous?.kind === 'tool_round') {
        previous.calls.push(call)
      } else {
        entries.push({ kind: 'tool_round', id: `${index}-tools`, calls: [call] })
      }
    } else if ((item.type === 'function_call_output' || item.type === 'custom_tool_call_output') && typeof item.call_id === 'string') {
      const call = byCallId.get(item.call_id)
      if (call) {
        const text = contentText(item.output) || (typeof item.output === 'string' ? item.output : '[non-text output]')
        call.output = text
        if (item.is_error === true) call.failed = true
      }
    }
  }
  return entries
}

export function timelineMessages(entries: TimelineEntry[]): HistoryMessage[] {
  return entries
    .filter((entry): entry is Extract<TimelineEntry, { kind: 'message' }> => entry.kind === 'message')
    .map(({ id, role, text, attachments }) => ({ id, role, text, attachments }))
}
