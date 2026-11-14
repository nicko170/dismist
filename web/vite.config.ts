import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

// The legacy app is served at /legacy/ from M1 (design §2.4): built once
// with this base so its assets live at /legacy/assets/, apart from the v1
// app's /_ui/. It is deleted in the release that ships the typed edit
// screens (M2).
export default defineConfig({
  base: '/legacy/',
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, './src') } },
  server: {
    proxy: {
      '/api': process.env.DISMIST_API_ORIGIN ?? 'http://127.0.0.1:8088',
      '/auth': process.env.DISMIST_API_ORIGIN ?? 'http://127.0.0.1:8088',
    },
  },
})
