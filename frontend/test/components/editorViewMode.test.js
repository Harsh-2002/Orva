import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'

// An existing function opens read-only so reading it cannot edit or deploy it
// by accident; Edit is an explicit switch that the URL (?mode=edit) mirrors.

window.matchMedia = window.matchMedia || ((q) => ({
  matches: false, media: q, onchange: null, dispatchEvent: () => false,
  addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
}))

const FN_A = { id: 'fn-a', name: 'alpha', runtime: 'python', entrypoint: 'handler.py', memory_mb: 64, cpus: 0.5, code_hash: 'ha' }
const FN_B = { id: 'fn-b', name: 'beta', runtime: 'python', entrypoint: 'handler.py', memory_mb: 64, cpus: 0.5, code_hash: 'hb' }

// Tests that need a build in flight park the deploy-inline response here.
let deployGate = null
let sourcePrefix = 'source of'
let sourceGate = null
const apiClient = {
  get: vi.fn(async (url) => {
    if (url === '/functions') return { data: { functions: [FN_A, FN_B] } }
    if (url.endsWith('/source')) {
      if (sourceGate) await sourceGate
    }
    if (url.endsWith('/source')) return { data: { code: `${sourcePrefix} ${url}`, dependencies: '' } }
    return { data: {} }
  }),
  post: vi.fn(async (url) => {
    if (deployGate && url.endsWith('/deploy-inline')) await deployGate
    return { data: {} }
  }),
  put: vi.fn(async () => ({ data: {} })),
  delete: vi.fn(async () => ({ data: {} })),
}
vi.mock('@/api/client', () => ({ default: apiClient, getApiKey: () => 'k' }))
vi.mock('@/api/endpoints', () => ({
  __esModule: true,
  listFunctions: vi.fn(async () => ({ data: { functions: [FN_A, FN_B], total: 2 } })),
  listDeployments: vi.fn(async () => ({ data: { deployments: [] } })),
  rollbackFunction: vi.fn(async () => ({ data: {} })),
  listFixtures: vi.fn(async () => ({ data: { fixtures: [] } })),
  updateFixture: vi.fn(async () => ({ data: {} })),
  deleteFixture: vi.fn(async () => ({ data: {} })),
  invokeFunctionFull: vi.fn(async () => ({ data: {} })),
  getInvocationLogs: vi.fn(async () => ({ data: { stderr: '', log_entries: [] } })),
  listRoutes: vi.fn(async () => ({ data: { routes: [] } })),
  setRoute: vi.fn(async () => ({ data: {} })),
  deleteRoute: vi.fn(async () => ({ data: {} })),
}))

// Exposes the readOnly prop as a data attribute so the test can see what the
// Editor handed the real CodeEditor; clicking types into the buffer.
vi.mock('@/components/common/CodeEditor.vue', async () => {
  const { h } = await import('vue')
  return {
    __esModule: true,
    default: {
      name: 'CodeEditorStub',
      props: {
        modelValue: { type: String, default: '' },
        language: { type: String, default: '' },
        readOnly: { type: Boolean, default: false },
      },
      emits: ['update:modelValue'],
      setup(props, { emit }) {
        return () => h('pre', {
          'data-code': '',
          'data-readonly': String(props.readOnly),
          onClick: () => emit('update:modelValue', 'CHANGED'),
          onContextmenu: () => emit('update:modelValue', 'LATE EDIT'),
        }, props.modelValue)
      },
    },
  }
})

const OTHER = { render: () => null }
const Shell = {
  template: '<router-view v-slot="{ Component }"><keep-alive :max="10"><component :is="Component" /></keep-alive></router-view>',
}
const settle = async (n = 8) => { for (let i = 0; i < n; i++) { await nextTick(); await new Promise((r) => setTimeout(r, 0)) } }

