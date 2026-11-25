export type TerminalTarget =
  | { kind: 'web'; wsUrl: string; token: string }
  | { kind: 'desktop'; cwd: string }

export interface TerminalTransport {
  write(data: string | Uint8Array): void
  resize(cols: number, rows: number): void
  close(): void
  onData(cb: (data: Uint8Array) => void): () => void
  onExit(cb: (code: number) => void): () => void
  onError(cb: (message: string) => void): () => void
}

function base64URL(value: string): string {
  const bytes = new TextEncoder().encode(value)
  let binary = ''
  for (const byte of bytes) binary += String.fromCharCode(byte)
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/g, '')
}

function encodeBase64Bytes(bytes: Uint8Array): string {
  let binary = ''
  const chunkSize = 0x8000
  for (let i = 0; i < bytes.length; i += chunkSize) {
    const chunk = bytes.subarray(i, i + chunkSize)
    binary += String.fromCharCode(...chunk)
  }
  return btoa(binary)
}

function decodeBase64Bytes(encoded: string): Uint8Array {
  const binary = atob(encoded)
  const out = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) {
    out[i] = binary.charCodeAt(i)
  }
  return out
}

export function terminalTargetKey(target: TerminalTarget | null | undefined): string {
  if (!target) return ''
  return target.kind === 'web' ? `web:${target.wsUrl}` : `desktop:${target.cwd}`
}

export async function createTerminalTransport(
  target: TerminalTarget,
  cols: number,
  rows: number,
): Promise<TerminalTransport> {
  const dataListeners = new Set<(data: Uint8Array) => void>()
  const exitListeners = new Set<(code: number) => void>()
  const errorListeners = new Set<(message: string) => void>()

  if (target.kind === 'desktop') {
    const desktop = window.luneDesktop
    if (!desktop) throw new Error('Desktop terminal API is unavailable.')

    let closed = false
    let termId = ''
    const offEvent = desktop.onTerminalEvent((event) => {
      if (event.id !== termId || closed) return
      if (event.type === 'data' && event.data) {
        try {
          const bytes = decodeBase64Bytes(event.data)
          for (const cb of dataListeners) cb(bytes)
        } catch { /* ignore malformed frame */ }
      } else if (event.type === 'exit') {
        const code = typeof event.code === 'number' ? event.code : 0
        for (const cb of exitListeners) cb(code)
      } else if (event.type === 'error' && event.message) {
        for (const cb of errorListeners) cb(event.message)
      }
    })

    try {
      const opened = await desktop.terminalOpen({ cwd: target.cwd, cols, rows })
      termId = opened.id
    } catch (error) {
      offEvent()
      throw error
    }

    const encoder = new TextEncoder()
    return {
      write(data) {
        if (closed || !termId) return
        const bytes = typeof data === 'string' ? encoder.encode(data) : data
        void desktop.terminalWrite(termId, encodeBase64Bytes(bytes)).catch(() => undefined)
      },
      resize(nextCols, nextRows) {
        if (closed || !termId) return
        void desktop.terminalResize(termId, nextCols, nextRows).catch(() => undefined)
      },
      close() {
        if (closed) return
        closed = true
        offEvent()
        if (termId) void desktop.terminalClose(termId).catch(() => undefined)
        dataListeners.clear()
        exitListeners.clear()
        errorListeners.clear()
      },
      onData(cb) {
        dataListeners.add(cb)
        return () => dataListeners.delete(cb)
      },
      onExit(cb) {
        exitListeners.add(cb)
        return () => exitListeners.delete(cb)
      },
      onError(cb) {
        errorListeners.add(cb)
        return () => errorListeners.delete(cb)
      },
    }
  }

  const endpoint = new URL(target.wsUrl)
  endpoint.searchParams.set('cols', String(Math.max(20, Math.round(cols))))
  endpoint.searchParams.set('rows', String(Math.max(5, Math.round(rows))))

  const socket = new WebSocket(endpoint, [
    'lune-terminal.v1',
    `lune-terminal-auth.${base64URL(target.token)}`,
  ])
  socket.binaryType = 'arraybuffer'

  let closed = false
  let exitedNotified = false
  const encoder = new TextEncoder()

  await new Promise<void>((resolve, reject) => {
    const timer = window.setTimeout(() => reject(new Error('Terminal connection timed out.')), 12_000)
    socket.addEventListener('open', () => {
      window.clearTimeout(timer)
      resolve()
    }, { once: true })
    socket.addEventListener('error', () => {
      window.clearTimeout(timer)
      reject(new Error('Could not connect to workspace terminal.'))
    }, { once: true })
  })

  socket.addEventListener('message', (event) => {
    if (closed) return
    if (event.data instanceof ArrayBuffer) {
      const bytes = new Uint8Array(event.data)
      for (const cb of dataListeners) cb(bytes)
      return
    }
    if (typeof event.data === 'string') {
      try {
        const msg = JSON.parse(event.data) as { type?: string; code?: number; message?: string }
        if (msg.type === 'exit' && !exitedNotified) {
          exitedNotified = true
          const code = typeof msg.code === 'number' ? msg.code : 0
          for (const cb of exitListeners) cb(code)
        } else if (msg.type === 'error' && msg.message) {
          for (const cb of errorListeners) cb(msg.message)
        }
      } catch { /* ignore non-json text */ }
    }
  })

  socket.addEventListener('close', (event) => {
    if (closed || exitedNotified) return
    exitedNotified = true
    const code = event.code === 1000 ? 0 : event.code
    for (const cb of exitListeners) cb(code)
  })

  socket.addEventListener('error', () => {
    if (closed) return
    for (const cb of errorListeners) cb('Terminal WebSocket error.')
  })

  return {
    write(data) {
      if (closed || socket.readyState !== WebSocket.OPEN) return
      const payload = typeof data === 'string' ? encoder.encode(data) : new Uint8Array(data)
      socket.send(payload)
    },
    resize(nextCols, nextRows) {
      if (closed || socket.readyState !== WebSocket.OPEN) return
      socket.send(JSON.stringify({ type: 'resize', cols: nextCols, rows: nextRows }))
    },
    close() {
      if (closed) return
      closed = true
      if (socket.readyState < WebSocket.CLOSING) {
        try { socket.send(JSON.stringify({ type: 'close' })) } catch { /* ignore */ }
        socket.close(1000, 'Terminal closed')
      }
      dataListeners.clear()
      exitListeners.clear()
      errorListeners.clear()
    },
    onData(cb) {
      dataListeners.add(cb)
      return () => dataListeners.delete(cb)
    },
    onExit(cb) {
      exitListeners.add(cb)
      return () => exitListeners.delete(cb)
    },
    onError(cb) {
      errorListeners.add(cb)
      return () => errorListeners.delete(cb)
    },
  }
}
