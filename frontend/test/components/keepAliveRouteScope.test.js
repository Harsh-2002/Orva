import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createPinia } from 'pinia'
import { nextTick } from 'vue'

// Editor.vue reads matchMedia during setup to decide whether the console
// starts open; jsdom does not implement it.
window.matchMedia = window.matchMedia || ((query) => ({
  matches: false, media: query, onchange: null, dispatchEvent: () => false,
  addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
}))

const FN_A = { id: 'fn-a', name: 'alpha', runtime: 'python', entrypoint: 'handler.py', memory_mb: 64, cpus: 0.5, code_hash: 'ha' }
const FN_B = { id: 'fn-b', name: 'beta', runtime: 'python', entrypoint: 'handler.py', memory_mb: 64, cpus: 0.5, code_hash: 'hb' }

const apiClient = {
  get: vi.fn(async (url) => {
    if (url === '/functions') return { data: { functions: [FN_A, FN_B] } }
    if (url.endsWith('/source')) return { data: { code: `source of ${url}`, dependencies: '' } }
    return { data: {} }
  }),
  post: vi.fn(async () => ({ data: {} })),
  put: vi.fn(async () => ({ data: {} })),
  delete: vi.fn(async () => ({ data: {} })),
}
vi.mock('@/api/client', () => ({ default: apiClient, getApiKey: () => 'test-key' }))

const endpoints = {
  listFunctions: vi.fn(async () => ({ data: { functions: [FN_A, FN_B], total: 2 } })),
  listInboundWebhooks: vi.fn(async () => ({ data: { inbound_webhooks: [] } })),
  createInboundWebhook: vi.fn(async () => ({ data: {} })),
  deleteInboundWebhook: vi.fn(async () => ({ data: {} })),
  updateInboundWebhook: vi.fn(async () => ({ data: {} })),
  signInboundWebhook: vi.fn(async () => ({ data: {} })),
  listDeployments: vi.fn(async () => ({ data: { deployments: [] } })),
  getDeployment: vi.fn(async () => ({ data: {} })),
  getDeploymentLogs: vi.fn(async () => ({ data: { logs: [] } })),
  rollbackFunction: vi.fn(async () => ({ data: {} })),
  listFixtures: vi.fn(async () => ({ data: { fixtures: [] } })),
  updateFixture: vi.fn(async () => ({ data: {} })),
  deleteFixture: vi.fn(async () => ({ data: {} })),
  invokeFunctionFull: vi.fn(async () => ({ data: {} })),
  listRoutes: vi.fn(async () => ({ data: { routes: [] } })),
  setRoute: vi.fn(async () => ({ data: {} })),
  deleteRoute: vi.fn(async () => ({ data: {} })),
  getTrace: vi.fn(async (id) => ({
    data: { trace_id: id, status: 'success', total_duration_ms: 1, span_count: 0, spans: [], user_spans: [], log_entries: [] },
  })),
}
vi.mock('@/api/endpoints', () => ({ __esModule: true, ...endpoints }))

// Editor.vue pulls CodeMirror in through defineAsyncComponent; the stub keeps
// the v-model contract so the buffer is readable. __esModule is what makes
// defineAsyncComponent unwrap `default` instead of handing Vue the namespace.
vi.mock('@/components/common/CodeEditor.vue', async () => {
  const { h } = await import('vue')
  return {
    __esModule: true,
    default: {
      name: 'CodeEditorStub',
      props: { modelValue: { type: String, default: '' }, language: { type: String, default: '' } },
      emits: ['update:modelValue'],
      setup(props, { emit }) {
        return () => h('pre', { 'data-code': '', onClick: () => emit('update:modelValue', 'UNSAVED WORK') }, props.modelValue)
      },
    },
  }
})

const OTHER = { render: () => null }

// Mirrors Layout.vue: one un-keyed keep-alive around the router-view.
const Shell = {
  template: '<router-view v-slot="{ Component }"><keep-alive :max="10"><component :is="Component" /></keep-alive></router-view>',
}

// A macrotask between ticks: the code buffer arrives through an async
// component and two chained awaits, neither of which a microtask flush covers.
const settle = async (times = 8) => {
  for (let i = 0; i < times; i++) { await nextTick(); await new Promise((r) => setTimeout(r, 0)) }
}

// Every view here binds window listeners; a wrapper left mounted keeps
// answering the next test's orva:deploy.
const mounted = []

const boot = async (routes, path) => {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [...routes, { path: '/elsewhere', component: OTHER }, { path: '/:rest(.*)*', component: OTHER }],
  })
  router.push(path)
  await router.isReady()
  const wrapper = mount(Shell, { global: { plugins: [router, createPinia()] } })
  mounted.push(wrapper)
  await settle()
  return { wrapper, go: async (to) => { await router.push(to); await settle() } }
}

beforeEach(() => { vi.clearAllMocks() })
afterEach(() => { while (mounted.length) mounted.pop().unmount() })

