import { useCallback, useEffect, useRef, useState, type FormEvent, type ReactNode } from 'react'
import { Folder, FolderOpen, Plus, X } from 'lucide-react'
import type { ThemePreference } from '../shared/theme'
import App from './App'
import { useHostLanguage, useTranslate, WebLanguage } from './language'
import { loadLoopbackToken } from './loopback'
import type { RuntimeConnection } from './runtime'

interface Workspace { id: string; path: string }
interface Workspaces { initial: string; items: Workspace[]; language?: string }
interface Directories { path: string; parent: string; directories: string[]; recent: string[] }

export type WorkspaceActivity = 'Needs attention' | 'Running' | 'Queued' | 'Paused' | ''

export interface ManagedWorkspace {
  workspace: Workspace
  options: Extract<RuntimeConnection, { mode: 'loopback' }>
  active: boolean
  navigation: ReactNode
  report: (id: string, status: WorkspaceActivity) => void
}

const selectionKey = 'lune:active-workspace'

function nameOf(path: string): string {
  return path.split(/[\\/]/).filter(Boolean).at(-1) || path
}

async function localRequest<T>(credential: string, method: string, params: { path?: string; language?: string } = {}): Promise<T> {
  const response = await fetch('/_lune/' + method, {
    method: 'POST', mode: 'same-origin', credentials: 'omit', redirect: 'error',
    headers: { Authorization: 'Bearer ' + credential, 'Content-Type': 'application/json' },
    body: JSON.stringify(params),
  })
  if (!response.ok) throw new Error((await response.text()).trim() || 'Could not open workspace.')
  return response.json() as Promise<T>
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
  const [credential, setCredential] = useState('')
  const [workspaces, setWorkspaces] = useState<Workspace[]>([])
  const [selected, setSelected] = useState('')
  const [activity, setActivity] = useState<Record<string, WorkspaceActivity>>({})
  const [picker, setPicker] = useState(false)

  useEffect(() => {
    let active = true
    void (async () => {
      const token = await loadLoopbackToken()
      if (!token) return
      const result = await localRequest<Workspaces>(token, 'workspaces')
      if (!active) return
      bindHostLanguage(result.language, language => localRequest(token, 'language', { language }))
      let preferred: string | null = null
      try { preferred = sessionStorage.getItem(selectionKey) } catch { /* use initial workspace */ }
      setWorkspaces(result.items)
      setSelected(result.items.some(item => item.id === preferred) ? preferred! : result.initial)
      setCredential(token)
    })().catch((error: Error) => {
      if (active) setPairingError(error.message)
    }).finally(() => { if (active) setInitializing(false) })
    return () => { active = false }
  }, [bindHostLanguage])

  function select(id: string) {
    setSelected(id)
    try { sessionStorage.setItem(selectionKey, id) } catch { /* current tab selection still works */ }
  }

  const report = useCallback((id: string, status: WorkspaceActivity) => {
    setActivity(previous => previous[id] === status ? previous : { ...previous, [id]: status })
  }, [])

  if (initializing) return <main className="web-launch"><p role="status">{t('Connecting to local Core…')}</p></main>
  if (!credential) return <App initialTheme={theme} pairingError={pairingError} />

  const navigation = <section className="workspace-list" aria-label={t('Workspaces')}>
    <div className="workspace-list-heading"><span>{t('Workspaces')}</span><button type="button" className="icon-button"
      aria-label={t('Open workspace')} title={t('Open workspace')} onClick={() => setPicker(true)}><Plus size={16} /></button></div>
    {workspaces.map(item => <button type="button" key={item.id} className={item.id === selected ? 'selected' : ''}
      aria-pressed={item.id === selected} title={item.path} onClick={() => select(item.id)}>
      <Folder size={16} /><span>{nameOf(item.path)}</span>{activity[item.id] && <small>{t(activity[item.id])}</small>}
    </button>)}
  </section>

  return <>
    {workspaces.map(workspace => <div key={workspace.id} hidden={workspace.id !== selected}>
      <App initialTheme={currentTheme} onThemeChange={setTheme} managed={{
        workspace, active: workspace.id === selected, report, navigation,
        options: { mode: 'loopback', token: credential, url: `ws://${location.host}/v1/workspaces/${workspace.id}/runtime` },
      }} />
    </div>)}
    {picker && <WorkspacePicker initial={workspaces.find(item => item.id === selected)?.path || ''}
      credential={credential} close={() => setPicker(false)} opened={item => {
        setWorkspaces(previous => previous.some(existing => existing.id === item.id) ? previous : [...previous, item])
        select(item.id)
        setPicker(false)
      }} />}
  </>
}

