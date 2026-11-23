import { contextBridge, ipcRenderer } from 'electron'
import type { ThemePreference } from '../shared/theme.js'

import type { ConfigDocument, ConfigImport, ConfigView, DesktopConnectionOptions, SavedConnection } from '../shared/desktop-settings.js'

contextBridge.exposeInMainWorld('luneDesktop', {
  getConfig: (scope: string): Promise<ConfigView> => ipcRenderer.invoke('settings:config', scope),
  chooseConfigImport: (): Promise<ConfigImport | null> => ipcRenderer.invoke('settings:import'),
  saveConfig: (scope: string, document: ConfigDocument, importId?: string): Promise<boolean> => ipcRenderer.invoke('settings:save-config', scope, document, importId),
  resetWorkspaceConfig: (workspace: string): Promise<void> => ipcRenderer.invoke('settings:reset-workspace', workspace),
  savedConnections: (): Promise<{ items: SavedConnection[]; notice: string }> => ipcRenderer.invoke('settings:connections'),
  forgetConnection: (mode: string, address: string): Promise<void> => ipcRenderer.invoke('settings:forget-connection', mode, address),
  lastSession: (): Promise<string | null> => ipcRenderer.invoke('settings:last-session'),
  rememberSession: (mode: string, address: string, session: string): Promise<void> => ipcRenderer.invoke('settings:remember-session', mode, address, session),
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
})
