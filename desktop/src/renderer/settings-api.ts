import type { ConfigDocument, ConfigImport, ConfigView, ProviderTestInput, ProviderTestResult, SavedConnection, UiPreferences } from '../shared/desktop-settings'
import { defaultUiPreferences, normalizeUiPreferences } from '../shared/ui-preferences'
import { loadSavedWebConnection, saveWebConnection } from './loopback'

export interface SaveRemoteInput {
  url: string
  name?: string
  token?: string
  rememberToken?: boolean
}

export interface SaveConfigResult {
  reloaded?: boolean
  requiresReconnect?: boolean
  reconnectError?: string
  initial?: string
  needsSetup?: boolean
}

export interface ProviderModelInfo {
  id: string
  displayName?: string
}

export interface FrontendSettingsAPI {
  kind: 'desktop' | 'web-host' | 'remote-only'
  supportsModelConfig: boolean
  supportsShortcut: boolean
  getConfig(scope: string): Promise<ConfigView>
  saveConfig(scope: string, document: ConfigDocument, importId?: string): Promise<SaveConfigResult>
  resetWorkspaceConfig(workspace: string): Promise<SaveConfigResult>
  fetchProviderModels?(scope: string, provider: string): Promise<ProviderModelInfo[]>
  testProvider?(scope: string, input: ProviderTestInput): Promise<ProviderTestResult>
  getUiPreferences(): Promise<UiPreferences>
  saveUiPreferences(patch: Partial<UiPreferences>): Promise<UiPreferences>
  chooseConfigImport?(): Promise<ConfigImport | null>
  importConfigText?(text: string): Promise<ConfigImport>
  savedConnections(): Promise<{ items: SavedConnection[]; notice: string }>
  saveRemoteConnection(input: SaveRemoteInput): Promise<void>
  forgetConnection(mode: 'local' | 'remote', address: string): Promise<void>
  getRemoteToken?(url: string): Promise<string>
}

export function createDesktopSettingsAPI(): FrontendSettingsAPI {
  const desktop = window.luneDesktop!
  return {
    kind: 'desktop',
    supportsModelConfig: true,
    supportsShortcut: true,
    getConfig: (scope) => desktop.getConfig(scope),
    saveConfig: async (scope, document, importId) => {
      await desktop.saveConfig(scope, document, importId)
      return { requiresReconnect: true }
    },
    resetWorkspaceConfig: async (workspace) => {
      await desktop.resetWorkspaceConfig(workspace)
      return { requiresReconnect: true }
    },
    fetchProviderModels: (scope, provider) => desktop.fetchProviderModels(scope, provider),
    testProvider: (scope, input) => desktop.testProvider(scope, input),
    getUiPreferences: () => desktop.getUiPreferences(),
    saveUiPreferences: (patch) => desktop.saveUiPreferences(patch),
    chooseConfigImport: () => desktop.chooseConfigImport(),
    savedConnections: () => desktop.savedConnections(),
    saveRemoteConnection: (input) => desktop.saveRemoteConnection(input),
    forgetConnection: (mode, address) => desktop.forgetConnection(mode, address),
  }
}

