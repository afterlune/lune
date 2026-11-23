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
const siblingCore = process.env.LUNE_CORE_DIR ? resolve(process.env.LUNE_CORE_DIR) : resolve(repositoryRoot, '../luneCore')
const siblingHead = existsSync(resolve(siblingCore, 'go.mod'))
  ? spawnSync('git', ['rev-parse', 'HEAD'], { cwd: siblingCore, encoding: 'utf8' }).stdout?.trim()
  : undefined
const siblingDirty = existsSync(resolve(siblingCore, 'go.mod'))
  ? spawnSync('git', ['status', '--porcelain'], { cwd: siblingCore, encoding: 'utf8' }).stdout?.trim()
  : 'missing'
const useSiblingCore = Boolean(process.env.LUNE_CORE_DIR || (pinnedRevision && siblingHead?.startsWith(pinnedRevision) && !siblingDirty))
let buildDirectory = useSiblingCore ? siblingCore : ''

if (!['win32', 'linux'].includes(target)) {
  throw new Error('Desktop Core builds currently support Windows x64 and Linux x64.')
}

if (!pinnedModule) throw new Error('Could not find the pinned luneCore version in the root go.mod.')

if (!buildDirectory) {
  const result = spawnSync(process.env.GO || 'go', ['mod', 'download', '-json', `github.com/afterlune/luneCore@${pinnedModule}`], {
    cwd: repositoryRoot,
    encoding: 'utf8',
    env: { ...process.env, GOWORK: 'off' },
    stdio: ['ignore', 'pipe', 'inherit'],
  })
  if (result.error) throw result.error
  if (result.status !== 0) throw new Error('Could not download the pinned luneCore source.')
  try {
    buildDirectory = JSON.parse(result.stdout).Dir
  } catch {
    throw new Error('Go returned an invalid luneCore module download result.')
  }
}

mkdirSync(outputDirectory, { recursive: true })

for (const [packagePath, filename] of [
  ['./cmd/lune-core', `lune-core${suffix}`],
  ['./cmd/lune-sandbox', `lune-sandbox${suffix}`],
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

console.log(`Built lune-core and lune-sandbox for ${target} x64 in ${outputDirectory}${process.env.LUNE_CORE_DIR ? ' from LUNE_CORE_DIR' : useSiblingCore ? ' from the pinned sibling Core checkout' : ' from the pinned Go module source'}`)
