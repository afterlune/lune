import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDir = path.dirname(fileURLToPath(import.meta.url))
const rendererDir = path.resolve(scriptDir, '../src/renderer')
const localesDir = path.join(rendererDir, 'i18n/locales')

const en = JSON.parse(fs.readFileSync(path.join(localesDir, 'en.json'), 'utf8'))
const zh = JSON.parse(fs.readFileSync(path.join(localesDir, 'zh.json'), 'utf8'))

const errors = []
const enKeys = new Set(Object.keys(en))
const zhKeys = new Set(Object.keys(zh))

for (const key of enKeys) {
  if (!zhKeys.has(key)) errors.push(`Missing key in zh.json: ${JSON.stringify(key)}`)
  if (typeof en[key] !== 'string' || !en[key].trim()) errors.push(`Empty value in en.json for: ${JSON.stringify(key)}`)
}
for (const key of zhKeys) {
  if (!enKeys.has(key)) errors.push(`Extra key in zh.json (missing in en.json): ${JSON.stringify(key)}`)
  if (typeof zh[key] !== 'string' || !zh[key].trim()) errors.push(`Empty value in zh.json for: ${JSON.stringify(key)}`)
}

const INVARIANT_KEYS = new Set(['MCP'])
const LITERAL_T_RE = /(?:^|[^a-zA-Z0-9_$])t\(\s*(['"])((?:\\.|(?!\1).)*?)\1\s*\)/g

function walk(dir) {
  const out = []
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name)
    if (entry.isDirectory()) out.push(...walk(full))
    else if (/\.(ts|tsx)$/.test(entry.name)) out.push(full)
  }
  return out
}

for (const file of walk(rendererDir)) {
  const src = fs.readFileSync(file, 'utf8')
  for (const match of src.matchAll(LITERAL_T_RE)) {
    const raw = match[2].replace(/\\(['"\\])/g, '$1')
    if (!INVARIANT_KEYS.has(raw) && !zhKeys.has(raw)) {
      errors.push(`${path.relative(rendererDir, file)}: untranslated literal key ${JSON.stringify(raw)}`)
    }
  }
}

if (errors.length > 0) {
  console.error(`i18n check failed with ${errors.length} issue(s):\n` + errors.map((e) => `  - ${e}`).join('\n'))
  process.exit(1)
}

console.log(`i18n check passed (${enKeys.size} keys verified across en.json, zh.json, and renderer sources).`)
