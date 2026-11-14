/// <reference types="vitest/config" />
import path from 'node:path'
import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { tanstackRouter } from '@tanstack/router-plugin/vite'
import { compression } from 'vite-plugin-compression2'
import { msw } from 'msw/vite'

// One build serves both mount points (design §2.4): base '/' with every asset
// and lazy chunk under /_ui/, so it never collides with the legacy app's
// /assets/. The router picks its basepath ('/next' or '/') at runtime.
//
// `--mode mock` (npm run dev:mock, build:mock) serves MSW's worker and turns
// the mocks on; production builds contain neither.
export default defineConfig(({ mode }) => {
  const mock = mode === 'mock'
  const api = process.env.DISMIST_API_ORIGIN ?? 'http://127.0.0.1:8088'
  return {
    base: '/',
    plugins: [
      // Options live in tsr.config.json so `npm run routes` generates the same tree.
      tanstackRouter({ target: 'react', autoCodeSplitting: true }),
      react(),
      tailwindcss(),
      mock ? msw({ mode: 'worker-only' }) : null,
      mock ? null : compression({ algorithms: ['gzip', 'brotliCompress'], exclude: [/\.keep$/, /\.html$/] }),
      keepStub(),
    ],
    resolve: { alias: { '@': path.resolve(import.meta.dirname, './src') } },
    build: {
      assetsDir: '_ui',
      sourcemap: false,
      target: 'es2022',
    },
    server: {
      proxy: mock
        ? undefined
        : {
            '/api': { target: api, changeOrigin: false },
            '/auth': { target: api, changeOrigin: false },
          },
    },
    test: {
      environment: 'jsdom',
      include: ['src/**/*.test.{ts,tsx}'],
      setupFiles: ['src/test/setup.ts'],
      restoreMocks: true,
    },
  }
})

// keepStub re-emits dist/_ui/.keep, so a local build leaves the committed
// stub file in place (only dist/index.html then differs from git).
function keepStub(): Plugin {
  return {
    name: 'dismist-keep-stub',
    apply: 'build',
    generateBundle() {
      this.emitFile({ type: 'asset', fileName: '_ui/.keep', source: '' })
    },
  }
}
