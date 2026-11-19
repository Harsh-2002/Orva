import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import { defineComponent, h, KeepAlive, nextTick, ref } from 'vue'
import CronJobs from '@/views/CronJobs.vue'
import JobAuthority from '@/components/jobs/JobAuthority.vue'
import { useConfirmStore } from '@/stores/confirm'

const api = vi.hoisted(() => ({
  listCronSchedules: vi.fn(), createCronSchedule: vi.fn(), updateCronSchedule: vi.fn(),
  deleteCronSchedule: vi.fn(), resumeCronSchedule: vi.fn(), listFunctions: vi.fn(),
  browserTimezone: () => 'UTC',
}))
vi.mock('@/api/endpoints', () => api)
let wrapper
afterEach(() => { wrapper?.unmount(); vi.clearAllMocks() })
const row = {
  id: '019a8000-0000-7000-8000-000000000001', function_id: '019a8000-0000-7000-8000-000000000002',
  function_name: 'cron-node', cron_expression: '@daily', enabled: true, origin_kind: 'function',
  creator_id: '019a8000-0000-7000-8000-000000000002', authority_state: 'suspended', authority_reason: 'revoked',
  config_revision: '9007199254740993', authority_generation: '2',
}
const response = (rows = [row], manage = true) => ({ data: { schedules: rows, cron_controls: true, can_manage: manage } })
const mountPage = (pinia = createPinia()) => mount(CronJobs, { global: { plugins: [pinia], stubs: { Modal: true } } })
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }

it('shows authority, source, revision and enabled separately; read-only cannot mutate either responsive branch', async () => {
  api.listCronSchedules.mockResolvedValue(response([row, { ...row, id: 'quarantine', authority_state: 'quarantined', origin_kind: 'legacy_unknown' }], false))
  wrapper = mountPage()
  await flushPromises()
  expect(wrapper.text()).toContain('Read-only')
  expect(wrapper.text()).toContain('Enabled')
  expect(wrapper.text()).not.toContain('Active')
  expect(wrapper.text()).toContain('Resume unavailable')
  expect(wrapper.text()).toContain(row.config_revision)
  expect(wrapper.text()).toContain('Every day at 00:00')
  expect(wrapper.text()).not.toContain('Invalid format')
  expect(wrapper.findAllComponents(JobAuthority)).toHaveLength(4)
  expect(wrapper.findAll('button').some(button => /Resume schedule|Delete|New schedule/.test(button.text()))).toBe(false)
  expect(wrapper.find('[title="Enable schedule"]').exists()).toBe(false)
  expect(api.resumeCronSchedule).not.toHaveBeenCalled()
})

it.each([
  ['@every 2s', 'Every 2 seconds'],
  ['@every 1h30m', 'Every 1 hour 30 minutes'],
  ['@hourly', 'Every hour at minute 0'],
  ['@every 1.5s', 'Schedule expression: @every 1.5s (server validation required)'],
  ['@unknown', 'Unrecognized descriptor: @unknown'],
])('renders advisory wording for %s in both responsive branches without changing authority', async (expression, description) => {
  api.listCronSchedules.mockResolvedValue(response([{ ...row, cron_expression: expression }], false))
  wrapper = mountPage()
  await flushPromises()
  const descriptions = wrapper.findAll('div, span').filter(node => node.text().replace(/\s+/g, ' ') === `${description} · UTC`)
  expect(descriptions.length).toBeGreaterThanOrEqual(2)
  expect(wrapper.text()).not.toContain('Invalid format')
  expect(wrapper.text()).toContain(row.config_revision)
  expect(wrapper.text()).toContain('Read-only')
  expect(api.resumeCronSchedule).not.toHaveBeenCalled()
  expect(api.updateCronSchedule).not.toHaveBeenCalled()
})

it('uses the inspected string CAS and retains terminal 409 through refresh without an optimistic active claim', async () => {
  const pinia = createPinia()
  vi.spyOn(useConfirmStore(pinia), 'ask').mockResolvedValue(true)
  api.listCronSchedules.mockResolvedValue(response())
  const pending = deferred()
  api.resumeCronSchedule.mockReturnValue(pending.promise)
  wrapper = mountPage(pinia)
  await flushPromises()
  await wrapper.findComponent(JobAuthority).get('button').trigger('click')
  await flushPromises()
  expect(api.resumeCronSchedule).toHaveBeenCalledExactlyOnceWith(row.id, row.function_id, {
    expected_config_revision: row.config_revision, expected_authority_generation: '2',
  })
  pending.reject({ response: { status: 409 } })
  await flushPromises()
  expect(wrapper.text()).toContain('Schedule changed or is not eligible')
  expect(wrapper.text()).toContain('unknown (stale snapshot)')
  expect(wrapper.findAllComponents(JobAuthority).every(child => !child.find('button').exists())).toBe(true)
  const refresh = wrapper.findAll('button').find(button => button.text() === 'Refresh')
  await refresh.trigger('click')
  await flushPromises()
  expect(wrapper.text()).toContain('Schedule changed or is not eligible')
  expect(wrapper.text()).toContain('suspended')
  expect(api.resumeCronSchedule).toHaveBeenCalledTimes(1)
})

it('keeps network refresh failure stale and ignores reversed obsolete failures', async () => {
  const first = deferred(), second = deferred()
  api.listCronSchedules.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
  wrapper = mountPage()
  const reload = wrapper.vm.loadJobs()
  second.resolve(response())
  await reload
  first.reject(new Error('obsolete failure'))
  await flushPromises()
  expect(wrapper.text()).not.toContain('obsolete failure')
  api.listCronSchedules.mockRejectedValueOnce(new Error('network unavailable'))
  await wrapper.vm.loadJobs()
  await flushPromises()
  expect(wrapper.text()).toContain('network unavailable')
  expect(wrapper.text()).toContain('unknown (stale snapshot)')
})

it('invalidates requests on deactivation and reloads only on activation', async () => {
  const pending = deferred()
  api.listCronSchedules.mockReturnValueOnce(pending.promise).mockResolvedValue(response())
  const visible = ref(true)
  const Host = defineComponent({ setup: () => () => h(KeepAlive, null, { default: () => visible.value ? h(CronJobs) : h('div', 'Other') }) })
  wrapper = mount(Host, { global: { plugins: [createPinia()], stubs: { Modal: true } } })
  visible.value = false
  await nextTick()
  pending.reject(new Error('obsolete failure'))
  await flushPromises()
  expect(wrapper.text()).not.toContain('obsolete failure')
  visible.value = true
  await flushPromises()
  expect(api.listCronSchedules).toHaveBeenCalledTimes(2)
  expect(wrapper.text()).toContain('suspended')
})
