import { defineConfig, mergeConfig } from 'vite'
import base from './vite.config.js'

// Separate scratch entry only. The production router and build stay unchanged.
export default mergeConfig(base, defineConfig({
  base: '/',
  build: { outDir: '../build/live-iam', emptyOutDir: true, rollupOptions: { input: 'live-iam.html' } },
  server: { host: '0.0.0.0', port: 4300, strictPort: true, allowedHosts: ['sandbox.l3b.cc.cd'],
    proxy: { '/fn': 'http://localhost:8443' } },
}))
