import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createPinia } from 'pinia'
import { nextTick } from 'vue'

// The editor's one-line strip is the only place a test run is visible without
// leaving the editor, so what it picks to show IS the feature. Every case here
// failed before: a successful JSON response rendered `{`, a Node failure
// rendered `at process.processTicksAndRejections`, a run whose only output was
// orva.log.* rendered nothing at all, and a 404 drew a green success dot.

window.matchMedia = window.matchMedia || ((q) => ({
  matches: false, media: q, onchange: null, dispatchEvent: () => false,
  addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
}))

const FN = { id: 'fn-a', name: 'alpha', runtime: 'node', entrypoint: 'handler.js', memory_mb: 64, cpus: 0.5, code_hash: 'ha' }
const apiClient = {
  get: vi.fn(async (url) => {
    if (url === '/functions') return { data: { functions: [FN] } }
    if (url.endsWith('/source')) return { data: { code: 'x', dependencies: '' } }
    return { data: {} }
  }),
  post: vi.fn(async () => ({ data: {} })),
  put: vi.fn(async () => ({ data: {} })),
  delete: vi.fn(async () => ({ data: {} })),
}
vi.mock('@/api/client', () => ({ default: apiClient, getApiKey: () => 'k' }))

const INVOKE = vi.fn()
const LOGS = vi.fn()
vi.mock('@/api/endpoints', () => ({
  __esModule: true,
  listFunctions: vi.fn(async () => ({ data: { functions: [FN], total: 1 } })),
  listDeployments: vi.fn(async () => ({ data: { deployments: [] } })),
  rollbackFunction: vi.fn(async () => ({ data: {} })),
  listFixtures: vi.fn(async () => ({ data: { fixtures: [] } })),
  updateFixture: vi.fn(async () => ({ data: {} })),
  deleteFixture: vi.fn(async () => ({ data: {} })),
  invokeFunctionFull: (...a) => INVOKE(...a),
  getInvocationLogs: (...a) => LOGS(...a),
  listRoutes: vi.fn(async () => ({ data: { routes: [] } })),
  setRoute: vi.fn(async () => ({ data: {} })),
  deleteRoute: vi.fn(async () => ({ data: {} })),
}))
vi.mock('@/components/common/CodeEditor.vue', async () => {
  const { h } = await import('vue')
  return {
    __esModule: true,
    default: {
      props: { modelValue: { type: String, default: '' }, language: { type: String, default: '' } },
      setup: (p) => () => h('pre', p.modelValue),
    },
  }
})

const OTHER = { render: () => null }
const settle = async (n = 10) => { for (let i = 0; i < n; i++) { await nextTick(); await new Promise((r) => setTimeout(r, 0)) } }

// Returns the strip after one Run: its excerpt and the status readout's tone,
// which is the whole of what the strip promises. There is no status word or
// dot -- the status code itself is drawn in the tone.
const runOnce = async () => {
  const Editor = (await import('@/views/Editor.vue')).default
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/functions/:name', name: 'function-detail', component: Editor, props: true },
      { path: '/functions/:name/test', name: 'function-test', component: OTHER },
      { path: '/ai', name: 'ai', component: OTHER },
      { path: '/:r(.*)*', component: OTHER },
    ],
  })
  router.push('/functions/alpha')
  await router.isReady()
  const wrapper = mount(
    { template: '<router-view v-slot="{ Component }"><keep-alive><component :is="Component"/></keep-alive></router-view>' },
    { global: { plugins: [router, createPinia()] } },
  )
  await settle()
  await wrapper.find('button.run-btn').trigger('click')
  // The run lands in history immediately and the logs poll in behind it, so the
  // strip's log-derived excerpt needs the extra ticks that poll resolves in.
  await settle(30)
  const live = wrapper.find('[role="status"]')
  return {
    wrapper,
    live,
    excerpt: live.find('code').exists() ? live.find('code').text() : '',
    // The status/duration readout, e.g. "200 · 12ms", and the class carrying
    // its tone. Class rather than word: the code is the word.
    meta: live.find('span.font-mono')?.text() ?? '',
    tone: live.find('span.font-mono')?.attributes('class') ?? '',
  }
}

