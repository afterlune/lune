import { app, safeStorage } from 'electron'
import { randomBytes } from 'node:crypto'
import { chmodSync, readFileSync, realpathSync, statSync } from 'node:fs'
import { basename, isAbsolute, join } from 'node:path'
import { DatabaseSync } from 'node:sqlite'
import type { ConfigDocument, ConfigImport, ConfigView, MCPServerSummary, SavedConnection, WorkspaceRecord, WorkspaceSessionRecord } from '../shared/desktop-settings.js'
import { defaultUiPreferences, normalizeUiPreferences, type UiPreferences } from '../shared/ui-preferences.js'

const BASE32_ALPHABET = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ234567'
function newWorkspaceId(): string {
  const bytes = randomBytes(20)
  let out = ''
  for (let i = 0; i < 26; i++) {
    out += BASE32_ALPHABET[bytes[i % bytes.length] & 31]
  }
  return out
}

function parseTimestampMs(raw: unknown, fallback: number): number {
  if (typeof raw === 'number' && Number.isFinite(raw) && raw > 0) return Math.round(raw)
  if (typeof raw === 'string' && raw.trim()) {
    const parsed = Date.parse(raw)
    if (!Number.isNaN(parsed) && parsed > 0) return parsed
  }
  return fallback
}

export const startupConfigLimit = 1024 * 1024
type Json = Record<string, unknown>
// A secret is either persisted as safeStorage ciphertext or kept in memory for this
// run only. Plaintext never reaches the settings database.
type Secret = { text: string; encrypted?: Buffer }
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
      this.database.exec('PRAGMA busy_timeout = 5000; PRAGMA foreign_keys = ON;')
      const version = this.database.prepare('PRAGMA user_version').get()?.user_version
      const workspaceSchema = `
        CREATE TABLE IF NOT EXISTS workspaces (
          id TEXT PRIMARY KEY,
          path TEXT NOT NULL UNIQUE,
          name TEXT NOT NULL,
          is_open INTEGER NOT NULL DEFAULT 1,
          last_session_id TEXT NOT NULL DEFAULT '',
          created_at INTEGER NOT NULL,
          updated_at INTEGER NOT NULL,
          last_opened_at INTEGER NOT NULL
        );
        CREATE INDEX IF NOT EXISTS idx_workspaces_open_time
          ON workspaces(is_open, last_opened_at DESC, created_at DESC);
        CREATE TABLE IF NOT EXISTS workspace_sessions (
          workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
          session_id TEXT NOT NULL,
          title TEXT NOT NULL DEFAULT '',
          archived INTEGER NOT NULL DEFAULT 0,
          created_at INTEGER NOT NULL,
          updated_at INTEGER NOT NULL,
          last_opened_at INTEGER NOT NULL DEFAULT 0,
          PRIMARY KEY(workspace_id, session_id)
        );
        CREATE INDEX IF NOT EXISTS idx_workspace_sessions_ws_time
          ON workspace_sessions(workspace_id, updated_at DESC, created_at DESC);
      `
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
            ${workspaceSchema}
            PRAGMA user_version = 3;
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
          this.database.exec(`
            ALTER TABLE secrets ADD COLUMN encoding TEXT NOT NULL DEFAULT 'safeStorage';
            ${workspaceSchema}
            PRAGMA user_version = 3;
          `)
          this.migrateLocalConnectionsToWorkspaces()
        })
      } else if (version === 2) {
        this.transaction(() => {
          this.database.exec(`
            ${workspaceSchema}
            PRAGMA user_version = 3;
          `)
          this.migrateLocalConnectionsToWorkspaces()
        })
      } else if (version !== 3) {
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
    // 'plain' rows are legacy: older builds wrote plaintext when no credential store
    // existed. They stay readable, but nothing writes plaintext any more.
    if (saved.encoding === 'plain') return buf.toString('utf8')
    if (!canEncrypt()) throw new Error('The system credential store is unavailable. Unlock it or enter the credential for this run.')
    try {
      const result = await safeStorage.decryptStringAsync(buf)
      return result.result
    } catch { throw new Error('A saved credential could not be unlocked. Re-enter it in settings.') }
  }

  private async protect(text: string): Promise<Secret> {
    if (!canEncrypt()) return { text }
    try {
      return { text, encrypted: await safeStorage.encryptStringAsync(text) }
    } catch {
      throw new Error('The system credential store rejected the credential. Re-enter it, or configure an API key environment variable.')
    }
  }

  private writeSecret(scope: string, name: string, value: Secret | null) {
    // Values kept for this run only have no ciphertext, so any stored copy is dropped.
    if (value?.encrypted) {
      this.database.prepare('INSERT INTO secrets(scope, name, encoding, encrypted) VALUES(?,?,?,?) ON CONFLICT(scope,name) DO UPDATE SET encoding = excluded.encoding, encrypted = excluded.encrypted')
        .run(scope, name, 'safeStorage', value.encrypted)
    } else {
      this.database.prepare('DELETE FROM secrets WHERE scope = ? AND name = ?').run(scope, name)
    }
  }

  private updateMemory(scope: string, name: string, value: Secret | null) {
    if (value) this.runSecrets.set(this.secretKey(scope, name), value.text)
    else this.runSecrets.delete(this.secretKey(scope, name))
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
      secretStorage: canEncrypt() ? 'encrypted' : 'memory',
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
    if (mode === 'local') {
      const ws = this.database.prepare('SELECT last_session_id FROM workspaces WHERE path = ?').get(address) as { last_session_id?: string } | undefined
      if (ws?.last_session_id) return ws.last_session_id
    }
    return this.database.prepare('SELECT session FROM connections WHERE mode = ? AND address = ?').get(mode, address)?.session as string | undefined
  }

  rememberSession(mode: string, address: string, session: string) {
    this.database.prepare('UPDATE connections SET session = ? WHERE mode = ? AND address = ?').run(session, mode, address)
    if (mode === 'local') {
      const now = Date.now()
      this.transaction(() => {
        const ws = this.database.prepare('SELECT id FROM workspaces WHERE path = ?').get(address) as { id?: string } | undefined
        if (!ws?.id) return
        this.database.prepare('UPDATE workspaces SET last_session_id = ?, updated_at = ? WHERE id = ?').run(session, now, ws.id)
        this.database.prepare('UPDATE workspace_sessions SET last_opened_at = ? WHERE workspace_id = ? AND session_id = ?').run(now, ws.id, session)
      })
    }
  }

  private migrateLocalConnectionsToWorkspaces() {
    const countRow = this.database.prepare('SELECT COUNT(*) AS cnt FROM workspaces').get() as { cnt?: number } | undefined
    if ((countRow?.cnt ?? 0) > 0) return
    const rows = this.database.prepare("SELECT address, name, touched, coalesce(session, '') AS session FROM connections WHERE mode = 'local' ORDER BY touched DESC").all() as Array<{
      address: string; name: string; touched: number; session: string
    }>
    for (const row of rows) {
      let resolved = row.address
      try {
        resolved = realpathSync.native(row.address)
        if (!statSync(resolved).isDirectory()) continue
      } catch {
        continue
      }
      const ts = typeof row.touched === 'number' && row.touched > 0 ? row.touched : Date.now()
      const displayName = row.name?.trim() || basename(resolved) || resolved
      this.database.prepare(`
        INSERT INTO workspaces(id, path, name, is_open, last_session_id, created_at, updated_at, last_opened_at)
        VALUES(?, ?, ?, 1, ?, ?, ?, ?)
        ON CONFLICT(path) DO NOTHING
      `).run(newWorkspaceId(), resolved, displayName, row.session || '', ts, ts, ts)
    }
  }

  ensureWorkspace(rawPath: string, touch = true): WorkspaceRecord {
    const resolved = workspacePath(rawPath)
    const now = Date.now()
    const name = basename(resolved) || resolved
    let out!: WorkspaceRecord
    this.transaction(() => {
      const existing = this.database.prepare(`
        SELECT id, path, name, is_open, last_session_id, created_at, updated_at, last_opened_at
        FROM workspaces WHERE path = ?
      `).get(resolved) as {
        id: string; path: string; name: string; is_open: number; last_session_id: string
        created_at: number; updated_at: number; last_opened_at: number
      } | undefined
      if (!existing) {
        const id = newWorkspaceId()
        this.database.prepare(`
          INSERT INTO workspaces(id, path, name, is_open, last_session_id, created_at, updated_at, last_opened_at)
          VALUES(?, ?, ?, 1, '', ?, ?, ?)
        `).run(id, resolved, name, now, now, now)
        out = { id, path: resolved, name, isOpen: true, lastSessionId: '', createdAt: now, updatedAt: now, lastOpenedAt: now, sessions: [] }
        return
      }
      if (touch) {
        this.database.prepare('UPDATE workspaces SET is_open = 1, name = ?, updated_at = ?, last_opened_at = ? WHERE id = ?').run(name, now, now, existing.id)
      } else if (!existing.is_open) {
        this.database.prepare('UPDATE workspaces SET is_open = 1, name = ? WHERE id = ?').run(name, existing.id)
      }
      out = {
        id: existing.id,
        path: existing.path,
        name,
        isOpen: true,
        lastSessionId: existing.last_session_id,
        createdAt: existing.created_at,
        updatedAt: touch ? now : existing.updated_at,
        lastOpenedAt: touch ? now : existing.last_opened_at,
      }
    })
    return out
  }

  selectWorkspace(id: string) {
    const now = Date.now()
    this.database.prepare('UPDATE workspaces SET is_open = 1, last_opened_at = ?, updated_at = ? WHERE id = ?').run(now, now, id)
  }

  closeWorkspace(id: string) {
    this.database.prepare('UPDATE workspaces SET is_open = 0, updated_at = ? WHERE id = ?').run(Date.now(), id)
  }

  listWorkspacesWithSessions(openOnly = true): WorkspaceRecord[] {
    const query = `
      SELECT id, path, name, is_open, last_session_id, created_at, updated_at, last_opened_at
      FROM workspaces
      ${openOnly ? 'WHERE is_open = 1' : ''}
      ORDER BY last_opened_at DESC, created_at DESC, rowid DESC
    `
    const rows = this.database.prepare(query).all() as Array<{
      id: string; path: string; name: string; is_open: number; last_session_id: string
      created_at: number; updated_at: number; last_opened_at: number
    }>
    const sessionStmt = this.database.prepare(`
      SELECT session_id, workspace_id, title, archived, created_at, updated_at, last_opened_at
      FROM workspace_sessions
      WHERE workspace_id = ?
      ORDER BY updated_at DESC, created_at DESC
    `)
    return rows.map((row) => {
      const sessRows = sessionStmt.all(row.id) as Array<{
        session_id: string; workspace_id: string; title: string; archived: number
        created_at: number; updated_at: number; last_opened_at: number
      }>
      const sessions: WorkspaceSessionRecord[] = sessRows.map((s) => ({
        sessionId: s.session_id,
        workspaceId: s.workspace_id,
        title: s.title,
        archived: s.archived !== 0,
        createdAt: s.created_at,
        updatedAt: s.updated_at,
        lastOpenedAt: s.last_opened_at,
      }))
      return {
        id: row.id,
        path: row.path,
        name: row.name,
        isOpen: row.is_open !== 0,
        lastSessionId: row.last_session_id,
        createdAt: row.created_at,
        updatedAt: row.updated_at,
        lastOpenedAt: row.last_opened_at,
        sessions,
      }
    })
  }

  upsertWorkspaceSessions(
    workspacePathOrID: string,
    sessions: Array<{ id?: unknown; title?: unknown; archived?: unknown; updatedAt?: unknown; createdAt?: unknown }>,
    pruneMissing = false,
  ) {
    if (!sessions.length && !pruneMissing) return
    const now = Date.now()
    this.transaction(() => {
      const row = this.database.prepare('SELECT id FROM workspaces WHERE id = ? OR path = ?').get(workspacePathOrID, workspacePathOrID) as { id?: string } | undefined
      if (!row?.id) return
      const wsId = row.id
      const keep = new Set<string>()
      let maxUpdated = 0
      const upsert = this.database.prepare(`
        INSERT INTO workspace_sessions(workspace_id, session_id, title, archived, created_at, updated_at, last_opened_at)
        VALUES(?, ?, ?, ?, ?, ?, 0)
        ON CONFLICT(workspace_id, session_id) DO UPDATE SET
          title = excluded.title,
          archived = excluded.archived,
          updated_at = excluded.updated_at,
          created_at = CASE
            WHEN workspace_sessions.created_at > 0 AND workspace_sessions.created_at < excluded.created_at THEN workspace_sessions.created_at
            ELSE excluded.created_at
          END
      `)
      for (const s of sessions) {
        const sid = typeof s.id === 'string' ? s.id.trim() : ''
        if (!sid) continue
        keep.add(sid)
        const title = typeof s.title === 'string' ? s.title : ''
        const archived = s.archived ? 1 : 0
        const updatedAt = parseTimestampMs(s.updatedAt, now)
        const createdAt = parseTimestampMs(s.createdAt, updatedAt)
        if (updatedAt > maxUpdated) maxUpdated = updatedAt
        upsert.run(wsId, sid, title, archived, createdAt, updatedAt)
      }
      if (pruneMissing) {
        const existing = this.database.prepare('SELECT session_id FROM workspace_sessions WHERE workspace_id = ?').all(wsId) as Array<{ session_id: string }>
        for (const item of existing) {
          if (!keep.has(item.session_id)) {
            this.database.prepare('DELETE FROM workspace_sessions WHERE workspace_id = ? AND session_id = ?').run(wsId, item.session_id)
          }
        }
      }
      if (maxUpdated > 0) {
        this.database.prepare('UPDATE workspaces SET updated_at = MAX(updated_at, ?) WHERE id = ?').run(maxUpdated, wsId)
      }
    })
  }

  updateWorkspaceSessionMeta(workspacePathOrID: string, sessionId: string, patch: { title?: string; archived?: boolean }) {
    const row = this.database.prepare('SELECT id FROM workspaces WHERE id = ? OR path = ?').get(workspacePathOrID, workspacePathOrID) as { id?: string } | undefined
    if (!row?.id) return
    const now = Date.now()
    if (patch.title !== undefined) {
      this.database.prepare('UPDATE workspace_sessions SET title = ?, updated_at = ? WHERE workspace_id = ? AND session_id = ?').run(patch.title, now, row.id, sessionId)
    }
    if (patch.archived !== undefined) {
      this.database.prepare('UPDATE workspace_sessions SET archived = ?, updated_at = ? WHERE workspace_id = ? AND session_id = ?').run(patch.archived ? 1 : 0, now, row.id, sessionId)
    }
  }

  deleteWorkspaceSession(workspacePathOrID: string, sessionId: string) {
    this.transaction(() => {
      const row = this.database.prepare('SELECT id, last_session_id FROM workspaces WHERE id = ? OR path = ?').get(workspacePathOrID, workspacePathOrID) as { id?: string; last_session_id?: string } | undefined
      if (!row?.id) return
      this.database.prepare('DELETE FROM workspace_sessions WHERE workspace_id = ? AND session_id = ?').run(row.id, sessionId)
      if (row.last_session_id === sessionId) {
        this.database.prepare("UPDATE workspaces SET last_session_id = '' WHERE id = ?").run(row.id)
      }
    })
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
