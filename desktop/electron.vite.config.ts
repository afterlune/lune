import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig, externalizeDepsPlugin } from 'electron-vite'
import react from '@vitejs/plugin-react'

const currentDirectory = dirname(fileURLToPath(import.meta.url))
const root = resolve(currentDirectory, 'src')

export default defineConfig({
  main: {
    plugins: [externalizeDepsPlugin()],
    build: { rolldownOptions: { input: resolve(root, 'electron/main.ts') } },
  },
  preload: {
    plugins: [externalizeDepsPlugin()],
    build: { rolldownOptions: { input: resolve(root, 'electron/preload.ts'), output: { format: 'cjs', entryFileNames: 'preload.js' } } },
  },
  renderer: {
    root: resolve(root, 'renderer'),
    base: './',
    plugins: [react()],
    resolve: { alias: { '@': resolve(root, 'renderer') } },
    build: { outDir: resolve(currentDirectory, 'out/renderer'), emptyOutDir: true, rolldownOptions: { input: resolve(root, 'renderer/index.html') } },
  },
})
