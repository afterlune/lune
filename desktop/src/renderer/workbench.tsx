import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { Folder, FolderOpen, KeyRound, Plus, Settings, ShieldAlert, X } from 'lucide-react'
import type { ThemePreference } from '../shared/theme'
import App from './App'
import { useHostLanguage, useTranslate, WebLanguage } from './language'
import {
  clearHostToken, detectWebHost, loadLoopbackToken, loadSavedHostToken,
  loadSavedWebConnection, saveHostToken, verifyHostToken,
} from './loopback'
import type { RuntimeConnection } from './runtime'
import { createWebHostSettingsAPI, type FrontendSettingsAPI, type SaveConfigResult } from './settings-api'
import { SettingsModal } from './settings-modal'
import { applyTheme } from './theme'

interface Workspace { id: string; path: string }
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
  return <WebLanguage><Workbench theme={theme} /></WebLanguage>
}

function Workbench({ theme }: { theme: ThemePreference }) {
  const t = useTranslate()
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

  function select(id: string) {
    setRemoteOverride(null)
    setSelected(id)
    try { sessionStorage.setItem(selectionKey, id) } catch { /* current tab selection still works */ }
  }

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

  if (initializing) return <main className="web-launch"><p role="status">{t('Connecting to local Core…')}</p></main>

  if (!credential) {
    if (hostAuthMode === 'token') {
      return (
        <div className="connect-page">
          <main className="connect-card">
            <h1>{t('Unlock Lune Workspace')}</h1>
            <p className="connect-intro">{t('Enter the access token for this Lune host to access workspaces and SQLite settings.')}</p>
            <form onSubmit={unlockHost}>
              <div className="form-field">
                <label>{t('Access token')}</label>
                <input
                  type="password"
                  autoFocus
                  value={authInput}
                  onChange={(e) => setAuthInput(e.target.value)}
                  placeholder={t('Paste your Core token')}
                  autoComplete="off"
                />
                <label className="remember-token">
                  <input type="checkbox" checked={authRemember} onChange={(e) => setAuthRemember(e.target.checked)} />
                  {t('Remember token in this browser')}
                </label>
              </div>
              {authError && <div className="form-error" role="alert"><ShieldAlert size={16} /><span>{authError}</span></div>}
              <button type="submit" className="primary-button connect-button" disabled={!authInput.trim()}>
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
    <section className="workspace-list" aria-label={t('Workspaces')}>
      <div className="workspace-list-heading">
        <span>{t('Workspaces')}</span>
        <button
          type="button"
          className="icon-button"
          aria-label={t('Open workspace')}
          title={t('Open workspace')}
          onClick={() => openDirectoryPicker(false)}
        >
          <Plus size={16} />
        </button>
      </div>
      {pairingError && <div className="form-error workspace-list-error" role="alert"><ShieldAlert size={15} /><span>{t(pairingError)}</span></div>}
      {workspaces.map((item) => (
        <div className={`workspace-row${!remoteOverride && item.id === selected ? ' selected' : ''}`} key={item.id}>
          <button
            type="button"
            className="workspace-row-btn"
            aria-pressed={!remoteOverride && item.id === selected}
            title={item.path}
            onClick={() => select(item.id)}
          >
            <Folder size={15} />
            <span>{nameOf(item.path)}</span>
            {activity[item.id] && <small className="workspace-activity"><i aria-hidden="true" />{t(activity[item.id])}</small>}
          </button>
          <button
            type="button"
            className="workspace-close-btn"
            aria-label={`${t('Close')} ${nameOf(item.path)}`}
            title={t('Close workspace')}
            onClick={(e) => { e.stopPropagation(); void handleCloseWorkspace(item.id) }}
          >
            <X size={13} />
          </button>
        </div>
      ))}
      {!workspaces.length && (
        <button type="button" className="workspace-row-btn" onClick={() => openDirectoryPicker(true)}>
          <FolderOpen size={15} /><span>{t('Choose a workspace')}</span>
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
      <div className="app-shell web-workbench">
        <aside className="sidebar" aria-label={t('Conversations')}>
          <div className="sidebar-scroll-region">{navigation}</div>
          <div className="sidebar-bottom">
            <button type="button" className="settings-button" onClick={() => setSetupSettingsOpen(true)}>
              <Settings size={15} /><span>{t('Settings')}</span>
            </button>
          </div>
        </aside>
        <main className="chat-area">
          <div className="empty-stage">
            <h1 className="empty-headline">{needsSetup ? t('Configure models to start') : t('Choose a workspace')}</h1>
            <p className="connect-intro">
              {needsSetup
                ? t('Model providers, API keys, models, and remote connections are stored in your Web SQLite database (TUI/CLI config.json stays separate).')
                : t('Select a local project directory to start conversations and inspect files.')}
            </p>
            <div className="suggestion-row">
              <button type="button" onClick={() => setSetupSettingsOpen(true)}>
                <Settings size={14} />{t('Open Settings')}
              </button>
              <button type="button" onClick={() => openDirectoryPicker(true)}>
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
          setWorkspaces((previous) => previous.some((existing) => existing.id === item.id) ? previous : [...previous, item])
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
  const t = useTranslate()
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
    <dialog className="workspace-picker" ref={dialog} aria-labelledby="workspace-picker-title"
      onCancel={(event) => { if (busy) event.preventDefault() }} onClose={close}>
      <header>
        <h2 id="workspace-picker-title">{t('Choose a workspace')}</h2>
        <button className="icon-button" aria-label={t('Close')} disabled={busy} onClick={close}><X size={18} /></button>
      </header>
      <form className="workspace-path" onSubmit={submit}>
        <label>{t('Folder path')}<input autoFocus value={path} disabled={busy} onChange={(event) => setPath(event.target.value)} /></label>
        <button className="secondary-button" disabled={busy || !path.trim()}>{t('Go')}</button>
      </form>
      {Boolean(result?.recent.length) && (
        <div className="workspace-recent">
          <span>{t('Recent folders')}</span>
          {result!.recent.map((item) => (
            <button className="plain-button" key={item} title={item} disabled={busy} onClick={() => void browse(item)}>{nameOf(item)}</button>
          ))}
        </div>
      )}
      <div className="workspace-directory">
        <div className="workspace-current">
          <button className="plain-button" disabled={busy || !result?.parent} onClick={() => void browse(result!.parent)}>↑ {t('Parent folder')}</button>
          <span title={result?.path}>{result?.path}</span>
        </div>
        <div className="workspace-directory-rows" aria-busy={busy}>
          {result?.directories.map((item) => (
            <button key={item} disabled={busy} onClick={() => void browse(item)}><Folder size={16} />{nameOf(item)}</button>
          ))}
          {!busy && result?.directories.length === 0 && <p>{t('No subfolders.')}</p>}
        </div>
      </div>
      {error && (
        <div className="form-error" role="alert">
          <span>{t(error)}</span>
          {requiresSetup && (
            <button type="button" className="plain-button" onClick={onOpenSettings}>{t('Open Settings')}</button>
          )}
        </div>
      )}
      <footer>
        <button className="plain-button" disabled={busy} onClick={close}>{t('Cancel')}</button>
        <button className="primary-button compact" disabled={busy || !path.trim()} onClick={() => void confirm()}>
          <FolderOpen size={16} />{t('Open workspace')}
        </button>
      </footer>
    </dialog>
  )
}
