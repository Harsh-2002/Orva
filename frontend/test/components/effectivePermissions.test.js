import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { useConfirmStore } from '@/stores/confirm.js'
import FunctionPermissionSources from '@/views/FunctionPermissionSources.vue'
import FunctionPermissions from '@/views/FunctionPermissions.vue'
import EffectiveAccess from '@/components/permissions/EffectiveAccess.vue'
import IAMPolicyDetail from '@/views/IAMPolicyDetail.vue'
import { CALLER_ID, DIGEST_A, DIGEST_B, FN_ID, OTHER_FN_ID } from '../fixtures/permissionsFixtures.js'

const empty = () => ({ schema_version: 1, allow: [], inbound: [] })
const reader = () => ({
  schema_version: 1,
  allow: [{ actions: ['kv.get'], resource: { own_kv: { key: 'demo' } }, context: 'execution' }],
  inbound: [],
})
const raw = (policy = reader()) => ({
  function: { id: FN_ID, name: 'test-worker' },
  host: { revision: '2', authority_epoch: '2', current_artifact: DIGEST_A, approved_artifact: DIGEST_A,
    review_required: false, quarantined: false, can_manage: true, enforcement: 'scratch_opt_in' },
  inline_policy: empty(),
  effective_policy: policy,
  attachments: [{ policy_id: OTHER_FN_ID, name: 'named-reader', version: '1', enabled: true, revoked: false, policy }],
  history: [],
})
const wrappers = []
const make = (component, props) => {
  const w = mount(component, { props })
  wrappers.push(w)
  return w
}
afterEach(() => { while (wrappers.length) wrappers.pop().unmount() })
const button = (w, label) => w.findAll('button').find((b) => b.text() === label)
const apiFor = (source) => ({
  permissions: vi.fn(async () => source),
  inline: vi.fn(async () => source),
  review: vi.fn(async () => source),
  get: vi.fn(async () => ({ policy: { membership_revision: '1' } })),
  mutate: vi.fn(async () => ({ owners: [{ before: source.effective_policy, after: empty() }] })),
})
const page = async (source) => {
  const api = apiFor(source)
  const w = make(FunctionPermissionSources, { functionId: FN_ID, api })
  await flushPromises()
  return { w, api }
}

