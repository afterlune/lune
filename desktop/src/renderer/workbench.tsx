import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { Folder, FolderOpen, KeyRound, Plus, Settings, ShieldAlert, X } from 'lucide-react'
import type { WorkspaceSessionRecord } from '../shared/desktop-settings'
import type { ThemePreference } from '../shared/theme'
import App from './App'
import { useTranslation } from 'react-i18next'
import { useHostLanguage } from './language'
import {
  clearHostToken, detectWebHost, loadLoopbackToken, loadSavedHostToken,
  loadSavedWebConnection, saveHostToken, verifyHostToken,
} from './loopback'
import type { RuntimeConnection } from './runtime'
import { createWebHostSettingsAPI, type FrontendSettingsAPI, type SaveConfigResult } from './settings-api'
import { SettingsModal } from './settings-modal'
import { applyTheme } from './theme'
import { bannerError, btnIcon, btnPlain, btnPrimary, btnPrimaryCompact, btnSecondary, cn, fieldInput } from './ui'

interface Workspace {
  id: string
  path: string
  name?: string
  lastSessionId?: string
  createdAt?: number
  updatedAt?: number
  lastOpenedAt?: number
  sessions?: WorkspaceSessionRecord[]
}
interface WorkspacesResponse {
  initial: string
  items: Workspace[]
  recent?: Array<{ path: string; lastUsed: string }>
  needsSetup?: boolean
  defaultPath?: string
  language?: string
  theme?: string
}
interface Directories { path: string; parent: string; directories: string[]; recent: string[] }

export type WorkspaceActivity = 'Needs attention' | 'Running' | 'Queued' | 'Paused' | ''

export interface ManagedWorkspace {
  workspace: Workspace
  options: Extract<RuntimeConnection, { mode: 'loopback' | 'remote' }>
  active: boolean
  navigation: ReactNode
  report: (id: string, status: WorkspaceActivity) => void
  settingsAPI: FrontendSettingsAPI
  openDirectoryPicker: (openFilesAfter?: boolean) => void
  focusFilesToken: number
  workspaceRevision: number
  reconnectWorkspace?: () => Promise<void>
  isRemoteOverride?: boolean
  onConnectRemote?: (options: { url: string; token: string; name?: string; rememberToken?: boolean }) => Promise<void>
  onReturnToLocal?: () => Promise<void>
  workspaces?: Workspace[]
  activityByWorkspace?: Record<string, WorkspaceActivity>
  workspaceSessions?: Record<string, WorkspaceSessionRecord[]>
  onSelectWorkspace?: (id: string) => void
  onSelectWorkspaceSession?: (workspaceId: string, sessionId: string) => void
  onCreateWorkspaceSession?: (workspaceId: string) => void
  onCloseWorkspace?: (workspaceId: string) => void
  onRememberSession?: (workspaceId: string, sessionId: string) => void
  onSyncSessions?: (workspaceId: string) => void
  onReportSessions?: (workspaceId: string, sessions: WorkspaceSessionRecord[]) => void
  requestedSession?: { sessionId: string; seq: number } | null
  newSessionToken?: number
}

const selectionKey = 'lune:active-workspace'

function nameOf(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) || path
}

async function localRequest<T>(credential: string, method: string, params: Record<string, unknown> = {}): Promise<T> {
  const response = await fetch('/_lune/' + method, {
    method: 'POST', mode: 'same-origin', credentials: 'omit', redirect: 'error',
    headers: { Authorization: 'Bearer ' + credential, 'Content-Type': 'application/json' },
    body: JSON.stringify(params),
  })
  const text = await response.text()
  if (!response.ok) {
    let message = text.trim() || 'Could not open workspace.'
    try {
      const parsed = JSON.parse(message) as { error?: string; needsSetup?: boolean }
      if (parsed.error) {
        const err = new Error(parsed.error) as Error & { needsSetup?: boolean }
        err.needsSetup = Boolean(parsed.needsSetup)
        throw err
      }
    } catch (e) {
      if (e instanceof Error && 'needsSetup' in e) throw e
    }
    throw new Error(message)
  }
  return JSON.parse(text) as T
}

export default function WebApp({ theme }: { theme: ThemePreference }) {
  return <Workbench theme={theme} />
}

