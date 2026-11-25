export type ConfigDocument = Record<string, unknown>

export type MCPServerSummary = {
  transport: 'stdio' | 'streamable-http'
  command?: string
  args?: string[]
  envKeys?: string[]
  url?: string
  clientMetadataURL?: string
}

export type ConfigView = {
  document: ConfigDocument
  savedKeys: string[]
  hasMCP: boolean
  mcpServers?: Record<string, MCPServerSummary>
  canSaveSecrets: boolean
  secretStorage?: 'encrypted' | 'memory'
  inheritedProfiles: string[]
}

export type ConfigImport = {
  id: string
  document: ConfigDocument
  suppliedKeys: string[]
  unresolvedKeys: string[]
  hasMCP: boolean
}

export type SavedConnection = {
  mode: 'local' | 'remote'
  address: string
  name: string
  remembered: boolean
}

export type WorkspaceSessionRecord = {
  sessionId: string
  workspaceId: string
  title: string
  archived: boolean
  createdAt: number
  updatedAt: number
  lastOpenedAt: number
}

export type WorkspaceRecord = {
  id: string
  path: string
  name: string
  isOpen: boolean
  lastSessionId: string
  createdAt: number
  updatedAt: number
  lastOpenedAt: number
  sessions?: WorkspaceSessionRecord[]
}

export type DesktopConnectionOptions =
  | { mode: 'local'; workspace: string }
  | { mode: 'remote'; url: string; token: string; name?: string; rememberToken?: boolean }

export type ProviderTestInput = {
  provider: string
  protocol: 'responses' | 'anthropic-messages'
  baseURL: string
  apiKey?: string
  apiKeyEnv?: string
}

export type ProviderModelInfo = {
  id: string
  displayName?: string
  contextWindow?: number
  maxOutputTokens?: number
  supportsInputImages?: boolean
  supportsInputAudio?: boolean
}

export type ProviderTestResult = {
  ok: boolean
  durationMs: number
  modelCount?: number
  models?: ProviderModelInfo[]
  error?: string
}
export type { UiPreferences } from './ui-preferences.js'
