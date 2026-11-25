import type { ThemePreference } from '../shared/theme'
import type { Attachment } from './attachments'
import type { ConfigDocument, ConfigImport, ConfigView, DesktopConnectionOptions, ProviderModelInfo, ProviderTestInput, ProviderTestResult, SavedConnection, UiPreferences, WorkspaceRecord } from '../shared/desktop-settings'

declare global {
  interface Window {
    luneDesktop?: {
      getConfig(scope: string): Promise<ConfigView>
      chooseConfigImport(): Promise<ConfigImport | null>
      saveConfig(scope: string, document: ConfigDocument, importId?: string): Promise<boolean>
      resetWorkspaceConfig(workspace: string): Promise<void>
      fetchProviderModels(scope: string, provider: string): Promise<ProviderModelInfo[]>
      testProvider(scope: string, input: ProviderTestInput): Promise<ProviderTestResult>
      getUiPreferences(): Promise<UiPreferences>
      saveUiPreferences(patch: Partial<UiPreferences>): Promise<UiPreferences>
      savedConnections(): Promise<{ items: SavedConnection[]; notice: string }>
      saveRemoteConnection(input: { url: string; name?: string; token?: string; rememberToken?: boolean }): Promise<void>
      forgetConnection(mode: string, address: string): Promise<void>
      lastSession(): Promise<string | null>
      rememberSession(mode: string, address: string, session: string): Promise<void>
      listWorkspaces(): Promise<WorkspaceRecord[]>
      selectPersistedWorkspace(id: string): Promise<void>
      closePersistedWorkspace(id: string): Promise<void>
      saveWorkspacePermission(workspace: string, mode: string): Promise<void>
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
      terminalOpen(input: { cwd?: string; cols?: number; rows?: number }): Promise<{ id: string }>
      terminalWrite(id: string, data: string): Promise<void>
      terminalResize(id: string, cols: number, rows: number): Promise<void>
      terminalClose(id: string): Promise<void>
      onTerminalEvent(listener: (event: { id: string; type: string; data?: string; code?: number; message?: string }) => void): () => void
    }
  }
}

export {}
