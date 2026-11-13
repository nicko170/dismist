import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'node:path'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, './src') } },
  server: {
    proxy: {
      '/api': process.env.DISMIST_API_ORIGIN ?? 'http://127.0.0.1:8088',
      '/auth': process.env.DISMIST_API_ORIGIN ?? 'http://127.0.0.1:8088',
    },
  },
})
