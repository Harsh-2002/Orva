import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

export default defineConfig({
  plugins: [vue()],
  base: '/web/',
  build: {
    // The lazy-loaded editor vendor chunk contains CodeMirror's language data
    // (about 542 kB raw / 185 kB gzip). Keep a tight explicit ceiling so the
    // build stays warning-free while still catching meaningful bundle growth.
    chunkSizeWarningLimit: 600,
  },
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url))
    }
  },
  server: {
    host: true,
    port: 3000,
    proxy: {
      '/api': 'http://localhost:8443',
      '/auth': 'http://localhost:8443'
    }
  }
})
