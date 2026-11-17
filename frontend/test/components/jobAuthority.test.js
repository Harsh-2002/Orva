import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import JobAuthority from '@/components/jobs/JobAuthority.vue'
import Jobs from '@/views/Jobs.vue'
import { createPinia } from 'pinia'
import { useConfirmStore } from '@/stores/confirm'

const api = vi.hoisted(() => ({
  listJobs: vi.fn(),
  retryJob: vi.fn(),
  resumeJob: vi.fn(),
  deleteJob: vi.fn(),
  enqueueJob: vi.fn(),
  listFunctions: vi.fn(),
}))
vi.mock('@/api/endpoints', () => api)

let wrapper
afterEach(() => { wrapper?.unmount(); vi.clearAllMocks() })
const job = {
  id: '019a8000-0000-7000-8000-000000000001',
  function_id: '019a8000-0000-7000-8000-000000000002',
  creator_id: '019a8000-0000-7000-8000-000000000003',
  status: 'pending', authority_state: 'suspended', authority_reason: 'policy_denied', origin_kind: 'function',
  attempts: 0, max_attempts: 3,
}

it('separates stored execution status from authority, shows source/reason and emits only explicit resume', async () => {
  wrapper = mount(JobAuthority, { props: { job, canManage: true } })
  expect(wrapper.text()).toContain('Authority: suspended')
  expect(wrapper.text()).toContain('policy_denied')
  expect(wrapper.text()).toContain(job.creator_id)
  expect(wrapper.text()).toContain('Restoring permissions does not resume it.')
  expect(wrapper.emitted('resume')).toBeUndefined()
  await wrapper.get('button').trigger('click')
  expect(wrapper.emitted('resume')).toEqual([[job]])
  await wrapper.setProps({ busy: true })
  expect(wrapper.get('button').attributes('disabled')).toBeDefined()
})

it.each([
  { ...job, authority_state: 'active' },
  { ...job, authority_state: 'quarantined' },
  { ...job, status: 'succeeded' },
  { ...job, status: 'failed' },
])('never offers resume for active, quarantined or terminal work: $status/$authority_state', ({ ...row }) => {
  wrapper = mount(JobAuthority, { props: { job: row, canManage: true } })
  expect(wrapper.find('button').exists()).toBe(false)
})

it('has no mutation controls for a read-only/write-only server projection in either mobile or desktop subtree', async () => {
  api.listJobs.mockResolvedValue({ data: { jobs: [job, { ...job, id: `${job.id}-failed`, status: 'failed' }], async_controls: true, can_manage: false } })
  wrapper = mount(Jobs, {
    global: {
      plugins: [createPinia()],
      stubs: { Drawer: true, FilterSelect: true },
    },
  })
  await flushPromises()
  expect(wrapper.findAllComponents(JobAuthority)).toHaveLength(4)
  for (const child of wrapper.findAllComponents(JobAuthority)) {
    expect(child.props('canManage')).toBe(false)
    expect(child.find('button').exists()).toBe(false)
  }
  expect(wrapper.find('[title="Retry"]').exists()).toBe(false)
  expect(wrapper.find('[title="Delete"]').exists()).toBe(false)
  expect(wrapper.findAll('button').some(button => button.text() === 'Enqueue')).toBe(false)
  expect(api.resumeJob).not.toHaveBeenCalled()
})

it('resumes only the explicitly confirmed job and keeps both responsive controls busy until acknowledgement', async () => {
  const pinia = createPinia()
  const confirm = useConfirmStore(pinia)
  vi.spyOn(confirm, 'ask').mockResolvedValue(true)
  let acknowledge
  api.resumeJob.mockImplementation(() => new Promise(resolve => { acknowledge = resolve }))
  api.listJobs.mockResolvedValue({ data: { jobs: [job], async_controls: true, can_manage: true } })
  wrapper = mount(Jobs, { global: { plugins: [pinia], stubs: { Drawer: true, FilterSelect: true } } })
  await flushPromises()
  expect(api.resumeJob).not.toHaveBeenCalled()
  const controls = wrapper.findAllComponents(JobAuthority)
  await controls[0].get('button').trigger('click')
  await flushPromises()
  expect(confirm.ask).toHaveBeenCalledOnce()
  expect(api.resumeJob).toHaveBeenCalledExactlyOnceWith(job.id)
  for (const child of controls) expect(child.get('button').attributes('disabled')).toBeDefined()
  acknowledge({ data: { id: job.id, action: 'resume' } })
  await flushPromises()
  for (const child of controls) expect(child.get('button').attributes('disabled')).toBeUndefined()
})

it('ignores an older successful refresh after a confirmed resume and fresh authority', async () => {
  const pinia = createPinia()
  vi.spyOn(useConfirmStore(pinia), 'ask').mockResolvedValue(true)
  const response = row => ({ data: { jobs: [row], async_controls: true, can_manage: true } })
  api.listJobs.mockResolvedValue(response(job))
  api.resumeJob.mockResolvedValue({ data: { id: job.id, action: 'resume' } })
  wrapper = mount(Jobs, { global: { plugins: [pinia], stubs: { Drawer: true, FilterSelect: true } } })
  await flushPromises()
  let finishOld
  api.listJobs.mockImplementationOnce(() => new Promise(resolve => { finishOld = resolve }))
  await wrapper.findAll('button').find(button => button.text() === 'Refresh').trigger('click')
  api.listJobs.mockResolvedValue(response({ ...job, authority_state: 'active', status: 'succeeded' }))
  await wrapper.findComponent(JobAuthority).get('button').trigger('click')
  await flushPromises()
  finishOld(response(job))
  await flushPromises()
  expect(wrapper.findComponent(JobAuthority).props('job').authority_state).toBe('active')
  expect(wrapper.findComponent(JobAuthority).find('button').exists()).toBe(false)
})

it('ignores an older refresh failure after a newer successful snapshot', async () => {
  api.listJobs.mockResolvedValue({ data: { jobs: [job], async_controls: true, can_manage: true } })
  wrapper = mount(Jobs, { global: { plugins: [createPinia()], stubs: { Drawer: true, FilterSelect: true } } })
  await flushPromises()
  let rejectOld
  api.listJobs.mockImplementationOnce(() => new Promise((resolve, reject) => { rejectOld = reject }))
  const refresh = wrapper.findAll('button').find(button => button.text() === 'Refresh')
  await refresh.trigger('click')
  await refresh.trigger('click')
  await flushPromises()
  rejectOld(new Error('obsolete refresh failure'))
  await flushPromises()
  expect(wrapper.text()).not.toContain('obsolete refresh failure')
})
