import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import {
  ArrowDown, ArrowUp, Check, ChevronDown, CircleHelp, CircleStop, Code2, Command,
  FilePlus2, FolderOpen, Laptop, LoaderCircle, LockKeyhole, MessageSquare, Plus,
  Radio, RefreshCw, Search, Settings2, ShieldAlert, Sparkles, SquarePen, Wifi,
  WifiOff, X,
} from 'lucide-react'
import { connectRuntime, type RuntimeClient, type RuntimeConnection } from './runtime'

type JsonObject = Record<string, unknown>
type SessionSummary = {
  id: string
  title: string
  archived: boolean
  updatedAt: string
  settings: { modelProfile: string; reasoningEffort?: string }
}
type ModelOption = { profile: string; provider: string; model: string; displayName?: string }
type TaskState = { status: string; executionId?: string; taskId?: string; goal?: string }
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
type HistoryMessage = { id: string; role: 'user' | 'assistant'; text: string }
type OptimisticMessage = HistoryMessage & { baselineCount: number; sessionId: string }
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
  const items = Array.isArray(history) ? history : Array.isArray(object(history).items) ? object(history).items as unknown[] : []
  return items.flatMap((raw, index) => {
    const item = object(raw)
    if (item.type !== 'message' || (item.role !== 'user' && item.role !== 'assistant')) return []
    const text = contentText(item.content ?? item.parts)
    if (!text.trim()) return []
    return [{ id: `${index}-${String(item.role)}`, role: item.role, text }]
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

function describeSession(session: SessionSummary, messages: HistoryMessage[]): string {
  return session.title || messages.find((message) => message.role === 'user')?.text.slice(0, 56) || 'New conversation'
}

export default function App() {
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
  const [busy, setBusy] = useState(false)
  const [working, setWorking] = useState(false)
  const [actionError, setActionError] = useState('')

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
  const bottomRef = useRef<HTMLDivElement | null>(null)

  const filteredSessions = useMemo(() => {
    const term = search.trim().toLowerCase()
    return sessions.filter((session) => !session.archived && (!term || (session.title || '').toLowerCase().includes(term) || session.id.toLowerCase().includes(term)))
  }, [sessions, search])
  const displayedMessages = useMemo(() => [...history, ...optimistic.filter((message) => message.sessionId === activeSessionId)], [history, optimistic, activeSessionId])
  const selectedModel = activeSession?.settings.modelProfile || models[0]?.profile || ''
  const taskIsRunning = runtime.task.status === 'running'
  const taskIsQueued = runtime.task.status === 'queued'
  const taskIsPaused = runtime.task.status === 'paused'

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
      const hello = await candidate.request<{ protocolVersion?: string }>('runtime.hello', {
        clientName: desktop ? 'Lune Desktop' : 'Lune Web',
        clientVersion: '0.1.0',
      })
      if (hello?.protocolVersion !== '1') throw new Error(`Unsupported Core protocol version: ${hello?.protocolVersion ?? 'unknown'}`)
      if (!autoReconnect.current && retry) {
        await candidate.close()
        return
      }
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
    const [summary, rawHistory, nextRuntime, nextPending] = await Promise.all([
      targetClient.request<SessionSummary>('session.get', { sessionId }),
      targetClient.request<unknown>('session.history', { sessionId }),
      targetClient.request<RuntimeState>('runtime.state', { sessionId }),
      targetClient.request<Pending[]>('runtime.pending', { sessionId }),
    ])
    const messages = historyMessages(rawHistory)
    setActiveSession(summary)
    setHistory(messages)
    setOptimistic((items) => {
      const counts = new Map<string, number>()
      for (const message of messages) counts.set(message.text, (counts.get(message.text) ?? 0) + 1)
      const seen = new Map<string, number>()
      return items.filter((item) => {
        if (item.sessionId !== sessionId) return true
        const count = counts.get(item.text) ?? 0
        const alreadySeen = seen.get(item.text) ?? 0
        seen.set(item.text, alreadySeen + 1)
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
    setRuntime(normalizeRuntimeState(nextRuntime))
    setPending(nextPending ?? [])
  }, [])

  useEffect(() => {
    if (!client || initializedClient.current === client) return
    initializedClient.current = client
    let cancelled = false
    void Promise.all([
      client.request<SessionSummary[]>('session.list', { limit: 100, includeArchived: false }),
      client.request<ModelOption[]>('model.list'),
    ]).then(async ([nextSessions, nextModels]) => {
      if (cancelled) return
      setSessions(nextSessions ?? [])
      setModels(nextModels ?? [])
      const existing = nextSessions?.find((item) => item.id === activeSessionIdRef.current) ?? nextSessions?.[0]
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
        setStreamingText('')
        setRuntime((state) => ({ ...state, task: { ...state.task, status: 'running', executionId: event.executionId } }))
        setActivity((items) => [{ id: `${event.taskId}-started`, label: 'Working on your request', detail: event.executionId }, ...items].slice(0, 5))
      } else if (event.kind === 'task_finished' || event.kind === 'task_discarded') {
        setStreamingText('')
        setActivity((items) => [{
          id: `${event.executionId}-${event.kind}`,
          label: event.kind === 'task_discarded' ? 'Run discarded' : `Run ${event.status ?? 'finished'}`,
          detail: event.error,
          failed: ['failed', 'cancelled'].includes(event.status ?? ''),
        }, ...items].slice(0, 5))
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
      await targetClient.request('runtime.subscribe', { sessionId, subscriptionId, afterCursor: cursor })
      if (!cancelled) subscriptionRef.current = subscriptionId
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

  const createSession = async () => {
    if (!client) return
    setActionError('')
    try {
      const created = await client.request<SessionSummary>('session.create')
      setSessions((items) => [created, ...items])
      setActiveSessionId(created.id)
      setHistory([])
      setOptimistic([])
      setActivity([])
      setStreamingText('')
    } catch (error) { setActionError(errorMessage(error)) }
  }

  const sendMessage = async () => {
    const text = draft.trim()
    if (!client || !activeSessionId || !text || working) return
    setWorking(true)
    setActionError('')
    setDraft('')
    const baselineCount = history.filter((message) => message.text === text).length
    const submissionId = crypto.randomUUID()
    setOptimistic((items) => [...items, { id: submissionId, role: 'user', text, baselineCount, sessionId: activeSessionId }])
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
      const mode = taskIsRunning || runtime.queue.length > 0 ? 'queue_input' : 'start_if_idle'
      await client.request('turn.submit', { sessionId: activeSessionId, submissionId, input: text, mode })
      await refreshRuntime(client, activeSessionId)
    } catch (error) {
      setOptimistic((items) => items.filter((item) => item.id !== submissionId))
      setDraft(text)
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

  const changeModel = async (profile: string) => {
    if (!client || !activeSessionId || !activeSession) return
    try {
      await client.request('session.setSettings', { sessionId: activeSessionId, settings: { ...activeSession.settings, modelProfile: profile } })
      await refreshSessionData(client, activeSessionId)
    } catch (error) { setActionError(errorMessage(error)) }
  }

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

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="sidebar-brand"><div className="brand-mark small"><Sparkles size={16} /></div><span>Lune</span><button className="icon-button sidebar-compose" title="New conversation" onClick={() => void createSession()}><SquarePen size={17} /></button></div>
        <button className="new-chat-button" onClick={() => void createSession()}><Plus size={16} /> New conversation</button>
        <div className="sidebar-section-head"><span>RECENT</span><button className="subtle-icon" title="Refresh sessions" onClick={() => client.request<SessionSummary[]>('session.list', { limit: 100 }).then(setSessions).catch((error) => setActionError(errorMessage(error)))}><RefreshCw size={13} /></button></div>
        <div className="session-search"><Search size={14} /><input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Find a conversation" /></div>
        <div className="session-list">
          {filteredSessions.map((session) => {
            const preview = session.title || `Conversation ${session.id.slice(0, 8)}`
            return <button key={session.id} className={`session-item ${session.id === activeSessionId ? 'selected' : ''}`} onClick={() => setActiveSessionId(session.id)}>
              <MessageSquare size={15} /><span>{preview}</span><span className="session-time">{new Date(session.updatedAt).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}</span>
            </button>
          })}
          {!filteredSessions.length && <div className="empty-sessions">No conversations yet.<br />Start one when you're ready.</div>}
        </div>
        <div className="sidebar-bottom">
          <div className="core-card"><div className="core-card-icon"><Code2 size={16} /></div><div className="core-card-copy"><strong>{connection?.mode === 'local' ? 'Local Core' : 'Remote Core'}</strong><span>{connection?.mode === 'local' ? workspace.split(/[\\/]/).filter(Boolean).at(-1) || 'This computer' : connection?.url}</span></div><span className={`connection-light ${connectionStatus}`} /></div>
          <button className="settings-button" onClick={() => setShowConnection(true)}><Settings2 size={16} /> Connection settings <ChevronDown size={14} /></button>
          <button className="disconnect-button" onClick={() => void disconnect()}><X size={15} /> Disconnect</button>
        </div>
      </aside>

      <main className="chat-area">
        <header className="chat-header">
          <div className="header-title"><div className="mobile-brand"><div className="brand-mark small"><Sparkles size={16} /></div>Lune</div><span>{activeSession ? describeSession(activeSession, history) : 'Loading conversation…'}</span></div>
          <div className="header-actions">
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
              <div className="message-content"><div className="message-label">{message.role === 'user' ? 'YOU' : 'LUNE'}</div><div className="markdown-body"><ReactMarkdown>{message.text}</ReactMarkdown></div></div>
            </article>)}
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
            {runtime.queue.length > 0 && <div className="queue-banner"><div className="queue-icon"><Command size={15} /></div><div><strong>{runtime.queue.length} {runtime.queue.length === 1 ? 'message' : 'messages'} in the queue</strong><span>They will run in order after the current turn.</span></div><span className="queue-count">{runtime.queue.length}</span></div>}
            <div ref={bottomRef} />
          </div>
        </div>

        <div className="composer-wrap">
          {actionError && <div className="action-error"><ShieldAlert size={15} /><span>{actionError}</span><button onClick={() => setActionError('')} aria-label="Dismiss"><X size={14} /></button></div>}
          {connectionStatus !== 'connected' && <div className="reconnect-banner"><LoaderCircle className="spin" size={14} /> {connectionStatus === 'reconnecting' ? 'Reconnecting to Core. Your current work continues there.' : 'Core connection lost.'}</div>}
          <div className="composer">
            <textarea value={draft} onChange={(event) => setDraft(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); void sendMessage() } }} placeholder={runtime.queue.length || taskIsRunning ? 'Add a message to the queue…' : 'Message Lune…'} rows={1} disabled={!client || working} />
            <div className="composer-footer"><span><kbd>↵</kbd> to send <span className="composer-separator">·</span> <kbd>⇧ ↵</kbd> for new line</span><div className="composer-actions">{(taskIsRunning || taskIsQueued) && <button className="stop-button" disabled={working} onClick={() => void stopRun()}><CircleStop size={15} />{taskIsQueued ? 'Cancel queued' : 'Stop'}</button>}<button className="send-button" disabled={!draft.trim() || working || connectionStatus !== 'connected'} onClick={() => void sendMessage()} aria-label="Send message">{working ? <LoaderCircle size={17} className="spin" /> : <ArrowUp size={17} />}</button></div></div>
          </div>
          <div className="composer-note">Lune can make mistakes. Review important information.</div>
        </div>
      </main>

      {showConnection && <div className="modal-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) setShowConnection(false) }}>
        <section className="connection-modal" role="dialog" aria-modal="true" aria-labelledby="connection-modal-title">
          <div className="modal-top"><div className="brand-mark small"><Settings2 size={15} /></div><button className="icon-button" onClick={() => setShowConnection(false)} aria-label="Close"><X size={17} /></button></div>
          <div className="eyebrow">CONNECTION</div><h2 id="connection-modal-title">Connect to a Core</h2><p className="modal-copy">Change the Core that serves this frontend. Your sessions are stored with that Core.</p>
          {desktop && <div className="connection-tabs compact-tabs"><button className={mode === 'local' ? 'active' : ''} onClick={() => setMode('local')}><Laptop size={15} /> This computer</button><button className={mode === 'remote' ? 'active' : ''} onClick={() => setMode('remote')}><Radio size={15} /> Remote Core</button></div>}
          {mode === 'local' && desktop ? <div className="form-field"><label>Workspace folder</label><div className="input-with-action"><input value={workspace} onChange={(event) => setWorkspace(event.target.value)} placeholder="Choose a folder with config.json" /><button onClick={() => void chooseWorkspace()}><FolderOpen size={16} /></button></div><small>Requires a workspace config.json with a model provider.</small></div> : <><div className="form-field"><label>Core WebSocket URL</label><input value={remoteURL} onChange={(event) => setRemoteURL(event.target.value)} autoCapitalize="none" spellCheck={false} /></div><div className="form-field"><label>Access token</label><input type="password" value={token} onChange={(event) => setToken(event.target.value)} autoComplete="off" /></div></>}
          {(connectionError || connectionStatus === 'reconnecting') && <div className="form-error"><ShieldAlert size={15} /><span>{connectionError || 'The connection was interrupted. Reconnect to continue.'}</span></div>}
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
