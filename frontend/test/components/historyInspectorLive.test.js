import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import { defineComponent, h, KeepAlive, ref } from 'vue'
import HistoryInspector from '@/components/history/HistoryInspector.vue'
// Bounded regressions for cases the live browser run cannot schedule deterministically.
// These are component results; the live fixture is the runtime evidence.
const api = vi.hoisted(() => ({ historyRequest: vi.fn(), listFunctions: vi.fn() }))
vi.mock('@/api/history', () => ({ historyRequest: api.historyRequest }))
vi.mock('@/api/endpoints', () => ({ listFunctions: api.listFunctions }))
let wrapper
afterEach(() => { wrapper?.unmount(); vi.resetAllMocks() })
const job = { kind: 'job', id: 'same-id', reserved: false, metadata: { target_id: 'old-target', origin: { origin_kind: 'function' }, eligible_at: '2099-01-01' } }
const cron = { kind: 'cron', id: 'same-id', reserved: false, metadata: { target_id: 'old-target', origin: { origin_kind: 'function' }, eligible_at: '2099-01-01' } }
const cap = (manage = true) => ({ can_manage: manage, rows_used: '3', max_rows: '100000', bytes_used: '1000', max_bytes: '67108864', rows_reserved: '0', bytes_reserved: '0' })
const pick = { props: ['modelValue', 'options'], emits: ['update:modelValue'], template: '<button data-target @click="$emit(\'update:modelValue\', options[0].value)">Pick target</button>' }
const stubs = { global: { stubs: { FilterSelect: pick } } }
const rows = [job, cron]
const reply = (manage = () => true) => async (op, raw) => ({ data: op === 'list' ? rows : op === 'get' ? rows.find(r => r.kind === JSON.parse(raw).kind) : cap(manage()) })
const click = async text => { await wrapper.findAll('button').find(b => b.text() === text).trigger('click'); await flushPromises() }
const inspect = async kind => { await wrapper.findAll('button').find(b => b.attributes('aria-label') === `Inspect ${kind} same-id`).trigger('click'); await flushPromises() }
const functions = () => api.listFunctions.mockResolvedValue({ data: { functions: [{ id: 'fresh', name: 'n', status: 'active' }] } })

it('keeps job and cron drafts separate when both archives share a UUID (mixed-kind A/B/A)', async () => {
  functions(); api.historyRequest.mockImplementation(reply())
  wrapper = mount(HistoryInspector, stubs); await flushPromises()
  await inspect('job'); await click('Create fresh work')
  await wrapper.get('textarea').setValue('{"kind":"job"}')
  await inspect('cron'); await click('Create fresh work')
  expect(wrapper.get('textarea').element.value).toBe('')
  await wrapper.get('textarea').setValue('{"kind":"cron"}')
  await inspect('job')
  expect(wrapper.get('textarea').element.value).toBe('{"kind":"job"}')
  await inspect('cron')
  expect(wrapper.get('textarea').element.value).toBe('{"kind":"cron"}')
})

it('withdraws confirmation when capability is downgraded during review', async () => {
  functions(); let manage = true
  api.historyRequest.mockImplementation(reply(() => manage))
  wrapper = mount(HistoryInspector, stubs); await flushPromises()
  await inspect('job'); await click('Create fresh work')
  await wrapper.get('[data-target]').trigger('click')
  await wrapper.get('textarea').setValue('{"fresh":1}')
  await wrapper.get('input[aria-label="Run at (UTC RFC3339)"]').setValue('2099-01-01T00:00:00Z')
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(wrapper.text()).toContain('Confirm new operator work')
  manage = false
  await click('Refresh history')
  expect(wrapper.text()).toContain('Read-only access')
  expect(wrapper.findAll('button').some(b => b.text() === 'Confirm new work')).toBe(false)
  expect(api.historyRequest.mock.calls.some(([op]) => op === 'reissue')).toBe(false)
})

it('ignores a mutation rejection that arrives after deactivation and never mints an acknowledgement', async () => {
  functions(); let reject
  api.historyRequest.mockImplementation(async (op, raw) => {
    if (op === 'reissue') return new Promise((_, no) => { reject = no })
    return reply()(op, raw)
  })
  wrapper = mount(HistoryInspector, stubs); await flushPromises()
  await inspect('cron'); await click('Create fresh work')
  await wrapper.get('[data-target]').trigger('click')
  await wrapper.get('textarea').setValue('{"fresh":1}')
  for (const [label, value] of [['name', 'n'], ['expression', '@every 2s'], ['timezone', 'UTC']]) await wrapper.get(`input[aria-label="${label}"]`).setValue(value)
  await wrapper.get('form').trigger('submit'); await flushPromises()
  await click('Confirm new work')
  await wrapper.setProps({ active: false })
  reject({ response: { status: 409 } }); await flushPromises()
  expect(wrapper.text()).not.toContain('Created new operator work')
  await wrapper.setProps({ active: true }); await flushPromises()
  expect(wrapper.get('textarea').element.value).toBe('{"fresh":1}')
  expect(wrapper.text()).not.toContain('Confirm new operator work')
})

it('KeepAlive deactivation withdraws review while keeping the draft; reactivation re-reads', async () => {
  functions(); api.historyRequest.mockImplementation(reply())
  const showing = ref(true)
  const Host = defineComponent({ setup: () => () => h(KeepAlive, null, showing.value ? h(HistoryInspector) : h('div', { id: 'other' })) })
  wrapper = mount(Host, stubs); await flushPromises()
  await inspect('job'); await click('Create fresh work')
  await wrapper.get('[data-target]').trigger('click')
  await wrapper.get('textarea').setValue('{"fresh":2}')
  await wrapper.get('input[aria-label="Run at (UTC RFC3339)"]').setValue('2099-01-01T00:00:00Z')
  await wrapper.get('form').trigger('submit'); await flushPromises()
  expect(wrapper.text()).toContain('Confirm new operator work')
  const before = api.historyRequest.mock.calls.filter(([op]) => op === 'list').length
  showing.value = false; await flushPromises()
  showing.value = true; await flushPromises()
  expect(api.historyRequest.mock.calls.filter(([op]) => op === 'list').length).toBeGreaterThan(before)
  expect(wrapper.get('textarea').element.value).toBe('{"fresh":2}')
  expect(wrapper.text()).not.toContain('Confirm new operator work')
  // Regression: management capability must survive deactivation/reactivation.
  expect(wrapper.findAll('button').some(b => b.text() === 'Review fresh work')).toBe(true)
  expect(wrapper.findAll('button').some(b => b.text() === 'Review purge')).toBe(true)
})

it('keeps the draft fieldset shrinkable so phone-width controls cannot overflow the card', async () => {
  functions(); api.historyRequest.mockImplementation(reply())
  wrapper = mount(HistoryInspector, stubs); await flushPromises()
  await inspect('job'); await click('Create fresh work')
  // A fieldset defaults to min-width: min-content; without min-w-0 its inputs push past the card at 390px.
  expect(wrapper.get('fieldset').classes()).toContain('min-w-0')
})
