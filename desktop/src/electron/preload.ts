import { contextBridge, ipcRenderer } from 'electron'
import type { ThemePreference } from '../shared/theme.js'

export interface LocalConnectionOptions {
  mode: 'local'
  workspace: string
}

export interface RemoteConnectionOptions {
  mode: 'remote'
  url: string
  token: string
}

export type DesktopConnectionOptions = LocalConnectionOptions | RemoteConnectionOptions

contextBridge.exposeInMainWorld('luneDesktop', {
  getTheme: (): Promise<ThemePreference> => ipcRenderer.invoke('desktop:get-theme'),
  setTheme: (theme: ThemePreference): Promise<void> => ipcRenderer.invoke('desktop:set-theme', theme),
  shortcut: (): Promise<string> => ipcRenderer.invoke('desktop:shortcut'),
  setShortcut: (accelerator: string): Promise<string> => ipcRenderer.invoke('desktop:set-shortcut', accelerator),
  toggleQuick: (): Promise<void> => ipcRenderer.invoke('desktop:quick-toggle'),
  saveMarkdown: (markdown: string): Promise<void> => ipcRenderer.invoke('desktop:save-markdown', markdown),
  captureScreenshot: () => ipcRenderer.invoke('desktop:screenshot'),
  onQuickMode: (listener: (quick: boolean) => void): (() => void) => {
    const handler = (_event: Electron.IpcRendererEvent, enabled: boolean) => listener(enabled)
    ipcRenderer.on('desktop:quick-mode', handler)
    return () => ipcRenderer.removeListener('desktop:quick-mode', handler)
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
