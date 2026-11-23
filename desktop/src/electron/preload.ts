import { contextBridge, ipcRenderer } from 'electron'

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
