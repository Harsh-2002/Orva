import { mount } from '@vue/test-utils'
import { describe, expect, it, vi, beforeEach } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createPinia } from 'pinia'
import { nextTick } from 'vue'

// A build is headless: Deploy carries its own progress and the log modal is
// raised by a failure or on demand, never by starting. Most builds finish in
// under a second, and a dialog that opens and closes inside that window is a
// flash. The properties under test: nothing opens on deploy, the strip reports
// progress, a failure raises the log by itself, and dismissing the log never
// takes the build down with it.

window.matchMedia = window.matchMedia || ((q) => ({
  matches: false, media: q, onchange: null, dispatchEvent: () => false,
  addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
}))

const FN = { id: 'fn-a', name: 'alpha', runtime: 'node', entrypoint: 'handler.js', memory_mb: 64, cpus: 0.5, code_hash: 'ha' }
const FN2 = { id: 'fn-b', name: 'beta', runtime: 'python', entrypoint: 'handler.py', memory_mb: 64, cpus: 0.5, code_hash: 'hb' }
const apiClient = {
  get: vi.fn(async (url) => {
    if (url === '/functions') return { data: { functions: [FN, FN2] } }
    if (url.endsWith('/source')) return { data: { code: 'exports.handler = () => {}', dependencies: '' } }
    if (url.includes('/deployments')) return { data: { deployments: [] } }
    return { data: {} }
  }),
  post: vi.fn(async () => ({ data: { deployment_id: 'dep-1' } })),
  put: vi.fn(async () => ({ data: {} })),
  delete: vi.fn(async () => ({ data: {} })),
}
vi.mock('@/api/client', () => ({ default: apiClient, getApiKey: () => 'k' }))

vi.mock('@/api/endpoints', () => ({
  __esModule: true,
  listFunctions: vi.fn(async () => ({ data: { functions: [FN, FN2], total: 2 } })),
  listDeployments: vi.fn(async () => ({ data: { deployments: [] } })),
  rollbackFunction: vi.fn(async () => ({ data: {} })),
  listFixtures: vi.fn(async () => ({ data: { fixtures: [] } })),
  updateFixture: vi.fn(async () => ({ data: {} })),
  deleteFixture: vi.fn(async () => ({ data: {} })),
  invokeFunctionFull: vi.fn(),
  getInvocationLogs: vi.fn(),
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

// The stream stands in for the server: the test decides when a log line, a
// success or a failure arrives, which is the only way to land an event AFTER
// the operator has dismissed the modal.
let stream = null
class FakeEventSource {
  static CLOSED = 2
  constructor(url) {
    this.url = url
    this.readyState = 1
    this.listeners = {}
    this.closed = false
    stream = this
  }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn) }
  close() { this.closed = true; this.readyState = 2 }
  emit(type, data) {
    for (const fn of this.listeners[type] || []) fn({ data: JSON.stringify(data) })
  }
}

const OTHER = { render: () => null }
const settle = async (n = 12) => { for (let i = 0; i < n; i++) { await nextTick(); await new Promise((r) => setTimeout(r, 0)) } }

const dialog = () => document.body.querySelector('[role="dialog"]')
const dialogText = () => dialog()?.textContent.replace(/\s+/g, ' ').trim() || ''

const openEditor = async () => {
  const Editor = (await import('@/views/Editor.vue')).default
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/functions/:name', name: 'function-detail', component: Editor, props: true },
      { path: '/functions/:name/test', name: 'function-test', component: OTHER },
      { path: '/functions/:name/deployments', name: 'function-deployments', component: OTHER },
      { path: '/ai', name: 'ai', component: OTHER },
      { path: '/:r(.*)*', component: OTHER },
    ],
  })
  router.push('/functions/alpha')
  await router.isReady()
  const wrapper = mount(
    { template: '<router-view v-slot="{ Component }"><keep-alive><component :is="Component"/></keep-alive></router-view>' },
    { global: { plugins: [router, createPinia()] }, attachTo: document.body },
  )
  await settle()
  return { wrapper, router }
}

// The toolbar's Deploy is the only primary-variant button in the header row.
const clickDeploy = async (wrapper) => {
  const btn = wrapper.findAll('button').find((b) => b.text().trim() === 'Deploy')
  expect(btn).toBeTruthy()
  await btn.trigger('click')
  await settle()
}

// The strip's way into a build that is still running.
const clickViewLog = async (wrapper) => {
  const btn = wrapper.findAll('button').find((b) => b.text().trim() === 'View log')
  expect(btn).toBeTruthy()
  await btn.trigger('click')
  await settle()
}

const dismiss = async () => {
  const close = dialog().querySelector('header button')
  close.dispatchEvent(new window.MouseEvent('click', { bubbles: true }))
  await settle()
}

