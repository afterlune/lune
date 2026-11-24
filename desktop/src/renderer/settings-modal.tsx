import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { Activity, Check, Cpu, Database, FolderGit2, Globe, KeyRound, Plus, RefreshCw, Search, SlidersHorizontal, Trash2, Upload, X } from 'lucide-react'
import type { ConfigDocument, ConfigImport, ConfigView, ProviderTestInput, SavedConnection, UiPreferences } from '../shared/desktop-settings'
import type { ThemePreference } from '../shared/theme'
import { useLanguage, useTranslate } from './language'
import type { FrontendSettingsAPI, ProviderModelInfo, SaveConfigResult } from './settings-api'
import { ThemePicker } from './theme'
import { defaultUiPreferences } from '../shared/ui-preferences'

function normalizeRemoteInput(raw: string): string {
  let value = raw.trim()
  if (!value) return value
  if (value.startsWith('https://')) value = `wss://${value.slice('https://'.length)}`
  else if (!value.includes('://')) value = `wss://${value}`
  if (value.startsWith('wss://')) {
    try {
      const parsed = new URL(value)
      if (!parsed.pathname || parsed.pathname === '/') parsed.pathname = '/v1/runtime'
      return parsed.href
    } catch { /* let server/client validation explain invalid syntax */ }
  }
  return value
}

export type SettingsTab = 'providers' | 'models' | 'runtime' | 'remote' | 'general'

interface ProviderEntry {
  protocol: string
  baseURL: string
  apiKey?: string
  apiKeyEnv?: string
  apiKeyKeyringRef?: string
  serverCompaction?: boolean
}

interface ProfileEntry {
  provider: string
  model: string
  displayName?: string
  contextWindow?: number
  maxOutputTokens?: number
  supportsInputImages?: boolean
  supportsInputAudio?: boolean
}

function asObject(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? (value as Record<string, unknown>) : {}
}

function normalizeDoc(doc: ConfigDocument): {
  providers: Record<string, ProviderEntry>
  profiles: Record<string, ProfileEntry>
  defaultProfile: string
} {
  const models = asObject(doc.models)
  const rawProviders = asObject(models.providers)
  const rawProfiles = asObject(models.profiles)
  const providers: Record<string, ProviderEntry> = {}
  for (const [key, val] of Object.entries(rawProviders)) {
    const p = asObject(val)
    providers[key] = {
      protocol: typeof p.protocol === 'string' ? p.protocol : 'responses',
      baseURL: typeof p.baseURL === 'string' ? p.baseURL : '',
      ...(typeof p.apiKey === 'string' ? { apiKey: p.apiKey } : {}),
      ...(typeof p.apiKeyEnv === 'string' ? { apiKeyEnv: p.apiKeyEnv } : {}),
      ...(typeof p.apiKeyKeyringRef === 'string' ? { apiKeyKeyringRef: p.apiKeyKeyringRef } : {}),
      ...(typeof p.serverCompaction === 'boolean' ? { serverCompaction: p.serverCompaction } : {}),
    }
  }
  const profiles: Record<string, ProfileEntry> = {}
  for (const [key, val] of Object.entries(rawProfiles)) {
    const m = asObject(val)
    profiles[key] = {
      provider: typeof m.provider === 'string' ? m.provider : '',
      model: typeof m.model === 'string' ? m.model : '',
      ...(typeof m.displayName === 'string' ? { displayName: m.displayName } : {}),
      ...(typeof m.contextWindow === 'number' ? { contextWindow: m.contextWindow } : {}),
      ...(typeof m.maxOutputTokens === 'number' ? { maxOutputTokens: m.maxOutputTokens } : {}),
      ...(typeof m.supportsInputImages === 'boolean' ? { supportsInputImages: m.supportsInputImages } : {}),
      ...(typeof m.supportsInputAudio === 'boolean' ? { supportsInputAudio: m.supportsInputAudio } : {}),
    }
  }
  return {
    providers,
    profiles,
    defaultProfile: typeof models.defaultProfile === 'string' ? models.defaultProfile : '',
  }
}

