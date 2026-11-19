import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import InboundWebhooksView from '@/views/InboundWebhooks.vue'
import { useAuthStore } from '@/stores/auth.js'
import { readManagedIAMCapability } from '@/api/managedPolicies.js'

vi.mock('@/api/managedPolicies.js', () => ({
  readManagedIAMCapability: vi.fn(),
  listManagedIAMFunctions: vi.fn(async () => []),
}))
vi.mock('@/views/LegacyInboundWebhooks.vue', () => ({
  default: {
    name: 'LegacyInboundWebhooks',
    props: ['name'],
    template: '<div data-testid="legacy-inbound">{{ name }}</div>',
  },
}))
vi.mock('@/views/ManagedInboundWebhooks.vue', () => ({
  default: {
    name: 'ManagedInboundWebhooks',
    props: ['name'],
    template: '<div data-testid="managed-inbound">{{ name }}</div>',
  },
}))

const deferred = () => {
  let resolve
  let reject
  const promise = new Promise((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

let wrapper
beforeEach(() => {
  setActivePinia(createPinia())
  useAuthStore().isAuthenticated = true
  readManagedIAMCapability.mockReset()
})
afterEach(() => wrapper?.unmount())

const mountView = () => {
  wrapper = mount(InboundWebhooksView, { props: { name: 'payments' } })
  return wrapper
}

describe('inbound webhooks route dispatch', () => {
  it('mounts neither view while the capability answer is pending', async () => {
    const gate = deferred()
    readManagedIAMCapability.mockReturnValue(gate.promise)
    const view = mountView()
    await flushPromises()

    expect(view.find('[data-testid="inbound-capability-pending"]').exists()).toBe(true)
    expect(view.find('[data-testid="legacy-inbound"]').exists()).toBe(false)
    expect(view.find('[data-testid="managed-inbound"]').exists()).toBe(false)

    gate.resolve(true)
    await flushPromises()

    expect(view.find('[data-testid="inbound-capability-pending"]').exists()).toBe(false)
    expect(view.get('[data-testid="managed-inbound"]').text()).toBe('payments')
    expect(view.find('[data-testid="legacy-inbound"]').exists()).toBe(false)
  })

  it('keeps the legacy view when the server is not managed', async () => {
    readManagedIAMCapability.mockResolvedValue(false)
    const view = mountView()
    await flushPromises()

    expect(view.get('[data-testid="legacy-inbound"]').text()).toBe('payments')
    expect(view.find('[data-testid="managed-inbound"]').exists()).toBe(false)
  })

  it('treats a failed probe as absent capability and mounts legacy', async () => {
    readManagedIAMCapability.mockRejectedValue(new Error('network down'))
    const view = mountView()
    await flushPromises()

    expect(view.get('[data-testid="legacy-inbound"]').text()).toBe('payments')
    expect(view.find('[data-testid="inbound-capability-pending"]').exists()).toBe(false)
  })
})
