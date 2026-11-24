import { app, safeStorage } from 'electron'
import { chmodSync, readFileSync, realpathSync, statSync } from 'node:fs'
import { isAbsolute, join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import type { ConfigDocument, ConfigImport, ConfigView, MCPServerSummary, SavedConnection } from '../shared/desktop-settings.js'
import { defaultUiPreferences, normalizeUiPreferences, type UiPreferences } from '../shared/ui-preferences.js'

export const startupConfigLimit = 1024 * 1024
type Json = Record<string, unknown>
type Secret = { text: string; encrypted: Buffer; encoding: 'safeStorage' | 'plain' }
type Catalog = {
  models: Json
  providers: Record<string, Json>
  profiles: Record<string, Json>
}

function asObject(value: unknown): Json {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Configuration must be a JSON object.')
  const result = value as Json
  for (const key of Object.keys(result)) {
    if (key === '__proto__' || key === 'constructor' || key === 'prototype') throw new Error('Configuration contains a reserved field name.')
  }
  return result
}

function optionalObject(value: unknown): Json {
  return value === undefined ? {} : asObject(value)
}

function entries(value: unknown): Record<string, Json> {
  return Object.fromEntries(Object.entries(optionalObject(value)).map(([name, entry]) => [name, asObject(entry)]))
}

function checkFields(document: Json, fields: string[], section: string) {
  if (Object.keys(document).some((name) => !fields.includes(name))) {
    throw new Error(`Use the supported, case-sensitive fields in ${section}.`)
  }
}

function catalog(document: ConfigDocument): Catalog {
  checkFields(document, ['models', 'runtime', 'attachments', 'mcpServers'], 'the configuration')
  const models = optionalObject(document.models)
  checkFields(models, ['providers', 'profiles', 'defaultProfile'], 'models')
  const providers = entries(models.providers)
  for (const item of Object.values(providers)) {
    checkFields(item, ['protocol', 'baseURL', 'apiKey', 'apiKeyEnv', 'apiKeyKeyringRef', 'serverCompaction'], 'a provider')
  }
  return { models, providers, profiles: entries(models.profiles) }
}

function configWithProviders(document: ConfigDocument, models: Catalog, providers: Record<string, Json>): ConfigDocument {
  return {
    ...document, models: { ...models.models, providers, profiles: models.profiles },
  }
}

function mergeConfig(global: ConfigDocument, local: ConfigDocument): ConfigDocument {
  const inherited = catalog(global)
  const overrides = catalog(local)
  return {
    ...global, ...local,
    models: {
      ...inherited.models, ...overrides.models,
      providers: { ...inherited.providers, ...overrides.providers },
      profiles: { ...inherited.profiles, ...overrides.profiles },
    },
    runtime: { ...optionalObject(global.runtime), ...optionalObject(local.runtime) },
    attachments: { ...optionalObject(global.attachments), ...optionalObject(local.attachments) },
    mcpServers: { ...optionalObject(global.mcpServers), ...optionalObject(local.mcpServers) },
  }
}

export function workspacePath(value: unknown): string {
  if (typeof value !== 'string' || !isAbsolute(value)) throw new Error('Choose an existing workspace folder.')
  try {
    const path = realpathSync.native(value)
    if (statSync(path).isDirectory()) return path
  } catch { /* report a writable, actionable path error without printing user input */ }
  throw new Error('Choose an existing workspace folder.')
}

export function remoteAddress(value: unknown): string {
  if (typeof value !== 'string') throw new Error('Enter a remote Core URL.')
  const url = new URL(value)
  if (url.protocol !== 'wss:' || url.username || url.password || url.search || url.hash) {
    throw new Error('Use a wss:// Core URL without credentials, query, or fragment.')
  }
  return url.href
}

function canEncrypt(): boolean {
  if (!safeStorage.isEncryptionAvailable()) return false
  if (process.platform !== 'linux') return true
  return ['gnome_libsecret', 'kwallet', 'kwallet5', 'kwallet6'].includes(safeStorage.getSelectedStorageBackend())
}

export function configFrame(config: ConfigDocument): string {
  const frame = JSON.stringify(config) + '\n'
  if (Buffer.byteLength(frame) > startupConfigLimit) throw new Error('Core configuration exceeds 1 MiB.')
  return frame
}

export class DesktopSettings {
  private readonly database: DatabaseSync
  private readonly runSecrets = new Map<string, string>()
  private readonly importDrafts = new Map<string, ConfigDocument>()

  constructor() {
    const directory = app.getPath('userData')
    const path = join(directory, 'settings.sqlite')
    this.database = new DatabaseSync(path)
    try {
      if (process.platform !== 'win32') chmodSync(path, 0o600)
      this.database.exec('PRAGMA busy_timeout = 5000')
      const version = this.database.prepare('PRAGMA user_version').get()?.user_version
      if (version === 0) {
        this.transaction(() => {
          this.database.exec(`
            CREATE TABLE preferences (key TEXT PRIMARY KEY, value TEXT NOT NULL);
            CREATE TABLE configs (scope TEXT PRIMARY KEY, document TEXT NOT NULL);
            CREATE TABLE secrets (scope TEXT NOT NULL, name TEXT NOT NULL, encoding TEXT NOT NULL DEFAULT 'safeStorage', encrypted BLOB NOT NULL, PRIMARY KEY(scope,name));
            CREATE TABLE connections (
              mode TEXT NOT NULL, address TEXT NOT NULL, name TEXT NOT NULL,
              touched INTEGER NOT NULL, session TEXT, PRIMARY KEY(mode,address)
            );
            PRAGMA user_version = 2;
          `)
          for (const [key, filename] of [['theme', 'appearance.txt'], ['shortcut', 'quick-shortcut.txt']]) {
            try {
              const previous = readFileSync(join(directory, filename), 'utf8').trim()
              if (previous) this.setPreference(key, previous)
            } catch (error) {
              if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error
            }
          }
        })
      } else if (version === 1) {
        this.transaction(() => {
          this.database.exec(`ALTER TABLE secrets ADD COLUMN encoding TEXT NOT NULL DEFAULT 'safeStorage'; PRAGMA user_version = 2;`)
        })
      } else if (version !== 2) {
        throw new Error('Unsupported desktop settings database version.')
      }
    } catch (error) {
      this.database.close()
      throw error
    }
  }

  close() { this.database.close() }

  private transaction(work: () => void) {
    this.database.exec('BEGIN IMMEDIATE')
    try { work(); this.database.exec('COMMIT') }
    catch (error) { this.database.exec('ROLLBACK'); throw error }
  }

  preference(key: string): string | undefined {
    return this.database.prepare('SELECT value FROM preferences WHERE key = ?').get(key)?.value as string | undefined
  }

  setPreference(key: string, value: string) {
    this.database.prepare('INSERT INTO preferences VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value = excluded.value').run(key, value)
  }

  uiPreferences(): UiPreferences {
    const saved = this.preference('ui.preferences')
    let parsed: unknown = {}
    try { if (saved) parsed = JSON.parse(saved) } catch { /* repair malformed preferences with defaults */ }
    const base = normalizeUiPreferences(parsed, {
      ...defaultUiPreferences,
      theme: this.preference('theme') === 'light' || this.preference('theme') === 'dark' || this.preference('theme') === 'system'
        ? this.preference('theme') as UiPreferences['theme'] : defaultUiPreferences.theme,
    })
    return base
  }

  saveUiPreferences(patch: unknown): UiPreferences {
    const next = normalizeUiPreferences(patch, this.uiPreferences())
    this.setPreference('ui.preferences', JSON.stringify(next))
    this.setPreference('theme', next.theme)
    return next
  }

  private document(scope: string): ConfigDocument {
    const saved = this.database.prepare('SELECT document FROM configs WHERE scope = ?').get(scope)
    return saved ? asObject(JSON.parse(String(saved.document))) : {}
  }

  private secretKey(scope: string, name: string): string { return JSON.stringify([scope, name]) }

  private hasSecret(scope: string, name: string): boolean {
    return this.runSecrets.has(this.secretKey(scope, name)) ||
      Boolean(this.database.prepare('SELECT 1 FROM secrets WHERE scope = ? AND name = ?').get(scope, name))
  }

  private async secret(scope: string, name: string): Promise<string | undefined> {
    const transient = this.runSecrets.get(this.secretKey(scope, name))
    if (transient !== undefined) return transient
    const saved = this.database.prepare('SELECT encoding, encrypted FROM secrets WHERE scope = ? AND name = ?').get(scope, name)
    if (!saved) return undefined
    const buf = Buffer.from(saved.encrypted as Uint8Array)
    if (saved.encoding === 'plain') return buf.toString('utf8')
    if (!canEncrypt()) throw new Error('The system credential store is unavailable. Unlock it or enter the credential for this run.')
    try {
      const result = await safeStorage.decryptStringAsync(buf)
      return result.result
    } catch { throw new Error('A saved credential could not be unlocked. Re-enter it in settings.') }
  }

  private async protect(text: string): Promise<Secret> {
    if (canEncrypt()) {
      try {
        return { text, encrypted: await safeStorage.encryptStringAsync(text), encoding: 'safeStorage' }
      } catch { /* fallback to sqlite persistence */ }
    }
    return { text, encrypted: Buffer.from(text, 'utf8'), encoding: 'plain' }
  }

  private writeSecret(scope: string, name: string, value: Secret | null) {
    if (value) {
      this.database.prepare('INSERT INTO secrets(scope, name, encoding, encrypted) VALUES(?,?,?,?) ON CONFLICT(scope,name) DO UPDATE SET encoding = excluded.encoding, encrypted = excluded.encrypted')
        .run(scope, name, value.encoding, value.encrypted)
    } else {
      this.database.prepare('DELETE FROM secrets WHERE scope = ? AND name = ?').run(scope, name)
    }
  }

  private updateMemory(scope: string, name: string, value: Secret | null) {
    this.runSecrets.delete(this.secretKey(scope, name))
    void value
  }

  async config(scope: string): Promise<ConfigView> {
    const document = this.document(scope)
    const mcpRaw = await this.secret(scope, 'mcp')
    const mcpServers = mcpRaw ? summarizeMCPServers(mcpRaw) : undefined
    return {
      document,
      savedKeys: Object.keys(catalog(document).providers).filter((name) => this.hasSecret(scope, 'provider:' + name)),
      hasMCP: this.hasSecret(scope, 'mcp'),
      ...(mcpServers ? { mcpServers } : {}),
      canSaveSecrets: true,
      secretStorage: canEncrypt() ? 'encrypted' : 'sqlite',
      inheritedProfiles: scope ? Object.keys(catalog(this.document('')).profiles) : [],
    }
  }

  prepareImport(text: string): ConfigImport {
    if (Buffer.byteLength(text) > startupConfigLimit) throw new Error('The configuration file exceeds 1 MiB.')
    let document: ConfigDocument
    try { document = asObject(JSON.parse(text)) } catch { throw new Error('The selected file is not a valid JSON object.') }
    const { publicDocument, suppliedKeys, unresolvedKeys } = splitImportedConfig(document)
    this.importDrafts.clear()
    const id = globalThis.crypto.randomUUID()
    this.importDrafts.set(id, document)
    return { id, document: publicDocument, suppliedKeys, unresolvedKeys, hasMCP: document.mcpServers !== undefined }
  }

  private async resolvedConfig(scope: string, document: ConfigDocument, changes = new Map<string, Secret | null>(), selection = document): Promise<ConfigDocument> {
    const cfg = catalog(document)
    const selected = catalog(selection).providers
    const providers: Record<string, Json> = Object.create(null)
    const read = (name: string) => changes.has(name) ? changes.get(name)?.text : this.secret(scope, name)
    for (const [name, provider] of Object.entries(cfg.providers)) {
      if (!Object.hasOwn(selected, name)) continue
      const { apiKeyEnv, apiKeyKeyringRef: _, ...item } = provider
      let apiKey: string | undefined
      if (apiKeyEnv !== undefined) {
        if (typeof apiKeyEnv !== 'string' || !/^[A-Za-z_][A-Za-z_0-9]*$/.test(apiKeyEnv)) throw new Error('Invalid API key environment variable name.')
        apiKey = process.env[apiKeyEnv]
        if (!apiKey?.trim()) throw new Error(`Set ${apiKeyEnv} in the Electron environment before starting Core.`)
      } else {
        apiKey = await read('provider:' + name)
      }
      providers[name] = { ...item, apiKey: apiKey ?? '' }
    }
    const mcp = await read('mcp')
    return { ...configWithProviders(document, cfg, providers), ...(mcp ? { mcpServers: JSON.parse(mcp) as unknown } : {}) }
  }

  async effectiveConfig(workspace: string): Promise<ConfigDocument> {
    const global = this.document('')
    const local = this.document(workspace)
    const hidden = new Set(Object.keys(catalog(local).providers))
    const scope = catalog(global)
    const usedGlobal = configWithProviders(global, scope, Object.fromEntries(Object.entries(scope.providers).filter(([name]) => !hidden.has(name))))
    return mergeConfig(await this.resolvedConfig('', global, undefined, usedGlobal), await this.resolvedConfig(workspace, local))
  }

  async saveConfig(scope: string, value: unknown, importId: string | undefined, validate: (config: ConfigDocument) => Promise<void>): Promise<boolean> {
    const document = asObject(value)
    const imported = importId ? this.importDrafts.get(importId) : undefined
    if (importId && !imported) throw new Error('Select the configuration file again.')
    const changes = new Map<string, Secret | null>()
    const cfg = catalog(document)
    const old = catalog(this.document(scope))
    const incoming = catalog(imported ?? {})
    const providers: Record<string, Json> = Object.create(null)
    for (const [name, entry] of Object.entries(cfg.providers)) {
      const { apiKey, apiKeyKeyringRef, ...publicFields } = entry
      const value = apiKey ?? incoming.providers[name]?.apiKey
      const oldReference = apiKeyKeyringRef ?? incoming.providers[name]?.apiKeyKeyringRef
      if (oldReference && !value && !entry.apiKeyEnv && !this.hasSecret(scope, 'provider:' + name)) {
        throw new Error('Replace the imported keyring reference with an API key or environment variable.')
      }
      if (entry.apiKeyEnv !== undefined) changes.set('provider:' + name, null)
      else if (value !== undefined) {
        if (typeof value !== 'string') throw new Error('API keys must be strings.')
        if (value.includes('\n') || value.includes('\r')) throw new Error('API keys must be a single line.')
        changes.set('provider:' + name, value ? await this.protect(value) : null)
      }
      providers[name] = publicFields
    }
    for (const name of Object.keys(old.providers)) if (!Object.hasOwn(providers, name)) changes.set('provider:' + name, null)
    const { mcpServers, ...publicDocument } = configWithProviders(document, cfg, providers)
    const newMCP = mcpServers ?? imported?.mcpServers
    if (newMCP !== undefined) {
      const entriesMap = asObject(newMCP)
      const oldMCP = await this.secret(scope, 'mcp')
      const merged = mergeMCPServers(entriesMap, oldMCP)
      changes.set('mcp', Object.keys(merged).length ? await this.protect(JSON.stringify(merged)) : null)
    }
    const inherited = scope ? this.document('') : {}
    const hidden = new Set(Object.keys(providers))
    const defaults = catalog(inherited)
    const usedGlobal = configWithProviders(inherited, defaults, Object.fromEntries(Object.entries(defaults.providers).filter(([name]) => !hidden.has(name))))
    const candidate = await this.resolvedConfig(scope, publicDocument, changes)
    await validate(scope ? mergeConfig(await this.resolvedConfig('', inherited, undefined, usedGlobal), candidate) : candidate)
    this.transaction(() => {
      this.database.prepare('INSERT INTO configs VALUES(?,?) ON CONFLICT(scope) DO UPDATE SET document = excluded.document').run(scope, JSON.stringify(publicDocument))
      for (const [name, change] of changes) this.writeSecret(scope, name, change)
    })
    for (const [name, change] of changes) this.updateMemory(scope, name, change)
    if (importId) this.importDrafts.delete(importId)
    return false
  }

  async resetWorkspace(scope: string, validate: (config: ConfigDocument) => Promise<void>) {
    if (!scope) throw new Error('Select a workspace to restore inheritance.')
    await validate(await this.resolvedConfig('', this.document('')))
    this.transaction(() => {
      this.database.prepare('DELETE FROM configs WHERE scope = ?').run(scope)
      this.database.prepare('DELETE FROM secrets WHERE scope = ?').run(scope)
    })
    for (const key of this.runSecrets.keys()) if ((JSON.parse(key) as string[])[0] === scope) this.runSecrets.delete(key)
  }

  connections(): SavedConnection[] {
    return this.database.prepare('SELECT mode,address,name FROM connections ORDER BY touched DESC').all().map((row) => ({
      mode: row.mode as SavedConnection['mode'], address: String(row.address), name: String(row.name),
      remembered: row.mode === 'remote' && this.hasSecret('remote:' + String(row.address), 'token'),
    }))
  }

  remoteToken(url: string) { return this.secret('remote:' + url, 'token') }

  async recordConnection(mode: SavedConnection['mode'], address: string, name?: string, remember?: { token: string; save: boolean }): Promise<boolean> {
    const scope = 'remote:' + address
    const protectedToken = remember?.save ? await this.protect(remember.token) : null
    this.transaction(() => {
      this.database.prepare(`
        INSERT INTO connections(mode,address,name,touched) VALUES(?,?,coalesce(?,?),?)
        ON CONFLICT(mode,address) DO UPDATE SET name = coalesce(?,name), touched = excluded.touched
      `).run(mode, address, name ?? null, address, Date.now(), name ?? null)
      if (remember) this.writeSecret(scope, 'token', protectedToken)
    })
    if (remember) this.updateMemory(scope, 'token', protectedToken)
    return false
  }

  async saveRemoteConnection(address: string, name?: string, token?: string, rememberToken = true): Promise<void> {
    const normalized = remoteAddress(address)
    const trimmedName = typeof name === 'string' && name.trim() ? name.trim().slice(0, 120) : normalized
    const remember = typeof token === 'string' ? { token, save: rememberToken && token.length > 0 } : undefined
    await this.recordConnection('remote', normalized, trimmedName, remember)
  }

  async providerCredentials(scope: string, providerName: string): Promise<{ protocol: string; baseURL: string; apiKey: string }> {
    const cfg = scope ? await this.effectiveConfig(scope) : await this.resolvedConfig('', this.document(''))
    const models = cfg.models as { providers?: Record<string, { protocol?: string; baseURL?: string; apiKey?: string }> } | undefined
    const entry = models?.providers?.[providerName]
    if (!entry) throw new Error(`Provider "${providerName}" is not saved yet; save it first.`)
    if (!entry.baseURL) throw new Error(`Provider "${providerName}" has no baseURL.`)
    return {
      protocol: typeof entry.protocol === 'string' ? entry.protocol : 'responses',
      baseURL: entry.baseURL,
      apiKey: typeof entry.apiKey === 'string' ? entry.apiKey : '',
    }
  }

  forgetConnection(mode: SavedConnection['mode'], address: string) {
    this.transaction(() => {
      this.database.prepare('DELETE FROM connections WHERE mode = ? AND address = ?').run(mode, address)
      if (mode === 'remote') this.writeSecret('remote:' + address, 'token', null)
    })
    if (mode === 'remote') this.updateMemory('remote:' + address, 'token', null)
  }

  lastSession(mode: string, address: string): string | undefined {
    return this.database.prepare('SELECT session FROM connections WHERE mode = ? AND address = ?').get(mode, address)?.session as string | undefined
  }

  rememberSession(mode: string, address: string, session: string) {
    this.database.prepare('UPDATE connections SET session = ? WHERE mode = ? AND address = ?').run(session, mode, address)
  }

  workspacePermissionMode(workspace: string): string {
    const mode = this.preference('workspace.permission:' + workspace)
    return mode === 'read-only' || mode === 'workspace' || mode === 'auto' || mode === 'full-access' ? mode : 'workspace'
  }

  saveWorkspacePermissionMode(workspace: string, mode: string) {
    if (mode !== 'read-only' && mode !== 'workspace' && mode !== 'auto' && mode !== 'full-access') {
      throw new Error(`Invalid permission mode: ${mode}`)
    }
    this.setPreference('workspace.permission:' + workspace, mode)
  }
}

function summarizeMCPServers(rawJSON: string): Record<string, MCPServerSummary> | undefined {
  let parsed: Record<string, Json>
  try { parsed = asObject(JSON.parse(rawJSON)) as Record<string, Json> } catch { return undefined }
  const out: Record<string, MCPServerSummary> = Object.create(null)
  for (const [name, rawEntry] of Object.entries(parsed)) {
    if (!rawEntry || typeof rawEntry !== 'object' || Array.isArray(rawEntry)) continue
    const entry = rawEntry
    const transport = entry.transport === 'streamable-http' ? 'streamable-http' : 'stdio'
    const summary: MCPServerSummary = { transport }
    if (typeof entry.command === 'string' && entry.command) summary.command = entry.command
    if (Array.isArray(entry.args)) {
      const args = entry.args.filter((a): a is string => typeof a === 'string')
      if (args.length) summary.args = args
    }
    if (entry.env && typeof entry.env === 'object' && !Array.isArray(entry.env)) {
      const envKeys = Object.keys(entry.env as Record<string, unknown>).sort()
      if (envKeys.length) summary.envKeys = envKeys
    }
    if (typeof entry.url === 'string' && entry.url) summary.url = entry.url
    if (typeof entry.clientMetadataURL === 'string' && entry.clientMetadataURL) summary.clientMetadataURL = entry.clientMetadataURL
    out[name] = summary
  }
  return Object.keys(out).length ? out : undefined
}

function mergeMCPServers(incoming: Json, existingJSON?: string): Json {
  let oldServers: Record<string, Json> = {}
  if (existingJSON) {
    try { oldServers = asObject(JSON.parse(existingJSON)) as Record<string, Json> } catch { oldServers = {} }
  }
  const out: Json = Object.create(null)
  for (const [name, rawEntry] of Object.entries(incoming)) {
    const entry = asObject(rawEntry)
    checkFields(entry, ['transport', 'command', 'args', 'env', 'keepEnvKeys', 'url', 'clientMetadataURL'], `mcpServers[${JSON.stringify(name)}]`)
    const clean: Json = { ...entry }
    const keepKeys = Array.isArray(clean.keepEnvKeys) ? clean.keepEnvKeys.filter((k): k is string => typeof k === 'string') : []
    delete clean.keepEnvKeys
    const mergedEnv: Record<string, string> = Object.create(null)
    const oldEntry = oldServers[name]
    if (oldEntry && typeof oldEntry === 'object' && !Array.isArray(oldEntry) && oldEntry.env && typeof oldEntry.env === 'object' && !Array.isArray(oldEntry.env)) {
      const oldEnv = oldEntry.env as Record<string, unknown>
      for (const key of keepKeys) {
        if (typeof oldEnv[key] === 'string') mergedEnv[key] = oldEnv[key] as string
      }
    }
    if (clean.env !== undefined) {
      const newEnv = asObject(clean.env)
      for (const [k, v] of Object.entries(newEnv)) {
        if (typeof v !== 'string') throw new Error(`mcpServers[${JSON.stringify(name)}].env[${JSON.stringify(k)}] must be a string.`)
        mergedEnv[k] = v
      }
    }
    delete clean.env
    if (Object.keys(mergedEnv).length > 0) clean.env = mergedEnv
    out[name] = clean
  }
  return out
}

function splitImportedConfig(document: ConfigDocument) {
  const cfg = catalog(document)
  const suppliedKeys: string[] = []
  const unresolvedKeys: string[] = []
  const providers = Object.fromEntries(Object.entries(cfg.providers).map(([name, provider]) => {
    const { apiKey, apiKeyKeyringRef, ...publicFields } = provider
    if (apiKeyKeyringRef && !apiKey && !publicFields.apiKeyEnv) unresolvedKeys.push(name)
    if (apiKey) suppliedKeys.push(name)
    return [name, publicFields]
  }))
  const { mcpServers: _, ...publicDocument } = configWithProviders(document, cfg, providers)
  return { publicDocument, suppliedKeys, unresolvedKeys }
}
