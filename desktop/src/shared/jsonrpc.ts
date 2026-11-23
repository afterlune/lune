export interface JsonRpcNotification {
  method: string
  params?: unknown
}

interface JsonRpcResponse {
  jsonrpc?: string
  id?: number | string | null
  method?: string
  params?: unknown
  result?: unknown
  error?: { code?: number; message?: string; data?: unknown }
}

interface PendingRequest {
  resolve: (value: unknown) => void
  reject: (reason: Error) => void
  timeout: ReturnType<typeof setTimeout>
}

export class JsonRpcPeer {
  private nextID = 0
  private readonly pending = new Map<string, PendingRequest>()
  private readonly listeners = new Set<(notification: JsonRpcNotification) => void>()

  constructor(private readonly sendWire: (message: string) => void) {}

  request<T>(method: string, params: unknown = {}, timeoutMilliseconds = 30_000): Promise<T> {
    const id = ++this.nextID
    const key = String(id)
    return new Promise<T>((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.pending.delete(key)
        reject(new Error(`${method} timed out`))
      }, timeoutMilliseconds)
      this.pending.set(key, { resolve: resolve as (value: unknown) => void, reject, timeout })
      try {
        this.sendWire(JSON.stringify({ jsonrpc: '2.0', id, method, params }))
      } catch (error) {
        clearTimeout(timeout)
        this.pending.delete(key)
        reject(error instanceof Error ? error : new Error(String(error)))
      }
    })
  }

  receive(wireMessage: string): void {
    let decoded: unknown
    try {
      decoded = JSON.parse(wireMessage) as unknown
    } catch {
      this.fail(new Error('Core sent an invalid JSON-RPC message'))
      return
    }
    if (!decoded || typeof decoded !== 'object' || Array.isArray(decoded)) {
      this.fail(new Error('Core sent an invalid JSON-RPC message'))
      return
    }
    const response = decoded as JsonRpcResponse

    if (response.method) {
      const notification = { method: response.method, params: response.params }
      for (const listener of this.listeners) listener(notification)
      return
    }
    if (response.id === undefined || response.id === null) return

    const key = String(response.id)
    const pending = this.pending.get(key)
    if (!pending) return
    this.pending.delete(key)
    clearTimeout(pending.timeout)
    if (response.error) {
      const code = response.error.data && typeof response.error.data === 'object'
        ? (response.error.data as { code?: string }).code
        : undefined
      pending.reject(new Error(code ? `${response.error.message ?? 'Core request failed'} (${code})` : response.error.message ?? 'Core request failed'))
      return
    }
    pending.resolve(response.result)
  }

  onNotification(listener: (notification: JsonRpcNotification) => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  fail(reason: Error): void {
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timeout)
      pending.reject(reason)
    }
    this.pending.clear()
    for (const listener of this.listeners) listener({ method: 'runtime.connectionClosed', params: { message: reason.message } })
  }
}
