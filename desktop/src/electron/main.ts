import { app, BrowserWindow, desktopCapturer, dialog, globalShortcut, ipcMain, nativeTheme } from 'electron'
import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process'
import { constants, existsSync } from 'node:fs'
import { open, writeFile } from 'node:fs/promises'
import { StringDecoder } from 'node:string_decoder'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import WebSocket from 'ws'
import { JsonRpcPeer, type JsonRpcNotification } from '../shared/jsonrpc.js'
import { isThemePreference, windowColors } from '../shared/theme.js'
import type { ConfigDocument, DesktopConnectionOptions, ProviderTestInput, ProviderTestResult } from '../shared/desktop-settings.js'
import { configFrame, DesktopSettings, remoteAddress, startupConfigLimit, workspacePath } from './settings.js'

const currentDirectory = dirname(fileURLToPath(import.meta.url))
const developmentServer = process.env.ELECTRON_RENDERER_URL
const rendererFileURL = pathToFileURL(resolve(currentDirectory, '../renderer/index.html')).toString()
const allowedRuntimeMethods = new Set([
  'runtime.hello', 'runtime.subscribe', 'runtime.unsubscribe', 'runtime.state', 'runtime.pending',
  'workspace.info', 'workspace.listDirectory', 'workspace.readFile',
  'session.create', 'session.get', 'session.history', 'session.historyPage', 'session.list', 'session.search',
  'session.setSettings', 'session.setTitle', 'session.setArchived', 'session.delete', 'session.fork',
  'session.rewindPreview', 'session.rewind', 'session.compact',
  'goal.get', 'goal.create', 'goal.pause', 'goal.resume', 'goal.edit', 'goal.setBudget', 'goal.clear',
  'command.list', 'command.read', 'command.stop',
  'mcp.servers', 'mcp.enable', 'mcp.disable', 'mcp.listPrompts', 'mcp.getPrompt',
  'mcp.listResources', 'mcp.listResourceTemplates', 'mcp.readResource',
  'model.list', 'turn.submit', 'turn.interrupt', 'turn.recover', 'turn.discardPaused',
  'turn.cancelQueued', 'turn.cancelPending', 'turn.replaceQueued', 'turn.implementPlan',
  'approval.resolve', 'question.resolve', 'question.saveDraft', 'interaction.resolve', 'toolRecovery.resolve',
])
let mainWindow: BrowserWindow | null = null
let peer: JsonRpcPeer | null = null
let socket: WebSocket | null = null
let coreProcess: ChildProcessWithoutNullStreams | null = null
let coreExit: Promise<void> | null = null
let lastConnection: DesktopConnectionOptions | null = null
let quitting = false
let settings: DesktopSettings
let connectionNotice = ''
let settingsWrite = Promise.resolve()
let runtimeChange = Promise.resolve()

function serial<T>(task: Promise<unknown>, work: () => Promise<T>, setPending: (pending: Promise<void>) => void): Promise<T> {
  const result = task.then(work)
  setPending(result.then(() => undefined, () => undefined))
  return result
}
const defaultShortcut = 'CommandOrControl+Shift+Space'
let quickShortcut = defaultShortcut
let expandedWindow: { bounds: Electron.Rectangle; maximized: boolean } | null = null

function toggleQuickWindow(): void {
  if (!mainWindow || mainWindow.isDestroyed()) createMainWindow()
  const window = mainWindow!
  if (window.isMinimized()) window.restore()
  if (expandedWindow) {
    const { bounds, maximized } = expandedWindow
    expandedWindow = null
    window.setAlwaysOnTop(false)
    window.setMinimumSize(980, 680)
    window.setBounds(bounds)
    if (maximized) window.maximize()
  } else {
    expandedWindow = { bounds: window.getNormalBounds(), maximized: window.isMaximized() }
    if (window.isMaximized()) window.unmaximize()
    window.setMinimumSize(440, 440)
    window.setSize(680, 600)
    window.center()
    window.setAlwaysOnTop(true)
  }
  window.show()
  window.focus()
  window.webContents.send('desktop:quick-mode', Boolean(expandedWindow))
}

function registerQuickShortcut(accelerator: string): boolean {
  try { return globalShortcut.register(accelerator, toggleQuickWindow) } catch { return false }
}

function sendNotification(notification: JsonRpcNotification): void {
  if (mainWindow && !mainWindow.isDestroyed()) mainWindow.webContents.send('runtime:notification', notification)
}

