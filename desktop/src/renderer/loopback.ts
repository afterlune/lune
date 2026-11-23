const tokenKey = 'lune:loopback-runtime'
const tokenPattern = /^[A-Z2-7]{26,120}$/

export function isLocalBrowser(): boolean {
  return !window.luneDesktop && location.protocol === 'http:' && location.hostname === '127.0.0.1'
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

export function lastLoopbackSession(): string | null {
  try { return sessionStorage.getItem('lune:loopback-session') } catch { return null }
}

export function rememberLoopbackSession(id: string): void {
  try { sessionStorage.setItem('lune:loopback-session', id) } catch { /* session can still be selected by hand */ }
}
