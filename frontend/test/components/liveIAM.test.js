import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import LiveIAM from '@/scratch/LiveIAM.vue'
import FilterSelect from '@/components/common/FilterSelect.vue'
import { FN_ID, OTHER_FN_ID, makeSnapshot } from '../fixtures/permissionsFixtures.js'

let wrapper
afterEach(() => { wrapper?.unmount(); vi.unstubAllGlobals() })
const response = (body, headers = {}) => ({ ok: true, json: async () => body, headers: { get: k => headers[k] || null } })

it('uses only resolved owner-scoped metadata for calls, disables streams and discards obsolete invocation results', async () => {
  let finish
  const pending = new Promise(resolve => { finish = resolve })
  const transport = vi.fn(async path => {
    if (path === '/api/scratch/functions') return response([
      { id: FN_ID, name: 'scratch-node', runtime: 'node' },
      { id: OTHER_FN_ID, name: 'scratch-python', runtime: 'python' },
    ])
    if (path.startsWith('/fn/')) return pending
    return response(makeSnapshot({
      function: { id: FN_ID, name: 'scratch-node' },
      targets: [
        { id: FN_ID, label: 'scratch-node', state: 'resolved' },
        { id: OTHER_FN_ID, label: 'scratch-python', state: 'resolved' },
        { id: '01900000-0000-7000-8000-000000000003', label: 'hidden', state: 'unknown' },
      ],
    }))
  })
  vi.stubGlobal('fetch', transport)
  wrapper = mount(LiveIAM, {
    global: { stubs: { FunctionPermissions: true, FilterSelect: { props: ['options', 'modelValue'], template: '<div />' } } },
  })
  await flushPromises()
  expect(wrapper.find('[data-value="stream"]').attributes('disabled')).toBeDefined()
  await wrapper.find('[data-value="call"]').trigger('click')
  const picker = wrapper.findComponent(FilterSelect)
  expect(picker.props('options')).toEqual([{ value: OTHER_FN_ID, label: 'scratch-python' }])
  picker.vm.$emit('update:modelValue', OTHER_FN_ID)
  await flushPromises()
  const invoke = wrapper.findAll('button').find(b => b.text() === 'Invoke function call')
  await invoke.trigger('click')
  const [, options] = transport.mock.calls.find(([path]) => path.startsWith('/fn/'))
  expect(JSON.parse(options.body)).toEqual({ mode: 'call', target: 'scratch-python', marker: 'calls-workflow' })
  await wrapper.find('[data-value="kv"]').trigger('click')
  expect(options.signal.aborted).toBe(true)
  finish(response({ decision: 'ALLOWED' }))
  await flushPromises()
  expect(wrapper.find('[data-testid="invocation-result"]').exists()).toBe(false)
})
