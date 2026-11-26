import { useCallback, useEffect, useId, useMemo, useRef, useState, type CSSProperties } from 'react'
import { createDesktopSettingsAPI, createRemoteOnlySettingsAPI } from './settings-api'
import { formatCapacity, SettingsModal, type SettingsTab } from './settings-modal'
import type { SavedConnection, WorkspaceRecord, WorkspaceSessionRecord } from '../shared/desktop-settings'
import { WorkspaceTree, type WorkspaceTreeItem } from './workspace-tree'
import { lastLoopbackSession, rememberLoopbackSession } from './loopback'
import {
  loadLastSessionSettings,
  resolveNewSessionSettings,
  saveLastSessionSettings,
  sessionDefaultsScopeKey,
} from './session-defaults'
import {
  applyUsageEvent, deriveSessionUsageMetrics, formatTokenCount, isLeadMainUsage,
  mergeAppliedUsageKeys, usageEventDedupKey, type LiveContextOverride,
} from './usage-metrics'
import { useTranslation } from 'react-i18next'
import { LanguagePicker, useLanguage } from './language'
import type { ManagedWorkspace, WorkspaceActivity } from './workbench'
import { type WorkspaceInfo } from './workspace-files'
import { applyTheme, ThemePicker } from './theme'
import type { ThemePreference } from '../shared/theme'
import { defaultUiPreferences, type UiPreferences } from '../shared/ui-preferences'
import { MarkdownContent } from './markdown'
import { HarnessPanel } from './harness-panel'
import { deriveAgentSnapshots } from './agent-state'
import type { TerminalTarget } from './terminal-transport'
import { TimelineView } from './timeline'
import {
  clearCapturedReasoning, mergeCapturedReasoning, readCapturedReasoning,
  StreamStore, writeCapturedReasoning,
} from './stream-store'
import { CommandPalette, type PaletteActionItem, type PaletteWorkspaceItem } from './command-palette'
import { matchSlashCommands, parseSlashCommand, slashCommands } from './slash-commands'
import {
  buildTimeline, objectValue as object, timelineMessages,
  type Activity, type CommandInfo, type HarnessTab, type HistoryAttachment, type HistoryMessage, type LiveAgentRun,
  type LiveToolStep, type ModelFailure, type ModelOption, type OptimisticMessage, type Pending,
  type ReviewContext, type RuntimeEvent, type RuntimeState, type SessionSummary, type TaskDiagnosisContext, type TimelineEntry,
  type UserQuestionDraft,
} from './harness-types'
import {
  Archive, ArrowDown, ArrowUp, ArrowUpRight, Camera, Check, ChevronDown, CircleHelp, Code2,
  Command, Compass, CornerDownRight, Download, File as FileIcon, Folder, FolderOpen, GitFork, Laptop,
  LoaderCircle, Lock, LockKeyhole, Minimize2, MoreHorizontal, PanelLeftClose,
  PanelLeftOpen, PanelRight, Pause, Pencil, Play, Plus, Radio, RefreshCw,
  RotateCcw, Search, Settings, Shield, ShieldAlert, ShieldCheck, Sparkles,
  Square, SquarePen, Target, Terminal, Trash2, Wifi, WifiOff, X,
} from 'lucide-react'
import { downloadMarkdown, isSupportedClipboardFile, readAttachment, restoreHistoryAttachments, validateAttachments, type Attachment } from './attachments'
import { connectRuntime, type RuntimeClient, type RuntimeConnection } from './runtime'
import {
  backdropOverlay, bannerError, bannerStatus, btnIcon, btnPlain, btnPlainDanger,
  btnPrimary, btnPrimaryCompact, btnPrimaryDanger, btnSecondary, btnSubtleIcon,
  cn, fieldInput, fieldTextarea, popoverSurface, statusPill,
} from './ui'
import { filterMentionItems, indexWorkspaceFiles, matchMentionQuery, type MentionItem } from './workspace-mentions'

const sessionPageSize = 50
type PermissionMode = 'read-only' | 'workspace' | 'auto' | 'full-access'
const PERMISSION_MODES: readonly PermissionMode[] = ['read-only', 'workspace', 'auto', 'full-access']
const PERM_LABEL: Record<PermissionMode, string> = {
  'read-only': 'Read-only',
  workspace: 'Workspace',
  auto: 'Auto review',
  'full-access': 'Full access',
}
const PERM_DESC: Record<PermissionMode, string> = {
  'read-only': 'Read workspace files; external directories and web need approval; block writes and commands',
  workspace: 'Read and write workspace files; commands, external directories, and web need approval',
  auto: 'Read and write workspace files; commands and external actions are auto-reviewed first',
  'full-access': 'Allow tools and user directories without prompts; extremely dangerous commands stay blocked',
}
const EFFORT_TIERS: ReadonlyArray<{ value: string; label: string; bars: number }> = [
  { value: '', label: 'Auto', bars: 0 },
  { value: 'low', label: 'Low', bars: 1 },
  { value: 'medium', label: 'Medium', bars: 2 },
  { value: 'high', label: 'High', bars: 3 },
  { value: 'max', label: 'Max', bars: 4 },
]

