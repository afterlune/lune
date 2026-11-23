import { app, BrowserWindow, desktopCapturer, dialog, globalShortcut, ipcMain, nativeTheme } from 'electron'
import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process'
import { existsSync, readFileSync, renameSync, statSync, writeFileSync } from 'node:fs'
import { writeFile } from 'node:fs/promises'
import { StringDecoder } from 'node:string_decoder'
import { dirname, isAbsolute, join, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import WebSocket from 'ws'
import { JsonRpcPeer, type JsonRpcNotification } from '../shared/jsonrpc.js'
import { isThemePreference, windowColors } from '../shared/theme.js'
import type { DesktopConnectionOptions } from './preload.js'

const currentDirectory = dirname(fileURLToPath(import.meta.url))
const developmentServer = process.env.ELECTRON_RENDERER_URL
const rendererFileURL = pathToFileURL(resolve(currentDirectory, '../renderer/index.html')).toString()
const allowedRuntimeMethods = new Set([
  'runtime.hello', 'runtime.subscribe', 'runtime.unsubscribe', 'runtime.state', 'runtime.pending',
  'session.create', 'session.get', 'session.history', 'session.historyPage', 'session.list', 'session.search',
  'session.setSettings', 'session.setTitle', 'session.setArchived', 'session.delete', 'session.fork',
  'session.rewindPreview', 'session.rewind', 'session.compact', 'turn.cancelQueued',
  'model.list', 'turn.submit', 'turn.interrupt', 'turn.recover', 'turn.discardPaused',
  'approval.resolve', 'question.resolve', 'interaction.resolve', 'toolRecovery.resolve',
])
let mainWindow: BrowserWindow | null = null
let peer: JsonRpcPeer | null = null
let socket: WebSocket | null = null
let coreProcess: ChildProcessWithoutNullStreams | null = null
let coreExit: Promise<void> | null = null
let lastConnection: DesktopConnectionOptions | null = null
let quitting = false
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

function shortcutFile(): string {
  return join(app.getPath('userData'), 'quick-shortcut.txt')
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
  peer.onNotification(sendNotification)
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

function themeFile(): string {
  return join(app.getPath('userData'), 'appearance.txt')
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
  const pending = `${themeFile()}.${process.pid}.tmp`
  writeFileSync(pending, preference, { mode: 0o600 })
  renameSync(pending, themeFile())
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
  try { writeFileSync(shortcutFile(), accelerator, { mode: 0o600 }) }
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

async function openRuntime(options: DesktopConnectionOptions): Promise<unknown> {
  if (!options || (options.mode !== 'local' && options.mode !== 'remote')) throw new Error('Choose a valid connection type.')
  await disconnectRuntime()

  let nextPeer: JsonRpcPeer
  if (options.mode === 'local') {
    if (typeof options.workspace !== 'string' || !isAbsolute(options.workspace) || !statSync(options.workspace, { throwIfNoEntry: false })?.isDirectory()) {
      throw new Error('Choose an existing workspace folder.')
    }
    if (!existsSync(join(options.workspace, 'config.json'))) {
      throw new Error('This workspace needs a config.json with at least one model provider. Use config.example.json as a starting point.')
    }
    const binary = coreExecutable()
    const helper = sandboxExecutable()
    const child = spawn(binary, [
      'stdio', '--workspace', options.workspace,
      '--state-dir', join(options.workspace, '.lune'),
      '--sandbox-helper', helper,
    ], { stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true, env: { ...process.env } })
    coreProcess = child
    coreExit = new Promise((resolveExit) => child.once('exit', () => resolveExit()))
    child.stderr.on('data', (chunk: Buffer) => console.error('[lune-core]', chunk.toString().trimEnd()))
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
    child.once('exit', (code, signal) => nextPeer.fail(new Error(`lune-core exited (${code ?? signal ?? 'unknown'})`)))
  } else {
    if (typeof options.url !== 'string' || typeof options.token !== 'string') throw new Error('Enter the Core URL and access token.')
    const remoteURL = new URL(options.url)
    if (remoteURL.protocol !== 'wss:' || remoteURL.username || remoteURL.password || remoteURL.search || remoteURL.hash) throw new Error('Use a wss:// Core URL without credentials, query, or fragment.')
    if (!options.token.trim()) throw new Error('Enter the Core access token.')
    if (options.token !== options.token.trim()) throw new Error('The Core access token must not contain surrounding whitespace.')
    const nextSocket = new WebSocket(remoteURL, { headers: { Authorization: `Bearer ${options.token}` } })
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
    lastConnection = options
    return hello
  } catch (error) {
    await disconnectRuntime()
    throw error
  }
}

ipcMain.handle('runtime:connect', async (event, options: DesktopConnectionOptions) => {
  assertMainWindow(event)
  lastConnection = null
  return openRuntime(options)
})

ipcMain.handle('runtime:reconnect', async (event) => {
  assertMainWindow(event)
  if (!lastConnection) throw new Error('Connect to a Core before reconnecting.')
  return openRuntime(lastConnection)
})

ipcMain.handle('runtime:request', (event, message: { method?: unknown; params?: unknown }) => {
  assertMainWindow(event)
  if (typeof message?.method !== 'string' || !allowedRuntimeMethods.has(message.method)) throw new Error('This Runtime Protocol method is not available to the frontend.')
  if (!peer) throw new Error('Connect to Core first.')
  return peer.request(message.method, message.params ?? {}, message.method === 'session.compact' ? 600_000 : undefined)
})

ipcMain.handle('runtime:close', async (event, forget = false) => {
  assertMainWindow(event)
  await disconnectRuntime()
  if (forget) lastConnection = null
})

app.whenReady().then(() => {
  try {
    const theme = readFileSync(themeFile(), 'utf8').trim()
    if (isThemePreference(theme)) nativeTheme.themeSource = theme
  } catch { /* default to the system appearance */ }
  try { quickShortcut = readFileSync(shortcutFile(), 'utf8').trim() || defaultShortcut } catch { /* first launch */ }
  registerQuickShortcut(quickShortcut)
  createMainWindow()
})
app.on('will-quit', () => globalShortcut.unregisterAll())
app.on('activate', () => { if (BrowserWindow.getAllWindows().length === 0) createMainWindow() })
app.on('before-quit', (event) => {
  if (quitting) return
  event.preventDefault()
  quitting = true
  void disconnectRuntime().finally(() => app.quit())
})
app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit() })
