import { spawnSync } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDirectory = dirname(fileURLToPath(import.meta.url))
const desktopDirectory = resolve(scriptDirectory, '..')
const repositoryRoot = resolve(desktopDirectory, '..')
const target = process.argv[2] || process.platform
const outputDirectory = resolve(desktopDirectory, 'resources/bin', target)
const goos = target === 'win32' ? 'windows' : target
const suffix = target === 'win32' ? '.exe' : ''
const pinnedModule = readFileSync(resolve(repositoryRoot, 'go.mod'), 'utf8').match(/github\.com\/afterlune\/luneCore\s+(v\S+)/)?.[1]
const pinnedRevision = pinnedModule?.split('-').at(-1)
const siblingCore = process.env.LUNE_CORE_DIR || resolve(repositoryRoot, '../luneCore')
const siblingHead = existsSync(resolve(siblingCore, 'go.mod'))
  ? spawnSync('git', ['rev-parse', 'HEAD'], { cwd: siblingCore, encoding: 'utf8' }).stdout?.trim()
  : undefined
const siblingDirty = existsSync(resolve(siblingCore, 'go.mod'))
  ? spawnSync('git', ['status', '--porcelain'], { cwd: siblingCore, encoding: 'utf8' }).stdout?.trim()
  : 'missing'
const useSiblingCore = Boolean(process.env.LUNE_CORE_DIR || (pinnedRevision && siblingHead?.startsWith(pinnedRevision) && !siblingDirty))
const buildDirectory = useSiblingCore ? siblingCore : repositoryRoot

if (!['win32', 'linux'].includes(target)) {
  throw new Error('Desktop Core builds currently support Windows x64 and Linux x64.')
}

mkdirSync(outputDirectory, { recursive: true })

for (const [packagePath, filename] of [
  ['github.com/afterlune/luneCore/cmd/lune-core', `lune-core${suffix}`],
  ['github.com/afterlune/luneCore/cmd/lune-sandbox', `lune-sandbox${suffix}`],
]) {
  const outputPath = resolve(outputDirectory, filename)
  const result = spawnSync(process.env.GO || 'go', ['build', '-trimpath', '-o', outputPath, packagePath], {
    cwd: buildDirectory,
    stdio: 'inherit',
    env: { ...process.env, GOOS: goos, GOARCH: 'amd64', CGO_ENABLED: '0', GOWORK: 'off' },
  })
  if (result.error) throw result.error
  if (result.status !== 0) throw new Error(`Could not build ${packagePath}.`)
}

console.log(`Built lune-core and lune-sandbox for ${target} x64 in ${outputDirectory}${useSiblingCore ? ' from the pinned sibling Core checkout' : ''}`)
