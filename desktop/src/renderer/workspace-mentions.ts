import type { RuntimeClient } from './runtime'

export type MentionItem = {
  path: string
  name: string
  kind: 'file' | 'directory'
}

type DirectoryEntry = {
  name: string
  kind: 'file' | 'directory'
}

type DirectoryPage = {
  items: DirectoryEntry[] | null
  nextOffset: number
  more: boolean
}

const IGNORED_DIRS = new Set([
  '.git',
  '.lune',
  'node_modules',
  'dist',
  'out',
  'build',
  'target',
  '__pycache__',
  '.next',
  '.nuxt',
  '.venv',
  'venv',
  'vendor',
  '.cache',
  '.turbo',
])

export async function indexWorkspaceFiles(
  client: RuntimeClient,
  options?: { maxDepth?: number; maxEntries?: number; signal?: AbortSignal },
): Promise<MentionItem[]> {
  const maxDepth = options?.maxDepth ?? 4
  const maxEntries = options?.maxEntries ?? 600
  const signal = options?.signal
  const collected: MentionItem[] = []
  const queue: Array<{ path: string; depth: number }> = [{ path: '', depth: 0 }]

  while (queue.length > 0 && collected.length < maxEntries) {
    if (signal?.aborted) break
    const current = queue.shift()!
    let offset = 0
    for (;;) {
      if (signal?.aborted) break
      const page = await client.request<DirectoryPage>('workspace.listDirectory', {
        path: current.path,
        offset,
      })
      const entries = page.items ?? []
      for (const entry of entries) {
        const isDir = entry.kind === 'directory'
        if (isDir && IGNORED_DIRS.has(entry.name)) continue
        const fullPath = current.path ? `${current.path}/${entry.name}` : entry.name
        collected.push({ path: fullPath, name: entry.name, kind: isDir ? 'directory' : 'file' })
        if (isDir && current.depth < maxDepth) {
          queue.push({ path: fullPath, depth: current.depth + 1 })
        }
        if (collected.length >= maxEntries) break
      }
      if (!page.more || collected.length >= maxEntries) break
      if (page.nextOffset <= offset) break
      offset = page.nextOffset
    }
  }

  return collected
}

export function matchMentionQuery(draft: string, caret: number): { start: number; end: number; query: string } | null {
  const safeCaret = Math.max(0, Math.min(caret, draft.length))
  const before = draft.slice(0, safeCaret)
  const match = /(?:^|\s)(@([^\s@]*))$/.exec(before)
  if (!match) return null
  const token = match[1]
  const start = safeCaret - token.length
  return { start, end: safeCaret, query: match[2] ?? '' }
}

function subsequenceScore(target: string, query: string): number {
  let ti = 0
  let qi = 0
  let consecutive = 0
  let score = 0
  while (ti < target.length && qi < query.length) {
    if (target[ti] === query[qi]) {
      qi += 1
      consecutive += 1
      score += 1 + consecutive
    } else {
      consecutive = 0
    }
    ti += 1
  }
  return qi === query.length ? score : -1
}

export function filterMentionItems(items: MentionItem[], rawQuery: string, limit = 12): MentionItem[] {
  const query = rawQuery.trim().toLowerCase()
  if (!query) {
    return [...items]
      .sort((a, b) => {
        const depthA = a.path.split('/').length
        const depthB = b.path.split('/').length
        if (depthA !== depthB) return depthA - depthB
        if (a.kind !== b.kind) return a.kind === 'file' ? -1 : 1
        return a.path.localeCompare(b.path)
      })
      .slice(0, limit)
  }

  const scored: Array<{ item: MentionItem; score: number }> = []
  for (const item of items) {
    const nameLower = item.name.toLowerCase()
    const pathLower = item.path.toLowerCase()
    let score = -1
    if (nameLower === query) score = 1000
    else if (nameLower.startsWith(query)) score = 800 - (nameLower.length - query.length)
    else if (nameLower.includes(query)) score = 600 - nameLower.indexOf(query)
    else if (pathLower.startsWith(query)) score = 450 - (pathLower.length - query.length)
    else if (pathLower.includes(query)) score = 300 - pathLower.indexOf(query)
    else {
      const sub = subsequenceScore(pathLower, query)
      if (sub > 0) score = 100 + sub
    }
    if (score > 0) {
      if (item.kind === 'file') score += 20
      scored.push({ item, score })
    }
  }

  scored.sort((a, b) => b.score - a.score || a.item.path.length - b.item.path.length || a.item.path.localeCompare(b.item.path))
  return scored.slice(0, limit).map((entry) => entry.item)
}
