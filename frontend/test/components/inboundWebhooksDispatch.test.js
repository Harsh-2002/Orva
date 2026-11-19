import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import InboundWebhooksView from '@/views/InboundWebhooks.vue'
import { useManagedIAMStore } from '@/stores/managedIAM.js'

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

let wrapper
beforeEach(() => setActivePinia(createPinia()))
afterEach(() => wrapper?.unmount())

describe('inbound webhooks route dispatch', () => {
  it('keeps the legacy view when the server is not managed', async () => {
    wrapper = mount(InboundWebhooksView, { props: { name: 'payments' } })
    await flushPromises()

    expect(wrapper.get('[data-testid="legacy-inbound"]').text()).toBe('payments')
    expect(wrapper.find('[data-testid="managed-inbound"]').exists()).toBe(false)
  })

  it('uses the managed view under the managed capability', async () => {
    const iam = useManagedIAMStore()
    iam.available = true
    wrapper = mount(InboundWebhooksView, { props: { name: 'payments' } })
    await flushPromises()

    expect(wrapper.get('[data-testid="managed-inbound"]').text()).toBe('payments')
    expect(wrapper.find('[data-testid="legacy-inbound"]').exists()).toBe(false)
  })
})
