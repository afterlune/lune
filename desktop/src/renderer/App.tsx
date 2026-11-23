import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { ThemePicker } from './theme'
import type { ThemePreference } from '../shared/theme'
import ReactMarkdown from 'react-markdown'
import {
  ArrowDown, ArrowUp, Check, ChevronDown, CircleHelp, CircleStop, Code2, Command,
  FilePlus2, FolderOpen, Laptop, LoaderCircle, LockKeyhole, MessageSquare, Plus,
  Radio, RefreshCw, Search, Settings2, ShieldAlert, Sparkles, SquarePen, Wifi,
  WifiOff, X,
} from 'lucide-react'
import { downloadMarkdown, readAttachment, validateAttachments, type Attachment } from './attachments'
import { connectRuntime, type RuntimeClient, type RuntimeConnection } from './runtime'

type JsonObject = Record<string, unknown>
type SessionSummary = {
  id: string
  title: string
  archived: boolean
  updatedAt: string
  sessionUsage?: { input: number; output: number; estimated_input?: number }
  settings: { modelProfile: string; reasoningEffort?: string }
}
type ModelOption = { profile: string; provider: string; model: string; displayName?: string; contextWindow?: number; supportsInputImages?: boolean }
type TaskState = { status: string; contextWindow?: number; executionId?: string; taskId?: string; goal?: string }
type RuntimeState = { task: TaskState; queue: Array<{ queueId: string; executionId: string; position: number; status: string }> }
type Pending = {
  kind: string
  approval?: { id: string; agentId?: string; tool: string; arguments: unknown; autoReviewReason?: string }
  userQuestion?: {
    id: string; executionId: string; runId: string; invocationId: string; callId: string
    questions?: Array<{ id: string; question: string; options?: Array<{ label: string; description: string; recommended?: boolean }> }>
  }
  interaction?: { id: string; serverName: string; request: unknown }
  toolRecovery?: { invocationId: string; tool: string; arguments: unknown }
  modelRecovery?: unknown
  blocker?: { id: string; tool: string; reason: string }
}
type HistoryMessage = { id: string; role: 'user' | 'assistant'; text: string; attachments?: Array<{ name: string; image?: string; text?: string }> }
type OptimisticMessage = HistoryMessage & { baselineCount: number; sessionId: string; executionId?: string }
type RuntimeEvent = {
  kind: string
  cursor?: number
  status?: string
  executionId?: string
  taskId?: string
  error?: string
  tool?: { name?: string; arguments?: unknown; failed?: boolean; duration?: number }
  stream?: { type?: string; purpose?: string; delta?: string; toolName?: string }
  agentName?: string
}
type Activity = { id: string; label: string; detail?: string; failed?: boolean }
type ToolRecord = { name: string; arguments: string; output?: string }
const sessionPageSize = 50

function historyItems(history: unknown): unknown[] {
  return Array.isArray(history) ? history : Array.isArray(object(history).items) ? object(history).items as unknown[] : []
}

function toolRecords(history: unknown): ToolRecord[] {
  const records: ToolRecord[] = []
  const byCall = new Map<string, ToolRecord>()
  for (const raw of historyItems(history)) {
    const item = object(raw)
    if (typeof item.call_id !== 'string') continue
    if (item.type === 'function_call' || item.type === 'custom_tool_call') {
      const args = item.arguments ?? item.input
      const record = { name: String(item.name ?? 'Tool'), arguments: typeof args === 'string' ? args : JSON.stringify(args ?? {}, null, 2) }
      byCall.set(item.call_id, record)
      records.push(record)
    } else if (item.type === 'function_call_output' || item.type === 'custom_tool_call_output') {
      const record = byCall.get(item.call_id)
      if (record) record.output = contentText(item.output) || (typeof item.output === 'string' ? item.output : '[non-text output]')
    }
  }
  return records
}

function safeImageURL(value: unknown): string | undefined {
  return typeof value === 'string' && /^data:image\/(png|jpeg);base64,[a-zA-Z0-9+/=]+$/.test(value) ? value : undefined
}

async function loadHistory(client: RuntimeClient, sessionId: string, paged: boolean): Promise<unknown> {
  if (!paged) return client.request('session.history', { sessionId })
  for (let attempt = 0; ; attempt++) {
    const items: unknown[] = []
    let offset = 0
    let digest = ''
    try {
      for (;;) {
        const page = await client.request<{ items: unknown[] | null; nextOffset: number; digest: string; done: boolean }>('session.historyPage', { sessionId, offset, digest })
        items.push(...(page.items ?? []))
        if (page.done) return items
        if (page.nextOffset <= offset) throw new Error('Core returned an invalid history page.')
        offset = page.nextOffset
        digest = page.digest
      }
    } catch (error) {
      if (attempt >= 2 || !errorMessage(error).includes('history changed while loading')) throw error
    }
  }
}


const emptyRuntime: RuntimeState = { task: { status: 'idle' }, queue: [] }

function normalizeRuntimeState(value: RuntimeState | null | undefined): RuntimeState {
  if (!value) return emptyRuntime
  return {
    ...value,
    task: value.task ?? emptyRuntime.task,
    queue: Array.isArray(value.queue) ? value.queue : [],
  }
}

function object(value: unknown): JsonObject {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as JsonObject : {}
}

function contentText(value: unknown): string {
  if (typeof value === 'string') return value
  if (Array.isArray(value)) {
    return value.map((part) => {
      const item = object(part)
      if (typeof item.text === 'string') return item.text
      if (item.type === 'input_image' || item.type === 'image') return '[image]'
      if (item.type === 'refusal' && typeof item.refusal === 'string') return item.refusal
      return ''
    }).filter(Boolean).join('\n')
  }
  return ''
}

function historyMessages(history: unknown): HistoryMessage[] {
  return historyItems(history).flatMap((raw, index) => {
    const item = object(raw)
    if (item.type !== 'message' || (item.role !== 'user' && item.role !== 'assistant')) return []
    const parts = item.content ?? item.parts
    const attachments: NonNullable<HistoryMessage['attachments']> = []
    const textParts: unknown[] = []
    if (Array.isArray(parts)) for (const rawPart of parts) {
      const part = object(rawPart)
      const match = typeof part.text === 'string' && /^Attachment: ("(?:[^"\\]|\\.)*")(?:\n([\s\S]*))?$/.exec(part.text)
      if (match && item.role === 'user') {
        try { attachments.push({ name: JSON.parse(match[1]) as string, text: match[2] }); continue } catch { /* ordinary message */ }
      }
      const image = part.type === 'input_image' ? safeImageURL(part.image_url) : undefined
      if (image) {
        const last = attachments.at(-1)
        if (last && last.text === undefined && !last.image) last.image = image
        else attachments.push({ name: 'Image', image })
      } else textParts.push(rawPart)
    }
    const text = contentText(Array.isArray(parts) ? textParts : parts)
    if (!text.trim() && !attachments.length) return []
    return [{ id: `${index}-${String(item.role)}`, role: item.role, text, attachments }]
  })
}

function readCursor(key: string): number {
  try {
    const value = Number(localStorage.getItem(key))
    return Number.isFinite(value) && value > 0 ? value : 0
  } catch { return 0 }
}

function writeCursor(key: string, value: number): void {
  try { localStorage.setItem(key, String(value)) } catch { /* cursors can be replayed after a reload */ }
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error)
}

function messageIdentity(message: HistoryMessage): string {
  return JSON.stringify([message.text, message.attachments?.map((item) => item.name) ?? []])
}

function describeSession(session: SessionSummary, messages: HistoryMessage[]): string {
  return session.title || messages.find((message) => message.role === 'user')?.text.slice(0, 56) || 'New conversation'
}

