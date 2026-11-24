import { useCallback, useEffect, useId, useMemo, useRef, useState, type CSSProperties } from 'react'
import { createDesktopSettingsAPI, createRemoteOnlySettingsAPI } from './settings-api'
import { formatCapacity, SettingsModal, type SettingsTab } from './settings-modal'
import type { SavedConnection } from '../shared/desktop-settings'
import { lastLoopbackSession, rememberLoopbackSession } from './loopback'
import { LanguagePicker, useLanguage, useTranslate } from './language'
import type { ManagedWorkspace, WorkspaceActivity } from './workbench'
import { type WorkspaceInfo } from './workspace-files'
import { applyTheme, ThemePicker } from './theme'
import type { ThemePreference } from '../shared/theme'
import { defaultUiPreferences, type UiPreferences } from '../shared/ui-preferences'
import { MarkdownContent } from './markdown'
import { HarnessPanel } from './harness-panel'
import { TimelineView } from './timeline'
import { matchSlashCommands, parseSlashCommand, slashCommands } from './slash-commands'
import {
  buildTimeline, objectValue as object, timelineMessages,
  type Activity, type CommandInfo, type HarnessTab, type HistoryMessage, type LiveToolStep,
  type ModelFailure, type ModelOption, type OptimisticMessage, type Pending, type RuntimeEvent,
  type RuntimeState, type SessionSummary, type TimelineEntry,
} from './harness-types'
import {
  Archive, ArrowUp, ArrowUpRight, Brain, Camera, Check, ChevronDown, CircleHelp, Code2,
  Command, Compass, CornerDownRight, Download, Folder, FolderOpen, GitFork, Laptop,
  LoaderCircle, LockKeyhole, Minimize2, MoreHorizontal, PanelLeftClose,
  PanelLeftOpen, PanelRight, Pause, Pencil, Play, Plus, Radio, RefreshCw,
  RotateCcw, Search, Settings, ShieldAlert, Sparkles,
  Square, SquarePen, Target, Terminal, Trash2, Wifi, WifiOff, X,
} from 'lucide-react'
import { downloadMarkdown, readAttachment, validateAttachments, type Attachment } from './attachments'
import { connectRuntime, type RuntimeClient, type RuntimeConnection } from './runtime'

const sessionPageSize = 50

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
    goal: value.goal ?? null,
    task: value.task ?? emptyRuntime.task,
    queue: Array.isArray(value.queue) ? value.queue : [],
  }
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

function describeSession(session: SessionSummary, messages: HistoryMessage[], fallback = 'New conversation'): string {
  return session.title || messages.find((message) => message.role === 'user')?.text.slice(0, 56) || fallback
}

type SessionGroupLabel = 'Today' | 'Yesterday' | 'Previous 7 Days' | 'Older'

function groupSessionsByTime(items: SessionSummary[]): Array<{ label: SessionGroupLabel; items: SessionSummary[] }> {
  const now = new Date()
  const todayStart = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime()
  const yesterdayStart = todayStart - 86_400_000
  const weekStart = todayStart - 7 * 86_400_000

  const buckets: Record<SessionGroupLabel, SessionSummary[]> = {
    'Today': [],
    'Yesterday': [],
    'Previous 7 Days': [],
    'Older': [],
  }
  for (const item of items) {
    const time = Date.parse(item.updatedAt)
    if (Number.isNaN(time) || time >= todayStart) buckets['Today'].push(item)
    else if (time >= yesterdayStart) buckets['Yesterday'].push(item)
    else if (time >= weekStart) buckets['Previous 7 Days'].push(item)
    else buckets['Older'].push(item)
  }
  return (['Today', 'Yesterday', 'Previous 7 Days', 'Older'] as const)
    .filter((label) => buckets[label].length > 0)
    .map((label) => ({ label, items: buckets[label] }))
}

const webLastURLKey = 'lune:web-remote-url'
const webSessionConnKey = 'lune:web-session-connection'
const webRememberedConnKey = 'lune:web-remembered-connection'

function webLastSessionKey(url: string): string {
  return `lune:web-last-session:${url}`
}

function defaultRemoteURL(isDesktop: boolean): string {
  if (!isDesktop) {
    try {
      const saved = localStorage.getItem(webLastURLKey)?.trim()
      if (saved && saved.startsWith('wss://')) return saved
    } catch { /* ignore storage errors */ }
    if (typeof location !== 'undefined') {
      if (location.protocol === 'https:' && location.host) return `wss://${location.host}/v1/runtime`
      if (location.protocol === 'http:' && location.hostname) return `wss://${location.hostname}:18092/v1/runtime`
    }
  }
  return 'wss://localhost:18092/v1/runtime'
}

function normalizeRemoteURL(raw: string): string {
  let value = raw.trim()
  if (!value) return value
  if (value.startsWith('https://')) value = `wss://${value.slice('https://'.length)}`
  if (value.startsWith('wss://')) {
    try {
      const parsed = new URL(value)
      if (!parsed.pathname || parsed.pathname === '/') {
        parsed.pathname = '/v1/runtime'
        return parsed.href
      }
    } catch { /* let connectRuntime report invalid URL details */ }
  }
  return value
}

function readStoredConnection(storage: Storage, key: string): { url: string; token: string } | null {
  try {
    const raw = storage.getItem(key)
    if (!raw) return null
    const parsed = JSON.parse(raw) as { url?: unknown; token?: unknown }
    if (typeof parsed.url === 'string' && parsed.url.startsWith('wss://') && typeof parsed.token === 'string' && parsed.token.trim()) {
      return { url: parsed.url, token: parsed.token }
    }
  } catch { /* ignore malformed storage */ }
  return null
}

function storedWidth(key: string, fallback: number, min: number, max: number): number {
  try {
    const value = Number(localStorage.getItem(key))
    if (Number.isFinite(value) && value >= min && value <= max) return value
  } catch { /* use the default layout */ }
  return fallback
}

function loadSavedWebConnection(): { url: string; token: string; remembered: boolean } | null {
  const fromLocal = typeof localStorage !== 'undefined' ? readStoredConnection(localStorage, webRememberedConnKey) : null
  const fromSession = typeof sessionStorage !== 'undefined' ? readStoredConnection(sessionStorage, webSessionConnKey) : null
  if (fromSession) return { ...fromSession, remembered: Boolean(fromLocal) }
  if (fromLocal) return { ...fromLocal, remembered: true }
  return null
}

function saveWebConnection(url: string, token: string, remember: boolean): void {
  const payload = JSON.stringify({ url, token })
  try { localStorage.setItem(webLastURLKey, url) } catch { /* ignore storage errors */ }
  try { sessionStorage.setItem(webSessionConnKey, payload) } catch { /* ignore storage errors */ }
  try {
    if (remember) localStorage.setItem(webRememberedConnKey, payload)
    else localStorage.removeItem(webRememberedConnKey)
  } catch { /* ignore storage errors */ }
}

function clearWebConnection(clearURL = false): void {
  try { sessionStorage.removeItem(webSessionConnKey) } catch { /* ignore */ }
  try { localStorage.removeItem(webRememberedConnKey) } catch { /* ignore */ }
  if (clearURL) {
    try { localStorage.removeItem(webLastURLKey) } catch { /* ignore */ }
  }
}

