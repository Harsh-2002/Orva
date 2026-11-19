/* global ReadableStream */
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, expect, it, vi } from 'vitest'
import LiveIAM from '@/scratch/LiveIAM.vue'
import FilterSelect from '@/components/common/FilterSelect.vue'
import { FN_ID, OTHER_FN_ID, makeSnapshot } from '../fixtures/permissionsFixtures.js'

let wrapper
afterEach(() => { wrapper?.unmount(); vi.unstubAllGlobals() })
const response = (body, headers = {}) => ({ ok: true, json: async () => body, headers: { get: k => headers[k] || null } })
const snapshotFor = (id, name) => makeSnapshot({
  function: { id, name },
  targets: [
    { id: FN_ID, label: 'scratch-node', state: 'resolved' },
    { id: OTHER_FN_ID, label: 'scratch-python', state: 'resolved' },
    { id: '01900000-0000-7000-8000-000000000003', label: 'hidden', state: 'unknown' },
  ],
})
const functions = [
  { id: FN_ID, name: 'scratch-node', runtime: 'node' },
  { id: OTHER_FN_ID, name: 'scratch-python', runtime: 'python' },
]
const mountView = (transport) => {
  vi.stubGlobal('fetch', transport)
  wrapper = mount(LiveIAM, {
    global: { stubs: { FunctionPermissions: true, FilterSelect: { props: ['options', 'modelValue'], template: '<div />' } } },
  })
}
const baseTransport = (stream) => vi.fn(async (path, options) => {
  if (path === '/api/scratch/functions') return response(functions)
  if (path.endsWith('/stream')) return stream(path, options)
  if (path.startsWith('/fn/')) return new Promise(() => {})
  const id = path.split('/')[4]
  return response(snapshotFor(id, functions.find(f => f.id === id).name))
})
const hand = () => {
  let controller
  const body = new ReadableStream({ start(c) { controller = c } })
  return { controller, body, enqueue: (t) => controller.enqueue(new TextEncoder().encode(t)) }
}

it('uses only resolved owner-scoped metadata for calls and discards obsolete invocation results', async () => {
  let finish
  const pending = new Promise(resolve => { finish = resolve })
  const transport = vi.fn(async path => {
    if (path === '/api/scratch/functions') return response(functions)
    if (path.startsWith('/fn/')) return pending
    return response(snapshotFor(FN_ID, 'scratch-node'))
  })
  mountView(transport)
  await flushPromises()
  expect(wrapper.find('[data-value="stream"]').attributes('disabled')).toBeUndefined()
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

it('streams from the selected function, survives owner and operation changes, and Stop aborts it', async () => {
  const feed = hand()
  let streamOptions
  const transport = baseTransport((_path, options) => {
    streamOptions = options
    return { ok: true, body: feed.body, headers: { get: k => ({ 'X-Orva-Execution-ID': 'exec-9', 'X-Scratch-Worker': 'w-9', 'X-Scratch-Artifact': 'a-9' })[k] || null } }
  })
  mountView(transport)
  await flushPromises()
  await wrapper.find('[data-value="stream"]').trigger('click')
  const start = () => wrapper.findAll('button').find(b => b.text() === 'Start stream')
  expect(start().attributes('disabled')).toBeDefined()
  wrapper.findComponent(FilterSelect).vm.$emit('update:modelValue', OTHER_FN_ID)
  await flushPromises()
  expect(start().attributes('disabled')).toBeUndefined()
  await start().trigger('click')
  await flushPromises()
  const path = transport.mock.calls.find(([p]) => p.endsWith('/stream'))[0]
  expect(path).toBe(`/fn/${FN_ID}/stream`)
  expect(JSON.parse(streamOptions.body)).toMatchObject({ mode: 'stream-call', target: 'scratch-python', count: 5 })
  feed.enqueue('chunk-001\nchunk-002\n')
  await flushPromises()
  expect(wrapper.find('[data-testid="stream-count"]').text()).toContain('2 of 5')
  expect(wrapper.find('[data-testid="stream-phase"]').text()).toBe('Streaming')
  expect(start().attributes('disabled')).toBeDefined()

  // Looking at the other function's permissions must not cancel or relabel it.
  await wrapper.find('[data-value="kv"]').trigger('click')
  await wrapper.findAll('[role="radio"]').find(b => b.text() === 'Python').trigger('click')
  await flushPromises()
  expect(streamOptions.signal.aborted).toBe(false)
  expect(wrapper.find('[data-testid="stream-panel"]').text()).toContain('scratch-node reading scratch-python')
  feed.enqueue('chunk-003\n')
  await flushPromises()
  expect(wrapper.find('[data-testid="stream-count"]').text()).toContain('3 of 5')

  await wrapper.findAll('button').find(b => b.text() === 'Stop').trigger('click')
  expect(streamOptions.signal.aborted).toBe(true)
  expect(wrapper.find('[data-testid="stream-phase"]').text()).toBe('Stopped')
  expect(wrapper.find('[data-testid="stream-note"]').text()).toContain('3 chunks were received')
})

it('shows a denial honestly: earlier chunks stay listed and no private error text is rendered', async () => {
  const feed = hand()
  mountView(baseTransport(() => ({ ok: true, body: feed.body, headers: { get: () => null } })))
  await flushPromises()
  await wrapper.find('[data-value="stream"]').trigger('click')
  wrapper.findComponent(FilterSelect).vm.$emit('update:modelValue', OTHER_FN_ID)
  await flushPromises()
  await wrapper.findAll('button').find(b => b.text() === 'Start stream').trigger('click')
  await flushPromises()
  feed.enqueue('chunk-001\nerror:IAM_DENIED\n')
  feed.controller.close()
  await flushPromises()
  expect(wrapper.find('[data-testid="stream-phase"]').text()).toBe('Denied')
  const note = wrapper.find('[data-testid="stream-note"]').text()
  expect(note).toContain('1 chunk was delivered earlier and stays listed')
  expect(note).toContain('sent nothing after the denial')
  expect(wrapper.text()).not.toContain('IAM_DENIED')
})
