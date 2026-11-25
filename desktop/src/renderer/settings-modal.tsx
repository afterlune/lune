import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import {
  Activity, Check, ChevronDown, Cpu, Database, Eye, EyeOff,
  FolderGit2, Globe, KeyRound, Plug, Plus, RefreshCw, Search, SlidersHorizontal,
  Star, Trash2, Upload, X,
} from 'lucide-react'
import type { ConfigDocument, ConfigImport, ConfigView, MCPServerSummary, ProviderModelInfo, ProviderTestInput, SavedConnection, UiPreferences } from '../shared/desktop-settings'
import type { ThemePreference } from '../shared/theme'
import { useTranslation } from 'react-i18next'
import { useLanguage } from './language'
import type { FrontendSettingsAPI, SaveConfigResult } from './settings-api'
import { ThemePicker } from './theme'
import { defaultUiPreferences } from '../shared/ui-preferences'
import {
  bannerError, bannerStatus, btnIcon, btnPlain, btnPlainDanger, btnPrimaryCompact,
  btnSecondary, btnSubtleIcon, cn, fieldInput, fieldSelect, fieldTextarea, statusPill,
} from './ui'

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

export type SettingsTab = 'models' | 'providers' | 'mcp' | 'runtime' | 'remote' | 'general'

function normalizeTab(tab?: SettingsTab): Exclude<SettingsTab, 'providers'> | undefined {
  if (!tab) return undefined
  return tab === 'providers' ? 'models' : tab
}