export default function App({ initialTheme }: { initialTheme: ThemePreference }) {
  const [theme, setTheme] = useState(initialTheme)
  const desktop = Boolean(window.luneDesktop)
  const [client, setClient] = useState<RuntimeClient | null>(null)
  const [connection, setConnection] = useState<RuntimeConnection | null>(null)
  const [connectionStatus, setConnectionStatus] = useState<'offline' | 'connecting' | 'connected' | 'reconnecting'>('offline')
  const [connectionError, setConnectionError] = useState('')
  const [showConnection, setShowConnection] = useState(true)
  const [mode, setMode] = useState<'local' | 'remote'>(desktop ? 'local' : 'remote')
  const [workspace, setWorkspace] = useState('')
  const [remoteURL, setRemoteURL] = useState('wss://localhost:18092/v1/runtime')
  const [token, setToken] = useState('')
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [models, setModels] = useState<ModelOption[]>([])
  const [activeSessionId, setActiveSessionId] = useState('')
  const [activeSession, setActiveSession] = useState<SessionSummary | null>(null)
  const [history, setHistory] = useState<HistoryMessage[]>([])
  const [optimistic, setOptimistic] = useState<OptimisticMessage[]>([])
  const [runtime, setRuntime] = useState<RuntimeState>(emptyRuntime)
  const [pending, setPending] = useState<Pending[]>([])
  const [streamingText, setStreamingText] = useState('')
  const [activity, setActivity] = useState<Activity[]>([])
  const [draft, setDraft] = useState('')
  const [search, setSearch] = useState('')
  const [showArchived, setShowArchived] = useState(false)
  const [sessionOffset, setSessionOffset] = useState(0)
  const [moreSessions, setMoreSessions] = useState(false)
  const [listLoading, setListLoading] = useState(false)
  const [listRevision, setListRevision] = useState(0)
  const [capabilities, setCapabilities] = useState<string[]>([])
  const [record, setRecord] = useState<ToolRecord[]>([])
  const [planMode, setPlanMode] = useState(false)
  const [attachments, setAttachments] = useState<Attachment[]>([])
  const [info, setInfo] = useState('')
  const [quick, setQuick] = useState(false)
  const [narrow, setNarrow] = useState(() => window.matchMedia('(max-width: 680px)').matches)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const compactLayout = narrow || quick
  const [shortcut, setShortcut] = useState('CommandOrControl+Shift+Space')
  const [sessionDialog, setSessionDialog] = useState<null | {
    kind: 'rename' | 'delete' | 'rewind'; value: string; digest?: string; removed?: number; excerpts?: string[]
  }>(null)
  const [busy, setBusy] = useState(false)
  const [working, setWorking] = useState(false)
  const [actionError, setActionError] = useState('')

  const capabilitiesRef = useRef<string[]>([])
  const submissionRetry = useRef<{ key: string; id: string; mode: string } | null>(null)
  const clientRef = useRef<RuntimeClient | null>(null)
  const connectionRef = useRef<RuntimeConnection | null>(null)
  const autoReconnect = useRef(false)
  const reconnectTimer = useRef<number | null>(null)
  const reconnectCount = useRef(0)
  const startConnection = useRef<(options: RuntimeConnection, retry?: boolean) => Promise<void>>(async () => {})
  const subscriptionRef = useRef('')
  const cursorKeyRef = useRef('')
  const eventHandler = useRef<(event: RuntimeEvent) => void>(() => {})
  const initializedClient = useRef<RuntimeClient | null>(null)
  const activeSessionIdRef = useRef(activeSessionId)
  const streamingExecution = useRef<string | undefined>(undefined)
  const sessionRefreshSequence = useRef(0)
  const fileInput = useRef<HTMLInputElement>(null)
  const bottomRef = useRef<HTMLDivElement | null>(null)
  const drawerRef = useRef<HTMLDialogElement>(null)
  const drawerButtonRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    const viewport = window.matchMedia('(max-width: 680px)')
    const update = () => setNarrow(viewport.matches)
    viewport.addEventListener('change', update)
    update()
    return () => viewport.removeEventListener('change', update)
  }, [])

  const has = (feature: string) => capabilities.includes(feature)
  const serverSearch = has('session.search')
  const filteredSessions = useMemo(() => {
    const term = search.trim().toLowerCase()
    return sessions.filter((session) => (showArchived || !session.archived) && (serverSearch || !term || (session.title || '').toLowerCase().includes(term) || session.id.toLowerCase().includes(term)))
  }, [sessions, search, showArchived, serverSearch])
  const displayedMessages = useMemo(() => [...history, ...optimistic.filter((message) => message.sessionId === activeSessionId)], [history, optimistic, activeSessionId])
  const selectedModel = activeSession?.settings.modelProfile || models[0]?.profile || ''
  const taskIsRunning = runtime.task.status === 'running'
  const taskIsQueued = runtime.task.status === 'queued'
  const taskIsPaused = runtime.task.status === 'paused'
  const sessionBusy = taskIsRunning || taskIsQueued || taskIsPaused || runtime.queue.length > 0
  const modelOption = models.find((option) => option.profile === selectedModel)
  const contextLimit = runtime.task.contextWindow || modelOption?.contextWindow

  const listSessions = useCallback((target: RuntimeClient, offset: number, term: string, archived: boolean) =>
    target.request<SessionSummary[]>(serverSearch && term ? 'session.search' : 'session.list', {
      offset, limit: sessionPageSize, includeArchived: archived,
      ...(serverSearch && term ? { query: term } : {}),
    }), [serverSearch])

  useEffect(() => {
    if (!client) return
    setListLoading(true)
    let cancelled = false
    const timer = window.setTimeout(() => {
      void listSessions(client, 0, search.trim(), showArchived).then((page) => {
        if (cancelled) return
        setSessions(page)
        setSessionOffset(page.length)
        setMoreSessions(page.length === sessionPageSize)
      }).catch((error) => { if (!cancelled) setActionError(errorMessage(error)) })
        .finally(() => { if (!cancelled) setListLoading(false) })
    }, search.trim() && serverSearch ? 250 : 0)
    return () => { cancelled = true; window.clearTimeout(timer) }
  }, [client, listSessions, search, showArchived, listRevision, serverSearch])

  useEffect(() => {
    const desktopAPI = window.luneDesktop
    if (!desktopAPI) return
    void desktopAPI.shortcut().then(setShortcut).catch((error) => setConnectionError(errorMessage(error)))
    return desktopAPI.onQuickMode(setQuick)
  }, [])

  const scheduleReconnect = useCallback(() => {
    if (!autoReconnect.current || !connectionRef.current) return
    if (reconnectTimer.current !== null) window.clearTimeout(reconnectTimer.current)
    const delay = Math.min(1000 * 2 ** reconnectCount.current, 30_000)
    reconnectCount.current += 1
    setConnectionStatus('reconnecting')
    reconnectTimer.current = window.setTimeout(() => {
      const options = connectionRef.current
      if (options && autoReconnect.current) void startConnection.current(options, true)
    }, delay)
  }, [])

  const activateConnection = useCallback(async (options: RuntimeConnection, retry = false) => {
    if (!retry) autoReconnect.current = false
    if (reconnectTimer.current !== null) {
      window.clearTimeout(reconnectTimer.current)
      reconnectTimer.current = null
    }
    setConnectionStatus(retry ? 'reconnecting' : 'connecting')
    setConnectionError('')
    let candidate: RuntimeClient | null = null
    try {
      const previousClient = clientRef.current
      if (previousClient) {
        clientRef.current = null
        subscriptionRef.current = ''
        setClient(null)
        await previousClient.close().catch(() => undefined)
      }
      candidate = await connectRuntime(options, retry)
      const hello = await candidate.request<{ protocolVersion?: string; capabilities?: string[] }>('runtime.hello', {
        clientName: desktop ? 'Lune Desktop' : 'Lune Web',
        clientVersion: '0.1.0',
      })
      if (hello?.protocolVersion !== '1') throw new Error(`Unsupported Core protocol version: ${hello?.protocolVersion ?? 'unknown'}`)
      if (!autoReconnect.current && retry) {
        await candidate.close()
        return
      }
      capabilitiesRef.current = hello.capabilities ?? []
      setCapabilities(capabilitiesRef.current)
      clientRef.current = candidate
      const savedOptions = desktop && options.mode === 'remote' ? { ...options, token: '' } : options
      connectionRef.current = savedOptions
      setClient(candidate)
      setConnection(savedOptions)
      setConnectionStatus('connected')
      reconnectCount.current = 0
      autoReconnect.current = true
      if (desktop && options.mode === 'remote') setToken('')
    } catch (error) {
      if (candidate) await candidate.close().catch(() => undefined)
      setConnectionError(errorMessage(error))
      setConnectionStatus(retry ? 'reconnecting' : 'offline')
      if (retry && autoReconnect.current) scheduleReconnect()
      else if (!retry) autoReconnect.current = false
    }
  }, [desktop, scheduleReconnect])

  useEffect(() => { startConnection.current = activateConnection }, [activateConnection])
  useEffect(() => { activeSessionIdRef.current = activeSessionId }, [activeSessionId])

  const disconnect = useCallback(async () => {
    autoReconnect.current = false
    if (reconnectTimer.current !== null) window.clearTimeout(reconnectTimer.current)
    reconnectTimer.current = null
    subscriptionRef.current = ''
    const active = clientRef.current
    clientRef.current = null
    setClient(null)
    setConnection(null)
    connectionRef.current = null
    setToken('')
    setConnectionStatus('offline')
    setSessions([])
    setModels([])
    setActiveSession(null)
    setActiveSessionId('')
    setHistory([])
    setRecord([])
    setAttachments([])
    submissionRetry.current = null
    setPending([])
    setRuntime(emptyRuntime)
    setStreamingText('')
    setShowConnection(true)
    if (active) await active.close(true).catch(() => undefined)
    else if (window.luneDesktop) await window.luneDesktop.close(true).catch(() => undefined)
  }, [])

  useEffect(() => {
    if (!client) return
    return client.onNotification((notification) => {
      if (notification.method === 'runtime.connectionClosed') {
        clientRef.current = null
        subscriptionRef.current = ''
        setClient(null)
        if (autoReconnect.current) scheduleReconnect()
        else setConnectionStatus('offline')
        return
      }
      if (notification.method !== 'runtime.event') return
      const params = object(notification.params)
      if (params.subscriptionId !== subscriptionRef.current) return
      eventHandler.current(object(params.event) as RuntimeEvent)
    })
  }, [client, scheduleReconnect])

  const refreshSessionData = useCallback(async (targetClient: RuntimeClient, sessionId: string) => {
    const sequence = ++sessionRefreshSequence.current
    const [summary, rawHistory, nextRuntime, nextPending] = await Promise.all([
      targetClient.request<SessionSummary>('session.get', { sessionId }),
      loadHistory(targetClient, sessionId, capabilitiesRef.current.includes('session.historyPage')),
      targetClient.request<RuntimeState>('runtime.state', { sessionId }),
      targetClient.request<Pending[]>('runtime.pending', { sessionId }),
    ])
    if (clientRef.current !== targetClient || activeSessionIdRef.current !== sessionId || sequence !== sessionRefreshSequence.current) return
    const messages = historyMessages(rawHistory)
    setRecord(toolRecords(rawHistory))
    setActiveSession(summary)
    setHistory(messages)
    setOptimistic((items) => {
      const counts = new Map<string, number>()
      for (const message of messages) if (message.role === 'user') {
        const identity = messageIdentity(message)
        counts.set(identity, (counts.get(identity) ?? 0) + 1)
      }
      const seen = new Map<string, number>()
      return items.filter((item) => {
        if (item.sessionId !== sessionId) return true
        const identity = messageIdentity(item)
        const count = counts.get(identity) ?? 0
        const alreadySeen = seen.get(identity) ?? 0
        seen.set(identity, alreadySeen + 1)
        return count <= item.baselineCount + alreadySeen
      })
    })
    setRuntime(normalizeRuntimeState(nextRuntime))
    setPending(nextPending ?? [])
    setSessions((items) => items.map((item) => item.id === summary.id ? summary : item))
  }, [])

  const refreshRuntime = useCallback(async (targetClient: RuntimeClient, sessionId: string) => {
    const [nextRuntime, nextPending] = await Promise.all([
      targetClient.request<RuntimeState>('runtime.state', { sessionId }),
      targetClient.request<Pending[]>('runtime.pending', { sessionId }),
    ])
    if (clientRef.current !== targetClient || activeSessionIdRef.current !== sessionId) return
    setRuntime(normalizeRuntimeState(nextRuntime))
    setPending(nextPending ?? [])
  }, [])

  useEffect(() => {
    if (!client || initializedClient.current === client) return
    initializedClient.current = client
    let cancelled = false
    void Promise.all([
      client.request<SessionSummary[]>('session.list', { limit: sessionPageSize, includeArchived: false }),
      client.request<ModelOption[]>('model.list'),
    ]).then(async ([nextSessions, nextModels]) => {
      if (cancelled) return
      setSessions(nextSessions ?? [])
      setModels(nextModels ?? [])
      const previousID = activeSessionIdRef.current
      const existing = (previousID ? await client.request<SessionSummary>('session.get', { sessionId: previousID }).catch(() => undefined) : undefined) ?? nextSessions?.[0]
      if (cancelled) return
      if (existing) setActiveSessionId(existing.id)
      else {
        const created = await client.request<SessionSummary>('session.create')
        if (!cancelled) {
          setSessions([created])
          setActiveSessionId(created.id)
        }
      }
      setShowConnection(false)
    }).catch((error: unknown) => {
      if (!cancelled) setActionError(errorMessage(error))
    })
    return () => { cancelled = true }
  }, [client])

  useEffect(() => {
    if (!client || !activeSessionId) return
    let cancelled = false
    let subscriptionId = ''
    const targetClient = client
    const sessionId = activeSessionId
    setActiveSession(null)
    setHistory([])
    setRecord([])
    streamingExecution.current = undefined
    setPending([])
    setRuntime(emptyRuntime)
    setStreamingText('')
    setActivity([])
    cursorKeyRef.current = `lune:cursor:${connection?.mode ?? 'remote'}:${connection?.mode === 'remote' ? connection.url : connection?.workspace ?? 'local'}:${sessionId}`

    const refreshAll = () => refreshSessionData(targetClient, sessionId).catch((error: unknown) => setActionError(errorMessage(error)))
    const refreshLiveState = () => refreshRuntime(targetClient, sessionId).catch((error: unknown) => setActionError(errorMessage(error)))
    eventHandler.current = (event) => {
      if (event.cursor && event.cursor > readCursor(cursorKeyRef.current)) writeCursor(cursorKeyRef.current, event.cursor)
      if (event.kind === 'task_started') {
        streamingExecution.current = event.executionId
        setStreamingText('')
        setRuntime((state) => ({ ...state, task: { ...state.task, status: 'running', executionId: event.executionId } }))
        setActivity((items) => [{ id: `${event.taskId}-started`, label: 'Working on your request', detail: event.executionId }, ...items].slice(0, 5))
      } else if (event.kind === 'task_finished' || event.kind === 'task_discarded') {
        if (!event.taskId && event.status === 'cancelled' && event.executionId) {
          setOptimistic((items) => items.filter((item) => item.executionId !== event.executionId))
        }
        if (event.executionId === streamingExecution.current) {
          streamingExecution.current = undefined
          setStreamingText('')
        }
        setActivity((items) => [{
          id: `${event.executionId}-${event.kind}`,
          label: event.kind === 'task_discarded' ? 'Run discarded' : `Run ${event.status ?? 'finished'}`,
          detail: event.error,
          failed: ['failed', 'cancelled'].includes(event.status ?? ''),
        }, ...items].slice(0, 5))
        void refreshAll()
      } else if (event.kind === 'context_compaction') {
        void refreshAll()
      } else if (event.kind === 'task_queued') {
        void refreshLiveState()
      } else if (event.kind === 'model_stream' && event.stream?.type === 'text_delta' && (event.stream.purpose === 'main' || !event.stream.purpose)) {
        setStreamingText((text) => text + (event.stream?.delta ?? ''))
      } else if (event.kind === 'tool_started' || event.kind === 'tool_finished') {
        const name = event.tool?.name ?? 'tool'
        setActivity((items) => [{
          id: `${event.taskId}-${name}-${event.kind}-${Date.now()}`,
          label: event.kind === 'tool_started' ? `Using ${name}` : `${name} finished`,
          detail: event.tool?.failed ? 'The tool reported an error.' : undefined,
          failed: event.tool?.failed,
        }, ...items].slice(0, 5))
      } else if (['approval_required', 'user_question_required', 'user_interaction_required', 'tool_recovery_required', 'execution_blocked'].includes(event.kind)) {
        void refreshLiveState()
      } else if (event.kind === 'runtime_replay_complete') {
        void refreshAll()
      }
    }

    void refreshAll().then(async () => {
      if (cancelled) return
      subscriptionId = crypto.randomUUID()
      const cursor = readCursor(cursorKeyRef.current)
      subscriptionRef.current = subscriptionId
      await targetClient.request('runtime.subscribe', { sessionId, subscriptionId, afterCursor: cursor })
    }).catch((error: unknown) => {
      if (!cancelled) setActionError(errorMessage(error))
    })

    return () => {
      cancelled = true
      if (subscriptionId) {
        if (subscriptionRef.current === subscriptionId) subscriptionRef.current = ''
        void targetClient.request('runtime.unsubscribe', { subscriptionId }).catch(() => undefined)
      }
    }
  }, [client, activeSessionId, connection, refreshRuntime, refreshSessionData])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' })
  }, [displayedMessages.length, streamingText, pending.length])

  const connectFromForm = async () => {
    setBusy(true)
    setConnectionError('')
    const options: RuntimeConnection = mode === 'local'
      ? { mode: 'local', workspace }
      : { mode: 'remote', url: remoteURL.trim(), token }
    await activateConnection(options)
    setBusy(false)
  }

  function openDrawer() {
    const drawer = drawerRef.current
    if (!drawer || drawer.open) return
    setActionError('')
    drawer.showModal()
    setDrawerOpen(true)
  }

  function closeDrawer() {
    drawerRef.current?.close()
  }

  useEffect(() => {
    if (!compactLayout || !client || showConnection) setDrawerOpen(false)
  }, [compactLayout, client, showConnection])

  const createSession = async () => {
    if (!client || working) return
    setWorking(true)
    setActionError('')
    try {
      const created = await client.request<SessionSummary>('session.create')
      if (clientRef.current !== client) return
      setSearch('')
      setSessions((items) => [created, ...items])
      setActiveSessionId(created.id)
      setListRevision((revision) => revision + 1)
      setHistory([])
      setOptimistic([])
      setActivity([])
      setStreamingText('')
      closeDrawer()
    } catch (error) { setActionError(errorMessage(error)) }
    finally { setWorking(false) }
  }

  const sendMessage = async (steer = false) => {
    const text = draft.trim()
    if (!client || activeSession?.id !== activeSessionId || !activeSessionId || (!text && !attachments.length) || working || connectionStatus !== 'connected') return
    if (attachments.length && !has('turn.attachments')) {
      setActionError('This Core does not support attachments. Remove them or connect to an updated Core.')
      return
    }
    try { validateAttachments(attachments, Boolean(modelOption?.supportsInputImages)) } catch (error) { setActionError(errorMessage(error)); return }
    const plan = has('turn.plan') && planMode && !steer
    const requestedMode = steer ? 'steer_input' : taskIsRunning || taskIsQueued || taskIsPaused || runtime.queue.length > 0 ? 'queue_input' : 'start_if_idle'
    const key = JSON.stringify([activeSessionId, text, attachments, plan, steer])
    const existing = submissionRetry.current
    const canRetry = existing?.key === key
    const submissionId = canRetry ? existing.id : crypto.randomUUID()
    const submissionMode = canRetry ? existing.mode : requestedMode
    submissionRetry.current = { key, id: submissionId, mode: submissionMode }
    setWorking(true)
    setActionError('')
    setDraft('')
    const attachmentNames = attachments.map((item) => item.name)
    const identity = JSON.stringify([text, attachmentNames])
    const baselineCount = history.filter((message) => message.role === 'user' && messageIdentity(message) === identity).length
    setOptimistic((items) => [...items, { id: submissionId, role: 'user', text, baselineCount, sessionId: activeSessionId,
      attachments: attachments.map(({ name, mediaType, data }) => ({ name, image: mediaType.startsWith('image/') ? `data:${mediaType};base64,${data}` : undefined })),
    }])
    try {
      if (activeSession && !activeSession.title && !history.some((message) => message.role === 'user')) {
        const title = Array.from(text.replace(/\s+/g, ' ').trim()).slice(0, 64).join('')
        if (title) {
          try {
            await client.request('session.setTitle', { sessionId: activeSessionId, title })
            setActiveSession({ ...activeSession, title })
            setSessions((items) => items.map((item) => item.id === activeSessionId ? { ...item, title } : item))
          } catch { /* session titles are cosmetic; do not block a turn */ }
        }
      }
      const submitted = await client.request<{ executionId: string }>('turn.submit', { sessionId: activeSessionId, submissionId, input: text, mode: submissionMode,
        ...(plan ? { planMode: true } : {}),
        ...(attachments.length ? { attachments: attachments.map(({ name, mediaType, data }) => ({ name, mediaType, data })) } : {}),
      })
      setOptimistic((items) => items.map((item) => item.id === submissionId ? { ...item, executionId: submitted.executionId } : item))
      submissionRetry.current = null
      setAttachments([])
      void refreshRuntime(client, activeSessionId).catch((error) => setActionError(errorMessage(error)))
    } catch (error) {
      setOptimistic((items) => items.filter((item) => item.id !== submissionId))
      setDraft(text)
      if (/session_busy|task_paused/.test(errorMessage(error))) submissionRetry.current = null
      setActionError(errorMessage(error))
    } finally { setWorking(false) }
  }

  const stopRun = async () => {
    if (!client || !activeSessionId || !runtime.task.executionId) return
    setWorking(true)
    try {
      await client.request('turn.interrupt', { sessionId: activeSessionId, executionId: runtime.task.executionId })
      await refreshRuntime(client, activeSessionId)
    } catch (error) { setActionError(errorMessage(error)) }
    finally { setWorking(false) }
  }

  const resumeRun = async () => {
    if (!client || !activeSessionId) return
    setWorking(true)
    try {
      await client.request('turn.recover', { sessionId: activeSessionId })
      await refreshRuntime(client, activeSessionId)
    } catch (error) { setActionError(errorMessage(error)) }
    finally { setWorking(false) }
  }

  const discardPaused = async () => {
    if (!client || !activeSessionId) return
    setWorking(true)
    try {
      await client.request('turn.discardPaused', { sessionId: activeSessionId })
      await refreshSessionData(client, activeSessionId)
    } catch (error) { setActionError(errorMessage(error)) }
    finally { setWorking(false) }
  }

  const changeModel = async (profile: string) => changeSettings({ modelProfile: profile })
  const changeSettings = async (settings: Partial<SessionSummary['settings']>) => {
    if (!client || !activeSessionId || !activeSession) return
    try {
      await client.request('session.setSettings', { sessionId: activeSessionId, settings: { ...activeSession.settings, ...settings } })
      await refreshSessionData(client, activeSessionId)
    } catch (error) { setActionError(errorMessage(error)) }
  }

  const sessionAction = async (action: () => Promise<void>) => {
    setWorking(true)
    setInfo('')
    setActionError('')
    try { await action() }
    catch (error) { setActionError(errorMessage(error)) }
    finally { setWorking(false) }
  }

  const chooseSession = (id: string) => {
    if (!client || working) return
    if (!drawerRef.current?.open || id === activeSessionId) {
      setActiveSessionId(id)
      closeDrawer()
      return
    }
    void sessionAction(async () => {
      await client.request('session.get', { sessionId: id })
      if (clientRef.current !== client) return
      setActiveSessionId(id)
      closeDrawer()
    })
  }

  const loadMoreSessions = () => {
    if (!client || working || listLoading) return
    void sessionAction(async () => {
      const page = await listSessions(client, sessionOffset, search.trim(), showArchived)
      if (clientRef.current !== client) return
      setSessions((items) => [...items, ...page.filter((item) => !items.some((old) => old.id === item.id))])
      setSessionOffset(sessionOffset + page.length)
      setMoreSessions(page.length === sessionPageSize)
    })
  }

  const changeArchived = () => void sessionAction(async () => {
    if (!client || !activeSession) return
    await client.request('session.setArchived', { sessionId: activeSessionId, archived: !activeSession.archived })
    await refreshSessionData(client, activeSessionId)
    setListRevision((revision) => revision + 1)
  })

  const forkSession = () => void sessionAction(async () => {
    if (!client) return
    const forked = await client.request<SessionSummary>('session.fork', { sessionId: activeSessionId })
    setSearch('')
    setActiveSessionId(forked.id)
    setListRevision((revision) => revision + 1)
  })

  const compactSession = () => void sessionAction(async () => {
    if (!client) return
    setInfo('Compacting context…')
    try {
      const result = await client.request<{ compacted: boolean; backend?: string }>('session.compact', { sessionId: activeSessionId })
      setInfo(result.compacted ? (result.backend ? `Context compacted (${result.backend}).` : 'Context compacted.') : 'Context is already within its budget.')
      await refreshSessionData(client, activeSessionId)
    } catch (error) { setInfo(''); throw error }
  })

  const cancelQueued = (executionId: string) => void sessionAction(async () => {
    if (!client) return
    try {
      await client.request('turn.cancelQueued', { sessionId: activeSessionId, executionId })
      setOptimistic((items) => items.filter((item) => item.executionId !== executionId))
    }
    finally { await refreshRuntime(client, activeSessionId) }
  })

  const saveConversation = () => void sessionAction(async () => {
    if (!client || !activeSession) return
    const title = describeSession(activeSession, history)
    const markdown = [`# ${title}`, ...history.map((message) =>
      [`## ${message.role === 'user' ? 'You' : 'Lune'}`, message.text,
        ...(message.attachments ?? []).map((file) => file.image ? `![Attachment](${file.image})` : `### ${file.name}\n\n${file.text ?? ''}`)].join('\n\n'),
    )].join('\n\n')
    if (window.luneDesktop) await window.luneDesktop.saveMarkdown(markdown)
    else downloadMarkdown(markdown)
  })

  const confirmSessionDialog = () => void sessionAction(async () => {
    if (!client || !sessionDialog) return
    const { kind, value, digest } = sessionDialog
    if (kind === 'rename') {
      await client.request('session.setTitle', { sessionId: activeSessionId, title: value })
    } else if (kind === 'delete') {
      await client.request('session.delete', { sessionId: activeSessionId })
      const next = (await client.request<SessionSummary[]>('session.list', { limit: 1 }))[0]
      const selected = next ?? await client.request<SessionSummary>('session.create')
      setActiveSessionId(selected.id)
    } else if (!digest) {
      const turns = Number(value)
      if (!Number.isSafeInteger(turns) || turns < 1) throw new Error('Enter a positive number of turns.')
      const preview = await client.request<{ removed: number; digest: string; excerpts: string[] | null }>('session.rewindPreview', { sessionId: activeSessionId, turns })
      setSessionDialog({ ...sessionDialog, ...preview, excerpts: preview.excerpts ?? [] })
      return
    } else {
      try {
        await client.request('session.rewind', { sessionId: activeSessionId, turns: Number(value), digest })
      } catch (error) {
        setSessionDialog({ kind: 'rewind', value })
        throw error
      }
      setInfo(`Removed ${sessionDialog.removed} turn(s) from this conversation.`)
    }
    setSessionDialog(null)
    setListRevision((revision) => revision + 1)
    if (kind !== 'delete') await refreshSessionData(client, activeSessionId)
  })

  const addFiles = (files: File[]) => void sessionAction(async () => {
    if (!files.length) return
    if (files.length + attachments.length > 5) throw new Error('Attach up to five files.')
    const next = [...attachments, ...await Promise.all(files.map(readAttachment))]
    validateAttachments(next, Boolean(modelOption?.supportsInputImages))
    setAttachments(next)
  })

  const captureScreenshot = () => void sessionAction(async () => {
    if (!window.luneDesktop) return
    const screenshot = await window.luneDesktop.captureScreenshot()
    if (!screenshot) return
    const next = [...attachments, screenshot]
    validateAttachments(next, Boolean(modelOption?.supportsInputImages))
    setAttachments(next)
  })

  const resolvePending = async (method: string, params: unknown) => {
    if (!client) return
    setWorking(true)
    setActionError('')
    try {
      await client.request(method, params)
      if (activeSessionId) await refreshSessionData(client, activeSessionId)
    } catch (error) { setActionError(errorMessage(error)) }
    finally { setWorking(false) }
  }

  const resolveApproval = (request: NonNullable<Pending['approval']>, decision: string) =>
    resolvePending('approval.resolve', { sessionId: activeSessionId, id: request.id, decision })

  const resolveQuestion = (request: NonNullable<Pending['userQuestion']>, answers: Array<{ question_id: string; answer: string }>) =>
    resolvePending('question.resolve', { sessionId: activeSessionId, answer: {
      executionId: request.executionId, runId: request.runId, invocationId: request.invocationId,
      callId: request.callId, answers,
    } })

  const resolveInteraction = (request: NonNullable<Pending['interaction']>, value: unknown) =>
    resolvePending('interaction.resolve', { sessionId: activeSessionId, id: request.id, response: value })

  const chooseWorkspace = async () => {
    const selected = await window.luneDesktop?.selectWorkspace()
    if (selected) setWorkspace(selected)
  }

  const statusLabel = connectionStatus === 'connected' ? 'Connected' : connectionStatus === 'reconnecting' ? 'Reconnecting' : connectionStatus === 'connecting' ? 'Connecting' : 'Not connected'
  const statusIcon = connectionStatus === 'connected' ? <Wifi size={15} /> : connectionStatus === 'reconnecting' || connectionStatus === 'connecting' ? <LoaderCircle className="spin" size={15} /> : <WifiOff size={15} />
  const canReuseDesktopRemote = desktop && connection?.mode === 'remote' && remoteURL.trim() === connection.url
  const connectDisabled = busy || (mode === 'local' ? !workspace.trim() : !remoteURL.trim() || (!token.trim() && !canReuseDesktopRemote))

  if (!client || showConnection) {
    return (
      <div className="connect-page">
        <div className="connect-orb orb-one" /><div className="connect-orb orb-two" />
        <div className="connect-top"><div className="brand-mark"><Sparkles size={18} /></div><span>Lune</span><span className="build-label">CORE CLIENT</span></div>
        <div className="connect-appearance"><ThemePicker value={theme} onChange={setTheme} /></div>
        <main className="connect-card">
          <div className="eyebrow"><span className="eyebrow-dot" /> YOUR WORKSPACE, YOUR CORE</div>
          <h1>A quiet place<br />to think with AI.</h1>
          <p className="connect-intro">Connect to a Lune Core. Your sessions, tools, and permissions stay with the Core you choose.</p>
          {desktop && <div className="connection-tabs">
            <button className={mode === 'local' ? 'active' : ''} onClick={() => setMode('local')}><Laptop size={16} /> This computer</button>
            <button className={mode === 'remote' ? 'active' : ''} onClick={() => setMode('remote')}><Radio size={16} /> Remote Core</button>
          </div>}
          {mode === 'local' && desktop ? <div className="form-field">
            <label>Workspace folder</label>
            <div className="input-with-action"><input value={workspace} onChange={(event) => setWorkspace(event.target.value)} placeholder="Choose a folder with config.json" /><button onClick={() => void chooseWorkspace()} aria-label="Browse folders"><FolderOpen size={17} /></button></div>
            <small>Electron starts a private Core process for this workspace. Sessions are stored in its <code>.lune</code> folder.</small>
          </div> : <>
            <div className="form-field">
              <label>Core WebSocket URL</label>
              <input value={remoteURL} onChange={(event) => setRemoteURL(event.target.value)} placeholder="wss://core.example.com:18092/v1/runtime" autoCapitalize="none" spellCheck={false} />
            </div>
            <div className="form-field">
              <label>Access token</label>
              <input type="password" value={token} onChange={(event) => setToken(event.target.value)} placeholder="Paste your Core token" autoComplete="off" />
              <small><LockKeyhole size={13} /> Kept in memory for this page only. The browser connects over TLS.</small>
            </div>
          </>}
          {connectionError && <div className="form-error"><ShieldAlert size={16} /> <span>{connectionError}</span></div>}
          {!connectionError && connectionStatus === 'reconnecting' && <div className="form-status"><LoaderCircle size={15} className="spin" /> The connection dropped. Lune is trying again automatically.</div>}
          <button className="primary-button connect-button" disabled={connectDisabled} onClick={() => void connectFromForm()}>
            {busy ? <LoaderCircle size={17} className="spin" /> : <ArrowUp size={17} />}{busy ? 'Connecting…' : 'Connect to Core'}
          </button>
          {desktop && connection && <button className="forget-connection" onClick={() => void disconnect()}>Forget saved desktop connection</button>}
          <div className="connect-footnote"><span><LockKeyhole size={13} /> End-to-end transport security</span><span>Runtime Protocol v1</span></div>
        </main>
        <div className="connect-bottom"><span>CORE RUNS WHERE YOU CHOOSE</span><span>WEB · DESKTOP · MULTI-DEVICE</span></div>
      </div>
    )
  }

  const sessionNavigation = <>
    <button className="new-chat-button" disabled={working} onClick={() => void createSession()}><Plus size={16} /> New conversation</button>
    <div className="sidebar-section-head">{!compactLayout && <span>CONVERSATIONS</span>}<button className="subtle-icon" title="Refresh sessions" aria-label="Refresh sessions" disabled={working || listLoading} onClick={() => setListRevision((revision) => revision + 1)}><RefreshCw size={13} /></button></div>
    <div className="session-search"><Search size={14} /><input aria-label="Search conversations" value={search} disabled={working} onChange={(event) => setSearch(event.target.value)} placeholder={serverSearch ? 'Search titles and messages' : 'Find a conversation'} /></div>
    <label className="archived-toggle"><input type="checkbox" checked={showArchived} disabled={working} onChange={(event) => setShowArchived(event.target.checked)} /> Include archived</label>
    <div className="session-list" aria-busy={listLoading}>
      {filteredSessions.map((session) => {
        const preview = session.title || `Conversation ${session.id.slice(0, 8)}`
        return <button key={session.id} className={`session-item ${session.id === activeSessionId ? 'selected' : ''}`} disabled={working} aria-current={session.id === activeSessionId ? 'true' : undefined} onClick={() => chooseSession(session.id)}>
          <MessageSquare size={15} /><span>{preview}{session.archived ? ' (archived)' : ''}</span><span className="session-time">{new Date(session.updatedAt).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}</span>
        </button>
      })}
      {moreSessions && <button className="plain-button" disabled={working || listLoading} onClick={loadMoreSessions}>Load more</button>}
      {!filteredSessions.length && <div className="empty-sessions">{listLoading ? 'Loading conversations…' : search.trim() ? 'No matching conversations.' : 'No conversations yet.'}</div>}
    </div>
    {drawerOpen && actionError && <div className="action-error" role="alert">{actionError}</div>}
    <div className="sidebar-bottom">
      <div className="core-card"><div className="core-card-icon"><Code2 size={16} /></div><div className="core-card-copy"><strong>{connection?.mode === 'local' ? 'Local Core' : 'Remote Core'}</strong><span>{connection?.mode === 'local' ? workspace.split(/[\\/]/).filter(Boolean).at(-1) || 'This computer' : connection?.url}</span></div><span className={`connection-light ${connectionStatus}`} /></div>
      <button className="settings-button" onClick={() => setShowConnection(true)}><Settings2 size={16} /> Connection settings <ChevronDown size={14} /></button>
      {desktop && <button className="settings-button" onClick={() => void window.luneDesktop?.toggleQuick()}><Command size={16} /> Quick window</button>}
      <button className="disconnect-button" onClick={() => void disconnect()}><X size={15} /> Disconnect</button>
    </div>
  </>

  return (
    <div className={`app-shell${quick ? ' quick-mode' : ''}`}>
      {compactLayout ? <dialog id="session-drawer" className="session-drawer" aria-labelledby="session-drawer-title" ref={drawerRef}
        onClose={() => {
          if (drawerRef.current?.open) return
          setDrawerOpen(false)
          drawerButtonRef.current?.focus()
        }}
        onClick={(event) => { if (event.target === event.currentTarget) closeDrawer() }}>
        <div className="drawer-panel">
          <div className="drawer-heading"><h2 id="session-drawer-title">Conversations</h2><button type="button" className="icon-button" aria-label="Close conversations" onClick={closeDrawer}><X size={18} /></button></div>
          {sessionNavigation}
        </div>
      </dialog> : <aside className="sidebar" aria-label="Conversations">
        <div className="sidebar-brand"><div className="brand-mark small"><Sparkles size={16} /></div><span>Lune</span><button className="icon-button sidebar-compose" title="New conversation" disabled={working} onClick={() => void createSession()}><SquarePen size={17} /></button></div>
        {sessionNavigation}
      </aside>}

      <main className="chat-area">
        <header className="chat-header">
          <div className="header-title">
            {compactLayout && <button type="button" className="icon-button drawer-toggle" title="Conversations" aria-label="Open conversations" aria-haspopup="dialog" aria-controls="session-drawer" aria-expanded={drawerOpen} onClick={openDrawer} ref={drawerButtonRef}><MessageSquare size={17} /></button>}
            <span>{activeSession ? describeSession(activeSession, history) : 'Loading conversation…'}</span></div>
          <div className="header-actions">
            <ThemePicker value={theme} onChange={setTheme} />
            {quick && <button className="plain-button" onClick={() => void window.luneDesktop?.toggleQuick()}>Main window</button>}
            {activeSession && <details className="conversation-menu">
              <summary>Conversation</summary>
              <div>
                <button disabled={working} onClick={() => setSessionDialog({ kind: 'rename', value: activeSession.title || '' })}>Rename</button>
                <button disabled={working} onClick={changeArchived}>{activeSession.archived ? 'Unarchive' : 'Archive'}</button>
                <button disabled={sessionBusy || working} onClick={() => setSessionDialog({ kind: 'delete', value: '' })}>Delete…</button>
                {has('session.fork') && <button disabled={taskIsRunning || working} onClick={forkSession}>Fork conversation</button>}
                <button disabled={working} onClick={saveConversation}>Export Markdown</button>
                {has('session.compact') && <button disabled={sessionBusy || working} onClick={compactSession}>Compact context</button>}
                {has('session.rewind') && <button disabled={sessionBusy || working || !history.length} onClick={() => setSessionDialog({ kind: 'rewind', value: '1' })}>Rewind…</button>}
                <label>Reasoning effort <select aria-label="Reasoning effort" value={activeSession.settings.reasoningEffort || ''} disabled={sessionBusy || working} onChange={(event) => void changeSettings({ reasoningEffort: event.target.value })}>
                  <option value="">Default</option>
                  {['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'].map((effort) => <option key={effort}>{effort}</option>)}
                </select></label>
              </div>
            </details>}
            <span className={`connection-pill ${connectionStatus}`}>{statusIcon}{statusLabel}</span>
            {models.length > 0 && <label className="model-picker"><span>MODEL</span><select value={selectedModel} disabled={!activeSession || taskIsRunning || taskIsQueued || taskIsPaused} onChange={(event) => void changeModel(event.target.value)}>{models.map((model) => <option value={model.profile} key={model.profile}>{model.displayName || model.profile}</option>)}</select><ChevronDown size={13} /></label>}
            <button className="icon-button header-new" title="New conversation" onClick={() => void createSession()}><FilePlus2 size={17} /></button>
          </div>
        </header>

        <div className="conversation-scroll">
          <div className="conversation-column">
            {displayedMessages.length === 0 && !streamingText && <div className="welcome-block">
              <div className="welcome-symbol"><Sparkles size={23} /></div><div className="welcome-kicker">LUNE CORE IS READY</div>
              <h2>What would you like<br />to work through?</h2>
              <p>Ask a question, explore an idea, or give Lune a task. Your conversation belongs to this session.</p>
              <div className="suggestion-row"><button onClick={() => setDraft('Help me understand this project.')}>Understand a project <ArrowDown size={13} /></button><button onClick={() => setDraft('Help me make a plan for…')}>Make a plan <ArrowDown size={13} /></button></div>
            </div>}
            {displayedMessages.map((message) => <article className={`message ${message.role}`} key={message.id}>
              {message.role === 'assistant' && <div className="assistant-avatar"><Sparkles size={14} /></div>}
              <div className="message-content"><div className="message-label">{message.role === 'user' ? 'YOU' : 'LUNE'}</div><div className="markdown-body"><ReactMarkdown>{message.text}</ReactMarkdown>
                {message.attachments?.map((file, index) => <details className="history-attachment" key={index}><summary>{file.name}</summary>
                  {file.image ? <img src={file.image} alt={file.name} /> : <pre>{file.text}</pre>}
                </details>)}
              </div></div>
            </article>)}
            {record.length > 0 && <details className="tool-record"><summary>Tool calls ({record.length})</summary>
              {record.map((item, index) => <details key={index}><summary>{item.name}</summary><pre>{item.arguments}</pre>{item.output !== undefined && <pre>{item.output}</pre>}</details>)}
            </details>}
            {streamingText && <article className="message assistant streaming-message"><div className="assistant-avatar"><Sparkles size={14} /></div><div className="message-content"><div className="message-label">LUNE <span className="streaming-indicator"><i /><i /><i /></span></div><div className="markdown-body"><ReactMarkdown>{streamingText}</ReactMarkdown><span className="caret" /></div></div></article>}

            {activity.length > 0 && (taskIsRunning || runtime.queue.length > 0) && <div className="activity-tray">{activity.slice(0, 3).map((item) => <div className={`activity-item ${item.failed ? 'failed' : ''}`} key={item.id}><span className="activity-dot" /> <span>{item.label}</span>{item.detail && <small>{item.detail}</small>}</div>)}</div>}

            {pending.map((item, index) => <div className="pending-card" key={`${item.kind}-${item.approval?.id ?? item.userQuestion?.id ?? item.interaction?.id ?? item.toolRecovery?.invocationId ?? index}`}>
              <div className="pending-heading"><div className="pending-icon"><CircleHelp size={17} /></div><div><strong>{item.kind === 'approval' ? 'Permission needed' : item.kind === 'user_question' ? 'A question for you' : item.kind === 'user_interaction' ? 'Input requested' : item.kind === 'tool_recovery' ? 'Tool needs attention' : item.kind === 'model_recovery' ? 'Run paused' : 'Execution blocked'}</strong><span>{item.kind === 'approval' ? `Lune wants to use ${item.approval?.tool ?? 'a tool'}` : 'Your response will continue this run.'}</span></div></div>
              {item.approval && <>
                {item.approval.autoReviewReason && <p className="pending-reason">{item.approval.autoReviewReason}</p>}
                <pre className="json-preview">{JSON.stringify(item.approval.arguments, null, 2)}</pre>
                <div className="pending-actions"><button className="plain-button" disabled={working} onClick={() => void resolveApproval(item.approval!, 'deny')}>Deny</button><button className="secondary-button" disabled={working} onClick={() => void resolveApproval(item.approval!, 'allow_once')}>Allow once</button><button className="primary-button compact" disabled={working} onClick={() => void resolveApproval(item.approval!, 'allow_session')}>Allow this session</button></div>
              </>}
              {item.userQuestion && <QuestionForm request={item.userQuestion} disabled={working} onSubmit={(answers) => resolveQuestion(item.userQuestion!, answers)} />}
              {item.interaction && <InteractionForm request={item.interaction} disabled={working} onSubmit={(value) => resolveInteraction(item.interaction!, value)} />}
              {item.toolRecovery && <><p className="pending-reason">The <strong>{item.toolRecovery.tool}</strong> tool did not finish cleanly. You can retry it or skip it.</p><pre className="json-preview">{JSON.stringify(item.toolRecovery.arguments, null, 2)}</pre><div className="pending-actions"><button className="secondary-button" disabled={working} onClick={() => void resolvePending('toolRecovery.resolve', { sessionId: activeSessionId, invocationId: item.toolRecovery!.invocationId, decision: 'skip' })}>Skip</button><button className="primary-button compact" disabled={working} onClick={() => void resolvePending('toolRecovery.resolve', { sessionId: activeSessionId, invocationId: item.toolRecovery!.invocationId, decision: 'retry' })}>Retry tool</button></div></>}
              {Boolean(item.modelRecovery) && <p className="pending-reason">This run has a saved checkpoint. Resume it when you are ready.</p>}
              {item.blocker && <p className="pending-reason">{item.blocker.reason}</p>}
            </div>)}

            {taskIsPaused && <div className="paused-banner"><div><strong>This run is paused</strong><span>Resume from its saved checkpoint, or discard it.</span></div><button className="plain-button" disabled={working} onClick={() => void discardPaused()}>Discard</button><button className="primary-button compact" disabled={working} onClick={() => void resumeRun()}><RefreshCw size={14} /> Resume run</button></div>}
            {runtime.queue.length > 0 && <div className="queue-banner"><Command size={15} /><div><strong>Message queue</strong>
              <ol>{runtime.queue.map((item) => <li key={item.executionId}>
                <span>#{item.position} · {item.status}</span>
                {has('turn.cancelQueued') && item.status === 'queued' && <button className="plain-button" disabled={working} onClick={() => cancelQueued(item.executionId)}>Cancel</button>}
              </li>)}</ol>
            </div></div>}
            <div ref={bottomRef} />
          </div>
        </div>

        <div className="composer-wrap">
          {info && <div className="form-status" role="status">{info}</div>}
          {!drawerOpen && actionError && <div className="action-error"><ShieldAlert size={15} /><span>{actionError}</span><button onClick={() => setActionError('')} aria-label="Dismiss"><X size={14} /></button></div>}
          {connectionStatus !== 'connected' && <div className="reconnect-banner"><LoaderCircle className="spin" size={14} /> {connectionStatus === 'reconnecting' ? 'Reconnecting to Core. Your current work continues there.' : 'Core connection lost.'}</div>}
          <div className="composer" onDragOver={(event) => { if (has('turn.attachments')) event.preventDefault() }} onDrop={(event) => {
            if (!has('turn.attachments')) return
            event.preventDefault()
            if (!working) addFiles(Array.from(event.dataTransfer.files))
          }}>
            {attachments.length > 0 && <ul className="attachment-list">{attachments.map((file, index) => <li key={`${file.name}-${index}`}>
              {file.mediaType.startsWith('image/') && <img alt="" src={`data:${file.mediaType};base64,${file.data}`} />}
              <span>{file.name}</span><button aria-label={`Remove ${file.name}`} disabled={working} onClick={() => setAttachments((items) => items.filter((_, at) => at !== index))}>×</button>
            </li>)}</ul>}
            <textarea value={draft} onChange={(event) => setDraft(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); void sendMessage() } }} placeholder={runtime.queue.length || taskIsRunning ? 'Add a message to the queue…' : 'Message Lune…'} rows={1} disabled={!client || !activeSession || working} />
            <div className="composer-footer">
              <div className="composer-options">
                {has('turn.attachments') && <>
                  <input type="file" ref={fileInput} hidden multiple accept=".txt,.md,.markdown,.csv,.png,.jpg,.jpeg" onChange={(event) => {
                    addFiles(Array.from(event.target.files ?? []))
                    event.target.value = ''
                  }} />
                  <button className="plain-button" disabled={working} onClick={() => fileInput.current?.click()}>Attach</button>
                  {desktop && modelOption?.supportsInputImages && <button className="plain-button" disabled={working} onClick={captureScreenshot}>Screenshot</button>}
                </>}
                {has('turn.plan') && <button className="plain-button plan-button" aria-pressed={planMode} disabled={working} onClick={() => setPlanMode(!planMode)} title="Use read-only tools for this turn">Plan</button>}
              </div>
              <div className="composer-actions">
                {taskIsRunning && runtime.task.executionId && !planMode && <button className="plain-button" disabled={working || (!draft.trim() && !attachments.length) || connectionStatus !== 'connected'} onClick={() => void sendMessage(true)}>Add to running task</button>}
                {((taskIsRunning && runtime.task.executionId) || (taskIsQueued && !has('turn.cancelQueued'))) && <button className="stop-button" disabled={working} onClick={() => void stopRun()}><CircleStop size={15} />{taskIsQueued ? 'Cancel queued' : 'Stop'}</button>}
                <button className="send-button" disabled={!activeSession || (!draft.trim() && !attachments.length) || working || connectionStatus !== 'connected'} onClick={() => void sendMessage()} aria-label="Send message">{working ? <LoaderCircle size={17} className="spin" /> : <ArrowUp size={17} />}</button>
              </div>
            </div>
          </div>
          {activeSession?.sessionUsage && <div className="composer-note">
            Session tokens · {activeSession.sessionUsage.input.toLocaleString()} input · {activeSession.sessionUsage.output.toLocaleString()} output
            {contextLimit ? ` · ${contextLimit.toLocaleString()} context limit` : ''}
          </div>}
        </div>
      </main>

      {sessionDialog && <div className="modal-backdrop">
        <section className="connection-modal" role="dialog" aria-modal="true" aria-labelledby="session-dialog-title">
          <h2 id="session-dialog-title">{sessionDialog.kind === 'rename' ? 'Rename conversation' : sessionDialog.kind === 'delete' ? 'Delete conversation?' : 'Rewind conversation'}</h2>
          {sessionDialog.kind === 'delete' ? <p>This removes the conversation and its saved history.</p> :
            <label className="form-field">{sessionDialog.kind === 'rename' ? 'Title' : 'User turns to remove'}
              <input value={sessionDialog.value} type={sessionDialog.kind === 'rewind' ? 'number' : 'text'} min={1}
                onChange={(event) => setSessionDialog({ kind: sessionDialog.kind, value: event.target.value })} />
            </label>}
          {Boolean(sessionDialog.excerpts?.length) && <ol className="rewind-excerpts">{sessionDialog.excerpts?.map((excerpt, index) => <li key={index}>{excerpt}</li>)}</ol>}
          {sessionDialog.removed !== undefined && <p>{sessionDialog.removed} turn(s) will be removed from the end of the conversation.</p>}
          {actionError && <div className="form-error" role="alert">{actionError}</div>}
          <div className="modal-footer"><button className="plain-button" disabled={working} onClick={() => setSessionDialog(null)}>Cancel</button><button className="primary-button compact" disabled={working || sessionDialog.removed === 0} onClick={confirmSessionDialog}>
            {sessionDialog.kind === 'rename' ? 'Save' : sessionDialog.kind === 'delete' ? 'Delete' : sessionDialog.digest ? 'Rewind' : 'Preview'}
          </button></div>
        </section>
      </div>}

      {showConnection && <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) setShowConnection(false) }}>
        <section className="connection-modal" role="dialog" aria-modal="true" aria-labelledby="connection-modal-title">
          <div className="modal-top"><div className="brand-mark small"><Settings2 size={15} /></div><button className="icon-button" onClick={() => setShowConnection(false)} aria-label="Close"><X size={17} /></button></div>
          <div className="eyebrow">CONNECTION</div><h2 id="connection-modal-title">Connect to a Core</h2><p className="modal-copy">Change the Core that serves this frontend. Your sessions are stored with that Core.</p>
          {desktop && <div className="connection-tabs compact-tabs"><button className={mode === 'local' ? 'active' : ''} onClick={() => setMode('local')}><Laptop size={15} /> This computer</button><button className={mode === 'remote' ? 'active' : ''} onClick={() => setMode('remote')}><Radio size={15} /> Remote Core</button></div>}
          {mode === 'local' && desktop ? <div className="form-field"><label>Workspace folder</label><div className="input-with-action"><input value={workspace} onChange={(event) => setWorkspace(event.target.value)} placeholder="Choose a folder with config.json" /><button onClick={() => void chooseWorkspace()}><FolderOpen size={16} /></button></div><small>Requires a workspace config.json with a model provider.</small></div> : <><div className="form-field"><label>Core WebSocket URL</label><input value={remoteURL} onChange={(event) => setRemoteURL(event.target.value)} autoCapitalize="none" spellCheck={false} /></div><div className="form-field"><label>Access token</label><input type="password" value={token} onChange={(event) => setToken(event.target.value)} autoComplete="off" /></div></>}
          {(connectionError || connectionStatus === 'reconnecting') && <div className="form-error"><ShieldAlert size={15} /><span>{connectionError || 'The connection was interrupted. Reconnect to continue.'}</span></div>}
          {desktop && <div className="form-field"><label htmlFor="quick-shortcut">Quick window shortcut</label>
            <div className="input-with-action"><input id="quick-shortcut" value={shortcut} onChange={(event) => setShortcut(event.target.value)} />
              <button onClick={() => void window.luneDesktop?.setShortcut(shortcut).then(setShortcut).catch((error) => setConnectionError(errorMessage(error)))}>Set</button>
            </div>
          </div>}
          <div className="modal-footer"><span>{connectionStatus === 'connected' ? <><span className="connection-light connected" /> Connected</> : statusLabel}</span><button className="primary-button compact" disabled={connectDisabled} onClick={() => { setBusy(true); const options: RuntimeConnection = mode === 'local' ? { mode: 'local', workspace } : { mode: 'remote', url: remoteURL.trim(), token }; void activateConnection(options).finally(() => setBusy(false)) }}>{busy ? <LoaderCircle size={15} className="spin" /> : <RefreshCw size={14} />}{connectionStatus === 'connected' ? 'Reconnect' : 'Connect'}</button></div>
        </section>
      </div>}
    </div>
  )
}