describe('keep-alive route scoping', () => {
  it('Editor keeps an unsaved buffer when the route loses :name, and reloads only for a different function', async () => {
    const Editor = (await import('@/views/Editor.vue')).default
    const { wrapper, go } = await boot([
      { path: '/functions/new', name: 'function-new', component: Editor, props: true },
      { path: '/functions/:name', name: 'function-detail', component: Editor, props: true },
    ], '/functions/alpha')

    const buffer = () => wrapper.find('[data-code]').text()
    expect(buffer()).toBe('source of /functions/fn-a/source')

    await wrapper.find('[data-code]').trigger('click')
    await settle()
    expect(buffer()).toBe('UNSAVED WORK')

    // Leaving for a route with no :name only deactivates the Editor. A cached
    // instance must not read someone else's navigation as "create mode".
    await go('/elsewhere')
    await go('/functions/alpha')
    expect(buffer()).toBe('UNSAVED WORK')

    // Opening a different function does replace the buffer.
    await go('/functions/beta')
    expect(buffer()).toBe('source of /functions/fn-b/source')

    // ...and /functions/new, which reuses this same instance under a different
    // route record, still gets a fresh create-mode slate rather than beta's.
    await go('/functions/new')
    expect(buffer().startsWith('import json')).toBe(true)
  })

  it('Editor applies a ?prefill deep link to the function it is already showing', async () => {
    const Editor = (await import('@/views/Editor.vue')).default
    const { wrapper, go } = await boot([
      { path: '/functions/:name', name: 'function-detail', component: Editor, props: true },
    ], '/functions/alpha')

    // The real flow: edit alpha, go to Invocations, "Save as fixture" pushes
    // base64(JSON) of the captured request back onto that same function.
    await go('/elsewhere')
    const envelope = btoa(JSON.stringify({ method: 'PUT', path: '/replay', body: '{"a":1}' }))
    await go(`/functions/alpha?prefill=${envelope}`)

    expect(wrapper.find('input[placeholder="/"]').element.value).toBe('/replay')
    expect(wrapper.find('textarea[placeholder="{}"]').element.value).toBe('{"a":1}')
  })

  it('Editor applies a ?prefill deep link aimed at a function it is not holding', async () => {
    const Editor = (await import('@/views/Editor.vue')).default
    const { wrapper, go } = await boot([
      { path: '/functions/:name', name: 'function-detail', component: Editor, props: true },
    ], '/functions/alpha')

    // Same flow, but the captured request belongs to another function. The
    // prop change runs resetEditorState pre-flush, which wiped a prefill
    // applied by a pre-flush watcher -- and the param is stripped on apply,
    // so there is nothing left to retry from.
    await go('/elsewhere')
    const envelope = btoa(JSON.stringify({ method: 'PUT', path: '/replay', body: '{"a":1}' }))
    await go(`/functions/beta?prefill=${envelope}`)

    expect(wrapper.find('[data-code]').text()).toBe('source of /functions/fn-b/source')
    expect(wrapper.find('input[placeholder="/"]').element.value).toBe('/replay')
    expect(wrapper.find('textarea[placeholder="{}"]').element.value).toBe('{"a":1}')
  })

  it('Editor ignores orva:deploy while it is cached behind another view', async () => {
    const Editor = (await import('@/views/Editor.vue')).default
    const { go } = await boot([
      { path: '/functions/:name', name: 'function-detail', component: Editor, props: true },
      { path: '/functions/:name/kv', name: 'function-kv', component: OTHER },
    ], '/functions/alpha')

    window.dispatchEvent(new CustomEvent('orva:deploy'))
    await settle()
    expect(apiClient.put).toHaveBeenCalledTimes(1)

    await go('/functions/alpha/kv')
    window.dispatchEvent(new CustomEvent('orva:deploy'))
    await settle()
    expect(apiClient.put).toHaveBeenCalledTimes(1)
  })

  it('Deployments follows the function in the URL', async () => {
    const Deployments = (await import('@/views/Deployments.vue')).default
    const { wrapper, go } = await boot([
      { path: '/functions/:name/deployments', name: 'function-deployments', component: Deployments, props: true },
    ], '/functions/alpha/deployments')
    expect(endpoints.listDeployments).toHaveBeenLastCalledWith('fn-a', 100)

    await go('/functions/beta/deployments')
    expect(endpoints.listDeployments).toHaveBeenLastCalledWith('fn-b', 100)
    expect(wrapper.text()).toContain('beta')
  })

  it('InboundWebhooks follows the function in the URL', async () => {
    const InboundWebhooks = (await import('@/views/InboundWebhooks.vue')).default
    const { go } = await boot([
      { path: '/functions/:name/inbound-webhooks', name: 'function-inbound-webhooks', component: InboundWebhooks, props: true },
    ], '/functions/alpha/inbound-webhooks')
    expect(endpoints.listInboundWebhooks).toHaveBeenLastCalledWith('alpha')

    await go('/functions/beta/inbound-webhooks')
    expect(endpoints.listInboundWebhooks).toHaveBeenLastCalledWith('beta')
  })

  it('TraceDetail renders the trace in the URL, not the one before it', async () => {
    const TraceDetail = (await import('@/views/TraceDetail.vue')).default
    const { wrapper, go } = await boot([
      { path: '/traces/:id', name: 'trace-detail', component: TraceDetail, props: true },
    ], '/traces/trace-one')
    expect(wrapper.text()).toContain('trace-one')

    await go('/traces/trace-two')
    expect(wrapper.text()).toContain('trace-two')
    expect(wrapper.text()).not.toContain('trace-one')
  })

  it('FunctionsList reports a failed load instead of the first-run empty state', async () => {
    endpoints.listFunctions.mockRejectedValue(new Error('network is down'))
    const FunctionsList = (await import('@/views/FunctionsList.vue')).default
    const { wrapper } = await boot([{ path: '/functions', name: 'functions', component: FunctionsList }], '/functions')

    expect(wrapper.text()).not.toContain('No functions deployed yet')
    expect(wrapper.text()).toContain('could not be loaded')
    expect(wrapper.text()).toContain('network is down')
  })
})
