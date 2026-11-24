const tokenKey = 'lune:loopback-runtime'
const webHostTokenKey = 'lune:web-host-token'
const tokenPattern = /^[A-Z2-7]{26,120}$/

export interface WebHostStatus {
  host: boolean
  auth: 'pair' | 'token'
}

export function isLocalBrowser(): boolean {
  return !window.luneDesktop && location.protocol === 'http:' && location.hostname === '127.0.0.1'
}

export async function detectWebHost(): Promise<WebHostStatus | null> {
  if (window.luneDesktop) return null
  try {
    const response = await fetch('/_lune/status', {
      method: 'GET',
      mode: 'same-origin',
      credentials: 'omit',
      redirect: 'error',
    })
    if (!response.ok) return null
    const data = await response.json() as Partial<WebHostStatus>
    if (data?.host && (data.auth === 'pair' || data.auth === 'token')) {
      return { host: true, auth: data.auth }
    }
    return null
  } catch {
    return null
  }
}

export function loadSavedHostToken(): string | null {
  try {
    return localStorage.getItem(webHostTokenKey) || sessionStorage.getItem(webHostTokenKey) || null
  } catch {
    return null
  }
}

export function saveHostToken(token: string, remember: boolean): void {
  try {
    sessionStorage.setItem(webHostTokenKey, token)
    if (remember) localStorage.setItem(webHostTokenKey, token)
    else localStorage.removeItem(webHostTokenKey)
  } catch { /* ignore storage errors */ }
}

export function clearHostToken(): void {
  try {
    sessionStorage.removeItem(webHostTokenKey)
    localStorage.removeItem(webHostTokenKey)
  } catch { /* ignore */ }
}

export async function verifyHostToken(token: string): Promise<{ ok: boolean; error?: string }> {
  try {
    const response = await fetch('/_lune/auth', {
      method: 'POST',
      mode: 'same-origin',
      credentials: 'omit',
      redirect: 'error',
      headers: { Authorization: 'Bearer ' + token },
    })
    if (response.ok) return { ok: true }
    if (response.status === 401) return { ok: false, error: 'Invalid access token.' }
    const text = (await response.text()).trim()
    return { ok: false, error: text || `Request rejected (${response.status})` }
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : 'Network error' }
  }
}

export async function loadLoopbackToken(): Promise<string | null> {
  if (!isLocalBrowser()) return null
  const prefix = '#lune-pair='
  if (!location.hash.startsWith(prefix)) {
    try {
      const token = sessionStorage.getItem(tokenKey) ?? ''
      return tokenPattern.test(token) ? token : null
    } catch {
      return null
    }
  }
  const code = location.hash.slice(prefix.length)
  history.replaceState(null, '', location.pathname + location.search)
  if (!tokenPattern.test(code)) throw new Error('The local Web launch link is invalid.')
  const response = await fetch('/_lune/pair', {
    method: 'POST', body: code, mode: 'same-origin', credentials: 'omit', redirect: 'error',
  })
  if (!response.ok) throw new Error('The launch link has expired or has already been used. Restart lune web to get a fresh link.')
  const { token } = await response.json() as { token?: string }
  if (!token || !tokenPattern.test(token)) throw new Error('Core returned an invalid local credential.')
  try { sessionStorage.setItem(tokenKey, token) } catch { /* current tab can still connect */ }
  return token
}

const remoteConnKey = 'lune:remote-connection'

export function loadSavedWebConnection(): { url: string; token: string; remembered: boolean } | null {
  try {
    const raw = localStorage.getItem(remoteConnKey) || sessionStorage.getItem(remoteConnKey)
    if (!raw) return null
    const parsed = JSON.parse(raw) as { url?: string; token?: string; remembered?: boolean }
    if (!parsed.url || !parsed.token) return null
    return { url: parsed.url, token: parsed.token, remembered: Boolean(parsed.remembered) }
  } catch {
    return null
  }
}

export function saveWebConnection(url: string, token: string, remember: boolean): void {
  try {
    const payload = JSON.stringify({ url, token, remembered: remember })
    sessionStorage.setItem(remoteConnKey, payload)
    if (remember) localStorage.setItem(remoteConnKey, payload)
    else localStorage.removeItem(remoteConnKey)
  } catch { /* ignore */ }
}

export function clearWebConnection(): void {
  try {
    sessionStorage.removeItem(remoteConnKey)
    localStorage.removeItem(remoteConnKey)
  } catch { /* ignore */ }
}

export function lastLoopbackSession(workspace: string): string | null {
  try { return sessionStorage.getItem(`lune:loopback-session:${workspace}`) } catch { return null }
}

export function rememberLoopbackSession(workspace: string, id: string): void {
  try { sessionStorage.setItem(`lune:loopback-session:${workspace}`, id) } catch { /* session can still be selected by hand */ }
}
