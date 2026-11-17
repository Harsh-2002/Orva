import { describe, expect, it } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, ref } from 'vue'
import ManagedPolicyDraft from '@/components/permissions/ManagedPolicyDraft.vue'
import { inlineSnapshot } from '@/api/managedPolicies.js'
import { useIAMResource } from '@/composables/useIAMResource.js'

const probe = (setup) => defineComponent({ name: 'IAMControllerProbe', setup })

describe('managed policy authoring', () => {
  it('never fabricates a function identity for a reusable draft', async () => {
    const page = mount(ManagedPolicyDraft)
    await page.find('input').setValue('own-config')
    await page.findAll('button').find((b) => b.text() === 'Add permission').trigger('click')
    expect(page.findComponent({ name: 'PermissionWizard' }).props('functionId')).toBe('')
    expect(page.text()).not.toContain('Recurring work')
    page.unmount()
  })
  it('keeps canonical JSON read-only and requires final review', async () => {
    const page = mount(ManagedPolicyDraft)
    await page.find('input').setValue('reader')
    expect(page.find('textarea').exists()).toBe(false)
    await page.findAll('button').find((b) => b.text() === 'Review policy').trigger('click')
    expect(page.emitted('save')).toBeUndefined()
    await page.findAll('button').find((b) => b.text() === 'Create reusable policy').trigger('click')
    expect(page.emitted('save')[0][0].name).toBe('reader')
    page.unmount()
  })
  it('only maps the inline source into the incumbent editor', () => {
    const raw = { function: { id: 'owner' }, inline_policy: { allow: [] }, effective_policy: { allow: ['managed'] }, attachments: ['pin'], host: { revision: '9007199254740993', authority_epoch: '8', can_manage: false }, history: [] }
    const value = inlineSnapshot(raw)
    expect(value.policy).toEqual({ allow: [] })
    expect(value.host.revision).toBe('9007199254740993')
    expect(value.host.capabilities.canManage).toBe(false)
  })
})

describe('managed request lifetime', () => {
  it('discards an obsolete owner A response after A/B/A', async () => {
    const owner = ref('A')
    const pending = []
    let resource
    const page = mount(probe(() => {
      resource = useIAMResource(() => owner.value, () => new Promise((resolve) => pending.push(resolve)))
      return () => null
    }))
    owner.value = 'B'
    owner.value = 'A'
    pending[2]({ revision: 'new-A' })
    await flushPromises()
    pending[0]({ revision: 'old-A' })
    await flushPromises()
    expect(resource.data.value.revision).toBe('new-A')
    page.unmount()
  })
  it('publishes an error before a queued refresh and never acknowledges old success', async () => {
    let resource
    const page = mount(probe(() => {
      resource = useIAMResource(() => 'owner', async () => ({ revision: '1' }))
      return () => null
    }))
    await flushPromises()
    let reject
    const write = resource.mutate(() => new Promise((_, fail) => { reject = fail }))
    await resource.refresh()
    reject(Object.assign(new Error('conflict'), { status: 409 }))
    expect(await write).toBe(null)
    expect(resource.busy.value).toBe(false)
    page.unmount()
  })
})
