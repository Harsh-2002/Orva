import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import TraceDetail from '../../src/views/TraceDetail.vue'
import Traces from '../../src/views/Traces.vue'

const api = vi.hoisted(() => ({ getTrace: vi.fn(), listTraces: vi.fn() }))
vi.mock('@/api/endpoints', () => api)
const pending = () => {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve: (data) => resolve({ data }), reject }
}
const trace = (id, late = false) => ({ trace_id: id, status: 'success', total_duration_ms: 1, spans: [
  { span_id: 'root', execution_id: 'exec', function_name: 'root', duration_ms: 1, status: 'success' },
  ...(late ? [{ span_id: 'late', parent_span_id: 'root', execution_id: 'late-exec', function_name: 'late job', offset_ms: 3600000, duration_ms: 1 }] : []),
], log_entries: [] })
const result = (id) => ({ traces: [{ trace_id: id, function_name: id, status: 'success', span_count: 1 }], next_cursor: id })
const mounted = []
const boot = async (component, path) => {
  const router = createRouter({ history: createMemoryHistory(), routes: [
    { path: '/traces/:id', component: TraceDetail, props: true },
    { path: '/traces', component: Traces },
    { path: '/away', component: { template: '<p>Away</p>' } },
  ] })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(component || { template: '<router-view v-slot="{ Component }"><keep-alive><component :is="Component" /></keep-alive></router-view>' }, { global: { plugins: [router] } })
  mounted.push(wrapper)
  await flushPromises()
  return { wrapper, router }
}
const click = async (wrapper, text) => {
  await wrapper.findAll('button').find((button) => button.text() === text).trigger('click')
  await flushPromises()
}
beforeEach(() => { vi.useFakeTimers(); vi.resetAllMocks() })
afterEach(() => { mounted.splice(0).forEach((wrapper) => wrapper.unmount()); vi.useRealTimers() })

describe('trace detail request ownership', () => {
  it.each(['resolve', 'reject'])('ignores obsolete A %s, including its finally while B loads', async (outcome) => {
    const a = pending(), b = pending()
    api.getTrace.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise)
    const { wrapper, router } = await boot(null, '/traces/A')
    await router.push('/traces/B'); await flushPromises()
    if (outcome === 'resolve') a.resolve(trace('A')); else a.reject(new Error('old A failure'))
    await flushPromises()
    expect(wrapper.text()).toContain('Loading trace')
    expect(wrapper.text()).not.toContain('old A failure')
    b.resolve(trace('B')); await flushPromises()
    expect(wrapper.text()).toContain('B')
    expect(wrapper.text()).not.toContain('Loading trace')
  })

  it.each(['resolve', 'reject'])('does not let delayed A %s replace successful B', async (outcome) => {
    const a = pending()
    api.getTrace.mockReturnValueOnce(a.promise).mockResolvedValueOnce({ data: trace('B') })
    const { wrapper, router } = await boot(null, '/traces/A')
    await router.push('/traces/B'); await flushPromises()
    if (outcome === 'resolve') a.resolve(trace('obsolete trace')); else a.reject(new Error('obsolete failure'))
    await flushPromises()
    expect(wrapper.text()).toContain('B')
    expect(wrapper.text()).not.toContain('obsolete failure')
    expect(wrapper.text()).not.toContain('obsolete trace')
  })

  it('refreshes late children without overlap, preserves selection, and labels stale data after failure', async () => {
    const refresh = pending()
    api.getTrace.mockResolvedValueOnce({ data: trace('A') }).mockReturnValueOnce(refresh.promise)
      .mockRejectedValueOnce(new Error('refresh unavailable'))
    const { wrapper } = await boot(null, '/traces/A')
    await wrapper.find('[data-span-key="system:root"]').trigger('click')
    await vi.advanceTimersByTimeAsync(5000)
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.getTrace).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('Updating trace')
    refresh.resolve(trace('A', true)); await flushPromises()
    expect(wrapper.text()).toContain('late job')
    expect(wrapper.find('[data-span-key="system:root"]').attributes('aria-expanded')).toBe('true')
    await click(wrapper, 'Refresh')
    expect(wrapper.text()).toContain('Showing previously loaded data')
    expect(wrapper.text()).toContain('refresh unavailable')
    expect(wrapper.text()).toContain('late job')
  })

  it('ignores stopped requests and resumes cached views to discover late children', async () => {
    const old = pending()
    api.getTrace.mockReturnValueOnce(old.promise).mockResolvedValueOnce({ data: trace('A', true) })
    const { wrapper, router } = await boot(null, '/traces/A')
    await router.push('/away'); await flushPromises()
    old.reject(new Error('hidden error')); await flushPromises()
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.getTrace).toHaveBeenCalledTimes(1)
    await router.push('/traces/A'); await flushPromises()
    expect(wrapper.text()).toContain('late job')
    expect(wrapper.text()).not.toContain('hidden error')
    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.getTrace).toHaveBeenCalledTimes(2)
  })

  it('ignores an unmounted success and never schedules another poll', async () => {
    const request = pending()
    api.getTrace.mockReturnValue(request.promise)
    const { wrapper } = await boot(null, '/traces/A')
    const view = wrapper.findComponent(TraceDetail)
    wrapper.unmount()
    request.resolve(trace('A')); await flushPromises()
    expect(view.vm.trace).toBeNull()
    await vi.advanceTimersByTimeAsync(20000)
    expect(api.getTrace).toHaveBeenCalledTimes(1)
  })
})