function ToggleSwitch({
  checked,
  disabled,
  label,
  ariaLabel,
  onChange,
}: {
  checked: boolean
  disabled?: boolean
  label?: string
  ariaLabel?: string
  onChange: (checked: boolean) => void
}) {
  return (
    <label className="settings-toggle inline-flex cursor-pointer select-none items-center gap-[9px] text-[13px] text-ink">
      <span className="toggle-control relative inline-flex h-5 w-[34px] flex-none items-center">
        <input
          type="checkbox"
          className="absolute m-0 h-0 w-0 opacity-0"
          checked={checked}
          disabled={disabled}
          aria-label={ariaLabel || label}
          onChange={(e) => onChange(e.target.checked)}
        />
        <span className="toggle-track" aria-hidden="true" />
      </span>
      {label ? <span className="toggle-label text-[13px] text-ink-secondary">{label}</span> : null}
    </label>
  )
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
  const { t } = useTranslation()
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

  // MCP editor state
  const [mcpEditorMode, setMcpEditorMode] = useState<'closed' | 'add' | 'edit'>('closed')
  const [editingMcpName, setEditingMcpName] = useState('')
  const [confirmDeleteMcp, setConfirmDeleteMcp] = useState('')
  const [mcpName, setMcpName] = useState('')
  const [mcpTransport, setMcpTransport] = useState<'stdio' | 'streamable-http'>('stdio')
  const [mcpCommand, setMcpCommand] = useState('')
  const [mcpArgs, setMcpArgs] = useState<string[]>([])
  const [mcpEnvRows, setMcpEnvRows] = useState<Array<{ id: string; key: string; value: string; saved: boolean }>>([])
  const [mcpURL, setMcpURL] = useState('')
  const [mcpClientMetadataURL, setMcpClientMetadataURL] = useState('')

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
      setMcpEditorMode('closed')
      setConfirmDeleteMcp('')
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
    setMcpEditorMode('closed')
    setEditingMcpName('')
    setConfirmDeleteMcp('')
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
    { id: 'mcp', label: 'MCP', icon: Plug, hidden: !api.supportsModelConfig },
    { id: 'runtime', label: 'Runtime & Agents', icon: Activity, hidden: !api.supportsModelConfig },
    { id: 'remote', label: 'Remote & Token', icon: Globe },
    { id: 'general', label: 'General', icon: SlidersHorizontal },
  ]

  const mcpServers = view?.mcpServers ?? {}
  const mcpServerNames = Object.keys(mcpServers).sort()

  const toMCPPayloadEntry = (summary: MCPServerSummary): Record<string, unknown> => {
    if (summary.transport === 'streamable-http') {
      return {
        transport: 'streamable-http',
        url: summary.url || '',
        ...(summary.clientMetadataURL ? { clientMetadataURL: summary.clientMetadataURL } : {}),
      }
    }
    return {
      transport: 'stdio',
      command: summary.command || '',
      ...(summary.args?.length ? { args: summary.args } : {}),
      ...(summary.envKeys?.length ? { keepEnvKeys: summary.envKeys } : {}),
    }
  }

  const openAddMCP = () => {
    setError('')
    setStatus('')
    setConfirmDeleteMcp('')
    setMcpEditorMode('add')
    setEditingMcpName('')
    setMcpName('')
    setMcpTransport('stdio')
    setMcpCommand('')
    setMcpArgs([])
    setMcpEnvRows([])
    setMcpURL('')
    setMcpClientMetadataURL('')
  }

  const openEditMCP = (name: string) => {
    const entry = mcpServers[name]
    if (!entry) return
    setError('')
    setStatus('')
    setConfirmDeleteMcp('')
    setMcpEditorMode('edit')
    setEditingMcpName(name)
    setMcpName(name)
    setMcpTransport(entry.transport === 'streamable-http' ? 'streamable-http' : 'stdio')
    setMcpCommand(entry.command || '')
    setMcpArgs([...(entry.args ?? [])])
    setMcpEnvRows((entry.envKeys ?? []).map((key) => ({ id: `${name}:${key}`, key, value: '', saved: true })))
    setMcpURL(entry.url || '')
    setMcpClientMetadataURL(entry.clientMetadataURL || '')
  }

  const submitMCPServer = async (event: FormEvent) => {
    event.preventDefault()
    setError('')
    const name = (mcpEditorMode === 'edit' ? editingMcpName : mcpName).trim()
    if (!name) { setError(t('Server name is required.')); return }
    if (mcpEditorMode === 'add' && Object.hasOwn(mcpServers, name)) {
      setError(`${t('Duplicate server name')}: ${name}`)
      return
    }
    let nextEntry: Record<string, unknown>
    if (mcpTransport === 'stdio') {
      if (!mcpCommand.trim()) { setError(t('Command is required for stdio transport.')); return }
      const args = mcpArgs.map((a) => a.trim()).filter(Boolean)
      const keepEnvKeys: string[] = []
      const env: Record<string, string> = {}
      const seenKeys = new Set<string>()
      for (const row of mcpEnvRows) {
        const k = row.key.trim()
        if (!k) continue
        const lowered = k.toLowerCase()
        if (seenKeys.has(lowered)) { setError(`${t('Duplicate environment variable')}: ${k}`); return }
        seenKeys.add(lowered)
        if (row.value !== '') env[k] = row.value
        else if (row.saved) keepEnvKeys.push(k)
        else env[k] = ''
      }
      nextEntry = {
        transport: 'stdio',
        command: mcpCommand.trim(),
        ...(args.length ? { args } : {}),
        ...(keepEnvKeys.length ? { keepEnvKeys } : {}),
        ...(Object.keys(env).length ? { env } : {}),
      }
    } else {
      if (!mcpURL.trim()) { setError(t('Endpoint URL is required.')); return }
      nextEntry = {
        transport: 'streamable-http',
        url: mcpURL.trim(),
        ...(mcpClientMetadataURL.trim() ? { clientMetadataURL: mcpClientMetadataURL.trim() } : {}),
      }
    }

    const nextServers: Record<string, unknown> = {}
    for (const [key, summary] of Object.entries(mcpServers)) {
      nextServers[key] = toMCPPayloadEntry(summary)
    }
    nextServers[name] = nextEntry
    const ok = await commitDocument({ ...doc, mcpServers: nextServers }, imported?.id, t('MCP servers saved to SQLite database.'))
    if (ok) {
      setMcpEditorMode('closed')
      setEditingMcpName('')
    }
  }

  const deleteMCPServer = async (name: string) => {
    const nextServers: Record<string, unknown> = {}
    for (const [key, summary] of Object.entries(mcpServers)) {
      if (key !== name) nextServers[key] = toMCPPayloadEntry(summary)
    }
    const ok = await commitDocument({ ...doc, mcpServers: nextServers }, imported?.id, t('MCP servers saved to SQLite database.'))
    if (ok) {
      setConfirmDeleteMcp('')
      if (mcpEditorMode === 'edit' && editingMcpName === name) {
        setMcpEditorMode('closed')
        setEditingMcpName('')
      }
    }
  }

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

    const formFieldCls = 'form-field flex flex-col gap-1.5 text-[12.5px] font-medium text-ink-secondary'
    const formGridCls = 'settings-form-grid grid grid-cols-1 gap-3 sm:grid-cols-2'
    const compactTabsCls = 'connection-tabs compact-tabs flex gap-1 rounded-[9px] border border-line-subtle bg-muted p-1'
    const compactTabBtnCls = (active: boolean) =>
      cn(
        'min-h-[28px] cursor-pointer rounded-[6px] border-0 px-3 text-[12.5px] font-medium transition-[background,color,box-shadow] duration-120 disabled:cursor-not-allowed disabled:opacity-45',
        active ? 'active bg-canvas text-ink shadow-elev-sm' : 'bg-transparent text-ink-secondary hover:text-ink',
      )

    return (
      <form
        className="provider-editor-card flex flex-col gap-4 rounded-[12px] border border-line bg-canvas p-4 shadow-elev-sm"
        onSubmit={submitUnifiedProvider}
      >
        <div className="settings-form-head flex flex-wrap items-center justify-between gap-2.5">
          <div className="provider-editor-title flex items-center gap-2 text-ink">
            <KeyRound size={15} aria-hidden="true" />
            <h3 className="m-0 text-[14px] font-semibold">{isEdit ? `${t('Edit provider')}: ${editingProviderId}` : t('Add provider')}</h3>
          </div>
          {!isEdit && (
            <div className={compactTabsCls}>
              <button type="button" className={compactTabBtnCls(addKind === 'preset')} disabled={busy} onClick={() => {
                setAddKind('preset')
                const p = PROVIDER_PRESETS.find((item) => item.id === (selectedPreset || 'openai')) ?? PROVIDER_PRESETS[0]
                applyPreset(p, providerNames)
              }}>{t('Presets')}</button>
              <button type="button" className={compactTabBtnCls(addKind === 'custom')} disabled={busy} onClick={openCustomAdd}>{t('Custom endpoint')}</button>
            </div>
          )}
          <button type="button" className={cn('icon-button', btnIcon)} aria-label={t('Close')} disabled={busy} onClick={closeEditor}><X size={15} /></button>
        </div>

        {!isEdit && addKind === 'preset' && (
          <div className="preset-grid grid grid-cols-2 gap-2 sm:grid-cols-3" role="radiogroup" aria-label={t('Preset providers')}>
            {PROVIDER_PRESETS.map((preset) => {
              const active = selectedPreset === preset.id
              return (
                <button
                  key={preset.id}
                  type="button"
                  className={cn(
                    'preset-chip flex cursor-pointer flex-col gap-0.5 rounded-[9px] border px-3 py-2 text-left transition-[background,border-color] duration-120 disabled:opacity-50',
                    active ? 'active border-accent bg-subtle text-ink' : 'border-line-subtle bg-canvas text-ink-secondary hover:border-line hover:bg-hover',
                  )}
                  disabled={busy}
                  onClick={() => applyPreset(preset, providerNames)}
                >
                  <strong className="text-[12.5px] font-semibold text-ink">{preset.name}</strong>
                  <span className="truncate text-[11px] text-ink-muted">{preset.hint}</span>
                </button>
              )
            })}
          </div>
        )}

        <div className={formGridCls}>
          <label className={formFieldCls}>{t('Provider ID')}
            <input
              required
              className={fieldInput}
              disabled={isEdit || busy}
              value={provId}
              onChange={(e) => setProvId(e.target.value)}
              placeholder="openai / anthropic / deepseek"
            />
          </label>
          {provAuthMode === 'key' ? (
            <div className={formFieldCls}>
              <div className="field-label-row flex items-center justify-between gap-2">
                <span>{t('API key')}</span>
                {isEdit && hasSavedKey && !provClearKey && (
                  <span className="cred-inline-ok inline-flex items-center gap-1.5 text-[11.5px] text-ok">
                    <span className="cred-dot ok h-2 w-2 rounded-full bg-ok" aria-hidden="true" />
                    {t('Key saved in DB')}
                  </span>
                )}
              </div>
              <div className="secret-input-wrap relative flex items-center">
                <input
                  type={showAPIKey ? 'text' : 'password'}
                  className={cn(fieldInput, 'pr-9')}
                  autoComplete="new-password"
                  disabled={busy || provClearKey}
                  value={provAPIKey}
                  onChange={(e) => setProvAPIKey(e.target.value)}
                  placeholder={isEdit && hasSavedKey ? t('Leave blank to keep existing DB key') : (activePreset?.keyPlaceholder || 'sk-...')}
                />
                <button
                  type="button"
                  className={cn(btnSubtleIcon, 'absolute right-1.5')}
                  aria-label={t(showAPIKey ? 'Hide key' : 'Show key')}
                  onClick={() => setShowAPIKey((v) => !v)}
                >
                  {showAPIKey ? <EyeOff size={14} /> : <Eye size={14} />}
                </button>
              </div>
            </div>
          ) : (
            <label className={formFieldCls}>{t('Environment variable name')}
              <input required className={fieldInput} disabled={busy} value={provAPIKeyEnv} onChange={(e) => setProvAPIKeyEnv(e.target.value)} placeholder="OPENAI_API_KEY" />
            </label>
          )}
        </div>

        {(isEdit || addKind === 'custom') && (
          <div className={formGridCls}>
            <label className={formFieldCls}>{t('Protocol')}
              <select className={fieldSelect} value={provProtocol} disabled={busy} onChange={(e) => setProvProtocol(e.target.value as 'responses' | 'anthropic-messages')}>
                <option value="responses">OpenAI Responses</option>
                <option value="anthropic-messages">Anthropic Messages</option>
              </select>
            </label>
            <label className={formFieldCls}>{t('Base URL')}
              <input required type="url" className={fieldInput} disabled={busy} value={provBaseURL} onChange={(e) => setProvBaseURL(e.target.value)} placeholder={provProtocol === 'responses' ? 'https://api.openai.com/v1' : 'https://api.anthropic.com'} />
            </label>
          </div>
        )}

        <div className="provider-models-block flex flex-col gap-2.5 border-t border-line-subtle pt-3">
          <div className="provider-models-head flex flex-wrap items-center justify-between gap-2">
            <div className="provider-models-label flex items-center gap-2 text-[13px] text-ink">
              <strong>{t('Enabled models')}</strong>
              <span className={statusPill}>{draftModels.length}</span>
            </div>
            <div className="provider-models-actions flex flex-wrap items-center gap-1.5">
              <button
                type="button"
                className={btnSecondary}
                disabled={busy || testingProvider}
                onClick={() => void testAndFetchModels()}
              >
                <RefreshCw size={12} className={testingProvider ? 'spin' : undefined} aria-hidden="true" />
                {t(testingProvider ? 'Fetching models…' : 'Test & fetch models')}
              </button>
              <button type="button" className={btnSecondary} disabled={busy} onClick={addManualModelRow}>
                <Plus size={12} aria-hidden="true" />{t('Add model')}
              </button>
            </div>
          </div>

          {providerTestMessage && (
            <p className={providerTestOK ? bannerStatus : bannerError} role={providerTestOK ? 'status' : 'alert'}>
              {providerTestMessage}
            </p>
          )}

          {pickerOpen && (
            <div className="model-picker-drawer flex flex-col gap-2.5 rounded-[10px] border border-line bg-subtle p-3">
              <div className="model-fetch-head flex flex-wrap items-center justify-between gap-2">
                <span className="text-[12.5px] font-medium text-ink">{t('Select models to enable')}</span>
                <div className="model-fetch-actions flex flex-wrap items-center gap-1.5">
                  <div className="model-fetch-search flex h-[29px] items-center gap-1.5 rounded-[7px] border border-line bg-canvas px-2 text-ink-muted">
                    <Search size={12} aria-hidden="true" />
                    <input
                      className="w-[150px] border-0 bg-transparent text-[12px] text-ink outline-none placeholder:text-ink-faint"
                      value={modelFilter}
                      onChange={(e) => setModelFilter(e.target.value)}
                      placeholder={t('Search fetched models…')}
                      aria-label={t('Search fetched models…')}
                    />
                  </div>
                  <button
                    type="button"
                    className={btnPlain}
                    onClick={() => setCheckedModelIds((prev) => {
                      const next = new Set(prev)
                      for (const m of filteredDiscovered) next.add(m.id)
                      return next
                    })}
                  >
                    {t('Select all')}
                  </button>
                  <button type="button" className={btnPlain} onClick={() => setCheckedModelIds(new Set())}>{t('Clear')}</button>
                </div>
              </div>
              {filteredDiscovered.length === 0 ? (
                <small className="text-[12px] text-ink-muted">{t('No models returned by provider.')}</small>
              ) : (
                <div className="model-checklist flex max-h-[184px] flex-col gap-[3px] overflow-y-auto">
                  {filteredDiscovered.map((m) => {
                    const checked = checkedModelIds.has(m.id)
                    const cap = formatCapacity(m.contextWindow)
                    return (
                      <label
                        key={m.id}
                        className={cn(
                          'model-check-row flex cursor-pointer items-center gap-2 rounded-[7px] px-2 py-[5px] text-[12px] hover:bg-hover',
                          checked && 'checked bg-muted',
                        )}
                      >
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
                        <span className="model-check-id truncate font-mono text-[12px] text-ink">{m.id}</span>
                        {m.displayName && m.displayName !== m.id && (
                          <span className="model-check-name truncate text-[11.5px] text-ink-secondary">{m.displayName}</span>
                        )}
                        <span className="model-check-meta ml-auto whitespace-nowrap font-mono text-[11px] text-ink-muted">
                          {cap ? `${cap}` : ''}
                          {m.supportsInputImages ? (cap ? ` · ${t('Images')}` : t('Images')) : ''}
                        </span>
                      </label>
                    )
                  })}
                </div>
              )}
              <div className="model-picker-footer flex items-center justify-end gap-2 border-t border-line-subtle pt-2">
                <button type="button" className={btnPlain} onClick={() => setPickerOpen(false)}>{t('Cancel')}</button>
                <button type="button" className={btnPrimaryCompact} onClick={applyCheckedModels}>
                  <Check size={13} aria-hidden="true" />
                  {t('Apply selection')} ({checkedModelIds.size})
                </button>
              </div>
            </div>
          )}

          {draftModels.length === 0 ? (
            <p className="m-0 text-[12.5px] leading-[1.55] text-ink-muted">{t('No models enabled yet. Click "Test & fetch models" to multi-select, or "Add model" manually.')}</p>
          ) : (
            <div className="model-draft-list flex flex-col gap-2">
              {draftModels.map((row, index) => {
                const isDefault = draftDefaultProfile === row.profileId
                return (
                  <div key={`${row.originalProfileId || 'new'}-${index}`} className="model-draft-item flex flex-col gap-2 rounded-[9px] border border-line-subtle bg-subtle p-2">
                    <div className="model-draft-main flex flex-wrap items-center gap-1.5 sm:flex-nowrap">
                      <button
                        type="button"
                        className={cn(
                          'inline-grid h-7 w-7 shrink-0 cursor-pointer place-items-center rounded-[6px] border-0 bg-transparent transition-[background,color] duration-120 hover:bg-hover',
                          isDefault ? 'text-ink' : 'text-ink-muted hover:text-ink',
                        )}
                        title={t('Set as default model')}
                        aria-label={t('Set as default model')}
                        aria-pressed={isDefault}
                        onClick={() => setDraftDefaultProfile(row.profileId)}
                      >
                        <Star size={13} fill={isDefault ? 'currentColor' : 'none'} aria-hidden="true" />
                      </button>
                      <input
                        className={cn(fieldInput, 'h-[32px] min-w-[140px] flex-1 font-mono text-[12.5px]')}
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
                        className={cn(fieldInput, 'h-[32px] min-w-[120px] flex-1 text-[12.5px]')}
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
                        className={btnSubtleIcon}
                        aria-expanded={Boolean(row.expanded)}
                        title={t('Model capacities & options')}
                        onClick={() => setDraftModels((prev) => prev.map((item, idx) => idx === index ? { ...item, expanded: !item.expanded } : item))}
                      >
                        <ChevronDown size={13} className={cn('transition-transform duration-150', row.expanded && 'rotate-180')} />
                      </button>
                      <button
                        type="button"
                        className={cn(btnSubtleIcon, 'hover:not-disabled:bg-danger-bg hover:not-disabled:text-danger')}
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
                      <div className="model-draft-extra flex flex-col gap-2 border-t border-line-subtle pt-2">
                        <div className={formGridCls}>
                          <label className={formFieldCls}>{t('Profile ID')}
                            <input
                              className={cn(fieldInput, 'h-[32px] text-[12.5px]')}
                              value={row.profileId}
                              disabled={busy}
                              onChange={(e) => {
                                const value = e.target.value
                                if (draftDefaultProfile === row.profileId) setDraftDefaultProfile(value)
                                setDraftModels((prev) => prev.map((item, idx) => idx === index ? { ...item, profileId: value } : item))
                              }}
                            />
                          </label>
                          <label className={formFieldCls}>{t('Context window (e.g. 128K, 1M)')}
                            <input
                              className={cn(fieldInput, 'h-[32px] text-[12.5px]')}
                              value={row.contextWindow}
                              disabled={busy}
                              placeholder={t('Auto')}
                              onChange={(e) => setDraftModels((prev) => prev.map((item, idx) => idx === index ? { ...item, contextWindow: e.target.value } : item))}
                            />
                          </label>
                        </div>
                        <div className={formGridCls}>
                          <label className={formFieldCls}>{t('Max output tokens (e.g. 16K, 128K)')}
                            <input
                              className={cn(fieldInput, 'h-[32px] text-[12.5px]')}
                              value={row.maxOutputTokens}
                              disabled={busy}
                              placeholder={provProtocol === 'anthropic-messages' ? '8192' : t('Auto')}
                              onChange={(e) => setDraftModels((prev) => prev.map((item, idx) => idx === index ? { ...item, maxOutputTokens: e.target.value } : item))}
                            />
                          </label>
                          <div className="settings-checkboxes model-draft-caps flex flex-wrap items-center gap-4 pt-3">
                            <ToggleSwitch
                              label={t('Images')}
                              checked={row.supportsInputImages}
                              disabled={busy}
                              onChange={(checked) => setDraftModels((prev) => prev.map((item, idx) => idx === index ? { ...item, supportsInputImages: checked } : item))}
                            />
                            <ToggleSwitch
                              label={t('Audio')}
                              checked={row.supportsInputAudio}
                              disabled={busy}
                              onChange={(checked) => setDraftModels((prev) => prev.map((item, idx) => idx === index ? { ...item, supportsInputAudio: checked } : item))}
                            />
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

        <details className="settings-subdetails rounded-[9px] border border-line-subtle bg-subtle px-3 py-2">
          <summary className="cursor-pointer select-none text-[12.5px] font-medium text-ink-secondary hover:text-ink">{t('Advanced options')}</summary>
          <div className="settings-subdetails-body mt-2.5 flex flex-col gap-2.5">
            {!isEdit && addKind === 'preset' && (
              <div className={formGridCls}>
                <label className={formFieldCls}>{t('Protocol')}
                  <select className={fieldSelect} value={provProtocol} disabled={busy} onChange={(e) => setProvProtocol(e.target.value as 'responses' | 'anthropic-messages')}>
                    <option value="responses">OpenAI Responses</option>
                    <option value="anthropic-messages">Anthropic Messages</option>
                  </select>
                </label>
                <label className={formFieldCls}>{t('Base URL')}
                  <input required type="url" className={fieldInput} disabled={busy} value={provBaseURL} onChange={(e) => setProvBaseURL(e.target.value)} />
                </label>
              </div>
            )}
            <div className={formGridCls}>
              <label className={formFieldCls}>{t('Credential source')}
                <select className={fieldSelect} value={provAuthMode} disabled={busy} onChange={(e) => setProvAuthMode(e.target.value as 'key' | 'env')}>
                  <option value="key">{t('API Key (save in SQLite DB)')}</option>
                  <option value="env">{t('Environment variable (apiKeyEnv)')}</option>
                </select>
              </label>
              <div className="settings-checkboxes model-draft-caps flex flex-wrap items-center gap-4 pt-3">
                {provProtocol === 'responses' && (
                  <ToggleSwitch
                    label={t('Enable server compaction')}
                    checked={provCompaction}
                    disabled={busy}
                    onChange={(checked) => setProvCompaction(checked)}
                  />
                )}
                {provAuthMode === 'key' && isEdit && hasSavedKey && (
                  <ToggleSwitch
                    label={t('Remove saved API key')}
                    checked={provClearKey}
                    disabled={busy}
                    onChange={(checked) => setProvClearKey(checked)}
                  />
                )}
              </div>
            </div>
          </div>
        </details>

        <div className="settings-form-footer flex items-center justify-end gap-2 border-t border-line-subtle pt-3">
          <button type="button" className={btnSecondary} disabled={busy} onClick={closeEditor}>{t('Cancel')}</button>
          <button type="submit" className={btnPrimaryCompact} disabled={busy}>
            <Check size={14} aria-hidden="true" />
            {t('Save')}
          </button>
        </div>
      </form>
    )
  }

  const formFieldCls = 'form-field flex flex-col gap-1.5 text-[12.5px] font-medium text-ink-secondary'
  const formGridCls = 'settings-form-grid grid grid-cols-1 gap-3 sm:grid-cols-2'
  const compactTabsCls = 'connection-tabs compact-tabs flex gap-1 rounded-[9px] border border-line-subtle bg-muted p-1'
  const compactTabBtnCls = (active: boolean) =>
    cn(
      'min-h-[28px] cursor-pointer rounded-[6px] border-0 px-3 text-[12.5px] font-medium transition-[background,color,box-shadow] duration-120 disabled:cursor-not-allowed disabled:opacity-45',
      active ? 'active bg-canvas text-ink shadow-elev-sm' : 'bg-transparent text-ink-secondary hover:text-ink',
    )
  const settingsCardCls = 'settings-card flex items-center justify-between gap-3 rounded-[11px] border border-line-subtle bg-subtle px-[15px] py-[13px] transition-[border-color] duration-150 hover:border-line'
  const settingsRowCls = 'settings-row flex min-h-[50px] flex-wrap sm:flex-nowrap items-center justify-between gap-2.5 sm:gap-4 px-3.5 py-2.5 text-[13px]'
  const settingsRowControlCls = 'settings-row-control flex w-full sm:w-auto flex-none items-center justify-start sm:justify-end'
  const settingsGroupCls = 'settings-group divide-y divide-line-subtle overflow-hidden rounded-[11px] border border-line-subtle bg-subtle'
  const settingsSectionTitleCls = 'settings-section-title m-0 pl-0.5 text-[11.5px] font-semibold tracking-[0.02em] text-ink-muted'

  return (
    <dialog
      ref={dialogRef}
      className="settings-modal m-auto h-[min(700px,calc(100dvh-36px))] max-h-[calc(100dvh-24px)] w-[min(880px,calc(100vw-32px))] max-w-[calc(100vw-24px)] overflow-hidden rounded-[14px] border border-line bg-canvas p-0 text-ink shadow-elev-lg animate-[arrive_.18s_cubic-bezier(.22,1,.36,1)_both]"
      aria-labelledby="settings-modal-title"
      onClose={onClose}
      onCancel={(event) => { if (busy) event.preventDefault() }}
    >
      <header className="settings-modal-head flex h-[52px] flex-none items-center justify-between gap-3 border-b border-line-subtle bg-canvas pr-4 pl-[22px]">
        <h2 id="settings-modal-title" className="m-0 text-[15px] font-semibold tracking-[-0.015em] text-ink">{t('Settings')}</h2>
        <button type="button" className={cn('icon-button', btnIcon)} aria-label={t('Close')} disabled={busy} onClick={onClose}><X size={17} /></button>
      </header>

      <div className="settings-shell grid min-h-0 flex-1 grid-cols-1 grid-rows-[auto_minmax(0,1fr)] sm:grid-cols-[218px_minmax(0,1fr)] sm:grid-rows-none">
        <aside className="settings-sidebar flex min-h-0 flex-col justify-between border-b border-line-subtle bg-sidebar px-2.5 py-2 sm:border-r sm:border-b-0 sm:pt-3 sm:pb-2.5">
          <nav className="settings-nav flex flex-row gap-1 overflow-x-auto sm:flex-col sm:gap-0.5" aria-label={t('Settings sections')}>
            {navItems.filter((item) => !item.hidden).map((item) => {
              const Icon = item.icon
              const active = tab === item.id
              return (
                <button
                  key={item.id}
                  type="button"
                  className={cn(
                    'flex h-[30px] sm:h-[34px] flex-none sm:w-full cursor-pointer items-center gap-[9px] whitespace-nowrap rounded-[8px] border-0 px-2.5 text-left text-[12.5px] sm:text-[13px] transition-[background,color,box-shadow] duration-120',
                    active
                      ? 'active bg-canvas font-semibold text-ink shadow-[0_1px_2px_light-dark(rgb(0_0_0/.05),rgb(0_0_0/.28)),inset_0_0_0_1px_var(--border-subtle)]'
                      : 'bg-transparent font-medium text-ink-secondary hover:bg-hover hover:text-ink',
                  )}
                  onClick={() => { setError(''); setStatus(''); setTab(item.id) }}
                >
                  <Icon size={15} aria-hidden="true" />
                  <span>{t(item.label)}</span>
                </button>
              )
            })}
          </nav>
          <div
            className="settings-storage-foot hidden sm:flex items-start gap-2 border-t border-line-subtle px-2 pt-2.5 pb-0.5 text-[11px] leading-[1.35] text-ink-muted"
            title={api.kind === 'web-host' ? t('Web SQLite DB (settings.db)') : api.kind === 'desktop' ? t('Electron SQLite DB (settings.sqlite)') : t('Browser storage')}
          >
            <Database size={12} className="mt-0.5 shrink-0 opacity-75" aria-hidden="true" />
            <div className="flex min-w-0 flex-wrap items-center gap-x-1.5 gap-y-0.5">
              {api.kind === 'web-host' ? (
                <>
                  <span>{t('Web SQLite DB')}</span>
                  <code className="rounded bg-muted px-1 py-[1px] font-mono text-[10px] text-ink-secondary">settings.db</code>
                </>
              ) : api.kind === 'desktop' ? (
                <>
                  <span>{t('Electron SQLite DB')}</span>
                  <code className="rounded bg-muted px-1 py-[1px] font-mono text-[10px] text-ink-secondary">settings.sqlite</code>
                </>
              ) : (
                <span>{t('Browser storage')}</span>
              )}
            </div>
          </div>
        </aside>

        <div className="settings-body flex min-h-0 flex-col gap-4 overflow-y-auto bg-canvas px-3.5 pt-3.5 pb-5 sm:px-6 sm:pt-5 sm:pb-7">
          {error && <p className={cn('form-error', bannerError)} role="alert">{error}</p>}
          {status && <p className={cn('form-status', bannerStatus)} role="status">{status}</p>}

          {(tab === 'models' || tab === 'mcp' || tab === 'runtime') && api.supportsModelConfig && (
            <div className="settings-scope-row flex flex-wrap items-center justify-between gap-2.5 border-b border-line-subtle pb-3">
              <div className={compactTabsCls}>
                <button type="button" className={compactTabBtnCls(!scope)} disabled={busy} onClick={() => { closeEditor(); setScope('') }}>{t('Defaults')}</button>
                <button type="button" className={compactTabBtnCls(Boolean(scope))} disabled={busy || !workspace.trim()} onClick={() => { closeEditor(); setScope(workspace.trim()) }}>{t('This workspace')}</button>
              </div>
              <div className="settings-scope-actions flex items-center gap-2">
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
                <button type="button" className={btnSecondary} disabled={busy} onClick={() => void handleImportConfig()}>
                  <Upload size={13} aria-hidden="true" />{t('Import config.json…')}
                </button>
                {scope && (
                  <button
                    type="button"
                    className={btnPlain}
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
            <div className="settings-pane flex flex-col gap-4">
              <div className="settings-tab-head flex flex-wrap items-center justify-between gap-4 sm:flex-nowrap">
                <div className="min-w-0 flex-1">
                  <h3 className="m-0 text-[16px] font-semibold tracking-[-0.02em] text-ink">{t('Models')}</h3>
                  <p className="settings-tab-desc mt-[3px] mb-0 text-[12.5px] leading-[1.5] text-ink-muted">
                    {view?.secretStorage === 'encrypted'
                      ? t('API keys are encrypted and stored in this frontend SQLite database.')
                      : t('API keys are persisted in this frontend SQLite database (0600); TUI/CLI config.json is untouched.')}
                  </p>
                </div>
                {editorMode !== 'add' && providerNames.length > 0 && (
                  <button type="button" className={cn(btnPrimaryCompact, 'flex-none')} disabled={busy} onClick={openAddProvider}>
                    <Plus size={14} aria-hidden="true" />{t('Add provider')}
                  </button>
                )}
              </div>

              {editorMode === 'add' && renderUnifiedEditor()}

              <div className="provider-cards flex flex-col gap-2.5">
                {providerNames.length === 0 && editorMode !== 'add' && (
                  <div className="provider-empty-card flex flex-col items-center gap-3 rounded-[12px] border border-dashed border-line bg-subtle px-6 py-8 text-center">
                    <div className="empty-card-icon grid h-10 w-10 place-items-center rounded-[10px] border border-line-subtle bg-canvas text-ink-secondary"><Cpu size={20} aria-hidden="true" /></div>
                    <p className="m-0 text-[13px] text-ink-muted">{t('No providers configured in this scope yet.')}</p>
                    <button type="button" className={btnPrimaryCompact} disabled={busy} onClick={openAddProvider}>
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
                    <div className="provider-card flex flex-col gap-3 rounded-[12px] border border-line-subtle bg-subtle p-3.5 transition-[border-color] duration-150 hover:border-line" key={name}>
                      <div className="provider-card-head flex items-start justify-between gap-3">
                        <div className="provider-card-main min-w-0 flex-1">
                          <div className="provider-card-title flex flex-wrap items-center gap-2">
                            <span
                              className={cn('cred-dot h-2 w-2 shrink-0 rounded-full', hasCred ? 'ok bg-ok' : 'missing bg-warn')}
                              title={t(hasCred ? 'Credential configured' : 'Credential missing')}
                              aria-label={t(hasCred ? 'Credential configured' : 'Credential missing')}
                            />
                            <strong className="text-[13.5px] font-semibold text-ink">{name}</strong>
                            <span className={statusPill}>{protocolBadge(entry.protocol)}</span>
                            {entry.apiKeyEnv && <span className={statusPill}>env:{entry.apiKeyEnv}</span>}
                          </div>
                          <small className="mt-1 block truncate font-mono text-[11.5px] text-ink-muted">{entry.baseURL}</small>
                        </div>
                        <div className="settings-card-actions flex shrink-0 items-center gap-1.5">
                          {confirmDeleteId === name ? (
                            <>
                              <button type="button" className={btnPlain} disabled={busy} onClick={() => setConfirmDeleteId('')}>{t('Cancel')}</button>
                              <button type="button" className={btnPlainDanger} disabled={busy} onClick={() => void deleteProvider(name)}>{t('Confirm delete')}</button>
                            </>
                          ) : (
                            <>
                              <button type="button" className={btnSecondary} disabled={busy} onClick={() => openEditProvider(name)}>{t('Edit')}</button>
                              <button type="button" className={cn('icon-button', btnIcon)} disabled={busy} aria-label={t('Delete')} onClick={() => setConfirmDeleteId(name)}><Trash2 size={14} /></button>
                            </>
                          )}
                        </div>
                      </div>

                      <div className="provider-model-chips flex flex-wrap gap-1.5">
                        {providerProfiles.length === 0 ? (
                          <button type="button" className={btnPlain} onClick={() => openEditProvider(name)}>
                            <Plus size={12} aria-hidden="true" />{t('Add models to this provider')}
                          </button>
                        ) : (
                          providerProfiles.map(([profIdKey, prof]) => {
                            const isDefault = catalog.defaultProfile === profIdKey
                            const cap = formatCapacity(prof.contextWindow)
                            return (
                              <div
                                key={profIdKey}
                                className={cn(
                                  'provider-model-chip inline-flex items-center gap-1.5 rounded-[8px] border px-2 py-1 text-[12px]',
                                  isDefault ? 'default border-line-strong bg-canvas font-medium text-ink' : 'border-line-subtle bg-canvas text-ink-secondary',
                                )}
                              >
                                <button
                                  type="button"
                                  className={cn(
                                    'chip-star-btn inline-grid h-4 w-4 cursor-pointer place-items-center border-0 bg-transparent p-0 disabled:cursor-default',
                                    isDefault ? 'text-ink' : 'text-ink-muted hover:text-ink',
                                  )}
                                  disabled={busy || isDefault}
                                  title={t(isDefault ? 'Default model' : 'Set as default model')}
                                  aria-label={t(isDefault ? 'Default model' : 'Set as default model')}
                                  onClick={() => void setDefaultProfile(profIdKey)}
                                >
                                  <Star size={12} fill={isDefault ? 'currentColor' : 'none'} aria-hidden="true" />
                                </button>
                                <span className="chip-model-label text-ink">{prof.displayName || prof.model}</span>
                                {cap && <small className="font-mono text-[10.5px] text-ink-muted">{cap}</small>}
                                {isDefault && <span className="chip-default-tag rounded bg-muted px-1.5 py-[1px] text-[10px] font-semibold text-ink-secondary">{t('Default')}</span>}
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
                <div className={settingsCardCls}>
                  <div className="settings-card-main min-w-0 flex flex-col gap-[3px]">
                    <strong className="text-[13px] font-medium text-ink">{t('Unlinked model profiles')}</strong>
                    <small className="truncate font-mono text-[11.5px] text-ink-muted">{profileNames.filter((key) => !Object.hasOwn(catalog.providers, catalog.profiles[key].provider)).join(', ')}</small>
                  </div>
                </div>
              )}

              <details className="settings-advanced rounded-[10px] border border-line-subtle bg-subtle px-3.5 py-2.5">
                <summary className="cursor-pointer select-none text-[12.5px] font-medium text-ink-secondary hover:text-ink">{t('Advanced JSON editor')}</summary>
                <div className="settings-advanced-body mt-2.5 flex flex-col gap-2.5">
                  <textarea
                    rows={10}
                    className={cn(fieldTextarea, 'font-mono text-[12px]')}
                    value={rawJSON}
                    disabled={busy}
                    spellCheck={false}
                    onChange={(e) => setRawJSON(e.target.value)}
                  />
                  <div className="flex justify-end">
                    <button
                      type="button"
                      className={btnSecondary}
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
                </div>
              </details>
            </div>
          )}

          {tab === 'mcp' && api.supportsModelConfig && (
            <div className="settings-pane flex flex-col gap-4">
              <div className="settings-tab-head flex flex-wrap items-center justify-between gap-4 sm:flex-nowrap">
                <div className="min-w-0 flex-1">
                  <h3 className="m-0 text-[16px] font-semibold tracking-[-0.02em] text-ink">{t('MCP')}</h3>
                  <p className="settings-tab-desc mt-[3px] mb-0 text-[12.5px] leading-[1.5] text-ink-muted">
                    {t('Configure local stdio or remote streamable-http MCP servers. Environment variable values stay encrypted in SQLite.')}
                  </p>
                </div>
                {mcpEditorMode === 'closed' && mcpServerNames.length > 0 && (
                  <button type="button" className={cn(btnPrimaryCompact, 'flex-none')} disabled={busy} onClick={openAddMCP}>
                    <Plus size={14} aria-hidden="true" />{t('Add server')}
                  </button>
                )}
              </div>

              {mcpEditorMode !== 'closed' && (
                <form className="provider-editor-card flex flex-col gap-4 rounded-[12px] border border-line bg-canvas p-4 shadow-elev-sm" onSubmit={submitMCPServer}>
                  <div className="settings-form-head flex items-center justify-between gap-2.5">
                    <div className="provider-editor-title flex items-center gap-2 text-ink">
                      <Plug size={15} aria-hidden="true" />
                      <h3 className="m-0 text-[14px] font-semibold">{mcpEditorMode === 'edit' ? `${t('Edit MCP server')}: ${editingMcpName}` : t('Add MCP server')}</h3>
                    </div>
                    <button type="button" className={cn('icon-button', btnIcon)} aria-label={t('Close')} disabled={busy} onClick={() => setMcpEditorMode('closed')}><X size={15} /></button>
                  </div>

                  <div className={formGridCls}>
                    <label className={formFieldCls}>{t('Server name')}
                      <input
                        required
                        className={fieldInput}
                        disabled={busy || mcpEditorMode === 'edit'}
                        value={mcpName}
                        placeholder="filesystem"
                        autoCapitalize="none"
                        spellCheck={false}
                        onChange={(e) => setMcpName(e.target.value)}
                      />
                    </label>
                    <label className={formFieldCls}>{t('Transport')}
                      <select className={fieldSelect} value={mcpTransport} disabled={busy} onChange={(e) => setMcpTransport(e.target.value as 'stdio' | 'streamable-http')}>
                        <option value="stdio">stdio</option>
                        <option value="streamable-http">streamable-http</option>
                      </select>
                    </label>
                  </div>

                  {mcpTransport === 'stdio' ? (
                    <>
                      <label className={formFieldCls}>{t('Command')}
                        <input
                          required
                          className={fieldInput}
                          disabled={busy}
                          value={mcpCommand}
                          placeholder="npx"
                          autoCapitalize="none"
                          spellCheck={false}
                          onChange={(e) => setMcpCommand(e.target.value)}
                        />
                      </label>

                      <div className="mcp-editor-block flex flex-col gap-2">
                        <div className="provider-models-head flex items-center justify-between gap-2">
                          <span className="text-[12.5px] font-semibold text-ink">{t('Arguments')}</span>
                          <button type="button" className={btnPlain} disabled={busy} onClick={() => setMcpArgs((prev) => [...prev, ''])}>
                            <Plus size={12} />{t('Add argument')}
                          </button>
                        </div>
                        {mcpArgs.map((arg, idx) => (
                          <div className="mcp-kv-row flex items-center gap-2" key={idx}>
                            <input
                              className={cn(fieldInput, 'h-[32px] font-mono text-[12.5px]')}
                              value={arg}
                              disabled={busy}
                              placeholder={`arg ${idx + 1}`}
                              spellCheck={false}
                              onChange={(e) => setMcpArgs((prev) => prev.map((v, i) => (i === idx ? e.target.value : v)))}
                            />
                            <button type="button" className={cn('icon-button', btnIcon)} aria-label={t('Delete')} disabled={busy} onClick={() => setMcpArgs((prev) => prev.filter((_, i) => i !== idx))}>
                              <Trash2 size={13} />
                            </button>
                          </div>
                        ))}
                      </div>

                      <div className="mcp-editor-block flex flex-col gap-2">
                        <div className="provider-models-head flex items-center justify-between gap-2">
                          <span className="text-[12.5px] font-semibold text-ink">{t('Environment variables')}</span>
                          <button
                            type="button"
                            className={btnPlain}
                            disabled={busy}
                            onClick={() => setMcpEnvRows((prev) => [...prev, { id: `new:${Date.now()}:${prev.length}`, key: '', value: '', saved: false }])}
                          >
                            <Plus size={12} />{t('Add variable')}
                          </button>
                        </div>
                        {mcpEnvRows.map((row) => (
                          <div className="mcp-kv-row env grid grid-cols-[minmax(120px,0.9fr)_minmax(0,1.2fr)_auto] items-center gap-2" key={row.id}>
                            <input
                              className={cn(fieldInput, 'h-[32px] font-mono text-[12.5px]')}
                              value={row.key}
                              disabled={busy}
                              placeholder="ENV_KEY"
                              autoCapitalize="none"
                              spellCheck={false}
                              onChange={(e) => setMcpEnvRows((prev) => prev.map((r) => (r.id === row.id ? { ...r, key: e.target.value, saved: false } : r)))}
                            />
                            <input
                              type="password"
                              className={cn(fieldInput, 'h-[32px] text-[12.5px]')}
                              value={row.value}
                              disabled={busy}
                              autoComplete="off"
                              placeholder={row.saved ? t('Saved — leave blank to keep') : t('Value')}
                              onChange={(e) => setMcpEnvRows((prev) => prev.map((r) => (r.id === row.id ? { ...r, value: e.target.value } : r)))}
                            />
                            <button type="button" className={cn('icon-button', btnIcon)} aria-label={t('Delete')} disabled={busy} onClick={() => setMcpEnvRows((prev) => prev.filter((r) => r.id !== row.id))}>
                              <Trash2 size={13} />
                            </button>
                          </div>
                        ))}
                      </div>
                    </>
                  ) : (
                    <>
                      <label className={formFieldCls}>{t('Endpoint URL')}
                        <input
                          required
                          type="url"
                          className={fieldInput}
                          disabled={busy}
                          value={mcpURL}
                          placeholder="https://mcp.example.com/mcp"
                          autoCapitalize="none"
                          spellCheck={false}
                          onChange={(e) => setMcpURL(e.target.value)}
                        />
                      </label>
                      <label className={formFieldCls}>{t('OAuth client metadata URL (optional)')}
                        <input
                          type="url"
                          className={fieldInput}
                          disabled={busy}
                          value={mcpClientMetadataURL}
                          placeholder="https://app.example.com/oauth/client.json"
                          autoCapitalize="none"
                          spellCheck={false}
                          onChange={(e) => setMcpClientMetadataURL(e.target.value)}
                        />
                      </label>
                    </>
                  )}

                  <div className="settings-form-footer flex items-center justify-end gap-2 border-t border-line-subtle pt-3">
                    <button type="button" className={btnSecondary} disabled={busy} onClick={() => setMcpEditorMode('closed')}>{t('Cancel')}</button>
                    <button type="submit" className={btnPrimaryCompact} disabled={busy}><Check size={14} />{t('Save')}</button>
                  </div>
                </form>
              )}

              <div className="provider-cards flex flex-col gap-2.5">
                {mcpServerNames.length === 0 && mcpEditorMode === 'closed' && (
                  <div className="provider-empty-card flex flex-col items-center gap-3 rounded-[12px] border border-dashed border-line bg-subtle px-6 py-8 text-center">
                    <div className="empty-card-icon grid h-10 w-10 place-items-center rounded-[10px] border border-line-subtle bg-canvas text-ink-secondary"><Plug size={20} aria-hidden="true" /></div>
                    <p className="m-0 text-[13px] text-ink-muted">{t('No custom MCP servers configured in this scope.')}</p>
                    <button type="button" className={btnPrimaryCompact} disabled={busy} onClick={openAddMCP}>
                      <Plus size={14} aria-hidden="true" />{t('Add server')}
                    </button>
                  </div>
                )}

                {mcpServerNames.map((name) => {
                  const s = mcpServers[name]
                  const cmdLine = s.transport === 'stdio'
                    ? [s.command || '', ...(s.args ?? [])].filter(Boolean).join(' ')
                    : (s.url || '')
                  return (
                    <div className="provider-card mcp-config-card flex flex-col gap-3 rounded-[12px] border border-line-subtle bg-subtle p-3.5 transition-[border-color] duration-150 hover:border-line" key={name}>
                      <div className="provider-card-head flex items-start justify-between gap-3">
                        <div className="provider-card-main min-w-0 flex-1">
                          <div className="provider-card-title flex flex-wrap items-center gap-2">
                            <strong className="text-[13.5px] font-semibold text-ink">{name}</strong>
                            <span className={statusPill}>{s.transport}</span>
                            {Boolean(s.envKeys?.length) && (
                              <span className={cn(statusPill, 'mcp-env-saved-tag')}>{s.envKeys!.length} {t('env')}</span>
                            )}
                          </div>
                          <small className="mt-1 block truncate font-mono text-[11.5px] text-ink-muted">{cmdLine}</small>
                        </div>
                        <div className="settings-card-actions flex shrink-0 items-center gap-1.5">
                          {confirmDeleteMcp === name ? (
                            <>
                              <button type="button" className={btnPlain} disabled={busy} onClick={() => setConfirmDeleteMcp('')}>{t('Cancel')}</button>
                              <button type="button" className={btnPlainDanger} disabled={busy} onClick={() => void deleteMCPServer(name)}>{t('Confirm delete')}</button>
                            </>
                          ) : (
                            <>
                              <button type="button" className={btnSecondary} disabled={busy} onClick={() => openEditMCP(name)}>{t('Edit')}</button>
                              <button type="button" className={cn('icon-button', btnIcon)} disabled={busy} aria-label={t('Delete')} onClick={() => setConfirmDeleteMcp(name)}><Trash2 size={14} /></button>
                            </>
                          )}
                        </div>
                      </div>
                    </div>
                  )
                })}
              </div>
            </div>
          )}

          {tab === 'runtime' && api.supportsModelConfig && (
            <div className="settings-pane flex flex-col gap-4">
              <div className="settings-tab-head flex items-center justify-between gap-4">
                <div>
                  <h3 className="m-0 text-[16px] font-semibold tracking-[-0.02em] text-ink">{t('Runtime & Agents')}</h3>
                  <p className="settings-tab-desc mt-[3px] mb-0 text-[12.5px] leading-[1.5] text-ink-muted">{t('These limits apply to new tasks. Running tasks keep their current settings.')}</p>
                </div>
              </div>

              <section className="settings-section flex flex-col gap-[7px]">
                <h4 className={settingsSectionTitleCls}>{t('Concurrency & context')}</h4>
                <div className="settings-group-card flex flex-col gap-3 rounded-[11px] border border-line-subtle bg-subtle px-4 py-3.5">
                  <div className={formGridCls}>
                    <label className={formFieldCls}>{t('Parallel agents (0 = automatic)')}
                      <input type="number" className={fieldInput} min={0} placeholder="0" value={Number(asObject(doc.runtime).maxParallelAgents) || ''} onChange={(e) => updateConfigField('runtime', 'maxParallelAgents', e.target.value ? Number(e.target.value) : '')} />
                    </label>
                    <label className={formFieldCls}>{t('Context window (0 = automatic)')}
                      <input type="number" className={fieldInput} min={0} placeholder="0" value={Number(asObject(doc.runtime).contextWindow) || ''} onChange={(e) => updateConfigField('runtime', 'contextWindow', e.target.value ? Number(e.target.value) : '')} />
                    </label>
                    <label className={formFieldCls}>{t('Children per agent (0 = default)')}
                      <input type="number" className={fieldInput} min={0} placeholder="0" value={Number(asObject(doc.runtime).maxTeamChildrenPerAgent) || ''} onChange={(e) => updateConfigField('runtime', 'maxTeamChildrenPerAgent', e.target.value ? Number(e.target.value) : '')} />
                    </label>
                    <label className={formFieldCls}>{t('Delegation depth (0 = default)')}
                      <input type="number" className={fieldInput} min={0} placeholder="0" value={Number(asObject(doc.runtime).maxTeamDepth) || ''} onChange={(e) => updateConfigField('runtime', 'maxTeamDepth', e.target.value ? Number(e.target.value) : '')} />
                    </label>
                  </div>
                </div>
              </section>

              <section className="settings-section flex flex-col gap-[7px]">
                <h4 className={settingsSectionTitleCls}>{t('Context compaction')}</h4>
                <div className={settingsGroupCls}>
                  <label className={cn(settingsRowCls, 'settings-row-interactive cursor-pointer transition-[background] duration-120 hover:bg-hover')}>
                    <span className="settings-row-info flex min-w-0 flex-col gap-0.5">
                      <strong className="text-[13px] font-medium text-ink">{t('Automatically compact context')}</strong>
                    </span>
                    <span className={settingsRowControlCls}>
                      <span className="toggle-control relative inline-flex h-5 w-[34px] flex-none items-center">
                        <input
                          type="checkbox"
                          className="absolute m-0 h-0 w-0 opacity-0"
                          checked={asObject(doc.runtime).autoCompact !== false}
                          onChange={(e) => updateConfigField('runtime', 'autoCompact', e.target.checked)}
                        />
                        <span className="toggle-track" aria-hidden="true" />
                      </span>
                    </span>
                  </label>
                  <div className="settings-group-inner px-4 py-3.5">
                    <div className={formGridCls}>
                      <label className={formFieldCls}>{t('Compaction threshold (0.1–1.0)')}
                        <input type="number" className={fieldInput} min="0.1" max="1" step="0.05" placeholder="0.85" value={Number(asObject(doc.runtime).autoCompactThreshold) || ''} onChange={(e) => updateConfigField('runtime', 'autoCompactThreshold', e.target.value ? Number(e.target.value) : '')} />
                      </label>
                      <label className={formFieldCls}>{t('Reserve output tokens (0 = default)')}
                        <input type="number" className={fieldInput} min={0} placeholder="0" value={Number(asObject(doc.runtime).reserveOutputTokens) || ''} onChange={(e) => updateConfigField('runtime', 'reserveOutputTokens', e.target.value ? Number(e.target.value) : '')} />
                      </label>
                    </div>
                  </div>
                </div>
              </section>

              <section className="settings-section flex flex-col gap-[7px]">
                <h4 className={settingsSectionTitleCls}>{t('Attachment limits')}</h4>
                <div className="settings-group-card flex flex-col gap-3 rounded-[11px] border border-line-subtle bg-subtle px-4 py-3.5">
                  <div className={formGridCls}>
                    <label className={formFieldCls}>{t('Maximum files')}
                      <input type="number" className={fieldInput} min={0} placeholder="0" value={Number(asObject(doc.attachments).maxFiles) || ''} onChange={(e) => updateConfigField('attachments', 'maxFiles', e.target.value ? Number(e.target.value) : '')} />
                    </label>
                    <label className={formFieldCls}>{t('Maximum size per file (bytes)')}
                      <input type="number" className={fieldInput} min={0} placeholder="0" value={Number(asObject(doc.attachments).maxFileBytes) || ''} onChange={(e) => updateConfigField('attachments', 'maxFileBytes', e.target.value ? Number(e.target.value) : '')} />
                    </label>
                  </div>
                  <label className={formFieldCls}>{t('Maximum combined attachment size (bytes)')}
                    <input type="number" className={fieldInput} min={0} placeholder="0" value={Number(asObject(doc.attachments).maxTotalBytes) || ''} onChange={(e) => updateConfigField('attachments', 'maxTotalBytes', e.target.value ? Number(e.target.value) : '')} />
                  </label>
                </div>
              </section>

              <div className="settings-save-bar flex justify-end pt-0.5">
                <button type="button" className={btnPrimaryCompact} disabled={busy} onClick={() => void commitDocument(doc, imported?.id, t('Runtime settings saved to SQLite database.'))}>
                  <Check size={14} aria-hidden="true" />{t('Save runtime settings')}
                </button>
              </div>
            </div>
          )}

          {tab === 'remote' && (
            <div className="settings-pane flex flex-col gap-4">
              <div className="settings-tab-head flex items-center justify-between gap-4">
                <div>
                  <h3 className="m-0 text-[16px] font-semibold tracking-[-0.02em] text-ink">{t('Remote & Token')}</h3>
                  <p className="settings-tab-desc mt-[3px] mb-0 text-[12.5px] leading-[1.5] text-ink-muted">{t('Connect to a remote Lune Core endpoint over WebSocket and manage saved tokens.')}</p>
                </div>
              </div>

              {isRemoteActive && (
                <div className={settingsCardCls}>
                  <div className="settings-card-main flex min-w-0 flex-col gap-[3px]">
                    <div className="settings-card-title flex items-center gap-2">
                      <strong className="text-[13px] font-medium text-ink">{t('Connected to Remote Core')}</strong>
                      <span className={cn(statusPill, 'border-accent bg-accent text-accent-fg')}>{t('Connected')}</span>
                    </div>
                    <small className="truncate font-mono text-[11.5px] text-ink-muted">{activeRemoteURL}</small>
                  </div>
                  <div className="settings-card-actions flex shrink-0 items-center gap-1.5">
                    {hasLocalHost && onReturnToLocal && (
                      <button type="button" className={btnSecondary} disabled={busy} onClick={() => void onReturnToLocal().then(onClose)}>
                        <FolderGit2 size={14} aria-hidden="true" />{t('Return to local workspaces')}
                      </button>
                    )}
                    {onDisconnectRemote && (
                      <button type="button" className={btnPlain} disabled={busy} onClick={() => void onDisconnectRemote().then(onClose)}>{t('Disconnect')}</button>
                    )}
                  </div>
                </div>
              )}

              {connections.length > 0 ? (
                <div className="settings-cards flex flex-col gap-2">
                  {connections.map((saved) => (
                    <div className={settingsCardCls} key={saved.address}>
                      <div className="settings-card-main flex min-w-0 flex-col gap-[3px]">
                        <div className="settings-card-title flex items-center gap-2">
                          <strong className="text-[13px] font-medium text-ink">{saved.name || saved.address}</strong>
                          {saved.remembered && <span className={cn(statusPill, 'border-accent bg-accent text-accent-fg')}>{t('Token saved in DB')}</span>}
                        </div>
                        <small className="truncate font-mono text-[11.5px] text-ink-muted">{saved.address}</small>
                      </div>
                      <div className="settings-card-actions flex shrink-0 items-center gap-1.5">
                        <button type="button" className={btnSecondary} disabled={busy} onClick={() => void connectSavedRemote(saved)}>{t('Connect')}</button>
                        <button
                          type="button"
                          className={cn('icon-button', btnIcon)}
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
              ) : (
                <p className="m-0 text-[13px] text-ink-muted">{t('No saved remote Core endpoints yet.')}</p>
              )}

              <form className="settings-group-card flex flex-col gap-3 rounded-[11px] border border-line-subtle bg-subtle px-4 py-3.5" onSubmit={(e) => { e.preventDefault(); void saveRemoteForm(true) }}>
                <div className="settings-form-head">
                  <h3 className="m-0 text-[14px] font-semibold text-ink">{t('Remote Core endpoint')}</h3>
                </div>
                <label className={formFieldCls}>{t('Core WebSocket URL')}
                  <input required className={fieldInput} disabled={busy} value={remoteURL} onChange={(e) => setRemoteURL(e.target.value)} placeholder="wss://core.example.com:18092/v1/runtime" autoCapitalize="none" spellCheck={false} />
                </label>
                <div className={formGridCls}>
                  <label className={formFieldCls}>{t('Connection name')}
                    <input className={fieldInput} disabled={busy} value={remoteName} onChange={(e) => setRemoteName(e.target.value)} placeholder={t('Optional label')} />
                  </label>
                  <label className={formFieldCls}>{t('Access token')}
                    <input type="password" className={fieldInput} autoComplete="off" disabled={busy} value={remoteToken} onChange={(e) => setRemoteToken(e.target.value)} placeholder={t('Paste your Core token')} />
                  </label>
                </div>
                <div className="settings-form-footer settings-form-footer-split flex flex-wrap items-center justify-between gap-2.5 border-t border-line-subtle pt-3">
                  <ToggleSwitch
                    label={t('Save token in SQLite database')}
                    checked={rememberToken}
                    disabled={busy}
                    onChange={(checked) => setRememberToken(checked)}
                  />
                  <div className="settings-footer-btns flex items-center gap-2">
                    <button type="button" className={btnSecondary} disabled={busy || !remoteURL.trim()} onClick={() => void saveRemoteForm(false)}>{t('Save only')}</button>
                    <button type="submit" className={btnPrimaryCompact} disabled={busy || !remoteURL.trim()}>
                      <RefreshCw size={14} aria-hidden="true" />{t('Save & Connect')}
                    </button>
                  </div>
                </div>
              </form>
            </div>
          )}

          {tab === 'general' && (
            <div className="settings-pane flex flex-col gap-4">
              <div className="settings-tab-head flex items-center justify-between gap-4">
                <div>
                  <h3 className="m-0 text-[16px] font-semibold tracking-[-0.02em] text-ink">{t('General')}</h3>
                  <p className="settings-tab-desc mt-[3px] mb-0 text-[12.5px] leading-[1.5] text-ink-muted">{t('Configure personal appearance, typography, and interaction preferences.')}</p>
                </div>
              </div>

              <section className="settings-section flex flex-col gap-[7px]">
                <h4 className={settingsSectionTitleCls}>{t('Appearance & language')}</h4>
                <div className={settingsGroupCls}>
                  <div className={settingsRowCls}>
                    <div className="settings-row-info flex min-w-0 flex-col gap-0.5">
                      <strong className="text-[13px] font-medium text-ink">{t('Appearance')}</strong>
                    </div>
                    <div className={cn(settingsRowControlCls, '[&_.theme-picker]:w-full sm:[&_.theme-picker]:w-[176px]')}>
                      <ThemePicker value={theme} onChange={(value) => void saveUiPreference({ theme: value })} />
                    </div>
                  </div>
                  <label className={settingsRowCls}>
                    <span className="settings-row-info flex min-w-0 flex-col gap-0.5">
                      <strong className="text-[13px] font-medium text-ink">{t('Interface language')}</strong>
                    </span>
                    <span className={settingsRowControlCls}>
                      <select
                        className={cn(fieldSelect, 'h-[32px] w-full sm:w-[176px] text-[12.5px]')}
                        value={language}
                        onChange={(event) => void saveUiPreference({ language: event.target.value as UiPreferences['language'] })}
                      >
                        <option value="en">English</option>
                        <option value="zh">简体中文</option>
                      </select>
                    </span>
                  </label>
                </div>
              </section>

              <section className="settings-section flex flex-col gap-[7px]">
                <h4 className={settingsSectionTitleCls}>{t('Typography & display')}</h4>
                <div className={settingsGroupCls}>
                  <label className={settingsRowCls}>
                    <span className="settings-row-info flex min-w-0 flex-col gap-0.5">
                      <strong className="text-[13px] font-medium text-ink">{t('Message text size')}</strong>
                    </span>
                    <span className={settingsRowControlCls}>
                      <select
                        className={cn(fieldSelect, 'h-[32px] w-full sm:w-[176px] text-[12.5px]')}
                        value={uiPreferences.bodyFontSize}
                        onChange={(event) => void saveUiPreference({ bodyFontSize: Number(event.target.value) })}
                      >
                        {[12, 13, 14, 15, 16, 17, 18].map((size) => <option key={size} value={size}>{size} px</option>)}
                      </select>
                    </span>
                  </label>
                  <label className={settingsRowCls}>
                    <span className="settings-row-info flex min-w-0 flex-col gap-0.5">
                      <strong className="text-[13px] font-medium text-ink">{t('Code text size')}</strong>
                    </span>
                    <span className={settingsRowControlCls}>
                      <select
                        className={cn(fieldSelect, 'h-[32px] w-full sm:w-[176px] text-[12.5px]')}
                        value={uiPreferences.codeFontSize}
                        onChange={(event) => void saveUiPreference({ codeFontSize: Number(event.target.value) })}
                      >
                        {[10, 11, 12, 13, 14, 15, 16].map((size) => <option key={size} value={size}>{size} px</option>)}
                      </select>
                    </span>
                  </label>
                  <label className={settingsRowCls}>
                    <span className="settings-row-info flex min-w-0 flex-col gap-0.5">
                      <strong className="text-[13px] font-medium text-ink">{t('Usage display density')}</strong>
                    </span>
                    <span className={settingsRowControlCls}>
                      <select
                        className={cn(fieldSelect, 'h-[32px] w-full sm:w-[176px] text-[12.5px]')}
                        value={uiPreferences.usageDensity}
                        onChange={(event) => void saveUiPreference({ usageDensity: event.target.value as UiPreferences['usageDensity'] })}
                      >
                        <option value="comfortable">{t('Comfortable')}</option>
                        <option value="compact">{t('Compact')}</option>
                      </select>
                    </span>
                  </label>
                </div>
              </section>

              <section className="settings-section flex flex-col gap-[7px]">
                <h4 className={settingsSectionTitleCls}>{t('Behavior')}</h4>
                <div className={settingsGroupCls}>
                  <label className={settingsRowCls}>
                    <span className="settings-row-info flex min-w-0 flex-col gap-0.5">
                      <strong className="text-[13px] font-medium text-ink">{t('Pressing Enter while a task is running')}</strong>
                    </span>
                    <span className={settingsRowControlCls}>
                      <select
                        className={cn(fieldSelect, 'h-[32px] w-full sm:w-[176px] text-[12.5px]')}
                        value={uiPreferences.runningEnter}
                        onChange={(event) => void saveUiPreference({ runningEnter: event.target.value as UiPreferences['runningEnter'] })}
                      >
                        <option value="queue">{t('Queue a follow-up')}</option>
                        <option value="steer">{t('Interrupt with this message')}</option>
                      </select>
                    </span>
                  </label>
                  {api.supportsShortcut && window.luneDesktop && (
                    <div className={settingsRowCls}>
                      <label className="settings-row-info flex min-w-0 flex-col gap-0.5" htmlFor="settings-quick-shortcut">
                        <strong className="text-[13px] font-medium text-ink">{t('Quick window shortcut')}</strong>
                      </label>
                      <div className={settingsRowControlCls}>
                        <div className="input-with-action compact-action flex w-[240px] max-w-full items-center gap-1.5">
                          <input
                            id="settings-quick-shortcut"
                            className={cn(fieldInput, 'h-[32px] flex-1 px-2.5 text-[12.5px]')}
                            value={shortcut}
                            onChange={(e) => setShortcut(e.target.value)}
                          />
                          <button
                            type="button"
                            className={btnSecondary}
                            onClick={() => void window.luneDesktop?.setShortcut(shortcut).then(setShortcut).catch((err) => setError(err instanceof Error ? err.message : String(err)))}
                          >
                            {t('Apply')}
                          </button>
                        </div>
                      </div>
                    </div>
                  )}
                </div>
              </section>
            </div>
          )}
        </div>
      </div>
    </dialog>
  )
}