function Workbench({ theme }: { theme: ThemePreference }) {
  const { t } = useTranslation()
  const bindHostLanguage = useHostLanguage()
  const [currentTheme, setTheme] = useState(theme)
  const [initializing, setInitializing] = useState(true)
  const [pairingError, setPairingError] = useState('')
  const [hostAuthMode, setHostAuthMode] = useState<'none' | 'pair' | 'token'>('none')
  const [authInput, setAuthInput] = useState('')
  const [authRemember, setAuthRemember] = useState(true)
  const [authError, setAuthError] = useState('')
  const [credential, setCredential] = useState('')
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [selected, setSelected] = useState('')
  const [needsSetup, setNeedsSetup] = useState(false)
  const [defaultPath, setDefaultPath] = useState('')
  const [activity, setActivity] = useState<Record<string, WorkspaceActivity>>({})
  const [workspaceSessions, setWorkspaceSessions] = useState<Record<string, WorkspaceSessionRecord[]>>({})
  const [requestedSession, setRequestedSession] = useState<{ workspaceId: string; sessionId: string; seq: number } | null>(null)
  const [newSessionRequest, setNewSessionRequest] = useState<Record<string, number>>({})
  const [picker, setPicker] = useState<{ openFilesAfter: boolean } | null>(null)
  const [focusFilesRequest, setFocusFilesRequest] = useState<{ id: string; seq: number } | null>(null)
  const [workspaceRevision, setWorkspaceRevision] = useState(0)
  const [setupSettingsOpen, setSetupSettingsOpen] = useState(false)
  const [remoteOverride, setRemoteOverride] = useState<null | { url: string; token: string; name?: string }>(null)

  const syncWorkspaces = useCallback(async (token: string, preferredPath?: string) => {
    const result = await localRequest<WorkspacesResponse>(token, 'workspaces')
    bindHostLanguage(result.language, (language) => localRequest(token, 'preferences', { language }))
    if (result.theme === 'system' || result.theme === 'light' || result.theme === 'dark') {
      setTheme(result.theme)
      applyTheme(result.theme)
    }
    let preferredId: string | null = null
    if (preferredPath) {
      preferredId = result.items.find((item) => item.path === preferredPath)?.id ?? null
    }
    if (!preferredId) {
      try { preferredId = sessionStorage.getItem(selectionKey) } catch { /* use initial workspace */ }
    }
    setWorkspaces(result.items)
    setWorkspaceSessions((prev) => {
      const next = { ...prev }
      for (const item of result.items) {
        if (Array.isArray(item.sessions)) {
          next[item.id] = item.sessions
        }
      }
      return next
    })
    setNeedsSetup(Boolean(result.needsSetup))
    if (result.defaultPath) setDefaultPath(result.defaultPath)
    setSelected(result.items.some((item) => item.id === preferredId) ? preferredId! : result.initial)
    return result
  }, [bindHostLanguage])

  useEffect(() => {
    let active = true
    void (async () => {
      const loopbackToken = await loadLoopbackToken()
      if (loopbackToken) {
        if (!active) return
        setHostAuthMode('pair')
        await syncWorkspaces(loopbackToken)
        if (!active) return
        setCredential(loopbackToken)
        return
      }
      const status = await detectWebHost()
      if (!active) return
      if (!status) {
        setHostAuthMode('none')
        return
      }
      setHostAuthMode(status.auth)
      if (status.auth === 'token') {
        const saved = loadSavedHostToken() || loadSavedWebConnection()?.token
        if (saved) {
          const check = await verifyHostToken(saved)
          if (check.ok) {
            if (!active) return
            saveHostToken(saved, true)
            await syncWorkspaces(saved)
            if (!active) return
            setCredential(saved)
          }
        }
      }
    })().catch((error: Error) => {
      if (active) setPairingError(error.message)
    }).finally(() => { if (active) setInitializing(false) })
    return () => { active = false }
  }, [syncWorkspaces])

  useEffect(() => {
    if (!credential) return
    let stopped = false
    let timer = 0
    const refresh = async () => {
      await syncWorkspaces(credential).catch(() => undefined)
      if (!stopped) timer = window.setTimeout(() => void refresh(), 15_000)
    }
    timer = window.setTimeout(() => void refresh(), 15_000)
    return () => {
      stopped = true
      window.clearTimeout(timer)
    }
  }, [credential, syncWorkspaces])

  const unlockHost = async (event: FormEvent) => {
    event.preventDefault()
    const tok = authInput.trim()
    if (!tok) return
    setAuthError('')
    setInitializing(true)
    try {
      const check = await verifyHostToken(tok)
      if (!check.ok) {
        clearHostToken()
        setAuthError(t(check.error || 'Invalid access token.'))
        return
      }
      saveHostToken(tok, authRemember)
      await syncWorkspaces(tok)
      setCredential(tok)
      setAuthInput('')
    } catch (err) {
      setAuthError(err instanceof Error ? err.message : String(err))
    } finally {
      setInitializing(false)
    }
  }

  const select = useCallback((id: string) => {
    setRemoteOverride(null)
    setSelected(id)
    try { sessionStorage.setItem(selectionKey, id) } catch { /* current tab selection still works */ }
    if (credential) {
      void localRequest(credential, 'workspaces/select', { id }).catch(() => undefined)
    }
  }, [credential])

  const selectWorkspaceSession = useCallback((workspaceId: string, sessionId: string) => {
    select(workspaceId)
    setRequestedSession((prev) => ({ workspaceId, sessionId, seq: (prev?.seq ?? 0) + 1 }))
  }, [select])

  const createWorkspaceSession = useCallback((workspaceId: string) => {
    select(workspaceId)
    setNewSessionRequest((prev) => ({ ...prev, [workspaceId]: (prev[workspaceId] ?? 0) + 1 }))
  }, [select])

  const rememberWorkspaceSession = useCallback((workspaceId: string, sessionId: string) => {
    setWorkspaces((prev) => prev.map((w) => w.id === workspaceId ? { ...w, lastSessionId: sessionId } : w))
    if (credential) {
      void localRequest(credential, 'workspaces/active-session', { id: workspaceId, sessionId }).catch(() => undefined)
    }
  }, [credential])

  const syncWorkspaceSessions = useCallback((workspaceId: string) => {
    if (!credential) return
    void localRequest<{ sessions?: WorkspaceSessionRecord[] }>(credential, 'workspaces/sessions/sync', { id: workspaceId })
      .then((res) => {
        if (Array.isArray(res.sessions)) {
          setWorkspaceSessions((prev) => ({ ...prev, [workspaceId]: res.sessions! }))
        }
      })
      .catch(() => undefined)
  }, [credential])

  const reportWorkspaceSessions = useCallback((workspaceId: string, list: WorkspaceSessionRecord[]) => {
    setWorkspaceSessions((prev) => {
      const saved = new Map((prev[workspaceId] ?? []).map((item) => [item.sessionId, item]))
      return {
        ...prev,
        [workspaceId]: list.map((item) => ({
          ...item,
          taskStatus: item.taskStatus ?? saved.get(item.sessionId)?.taskStatus,
          executionId: item.executionId ?? saved.get(item.sessionId)?.executionId,
          queueCount: item.queueCount ?? saved.get(item.sessionId)?.queueCount,
        })),
      }
    })
  }, [])

  const report = useCallback((id: string, status: WorkspaceActivity) => {
    setActivity((previous) => previous[id] === status ? previous : { ...previous, [id]: status })
  }, [])

  const openDirectoryPicker = useCallback((openFilesAfter = false) => {
    setPicker({ openFilesAfter })
  }, [])

  const currentWorkspacePath = workspaces.find((item) => item.id === selected)?.path || defaultPath

  const handleReloaded = useCallback(async (_result: SaveConfigResult) => {
    if (!credential) return
    await syncWorkspaces(credential, currentWorkspacePath)
    setWorkspaceRevision((rev) => rev + 1)
  }, [credential, currentWorkspacePath, syncWorkspaces])

  const settingsAPI = useMemo(() => createWebHostSettingsAPI(credential, handleReloaded), [credential, handleReloaded])

  const handleCloseWorkspace = async (id: string) => {
    try {
      await localRequest(credential, 'close-workspace', { id })
      setPairingError('')
      await syncWorkspaces(credential)
    } catch (err) {
      setPairingError(err instanceof Error ? err.message : String(err))
    }
  }

  const handleThemeChange = (nextTheme: ThemePreference) => {
    setTheme(nextTheme)
    applyTheme(nextTheme)
    if (credential) {
      void localRequest(credential, 'preferences', { theme: nextTheme }).catch(() => undefined)
    }
  }

  if (initializing) {
    return (
      <main className="web-launch flex min-h-screen items-center justify-center p-6 text-center text-[14px] text-ink-secondary">
        <p role="status">{t('Connecting to local Core…')}</p>
      </main>
    )
  }

  if (!credential) {
    if (hostAuthMode === 'token') {
      return (
        <div className="connect-page relative flex min-h-screen items-center justify-center bg-canvas px-4 py-16 sm:px-6">
          <main className="connect-card relative z-[1] w-[min(100%,430px)] rounded-[18px] border border-line bg-canvas px-[34px] pt-9 pb-7 shadow-elev-md animate-[arrive_.25s_ease_both]">
            <h1 className="m-0 mb-2 text-[20px] font-semibold tracking-[-0.02em] text-ink">{t('Unlock Lune Workspace')}</h1>
            <p className="connect-intro m-0 mb-5 text-[13.5px] leading-[1.6] text-ink-secondary">
              {t('Enter the access token for this Lune host to access workspaces and SQLite settings.')}
            </p>
            <form onSubmit={unlockHost} className="space-y-4">
              <div className="form-field flex flex-col gap-1.5 text-[13px] font-medium text-ink-secondary">
                <label>{t('Access token')}</label>
                <input
                  type="password"
                  autoFocus
                  className={fieldInput}
                  value={authInput}
                  onChange={(e) => setAuthInput(e.target.value)}
                  placeholder={t('Paste your Core token')}
                  autoComplete="off"
                />
                <label className="remember-token mt-1 flex cursor-pointer items-center gap-2 text-[12.5px] font-normal text-ink-secondary">
                  <input type="checkbox" className="accent-ink" checked={authRemember} onChange={(e) => setAuthRemember(e.target.checked)} />
                  {t('Remember token in this browser')}
                </label>
              </div>
              {authError && (
                <div className={cn('form-error flex items-center gap-2', bannerError)} role="alert">
                  <ShieldAlert size={16} /><span>{authError}</span>
                </div>
              )}
              <button type="submit" className={cn('primary-button connect-button w-full', btnPrimary)} disabled={!authInput.trim()}>
                <KeyRound size={16} />{t('Connect')}
              </button>
            </form>
          </main>
        </div>
      )
    }
    return <App initialTheme={theme} pairingError={pairingError} />
  }

  const wsScheme = location.protocol === 'https:' ? 'wss' : 'ws'

  const navigation = (
    <section className="workspace-list mb-3 grid gap-0.5" aria-label={t('Workspaces')}>
      <div className="workspace-list-heading flex select-none items-center justify-between px-2.5 pt-0.5 pb-1 text-[11.5px] font-medium text-ink-muted">
        <span>{t('Workspaces')}</span>
        <button
          type="button"
          className={cn('icon-button', btnIcon)}
          aria-label={t('Open workspace')}
          title={t('Open workspace')}
          onClick={() => openDirectoryPicker(false)}
        >
          <Plus size={16} />
        </button>
      </div>
      {pairingError && (
        <div className={cn('form-error workspace-list-error mx-2 mb-2 flex items-center gap-1.5 px-2 py-[7px] text-[11px]', bannerError)} role="alert">
          <ShieldAlert size={15} className="shrink-0" /><span>{t(pairingError)}</span>
        </div>
      )}
      {workspaces.map((item) => {
        const isSelected = !remoteOverride && item.id === selected
        return (
          <div
            className={cn(
              'workspace-row group/ws flex items-center rounded-[9px] transition-[background] duration-120 hover:bg-hover focus-within:bg-hover',
              isSelected && 'selected bg-active',
            )}
            key={item.id}
          >
            <button
              type="button"
              className={cn(
                'workspace-row-btn flex min-h-[33px] min-w-0 flex-1 cursor-pointer items-center gap-2 rounded-[9px] border-0 bg-transparent px-2.5 py-[5px] text-left text-[13.5px] text-ink',
                isSelected && 'font-medium',
              )}
              aria-pressed={isSelected}
              title={item.path}
              onClick={() => select(item.id)}
            >
              <Folder size={15} className={cn('shrink-0', isSelected ? 'text-ink' : 'text-ink-secondary')} />
              <span className="min-w-0 flex-1 truncate">{nameOf(item.path)}</span>
              {activity[item.id] && (
                <small className="workspace-activity inline-flex items-center gap-[5px] whitespace-nowrap text-[11.5px] font-normal text-ink-muted">
                  <i className="h-1.5 w-1.5 rounded-full bg-ink animate-[pulse_1.5s_infinite]" aria-hidden="true" />
                  {t(activity[item.id])}
                </small>
              )}
            </button>
            <button
              type="button"
              className="workspace-close-btn mr-1 inline-grid h-6 w-6 cursor-pointer place-items-center rounded-[6px] border-0 bg-transparent text-ink-muted opacity-0 transition-[opacity,background,color] duration-120 hover:bg-active hover:text-ink group-hover/ws:opacity-100 group-focus-within/ws:opacity-100"
              aria-label={`${t('Close')} ${nameOf(item.path)}`}
              title={t('Close workspace')}
              onClick={(e) => { e.stopPropagation(); void handleCloseWorkspace(item.id) }}
            >
              <X size={13} />
            </button>
          </div>
        )
      })}
      {!workspaces.length && (
        <button
          type="button"
          className="workspace-row-btn flex min-h-[33px] w-full cursor-pointer items-center gap-2 rounded-[9px] border-0 bg-transparent px-2.5 py-[5px] text-left text-[13.5px] text-ink hover:bg-hover"
          onClick={() => openDirectoryPicker(true)}
        >
          <FolderOpen size={15} className="shrink-0 text-ink-secondary" /><span className="min-w-0 flex-1 truncate">{t('Choose a workspace')}</span>
        </button>
      )}
    </section>
  )

  const activeWorkspaces = remoteOverride ? [] : workspaces

  return <>
    {remoteOverride && (
      <App
        key={`remote:${remoteOverride.url}`}
        initialTheme={currentTheme}
        onThemeChange={handleThemeChange}
        managed={{
          workspace: { id: 'remote', path: remoteOverride.url },
          active: true,
          report,
          navigation,
          settingsAPI,
          openDirectoryPicker,
          focusFilesToken: 0,
          workspaceRevision,
          isRemoteOverride: true,
          onConnectRemote: async (opts) => setRemoteOverride({ url: opts.url, token: opts.token, name: opts.name }),
          onReturnToLocal: async () => setRemoteOverride(null),
          options: { mode: 'remote', url: remoteOverride.url, token: remoteOverride.token, name: remoteOverride.name },
        }}
      />
    )}

    {!remoteOverride && activeWorkspaces.map((workspace) => (
      <div key={workspace.id} hidden={workspace.id !== selected}>
        <App
          initialTheme={currentTheme}
          onThemeChange={handleThemeChange}
          managed={{
            workspace,
            active: workspace.id === selected,
            report,
            navigation,
            settingsAPI,
            openDirectoryPicker,
            focusFilesToken: focusFilesRequest?.id === workspace.id ? focusFilesRequest.seq : 0,
            workspaceRevision,
            workspaces,
            activityByWorkspace: activity,
            workspaceSessions,
            onSelectWorkspace: select,
            onSelectWorkspaceSession: selectWorkspaceSession,
            onCreateWorkspaceSession: createWorkspaceSession,
            onCloseWorkspace: (id) => { void handleCloseWorkspace(id) },
            onRememberSession: rememberWorkspaceSession,
            onSyncSessions: syncWorkspaceSessions,
            onReportSessions: reportWorkspaceSessions,
            requestedSession: requestedSession?.workspaceId === workspace.id
              ? { sessionId: requestedSession.sessionId, seq: requestedSession.seq }
              : null,
            newSessionToken: newSessionRequest[workspace.id] ?? 0,
          reconnectWorkspace: async () => {
            await localRequest(credential, 'reload-workspace', { id: workspace.id })
            setPairingError('')
            await syncWorkspaces(credential, workspace.path)
            setWorkspaceRevision((rev) => rev + 1)
          },
            onConnectRemote: async (opts) => setRemoteOverride({ url: opts.url, token: opts.token, name: opts.name }),
            options: { mode: 'loopback', token: credential, url: `${wsScheme}://${location.host}/v1/workspaces/${workspace.id}/runtime` },
          }}
        />
      </div>
    ))}

    {!remoteOverride && activeWorkspaces.length === 0 && (
      <div className="app-shell web-workbench relative grid h-dvh min-h-0 bg-canvas">
        <aside className="sidebar flex h-full min-h-0 flex-col overflow-hidden bg-sidebar px-2.5 pt-2.5 pb-3" aria-label={t('Conversations')}>
          <div className="sidebar-scroll-region min-h-0 flex-1 overflow-y-auto">{navigation}</div>
          <div className="sidebar-bottom mt-2 pt-2">
            <button
              type="button"
              className="settings-button flex h-9 w-full cursor-pointer items-center gap-2.5 rounded-[9px] border-0 bg-transparent px-2.5 text-[13.5px] font-medium text-ink transition-[background] duration-120 hover:bg-hover"
              onClick={() => setSetupSettingsOpen(true)}
            >
              <Settings size={15} /><span>{t('Settings')}</span>
            </button>
          </div>
        </aside>
        <main className="chat-area flex min-h-0 min-w-0 flex-col bg-canvas">
          <div className="empty-stage m-auto flex max-w-[560px] flex-col items-center px-6 py-10 text-center">
            <h1 className="empty-headline m-0 mb-4 text-[26px] font-semibold tracking-[-0.025em] text-ink">
              {needsSetup ? t('Configure models to start') : t('Choose a workspace')}
            </h1>
            <p className="connect-intro m-0 mb-6 text-[13.5px] leading-[1.6] text-ink-secondary">
              {needsSetup
                ? t('Model providers, API keys, models, and remote connections are stored in your Web SQLite database (TUI/CLI config.json stays separate).')
                : t('Select a local project directory to start conversations and inspect files.')}
            </p>
            <div className="suggestion-row flex flex-wrap items-center justify-center gap-2.5">
              <button type="button" className={btnSecondary} onClick={() => setSetupSettingsOpen(true)}>
                <Settings size={14} />{t('Open Settings')}
              </button>
              <button type="button" className={btnSecondary} onClick={() => openDirectoryPicker(true)}>
                <FolderOpen size={14} />{t('Open workspace')}
              </button>
            </div>
          </div>
        </main>
      </div>
    )}

    <SettingsModal
      open={setupSettingsOpen}
      initialTab={needsSetup ? 'models' : 'remote'}
      api={settingsAPI}
      workspace={currentWorkspacePath}
      theme={currentTheme}
      onThemeChange={handleThemeChange}
      onConnectRemote={async (opts) => setRemoteOverride({ url: opts.url, token: opts.token, name: opts.name })}
      onSavedConfig={handleReloaded}
      onClose={() => setSetupSettingsOpen(false)}
    />

    {picker && (
      <WorkspacePicker
        initial={currentWorkspacePath}
        credential={credential}
        close={() => setPicker(null)}
        onOpenSettings={() => { setPicker(null); setSetupSettingsOpen(true) }}
        opened={(item) => {
          setWorkspaces((previous) => [item, ...previous.filter((existing) => existing.id !== item.id)])
          if (Array.isArray(item.sessions)) {
            setWorkspaceSessions((prev) => ({ ...prev, [item.id]: item.sessions! }))
          }
          setNeedsSetup(false)
          select(item.id)
          const shouldOpenFiles = picker.openFilesAfter
          setPicker(null)
          if (shouldOpenFiles) setFocusFilesRequest((prev) => ({ id: item.id, seq: (prev?.seq ?? 0) + 1 }))
        }}
      />
    )}
  </>
}