function WorkspacePicker({ initial, credential, close, opened }: {
  initial: string; credential: string; close: () => void; opened: (workspace: Workspace) => void
}) {
  const t = useTranslate()
  const dialog = useRef<HTMLDialogElement>(null)
  const [path, setPath] = useState(initial)
  const [result, setResult] = useState<Directories>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function browse(target: string) {
    setBusy(true)
    setError('')
    try {
      const next = await localRequest<Directories>(credential, 'browse-directories', { path: target })
      setResult(next)
      setPath(next.path)
    } catch (error) {
      setError(error instanceof Error ? error.message : 'Could not browse directories.')
    } finally { setBusy(false) }
  }

  useEffect(() => {
    dialog.current?.showModal()
    void browse(initial)
    // The picker is mounted for a single opening.
  }, [])

  async function confirm() {
    setBusy(true)
    setError('')
    try { opened(await localRequest<Workspace>(credential, 'open-workspace', { path: path.trim() })) }
    catch (error) { setError(error instanceof Error ? error.message : 'Could not open workspace.') }
    finally { setBusy(false) }
  }

  function submit(event: FormEvent) {
    event.preventDefault()
    void browse(path.trim())
  }

  return <dialog className="workspace-picker" ref={dialog} aria-labelledby="workspace-picker-title"
    onCancel={event => { if (busy) event.preventDefault() }} onClose={close}>
    <header><h2 id="workspace-picker-title">{t('Choose a workspace')}</h2><button className="icon-button"
      aria-label={t('Close')} disabled={busy} onClick={close}><X size={18} /></button></header>
    <form className="workspace-path" onSubmit={submit}>
      <label>{t('Folder path')}<input autoFocus value={path} disabled={busy} onChange={event => setPath(event.target.value)} /></label>
      <button className="secondary-button" disabled={busy || !path.trim()}>{t('Go')}</button>
    </form>
    {Boolean(result?.recent.length) && <div className="workspace-recent">
      <span>{t('Recent folders')}</span>
      {result!.recent.map(item => <button className="plain-button" key={item} title={item} disabled={busy} onClick={() => void browse(item)}>{nameOf(item)}</button>)}
    </div>}
    <div className="workspace-directory">
      <div className="workspace-current">
        <button className="plain-button" disabled={busy || !result?.parent} onClick={() => void browse(result!.parent)}>↑ {t('Parent folder')}</button>
        <span title={result?.path}>{result?.path}</span>
      </div>
      <div className="workspace-directory-rows" aria-busy={busy}>
        {result?.directories.map(item => <button key={item} disabled={busy} onClick={() => void browse(item)}><Folder size={16} />{nameOf(item)}</button>)}
        {!busy && result?.directories.length === 0 && <p>{t('No subfolders.')}</p>}
      </div>
    </div>
    {error && <p className="form-error" role="alert">{t(error)}</p>}
    <footer><button className="plain-button" disabled={busy} onClick={close}>{t('Cancel')}</button>
      <button className="primary-button compact" disabled={busy || !path.trim()} onClick={() => void confirm()}><FolderOpen size={16} />{t('Open workspace')}</button>
    </footer>
  </dialog>
}
