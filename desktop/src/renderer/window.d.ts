import type { ThemePreference } from '../shared/theme'
import type { Attachment } from './attachments'
import type { ConfigDocument, ConfigImport, ConfigView, DesktopConnectionOptions, SavedConnection } from '../shared/desktop-settings'

declare global {
  interface Window {
    luneDesktop?: {
      getConfig(scope: string): Promise<ConfigView>
      chooseConfigImport(): Promise<ConfigImport | null>
      saveConfig(scope: string, document: ConfigDocument, importId?: string): Promise<boolean>
      resetWorkspaceConfig(workspace: string): Promise<void>
      savedConnections(): Promise<{ items: SavedConnection[]; notice: string }>
      forgetConnection(mode: string, address: string): Promise<void>
      lastSession(): Promise<string | null>
      rememberSession(mode: string, address: string, session: string): Promise<void>
      getTheme(): Promise<ThemePreference>
      setTheme(theme: ThemePreference): Promise<void>
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
