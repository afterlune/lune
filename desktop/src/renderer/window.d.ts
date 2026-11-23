import type { Attachment } from './attachments'
import type { DesktopConnectionOptions } from '../electron/preload'

declare global {
  interface Window {
    luneDesktop?: {
      shortcut(): Promise<string>
      setShortcut(accelerator: string): Promise<string>
      toggleQuick(): Promise<void>
      saveMarkdown(markdown: string): Promise<void>
      captureScreenshot(): Promise<Attachment | null>
      onQuickMode(listener: (enabled: boolean) => void): () => void
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
