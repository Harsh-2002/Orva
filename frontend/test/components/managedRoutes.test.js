import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import ManagedRoutesPanel from '@/components/exposure/ManagedRoutesPanel.vue'
import { useConfirmStore } from '@/stores/confirm'

const OWNER = '0190000a-0000-7000-8000-000000000001'
const OTHER = '0190000b-0000-7000-8000-000000000002'
const ROUTE = {
  path: '/hooks/stripe', function_id: OWNER, methods: '*', version: '4', published: true,
  created_at: '2026-01-02T03:04:05Z',
}
const EXPECTED = { [OWNER]: { policy_revision: '2', authority_epoch: '3' } }
const functions = [{ id: OWNER, name: 'payments' }, { id: OTHER, name: 'proxy' }]

// Modal teleports to body and FilterSelect opens a popover; neither is what
// these assertions are about.
const ModalStub = {
  props: ['modelValue', 'title', 'size'],
  emits: ['update:modelValue'],
  template: '<section v-if="modelValue" data-testid="modal"><slot /><slot name="footer" /></section>',
}
const FilterSelectStub = {
  props: ['options', 'modelValue', 'label', 'triggerId', 'wide'],
  emits: ['update:modelValue'],
  template: '<div data-testid="function-picker" />',
}

const makeApi = (overrides = {}) => ({
  listRoutes: vi.fn(async () => ({ routes: [ROUTE], truncated: false })),
  inspectRoute: vi.fn(async () => ({ version: ROUTE.version, expected: EXPECTED, route: ROUTE, routePublished: true })),
  setRoute: vi.fn(async () => ({ status: 'saved' })),
  deleteRoute: vi.fn(async () => ({ status: 'deleted' })),
  ...overrides,
})

let wrapper
const mounted = []
const mountPanel = (api, props = {}) => {
  wrapper = mount(ManagedRoutesPanel, {
    props: { api, functions, ...props },
    global: { stubs: { Modal: ModalStub, FilterSelect: FilterSelectStub } },
  })
  mounted.push(wrapper)
  return wrapper
}
const fillDraft = async (view, path = '/hooks/stripe', owner = OWNER) => {
  await view.get('[data-testid="route-new"]').trigger('click')
  await view.get('[data-testid="route-path-input"]').setValue(path)
  view.findComponent(FilterSelectStub).vm.$emit('update:modelValue', owner)
  await flushPromises()
}

beforeEach(() => setActivePinia(createPinia()))
afterEach(() => {
  while (mounted.length) mounted.pop().unmount()
})

describe('managed routes panel', () => {
  it('offers no mutation and says so when the operator is read-only', async () => {
    const api = makeApi()
    const view = mountPanel(api, { readOnly: true })
    await flushPromises()

    expect(view.get('[data-testid="route-readonly"]').text()).toMatch(/Read-only/)
    expect(view.get('[data-testid="route-new"]').attributes('disabled')).toBeDefined()
    for (const button of view.findAll('button[title="Delete route"]')) {
      expect(button.attributes('disabled')).toBeDefined()
    }
    await view.get('[data-testid="route-new"]').trigger('click')
    expect(view.find('[data-testid="modal"]').exists()).toBe(false)
    expect(api.listRoutes).toHaveBeenCalledTimes(1)
  })

  it('reads the mapping immediately before saving and sends exactly those tokens', async () => {
    const order = []
    const bigVersion = '9999999999999999'
    const api = makeApi({
      inspectRoute: vi.fn(async () => {
        order.push('inspect')
        return { version: bigVersion, expected: EXPECTED }
      }),
      setRoute: vi.fn(async () => {
        order.push('save')
        return { status: 'saved' }
      }),
      listRoutes: vi.fn(async () => {
        order.push('list')
        return { routes: [], truncated: false }
      }),
    })
    const view = mountPanel(api)
    await flushPromises()
    order.length = 0

    await fillDraft(view)
    await view.get('[data-testid="route-save"]').trigger('click')
    await flushPromises()

    expect(order).toEqual(['inspect', 'save', 'list'])
    expect(api.inspectRoute).toHaveBeenCalledWith('/hooks/stripe', { functionId: OWNER })
    expect(api.setRoute).toHaveBeenCalledWith({
      path: '/hooks/stripe',
      functionId: OWNER,
      methods: '',
      version: bigVersion,
      expected: EXPECTED,
    })
  })

  it('keeps the draft on a 409, refreshes, and saves only when asked again', async () => {
    let attempts = 0
    const api = makeApi({
      setRoute: vi.fn(async () => {
        attempts += 1
        if (attempts === 1) throw Object.assign(new Error('conflict'), { stale: true, status: 409 })
        return { status: 'saved' }
      }),
    })
    const view = mountPanel(api)
    await flushPromises()

    await fillDraft(view)
    await view.get('[data-testid="route-save"]').trigger('click')
    await flushPromises()

    expect(attempts).toBe(1)
    expect(api.listRoutes).toHaveBeenCalledTimes(2)
    expect(view.get('[data-testid="route-error"]').text()).toMatch(/changed since it was read/)
    expect(view.get('[data-testid="route-path-input"]').element.value).toBe('/hooks/stripe')
    expect(view.find('[data-testid="modal"]').exists()).toBe(true)
    expect(view.get('[data-testid="route-save"]').text()).toBe('Save again')

    await view.get('[data-testid="route-save"]').trigger('click')
    await flushPromises()

    expect(attempts).toBe(2)
    expect(api.inspectRoute).toHaveBeenCalledTimes(2)
    expect(view.find('[data-testid="modal"]').exists()).toBe(false)
  })

  it('asks for confirmation and sends the freshly read tokens on delete', async () => {
    const api = makeApi()
    const confirm = useConfirmStore()
    const ask = vi.spyOn(confirm, 'ask').mockResolvedValue(false)
    const view = mountPanel(api)
    await flushPromises()

    await view.findAll('button[title="Delete route"]')[0].trigger('click')
    await flushPromises()
    expect(ask).toHaveBeenCalledTimes(1)
    expect(api.deleteRoute).not.toHaveBeenCalled()

    ask.mockResolvedValue(true)
    await view.findAll('button[title="Delete route"]')[0].trigger('click')
    await flushPromises()

    expect(api.inspectRoute).toHaveBeenCalledWith('/hooks/stripe')
    expect(api.deleteRoute).toHaveBeenCalledWith({
      path: '/hooks/stripe', version: '4', expected: EXPECTED,
    })
  })

  it('flips to read-only when a mutation is refused with 403', async () => {
    const api = makeApi({
      setRoute: vi.fn(async () => {
        throw Object.assign(new Error('forbidden'), { forbidden: true, status: 403 })
      }),
    })
    const view = mountPanel(api)
    await flushPromises()

    await fillDraft(view)
    await view.get('[data-testid="route-save"]').trigger('click')
    await flushPromises()

    expect(view.get('[data-testid="route-readonly"]').exists()).toBe(true)
    expect(view.get('[data-testid="route-new"]').attributes('disabled')).toBeDefined()
    expect(view.get('[data-testid="route-error"]').text()).toMatch(/write access/)
  })
})
