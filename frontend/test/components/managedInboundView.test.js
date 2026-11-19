import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import ManagedInboundWebhooks from '@/views/ManagedInboundWebhooks.vue'
import { useManagedIAMStore } from '@/stores/managedIAM.js'

const OWNER = '0190000a-0000-7000-8000-000000000001'

vi.mock('@/api/managedPolicies.js', () => ({
  readManagedIAMCapability: vi.fn(async () => true),
  listManagedIAMFunctions: vi.fn(async () => [{ id: OWNER, name: 'payments' }]),
}))
vi.mock('@/components/exposure/ManagedInboundWebhooksPanel.vue', () => ({
  default: {
    name: 'ManagedInboundWebhooksPanel',
    props: ['functionId', 'functionName'],
    template: '<div data-testid="inbound-panel">{{ functionId }}</div>',
  },
}))

let wrapper
const mountView = async (name) => {
  const iam = useManagedIAMStore()
  iam.available = true
  wrapper = mount(ManagedInboundWebhooks, { props: { name } })
  await flushPromises()
  return wrapper
}

beforeEach(() => setActivePinia(createPinia()))
afterEach(() => wrapper?.unmount())

describe('managed inbound webhooks owner resolution', () => {
  it('hands the panel the exact owner UUID resolved from the directory', async () => {
    const view = await mountView('payments')
    expect(view.get('[data-testid="inbound-panel"]').text()).toBe(OWNER)
  })

  it('refuses a name the directory does not contain instead of guessing an owner', async () => {
    const view = await mountView('ghost')
    expect(view.get('[data-testid="managed-inbound-unknown-function"]').text()).toMatch(/No managed function named "ghost"/)
    expect(view.find('[data-testid="inbound-panel"]').exists()).toBe(false)
  })
})
