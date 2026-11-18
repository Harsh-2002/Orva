import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import HistoryInspector from '@/components/history/HistoryInspector.vue'
const api = vi.hoisted(() => ({ historyRequest: vi.fn(), listFunctions: vi.fn() }))
vi.mock('@/api/history', () => ({ historyRequest: api.historyRequest }))
vi.mock('@/api/endpoints', () => ({ listFunctions: api.listFunctions }))
let wrapper
afterEach(() => { wrapper?.unmount(); vi.resetAllMocks() })
const row = { kind: 'job', id: 'old-job', reserved: false, metadata: { target_id: 'old-target', origin: { origin_kind: 'function' }, eligible_at: '2099-01-01' } }
function setup(manage = true) {
  api.historyRequest.mockImplementation(async op => ({ data: op === 'list' ? [row] : op === 'get' ? row : { can_manage: manage, rows_used: '3', max_rows: '100000', bytes_used: '1000', max_bytes: '67108864', rows_reserved: '0', bytes_reserved: '0' } }))
  api.listFunctions.mockResolvedValue({ data: { functions: [{ id: 'old-target', name: 'same-name', status: 'active' }, { id: 'fresh', name: 'same-name', status: 'active' }] } })
  wrapper = mount(HistoryInspector, { global: { stubs: { FilterSelect: { props: ['modelValue', 'options'], emits: ['update:modelValue'], template: '<button data-target @click="$emit(\'update:modelValue\', options[0].value)">Pick target</button>' } } } })
}
const click = async text => {
  await wrapper.findAll('button').find(b => b.text() === text).trigger('click'); await flushPromises()
}
it('read-only users inspect metadata, never receive mutation controls', async () => {
  setup(false); await flushPromises(); await click('Inspect')
  expect(wrapper.text()).toContain('Original payload is unavailable')
  expect(wrapper.text()).toContain('function')
  expect(wrapper.text()).toContain('old-target')
  expect(wrapper.text()).not.toContain('Create fresh work')
  expect(wrapper.text()).not.toContain('Review purge')
})
it('requires explicit target/fresh payload/review; preserves draft on conflict and blocks stale authority', async () => {
  setup(); await flushPromises(); await click('Inspect'); await click('Create fresh work')
  expect(wrapper.find('[data-target]').exists()).toBe(true)
  expect(wrapper.get('textarea').element.value).toBe('')
  expect(wrapper.text()).toContain('No target selected')
  await wrapper.get('[data-target]').trigger('click')
  await wrapper.get('textarea').setValue('{"fresh":true}')
  await wrapper.get('input[aria-label="Run at (UTC RFC3339)"]').setValue('2099-01-01T00:00:00Z')
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(wrapper.text()).toContain('Confirm new operator work')
  expect(api.historyRequest.mock.calls.some(([op]) => op === 'reissue')).toBe(false)
  api.historyRequest.mockRejectedValueOnce({ response: { status: 409 } })
  await click('Confirm new work')
  expect(wrapper.text()).toContain('Snapshot is stale')
  expect(wrapper.get('textarea').element.value).toBe('{"fresh":true}')
  expect(wrapper.findAll('button').some(b => b.text() === 'Confirm new work')).toBe(false)
})

