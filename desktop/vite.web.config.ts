import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'

const currentDirectory = dirname(fileURLToPath(import.meta.url))

export default defineConfig({
  root: resolve(currentDirectory, 'src/renderer'),
  base: './',
  plugins: [tailwindcss(), react()],
  resolve: { alias: { '@': resolve(currentDirectory, 'src/renderer') } },
  build: { outDir: resolve(currentDirectory, 'dist'), emptyOutDir: true },
})
