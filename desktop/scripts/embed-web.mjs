import { cpSync, mkdirSync, rmSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const from = fileURLToPath(new URL('../dist/', import.meta.url))
const to = fileURLToPath(new URL('../../internal/app/webdist/', import.meta.url))
rmSync(to, { recursive: true, force: true })
mkdirSync(to, { recursive: true })
cpSync(from, to, { recursive: true })
