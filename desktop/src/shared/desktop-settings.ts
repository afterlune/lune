export type ConfigDocument = Record<string, unknown>

export type ConfigView = {
  document: ConfigDocument
  savedKeys: string[]
  hasMCP: boolean
  canSaveSecrets: boolean
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

export type DesktopConnectionOptions =
  | { mode: 'local'; workspace: string }
  | { mode: 'remote'; url: string; token: string; name?: string; rememberToken?: boolean }