function QuestionForm({ request, disabled, onSubmit }: {
  request: NonNullable<Pending['userQuestion']>
  disabled: boolean
  onSubmit: (answers: Array<{ question_id: string; answer: string }>) => void
}) {
  const [answers, setAnswers] = useState<Record<string, string>>({})
  const questions = request.questions ?? []
  const complete = questions.length > 0 && questions.every((question) => (answers[question.id] ?? '').trim())
  return <div className="question-form">
    {questions.map((question) => <fieldset className="question-item" key={question.id}><legend>{question.question}</legend>
      {question.options?.length ? <div className="question-options">{question.options.map((option) => <label className={`question-option ${answers[question.id] === option.label ? 'chosen' : ''}`} key={option.label}><input type="radio" name={question.id} value={option.label} checked={answers[question.id] === option.label} onChange={() => setAnswers((value) => ({ ...value, [question.id]: option.label }))} /><span><strong>{option.label}{option.recommended && <em>Suggested</em>}</strong><small>{option.description}</small></span></label>)}</div> : <input className="text-answer" value={answers[question.id] ?? ''} onChange={(event) => setAnswers((value) => ({ ...value, [question.id]: event.target.value }))} placeholder="Your answer" />}
    </fieldset>)}
    {!questions.length && <p className="pending-reason">This clarification needs an answer, but Core did not provide question details.</p>}
    <div className="pending-actions"><button className="primary-button compact" disabled={disabled || !complete} onClick={() => onSubmit(questions.map((question) => ({ question_id: question.id, answer: answers[question.id].trim() })))}><Check size={14} /> Send answer</button></div>
  </div>
}

function InteractionForm({ request, disabled, onSubmit }: {
  request: NonNullable<Pending['interaction']>
  disabled: boolean
  onSubmit: (value: unknown) => void
}) {
  const [value, setValue] = useState('{\n  "action": "accept",\n  "content": {}\n}')
  const [error, setError] = useState('')
  return <div className="interaction-form"><p className="pending-reason">{request.serverName} is asking for input.</p><pre className="json-preview">{JSON.stringify(request.request, null, 2)}</pre><label className="interaction-label">Response JSON</label><textarea value={value} onChange={(event) => { setValue(event.target.value); setError('') }} spellCheck={false} /><div className="pending-actions">{error && <small className="inline-error">{error}</small>}<button className="primary-button compact" disabled={disabled} onClick={() => { try { onSubmit(JSON.parse(value)) } catch { setError('Enter a valid JSON response.') } }}><Check size={14} /> Send response</button></div></div>
}
