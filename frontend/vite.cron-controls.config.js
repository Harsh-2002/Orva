import { defineConfig, mergeConfig } from 'vite'
import base from './vite.config.js'

// Qualification entry only; the normal application router/build is unchanged.
export default mergeConfig(base, defineConfig({
  base: '/',
  build: { outDir: '../build/cron-controls-ui', emptyOutDir: true, rollupOptions: { input: 'cron-controls.html' } },
}))