function assertMainWindow(event: Electron.IpcMainInvokeEvent): void {
  if (!mainWindow || event.sender !== mainWindow.webContents || event.senderFrame !== mainWindow.webContents.mainFrame) throw new Error('Untrusted IPC sender')
}

function coreExecutable(): string {
  const suffix = process.platform === 'win32' ? '.exe' : ''
  const candidates = [
    process.env.LUNE_CORE_BIN,
    join(process.resourcesPath, 'bin', `lune-core${suffix}`),
    resolve(currentDirectory, '../../resources/bin', process.platform === 'win32' ? 'win32' : 'linux', `lune-core${suffix}`),
  ].filter((value): value is string => Boolean(value))
  const binary = candidates.find(existsSync)
  if (!binary) throw new Error('lune-core was not found. Build the desktop app with `npm run build:core`.')
  return binary
}

function sandboxExecutable(): string {
  const suffix = process.platform === 'win32' ? '.exe' : ''
  return join(dirname(coreExecutable()), `lune-sandbox${suffix}`)
}

function attachPeer(nextPeer: JsonRpcPeer): void {
  peer = nextPeer
  nextPeer.onNotification((notification) => {
    if (peer === nextPeer) sendNotification(notification)
  })
}

async function disconnectRuntime(): Promise<void> {
  const activePeer = peer
  peer = null
  activePeer?.fail(new Error('Connection closed'))

  const activeSocket = socket
  socket = null
  if (activeSocket && activeSocket.readyState < WebSocket.CLOSING) activeSocket.close(1000, 'Application disconnected')

  const activeProcess = coreProcess
  const activeExit = coreExit
  coreProcess = null
  coreExit = null
  if (activeProcess && activeExit) {
    activeProcess.stdin.end()
    const exited = await Promise.race([
      activeExit.then(() => true),
      new Promise<boolean>((resolveTimeout) => setTimeout(() => resolveTimeout(false), 12_000)),
    ])
    if (!exited && activeProcess.exitCode === null) activeProcess.kill()
  }
}

function nativeBackground(): string {
  return nativeTheme.shouldUseDarkColors ? windowColors.dark : windowColors.light
}

nativeTheme.on('updated', () => mainWindow?.setBackgroundColor(nativeBackground()))

ipcMain.handle('desktop:get-theme', (event) => {
  assertMainWindow(event)
  return nativeTheme.themeSource
})

ipcMain.handle('desktop:set-theme', (event, preference: unknown) => {
  assertMainWindow(event)
  if (!isThemePreference(preference)) throw new Error('Invalid appearance preference.')
  if (preference === nativeTheme.themeSource) return
  settings.setPreference('theme', preference)
  nativeTheme.themeSource = preference
  mainWindow?.setBackgroundColor(nativeBackground())
})

