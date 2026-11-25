import { contextBridge, ipcRenderer } from 'electron'
import type { ThemePreference } from '../shared/theme.js'

import type { ConfigDocument, ConfigImport, ConfigView, DesktopConnectionOptions, ProviderModelInfo, ProviderTestInput, ProviderTestResult, SavedConnection, UiPreferences } from '../shared/desktop-settings.js'

contextBridge.exposeInMainWorld('luneDesktop', {
  getConfig: (scope: string): Promise<ConfigView> => ipcRenderer.invoke('settings:config', scope),
  chooseConfigImport: (): Promise<ConfigImport | null> => ipcRenderer.invoke('settings:import'),
  saveConfig: (scope: string, document: ConfigDocument, importId?: string): Promise<boolean> => ipcRenderer.invoke('settings:save-config', scope, document, importId),
  resetWorkspaceConfig: (workspace: string): Promise<void> => ipcRenderer.invoke('settings:reset-workspace', workspace),
  fetchProviderModels: (scope: string, provider: string): Promise<ProviderModelInfo[]> =>
    ipcRenderer.invoke('settings:fetch-provider-models', scope, provider),
  testProvider: (scope: string, input: ProviderTestInput): Promise<ProviderTestResult> => ipcRenderer.invoke('settings:test-provider', scope, input),
  getUiPreferences: (): Promise<UiPreferences> => ipcRenderer.invoke('settings:ui-preferences'),
  saveUiPreferences: (patch: Partial<UiPreferences>): Promise<UiPreferences> => ipcRenderer.invoke('settings:save-ui-preferences', patch),
  savedConnections: (): Promise<{ items: SavedConnection[]; notice: string }> => ipcRenderer.invoke('settings:connections'),
  saveRemoteConnection: (input: { url: string; name?: string; token?: string; rememberToken?: boolean }): Promise<void> =>
    ipcRenderer.invoke('settings:save-remote', input),
  forgetConnection: (mode: string, address: string): Promise<void> => ipcRenderer.invoke('settings:forget-connection', mode, address),
  lastSession: (): Promise<string | null> => ipcRenderer.invoke('settings:last-session'),
  rememberSession: (mode: string, address: string, session: string): Promise<void> => ipcRenderer.invoke('settings:remember-session', mode, address, session),
  listWorkspaces: () => ipcRenderer.invoke('settings:workspaces'),
  selectPersistedWorkspace: (id: string): Promise<void> => ipcRenderer.invoke('settings:select-workspace', id),
  closePersistedWorkspace: (id: string): Promise<void> => ipcRenderer.invoke('settings:close-workspace', id),
  saveWorkspacePermission: (workspace: string, mode: string): Promise<void> =>
    ipcRenderer.invoke('settings:save-workspace-permission', workspace, mode),
  getTheme: (): Promise<ThemePreference> => ipcRenderer.invoke('desktop:get-theme'),
  setTheme: (theme: ThemePreference): Promise<void> => ipcRenderer.invoke('desktop:set-theme', theme),
  shortcut: (): Promise<string> => ipcRenderer.invoke('desktop:shortcut'),
  setShortcut: (accelerator: string): Promise<string> => ipcRenderer.invoke('desktop:set-shortcut', accelerator),
  toggleQuick: (): Promise<void> => ipcRenderer.invoke('desktop:quick-toggle'),
  saveMarkdown: (markdown: string): Promise<void> => ipcRenderer.invoke('desktop:save-markdown', markdown),
  captureScreenshot: () => ipcRenderer.invoke('desktop:screenshot'),
  onQuickMode: (listener: (quick: boolean) => void): (() => void) => {
    let needsSnapshot = true
    const handler = (_event: Electron.IpcRendererEvent, enabled: boolean) => {
      needsSnapshot = false
      listener(enabled)
    }
    ipcRenderer.on('desktop:quick-mode', handler)
    void ipcRenderer.invoke('desktop:quick-mode').then((enabled: boolean) => {
      if (needsSnapshot) listener(enabled)
    }).catch(() => undefined)
    return () => {
      needsSnapshot = false
      ipcRenderer.removeListener('desktop:quick-mode', handler)
    }
  },
  selectWorkspace: (): Promise<string | null> => ipcRenderer.invoke('desktop:select-workspace'),
  connect: (options: DesktopConnectionOptions): Promise<unknown> => ipcRenderer.invoke('runtime:connect', options),
  reconnect: (): Promise<unknown> => ipcRenderer.invoke('runtime:reconnect'),
  request: <T>(method: string, params?: unknown): Promise<T> => ipcRenderer.invoke('runtime:request', { method, params }),
  close: (forget?: boolean): Promise<void> => ipcRenderer.invoke('runtime:close', forget),
  onNotification: (listener: (notification: { method: string; params?: unknown }) => void): (() => void) => {
    const handler = (_event: Electron.IpcRendererEvent, notification: { method: string; params?: unknown }) => listener(notification)
    ipcRenderer.on('runtime:notification', handler)
    return () => ipcRenderer.removeListener('runtime:notification', handler)
  },
  terminalOpen: (input: { cwd?: string; cols?: number; rows?: number }): Promise<{ id: string }> =>
    ipcRenderer.invoke('terminal:open', input),
  terminalWrite: (id: string, data: string): Promise<void> => ipcRenderer.invoke('terminal:write', { id, data }),
  terminalResize: (id: string, cols: number, rows: number): Promise<void> => ipcRenderer.invoke('terminal:resize', { id, cols, rows }),
  terminalClose: (id: string): Promise<void> => ipcRenderer.invoke('terminal:close', { id }),
  onTerminalEvent: (listener: (event: { id: string; type: string; data?: string; code?: number; message?: string }) => void): (() => void) => {
    const handler = (_event: Electron.IpcRendererEvent, payload: { id: string; type: string; data?: string; code?: number; message?: string }) => listener(payload)
    ipcRenderer.on('terminal:event', handler)
    return () => ipcRenderer.removeListener('terminal:event', handler)
  },
})
