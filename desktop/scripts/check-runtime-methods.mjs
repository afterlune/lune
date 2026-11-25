import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDir = path.dirname(fileURLToPath(import.meta.url))
const desktopDir = path.resolve(scriptDir, '..')
const rendererDir = path.join(desktopDir, 'src/renderer')
const mainFile = path.join(desktopDir, 'src/electron/main.ts')

// The Electron main process is the desktop renderer's only transport to Core, so a
// Runtime Protocol method that is missing from its allowlist compiles fine and fails
// only at runtime. Keep the renderer's requests and the allowlist in the same check.
const allowlistBlock = /const allowedRuntimeMethods = new Set\(\[([\s\S]*?)\]\)/.exec(fs.readFileSync(mainFile, 'utf8'))
if (!allowlistBlock) {
  console.error('runtime method check failed: allowedRuntimeMethods not found in src/electron/main.ts')
  process.exit(1)
}
const allowed = new Set([...allowlistBlock[1].matchAll(/'([A-Za-z][\w.]*)'/g)].map((match) => match[1]))

// Anchor on ".request" and read the literal that the call passes, so capability
// checks such as capabilities.includes('question.draft') never look like requests.
const ANCHOR_RE = /\.request\b/g
const CALL_RE = /^\s*(['"`])([A-Za-z][\w.]*)\1/
const WINDOW = 300

function requestedMethods(source) {
  const methods = new Set()
  for (const match of source.matchAll(ANCHOR_RE)) {
    const afterAnchor = source.slice(match.index + match[0].length, match.index + match[0].length + WINDOW)
    const paren = afterAnchor.indexOf('(')
    if (paren < 0) continue
    const gap = afterAnchor.slice(0, paren)
    if (/[()'"`]/.test(gap)) continue
    const call = CALL_RE.exec(afterAnchor.slice(paren + 1))
    if (call && call[2].includes('.')) methods.add(call[2])
  }
  return methods
}

function walk(dir) {
  const out = []
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) out.push(...walk(full))
    else if (/\.(ts|tsx)$/.test(entry.name)) out.push(full)
  }
  return out
}

const missing = new Map()
for (const file of walk(rendererDir)) {
  for (const method of requestedMethods(fs.readFileSync(file, 'utf8'))) {
    if (allowed.has(method)) continue
    if (!missing.has(method)) missing.set(method, new Set())
    missing.get(method).add(path.relative(desktopDir, file))
  }
}

if (missing.size > 0) {
  const lines = [...missing].map(([method, files]) => `  - ${method} (${[...files].join(', ')})`)
  console.error(`runtime method check failed: ${missing.size} requested method(s) blocked by the Electron allowlist:\n${lines.join('\n')}`)
  process.exit(1)
}

console.log(`runtime method check passed (${allowed.size} allowlisted methods cover every renderer request).`)