export function createWebHostSettingsAPI(
  credential: string,
  onReloaded?: (result: SaveConfigResult) => void | Promise<void>,
): FrontendSettingsAPI {
  async function request<T>(path: string, body: unknown = {}): Promise<T> {
    const response = await fetch('/_lune/' + path, {
      method: 'POST',
      mode: 'same-origin',
      credentials: 'omit',
      redirect: 'error',
      headers: {
        Authorization: 'Bearer ' + credential,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(body),
    })
    const text = await response.text()
    if (!response.ok) {
      let message = text.trim()
      try {
        const parsed = JSON.parse(message) as { error?: string }
        if (parsed.error) message = parsed.error
      } catch { /* plain text error */ }
      throw new Error(message || 'Request failed.')
    }
    return JSON.parse(text) as T
  }

  return {
    kind: 'web-host',
    supportsModelConfig: true,
    supportsShortcut: false,
    getConfig: (scope) => request<ConfigView>('config/get', { scope }),
    saveConfig: async (scope, document, importId) => {
      const result = await request<SaveConfigResult>('config/save', { scope, document, importId })
      if (result.reloaded) await onReloaded?.(result)
      return result
    },
    resetWorkspaceConfig: async (workspace) => {
      const result = await request<SaveConfigResult>('config/reset', { scope: workspace })
      if (result.reloaded) await onReloaded?.(result)
      return result
    },
    fetchProviderModels: async (scope, provider) => {
      const res = await request<{ models?: ProviderModelInfo[] }>('providers/models', { scope, provider })
      return res.models ?? []
    },
    testProvider: (scope, input) => request<ProviderTestResult>('providers/test', { scope, ...input }),
    getUiPreferences: async () => {
      const result = await request<{ preferences: UiPreferences }>('preferences')
      return normalizeUiPreferences(result.preferences)
    },
    saveUiPreferences: async (patch) => {
      const current = await request<{ preferences: UiPreferences }>('preferences')
      const next = normalizeUiPreferences(patch, normalizeUiPreferences(current.preferences))
      const result = await request<{ preferences: UiPreferences }>('preferences', { preferences: next })
      return normalizeUiPreferences(result.preferences)
    },
    importConfigText: (text) => request<ConfigImport>('config/import', { text }),
    savedConnections: () => request<{ items: SavedConnection[]; notice: string }>('connections/list'),
    saveRemoteConnection: async (input) => {
      await request('connections/save', {
        mode: 'remote',
        address: input.url,
        name: input.name || '',
        token: input.token ?? '',
        rememberToken: input.rememberToken ?? true,
      })
    },
    forgetConnection: async (mode, address) => {
      await request('connections/delete', { mode, address })
    },
    getRemoteToken: async (url) => {
      const res = await request<{ token: string }>('connections/token', { address: url })
      return res.token || ''
    },
  }
}

const remoteHistoryKey = 'lune:remote-bookmarks'

export function createRemoteOnlySettingsAPI(): FrontendSettingsAPI {
  const preferencesKey = 'lune:ui-preferences'
  const readPreferences = (): UiPreferences => {
    try {
      const raw = localStorage.getItem(preferencesKey)
      return raw ? normalizeUiPreferences(JSON.parse(raw)) : { ...defaultUiPreferences }
    } catch { return { ...defaultUiPreferences } }
  }
  const readBookmarks = (): SavedConnection[] => {
    const list: SavedConnection[] = []
    try {
      const raw = localStorage.getItem(remoteHistoryKey)
      if (raw) {
        const parsed = JSON.parse(raw) as SavedConnection[]
        if (Array.isArray(parsed)) list.push(...parsed)
      }
    } catch { /* ignore */ }
    const active = loadSavedWebConnection()
    if (active && !list.some((item) => item.address === active.url)) {
      list.unshift({
        mode: 'remote',
        address: active.url,
        name: active.url,
        remembered: active.remembered,
      })
    }
    return list
  }

  const writeBookmarks = (items: SavedConnection[]) => {
    try { localStorage.setItem(remoteHistoryKey, JSON.stringify(items)) } catch { /* ignore */ }
  }

  return {
    kind: 'remote-only',
    supportsModelConfig: false,
    supportsShortcut: false,
    getConfig: async () => ({ document: {}, savedKeys: [], hasMCP: false, canSaveSecrets: false, inheritedProfiles: [] }),
    saveConfig: async () => ({}),
    resetWorkspaceConfig: async () => ({}),
    getUiPreferences: async () => readPreferences(),
    saveUiPreferences: async (patch) => {
      const next = normalizeUiPreferences(patch, readPreferences())
      try { localStorage.setItem(preferencesKey, JSON.stringify(next)) } catch { /* keep the current page preference */ }
      return next
    },
    savedConnections: async () => ({ items: readBookmarks(), notice: '' }),
    saveRemoteConnection: async (input) => {
      const current = readBookmarks().filter((item) => item.address !== input.url)
      current.unshift({
        mode: 'remote',
        address: input.url,
        name: input.name?.trim() || input.url,
        remembered: Boolean(input.rememberToken && input.token),
      })
      writeBookmarks(current)
      if (input.token !== undefined) {
        saveWebConnection(input.url, input.token, Boolean(input.rememberToken))
      }
    },
    forgetConnection: async (_mode, address) => {
      writeBookmarks(readBookmarks().filter((item) => item.address !== address))
    },
  }
}