function createMainWindow(): void {
  mainWindow = new BrowserWindow({
    width: 1440,
    height: 960,
    minWidth: 980,
    minHeight: 680,
    title: 'Lune',
    backgroundColor: nativeBackground(),
    webPreferences: {
      preload: join(currentDirectory, '../preload/preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  })

  mainWindow.webContents.setWindowOpenHandler(() => ({ action: 'deny' }))
  mainWindow.webContents.on('will-navigate', (event, targetURL) => {
    if (developmentServer) {
      try {
        if (new URL(targetURL).origin === new URL(developmentServer).origin) return
      } catch { /* block malformed navigation URLs */ }
    }
    if (targetURL === rendererFileURL || targetURL.startsWith(`${rendererFileURL}#`)) return
    event.preventDefault()
  })

  if (developmentServer) void mainWindow.loadURL(developmentServer)
  else void mainWindow.loadFile(join(currentDirectory, '../renderer/index.html'))
  mainWindow.on('closed', () => { mainWindow = null; expandedWindow = null })
}

ipcMain.handle('desktop:select-workspace', async (event) => {
  assertMainWindow(event)
  const selection = await dialog.showOpenDialog(mainWindow!, { properties: ['openDirectory', 'createDirectory'] })
  return selection.canceled ? null : selection.filePaths[0] ?? null
})

ipcMain.handle('desktop:quick-mode', (event) => {
  assertMainWindow(event)
  return Boolean(expandedWindow)
})

ipcMain.handle('desktop:quick-toggle', (event) => {
  assertMainWindow(event)
  toggleQuickWindow()
})

ipcMain.handle('desktop:shortcut', (event) => {
  assertMainWindow(event)
  if (!globalShortcut.isRegistered(quickShortcut)) throw new Error('The quick window shortcut is unavailable. Choose another in Connection settings.')
  return quickShortcut
})

ipcMain.handle('desktop:set-shortcut', (event, value: unknown) => {
  assertMainWindow(event)
  const accelerator = typeof value === 'string' ? value.trim() : ''
  if (!accelerator || accelerator.length > 100) throw new Error('Enter an Electron shortcut such as Control+Alt+Space.')
  if (accelerator === quickShortcut && globalShortcut.isRegistered(quickShortcut)) return quickShortcut
  if (!registerQuickShortcut(accelerator)) throw new Error('This shortcut is invalid or already in use.')
  try { settings.setPreference('shortcut', accelerator) }
  catch (error) { globalShortcut.unregister(accelerator); throw error }
  globalShortcut.unregister(quickShortcut)
  quickShortcut = accelerator
  return quickShortcut
})

ipcMain.handle('desktop:save-markdown', async (event, markdown: unknown) => {
  assertMainWindow(event)
  if (typeof markdown !== 'string') throw new Error('Conversation export must be Markdown text.')
  const result = await dialog.showSaveDialog(mainWindow!, {
    defaultPath: 'lune-conversation.md', filters: [{ name: 'Markdown', extensions: ['md'] }],
  })
  if (result.filePath && !result.canceled) await writeFile(result.filePath, markdown, 'utf8')
})

ipcMain.handle('desktop:screenshot', async (event) => {
  assertMainWindow(event)
  const sources = (await desktopCapturer.getSources({ types: ['screen'], thumbnailSize: { width: 1600, height: 1200 } }))
    .filter((source) => !source.thumbnail.isEmpty())
  if (!sources.length) throw new Error('No display is available for capture. Check your system screen capture permissions.')
  const result = await dialog.showMessageBox(mainWindow!, {
    title: 'Attach a screenshot', message: 'Choose a display to attach to the draft',
    buttons: ['Cancel', ...sources.map((source) => source.name)], cancelId: 0, defaultId: 0, noLink: true,
  })
  const selected = sources[result.response - 1]
  if (!selected) return null
  const data = selected.thumbnail.toJPEG(82)
  return { name: 'screenshot.jpg', mediaType: 'image/jpeg', data: data.toString('base64'), size: data.length }
})

function configSecrets(config: ConfigDocument): string[] {
  const models = config.models as { providers?: Record<string, { apiKey?: string }> } | undefined
  const values: unknown[] = Object.values(models?.providers ?? {}).map((provider) => provider?.apiKey)
  const servers = config.mcpServers as Record<string, { env?: Record<string, string>; args?: string[]; url?: string }> | undefined
  for (const server of Object.values(servers ?? {})) {
    if (!server || typeof server !== 'object') continue
    if (server.env && typeof server.env === 'object') values.push(...Object.values(server.env))
    if (Array.isArray(server.args)) values.push(...server.args)
    values.push(server.url)
  }
  const secrets = values.filter((value): value is string => typeof value === 'string' && value.length > 0)
  return [...new Set(secrets)].sort((a, b) => b.length - a.length)
}

function redact(text: string, credentials: string[]): string {
  for (const credential of credentials) text = text.replaceAll(credential, '[redacted]')
  return text
}

function validateCoreConfig(config: ConfigDocument): Promise<void> {
  const frame = configFrame(config)
  const child = spawn(coreExecutable(), ['validate-config'], { stdio: ['pipe', 'ignore', 'pipe'], windowsHide: true })
  return new Promise((resolve, reject) => {
    let diagnostics = ''
    child.stderr.setEncoding('utf8').on('data', (part: string) => { if (diagnostics.length < 8192) diagnostics += part })
    const deadline = setTimeout(() => {
      child.kill()
      reject(new Error('Core configuration validation timed out.'))
    }, 15_000)
    child.once('error', () => {
      clearTimeout(deadline)
      reject(new Error('Could not start Core to validate the configuration.'))
    })
    child.once('close', (code) => {
      clearTimeout(deadline)
      if (code === 0) resolve()
      else reject(new Error(redact(diagnostics.trim(), configSecrets(config)) ||
        'Core rejected the configuration. Make sure the bundled Core supports Electron configuration.'))
    })
    child.stdin.on('error', () => { /* exit status supplies the validation error */ })
    child.stdin.end(frame)
  })
}

function configScope(workspace: unknown) { return workspace === '' ? '' : workspacePath(workspace) }

ipcMain.handle('settings:config', (event, scope: unknown) => {
  assertMainWindow(event)
  return settings.config(configScope(scope))
})

ipcMain.handle('settings:ui-preferences', (event) => {
  assertMainWindow(event)
  return settings.uiPreferences()
})

ipcMain.handle('settings:save-ui-preferences', (event, patch: unknown) => {
  assertMainWindow(event)
  return settings.saveUiPreferences(patch)
})

ipcMain.handle('settings:import', async (event) => {
  assertMainWindow(event)
  const result = await dialog.showOpenDialog(mainWindow!, { properties: ['openFile'], filters: [{ name: 'Core configuration', extensions: ['json'] }] })
  if (result.canceled || !result.filePaths[0]) return null
  const flags = process.platform === 'linux' ? constants.O_RDONLY | constants.O_NONBLOCK : constants.O_RDONLY
  const file = await open(result.filePaths[0], flags)
  try {
    const info = await file.stat()
    if (!info.isFile() || info.size > startupConfigLimit) throw new Error('Select a JSON file of at most 1 MiB.')
    const buffer = Buffer.alloc(startupConfigLimit + 1)
    let size = 0
    while (size < buffer.length) {
      const { bytesRead } = await file.read(buffer, size, buffer.length - size, null)
      if (!bytesRead) break
      size += bytesRead
    }
    if (size > startupConfigLimit) throw new Error('The configuration file exceeds 1 MiB.')
    return settings.prepareImport(buffer.toString('utf8', 0, size))
  } finally { await file.close() }
})

ipcMain.handle('settings:save-config', (event, scope: unknown, document: unknown, importId?: string) => {
  assertMainWindow(event)
  return serial(settingsWrite, () => settings.saveConfig(configScope(scope), document, importId, validateCoreConfig), (pending) => { settingsWrite = pending })
})

ipcMain.handle('settings:reset-workspace', (event, workspace: unknown) => {
  assertMainWindow(event)
  return serial(settingsWrite, () => settings.resetWorkspace(workspacePath(workspace), validateCoreConfig), (pending) => { settingsWrite = pending })
})

async function requestProviderModels(protocol: string, baseURL: string, apiKey: string) {
  if (protocol !== 'responses' && protocol !== 'anthropic-messages') throw new Error('Choose a supported provider protocol.')
  if (baseURL.length > 2048) throw new Error('Provider URL is too long.')
  let parsedBase: URL
  try { parsedBase = new URL(baseURL) } catch { throw new Error('Enter a valid provider URL.') }
  if (!['http:', 'https:'].includes(parsedBase.protocol) || parsedBase.username || parsedBase.password || parsedBase.hash) throw new Error('Use an HTTP or HTTPS provider URL without embedded credentials or fragments.')
  const base = baseURL.replace(/\/+$/, '')
  const urls = protocol === 'anthropic-messages'
    ? [base.endsWith('/v1') ? `${base}/models` : `${base}/v1/models`]
    : [`${base}/models`, ...(base.endsWith('/v1') ? [] : [`${base}/v1/models`])]
  let lastError = 'Provider connection failed. Check the URL and credentials.'
  for (let i = 0; i < urls.length; i++) {
    try {
      const headers: Record<string, string> = { Accept: 'application/json' }
      if (protocol === 'anthropic-messages') {
        headers['anthropic-version'] = '2023-06-01'
        if (apiKey) headers['x-api-key'] = apiKey
      } else if (apiKey) headers.Authorization = `Bearer ${apiKey}`
      const res = await fetch(urls[i], { headers, signal: AbortSignal.timeout(12_000), redirect: 'error' })
      if (res.status === 404 && i + 1 < urls.length) { lastError = 'Provider returned HTTP 404.'; continue }
      if (!res.ok) throw new Error(`Provider returned HTTP ${res.status}.`)
      const declaredLength = Number(res.headers.get('content-length') || 0)
      if (declaredLength > 4 * 1024 * 1024) throw new Error('Provider model response is too large.')
      const body = await res.text()
      if (body.length > 4 * 1024 * 1024) throw new Error('Provider model response is too large.')
      let parsed: { data?: Array<{ id?: string; display_name?: string; name?: string }>; models?: Array<{ id?: string; displayName?: string; name?: string }> }
      try { parsed = JSON.parse(body) as typeof parsed } catch { throw new Error('Provider returned invalid model data.') }
      const map = new Map<string, { id: string; displayName?: string }>()
      for (const item of parsed.data ?? []) {
        const id = item.id?.trim()
        if (id) map.set(id, { id, displayName: item.display_name?.trim() || item.name?.trim() || undefined })
      }
      for (const item of parsed.models ?? []) {
        const id = (item.id || item.name)?.trim()
        if (id && !map.has(id)) map.set(id, { id, displayName: item.displayName?.trim() || item.name?.trim() || undefined })
      }
      return [...map.values()].sort((a, b) => a.id.localeCompare(b.id))
    } catch (error) {
      const message = error instanceof Error ? error.message : ''
      lastError = /^Provider (returned HTTP|model response)/.test(message) || message === 'Provider returned invalid model data.'
        ? message : 'Provider connection failed. Check the URL and credentials.'
    }
  }
  throw new Error(lastError)
}

ipcMain.handle('settings:fetch-provider-models', async (event, scope: unknown, provider: unknown) => {
  assertMainWindow(event)
  if (typeof provider !== 'string' || !provider.trim()) throw new Error('Provider name is required.')
  const normalizedScope = typeof scope === 'string' && scope ? configScope(scope) : ''
  const saved = await settings.providerCredentials(normalizedScope, provider.trim())
  return requestProviderModels(saved.protocol, saved.baseURL, saved.apiKey)
})

ipcMain.handle('settings:test-provider', async (event, scope: unknown, rawInput: unknown): Promise<ProviderTestResult> => {
  assertMainWindow(event)
  if (!rawInput || typeof rawInput !== 'object' || Array.isArray(rawInput)) throw new Error('Provider details are required.')
  const input = rawInput as Partial<ProviderTestInput>
  if (typeof input.provider !== 'string' || input.provider.length > 120 || typeof input.baseURL !== 'string' || input.baseURL.length > 2048) throw new Error('Enter a provider name and URL.')
  const normalizedScope = typeof scope === 'string' && scope ? configScope(scope) : ''
  let saved: { protocol: string; baseURL: string; apiKey: string } | undefined
  try { if (input.provider.trim()) saved = await settings.providerCredentials(normalizedScope, input.provider.trim()) } catch { /* a new provider can be tested before saving */ }
  const protocol = input.protocol === 'anthropic-messages' ? input.protocol : input.protocol === 'responses' ? input.protocol : saved?.protocol || 'responses'
  const baseURL = input.baseURL.trim() || saved?.baseURL || ''
  let apiKey = typeof input.apiKey === 'string' && input.apiKey ? input.apiKey : saved?.apiKey || ''
  if (input.apiKeyEnv) {
    if (!/^[A-Za-z_][A-Za-z0-9_]{0,127}$/.test(input.apiKeyEnv)) throw new Error('Enter a valid environment variable name.')
    const value = process.env[input.apiKeyEnv]
    if (!value) return { ok: false, durationMs: 0, error: 'The selected environment variable is not set.' }
    apiKey = value
  }
  const started = Date.now()
  try {
    const models = await requestProviderModels(protocol, baseURL, apiKey)
    return { ok: true, durationMs: Date.now() - started, modelCount: models.length }
  } catch (error) {
    return { ok: false, durationMs: Date.now() - started, error: error instanceof Error ? error.message : 'Provider connection failed.' }
  }
})

function connectionAddress(connection: DesktopConnectionOptions) {
  return connection.mode === 'local' ? connection.workspace : connection.url
}

ipcMain.handle('settings:connections', (event) => {
  assertMainWindow(event)
  return { items: settings.connections(), notice: connectionNotice }
})

ipcMain.handle('settings:save-remote', (event, input: { url?: unknown; name?: unknown; token?: unknown; rememberToken?: unknown }) => {
  assertMainWindow(event)
  if (typeof input?.url !== 'string') throw new Error('Enter a remote Core URL.')
  if (input.token !== undefined && typeof input.token !== 'string') throw new Error('Invalid token.')
  const name = typeof input.name === 'string' ? input.name : undefined
  const remember = typeof input.rememberToken === 'boolean' ? input.rememberToken : true
  return serial(settingsWrite, () => settings.saveRemoteConnection(input.url as string, name, input.token as string | undefined, remember), (pending) => { settingsWrite = pending })
})

ipcMain.handle('settings:forget-connection', async (event, mode: unknown, address: unknown) => {
  assertMainWindow(event)
  if (mode !== 'local' && mode !== 'remote') throw new Error('Invalid connection type.')
  if (typeof address !== 'string' || address.length > 4096) throw new Error('Invalid connection address.')
  const answer = await dialog.showMessageBox(mainWindow!, {
    type: 'question', message: 'Remove this saved connection?',
    detail: 'Workspace configuration and conversations will remain available.',
    buttons: ['Cancel', 'Remove'], cancelId: 0, defaultId: 0,
  })
  if (answer.response !== 1) return
  settings.forgetConnection(mode, address)
  if (lastConnection?.mode === mode && connectionAddress(lastConnection) === address) lastConnection = null
})

ipcMain.handle('settings:last-session', (event) => {
  assertMainWindow(event)
  return lastConnection ? settings.lastSession(lastConnection.mode, connectionAddress(lastConnection)) ?? null : null
})

ipcMain.handle('settings:remember-session', (event, mode: unknown, address: unknown, session: unknown) => {
  assertMainWindow(event)
  if (!peer || !lastConnection || mode !== lastConnection.mode || typeof session !== 'string' || !session || session.length > 160) return
  const normalized = mode === 'local' ? workspacePath(address) : remoteAddress(address)
  if (normalized !== connectionAddress(lastConnection)) return
  settings.rememberSession(lastConnection.mode, normalized, session)
})

async function openRuntime(options: DesktopConnectionOptions): Promise<unknown> {
  if (!options || (options.mode !== 'local' && options.mode !== 'remote')) throw new Error('Choose a valid connection type.')
  let frame: string | undefined
  let credentials: string[] = []
  let suppliedToken = ''
  if (options.mode === 'local') {
    options = { mode: 'local', workspace: workspacePath(options.workspace) }
    const config = await settings.effectiveConfig(options.workspace)
    frame = configFrame(config)
    credentials = configSecrets(config)
  } else {
    const url = remoteAddress(options.url)
    if (typeof options.token !== 'string' || options.token.trim() !== options.token) throw new Error('Enter a token without surrounding whitespace.')
    suppliedToken = options.token
    const token = suppliedToken || (lastConnection?.mode === 'remote' && lastConnection.url === url ? lastConnection.token : '') || await settings.remoteToken(url)
    if (!token) throw new Error('Enter the Core access token.')
    const name = typeof options.name === 'string' ? options.name.trim().slice(0, 120) || undefined : undefined
    options = { ...options, url, token, name }
  }
  await disconnectRuntime()

  let nextPeer: JsonRpcPeer
  let finishLocalStartup: (() => void) | undefined
  if (options.mode === 'local') {
    const binary = coreExecutable()
    const helper = sandboxExecutable()
    const child = spawn(binary, [
      'stdio', '--config-stdin', '--workspace', options.workspace,
      '--state-dir', join(options.workspace, '.lune'),
      '--sandbox-helper', helper,
    ], { stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true, env: { ...process.env } })
    coreProcess = child
    coreExit = new Promise((resolveExit) => child.once('close', () => resolveExit()))
    // Keep provider diagnostics off Electron's console. Core startup errors
    // are reported through the connection result with credentials redacted.
    let diagnostics = ''
    let connecting = true
    child.stderr.setEncoding('utf8').on('data', (part: string) => {
      if (connecting && diagnostics.length < 8192) diagnostics += part
    })
    nextPeer = new JsonRpcPeer((message) => {
      if (!child.stdin.writable) throw new Error('lune-core input is closed')
      child.stdin.write(`${message}\n`)
    })
    let pending = ''
    const decoder = new StringDecoder('utf8')
    child.stdout.on('data', (chunk: Buffer) => {
      pending += decoder.write(chunk)
      for (;;) {
        const lineEnd = pending.indexOf('\n')
        if (lineEnd < 0) break
        const line = pending.slice(0, lineEnd).trim()
        pending = pending.slice(lineEnd + 1)
        if (line) nextPeer.receive(line)
      }
    })
    child.once('error', (error) => nextPeer.fail(error))
    child.once('close', (code, signal) => {
      const detail = connecting && diagnostics ? redact(diagnostics.trim(), credentials) : ''
      nextPeer.fail(new Error(detail || `lune-core exited (${code ?? signal ?? 'unknown'})`))
    })
    child.stdin.on('error', () => { /* let close report the startup error */ })
    child.stdin.write(frame!)
    finishLocalStartup = () => {
      connecting = false
      diagnostics = ''
    }
  } else {
    const nextSocket = new WebSocket(options.url, { headers: { Authorization: `Bearer ${options.token}` } })
    socket = nextSocket
    nextPeer = new JsonRpcPeer((message) => {
      if (nextSocket.readyState !== WebSocket.OPEN) throw new Error('Remote Core is disconnected')
      nextSocket.send(message)
    })
    nextSocket.on('message', (data) => nextPeer.receive(data.toString()))
    nextSocket.on('error', (error) => nextPeer.fail(new Error(`Remote Core connection failed: ${error.message}`)))
    nextSocket.on('close', (code) => nextPeer.fail(new Error(`Remote Core disconnected (${code})`)))
    await new Promise<void>((resolveOpen, rejectOpen) => {
      const timer = setTimeout(() => rejectOpen(new Error('Remote Core connection timed out')), 15_000)
      nextSocket.once('open', () => { clearTimeout(timer); resolveOpen() })
      nextSocket.once('error', (error) => { clearTimeout(timer); rejectOpen(error) })
    })
  }

  attachPeer(nextPeer)
  try {
    const hello = await nextPeer.request('runtime.hello', { clientName: 'Lune Desktop', clientVersion: app.getVersion() })
    finishLocalStartup?.()
    connectionNotice = ''
    const remember = options.mode === 'remote' && suppliedToken && typeof options.rememberToken === 'boolean'
      ? { token: options.token, save: options.rememberToken } : undefined
    try {
      const transient = await settings.recordConnection(options.mode, connectionAddress(options), options.mode === 'remote' ? options.name : undefined, remember)
      if (transient) connectionNotice = 'The system credential store is unavailable. This token is remembered only until the app closes.'
    } catch {
      connectionNotice = 'The connection is active, but its settings could not be saved.'
    }
    lastConnection = options
    return hello
  } catch (error) {
    await disconnectRuntime()
    throw error
  }
}

ipcMain.handle('runtime:connect', async (event, options: DesktopConnectionOptions) => {
  assertMainWindow(event)
  return serial(runtimeChange, () => openRuntime(options), (pending) => { runtimeChange = pending })
})

ipcMain.handle('runtime:reconnect', async (event) => {
  assertMainWindow(event)
  return serial(runtimeChange, async () => {
    if (!lastConnection) throw new Error('Connect to a Core before reconnecting.')
    const options = lastConnection.mode === 'remote' ? { ...lastConnection, rememberToken: undefined } : lastConnection
    return openRuntime(options)
  }, (pending) => { runtimeChange = pending })
})

ipcMain.handle('runtime:request', (event, message: { method?: unknown; params?: unknown }) => {
  assertMainWindow(event)
  if (typeof message?.method !== 'string' || !allowedRuntimeMethods.has(message.method)) throw new Error('This Runtime Protocol method is not available to the frontend.')
  if (!peer) throw new Error('Connect to Core first.')
  return peer.request(message.method, message.params ?? {}, message.method === 'session.compact' ? 600_000 : undefined)
})

ipcMain.handle('runtime:close', async (event, forget = false) => {
  assertMainWindow(event)
  return serial(runtimeChange, async () => {
    await disconnectRuntime()
    if (forget) lastConnection = null
  }, (pending) => { runtimeChange = pending })
})

app.whenReady().then(() => {
  settings = new DesktopSettings()
  const theme = settings.preference('theme')
  if (isThemePreference(theme)) nativeTheme.themeSource = theme
  quickShortcut = settings.preference('shortcut') || defaultShortcut
  registerQuickShortcut(quickShortcut)
  createMainWindow()
}).catch(() => {
  dialog.showErrorBox('Lune settings are unavailable', 'The settings database could not be opened or upgraded. The original settings have been left in place.')
  app.quit()
})
app.on('will-quit', () => {
  globalShortcut.unregisterAll()
  settings?.close()
})
app.on('activate', () => { if (BrowserWindow.getAllWindows().length === 0) createMainWindow() })
app.on('before-quit', (event) => {
  if (quitting) return
  event.preventDefault()
  quitting = true
  void Promise.all([settingsWrite, runtimeChange]).then(disconnectRuntime).finally(() => app.quit())
})
app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit() })
