import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import ManagedInboundWebhooksPanel from '@/components/exposure/ManagedInboundWebhooksPanel.vue'
import { useConfirmStore } from '@/stores/confirm'

const OWNER = '0190000a-0000-7000-8000-000000000001'
const TRIGGER = '0190000c-0000-7000-8000-0000000000aa'
const ROW = {
  id: TRIGGER,
  function_id: OWNER,
  name: 'github',
  signature_format: 'github',
  signature_header: 'X-Hub-Signature-256',
  secret_preview: 'abcd1234…',
  active: true,
  version: '5',
  published: true,
  created_at: '2026-01-02T03:04:05Z',
}
const EXPECTED = { [OWNER]: { policy_revision: '2', authority_epoch: '3' } }

const ModalStub = {
  props: ['modelValue', 'title', 'size'],
  emits: ['update:modelValue'],
  template: '<section v-if="modelValue" data-testid="modal"><slot /><slot name="footer" /></section>',
}
const FilterSelectStub = {
  props: ['options', 'modelValue', 'label', 'triggerId', 'wide'],
  emits: ['update:modelValue'],
  template: '<div data-testid="format-picker" />',
}

const makeApi = (overrides = {}) => ({
  listInbound: vi.fn(async () => ({ rows: [ROW], expected: EXPECTED, truncated: false })),
  inspectInbound: vi.fn(async () => ({ version: '5', expected: EXPECTED, inboundWebhook: ROW, inboundPublished: true })),
  createInbound: vi.fn(async () => ({
    inbound_webhook: { id: TRIGGER }, secret: 'sup3r-secret', trigger_url: `/webhook/${TRIGGER}`,
  })),
  updateInbound: vi.fn(async () => ({})),
  deleteInbound: vi.fn(async () => ({ status: 'deleted' })),
  ...overrides,
})

const mounted = []
const mountPanel = (api, props = {}) => {
  const view = mount(ManagedInboundWebhooksPanel, {
    props: { api, functionId: OWNER, functionName: 'payments', ...props },
    global: { stubs: { Modal: ModalStub, FilterSelect: FilterSelectStub } },
  })
  mounted.push(view)
  return view
}

const openCreateWithName = async (view, name = 'github-deploys') => {
  await view.get('[data-testid="inbound-new"]').trigger('click')
  await view.get('[data-testid="inbound-name-input"]').setValue(name)
  await flushPromises()
}

beforeEach(() => setActivePinia(createPinia()))
afterEach(() => {
  while (mounted.length) mounted.pop().unmount()
})