const mounted = []
const boot = async (path) => {
  const Editor = (await import('@/views/Editor.vue')).default
  // Mirrors router/index.js: ?mode=edit reaches the Editor as a prop.
  const editorProps = (r) => ({ ...r.params, mode: r.query.mode === 'edit' ? 'edit' : '' })
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/functions/new', name: 'function-new', component: Editor, props: editorProps },
      { path: '/functions/:name', name: 'function-detail', component: Editor, props: editorProps },
      { path: '/functions/:name/test', name: 'function-test', component: OTHER },
      { path: '/:rest(.*)*', component: OTHER },
    ],
  })
  router.push(path)
  await router.isReady()
  const pinia = createPinia()
  setActivePinia(pinia)
  const wrapper = mount(Shell, { global: { plugins: [router, pinia] }, attachTo: document.body })
  mounted.push(wrapper)
  await settle()
  const { useConfirmStore } = await import('@/stores/confirm')
  return { wrapper, router, confirm: useConfirmStore(), go: async (to) => { await router.push(to); await settle() } }
}

const buttonText = (w, label) => w.findAll('button').filter((b) => b.text() === label)
const isReadOnly = (w) => w.find('[data-code]').attributes('data-readonly')
const radio = (w, label) => w.findAll('button[role="radio"]').find((b) => b.text() === label)
const checkedLabel = (w) => w.findAll('button[role="radio"]').find((b) => b.attributes('aria-checked') === 'true')?.text()

beforeEach(() => { vi.clearAllMocks(); deployGate = null; sourceGate = null; sourcePrefix = 'source of' })
afterEach(() => { while (mounted.length) mounted.pop().unmount() })

