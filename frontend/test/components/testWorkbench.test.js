import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createPinia } from 'pinia'
import { nextTick } from 'vue'

// The editor's "Function logs" panel rendered, badge-counted and prompt-stitched
// a list nothing ever assigned, and no test noticed for two releases. Both log
// paths are asserted here so the same silence cannot happen twice.

window.matchMedia = window.matchMedia || ((query) => ({
  matches: false, media: query, onchange: null, dispatchEvent: () => false,
  addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
}))

const FN_A = { id: 'fn-a', name: 'alpha', runtime: 'python', code_hash: 'ha', active_deployment_id: 'd1' }
const FN_NEW = { id: 'fn-n', name: 'fresh', runtime: 'python', code_hash: '', active_deployment_id: '' }

const serverError = () => Object.assign(new Error('Request failed with status code 500'), {
  response: {
    status: 500,
    data: '{"error":"boom"}',
    headers: { 'x-orva-duration-ms': '42', 'x-orva-execution-id': 'ex-1' },
  },
})

let listFail = false
const endpoints = {
  listFunctions: vi.fn(async () => {
    if (listFail) throw new Error('network is down')
    // Fresh objects per call, the way the API answers.
    return { data: { functions: [{ ...FN_A }, { ...FN_NEW }] } }
  }),
  listFixtures: vi.fn(async () => ({ data: { fixtures: [{ id: 'fx1', name: 'happy-path', method: 'POST', path: '/', headers: { A: 'b' }, body: '{}' }] } })),
  updateFixture: vi.fn(async () => ({ data: {} })),
  deleteFixture: vi.fn(async () => ({ data: {} })),
  // fnClient (src/api/client.js) sets no validateStatus, so axios REJECTS every
  // 4xx/5xx. Mocking a 500 as a resolved response drives the store's success
  // branch, which is not the branch a 500 takes.
  invokeFunctionFull: vi.fn(async () => { throw serverError() }),
  getInvocationLogs: vi.fn(async () => ({
    data: {
      stderr: 'Traceback (most recent call last):\n  File "handler.py"\nZeroDivisionError',
      log_entries: [{ id: 1, ts: '2026-08-31T10:00:00.5Z', level: 'error', message: 'divide failed', fields: '{"n":0}' }],
    },
  })),
}
vi.mock('@/api/endpoints', () => ({ __esModule: true, ...endpoints }))
vi.mock('@/api/client', () => ({ default: {}, getApiKey: () => 'k' }))

const OTHER = { render: () => null }
const Shell = {
  template: '<router-view v-slot="{ Component }"><keep-alive :max="10"><component :is="Component" /></keep-alive></router-view>',
}
// The log poll retries on real timers until the execution row is written, so a
// fixed number of ticks cannot reach its settled state. Wait for the text.
const waitForText = async (wrapper, text, ms = 4000) => {
  const deadline = Date.now() + ms
  while (Date.now() < deadline) {
    await nextTick()
    if (wrapper.text().includes(text)) return true
    await new Promise((r) => setTimeout(r, 50))
  }
  return false
}

const settle = async (n = 8) => {
  for (let i = 0; i < n; i++) { await nextTick(); await new Promise((r) => setTimeout(r, 0)) }
}

// TestWorkbench binds a window keydown in onActivated. A wrapper left mounted
// keeps answering the next test's keystroke, which is why the sibling
// keep-alive suite unmounts too: without this, one Ctrl+Enter fired seven
// invocations.
const mounted = []
beforeEach(() => { vi.clearAllMocks() })
afterEach(() => { while (mounted.length) mounted.pop().unmount() })

const boot = async (path) => {
  const TestWorkbench = (await import('@/views/TestWorkbench.vue')).default
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/functions/:name/test', name: 'function-test', component: TestWorkbench, props: true },
      { path: '/:rest(.*)*', component: OTHER },
    ],
  })
  router.push(path)
  await router.isReady()
  const wrapper = mount(Shell, { global: { plugins: [router, createPinia()] } })
  mounted.push(wrapper)
  await settle()
  return { wrapper, go: async (to) => { await router.push(to); await settle() } }
}

const ctrlEnter = () => window.dispatchEvent(
  new window.KeyboardEvent('keydown', { key: 'Enter', ctrlKey: true, bubbles: true, cancelable: true }))

