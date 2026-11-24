import { useEffect, useRef, useState, type FormEvent } from 'react'
import { X } from 'lucide-react'
import type { ConfigDocument, ConfigImport, ConfigView } from '../shared/desktop-settings'

export function ConfigSettings({ workspace, onClose }: { workspace: string; onClose: () => void }) {
  const api = window.luneDesktop!
  const dialog = useRef<HTMLDialogElement>(null)
  const [scope, setScope] = useState('')
  const [view, setView] = useState<ConfigView>()
  const [draft, setDraft] = useState('')
  const [imported, setImported] = useState<ConfigImport | null>(null)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const [status, setStatus] = useState('')
  const [protocol, setProtocol] = useState('responses')
  const [baseURL, setBaseURL] = useState('')
  const [modelID, setModelID] = useState('')
  const [apiKey, setAPIKey] = useState('')
  const [apiKeyEnv, setAPIKeyEnv] = useState('')
  const [showStarter, setShowStarter] = useState(false)
  const [confirmReset, setConfirmReset] = useState(false)

  function display(result: ConfigView) {
    setView(result)
    setDraft(JSON.stringify(result.document, null, 2))
    setShowStarter(!Object.keys(result.document).length && !result.inheritedProfiles.length)
  }

  useEffect(() => { dialog.current?.showModal() }, [])
  useEffect(() => {
    let current = true
    setView(undefined)
    setImported(null)
    setError('')
    setStatus('')
    setConfirmReset(false)
    void api.getConfig(scope).then((config) => { if (current) display(config) })
      .catch((err) => { if (current) setError(err instanceof Error ? err.message : String(err)) })
    return () => { current = false }
  }, [api, scope])

  async function perform(action: () => Promise<void>) {
    setPending(true)
    setError('')
    setStatus('')
    try { await action() }
    catch (err) { setError(err instanceof Error ? err.message : String(err)) }
    finally { setPending(false) }
  }

  function save(document: ConfigDocument, importId?: string) {
    return perform(async () => {
      const transient = await api.saveConfig(scope, document, importId)
      display(await api.getConfig(scope))
      setImported(null)
      setAPIKey('')
      setStatus(transient ? 'Settings saved. New credentials are available until this app closes.' : 'Settings saved. They apply next time this Core starts.')
    })
  }

  const submitStarter = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    void save({
      models: {
        providers: { default: { protocol, baseURL: baseURL.trim(), ...(apiKeyEnv.trim() ? { apiKeyEnv: apiKeyEnv.trim() } : { apiKey }) } },
        profiles: { default: { provider: 'default', model: modelID.trim(), ...(protocol === 'anthropic-messages' ? { maxOutputTokens: 4096 } : {}) } },
        defaultProfile: 'default',
      },
    })
  }

  return <dialog className="desktop-config" ref={dialog} aria-labelledby="desktop-config-title" onClose={onClose}
    onCancel={(event) => { if (pending) event.preventDefault() }}>
    <header className="drawer-heading">
      <h2 id="desktop-config-title">Core configuration</h2>
      <button type="button" className="icon-button" aria-label="Close configuration" disabled={pending} onClick={() => dialog.current?.close()}><X size={16} /></button>
    </header>
    <div className="connection-tabs compact-tabs">
      <button type="button" className={!scope ? 'active' : ''} disabled={pending} onClick={() => setScope('')}>Defaults</button>
      <button type="button" className={scope ? 'active' : ''} disabled={pending || !workspace.trim()} onClick={() => setScope(workspace.trim())}>This workspace</button>
    </div>
    {error && <p className="form-error" role="alert">{error}</p>}
    {status && <p className="form-status" role="status">{status}</p>}
    {view && <>
      {scope && <p className="modal-copy">This directory inherits the default configuration. Save only the entries or fields you want to override.</p>}
      {!view.canSaveSecrets && <p className="form-status">The system credential store is unavailable. Use an environment variable or re-enter the key next time.</p>}
      {view.savedKeys.length > 0 && <p className="modal-copy">Keys available for {view.savedKeys.join(', ')}.</p>}
      {view.hasMCP && <p className="modal-copy">MCP settings are stored separately from editable configuration.</p>}
      <div className="config-actions">
        <button type="button" className="secondary-button" disabled={pending} onClick={() => void perform(async () => {
          const candidate = await api.chooseConfigImport()
          if (!candidate) return
          setImported(candidate)
          setDraft(JSON.stringify(candidate.document, null, 2))
          setShowStarter(false)
          setStatus('Import preview. Save to apply this configuration.')
        })}>Import config.json…</button>
        <button type="button" className="secondary-button" disabled={pending} onClick={() => { setImported(null); setShowStarter(!showStarter) }}>{showStarter ? 'Edit JSON' : 'Model setup'}</button>
      </div>
      {showStarter ? <form onSubmit={submitStarter}>
        <label className="form-field">Protocol<select value={protocol} onChange={(event) => setProtocol(event.target.value)}>
          <option value="responses">Responses</option><option value="anthropic-messages">Anthropic Messages</option>
        </select></label>
        <label className="form-field">Provider base URL<input required type="url" value={baseURL} onChange={(event) => setBaseURL(event.target.value)} /></label>
        <label className="form-field">Model ID<input required value={modelID} onChange={(event) => setModelID(event.target.value)} /></label>
        <label className="form-field">API key<input type="password" autoComplete="new-password" disabled={Boolean(apiKeyEnv.trim())} value={apiKey} onChange={(event) => setAPIKey(event.target.value)} /></label>
        <label className="form-field">API key environment variable<input value={apiKeyEnv} onChange={(event) => setAPIKeyEnv(event.target.value)} /></label>
        <button className="primary-button compact" disabled={pending}>Save model configuration</button>
      </form> : <>
        {view.inheritedProfiles.length > 0 && <label className="form-field">Use a default model from this app
          <select value="" onChange={(event) => {
            const defaultProfile = event.target.value
            if (!defaultProfile) return
            try {
              const config = JSON.parse(draft)
              setDraft(JSON.stringify({ ...config, models: { ...config.models, defaultProfile } }, null, 2))
            } catch { setError('Fix the JSON before choosing a default model.') }
          }}><option value="">Choose model…</option>{view.inheritedProfiles.map((profile) => <option key={profile}>{profile}</option>)}</select>
        </label>}
        {imported && <p className="modal-copy">
          {imported.suppliedKeys.length ? `Import supplies keys for ${imported.suppliedKeys.join(', ')}. ` : ''}
          {imported.unresolvedKeys.length ? `Re-enter keys or configure apiKeyEnv for ${imported.unresolvedKeys.join(', ')}. ` : ''}
          {imported.hasMCP ? 'Import includes MCP settings. ' : ''}
          Keys are hidden in the preview.
        </p>}
        <label className="form-field">Configuration JSON
          <textarea rows={12} value={draft} disabled={pending} spellCheck={false} onChange={(event) => setDraft(event.target.value)} />
          <small>Omit existing API keys to keep them. Set apiKey to an empty string to remove it, or set apiKeyEnv to use an environment variable.</small>
        </label>
        <button type="button" className="primary-button compact" disabled={pending} onClick={() => {
          let config: ConfigDocument
          try { config = JSON.parse(draft) as ConfigDocument }
          catch { setError('Enter valid JSON.'); return }
          void save(config, imported?.id)
        }}>{imported ? 'Save imported configuration' : 'Save configuration'}</button>
      </>}
      {scope && <div className="config-actions">
        <button type="button" className="secondary-button" disabled={pending} onClick={() => {
          if (!confirmReset) { setConfirmReset(true); return }
          void perform(async () => {
            await api.resetWorkspaceConfig(scope)
            display(await api.getConfig(scope))
            setConfirmReset(false)
            setImported(null)
            setStatus('Workspace now uses the default configuration.')
          })
        }}>{confirmReset ? 'Confirm removal of workspace overrides' : 'Use defaults for this workspace'}</button>
      </div>}
    </>}
  </dialog>
}
