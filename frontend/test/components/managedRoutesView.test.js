import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import ManagedRoutesView from '@/views/ManagedRoutes.vue'
import router from '@/router/index.js'
import { useManagedIAMStore } from '@/stores/managedIAM.js'

vi.mock('@/api/managedPolicies.js', () => ({
  readManagedIAMMode: vi.fn(async () => 'legacy'),
  listManagedIAMFunctions: vi.fn(async () => []),
}))
vi.mock('@/components/exposure/ManagedRoutesPanel.vue', () => ({
  default: { name: 'ManagedRoutesPanel', template: '<div data-testid="routes-panel" />' },
}))

let wrapper
beforeEach(() => setActivePinia(createPinia()))
afterEach(() => wrapper?.unmount())

describe('managed routes availability', () => {
  it('routes /routes through the managed IAM guard', () => {
    const record = router.getRoutes().find(route => route.name === 'managed-routes')
    expect(record).toBeTruthy()
    expect(record.path).toBe('/routes')
    expect(record.meta.managedIAM).toBe(true)
  })

  it('draws the editing panel only once the server reports managed IAM', async () => {
    const iam = useManagedIAMStore()
    wrapper = mount(ManagedRoutesView)
    await flushPromises()

    expect(wrapper.find('[data-testid="managed-routes-unavailable"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="routes-panel"]').exists()).toBe(false)

    iam.available = true
    await flushPromises()

    expect(wrapper.find('[data-testid="managed-routes-unavailable"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="routes-panel"]').exists()).toBe(true)
  })
})