describe('editor view / edit mode', () => {
  it('opens an existing function in view: read-only editor, no Reset or Deploy, View checked', async () => {
    const { wrapper } = await boot('/functions/alpha')
    expect(isReadOnly(wrapper)).toBe('true')
    expect(buttonText(wrapper, 'Reset')).toHaveLength(0)
    expect(buttonText(wrapper, 'Deploy')).toHaveLength(0)
    expect(checkedLabel(wrapper)).toBe('View')
    // Mutating Config items are gone; the non-mutating surfaces remain.
    expect(wrapper.text()).toContain('Test')
    expect(wrapper.text()).toContain('Invoke URL')
    expect(wrapper.text()).toContain('Bindings')
  })

  it('Edit shows Deploy and Reset, unlocks the editor and puts ?mode=edit in the URL', async () => {
    const { wrapper, router } = await boot('/functions/alpha')
    await radio(wrapper, 'Edit').trigger('click')
    await settle()
    expect(isReadOnly(wrapper)).toBe('false')
    expect(buttonText(wrapper, 'Reset')).toHaveLength(1)
    expect(buttonText(wrapper, 'Deploy')).toHaveLength(1)
    expect(checkedLabel(wrapper)).toBe('Edit')
    expect(router.currentRoute.value.query.mode).toBe('edit')

    await radio(wrapper, 'View').trigger('click')
    await settle()
    expect(isReadOnly(wrapper)).toBe('true')
    expect(router.currentRoute.value.query.mode).toBeUndefined()
  })

  it('a ?mode=edit deep link opens straight into edit', async () => {
    const { wrapper } = await boot('/functions/alpha?mode=edit')
    expect(isReadOnly(wrapper)).toBe('false')
    expect(checkedLabel(wrapper)).toBe('Edit')
    expect(buttonText(wrapper, 'Deploy')).toHaveLength(1)
  })

  it('asks before leaving edit with changes: Keep editing stays, Discard reverts the buffer', async () => {
    const { wrapper, confirm } = await boot('/functions/alpha?mode=edit')
    const original = wrapper.find('[data-code]').text()
    await wrapper.find('[data-code]').trigger('click')
    await settle()
    expect(wrapper.find('[data-code]').text()).toBe('CHANGED')

    await radio(wrapper, 'View').trigger('click')
    await settle()
    expect(confirm.visible).toBe(true)
    expect(confirm.title).toBe('Discard changes?')
    expect(confirm.cancelLabel).toBe('Keep editing')
    confirm.settle(false)
    await settle()
    expect(checkedLabel(wrapper)).toBe('Edit')
    expect(wrapper.find('[data-code]').text()).toBe('CHANGED')

    await radio(wrapper, 'View').trigger('click')
    await settle()
    confirm.settle(true)
    await settle()
    expect(checkedLabel(wrapper)).toBe('View')
    expect(isReadOnly(wrapper)).toBe('true')
    expect(wrapper.find('[data-code]').text()).toBe(original)
  })

  it('does not ask when there is nothing to discard, and never asks going to edit', async () => {
    const { wrapper, confirm } = await boot('/functions/alpha')
    await radio(wrapper, 'Edit').trigger('click')
    await settle()
    await radio(wrapper, 'View').trigger('click')
    await settle()
    expect(confirm.visible).toBe(false)
    expect(checkedLabel(wrapper)).toBe('View')
  })

  // M3: a list row or breadcrumb link carries no ?mode. That is not a decision
  // to discard work, so a dirty Edit stays in Edit and gets the flag back.
  it('a link without ?mode keeps a dirty Edit and restores ?mode=edit, without asking', async () => {
    const { wrapper, router, confirm, go } = await boot('/functions/alpha?mode=edit')
    await wrapper.find('[data-code]').trigger('click')
    await settle()
    await go('/functions/alpha')
    expect(confirm.visible).toBe(false)
    expect(checkedLabel(wrapper)).toBe('Edit')
    expect(router.currentRoute.value.query.mode).toBe('edit')
    expect(wrapper.find('[data-code]').text()).toBe('CHANGED')

    await go('/elsewhere')
    await go('/functions/alpha')
    expect(confirm.visible).toBe(false)
    expect(checkedLabel(wrapper)).toBe('Edit')
    expect(wrapper.find('[data-code]').text()).toBe('CHANGED')
  })

  it('a clean Edit reached by a link without ?mode drops to View', async () => {
    const { wrapper, go } = await boot('/functions/alpha?mode=edit')
    await go('/functions/alpha')
    expect(checkedLabel(wrapper)).toBe('View')
    expect(isReadOnly(wrapper)).toBe('true')
  })

  it('/functions/new is edit with no switch; another function opened from edit returns to view', async () => {
    const { wrapper, go } = await boot('/functions/new')
    expect(wrapper.findAll('button[role="radio"]')).toHaveLength(0)
    expect(isReadOnly(wrapper)).toBe('false')
    expect(buttonText(wrapper, 'Deploy')).toHaveLength(1)

    await go('/functions/alpha?mode=edit')
    expect(isReadOnly(wrapper)).toBe('false')
    await go('/functions/beta')
    expect(wrapper.find('[data-code]').text()).toBe('source of /functions/fn-b/source')
    expect(isReadOnly(wrapper)).toBe('true')
    expect(checkedLabel(wrapper)).toBe('View')

    await go('/functions/new')
    expect(wrapper.findAll('button[role="radio"]')).toHaveLength(0)
    expect(isReadOnly(wrapper)).toBe('false')
  })

  it('keeps its mode and buffer while cached behind another view', async () => {
    const { wrapper, go } = await boot('/functions/alpha?mode=edit')
    await wrapper.find('[data-code]').trigger('click')
    await settle()
    await go('/elsewhere')
    await go('/functions/alpha?mode=edit')
    expect(wrapper.find('[data-code]').text()).toBe('CHANGED')
    expect(checkedLabel(wrapper)).toBe('Edit')
  })

  it('orva:deploy (Cmd/Ctrl-S) does nothing in view mode and deploys in edit mode', async () => {
    const { wrapper } = await boot('/functions/alpha')
    window.dispatchEvent(new CustomEvent('orva:deploy'))
    await settle()
    expect(apiClient.put).not.toHaveBeenCalled()
    expect(apiClient.post).not.toHaveBeenCalled()

    await radio(wrapper, 'Edit').trigger('click')
    await settle()
    window.dispatchEvent(new CustomEvent('orva:deploy'))
    await settle()
    expect(apiClient.put).toHaveBeenCalledTimes(1)
  })

  // H1: refocusing the window re-pulls the server's source. In a dirty Edit that
  // replaced the only copy of the work (and re-based the dirty check on it).
  it('window focus does not overwrite a dirty Edit, but does refresh a View', async () => {
    const { wrapper } = await boot('/functions/alpha?mode=edit')
    await wrapper.find('[data-code]').trigger('click')
    await settle()
    sourcePrefix = 'server changed'
    window.dispatchEvent(new window.Event('focus'))
    await settle()
    expect(wrapper.find('[data-code]').text()).toBe('CHANGED')

    const view = await boot('/functions/beta')
    sourcePrefix = 'server changed again'
    window.dispatchEvent(new window.Event('focus'))
    await settle()
    expect(view.wrapper.find('[data-code]').text()).toContain('server changed again')
  })

  // M1 + H2: dirtiness tracks the baseline, a deploy moves the baseline to what
  // was SENT, and keystrokes made while the build ran stay unsaved.
  it('deploy success cleans the sent buffer; typing during a pending deploy stays dirty', async () => {
    const first = await boot('/functions/alpha?mode=edit')
    await first.wrapper.find('[data-code]').trigger('click')
    await settle()
    await radio(first.wrapper, 'View').trigger('click')
    await settle()
    expect(first.confirm.visible).toBe(true) // edited -> dirty
    first.confirm.settle(false)
    await settle()

    window.dispatchEvent(new CustomEvent('orva:deploy'))
    await settle()
    await radio(first.wrapper, 'View').trigger('click')
    await settle()
    expect(first.confirm.visible).toBe(false) // same buffer deployed -> clean
    expect(checkedLabel(first.wrapper)).toBe('View')
    while (mounted.length) mounted.pop().unmount()

    let release
    deployGate = new Promise((r) => { release = r })
    const second = await boot('/functions/alpha?mode=edit')
    await second.wrapper.find('[data-code]').trigger('click')
    await settle()
    window.dispatchEvent(new CustomEvent('orva:deploy'))
    await settle()
    await second.wrapper.find('[data-code]').trigger('contextmenu') // typed while the build runs
    await settle()
    release()
    await settle()
    await radio(second.wrapper, 'View').trigger('click')
    await settle()
    expect(second.confirm.visible).toBe(true) // never deployed -> still dirty
    second.confirm.settle(false)
    await settle()
    expect(second.wrapper.find('[data-code]').text()).toBe('LATE EDIT')
  })

  // M2
  it('opening another function from a dirty Edit asks; Keep stays, Discard goes', async () => {
    const { wrapper, router, confirm } = await boot('/functions/alpha?mode=edit')
    await wrapper.find('[data-code]').trigger('click')
    await settle()

    let nav = router.push('/functions/beta')
    await settle()
    expect(confirm.visible).toBe(true)
    confirm.settle(false)
    await nav
    await settle()
    expect(router.currentRoute.value.path).toBe('/functions/alpha')
    expect(wrapper.find('[data-code]').text()).toBe('CHANGED')

    nav = router.push('/functions/beta')
    await settle()
    confirm.settle(true)
    await nav
    await settle()
    expect(router.currentRoute.value.path).toBe('/functions/beta')
    expect(wrapper.find('[data-code]').text()).toBe('source of /functions/fn-b/source')
  })

  it('a clean Edit, or any View, opens another function without asking', async () => {
    const { router, confirm } = await boot('/functions/alpha?mode=edit')
    await router.push('/functions/beta')
    await settle()
    expect(confirm.visible).toBe(false)
    expect(router.currentRoute.value.path).toBe('/functions/beta')
  })

  // L1
  it('closes an open editable panel when the mode turns to View', async () => {
    const Editor = (await import('@/views/Editor.vue')).default
    const { wrapper, go } = await boot('/functions/alpha?mode=edit')
    const vm = wrapper.findComponent(Editor).vm
    vm.modals.secrets = true
    vm.modals.settings = true
    await settle()
    await go('/functions/alpha')
    expect(checkedLabel(wrapper)).toBe('View')
    expect(vm.modals.secrets).toBe(false)
    expect(vm.modals.settings).toBe(false)
  })

  // vue-router unregisters per-component guards while the page is cached, so the
  // check has to live somewhere that stays registered.
  it('dirty Edit -> another page -> a different function asks; Keep leaves the buffer, same function does not ask', async () => {
    const { wrapper, router, confirm, go } = await boot('/functions/alpha?mode=edit')
    await wrapper.find('[data-code]').trigger('click')
    await settle()
    await go('/elsewhere')
    expect(confirm.visible).toBe(false)

    const nav = router.push('/functions/beta')
    await settle()
    expect(confirm.visible).toBe(true)
    expect(confirm.title).toBe('Discard changes?')
    confirm.settle(false)
    await nav
    await settle()
    expect(router.currentRoute.value.path).toBe('/elsewhere')

    await go('/functions/alpha?mode=edit')
    expect(confirm.visible).toBe(false)
    expect(wrapper.find('[data-code]').text()).toBe('CHANGED')
    expect(checkedLabel(wrapper)).toBe('Edit')

    // Discard lets the move through.
    await go('/elsewhere')
    const nav2 = router.push('/functions/beta')
    await settle()
    confirm.settle(true)
    await nav2
    await settle()
    expect(router.currentRoute.value.path).toBe('/functions/beta')
    expect(wrapper.find('[data-code]').text()).toBe('source of /functions/fn-b/source')
  })

  it('a new function with typed code is protected; an untouched seed is not', async () => {
    const first = await boot('/functions/new')
    await first.router.push('/functions/alpha')
    await settle()
    expect(first.confirm.visible).toBe(false) // nothing typed -> free to leave
    expect(first.router.currentRoute.value.path).toBe('/functions/alpha')
    while (mounted.length) mounted.pop().unmount()

    const { wrapper, router, confirm } = await boot('/functions/new')
    await wrapper.find('[data-code]').trigger('click')
    await settle()
    const nav = router.push('/functions/alpha')
    await settle()
    expect(confirm.visible).toBe(true)
    confirm.settle(false)
    await nav
    await settle()
    expect(router.currentRoute.value.path).toBe('/functions/new')
    expect(wrapper.find('[data-code]').text()).toBe('CHANGED')
  })

  it('cannot switch to View while a build is running, and can afterwards without a prompt', async () => {
    let release
    deployGate = new Promise((r) => { release = r })
    const { wrapper, confirm } = await boot('/functions/alpha?mode=edit')
    window.dispatchEvent(new CustomEvent('orva:deploy'))
    await settle()
    expect(radio(wrapper, 'View').attributes('disabled')).toBeDefined()
    await radio(wrapper, 'View').trigger('click')
    await settle()
    expect(checkedLabel(wrapper)).toBe('Edit')
    expect(confirm.visible).toBe(false)

    release()
    await settle()
    expect(radio(wrapper, 'View').attributes('disabled')).toBeUndefined()
    await radio(wrapper, 'View').trigger('click')
    await settle()
    expect(confirm.visible).toBe(false)
    expect(checkedLabel(wrapper)).toBe('View')
  })

  it('typing while the focus refresh is fetching is not overwritten', async () => {
    const { wrapper } = await boot('/functions/alpha?mode=edit')
    let release
    sourceGate = new Promise((r) => { release = r })
    sourcePrefix = 'server changed'
    window.dispatchEvent(new window.Event('focus'))
    await settle()
    await wrapper.find('[data-code]').trigger('click') // typed during the fetch
    await settle()
    release()
    await settle()
    expect(wrapper.find('[data-code]').text()).toBe('CHANGED')
  })

  it('a guard prompt cannot open on top of the mode prompt', async () => {
    const { wrapper, router, confirm } = await boot('/functions/alpha?mode=edit')
    await wrapper.find('[data-code]').trigger('click')
    await settle()
    await radio(wrapper, 'View').trigger('click')
    await settle()
    expect(confirm.visible).toBe(true)

    await router.push('/functions/beta').catch(() => {})
    await settle()
    expect(router.currentRoute.value.path).toBe('/functions/alpha')
    expect(confirm.visible).toBe(true) // still the first question

    confirm.settle(false)
    await settle()
    expect(checkedLabel(wrapper)).toBe('Edit')
    expect(wrapper.find('[data-code]').text()).toBe('CHANGED')
  })
})