describe('trace list request ownership', () => {
  it.each(['resolve', 'reject'])('ignores obsolete filter response %s and its finally', async (outcome) => {
    const a = pending(), b = pending()
    api.listTraces.mockReturnValueOnce(a.promise).mockReturnValueOnce(b.promise)
    const { wrapper } = await boot(null, '/traces')
    await click(wrapper, 'Errors')
    if (outcome === 'resolve') a.resolve(result('obsolete')); else a.reject(new Error('obsolete error'))
    await flushPromises()
    expect(wrapper.text()).toContain('Loading traces')
    expect(wrapper.text()).not.toContain('obsolete')
    b.resolve(result('current')); await flushPromises()
    expect(wrapper.text()).toContain('current')
    expect(wrapper.text()).not.toContain('Loading traces')
  })

  it.each(['resolve', 'reject'])('ignores old page %s after a filter refresh', async (outcome) => {
    const page = pending()
    api.listTraces.mockResolvedValueOnce({ data: result('first') }).mockReturnValueOnce(page.promise)
      .mockResolvedValueOnce({ data: result('filtered') })
    const { wrapper } = await boot(null, '/traces')
    await click(wrapper, 'Load more')
    await click(wrapper, 'Errors')
    if (outcome === 'resolve') page.resolve(result('old page')); else page.reject(new Error('old page failed'))
    await flushPromises()
    expect(wrapper.text()).toContain('filtered')
    expect(wrapper.text()).not.toContain('old page failed')
    expect(wrapper.text()).not.toContain('first')
    expect(wrapper.text()).not.toContain('old page')
  })

  it('clears failed fresh results and suppresses late errors while cached or unmounted', async () => {
    const late = pending()
    api.listTraces.mockResolvedValueOnce({ data: result('initial') }).mockRejectedValueOnce(new Error('latest failed'))
      .mockReturnValueOnce(late.promise).mockResolvedValueOnce({ data: result('reactivated') })
    const { wrapper, router } = await boot(null, '/traces')
    await click(wrapper, 'Refresh')
    expect(wrapper.text()).toContain('latest failed')
    expect(wrapper.text()).not.toContain('initial')
    await click(wrapper, 'Refresh')
    await router.push('/away'); await flushPromises()
    late.reject(new Error('hidden failure')); await flushPromises()
    await router.push('/traces'); await flushPromises()
    expect(wrapper.text()).toContain('reactivated')
    expect(wrapper.text()).not.toContain('hidden failure')
    wrapper.unmount()
    expect(api.listTraces).toHaveBeenCalledTimes(4)
  })

  it('ignores an unmounted list success, error, and completion', async () => {
    const success = pending(), failure = pending()
    api.listTraces.mockReturnValueOnce(success.promise).mockReturnValueOnce(failure.promise)
    const { wrapper } = await boot(null, '/traces')
    const view = wrapper.findComponent(Traces)
    await click(wrapper, 'Errors')
    wrapper.unmount()
    success.resolve(result('unmounted result')); failure.reject(new Error('unmounted failure'))
    await flushPromises()
    expect(view.vm.traces).toEqual([])
    expect(view.vm.error).toBe('')
    expect(view.vm.loading).toBe(false)
  })
})