const deferred = () => {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const cap = { can_manage: true, rows_used: '3', max_rows: '100000', bytes_used: '1000', max_bytes: '67108864', rows_reserved: '0', bytes_reserved: '0' }
const snapshot = async (op, raw) => ({ data: op === 'list' ? [row] : op === 'get' ? { ...row, ...JSON.parse(raw) } : cap })
async function enterDraft() {
  await click('Inspect'); await click('Create fresh work')
  await wrapper.get('[data-target]').trigger('click')
  await wrapper.get('textarea').setValue('{"fresh":true}')
  await wrapper.get('input[aria-label="Run at (UTC RFC3339)"]').setValue('2099-01-01T00:00:00Z')
}
it.each([
  ['reissue', 409], ['reissue', undefined], ['purge', 409], ['purge', undefined],
])('retains the complete draft after %s refusal/network failure (%s), and only a matching refresh restores freshness', async (op, status) => {
  setup(); await flushPromises(); await enterDraft()
  if (op === 'reissue') { await wrapper.get('form').trigger('submit'); await flushPromises() }
  else await click('Review purge')
  api.historyRequest.mockRejectedValueOnce({ response: { status } })
  await click(op === 'reissue' ? 'Confirm new work' : 'Confirm purge')
  expect(wrapper.text()).toContain('Draft retained')
  expect(wrapper.text()).not.toContain('Created new operator work')
  expect(wrapper.text()).not.toContain('Selected metadata purged')
  expect(wrapper.get('textarea').element.value).toBe('{"fresh":true}')
  expect(wrapper.get('input[aria-label="Run at (UTC RFC3339)"]').element.value).toBe('2099-01-01T00:00:00Z')
  expect(wrapper.text()).toContain('stale or unknown')
  api.historyRequest.mockImplementation(async (action, raw) => action === 'get'
    ? { data: { ...row, id: 'other-archive' } } : snapshot(action, raw))
  await click('Refresh history')
  expect(wrapper.text()).toContain('Snapshot is stale')
  expect(wrapper.findAll('button').some(b => b.text() === 'Review fresh work')).toBe(false)
  api.historyRequest.mockImplementation(snapshot)
  await click('Refresh history')
  expect(wrapper.text()).not.toContain('stale or unknown')
  expect(wrapper.get('textarea').element.value).toBe('{"fresh":true}')
  expect(wrapper.text()).not.toContain('Confirm new operator work')
  expect(wrapper.findAll('button').some(b => b.text() === 'Review fresh work')).toBe(true)
})
it('keeps a pending mutation independent from read abort and active ABA; its late success cannot acknowledge or erase the retained draft', async () => {
  setup(); await flushPromises(); await enterDraft()
  await wrapper.get('form').trigger('submit'); await flushPromises()
  const pending = deferred()
  api.historyRequest.mockImplementation((op, raw, signal) => {
    if (op === 'reissue') { expect(signal).toBeUndefined(); return pending.promise }
    return snapshot(op, raw)
  })
  await click('Confirm new work')
  await wrapper.setProps({ active: false }); await wrapper.setProps({ active: true })
  expect(wrapper.text()).toContain('Mutation outcome unknown')
  pending.resolve({ data: { new_id: '019a8000-0000-7000-8000-000000000001', origin: 'operator' } })
  await flushPromises()
  expect(wrapper.text()).not.toContain('Created new operator work')
  expect(wrapper.get('textarea').element.value).toBe('{"fresh":true}')
  await click('Refresh history')
  expect(wrapper.text()).not.toContain('stale or unknown')
})
it('ignores obsolete reads and stale errors after newer mutation acknowledgement', async () => {
  setup(); await flushPromises(); await enterDraft()
  const old = deferred()
  api.historyRequest.mockImplementation((op, raw) => op === 'list' ? old.promise : snapshot(op, raw))
  await click('Refresh history')
  await wrapper.setProps({ active: false })
  api.historyRequest.mockImplementation(snapshot)
  await wrapper.setProps({ active: true }); await flushPromises()
  await wrapper.get('form').trigger('submit'); await flushPromises()
  api.historyRequest.mockImplementation((op, raw) => op === 'reissue'
    ? { data: { new_id: '019a8000-0000-7000-8000-000000000001', origin: 'operator' } } : snapshot(op, raw))
  await click('Confirm new work')
  old.reject(new Error('obsolete read')); await flushPromises()
  expect(wrapper.text()).toContain('Created new operator work')
  expect(wrapper.text()).not.toContain('History refresh failed')
})
it('correlates archive A/B/A responses and retains a separate draft per exact kind/UUID', async () => {
  setup(); await flushPromises(); await enterDraft()
  const b = { ...row, id: 'archive-b', metadata: { ...row.metadata, target_id: 'target-b' } }
  api.historyRequest.mockImplementation(async (op, raw) => ({ data: op === 'list' ? [row, b] : op === 'get' ? (JSON.parse(raw).id === b.id ? b : row) : cap }))
  await click('Refresh history')
  await wrapper.findAll('button').find(button => button.attributes('aria-label') === 'Inspect job archive-b').trigger('click')
  await flushPromises(); await click('Create fresh work')
  await wrapper.get('textarea').setValue('{"draft":"B"}')
  await wrapper.findAll('button').find(button => button.attributes('aria-label') === 'Inspect job old-job').trigger('click')
  await flushPromises()
  expect(wrapper.get('textarea').element.value).toBe('{"fresh":true}')
  await wrapper.findAll('button').find(button => button.attributes('aria-label') === 'Inspect job archive-b').trigger('click')
  await flushPromises()
  expect(wrapper.get('textarea').element.value).toBe('{"draft":"B"}')
})
it('does not publish an obsolete read after deactivation or a failed newer refresh', async () => {
  setup(); await flushPromises()
  let resolve
  api.historyRequest.mockImplementation(() => new Promise(r => { resolve = r }))
  await wrapper.setProps({ active: false })
  await wrapper.setProps({ active: true })
  await wrapper.setProps({ active: false })
  resolve({ data: { can_manage: true } }); await flushPromises()
  expect(wrapper.text()).toContain('stale or unknown')
  expect(wrapper.findAll('button').some(b => b.text() === 'Create fresh work')).toBe(false)
})
