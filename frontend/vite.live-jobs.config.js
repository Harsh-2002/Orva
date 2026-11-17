import { defineConfig, mergeConfig } from 'vite'
import base from './vite.config.js'

export default mergeConfig(base, defineConfig({
  base: '/',
  build: { outDir: '../build/live-jobs', emptyOutDir: true, rollupOptions: { input: 'live-jobs.html' } },
  server: {
    host: '127.0.0.1', port: 4300, strictPort: true,
    proxy: { '/api': process.env.ORVA_JOBS_BACKEND || 'http://127.0.0.1:8443' },
  },
}))