const ok = (status, data, headers = {}) => {
  INVOKE.mockResolvedValue({ status, data, headers: { 'x-orva-duration-ms': '12', 'x-orva-execution-id': 'e1', ...headers } })
}
const fail = (status, data) => {
  INVOKE.mockRejectedValue({ response: { status, data, headers: { 'x-orva-duration-ms': '9', 'x-orva-execution-id': 'e1' } } })
}
const logs = (stderr, entries = []) => LOGS.mockResolvedValue({ data: { stderr, log_entries: entries } })

describe('editor result strip', () => {
  it('shows the whole answer on one line, not the first line of pretty JSON', async () => {
    ok(200, { message: 'Hello, World' })
    logs('')
    const { excerpt, meta, tone } = await runOnce()
    expect(excerpt).toBe('{ "message": "Hello, World" }')
    expect(meta).toBe('200 · 12ms')
    expect(tone).toContain('text-success-fg')
  })

  // The invoke handler answers a timeout or a pool rejection itself and sets
  // no duration header. It used to render "503 · ?ms", which reads as a
  // measurement rather than an absence.
  it('reports no duration rather than inventing one, when the header is absent', async () => {
    INVOKE.mockRejectedValue({ response: { status: 503, data: '', headers: { 'x-orva-execution-id': 'e1' } } })
    logs('')
    const { meta, tone } = await runOnce()
    expect(meta).toBe('503')
    expect(meta).not.toContain('?')
    expect(tone).toContain('text-danger-fg')
  })

  it('picks the exception, not the last stack frame, out of a Node traceback', async () => {
    fail(500, '')
    logs([
      '/var/task/handler.js:3',
      "    throw new Error('boom')",
      '    ^',
      '',
      'Error: boom',
      '    at handler (/var/task/handler.js:3:11)',
      '    at process.processTicksAndRejections (node:internal/process/task_queues:105:5)',
    ].join('\n'))
    const { excerpt, live, tone } = await runOnce()
    expect(excerpt).toBe('Error: boom')
    expect(tone).toContain('text-danger-fg')
    // ...and says there is more to read, so the workbench link has a reason.
    expect(live.text()).toContain('6 log lines')
  })

  it('picks the exception out of a Python traceback too', async () => {
    fail(500, '')
    logs('Traceback (most recent call last):\n  File "/var/task/handler.py", line 4, in handler\n    return payload["name"]\nKeyError: \'name\'\n')
    const { excerpt } = await runOnce()
    expect(excerpt).toBe("KeyError: 'name'")
  })

  it('surfaces an orva.log.* entry, level marked, when it is the only output', async () => {
    ok(204, '')
    logs('', [{ level: 'warn', message: 'kv miss, recomputed' }])
    const { excerpt } = await runOnce()
    expect(excerpt).toBe('warn: kv miss, recomputed')
  })

  it('prefers a structured error over raw stderr on a failure', async () => {
    fail(500, '')
    logs('Error: boom\n    at handler (/var/task/handler.js:3:11)\n', [
      { level: 'error', message: 'validation failed for field name' },
    ])
    const { excerpt } = await runOnce()
    expect(excerpt).toBe('error: validation failed for field name')
  })

  it('does not paint a 404 as a green success', async () => {
    fail(404, 'no such user')
    logs('')
    const { meta, tone } = await runOnce()
    expect(meta).toBe('404 · 9ms')
    expect(tone).toContain('text-warning-fg')
    expect(tone).not.toContain('text-success-fg')
  })

  it('says a silent run was silent instead of pointing at logs that do not exist', async () => {
    ok(204, '')
    logs('')
    const { excerpt, live } = await runOnce()
    expect(excerpt).toBe('')
    expect(live.text()).toContain('This run printed nothing')
    expect(live.text()).not.toContain('full logs live in the workbench')
  })
})
