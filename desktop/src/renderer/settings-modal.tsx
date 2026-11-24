import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import {
  Activity, Check, ChevronDown, Cpu, Database, Eye, EyeOff,
  FolderGit2, Globe, KeyRound, Plus, RefreshCw, Search, SlidersHorizontal,
  Star, Trash2, Upload, X,
} from 'lucide-react'
import type { ConfigDocument, ConfigImport, ConfigView, ProviderModelInfo, ProviderTestInput, SavedConnection, UiPreferences } from '../shared/desktop-settings'
import type { ThemePreference } from '../shared/theme'
import { useLanguage, useTranslate } from './language'
import type { FrontendSettingsAPI, SaveConfigResult } from './settings-api'
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

export type SettingsTab = 'models' | 'providers' | 'runtime' | 'remote' | 'general'

function normalizeTab(tab?: SettingsTab): Exclude<SettingsTab, 'providers'> | undefined {
  if (!tab) return undefined
  return tab === 'providers' ? 'models' : tab
}

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

interface DraftModelRow {
  profileId: string
  originalProfileId?: string
  model: string
  displayName: string
  contextWindow: string
  maxOutputTokens: string
  supportsInputImages: boolean
  supportsInputAudio: boolean
  expanded?: boolean
}

interface ProviderPreset {
  id: string
  name: string
  protocol: 'responses' | 'anthropic-messages'
  baseURL: string
  hint: string
  keyPlaceholder: string
}

const PROVIDER_PRESETS: readonly ProviderPreset[] = [
  { id: 'openai', name: 'OpenAI', protocol: 'responses', baseURL: 'https://api.openai.com/v1', hint: 'OpenAI Responses API', keyPlaceholder: 'sk-...' },
  { id: 'anthropic', name: 'Anthropic', protocol: 'anthropic-messages', baseURL: 'https://api.anthropic.com', hint: 'Claude Messages API', keyPlaceholder: 'sk-ant-...' },
  { id: 'deepseek', name: 'DeepSeek', protocol: 'responses', baseURL: 'https://api.deepseek.com', hint: 'Responses API', keyPlaceholder: 'sk-...' },
  { id: 'openrouter', name: 'OpenRouter', protocol: 'responses', baseURL: 'https://openrouter.ai/api/v1', hint: 'Multi-provider Gateway', keyPlaceholder: 'sk-or-...' },
  { id: 'ollama', name: 'Ollama', protocol: 'responses', baseURL: 'http://127.0.0.1:11434/v1', hint: 'Local OpenAI-compatible', keyPlaceholder: 'ollama' },
]

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

export function formatCapacity(tokens?: number): string {
  if (!tokens || tokens <= 0) return ''
  if (tokens >= 1_000_000 && tokens % 1_000_000 === 0) return `${tokens / 1_000_000}M`
  if (tokens >= 1_048_576 && tokens % 1_048_576 === 0) return `${tokens / 1_048_576}M`
  if (tokens >= 1_000 && tokens % 1_000 === 0) return `${tokens / 1_000}K`
  if (tokens >= 1024 && tokens % 1024 === 0) return `${tokens / 1024}K`
  return String(tokens)
}

function parseCapacity(raw: string): number {
  const cleaned = raw.trim().replace(/,/g, '')
  if (!cleaned) return 0
  const match = cleaned.match(/^(\d+(?:\.\d+)?)\s*([kKmM])?$/)
  if (!match) return 0
  const num = Number.parseFloat(match[1])
  if (!Number.isFinite(num) || num <= 0) return 0
  const suffix = (match[2] || '').toUpperCase()
  if (suffix === 'M') return Math.round(num * 1_000_000)
  if (suffix === 'K') return Math.round(num * 1_000)
  return Math.round(num)
}

function makeUniqueId(base: string, taken: Set<string>): string {
  const stem = base.trim().replace(/[^a-zA-Z0-9._-]+/g, '-').replace(/^-+|-+$/g, '') || 'model'
  if (!taken.has(stem)) return stem
  for (let i = 2; ; i++) {
    const candidate = `${stem}-${i}`
    if (!taken.has(candidate)) return candidate
  }
}

