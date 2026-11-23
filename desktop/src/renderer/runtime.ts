import { JsonRpcPeer, type JsonRpcNotification } from '../shared/jsonrpc'

export interface RuntimeClient {
  request<T>(method: string, params?: unknown): Promise<T>
  onNotification(listener: (notification: JsonRpcNotification) => void): () => void
  close(forget?: boolean): Promise<void>
}

export type RuntimeConnection =
  | { mode: 'local'; workspace: string }
  | { mode: 'remote'; url: string; token: string }

function base64URL(value: string): string {
  const bytes = new TextEncoder().encode(value)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '')
}

function browserClient(url: string, token: string): Promise<RuntimeClient> {
  const endpoint = new URL(url)
  if (endpoint.protocol !== 'wss:' || endpoint.username || endpoint.password || endpoint.search || endpoint.hash) {
    throw new Error('Use a wss:// Core URL without credentials, query, or fragment.')
  }
  if (!token.trim()) throw new Error('Enter the Core access token.')
  if (token !== token.trim()) throw new Error('The Core access token must not contain surrounding whitespace.')

  const socket = new WebSocket(endpoint, [
    'lune-core.jsonrpc.v1',
    `lune-core-auth.${base64URL(token)}`,
  ])
  let peer: JsonRpcPeer
  const ready = new Promise<void>((resolve, reject) => {
    const timer = window.setTimeout(() => reject(new Error('Core connection timed out.')), 15_000)
    socket.addEventListener('open', () => { window.clearTimeout(timer); resolve() }, { once: true })
    socket.addEventListener('error', () => {
      window.clearTimeout(timer)
      reject(new Error('Could not connect to Core. Check the URL, certificate, token, and allowed web origins.'))
    }, { once: true })
  })
  peer = new JsonRpcPeer((message) => {
    if (socket.readyState !== WebSocket.OPEN) throw new Error('Core is disconnected')
    socket.send(message)
  })
  socket.addEventListener('message', (event) => {
    if (typeof event.data === 'string') peer.receive(event.data)
    else peer.fail(new Error('Core sent a non-text WebSocket message.'))
  })
  socket.addEventListener('close', (event) => peer.fail(new Error(`Core disconnected (${event.code}).`)))
  socket.addEventListener('error', () => peer.fail(new Error('Core WebSocket connection failed.')))

  return ready.then(() => ({
    request: <T,>(method: string, params?: unknown) => peer.request<T>(method, params ?? {}),
    onNotification: (listener: (notification: JsonRpcNotification) => void) => peer.onNotification(listener),
    close: async () => {
      if (socket.readyState < WebSocket.CLOSING) socket.close(1000, 'Application disconnected')
    },
  }), (error: unknown) => {
    socket.close()
    throw error
  })
}

export async function connectRuntime(options: RuntimeConnection, retry = false): Promise<RuntimeClient> {
  if (window.luneDesktop) {
    const reconnecting = retry || (options.mode === 'remote' && !options.token)
    if (reconnecting) await window.luneDesktop.reconnect()
    else await window.luneDesktop.connect(options)
    return {
      request: <T,>(method: string, params?: unknown) => window.luneDesktop!.request<T>(method, params),
      onNotification: (listener) => window.luneDesktop!.onNotification(listener),
      close: async (forget = false) => { await window.luneDesktop!.close(forget) },
    }
  }

  if (options.mode !== 'remote') throw new Error('Local Core processes are available in the Electron desktop app.')
  return browserClient(options.url, options.token)
}