describe('source-aware current access', () => {
  it('shows the effective managed grant first and labels every empty inline claim', async () => {
    const { w } = await page(raw())
    const effective = w.find('[aria-label="Effective platform access"]')
    expect(effective.text()).toContain('Can read the key “demo”')
    expect(effective.text()).toContain('1 permission')
    expect(w.findComponent(FunctionPermissions).props('snapshot').policy).toEqual(empty())
    expect(w.findComponent(FunctionPermissions).text()).toContain('No inline platform grants')
    expect(w.findComponent(FunctionPermissions).text()).toContain('No inline caller grants')
    expect(w.findComponent(FunctionPermissions).text()).not.toContain('No protected platform actions allowed')
    expect(w.findComponent(FunctionPermissions).text()).not.toContain('No callers accepted.')
    expect(w.findComponent(FunctionPermissions).text()).not.toContain('No permissions are stored.')
    expect(button(w, 'Create inline permissions')).toBeDefined()
    expect(w.text()).toContain('named-reader · Version 1')
    expect(w.text()).toContain('Source allowance:')
    expect(w.findAll('h1')).toHaveLength(1)
    expect(w.html().indexOf('Effective access')).toBeLessThan(w.html().indexOf('Inline permissions'))
  })

  it('shows managed inbound acceptance even when the inline caller list is empty', async () => {
    const p = { ...empty(), inbound: [{ caller_id: CALLER_ID, triggers: ['f2f', 'job'] }] }
    const { w } = await page(raw(p))
    const effective = w.find('[aria-label="Effective caller acceptance"]')
    expect(effective.text()).toContain(CALLER_ID)
    expect(effective.text()).toContain('a direct call or a background job')
    expect(effective.text()).not.toContain('No callers accepted')
    expect(w.findComponent(FunctionPermissions).text()).toContain('No inline caller grants')
    expect(w.text()).toContain('Source caller:')
    expect(w.text()).toContain('own outbound allowance')
  })

  it('shows actual none after the last outbound and inbound source is removed', async () => {
    const s = raw({ ...reader(), inbound: [{ caller_id: CALLER_ID, triggers: ['f2f'] }] })
    const w = make(EffectiveAccess, { source: s })
    await w.setProps({ source: { ...s, effective_policy: empty(), attachments: [] } })
    expect(w.find('[aria-label="Effective platform access"]').text()).toContain('No protected platform actions allowed.')
    expect(w.find('[aria-label="Effective caller acceptance"]').text()).toContain('No callers accepted.')
  })

  it.each(['quarantined', 'review_required'])('does not label retained %s grants as active', async (flag) => {
    const s = raw({ ...reader(), inbound: [{ caller_id: CALLER_ID, triggers: ['f2f'] }] })
    s.host[flag] = true
    const w = make(EffectiveAccess, { source: s })
    expect(w.text()).toContain('No effective grants are active')
    expect(w.find('[aria-label="Effective platform access"]').text()).not.toContain('Can read')
    expect(w.find('[aria-label="Effective caller acceptance"]').text()).not.toContain('may start')
    expect(w.find('[aria-label="Stored effective grants, not active"]').text()).toContain('Stored allowance, not active')
    expect(w.text()).toContain('Stored caller, not active')
  })

  it('never claims a stale artifact approval establishes active access', () => {
    const s = raw()
    s.host.current_artifact = DIGEST_B
    const w = make(EffectiveAccess, { source: s })
    expect(w.text()).toContain('Approval is missing, unknown or no longer matches')
    expect(w.find('[aria-label="Effective platform access"]').text()).not.toContain('Can read')
  })

  it('keeps unknown enforcement and refresh failures uncertain, not affirmative denial', async () => {
    const s = raw()
    s.host.enforcement = 'unknown'
    const w = make(EffectiveAccess, { source: s })
    expect(w.text()).toContain('Enforcement is not confirmed')
    expect(w.text()).not.toContain('No protected platform actions allowed.')
    expect(w.text()).toContain('Stored effective grants, active state not confirmed')
    expect(w.text()).not.toContain('Stored effective grants, not active')
    await w.setProps({ source: raw(), unconfirmed: 'error' })
    expect(w.text()).toContain('Current access is not confirmed')
    expect(w.text()).not.toContain('No protected platform actions allowed.')
    expect(w.text()).toContain('Stored effective grants, active state not confirmed')
    expect(w.text()).not.toContain('Stored effective grants, not active')
  })

  it('keeps startup and execution scopes visible in the human summary', () => {
    const p = reader()
    p.allow.push({ actions: ['kv.list'], resource: { own_kv: { prefix: 'config/' } }, context: 'bootstrap' })
    const w = make(EffectiveAccess, { source: raw(p) })
    expect(w.text()).toContain('at startup')
    expect(w.text()).toContain('Execution grants apply during a request')
    expect(w.text()).toContain('before a request')
  })

  it('retains source attribution when two pins supply overlapping grants', async () => {
    const s = raw()
    s.attachments.push({ ...s.attachments[0], policy_id: CALLER_ID, name: 'second-reader' })
    const { w } = await page(s)
    expect(w.text()).toContain('named-reader · Version 1')
    expect(w.text()).toContain('second-reader · Version 1')
    expect(w.text()).toContain('Removing a source keeps overlapping grants')
    expect(w.find('[aria-label="Effective platform access"]').text()).toContain('1 permission')
  })

  it('does not expose edit, attach or destructive controls for a read-only source snapshot', async () => {
    const s = raw()
    s.host.can_manage = false
    const { w } = await page(s)
    for (const label of ['Create inline permissions', 'Edit inline permissions', 'Manage policy attachments', 'Review detach', 'Detach reviewed policy']) {
      expect(button(w, label)).toBeUndefined()
    }
    expect(w.text()).toContain('Read only. An administrator')
  })

  it('disables source controls with a reason while busy and after a CAS failure', async () => {
    const { w, api } = await page(raw())
    let reject
    api.mutate.mockImplementation(() => new Promise((_, fail) => { reject = fail }))
    await button(w, 'Review detach').trigger('click')
    await flushPromises()
    expect(button(w, 'Review detach').attributes('disabled')).toBeDefined()
    expect(button(w, 'Review detach').attributes('title')).toContain('in progress')
    reject(Object.assign(new Error('stale'), { status: 409 }))
    await flushPromises()
    expect(button(w, 'Review detach').attributes('disabled')).toBeDefined()
    expect(button(w, 'Review detach').attributes('title')).toContain('Refresh')
    expect(button(w, 'Detach reviewed policy')).toBeUndefined()
  })

  it('hides a managed definition editor and attachment controls when capabilities are false', async () => {
    const api = { get: async () => ({ can_manage: false, policy: { latest_version: '1', revoked: false },
      version: { policy: reader() }, attachments: [] }) }
    const w = make(IAMPolicyDetail, { policyId: OTHER_FN_ID, api })
    await flushPromises()
    for (const label of ['Create new version', 'Review attachment', 'Review global revoke', 'Review deletion']) {
      expect(button(w, label)).toBeUndefined()
    }
  })

  it('offers Review and approve where an inactive function is noticed, and approves with current expectations', async () => {
    setActivePinia(createPinia())
    const s = raw()
    s.host.review_required = true
    s.host.approved_artifact = null
    const { w, api } = await page(s)
    const panel = w.find('[data-testid="permissions-approval"]')
    expect(panel.exists()).toBe(true)
    expect(panel.text()).toContain('Not active yet')
    const confirm = useConfirmStore()
    confirm.ask = vi.fn(async () => true)
    await w.find('[data-testid="permissions-approve"]').trigger('click')
    await flushPromises()
    expect(confirm.ask).toHaveBeenCalledTimes(1)
    expect(api.review).toHaveBeenCalledTimes(1)
    expect(api.review.mock.calls[0][0]).toBe(FN_ID)
    expect(api.review.mock.calls[0][1]).toEqual({
      expected: { revision: '2', current_artifact: DIGEST_A, approved_artifact: null },
    })
  })

  it('does not approve when the operator cancels the confirmation', async () => {
    setActivePinia(createPinia())
    const s = raw()
    s.host.quarantined = true
    const { w, api } = await page(s)
    const confirm = useConfirmStore()
    confirm.ask = vi.fn(async () => false)
    await w.find('[data-testid="permissions-approve"]').trigger('click')
    await flushPromises()
    expect(api.review).not.toHaveBeenCalled()
  })

  it('tells a read-only operator that an administrator must approve, without an approve control', async () => {
    const s = raw()
    s.host.review_required = true
    s.host.can_manage = false
    const { w } = await page(s)
    expect(w.find('[data-testid="permissions-approval"]').text()).toContain('An administrator must review and approve')
    expect(w.find('[data-testid="permissions-approve"]').exists()).toBe(false)
  })

  it('shows no approval prompt once the current deployment is approved', async () => {
    const { w } = await page(raw())
    expect(w.find('[data-testid="permissions-approval"]').exists()).toBe(false)
  })
})
