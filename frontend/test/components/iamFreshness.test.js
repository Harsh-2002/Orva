import { afterEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { useIAMResource } from '@/composables/useIAMResource.js'
import { managedPoliciesAPI } from '@/api/managedPolicies.js'
import EffectiveAccess from '@/components/permissions/EffectiveAccess.vue'
import FunctionPermissionSources from '@/views/FunctionPermissionSources.vue'
import FunctionPermissions from '@/views/FunctionPermissions.vue'
import { DIGEST_A, FN_ID, OTHER_FN_ID } from '../fixtures/permissionsFixtures.js'

const deferred = () => {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const empty = () => ({ schema_version: 1, allow: [], inbound: [] })
const reader = () => ({ ...empty(), allow: [{ actions: ['kv.get'], resource: { own_kv: { key: 'demo' } }, context: 'execution' }] })
const snapshot = (revision = '1', policy = reader(), id = FN_ID) => ({
  function: { id, name: 'worker' },
  host: { revision, authority_epoch: revision, current_artifact: DIGEST_A, approved_artifact: DIGEST_A,
    review_required: false, quarantined: false, enforcement: 'scratch_opt_in', can_manage: true },
  inline_policy: empty(), effective_policy: policy, attachments: [], history: [],
})
const wrappers = []
afterEach(() => { while (wrappers.length) wrappers.pop().unmount() })
const mountProbe = (load) => {
  const owner = ref(FN_ID)
  let resource
  const wrapper = mount(defineComponent({
    setup() {
      resource = useIAMResource(() => owner.value, load)
      return () => h('div', [
        h('p', { role: 'alert' }, resource.error.value),
        resource.data.value && h(EffectiveAccess, { source: resource.data.value, unconfirmed: resource.fresh.value ? '' : 'stale' }),
      ])
    },
  }))
  wrappers.push(wrapper)
  return { resource, owner, wrapper }
}

describe('independent authority snapshot freshness (controlled component timings)', () => {
  it('own mutation broadcast invalidates freshness but never aborts its write', async () => {
    const request = deferred()
    const api = managedPoliciesAPI(() => request.promise)
    const { resource } = mountProbe(async () => snapshot())
    api.subscribeMutations(() => resource.invalidate())
    await flushPromises()
    let signal
    const pending = resource.mutate((s) => { signal = s; return api.inline(FN_ID, {}, s) })
    expect(resource.fresh.value).toBe(false)
    expect(signal.aborted).toBe(false)
    request.resolve(snapshot('2'))
    expect(await pending).not.toBe(null)
    expect(signal.aborted).toBe(false)
    // The old GET is below the acknowledgement's revision floor.
    expect(resource.fresh.value).toBe(false)
  })

  it('foreign invalidation aborts obsolete reads but not an in-flight mutation', async () => {
    const old = deferred(), write = deferred()
    const load = vi.fn().mockResolvedValueOnce(snapshot()).mockImplementationOnce(() => old.promise).mockResolvedValue(snapshot('2'))
    const { resource } = mountProbe(load)
    await flushPromises()
    void resource.refresh()
    const readSignal = load.mock.calls[1][1]
    let writeSignal
    const pending = resource.mutate((s) => { writeSignal = s; return write.promise })
    resource.invalidate()
    expect(readSignal.aborted).toBe(true)
    expect(writeSignal.aborted).toBe(false)
    old.resolve(snapshot('99'))
    write.resolve(snapshot('2'))
    await pending
    expect(resource.data.value.host.revision).toBe('2')
    expect(resource.fresh.value).toBe(true)
  })

  it('does not mistake a named definition revision for the affected function revision', async () => {
    const { resource } = mountProbe(async () => snapshot('2'))
    await flushPromises()
    await resource.mutate(async () => ({ policy: { id: OTHER_FN_ID, definition_revision: '99' } }))
    expect(resource.fresh.value).toBe(true)
    expect(resource.data.value.host.revision).toBe('2')
  })

  it.each([409, 403, undefined])('invalidates before ambiguous/%s failure and renders terminal error before independent refresh', async (status) => {
    const write = deferred(), read = deferred()
    const load = vi.fn().mockResolvedValueOnce(snapshot()).mockImplementation(() => read.promise)
    const { resource, wrapper } = mountProbe(load)
    await flushPromises()
    const pending = resource.mutate(() => write.promise)
    await nextTick()
    expect(wrapper.text()).toContain('Stale snapshot')
    expect(wrapper.text()).not.toContain('What this function may do now')
    expect(wrapper.find('[aria-label="Effective platform access"]').text()).not.toContain('Can read')
    expect(wrapper.text()).not.toContain('The last loaded list was empty')
    expect(wrapper.text()).toContain('Do not infer absence')
    write.reject(Object.assign(new Error('Ambiguous request failure'), { status }))
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).not.toBe('')
    expect(load).toHaveBeenCalledTimes(2)
    read.resolve(snapshot('2', empty()))
    await pending
    expect(resource.fresh.value).toBe(true)
    expect(wrapper.text()).toContain('No protected platform actions allowed.')
    expect(wrapper.get('[role="alert"]').text()).not.toBe('')
  })

  it('does not promote an old snapshot if commit succeeds but its independent GET fails', async () => {
    const load = vi.fn().mockResolvedValueOnce(snapshot()).mockRejectedValue(new Error('GET unavailable after commit'))
    const { resource, wrapper } = mountProbe(load)
    await flushPromises()
    await resource.mutate(async () => snapshot('2', empty()))
    expect(resource.data.value.host.revision).toBe('1')
    expect(resource.fresh.value).toBe(false)
    expect(wrapper.text()).toContain('Stale snapshot')
    expect(wrapper.text()).not.toContain('What this function may do now')
    expect(wrapper.text()).toContain('GET unavailable after commit')
  })

  it.each(['resolve', 'reject'])('rejects old mutation %s across owner ABA without changing the new owner snapshot', async (settle) => {
    const write = deferred()
    const { resource, owner } = mountProbe(async (id) => snapshot('2', empty(), id))
    await flushPromises()
    let signal
    const pending = resource.mutate((s) => { signal = s; return write.promise })
    owner.value = OTHER_FN_ID
    owner.value = FN_ID
    await flushPromises()
    expect(signal.aborted).toBe(true)
    if (settle === 'resolve') write.resolve(snapshot('99'))
    else write.reject(new Error('Obsolete failure'))
    expect(await pending).toBe(null)
    expect(resource.data.value.host.revision).toBe('2')
    expect(resource.fresh.value).toBe(true)
    expect(resource.error.value).toBe('')
  })

  it('rejects an obsolete same-owner ACK/read after a later operation starts', async () => {
    const oldRead = deferred()
    const load = vi.fn().mockResolvedValueOnce(snapshot()).mockImplementationOnce(() => oldRead.promise).mockResolvedValue(snapshot('3', empty()))
    const { resource } = mountProbe(load)
    await flushPromises()
    const first = resource.mutate(async () => snapshot('2'))
    await flushPromises()
    const second = resource.mutate(async () => snapshot('3', empty()))
    await second
    oldRead.resolve(snapshot('99'))
    expect(await first).toBe(null)
    expect(resource.data.value.host.revision).toBe('3')
    expect(resource.fresh.value).toBe(true)
  })

  it('late read errors and lower revisions cannot replace a newer same-owner response', async () => {
    const obsolete = deferred()
    const load = vi.fn().mockResolvedValueOnce(snapshot('2')).mockImplementationOnce(() => obsolete.promise).mockResolvedValue(snapshot('3', empty()))
    const { resource } = mountProbe(load)
    await flushPromises()
    void resource.refresh()
    await resource.refresh()
    obsolete.reject(new Error('Old GET failed'))
    await flushPromises()
    expect(resource.error.value).toBe('')
    expect(resource.fresh.value).toBe(true)
    load.mockResolvedValue(snapshot('2'))
    await resource.refresh()
    expect(resource.fresh.value).toBe(false)
    expect(resource.data.value.host.revision).toBe('3')
  })

  it('a response for the wrong owner never restores freshness', async () => {
    const { resource } = mountProbe(async () => snapshot('9', reader(), OTHER_FN_ID))
    await flushPromises()
    expect(resource.data.value).toBe(null)
    expect(resource.fresh.value).toBe(false)
  })

  it('refreshes effective sources independently after inline 409 without adopting the response as a draft acknowledgement', async () => {
    const fresh = deferred(), write = deferred()
    let reads = 0
    const api = {
      permissions: vi.fn(() => ++reads <= 2 ? Promise.resolve(snapshot()) : fresh.promise),
      inline: vi.fn(() => write.promise),
    }
    const w = mount(FunctionPermissionSources, { props: { functionId: FN_ID, api } })
    wrappers.push(w)
    await flushPromises()
    const inline = w.findComponent(FunctionPermissions)
    const baseline = inline.props('snapshot')
    inline.vm.$emit('save', { policy: reader(), expected: { revision: '1', currentArtifact: DIGEST_A, approvedArtifact: DIGEST_A } })
    await nextTick()
    expect(w.find('[aria-label="Effective platform access"]').text()).not.toContain('Can read')
    expect(w.text()).toContain('Stale source snapshot')
    write.reject({ status: 409 })
    await flushPromises()
    expect(inline.props('saveError').kind).toBe('conflict')
    fresh.resolve(snapshot('2', empty()))
    await flushPromises()
    expect(w.find('[aria-label="Effective platform access"]').text()).toContain('No protected platform actions allowed.')
    expect(inline.props('snapshot')).toEqual(baseline)
    expect(inline.props('saveError').kind).toBe('conflict')
    expect(inline.props('authorityUnconfirmed')).toBe('stale')
  })
})
