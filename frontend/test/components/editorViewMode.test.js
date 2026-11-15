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

beforeEach(() => { vi.clearAllMocks() })
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

  it('Back to the plain URL while dirty asks too, and Keep editing restores ?mode=edit', async () => {
    const { wrapper, router, confirm, go } = await boot('/functions/alpha?mode=edit')
    await wrapper.find('[data-code]').trigger('click')
    await settle()
    await go('/functions/alpha')
    expect(confirm.visible).toBe(true)
    confirm.settle(false)
    await settle()
    expect(checkedLabel(wrapper)).toBe('Edit')
    expect(router.currentRoute.value.query.mode).toBe('edit')
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
})
