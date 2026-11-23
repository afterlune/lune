import type { DesktopConnectionOptions } from '../electron/preload'

declare global {
  interface Window {
    luneDesktop?: {
      selectWorkspace(): Promise<string | null>
      connect(options: DesktopConnectionOptions): Promise<unknown>
      reconnect(): Promise<unknown>
      request<T>(method: string, params?: unknown): Promise<T>
      close(forget?: boolean): Promise<void>
      onNotification(listener: (notification: { method: string; params?: unknown }) => void): () => void
    }
  }
}

export {}
