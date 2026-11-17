import { defineConfig, mergeConfig } from 'vite'
import base from './vite.config.js'
export default mergeConfig(base, defineConfig({
  base: '/',
  build: { outDir: '../build/managed-iam', emptyOutDir: true, rollupOptions: { input: 'managed-iam.html' } },
  server: { host: '0.0.0.0', port: 4300, strictPort: true, proxy: { '/fn': 'http://localhost:8443' } },
}))
