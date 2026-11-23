import { spawnSync } from 'node:child_process'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDirectory = dirname(fileURLToPath(import.meta.url))
const desktopDirectory = resolve(scriptDirectory, '..')
const target = process.argv[2] || process.platform
if (!['linux', 'win32'].includes(target)) throw new Error('Electron packaging supports Linux x64 and Windows x64.')

const cli = resolve(desktopDirectory, 'node_modules/electron-builder/cli.js')
const result = spawnSync(process.execPath, [
  cli, '--config', 'electron-builder.config.cjs', target === 'win32' ? '--win' : '--linux', '--x64',
], {
  cwd: desktopDirectory,
  stdio: 'inherit',
  env: { ...process.env, LUNE_DESKTOP_TARGET: target },
})
if (result.error) throw result.error
if (result.status !== 0) throw new Error(`Could not package Lune for ${target} x64.`)