describe('editor build log modal', () => {
  beforeEach(() => {
    stream = null
    document.body.innerHTML = ''
    vi.stubGlobal('EventSource', FakeEventSource)
    vi.stubGlobal('fetch', vi.fn(async () => ({ ok: false })))
  })

  it('does not open on deploy; the strip carries the build instead', async () => {
    const { wrapper } = await openEditor()
    await clickDeploy(wrapper)

    // The whole point: a sub-second build must not flash a dialog.
    expect(dialog()).toBeNull()
    expect(stream.url).toBe('/api/v1/deployments/dep-1/stream')
    expect(wrapper.text()).toContain('Building')

    stream.emit('log', { stream: 'build', line: 'installing express' })
    await settle()
    // The newest line shows in the strip, so a slow build is not a blank spinner.
    expect(wrapper.text()).toContain('installing express')
    expect(dialog()).toBeNull()
  })

  it('opens the full log on demand while the build is still running', async () => {
    const { wrapper } = await openEditor()
    await clickDeploy(wrapper)
    stream.emit('log', { stream: 'build', line: 'installing express' })
    stream.emit('log', { stream: 'build', line: 'compiling handler' })
    await settle()

    await clickViewLog(wrapper)
    expect(dialog()).toBeTruthy()
    // Both lines, not just the one the strip had room for.
    expect(dialogText()).toContain('installing express')
    expect(dialogText()).toContain('compiling handler')
  })

  it('says the build survives being dismissed, and it does', async () => {
    const { wrapper } = await openEditor()
    await clickDeploy(wrapper)
    await clickViewLog(wrapper)
    expect(dialogText()).toContain('Closing this does not cancel the build')

    await dismiss()
    expect(dialog()).toBeNull()

    // The stream is still live: the terminal event lands, closes it, and the
    // editor takes the deploy as done. A cancelled build reaches none of this.
    expect(stream.closed).toBe(false)
    stream.emit('succeeded', { version: 7, duration_ms: 1240 })
    await settle()
    expect(stream.closed).toBe(true)
    // Dismissed or not, success leaves no dialog: it closes itself.
    expect(dialog()).toBeNull()
    expect(wrapper.text()).not.toContain('Building')
  })

  it('raises the log by itself when the build fails', async () => {
    const { wrapper } = await openEditor()
    await clickDeploy(wrapper)
    expect(dialog()).toBeNull()

    stream.emit('failed', { error_message: "tsc: Cannot find name 'reqeust'" })
    await settle()

    // Failure is the one time the log is worth the interruption.
    expect(dialog()).toBeTruthy()
    expect(dialogText()).toContain("Cannot find name 'reqeust'")
    expect(dialogText()).toContain('Suggest fix')
    expect(dialogText()).not.toContain('Open workbench')
  })

  // Closing the dialog used to be the end of the evidence: the deploy had
  // failed and nothing on screen said so.
  it('keeps a failed build visible after its log is dismissed', async () => {
    const { wrapper } = await openEditor()
    await clickDeploy(wrapper)
    stream.emit('failed', { error_message: "tsc: Cannot find name 'reqeust'" })
    await settle()
    await dismiss()

    expect(dialog()).toBeNull()
    expect(wrapper.text()).toContain('Build failed')

    const back = wrapper.findAll('button').find((b) => b.text().trim() === 'Build failed')
    await back.trigger('click')
    await settle()
    expect(dialogText()).toContain("Cannot find name 'reqeust'")
  })

  it('acknowledges a successful build on the button, with no banner', async () => {
    const { wrapper } = await openEditor()
    await clickDeploy(wrapper)
    stream.emit('succeeded', { version: 7, duration_ms: 1240 })
    await settle()

    // A 1ms build is not worth an interruption. Deploy says it landed, the
    // file header says which version is live, and nothing has to be dismissed.
    expect(dialog()).toBeNull()
    expect(wrapper.text()).not.toContain('Build failed')
    expect(wrapper.text()).not.toContain('Suggest fix')
    expect(wrapper.findAll('button').some((b) => b.text().trim() === 'Deployed')).toBe(true)
    expect(wrapper.text()).toContain('v7 live')
    // The toast this replaced put a dismissable banner on screen for every
    // deploy, carrying this copy. (The strip's own workbench link stays.)
    expect(document.body.textContent || '').not.toContain('Built in 1240ms')
    expect(document.body.textContent || '').not.toContain('The full build log is on the')
  })

  it('has no build drawer left standing under the editor', async () => {
    const { wrapper } = await openEditor()
    expect(wrapper.text()).not.toContain('No build activity yet')
    expect(wrapper.find('#build-log').exists()).toBe(false)
  })

  // The router reuses this component across /functions/:name, so an abandoned
  // build could re-open the modal over whichever function was loaded next and
  // report the previous one's error as that function's.
  it('does not report one function\'s build failure on another function', async () => {
    const { wrapper, router } = await openEditor()
    await clickDeploy(wrapper)

    router.push('/functions/beta')
    await settle()
    expect(wrapper.text()).toContain('handler.py')

    stream.emit('failed', { error_message: "alpha: tsc Cannot find name 'reqeust'" })
    await settle()

    expect(dialog()).toBeNull()
    expect(wrapper.text()).not.toContain('reqeust')
    // beta is not building and did not fail, so nothing may claim either.
    expect(wrapper.text()).not.toContain('Building')
    expect(wrapper.text()).not.toContain('Build failed')
    expect(stream.closed).toBe(true)
  })

  // The acknowledgement is on a timer, so it outlives the navigation that
  // follows a deploy unless the re-scope clears it.
  it('does not carry the deploy acknowledgement onto another function', async () => {
    const { wrapper, router } = await openEditor()
    await clickDeploy(wrapper)
    stream.emit('succeeded', { version: 7, duration_ms: 1240 })
    await settle()
    expect(wrapper.findAll('button').some((b) => b.text().trim() === 'Deployed')).toBe(true)

    router.push('/functions/beta')
    await settle()
    expect(wrapper.text()).toContain('handler.py')
    // beta was not deployed, so its button must not say so.
    expect(wrapper.findAll('button').some((b) => b.text().trim() === 'Deployed')).toBe(false)
    expect(wrapper.text()).not.toContain('v7 live')
  })

  it('routes the toolbar Test control to the workbench', async () => {
    const { wrapper, router } = await openEditor()
    const btn = wrapper.findAll('button').find((b) => b.text().trim() === 'Test')
    expect(btn).toBeTruthy()
    await btn.trigger('click')
    await settle()
    expect(router.currentRoute.value.fullPath).toBe('/functions/alpha/test')
  })
})
