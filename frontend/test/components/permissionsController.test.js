/* eslint-disable vue/one-component-per-file -- Small lifecycle harnesses, not shipping components. */
import { defineComponent, h, KeepAlive, nextTick, ref, watch } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { usePermissions } from '@/composables/usePermissions.js'
import { permissionsAPI, permissionsWire } from '@/api/permissions.js'
import { FN_ID, OTHER_FN_ID, makeSnapshot } from '../fixtures/permissionsFixtures.js'

const deferred = () => {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const wrappers = []
afterEach(() => { while (wrappers.length) wrappers.pop().unmount() })
function setup(api) {
  const owner = ref(FN_ID)
  let controller
  const events = []
  const wrapper = mount(defineComponent({
    setup() {
      controller = usePermissions(() => owner.value, api)
      watch(() => [controller.saving.value, controller.saveError.value, controller.snapshot.value], (state) => events.push(state), { flush: 'sync' })
      return () => h('div')
    },
  }))
  wrappers.push(wrapper)
  return { owner, controller, events, wrapper }
}
const payload = () => {
  const s = makeSnapshot()
  return { policy: s.policy, expected: { revision: s.host.revision, currentArtifact: s.host.currentArtifact, approvedArtifact: s.host.approvedArtifact } }
}

describe('permissions HTTP wire', () => {
  it('maps only expectations and preserves decimal strings; review does not send policy', () => {
    const p = payload()
    p.expected.revision = '9007199254740993'
    expect(permissionsWire(p)).toEqual({ policy: p.policy, expected: {
      revision: '9007199254740993', current_artifact: p.expected.currentArtifact, approved_artifact: p.expected.approvedArtifact,
    } })
    expect(Object.keys(permissionsWire(p, true))).toEqual(['expected'])
    expect(() => permissionsWire({ ...p, actor: FN_ID })).toThrow()
    expect(() => permissionsWire({ ...p, expected: { ...p.expected, revision: 2 } })).toThrow()
  })
  it('rejects a successful response for another owner', async () => {
    const request = vi.fn().mockResolvedValue(makeSnapshot({ function: { id: OTHER_FN_ID, name: 'other' } }))
    await expect(permissionsAPI(request).get(FN_ID)).rejects.toThrow('Invalid permissions response')
  })
})

describe('owner-scoped request ordering', () => {
  it('discards stale success AND error across owner A/B/A', async () => {
    const reads = [deferred(), deferred(), deferred()]
    let n = 0
    const api = { get: vi.fn(() => reads[n++].promise) }
    const { owner, controller } = setup(api)
    owner.value = OTHER_FN_ID
    owner.value = FN_ID
    reads[2].resolve(makeSnapshot())
    await flushPromises()
    const current = controller.snapshot.value
    reads[0].resolve(makeSnapshot({ host: { revision: '99' } }))
    reads[1].reject(new Error('stale'))
    await flushPromises()
    expect(controller.snapshot.value).toEqual(current)
    expect(controller.loadError.value).toBe(null)
    expect(api.get.mock.calls[0][1].aborted).toBe(true)
  })
  it('invalidates an earlier refresh when saving and queues in-flight refresh until completion', async () => {
    const read = deferred(), save = deferred()
    const api = { get: vi.fn().mockResolvedValueOnce(makeSnapshot()).mockImplementationOnce(() => read.promise).mockResolvedValue(makeSnapshot({ host: { revision: '3' } })),
      save: vi.fn(() => save.promise) }
    const { controller, events } = setup(api)
    await flushPromises()
    void controller.refresh()
    const pending = controller.save(payload())
    void controller.refresh()
    read.resolve(makeSnapshot({ host: { revision: '9' } }))
    await flushPromises()
    expect(controller.snapshot.value.host.revision).not.toBe('9')
    save.resolve(makeSnapshot({ host: { revision: '2' } }))
    await pending
    expect(events.filter(([, , s]) => s?.host.revision === '2').every(([saving]) => saving === false)).toBe(true)
    expect(controller.snapshot.value.host.revision).toBe('3')
  })
  it.each([409, 403, 400, 500])('delivers terminal %s before a queued refresh and permits controller retry', async (status) => {
    const save = deferred()
    const api = { get: vi.fn().mockResolvedValueOnce(makeSnapshot()).mockResolvedValue(makeSnapshot({ host: { revision: '3' } })),
      save: vi.fn().mockImplementationOnce(() => save.promise).mockResolvedValue(makeSnapshot({ host: { revision: '4' } })) }
    const { controller, events } = setup(api)
    await flushPromises()
    const pending = controller.save(payload())
    void controller.refresh()
    save.reject({ status })
    await pending
    const failed = events.findIndex(([, error]) => error !== null)
    const refreshed = events.findIndex(([, , s]) => s?.host.revision === '3')
    expect(failed).toBeGreaterThanOrEqual(0)
    expect(refreshed).toBeGreaterThan(failed)
    expect(controller.saveError.value.kind).toBe(({ 409: 'conflict', 403: 'forbidden', 400: 'validation', 500: 'error' })[status])
    await controller.save(payload())
    expect(controller.saveError.value).toBe(null)
    expect(controller.snapshot.value.host.revision).toBe('4')
  })
  it.each(['success', 'error'])('discards a stale mutation %s and aborts on unmount', async (mode) => {
    const save = deferred()
    const api = { get: vi.fn().mockResolvedValue(makeSnapshot()), save: vi.fn(() => save.promise) }
    const { controller, owner, wrapper } = setup(api)
    await flushPromises()
    const pending = controller.save(payload())
    owner.value = OTHER_FN_ID
    owner.value = FN_ID
    await flushPromises()
    if (mode === 'success') save.resolve(makeSnapshot({ host: { revision: '99' } }))
    else save.reject({ status: 403 })
    await pending
    expect(controller.saveError.value).toBe(null)
    expect(controller.snapshot.value.host.revision).not.toBe('99')
    wrapper.unmount()
    expect(api.save.mock.calls[0][2].aborted).toBe(true)
  })
  it('cancels deactivated keep-alive instances and refreshes only on activation', async () => {
    const visible = ref(true)
    const pending = deferred()
    const api = { get: vi.fn().mockImplementationOnce(() => pending.promise).mockResolvedValue(makeSnapshot()) }
    let controller
    const Child = defineComponent({ setup() { controller = usePermissions(() => FN_ID, api); return () => h('div') } })
    const wrapper = mount(defineComponent({ setup: () => () => h(KeepAlive, null, { default: () => visible.value ? h(Child) : null }) }))
    wrappers.push(wrapper)
    visible.value = false
    await nextTick()
    expect(api.get.mock.calls[0][1].aborted).toBe(true)
    pending.resolve(makeSnapshot({ host: { revision: '99' } }))
    await flushPromises()
    expect(controller.snapshot.value).toBe(null)
    visible.value = true
    await flushPromises()
    expect(controller.snapshot.value.host.revision).not.toBe('99')
    expect(api.get).toHaveBeenCalledTimes(2)
  })
})