function effortDisplayInfo(raw: string): { idx: number; label: string; bars: number; isMax: boolean } {
  const exact = EFFORT_TIERS.findIndex((t) => t.value === raw)
  if (exact >= 0) {
    const tier = EFFORT_TIERS[exact]
    return { idx: exact, label: tier.label, bars: tier.bars, isMax: raw === 'max' }
  }
  switch (raw) {
    case 'none': return { idx: 0, label: 'None', bars: 0, isMax: false }
    case 'minimal': return { idx: 1, label: 'Minimal', bars: 1, isMax: false }
    case 'xhigh': return { idx: 3, label: 'XHigh', bars: 3, isMax: false }
    default: return { idx: 0, label: 'Auto', bars: 0, isMax: false }
  }
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

function runtimeHasPendingWork(state: RuntimeState): boolean {
  return ['running', 'queued', 'paused'].includes(state.task.status) || state.queue.length > 0
}

function runtimeHasPausedWork(state: RuntimeState): boolean {
  return state.task.status === 'paused' || state.queue.some((turn) => turn.status === 'paused')
}

async function coreHasPendingWork(client: RuntimeClient): Promise<boolean> {
  const pageSize = 100
  for (let offset = 0; ; ) {
    const sessions = await client.request<Array<{ id: string }>>('session.list', {
      offset,
      limit: pageSize,
      includeArchived: true,
    })
    for (let start = 0; start < sessions.length; start += 20) {
      const batch = sessions.slice(start, start + 20)
      const states = await Promise.all(batch.map((session) => client.request<RuntimeState>('runtime.state', { sessionId: session.id })))
      if (states.some((state) => runtimeHasPendingWork(normalizeRuntimeState(state)))) return true
    }
    offset += sessions.length
    if (sessions.length < pageSize) return false
  }
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
    .map((label) => ({
      label,
      items: [...buckets[label]].sort((a, b) => (Date.parse(b.updatedAt) || 0) - (Date.parse(a.updatedAt) || 0)),
    }))
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

function keepOptionInView(container: HTMLElement | null, index: number): void {
  if (!container) return
  const items = container.querySelectorAll<HTMLElement>('[role="option"]')
  const item = items[index]
  if (!item) return
  const pad = 6 // matches p-1.5 container padding
  const cRect = container.getBoundingClientRect()
  const iRect = item.getBoundingClientRect()
  const itemTop = iRect.top - cRect.top + container.scrollTop
  const itemBottom = itemTop + iRect.height
  if (itemTop - pad < container.scrollTop) {
    container.scrollTop = Math.max(0, itemTop - pad)
  } else if (itemBottom + pad > container.scrollTop + container.clientHeight) {
    container.scrollTop = itemBottom + pad - container.clientHeight
  }
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
  const { t } = useTranslation()
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
  const [desktopWorkspaces, setDesktopWorkspaces] = useState<WorkspaceRecord[]>([])
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
  const [reviewContext, setReviewContext] = useState<ReviewContext | null>(null)
  const [diagnosisContext, setDiagnosisContext] = useState<TaskDiagnosisContext | null>(null)
  const [timeline, setTimeline] = useState<TimelineEntry[]>([])
  const [history, setHistory] = useState<HistoryMessage[]>([])
  const [optimistic, setOptimistic] = useState<OptimisticMessage[]>([])
  const [runtime, setRuntime] = useState<RuntimeState>(emptyRuntime)
  const [uiPreferences, setUiPreferences] = useState<UiPreferences>(defaultUiPreferences)
  const [commands, setCommands] = useState<CommandInfo[]>([])
  const [pending, setPending] = useState<Pending[]>([])
  const [modelFailure, setModelFailure] = useState<ModelFailure | null>(null)
  const [recoveryClock, setRecoveryClock] = useState(Date.now())
  const [hasActiveStream, setHasActiveStream] = useState(false)
  const [liveAgents, setLiveAgents] = useState<LiveAgentRun[]>([])
  const [selectedAgentId, setSelectedAgentId] = useState('')
  const [liveTools, setLiveTools] = useState<LiveToolStep[]>([])
  const [latestExecutionId, setLatestExecutionId] = useState('')
  const [activity, setActivity] = useState<Activity[]>([])
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [editingQueueExecId, setEditingQueueExecId] = useState('')
  const [queueEditDraft, setQueueEditDraft] = useState('')
  const [draft, setDraft] = useState('')
  const [cursorPos, setCursorPos] = useState(0)
  const [mentionItems, setMentionItems] = useState<MentionItem[]>([])
  const [mentionLoading, setMentionLoading] = useState(false)
  const [mentionIndex, setMentionIndex] = useState(0)
  const [mentionDismissedAt, setMentionDismissedAt] = useState<number | null>(null)
  const [showScrollBottom, setShowScrollBottom] = useState(false)
  const [slashIndex, setSlashIndex] = useState(0)
  const [slashNotice, setSlashNotice] = useState('')
  const [rowMenuId, setRowMenuId] = useState('')
  const [search, setSearch] = useState('')
  const [showArchived, setShowArchived] = useState(false)
  const [sessionOffset, setSessionOffset] = useState(0)
  const [moreSessions, setMoreSessions] = useState(false)
  const [listLoading, setListLoading] = useState(false)
  const [listRevision, setListRevision] = useState(0)
  const linkedSessionId = useMemo(() => {
    const value = new URLSearchParams(window.location.search).get('sessionId')?.trim() ?? ''
    return value.slice(0, 256)
  }, [])
  const linkedSessionIdRef = useRef(linkedSessionId)
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
  const [composerMenu, setComposerMenu] = useState<'permission' | 'model' | null>(null)
  const [permissionMode, setPermissionMode] = useState<PermissionMode>('workspace')
  const [allowedPermissionModes, setAllowedPermissionModes] = useState<PermissionMode[]>([...PERMISSION_MODES])
  const [draggingColumn, setDraggingColumn] = useState<'sidebar' | 'harness' | null>(null)
  const compactLayout = narrow || quick
  const [sessionDialog, setSessionDialog] = useState<null | {
    kind: 'rename' | 'delete' | 'rewind'; sessionId: string; value: string; digest?: string; removed?: number; excerpts?: string[]
  }>(null)
  const [editResendConfirmation, setEditResendConfirmation] = useState<null | { sessionId: string; turns: number; text: string; attachments: HistoryAttachment[] }>(null)
  const [configReloadPending, setConfigReloadPending] = useState(false)
  const [busy, setBusy] = useState(false)
  const [working, setWorking] = useState(false)
  const [actionError, setActionError] = useState('')

  const capabilitiesRef = useRef<string[]>([])
  const submissionRetry = useRef<{ key: string; id: string; mode: string } | null>(null)
  const editResendRetry = useRef<{ key: string; id: string; historyDigest: string; baselineCount: number } | null>(null)
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
  const [liveContextOverride, setLiveContextOverride] = useState<LiveContextOverride>({ compacting: false })
  const appliedUsageKeysRef = useRef<Set<string>>(new Set())
  const sessionsRef = useRef<SessionSummary[]>(sessions)
  sessionsRef.current = sessions
  const modelsRef = useRef<ModelOption[]>(models)
  modelsRef.current = models
  const activeSessionRef = useRef<SessionSummary | null>(activeSession)
  activeSessionRef.current = activeSession
  const workspacePathRef = useRef(workspaceInfo?.path ?? '')
  workspacePathRef.current = workspaceInfo?.path ?? ''
  const currentDefaultsScopeKey = useCallback(() => {
    const conn = connectionRef.current
    return sessionDefaultsScopeKey({
      mode: conn?.mode,
      workspace: managed?.workspace.path || workspacePathRef.current || (conn?.mode === 'local' ? conn.workspace : undefined),
      workspaceId: managed?.workspace.id ?? '',
      url: conn?.mode === 'remote' ? conn.url : undefined,
    })
  }, [managed?.workspace.id, managed?.workspace.path])
  const createSessionWithDefaults = useCallback(async (
    targetClient: RuntimeClient,
    availableModels: ModelOption[],
  ): Promise<SessionSummary> => {
    const created = await targetClient.request<SessionSummary>('session.create')
    const saved = loadLastSessionSettings(currentDefaultsScopeKey())
    const { nextSettings, changed } = resolveNewSessionSettings(created, saved, availableModels)
    if (!changed) return created
    try {
      await targetClient.request('session.setSettings', { sessionId: created.id, settings: nextSettings })
      return { ...created, settings: nextSettings }
    } catch {
      return created
    }
  }, [currentDefaultsScopeKey])
  const streamingExecution = useRef<string | undefined>(undefined)
  const pendingClearLiveRef = useRef(false)
  const reasoningStartTsRef = useRef(0)
  const lastRawEntryIdRef = useRef('')
  const turnBaselineEntryIdRef = useRef('')
  const streamStore = useMemo(() => new StreamStore((active) => setHasActiveStream(active)), [])
  useEffect(() => () => streamStore.dispose(), [streamStore])
  const subagentStreamBufRef = useRef<Map<string, {
    agentName?: string
    activity?: string
    reasoning: string
    text: string
    modelProfile?: string
    provider?: string
    model?: string
  }>>(new Map())
  const subagentStreamRafRef = useRef<number | null>(null)
  const flushSubagentStreams = useCallback(() => {
    if (subagentStreamRafRef.current !== null) {
      window.cancelAnimationFrame(subagentStreamRafRef.current)
      subagentStreamRafRef.current = null
    }
    const buf = subagentStreamBufRef.current
    if (buf.size === 0) return
    const batch = new Map(buf)
    buf.clear()
    const now = Date.now()
    setLiveAgents((agents) => {
      const next = [...agents]
      for (const [agentKey, delta] of batch) {
        const idx = next.findIndex((a) => a.id === agentKey)
        if (idx >= 0) {
          const cur = next[idx]
          const streamStartedAt = cur.streamStartedAt || now
          const deltaChars = delta.reasoning.length + delta.text.length
          const streamChars = (cur.streamChars ?? 0) + deltaChars
          const elapsed = (now - streamStartedAt) / 1000
          const tokensPerSecond = elapsed >= 0.05 ? Math.max(1, streamChars / 4) / elapsed : cur.tokensPerSecond
          next[idx] = {
            ...cur,
            name: delta.agentName || cur.name,
            activity: delta.activity || cur.activity,
            modelProfile: delta.modelProfile || cur.modelProfile,
            provider: delta.provider || cur.provider,
            model: delta.model || cur.model,
            reasoningText: (cur.reasoningText ?? '') + delta.reasoning,
            outputText: (cur.outputText ?? '') + delta.text,
            streamStartedAt,
            streamChars,
            tokensPerSecond,
          }
        } else {
          const shortName = delta.agentName || agentKey.split('/').at(-1) || 'Subagent'
          const streamChars = delta.reasoning.length + delta.text.length
          next.push({
            id: agentKey,
            name: shortName,
            role: 'worker',
            status: 'running',
            activity: delta.activity || 'Working',
            toolCount: 0,
            tools: [],
            startedAt: now,
            modelProfile: delta.modelProfile,
            provider: delta.provider,
            model: delta.model,
            reasoningText: delta.reasoning,
            outputText: delta.text,
            streamStartedAt: now,
            streamChars,
          })
        }
      }
      return next
    })
  }, [])
  useEffect(() => () => {
    if (subagentStreamRafRef.current !== null) {
      window.cancelAnimationFrame(subagentStreamRafRef.current)
      subagentStreamRafRef.current = null
    }
  }, [])
  const sessionRefreshSequence = useRef(0)
  const fileInput = useRef<HTMLInputElement>(null)
  const messagesScrollRef = useRef<HTMLDivElement | null>(null)
  const conversationColumnRef = useRef<HTMLDivElement | null>(null)
  const stickToBottomRef = useRef(true)
  const initialScrollSnapRef = useRef(true)
  const virtualScrollTopRef = useRef<number | null>(null)
  const scrollRafRef = useRef<number | null>(null)
  const scrollLastTsRef = useRef(0)
  const smoothButtonUntilRef = useRef(0)
  const touchStartYRef = useRef<number | null>(null)
  const [activeTurnId, setActiveTurnId] = useState('')
  const activeTurnIdRef = useRef('')
  const activeTurnRafRef = useRef<number | null>(null)
  const jumpFlashTimerRef = useRef<number | null>(null)
  const mentionCacheRef = useRef<{ key: string; items: MentionItem[]; loaded: boolean }>({ key: '', items: [], loaded: false })
  const bottomRef = useRef<HTMLDivElement | null>(null)
  const drawerRef = useRef<HTMLDialogElement>(null)
  const drawerButtonRef = useRef<HTMLButtonElement>(null)
  const fileButtonRef = useRef<HTMLButtonElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const slashPopoverRef = useRef<HTMLDivElement | null>(null)
  const mentionPopoverRef = useRef<HTMLDivElement | null>(null)
  const slashScrollByKeyRef = useRef(false)
  const mentionScrollByKeyRef = useRef(false)
  const popoverPointerRef = useRef<{ x: number; y: number }>({ x: -1, y: -1 })

  useEffect(() => {
    const el = textareaRef.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.max(48, Math.min(el.scrollHeight, 200))}px`
  }, [draft])

  useEffect(() => {
    const onPointerDown = (event: PointerEvent) => {
      if (event.target instanceof Element) {
        if (!event.target.closest('.session-row-menu')) {
          setRowMenuId('')
        }
        if (!event.target.closest('.composer-picker-group') && !event.target.closest('.composer-perm-wrap')) {
          setComposerMenu(null)
        }
      }
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        setRowMenuId('')
        setComposerMenu(null)
      }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        setPaletteOpen((open) => !open)
      }
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'b' && !(event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement || (event.target instanceof Element && event.target.closest('.terminal-panel')))) {
        event.preventDefault()
        setHarnessOpen((open) => !open)
      }
      if ((event.ctrlKey || event.metaKey) && event.key === '`' && hasTerminalRef.current) {
        event.preventDefault()
        setHarnessTab('terminal')
        setHarnessOpen((open) => (harnessTabRef.current === 'terminal' ? !open : true))
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
  useEffect(() => {
    setSlashIndex(0)
    if (slashPopoverRef.current) slashPopoverRef.current.scrollTop = 0
  }, [slashMatches.length, draft])
  useEffect(() => {
    if (slashMatches.length === 0 || !slashScrollByKeyRef.current) return
    slashScrollByKeyRef.current = false
    keepOptionInView(slashPopoverRef.current, slashIndex)
  }, [slashIndex, slashMatches.length])
  const mentionWorkspaceKey = workspaceInfo?.path || managed?.workspace.path || (connection?.mode === 'local' ? connection.workspace : connection?.mode === 'remote' ? connection.url : '')
  useEffect(() => {
    mentionCacheRef.current = { key: mentionWorkspaceKey, items: [], loaded: false }
    setMentionItems([])
    setMentionLoading(false)
    setMentionIndex(0)
    setMentionDismissedAt(null)
  }, [client, mentionWorkspaceKey])
  const rawMentionMatch = useMemo(
    () => (has('workspace.files') && activeSession && !working && slashMatches.length === 0 ? matchMentionQuery(draft, cursorPos) : null),
    [capabilities, activeSession, working, slashMatches.length, draft, cursorPos],
  )
  useEffect(() => {
    if (!rawMentionMatch) setMentionDismissedAt(null)
  }, [rawMentionMatch])
  const activeMention = rawMentionMatch && rawMentionMatch.start !== mentionDismissedAt ? rawMentionMatch : null
  useEffect(() => {
    if ((!activeMention && !paletteOpen) || !client || !has('workspace.files') || mentionCacheRef.current.loaded) return
    const controller = new AbortController()
    const targetClient = client
    const cacheKey = mentionWorkspaceKey
    setMentionLoading(true)
    void indexWorkspaceFiles(targetClient, { signal: controller.signal })
      .then((items) => {
        if (controller.signal.aborted || clientRef.current !== targetClient) return
        mentionCacheRef.current = { key: cacheKey, items, loaded: true }
        setMentionItems(items)
      })
      .catch(() => undefined)
      .finally(() => {
        if (!controller.signal.aborted) setMentionLoading(false)
      })
    return () => controller.abort()
  }, [activeMention !== null, paletteOpen, client, mentionWorkspaceKey, capabilities])
  const mentionMatches = useMemo(
    () => (activeMention ? filterMentionItems(mentionItems, activeMention.query, 12) : []),
    [activeMention, mentionItems],
  )
  useEffect(() => {
    setMentionIndex(0)
    if (mentionPopoverRef.current) mentionPopoverRef.current.scrollTop = 0
  }, [activeMention?.query])
  useEffect(() => {
    if (!activeMention || mentionMatches.length === 0 || !mentionScrollByKeyRef.current) return
    mentionScrollByKeyRef.current = false
    keepOptionInView(mentionPopoverRef.current, mentionIndex)
  }, [mentionIndex, mentionMatches.length, activeMention])
  const displayedMessages = useMemo(() => [...history, ...optimistic.filter((message) => message.sessionId === activeSessionId)], [history, optimistic, activeSessionId])
  const selectedModel = activeSession?.settings.modelProfile || models[0]?.profile || ''
  const taskIsRunning = runtime.task.status === 'running'
  const taskIsQueued = runtime.task.status === 'queued'
  const taskIsPaused = runtime.task.status === 'paused'
  const taskHasQueuedWork = taskIsQueued || runtime.queue.some((item) => item.status === 'queued' || item.status === 'active')
  const sessionBusy = taskIsRunning || taskIsQueued || taskIsPaused || runtime.queue.length > 0

  useEffect(() => {
    setEditResendConfirmation(null)
    setSelectedAgentId('')
  }, [activeSessionId])
  const modelOption = models.find((option) => option.profile === selectedModel)
  const usageMetrics = useMemo(
    () => deriveSessionUsageMetrics({ session: activeSession, runtime, modelOption, liveContext: liveContextOverride }),
    [activeSession, runtime, modelOption, liveContextOverride],
  )

  const terminalTarget = useMemo<TerminalTarget | null>(() => {
    if (desktop && connection?.mode === 'local' && connection.workspace) {
      return { kind: 'desktop', cwd: connection.workspace }
    }
    if (!desktop && connection?.mode === 'loopback' && connection.url && connection.token && connection.url.endsWith('/runtime')) {
      return { kind: 'web', wsUrl: connection.url.replace(/\/runtime$/, '/terminal'), token: connection.token }
    }
    return null
  }, [desktop, connection])
  const hasTerminalRef = useRef(Boolean(terminalTarget))
  hasTerminalRef.current = Boolean(terminalTarget)
  const harnessTabRef = useRef(harnessTab)
  harnessTabRef.current = harnessTab

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
    const [saved, wsList] = await Promise.all([
      window.luneDesktop?.savedConnections(),
      window.luneDesktop?.listWorkspaces().catch(() => undefined),
    ])
    if (saved) { setSavedConnections(saved.items); setConnectionNotice(saved.notice) }
    if (Array.isArray(wsList)) setDesktopWorkspaces(wsList)
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
      const hello = await candidate.request<{ protocolVersion?: string; capabilities?: string[]; permissionMode?: PermissionMode }>('runtime.hello', {
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
      if (hello.permissionMode && PERMISSION_MODES.includes(hello.permissionMode)) {
        setPermissionMode(hello.permissionMode)
      }
      if (capabilitiesRef.current.includes('permission.manage')) {
        void candidate.request<{ mode?: PermissionMode; modes?: PermissionMode[] }>('permission.get').then((res) => {
          if (res.mode && PERMISSION_MODES.includes(res.mode)) setPermissionMode(res.mode)
          if (res.modes?.length) {
            const valid = res.modes.filter((m): m is PermissionMode => PERMISSION_MODES.includes(m))
            if (valid.length) setAllowedPermissionModes(valid)
          }
        }).catch(() => undefined)
      }
      clientRef.current = candidate
      const savedOptions = desktop && options.mode === 'remote' ? { ...options, token: '' } : options
      connectionRef.current = savedOptions
      setActiveSession(null)
      setClient(candidate)
      setConnection(savedOptions)
      setConnectionStatus('connected')
      setConfigReloadPending(false)
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
  const pendingDesktopSessionRef = useRef<{ workspace: string; sessionId: string } | null>(null)
  const pendingDesktopNewSessionRef = useRef('')
  const handledSessionReqSeqRef = useRef(0)
  const handledNewSessionTokenRef = useRef(0)
  const onRememberSessionRef = useRef(managed?.onRememberSession)
  onRememberSessionRef.current = managed?.onRememberSession
  const onSyncSessionsRef = useRef(managed?.onSyncSessions)
  onSyncSessionsRef.current = managed?.onSyncSessions
  const onReportSessionsRef = useRef(managed?.onReportSessions)
  onReportSessionsRef.current = managed?.onReportSessions
  const workspaceIdRef = useRef(workspaceId)
  workspaceIdRef.current = workspaceId

  useEffect(() => { activeSessionIdRef.current = activeSessionId }, [activeSessionId])
  useEffect(() => {
    if (!activeSessionId) {
      setReviewContext(null)
      setDiagnosisContext(null)
      return
    }
    try {
      const raw = localStorage.getItem(`lune:review:${activeSessionId}`)
      const parsed = raw ? JSON.parse(raw) as ReviewContext : null
      setReviewContext(parsed?.reviewSessionId === activeSessionId ? { ...parsed, stale: undefined } : null)
    } catch {
      setReviewContext(null)
    }
    try {
      const raw = localStorage.getItem(`lune:diagnosis:${activeSessionId}`)
      const parsed = raw ? JSON.parse(raw) as TaskDiagnosisContext : null
      setDiagnosisContext(parsed?.diagnosisSessionId === activeSessionId ? { ...parsed, stale: undefined } : null)
    } catch {
      setDiagnosisContext(null)
    }
  }, [activeSessionId])
  useEffect(() => {
    if (!workspaceId || !onReportSessionsRef.current) return
    const mapped: WorkspaceSessionRecord[] = sessions.map((s) => ({
      sessionId: s.id,
      workspaceId,
      title: s.title || '',
      archived: Boolean(s.archived),
      createdAt: Date.parse(s.updatedAt) || Date.now(),
      updatedAt: Date.parse(s.updatedAt) || Date.now(),
      lastOpenedAt: s.id === activeSessionId ? Date.now() : 0,
      ...(s.id === activeSessionId ? {
        taskStatus: runtime.task.status,
        executionId: runtime.task.executionId,
        queueCount: runtime.queue.length,
      } : {}),
    }))
    onReportSessionsRef.current(workspaceId, mapped)
  }, [sessions, workspaceId, activeSessionId, runtime.task.status, runtime.task.executionId, runtime.queue.length])
  useEffect(() => {
    if (!connection || !activeSession || activeSession.id !== activeSessionId) return
    if (connection.mode === 'loopback') {
      rememberLoopbackSession(workspaceId, activeSessionId)
      if (workspaceId) onRememberSessionRef.current?.(workspaceId, activeSessionId)
      return
    }
    if (!desktop && connection.mode === 'remote') {
      try { sessionStorage.setItem(webLastSessionKey(connection.url), activeSessionId) } catch { /* ignore */ }
      return
    }
    const address = connection.mode === 'local' ? connection.workspace : connection.url
    void window.luneDesktop?.rememberSession(connection.mode, address, activeSessionId)
      .then(() => { if (desktop) void reloadSavedConnections().catch(() => undefined) })
      .catch(() => undefined)
  }, [connection, activeSession, activeSessionId, desktop, workspaceId, reloadSavedConnections])

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
    sessionsRef.current = []
    setSessions([])
    modelsRef.current = []
    setModels([])
    activeSessionRef.current = null
    setActiveSession(null)
    setActiveSessionId('')
    setLiveContextOverride({ compacting: false })
    appliedUsageKeysRef.current = new Set()
    setTimeline([])
    setHistory([])
    setAttachments([])
    submissionRetry.current = null
    setPending([])
    setModelFailure(null)
    setRuntime(emptyRuntime)
    setCommands([])
    pendingClearLiveRef.current = false
    streamStore.clear()
    subagentStreamBufRef.current.clear()
    if (subagentStreamRafRef.current !== null) {
      window.cancelAnimationFrame(subagentStreamRafRef.current)
      subagentStreamRafRef.current = null
    }
    setLiveAgents([])
    setSelectedAgentId('')
    setLiveTools([])
    setEditingQueueExecId('')
    setQueueEditDraft('')
    setLatestExecutionId('')
    setSlashNotice('')
    setShowConnection(true)
    if (active) await active.close(true).catch(() => undefined)
    else if (window.luneDesktop) await window.luneDesktop.close(true).catch(() => undefined)
  }, [streamStore])

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
    lastRawEntryIdRef.current = entries.at(-1)?.id ?? ''
    const messages = timelineMessages(entries)
    const normalized = normalizeRuntimeState(nextRuntime)
    const taskDone = ['completed', 'failed', 'cancelled', 'discarded', 'idle'].includes(normalized.task.status)

    const capturedMap = readCapturedReasoning(sessionId)
    let capturedMapChanged = false
    if (pendingClearLiveRef.current || (taskDone && !streamingExecution.current)) {
      const capturedReasoning = streamStore.getTargetReasoning().trim()
      if (capturedReasoning) {
        const baselineIdx = turnBaselineEntryIdRef.current
          ? entries.findIndex((e) => e.id === turnBaselineEntryIdRef.current)
          : -1
        const newEntries = baselineIdx >= 0 ? entries.slice(baselineIdx + 1) : entries
        const anchor =
          [...newEntries].reverse().find((e) => e.kind === 'message' && e.role === 'assistant') ??
          [...newEntries].reverse().find((e) => e.kind === 'tool_round')
        if (anchor && !Object.hasOwn(capturedMap, anchor.id)) {
          const durationMs = reasoningStartTsRef.current ? Math.max(0, Date.now() - reasoningStartTsRef.current) : undefined
          capturedMap[anchor.id] = { text: capturedReasoning, ...(durationMs !== undefined ? { durationMs } : {}) }
          capturedMapChanged = true
        }
      }
      reasoningStartTsRef.current = 0
      pendingClearLiveRef.current = false
      streamStore.clear()
      setLiveAgents([])
      setLiveTools([])
    }
    if (entries.length > 0) {
      const validIds = new Set(entries.map((e) => e.id))
      for (const k of Object.keys(capturedMap)) {
        if (!validIds.has(k)) {
          delete capturedMap[k]
          capturedMapChanged = true
        }
      }
    }
    if (capturedMapChanged) {
      writeCapturedReasoning(sessionId, capturedMap)
    }
    const mergedEntries = mergeCapturedReasoning(entries, capturedMap)
    setTimeline(mergedEntries)
    activeSessionRef.current = summary
    setActiveSession(summary)
    appliedUsageKeysRef.current = mergeAppliedUsageKeys(appliedUsageKeysRef.current, summary)
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
    setRuntime(normalized)
    setCommands(nextCommands ?? [])
    if (normalized.task.executionId) setLatestExecutionId(normalized.task.executionId)
    setPending(nextPending ?? [])
    const recoveredFailure = (nextPending ?? []).find((item) => item.kind === 'model_recovery')?.modelRecovery?.failure
    if (recoveredFailure) setModelFailure(recoveredFailure)
    else if (taskDone) setModelFailure(null)
    setSessions((items) => {
      const next = items.map((item) => item.id === summary.id ? summary : item)
      sessionsRef.current = next
      return next
    })
    return messages
  }, [streamStore])

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
      sessionsRef.current = nextSessions ?? []
      setSessions(sessionsRef.current)
      modelsRef.current = nextModels ?? []
      setModels(modelsRef.current)
      const requestedManaged = managed?.requestedSession
      if (requestedManaged) handledSessionReqSeqRef.current = requestedManaged.seq
      const newTokenManaged = managed?.newSessionToken ?? 0
      if (newTokenManaged > 0) handledNewSessionTokenRef.current = newTokenManaged

      const connNow = connectionRef.current
      const forceNewDesktop = Boolean(
        window.luneDesktop && connNow?.mode === 'local' && pendingDesktopNewSessionRef.current === connNow.workspace,
      )
      if (forceNewDesktop) pendingDesktopNewSessionRef.current = ''
      const forceNew = forceNewDesktop || newTokenManaged > 0

      let linkedSession: SessionSummary | undefined
      if (linkedSessionIdRef.current) {
        linkedSession = await client.request<SessionSummary>(
          'session.get',
          { sessionId: linkedSessionIdRef.current },
        ).catch(() => undefined)
        if (cancelled) return
        if (linkedSession) {
          linkedSessionIdRef.current = ''
          const target = new URL(window.location.href)
          target.searchParams.delete('sessionId')
          window.history.replaceState(window.history.state, '', target)
        } else {
          setActionError(t('Linked session was not found in this workspace'))
        }
      }

      let pendingDesktopSessionId: string | null = null
      if (window.luneDesktop && connNow?.mode === 'local' && pendingDesktopSessionRef.current?.workspace === connNow.workspace) {
        pendingDesktopSessionId = pendingDesktopSessionRef.current.sessionId
        pendingDesktopSessionRef.current = null
      }

      const previousID = linkedSession?.id ?? (forceNew
        ? null
        : window.luneDesktop
          ? (pendingDesktopSessionId ?? await window.luneDesktop.lastSession().catch(() => null))
          : connNow?.mode === 'loopback'
            ? (requestedManaged?.sessionId || managed?.workspace.lastSessionId || lastLoopbackSession(workspaceId))
            : connNow?.mode === 'remote'
              ? (() => { try { return sessionStorage.getItem(webLastSessionKey(connNow.url)) } catch { return null } })()
              : activeSessionIdRef.current)
      if (cancelled) return
      const existing = linkedSession ?? (forceNew
        ? undefined
        : ((previousID ? await client.request<SessionSummary>('session.get', { sessionId: previousID }).catch(() => undefined) : undefined) ?? nextSessions?.[0]))
      if (cancelled) return
      if (existing) {
        activeSessionRef.current = existing
        setActiveSession(existing)
        setActiveSessionId(existing.id)
      } else {
        const created = await createSessionWithDefaults(client, nextModels ?? [])
        if (!cancelled) {
          activeSessionRef.current = created
          setActiveSession(created)
          const next = [created, ...(nextSessions ?? []).filter((item) => item.id !== created.id)]
          sessionsRef.current = next
          setSessions(next)
          setActiveSessionId(created.id)
          if (workspaceId) onSyncSessionsRef.current?.(workspaceId)
        }
      }
      setShowConnection(false)
    }).catch((error: unknown) => {
      if (!cancelled) setActionError(errorMessage(error))
    })
    return () => { cancelled = true }
  }, [client, workspaceId, createSessionWithDefaults, t])

  useEffect(() => {
    const req = managed?.requestedSession
    if (!req || !client || initializedClient.current !== client || req.seq <= handledSessionReqSeqRef.current) return
    handledSessionReqSeqRef.current = req.seq
    setActiveSessionId(req.sessionId)
  }, [managed?.requestedSession, client])

  useEffect(() => {
    if (!client || !activeSessionId) return
    let cancelled = false
    let subscriptionId = ''
    const targetClient = client
    const sessionId = activeSessionId
    const cachedSession =
      sessionsRef.current.find((item) => item.id === sessionId) ??
      (activeSessionRef.current?.id === sessionId ? activeSessionRef.current : null)
    activeSessionRef.current = cachedSession
    setActiveSession(cachedSession)
    setLiveContextOverride({ compacting: false })
    appliedUsageKeysRef.current = cachedSession ? mergeAppliedUsageKeys(new Set(), cachedSession) : new Set()
    setTimeline([])
    setHistory([])
    streamingExecution.current = undefined
    pendingClearLiveRef.current = false
    setPending([])
    setModelFailure(null)
    setRuntime(emptyRuntime)
    setCommands([])
    streamStore.clear()
    subagentStreamBufRef.current.clear()
    if (subagentStreamRafRef.current !== null) {
      window.cancelAnimationFrame(subagentStreamRafRef.current)
      subagentStreamRafRef.current = null
    }
    setLiveAgents([])
    setSelectedAgentId('')
    setLiveTools([])
    setEditingQueueExecId('')
    setQueueEditDraft('')
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
        pendingClearLiveRef.current = false
        reasoningStartTsRef.current = 0
        turnBaselineEntryIdRef.current = lastRawEntryIdRef.current
        if (event.executionId) setLatestExecutionId(event.executionId)
        streamStore.clear()
        subagentStreamBufRef.current.clear()
        setLiveAgents([])
        setLiveTools([])
        setRuntime((state) => ({ ...state, task: { ...state.task, status: 'running', executionId: event.executionId } }))
        setActivity((items) => [{ id: `${event.taskId}-started`, label: 'Working on your request', detail: event.executionId }, ...items].slice(0, 5))
      } else if (event.kind === 'task_finished' || event.kind === 'task_discarded') {
        flushSubagentStreams()
        if (!event.taskId && event.status === 'cancelled' && event.executionId) {
          setOptimistic((items) => items.filter((item) => item.executionId !== event.executionId))
        }
        if (event.executionId) setLatestExecutionId(event.executionId)
        if (event.executionId === streamingExecution.current) {
          streamingExecution.current = undefined
          streamStore.flushImmediate()
          pendingClearLiveRef.current = true
        }
        const finishedAt = Date.parse(event.occurredAt || '') || Date.now()
        setLiveAgents((agents) => agents.map((a) =>
          a.status === 'running'
            ? {
                ...a,
                status: event.status === 'cancelled' ? 'cancelled' : event.status === 'failed' ? 'failed' : 'completed',
                activity: event.status === 'cancelled' ? 'Cancelled' : event.status === 'failed' ? 'Failed' : 'Completed',
                currentTool: undefined,
                durationMs: a.durationMs ?? Math.max(0, finishedAt - a.startedAt),
              }
            : a,
        ))
        setActivity((items) => [{
          id: `${event.executionId}-${event.kind}`,
          label: event.kind === 'task_discarded' ? 'Run discarded' : `Run ${event.status ?? 'finished'}`,
          detail: event.error,
          failed: ['failed', 'cancelled'].includes(event.status ?? ''),
        }, ...items].slice(0, 5))
        setLiveContextOverride((prev) => (prev.compacting ? { ...prev, compacting: false } : prev))
        void refreshAll()
        if (workspaceIdRef.current) onSyncSessionsRef.current?.(workspaceIdRef.current)
      } else if (event.kind === 'agent_started') {
        const agentKey = event.agentId || event.agentName || event.taskId || `agent-${Date.now()}`
        const name = event.agentName || event.agentRole || 'Subagent'
        const startedAt = Date.parse(event.occurredAt || '') || Date.now()
        setLiveAgents((agents) => [
          ...agents.filter((a) => a.id !== agentKey),
          {
            id: agentKey,
            name,
            role: event.agentRole,
            task: event.agentTask,
            teamRunId: event.teamRunId,
            status: 'running',
            activity: 'Starting',
            toolCount: 0,
            tools: [],
            startedAt,
            provider: event.provider,
            modelProfile: event.modelProfile,
            model: event.model,
            contextWindow: event.contextWindow,
            reasoningText: '',
            outputText: '',
          },
        ])
        setActivity((items) => [{
          id: `${agentKey}-started-${startedAt}`,
          label: `Subagent ${name} started`,
          detail: event.agentTask,
        }, ...items].slice(0, 5))
      } else if (event.kind === 'agent_finished') {
        flushSubagentStreams()
        const agentKey = event.agentId || event.agentName || event.taskId || ''
        const cancelledStatus = event.status === 'cancelled'
        const failed = Boolean(event.error) || event.status === 'failed' || cancelledStatus
        const finishedAt = Date.parse(event.occurredAt || '') || Date.now()
        setLiveAgents((agents) => agents.map((a) =>
          (agentKey && a.id === agentKey) || (event.agentName && a.name === event.agentName)
            ? {
                ...a,
                status: cancelledStatus ? 'cancelled' : failed ? 'failed' : 'completed',
                activity: cancelledStatus ? 'Cancelled' : failed ? 'Failed' : 'Completed',
                currentTool: undefined,
                durationMs: Math.max(0, finishedAt - a.startedAt),
                error: event.error,
                provider: event.provider || a.provider,
                modelProfile: event.modelProfile || a.modelProfile,
                model: event.model || a.model,
                contextWindow: event.contextWindow || a.contextWindow,
              }
            : a,
        ))
        setActivity((items) => [{
          id: `${agentKey || 'agent'}-finished-${finishedAt}`,
          label: `Subagent ${event.agentName || 'worker'} ${failed ? 'failed' : 'finished'}`,
          detail: event.error,
          failed,
        }, ...items].slice(0, 5))
      } else if (event.kind === 'usage') {
        const dedupKey = usageEventDedupKey(event)
        if (dedupKey && appliedUsageKeysRef.current.has(dedupKey)) return
        if (dedupKey) appliedUsageKeysRef.current.add(dedupKey)
        if (isLeadMainUsage(event.agentId, event.usagePurpose)) {
          setLiveContextOverride((prev) => (prev.afterTokens !== undefined ? { ...prev, afterTokens: undefined } : prev))
        }
        setActiveSession((prev) => {
          if (!prev || prev.id !== sessionId) return prev
          const updated = applyUsageEvent(prev, event)
          activeSessionRef.current = updated
          return updated
        })
      } else if (event.kind === 'model_retry' || event.kind === 'model_recovery_required') {
        if (event.modelFailure) setModelFailure(event.modelFailure)
        if (event.kind === 'model_recovery_required') void refreshLiveState()
      } else if (event.kind === 'model_recovery_resumed') {
        setModelFailure(null)
      } else if (event.kind === 'context_compaction') {
        const stage = event.compaction?.stage
        const afterTokens = event.compaction?.afterTokens
        if (stage === 'started' || stage === 'running') {
          setLiveContextOverride((prev) => ({ ...prev, compacting: true }))
        } else if (stage) {
          setLiveContextOverride((prev) => ({
            compacting: false,
            afterTokens: typeof afterTokens === 'number' && afterTokens > 0 ? afterTokens : prev.afterTokens,
          }))
        } else {
          setLiveContextOverride((prev) => (prev.compacting ? { ...prev, compacting: false } : prev))
        }
        void refreshAll()
      } else if (event.kind === 'task_queued' || event.kind === 'goal_updated' || event.kind === 'goal_cleared' || event.kind === 'command_started' || event.kind === 'command_finished') {
        void refreshLiveState()
      } else if (event.kind === 'model_stream' && event.agentId && event.agentId !== 'lead' && (event.stream?.type === 'text_delta' || event.stream?.type === 'reasoning_delta')) {
        const deltaText = event.stream?.delta ?? ''
        if (deltaText) {
          const buf = subagentStreamBufRef.current
          const existing = buf.get(event.agentId) ?? { reasoning: '', text: '' }
          if (event.stream.type === 'reasoning_delta') {
            existing.reasoning += deltaText
            existing.activity = 'Thinking'
          } else {
            existing.text += deltaText
            existing.activity = 'Generating response'
          }
          if (event.agentName) existing.agentName = event.agentName
          if (event.modelProfile) existing.modelProfile = event.modelProfile
          if (event.provider) existing.provider = event.provider
          if (event.model) existing.model = event.model
          buf.set(event.agentId, existing)
          if (subagentStreamRafRef.current === null) {
            subagentStreamRafRef.current = window.requestAnimationFrame(() => flushSubagentStreams())
          }
        }
      } else if (event.kind === 'model_stream' && event.stream?.type === 'text_delta' && (event.stream.purpose === 'main' || !event.stream.purpose)) {
        streamStore.appendText(event.stream?.delta ?? '')
      } else if (event.kind === 'model_stream' && event.stream?.type === 'reasoning_delta' && (event.stream.purpose === 'main' || !event.stream.purpose)) {
        if (!reasoningStartTsRef.current) reasoningStartTsRef.current = Date.now()
        streamStore.appendReasoning(event.stream?.delta ?? '')
      } else if (event.kind === 'tool_started' || event.kind === 'tool_finished') {
        const name = event.tool?.name ?? 'tool'
        const stepId = event.tool?.invocationId || event.tool?.id || `${event.taskId}-${name}`
        if (event.kind === 'tool_started') {
          streamStore.appendReasoningBreak()
          setLiveTools((steps) => [...steps.filter((s) => s.id !== stepId), { id: stepId, name, status: 'running' as const }].slice(-8))
          if (event.tool?.agentId && event.tool.agentId !== 'lead') {
            setLiveAgents((agents) => agents.map((a) => {
              if (a.id !== event.tool?.agentId) return a
              const prevTools = a.tools ?? []
              const nextTools = [...prevTools.filter((s) => s.id !== stepId), { id: stepId, name, status: 'running' as const }].slice(-40)
              return { ...a, currentTool: name, toolCount: a.toolCount + 1, activity: `Running ${name}`, tools: nextTools }
            }))
          }
        } else {
          const durationMs = event.tool?.duration ? Math.round(event.tool.duration / 1_000_000) : undefined
          setLiveTools((steps) => steps.map((s) => s.id === stepId ? { ...s, status: event.tool?.failed ? 'failed' : 'completed', durationMs } : s))
          if (event.tool?.agentId && event.tool.agentId !== 'lead') {
            setLiveAgents((agents) => agents.map((a) => {
              if (a.id !== event.tool?.agentId) return a
              const prevTools = a.tools ?? []
              const nextTools = prevTools.map((s) => s.id === stepId ? { ...s, status: (event.tool?.failed ? 'failed' : 'completed') as 'failed' | 'completed', durationMs } : s)
              return {
                ...a,
                currentTool: a.currentTool === name ? undefined : a.currentTool,
                activity: event.tool?.failed ? `Failed ${name}` : `Finished ${name}`,
                tools: nextTools,
              }
            }))
          }
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

  const stopScrollRaf = useCallback(() => {
    if (scrollRafRef.current !== null) {
      window.cancelAnimationFrame(scrollRafRef.current)
      scrollRafRef.current = null
    }
    scrollLastTsRef.current = 0
    virtualScrollTopRef.current = null
  }, [])

  useEffect(() => stopScrollRaf, [stopScrollRaf])

  const scheduleScrollCatchup = useCallback((instant = false) => {
    if (!stickToBottomRef.current) return
    const el = messagesScrollRef.current
    if (!el) return
    if (performance.now() < smoothButtonUntilRef.current) return

    const reducedMotion = Boolean(window.matchMedia?.('(prefers-reduced-motion: reduce)').matches)
    if (instant || reducedMotion) {
      stopScrollRaf()
      const maxTop = Math.max(0, el.scrollHeight - el.clientHeight)
      el.scrollTop = maxTop
      return
    }

    if (scrollRafRef.current !== null) return

    const step = (ts: number) => {
      scrollRafRef.current = null
      const scroller = messagesScrollRef.current
      if (!scroller || !stickToBottomRef.current || performance.now() < smoothButtonUntilRef.current) {
        scrollLastTsRef.current = 0
        virtualScrollTopRef.current = null
        return
      }

      // Unified Read phase
      const maxTop = Math.max(0, scroller.scrollHeight - scroller.clientHeight)
      const domTop = scroller.scrollTop
      const cur = virtualScrollTopRef.current !== null && Math.abs(virtualScrollTopRef.current - domTop) <= 2
        ? virtualScrollTopRef.current
        : domTop
      const gap = maxTop - cur

      if (gap <= 0.5) {
        if (gap > 0) scroller.scrollTop = maxTop
        scrollLastTsRef.current = 0
        virtualScrollTopRef.current = null
        return
      }

      if (gap > 640) {
        scroller.scrollTop = maxTop
        scrollLastTsRef.current = 0
        virtualScrollTopRef.current = null
        return
      }

      const dt = scrollLastTsRef.current ? Math.min(0.05, Math.max(0.001, (ts - scrollLastTsRef.current) / 1000)) : 1 / 120
      scrollLastTsRef.current = ts
      const alpha = 1 - Math.exp(-18 * dt)
      const next = Math.min(maxTop, cur + Math.max(0.5, gap * alpha))

      // Unified Write phase
      virtualScrollTopRef.current = next
      scroller.scrollTop = next
      scrollRafRef.current = window.requestAnimationFrame(step)
    }

    scrollRafRef.current = window.requestAnimationFrame(step)
  }, [stopScrollRaf])

  const syncActiveTurn = useCallback(() => {
    if (activeTurnRafRef.current !== null) return
    activeTurnRafRef.current = window.requestAnimationFrame(() => {
      activeTurnRafRef.current = null
      const scroller = messagesScrollRef.current
      if (!scroller) return
      const nodes = scroller.querySelectorAll<HTMLElement>('article.message.user[data-turn-id]')
      if (nodes.length === 0) return

      let nextId = nodes[0].dataset.turnId ?? ''
      const nearBottom = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight <= 24
      if (nearBottom) {
        nextId = nodes[nodes.length - 1].dataset.turnId ?? nextId
      } else {
        const scrollerRect = scroller.getBoundingClientRect()
        const threshold = scroller.scrollTop + Math.min(180, scroller.clientHeight * 0.3)
        for (const node of nodes) {
          const offsetTop = node.getBoundingClientRect().top - scrollerRect.top + scroller.scrollTop
          if (offsetTop <= threshold) {
            nextId = node.dataset.turnId ?? nextId
          } else {
            break
          }
        }
      }
      if (nextId && nextId !== activeTurnIdRef.current) {
        activeTurnIdRef.current = nextId
        setActiveTurnId(nextId)
      }
    })
  }, [])

  const jumpToTurn = useCallback((turnId: string) => {
    const scroller = messagesScrollRef.current
    if (!scroller) return
    stickToBottomRef.current = false
    smoothButtonUntilRef.current = 0
    stopScrollRaf()
    const target = scroller.querySelector<HTMLElement>(`article.message.user[data-turn-id="${CSS.escape(turnId)}"]`)
    if (!target) return
    const top = Math.max(0, target.getBoundingClientRect().top - scroller.getBoundingClientRect().top + scroller.scrollTop - 16)
    scroller.scrollTo({ top, behavior: 'smooth' })
    activeTurnIdRef.current = turnId
    setActiveTurnId(turnId)
    scroller.querySelectorAll('.turn-jump-flash').forEach((el) => el.classList.remove('turn-jump-flash'))
    target.classList.add('turn-jump-flash')
    if (jumpFlashTimerRef.current !== null) window.clearTimeout(jumpFlashTimerRef.current)
    jumpFlashTimerRef.current = window.setTimeout(() => {
      target.classList.remove('turn-jump-flash')
      jumpFlashTimerRef.current = null
    }, 800)
    if (scroller.scrollHeight > scroller.clientHeight + 60) {
      setShowScrollBottom(true)
    }
  }, [stopScrollRaf])

  useEffect(() => () => {
    if (activeTurnRafRef.current !== null) window.cancelAnimationFrame(activeTurnRafRef.current)
    if (jumpFlashTimerRef.current !== null) window.clearTimeout(jumpFlashTimerRef.current)
  }, [])

  const handleMessagesScroll = useCallback(() => {
    const el = messagesScrollRef.current
    if (!el) return
    const distance = el.scrollHeight - el.scrollTop - el.clientHeight
    if (distance <= 48) {
      stickToBottomRef.current = true
      smoothButtonUntilRef.current = 0
      setShowScrollBottom(false)
    } else if (!stickToBottomRef.current) {
      setShowScrollBottom(el.scrollHeight > el.clientHeight + 60)
    }
    syncActiveTurn()
  }, [syncActiveTurn])

  const scrollToBottom = useCallback((smooth = true) => {
    stickToBottomRef.current = true
    setShowScrollBottom(false)
    const el = messagesScrollRef.current
    if (el) {
      stopScrollRaf()
      if (smooth) {
        smoothButtonUntilRef.current = performance.now() + 450
        el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' })
      } else {
        smoothButtonUntilRef.current = 0
        el.scrollTop = Math.max(0, el.scrollHeight - el.clientHeight)
      }
    } else {
      bottomRef.current?.scrollIntoView({ behavior: smooth ? 'smooth' : 'auto', block: 'end' })
    }
  }, [stopScrollRaf])

  useEffect(() => {
    stickToBottomRef.current = true
    initialScrollSnapRef.current = true
    smoothButtonUntilRef.current = 0
    stopScrollRaf()
    setShowScrollBottom(false)
    activeTurnIdRef.current = ''
    setActiveTurnId('')
  }, [activeSessionId, stopScrollRaf])

  useEffect(() => {
    if (!stickToBottomRef.current) return
    scheduleScrollCatchup(false)
  }, [timeline.length, optimistic.length, liveTools.length, liveAgents.length, pending.length, scheduleScrollCatchup])

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
      const created = await createSessionWithDefaults(client, modelsRef.current)
      if (clientRef.current !== client) return
      setSearch('')
      activeSessionRef.current = created
      setActiveSession(created)
      setSessions((items) => {
        const next = [created, ...items]
        sessionsRef.current = next
        return next
      })
      setActiveSessionId(created.id)
      setListRevision((revision) => revision + 1)
      if (workspaceIdRef.current) onSyncSessionsRef.current?.(workspaceIdRef.current)
      if (desktop) void reloadSavedConnections().catch(() => undefined)
      setHistory([])
      setTimeline([])
      setOptimistic([])
      setActivity([])
      setLiveAgents([])
      setLiveTools([])
      setEditingQueueExecId('')
      setQueueEditDraft('')
      pendingClearLiveRef.current = false
      streamStore.clear()
      setLatestExecutionId('')
      setSlashNotice('')
      setRowMenuId('')
      setCommands([])
      closeDrawer()
    } catch (error) { setActionError(errorMessage(error)) }
    finally { setWorking(false) }
  }

  const newSessionToken = managed?.newSessionToken ?? 0
  useEffect(() => {
    if (!newSessionToken || !client || initializedClient.current !== client || newSessionToken <= handledNewSessionTokenRef.current) return
    handledNewSessionTokenRef.current = newSessionToken
    void createSession()
  })

  const sendMessage = async (steer = false, options?: { textOverride?: string; forcePlanMode?: boolean; restoreDraftOnError?: string }) => {
    let text = (options?.textOverride !== undefined ? options.textOverride : draft).trim()
    let forcePlan = Boolean(options?.forcePlanMode)
    let steerMode = steer
    let restoreDraft = options?.restoreDraftOnError
    if (!client || activeSession?.id !== activeSessionId || !activeSessionId || (!text && !attachments.length) || working || connectionStatus !== 'connected') return
    if (options?.textOverride === undefined) {
      const parsed = parseSlashCommand(text)
      if (parsed?.command.name === '/plan' && (parsed.args || attachments.length)) {
        if (!has('turn.plan')) {
          setActionError('Current Core does not support /plan (turn.plan).')
          return
        }
        restoreDraft = draft
        text = parsed.args
        forcePlan = true
        steerMode = false
      } else if (parsed && !attachments.length) {
        await executeSlashCommand(text)
        return
      }
    }
    if (configReloadPending) {
      setActionError('Settings are being applied after the current work finishes. New turns are paused briefly.')
      return
    }
    if (attachments.length && !has('turn.attachments')) {
      setActionError('This Core does not support attachments. Remove them or connect to an updated Core.')
      return
    }
    try { validateAttachments(attachments, Boolean(modelOption?.supportsInputImages)) } catch (error) { setActionError(errorMessage(error)); return }
    const plan = has('turn.plan') && (forcePlan || planMode) && !steerMode
    const requestedMode = steerMode ? 'steer_input' : taskIsRunning || taskIsQueued || taskIsPaused || runtime.queue.length > 0 ? 'queue_input' : 'start_if_idle'
    const key = JSON.stringify([activeSessionId, text, attachments, plan, steerMode])
    const existing = submissionRetry.current
    const canRetry = existing?.key === key
    const submissionId = canRetry ? existing.id : crypto.randomUUID()
    const submissionMode = canRetry ? existing.mode : requestedMode
    submissionRetry.current = { key, id: submissionId, mode: submissionMode }
    stickToBottomRef.current = true
    smoothButtonUntilRef.current = 0
    setShowScrollBottom(false)
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
      const currentSession = activeSessionRef.current ?? activeSession
      if (currentSession && !currentSession.title && !history.some((message) => message.role === 'user')) {
        const title = Array.from(text.replace(/\s+/g, ' ').trim()).slice(0, 64).join('')
        if (title) {
          try {
            await client.request('session.setTitle', { sessionId: activeSessionId, title })
            setActiveSession((prev) => {
              if (!prev || prev.id !== activeSessionId) return prev
              const updated = { ...prev, title }
              activeSessionRef.current = updated
              return updated
            })
            setSessions((items) => {
              const next = items.map((item) => item.id === activeSessionId ? { ...item, title } : item)
              sessionsRef.current = next
              return next
            })
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
      setDraft(restoreDraft ?? text)
      if (/session_busy|task_paused/.test(errorMessage(error))) submissionRetry.current = null
      setActionError(errorMessage(error))
    } finally { setWorking(false) }
  }

  const rewindAndSubmit = async (turns: number, nextText: string, historyAttachments: HistoryAttachment[] = [], confirmBusyWork = false) => {
    const trimmed = nextText.trim()
    const targetSessionId = activeSessionId
    if (!client || !activeSession?.id || !targetSessionId || working || connectionStatus !== 'connected' || !has('session.rewind')) return
    if (!has('turn.replaceHistory')) {
      setActionError('This Core cannot atomically edit and resend a message. Update Core to continue.')
      return
    }
    if (configReloadPending) {
      setActionError('Settings are being applied after the current work finishes. Editing and resending is paused briefly.')
      return
    }
    let nextAttachments: Attachment[]
    try {
      nextAttachments = restoreHistoryAttachments(historyAttachments)
      if (!trimmed && nextAttachments.length === 0) return
      if (nextAttachments.length > 0 && !has('turn.attachments')) {
        throw new Error('This Core cannot resend the attachments from this message.')
      }
      validateAttachments(nextAttachments, Boolean(modelOption?.supportsInputImages))
    } catch (error) {
      setActionError(errorMessage(error))
      return
    }
    stickToBottomRef.current = true
    smoothButtonUntilRef.current = 0
    setShowScrollBottom(false)
    setWorking(true)
    setActionError('')
    let submittedOptimisticId = ''
    try {
      const plan = has('turn.plan') && planMode
      const retryKey = JSON.stringify([targetSessionId, turns, trimmed, nextAttachments, plan])
      const previousRetry = editResendRetry.current
      const retrySubmissionId = previousRetry?.key === retryKey ? previousRetry.id : ''
      const submitReplacement = (submissionId: string, historyDigest: string) => client.request<{ executionId: string }>('turn.replaceHistory', {
        sessionId: targetSessionId,
        submissionId,
        turns,
        historyDigest,
        input: trimmed,
        mode: 'start_if_idle',
        ...(plan ? { planMode: true } : {}),
        ...(nextAttachments.length ? { attachments: nextAttachments.map(({ name, mediaType, data }) => ({ name, mediaType, data })) } : {}),
      })
      if (retrySubmissionId && previousRetry) {
        submittedOptimisticId = retrySubmissionId
        setOptimistic([{
          id: retrySubmissionId,
          role: 'user',
          text: trimmed,
          baselineCount: previousRetry.baselineCount,
          sessionId: targetSessionId,
          attachments: nextAttachments.map((item, index) => ({ ...historyAttachments[index], name: item.name })),
        }])
        const submitted = await submitReplacement(retrySubmissionId, previousRetry.historyDigest)
        setOptimistic((items) => items.map((item) => item.id === retrySubmissionId ? { ...item, executionId: submitted.executionId } : item))
        if (editResendRetry.current?.id === retrySubmissionId) editResendRetry.current = null
        void refreshRuntime(client, targetSessionId).catch((error) => setActionError(errorMessage(error)))
        return
      }
      let currentRuntime = normalizeRuntimeState(await client.request<RuntimeState>('runtime.state', { sessionId: targetSessionId }))
      if (runtimeHasPendingWork(currentRuntime)) {
        if (!confirmBusyWork) {
          setEditResendConfirmation({ sessionId: targetSessionId, turns, text: trimmed, attachments: historyAttachments })
          return
        }

        if (runtimeHasPausedWork(currentRuntime)) {
          for (const turn of currentRuntime.queue) {
            if (turn.status !== 'queued') continue
            if (!has('turn.cancelQueued')) throw new Error('This Core cannot cancel queued turns before editing this conversation.')
            await client.request('turn.cancelQueued', { sessionId: targetSessionId, executionId: turn.executionId })
          }
          await client.request('turn.discardPaused', { sessionId: targetSessionId })
          currentRuntime = normalizeRuntimeState(await client.request<RuntimeState>('runtime.state', { sessionId: targetSessionId }))
        }

        if (runtimeHasPendingWork(currentRuntime)) {
          if (!has('turn.cancelPending')) throw new Error('This Core cannot stop active or queued work before editing this conversation.')
          await client.request('turn.cancelPending', { sessionId: targetSessionId })
        }
        currentRuntime = normalizeRuntimeState(await client.request<RuntimeState>('runtime.state', { sessionId: targetSessionId }))
        if (runtimeHasPendingWork(currentRuntime)) throw new Error('The current task is still active. Wait for it to stop, then retry the edit.')

        streamingExecution.current = undefined
        pendingClearLiveRef.current = false
        streamStore.clear()
        subagentStreamBufRef.current.clear()
        if (subagentStreamRafRef.current !== null) {
          window.cancelAnimationFrame(subagentStreamRafRef.current)
          subagentStreamRafRef.current = null
        }
        setLiveAgents([])
        setSelectedAgentId('')
        setLiveTools([])
        setOptimistic((items) => items.filter((item) => item.sessionId !== targetSessionId))
        setActivity([])
        setLatestExecutionId('')
        setEditingQueueExecId('')
        setQueueEditDraft('')
      }

      const preview = await client.request<{ removed: number; digest: string }>('session.rewindPreview', { sessionId: targetSessionId, turns })
      if (!preview.digest || preview.removed === 0) throw new Error('Nothing to rewind.')
      streamingExecution.current = undefined
      pendingClearLiveRef.current = false
      streamStore.clear()
      subagentStreamBufRef.current.clear()
      if (subagentStreamRafRef.current !== null) {
        window.cancelAnimationFrame(subagentStreamRafRef.current)
        subagentStreamRafRef.current = null
      }
      setLiveAgents([])
      setLiveTools([])
      setOptimistic([])
      const attachmentNames = nextAttachments.map((item) => item.name)
      const identity = JSON.stringify([trimmed, attachmentNames])
      const userPositions = history.flatMap((message, index) => message.role === 'user' ? [index] : [])
      const retainedHistory = preview.removed >= userPositions.length
        ? []
        : history.slice(0, userPositions[userPositions.length - preview.removed])
      const baselineCount = retainedHistory.filter((message) => message.role === 'user' && messageIdentity(message) === identity).length
      const submissionId = crypto.randomUUID()
      editResendRetry.current = { key: retryKey, id: submissionId, historyDigest: preview.digest, baselineCount }
      submittedOptimisticId = submissionId
      setOptimistic([{
        id: submissionId,
        role: 'user',
        text: trimmed,
        baselineCount,
        sessionId: targetSessionId,
        attachments: nextAttachments.map((item, index) => ({ ...historyAttachments[index], name: item.name })),
      }])
      const submitted = await submitReplacement(submissionId, preview.digest)
      setOptimistic((items) => items.map((item) => item.id === submissionId ? { ...item, executionId: submitted.executionId } : item))
      if (editResendRetry.current?.id === submissionId) editResendRetry.current = null
      void refreshRuntime(client, targetSessionId).catch((error) => setActionError(errorMessage(error)))
    } catch (error) {
      if (submittedOptimisticId) setOptimistic((items) => items.filter((item) => item.id !== submittedOptimisticId))
      if (/history changed|preview the edit again|there are no user turns|session_busy|task_paused|session already has active/i.test(errorMessage(error))) editResendRetry.current = null
      void refreshSessionData(client, targetSessionId).catch(() => undefined)
      void refreshRuntime(client, targetSessionId).catch(() => undefined)
      setActionError(errorMessage(error))
    } finally {
      setWorking(false)
    }
  }

  const confirmEditAndResend = () => {
    const pendingEdit = editResendConfirmation
    setEditResendConfirmation(null)
    if (!pendingEdit) return
    if (pendingEdit.sessionId !== activeSessionId) {
      setActionError('The conversation changed before the edit was confirmed. Please open the message again.')
      return
    }
    void rewindAndSubmit(pendingEdit.turns, pendingEdit.text, pendingEdit.attachments, true)
  }

  const regenerateLastResponse = async () => {
    const lastUser = [...timeline].reverse().find((entry): entry is Extract<TimelineEntry, { kind: 'message' }> => entry.kind === 'message' && entry.role === 'user')
    if (!lastUser) return
    if (!lastUser.text.trim()) {
      setActionError('Cannot regenerate an attachment-only message.')
      return
    }
    await rewindAndSubmit(1, lastUser.text, lastUser.attachments)
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
    const currentId = activeSessionIdRef.current || activeSessionId
    const latestSession =
      activeSessionRef.current?.id === currentId
        ? activeSessionRef.current
        : (activeSession?.id === currentId
            ? activeSession
            : (sessionsRef.current.find((item) => item.id === currentId) ?? null))
    if (!client || !currentId || !latestSession) return
    const targetSessionId = currentId
    const previousSettings = latestSession.settings
    const nextSettings = { ...previousSettings, ...settings }
    const optimisticSession: SessionSummary = { ...latestSession, settings: nextSettings }
    activeSessionRef.current = optimisticSession
    setActiveSession((prev) => (prev && prev.id === targetSessionId ? optimisticSession : prev))
    setSessions((items) => {
      const next = items.map((item) => (item.id === targetSessionId ? { ...item, settings: nextSettings } : item))
      sessionsRef.current = next
      return next
    })
    try {
      await client.request('session.setSettings', { sessionId: targetSessionId, settings: nextSettings })
      const defaultsPatch: { modelProfile?: string; reasoningEffort?: string } = {}
      if (Object.prototype.hasOwnProperty.call(settings, 'modelProfile') && settings.modelProfile !== undefined) {
        defaultsPatch.modelProfile = settings.modelProfile
      }
      if (Object.prototype.hasOwnProperty.call(settings, 'reasoningEffort') && settings.reasoningEffort !== undefined) {
        defaultsPatch.reasoningEffort = settings.reasoningEffort
      }
      if (Object.keys(defaultsPatch).length > 0) {
        saveLastSessionSettings(currentDefaultsScopeKey(), defaultsPatch)
      }
    } catch (error) {
      if (activeSessionRef.current?.id === targetSessionId && activeSessionRef.current.settings === nextSettings) {
        activeSessionRef.current = { ...activeSessionRef.current, settings: previousSettings }
      }
      setActiveSession((prev) => (prev && prev.id === targetSessionId && prev.settings === nextSettings ? { ...prev, settings: previousSettings } : prev))
      setSessions((items) => {
        const next = items.map((item) => (item.id === targetSessionId && item.settings === nextSettings ? { ...item, settings: previousSettings } : item))
        sessionsRef.current = next
        return next
      })
      setActionError(errorMessage(error))
    }
  }
  const changePermissionMode = async (mode: PermissionMode) => {
    if (!client) return
    try {
      const res = await client.request<{ mode?: PermissionMode }>('permission.set', { mode })
      const applied = res.mode && PERMISSION_MODES.includes(res.mode) ? res.mode : mode
      setPermissionMode(applied)
      const persistPath = managed?.workspace.path || (connection?.mode === 'local' ? connection.workspace : '')
      if (persistPath && settingsAPI.saveWorkspacePermission) {
        void settingsAPI.saveWorkspacePermission(persistPath, applied, managed?.workspace.id).catch(() => undefined)
      }
    } catch (error) {
      setActionError(errorMessage(error))
    }
  }

  const sessionAction = async (action: () => Promise<void>) => {
    setWorking(true)
    setInfo('')
    setActionError('')
    try { await action() }
    catch (error) { setActionError(errorMessage(error)) }
    finally { setWorking(false) }
  }

  const startWorkspaceReview = async (path = '') => {
    if (!client || !activeSessionId || !has('review.workspace')) return
    const sourceSessionId = activeSessionId
    const sourceExecutionId = latestExecutionId || runtime.task.executionId || ''
    await sessionAction(async () => {
      const result = await client.request<{
        changed: boolean
        session?: SessionSummary
        executionId?: string
        sourceSessionId: string
        sourceExecutionId?: string
        path?: string
        materialsDigest?: string
      }>('review.start', {
        sourceSessionId,
        ...(sourceExecutionId ? { sourceExecutionId } : {}),
        ...(path.trim() ? { path: path.trim() } : {}),
      })
      if (!result.changed || !result.session) {
        setInfo(t('No text changes to review. Git-ignored and binary files are excluded.'))
        return
      }
      if (!result.materialsDigest) throw new Error('Core did not return a review evidence digest.')
      if (clientRef.current !== client) return
      const reviewLink: ReviewContext = {
        reviewSessionId: result.session.id,
        executionId: result.executionId,
        sourceSessionId,
        ...(sourceExecutionId ? { sourceExecutionId } : {}),
        path: result.path ?? path.trim(),
        materialsDigest: result.materialsDigest,
        stale: false,
      }
      try { localStorage.setItem(`lune:review:${result.session.id}`, JSON.stringify(reviewLink)) } catch { /* freshness links remain available for this view */ }
      setReviewContext(reviewLink)
      setSearch('')
      activeSessionRef.current = result.session
      setActiveSession(result.session)
      setSessions((items) => {
        const next = [result.session!, ...items.filter((item) => item.id !== result.session!.id)]
        sessionsRef.current = next
        return next
      })
      setActiveSessionId(result.session.id)
      setListRevision((revision) => revision + 1)
      if (workspaceIdRef.current) onSyncSessionsRef.current?.(workspaceIdRef.current)
      setInfo(t('Review started in a separate read-only session.'))
    })
  }

  const startTaskDiagnosis = async () => {
    if (!client || !activeSessionId || !has('task.diagnose')) return
    const sourceSessionId = activeSessionId
    const sourceExecutionId = latestExecutionId || runtime.task.executionId || ''
    await sessionAction(async () => {
      const result = await client.request<{
        session: SessionSummary
        executionId: string
        sourceSessionId: string
        sourceExecutionId?: string
        evidenceDigest: string
      }>('diagnosis.start', {
        sourceSessionId,
        ...(sourceExecutionId ? { sourceExecutionId } : {}),
      })
      if (!result.evidenceDigest) throw new Error('Core did not return a diagnosis evidence digest.')
      if (clientRef.current !== client) return
      const link: TaskDiagnosisContext = {
        diagnosisSessionId: result.session.id,
        executionId: result.executionId,
        sourceSessionId,
        ...(sourceExecutionId ? { sourceExecutionId } : {}),
        evidenceDigest: result.evidenceDigest,
        stale: false,
      }
      try { localStorage.setItem(`lune:diagnosis:${result.session.id}`, JSON.stringify(link)) } catch { /* freshness link remains available for this view */ }
      setDiagnosisContext(link)
      setSearch('')
      activeSessionRef.current = result.session
      setActiveSession(result.session)
      setSessions((items) => {
        const next = [result.session, ...items.filter((item) => item.id !== result.session.id)]
        sessionsRef.current = next
        return next
      })
      setActiveSessionId(result.session.id)
      setListRevision((revision) => revision + 1)
      if (workspaceIdRef.current) onSyncSessionsRef.current?.(workspaceIdRef.current)
      setInfo(t('Task diagnosis started in a separate read-only session.'))
    })
  }

  const checkWorkspaceReview = async () => {
    const target = reviewContext
    if (!client || !target || target.reviewSessionId !== activeSessionId) throw new Error('Open a review session before checking evidence freshness.')
    const result = await client.request<{ stale: boolean; turnResult?: { status: string } | null }>('review.get', {
      sessionId: target.reviewSessionId,
      ...(target.executionId ? { executionId: target.executionId } : {}),
      sourceSessionId: target.sourceSessionId,
      path: target.path,
      materialsDigest: target.materialsDigest,
    })
    setReviewContext((current) => {
      if (!current || current.reviewSessionId !== target.reviewSessionId) return current
      const updated = { ...current, stale: result.stale, status: result.turnResult?.status ?? current.status }
      try { localStorage.setItem(`lune:review:${target.reviewSessionId}`, JSON.stringify(updated)) } catch { /* keep the in-memory result */ }
      return updated
    })
    return result.stale
  }

  const checkTaskDiagnosis = async () => {
    const target = diagnosisContext
    if (!client || !target || target.diagnosisSessionId !== activeSessionId) throw new Error('Open a task diagnosis session before checking evidence freshness.')
    const result = await client.request<{ stale: boolean; turnResult?: { status: string } | null }>('diagnosis.get', {
      sessionId: target.diagnosisSessionId,
      ...(target.executionId ? { executionId: target.executionId } : {}),
      sourceSessionId: target.sourceSessionId,
      ...(target.sourceExecutionId ? { sourceExecutionId: target.sourceExecutionId } : {}),
      evidenceDigest: target.evidenceDigest,
    })
    setDiagnosisContext((current) => {
      if (!current || current.diagnosisSessionId !== target.diagnosisSessionId) return current
      const updated = { ...current, stale: result.stale, status: result.turnResult?.status ?? current.status }
      try { localStorage.setItem(`lune:diagnosis:${target.diagnosisSessionId}`, JSON.stringify(updated)) } catch { /* keep the in-memory result */ }
      return updated
    })
    return result.stale
  }

  const applyTaskDiagnosis = async () => {
    const target = diagnosisContext
    if (!client || !target || target.diagnosisSessionId !== activeSessionId) throw new Error('Open a task diagnosis session before handing off its next steps.')
    await sessionAction(async () => {
      const diagnosis = await client.request<{ stale: boolean; runtime: RuntimeState; history: unknown; turnResult?: { status: string } | null }>('diagnosis.get', {
        sessionId: target.diagnosisSessionId,
        ...(target.executionId ? { executionId: target.executionId } : {}),
        sourceSessionId: target.sourceSessionId,
        ...(target.sourceExecutionId ? { sourceExecutionId: target.sourceExecutionId } : {}),
        evidenceDigest: target.evidenceDigest,
      })
      setDiagnosisContext((current) => current?.diagnosisSessionId === target.diagnosisSessionId ? { ...current, stale: diagnosis.stale, status: diagnosis.turnResult?.status ?? current.status } : current)
      if (diagnosis.stale) throw new Error(t('Task evidence changed after this diagnosis started. Run a fresh diagnosis before handing off its next steps.'))
      if (diagnosis.turnResult?.status !== 'completed' && diagnosis.runtime.task.status !== 'completed') throw new Error(t('Wait for the diagnosis to finish before sending its next steps.'))
      const report = timelineMessages(buildTimeline(diagnosis.history)).filter((message) => message.role === 'assistant').at(-1)?.text.trim()
      if (!report) throw new Error(t('The diagnosis has not produced a report yet.'))
      const sourceRuntime = normalizeRuntimeState(await client.request<RuntimeState>('runtime.state', { sessionId: target.sourceSessionId }))
      if (sourceRuntime.task.status === 'paused') throw new Error(t('Resume the source task before sending it diagnosis next steps.'))
      const mode = runtimeHasPendingWork(sourceRuntime) ? 'queue_input' : 'start_if_idle'
      await client.request('turn.submit', {
        sessionId: target.sourceSessionId,
        submissionId: crypto.randomUUID(),
        input: `Independent diagnosis from session ${target.diagnosisSessionId}:\n\n${report}\n\nUse the observed facts and useful next steps that still apply to this task. Continue the work, verify any changes, and report what you did. Treat unresolved hypotheses as questions, not established facts.`,
        mode,
      })
      setActiveSessionId(target.sourceSessionId)
      setInfo(t(mode === 'queue_input' ? 'Diagnosis next steps were queued in the source session.' : 'Diagnosis next steps were sent to the source session.'))
    })
  }

  const applyReviewFindings = async () => {
    const target = reviewContext
    if (!client || !target || target.reviewSessionId !== activeSessionId) throw new Error('Open a review session before handing off findings.')
    await sessionAction(async () => {
      const review = await client.request<{ stale: boolean; runtime: RuntimeState; history: unknown; turnResult?: { status: string } | null }>('review.get', {
        sessionId: target.reviewSessionId,
        ...(target.executionId ? { executionId: target.executionId } : {}),
        sourceSessionId: target.sourceSessionId,
        path: target.path,
        materialsDigest: target.materialsDigest,
      })
      setReviewContext((current) => current?.reviewSessionId === target.reviewSessionId ? { ...current, stale: review.stale, status: review.turnResult?.status ?? current.status } : current)
      if (review.stale) throw new Error(t('Workspace evidence changed after this review started. Run a fresh review before handing off these findings.'))
      if (review.turnResult?.status !== 'completed' && review.runtime.task.status !== 'completed') throw new Error(t('Wait for the review to finish before sending its findings.'))
      const report = timelineMessages(buildTimeline(review.history)).filter((message) => message.role === 'assistant').at(-1)?.text.trim()
      if (!report) throw new Error(t('The review has not produced a report yet.'))

      const sourceRuntime = normalizeRuntimeState(await client.request<RuntimeState>('runtime.state', { sessionId: target.sourceSessionId }))
      if (sourceRuntime.task.status === 'paused') throw new Error(t('Resume the source task before sending it review findings.'))
      const mode = runtimeHasPendingWork(sourceRuntime) ? 'queue_input' : 'start_if_idle'
      await client.request('turn.submit', {
        sessionId: target.sourceSessionId,
        submissionId: crypto.randomUUID(),
        input: `Review findings from independent session ${target.reviewSessionId}:\n\n${report}\n\nAddress the confirmed findings that still apply. Recheck the relevant code and report the changes and validation results. Treat unverified items as questions, not defects.`,
        mode,
      })
      setActiveSessionId(target.sourceSessionId)
      setInfo(t(mode === 'queue_input' ? 'Review findings were queued in the source session.' : 'Review findings were sent to the source session.'))
    })
  }

  useEffect(() => {
    const target = reviewContext
    if (!client || !target || target.reviewSessionId !== activeSessionId) return
    void client.request<{ stale: boolean; turnResult?: { status: string } | null }>('review.get', {
      sessionId: target.reviewSessionId,
      ...(target.executionId ? { executionId: target.executionId } : {}),
      sourceSessionId: target.sourceSessionId,
      path: target.path,
      materialsDigest: target.materialsDigest,
    }).then((result) => {
      setReviewContext((current) => {
        if (!current || current.reviewSessionId !== target.reviewSessionId) return current
        const updated = { ...current, stale: result.stale, status: result.turnResult?.status ?? current.status }
        try { localStorage.setItem(`lune:review:${target.reviewSessionId}`, JSON.stringify(updated)) } catch { /* keep the in-memory result */ }
        return updated
      })
    }).catch((error) => setActionError(errorMessage(error)))
  }, [client, activeSessionId, reviewContext?.reviewSessionId, reviewContext?.sourceSessionId, reviewContext?.path, reviewContext?.materialsDigest])

  useEffect(() => {
    const target = diagnosisContext
    if (!client || !target || target.diagnosisSessionId !== activeSessionId) return
    void client.request<{ stale: boolean; turnResult?: { status: string } | null }>('diagnosis.get', {
      sessionId: target.diagnosisSessionId,
      ...(target.executionId ? { executionId: target.executionId } : {}),
      sourceSessionId: target.sourceSessionId,
      ...(target.sourceExecutionId ? { sourceExecutionId: target.sourceExecutionId } : {}),
      evidenceDigest: target.evidenceDigest,
    }).then((result) => {
      setDiagnosisContext((current) => {
        if (!current || current.diagnosisSessionId !== target.diagnosisSessionId) return current
        const updated = { ...current, stale: result.stale, status: result.turnResult?.status ?? current.status }
        try { localStorage.setItem(`lune:diagnosis:${target.diagnosisSessionId}`, JSON.stringify(updated)) } catch { /* keep the in-memory result */ }
        return updated
      })
    }).catch((error) => setActionError(errorMessage(error)))
  }, [client, activeSessionId, diagnosisContext?.diagnosisSessionId, diagnosisContext?.sourceSessionId, diagnosisContext?.sourceExecutionId, diagnosisContext?.evidenceDigest])

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
    if (workspaceIdRef.current) onSyncSessionsRef.current?.(workspaceIdRef.current)
    if (desktop) void reloadSavedConnections().catch(() => undefined)
  })

  const forkSession = (targetId = activeSessionId) => void sessionAction(async () => {
    if (!client || !targetId) return
    const forked = await client.request<SessionSummary>('session.fork', { sessionId: targetId })
    setSearch('')
    activeSessionRef.current = forked
    setActiveSession(forked)
    setSessions((items) => {
      const next = [forked, ...items.filter((item) => item.id !== forked.id)]
      sessionsRef.current = next
      return next
    })
    setActiveSessionId(forked.id)
    setListRevision((revision) => revision + 1)
    if (workspaceIdRef.current) onSyncSessionsRef.current?.(workspaceIdRef.current)
    if (desktop) void reloadSavedConnections().catch(() => undefined)
  })

  const compactSession = () => void sessionAction(async () => {
    if (!client) return
    setInfo('Compacting context…')
    try {
      const result = await client.request<{ compacted: boolean; backend?: string; after_tokens?: number }>('session.compact', { sessionId: activeSessionId })
      if (result.compacted && typeof result.after_tokens === 'number' && result.after_tokens > 0) {
        setLiveContextOverride({ compacting: false, afterTokens: result.after_tokens })
      } else {
        setLiveContextOverride((prev) => (prev.compacting ? { ...prev, compacting: false } : prev))
      }
      setInfo(result.compacted ? (result.backend ? `Context compacted (${result.backend}).` : 'Context compacted.') : 'Context is already within its budget.')
      await refreshSessionData(client, activeSessionId)
    } catch (error) {
      setLiveContextOverride((prev) => (prev.compacting ? { ...prev, compacting: false } : prev))
      setInfo('')
      throw error
    }
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

  const replaceQueued = (executionId: string, nextText: string) => void sessionAction(async () => {
    const trimmed = nextText.trim()
    if (!client || !activeSessionId || !trimmed) return
    const result = await client.request<{ submission?: { executionId?: string }; cancelledExecutionIds?: string[] }>('turn.replaceQueued', {
      sessionId: activeSessionId,
      executionId,
      input: trimmed,
    })
    const cancelled = new Set(result.cancelledExecutionIds ?? [])
    const nextExecId = result.submission?.executionId || executionId
    const identity = JSON.stringify([trimmed, []])
    const baselineCount = history.filter((m) => m.role === 'user' && messageIdentity(m) === identity).length
    setOptimistic((items) => {
      const kept = items.filter((item) => !item.executionId || !cancelled.has(item.executionId))
      const found = kept.some((item) => item.executionId === executionId)
      if (found) {
        return kept.map((item) => item.executionId === executionId ? { ...item, executionId: nextExecId, text: trimmed, baselineCount } : item)
      }
      return [...kept, { id: nextExecId, role: 'user', text: trimmed, baselineCount, sessionId: activeSessionId, executionId: nextExecId }]
    })
    setEditingQueueExecId('')
    setQueueEditDraft('')
    await refreshRuntime(client, activeSessionId)
  })

  const saveQuestionDraft = useCallback(async (request: NonNullable<Pending['userQuestion']>, draftValue: UserQuestionDraft) => {
    if (!client || !activeSessionId || !capabilitiesRef.current.includes('question.draft')) return
    await client.request('question.saveDraft', {
      sessionId: activeSessionId,
      wait: {
        executionId: request.executionId,
        runId: request.runId,
        invocationId: request.invocationId,
        callId: request.callId,
      },
      draft: draftValue,
    })
  }, [client, activeSessionId])

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
      clearCapturedReasoning(targetId)
      if (targetId === activeSessionId) {
        const next = (await client.request<SessionSummary[]>('session.list', { limit: 1 }))[0]
        if (next) {
          setActiveSessionId(next.id)
        } else {
          const created = await createSessionWithDefaults(client, modelsRef.current)
          activeSessionRef.current = created
          setActiveSession(created)
          sessionsRef.current = [created]
          setSessions([created])
          setActiveSessionId(created.id)
        }
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
    if (workspaceIdRef.current) onSyncSessionsRef.current?.(workspaceIdRef.current)
    if (desktop) void reloadSavedConnections().catch(() => undefined)
    if (kind !== 'delete' && targetId === activeSessionId) await refreshSessionData(client, targetId)
  })

  const agentSnapshots = useMemo(() => deriveAgentSnapshots({
    session: activeSession,
    timeline,
    liveAgents,
    runtime,
    models,
    taskIsRunning,
    liveContext: liveContextOverride,
  }), [activeSession, timeline, liveAgents, runtime, models, taskIsRunning, liveContextOverride])

  const openHarnessTab = (tab: HarnessTab) => {
    setHarnessTab(tab)
    setHarnessOpen(true)
  }

  const openAgentsInspector = (query = '') => {
    const trimmed = query.trim()
    if (!trimmed) {
      setSelectedAgentId('')
      openHarnessTab('agents')
      return
    }
    const lower = trimmed.toLowerCase()
    const match = agentSnapshots.find((a) =>
      a.id.toLowerCase() === lower || a.name.toLowerCase() === lower,
    ) ?? (() => {
      const idx = Number(trimmed)
      return Number.isInteger(idx) && idx >= 1 && idx <= agentSnapshots.length ? agentSnapshots[idx - 1] : undefined
    })()
    if (!match) {
      setActionError(`Agent "${trimmed}" not found. Use /agents to list agents in this session.`)
      return
    }
    setSelectedAgentId(match.id)
    openHarnessTab('agents')
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
        if (args) {
          await sendMessage(false, { textOverride: args, forcePlanMode: true, restoreDraftOnError: rawInput })
          return
        }
        setPlanMode((value) => !value)
        return
      case '/files':
        if (args) setRequestedFile(args)
        openHarnessTab('files')
        return
      case '/review':
        await startWorkspaceReview(args)
        return
      case '/diagnose':
        await startTaskDiagnosis()
        return
      case '/agents':
        openAgentsInspector(args)
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
      case '/terminal':
        if (!terminalTarget) {
          setActionError(t('Terminal is only available for local workspaces.'))
          return
        }
        openHarnessTab('terminal')
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
      case '/permissions': {
        if (!args) {
          setComposerMenu('permission')
          return
        }
        const normalized = args.toLowerCase() as PermissionMode
        if (!allowedPermissionModes.includes(normalized)) {
          setActionError(`Invalid permission mode: ${args}`)
          return
        }
        await changePermissionMode(normalized)
        setInfo(`${t('Permission mode set to')} ${t(PERM_LABEL[normalized])}.`)
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
        const m = usageMetrics
        setSlashNotice([
          `**Session** \`${activeSessionId}\``,
          `- **Model**: \`${selectedModel || '-'}\` (${activeSession?.settings.reasoningEffort || 'default'})`,
          `- **Task**: \`${runtime.task.status}\` · **Queued**: ${runtime.queue.length}`,
          m.hasUsage ? `- **Tokens**: ${formatTokenCount(m.input)} in / ${formatTokenCount(m.output)} out · ${formatTokenCount(m.cached)} cached (${m.cachePct}%) · ${formatTokenCount(m.total)} total` : '',
          m.hasUsage && m.contextWindow > 0 ? `- **Context**: ${formatTokenCount(m.usedContext)} / ${formatTokenCount(m.contextWindow)} (${m.contextPct}%)` : '',
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
    if (!configReloadPending) return
    let cancelled = false
    let timer = 0
    const schedule = (delay: number) => {
      timer = window.setTimeout(() => void check(), delay)
    }
    const check = async () => {
      if (cancelled) return
      if (desktop && connection?.mode === 'local') {
        const currentClient = clientRef.current
        if (!currentClient) {
          schedule(1000)
          return
        }
        try {
          if (await coreHasPendingWork(currentClient)) {
            schedule(2500)
            return
          }
        } catch {
          schedule(5000)
          return
        }
        setApplyingConfig(true)
        await activateConnection({ mode: 'local', workspace: connection.workspace })
        if (cancelled) {
          setApplyingConfig(false)
          return
        }
        if (clientRef.current && clientRef.current !== currentClient) {
          setConfigReloadPending(false)
          setConfigReconnectRequired(false)
        } else {
          setConfigReloadPending(false)
          setConfigReconnectRequired(true)
        }
        setApplyingConfig(false)
        return
      }

      const getStale = settingsAPI.workspaceConfigStale
      const workspaceId = managed?.workspace.id
      if (settingsAPI.kind !== 'web-host' || !getStale || !workspaceId) return
      try {
        if (await getStale(workspaceId)) schedule(1500)
        else setConfigReloadPending(false)
      } catch {
        schedule(5000)
      }
    }
    schedule(1000)
    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [activateConnection, configReloadPending, connection, desktop, managed?.workspace.id, settingsAPI])
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

  const isEmptyConversation = displayedMessages.length === 0 && timeline.length === 0 && !hasActiveStream && liveTools.length === 0 && pending.length === 0 && !taskIsPaused && runtime.queue.length === 0
  const queuedItems = useMemo(
    () => runtime.queue.filter((item) => item.status === 'queued'),
    [runtime.queue],
  )
  const queuedExecutionIds = useMemo(
    () => new Set(queuedItems.map((item) => item.executionId).filter(Boolean)),
    [queuedItems],
  )
  const sessionOptimistic = useMemo(
    () => optimistic.filter((item) => item.sessionId === activeSessionId && (!item.executionId || !queuedExecutionIds.has(item.executionId))),
    [optimistic, activeSessionId, queuedExecutionIds],
  )
  const userTurns = useMemo(() => {
    const list: Array<{ id: string; label: string }> = []
    for (const entry of timeline) {
      if (entry.kind === 'message' && entry.role === 'user') {
        const raw = entry.text.replace(/\s+/g, ' ').trim()
        const fallback = entry.attachments?.[0]?.name || t('Attachment')
        list.push({ id: entry.id, label: raw || fallback })
      }
    }
    for (const msg of sessionOptimistic) {
      if (msg.role === 'user') {
        const raw = msg.text.replace(/\s+/g, ' ').trim()
        list.push({ id: msg.id, label: raw || msg.attachments?.[0]?.name || t('Attachment') })
      }
    }
    return list
  }, [timeline, sessionOptimistic, t])
  const resolvedActiveTurnId = activeTurnId || userTurns[userTurns.length - 1]?.id || ''
  const outlineTicks = useMemo(() => {
    if (userTurns.length <= 24) return userTurns
    const maxTicks = 24
    const activeIdx = Math.max(0, userTurns.findIndex((item) => item.id === resolvedActiveTurnId))
    const picked = new Set<number>([0, userTurns.length - 1, activeIdx])
    const step = (userTurns.length - 1) / (maxTicks - 1)
    for (let i = 0; i < maxTicks && picked.size < maxTicks; i++) {
      picked.add(Math.round(i * step))
    }
    return [...picked].sort((a, b) => a - b).map((idx) => userTurns[idx])
  }, [userTurns, resolvedActiveTurnId])
  useEffect(() => {
    syncActiveTurn()
  }, [userTurns.length, activeSessionId, syncActiveTurn])
  const timelineCallbacksRef = useRef({ implementPlan, rewindAndSubmit, regenerateLastResponse })
  timelineCallbacksRef.current = { implementPlan, rewindAndSubmit, regenerateLastResponse }
  const stableImplementPlan = useCallback((executionId: string, planModeChoice: 'same_context' | 'new_context') => {
    timelineCallbacksRef.current.implementPlan(executionId, planModeChoice)
  }, [])
  const stableEditAndResend = useCallback((turnsFromEnd: number, nextText: string, attachments?: HistoryAttachment[]) => (
    timelineCallbacksRef.current.rewindAndSubmit(turnsFromEnd, nextText, attachments)
  ), [])
  const stableRegenerate = useCallback(() => (
    timelineCallbacksRef.current.regenerateLastResponse()
  ), [])

  const isChatMounted = Boolean(client) && !showConnection && !isEmptyConversation
  useEffect(() => {
    if (!isChatMounted) return
    const scroller = messagesScrollRef.current
    const column = conversationColumnRef.current
    if (!scroller || !column) return

    const detachSticky = () => {
      stickToBottomRef.current = false
      smoothButtonUntilRef.current = 0
      stopScrollRaf()
      if (scroller.scrollHeight > scroller.clientHeight + 60) {
        setShowScrollBottom(true)
      }
    }

    const onWheel = (e: WheelEvent) => {
      if (e.deltaY < 0) detachSticky()
    }
    const onTouchStart = (e: TouchEvent) => {
      touchStartYRef.current = e.touches[0]?.clientY ?? null
    }
    const onTouchMove = (e: TouchEvent) => {
      const y = e.touches[0]?.clientY
      if (touchStartYRef.current !== null && y !== undefined && y > touchStartYRef.current + 4) {
        detachSticky()
      }
    }
    const onPointerDown = (e: PointerEvent) => {
      if (e.clientX >= scroller.getBoundingClientRect().right - 16) {
        detachSticky()
      }
    }

    scroller.addEventListener('wheel', onWheel, { passive: true })
    scroller.addEventListener('touchstart', onTouchStart, { passive: true })
    scroller.addEventListener('touchmove', onTouchMove, { passive: true })
    scroller.addEventListener('pointerdown', onPointerDown, { passive: true })

    let observer: ResizeObserver | null = null
    if (typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(() => {
        if (stickToBottomRef.current) {
          const snap = initialScrollSnapRef.current
          if (snap) initialScrollSnapRef.current = false
          scheduleScrollCatchup(snap)
        } else {
          setShowScrollBottom(scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight > 60)
        }
      })
      observer.observe(column)
      observer.observe(scroller)
    }

    return () => {
      scroller.removeEventListener('wheel', onWheel)
      scroller.removeEventListener('touchstart', onTouchStart)
      scroller.removeEventListener('touchmove', onTouchMove)
      scroller.removeEventListener('pointerdown', onPointerDown)
      observer?.disconnect()
    }
  }, [isChatMounted, scheduleScrollCatchup, stopScrollRaf])
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
      workspaceId={managed?.workspace.id}
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
          const currentClient = clientRef.current
          if (currentClient) {
            try {
              if (await coreHasPendingWork(currentClient)) {
                setConfigReloadPending(true)
                setConfigReconnectRequired(false)
                return { ...savedResult, reloadPending: true, requiresReconnect: false }
              }
            } catch (error) {
              setConfigReloadPending(false)
              setConfigReconnectRequired(true)
              setActionError(`Could not confirm that all workspace tasks are idle: ${errorMessage(error)}`)
              return { ...savedResult, requiresReconnect: true, reconnectError: errorMessage(error) }
            }
          }
          await activateConnection({ mode: 'local', workspace: connection.workspace })
          if (clientRef.current) {
            setConfigReloadPending(false)
            setConfigReconnectRequired(false)
            return { ...savedResult, reloaded: true, requiresReconnect: false }
          }
          setConfigReloadPending(false)
          setConfigReconnectRequired(true)
          return { ...savedResult, requiresReconnect: true }
        }
        if (settingsAPI.kind === 'web-host' && savedResult.reloadPending) {
          setConfigReloadPending(true)
          setConfigReconnectRequired(false)
        } else if (settingsAPI.kind === 'web-host' && savedResult.requiresReconnect) {
          setConfigReloadPending(false)
          setConfigReconnectRequired(true)
          if (savedResult.reconnectError) setActionError(savedResult.reconnectError)
        } else if (settingsAPI.kind === 'web-host' && savedResult.reloaded) {
          setConfigReloadPending(false)
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

  const statusDotCls =
    connectionStatus === 'connected'
      ? 'bg-ok'
      : connectionStatus === 'reconnecting'
        ? 'bg-warn animate-[pulse_1.2s_infinite]'
        : 'bg-ink-faint'
  const formFieldCls = 'form-field flex flex-col gap-1.5 text-[13px] font-medium text-ink-secondary mb-3.5'
  const compactTabsCls = 'connection-tabs compact-tabs flex gap-1 rounded-[9px] border border-line-subtle bg-muted p-1 mb-4'
  const tabBtnCls = (active: boolean) =>
    cn(
      'flex flex-1 min-h-[34px] cursor-pointer items-center justify-center gap-1.5 rounded-[7px] border-0 px-3 text-[13px] font-medium transition-[background,color,box-shadow] duration-120',
      active ? 'active bg-canvas text-ink shadow-elev-sm' : 'bg-transparent text-ink-secondary hover:text-ink',
    )

  if (managed && (!client || showConnection)) {
    return (
      <div className="web-workbench web-workbench-loading grid h-dvh min-h-0 grid-cols-1 grid-rows-[52px_auto_minmax(0,1fr)] bg-canvas md:grid-cols-[260px_minmax(0,1fr)] md:grid-rows-[52px_minmax(0,1fr)]">
        <header className="workbench-top col-span-full flex items-center gap-3 border-b border-line-subtle bg-canvas px-[18px]">
          <span className="workbench-brand flex items-center gap-2 text-[15px] font-semibold tracking-[-0.02em] text-ink">
            <Sparkles size={19} /> Lune
          </span>
          <div className="ml-auto"><LanguagePicker /></div>
        </header>
        <aside className="sidebar flex h-full min-h-0 flex-col overflow-hidden bg-sidebar px-2.5 pt-2.5 pb-3" aria-label={t('Conversations')}>
          <div className="sidebar-scroll-region min-h-0 flex-1 overflow-y-auto">{managed.navigation}</div>
          <div className="sidebar-bottom mt-2 flex flex-col gap-2 pt-2">
            <div className="core-card flex items-center gap-2.5 rounded-[10px] border border-line-subtle bg-subtle px-2.5 py-2">
              <div className="core-card-icon grid h-7 w-7 shrink-0 place-items-center rounded-[7px] bg-muted text-ink-secondary"><Code2 size={15} /></div>
              <div className="core-card-copy min-w-0 flex-1">
                <strong className="block truncate text-[12px] font-semibold text-ink">{managed.isRemoteOverride ? t('Remote Core') : t('Local Core')}</strong>
                <span className="block truncate font-mono text-[11px] text-ink-muted">{workspacePath || workspaceName || 'Workspace'}</span>
              </div>
              <span className={cn('connection-light h-2 w-2 shrink-0 rounded-full', connectionStatus, statusDotCls)} />
            </div>
            <button
              type="button"
              className="settings-button flex h-9 w-full cursor-pointer items-center gap-2.5 rounded-[9px] border-0 bg-transparent px-2.5 text-[13.5px] font-medium text-ink transition-[background] duration-120 hover:bg-hover"
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
        <main className="web-launch m-auto flex flex-col items-center gap-3 p-6 text-center text-[14px] text-ink-secondary">
          {connectionError || actionError ? (
            <>
              <p className="m-0 text-danger" role="alert">{t(connectionError || actionError)}</p>
              <button className={btnSecondary} onClick={() => void activateConnection(managed.options)}>{t('Retry')}</button>
            </>
          ) : (
            <p className="m-0" role="status">{t('Connecting to local Core…')}</p>
          )}
        </main>
        {settingsDialogNode}
      </div>
    )
  }
  if (!client || showConnection) {
    if (!desktop && autoRestoring && !connectionError) {
      return (
        <div className="connect-page relative flex min-h-screen items-center justify-center bg-canvas px-6 py-20">
          <main className="web-launch m-auto p-6 text-center text-[14px] text-ink-secondary"><p role="status">{t('Connecting…')}</p></main>
        </div>
      )
    }
    return (
      <div className="connect-page relative flex min-h-screen items-center justify-center bg-canvas px-4 pt-[72px] pb-[60px] sm:px-6 sm:pt-[88px] sm:pb-[72px]">
        <div className="connect-top absolute top-[18px] left-4 z-[1] flex items-center gap-2.5 text-[16px] font-semibold tracking-[-0.02em] text-ink sm:top-6 sm:left-8">
          <div className="brand-mark grid h-[30px] w-[30px] place-items-center rounded-[8px] bg-accent text-accent-fg"><Sparkles size={16} /></div>
          <span>Lune</span>
          {desktop && <span className="build-label ml-1 rounded-[5px] border border-line px-[7px] py-0.5 font-mono text-[11px] font-medium tracking-[0.06em] text-ink-muted">CORE CLIENT</span>}
        </div>
        <div className="connect-appearance absolute top-[18px] right-4 z-[2] flex items-center gap-2 sm:top-6 sm:right-8">
          {!desktop && <LanguagePicker />}<ThemePicker value={theme} onChange={setTheme} />
        </div>
        <main
          className="connect-card relative z-[1] w-[min(100%,430px)] rounded-[16px] border border-line bg-canvas px-5 pt-[26px] pb-[22px] shadow-elev-md animate-[arrive_.25s_ease_both] sm:rounded-[18px] sm:px-[34px] sm:pt-9 sm:pb-7"
          onKeyDown={(event) => {
            if (event.key === 'Enter' && !connectDisabled && event.target instanceof HTMLInputElement && event.target.type !== 'checkbox') {
              event.preventDefault()
              void connectFromForm()
            }
          }}
        >
          {desktop ? (
            <>
              <div className="eyebrow mb-2 flex items-center gap-2 font-mono text-[11px] tracking-[0.06em] text-ink-muted">
                <span className="eyebrow-dot h-1.5 w-1.5 rounded-full bg-ink" /> YOUR WORKSPACE, YOUR CORE
              </div>
              <h1 className="m-0 mb-2 text-[24px] font-semibold leading-[1.2] tracking-[-0.03em] text-ink">A quiet place<br />to think with AI.</h1>
              <p className="connect-intro m-0 mb-5 text-[13.5px] leading-[1.6] text-ink-secondary">
                Connect to a Lune Core. Your sessions, tools, and permissions stay with the Core you choose.
              </p>
            </>
          ) : (
            <h1 className="m-0 mb-4 text-[21px] font-semibold tracking-[-0.025em] text-ink">{t('Link a remote Core')}</h1>
          )}
          {desktop && (
            <div className={compactTabsCls}>
              <button type="button" className={tabBtnCls(mode === 'local')} onClick={() => setMode('local')}><Laptop size={16} /> This computer</button>
              <button type="button" className={tabBtnCls(mode === 'remote')} onClick={() => setMode('remote')}><Radio size={16} /> Remote Core</button>
            </div>
          )}
          {desktop && savedConnections.length > 0 && (
            <div className="recent-connections mb-3.5 flex flex-wrap gap-1.5" aria-label="Saved connections">
              {savedConnections.filter((saved) => saved.mode === mode).map((saved) => (
                <div className="recent-connection inline-flex items-center gap-1" key={saved.address}>
                  <button type="button" className={btnSecondary} title={saved.address} disabled={busy} onClick={() => void useSavedConnection(saved)}>
                    {saved.mode === 'local' ? saved.address.split(/[\\/]/).filter(Boolean).at(-1) ?? saved.address : saved.name}
                  </button>
                  <button
                    type="button"
                    className={cn('icon-button', btnIcon, 'h-7 w-7')}
                    aria-label={`Remove saved connection ${saved.name}`}
                    disabled={busy}
                    onClick={() => void window.luneDesktop?.forgetConnection(saved.mode, saved.address).then(reloadSavedConnections).catch((error) => setConnectionError(errorMessage(error)))}
                  >
                    <X size={13} />
                  </button>
                </div>
              ))}
            </div>
          )}
          {mode === 'local' && desktop ? (
            <div className={formFieldCls}>
              <label>Workspace folder</label>
              <div className="input-with-action flex items-center gap-2">
                <input className={cn(fieldInput, 'flex-1')} value={workspace} onChange={(event) => setWorkspace(event.target.value)} placeholder="Choose a workspace folder" />
                <button type="button" className={btnSecondary} onClick={() => void chooseWorkspace()} aria-label="Browse folders"><FolderOpen size={17} /></button>
              </div>
              <small className="text-[11.5px] font-normal text-ink-muted">
                Core uses your desktop settings. Sessions are stored in this workspace’s <code className="font-mono">.lune</code> folder.
              </small>
            </div>
          ) : (
            <>
              <div className={formFieldCls}>
                <label>{t('Core WebSocket URL')}</label>
                <input
                  className={fieldInput}
                  value={remoteURL}
                  onChange={(event) => setRemoteURL(event.target.value)}
                  placeholder={`wss://${typeof location !== 'undefined' && location.host ? location.host : 'core.example.com:18092'}/v1/runtime`}
                  autoCapitalize="none"
                  spellCheck={false}
                />
              </div>
              <div className={formFieldCls}>
                <label>{t('Access token')}</label>
                <input
                  type="password"
                  className={fieldInput}
                  value={token}
                  onChange={(event) => setToken(event.target.value)}
                  placeholder={t('Paste your Core token')}
                  autoComplete="off"
                />
                {!desktop && (
                  <label className="remember-token mt-1 flex cursor-pointer items-center gap-2 text-[12.5px] font-normal text-ink-secondary">
                    <input type="checkbox" className="accent-ink" checked={rememberToken} onChange={(event) => setRememberToken(event.target.checked)} /> {t('Remember token in this browser')}
                  </label>
                )}
                {!desktop && (
                  <small className="mt-1 flex items-center gap-1.5 text-[11.5px] font-normal text-ink-muted">
                    <LockKeyhole size={13} className="shrink-0" />
                    {t('Automatically reconnects on refresh. Uncheck to keep for this tab only.')}
                  </small>
                )}
                {desktop && (
                  <label className="remember-token mt-1 flex cursor-pointer items-center gap-2 text-[12.5px] font-normal text-ink-secondary">
                    <input type="checkbox" className="accent-ink" checked={rememberToken} onChange={(event) => setRememberToken(event.target.checked)} /> Remember token with system credentials
                  </label>
                )}
              </div>
              {desktop && (
                <label className={formFieldCls}>Connection name
                  <input className={fieldInput} value={remoteName} onChange={(event) => setRemoteName(event.target.value)} maxLength={120} />
                </label>
              )}
            </>
          )}
          {desktop && mode === 'local' && (
            <button type="button" className={cn(btnSecondary, 'mb-3')} disabled={busy} onClick={() => { setSettingsTab('models'); setSettingsOpen(true) }}>
              {t('Settings')}
            </button>
          )}
          {connectionNotice && <div className={cn('form-status mb-3', bannerStatus)} role="status">{connectionNotice}</div>}
          {(connectionError || pairingError) && (
            <div className={cn('form-error mb-3 flex items-center gap-2', bannerError)} role="alert">
              <ShieldAlert size={16} className="shrink-0" /> <span>{t(connectionError || pairingError || '')}</span>
            </div>
          )}
          {!connectionError && connectionStatus === 'reconnecting' && (
            <div className={cn('form-status mb-3 flex items-center gap-2', bannerStatus)}>
              <LoaderCircle size={15} className="spin" /> The connection dropped. Lune is trying again automatically.
            </div>
          )}
          <button className={cn('primary-button connect-button w-full', btnPrimary)} disabled={connectDisabled} onClick={() => void connectFromForm()}>
            {busy ? <LoaderCircle size={17} className="spin" /> : <ArrowUp size={17} />}{t(busy ? 'Connecting…' : 'Connect to Core')}
          </button>
          {desktop && connection && (
            <button className={cn(btnPlain, 'mt-2 w-full')} onClick={() => void disconnect()}>{t('Disconnect')}</button>
          )}
          <div className="connect-footnote mt-4 flex items-center justify-between gap-2 border-t border-line-subtle pt-3 text-[11px] sm:text-[11.5px] text-ink-muted">
            <span className="inline-flex items-center gap-1.5"><LockKeyhole size={13} /> End-to-end transport security</span>
            <span className="font-mono">Runtime Protocol v1</span>
          </div>
        </main>
        {settingsDialogNode}
        {desktop && (
          <div className="connect-bottom absolute right-4 bottom-[18px] left-4 flex items-center justify-between font-mono text-[10.5px] tracking-[0.06em] text-ink-faint sm:right-8 sm:bottom-6 sm:left-8 sm:text-[11px]">
            <span>CORE RUNS WHERE YOU CHOOSE</span><span>WEB · DESKTOP · MULTI-DEVICE</span>
          </div>
        )}
      </div>
    )
  }

  const showSearchBlock = searchOpen || Boolean(search.trim()) || showArchived || compactLayout

  const rowActionBtnCls =
    'row-action-btn inline-grid h-6 w-6 cursor-pointer place-items-center rounded-[6px] border-0 bg-transparent p-0 text-ink-muted transition-[background,color] duration-120 hover:not-disabled:bg-active hover:not-disabled:text-ink disabled:opacity-40'
  const popoverBtnCls =
    'flex w-full cursor-pointer items-center gap-2 rounded-[7px] border-0 bg-transparent px-[9px] py-1.5 text-left text-[12.5px] text-ink hover:not-disabled:bg-hover disabled:cursor-not-allowed disabled:opacity-40 [&_svg]:shrink-0 [&_svg]:text-ink-secondary'

  const renderSessionRowActions = (session: SessionSummary) => {
    const menuOpen = rowMenuId === session.id
    return (
      <div
        className={cn(
          'session-row-actions flex items-center gap-[1px] pr-1 transition-opacity duration-120',
          menuOpen ? 'opacity-100 pointer-events-auto' : 'opacity-0 pointer-events-none group-hover/srow:opacity-100 group-hover/srow:pointer-events-auto group-focus-within/srow:opacity-100 group-focus-within/srow:pointer-events-auto',
        )}
      >
        <button
          type="button"
          className={rowActionBtnCls}
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
          className={cn(rowActionBtnCls, 'hover:not-disabled:bg-danger-bg hover:not-disabled:text-danger')}
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
        <div className="session-row-menu relative">
          <button
            type="button"
            className={rowActionBtnCls}
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
            <div className="session-row-popover absolute top-[calc(100%+4px)] right-0 z-40 flex min-w-[175px] flex-col gap-0.5 rounded-[11px] border border-line bg-composer p-[5px] shadow-elev-md" role="menu">
              <button type="button" className={popoverBtnCls} disabled={working} onClick={() => { setRowMenuId(''); setSessionDialog({ kind: 'rename', sessionId: session.id, value: session.title || '' }) }}>
                <Pencil size={13} aria-hidden="true" /><span>{t('Rename')}</span>
              </button>
              <button type="button" className={popoverBtnCls} disabled={working} onClick={() => { setRowMenuId(''); changeArchived(session) }}>
                <Archive size={13} aria-hidden="true" /><span>{t(session.archived ? 'Unarchive' : 'Archive')}</span>
              </button>
              {has('session.fork') && (
                <button type="button" className={popoverBtnCls} disabled={working || (session.id === activeSessionId && taskIsRunning)} onClick={() => { setRowMenuId(''); forkSession(session.id) }}>
                  <GitFork size={13} aria-hidden="true" /><span>{t('Fork conversation')}</span>
                </button>
              )}
              <button type="button" className={popoverBtnCls} disabled={working} onClick={() => { setRowMenuId(''); saveConversation(session) }}>
                <Download size={13} aria-hidden="true" /><span>{t('Export Markdown')}</span>
              </button>
              {session.id === activeSessionId && has('session.compact') && (
                <button type="button" className={popoverBtnCls} disabled={sessionBusy || working} onClick={() => { setRowMenuId(''); compactSession() }}>
                  <Minimize2 size={13} aria-hidden="true" /><span>{t('Compact context')}</span>
                </button>
              )}
              {session.id === activeSessionId && has('session.rewind') && (
                <button type="button" className={popoverBtnCls} disabled={sessionBusy || working || !history.length} onClick={() => { setRowMenuId(''); setSessionDialog({ kind: 'rewind', sessionId: session.id, value: '1' }) }}>
                  <RotateCcw size={13} aria-hidden="true" /><span>{t('Rewind…')}</span>
                </button>
              )}
              <hr className="menu-divider my-1 h-[1px] border-0 bg-line-subtle" />
              <button
                type="button"
                className={cn(popoverBtnCls, 'text-danger hover:not-disabled:bg-danger-bg [&_svg]:text-danger')}
                disabled={working || (session.id === activeSessionId && sessionBusy)}
                onClick={() => { setRowMenuId(''); setSessionDialog({ kind: 'delete', sessionId: session.id, value: '' }) }}
              >
                <Trash2 size={13} aria-hidden="true" /><span>{t('Delete…')}</span>
              </button>
            </div>
          )}
        </div>
      </div>
    )
  }

  const useWebTree = Boolean(managed && !managed.isRemoteOverride)
  const useDesktopTree = Boolean(!managed && desktop && connection?.mode !== 'remote')

  const webTreeItems: WorkspaceTreeItem[] = useWebTree
    ? (managed!.workspaces?.length ? managed!.workspaces : [managed!.workspace]).map((w) => {
        const records = managed!.workspaceSessions?.[w.id] ?? w.sessions ?? []
        const activity = managed!.activityByWorkspace?.[w.id]
          ?? (records.some((session) => session.taskStatus === 'running') ? 'Running'
            : records.some((session) => session.taskStatus === 'paused') ? 'Paused'
              : records.some((session) => session.taskStatus === 'queued') ? 'Queued' : '')
        return {
          id: w.id,
          path: w.path,
          name: w.name || w.path.split(/[\\/]/).filter(Boolean).at(-1) || w.path,
          activity,
          sessions: records.map((s) => ({
            id: s.sessionId,
            title: s.title,
            archived: s.archived,
            updatedAt: s.updatedAt,
            taskStatus: s.taskStatus,
            executionId: s.executionId,
            queueCount: s.queueCount,
          })),
        }
      })
    : []

  const desktopTreeItems: WorkspaceTreeItem[] = useDesktopTree
    ? (desktopWorkspaces.length
        ? desktopWorkspaces.map((w) => ({
            id: w.id,
            path: w.path,
            name: w.name || w.path.split(/[\\/]/).filter(Boolean).at(-1) || w.path,
            sessions: (w.sessions ?? []).map((s) => ({
              id: s.sessionId,
              title: s.title,
              archived: s.archived,
              updatedAt: s.updatedAt,
              taskStatus: s.taskStatus,
              executionId: s.executionId,
              queueCount: s.queueCount,
            })),
          }))
        : savedConnections
            .filter((c) => c.mode === 'local')
            .map((c) => ({
              id: c.address,
              path: c.address,
              name: c.address.split(/[\\/]/).filter(Boolean).at(-1) || c.address,
              sessions: [],
            })))
    : []

  const activeDesktopWorkspaceId = useDesktopTree && connection?.mode === 'local'
    ? (desktopTreeItems.find((w) => w.path === connection.workspace)?.id ?? connection.workspace)
    : ''

  const sessionNavigation = <>
    <button
      className="new-chat-button mb-2.5 flex h-9 w-full cursor-pointer items-center gap-2.5 rounded-[9px] border-0 bg-transparent px-2.5 text-left text-[13.5px] font-medium text-ink transition-[background] duration-120 hover:not-disabled:bg-hover disabled:opacity-45"
      disabled={working}
      onClick={() => void createSession()}
    >
      <SquarePen size={16} aria-hidden="true" /><span>{t('New conversation')}</span>
    </button>
    {showSearchBlock && (
      <div className="sidebar-search-block mb-2.5">
        <div className="session-search flex h-[34px] items-center gap-2 rounded-[9px] border border-line bg-canvas px-2.5 text-ink-muted">
          <Search size={14} aria-hidden="true" />
          <input
            autoFocus={searchOpen && !compactLayout}
            className="min-w-0 w-full border-0 bg-transparent p-0 text-[13px] text-ink outline-none placeholder:text-ink-faint"
            aria-label={t('Search conversations')}
            value={search}
            disabled={working}
            onChange={(event) => setSearch(event.target.value)}
            placeholder={t(serverSearch ? 'Search titles and messages' : 'Find a conversation')}
          />
          {search && (
            <button type="button" className={btnSubtleIcon} aria-label={t('Dismiss')} onClick={() => setSearch('')}><X size={13} /></button>
          )}
        </div>
        <div className="sidebar-filter-row flex items-center justify-between px-1 pt-1.5 pb-0.5">
          <label className="archived-toggle flex cursor-pointer select-none items-center gap-[7px] text-[12px] text-ink-muted">
            <input type="checkbox" className="accent-ink" checked={showArchived} disabled={working} onChange={(event) => setShowArchived(event.target.checked)} />
            {t('Include archived')}
          </label>
          <button
            className={btnSubtleIcon}
            title={t('Refresh sessions')}
            aria-label={t('Refresh sessions')}
            disabled={working || listLoading}
            onClick={() => setListRevision((revision) => revision + 1)}
          >
            <RefreshCw size={13} />
          </button>
        </div>
      </div>
    )}
    {useWebTree ? (
      <WorkspaceTree
        workspaces={webTreeItems}
        activeWorkspaceId={managed!.workspace.id}
        activeSessionId={activeSessionId}
        activeSessions={filteredSessions}
        listLoading={listLoading}
        moreSessions={moreSessions}
        onLoadMore={loadMoreSessions}
        working={working}
        search={search}
        showArchived={showArchived}
        onOpenWorkspacePicker={() => managed!.openDirectoryPicker(false)}
        onSelectWorkspace={(id) => {
          closeDrawer()
          managed!.onSelectWorkspace?.(id)
        }}
        onSelectSession={(wsId, sessionId) => {
          if (wsId === managed!.workspace.id) {
            chooseSession(sessionId)
          } else {
            closeDrawer()
            managed!.onSelectWorkspaceSession?.(wsId, sessionId)
          }
        }}
        onNewSessionInWorkspace={(wsId) => {
          if (wsId === managed!.workspace.id) {
            void createSession()
          } else {
            closeDrawer()
            managed!.onCreateWorkspaceSession?.(wsId)
          }
        }}
        onCloseWorkspace={managed!.onCloseWorkspace}
        renderActiveSessionActions={renderSessionRowActions}
      />
    ) : useDesktopTree ? (
      <WorkspaceTree
        workspaces={desktopTreeItems}
        activeWorkspaceId={activeDesktopWorkspaceId}
        activeSessionId={activeSessionId}
        activeSessions={filteredSessions}
        listLoading={listLoading}
        moreSessions={moreSessions}
        onLoadMore={loadMoreSessions}
        working={working || busy}
        search={search}
        showArchived={showArchived}
        onOpenWorkspacePicker={() => void openWorkspaceAsFileTree(false)}
        onSelectWorkspace={(id) => {
          const target = desktopTreeItems.find((w) => w.id === id)
          if (!target) return
          closeDrawer()
          setMode('local')
          setWorkspace(target.path)
          void activateConnection({ mode: 'local', workspace: target.path })
        }}
        onSelectSession={(wsId, sessionId) => {
          const target = desktopTreeItems.find((w) => w.id === wsId)
          if (!target) return
          if (wsId === activeDesktopWorkspaceId) {
            chooseSession(sessionId)
            return
          }
          closeDrawer()
          pendingDesktopSessionRef.current = { workspace: target.path, sessionId }
          setMode('local')
          setWorkspace(target.path)
          void activateConnection({ mode: 'local', workspace: target.path })
        }}
        onNewSessionInWorkspace={(wsId) => {
          const target = desktopTreeItems.find((w) => w.id === wsId)
          if (!target) return
          if (wsId === activeDesktopWorkspaceId) {
            void createSession()
            return
          }
          closeDrawer()
          pendingDesktopNewSessionRef.current = target.path
          setMode('local')
          setWorkspace(target.path)
          void activateConnection({ mode: 'local', workspace: target.path })
        }}
        onCloseWorkspace={(id) => {
          const target = desktopTreeItems.find((w) => w.id === id)
          if (!target || target.path === (connection?.mode === 'local' ? connection.workspace : '')) return
          void window.luneDesktop?.closePersistedWorkspace(id).then(() => reloadSavedConnections()).catch(() => undefined)
        }}
        renderActiveSessionActions={renderSessionRowActions}
      />
    ) : (
      <>
        {managed?.navigation}
        <div className="session-list min-h-0 flex-1 overflow-x-hidden overflow-y-auto pr-0.5" aria-busy={listLoading}>
          {sessionGroups.map((group) => (
            <div className="session-group mb-3" key={group.label}>
              <div className="session-group-label px-2.5 pt-1 pb-1 text-[11px] font-medium text-ink-muted">{t(group.label)}</div>
              {group.items.map((session) => {
                const preview = session.title || `${t('Conversation')} ${session.id.slice(0, 8)}`
                const menuOpen = rowMenuId === session.id
                const selected = session.id === activeSessionId
                return (
                  <div
                    key={session.id}
                    className={cn(
                      'session-row group/srow relative my-[1px] flex items-center rounded-[9px] transition-[background] duration-120 hover:bg-hover focus-within:bg-hover',
                      selected && 'selected bg-active',
                      menuOpen && 'menu-open bg-hover',
                    )}
                  >
                    <button
                      className={cn(
                        'session-item m-0 min-h-[34px] min-w-0 flex-1 cursor-pointer truncate rounded-[9px] border-0 bg-transparent px-2.5 py-1.5 text-left text-[13.5px] text-ink',
                        selected && 'font-medium',
                      )}
                      disabled={working}
                      aria-current={selected ? 'true' : undefined}
                      onClick={() => chooseSession(session.id)}
                    >
                      <span className="block truncate">{preview}{session.archived ? t(' (archived)') : ''}</span>
                    </button>
                    {renderSessionRowActions(session)}
                  </div>
                )
              })}
            </div>
          ))}
          {moreSessions && <button className={cn(btnPlain, 'sidebar-load-more mt-1 w-full')} disabled={working || listLoading} onClick={loadMoreSessions}>{t('Load more')}</button>}
          {!filteredSessions.length && (
            <div className="empty-sessions px-2.5 py-6 text-center text-[12.5px] text-ink-muted">
              {t(listLoading ? 'Loading conversations…' : search.trim() ? 'No matching conversations.' : 'No conversations yet.')}
            </div>
          )}
        </div>
      </>
    )}
    {drawerOpen && actionError && <div className={cn('action-error my-2 flex items-center gap-2', bannerError)} role="alert">{actionError}</div>}
    <div className="sidebar-bottom mt-2 flex flex-col gap-2 pt-2">
      <div className="core-card flex items-center gap-2.5 rounded-[10px] border border-line-subtle bg-subtle px-2.5 py-2">
        <div className="core-card-icon grid h-7 w-7 shrink-0 place-items-center rounded-[7px] bg-muted text-ink-secondary"><Code2 size={15} /></div>
        <div className="core-card-copy min-w-0 flex-1">
          <strong className="block truncate text-[12px] font-semibold text-ink">{managed?.isRemoteOverride || connection?.mode === 'remote' ? t('Remote Core') : t('Local Core')}</strong>
          <span className="block truncate font-mono text-[11px] text-ink-muted">{workspaceName || (connection?.mode === 'remote' ? connection.url : connection?.mode === 'local' ? workspace.split(/[\\/]/).filter(Boolean).at(-1) || 'This computer' : workspacePath || 'Workspace')}</span>
        </div>
        <span className={cn('connection-light h-2 w-2 shrink-0 rounded-full', connectionStatus, statusDotCls)} />
      </div>
      <button
        type="button"
        className="settings-button flex h-9 w-full cursor-pointer items-center gap-2.5 rounded-[9px] border-0 bg-transparent px-2.5 text-[13.5px] font-medium text-ink transition-[background] duration-120 hover:bg-hover"
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

  const applyMentionMatch = (item: MentionItem) => {
    if (!activeMention) return
    const replacement = `@${item.path} `
    const nextDraft = `${draft.slice(0, activeMention.start)}${replacement}${draft.slice(activeMention.end)}`
    const nextCaret = activeMention.start + replacement.length
    setDraft(nextDraft)
    setCursorPos(nextCaret)
    setMentionDismissedAt(null)
    window.requestAnimationFrame(() => {
      const el = textareaRef.current
      if (el) {
        el.focus()
        el.setSelectionRange(nextCaret, nextCaret)
      }
    })
  }

  const runningCommandsCount = commands.filter((cmd) => cmd.status === 'running').length

  const composerPillCls =
    'composer-pill-btn inline-flex h-[30px] sm:h-[32px] cursor-pointer items-center gap-[5px] whitespace-nowrap rounded-full border border-line bg-subtle px-[10px] sm:px-[11px] text-[12px] sm:text-[12.5px] font-medium text-ink-secondary transition-[background,border-color,color] duration-120 hover:not-disabled:border-line-strong hover:not-disabled:bg-hover hover:not-disabled:text-ink disabled:cursor-not-allowed disabled:opacity-40'
  const composerPopoverCls =
    'composer-popover absolute bottom-[calc(100%+8px)] right-0 z-30 flex w-[min(320px,calc(100vw-24px))] sm:w-[300px] flex-col gap-1.5 rounded-[14px] border border-line bg-composer p-2 shadow-elev-md'
  const composerItemCls = (active: boolean) =>
    cn(
      'composer-popover-item flex w-full cursor-pointer items-center justify-between gap-2 rounded-[9px] border-0 px-2.5 py-2 text-left transition-[background] duration-100',
      active ? 'active bg-muted text-ink' : 'bg-transparent text-ink-secondary hover:bg-hover hover:text-ink',
    )

  const composerNode = (
    <div className="composer-wrap relative mx-auto w-[min(100%-20px,720px)] sm:w-[min(100%-40px,720px)] pb-[max(12px,env(safe-area-inset-bottom))]">
      {!isEmptyConversation && showScrollBottom && slashMatches.length === 0 && !activeMention && (
        <button
          type="button"
          className="scroll-bottom-btn absolute -top-[46px] left-1/2 z-25 grid h-[34px] w-[34px] -translate-x-1/2 cursor-pointer place-items-center rounded-full border border-line-strong bg-canvas text-ink shadow-elev-sm transition-[background,transform] duration-120 hover:bg-hover"
          aria-label={t('Scroll to bottom')}
          title={t('Scroll to bottom')}
          onClick={() => scrollToBottom(true)}
        >
          <ArrowDown size={16} aria-hidden="true" />
        </button>
      )}
      {connectionNotice && <div className={cn('form-status mb-2', bannerStatus)} role="status">{connectionNotice}</div>}
      {info && <div className={cn('form-status mb-2', bannerStatus)} role="status">{info}</div>}
      {slashNotice && (
        <div className="slash-notice mb-2 flex items-start justify-between gap-2 rounded-[12px] border border-line bg-subtle px-3.5 py-2.5" role="status">
          <div className="slash-notice-body min-w-0 flex-1"><div className="markdown-body text-[13px]"><MarkdownContent text={slashNotice} /></div></div>
          <button type="button" className={btnSubtleIcon} onClick={() => setSlashNotice('')} aria-label={t('Dismiss')}><X size={14} /></button>
        </div>
      )}
      {!drawerOpen && actionError && (
        <div className={cn('action-error mb-2 flex items-center gap-2', bannerError)}>
          <ShieldAlert size={15} className="shrink-0" />
          <span className="min-w-0 flex-1">{t(actionError)}</span>
          <button className={btnSubtleIcon} onClick={() => setActionError('')} aria-label={t('Dismiss')}><X size={14} /></button>
        </div>
      )}
      {configReloadPending && (
        <div className="reconnect-banner settings-reconnect-banner mb-2 rounded-[10px] border border-line bg-subtle px-3 py-2 text-[12.5px] text-ink-secondary" role="status">
          {t('Settings saved. Changes will apply when the current work finishes.')}
        </div>
      )}
      {configReconnectRequired && (
        <div className="reconnect-banner settings-reconnect-banner mb-2 flex flex-wrap items-center justify-between gap-2 rounded-[10px] border border-line bg-subtle px-3 py-2 text-[12.5px] text-ink-secondary" role="status">
          <span>{t('Settings saved. Reconnect this workspace after the current task finishes to apply the changes.')}</span>
          <button type="button" className={btnSecondary} onClick={() => void applySavedConfig()} disabled={applyingConfig || taskIsRunning || taskHasQueuedWork}>
            {t(applyingConfig ? 'Applying…' : 'Apply settings')}
          </button>
        </div>
      )}
      {connectionStatus !== 'connected' && (
        <div className="reconnect-banner mb-2 flex items-center gap-2 rounded-[10px] border border-line bg-subtle px-3 py-2 text-[12.5px] text-ink-secondary">
          <LoaderCircle className="spin shrink-0" size={14} /> {t(connectionStatus === 'reconnecting' ? 'Reconnecting to Core. Your current work continues there.' : 'Core connection lost.')}
        </div>
      )}
      {slashMatches.length > 0 && activeSession && !working && (
        <div ref={slashPopoverRef} className={cn('slash-popover absolute right-0 bottom-[calc(100%+6px)] left-0 z-30 flex max-h-[260px] flex-col gap-0.5 overflow-y-auto p-1.5', popoverSurface)} role="listbox" aria-label={t('Slash commands')}>
          {slashMatches.map((cmd, index) => (
            <button
              key={cmd.name}
              type="button"
              role="option"
              aria-selected={index === slashIndex}
              className={cn(
                'slash-item flex w-full cursor-pointer items-center gap-2.5 rounded-[8px] border-0 px-2.5 py-[7px] text-left text-[12.5px]',
                index === slashIndex ? 'active bg-hover text-ink' : 'bg-transparent text-ink-secondary hover:bg-hover',
              )}
              onMouseMove={(event) => {
                if (event.clientX === popoverPointerRef.current.x && event.clientY === popoverPointerRef.current.y) return
                popoverPointerRef.current = { x: event.clientX, y: event.clientY }
                if (slashIndex !== index) {
                  slashScrollByKeyRef.current = false
                  setSlashIndex(index)
                }
              }}
              onMouseDown={(event) => {
                event.preventDefault()
                applySlashMatch(cmd)
              }}
            >
              <span className="slash-name font-mono font-semibold text-ink">{cmd.name}</span>
              {cmd.argHint && <span className="slash-arg font-mono text-[11.5px] text-ink-muted">{cmd.argHint}</span>}
              <span className="slash-desc ml-auto truncate text-[12px] text-ink-muted">{t(cmd.description)}</span>
            </button>
          ))}
        </div>
      )}
      {activeMention && activeSession && !working && slashMatches.length === 0 && (mentionLoading || mentionMatches.length > 0 || activeMention.query.length > 0) && (
        <div ref={mentionPopoverRef} className={cn('slash-popover mention-popover absolute right-0 bottom-[calc(100%+6px)] left-0 z-30 flex max-h-[260px] flex-col gap-0.5 overflow-y-auto p-1.5', popoverSurface)} role="listbox" aria-label={t('Files matching @')}>
          {mentionLoading && mentionMatches.length === 0 ? (
            <div className="mention-empty px-3 py-3 text-[12.5px] text-ink-muted">{t('Loading files…')}</div>
          ) : mentionMatches.length === 0 ? (
            <div className="mention-empty px-3 py-3 text-[12.5px] text-ink-muted">{t('No matching files.')}</div>
          ) : (
            mentionMatches.map((item, index) => {
              const slashAt = item.path.lastIndexOf('/')
              const dirPart = slashAt >= 0 ? item.path.slice(0, slashAt) : ''
              return (
                <button
                  key={item.path}
                  type="button"
                  role="option"
                  aria-selected={index === mentionIndex}
                  className={cn(
                    'slash-item mention-item flex w-full cursor-pointer items-center gap-2 rounded-[8px] border-0 px-2.5 py-[7px] text-left text-[12.5px]',
                    index === mentionIndex ? 'active bg-hover text-ink' : 'bg-transparent text-ink-secondary hover:bg-hover',
                  )}
                  onMouseMove={(event) => {
                    if (event.clientX === popoverPointerRef.current.x && event.clientY === popoverPointerRef.current.y) return
                    popoverPointerRef.current = { x: event.clientX, y: event.clientY }
                    if (mentionIndex !== index) {
                      mentionScrollByKeyRef.current = false
                      setMentionIndex(index)
                    }
                  }}
                  onMouseDown={(event) => {
                    event.preventDefault()
                    applyMentionMatch(item)
                  }}
                >
                  <span className="mention-icon shrink-0 text-ink-muted" aria-hidden="true">
                    {item.kind === 'directory' ? <Folder size={14} /> : <FileIcon size={14} />}
                  </span>
                  <span className="mention-name font-mono font-medium text-ink">{item.name}</span>
                  {dirPart && <span className="mention-path ml-auto truncate font-mono text-[11.5px] text-ink-muted">{dirPart}</span>}
                </button>
              )
            })
          )}
        </div>
      )}
      {!isEmptyConversation && queuedItems.length > 0 && (
        <div className="composer-queue mb-2 rounded-[14px] border border-line bg-subtle px-3 py-2" aria-label={t('Message queue')}>
          <div className="composer-queue-head mb-1.5 flex items-center gap-1.5 text-[12px] font-semibold text-ink-secondary">
            <Command size={12} aria-hidden="true" />
            <span>{t('Message queue')} ({queuedItems.length})</span>
            {has('turn.cancelPending') && (
              <button type="button" className={cn(btnPlain, 'ml-auto min-h-[24px] px-2 text-[11.5px]')} disabled={working} onClick={cancelAllPending}>{t('Cancel all')}</button>
            )}
          </div>
          <ol className="composer-queue-list m-0 flex max-h-[120px] list-none flex-col gap-1 overflow-y-auto p-0">
            {queuedItems.map((item, idx) => {
              const queuedMsg = optimistic.find((m) => m.sessionId === activeSessionId && m.executionId === item.executionId)
              const isEditing = editingQueueExecId === item.executionId
              return (
                <li key={item.executionId} className="queue-item-row flex items-center gap-2 rounded-[8px] border border-line-subtle bg-canvas px-2.5 py-1 text-[12.5px]">
                  {isEditing ? (
                    <form
                      className="queue-edit-form flex w-full items-center gap-1.5"
                      onSubmit={(event) => {
                        event.preventDefault()
                        replaceQueued(item.executionId, queueEditDraft)
                      }}
                    >
                      <input
                        type="text"
                        className={cn(fieldInput, 'h-[28px] flex-1 px-2 text-[12.5px]')}
                        value={queueEditDraft}
                        disabled={working}
                        autoFocus
                        onChange={(event) => setQueueEditDraft(event.target.value)}
                        onKeyDown={(event) => {
                          if (event.key === 'Escape') {
                            event.preventDefault()
                            setEditingQueueExecId('')
                            setQueueEditDraft('')
                          }
                        }}
                      />
                      <button type="button" className={cn(btnPlain, 'min-h-[26px] text-[12px]')} disabled={working} onClick={() => { setEditingQueueExecId(''); setQueueEditDraft('') }}>{t('Cancel')}</button>
                      <button type="submit" className={cn(btnPrimaryCompact, 'min-h-[26px] px-2.5 text-[12px]')} disabled={working || !queueEditDraft.trim()}>{t('Save')}</button>
                    </form>
                  ) : (
                    <>
                      <span className="queue-item-idx shrink-0 font-mono text-[11px] text-ink-muted">#{idx + 1}</span>
                      {queuedMsg?.text ? (
                        <small className="queue-item-preview min-w-0 flex-1 truncate text-[12.5px] text-ink" title={queuedMsg.text}>{queuedMsg.text}</small>
                      ) : (
                        <small className="queue-item-preview min-w-0 flex-1 truncate text-[12.5px] text-ink-muted">{t(item.status)}</small>
                      )}
                      <span className="queue-item-actions flex shrink-0 items-center gap-1">
                        {has('turn.replaceQueued') && (
                          <button
                            type="button"
                            className={cn(btnPlain, 'min-h-[24px] px-2 text-[11.5px]')}
                            disabled={working}
                            onClick={() => {
                              setEditingQueueExecId(item.executionId)
                              setQueueEditDraft(queuedMsg?.text || '')
                            }}
                          >
                            {t('Edit')}
                          </button>
                        )}
                        {has('turn.cancelQueued') && (
                          <button type="button" className={cn(btnPlain, 'min-h-[24px] px-2 text-[11.5px]')} disabled={working} onClick={() => cancelQueued(item.executionId)}>{t('Cancel')}</button>
                        )}
                      </span>
                    </>
                  )}
                </li>
              )
            })}
          </ol>
        </div>
      )}
      <div
        className="composer flex flex-col gap-2 rounded-[22px] sm:rounded-[24px] border border-line bg-composer px-3.5 pt-2.5 pb-2 sm:px-4 sm:pt-3 sm:pb-2.5 shadow-composer transition-[border-color,box-shadow] duration-150"
        onDragOver={(event) => { if (has('turn.attachments')) event.preventDefault() }}
        onDrop={(event) => {
          if (!has('turn.attachments')) return
          event.preventDefault()
          if (!working) addFiles(Array.from(event.dataTransfer.files))
        }}
      >
        {planMode && (
          <div className="plan-active-strip flex items-center gap-1.5 rounded-[10px] bg-subtle px-2.5 py-1 text-[12px] font-medium text-ink-secondary" role="status">
            <Compass size={13} aria-hidden="true" />
            <span>{t('Plan mode active (read-only)')}</span>
            <button type="button" className="plan-active-exit ml-auto cursor-pointer border-0 bg-transparent text-[11.5px] font-medium text-ink underline" disabled={working} onClick={() => setPlanMode(false)}>{t('Exit Plan')}</button>
          </div>
        )}
        {attachments.length > 0 && (
          <ul className="attachment-list m-0 flex list-none flex-wrap gap-1.5 p-0">
            {attachments.map((file, index) => (
              <li key={`${file.name}-${index}`} className="inline-flex items-center gap-1.5 rounded-[10px] border border-line bg-subtle px-2 py-1 text-[12px] text-ink">
                {file.mediaType.startsWith('image/') && <img className="h-5 w-5 rounded object-cover" alt="" src={`data:${file.mediaType};base64,${file.data}`} />}
                <span className="max-w-[160px] truncate">{file.name}</span>
                <button className="cursor-pointer border-0 bg-transparent p-0 text-ink-muted hover:text-ink" aria-label={`Remove ${file.name}`} disabled={working} onClick={() => setAttachments((items) => items.filter((_, at) => at !== index))}>×</button>
              </li>
            ))}
          </ul>
        )}
        <textarea
          ref={textareaRef}
          className="max-h-[200px] min-h-[48px] w-full resize-none border-0 bg-transparent p-0 text-[14.5px] leading-[1.55] text-ink outline-none placeholder:text-ink-faint sm:text-[15px]"
          value={draft}
          onChange={(event) => {
            setDraft(event.target.value)
            setCursorPos(event.target.selectionStart ?? event.target.value.length)
          }}
          onSelect={(event) => setCursorPos(event.currentTarget.selectionStart ?? 0)}
          onClick={(event) => setCursorPos(event.currentTarget.selectionStart ?? 0)}
          onKeyUp={(event) => setCursorPos(event.currentTarget.selectionStart ?? 0)}
          onPaste={(event) => {
            if (!has('turn.attachments') || working) return
            const items = Array.from(event.clipboardData?.items ?? [])
            const itemFiles = items
              .filter((item) => item.kind === 'file')
              .map((item) => item.getAsFile())
              .filter((file): file is File => Boolean(file))
            const rawFiles = itemFiles.length ? itemFiles : Array.from(event.clipboardData?.files ?? [])
            const supported = rawFiles.filter(isSupportedClipboardFile)
            if (supported.length > 0) {
              event.preventDefault()
              addFiles(supported)
            }
          }}
          onKeyDown={(event) => {
            if (slashMatches.length > 0) {
              if (event.key === 'ArrowDown') {
                event.preventDefault()
                slashScrollByKeyRef.current = true
                setSlashIndex((i) => (i + 1) % slashMatches.length)
                return
              }
              if (event.key === 'ArrowUp') {
                event.preventDefault()
                slashScrollByKeyRef.current = true
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
            if (activeMention && (mentionMatches.length > 0 || mentionLoading)) {
              if (event.key === 'ArrowDown' && mentionMatches.length > 0) {
                event.preventDefault()
                mentionScrollByKeyRef.current = true
                setMentionIndex((i) => (i + 1) % mentionMatches.length)
                return
              }
              if (event.key === 'ArrowUp' && mentionMatches.length > 0) {
                event.preventDefault()
                mentionScrollByKeyRef.current = true
                setMentionIndex((i) => (i - 1 + mentionMatches.length) % mentionMatches.length)
                return
              }
              if (event.key === 'Tab' && mentionMatches.length > 0) {
                event.preventDefault()
                applyMentionMatch(mentionMatches[mentionIndex] ?? mentionMatches[0])
                return
              }
              if (event.key === 'Escape') {
                event.preventDefault()
                setMentionDismissedAt(activeMention.start)
                return
              }
              if (event.key === 'Enter' && !event.shiftKey && mentionMatches.length > 0) {
                event.preventDefault()
                applyMentionMatch(mentionMatches[mentionIndex] ?? mentionMatches[0])
                return
              }
            }
            if (event.key === 'Enter' && !event.shiftKey) {
              event.preventDefault()
              void sendMessage(uiPreferences.runningEnter === 'steer' && (taskIsRunning || taskIsQueued || taskIsPaused))
            }
          }}
          placeholder={t(runtime.queue.length || taskIsRunning ? 'Add a message to the queue…' : 'Message Lune, / for commands, @ for files…')}
          rows={2}
          disabled={!client || !activeSession || working}
        />
        <div className="composer-footer flex items-center justify-between gap-1.5 sm:gap-2">
          <div className="composer-options flex shrink-0 flex-nowrap items-center gap-1 sm:gap-1.5">
            {has('turn.attachments') && <>
              <input type="file" ref={fileInput} hidden multiple accept=".txt,.md,.markdown,.csv,.png,.jpg,.jpeg" onChange={(event) => {
                addFiles(Array.from(event.target.files ?? []))
                event.target.value = ''
              }} />
              <button
                type="button"
                className="composer-icon-btn inline-grid h-[30px] w-[30px] sm:h-[32px] sm:w-[32px] cursor-pointer place-items-center rounded-full border-0 bg-transparent text-ink-secondary transition-[background,color] duration-120 hover:not-disabled:bg-hover hover:not-disabled:text-ink disabled:cursor-not-allowed disabled:opacity-40"
                disabled={working}
                onClick={() => fileInput.current?.click()}
                aria-label={t('Attach')}
                title={t('Attach')}
              >
                <Plus size={18} aria-hidden="true" />
              </button>
              {desktop && modelOption?.supportsInputImages && (
                <button
                  type="button"
                  className="composer-icon-btn inline-grid h-[30px] w-[30px] sm:h-[32px] sm:w-[32px] cursor-pointer place-items-center rounded-full border-0 bg-transparent text-ink-secondary transition-[background,color] duration-120 hover:not-disabled:bg-hover hover:not-disabled:text-ink disabled:cursor-not-allowed disabled:opacity-40"
                  disabled={working}
                  onClick={captureScreenshot}
                  aria-label="Screenshot"
                  title="Screenshot"
                >
                  <Camera size={16} aria-hidden="true" />
                </button>
              )}
            </>}
            {has('permission.manage') && (
              <div className="composer-perm-wrap relative">
                <button
                  type="button"
                  className={cn(composerPillCls, 'composer-perm-btn max-w-[116px] sm:max-w-none')}
                  data-perm={permissionMode}
                  aria-haspopup="listbox"
                  aria-expanded={composerMenu === 'permission'}
                  title={t('Permission mode')}
                  disabled={sessionBusy || working}
                  onClick={() => setComposerMenu((current) => current === 'permission' ? null : 'permission')}
                >
                  {permissionMode === 'read-only' ? <Lock size={13} aria-hidden="true" />
                    : permissionMode === 'auto' ? <ShieldCheck size={13} aria-hidden="true" />
                      : permissionMode === 'full-access' ? <ShieldAlert size={13} aria-hidden="true" />
                        : <Shield size={13} aria-hidden="true" />}
                  <span className="truncate">{t(PERM_LABEL[permissionMode])}</span>
                  <ChevronDown size={13} className="shrink-0" aria-hidden="true" />
                </button>
                {composerMenu === 'permission' && (
                  <div className={cn(composerPopoverCls, 'composer-popover-left left-0 right-auto sm:left-0')} role="listbox" aria-label={t('Permission mode')}>
                    <div className="composer-popover-head px-[9px] pt-[5px] pb-1 text-[11px] font-semibold uppercase tracking-[0.03em] text-ink-muted">{t('Permission mode')}</div>
                    <div className="composer-popover-list flex max-h-[340px] flex-col gap-0.5 overflow-y-auto">
                      {allowedPermissionModes.map((mode) => {
                        const active = permissionMode === mode
                        return (
                          <button
                            key={mode}
                            type="button"
                            role="option"
                            aria-selected={active}
                            className={composerItemCls(active)}
                            onClick={() => {
                              setComposerMenu(null)
                              void changePermissionMode(mode)
                            }}
                          >
                            <div className="composer-popover-item-main min-w-0 flex-1">
                              <span className="flex items-center gap-1.5 text-[12.5px] font-medium text-ink">
                                {t(PERM_LABEL[mode])}<code className="composer-perm-code rounded bg-muted px-1 font-mono text-[10.5px] font-normal text-ink-muted">{mode}</code>
                              </span>
                              <small className="composer-perm-desc mt-0.5 block text-[11px] leading-[1.35] text-ink-muted">{t(PERM_DESC[mode])}</small>
                            </div>
                            {active && <Check size={14} className="shrink-0 text-ink" aria-hidden="true" />}
                          </button>
                        )
                      })}
                    </div>
                  </div>
                )}
              </div>
            )}
          </div>
          <div className="composer-actions flex min-w-0 items-center gap-[5px] sm:gap-1.5">
            {(() => {
              const currentEffort = activeSession?.settings.reasoningEffort || ''
              const effortInfo = effortDisplayInfo(currentEffort)
              const isMaxEffort = models.length > 0 && Boolean(selectedModel) && effortInfo.isMax
              return (
                <div className="composer-picker-group min-w-0">
                  <div className="composer-picker-wrap relative">
                    <button
                      type="button"
                      className={cn(
                        composerPillCls,
                        'composer-picker-btn max-w-[196px] sm:max-w-[260px]',
                        isMaxEffort && 'is-max border-ink bg-canvas text-ink',
                      )}
                      aria-haspopup="listbox"
                      aria-expanded={composerMenu === 'model'}
                      title={t(models.length > 0 ? 'MODEL' : 'Configure models')}
                      disabled={models.length > 0 ? (!activeSession || sessionBusy) : false}
                      onClick={() => {
                        if (models.length === 0) {
                          setSettingsTab(settingsAPI.supportsModelConfig ? 'models' : 'remote')
                          setSettingsOpen(true)
                          return
                        }
                        setComposerMenu((current) => current === 'model' ? null : 'model')
                      }}
                    >
                      <span className="composer-picker-label min-w-0 truncate">
                        {models.length > 0
                          ? (modelOption?.displayName || modelOption?.model || selectedModel || t('Select model'))
                          : t('Configure models')}
                      </span>
                      {models.length > 0 && Boolean(selectedModel) && currentEffort !== '' && (
                        <span className={cn('composer-effort-inline inline-flex shrink-0 items-center gap-1 text-[11px]', isMaxEffort && 'is-max')}>
                          <EffortBars bars={effortInfo.bars} max={isMaxEffort} />
                          <span className={isMaxEffort ? 'effort-max-tag rounded-full bg-accent px-1.5 py-[1px] text-[10.5px] leading-[1.35] font-bold tracking-[0.04em] text-accent-fg' : undefined}>
                            {effortInfo.label}
                          </span>
                        </span>
                      )}
                      <ChevronDown size={13} className="shrink-0" aria-hidden="true" />
                    </button>
                    {composerMenu === 'model' && models.length > 0 && (
                      <div className={composerPopoverCls} role="listbox" aria-label={t('MODEL')}>
                        <div className="composer-popover-list flex max-h-[340px] flex-col gap-0.5 overflow-y-auto">
                          {models.map((m) => {
                            const active = m.profile === selectedModel
                            const cap = formatCapacity(m.contextWindow)
                            return (
                              <button
                                key={m.profile}
                                type="button"
                                role="option"
                                aria-selected={active}
                                className={composerItemCls(active)}
                                onClick={() => { void changeModel(m.profile) }}
                              >
                                <div className="composer-popover-item-main min-w-0 flex-1">
                                  <span className="block truncate text-[12.5px] font-medium text-ink">{m.displayName || m.model}</span>
                                  <small className="block truncate font-mono text-[11px] text-ink-muted">{m.provider}{cap ? ` · ${cap}` : ''}</small>
                                </div>
                                {active && <Check size={14} className="shrink-0 text-ink" aria-hidden="true" />}
                              </button>
                            )
                          })}
                        </div>
                        {Boolean(selectedModel) && (
                          <EffortSlider
                            label={t('Reasoning effort')}
                            value={currentEffort}
                            disabled={!activeSession || sessionBusy}
                            onCommit={(next) => void changeSettings({ reasoningEffort: next })}
                          />
                        )}
                        {settingsAPI.supportsModelConfig && (
                          <div className="composer-popover-footer border-t border-line-subtle pt-1.5">
                            <button
                              type="button"
                              className={cn(btnPlain, 'w-full justify-start')}
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
                </div>
              )
            })()}
            {taskIsRunning && runtime.task.executionId && !planMode && (
              <button
                type="button"
                className={composerPillCls}
                disabled={working || configReloadPending || (!draft.trim() && !attachments.length) || connectionStatus !== 'connected'}
                onClick={() => void sendMessage(true)}
              >
                <CornerDownRight size={14} aria-hidden="true" /><span>{t('Add to running task')}</span>
              </button>
            )}
            {((taskIsRunning && runtime.task.executionId) || (taskIsQueued && !has('turn.cancelQueued'))) && (
              <button
                type="button"
                className="stop-button inline-grid h-[30px] w-[30px] sm:h-8 sm:w-8 shrink-0 cursor-pointer place-items-center rounded-full border border-line bg-canvas text-ink transition-[background,border-color,opacity] duration-120 hover:not-disabled:bg-hover disabled:cursor-not-allowed disabled:opacity-40"
                disabled={working}
                onClick={() => void stopRun()}
                aria-label={t(taskIsQueued ? 'Cancel queued' : 'Stop')}
                title={t(taskIsQueued ? 'Cancel queued' : 'Stop')}
              >
                <Square size={12} fill="currentColor" aria-hidden="true" />
              </button>
            )}
            <button
              type="button"
              className="send-button inline-grid h-[30px] w-[30px] sm:h-8 sm:w-8 shrink-0 cursor-pointer place-items-center rounded-full border-0 bg-accent text-accent-fg transition-[background,opacity,transform] duration-120 hover:not-disabled:bg-accent-hover disabled:cursor-not-allowed disabled:opacity-40"
              disabled={!activeSession || (!draft.trim() && !attachments.length) || working || configReloadPending || connectionStatus !== 'connected'}
              onClick={() => void sendMessage()}
              aria-label={t('Send message')}
              title={t('Send message')}
            >
              {working ? <LoaderCircle size={17} className="spin" /> : <ArrowUp size={18} />}
            </button>
          </div>
        </div>
      </div>
      {!isEmptyConversation && (
        <div className="composer-telemetry mt-1.5 flex min-h-[28px] flex-wrap items-center justify-center gap-2">
          {usageMetrics.hasUsage && (
            <button
              type="button"
              className="telemetry-chip inline-flex min-h-[26px] cursor-pointer items-center gap-1.5 rounded-full border border-line-subtle bg-subtle px-2.5 py-0.5 font-mono text-[11.5px] text-ink-muted transition-[background,border-color,color] duration-120 hover:border-line hover:bg-hover hover:text-ink"
              title={`${t('input')} ${usageMetrics.input.toLocaleString()} · ${t('output')} ${usageMetrics.output.toLocaleString()} · ${t('Cache')} ${usageMetrics.cached.toLocaleString()} · ${t('Total')} ${usageMetrics.total.toLocaleString()} · ${t('Context usage')} ${usageMetrics.usedContext.toLocaleString()} / ${usageMetrics.contextWindow.toLocaleString()}`}
              onClick={() => openHarnessTab('context')}
            >
              <span>
                {t('In')} {formatTokenCount(usageMetrics.input)} · {t('Out')} {formatTokenCount(usageMetrics.output)}
                {' · '}{t('Cache')} {formatTokenCount(usageMetrics.cached)} ({usageMetrics.cachePct}%)
                {' · '}{t('Total')} {formatTokenCount(usageMetrics.total)}
                {usageMetrics.compacting
                  ? ` · ${t('Context usage')} ${t('Compacting')}…`
                  : usageMetrics.contextWindow > 0
                    ? ` · ${t('Context usage')} ${formatTokenCount(usageMetrics.usedContext)}/${formatTokenCount(usageMetrics.contextWindow)} (${usageMetrics.contextPct}%)`
                    : usageMetrics.usedContext > 0
                      ? ` · ${t('Context usage')} ${formatTokenCount(usageMetrics.usedContext)}`
                      : ''}
              </span>
            </button>
          )}
          {runtime.goal && (
            <button
              type="button"
              className="telemetry-chip inline-flex min-h-[26px] cursor-pointer items-center gap-1.5 rounded-full border border-line-subtle bg-subtle px-2.5 py-0.5 font-mono text-[11.5px] text-ink-muted transition-[background,border-color,color] duration-120 hover:border-line hover:bg-hover hover:text-ink"
              onClick={() => openHarnessTab('goal')}
            >
              <Target size={11} aria-hidden="true" />
              <span>{t('Goal')}: {t(runtime.goal.status)}</span>
            </button>
          )}
          {runningCommandsCount > 0 && (
            <button
              type="button"
              className="telemetry-chip inline-flex min-h-[26px] cursor-pointer items-center gap-1.5 rounded-full border border-line-subtle bg-subtle px-2.5 py-0.5 font-mono text-[11.5px] text-ink-muted transition-[background,border-color,color] duration-120 hover:border-line hover:bg-hover hover:text-ink"
              onClick={() => openHarnessTab('processes')}
            >
              <Terminal size={11} aria-hidden="true" />
              <span>{runningCommandsCount} {t('running')}</span>
            </button>
          )}
        </div>
      )}
    </div>
  )

  const isSidebarCollapsed = sidebarCollapsed && !compactLayout
  const pendingHeadingCls = 'pending-heading flex items-center gap-3'
  const pendingIconCls = 'pending-icon grid h-8 w-8 shrink-0 place-items-center rounded-[9px] border border-line bg-canvas text-ink-secondary'
  const pendingCardCls = 'pending-card mb-4 flex flex-col gap-3 rounded-[14px] border border-line bg-subtle p-4 shadow-elev-sm'

  return (
    <div
      className={cn(
        'app-shell relative grid h-dvh min-h-0 bg-canvas',
        quick && 'quick-mode',
        !desktop && 'web-workbench',
        isSidebarCollapsed && 'sidebar-collapsed',
        harnessOpen && 'files-open',
        harnessOpen && harnessOverlay && 'harness-overlay',
        draggingColumn && 'resizing',
      )}
      style={{ '--sidebar-width': `${sidebarWidth}px`, '--sidebar-edge': `${sidebarCollapsed ? 56 : sidebarWidth}px`, '--harness-width': `${harnessWidth}px` } as CSSProperties}
    >
      {compactLayout ? (
        <dialog
          id={accessibilityId + '-drawer'}
          className="session-drawer m-0 h-dvh max-h-none w-[min(300px,calc(100%-36px))] max-w-none border-0 border-r border-line bg-sidebar p-0 text-ink shadow-elev-lg"
          aria-labelledby={accessibilityId + '-drawer-title'}
          ref={drawerRef}
          onClose={() => {
            if (drawerRef.current?.open) return
            setDrawerOpen(false)
            drawerButtonRef.current?.focus()
          }}
          onClick={(event) => { if (event.target === event.currentTarget) closeDrawer() }}
        >
          <div className="drawer-panel flex h-full min-h-0 flex-col px-2.5 pt-[max(12px,env(safe-area-inset-top))] pb-[max(12px,env(safe-area-inset-bottom))]">
            <div className="drawer-heading flex items-center justify-between px-1 pb-2">
              <h2 id={accessibilityId + '-drawer-title'} className="m-0 text-[15px] font-semibold text-ink">{t('Conversations')}</h2>
              <button type="button" className={cn('icon-button', btnIcon)} aria-label={t('Close conversations')} onClick={closeDrawer}><X size={18} /></button>
            </div>
            {sessionNavigation}
          </div>
        </dialog>
      ) : (
        <aside
          className={cn(
            'sidebar flex h-full min-h-0 flex-col overflow-hidden bg-sidebar transition-opacity duration-150',
            isSidebarCollapsed ? 'pointer-events-none p-0 opacity-0' : 'px-2.5 pt-0 pb-3',
          )}
          aria-label={t('Conversations')}
          aria-hidden={sidebarCollapsed || undefined}
        >
          <div className="sidebar-top flex h-[50px] sm:h-[52px] shrink-0 items-center justify-between">
            <button type="button" className={cn('icon-button', btnIcon)} aria-label={t('Toggle sidebar')} title={t('Toggle sidebar')} onClick={() => setSidebarCollapsed(true)}><PanelLeftClose size={18} /></button>
            <div className="sidebar-top-actions flex items-center gap-0.5">
              <button type="button" className={cn('icon-button', btnIcon)} aria-label={t('Search conversations')} title={t('Search conversations')} aria-pressed={searchOpen} onClick={() => setSearchOpen((open) => !open)}><Search size={17} /></button>
              <button type="button" className={cn('icon-button', btnIcon)} aria-label={t('New conversation')} title={t('New conversation')} disabled={working} onClick={() => void createSession()}><SquarePen size={17} /></button>
            </div>
          </div>
          {sessionNavigation}
        </aside>
      )}
      {!compactLayout && sidebarCollapsed && (
        <nav className="sidebar-rail absolute inset-y-0 left-0 z-12 flex w-[56px] flex-col items-center gap-[5px] bg-sidebar pt-[10px] pb-3" aria-label={t('Quick navigation')}>
          <button type="button" className={cn('icon-button', btnIcon)} title={t('Expand conversations')} aria-label={t('Expand conversations')} onClick={() => setSidebarCollapsed(false)}><PanelLeftOpen size={18} /></button>
          <button type="button" className={cn('icon-button', btnIcon)} title={t('New conversation')} aria-label={t('New conversation')} disabled={working} onClick={() => void createSession()}><SquarePen size={17} /></button>
          <button type="button" className={cn('icon-button sidebar-rail-settings mt-auto', btnIcon)} title={t('Settings')} aria-label={t('Settings')} onClick={() => { setSettingsTab(settingsAPI.supportsModelConfig ? 'models' : 'remote'); setSettingsOpen(true) }}><Settings size={17} /></button>
        </nav>
      )}
      {!compactLayout && !sidebarCollapsed && (
        <div
          className="column-resizer sidebar-resizer absolute inset-y-0 left-[var(--sidebar-edge,260px)] z-18 w-2 -translate-x-1 touch-none cursor-col-resize outline-none select-none"
          role="separator"
          aria-orientation="vertical"
          aria-label={t('Resize conversation sidebar')}
          aria-valuemin={200}
          aria-valuemax={420}
          aria-valuenow={sidebarWidth}
          tabIndex={0}
          onPointerDown={(event) => { event.preventDefault(); setDraggingColumn('sidebar'); columnDrag.current = { side: 'sidebar', x: event.clientX, width: sidebarWidth } }}
          onDoubleClick={() => setSidebarWidth(260)}
          onKeyDown={(event) => {
            if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
            event.preventDefault(); setSidebarWidth((width) => Math.max(200, Math.min(420, width + (event.key === 'ArrowRight' ? 16 : -16))))
          }}
        />
      )}
      {!compactLayout && harnessOpen && !harnessFullScreen && (
        <div
          className="column-resizer harness-resizer absolute inset-y-0 right-[var(--harness-width,420px)] z-18 w-2 translate-x-1 touch-none cursor-col-resize outline-none select-none"
          role="separator"
          aria-orientation="vertical"
          aria-label={t('Resize Harness panel')}
          aria-valuemin={320}
          aria-valuemax={760}
          aria-valuenow={harnessWidth}
          tabIndex={0}
          onPointerDown={(event) => { event.preventDefault(); setDraggingColumn('harness'); columnDrag.current = { side: 'harness', x: event.clientX, width: harnessWidth } }}
          onDoubleClick={() => setHarnessWidth(420)}
          onKeyDown={(event) => {
            if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
            event.preventDefault(); setHarnessWidth((width) => Math.max(320, Math.min(760, width + (event.key === 'ArrowLeft' ? 16 : -16))))
          }}
        />
      )}

      <main className="chat-area flex h-dvh min-h-0 min-w-0 flex-col bg-canvas">
        <header className="chat-header flex h-[50px] sm:h-[52px] shrink-0 items-center justify-between gap-1.5 sm:gap-3 px-2.5 sm:px-4">
          <div className="header-left flex min-w-0 flex-1 items-center gap-2">
            {compactLayout && (
              <button type="button" className={cn('icon-button drawer-toggle', btnIcon)} title={t('Open conversations')} aria-label={t('Open conversations')} aria-haspopup="dialog" aria-controls={accessibilityId + '-drawer'} aria-expanded={drawerOpen} onClick={openDrawer} ref={drawerButtonRef}><PanelLeftOpen size={18} /></button>
            )}
            {workspaceName ? (
              <span className="header-workspace hidden truncate text-[13.5px] font-medium text-ink-secondary sm:inline" title={workspacePath}>{workspaceName}</span>
            ) : (
              <span className="header-brand-title text-[14.5px] font-semibold tracking-[-0.015em] text-ink">Lune</span>
            )}
            {activeSession?.title && (
              <>
                <span className="header-crumb-sep hidden text-ink-faint sm:inline" aria-hidden="true">/</span>
                <span className="header-session-title truncate text-[13.5px] font-medium text-ink" title={activeSession.title}>{activeSession.title}</span>
              </>
            )}
          </div>
          <div className="header-actions flex shrink-0 items-center gap-1">
            {connectionStatus !== 'connected' && (
              <span className={cn('connection-pill mr-1 inline-flex items-center gap-1.5 rounded-full border border-line bg-subtle px-2.5 py-0.5 text-[11.5px] text-ink-secondary', connectionStatus)}>
                {statusIcon}{t(statusLabel)}
              </span>
            )}
            {quick && <button className={btnPlain} onClick={() => void window.luneDesktop?.toggleQuick()}>Main window</button>}
            <button
              ref={fileButtonRef}
              type="button"
              className={cn('icon-button header-files-toggle', btnIcon)}
              aria-label={t('Open folder as file tree')}
              title={t('Open folder as file tree')}
              onClick={() => void openWorkspaceAsFileTree(true)}
            >
              <FolderOpen size={17} />
            </button>
            <button
              type="button"
              className={cn('icon-button header-files-toggle', btnIcon)}
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
          <div className="goal-bar flex items-center gap-1.5 border-b border-line-subtle bg-subtle px-4 py-1.5">
            <button
              type="button"
              className="goal-bar-main flex min-w-0 flex-1 cursor-pointer items-center gap-2 rounded-[6px] border-0 bg-transparent px-1 py-0.5 text-left text-[12.5px] text-ink hover:bg-hover"
              onClick={() => openHarnessTab('goal')}
            >
              <Target size={14} className="shrink-0" aria-hidden="true" />
              <span className={cn(statusPill, runtime.goal.status === 'active' && 'border-accent bg-accent text-accent-fg')}>{t(runtime.goal.status)}</span>
              <span className="goal-bar-text min-w-0 flex-1 truncate text-ink-secondary">{runtime.goal.objective}</span>
              {runtime.goal.token_budget ? (
                <span className="goal-bar-budget whitespace-nowrap font-mono text-[11px] text-ink-muted">
                  {(runtime.goal.tokens_used ?? 0).toLocaleString()} / {runtime.goal.token_budget.toLocaleString()}
                </span>
              ) : null}
            </button>
            {has('goal.manage') && (runtime.goal.status === 'active' || runtime.goal.status === 'paused') && (
              <button
                type="button"
                className={rowActionBtnCls}
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
          <div className="empty-stage flex flex-1 flex-col items-center justify-center px-3 pb-10">
            <h1 className="empty-headline m-0 mb-5 sm:mb-7 text-center text-[22px] sm:text-[28px] font-semibold tracking-[-0.025em] text-ink">
              {t('What would you like to work through?')}
            </h1>
            {composerNode}
            <div className="suggestion-row mt-4 flex flex-wrap items-center justify-center gap-2">
              <button
                type="button"
                className="inline-flex cursor-pointer items-center gap-1.5 rounded-full border border-line bg-canvas px-3.5 py-1.5 text-[12.5px] font-medium text-ink-secondary transition-[background,border-color,color] duration-120 hover:border-line-strong hover:bg-hover hover:text-ink"
                onClick={() => setDraft(t('Help me understand this project.'))}
              >
                {t('Understand a project')}<ArrowUpRight size={14} aria-hidden="true" />
              </button>
              <button
                type="button"
                className="inline-flex cursor-pointer items-center gap-1.5 rounded-full border border-line bg-canvas px-3.5 py-1.5 text-[12.5px] font-medium text-ink-secondary transition-[background,border-color,color] duration-120 hover:border-line-strong hover:bg-hover hover:text-ink"
                onClick={() => setDraft(t('Help me make a plan for…'))}
              >
                {t('Make a plan')}<ArrowUpRight size={14} aria-hidden="true" />
              </button>
            </div>
          </div>
        ) : (
          <>
            <div className="conversation-stage relative flex min-h-0 flex-1 flex-col">
              <div className="conversation-scroll min-h-0 flex-1 overflow-y-auto" ref={messagesScrollRef} onScroll={handleMessagesScroll}>
                <div className="conversation-column mx-auto flex min-h-full w-[calc(100%-24px)] sm:w-[min(100%-40px,720px)] flex-col items-stretch pt-3.5 pb-6 sm:pt-5 sm:pb-9" ref={conversationColumnRef}>
                  <TimelineView
                    entries={timeline}
                    optimistic={sessionOptimistic}
                    liveAgents={liveAgents}
                    liveTools={liveTools}
                    streamStore={streamStore}
                    hasActiveStream={hasActiveStream}
                    canImplementPlan={has('turn.implementPlan')}
                    canRewind={has('session.rewind') && has('turn.replaceHistory')}
                    sessionBusy={sessionBusy}
                    working={working}
                    latestExecutionId={latestExecutionId}
                    detailsExpanded={uiPreferences.timelineDetailsExpanded}
                    onImplementPlan={stableImplementPlan}
                    onEditAndResend={stableEditAndResend}
                    onRegenerate={stableRegenerate}
                    onInspectAgent={(id) => {
                      setSelectedAgentId(id)
                      openHarnessTab('agents')
                    }}
                  />

                  {activity.length > 0 && taskIsRunning && !hasActiveStream && liveTools.length === 0 && liveAgents.length === 0 && (
                    <div className="activity-tray -mt-1.5 mb-[22px] flex flex-col gap-1.5 border-l-2 border-line px-3.5 py-2">
                      {activity.slice(0, 3).map((item) => (
                        <div className={cn('activity-item flex items-center gap-2 text-[13px]', item.failed ? 'failed text-danger' : 'text-ink-secondary')} key={item.id}>
                          <span className={cn('activity-dot h-[5px] w-[5px] shrink-0 rounded-full', item.failed ? 'bg-danger' : 'bg-ink')} />
                          <span>{item.label}</span>
                          {item.detail && <small className="max-w-[280px] truncate font-mono text-[11.5px] text-ink-muted">{item.detail}</small>}
                        </div>
                      ))}
                    </div>
                  )}

                  {taskIsPaused && modelFailure && (
                    <div className={cn('model-recovery-card', pendingCardCls)}>
                      <div className={pendingHeadingCls}>
                        <div className={pendingIconCls}><RefreshCw size={16} /></div>
                        <div className="flex flex-col text-[13px]"><strong className="text-ink">{t('Model request paused')}</strong><span className="text-ink-secondary">{t('The saved task can resume after the retry delay.')}</span></div>
                      </div>
                      <p className="pending-reason m-0 text-[13px] text-ink-secondary">
                        {t(modelFailure.category)}
                        {modelFailure.status_code ? ` · HTTP ${modelFailure.status_code}` : ''}
                        {modelFailure.code ? ` · ${modelFailure.code}` : ''}
                        {modelFailure.attempts ? ` · ${t('Attempts')}: ${modelFailure.attempts}` : ''}
                      </p>
                      {Number.isFinite(Date.parse(modelFailure.retry_not_before || '')) && recoveryClock < Date.parse(modelFailure.retry_not_before || '')
                        ? <p className="retry-countdown m-0 font-mono text-[12px] text-ink-muted">{t('Retry available in')} {Math.ceil((Date.parse(modelFailure.retry_not_before || '') - recoveryClock) / 1000)}s</p>
                        : <p className="retry-countdown m-0 font-mono text-[12px] text-ink-muted">{t('Retry is available now.')}</p>}
                      <div className="pending-actions flex flex-wrap items-center justify-end gap-2">
                        <button className={btnPlainDanger} disabled={working} onClick={() => void discardPaused()}>{t('Discard')}</button>
                        <button className={btnSecondary} disabled={working} onClick={() => { setSettingsTab('models'); setSettingsOpen(true) }}>{t('Open model settings')}</button>
                        <button className={btnPrimaryCompact} disabled={working || (Number.isFinite(Date.parse(modelFailure.retry_not_before || '')) && recoveryClock < Date.parse(modelFailure.retry_not_before || ''))} onClick={() => void resumeRun()}><RefreshCw size={14} />{t('Resume run')}</button>
                      </div>
                    </div>
                  )}

                  {pending.filter((item) => item.kind !== 'model_recovery').map((item, index) => (
                    <div className={pendingCardCls} key={`${item.kind}-${item.approval?.id ?? item.userQuestion?.id ?? item.interaction?.id ?? item.toolRecovery?.invocationId ?? index}`}>
                      <div className={pendingHeadingCls}>
                        <div className={pendingIconCls}><CircleHelp size={17} /></div>
                        <div className="flex flex-col text-[13px]">
                          <strong className="text-ink">{t(item.kind === 'approval' ? 'Permission needed' : item.kind === 'user_question' ? 'A question for you' : item.kind === 'user_interaction' ? 'Input requested' : item.kind === 'tool_recovery' ? 'Tool needs attention' : item.kind === 'model_recovery' ? 'Run paused' : 'Execution blocked')}</strong>
                          <span className="text-ink-secondary">{item.kind === 'approval' ? `${t('Lune wants to use')} ${item.approval?.tool ?? t('a tool')}` : t('Your response will continue this run.')}</span>
                        </div>
                      </div>
                      {item.approval && <>
                        {item.approval.autoReviewReason && <p className="pending-reason m-0 text-[13px] text-ink-secondary">{item.approval.autoReviewReason}</p>}
                        <pre className="json-preview m-0 max-h-[240px] overflow-auto rounded-[9px] border border-line-subtle bg-code p-3 font-mono text-[12px] leading-[1.55] text-ink-secondary">{JSON.stringify(item.approval.arguments, null, 2)}</pre>
                        <div className="pending-actions flex flex-wrap items-center justify-end gap-2">
                          <button className={btnPlainDanger} disabled={working} onClick={() => void resolveApproval(item.approval!, 'deny')}>{t('Deny')}</button>
                          <button className={btnSecondary} disabled={working} onClick={() => void resolveApproval(item.approval!, 'allow_once')}>{t('Allow once')}</button>
                          <button className={btnPrimaryCompact} disabled={working} onClick={() => void resolveApproval(item.approval!, 'allow_session')}>{t('Allow this session')}</button>
                        </div>
                      </>}
                      {item.userQuestion && (
                        <QuestionForm
                          request={item.userQuestion}
                          disabled={working}
                          canSaveDraft={has('question.draft')}
                          onSaveDraft={(draftValue) => saveQuestionDraft(item.userQuestion!, draftValue)}
                          onSubmit={(answers) => resolveQuestion(item.userQuestion!, answers)}
                        />
                      )}
                      {item.interaction && <InteractionForm request={item.interaction} disabled={working} onSubmit={(value) => resolveInteraction(item.interaction!, value)} />}
                      {item.toolRecovery && <>
                        <p className="pending-reason m-0 text-[13px] text-ink-secondary"><strong>{item.toolRecovery.tool}</strong> · {t('You can retry this tool or skip it.')}</p>
                        <pre className="json-preview m-0 max-h-[240px] overflow-auto rounded-[9px] border border-line-subtle bg-code p-3 font-mono text-[12px] leading-[1.55] text-ink-secondary">{JSON.stringify(item.toolRecovery.arguments, null, 2)}</pre>
                        <div className="pending-actions flex flex-wrap items-center justify-end gap-2">
                          <button className={btnSecondary} disabled={working} onClick={() => void resolvePending('toolRecovery.resolve', { sessionId: activeSessionId, invocationId: item.toolRecovery!.invocationId, decision: 'skip' })}>{t('Skip')}</button>
                          <button className={btnPrimaryCompact} disabled={working} onClick={() => void resolvePending('toolRecovery.resolve', { sessionId: activeSessionId, invocationId: item.toolRecovery!.invocationId, decision: 'retry' })}>{t('Retry tool')}</button>
                        </div>
                      </>}
                      {item.blocker && <p className="pending-reason m-0 text-[13px] text-ink-secondary">{item.blocker.reason}</p>}
                    </div>
                  ))}

                  {taskIsPaused && !modelFailure && (
                    <div className="paused-banner mb-4 flex flex-wrap items-center justify-between gap-3 rounded-[14px] border border-line bg-subtle p-4 text-[13px]">
                      <div className="flex flex-col"><strong className="text-ink">{t('This run is paused')}</strong><span className="text-ink-secondary">{t('Resume from its saved checkpoint, or discard it.')}</span></div>
                      <div className="flex items-center gap-2">
                        <button className={btnPlainDanger} disabled={working} onClick={() => void discardPaused()}>{t('Discard')}</button>
                        <button className={btnPrimaryCompact} disabled={working} onClick={() => void resumeRun()}><RefreshCw size={14} />{t('Resume run')}</button>
                      </div>
                    </div>
                  )}
                  <div ref={bottomRef} />
                </div>
              </div>
              {userTurns.length >= 2 && (
                <nav className="turn-outline group/outline absolute top-1/2 right-3 z-15 flex -translate-y-1/2 items-center py-3.5 pr-1 pl-4" aria-label={t('Conversation outline')}>
                  <div className="turn-outline-ticks flex max-h-[min(260px,46vh)] cursor-pointer flex-col items-end gap-1.5 overflow-hidden" aria-hidden="true">
                    {outlineTicks.map((turn) => (
                      <span
                        key={turn.id}
                        className={cn(
                          'turn-outline-tick h-[2.5px] w-[11px] rounded-full bg-line-strong opacity-60 transition-[width,background,opacity] duration-150',
                          turn.id === resolvedActiveTurnId && 'active',
                        )}
                      />
                    ))}
                  </div>
                  <div className="turn-outline-popover absolute top-1/2 right-6 flex max-h-[min(380px,60vh)] w-[256px] flex-col overflow-hidden rounded-[12px] border border-line bg-canvas shadow-elev-md" role="region" aria-label={t('Conversation outline')}>
                    <div className="turn-outline-head flex items-center justify-between border-b border-line-subtle px-3 pt-[9px] pb-[7px] text-[11.5px] font-semibold text-ink-muted">
                      <span>{t('Conversation outline')}</span>
                      <small className="font-mono text-[11px] text-ink-faint">{userTurns.length}</small>
                    </div>
                    <div className="turn-outline-list flex flex-col gap-0.5 overflow-y-auto p-[5px]">
                      {userTurns.map((turn, idx) => {
                        const active = turn.id === resolvedActiveTurnId
                        return (
                          <button
                            key={turn.id}
                            type="button"
                            className={cn(
                              'turn-outline-item group/oitem flex w-full cursor-pointer items-center gap-2 rounded-[7px] border-0 px-2 py-1.5 text-left text-[12.5px] transition-[background,color] duration-120',
                              active ? 'active bg-muted font-medium text-ink' : 'bg-transparent text-ink-secondary hover:bg-hover hover:text-ink',
                            )}
                            title={turn.label}
                            onClick={() => jumpToTurn(turn.id)}
                          >
                            <span className={cn('turn-outline-num shrink-0 font-mono text-[10.5px] tabular-nums', active ? 'text-ink-secondary' : 'text-ink-faint')}>{String(idx + 1).padStart(2, '0')}</span>
                            <span className="turn-outline-text min-w-0 flex-1 truncate">{turn.label}</span>
                          </button>
                        )
                      })}
                    </div>
                  </div>
                </nav>
              )}
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
          onInsertText={(snippet) => {
            setDraft((prev) => (prev.trimEnd() ? `${prev.trimEnd()}\n\n${snippet}` : snippet))
            window.requestAnimationFrame(() => textareaRef.current?.focus())
          }}
          onOpenMCPSettings={() => {
            setSettingsTab('mcp')
            setSettingsOpen(true)
          }}
          onChangeReasoningEffort={(effort) => void changeSettings({ reasoningEffort: effort })}
          liveContext={liveContextOverride}
          terminalTarget={terminalTarget}
          permissionMode={permissionMode}
          allowedPermissionModes={allowedPermissionModes}
          onChangePermissionMode={(mode) => void changePermissionMode(mode as PermissionMode)}
          agents={agentSnapshots}
          selectedAgentId={selectedAgentId}
          onSelectAgent={setSelectedAgentId}
          onStartReview={startWorkspaceReview}
          onStartDiagnosis={startTaskDiagnosis}
          onApplyReviewFindings={applyReviewFindings}
          reviewContext={reviewContext?.reviewSessionId === activeSession?.id ? reviewContext ?? undefined : undefined}
          onCheckReviewFreshness={checkWorkspaceReview}
          diagnosisContext={diagnosisContext?.diagnosisSessionId === activeSession?.id ? diagnosisContext ?? undefined : undefined}
          onCheckDiagnosisFreshness={checkTaskDiagnosis}
          onApplyDiagnosis={applyTaskDiagnosis}
          onRefreshState={async () => {
            if (client && activeSessionId) await refreshSessionData(client, activeSessionId)
          }}
        />
      )}

      {settingsDialogNode}
      <CommandPalette
        open={paletteOpen}
        onClose={() => setPaletteOpen(false)}
        sessions={sessions}
        activeSessionId={activeSessionId}
        onSelectSession={chooseSession}
        workspaces={(() => {
          if (managed?.workspaces?.length) {
            return managed.workspaces.map((w) => ({
              id: w.id,
              name: w.path.split(/[\\/]/).filter(Boolean).at(-1) || w.path,
              path: w.path,
              active: !managed.isRemoteOverride && w.id === managed.workspace.id,
            }))
          }
          if (desktop) {
            return (desktopWorkspaces.length
              ? desktopWorkspaces.map((w) => ({
                  id: w.path,
                  name: w.name || w.path.split(/[\\/]/).filter(Boolean).at(-1) || w.path,
                  path: w.path,
                  active: connection?.mode === 'local' && connection.workspace === w.path,
                }))
              : savedConnections
                  .filter((c) => c.mode === 'local')
                  .map((c) => ({
                    id: c.address,
                    name: c.address.split(/[\\/]/).filter(Boolean).at(-1) || c.address,
                    path: c.address,
                    active: connection?.mode === 'local' && connection.workspace === c.address,
                  })))
          }
          return []
        })() satisfies PaletteWorkspaceItem[]}
        onSelectWorkspace={managed?.onSelectWorkspace
          ? (id) => managed.onSelectWorkspace!(id)
          : desktop
            ? (address) => {
                setMode('local')
                setWorkspace(address)
                void activateConnection({ mode: 'local', workspace: address })
              }
            : undefined}
        files={mentionItems}
        filesLoading={mentionLoading}
        onOpenFile={(filePath) => {
          setRequestedFile(filePath)
          setHarnessTab('files')
          setHarnessOpen(true)
        }}
        actions={[
          { id: 'new-session', label: 'New conversation', section: 'Actions', run: () => void createSession() },
          ...(has('workspace.files') ? [{ id: 'tab-files', label: 'Open workspace files', hint: 'Harness', section: 'Actions' as const, run: () => openHarnessTab('files') }] : []),
          { id: 'tab-agents', label: 'Inspect agents', hint: 'Harness', section: 'Actions' as const, run: () => { setSelectedAgentId(''); openHarnessTab('agents') } },
          ...(has('goal.manage') && activeSession ? [{ id: 'tab-goal', label: 'Inspect Goal', hint: 'Harness', section: 'Actions' as const, run: () => openHarnessTab('goal') }] : []),
          ...(has('command.control') && activeSession ? [{ id: 'tab-proc', label: 'Inspect Processes', hint: 'Harness', section: 'Actions' as const, run: () => openHarnessTab('processes') }] : []),
          ...(terminalTarget ? [{ id: 'tab-terminal', label: 'Open terminal', hint: 'Harness', section: 'Actions' as const, run: () => openHarnessTab('terminal') }] : []),
          ...(has('mcp.inspect') ? [{ id: 'tab-mcp', label: 'Inspect MCP servers', hint: 'Harness', section: 'Actions' as const, run: () => openHarnessTab('mcp') }] : []),
          ...(has('turn.plan') ? [{ id: 'toggle-plan', label: planMode ? 'Disable plan mode' : 'Enable plan mode', section: 'Actions' as const, run: () => setPlanMode((v) => !v) }] : []),
          ...(has('session.compact') && activeSession ? [{ id: 'compact', label: 'Compact context', section: 'Actions' as const, run: compactSession }] : []),
          ...(activeSession ? [{ id: 'export', label: 'Export Markdown', section: 'Actions' as const, run: () => saveConversation() }] : []),
          ...(settingsAPI.supportsModelConfig ? [
            { id: 'set-models', label: 'Models', hint: 'Settings', section: 'Settings' as const, run: () => { setSettingsTab('models'); setSettingsOpen(true) } },
            { id: 'set-mcp', label: 'MCP', hint: 'Settings', section: 'Settings' as const, run: () => { setSettingsTab('mcp'); setSettingsOpen(true) } },
            { id: 'set-runtime', label: 'Runtime & Agents', hint: 'Settings', section: 'Settings' as const, run: () => { setSettingsTab('runtime'); setSettingsOpen(true) } },
          ] : []),
          { id: 'set-remote', label: 'Remote & Token', hint: 'Settings', section: 'Settings', run: () => { setSettingsTab('remote'); setSettingsOpen(true) } },
          { id: 'set-general', label: 'General', hint: 'Settings', section: 'Settings', run: () => { setSettingsTab('general'); setSettingsOpen(true) } },
        ] satisfies PaletteActionItem[]}
      />
      {sessionDialog && (
        <div className={cn('modal-backdrop', backdropOverlay, 'p-3 sm:p-5')}>
          <section className="connection-modal w-[min(100%,440px)] rounded-[16px] border border-line bg-canvas p-[18px] sm:p-6 shadow-elev-lg animate-[arrive_.18s_ease_both]" role="dialog" aria-modal="true" aria-labelledby="session-dialog-title">
            <h2 id="session-dialog-title" className="m-0 mb-2 text-[19px] font-semibold tracking-[-0.02em] text-ink">
              {t(sessionDialog.kind === 'rename' ? 'Rename conversation' : sessionDialog.kind === 'delete' ? 'Delete conversation?' : 'Rewind conversation')}
            </h2>
            {sessionDialog.kind === 'delete' ? (
              <p className="m-0 mb-3 text-[13.5px] leading-[1.6] text-ink-secondary">{t('This removes the conversation and its saved history.')}</p>
            ) : (
              <label className="form-field mb-3 flex flex-col gap-1.5 text-[13px] font-medium text-ink-secondary">
                {t(sessionDialog.kind === 'rename' ? 'Title' : 'User turns to remove')}
                <input
                  className={cn(fieldInput, 'h-[38px]')}
                  value={sessionDialog.value}
                  type={sessionDialog.kind === 'rewind' ? 'number' : 'text'}
                  min={1}
                  onChange={(event) => setSessionDialog({ ...sessionDialog, value: event.target.value })}
                />
              </label>
            )}
            {Boolean(sessionDialog.excerpts?.length) && (
              <ol className="rewind-excerpts m-0 mb-3 max-h-[160px] overflow-auto pl-6 text-[13px] leading-[1.55] text-ink-secondary [overflow-wrap:anywhere]">
                {sessionDialog.excerpts?.map((excerpt, index) => <li key={index} className="mb-1.5">{excerpt}</li>)}
              </ol>
            )}
            {sessionDialog.removed !== undefined && (
              <p className="m-0 mb-3 text-[13px] text-ink-secondary">{sessionDialog.removed} {t('turn(s) will be removed from the end of the conversation.')}</p>
            )}
            {actionError && <div className={cn('form-error mb-3', bannerError)} role="alert">{actionError}</div>}
            <div className="modal-footer mt-4 flex items-center justify-end gap-2.5 border-t border-line-subtle pt-4 text-[13px]">
              <button className={btnPlain} disabled={working} onClick={() => setSessionDialog(null)}>{t('Cancel')}</button>
              <button
                className={sessionDialog.kind === 'delete' ? btnPrimaryDanger : btnPrimaryCompact}
                disabled={working || sessionDialog.removed === 0}
                onClick={confirmSessionDialog}
              >
                {t(sessionDialog.kind === 'rename' ? 'Save' : sessionDialog.kind === 'delete' ? 'Delete' : sessionDialog.digest ? 'Rewind' : 'Preview')}
              </button>
            </div>
          </section>
        </div>
      )}
      {editResendConfirmation && (
        <div className={cn('modal-backdrop', backdropOverlay, 'p-3 sm:p-5')}>
          <section className="connection-modal w-[min(100%,480px)] rounded-[16px] border border-line bg-canvas p-[18px] sm:p-6 shadow-elev-lg animate-[arrive_.18s_ease_both]" role="dialog" aria-modal="true" aria-labelledby="edit-resend-title">
            <h2 id="edit-resend-title" className="m-0 mb-2 text-[19px] font-semibold tracking-[-0.02em] text-ink">
              {t('Replace this message and stop current work?')}
            </h2>
            <p className="m-0 text-[13.5px] leading-[1.6] text-ink-secondary">
              {t('This will stop the current run, cancel queued turns, remove the selected message and everything after it, then resend your edited message.')}
            </p>
            {actionError && <div className={cn('form-error mt-3', bannerError)} role="alert">{actionError}</div>}
            <div className="modal-footer mt-4 flex items-center justify-end gap-2.5 border-t border-line-subtle pt-4 text-[13px]">
              <button className={btnPlain} disabled={working} onClick={() => setEditResendConfirmation(null)}>{t('Cancel')}</button>
              <button className={btnPrimaryDanger} disabled={working} onClick={confirmEditAndResend}>{t('Stop & Resend')}</button>
            </div>
          </section>
        </div>
      )}
    </div>
  )
}

function QuestionForm({ request, disabled, canSaveDraft, onSaveDraft, onSubmit }: {
  request: NonNullable<Pending['userQuestion']>
  disabled: boolean
  canSaveDraft: boolean
  onSaveDraft?: (draft: UserQuestionDraft) => Promise<void>
  onSubmit: (answers: Array<{ question_id: string; answer: string }>) => void
}) {
  const { t } = useTranslation()
  const questions = request.questions ?? []
  const [answers, setAnswers] = useState<Record<string, string>>(() => {
    const initial: Record<string, string> = {}
    for (const resp of request.draft?.responses ?? []) {
      const q = questions.find((item) => item.id === resp.question_id)
      const val = resp.custom_answer || resp.notes || (
        q?.options && resp.selected_option !== undefined && resp.selected_option >= 0 && resp.selected_option < q.options.length
          ? q.options[resp.selected_option].label
          : ''
      )
      if (val) initial[resp.question_id] = val
    }
    return initial
  })
  const [draftState, setDraftState] = useState<'idle' | 'saving' | 'saved'>(() =>
    (request.draft?.responses?.length ? 'saved' : 'idle'),
  )
  const mountedRef = useRef(false)
  const saveDraftRef = useRef(onSaveDraft)
  saveDraftRef.current = onSaveDraft

  useEffect(() => {
    if (!canSaveDraft || !saveDraftRef.current) return
    if (!mountedRef.current) {
      mountedRef.current = true
      return
    }
    const timer = window.setTimeout(() => {
      const responses = questions
        .filter((q) => (answers[q.id] ?? '') !== '')
        .map((q) => {
          const val = answers[q.id] ?? ''
          const optIdx = q.options ? q.options.findIndex((o) => o.label === val) : -1
          return {
            question_id: q.id,
            ...(optIdx >= 0 ? { selected_option: optIdx } : {}),
            custom_answer: val,
            confirmed: false,
          }
        })
      setDraftState('saving')
      void saveDraftRef.current!({
        current_question_id: questions[0]?.id,
        responses,
      })
        .then(() => setDraftState('saved'))
        .catch(() => setDraftState('idle'))
    }, 400)
    return () => window.clearTimeout(timer)
  }, [answers, canSaveDraft, request.id])

  const complete = questions.length > 0 && questions.every((question) => (answers[question.id] ?? '').trim())
  return (
    <div className="question-form flex flex-col gap-3">
      {questions.map((question) => (
        <fieldset className="question-item m-0 flex flex-col gap-2 border-0 p-0" key={question.id}>
          <legend className="mb-1.5 text-[13.5px] font-semibold text-ink">{question.question}</legend>
          {question.options?.length ? (
            <div className="question-options flex flex-col gap-1.5">
              {question.options.map((option) => {
                const chosen = answers[question.id] === option.label
                return (
                  <label
                    className={cn(
                      'question-option flex cursor-pointer items-start gap-2.5 rounded-[9px] border px-3 py-2 text-[13px] transition-[background,border-color] duration-120',
                      chosen ? 'chosen border-accent bg-canvas' : 'border-line-subtle bg-canvas hover:border-line hover:bg-hover',
                    )}
                    key={option.label}
                  >
                    <input
                      type="radio"
                      className="mt-1 accent-ink"
                      name={question.id}
                      value={option.label}
                      checked={chosen}
                      onChange={() => setAnswers((value) => ({ ...value, [question.id]: option.label }))}
                    />
                    <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                      <strong className="flex items-center gap-2 text-[13px] font-medium text-ink">
                        {option.label}
                        {option.recommended && <em className="rounded bg-muted px-1.5 py-[1px] text-[10.5px] font-semibold not-italic text-ink-secondary">{t('Suggested')}</em>}
                      </strong>
                      {option.description && <small className="text-[12px] text-ink-muted">{option.description}</small>}
                    </span>
                  </label>
                )
              })}
            </div>
          ) : (
            <input
              className={cn('text-answer', fieldInput)}
              value={answers[question.id] ?? ''}
              onChange={(event) => setAnswers((value) => ({ ...value, [question.id]: event.target.value }))}
              placeholder={t('Your answer')}
            />
          )}
        </fieldset>
      ))}
      {!questions.length && <p className="pending-reason m-0 text-[13px] text-ink-secondary">{t('This clarification needs an answer, but Core did not provide question details.')}</p>}
      <div className="pending-actions flex items-center justify-end gap-2">
        {canSaveDraft && draftState === 'saved' && <small className="draft-saved-note font-mono text-[11.5px] text-ink-muted">{t('Draft saved')}</small>}
        <button className={btnPrimaryCompact} disabled={disabled || !complete} onClick={() => onSubmit(questions.map((question) => ({ question_id: question.id, answer: answers[question.id].trim() })))}>
          <Check size={14} />{t('Send answer')}
        </button>
      </div>
    </div>
  )
}

function InteractionForm({ request, disabled, onSubmit }: {
  request: NonNullable<Pending['interaction']>
  disabled: boolean
  onSubmit: (value: unknown) => void
}) {
  const { t } = useTranslation()
  const [value, setValue] = useState('{\n  "action": "accept",\n  "content": {}\n}')
  const [error, setError] = useState('')
  return (
    <div className="interaction-form flex flex-col gap-2.5">
      <p className="pending-reason m-0 text-[13px] text-ink-secondary">{request.serverName} {t('is asking for input.')}</p>
      <pre className="json-preview m-0 max-h-[200px] overflow-auto rounded-[9px] border border-line-subtle bg-code p-3 font-mono text-[12px] text-ink-secondary">{JSON.stringify(request.request, null, 2)}</pre>
      <label className="interaction-label text-[12.5px] font-medium text-ink-secondary">{t('Response JSON')}</label>
      <textarea
        className={cn(fieldTextarea, 'min-h-[96px] font-mono text-[12px]')}
        value={value}
        onChange={(event) => { setValue(event.target.value); setError('') }}
        spellCheck={false}
      />
      <div className="pending-actions flex items-center justify-end gap-2">
        {error && <small className="inline-error text-[12px] text-danger">{t(error)}</small>}
        <button className={btnPrimaryCompact} disabled={disabled} onClick={() => { try { onSubmit(JSON.parse(value)) } catch { setError('Enter a valid JSON response.') } }}>
          <Check size={14} />{t('Send response')}
        </button>
      </div>
    </div>
  )
}

function EffortBars({ bars, max = false }: { bars: number; max?: boolean }) {
  const heights = [3, 5.5, 8, 10.5]
  return (
    <svg className={cn('effort-bars block shrink-0', max && 'is-max')} width="13" height="12" viewBox="0 0 13 12" aria-hidden="true">
      {heights.map((h, i) => (
        <rect
          key={i}
          x={i * 3.2 + 0.4}
          y={11.5 - h}
          width="2"
          height={h}
          rx="0.9"
          fill="currentColor"
          opacity={i < bars ? 1 : 0.22}
        />
      ))}
    </svg>
  )
}

function EffortSlider({
  label,
  value,
  disabled,
  onCommit,
}: {
  label: string
  value: string
  disabled: boolean
  onCommit: (next: string) => void
}) {
  const trackRef = useRef<HTMLDivElement>(null)
  const [dragPos, setDragPos] = useState<number | null>(null)
  const [dragging, setDragging] = useState(false)

  const committed = effortDisplayInfo(value)
  const maxIndex = EFFORT_TIERS.length - 1
  const displayPos = dragPos ?? committed.idx
  const clampedPos = Math.max(0, Math.min(maxIndex, displayPos))
  const pct = (clampedPos / maxIndex) * 100
  const activeIdx = dragPos !== null ? Math.round(clampedPos) : committed.idx
  const previewTier = EFFORT_TIERS[activeIdx] ?? EFFORT_TIERS[0]
  const displayLabel = dragPos !== null ? previewTier.label : committed.label
  const displayBars = dragPos !== null ? previewTier.bars : committed.bars
  const isMax = activeIdx === maxIndex

  const posFromClientX = (clientX: number): number => {
    const el = trackRef.current
    if (!el) return committed.idx
    const rect = el.getBoundingClientRect()
    if (rect.width <= 0) return committed.idx
    return Math.max(0, Math.min(maxIndex, ((clientX - rect.left) / rect.width) * maxIndex))
  }

  return (
    <div
      className={cn(
        'effort-slider-section mt-0.5 flex flex-col gap-[7px] rounded-[10px] border border-transparent border-t-line-subtle px-3 pt-2.5 pb-[9px] transition-[background,border-color] duration-180',
        isMax ? 'is-max border-line-strong bg-[light-dark(#f8f8f9,#2c2d33)]' : 'bg-subtle',
      )}
    >
      <div className="effort-slider-head flex items-center justify-between gap-2 text-[12px] font-semibold text-ink-secondary">
        <span>{label}</span>
        <span
          className={cn(
            'effort-value-pill inline-flex items-center gap-[5px] rounded-full border px-2 py-0.5 text-[11px] font-semibold',
            isMax
              ? 'is-max-pill border-accent bg-accent tracking-[0.04em] text-accent-fg animate-[detentLock_220ms_cubic-bezier(.22,1,.36,1)_1]'
              : 'border-line bg-canvas text-ink',
          )}
        >
          <EffortBars bars={displayBars} max={isMax} />
          <span>{displayLabel}</span>
        </span>
      </div>
      <div
        ref={trackRef}
        role="slider"
        tabIndex={disabled ? -1 : 0}
        aria-label={label}
        aria-valuemin={0}
        aria-valuemax={maxIndex}
        aria-valuenow={activeIdx}
        aria-valuetext={displayLabel}
        aria-disabled={disabled || undefined}
        className={cn(
          'effort-track-hit relative flex h-6 select-none touch-none items-center px-[7px]',
          dragging && 'dragging',
          disabled ? 'disabled cursor-not-allowed opacity-50' : 'cursor-pointer',
        )}
        onPointerDown={(event) => {
          if (disabled) return
          event.preventDefault()
          event.currentTarget.setPointerCapture(event.pointerId)
          setDragging(true)
          setDragPos(posFromClientX(event.clientX))
        }}
        onPointerMove={(event) => {
          if (!dragging || disabled) return
          setDragPos(posFromClientX(event.clientX))
        }}
        onPointerUp={(event) => {
          if (!dragging) return
          if (event.currentTarget.hasPointerCapture(event.pointerId)) {
            event.currentTarget.releasePointerCapture(event.pointerId)
          }
          const finalIdx = Math.round(posFromClientX(event.clientX))
          const next = EFFORT_TIERS[finalIdx]?.value ?? ''
          setDragging(false)
          setDragPos(null)
          if (next !== value) onCommit(next)
        }}
        onPointerCancel={() => {
          setDragging(false)
          setDragPos(null)
        }}
        onKeyDown={(event) => {
          if (disabled) return
          let nextIdx: number | null = null
          if (event.key === 'ArrowRight' || event.key === 'ArrowUp') nextIdx = Math.min(maxIndex, committed.idx + 1)
          else if (event.key === 'ArrowLeft' || event.key === 'ArrowDown') nextIdx = Math.max(0, committed.idx - 1)
          else if (event.key === 'Home') nextIdx = 0
          else if (event.key === 'End') nextIdx = maxIndex
          if (nextIdx !== null) {
            event.preventDefault()
            const next = EFFORT_TIERS[nextIdx].value
            if (next !== value) onCommit(next)
          }
        }}
      >
        <div className="effort-track relative h-[3px] w-full rounded-full bg-line">
          <div className="effort-track-fill absolute inset-y-0 left-0 rounded-[inherit] bg-ink transition-[width] duration-180 ease-out" style={{ width: `${pct}%` }} />
          {EFFORT_TIERS.map((tier, index) => {
            const passed = clampedPos >= index - 0.05
            return (
              <span
                key={tier.value || 'auto'}
                className={cn(
                  'effort-detent pointer-events-none absolute top-1/2 h-[5px] w-[5px] -translate-x-1/2 -translate-y-1/2 rounded-full border-[1.5px] transition-[border-color,background] duration-140',
                  passed ? 'passed border-ink bg-ink' : 'border-line-strong bg-canvas',
                  index === maxIndex && 'is-end',
                )}
                style={{ left: `${(index / maxIndex) * 100}%` }}
                aria-hidden="true"
              />
            )
          })}
          <span
            className={cn(
              'effort-thumb pointer-events-none absolute top-1/2 -translate-x-1/2 -translate-y-1/2 rounded-full transition-[left,transform,box-shadow] duration-180 ease-out',
              dragging && 'grabbing',
              isMax
                ? 'is-max h-[15px] w-[15px] border-2 border-canvas bg-ink shadow-[0_0_0_1.5px_var(--ink-primary)]'
                : 'h-[14px] w-[14px] border-2 border-ink bg-canvas shadow-elev-sm',
            )}
            style={{ left: `${pct}%` }}
            aria-hidden="true"
          />
        </div>
      </div>
      <div className="effort-ticks grid grid-cols-5 gap-0.5">
        {EFFORT_TIERS.map((tier, index) => {
          const active = index === activeIdx
          const isMaxTick = tier.value === 'max'
          return (
            <button
              key={tier.value || 'auto'}
              type="button"
              disabled={disabled}
              className={cn(
                'effort-tick cursor-pointer whitespace-nowrap rounded-[6px] border-0 px-0.5 py-1 text-center text-[11px] transition-[background,color] duration-120',
                active && isMaxTick
                  ? 'active is-max-tick bg-accent font-semibold text-accent-fg'
                  : active
                    ? 'active bg-active font-semibold text-ink'
                    : isMaxTick
                      ? 'is-max-tick bg-transparent font-medium text-ink-muted hover:not-disabled:bg-hover hover:not-disabled:text-ink'
                      : 'bg-transparent font-medium text-ink-muted hover:not-disabled:bg-hover hover:not-disabled:text-ink',
              )}
              onClick={() => {
                if (tier.value !== value) onCommit(tier.value)
              }}
            >
              {tier.label}
            </button>
          )
        })}
      </div>
    </div>
  )
}