describe('managed inbound webhooks panel', () => {
  it('offers no mutation and never a legacy signing drawer when read-only', async () => {
    const api = makeApi()
    const view = mountPanel(api, { readOnly: true })
    await flushPromises()

    expect(view.get('[data-testid="inbound-readonly"]').text()).toMatch(/Read-only/)
    expect(view.get('[data-testid="inbound-new"]').attributes('disabled')).toBeDefined()
    for (const button of view.findAll('button[title="Delete trigger"]')) {
      expect(button.attributes('disabled')).toBeDefined()
    }
    expect(view.find('button[title="Send a test payload"]').exists()).toBe(false)
    await view.get('[data-testid="inbound-new"]').trigger('click')
    expect(view.find('[data-testid="modal"]').exists()).toBe(false)
  })

  it('creates with create-time tokens from a fresh inspection and shows the secret once', async () => {
    const api = makeApi()
    const view = mountPanel(api)
    await flushPromises()

    await openCreateWithName(view)
    await view.get('[data-testid="inbound-save"]').trigger('click')
    await flushPromises()

    expect(api.inspectInbound).toHaveBeenCalledWith(OWNER)
    expect(api.createInbound).toHaveBeenCalledWith(OWNER, {
      name: 'github-deploys',
      signatureFormat: 'hmac_sha256_hex',
      active: true,
      version: '5',
      expected: EXPECTED,
    })
    // Update and the removed /sign flow are never part of a create.
    expect(api.updateInbound).not.toHaveBeenCalled()
    expect(view.find('button[title="Send a test payload"]').exists()).toBe(false)

    const block = view.get('[data-testid="inbound-secret-once"]')
    expect(block.text()).toMatch(/not be shown again/)
    expect(view.get('[data-testid="inbound-created-secret"]').text()).toBe('sup3r-secret')
    expect(api.listInbound).toHaveBeenCalledTimes(2)
  })

  it('pauses with the full CAS body read immediately before the write', async () => {
    const api = makeApi()
    const view = mountPanel(api)
    await flushPromises()

    await view.findAll('button[title="Pause this trigger"]')[0].trigger('click')
    await flushPromises()

    expect(api.inspectInbound).toHaveBeenCalledWith(OWNER, { id: TRIGGER })
    expect(api.updateInbound).toHaveBeenCalledWith(OWNER, TRIGGER, {
      active: false, version: '5', expected: EXPECTED,
    })
  })

  it('deletes only after confirmation, with freshly read tokens', async () => {
    const api = makeApi()
    const confirm = useConfirmStore()
    const ask = vi.spyOn(confirm, 'ask').mockResolvedValue(false)
    const view = mountPanel(api)
    await flushPromises()

    await view.findAll('button[title="Delete trigger"]')[0].trigger('click')
    await flushPromises()
    expect(ask).toHaveBeenCalledTimes(1)
    expect(api.deleteInbound).not.toHaveBeenCalled()

    ask.mockResolvedValue(true)
    await view.findAll('button[title="Delete trigger"]')[0].trigger('click')
    await flushPromises()

    expect(api.inspectInbound).toHaveBeenCalledWith(OWNER, { id: TRIGGER })
    expect(api.deleteInbound).toHaveBeenCalledWith(OWNER, TRIGGER, {
      version: '5', expected: EXPECTED,
    })
  })

  it('keeps the draft on a 409, refreshes, and creates only when asked again', async () => {
    let attempts = 0
    const api = makeApi({
      createInbound: vi.fn(async () => {
        attempts += 1
        if (attempts === 1) throw Object.assign(new Error('conflict'), { stale: true, status: 409 })
        return { inbound_webhook: { id: TRIGGER }, secret: 's', trigger_url: '/webhook/x' }
      }),
    })
    const view = mountPanel(api)
    await flushPromises()

    await openCreateWithName(view, 'github-deploys')
    await view.get('[data-testid="inbound-save"]').trigger('click')
    await flushPromises()

    expect(attempts).toBe(1)
    expect(api.listInbound).toHaveBeenCalledTimes(2)
    expect(view.get('[data-testid="inbound-error"]').text()).toMatch(/changed since it was read/)
    expect(view.get('[data-testid="inbound-name-input"]').element.value).toBe('github-deploys')
    expect(view.find('[data-testid="modal"]').exists()).toBe(true)
    expect(view.get('[data-testid="inbound-save"]').text()).toBe('Save again')

    await view.get('[data-testid="inbound-save"]').trigger('click')
    await flushPromises()

    expect(attempts).toBe(2)
    expect(api.inspectInbound).toHaveBeenCalledTimes(2)
    expect(view.find('[data-testid="modal"]').exists()).toBe(false)
  })

  it('flips to read-only when a mutation is refused with 403', async () => {
    const api = makeApi({
      createInbound: vi.fn(async () => {
        throw Object.assign(new Error('forbidden'), { forbidden: true, status: 403 })
      }),
    })
    const view = mountPanel(api)
    await flushPromises()

    await openCreateWithName(view)
    await view.get('[data-testid="inbound-save"]').trigger('click')
    await flushPromises()

    expect(view.get('[data-testid="inbound-readonly"]').exists()).toBe(true)
    expect(view.get('[data-testid="inbound-new"]').attributes('disabled')).toBeDefined()
    expect(view.get('[data-testid="inbound-error"]').text()).toMatch(/write access/)
  })
})