function WorkspacePicker({ initial, credential, close, onOpenSettings, opened }: {
  initial: string
  credential: string
  close: () => void
  onOpenSettings: () => void
  opened: (workspace: Workspace) => void
}) {
  const { t } = useTranslation()
  const dialog = useRef<HTMLDialogElement>(null)
  const [path, setPath] = useState(initial)
  const [result, setResult] = useState<Directories>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [requiresSetup, setRequiresSetup] = useState(false)

  async function browse(target: string) {
    setBusy(true)
    setError('')
    setRequiresSetup(false)
    try {
      const next = await localRequest<Directories>(credential, 'browse-directories', { path: target })
      setResult(next)
      setPath(next.path)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not browse directories.')
    } finally { setBusy(false) }
  }

  useEffect(() => {
    dialog.current?.showModal()
    void browse(initial)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  async function confirm() {
    setBusy(true)
    setError('')
    setRequiresSetup(false)
    try {
      opened(await localRequest<Workspace>(credential, 'open-workspace', { path: path.trim() }))
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Could not open workspace.'
      setError(msg)
      if (err instanceof Error && (err as Error & { needsSetup?: boolean }).needsSetup) {
        setRequiresSetup(true)
      }
    } finally { setBusy(false) }
  }

  function submit(event: FormEvent) {
    event.preventDefault()
    void browse(path.trim())
  }

  return (
    <dialog
      className="workspace-picker m-auto max-h-[calc(100dvh-24px)] w-[min(560px,calc(100vw-24px))] rounded-[16px] border border-line bg-canvas p-6 text-ink shadow-elev-lg"
      ref={dialog}
      aria-labelledby="workspace-picker-title"
      onCancel={(event) => { if (busy) event.preventDefault() }}
      onClose={close}
    >
      <header className="flex items-center justify-between gap-2.5">
        <h2 id="workspace-picker-title" className="m-0 text-[18px] font-semibold text-ink">{t('Choose a workspace')}</h2>
        <button className={cn('icon-button', btnIcon)} aria-label={t('Close')} disabled={busy} onClick={close}><X size={18} /></button>
      </header>
      <form className="workspace-path my-4 flex items-end gap-2" onSubmit={submit}>
        <label className="grid min-w-0 flex-1 gap-1.5 text-[13px] font-medium text-ink">
          {t('Folder path')}
          <input autoFocus className={fieldInput} value={path} disabled={busy} onChange={(event) => setPath(event.target.value)} />
        </label>
        <button className={cn('secondary-button h-[36px]', btnSecondary)} disabled={busy || !path.trim()}>{t('Go')}</button>
      </form>
      {Boolean(result?.recent.length) && (
        <div className="workspace-recent my-2 mb-3 flex flex-wrap items-center gap-1 text-[12.5px] text-ink-muted">
          <span>{t('Recent folders')}</span>
          {result!.recent.map((item) => (
            <button className={btnPlain} key={item} title={item} disabled={busy} onClick={() => void browse(item)}>{nameOf(item)}</button>
          ))}
        </div>
      )}
      <div className="workspace-directory overflow-hidden rounded-[10px] border border-line">
        <div className="workspace-current flex items-center gap-2 border-b border-line-subtle bg-subtle px-2.5 py-1.5 font-mono text-[12px] text-ink-secondary">
          <button className={btnPlain} disabled={busy || !result?.parent} onClick={() => void browse(result!.parent)}>↑ {t('Parent folder')}</button>
          <span className="truncate" title={result?.path}>{result?.path}</span>
        </div>
        <div className="workspace-directory-rows max-h-[min(245px,28vh)] min-h-[72px] overflow-auto p-1.5" aria-busy={busy}>
          {result?.directories.map((item) => (
            <button
              key={item}
              disabled={busy}
              className="flex min-h-[33px] w-full cursor-pointer items-center gap-2 rounded-[9px] border-0 bg-transparent px-2.5 py-[5px] text-left text-[13.5px] text-ink transition-[background] duration-120 hover:bg-hover"
              onClick={() => void browse(item)}
            >
              <Folder size={16} className="shrink-0 text-ink-secondary" />
              <span className="min-w-0 flex-1 truncate">{nameOf(item)}</span>
            </button>
          ))}
          {!busy && result?.directories.length === 0 && <p className="m-0 pl-2 text-[13px] text-ink-muted">{t('No subfolders.')}</p>}
        </div>
      </div>
      {error && (
        <div className={cn('form-error mt-3 flex items-center justify-between gap-2', bannerError)} role="alert">
          <span>{t(error)}</span>
          {requiresSetup && (
            <button type="button" className={btnPlain} onClick={onOpenSettings}>{t('Open Settings')}</button>
          )}
        </div>
      )}
      <footer className="mt-4 flex items-center justify-end gap-2.5">
        <button className={btnPlain} disabled={busy} onClick={close}>{t('Cancel')}</button>
        <button className={cn('primary-button compact', btnPrimaryCompact)} disabled={busy || !path.trim()} onClick={() => void confirm()}>
          <FolderOpen size={16} />{t('Open workspace')}
        </button>
      </footer>
    </dialog>
  )
}