function protocolBadge(protocol: string): string {
  return protocol === 'anthropic-messages' ? 'Anthropic' : 'Responses'
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
  const [tab, setTab] = useState<Exclude<SettingsTab, 'providers'>>(normalizeTab(initialTab) || (api.supportsModelConfig ? 'models' : 'remote'))
  const [scope, setScope] = useState('')
  const [view, setView] = useState<ConfigView | null>(null)
  const [doc, setDoc] = useState<ConfigDocument>({})
  const [rawJSON, setRawJSON] = useState('{}')
  const [imported, setImported] = useState<ConfigImport | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [status, setStatus] = useState('')
  const [uiPreferences, setUiPreferences] = useState<UiPreferences>(defaultUiPreferences)

  // Unified Provider + Models editor state (one card open at a time)
  const [editorMode, setEditorMode] = useState<'closed' | 'add' | 'edit'>('closed')
  const [addKind, setAddKind] = useState<'preset' | 'custom'>('preset')
  const [selectedPreset, setSelectedPreset] = useState<string>('openai')
  const [editingProviderId, setEditingProviderId] = useState('')
  const [confirmDeleteId, setConfirmDeleteId] = useState('')

  const [provId, setProvId] = useState('')
  const [provProtocol, setProvProtocol] = useState<'responses' | 'anthropic-messages'>('responses')
  const [provBaseURL, setProvBaseURL] = useState('')
  const [provAuthMode, setProvAuthMode] = useState<'key' | 'env'>('key')
  const [provAPIKey, setProvAPIKey] = useState('')
  const [showAPIKey, setShowAPIKey] = useState(false)
  const [provClearKey, setProvClearKey] = useState(false)
  const [provAPIKeyEnv, setProvAPIKeyEnv] = useState('')
  const [provCompaction, setProvCompaction] = useState(true)
  const [draftModels, setDraftModels] = useState<DraftModelRow[]>([])
  const [draftDefaultProfile, setDraftDefaultProfile] = useState('')

  // Probe & multi-select picker state inside active editor
  const [testingProvider, setTestingProvider] = useState(false)
  const [providerTestMessage, setProviderTestMessage] = useState('')
  const [providerTestOK, setProviderTestOK] = useState(false)
  const [pickerOpen, setPickerOpen] = useState(false)
  const [discoveredModels, setDiscoveredModels] = useState<ProviderModelInfo[]>([])
  const [checkedModelIds, setCheckedModelIds] = useState<Set<string>>(() => new Set())
  const [modelFilter, setModelFilter] = useState('')

  // Remote connections state
  const [connections, setConnections] = useState<SavedConnection[]>([])
  const [remoteURL, setRemoteURL] = useState(activeRemoteURL)
  const [remoteName, setRemoteName] = useState('')
  const [remoteToken, setRemoteToken] = useState('')
  const [rememberToken, setRememberToken] = useState(true)
  const [shortcut, setShortcut] = useState('CommandOrControl+Shift+Space')

  const wasOpen = useRef(false)
  useEffect(() => {
    if (!open) {
      wasOpen.current = false
      return
    }
    if (!wasOpen.current) {
      wasOpen.current = true
      setTab(normalizeTab(initialTab) || (api.supportsModelConfig ? 'models' : 'remote'))
      setError('')
      setStatus('')
      setEditorMode('closed')
      setConfirmDeleteId('')
      setPickerOpen(false)
      setProviderTestMessage('')
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

  const catalog = useMemo(() => normalizeDoc(doc), [doc])
  const providerNames = Object.keys(catalog.providers)
  const profileNames = Object.keys(catalog.profiles)

  if (!open) return null

  const closeEditor = () => {
    setEditorMode('closed')
    setEditingProviderId('')
    setPickerOpen(false)
    setDiscoveredModels([])
    setProviderTestMessage('')
    setModelFilter('')
  }

  const applyPreset = (preset: ProviderPreset, existingIds: string[]) => {
    const taken = new Set(existingIds)
    const uniqueId = makeUniqueId(preset.id, taken)
    setSelectedPreset(preset.id)
    setProvId(uniqueId)
    setProvProtocol(preset.protocol)
    setProvBaseURL(preset.baseURL)
    setProvAuthMode('key')
    setProvAPIKey('')
    setShowAPIKey(false)
    setProvClearKey(false)
    setProvAPIKeyEnv('')
    setProvCompaction(preset.protocol === 'responses')
    setDraftModels([])
    setDraftDefaultProfile('')
    setPickerOpen(false)
    setDiscoveredModels([])
    setProviderTestMessage('')
  }

  const openAddProvider = () => {
    setError('')
    setStatus('')
    setConfirmDeleteId('')
    setEditorMode('add')
    setEditingProviderId('')
    const unusedPreset = PROVIDER_PRESETS.find((p) => !providerNames.includes(p.id)) ?? PROVIDER_PRESETS[0]
    setAddKind('preset')
    applyPreset(unusedPreset, providerNames)
  }

  const openCustomAdd = () => {
    setAddKind('custom')
    setSelectedPreset('')
    setProvId('')
    setProvProtocol('responses')
    setProvBaseURL('')
    setProvAuthMode('key')
    setProvAPIKey('')
    setShowAPIKey(false)
    setProvClearKey(false)
    setProvAPIKeyEnv('')
    setProvCompaction(true)
    setDraftModels([])
    setDraftDefaultProfile('')
    setPickerOpen(false)
    setDiscoveredModels([])
    setProviderTestMessage('')
  }

  const openEditProvider = (name: string) => {
    const entry = catalog.providers[name]
    if (!entry) return
    setError('')
    setStatus('')
    setConfirmDeleteId('')
    setEditorMode('edit')
    setEditingProviderId(name)
    setProvId(name)
    setProvProtocol(entry.protocol === 'anthropic-messages' ? 'anthropic-messages' : 'responses')
    setProvBaseURL(entry.baseURL || '')
    setProvAuthMode(entry.apiKeyEnv ? 'env' : 'key')
    setProvAPIKey('')
    setShowAPIKey(false)
    setProvClearKey(false)
    setProvAPIKeyEnv(entry.apiKeyEnv || '')
    setProvCompaction(Boolean(entry.serverCompaction))

    const rows: DraftModelRow[] = Object.entries(catalog.profiles)
      .filter(([, prof]) => prof.provider === name)
      .map(([profKey, prof]) => ({
        profileId: profKey,
        originalProfileId: profKey,
        model: prof.model,
        displayName: prof.displayName || '',
        contextWindow: formatCapacity(prof.contextWindow),
        maxOutputTokens: formatCapacity(prof.maxOutputTokens),
        supportsInputImages: Boolean(prof.supportsInputImages),
        supportsInputAudio: Boolean(prof.supportsInputAudio),
      }))
    setDraftModels(rows)
    setDraftDefaultProfile(rows.some((r) => r.profileId === catalog.defaultProfile) ? catalog.defaultProfile : '')
    setPickerOpen(false)
    setDiscoveredModels([])
    setProviderTestMessage('')
  }

  const commitDocument = async (nextDoc: ConfigDocument, importId?: string, message?: string) => {
    setBusy(true)
    setError('')
    setStatus('')
    try {
      const result = await api.saveConfig(scope, nextDoc, importId)
      const [followup] = await Promise.all([
        onSavedConfig?.(result),
        loadConfig(scope),
      ])
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

  const testAndFetchModels = async () => {
    const provider = (editorMode === 'edit' ? editingProviderId : provId).trim()
    if (!provider || !provBaseURL.trim()) {
      setError(t('Enter a provider ID and Base URL before testing.'))
      return
    }
    setTestingProvider(true)
    setProviderTestMessage('')
    setError('')
    try {
      let models: ProviderModelInfo[] = []
      let durationMs = 0
      if (api.testProvider) {
        const input: ProviderTestInput = {
          provider,
          protocol: provProtocol,
          baseURL: provBaseURL.trim(),
          ...(provAuthMode === 'env'
            ? { apiKeyEnv: provAPIKeyEnv.trim() }
            : provClearKey ? { apiKey: '' } : provAPIKey ? { apiKey: provAPIKey } : {}),
        }
        const result = await api.testProvider(scope, input)
        durationMs = result.durationMs
        if (!result.ok) {
          setProviderTestOK(false)
          setProviderTestMessage(t(result.error || 'Provider connection failed. Check the URL, credentials, and network.'))
          return
        }
        models = result.models ?? []
        if (models.length === 0 && api.fetchProviderModels && editorMode === 'edit') {
          models = await api.fetchProviderModels(scope, provider).catch(() => [])
        }
      } else if (api.fetchProviderModels) {
        models = await api.fetchProviderModels(scope, provider)
      }
      setProviderTestOK(true)
      setProviderTestMessage(`${t('Connection successful')}: ${models.length} ${t('models in')} ${durationMs} ms`)
      setDiscoveredModels(models)
      const currentIds = new Set(draftModels.map((row) => row.model))
      setCheckedModelIds(new Set(models.filter((m) => currentIds.has(m.id)).map((m) => m.id)))
      setModelFilter('')
      setPickerOpen(true)
    } catch (err) {
      setProviderTestOK(false)
      setProviderTestMessage(err instanceof Error ? err.message : t('Provider connection failed. Check the URL, credentials, and network.'))
    } finally {
      setTestingProvider(false)
    }
  }

  const applyCheckedModels = () => {
    const targetProvider = (editorMode === 'edit' ? editingProviderId : provId).trim()
    const takenProfileIds = new Set(
      Object.entries(catalog.profiles)
        .filter(([, p]) => p.provider !== targetProvider)
        .map(([k]) => k),
    )
    const discoveredMap = new Map(discoveredModels.map((m) => [m.id, m]))
    const discoveredIds = new Set(discoveredModels.map((m) => m.id))

    const kept = draftModels.filter((row) => !discoveredIds.has(row.model) || checkedModelIds.has(row.model))
    for (const row of kept) takenProfileIds.add(row.profileId)

    const existingModels = new Set(kept.map((row) => row.model))
    const nextRows = [...kept]
    for (const id of checkedModelIds) {
      if (existingModels.has(id)) continue
      const meta = discoveredMap.get(id)
      const profileId = makeUniqueId(id, takenProfileIds)
      takenProfileIds.add(profileId)
      nextRows.push({
        profileId,
        model: id,
        displayName: meta?.displayName || '',
        contextWindow: formatCapacity(meta?.contextWindow),
        maxOutputTokens: formatCapacity(meta?.maxOutputTokens) || (provProtocol === 'anthropic-messages' ? '8192' : ''),
        supportsInputImages: Boolean(meta?.supportsInputImages),
        supportsInputAudio: Boolean(meta?.supportsInputAudio),
      })
    }
    setDraftModels(nextRows)
    if (nextRows.length > 0 && (!draftDefaultProfile || !nextRows.some((r) => r.profileId === draftDefaultProfile))) {
      setDraftDefaultProfile((prev) => (prev && nextRows.some((r) => r.profileId === prev) ? prev : nextRows[0].profileId))
    }
    setPickerOpen(false)
  }

  const addManualModelRow = () => {
    const targetProvider = (editorMode === 'edit' ? editingProviderId : provId).trim()
    const taken = new Set([
      ...Object.entries(catalog.profiles).filter(([, p]) => p.provider !== targetProvider).map(([k]) => k),
      ...draftModels.map((r) => r.profileId),
    ])
    const profileId = makeUniqueId(targetProvider ? `${targetProvider}-model` : 'model', taken)
    const nextRow: DraftModelRow = {
      profileId,
      model: '',
      displayName: '',
      contextWindow: '',
      maxOutputTokens: provProtocol === 'anthropic-messages' ? '8192' : '',
      supportsInputImages: false,
      supportsInputAudio: false,
      expanded: true,
    }
    setDraftModels((prev) => [...prev, nextRow])
    if (!draftDefaultProfile) setDraftDefaultProfile(profileId)
  }

  const submitUnifiedProvider = async (event: FormEvent) => {
    event.preventDefault()
    setError('')
    const id = (editorMode === 'edit' ? editingProviderId : provId).trim()
    if (!id) { setError(t('Provider ID is required.')); return }
    if (!provBaseURL.trim()) { setError(t('Enter a valid provider URL.')); return }
    if (provAuthMode === 'env' && !provAPIKeyEnv.trim()) { setError(t('Environment variable name is required.')); return }
    if (draftModels.some((row) => !row.model.trim() || !row.profileId.trim())) {
      setError(t('Each model row requires a Model ID and Profile ID.'))
      return
    }
    if (api.kind === 'desktop' && draftModels.length === 0) {
      setError(t('Add at least one model before saving.'))
      return
    }

    const nextProviders: Record<string, ProviderEntry> = { ...catalog.providers }
    const nextEntry: ProviderEntry = {
      protocol: provProtocol,
      baseURL: provBaseURL.trim(),
      ...(provProtocol === 'responses' ? { serverCompaction: provCompaction } : {}),
    }
    if (provAuthMode === 'env') {
      nextEntry.apiKeyEnv = provAPIKeyEnv.trim()
    } else if (provClearKey) {
      nextEntry.apiKey = ''
    } else if (provAPIKey !== '') {
      nextEntry.apiKey = provAPIKey
    }
    nextProviders[id] = nextEntry

    // Rebuild profiles: keep other providers' profiles intact, replace this provider's profiles cleanly.
    const nextProfiles: Record<string, ProfileEntry> = {}
    for (const [key, prof] of Object.entries(catalog.profiles)) {
      if (prof.provider !== id) nextProfiles[key] = prof
    }
    for (const row of draftModels) {
      const profKey = row.profileId.trim()
      if (Object.hasOwn(nextProfiles, profKey)) {
        setError(`${t('Duplicate profile ID')}: ${profKey}`)
        return
      }
      const ctxWin = parseCapacity(row.contextWindow)
      let maxOut = parseCapacity(row.maxOutputTokens)
      if (provProtocol === 'anthropic-messages' && maxOut === 0) maxOut = 8192
      nextProfiles[profKey] = {
        provider: id,
        model: row.model.trim(),
        ...(row.displayName.trim() ? { displayName: row.displayName.trim() } : {}),
        ...(ctxWin > 0 ? { contextWindow: ctxWin } : {}),
        ...(maxOut > 0 ? { maxOutputTokens: maxOut } : {}),
        ...(row.supportsInputImages ? { supportsInputImages: true } : {}),
        ...(row.supportsInputAudio ? { supportsInputAudio: true } : {}),
      }
    }

    const nextProfileKeys = Object.keys(nextProfiles)
    let nextDefault = catalog.defaultProfile
    const editedDefault = draftDefaultProfile && Object.hasOwn(nextProfiles, draftDefaultProfile) ? draftDefaultProfile : ''
    if (editedDefault) {
      nextDefault = editedDefault
    } else if (!nextDefault || !Object.hasOwn(nextProfiles, nextDefault)) {
      nextDefault = nextProfileKeys[0] || ''
    }

    const nextModelsObj: Record<string, unknown> = {
      ...asObject(doc.models),
      providers: nextProviders,
    }
    if (nextProfileKeys.length > 0) nextModelsObj.profiles = nextProfiles
    else delete nextModelsObj.profiles
    if (nextDefault) nextModelsObj.defaultProfile = nextDefault
    else delete nextModelsObj.defaultProfile

    const nextDoc: ConfigDocument = { ...doc, models: nextModelsObj }
    const ok = await commitDocument(nextDoc, imported?.id, t('Provider and models saved to SQLite database.'))
    if (ok) closeEditor()
  }

  const deleteProvider = async (name: string) => {
    const nextProviders = { ...catalog.providers }
    delete nextProviders[name]
    const nextProfiles: Record<string, ProfileEntry> = {}
    for (const [key, prof] of Object.entries(catalog.profiles)) {
      if (prof.provider !== name) nextProfiles[key] = prof
    }
    const remainingKeys = Object.keys(nextProfiles)
    const nextDefault = Object.hasOwn(nextProfiles, catalog.defaultProfile) ? catalog.defaultProfile : (remainingKeys[0] || '')
    const nextModelsObj: Record<string, unknown> = {
      ...asObject(doc.models),
      providers: nextProviders,
    }
    if (remainingKeys.length > 0) nextModelsObj.profiles = nextProfiles
    else delete nextModelsObj.profiles
    if (nextDefault) nextModelsObj.defaultProfile = nextDefault
    else delete nextModelsObj.defaultProfile

    const ok = await commitDocument({ ...doc, models: nextModelsObj })
    if (ok) {
      setConfirmDeleteId('')
      if (editorMode === 'edit' && editingProviderId === name) closeEditor()
    }
  }

  const setDefaultProfile = async (profileId: string) => {
    if (!Object.hasOwn(catalog.profiles, profileId)) return
    const nextDoc: ConfigDocument = {
      ...doc,
      models: { ...asObject(doc.models), defaultProfile: profileId },
    }
    await commitDocument(nextDoc, undefined, t('Default model updated.'))
  }

  const updateConfigField = (section: 'runtime' | 'attachments', field: string, value: number | boolean | '') => {
    const values = { ...asObject(doc[section]) }
    if (value === '' || value === 0) delete values[field]
    else values[field] = value
    const next = { ...doc, [section]: values }
    setDoc(next)
    setRawJSON(JSON.stringify(next, null, 2))
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

  const navItems: Array<{ id: Exclude<SettingsTab, 'providers'>; label: string; icon: typeof KeyRound; hidden?: boolean }> = [
    { id: 'models', label: 'Models', icon: Cpu, hidden: !api.supportsModelConfig },
    { id: 'runtime', label: 'Runtime & Agents', icon: Activity, hidden: !api.supportsModelConfig },
    { id: 'remote', label: 'Remote & Token', icon: Globe },
    { id: 'general', label: 'General', icon: SlidersHorizontal },
  ]

  const renderUnifiedEditor = () => {
    const isEdit = editorMode === 'edit'
    const currentId = (isEdit ? editingProviderId : provId).trim()
    const hasSavedKey = Boolean(currentId && view?.savedKeys.includes(currentId))
    const activePreset = PROVIDER_PRESETS.find((p) => p.id === selectedPreset)
    const filteredDiscovered = discoveredModels.filter((m) =>
      !modelFilter.trim()
      || m.id.toLowerCase().includes(modelFilter.trim().toLowerCase())
      || (m.displayName || '').toLowerCase().includes(modelFilter.trim().toLowerCase()),
    )

    return (
      <form className="provider-editor-card" onSubmit={submitUnifiedProvider}>
        <div className="settings-form-head">
          <div className="provider-editor-title">
            <KeyRound size={15} aria-hidden="true" />
            <h3>{isEdit ? `${t('Edit provider')}: ${editingProviderId}` : t('Add provider')}</h3>
          </div>
          {!isEdit && (
            <div className="connection-tabs compact-tabs">
              <button type="button" className={addKind === 'preset' ? 'active' : ''} disabled={busy} onClick={() => {
                setAddKind('preset')
                const p = PROVIDER_PRESETS.find((item) => item.id === (selectedPreset || 'openai')) ?? PROVIDER_PRESETS[0]
                applyPreset(p, providerNames)
              }}>{t('Presets')}</button>
              <button type="button" className={addKind === 'custom' ? 'active' : ''} disabled={busy} onClick={openCustomAdd}>{t('Custom endpoint')}</button>
            </div>
          )}
          <button type="button" className="icon-button" aria-label={t('Close')} disabled={busy} onClick={closeEditor}><X size={15} /></button>
        </div>

        {!isEdit && addKind === 'preset' && (
          <div className="preset-grid" role="radiogroup" aria-label={t('Preset providers')}>
            {PROVIDER_PRESETS.map((preset) => (
              <button
                key={preset.id}
                type="button"
                className={`preset-chip${selectedPreset === preset.id ? ' active' : ''}`}
                disabled={busy}
                onClick={() => applyPreset(preset, providerNames)}
              >
                <strong>{preset.name}</strong>
                <span>{preset.hint}</span>
              </button>
            ))}
          </div>
        )}

        <div className="settings-form-grid">
          <label className="form-field">{t('Provider ID')}
            <input
              required
              disabled={isEdit || busy}
              value={provId}
              onChange={(e) => setProvId(e.target.value)}
              placeholder="openai / anthropic / deepseek"
            />
          </label>
          {provAuthMode === 'key' ? (
            <div className="form-field">
              <div className="field-label-row">
                <span>{t('API key')}</span>
                {isEdit && hasSavedKey && !provClearKey && <span className="cred-inline-ok"><span className="cred-dot ok" aria-hidden="true" />{t('Key saved in DB')}</span>}
              </div>
              <div className="secret-input-wrap">
                <input
                  type={showAPIKey ? 'text' : 'password'}
                  autoComplete="new-password"
                  disabled={busy || provClearKey}
                  value={provAPIKey}
                  onChange={(e) => setProvAPIKey(e.target.value)}
                  placeholder={isEdit && hasSavedKey ? t('Leave blank to keep existing DB key') : (activePreset?.keyPlaceholder || 'sk-...')}
                />
                <button
                  type="button"
                  className="subtle-icon"
                  aria-label={t(showAPIKey ? 'Hide key' : 'Show key')}
                  onClick={() => setShowAPIKey((v) => !v)}
                >
                  {showAPIKey ? <EyeOff size={14} /> : <Eye size={14} />}
                </button>
              </div>
            </div>
          ) : (
            <label className="form-field">{t('Environment variable name')}
              <input required disabled={busy} value={provAPIKeyEnv} onChange={(e) => setProvAPIKeyEnv(e.target.value)} placeholder="OPENAI_API_KEY" />
            </label>
          )}
        </div>

        {(isEdit || addKind === 'custom') && (
          <div className="settings-form-grid">
            <label className="form-field">{t('Protocol')}
              <select value={provProtocol} disabled={busy} onChange={(e) => setProvProtocol(e.target.value as 'responses' | 'anthropic-messages')}>
                <option value="responses">OpenAI Responses</option>
                <option value="anthropic-messages">Anthropic Messages</option>
              </select>
            </label>
            <label className="form-field">{t('Base URL')}
              <input required type="url" disabled={busy} value={provBaseURL} onChange={(e) => setProvBaseURL(e.target.value)} placeholder={provProtocol === 'responses' ? 'https://api.openai.com/v1' : 'https://api.anthropic.com'} />
            </label>
          </div>
        )}

        <div className="provider-models-block">
          <div className="provider-models-head">
            <div className="provider-models-label">
              <strong>{t('Enabled models')}</strong>
              <span className="status-pill">{draftModels.length}</span>
            </div>
            <div className="provider-models-actions">
              <button
                type="button"
                className="secondary-button"
                disabled={busy || testingProvider}
                onClick={() => void testAndFetchModels()}
              >
                <RefreshCw size={12} className={testingProvider ? 'spin' : undefined} aria-hidden="true" />
                {t(testingProvider ? 'Fetching models…' : 'Test & fetch models')}
              </button>
              <button type="button" className="secondary-button" disabled={busy} onClick={addManualModelRow}>
                <Plus size={12} aria-hidden="true" />{t('Add model')}
              </button>
            </div>
          </div>

          {providerTestMessage && (
            <p className={providerTestOK ? 'form-status' : 'form-error'} role={providerTestOK ? 'status' : 'alert'}>
              {providerTestMessage}
            </p>
          )}

          {pickerOpen && (
            <div className="model-picker-drawer">
              <div className="model-fetch-head">
                <span>{t('Select models to enable')}</span>
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
                    className="plain-button"
                    onClick={() => setCheckedModelIds((prev) => {
                      const next = new Set(prev)
                      for (const m of filteredDiscovered) next.add(m.id)
                      return next
                    })}
                  >
                    {t('Select all')}
                  </button>
                  <button type="button" className="plain-button" onClick={() => setCheckedModelIds(new Set())}>{t('Clear')}</button>
                </div>
              </div>
              {filteredDiscovered.length === 0 ? (
                <small className="inspector-hint">{t('No models returned by provider.')}</small>
              ) : (
                <div className="model-checklist">
                  {filteredDiscovered.map((m) => {
                    const checked = checkedModelIds.has(m.id)
                    const cap = formatCapacity(m.contextWindow)
                    return (
                      <label key={m.id} className={`model-check-row${checked ? ' checked' : ''}`}>
                        <input
                          type="checkbox"
                          checked={checked}
                          onChange={(e) => setCheckedModelIds((prev) => {
                            const next = new Set(prev)
                            if (e.target.checked) next.add(m.id)
                            else next.delete(m.id)
                            return next
                          })}
                        />
                        <span className="model-check-id">{m.id}</span>
                        {m.displayName && m.displayName !== m.id && <span className="model-check-name">{m.displayName}</span>}
                        <span className="model-check-meta">
                          {cap ? `${cap}` : ''}
                          {m.supportsInputImages ? (cap ? ` · ${t('Images')}` : t('Images')) : ''}
                        </span>
                      </label>
                    )
                  })}
                </div>
              )}
              <div className="model-picker-footer">
                <button type="button" className="plain-button" onClick={() => setPickerOpen(false)}>{t('Cancel')}</button>
                <button type="button" className="primary-button compact" onClick={applyCheckedModels}>
                  <Check size={13} aria-hidden="true" />
                  {t('Apply selection')} ({checkedModelIds.size})
                </button>
              </div>
            </div>
          )}

          {draftModels.length === 0 ? (
            <p className="inspector-hint">{t('No models enabled yet. Click "Test & fetch models" to multi-select, or "Add model" manually.')}</p>
          ) : (
            <div className="model-draft-list">
              {draftModels.map((row, index) => {
                const isDefault = draftDefaultProfile === row.profileId
                return (
                  <div key={`${row.originalProfileId || 'new'}-${index}`} className="model-draft-item">
                    <div className="model-draft-main">
                      <button
                        type="button"
                        className={`row-action-btn star-btn${isDefault ? ' active' : ''}`}
                        title={t('Set as default model')}
                        aria-label={t('Set as default model')}
                        aria-pressed={isDefault}
                        onClick={() => setDraftDefaultProfile(row.profileId)}
                      >
                        <Star size={13} fill={isDefault ? 'currentColor' : 'none'} aria-hidden="true" />
                      </button>
                      <input
                        className="model-draft-id"
                        value={row.model}
                        disabled={busy}
                        placeholder={t('Model ID')}
                        aria-label={t('Model ID')}
                        onChange={(e) => {
                          const value = e.target.value
                          setDraftModels((prev) => prev.map((item, idx) => idx === index ? { ...item, model: value } : item))
                        }}
                      />
                      <input
                        className="model-draft-display"
                        value={row.displayName}
                        disabled={busy}
                        placeholder={t('Display name (optional)')}
                        aria-label={t('Display name')}
                        onChange={(e) => {
                          const value = e.target.value
                          setDraftModels((prev) => prev.map((item, idx) => idx === index ? { ...item, displayName: value } : item))
                        }}
                      />
                      <button
                        type="button"
                        className="row-action-btn"
                        aria-expanded={Boolean(row.expanded)}
                        title={t('Model capacities & options')}
                        onClick={() => setDraftModels((prev) => prev.map((item, idx) => idx === index ? { ...item, expanded: !item.expanded } : item))}
                      >
                        <ChevronDown size={13} className={row.expanded ? 'rotated-180' : undefined} />
                      </button>
                      <button
                        type="button"
                        className="row-action-btn danger"
                        aria-label={t('Delete')}
                        title={t('Delete')}
                        disabled={busy}
                        onClick={() => {
                          setDraftModels((prev) => {
                            const next = prev.filter((_, idx) => idx !== index)
                            if (draftDefaultProfile === row.profileId) {
                              setDraftDefaultProfile(next[0]?.profileId || '')
                            }
                            return next
                          })
                        }}
                      >
                        <X size={13} />
                      </button>
                    </div>
                    {row.expanded && (
                      <div className="model-draft-extra">
                        <div className="settings-form-grid">
                          <label className="form-field">{t('Profile ID')}
                            <input
                              value={row.profileId}
                              disabled={busy}
                              onChange={(e) => {
                                const value = e.target.value
                                if (draftDefaultProfile === row.profileId) setDraftDefaultProfile(value)
                                setDraftModels((prev) => prev.map((item, idx) => idx === index ? { ...item, profileId: value } : item))
                              }}
                            />
                          </label>
                          <label className="form-field">{t('Context window (e.g. 128K, 1M)')}
                            <input
                              value={row.contextWindow}
                              disabled={busy}
                              placeholder={t('Auto')}
                              onChange={(e) => setDraftModels((prev) => prev.map((item, idx) => idx === index ? { ...item, contextWindow: e.target.value } : item))}
                            />
                          </label>
                        </div>
                        <div className="settings-form-grid">
                          <label className="form-field">{t('Max output tokens (e.g. 16K, 128K)')}
                            <input
                              value={row.maxOutputTokens}
                              disabled={busy}
                              placeholder={provProtocol === 'anthropic-messages' ? '8192' : t('Auto')}
                              onChange={(e) => setDraftModels((prev) => prev.map((item, idx) => idx === index ? { ...item, maxOutputTokens: e.target.value } : item))}
                            />
                          </label>
                          <div className="settings-checkboxes model-draft-caps">
                            <label className="remember-token">
                              <input type="checkbox" checked={row.supportsInputImages} disabled={busy} onChange={(e) => setDraftModels((prev) => prev.map((item, idx) => idx === index ? { ...item, supportsInputImages: e.target.checked } : item))} />
                              {t('Images')}
                            </label>
                            <label className="remember-token">
                              <input type="checkbox" checked={row.supportsInputAudio} disabled={busy} onChange={(e) => setDraftModels((prev) => prev.map((item, idx) => idx === index ? { ...item, supportsInputAudio: e.target.checked } : item))} />
                              {t('Audio')}
                            </label>
                          </div>
                        </div>
                      </div>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </div>

        <details className="settings-subdetails">
          <summary>{t('Advanced options')}</summary>
          <div className="settings-subdetails-body">
            {!isEdit && addKind === 'preset' && (
              <div className="settings-form-grid">
                <label className="form-field">{t('Protocol')}
                  <select value={provProtocol} disabled={busy} onChange={(e) => setProvProtocol(e.target.value as 'responses' | 'anthropic-messages')}>
                    <option value="responses">OpenAI Responses</option>
                    <option value="anthropic-messages">Anthropic Messages</option>
                  </select>
                </label>
                <label className="form-field">{t('Base URL')}
                  <input required type="url" disabled={busy} value={provBaseURL} onChange={(e) => setProvBaseURL(e.target.value)} />
                </label>
              </div>
            )}
            <div className="settings-form-grid">
              <label className="form-field">{t('Credential source')}
                <select value={provAuthMode} disabled={busy} onChange={(e) => setProvAuthMode(e.target.value as 'key' | 'env')}>
                  <option value="key">{t('API Key (save in SQLite DB)')}</option>
                  <option value="env">{t('Environment variable (apiKeyEnv)')}</option>
                </select>
              </label>
              <div className="settings-checkboxes model-draft-caps">
                {provProtocol === 'responses' && (
                  <label className="remember-token">
                    <input type="checkbox" checked={provCompaction} disabled={busy} onChange={(e) => setProvCompaction(e.target.checked)} />
                    {t('Enable server compaction')}
                  </label>
                )}
                {provAuthMode === 'key' && isEdit && hasSavedKey && (
                  <label className="remember-token">
                    <input type="checkbox" checked={provClearKey} disabled={busy} onChange={(e) => setProvClearKey(e.target.checked)} />
                    {t('Remove saved API key')}
                  </label>
                )}
              </div>
            </div>
          </div>
        </details>

        <div className="settings-form-footer">
          <button type="button" className="secondary-button" disabled={busy} onClick={closeEditor}>{t('Cancel')}</button>
          <button type="submit" className="primary-button compact" disabled={busy}>
            <Check size={14} aria-hidden="true" />
            {t('Save')}
          </button>
        </div>
      </form>
    )
  }

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

          {(tab === 'models' || tab === 'runtime') && api.supportsModelConfig && (
            <div className="settings-scope-row">
              <div className="connection-tabs compact-tabs">
                <button type="button" className={!scope ? 'active' : ''} disabled={busy} onClick={() => { closeEditor(); setScope('') }}>{t('Defaults')}</button>
                <button type="button" className={scope ? 'active' : ''} disabled={busy || !workspace.trim()} onClick={() => { closeEditor(); setScope(workspace.trim()) }}>{t('This workspace')}</button>
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
                      closeEditor()
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

          {tab === 'models' && api.supportsModelConfig && (
            <div className="settings-pane">
              <div className="models-top-bar">
                <p className="inspector-hint">
                  {view?.secretStorage === 'encrypted'
                    ? t('API keys are encrypted and stored in this frontend SQLite database.')
                    : t('API keys are persisted in this frontend SQLite database (0600); TUI/CLI config.json is untouched.')}
                </p>
                {editorMode !== 'add' && (
                  <button type="button" className="primary-button compact" disabled={busy} onClick={openAddProvider}>
                    <Plus size={14} aria-hidden="true" />{t('Add provider')}
                  </button>
                )}
              </div>

              {editorMode === 'add' && renderUnifiedEditor()}

              <div className="provider-cards">
                {providerNames.length === 0 && editorMode !== 'add' && (
                  <div className="provider-empty-card">
                    <p>{t('No providers configured in this scope yet.')}</p>
                    <button type="button" className="primary-button compact" disabled={busy} onClick={openAddProvider}>
                      <Plus size={14} aria-hidden="true" />{t('Add provider')}
                    </button>
                  </div>
                )}

                {providerNames.map((name) => {
                  const entry = catalog.providers[name]
                  const hasCred = Boolean(view?.savedKeys.includes(name) || entry.apiKeyEnv)
                  const providerProfiles = Object.entries(catalog.profiles).filter(([, p]) => p.provider === name)
                  const isEditingThis = editorMode === 'edit' && editingProviderId === name

                  if (isEditingThis) {
                    return <div key={name}>{renderUnifiedEditor()}</div>
                  }

                  return (
                    <div className="provider-card" key={name}>
                      <div className="provider-card-head">
                        <div className="provider-card-main">
                          <div className="provider-card-title">
                            <span
                              className={`cred-dot ${hasCred ? 'ok' : 'missing'}`}
                              title={t(hasCred ? 'Credential configured' : 'Credential missing')}
                              aria-label={t(hasCred ? 'Credential configured' : 'Credential missing')}
                            />
                            <strong>{name}</strong>
                            <span className="status-pill">{protocolBadge(entry.protocol)}</span>
                            {entry.apiKeyEnv && <span className="status-pill">env:{entry.apiKeyEnv}</span>}
                          </div>
                          <small>{entry.baseURL}</small>
                        </div>
                        <div className="settings-card-actions">
                          {confirmDeleteId === name ? (
                            <>
                              <button type="button" className="plain-button" disabled={busy} onClick={() => setConfirmDeleteId('')}>{t('Cancel')}</button>
                              <button type="button" className="plain-button danger" disabled={busy} onClick={() => void deleteProvider(name)}>{t('Confirm delete')}</button>
                            </>
                          ) : (
                            <>
                              <button type="button" className="secondary-button" disabled={busy} onClick={() => openEditProvider(name)}>{t('Edit')}</button>
                              <button type="button" className="icon-button" disabled={busy} aria-label={t('Delete')} onClick={() => setConfirmDeleteId(name)}><Trash2 size={14} /></button>
                            </>
                          )}
                        </div>
                      </div>

                      <div className="provider-model-chips">
                        {providerProfiles.length === 0 ? (
                          <button type="button" className="plain-button" onClick={() => openEditProvider(name)}>
                            <Plus size={12} aria-hidden="true" />{t('Add models to this provider')}
                          </button>
                        ) : (
                          providerProfiles.map(([profIdKey, prof]) => {
                            const isDefault = catalog.defaultProfile === profIdKey
                            const cap = formatCapacity(prof.contextWindow)
                            return (
                              <div key={profIdKey} className={`provider-model-chip${isDefault ? ' default' : ''}`}>
                                <button
                                  type="button"
                                  className="chip-star-btn"
                                  disabled={busy || isDefault}
                                  title={t(isDefault ? 'Default model' : 'Set as default model')}
                                  aria-label={t(isDefault ? 'Default model' : 'Set as default model')}
                                  onClick={() => void setDefaultProfile(profIdKey)}
                                >
                                  <Star size={12} fill={isDefault ? 'currentColor' : 'none'} aria-hidden="true" />
                                </button>
                                <span className="chip-model-label">{prof.displayName || prof.model}</span>
                                {cap && <small>{cap}</small>}
                                {isDefault && <span className="chip-default-tag">{t('Default')}</span>}
                              </div>
                            )
                          })
                        )}
                      </div>
                    </div>
                  )
                })}
              </div>

              {profileNames.some((key) => !Object.hasOwn(catalog.providers, catalog.profiles[key].provider)) && (
                <div className="settings-card">
                  <div className="settings-card-main">
                    <strong>{t('Unlinked model profiles')}</strong>
                    <small>{profileNames.filter((key) => !Object.hasOwn(catalog.providers, catalog.profiles[key].provider)).join(', ')}</small>
                  </div>
                </div>
              )}

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