describe('TestWorkbench', () => {
  it('renders the request bench for a deployed function', async () => {
    const { wrapper } = await boot('/functions/alpha/test')
    expect(wrapper.text()).toContain('Invoke URL')
    expect(wrapper.text()).toContain('/fn/fn-a')
    expect(wrapper.text()).toContain('happy-path')
    expect(wrapper.text()).toContain('No runs yet')
    expect(wrapper.find('#tw-path').element.value).toBe('/')
    expect(wrapper.find('#tw-body').element.value).toBe('{"name": "World"}')
    expect(wrapper.find('select').exists()).toBe(false)
  })

  it('says a never-deployed function cannot be invoked', async () => {
    const { wrapper } = await boot('/functions/fresh/test')
    expect(wrapper.text()).toContain('has no deployed version')
  })

  it('reports a failed lookup instead of an unknown function', async () => {
    listFail = true
    const { wrapper } = await boot('/functions/alpha/test')
    listFail = false
    expect(wrapper.text()).toContain('could not be loaded')
    expect(wrapper.text()).toContain('network is down')
    expect(wrapper.text()).not.toContain('No function named')
  })

  it('names an unknown function without claiming a load failure', async () => {
    const { wrapper } = await boot('/functions/ghost/test')
    expect(wrapper.text()).toContain('No function named ghost')
    expect(wrapper.text()).not.toContain('could not be loaded')
  })

  it('shows both log paths and the run in history after a failed run', async () => {
    const { wrapper } = await boot('/functions/alpha/test')
    const runBtn = wrapper.findAll('button').find((b) => b.text().includes('Run'))
    await runBtn.trigger('click')
    await settle()
    expect(wrapper.text()).toContain('Server error')
    expect(wrapper.text()).toContain('42 ms')
    expect(wrapper.text()).toContain('ZeroDivisionError')
    expect(wrapper.text()).toContain('divide failed')
    expect(wrapper.text()).toContain('{"n":0}')
    expect(wrapper.text()).toContain('Structured logs')
    expect(wrapper.text()).toContain('Console output')
    expect(wrapper.text()).not.toContain('No runs yet')
  })

  it('follows the function in the URL', async () => {
    const { wrapper, go } = await boot('/functions/alpha/test')
    await go('/functions/fresh/test')
    expect(wrapper.text()).toContain('fresh')
    expect(wrapper.text()).toContain('has no deployed version')
  })

  it('re-reads the function on the way back, so a deploy made elsewhere lands', async () => {
    const { wrapper, go } = await boot('/functions/fresh/test')
    expect(wrapper.text()).toContain('has no deployed version')
    await go('/elsewhere')
    FN_NEW.code_hash = 'now-deployed'
    await go('/functions/fresh/test')
    FN_NEW.code_hash = ''
    expect(wrapper.text()).not.toContain('has no deployed version')
  })

  it('edits headers through the store request', async () => {
    const { wrapper } = await boot('/functions/alpha/test')
    const add = wrapper.findAll('button').find((b) => b.text().includes('Add header'))
    await add.trigger('click')
    await settle(2)
    const key = wrapper.find('#tw-header-name-0')
    expect(key.exists()).toBe(true)
    await key.setValue('X-Test')
    await settle(2)
    expect(wrapper.find('#tw-header-name-0').element.value).toBe('X-Test')
  })

  it('separates "logged nothing" from "logs failed to load", and can retry', async () => {
    endpoints.getInvocationLogs.mockResolvedValue({ data: { stderr: '', log_entries: [] } })
    const { wrapper } = await boot('/functions/alpha/test')
    await wrapper.findAll('button').find((b) => b.text().includes('Run')).trigger('click')
    expect(await waitForText(wrapper, 'This run logged nothing')).toBe(true)
    expect(wrapper.text()).not.toContain('could not be loaded')

    endpoints.getInvocationLogs.mockRejectedValueOnce(new Error('logs endpoint 503'))
    await wrapper.findAll('button').find((b) => b.text().includes('Reload')).trigger('click')
    await settle()
    expect(wrapper.text()).toContain('Function logs could not be loaded')
    expect(wrapper.text()).toContain('logs endpoint 503')

    // Back to the file's default fixture: mockResolvedValue above replaced it
    // for the whole test, and this phase needs a run that did log something.
    endpoints.getInvocationLogs.mockResolvedValue({
      data: {
        stderr: 'Traceback (most recent call last):\n  File "handler.py"\nZeroDivisionError',
        log_entries: [{ id: 1, ts: '2026-08-31T10:00:00.5Z', level: 'error', message: 'divide failed', fields: '{"n":0}' }],
      },
    })
    await wrapper.findAll('button').find((b) => b.text().includes('Reload')).trigger('click')
    expect(await waitForText(wrapper, 'ZeroDivisionError')).toBe(true)
  })

  it('says a transport failure produced no execution record', async () => {
    endpoints.invokeFunctionFull.mockRejectedValueOnce(new Error('socket hang up'))
    const { wrapper } = await boot('/functions/alpha/test')
    await wrapper.findAll('button').find((b) => b.text().includes('Run')).trigger('click')
    await settle()
    expect(wrapper.text()).toContain('socket hang up')
    expect(wrapper.text()).toContain('no execution id back')
    expect(wrapper.text()).toContain('Never answered')
  })

  it('lets an earlier run be inspected without losing the way back', async () => {
    const { wrapper } = await boot('/functions/alpha/test')
    const runBtn = () => wrapper.findAll('button').find((b) => b.text().includes('Run'))
    await runBtn().trigger('click')
    await settle()
    endpoints.invokeFunctionFull.mockResolvedValueOnce({
      status: 200, data: '{"ok":true}', headers: { 'x-orva-duration-ms': '7', 'x-orva-execution-id': 'ex-2' },
    })
    await runBtn().trigger('click')
    await settle()
    expect(wrapper.text()).toContain('"ok": true')

    const older = wrapper.findAll('button').filter((b) => b.text().includes('Server error'))
    await older[older.length - 1].trigger('click')
    await settle()
    expect(wrapper.text()).toContain('an earlier run, not the latest')
    expect(wrapper.text()).toContain('"error": "boom"')
    await wrapper.findAll('button').find((b) => b.text().includes('Back to latest')).trigger('click')
    await settle()
    expect(wrapper.text()).toContain('"ok": true')
  })

  it('never claims the handler logged nothing when the log fetch failed', async () => {
    // Start from a run that genuinely returned nothing, so the two per-section
    // "None from this run" lines are the ones on screen when the retry fails.
    endpoints.getInvocationLogs.mockResolvedValue({ data: { stderr: '', log_entries: [] } })
    const { wrapper } = await boot('/functions/alpha/test')
    await wrapper.findAll('button').find((b) => b.text().includes('Run')).trigger('click')
    expect(await waitForText(wrapper, 'None from this run')).toBe(true)
    endpoints.getInvocationLogs.mockRejectedValueOnce(new Error('logs endpoint 503'))
    await wrapper.findAll('button').find((b) => b.text().includes('Reload')).trigger('click')
    await settle()
    expect(wrapper.text()).toContain('Function logs could not be loaded')
    // A fetch that failed knows nothing about what the handler wrote. Saying
    // both at once is the empty-state lie LoadError exists to prevent.
    expect(wrapper.text()).not.toContain('None from this run')
  })

  it('runs on Ctrl or Cmd + Enter, and stops once the view is left', async () => {
    const { go } = await boot('/functions/alpha/test')
    ctrlEnter()
    await settle()
    expect(endpoints.invokeFunctionFull).toHaveBeenCalledTimes(1)
    await go('/elsewhere')
    ctrlEnter()
    await settle()
    expect(endpoints.invokeFunctionFull).toHaveBeenCalledTimes(1)
    await go('/functions/alpha/test')
    ctrlEnter()
    await settle()
    expect(endpoints.invokeFunctionFull).toHaveBeenCalledTimes(2)
  })

  it('loads a fixture into the request', async () => {
    const { wrapper } = await boot('/functions/alpha/test')
    const fx = wrapper.findAll('button').find((b) => b.text().includes('happy-path'))
    await fx.trigger('click')
    await settle(2)
    expect(wrapper.find('#tw-header-name-0').element.value).toBe('A')
    expect(wrapper.find('#tw-header-value-0').element.value).toBe('b')
  })
})