export function SettingsModal({
  open,
  initialTab,
  api,
  workspace,
  theme,
  onThemeChange,
  onUiPreferencesChange,
  activeRemoteURL = '',
  isRemoteActive = false,
  hasLocalHost = false,
  onConnectRemote,
  onDisconnectRemote,
  onReturnToLocal,
  onSavedConfig,
  onClose,
}: {
  open: boolean
  initialTab?: SettingsTab
  api: FrontendSettingsAPI
  workspace: string
  theme: ThemePreference
  onThemeChange: (theme: ThemePreference) => void
  onUiPreferencesChange?: (preferences: UiPreferences) => void
  activeRemoteURL?: string
  isRemoteActive?: boolean
  hasLocalHost?: boolean
  onConnectRemote: (options: { url: string; token: string; name?: string; rememberToken?: boolean }) => Promise<void>
  onDisconnectRemote?: () => Promise<void>
  onReturnToLocal?: () => Promise<void>
  onSavedConfig?: (result: SaveConfigResult) => SaveConfigResult | void | Promise<SaveConfigResult | void>
  onClose: () => void
}) {
  const t = useTranslate()
  const { language, setLanguage } = useLanguage()
  const dialogRef = useRef<HTMLDialogElement>(null)
  const importInputRef = useRef<HTMLInputElement>(null)
  const [tab, setTab] = useState<SettingsTab>(initialTab || (api.supportsModelConfig ? 'providers' : 'remote'))
  const [scope, setScope] = useState('')
  const [view, setView] = useState<ConfigView | null>(null)
  const [doc, setDoc] = useState<ConfigDocument>({})
  const [rawJSON, setRawJSON] = useState('{}')
  const [imported, setImported] = useState<ConfigImport | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [status, setStatus] = useState('')

  // Provider form state
  const [editingProvider, setEditingProvider] = useState<string | null>(null)
  const [provId, setProvId] = useState('')
  const [provProtocol, setProvProtocol] = useState<'responses' | 'anthropic-messages'>('responses')
  const [provBaseURL, setProvBaseURL] = useState('')
  const [provAuthMode, setProvAuthMode] = useState<'key' | 'env'>('key')
  const [provAPIKey, setProvAPIKey] = useState('')
  const [provClearKey, setProvClearKey] = useState(false)
  const [provAPIKeyEnv, setProvAPIKeyEnv] = useState('')
  const [provCompaction, setProvCompaction] = useState(true)

  // Model form state
  const [editingProfile, setEditingProfile] = useState<string | null>(null)
  const [profId, setProfId] = useState('')
  const [profProvider, setProfProvider] = useState('')
  const [profModel, setProfModel] = useState('')
  const [profDisplayName, setProfDisplayName] = useState('')
  const [profContextWindow, setProfContextWindow] = useState('0')
  const [profMaxOutput, setProfMaxOutput] = useState('0')
  const [profImages, setProfImages] = useState(false)
  const [profAudio, setProfAudio] = useState(false)
  const [profAsDefault, setProfAsDefault] = useState(true)

  // Remote connections state
  const [connections, setConnections] = useState<SavedConnection[]>([])
  const [remoteURL, setRemoteURL] = useState(activeRemoteURL)
  const [remoteName, setRemoteName] = useState('')
  const [remoteToken, setRemoteToken] = useState('')
  const [rememberToken, setRememberToken] = useState(true)
  const [shortcut, setShortcut] = useState('CommandOrControl+Shift+Space')
  const [uiPreferences, setUiPreferences] = useState<UiPreferences>(defaultUiPreferences)
  const [testingProvider, setTestingProvider] = useState(false)
  const [providerTestMessage, setProviderTestMessage] = useState('')
  const [providerTestOK, setProviderTestOK] = useState(false)

  // Fetched models state
  const [fetchedModels, setFetchedModels] = useState<Record<string, ProviderModelInfo[]>>({})
  const [fetchingModels, setFetchingModels] = useState(false)
  const [fetchModelsError, setFetchModelsError] = useState('')
  const [modelFilter, setModelFilter] = useState('')

  const wasOpen = useRef(false)
  useEffect(() => {
    if (!open) {
      wasOpen.current = false
      return
    }
    if (!wasOpen.current) {
      wasOpen.current = true
      setTab(initialTab || (api.supportsModelConfig ? 'providers' : 'remote'))
      setError('')
      setStatus('')
    }
    if (dialogRef.current && !dialogRef.current.open) dialogRef.current.showModal()
  }, [open, initialTab, api.supportsModelConfig])

  const loadConfig = async (targetScope: string) => {
    if (!api.supportsModelConfig) return
    const next = await api.getConfig(targetScope)
    setView(next)
    setDoc(next.document)
    setRawJSON(JSON.stringify(next.document, null, 2))
    setImported(null)
    setFetchedModels({})
    setFetchModelsError('')
  }

  const loadConnections = async () => {
    const res = await api.savedConnections()
    setConnections(res.items.filter((item) => item.mode === 'remote'))
    if (res.notice) setStatus(res.notice)
  }

  useEffect(() => {
    if (!open) return
    let active = true
    setError('')
    void Promise.all([
      loadConfig(scope).catch((err) => { if (active) setError(err instanceof Error ? err.message : String(err)) }),
      loadConnections().catch(() => undefined),
      api.getUiPreferences().then((preferences) => {
        if (!active) return
        setUiPreferences(preferences)
        onUiPreferencesChange?.(preferences)
        onThemeChange(preferences.theme)
        setLanguage(preferences.language)
      }).catch((err) => { if (active) setError(err instanceof Error ? err.message : String(err)) }),
      api.supportsShortcut && window.luneDesktop ? window.luneDesktop.shortcut().then((s) => { if (active) setShortcut(s) }).catch(() => undefined) : Promise.resolve(),
    ])
    return () => { active = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, scope])

  const catalog = normalizeDoc(doc)
  const providerNames = Object.keys(catalog.providers)
  const profileNames = Object.keys(catalog.profiles)
  const availableProvidersForModel = Array.from(new Set([...providerNames]))
  const activeModelProvider = (profProvider || availableProvidersForModel[0] || '').trim()

  const fetchModelsForProvider = useCallback(async (providerName: string) => {
    const target = providerName.trim()
    if (!target || !api.fetchProviderModels) return
    setFetchingModels(true)
    setFetchModelsError('')
    try {
      const list = await api.fetchProviderModels(scope, target)
      setFetchedModels((prev) => ({ ...prev, [target]: list }))
    } catch (err) {
      setFetchModelsError(err instanceof Error ? err.message : String(err))
    } finally {
      setFetchingModels(false)
    }
  }, [api, scope])

  useEffect(() => {
    if (!open || tab !== 'models' || !api.supportsModelConfig || !api.fetchProviderModels) return
    if (!activeModelProvider || !view) return
    const viewProviders = asObject(asObject(view.document).models).providers
    if (!Object.hasOwn(asObject(viewProviders), activeModelProvider)) return
    if (Object.hasOwn(fetchedModels, activeModelProvider)) return
    void fetchModelsForProvider(activeModelProvider)
  }, [open, tab, api.supportsModelConfig, api.fetchProviderModels, activeModelProvider, view, fetchedModels, fetchModelsForProvider])

  if (!open) return null

  const commitDocument = async (nextDoc: ConfigDocument, importId?: string, message?: string) => {
    setBusy(true)
    setError('')
    setStatus('')
    try {
      const result = await api.saveConfig(scope, nextDoc, importId)
      await loadConfig(scope)
      const followup = await onSavedConfig?.(result)
      const finalResult = { ...result, ...followup }
      setStatus(finalResult.requiresReconnect
        ? t('Settings saved. Reconnect this workspace after the current task finishes to apply the changes.')
        : message || (finalResult.reloaded ? t('Settings saved and runtime reconnected.') : t('Settings saved to SQLite database.')))
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
      return false
    } finally {
      setBusy(false)
    }
  }

  const saveUiPreference = async (patch: Partial<UiPreferences>) => {
    setError('')
    try {
      const next = await api.saveUiPreferences(patch)
      setUiPreferences(next)
      onUiPreferencesChange?.(next)
      onThemeChange(next.theme)
      setLanguage(next.language)
      setStatus(t('Interface preferences saved.'))
    } catch (err) { setError(err instanceof Error ? err.message : String(err)) }
  }

  const testCurrentProvider = async () => {
    if (!api.testProvider) return
    const provider = (editingProvider ?? provId).trim()
    if (!provider || !provBaseURL.trim()) { setError(t('Enter a provider ID and Base URL before testing.')); return }
    setTestingProvider(true)
    setProviderTestMessage('')
    setError('')
    try {
      const input: ProviderTestInput = {
        provider, protocol: provProtocol, baseURL: provBaseURL.trim(),
        ...(provAuthMode === 'env' ? { apiKeyEnv: provAPIKeyEnv.trim() }
          : provClearKey ? { apiKey: '' } : provAPIKey ? { apiKey: provAPIKey } : {}),
      }
      const result = await api.testProvider(scope, input)
      setProviderTestOK(result.ok)
      setProviderTestMessage(result.ok
        ? `${t('Connection successful')}: ${result.modelCount ?? 0} ${t('models in')} ${result.durationMs} ms`
        : t(result.error || 'Provider connection failed. Check the URL, credentials, and network.'))
    } catch (err) {
      setProviderTestOK(false)
      setProviderTestMessage(err instanceof Error ? err.message : t('Provider connection failed. Check the URL, credentials, and network.'))
    } finally { setTestingProvider(false) }
  }

  const updateConfigField = (section: 'runtime' | 'attachments', field: string, value: number | boolean | '') => {
    const values = { ...asObject(doc[section]) }
    if (value === '' || value === 0) delete values[field]
    else values[field] = value
    const next = { ...doc, [section]: values }
    setDoc(next)
    setRawJSON(JSON.stringify(next, null, 2))
  }

  const resetProviderForm = () => {
    setEditingProvider(null)
    setProvId('')
    setProvProtocol('responses')
    setProvBaseURL('')
    setProvAuthMode('key')
    setProvAPIKey('')
    setProvClearKey(false)
    setProvAPIKeyEnv('')
    setProvCompaction(true)
  }

  const startEditProvider = (name: string, entry: ProviderEntry) => {
    setEditingProvider(name)
    setProvId(name)
    setProvProtocol(entry.protocol === 'anthropic-messages' ? 'anthropic-messages' : 'responses')
    setProvBaseURL(entry.baseURL || '')
    setProvAuthMode(entry.apiKeyEnv ? 'env' : 'key')
    setProvAPIKey('')
    setProvClearKey(false)
    setProvAPIKeyEnv(entry.apiKeyEnv || '')
    setProvCompaction(Boolean(entry.serverCompaction))
  }

  const submitProvider = async (event: FormEvent) => {
    event.preventDefault()
    const id = (editingProvider ?? provId).trim()
    if (!id) { setError(t('Provider ID is required.')); return }
    const nextProviders: Record<string, ProviderEntry> = { ...catalog.providers }
    const nextEntry: ProviderEntry = {
      protocol: provProtocol,
      baseURL: provBaseURL.trim(),
      ...(provProtocol === 'responses' ? { serverCompaction: provCompaction } : {}),
    }
    if (provAuthMode === 'env') {
      if (!provAPIKeyEnv.trim()) { setError(t('Environment variable name is required.')); return }
      nextEntry.apiKeyEnv = provAPIKeyEnv.trim()
    } else if (provClearKey) {
      nextEntry.apiKey = ''
    } else if (provAPIKey !== '') {
      nextEntry.apiKey = provAPIKey
    }
    nextProviders[id] = nextEntry
    const nextDoc: ConfigDocument = {
      ...doc,
      models: {
        ...asObject(doc.models),
        providers: nextProviders,
        ...(profileNames.length ? { profiles: catalog.profiles } : {}),
        ...(catalog.defaultProfile ? { defaultProfile: catalog.defaultProfile } : {}),
      },
    }

    if (api.kind === 'desktop' && profileNames.length === 0) {
      setDoc(nextDoc)
      setRawJSON(JSON.stringify(nextDoc, null, 2))
      resetProviderForm()
      setProfProvider(id)
      setTab('models')
      setStatus(t('Provider staged. Add at least one model profile and save to persist to SQLite.'))
      return
    }

    const ok = await commitDocument(nextDoc, imported?.id, t('Provider saved to SQLite database.'))
    if (ok) {
      resetProviderForm()
      if (profileNames.length === 0) {
        setProfProvider(id)
      }
    }
  }

  const deleteProvider = async (name: string) => {
    const nextProviders = { ...catalog.providers }
    delete nextProviders[name]
    const nextDoc: ConfigDocument = {
      ...doc,
      models: {
        ...asObject(doc.models),
        providers: nextProviders,
      },
    }
    if (editingProvider === name) resetProviderForm()
    await commitDocument(nextDoc)
  }

  const resetProfileForm = () => {
    setEditingProfile(null)
    setProfId('')
    setProfProvider(providerNames[0] || '')
    setProfModel('')
    setProfDisplayName('')
    setProfContextWindow('0')
    setProfMaxOutput('0')
    setProfImages(false)
    setProfAudio(false)
    setProfAsDefault(profileNames.length === 0)
  }

  const startEditProfile = (name: string, entry: ProfileEntry) => {
    setEditingProfile(name)
    setProfId(name)
    setProfProvider(entry.provider)
    setProfModel(entry.model)
    setProfDisplayName(entry.displayName || '')
    setProfContextWindow(String(entry.contextWindow ?? 0))
    setProfMaxOutput(String(entry.maxOutputTokens ?? 0))
    setProfImages(Boolean(entry.supportsInputImages))
    setProfAudio(Boolean(entry.supportsInputAudio))
    setProfAsDefault(catalog.defaultProfile === name)
  }

  const submitProfile = async (event: FormEvent) => {
    event.preventDefault()
    const id = (editingProfile ?? profId).trim()
    const provider = (profProvider || providerNames[0] || '').trim()
    if (!id) { setError(t('Model profile ID is required.')); return }
    if (!provider) { setError(t('Configure or select a provider first.')); return }
    const targetProvider = catalog.providers[provider]
    const parsedCtx = Math.max(0, Number.parseInt(profContextWindow, 10) || 0)
    let parsedOut = Math.max(0, Number.parseInt(profMaxOutput, 10) || 0)
    if (targetProvider?.protocol === 'anthropic-messages' && parsedOut === 0) parsedOut = 8192

    const nextProfiles: Record<string, ProfileEntry> = {
      ...catalog.profiles,
      [id]: {
        provider,
        model: profModel.trim(),
        ...(profDisplayName.trim() ? { displayName: profDisplayName.trim() } : {}),
        ...(parsedCtx > 0 ? { contextWindow: parsedCtx } : {}),
        ...(parsedOut > 0 ? { maxOutputTokens: parsedOut } : {}),
        ...(profImages ? { supportsInputImages: true } : {}),
        ...(profAudio ? { supportsInputAudio: true } : {}),
      },
    }
    const nextDefault = profAsDefault || !catalog.defaultProfile ? id : catalog.defaultProfile
    const nextDoc: ConfigDocument = {
      ...doc,
      models: {
        ...asObject(doc.models),
        providers: catalog.providers,
        profiles: nextProfiles,
        defaultProfile: nextDefault,
      },
    }
    const ok = await commitDocument(nextDoc, imported?.id, t('Model saved to SQLite database.'))
    if (ok) resetProfileForm()
  }

  const setDefaultProfile = async (name: string) => {
    const nextDoc: ConfigDocument = {
      ...doc,
      models: { ...asObject(doc.models), defaultProfile: name },
    }
    await commitDocument(nextDoc)
  }

  const deleteProfile = async (name: string) => {
    const nextProfiles = { ...catalog.profiles }
    delete nextProfiles[name]
    const remaining = Object.keys(nextProfiles)
    const nextDefault = catalog.defaultProfile === name ? (remaining[0] || '') : catalog.defaultProfile
    const nextDoc: ConfigDocument = {
      ...doc,
      models: {
        ...asObject(doc.models),
        profiles: nextProfiles,
        ...(nextDefault ? { defaultProfile: nextDefault } : {}),
      },
    }
    if (!nextDefault) delete (nextDoc.models as Record<string, unknown>).defaultProfile
    if (editingProfile === name) resetProfileForm()
    await commitDocument(nextDoc)
  }

  const handleImportConfig = async () => {
    setError('')
    setStatus('')
    if (api.chooseConfigImport) {
      setBusy(true)
      try {
        const candidate = await api.chooseConfigImport()
        if (!candidate) return
        setImported(candidate)
        setDoc(candidate.document)
        setRawJSON(JSON.stringify(candidate.document, null, 2))
        setStatus(t('Imported preview ready. Click Save to store into SQLite DB.'))
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
      } finally {
        setBusy(false)
      }
      return
    }
    importInputRef.current?.click()
  }

  const saveRemoteForm = async (connectAfter: boolean) => {
    setBusy(true)
    setError('')
    setStatus('')
    try {
      const normalized = normalizeRemoteInput(remoteURL)
      if (normalized !== remoteURL) setRemoteURL(normalized)
      await api.saveRemoteConnection({
        url: normalized,
        name: remoteName.trim(),
        token: remoteToken,
        rememberToken,
      })
      await loadConnections()
      if (connectAfter) {
        await onConnectRemote({
          url: normalized,
          token: remoteToken,
          name: remoteName.trim(),
          rememberToken,
        })
        onClose()
      } else {
        setRemoteToken('')
        setStatus(t('Remote connection saved to SQLite database.'))
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const connectSavedRemote = async (saved: SavedConnection) => {
    setBusy(true)
    setError('')
    try {
      let tokenToUse = remoteToken.trim()
      if (!tokenToUse && api.getRemoteToken && saved.remembered) {
        tokenToUse = await api.getRemoteToken(saved.address)
      }
      await onConnectRemote({
        url: saved.address,
        token: tokenToUse,
        name: saved.name,
        rememberToken: saved.remembered,
      })
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const navItems: Array<{ id: SettingsTab; label: string; icon: typeof KeyRound; hidden?: boolean }> = [
    { id: 'providers', label: 'Providers', icon: KeyRound, hidden: !api.supportsModelConfig },
    { id: 'models', label: 'Models', icon: Cpu, hidden: !api.supportsModelConfig },
    { id: 'runtime', label: 'Runtime & Agents', icon: Activity, hidden: !api.supportsModelConfig },
    { id: 'remote', label: 'Remote & Token', icon: Globe },
    { id: 'general', label: 'General', icon: SlidersHorizontal },
  ]

  return (
    <dialog
      ref={dialogRef}
      className="settings-modal"
      aria-labelledby="settings-modal-title"
      onClose={onClose}
      onCancel={(event) => { if (busy) event.preventDefault() }}
    >
      <header className="settings-modal-head">
        <div>
          <h2 id="settings-modal-title">{t('Settings')}</h2>
          <span className="settings-db-badge">
            <Database size={12} aria-hidden="true" />
            {api.kind === 'web-host' ? t('Web SQLite DB (settings.db)') : api.kind === 'desktop' ? t('Electron SQLite DB (settings.sqlite)') : t('Browser storage')}
          </span>
        </div>
        <button type="button" className="icon-button" aria-label={t('Close')} disabled={busy} onClick={onClose}><X size={17} /></button>
      </header>

      <div className="settings-shell">
        <nav className="settings-nav" aria-label={t('Settings sections')}>
          {navItems.filter((item) => !item.hidden).map((item) => {
            const Icon = item.icon
            return (
              <button
                key={item.id}
                type="button"
                className={tab === item.id ? 'active' : ''}
                onClick={() => { setError(''); setStatus(''); setTab(item.id) }}
              >
                <Icon size={15} aria-hidden="true" />
                <span>{t(item.label)}</span>
              </button>
            )
          })}
        </nav>

        <div className="settings-body">
          {error && <p className="form-error" role="alert">{error}</p>}
          {status && <p className="form-status" role="status">{status}</p>}

          {(tab === 'providers' || tab === 'models' || tab === 'runtime') && api.supportsModelConfig && (
            <div className="settings-scope-row">
              <div className="connection-tabs compact-tabs">
                <button type="button" className={!scope ? 'active' : ''} disabled={busy} onClick={() => setScope('')}>{t('Defaults')}</button>
                <button type="button" className={scope ? 'active' : ''} disabled={busy || !workspace.trim()} onClick={() => setScope(workspace.trim())}>{t('This workspace')}</button>
              </div>
              <div className="settings-scope-actions">
                <input
                  ref={importInputRef}
                  type="file"
                  accept=".json"
                  hidden
                  onChange={(event) => {
                    const file = event.target.files?.[0]
                    event.target.value = ''
                    if (!file || !api.importConfigText) return
                    setBusy(true)
                    void file.text().then((txt) => api.importConfigText!(txt)).then((candidate) => {
                      setImported(candidate)
                      setDoc(candidate.document)
                      setRawJSON(JSON.stringify(candidate.document, null, 2))
                      setStatus(t('Imported preview ready. Click Save to store into SQLite DB.'))
                    }).catch((err) => setError(err instanceof Error ? err.message : String(err))).finally(() => setBusy(false))
                  }}
                />
                <button type="button" className="secondary-button" disabled={busy} onClick={() => void handleImportConfig()}>
                  <Upload size={13} aria-hidden="true" />{t('Import config.json…')}
                </button>
                {scope && (
                  <button
                    type="button"
                    className="plain-button"
                    disabled={busy}
                    onClick={() => void api.resetWorkspaceConfig(scope).then(async (result) => {
                      await loadConfig(scope)
                      const followup = await onSavedConfig?.(result)
                      const finalResult = { ...result, ...followup }
                      setStatus(finalResult.requiresReconnect
                        ? t('Settings saved. Reconnect this workspace after the current task finishes to apply the changes.')
                        : t('Workspace now uses the default configuration.'))
                    }).catch((err) => setError(err instanceof Error ? err.message : String(err)))}
                  >
                    {t('Use defaults')}
                  </button>
                )}
              </div>
            </div>
          )}

          {tab === 'providers' && api.supportsModelConfig && (
            <div className="settings-pane">
              {view?.secretStorage && (
                <p className="inspector-hint">
                  {view.secretStorage === 'encrypted'
                    ? t('API keys are encrypted and stored in this frontend SQLite database.')
                    : t('API keys are persisted in this frontend SQLite database (0600); TUI/CLI config.json is untouched.')}
                </p>
              )}

              <div className="settings-cards">
                {providerNames.length === 0 && <p className="inspector-hint">{t('No providers configured in this scope yet.')}</p>}
                {providerNames.map((name) => {
                  const entry = catalog.providers[name]
                  const hasSavedKey = view?.savedKeys.includes(name)
                  return (
                    <div className="settings-card" key={name}>
                      <div className="settings-card-main">
                        <div className="settings-card-title">
                          <strong>{name}</strong>
                          <span className="status-pill">{entry.protocol}</span>
                          {hasSavedKey ? (
                            <span className="status-pill active">{t('Key saved in DB')}</span>
                          ) : entry.apiKeyEnv ? (
                            <span className="status-pill">env:{entry.apiKeyEnv}</span>
                          ) : (
                            <span className="status-pill">{t('No key')}</span>
                          )}
                        </div>
                        <small>{entry.baseURL}</small>
                      </div>
                      <div className="settings-card-actions">
                        <button type="button" className="secondary-button" disabled={busy} onClick={() => startEditProvider(name, entry)}>{t('Edit')}</button>
                        <button type="button" className="icon-button" disabled={busy} aria-label={t('Delete')} onClick={() => void deleteProvider(name)}><Trash2 size={14} /></button>
                      </div>
                    </div>
                  )
                })}
              </div>

              <form className="settings-form" onSubmit={submitProvider}>
                <div className="settings-form-head">
                  <h3>{editingProvider ? `${t('Edit provider')}: ${editingProvider}` : t('Add provider')}</h3>
                  {editingProvider && <button type="button" className="plain-button" onClick={resetProviderForm}>{t('New')}</button>}
                </div>
                <div className="settings-form-grid">
                  <label className="form-field">{t('Provider ID')}
                    <input required disabled={Boolean(editingProvider) || busy} value={provId} onChange={(e) => setProvId(e.target.value)} placeholder="deepseek / openai / anthropic" />
                  </label>
                  <label className="form-field">{t('Protocol')}
                    <select value={provProtocol} disabled={busy} onChange={(e) => setProvProtocol(e.target.value as 'responses' | 'anthropic-messages')}>
                      <option value="responses">OpenAI Responses</option>
                      <option value="anthropic-messages">Anthropic Messages</option>
                    </select>
                  </label>
                </div>
                <label className="form-field">{t('Base URL')}
                  <input required type="url" disabled={busy} value={provBaseURL} onChange={(e) => setProvBaseURL(e.target.value)} placeholder={provProtocol === 'responses' ? 'https://api.deepseek.com' : 'https://api.anthropic.com'} />
                </label>
                <div className="settings-form-grid">
                  <label className="form-field">{t('Credential source')}
                    <select value={provAuthMode} disabled={busy} onChange={(e) => setProvAuthMode(e.target.value as 'key' | 'env')}>
                      <option value="key">{t('API Key (save in SQLite DB)')}</option>
                      <option value="env">{t('Environment variable (apiKeyEnv)')}</option>
                    </select>
                  </label>
                  {provAuthMode === 'key' ? (
                    <label className="form-field">{t('API key')}
                      <input
                        type="password"
                        autoComplete="new-password"
                        disabled={busy || provClearKey}
                        value={provAPIKey}
                        onChange={(e) => setProvAPIKey(e.target.value)}
                        placeholder={editingProvider && view?.savedKeys.includes(editingProvider) ? t('Leave blank to keep existing DB key') : 'sk-...'}
                      />
                    </label>
                  ) : (
                    <label className="form-field">{t('Environment variable name')}
                      <input required disabled={busy} value={provAPIKeyEnv} onChange={(e) => setProvAPIKeyEnv(e.target.value)} placeholder="LUNE_DEEPSEEK_API_KEY" />
                    </label>
                  )}
                </div>
                <div className="settings-checkboxes">
                  {provProtocol === 'responses' && (
                    <label className="remember-token">
                      <input type="checkbox" checked={provCompaction} disabled={busy} onChange={(e) => setProvCompaction(e.target.checked)} />
                      {t('Enable server compaction')}
                    </label>
                  )}
                  {provAuthMode === 'key' && editingProvider && view?.savedKeys.includes(editingProvider) && (
                    <label className="remember-token">
                      <input type="checkbox" checked={provClearKey} disabled={busy} onChange={(e) => setProvClearKey(e.target.checked)} />
                      {t('Remove saved API key')}
                    </label>
                  )}
                </div>
                <div className="settings-form-footer">
                  <button type="button" className="secondary-button" disabled={busy || testingProvider || !api.testProvider} onClick={() => void testCurrentProvider()}>
                    <Activity size={13} aria-hidden="true" />{t(testingProvider ? 'Testing connection…' : 'Test connection')}
                  </button>
                  <button type="submit" className="primary-button compact" disabled={busy}>
                    <Plus size={14} aria-hidden="true" />
                    {editingProvider ? t('Update provider') : t('Save provider')}
                  </button>
                </div>
                {providerTestMessage && <p className={providerTestOK ? 'form-status' : 'form-error'} role={providerTestOK ? 'status' : 'alert'}>{providerTestMessage}</p>}
              </form>

              <details className="settings-advanced">
                <summary>{t('Advanced JSON editor')}</summary>
                <div className="settings-advanced-body">
                  <textarea rows={10} value={rawJSON} disabled={busy} spellCheck={false} onChange={(e) => setRawJSON(e.target.value)} />
                  <button
                    type="button"
                    className="secondary-button"
                    disabled={busy}
                    onClick={() => {
                      let parsed: ConfigDocument
                      try { parsed = JSON.parse(rawJSON) as ConfigDocument }
                      catch { setError(t('Enter valid JSON.')); return }
                      void commitDocument(parsed, imported?.id)
                    }}
                  >
                    {t('Save JSON')}
                  </button>
                </div>
              </details>
            </div>
          )}

          {tab === 'models' && api.supportsModelConfig && (
            <div className="settings-pane">
              <div className="settings-cards">
                {profileNames.length === 0 && <p className="inspector-hint">{t('No model profiles configured in this scope yet.')}</p>}
                {profileNames.map((name) => {
                  const entry = catalog.profiles[name]
                  const isDefault = catalog.defaultProfile === name
                  return (
                    <div className="settings-card" key={name}>
                      <div className="settings-card-main">
                        <div className="settings-card-title">
                          <strong>{entry.displayName || name}</strong>
                          <code>{name}</code>
                          <span className="status-pill">{entry.provider} / {entry.model}</span>
                          {isDefault && <span className="status-pill active">{t('Default')}</span>}
                        </div>
                        <small>
                          {entry.contextWindow ? `${t('Context')}: ${entry.contextWindow.toLocaleString()}` : `${t('Context')}: ${t('Auto')}`}
                          {entry.maxOutputTokens ? ` · ${t('Max output')}: ${entry.maxOutputTokens.toLocaleString()}` : ''}
                          {entry.supportsInputImages ? ` · ${t('Images')}` : ''}
                        </small>
                      </div>
                      <div className="settings-card-actions">
                        {!isDefault && (
                          <button type="button" className="plain-button" disabled={busy} onClick={() => void setDefaultProfile(name)}>{t('Set default')}</button>
                        )}
                        <button type="button" className="secondary-button" disabled={busy} onClick={() => startEditProfile(name, entry)}>{t('Edit')}</button>
                        <button type="button" className="icon-button" disabled={busy} aria-label={t('Delete')} onClick={() => void deleteProfile(name)}><Trash2 size={14} /></button>
                      </div>
                    </div>
                  )
                })}
              </div>

              <form className="settings-form" onSubmit={submitProfile}>
                <div className="settings-form-head">
                  <h3>{editingProfile ? `${t('Edit model')}: ${editingProfile}` : t('Add model')}</h3>
                  {editingProfile && <button type="button" className="plain-button" onClick={resetProfileForm}>{t('New')}</button>}
                </div>
                <div className="settings-form-grid">
                  <label className="form-field">{t('Provider')}
                    <select required value={activeModelProvider} disabled={busy} onChange={(e) => { setProfProvider(e.target.value); setFetchModelsError('') }}>
                      <option value="">{t('Choose provider…')}</option>
                      {availableProvidersForModel.map((name) => <option key={name} value={name}>{name}</option>)}
                    </select>
                  </label>
                  <label className="form-field">{t('Profile ID')}
                    <input required disabled={Boolean(editingProfile) || busy} value={profId} onChange={(e) => setProfId(e.target.value)} placeholder="deepseek-chat / gpt5" />
                  </label>
                </div>

                {api.fetchProviderModels && activeModelProvider && (
                  <div className="model-fetch-box">
                    <div className="model-fetch-head">
                      <span>{t('Available models from provider')}</span>
                      <div className="model-fetch-actions">
                        <div className="model-fetch-search">
                          <Search size={12} aria-hidden="true" />
                          <input
                            value={modelFilter}
                            onChange={(e) => setModelFilter(e.target.value)}
                            placeholder={t('Search fetched models…')}
                            aria-label={t('Search fetched models…')}
                          />
                        </div>
                        <button
                          type="button"
                          className="secondary-button"
                          disabled={busy || fetchingModels}
                          onClick={() => void fetchModelsForProvider(activeModelProvider)}
                        >
                          <RefreshCw size={12} className={fetchingModels ? 'spin' : undefined} aria-hidden="true" />
                          {t(fetchingModels ? 'Fetching models…' : 'Fetch models')}
                        </button>
                      </div>
                    </div>
                    {fetchModelsError && <small className="inline-error">{fetchModelsError}</small>}
                    {!fetchModelsError && Object.hasOwn(fetchedModels, activeModelProvider) && (
                      fetchedModels[activeModelProvider].length === 0 ? (
                        <small className="inspector-hint">{t('No models returned by provider.')}</small>
                      ) : (
                        <>
                          <div className="model-fetch-list" role="listbox" aria-label={t('Available models from provider')}>
                            {fetchedModels[activeModelProvider]
                              .filter((m) => !modelFilter.trim() || m.id.toLowerCase().includes(modelFilter.trim().toLowerCase()) || (m.displayName || '').toLowerCase().includes(modelFilter.trim().toLowerCase()))
                              .map((m) => (
                                <button
                                  type="button"
                                  key={m.id}
                                  className={`model-fetch-item${profModel === m.id ? ' chosen' : ''}`}
                                  title={m.displayName || m.id}
                                  onClick={() => {
                                    setProfModel(m.id)
                                    if (!editingProfile && !profId.trim()) setProfId(m.id.replace(/[^a-zA-Z0-9._-]+/g, '-'))
                                    if (!profDisplayName.trim() && m.displayName) setProfDisplayName(m.displayName)
                                  }}
                                >
                                  <span>{m.id}</span>
                                </button>
                              ))}
                          </div>
                          <small className="inspector-hint">{t('Click a model below to populate Model ID, or enter it manually.')}</small>
                        </>
                      )
                    )}
                  </div>
                )}
                <div className="settings-form-grid">
                  <label className="form-field">{t('Model ID')}
                    <input required disabled={busy} value={profModel} onChange={(e) => setProfModel(e.target.value)} placeholder="deepseek-chat / gpt-5" />
                  </label>
                  <label className="form-field">{t('Display name')}
                    <input disabled={busy} value={profDisplayName} onChange={(e) => setProfDisplayName(e.target.value)} placeholder="DeepSeek V3" />
                  </label>
                </div>
                <div className="settings-form-grid">
                  <label className="form-field">{t('Context window (0 = auto)')}
                    <input type="number" min={0} disabled={busy} value={profContextWindow} onChange={(e) => setProfContextWindow(e.target.value)} />
                  </label>
                  <label className="form-field">{t('Max output tokens (0 = auto)')}
                    <input type="number" min={0} disabled={busy} value={profMaxOutput} onChange={(e) => setProfMaxOutput(e.target.value)} />
                  </label>
                </div>
                <div className="settings-checkboxes">
                  <label className="remember-token"><input type="checkbox" checked={profImages} disabled={busy} onChange={(e) => setProfImages(e.target.checked)} />{t('Supports image input')}</label>
                  <label className="remember-token"><input type="checkbox" checked={profAudio} disabled={busy} onChange={(e) => setProfAudio(e.target.checked)} />{t('Supports audio input')}</label>
                  <label className="remember-token"><input type="checkbox" checked={profAsDefault} disabled={busy} onChange={(e) => setProfAsDefault(e.target.checked)} />{t('Set as default model')}</label>
                </div>
                <div className="settings-form-footer">
                  <button type="submit" className="primary-button compact" disabled={busy || providerNames.length === 0}>
                    <Check size={14} aria-hidden="true" />
                    {editingProfile ? t('Update model') : t('Save model')}
                  </button>
                </div>
              </form>
            </div>
          )}

          {tab === 'runtime' && api.supportsModelConfig && (
            <div className="settings-pane">
              <div className="settings-form">
                <div className="settings-form-head"><h3>{t('Runtime & Agents')}</h3></div>
                <p className="inspector-hint">{t('These limits apply to new tasks. Running tasks keep their current settings.')}</p>
                <div className="settings-form-grid">
                  <label className="form-field">{t('Parallel agents (0 = automatic)')}
                    <input type="number" min={0} value={Number(asObject(doc.runtime).maxParallelAgents) || ''} onChange={(e) => updateConfigField('runtime', 'maxParallelAgents', e.target.value ? Number(e.target.value) : '')} />
                  </label>
                  <label className="form-field">{t('Context window (0 = automatic)')}
                    <input type="number" min={0} value={Number(asObject(doc.runtime).contextWindow) || ''} onChange={(e) => updateConfigField('runtime', 'contextWindow', e.target.value ? Number(e.target.value) : '')} />
                  </label>
                </div>
                <div className="settings-form-grid">
                  <label className="form-field">{t('Children per agent (0 = default)')}
                    <input type="number" min={0} value={Number(asObject(doc.runtime).maxTeamChildrenPerAgent) || ''} onChange={(e) => updateConfigField('runtime', 'maxTeamChildrenPerAgent', e.target.value ? Number(e.target.value) : '')} />
                  </label>
                  <label className="form-field">{t('Delegation depth (0 = default)')}
                    <input type="number" min={0} value={Number(asObject(doc.runtime).maxTeamDepth) || ''} onChange={(e) => updateConfigField('runtime', 'maxTeamDepth', e.target.value ? Number(e.target.value) : '')} />
                  </label>
                </div>
                <label className="remember-token"><input type="checkbox" checked={asObject(doc.runtime).autoCompact !== false} onChange={(e) => updateConfigField('runtime', 'autoCompact', e.target.checked)} />{t('Automatically compact context')}</label>
                <div className="settings-form-grid">
                  <label className="form-field">{t('Compaction threshold (0.1–1.0)')}
                    <input type="number" min="0.1" max="1" step="0.05" value={Number(asObject(doc.runtime).autoCompactThreshold) || ''} onChange={(e) => updateConfigField('runtime', 'autoCompactThreshold', e.target.value ? Number(e.target.value) : '')} />
                  </label>
                  <label className="form-field">{t('Reserve output tokens (0 = default)')}
                    <input type="number" min={0} value={Number(asObject(doc.runtime).reserveOutputTokens) || ''} onChange={(e) => updateConfigField('runtime', 'reserveOutputTokens', e.target.value ? Number(e.target.value) : '')} />
                  </label>
                </div>
                <h3>{t('Attachment limits')}</h3>
                <div className="settings-form-grid">
                  <label className="form-field">{t('Maximum files')}
                    <input type="number" min={0} value={Number(asObject(doc.attachments).maxFiles) || ''} onChange={(e) => updateConfigField('attachments', 'maxFiles', e.target.value ? Number(e.target.value) : '')} />
                  </label>
                  <label className="form-field">{t('Maximum size per file (bytes)')}
                    <input type="number" min={0} value={Number(asObject(doc.attachments).maxFileBytes) || ''} onChange={(e) => updateConfigField('attachments', 'maxFileBytes', e.target.value ? Number(e.target.value) : '')} />
                  </label>
                </div>
                <label className="form-field">{t('Maximum combined attachment size (bytes)')}
                  <input type="number" min={0} value={Number(asObject(doc.attachments).maxTotalBytes) || ''} onChange={(e) => updateConfigField('attachments', 'maxTotalBytes', e.target.value ? Number(e.target.value) : '')} />
                </label>
                <div className="settings-form-footer">
                  <button type="button" className="primary-button compact" disabled={busy} onClick={() => void commitDocument(doc, imported?.id, t('Runtime settings saved to SQLite database.'))}><Check size={14} />{t('Save runtime settings')}</button>
                </div>
              </div>
            </div>
          )}

          {tab === 'remote' && (
            <div className="settings-pane">
              {isRemoteActive && (
                <div className="settings-card">
                  <div className="settings-card-main">
                    <div className="settings-card-title">
                      <strong>{t('Connected to Remote Core')}</strong>
                      <span className="status-pill active">{t('Connected')}</span>
                    </div>
                    <small>{activeRemoteURL}</small>
                  </div>
                  <div className="settings-card-actions">
                    {hasLocalHost && onReturnToLocal && (
                      <button type="button" className="secondary-button" disabled={busy} onClick={() => void onReturnToLocal().then(onClose)}>
                        <FolderGit2 size={14} aria-hidden="true" />{t('Return to local workspaces')}
                      </button>
                    )}
                    {onDisconnectRemote && (
                      <button type="button" className="plain-button" disabled={busy} onClick={() => void onDisconnectRemote().then(onClose)}>{t('Disconnect')}</button>
                    )}
                  </div>
                </div>
              )}

              <div className="settings-cards">
                {connections.length === 0 && <p className="inspector-hint">{t('No saved remote Core endpoints yet.')}</p>}
                {connections.map((saved) => (
                  <div className="settings-card" key={saved.address}>
                    <div className="settings-card-main">
                      <div className="settings-card-title">
                        <strong>{saved.name || saved.address}</strong>
                        {saved.remembered && <span className="status-pill active">{t('Token saved in DB')}</span>}
                      </div>
                      <small>{saved.address}</small>
                    </div>
                    <div className="settings-card-actions">
                      <button type="button" className="secondary-button" disabled={busy} onClick={() => void connectSavedRemote(saved)}>{t('Connect')}</button>
                      <button
                        type="button"
                        className="icon-button"
                        disabled={busy}
                        aria-label={t('Delete')}
                        onClick={() => void api.forgetConnection('remote', saved.address).then(loadConnections).catch((err) => setError(err instanceof Error ? err.message : String(err)))}
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                  </div>
                ))}
              </div>

              <form className="settings-form" onSubmit={(e) => { e.preventDefault(); void saveRemoteForm(true) }}>
                <div className="settings-form-head">
                  <h3>{t('Remote Core endpoint')}</h3>
                </div>
                <label className="form-field">{t('Core WebSocket URL')}
                  <input required disabled={busy} value={remoteURL} onChange={(e) => setRemoteURL(e.target.value)} placeholder="wss://core.example.com:18092/v1/runtime" autoCapitalize="none" spellCheck={false} />
                </label>
                <div className="settings-form-grid">
                  <label className="form-field">{t('Connection name')}
                    <input disabled={busy} value={remoteName} onChange={(e) => setRemoteName(e.target.value)} placeholder={t('Optional label')} />
                  </label>
                  <label className="form-field">{t('Access token')}
                    <input type="password" autoComplete="off" disabled={busy} value={remoteToken} onChange={(e) => setRemoteToken(e.target.value)} placeholder={t('Paste your Core token')} />
                  </label>
                </div>
                <label className="remember-token">
                  <input type="checkbox" checked={rememberToken} disabled={busy} onChange={(e) => setRememberToken(e.target.checked)} />
                  {t('Save token in SQLite database')}
                </label>
                <div className="settings-form-footer">
                  <button type="button" className="secondary-button" disabled={busy || !remoteURL.trim()} onClick={() => void saveRemoteForm(false)}>{t('Save only')}</button>
                  <button type="submit" className="primary-button compact" disabled={busy || !remoteURL.trim()}>
                    <RefreshCw size={14} aria-hidden="true" />{t('Save & Connect')}
                  </button>
                </div>
              </form>
            </div>
          )}

          {tab === 'general' && (
            <div className="settings-pane">
              <div className="settings-form">
                <div className="kv-row">
                  <span>{t('Appearance')}</span>
                  <ThemePicker value={theme} onChange={(value) => void saveUiPreference({ theme: value })} />
                </div>
                <label className="kv-row">{t('Interface language')}
                  <select value={language} onChange={(event) => void saveUiPreference({ language: event.target.value as UiPreferences['language'] })}>
                    <option value="en">English</option><option value="zh">简体中文</option>
                  </select>
                </label>
                <div className="settings-form-grid">
                  <label className="form-field">{t('Message text size')}
                    <select value={uiPreferences.bodyFontSize} onChange={(event) => void saveUiPreference({ bodyFontSize: Number(event.target.value) })}>{[12, 13, 14, 15, 16, 17, 18].map((size) => <option key={size} value={size}>{size} px</option>)}</select>
                  </label>
                  <label className="form-field">{t('Code text size')}
                    <select value={uiPreferences.codeFontSize} onChange={(event) => void saveUiPreference({ codeFontSize: Number(event.target.value) })}>{[10, 11, 12, 13, 14, 15, 16].map((size) => <option key={size} value={size}>{size} px</option>)}</select>
                  </label>
                </div>
                <label className="form-field">{t('Pressing Enter while a task is running')}
                  <select value={uiPreferences.runningEnter} onChange={(event) => void saveUiPreference({ runningEnter: event.target.value as UiPreferences['runningEnter'] })}>
                    <option value="queue">{t('Queue a follow-up')}</option><option value="steer">{t('Interrupt with this message')}</option>
                  </select>
                </label>
                <label className="remember-token"><input type="checkbox" checked={uiPreferences.timelineDetailsExpanded} onChange={(event) => void saveUiPreference({ timelineDetailsExpanded: event.target.checked })} />{t('Expand tool and reasoning details by default')}</label>
                <label className="form-field">{t('Usage display density')}
                  <select value={uiPreferences.usageDensity} onChange={(event) => void saveUiPreference({ usageDensity: event.target.value as UiPreferences['usageDensity'] })}>
                    <option value="comfortable">{t('Comfortable')}</option><option value="compact">{t('Compact')}</option>
                  </select>
                </label>
                {api.supportsShortcut && window.luneDesktop && (
                  <div className="form-field">
                    <label htmlFor="settings-quick-shortcut">{t('Quick window shortcut')}</label>
                    <div className="input-with-action">
                      <input id="settings-quick-shortcut" value={shortcut} onChange={(e) => setShortcut(e.target.value)} />
                      <button
                        type="button"
                        className="secondary-button"
                        onClick={() => void window.luneDesktop?.setShortcut(shortcut).then(setShortcut).catch((err) => setError(err instanceof Error ? err.message : String(err)))}
                      >
                        {t('Apply')}
                      </button>
                    </div>
                  </div>
                )}
              </div>
            </div>
          )}
        </div>
      </div>
    </dialog>
  )
}
