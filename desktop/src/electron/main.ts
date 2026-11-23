import { app, BrowserWindow, dialog, ipcMain } from 'electron'
import { spawn, type ChildProcessWithoutNullStreams } from 'node:child_process'
import { existsSync, statSync } from 'node:fs'
import { dirname, isAbsolute, join, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import WebSocket from 'ws'
import { JsonRpcPeer, type JsonRpcNotification } from '../shared/jsonrpc.js'
import type { DesktopConnectionOptions } from './preload.js'

const currentDirectory = dirname(fileURLToPath(import.meta.url))
const developmentServer = process.env.ELECTRON_RENDERER_URL
const rendererFileURL = pathToFileURL(resolve(currentDirectory, '../renderer/index.html')).toString()
const allowedRuntimeMethods = new Set([
  'runtime.hello', 'runtime.subscribe', 'runtime.unsubscribe', 'runtime.state', 'runtime.pending',
  'session.create', 'session.get', 'session.history', 'session.list', 'session.setSettings', 'session.setTitle',
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

function createMainWindow(): void {
  mainWindow = new BrowserWindow({
    width: 1440,
    height: 960,
    minWidth: 980,
    minHeight: 680,
    title: 'Lune',
    backgroundColor: '#f6f5f2',
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
  mainWindow.on('closed', () => { mainWindow = null })
}

ipcMain.handle('desktop:select-workspace', async (event) => {
  assertMainWindow(event)
  const selection = await dialog.showOpenDialog(mainWindow!, { properties: ['openDirectory', 'createDirectory'] })
  return selection.canceled ? null : selection.filePaths[0] ?? null
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
    child.stdout.on('data', (chunk: Buffer) => {
      pending += chunk.toString('utf8')
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
  return peer.request(message.method, message.params ?? {})
})

ipcMain.handle('runtime:close', async (event, forget = false) => {
  assertMainWindow(event)
  await disconnectRuntime()
  if (forget) lastConnection = null
})

app.whenReady().then(createMainWindow)
app.on('activate', () => { if (BrowserWindow.getAllWindows().length === 0) createMainWindow() })
app.on('before-quit', (event) => {
  if (quitting) return
  event.preventDefault()
  quitting = true
  void disconnectRuntime().finally(() => app.quit())
})
app.on('window-all-closed', () => { if (process.platform !== 'darwin') app.quit() })
