import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createPinia } from 'pinia'
import { nextTick } from 'vue'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import process from 'node:process'

// Layout.vue keeps pages alive, so FunctionDiff used to keep the previous
// function's record when another function's compare page was opened: the page
// said "other", compared with the old function's id ("deployment belongs to a
// different function") and Rollback would have acted on the wrong function.

window.matchMedia = window.matchMedia || ((query) => ({
  matches: false, media: query, onchange: null, dispatchEvent: () => false,
  addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
}))

const FN_A = { id: 'fn-a', name: 'alpha', runtime: 'node', entrypoint: 'handler.js', memory_mb: 64, cpus: 0.5 }
const FN_B = { id: 'fn-b', name: 'beta', runtime: 'node', entrypoint: 'handler.js', memory_mb: 64, cpus: 0.5 }
const dep = (id, n) => ({ id, version: n, status: 'succeeded', code_hash: `hash-${id}`, created_at: '2026-11-15T08:00:00Z' })

const endpoints = {
  listFunctions: vi.fn(async () => ({ data: { functions: [FN_A, FN_B], total: 2 } })),
  listDeployments: vi.fn(async (fnId) => ({
    data: { deployments: fnId === 'fn-a' ? [dep('a1', 1), dep('a2', 2)] : [dep('b1', 1), dep('b2', 2)] },
  })),
  compareDeployments: vi.fn(async () => ({ data: { files: [], from: { snapshot: {} }, to: { snapshot: {} } } })),
  getDeployment: vi.fn(async () => ({ data: {} })),
  rollbackFunction: vi.fn(async () => ({ data: {} })),
}
vi.mock('@/api/endpoints', () => ({ __esModule: true, ...endpoints }))

const OTHER = { render: () => null }
const Shell = {
  template: '<router-view v-slot="{ Component }"><keep-alive :max="10"><component :is="Component" /></keep-alive></router-view>',
}
const settle = async (times = 8) => {
  for (let i = 0; i < times; i++) { await nextTick(); await new Promise((r) => setTimeout(r, 0)) }
}

const mounted = []
beforeEach(() => { vi.clearAllMocks() })
afterEach(() => { while (mounted.length) mounted.pop().unmount() })

const diffRoute = (component) => ({
  path: '/functions/:name/diff',
  component,
  // Same shape as router/index.js (asserted below).
  props: (r) => ({ name: r.params.name, from: r.query.from || '', to: r.query.to || '' }),
})

describe('FunctionDiff under keep-alive', () => {
  it('reloads for the new function when another function\'s compare page is opened', async () => {
    const FunctionDiff = (await import('@/views/FunctionDiff.vue')).default
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [diffRoute(FunctionDiff), { path: '/:rest(.*)*', component: OTHER }],
    })
    router.push('/functions/alpha/diff?from=a1&to=a2')
    await router.isReady()
    const wrapper = mount(Shell, { global: { plugins: [router, createPinia()] } })
    mounted.push(wrapper)
    await settle()

    expect(endpoints.compareDeployments).toHaveBeenLastCalledWith('fn-a', 'a1', 'a2', 'json')
    expect(wrapper.text()).toContain('alpha')

    await router.push('/functions/beta/diff?from=b1&to=b2')
    await settle()

    // The pair must be compared against beta's id, never alpha's.
    expect(endpoints.compareDeployments).toHaveBeenLastCalledWith('fn-b', 'b1', 'b2', 'json')
    expect(endpoints.compareDeployments.mock.calls.some(([id, from]) => id === 'fn-a' && from === 'b1')).toBe(false)
    expect(endpoints.listDeployments).toHaveBeenLastCalledWith('fn-b', 100)
    expect(wrapper.text()).toContain('beta')
    expect(wrapper.text()).not.toContain('deployment belongs to a different function')
  })

  it('ignores another page\'s navigation while it is cached', async () => {
    const FunctionDiff = (await import('@/views/FunctionDiff.vue')).default
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [diffRoute(FunctionDiff), { path: '/:rest(.*)*', component: OTHER }],
    })
    router.push('/functions/alpha/diff?from=a1&to=a2')
    await router.isReady()
    mounted.push(mount(Shell, { global: { plugins: [router, createPinia()] } }))
    await settle()
    const before = endpoints.compareDeployments.mock.calls.length

    await router.push('/elsewhere?from=x&to=y')
    await settle()
    expect(endpoints.compareDeployments.mock.calls.length).toBe(before)
  })

  it('is registered with props, not read from useRoute', () => {
    const router = readFileSync(join(process.cwd(), 'src/router/index.js'), 'utf8')
    const line = router.split('\n').find((l) => l.includes("name: 'function-diff'"))
    expect(line).toMatch(/props: \(r\) => \(\{ name: r\.params\.name, from: r\.query\.from \|\| '', to: r\.query\.to \|\| '' \}\)/)
    const view = readFileSync(join(process.cwd(), 'src/views/FunctionDiff.vue'), 'utf8')
    expect(view).not.toMatch(/= useRoute\(\)/)
  })
})