export default function App({ initialTheme, managed, pairingError, onThemeChange }: {
  initialTheme: ThemePreference; managed?: ManagedWorkspace; pairingError?: string; onThemeChange?: (theme: ThemePreference) => void
}) {
  const [privateTheme, setPrivateTheme] = useState(initialTheme)
  const theme = onThemeChange ? initialTheme : privateTheme
  const setTheme = onThemeChange ?? setPrivateTheme
  const t = useTranslate()
  const { setLanguage } = useLanguage()
  const accessibilityId = useId()
  const desktop = Boolean(window.luneDesktop)
  const initialWebSaved = useMemo(() => (!desktop && !managed ? loadSavedWebConnection() : null), [desktop, managed])
  const [client, setClient] = useState<RuntimeClient | null>(null)
  const [connection, setConnection] = useState<RuntimeConnection | null>(null)
  const [connectionStatus, setConnectionStatus] = useState<'offline' | 'connecting' | 'connected' | 'reconnecting'>('offline')
  const [connectionError, setConnectionError] = useState('')
  const [showConnection, setShowConnection] = useState(true)
  const [autoRestoring, setAutoRestoring] = useState(() => Boolean(!desktop && !managed && initialWebSaved))
  const [mode, setMode] = useState<'local' | 'remote'>(desktop ? 'local' : 'remote')
  const [workspace, setWorkspace] = useState('')
  const [remoteURL, setRemoteURL] = useState(() => initialWebSaved?.url || defaultRemoteURL(desktop))
  const [token, setToken] = useState(() => initialWebSaved?.token || '')
  const [remoteName, setRemoteName] = useState('')
  const [rememberToken, setRememberToken] = useState(() => (desktop ? false : (initialWebSaved?.remembered ?? true)))
  const [savedConnections, setSavedConnections] = useState<SavedConnection[]>([])
  const [connectionNotice, setConnectionNotice] = useState('')
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [settingsTab, setSettingsTab] = useState<SettingsTab>('models')
  const [configReconnectRequired, setConfigReconnectRequired] = useState(false)
  const [applyingConfig, setApplyingConfig] = useState(false)
  const [harnessOpen, setHarnessOpen] = useState(false)
  const [harnessTab, setHarnessTab] = useState<HarnessTab>('files')
  const [requestedFile, setRequestedFile] = useState('')
  const [requestedCommandId, setRequestedCommandId] = useState('')
  const [workspaceInfo, setWorkspaceInfo] = useState<WorkspaceInfo | null>(null)
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [models, setModels] = useState<ModelOption[]>([])
  const [activeSessionId, setActiveSessionId] = useState('')
  const [activeSession, setActiveSession] = useState<SessionSummary | null>(null)
  const [timeline, setTimeline] = useState<TimelineEntry[]>([])
  const [history, setHistory] = useState<HistoryMessage[]>([])
  const [optimistic, setOptimistic] = useState<OptimisticMessage[]>([])
  const [runtime, setRuntime] = useState<RuntimeState>(emptyRuntime)
  const [uiPreferences, setUiPreferences] = useState<UiPreferences>(defaultUiPreferences)
  const [commands, setCommands] = useState<CommandInfo[]>([])
  const [pending, setPending] = useState<Pending[]>([])
  const [modelFailure, setModelFailure] = useState<ModelFailure | null>(null)
  const [recoveryClock, setRecoveryClock] = useState(Date.now())
  const [streamingReasoning, setStreamingReasoning] = useState('')
  const [streamingText, setStreamingText] = useState('')
  const [liveTools, setLiveTools] = useState<LiveToolStep[]>([])
  const [latestExecutionId, setLatestExecutionId] = useState('')
  const [activity, setActivity] = useState<Activity[]>([])
  const [draft, setDraft] = useState('')
  const [slashIndex, setSlashIndex] = useState(0)
  const [slashNotice, setSlashNotice] = useState('')
  const [rowMenuId, setRowMenuId] = useState('')
  const [search, setSearch] = useState('')
  const [showArchived, setShowArchived] = useState(false)
  const [sessionOffset, setSessionOffset] = useState(0)
  const [moreSessions, setMoreSessions] = useState(false)
  const [listLoading, setListLoading] = useState(false)
  const [listRevision, setListRevision] = useState(0)
  const [capabilities, setCapabilities] = useState<string[]>([])
  const [planMode, setPlanMode] = useState(false)
  const [attachments, setAttachments] = useState<Attachment[]>([])
  const [info, setInfo] = useState('')
  const [quick, setQuick] = useState(false)
  const [narrow, setNarrow] = useState(() => window.matchMedia('(max-width: 680px)').matches)
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [sidebarWidth, setSidebarWidth] = useState(() => storedWidth('lune:sidebar-width', 260, 200, 420))
  const [harnessWidth, setHarnessWidth] = useState(() => storedWidth('lune:harness-width', 420, 320, 760))
  const [harnessFullScreen, setHarnessFullScreen] = useState(false)
  const [harnessOverlay, setHarnessOverlay] = useState(() => window.innerWidth < 1100)
  const [searchOpen, setSearchOpen] = useState(false)
  const [composerMenu, setComposerMenu] = useState<'model' | 'effort' | null>(null)
  const [draggingColumn, setDraggingColumn] = useState<'sidebar' | 'harness' | null>(null)
  const compactLayout = narrow || quick
  const [sessionDialog, setSessionDialog] = useState<null | {
    kind: 'rename' | 'delete' | 'rewind'; sessionId: string; value: string; digest?: string; removed?: number; excerpts?: string[]
  }>(null)
  const [busy, setBusy] = useState(false)
  const [working, setWorking] = useState(false)
  const [actionError, setActionError] = useState('')

  const capabilitiesRef = useRef<string[]>([])
  const submissionRetry = useRef<{ key: string; id: string; mode: string } | null>(null)
  const columnDrag = useRef<{ side: 'sidebar' | 'harness'; x: number; width: number } | null>(null)
  const clientRef = useRef<RuntimeClient | null>(null)
  const connectionRef = useRef<RuntimeConnection | null>(null)
  const autoReconnect = useRef(false)
  const rememberTokenRef = useRef(rememberToken)
  rememberTokenRef.current = rememberToken
  const autoRestoringRef = useRef(autoRestoring)
  autoRestoringRef.current = autoRestoring
  const attemptedAutoRestore = useRef(false)
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
  const fileButtonRef = useRef<HTMLButtonElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  useEffect(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`
  }, [draft])

  useEffect(() => {
    const onPointerDown = (event: PointerEvent) => {
      if (event.target instanceof Element) {
        if (!event.target.closest('.session-row-menu')) {
          setRowMenuId('')
        }
        if (!event.target.closest('.composer-picker-group')) {
          setComposerMenu(null)
        }
      }
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setRowMenuId('')
        setComposerMenu(null)
      }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'b' && !(event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement)) {
        event.preventDefault()
        setHarnessOpen((open) => !open)
      }
    }
    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [])

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
  const sessionGroups = useMemo(() => groupSessionsByTime(filteredSessions), [filteredSessions])
  const slashMatches = useMemo(() => matchSlashCommands(draft, capabilities), [draft, capabilities])
  useEffect(() => { setSlashIndex(0) }, [slashMatches.length, draft])
  const displayedMessages = useMemo(() => [...history, ...optimistic.filter((message) => message.sessionId === activeSessionId)], [history, optimistic, activeSessionId])
  const selectedModel = activeSession?.settings.modelProfile || models[0]?.profile || ''
  const taskIsRunning = runtime.task.status === 'running'
  const taskIsQueued = runtime.task.status === 'queued'
  const taskIsPaused = runtime.task.status === 'paused'
  const taskHasQueuedWork = taskIsQueued || runtime.queue.some((item) => item.status === 'queued' || item.status === 'active')
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
    return desktopAPI.onQuickMode(setQuick)
  }, [])

  const reloadSavedConnections = useCallback(async () => {
    const saved = await window.luneDesktop?.savedConnections()
    if (saved) { setSavedConnections(saved.items); setConnectionNotice(saved.notice) }
  }, [])
  useEffect(() => {
    void reloadSavedConnections().catch((error) => setConnectionError(errorMessage(error)))
  }, [reloadSavedConnections])

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
      setActiveSession(null)
      setClient(candidate)
      setConnection(savedOptions)
      setConnectionStatus('connected')
      if (desktop && options.mode === 'local') setConfigReconnectRequired(false)
      reconnectCount.current = 0
      autoReconnect.current = true
      if (!desktop && options.mode === 'remote') {
        saveWebConnection(options.url, options.token, rememberTokenRef.current)
        setAutoRestoring(false)
      }
      if (desktop && options.mode === 'remote') setToken('')
      if (desktop) void reloadSavedConnections().catch(() => undefined)
    } catch (error) {
      if (candidate) await candidate.close().catch(() => undefined)
      if (!desktop && autoRestoringRef.current) {
        clearWebConnection()
        setAutoRestoring(false)
      }
      setConnectionError(errorMessage(error))
      setConnectionStatus(retry ? 'reconnecting' : 'offline')
      if (retry && autoReconnect.current) scheduleReconnect()
      else if (!retry) autoReconnect.current = false
    }
  }, [desktop, reloadSavedConnections, scheduleReconnect])

  useEffect(() => { startConnection.current = activateConnection }, [activateConnection])
  useEffect(() => {
    if (desktop || managed || !initialWebSaved || attemptedAutoRestore.current) return
    attemptedAutoRestore.current = true
    void activateConnection({ mode: 'remote', url: initialWebSaved.url, token: initialWebSaved.token })
  }, [activateConnection, desktop, initialWebSaved, managed])
  const workspaceId = managed?.workspace.id ?? ''
  const localURL = managed?.options.url
  const localToken = managed?.options.token
  const localMode = managed?.options.mode
  const workspaceRevision = managed?.workspaceRevision ?? 0
  const report = managed?.report
  useEffect(() => {
    if (localURL && localToken && localMode) void activateConnection({ mode: localMode, url: localURL, token: localToken })
  }, [activateConnection, localMode, localURL, localToken, workspaceRevision])
  useEffect(() => {
    if (managed?.focusFilesToken && managed?.active) {
      setHarnessTab('files')
      setHarnessOpen(true)
    }
  }, [managed?.focusFilesToken, managed?.active])
  const workspaceActivity: WorkspaceActivity = pending.length ? 'Needs attention'
    : taskIsRunning ? 'Running' : taskIsPaused ? 'Paused' : taskIsQueued || runtime.queue.length ? 'Queued' : ''
  useEffect(() => { if (report) report(workspaceId, workspaceActivity) }, [report, workspaceId, workspaceActivity])
  useEffect(() => {
    if (managed?.active === false && drawerRef.current?.open) drawerRef.current.close()
  }, [managed?.active])
  const canBrowse = has('workspace.files')
  useEffect(() => {
    setWorkspaceInfo(null)
    if (!client || !canBrowse) return
    let active = true
    void client.request<WorkspaceInfo>('workspace.info').then(
      workspace => { if (active) setWorkspaceInfo(workspace) },
      () => undefined,
    )
    return () => { active = false }
  }, [canBrowse, client])
  useEffect(() => { activeSessionIdRef.current = activeSessionId }, [activeSessionId])
  useEffect(() => {
    if (!connection || !activeSession || activeSession.id !== activeSessionId) return
    if (connection.mode === 'loopback') {
      rememberLoopbackSession(workspaceId, activeSessionId)
      return
    }
    if (!desktop && connection.mode === 'remote') {
      try { sessionStorage.setItem(webLastSessionKey(connection.url), activeSessionId) } catch { /* ignore */ }
      return
    }
    const address = connection.mode === 'local' ? connection.workspace : connection.url
    void window.luneDesktop?.rememberSession(connection.mode, address, activeSessionId).catch(() => undefined)
  }, [connection, activeSession, activeSessionId, desktop, workspaceId])

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
    if (!desktop) clearWebConnection()
    setAutoRestoring(false)
    setConnectionStatus('offline')
    setSessions([])
    setModels([])
    setActiveSession(null)
    setActiveSessionId('')
    setTimeline([])
    setHistory([])
    setAttachments([])
    submissionRetry.current = null
    setPending([])
    setModelFailure(null)
    setRuntime(emptyRuntime)
    setCommands([])
    setStreamingReasoning('')
    setStreamingText('')
    setLiveTools([])
    setLatestExecutionId('')
    setSlashNotice('')
    setShowConnection(true)
    if (active) await active.close(true).catch(() => undefined)
    else if (window.luneDesktop) await window.luneDesktop.close(true).catch(() => undefined)
  }, [])

  useEffect(() => () => {
    autoReconnect.current = false
    if (reconnectTimer.current !== null) window.clearTimeout(reconnectTimer.current)
    reconnectTimer.current = null
    connectionRef.current = null
    void clientRef.current?.close(true).catch(() => undefined)
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
      eventHandler.current(object(params.event) as unknown as RuntimeEvent)
    })
  }, [client, scheduleReconnect])

  const refreshSessionData = useCallback(async (targetClient: RuntimeClient, sessionId: string) => {
    const sequence = ++sessionRefreshSequence.current
    const canCommands = capabilitiesRef.current.includes('command.control')
    const [summary, rawHistory, nextRuntime, nextPending, nextCommands] = await Promise.all([
      targetClient.request<SessionSummary>('session.get', { sessionId }),
      loadHistory(targetClient, sessionId, capabilitiesRef.current.includes('session.historyPage')),
      targetClient.request<RuntimeState>('runtime.state', { sessionId }),
      targetClient.request<Pending[]>('runtime.pending', { sessionId }),
      canCommands ? targetClient.request<CommandInfo[]>('command.list', { sessionId }).catch(() => []) : Promise.resolve([]),
    ])
    if (clientRef.current !== targetClient || activeSessionIdRef.current !== sessionId || sequence !== sessionRefreshSequence.current) return
    const entries = buildTimeline(rawHistory)
    const messages = timelineMessages(entries)
    setTimeline(entries)
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
    const normalized = normalizeRuntimeState(nextRuntime)
    setRuntime(normalized)
    setCommands(nextCommands ?? [])
    if (normalized.task.executionId) setLatestExecutionId(normalized.task.executionId)
    setPending(nextPending ?? [])
    const recoveredFailure = (nextPending ?? []).find((item) => item.kind === 'model_recovery')?.modelRecovery?.failure
    if (recoveredFailure) setModelFailure(recoveredFailure)
    else if (['completed', 'failed', 'cancelled', 'discarded', 'idle'].includes(normalized.task.status)) setModelFailure(null)
    setSessions((items) => items.map((item) => item.id === summary.id ? summary : item))
  }, [])

  const refreshRuntime = useCallback(async (targetClient: RuntimeClient, sessionId: string) => {
    const canCommands = capabilitiesRef.current.includes('command.control')
    const [nextRuntime, nextPending, nextCommands] = await Promise.all([
      targetClient.request<RuntimeState>('runtime.state', { sessionId }),
      targetClient.request<Pending[]>('runtime.pending', { sessionId }),
      canCommands ? targetClient.request<CommandInfo[]>('command.list', { sessionId }).catch(() => []) : Promise.resolve([]),
    ])
    if (clientRef.current !== targetClient || activeSessionIdRef.current !== sessionId) return
    setRuntime(normalizeRuntimeState(nextRuntime))
    setCommands(nextCommands ?? [])
    setPending(nextPending ?? [])
    const recoveredFailure = (nextPending ?? []).find((item) => item.kind === 'model_recovery')?.modelRecovery?.failure
    if (recoveredFailure) setModelFailure(recoveredFailure)
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
      const previousID = window.luneDesktop
        ? await window.luneDesktop.lastSession().catch(() => null)
        : connectionRef.current?.mode === 'loopback'
          ? lastLoopbackSession(workspaceId)
          : connectionRef.current?.mode === 'remote'
            ? (() => { try { return sessionStorage.getItem(webLastSessionKey(connectionRef.current.url)) } catch { return null } })()
            : activeSessionIdRef.current
      if (cancelled) return
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
  }, [client, workspaceId])

  useEffect(() => {
    if (!client || !activeSessionId) return
    let cancelled = false
    let subscriptionId = ''
    const targetClient = client
    const sessionId = activeSessionId
    setActiveSession(null)
    setTimeline([])
    setHistory([])
    streamingExecution.current = undefined
    setPending([])
    setModelFailure(null)
    setRuntime(emptyRuntime)
    setCommands([])
    setStreamingReasoning('')
    setStreamingText('')
    setLiveTools([])
    setLatestExecutionId('')
    setActivity([])
    setSlashNotice('')
    cursorKeyRef.current = `lune:cursor:${connection?.mode ?? 'remote'}:${connection?.mode === 'local' ? connection.workspace : connection?.url ?? 'local'}:${sessionId}`

    const refreshAll = () => refreshSessionData(targetClient, sessionId).catch((error: unknown) => setActionError(errorMessage(error)))
    const refreshLiveState = () => refreshRuntime(targetClient, sessionId).catch((error: unknown) => setActionError(errorMessage(error)))
    eventHandler.current = (event) => {
      if (event.cursor && event.cursor > readCursor(cursorKeyRef.current)) writeCursor(cursorKeyRef.current, event.cursor)
      if (event.kind === 'task_started') {
        streamingExecution.current = event.executionId
        if (event.executionId) setLatestExecutionId(event.executionId)
        setStreamingReasoning('')
        setStreamingText('')
        setLiveTools([])
        setRuntime((state) => ({ ...state, task: { ...state.task, status: 'running', executionId: event.executionId } }))
        setActivity((items) => [{ id: `${event.taskId}-started`, label: 'Working on your request', detail: event.executionId }, ...items].slice(0, 5))
      } else if (event.kind === 'task_finished' || event.kind === 'task_discarded') {
        if (!event.taskId && event.status === 'cancelled' && event.executionId) {
          setOptimistic((items) => items.filter((item) => item.executionId !== event.executionId))
        }
        if (event.executionId) setLatestExecutionId(event.executionId)
        if (event.executionId === streamingExecution.current) {
          streamingExecution.current = undefined
          setStreamingReasoning('')
          setStreamingText('')
          setLiveTools([])
        }
        setActivity((items) => [{
          id: `${event.executionId}-${event.kind}`,
          label: event.kind === 'task_discarded' ? 'Run discarded' : `Run ${event.status ?? 'finished'}`,
          detail: event.error,
          failed: ['failed', 'cancelled'].includes(event.status ?? ''),
        }, ...items].slice(0, 5))
        void refreshAll()
      } else if (event.kind === 'model_retry' || event.kind === 'model_recovery_required') {
        if (event.modelFailure) setModelFailure(event.modelFailure)
        if (event.kind === 'model_recovery_required') void refreshLiveState()
      } else if (event.kind === 'model_recovery_resumed') {
        setModelFailure(null)
      } else if (event.kind === 'context_compaction') {
        void refreshAll()
      } else if (event.kind === 'task_queued' || event.kind === 'goal_updated' || event.kind === 'goal_cleared' || event.kind === 'command_started' || event.kind === 'command_finished') {
        void refreshLiveState()
      } else if (event.kind === 'model_stream' && event.stream?.type === 'text_delta' && (event.stream.purpose === 'main' || !event.stream.purpose)) {
        setStreamingText((text) => text + (event.stream?.delta ?? ''))
      } else if (event.kind === 'model_stream' && event.stream?.type === 'reasoning_delta' && (event.stream.purpose === 'main' || !event.stream.purpose)) {
        setStreamingReasoning((text) => text + (event.stream?.delta ?? ''))
      } else if (event.kind === 'tool_started' || event.kind === 'tool_finished') {
        const name = event.tool?.name ?? 'tool'
        const stepId = event.tool?.invocationId || event.tool?.id || `${event.taskId}-${name}`
        if (event.kind === 'tool_started') {
          setLiveTools((steps) => [...steps.filter((s) => s.id !== stepId), { id: stepId, name, status: 'running' as const }].slice(-8))
        } else {
          const durationMs = event.tool?.duration ? Math.round(event.tool.duration / 1_000_000) : undefined
          setLiveTools((steps) => steps.map((s) => s.id === stepId ? { ...s, status: event.tool?.failed ? 'failed' : 'completed', durationMs } : s))
        }
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
  }, [timeline.length, optimistic.length, streamingText, streamingReasoning, liveTools.length, pending.length])

  const connectFromForm = async () => {
    setBusy(true)
    setConnectionError('')
    const normalizedURL = mode === 'remote' ? normalizeRemoteURL(remoteURL) : remoteURL
    if (mode === 'remote' && normalizedURL !== remoteURL) setRemoteURL(normalizedURL)
    const options: RuntimeConnection = mode === 'local'
      ? { mode: 'local', workspace }
      : { mode: 'remote', url: normalizedURL, token, ...(desktop ? { name: remoteName, rememberToken } : {}) }
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
      setTimeline([])
      setOptimistic([])
      setActivity([])
      setLiveTools([])
      setStreamingReasoning('')
      setStreamingText('')
      setLatestExecutionId('')
      setSlashNotice('')
      setRowMenuId('')
      setCommands([])
      closeDrawer()
    } catch (error) { setActionError(errorMessage(error)) }
    finally { setWorking(false) }
  }

  const sendMessage = async (steer = false) => {
    const text = draft.trim()
    if (!client || activeSession?.id !== activeSessionId || !activeSessionId || (!text && !attachments.length) || working || connectionStatus !== 'connected') return
    if (!steer && !attachments.length && parseSlashCommand(text)) {
      await executeSlashCommand(text)
      return
    }
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

  const changeArchived = (targetSession?: SessionSummary) => void sessionAction(async () => {
    const target = targetSession ?? activeSession
    if (!client || !target) return
    await client.request('session.setArchived', { sessionId: target.id, archived: !target.archived })
    if (target.id === activeSessionId) await refreshSessionData(client, target.id)
    setListRevision((revision) => revision + 1)
  })

  const forkSession = (targetId = activeSessionId) => void sessionAction(async () => {
    if (!client || !targetId) return
    const forked = await client.request<SessionSummary>('session.fork', { sessionId: targetId })
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

  const cancelAllPending = () => void sessionAction(async () => {
    if (!client || !activeSessionId) return
    try {
      const result = await client.request<{ cancelledExecutionIds: string[] }>('turn.cancelPending', { sessionId: activeSessionId })
      const cancelled = new Set(result.cancelledExecutionIds ?? [])
      setOptimistic((items) => items.filter((item) => !item.executionId || !cancelled.has(item.executionId)))
    } finally {
      await refreshRuntime(client, activeSessionId)
    }
  })

  const implementPlan = (sourceExecutionId: string, planModeChoice: 'same_context' | 'new_context') => void sessionAction(async () => {
    if (!client || !activeSessionId) return
    const result = await client.request<{ sessionId: string }>('turn.implementPlan', {
      sessionId: activeSessionId,
      sourceExecutionId,
      mode: planModeChoice,
    })
    setPlanMode(false)
    if (result.sessionId && result.sessionId !== activeSessionId) {
      setActiveSessionId(result.sessionId)
      setListRevision((revision) => revision + 1)
    } else {
      await refreshRuntime(client, activeSessionId)
    }
  })

  const saveConversation = (targetSession?: SessionSummary) => void sessionAction(async () => {
    const target = targetSession ?? activeSession
    if (!client || !target) return
    let targetMessages = history
    if (target.id !== activeSessionId) {
      const raw = await loadHistory(client, target.id, has('session.historyPage'))
      targetMessages = timelineMessages(buildTimeline(raw))
    }
    const title = describeSession(target, targetMessages)
    const markdown = [`# ${title}`, ...targetMessages.map((message) =>
      [`## ${message.role === 'user' ? 'You' : 'Lune'}`, message.text,
        ...(message.attachments ?? []).map((file) => file.image ? `![Attachment](${file.image})` : `### ${file.name}\n\n${file.text ?? ''}`)].join('\n\n'),
    )].join('\n\n')
    if (window.luneDesktop) await window.luneDesktop.saveMarkdown(markdown)
    else downloadMarkdown(markdown)
  })

  const confirmSessionDialog = () => void sessionAction(async () => {
    if (!client || !sessionDialog) return
    const { kind, sessionId, value, digest } = sessionDialog
    const targetId = sessionId || activeSessionId
    if (kind === 'rename') {
      await client.request('session.setTitle', { sessionId: targetId, title: value })
    } else if (kind === 'delete') {
      await client.request('session.delete', { sessionId: targetId })
      if (targetId === activeSessionId) {
        const next = (await client.request<SessionSummary[]>('session.list', { limit: 1 }))[0]
        const selected = next ?? await client.request<SessionSummary>('session.create')
        setActiveSessionId(selected.id)
      }
    } else if (!digest) {
      const turns = Number(value)
      if (!Number.isSafeInteger(turns) || turns < 1) throw new Error('Enter a positive number of turns.')
      const preview = await client.request<{ removed: number; digest: string; excerpts: string[] | null }>('session.rewindPreview', { sessionId: targetId, turns })
      setSessionDialog({ ...sessionDialog, ...preview, excerpts: preview.excerpts ?? [] })
      return
    } else {
      try {
        await client.request('session.rewind', { sessionId: targetId, turns: Number(value), digest })
      } catch (error) {
        setSessionDialog({ kind: 'rewind', sessionId: targetId, value })
        throw error
      }
      setInfo(`Removed ${sessionDialog.removed} turn(s) from this conversation.`)
    }
    setSessionDialog(null)
    setListRevision((revision) => revision + 1)
    if (kind !== 'delete' && targetId === activeSessionId) await refreshSessionData(client, targetId)
  })

  const openHarnessTab = (tab: HarnessTab) => {
    setHarnessTab(tab)
    setHarnessOpen(true)
  }

  const insertPathReference = (path: string) => {
    const tokenText = `@${path} `
    setDraft((prev) => (!prev || /\s$/.test(prev) ? `${prev}${tokenText}` : `${prev} ${tokenText}`))
    textareaRef.current?.focus()
  }

  const executeSlashCommand = async (rawInput: string) => {
    const parsed = parseSlashCommand(rawInput)
    if (!parsed || !client) return
    const { command, args } = parsed
    if (command.capability && !has(command.capability)) {
      setActionError(`Current Core does not support ${command.name} (${command.capability}).`)
      return
    }
    setDraft('')
    setSlashNotice('')
    setActionError('')

    switch (command.name) {
      case '/new':
        await createSession()
        return
      case '/plan':
        setPlanMode((value) => !value)
        setInfo(!planMode ? 'Plan mode enabled (read-only).' : 'Plan mode disabled.')
        return
      case '/files':
        if (args) setRequestedFile(args)
        openHarnessTab('files')
        return
      case '/goal': {
        if (!activeSessionId) return
        if (!args) { openHarnessTab('goal'); return }
        const [sub, ...rest] = args.split(/\s+/)
        const restText = rest.join(' ').trim()
        await sessionAction(async () => {
          if (sub === 'pause') await client.request('goal.pause', { sessionId: activeSessionId })
          else if (sub === 'resume') await client.request('goal.resume', { sessionId: activeSessionId })
          else if (sub === 'clear') await client.request('goal.clear', { sessionId: activeSessionId })
          else if (sub === 'budget') {
            const n = restText ? Number(restText) : null
            if (n !== null && (!Number.isSafeInteger(n) || n <= 0)) throw new Error('Token budget must be a positive integer.')
            await client.request('goal.setBudget', { sessionId: activeSessionId, tokenBudget: n })
          } else if (sub === 'edit') {
            if (!restText) throw new Error('Provide the updated goal objective.')
            await client.request('goal.edit', { sessionId: activeSessionId, objective: restText })
          } else {
            await client.request('goal.create', { sessionId: activeSessionId, objective: args })
          }
          await refreshRuntime(client, activeSessionId)
          openHarnessTab('goal')
        })
        return
      }
      case '/ps':
        openHarnessTab('processes')
        return
      case '/logs':
        if (args) setRequestedCommandId(args)
        openHarnessTab('processes')
        return
      case '/stop-cmd':
        if (!args) { openHarnessTab('processes'); return }
        await sessionAction(async () => {
          await client.request('command.stop', { sessionId: activeSessionId, commandId: args })
          setRequestedCommandId(args)
          openHarnessTab('processes')
        })
        return
      case '/mcp': {
        if (!args || args === 'list') { openHarnessTab('mcp'); return }
        const [action, target] = args.split(/\s+/, 2)
        if ((action === 'enable' || action === 'disable') && target) {
          await sessionAction(async () => {
            await client.request(action === 'enable' ? 'mcp.enable' : 'mcp.disable', { serverName: target })
            openHarnessTab('mcp')
          })
          return
        }
        openHarnessTab('mcp')
        return
      }
      case '/model':
        if (!args) {
          setSlashNotice(`**Models**\n\n${models.map((m) => `- \`${m.profile}\` — ${m.displayName || m.model}`).join('\n')}`)
          return
        }
        await changeModel(args)
        setInfo(`Model set to ${args}.`)
        return
      case '/effort': {
        const levels = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max']
        if (!args) {
          setSlashNotice(`Current reasoning effort: \`${activeSession?.settings.reasoningEffort || 'default'}\`\n\nAvailable: ${levels.map((l) => `\`${l}\``).join(', ')}`)
          return
        }
        const normalized = args.toLowerCase()
        if (!levels.includes(normalized) && normalized !== 'default') {
          setActionError(`Invalid reasoning effort: ${args}`)
          return
        }
        await changeSettings({ reasoningEffort: normalized === 'default' ? '' : normalized })
        setInfo(`Reasoning effort set to ${normalized}.`)
        return
      }
      case '/compact':
        compactSession()
        return
      case '/rewind':
        setSessionDialog({ kind: 'rewind', sessionId: activeSessionId, value: args || '1' })
        return
      case '/fork':
        forkSession()
        return
      case '/title':
        if (!args) {
          setSessionDialog({ kind: 'rename', sessionId: activeSessionId, value: activeSession?.title || '' })
          return
        }
        await sessionAction(async () => {
          await client.request('session.setTitle', { sessionId: activeSessionId, title: args })
          setListRevision((r) => r + 1)
          await refreshSessionData(client, activeSessionId)
        })
        return
      case '/archive':
        changeArchived()
        return
      case '/drop':
        setSessionDialog({ kind: 'delete', sessionId: activeSessionId, value: '' })
        return
      case '/export':
        saveConversation()
        return
      case '/stop':
        await stopRun()
        return
      case '/cancel-queue':
        cancelAllPending()
        return
      case '/status': {
        const usage = activeSession?.sessionUsage
        setSlashNotice([
          `**Session** \`${activeSessionId}\``,
          `- **Model**: \`${selectedModel || '-'}\` (${activeSession?.settings.reasoningEffort || 'default'})`,
          `- **Task**: \`${runtime.task.status}\` · **Queued**: ${runtime.queue.length}`,
          usage ? `- **Tokens**: ${usage.input.toLocaleString()} in / ${usage.output.toLocaleString()} out (${usage.cached.toLocaleString()} cached)` : '',
        ].filter(Boolean).join('\n'))
        return
      }
      case '/context':
        openHarnessTab('context')
        return
      case '/theme': {
        const next = args.toLowerCase()
        if (next === 'system' || next === 'light' || next === 'dark') {
          if (window.luneDesktop) await window.luneDesktop.setTheme(next).catch(() => undefined)
          else try { localStorage.setItem('lune:appearance', next) } catch { /* ignore */ }
          applyTheme(next)
          setTheme(next)
          setInfo(`Theme set to ${next}.`)
        } else {
          setSlashNotice('Usage: `/theme system|light|dark`')
        }
        return
      }
      case '/lang': {
        const next = args.toLowerCase()
        if (next === 'zh' || next === 'en') {
          setLanguage(next)
        } else {
          setSlashNotice('Usage: `/lang zh|en`')
        }
        return
      }
      case '/help': {
        const available = slashCommands.filter((c) => !c.capability || has(c.capability))
        setSlashNotice(`**${t('Slash commands')}**\n\n` + available.map((c) => `- \`${c.usage}\` — ${t(c.description)}`).join('\n'))
        return
      }
    }
  }

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

  const useSavedConnection = async (saved: SavedConnection) => {
    setMode(saved.mode)
    setConnectionError('')
    if (saved.mode === 'remote') {
      setRemoteURL(saved.address)
      setRemoteName(saved.name === saved.address ? '' : saved.name)
      setRememberToken(saved.remembered)
      setToken('')
      if (!saved.remembered) return
    } else setWorkspace(saved.address)
    setBusy(true)
    try {
      await activateConnection(saved.mode === 'local'
        ? { mode: 'local', workspace: saved.address }
        : { mode: 'remote', url: saved.address, token: '', name: saved.name })
    } finally { setBusy(false) }
  }

  const chooseWorkspace = async () => {
    const selected = await window.luneDesktop?.selectWorkspace()
    if (selected) setWorkspace(selected)
  }

  const statusLabel = connectionStatus === 'connected' ? 'Connected' : connectionStatus === 'reconnecting' ? 'Reconnecting' : connectionStatus === 'connecting' ? 'Connecting' : 'Not connected'
  const statusIcon = connectionStatus === 'connected' ? <Wifi size={15} /> : connectionStatus === 'reconnecting' || connectionStatus === 'connecting' ? <LoaderCircle className="spin" size={15} /> : <WifiOff size={15} />
  const canReuseDesktopRemote = desktop && (
    (connection?.mode === 'remote' && remoteURL.trim() === connection.url) ||
    savedConnections.some((saved) => saved.mode === 'remote' && saved.address === remoteURL.trim() && saved.remembered)
  )
  const workspacePath = managed?.workspace.path || workspaceInfo?.path
  const workspaceName = workspaceInfo?.name || workspacePath?.split(/[\\/]/).filter(Boolean).at(-1)
  const settingsAPI = useMemo(
    () => managed?.settingsAPI ?? (desktop ? createDesktopSettingsAPI() : createRemoteOnlySettingsAPI()),
    [desktop, managed],
  )
  useEffect(() => {
    const move = (event: PointerEvent) => {
      const drag = columnDrag.current
      if (!drag) return
      if (drag.side === 'sidebar') setSidebarWidth(Math.min(420, Math.max(200, drag.width + event.clientX - drag.x)))
      else setHarnessWidth(Math.min(760, Math.max(320, drag.width + drag.x - event.clientX)))
    }
    const finish = () => {
      columnDrag.current = null
      setDraggingColumn(null)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', finish)
    window.addEventListener('pointercancel', finish)
    return () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', finish)
      window.removeEventListener('pointercancel', finish)
    }
  }, [])
  useEffect(() => {
    try {
      localStorage.setItem('lune:sidebar-width', String(sidebarWidth))
      localStorage.setItem('lune:harness-width', String(harnessWidth))
    } catch { /* layout still works for this window */ }
  }, [sidebarWidth, harnessWidth])
  useEffect(() => {
    const update = () => setHarnessOverlay(window.innerWidth < (sidebarCollapsed ? 56 : sidebarWidth) + harnessWidth + 420)
    update()
    window.addEventListener('resize', update)
    return () => window.removeEventListener('resize', update)
  }, [sidebarCollapsed, sidebarWidth, harnessWidth])
  useEffect(() => {
    let active = true
    void settingsAPI.getUiPreferences().then((preferences) => {
      if (!active) return
      setUiPreferences(preferences)
      setTheme(preferences.theme)
      setLanguage(preferences.language)
      applyTheme(preferences.theme)
    }).catch(() => undefined)
    return () => { active = false }
  }, [settingsAPI, setLanguage])
  useEffect(() => {
    const root = document.documentElement
    root.style.setProperty('--message-font-size', `${uiPreferences.bodyFontSize}px`)
    root.style.setProperty('--code-font-size', `${uiPreferences.codeFontSize}px`)
    root.dataset.usageDensity = uiPreferences.usageDensity
  }, [uiPreferences])
  useEffect(() => {
    const retryAt = modelFailure?.retry_not_before ? Date.parse(modelFailure.retry_not_before) : NaN
    if (!Number.isFinite(retryAt) || retryAt <= Date.now()) return
    const timer = window.setInterval(() => {
      const now = Date.now()
      setRecoveryClock(now)
      if (now >= retryAt) window.clearInterval(timer)
    }, 1000)
    return () => window.clearInterval(timer)
  }, [modelFailure])
  const openWorkspaceAsFileTree = async (openFilesAfter = true) => {
    if (managed?.openDirectoryPicker) {
      managed.openDirectoryPicker(openFilesAfter)
      return
    }
    if (desktop) {
      const selectedDir = await window.luneDesktop?.selectWorkspace()
      if (!selectedDir) return
      setMode('local')
      setWorkspace(selectedDir)
      setBusy(true)
      try {
        await activateConnection({ mode: 'local', workspace: selectedDir })
        if (openFilesAfter) {
          setHarnessTab('files')
          setHarnessOpen(true)
        }
      } finally {
        setBusy(false)
      }
      return
    }
    if (openFilesAfter) openHarnessTab('files')
  }
  const settingsDialogNode = (
    <SettingsModal
      open={settingsOpen}
      initialTab={settingsTab}
      api={settingsAPI}
      workspace={workspacePath || workspace}
      theme={theme}
      onThemeChange={setTheme}
      onUiPreferencesChange={setUiPreferences}
      activeRemoteURL={connection?.mode === 'remote' ? connection.url : remoteURL}
      isRemoteActive={Boolean(managed?.isRemoteOverride || connection?.mode === 'remote')}
      hasLocalHost={Boolean(managed)}
      onConnectRemote={async (opts) => {
        if (managed?.onConnectRemote) {
          await managed.onConnectRemote(opts)
          return
        }
        const normalizedURL = normalizeRemoteURL(opts.url)
        setMode('remote')
        setRemoteURL(normalizedURL)
        setRemoteName(opts.name ?? '')
        setToken(opts.token)
        setRememberToken(Boolean(opts.rememberToken))
        rememberTokenRef.current = Boolean(opts.rememberToken)
        await activateConnection({
          mode: 'remote',
          url: normalizedURL,
          token: opts.token,
          ...(desktop ? { name: opts.name, rememberToken: opts.rememberToken } : {}),
        })
      }}
      onDisconnectRemote={async () => { await disconnect() }}
      onReturnToLocal={managed?.onReturnToLocal}
      onSavedConfig={async (savedResult) => {
        if (desktop && connection?.mode === 'local') {
          if (taskIsRunning || taskHasQueuedWork) {
            setConfigReconnectRequired(true)
            return { ...savedResult, requiresReconnect: true }
          }
          await activateConnection({ mode: 'local', workspace: connection.workspace })
          if (clientRef.current) {
            setConfigReconnectRequired(false)
            return { ...savedResult, reloaded: true, requiresReconnect: false }
          }
          setConfigReconnectRequired(true)
          return { ...savedResult, requiresReconnect: true }
        }
        if (settingsAPI.kind === 'web-host' && savedResult.requiresReconnect) {
          setConfigReconnectRequired(true)
          if (savedResult.reconnectError) setActionError(savedResult.reconnectError)
        } else if (settingsAPI.kind === 'web-host' && savedResult.reloaded) {
          setConfigReconnectRequired(false)
        }
        return savedResult
      }}
      onClose={() => setSettingsOpen(false)}
    />
  )
  const applySavedConfig = async () => {
    setApplyingConfig(true)
    setActionError('')
    try {
      if (desktop && connection?.mode === 'local') {
        await activateConnection({ mode: 'local', workspace: connection.workspace })
      } else if (managed?.reconnectWorkspace) {
        await managed.reconnectWorkspace()
        setConfigReconnectRequired(false)
      }
    } catch (error) {
      setActionError(errorMessage(error))
    } finally {
      setApplyingConfig(false)
    }
  }
  const connectDisabled = busy || (mode === 'local' ? !workspace.trim() : !remoteURL.trim() || (!token.trim() && !canReuseDesktopRemote))

  if (managed && (!client || showConnection)) {
    return <div className="web-workbench web-workbench-loading">
      <header className="workbench-top"><span className="workbench-brand"><Sparkles size={19} /> Lune</span><LanguagePicker /></header>
      <aside className="sidebar" aria-label={t('Conversations')}>
        <div className="sidebar-scroll-region">{managed.navigation}</div>
        <div className="sidebar-bottom">
          <div className="core-card">
            <div className="core-card-icon"><Code2 size={15} /></div>
            <div className="core-card-copy">
              <strong>{managed.isRemoteOverride ? t('Remote Core') : t('Local Core')}</strong>
              <span>{workspacePath || workspaceName || 'Workspace'}</span>
            </div>
            <span className={`connection-light ${connectionStatus}`} />
          </div>
          <button
            type="button"
            className="settings-button"
            onClick={() => {
              setSettingsTab(settingsAPI.supportsModelConfig ? 'models' : 'remote')
              setSettingsOpen(true)
            }}
          >
            <Settings size={15} />
            <span>{t('Settings')}</span>
          </button>
        </div>
      </aside>
      <main className="web-launch">
        {connectionError || actionError ? <><p role="alert">{t(connectionError || actionError)}</p><button className="secondary-button"
          onClick={() => void activateConnection(managed.options)}>{t('Retry')}</button></> :
          <p role="status">{t('Connecting to local Core…')}</p>}
      </main>
      {settingsDialogNode}
    </div>
  }
  if (!client || showConnection) {
    if (!desktop && autoRestoring && !connectionError) {
      return <div className="connect-page"><main className="web-launch"><p role="status">{t('Connecting…')}</p></main></div>
    }
    return (
      <div className="connect-page">
        <div className="connect-top"><div className="brand-mark"><Sparkles size={16} /></div><span>Lune</span>{desktop && <span className="build-label">CORE CLIENT</span>}</div>
        <div className="connect-appearance">{!desktop && <LanguagePicker />}<ThemePicker value={theme} onChange={setTheme} /></div>
        <main className="connect-card" onKeyDown={(event) => {
          if (event.key === 'Enter' && !connectDisabled && event.target instanceof HTMLInputElement && event.target.type !== 'checkbox') {
            event.preventDefault()
            void connectFromForm()
          }
        }}>
          {desktop ? <><div className="eyebrow"><span className="eyebrow-dot" /> YOUR WORKSPACE, YOUR CORE</div>
            <h1>A quiet place<br />to think with AI.</h1>
            <p className="connect-intro">Connect to a Lune Core. Your sessions, tools, and permissions stay with the Core you choose.</p></> :
            <h1>{t('Link a remote Core')}</h1>}
          {desktop && <div className="connection-tabs">
            <button className={mode === 'local' ? 'active' : ''} onClick={() => setMode('local')}><Laptop size={16} /> This computer</button>
            <button className={mode === 'remote' ? 'active' : ''} onClick={() => setMode('remote')}><Radio size={16} /> Remote Core</button>
          </div>}
          {desktop && savedConnections.length > 0 && <div className="recent-connections" aria-label="Saved connections">
            {savedConnections.filter((saved) => saved.mode === mode).map((saved) =>
              <div className="recent-connection" key={saved.address}>
                <button type="button" className="secondary-button" title={saved.address} disabled={busy}
                  onClick={() => void useSavedConnection(saved)}>{saved.mode === 'local' ? saved.address.split(/[\\/]/).filter(Boolean).at(-1) ?? saved.address : saved.name}</button>
                <button type="button" className="icon-button" aria-label={`Remove saved connection ${saved.name}`} disabled={busy}
                  onClick={() => void window.luneDesktop?.forgetConnection(saved.mode, saved.address).then(reloadSavedConnections).catch((error) => setConnectionError(errorMessage(error)))}><X size={13} /></button>
              </div>)}
          </div>}
          {mode === 'local' && desktop ? <div className="form-field">
            <label>Workspace folder</label>
            <div className="input-with-action"><input value={workspace} onChange={(event) => setWorkspace(event.target.value)} placeholder="Choose a workspace folder" /><button onClick={() => void chooseWorkspace()} aria-label="Browse folders"><FolderOpen size={17} /></button></div>
            <small>Core uses your desktop settings. Sessions are stored in this workspace’s <code>.lune</code> folder.</small>
          </div> : <>
            <div className="form-field">
              <label>{t("Core WebSocket URL")}</label>
              <input value={remoteURL} onChange={(event) => setRemoteURL(event.target.value)} placeholder={`wss://${typeof location !== 'undefined' && location.host ? location.host : 'core.example.com:18092'}/v1/runtime`} autoCapitalize="none" spellCheck={false} />
            </div>
            <div className="form-field">
              <label>{t("Access token")}</label>
              <input type="password" value={token} onChange={(event) => setToken(event.target.value)} placeholder={t("Paste your Core token")} autoComplete="off" />
              {!desktop && <label className="remember-token"><input type="checkbox" checked={rememberToken} onChange={(event) => setRememberToken(event.target.checked)} /> {t("Remember token in this browser")}</label>}
              {!desktop && <small><LockKeyhole size={13} />{t("Automatically reconnects on refresh. Uncheck to keep for this tab only.")}</small>}
              {desktop && <label className="remember-token"><input type="checkbox" checked={rememberToken} onChange={(event) => setRememberToken(event.target.checked)} /> Remember token with system credentials</label>}
            </div>
            {desktop && <label className="form-field">Connection name<input value={remoteName} onChange={(event) => setRemoteName(event.target.value)} maxLength={120} /></label>}
          </>}
          {desktop && mode === 'local' && <button type="button" className="secondary-button" disabled={busy} onClick={() => { setSettingsTab('models'); setSettingsOpen(true) }}>{t('Settings')}</button>}
          {connectionNotice && <div className="form-status" role="status">{connectionNotice}</div>}
          {(connectionError || pairingError) && <div className="form-error" role="alert"><ShieldAlert size={16} /> <span>{t(connectionError || pairingError || '')}</span></div>}
          {!connectionError && connectionStatus === 'reconnecting' && <div className="form-status"><LoaderCircle size={15} className="spin" /> The connection dropped. Lune is trying again automatically.</div>}
          <button className="primary-button connect-button" disabled={connectDisabled} onClick={() => void connectFromForm()}>
            {busy ? <LoaderCircle size={17} className="spin" /> : <ArrowUp size={17} />}{t(busy ? 'Connecting…' : 'Connect to Core')}
          </button>
          {desktop && connection && <button className="forget-connection" onClick={() => void disconnect()}>{t("Disconnect")}</button>}
          <div className="connect-footnote"><span><LockKeyhole size={13} /> End-to-end transport security</span><span>Runtime Protocol v1</span></div>
        </main>
        {settingsDialogNode}
        {desktop && <div className="connect-bottom"><span>CORE RUNS WHERE YOU CHOOSE</span><span>WEB · DESKTOP · MULTI-DEVICE</span></div>}
      </div>
    )
  }

  const showSearchBlock = searchOpen || Boolean(search.trim()) || showArchived || compactLayout

  const sessionNavigation = <>
    <button className="new-chat-button" disabled={working} onClick={() => void createSession()}><SquarePen size={16} aria-hidden="true" /><span>{t("New conversation")}</span></button>
    {showSearchBlock && <div className="sidebar-search-block">
      <div className="session-search">
        <Search size={14} aria-hidden="true" />
        <input autoFocus={searchOpen && !compactLayout} aria-label={t("Search conversations")} value={search} disabled={working} onChange={(event) => setSearch(event.target.value)} placeholder={t(serverSearch ? 'Search titles and messages' : 'Find a conversation')} />
        {search && <button type="button" className="subtle-icon" aria-label={t('Dismiss')} onClick={() => setSearch('')}><X size={13} /></button>}
      </div>
      <div className="sidebar-filter-row">
        <label className="archived-toggle"><input type="checkbox" checked={showArchived} disabled={working} onChange={(event) => setShowArchived(event.target.checked)} />{t("Include archived")}</label>
        <button className="subtle-icon" title={t("Refresh sessions")} aria-label={t("Refresh sessions")} disabled={working || listLoading} onClick={() => setListRevision((revision) => revision + 1)}><RefreshCw size={13} /></button>
      </div>
    </div>}
    {managed?.navigation ?? (desktop && (
      <section className="workspace-list" aria-label={t('Workspaces')}>
        <div className="workspace-list-heading">
          <span>{t('Workspaces')}</span>
          <button
            type="button"
            className="icon-button"
            aria-label={t('Open workspace')}
            title={t('Open workspace')}
            onClick={() => void openWorkspaceAsFileTree(false)}
          >
            <Plus size={16} />
          </button>
        </div>
        {savedConnections.filter((item) => item.mode === 'local').map((item) => {
          const selectedWs = connection?.mode === 'local' && connection.workspace === item.address
          const label = item.address.split(/[\\/]/).filter(Boolean).at(-1) || item.address
          return (
            <div className={`workspace-row${selectedWs ? ' selected' : ''}`} key={item.address}>
              <button
                type="button"
                className="workspace-row-btn"
                aria-pressed={selectedWs}
                title={item.address}
                disabled={busy}
                onClick={() => {
                  setMode('local')
                  setWorkspace(item.address)
                  void activateConnection({ mode: 'local', workspace: item.address })
                }}
              >
                <Folder size={15} />
                <span>{label}</span>
              </button>
            </div>
          )
        })}
      </section>
    ))}
    <div className="session-list" aria-busy={listLoading}>
      {sessionGroups.map((group) => (
        <div className="session-group" key={group.label}>
          <div className="session-group-label">{t(group.label)}</div>
          {group.items.map((session) => {
            const preview = session.title || `${t('Conversation')} ${session.id.slice(0, 8)}`
            const menuOpen = rowMenuId === session.id
            return (
              <div key={session.id} className={`session-row${session.id === activeSessionId ? ' selected' : ''}${menuOpen ? ' menu-open' : ''}`}>
                <button className="session-item" disabled={working} aria-current={session.id === activeSessionId ? 'true' : undefined} onClick={() => chooseSession(session.id)}>
                  <span>{preview}{session.archived ? t(' (archived)') : ''}</span>
                </button>
                <div className="session-row-actions">
                  <button
                    type="button"
                    className="row-action-btn"
                    aria-label={t('Rename')}
                    title={t('Rename')}
                    disabled={working}
                    onClick={(event) => {
                      event.stopPropagation()
                      setRowMenuId('')
                      setSessionDialog({ kind: 'rename', sessionId: session.id, value: session.title || '' })
                    }}
                  >
                    <Pencil size={13} aria-hidden="true" />
                  </button>
                  <button
                    type="button"
                    className="row-action-btn danger"
                    aria-label={t('Delete…')}
                    title={t('Delete…')}
                    disabled={working || (session.id === activeSessionId && sessionBusy)}
                    onClick={(event) => {
                      event.stopPropagation()
                      setRowMenuId('')
                      setSessionDialog({ kind: 'delete', sessionId: session.id, value: '' })
                    }}
                  >
                    <Trash2 size={13} aria-hidden="true" />
                  </button>
                  <div className="session-row-menu">
                    <button
                      type="button"
                      className="row-action-btn"
                      aria-label={t('Conversation actions')}
                      title={t('Conversation actions')}
                      aria-expanded={menuOpen}
                      disabled={working}
                      onClick={(event) => {
                        event.stopPropagation()
                        setRowMenuId((current) => (current === session.id ? '' : session.id))
                      }}
                    >
                      <MoreHorizontal size={13} aria-hidden="true" />
                    </button>
                    {menuOpen && (
                      <div className="session-row-popover" role="menu">
                        <button type="button" disabled={working} onClick={() => { setRowMenuId(''); setSessionDialog({ kind: 'rename', sessionId: session.id, value: session.title || '' }) }}>
                          <Pencil size={13} aria-hidden="true" /><span>{t('Rename')}</span>
                        </button>
                        <button type="button" disabled={working} onClick={() => { setRowMenuId(''); changeArchived(session) }}>
                          <Archive size={13} aria-hidden="true" /><span>{t(session.archived ? 'Unarchive' : 'Archive')}</span>
                        </button>
                        {has('session.fork') && (
                          <button type="button" disabled={working || (session.id === activeSessionId && taskIsRunning)} onClick={() => { setRowMenuId(''); forkSession(session.id) }}>
                            <GitFork size={13} aria-hidden="true" /><span>{t('Fork conversation')}</span>
                          </button>
                        )}
                        <button type="button" disabled={working} onClick={() => { setRowMenuId(''); saveConversation(session) }}>
                          <Download size={13} aria-hidden="true" /><span>{t('Export Markdown')}</span>
                        </button>
                        {session.id === activeSessionId && has('session.compact') && (
                          <button type="button" disabled={sessionBusy || working} onClick={() => { setRowMenuId(''); compactSession() }}>
                            <Minimize2 size={13} aria-hidden="true" /><span>{t('Compact context')}</span>
                          </button>
                        )}
                        {session.id === activeSessionId && has('session.rewind') && (
                          <button type="button" disabled={sessionBusy || working || !history.length} onClick={() => { setRowMenuId(''); setSessionDialog({ kind: 'rewind', sessionId: session.id, value: '1' }) }}>
                            <RotateCcw size={13} aria-hidden="true" /><span>{t('Rewind…')}</span>
                          </button>
                        )}
                        <hr className="menu-divider" />
                        <button type="button" className="danger" disabled={working || (session.id === activeSessionId && sessionBusy)} onClick={() => { setRowMenuId(''); setSessionDialog({ kind: 'delete', sessionId: session.id, value: '' }) }}>
                          <Trash2 size={13} aria-hidden="true" /><span>{t('Delete…')}</span>
                        </button>
                      </div>
                    )}
                  </div>
                </div>
              </div>
            )
          })}
        </div>
      ))}
      {moreSessions && <button className="plain-button sidebar-load-more" disabled={working || listLoading} onClick={loadMoreSessions}>{t("Load more")}</button>}
      {!filteredSessions.length && <div className="empty-sessions">{t(listLoading ? 'Loading conversations…' : search.trim() ? 'No matching conversations.' : 'No conversations yet.')}</div>}
    </div>
    {drawerOpen && actionError && <div className="action-error" role="alert">{actionError}</div>}
    <div className="sidebar-bottom">
      <div className="core-card">
        <div className="core-card-icon"><Code2 size={15} /></div>
        <div className="core-card-copy">
          <strong>{managed?.isRemoteOverride || connection?.mode === 'remote' ? t('Remote Core') : t('Local Core')}</strong>
          <span>{workspaceName || (connection?.mode === 'remote' ? connection.url : connection?.mode === 'local' ? workspace.split(/[\\/]/).filter(Boolean).at(-1) || 'This computer' : workspacePath || 'Workspace')}</span>
        </div>
        <span className={`connection-light ${connectionStatus}`} />
      </div>
      <button
        type="button"
        className="settings-button"
        onClick={() => {
          closeDrawer()
          setSettingsTab(settingsAPI.supportsModelConfig ? 'models' : 'remote')
          setSettingsOpen(true)
        }}
      >
        <Settings size={15} />
        <span>{t('Settings')}</span>
      </button>
    </div>
  </>

  function closeHarness() {
    setHarnessOpen(false)
    setHarnessFullScreen(false)
    fileButtonRef.current?.focus()
  }

  const applySlashMatch = (cmd: (typeof slashMatches)[number]) => {
    if (!cmd.argHint) {
      void executeSlashCommand(cmd.name)
      return
    }
    setDraft(`${cmd.name} `)
    textareaRef.current?.focus()
  }

  const isEmptyConversation = displayedMessages.length === 0 && timeline.length === 0 && !streamingText && !streamingReasoning && liveTools.length === 0 && pending.length === 0 && !taskIsPaused && runtime.queue.length === 0
  const runningCommandsCount = commands.filter((cmd) => cmd.status === 'running').length

  const composerNode = (
    <div className="composer-wrap">
      {connectionNotice && <div className="form-status" role="status">{connectionNotice}</div>}
      {info && <div className="form-status" role="status">{info}</div>}
      {slashNotice && (
        <div className="slash-notice" role="status">
          <div className="slash-notice-body"><div className="markdown-body"><MarkdownContent text={slashNotice} /></div></div>
          <button type="button" className="subtle-icon" onClick={() => setSlashNotice('')} aria-label={t('Dismiss')}><X size={14} /></button>
        </div>
      )}
      {!drawerOpen && actionError && <div className="action-error"><ShieldAlert size={15} /><span>{t(actionError)}</span><button onClick={() => setActionError('')} aria-label={t("Dismiss")}><X size={14} /></button></div>}
      {configReconnectRequired && <div className="reconnect-banner settings-reconnect-banner" role="status">
        <span>{t('Settings saved. Reconnect this workspace after the current task finishes to apply the changes.')}</span>
        <button type="button" onClick={() => void applySavedConfig()} disabled={applyingConfig || taskIsRunning || taskHasQueuedWork}>
          {t(applyingConfig ? 'Applying…' : 'Apply settings')}
        </button>
      </div>}
      {connectionStatus !== 'connected' && <div className="reconnect-banner"><LoaderCircle className="spin" size={14} /> {t(connectionStatus === 'reconnecting' ? 'Reconnecting to Core. Your current work continues there.' : 'Core connection lost.')}</div>}
      {slashMatches.length > 0 && activeSession && !working && (
        <div className="slash-popover" role="listbox" aria-label={t('Slash commands')}>
          {slashMatches.map((cmd, index) => (
            <button
              key={cmd.name}
              type="button"
              role="option"
              aria-selected={index === slashIndex}
              className={`slash-item${index === slashIndex ? ' active' : ''}`}
              onMouseEnter={() => setSlashIndex(index)}
              onMouseDown={(event) => {
                event.preventDefault()
                applySlashMatch(cmd)
              }}
            >
              <span className="slash-name">{cmd.name}</span>
              {cmd.argHint && <span className="slash-arg">{cmd.argHint}</span>}
              <span className="slash-desc">{t(cmd.description)}</span>
            </button>
          ))}
        </div>
      )}
      <div className="composer" onDragOver={(event) => { if (has('turn.attachments')) event.preventDefault() }} onDrop={(event) => {
        if (!has('turn.attachments')) return
        event.preventDefault()
        if (!working) addFiles(Array.from(event.dataTransfer.files))
      }}>
        {attachments.length > 0 && <ul className="attachment-list">{attachments.map((file, index) => <li key={`${file.name}-${index}`}>
          {file.mediaType.startsWith('image/') && <img alt="" src={`data:${file.mediaType};base64,${file.data}`} />}
          <span>{file.name}</span><button aria-label={`Remove ${file.name}`} disabled={working} onClick={() => setAttachments((items) => items.filter((_, at) => at !== index))}>×</button>
        </li>)}</ul>}
        <textarea
          ref={textareaRef}
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          onKeyDown={(event) => {
            if (slashMatches.length > 0) {
              if (event.key === 'ArrowDown') {
                event.preventDefault()
                setSlashIndex((i) => (i + 1) % slashMatches.length)
                return
              }
              if (event.key === 'ArrowUp') {
                event.preventDefault()
                setSlashIndex((i) => (i - 1 + slashMatches.length) % slashMatches.length)
                return
              }
              if (event.key === 'Tab') {
                event.preventDefault()
                applySlashMatch(slashMatches[slashIndex] ?? slashMatches[0])
                return
              }
              if (event.key === 'Escape') {
                event.preventDefault()
                setDraft('')
                return
              }
              if (event.key === 'Enter' && !event.shiftKey) {
                event.preventDefault()
                if (parseSlashCommand(draft)) void sendMessage()
                else applySlashMatch(slashMatches[slashIndex] ?? slashMatches[0])
                return
              }
            }
            if (event.key === 'Enter' && !event.shiftKey) {
              event.preventDefault()
              void sendMessage(uiPreferences.runningEnter === 'steer' && (taskIsRunning || taskIsQueued || taskIsPaused))
            }
          }}
          placeholder={t(runtime.queue.length || taskIsRunning ? 'Add a message to the queue…' : 'Message Lune, or type / for commands…')}
          rows={1}
          disabled={!client || !activeSession || working}
        />
        <div className="composer-footer">
          <div className="composer-options">
            {has('turn.attachments') && <>
              <input type="file" ref={fileInput} hidden multiple accept=".txt,.md,.markdown,.csv,.png,.jpg,.jpeg" onChange={(event) => {
                addFiles(Array.from(event.target.files ?? []))
                event.target.value = ''
              }} />
              <button type="button" className="composer-icon-btn" disabled={working} onClick={() => fileInput.current?.click()} aria-label={t("Attach")} title={t("Attach")}><Plus size={18} aria-hidden="true" /></button>
              {desktop && modelOption?.supportsInputImages && <button type="button" className="composer-icon-btn" disabled={working} onClick={captureScreenshot} aria-label="Screenshot" title="Screenshot"><Camera size={16} aria-hidden="true" /></button>}
            </>}
            {has('turn.plan') && <button type="button" className="composer-pill-btn plan-button" aria-pressed={planMode} disabled={working} onClick={() => setPlanMode(!planMode)} title={t("Use read-only tools for this turn")}><Compass size={15} aria-hidden="true" /><span>{t("Plan")}</span></button>}
          </div>
          <div className="composer-actions">
            <div className="composer-picker-group">
              <div className="composer-picker-wrap">
                <button
                  type="button"
                  className="composer-pill-btn composer-picker-btn"
                  aria-haspopup="listbox"
                  aria-expanded={composerMenu === 'model'}
                  title={t(models.length > 0 ? 'MODEL' : 'Configure models')}
                  disabled={models.length > 0 ? (!activeSession || taskIsRunning || taskIsQueued || taskIsPaused) : false}
                  onClick={() => {
                    if (models.length === 0) {
                      setSettingsTab(settingsAPI.supportsModelConfig ? 'models' : 'remote')
                      setSettingsOpen(true)
                      return
                    }
                    setComposerMenu((current) => current === 'model' ? null : 'model')
                  }}
                >
                  <span className="composer-picker-label">
                    {models.length > 0
                      ? (modelOption?.displayName || modelOption?.model || selectedModel || t('Select model'))
                      : t('Configure models')}
                  </span>
                  <ChevronDown size={13} aria-hidden="true" />
                </button>
                {composerMenu === 'model' && models.length > 0 && (
                  <div className="composer-popover" role="listbox" aria-label={t('MODEL')}>
                    <div className="composer-popover-list">
                      {models.map((m) => {
                        const active = m.profile === selectedModel
                        const cap = formatCapacity(m.contextWindow)
                        return (
                          <button
                            key={m.profile}
                            type="button"
                            role="option"
                            aria-selected={active}
                            className={`composer-popover-item${active ? ' active' : ''}`}
                            onClick={() => {
                              setComposerMenu(null)
                              void changeModel(m.profile)
                            }}
                          >
                            <div className="composer-popover-item-main">
                              <span>{m.displayName || m.model}</span>
                              <small>{m.provider}{cap ? ` · ${cap}` : ''}</small>
                            </div>
                            {active && <Check size={14} aria-hidden="true" />}
                          </button>
                        )
                      })}
                    </div>
                    {settingsAPI.supportsModelConfig && (
                      <div className="composer-popover-footer">
                        <button
                          type="button"
                          className="plain-button"
                          onClick={() => {
                            setComposerMenu(null)
                            setSettingsTab('models')
                            setSettingsOpen(true)
                          }}
                        >
                          <Settings size={12} aria-hidden="true" />
                          <span>{t('Manage models…')}</span>
                        </button>
                      </div>
                    )}
                  </div>
                )}
              </div>
              {models.length > 0 && Boolean(selectedModel) && (
                <div className="composer-picker-wrap">
                  <button
                    type="button"
                    className="composer-pill-btn composer-picker-btn"
                    aria-haspopup="listbox"
                    aria-expanded={composerMenu === 'effort'}
                    title={t('Reasoning effort')}
                    disabled={!activeSession || taskIsRunning || taskIsQueued || taskIsPaused}
                    onClick={() => setComposerMenu((current) => current === 'effort' ? null : 'effort')}
                  >
                    <Brain size={14} aria-hidden="true" />
                    <span className="composer-picker-label">
                      {activeSession?.settings.reasoningEffort ? t(activeSession.settings.reasoningEffort) : t('Default')}
                    </span>
                    <ChevronDown size={13} aria-hidden="true" />
                  </button>
                  {composerMenu === 'effort' && (
                    <div className="composer-popover composer-popover-effort" role="listbox" aria-label={t('Reasoning effort')}>
                      <div className="composer-popover-head">{t('Reasoning effort')}</div>
                      <div className="composer-popover-list">
                        {(['', 'none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max'] as const).map((level) => {
                          const current = activeSession?.settings.reasoningEffort || ''
                          const active = current === level
                          return (
                            <button
                              key={level || 'default'}
                              type="button"
                              role="option"
                              aria-selected={active}
                              className={`composer-popover-item${active ? ' active' : ''}`}
                              onClick={() => {
                                setComposerMenu(null)
                                void changeSettings({ reasoningEffort: level })
                              }}
                            >
                              <div className="composer-popover-item-main">
                                <span>{level ? t(level) : t('Default')}</span>
                                {!level && <small>{t('Follow model default')}</small>}
                              </div>
                              {active && <Check size={14} aria-hidden="true" />}
                            </button>
                          )
                        })}
                      </div>
                    </div>
                  )}
                </div>
              )}
            </div>
            {taskIsRunning && runtime.task.executionId && !planMode && <button type="button" className="composer-pill-btn" disabled={working || (!draft.trim() && !attachments.length) || connectionStatus !== 'connected'} onClick={() => void sendMessage(true)}><CornerDownRight size={14} aria-hidden="true" /><span>{t("Add to running task")}</span></button>}
            {((taskIsRunning && runtime.task.executionId) || (taskIsQueued && !has('turn.cancelQueued'))) && <button type="button" className="stop-button" disabled={working} onClick={() => void stopRun()} aria-label={t(taskIsQueued ? 'Cancel queued' : 'Stop')} title={t(taskIsQueued ? 'Cancel queued' : 'Stop')}><Square size={12} fill="currentColor" aria-hidden="true" /></button>}
            <button type="button" className="send-button" disabled={!activeSession || (!draft.trim() && !attachments.length) || working || connectionStatus !== 'connected'} onClick={() => void sendMessage()} aria-label={t("Send message")} title={t("Send message")}>{working ? <LoaderCircle size={17} className="spin" /> : <ArrowUp size={18} />}</button>
          </div>
        </div>
      </div>
      {(activeSession?.sessionUsage || runtime.goal || runningCommandsCount > 0) && (
        <div className="composer-telemetry">
          {activeSession?.sessionUsage && (
            <button type="button" className="telemetry-chip" onClick={() => openHarnessTab('context')}>
              <span>
                {activeSession.sessionUsage.input.toLocaleString()} {t('input')} · {activeSession.sessionUsage.output.toLocaleString()} {t('output')}
                {contextLimit ? ` · ${contextLimit.toLocaleString()} ${t('context limit')}` : ''}
              </span>
            </button>
          )}
          {runtime.goal && (
            <button type="button" className="telemetry-chip" onClick={() => openHarnessTab('goal')}>
              <Target size={11} aria-hidden="true" />
              <span>{t('Goal')}: {t(runtime.goal.status)}</span>
            </button>
          )}
          {runningCommandsCount > 0 && (
            <button type="button" className="telemetry-chip" onClick={() => openHarnessTab('processes')}>
              <Terminal size={11} aria-hidden="true" />
              <span>{runningCommandsCount} {t('running')}</span>
            </button>
          )}
        </div>
      )}
    </div>
  )

  return (
    <div
      className={`app-shell${quick ? ' quick-mode' : ''}${!desktop ? ' web-workbench' : ''}${sidebarCollapsed && !compactLayout ? ' sidebar-collapsed' : ''}${harnessOpen ? ' files-open' : ''}${harnessOpen && harnessOverlay ? ' harness-overlay' : ''}${draggingColumn ? ' resizing' : ''}`}
      style={{ '--sidebar-width': `${sidebarWidth}px`, '--sidebar-edge': `${sidebarCollapsed ? 56 : sidebarWidth}px`, '--harness-width': `${harnessWidth}px` } as CSSProperties}
    >
      {compactLayout ? <dialog id={accessibilityId + '-drawer'} className="session-drawer" aria-labelledby={accessibilityId + '-drawer-title'} ref={drawerRef}
        onClose={() => {
          if (drawerRef.current?.open) return
          setDrawerOpen(false)
          drawerButtonRef.current?.focus()
        }}
        onClick={(event) => { if (event.target === event.currentTarget) closeDrawer() }}>
        <div className="drawer-panel">
          <div className="drawer-heading"><h2 id={accessibilityId + '-drawer-title'}>{t('Conversations')}</h2><button type="button" className="icon-button" aria-label={t('Close conversations')} onClick={closeDrawer}><X size={18} /></button></div>
          {sessionNavigation}
        </div>
      </dialog> : <aside className="sidebar" aria-label={t('Conversations')} aria-hidden={sidebarCollapsed || undefined}>
        <div className="sidebar-top">
          <button type="button" className="icon-button" aria-label={t('Toggle sidebar')} title={t('Toggle sidebar')} onClick={() => setSidebarCollapsed(true)}><PanelLeftClose size={18} /></button>
          <div className="sidebar-top-actions">
            <button type="button" className="icon-button" aria-label={t('Search conversations')} title={t('Search conversations')} aria-pressed={searchOpen} onClick={() => setSearchOpen((open) => !open)}><Search size={17} /></button>
            <button type="button" className="icon-button" aria-label={t('New conversation')} title={t('New conversation')} disabled={working} onClick={() => void createSession()}><SquarePen size={17} /></button>
          </div>
        </div>
        {sessionNavigation}
      </aside>}
      {!compactLayout && sidebarCollapsed && <nav className="sidebar-rail" aria-label={t('Quick navigation')}>
        <button type="button" className="icon-button" title={t('Expand conversations')} aria-label={t('Expand conversations')} onClick={() => setSidebarCollapsed(false)}><PanelLeftOpen size={18} /></button>
        <button type="button" className="icon-button" title={t('New conversation')} aria-label={t('New conversation')} disabled={working} onClick={() => void createSession()}><SquarePen size={17} /></button>
        <button type="button" className="icon-button sidebar-rail-settings" title={t('Settings')} aria-label={t('Settings')} onClick={() => { setSettingsTab(settingsAPI.supportsModelConfig ? 'models' : 'remote'); setSettingsOpen(true) }}><Settings size={17} /></button>
      </nav>}
      {!compactLayout && !sidebarCollapsed && <div
        className="column-resizer sidebar-resizer" role="separator" aria-orientation="vertical" aria-label={t('Resize conversation sidebar')}
        aria-valuemin={200} aria-valuemax={420} aria-valuenow={sidebarWidth} tabIndex={0}
        onPointerDown={(event) => { event.preventDefault(); setDraggingColumn('sidebar'); columnDrag.current = { side: 'sidebar', x: event.clientX, width: sidebarWidth } }}
        onDoubleClick={() => setSidebarWidth(260)}
        onKeyDown={(event) => {
          if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
          event.preventDefault(); setSidebarWidth((width) => Math.max(200, Math.min(420, width + (event.key === 'ArrowRight' ? 16 : -16))))
        }}
      />}
      {!compactLayout && harnessOpen && !harnessFullScreen && <div
        className="column-resizer harness-resizer" role="separator" aria-orientation="vertical" aria-label={t('Resize Harness panel')}
        aria-valuemin={320} aria-valuemax={760} aria-valuenow={harnessWidth} tabIndex={0}
        onPointerDown={(event) => { event.preventDefault(); setDraggingColumn('harness'); columnDrag.current = { side: 'harness', x: event.clientX, width: harnessWidth } }}
        onDoubleClick={() => setHarnessWidth(420)}
        onKeyDown={(event) => {
          if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
          event.preventDefault(); setHarnessWidth((width) => Math.max(320, Math.min(760, width + (event.key === 'ArrowLeft' ? 16 : -16))))
        }}
      />}

      <main className="chat-area">
        <header className="chat-header">
          <div className="header-left">
            {compactLayout && (
              <button type="button" className="icon-button drawer-toggle" title={t('Open conversations')} aria-label={t('Open conversations')} aria-haspopup="dialog" aria-controls={accessibilityId + '-drawer'} aria-expanded={drawerOpen} onClick={openDrawer} ref={drawerButtonRef}><PanelLeftOpen size={18} /></button>
            )}
            {workspaceName ? (
              <span className="header-workspace" title={workspacePath}>{workspaceName}</span>
            ) : (
              <span className="header-brand-title">Lune</span>
            )}
            {activeSession?.title && (
              <>
                <span className="header-crumb-sep" aria-hidden="true">/</span>
                <span className="header-session-title" title={activeSession.title}>{activeSession.title}</span>
              </>
            )}
          </div>
          <div className="header-actions">
            {connectionStatus !== 'connected' && <span className={`connection-pill ${connectionStatus}`}>{statusIcon}{t(statusLabel)}</span>}
            {quick && <button className="plain-button" onClick={() => void window.luneDesktop?.toggleQuick()}>Main window</button>}
            <button
              ref={fileButtonRef}
              type="button"
              className="icon-button header-files-toggle"
              aria-label={t('Open folder as file tree')}
              title={t('Open folder as file tree')}
              onClick={() => void openWorkspaceAsFileTree(true)}
            >
              <FolderOpen size={17} />
            </button>
            <button
              type="button"
              className="icon-button header-files-toggle"
              aria-expanded={harnessOpen}
              aria-label={t('Harness')}
              title={`${t('Harness')} (Ctrl+B)`}
              onClick={() => setHarnessOpen((open) => !open)}
            >
              <PanelRight size={17} />
            </button>
          </div>
        </header>

        {activeSession && runtime.goal && (
          <div className="goal-bar">
            <button type="button" className="goal-bar-main" onClick={() => openHarnessTab('goal')}>
              <Target size={14} aria-hidden="true" />
              <span className={`status-pill${runtime.goal.status === 'active' ? ' active' : ''}`}>{t(runtime.goal.status)}</span>
              <span className="goal-bar-text">{runtime.goal.objective}</span>
              {runtime.goal.token_budget ? (
                <span className="goal-bar-budget">{(runtime.goal.tokens_used ?? 0).toLocaleString()} / {runtime.goal.token_budget.toLocaleString()}</span>
              ) : null}
            </button>
            {has('goal.manage') && (runtime.goal.status === 'active' || runtime.goal.status === 'paused') && (
              <button
                type="button"
                className="row-action-btn"
                disabled={working}
                aria-label={t(runtime.goal.status === 'active' ? 'Pause goal' : 'Resume goal')}
                title={t(runtime.goal.status === 'active' ? 'Pause goal' : 'Resume goal')}
                onClick={() => void sessionAction(async () => {
                  if (!client || !activeSessionId) return
                  await client.request(runtime.goal?.status === 'active' ? 'goal.pause' : 'goal.resume', { sessionId: activeSessionId })
                  await refreshRuntime(client, activeSessionId)
                })}
              >
                {runtime.goal.status === 'active' ? <Pause size={13} aria-hidden="true" /> : <Play size={13} aria-hidden="true" />}
              </button>
            )}
          </div>
        )}

        {isEmptyConversation ? (
          <div className="empty-stage">
            <h1 className="empty-headline">{t('What would you like to work through?')}</h1>
            {composerNode}
            <div className="suggestion-row">
              <button type="button" onClick={() => setDraft(t('Help me understand this project.'))}>{t("Understand a project")}<ArrowUpRight size={14} aria-hidden="true" /></button>
              <button type="button" onClick={() => setDraft(t('Help me make a plan for…'))}>{t("Make a plan")}<ArrowUpRight size={14} aria-hidden="true" /></button>
            </div>
          </div>
        ) : (
          <>
            <div className="conversation-scroll">
              <div className="conversation-column">
                <TimelineView
                  entries={timeline}
                  optimistic={optimistic.filter((item) => item.sessionId === activeSessionId)}
                  liveTools={liveTools}
                  streamingReasoning={streamingReasoning}
                  streamingText={streamingText}
                  canImplementPlan={has('turn.implementPlan')}
                  sessionBusy={sessionBusy}
                  working={working}
                  latestExecutionId={latestExecutionId}
                  detailsExpanded={uiPreferences.timelineDetailsExpanded}
                  onImplementPlan={(executionId, planModeChoice) => implementPlan(executionId, planModeChoice)}
                />

                {activity.length > 0 && (taskIsRunning || runtime.queue.length > 0) && <div className="activity-tray">{activity.slice(0, 3).map((item) => <div className={`activity-item ${item.failed ? 'failed' : ''}`} key={item.id}><span className="activity-dot" /> <span>{item.label}</span>{item.detail && <small>{item.detail}</small>}</div>)}</div>}

                {taskIsPaused && modelFailure && <div className="model-recovery-card">
                  <div className="pending-heading"><div className="pending-icon"><RefreshCw size={16} /></div><div><strong>{t('Model request paused')}</strong><span>{t('The saved task can resume after the retry delay.')}</span></div></div>
                  <p className="pending-reason">
                    {t(modelFailure.category)}
                    {modelFailure.status_code ? ` · HTTP ${modelFailure.status_code}` : ''}
                    {modelFailure.code ? ` · ${modelFailure.code}` : ''}
                    {modelFailure.attempts ? ` · ${t('Attempts')}: ${modelFailure.attempts}` : ''}
                  </p>
                  {Number.isFinite(Date.parse(modelFailure.retry_not_before || '')) && recoveryClock < Date.parse(modelFailure.retry_not_before || '')
                    ? <p className="retry-countdown">{t('Retry available in')} {Math.ceil((Date.parse(modelFailure.retry_not_before || '') - recoveryClock) / 1000)}s</p>
                    : <p className="retry-countdown">{t('Retry is available now.')}</p>}
                  <div className="pending-actions">
                    <button className="plain-button danger" disabled={working} onClick={() => void discardPaused()}>{t('Discard')}</button>
                    <button className="secondary-button" disabled={working} onClick={() => { setSettingsTab('models'); setSettingsOpen(true) }}>{t('Open model settings')}</button>
                    <button className="primary-button compact" disabled={working || (Number.isFinite(Date.parse(modelFailure.retry_not_before || '')) && recoveryClock < Date.parse(modelFailure.retry_not_before || ''))} onClick={() => void resumeRun()}><RefreshCw size={14} />{t('Resume run')}</button>
                  </div>
                </div>}

                {pending.filter((item) => item.kind !== 'model_recovery').map((item, index) => <div className="pending-card" key={`${item.kind}-${item.approval?.id ?? item.userQuestion?.id ?? item.interaction?.id ?? item.toolRecovery?.invocationId ?? index}`}>
                  <div className="pending-heading"><div className="pending-icon"><CircleHelp size={17} /></div><div><strong>{t(item.kind === 'approval' ? 'Permission needed' : item.kind === 'user_question' ? 'A question for you' : item.kind === 'user_interaction' ? 'Input requested' : item.kind === 'tool_recovery' ? 'Tool needs attention' : item.kind === 'model_recovery' ? 'Run paused' : 'Execution blocked')}</strong><span>{item.kind === 'approval' ? `${t('Lune wants to use')} ${item.approval?.tool ?? t('a tool')}` : t('Your response will continue this run.')}</span></div></div>
                  {item.approval && <>
                    {item.approval.autoReviewReason && <p className="pending-reason">{item.approval.autoReviewReason}</p>}
                    <pre className="json-preview">{JSON.stringify(item.approval.arguments, null, 2)}</pre>
                    <div className="pending-actions"><button className="plain-button danger" disabled={working} onClick={() => void resolveApproval(item.approval!, 'deny')}>{t("Deny")}</button><button className="secondary-button" disabled={working} onClick={() => void resolveApproval(item.approval!, 'allow_once')}>{t("Allow once")}</button><button className="primary-button compact" disabled={working} onClick={() => void resolveApproval(item.approval!, 'allow_session')}>{t("Allow this session")}</button></div>
                  </>}
                  {item.userQuestion && <QuestionForm request={item.userQuestion} disabled={working} onSubmit={(answers) => resolveQuestion(item.userQuestion!, answers)} />}
                  {item.interaction && <InteractionForm request={item.interaction} disabled={working} onSubmit={(value) => resolveInteraction(item.interaction!, value)} />}
                  {item.toolRecovery && <><p className="pending-reason"><strong>{item.toolRecovery.tool}</strong> · {t('You can retry this tool or skip it.')}</p><pre className="json-preview">{JSON.stringify(item.toolRecovery.arguments, null, 2)}</pre><div className="pending-actions"><button className="secondary-button" disabled={working} onClick={() => void resolvePending('toolRecovery.resolve', { sessionId: activeSessionId, invocationId: item.toolRecovery!.invocationId, decision: 'skip' })}>{t("Skip")}</button><button className="primary-button compact" disabled={working} onClick={() => void resolvePending('toolRecovery.resolve', { sessionId: activeSessionId, invocationId: item.toolRecovery!.invocationId, decision: 'retry' })}>{t("Retry tool")}</button></div></>}
                  {item.blocker && <p className="pending-reason">{item.blocker.reason}</p>}
                </div>)}

                {taskIsPaused && !modelFailure && <div className="paused-banner"><div><strong>{t("This run is paused")}</strong><span>{t("Resume from its saved checkpoint, or discard it.")}</span></div><button className="plain-button danger" disabled={working} onClick={() => void discardPaused()}>{t("Discard")}</button><button className="primary-button compact" disabled={working} onClick={() => void resumeRun()}><RefreshCw size={14} />{t("Resume run")}</button></div>}
                {runtime.queue.length > 0 && <div className="queue-banner"><Command size={15} /><div><div className="queue-banner-head"><strong>{t("Message queue")}</strong>{has('turn.cancelPending') && <button type="button" className="plain-button" disabled={working} onClick={cancelAllPending}>{t("Cancel all")}</button>}</div>
                  <ol>{runtime.queue.map((item) => <li key={item.executionId}>
                    <span>#{item.position} · {t(item.status)}</span>
                    {has('turn.cancelQueued') && item.status === 'queued' && <button className="plain-button" disabled={working} onClick={() => cancelQueued(item.executionId)}>{t("Cancel")}</button>}
                  </li>)}</ol>
                </div></div>}
                <div ref={bottomRef} />
              </div>
            </div>
            {composerNode}
          </>
        )}
      </main>
      {client && (
        <HarnessPanel
          client={client}
          workspace={workspaceInfo}
          visible={harnessOpen}
          fullScreen={harnessFullScreen}
          onToggleFullScreen={() => setHarnessFullScreen((value) => !value)}
          tab={harnessTab}
          onTabChange={setHarnessTab}
          onClose={closeHarness}
          capabilities={capabilities}
          activeSession={activeSession}
          runtime={runtime}
          models={models}
          initialFile={requestedFile}
          initialCommandId={requestedCommandId}
          onInsertPath={insertPathReference}
          onChangeReasoningEffort={(effort) => void changeSettings({ reasoningEffort: effort })}
          onRefreshState={async () => {
            if (client && activeSessionId) await refreshSessionData(client, activeSessionId)
          }}
        />
      )}

      {settingsDialogNode}
      {sessionDialog && <div className="modal-backdrop">
        <section className="connection-modal" role="dialog" aria-modal="true" aria-labelledby="session-dialog-title">
          <h2 id="session-dialog-title">{t(sessionDialog.kind === 'rename' ? 'Rename conversation' : sessionDialog.kind === 'delete' ? 'Delete conversation?' : 'Rewind conversation')}</h2>
          {sessionDialog.kind === 'delete' ? <p>{t("This removes the conversation and its saved history.")}</p> :
            <label className="form-field">{t(sessionDialog.kind === 'rename' ? 'Title' : 'User turns to remove')}
              <input value={sessionDialog.value} type={sessionDialog.kind === 'rewind' ? 'number' : 'text'} min={1}
                onChange={(event) => setSessionDialog({ ...sessionDialog, value: event.target.value })} />
            </label>}
          {Boolean(sessionDialog.excerpts?.length) && <ol className="rewind-excerpts">{sessionDialog.excerpts?.map((excerpt, index) => <li key={index}>{excerpt}</li>)}</ol>}
          {sessionDialog.removed !== undefined && <p>{sessionDialog.removed} {t('turn(s) will be removed from the end of the conversation.')}</p>}
          {actionError && <div className="form-error" role="alert">{actionError}</div>}
          <div className="modal-footer"><button className="plain-button" disabled={working} onClick={() => setSessionDialog(null)}>{t("Cancel")}</button><button className={`primary-button compact${sessionDialog.kind === 'delete' ? ' danger' : ''}`} disabled={working || sessionDialog.removed === 0} onClick={confirmSessionDialog}>
            {t(sessionDialog.kind === 'rename' ? 'Save' : sessionDialog.kind === 'delete' ? 'Delete' : sessionDialog.digest ? 'Rewind' : 'Preview')}
          </button></div>
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
  const t = useTranslate()
  const [answers, setAnswers] = useState<Record<string, string>>({})
  const questions = request.questions ?? []
  const complete = questions.length > 0 && questions.every((question) => (answers[question.id] ?? '').trim())
  return <div className="question-form">
    {questions.map((question) => <fieldset className="question-item" key={question.id}><legend>{question.question}</legend>
      {question.options?.length ? <div className="question-options">{question.options.map((option) => <label className={`question-option ${answers[question.id] === option.label ? 'chosen' : ''}`} key={option.label}><input type="radio" name={question.id} value={option.label} checked={answers[question.id] === option.label} onChange={() => setAnswers((value) => ({ ...value, [question.id]: option.label }))} /><span><strong>{option.label}{option.recommended && <em>{t("Suggested")}</em>}</strong><small>{option.description}</small></span></label>)}</div> : <input className="text-answer" value={answers[question.id] ?? ''} onChange={(event) => setAnswers((value) => ({ ...value, [question.id]: event.target.value }))} placeholder={t("Your answer")} />}
    </fieldset>)}
    {!questions.length && <p className="pending-reason">{t("This clarification needs an answer, but Core did not provide question details.")}</p>}
    <div className="pending-actions"><button className="primary-button compact" disabled={disabled || !complete} onClick={() => onSubmit(questions.map((question) => ({ question_id: question.id, answer: answers[question.id].trim() })))}><Check size={14} />{t("Send answer")}</button></div>
  </div>
}

function InteractionForm({ request, disabled, onSubmit }: {
  request: NonNullable<Pending['interaction']>
  disabled: boolean
  onSubmit: (value: unknown) => void
}) {
  const t = useTranslate()
  const [value, setValue] = useState('{\n  "action": "accept",\n  "content": {}\n}')
  const [error, setError] = useState('')
  return <div className="interaction-form"><p className="pending-reason">{request.serverName} {t('is asking for input.')}</p><pre className="json-preview">{JSON.stringify(request.request, null, 2)}</pre><label className="interaction-label">{t("Response JSON")}</label><textarea value={value} onChange={(event) => { setValue(event.target.value); setError('') }} spellCheck={false} /><div className="pending-actions">{error && <small className="inline-error">{t(error)}</small>}<button className="primary-button compact" disabled={disabled} onClick={() => { try { onSubmit(JSON.parse(value)) } catch { setError('Enter a valid JSON response.') } }}><Check size={14} />{t("Send response")}</button></div></div>
}
