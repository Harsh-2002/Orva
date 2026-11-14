import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import TraceWaterfall from '../../src/components/traces/TraceWaterfall.vue'

const trace = {
  total_duration_ms: 120,
  external_parent_span_id: 'upstream-parent',
  spans: [
    {
      span_id: 'root', execution_id: 'ex-root', function_name: 'gateway',
      function_id: 'fn-root', parent_span_id: 'upstream-parent', trigger: 'http',
      status: 'success', status_code: 200, cold_start: false,
      offset_ms: 0, duration_ms: 120, baseline_p95_ms: 80,
    },
    {
      span_id: 'child', execution_id: 'ex-child', function_name: 'worker',
      function_id: 'fn-child', parent_span_id: 'root', trigger: 'f2f',
      status: 'error', status_code: 500, cold_start: true,
      offset_ms: 10, duration_ms: 60, error_message: 'worker failed',
    },
  ],
  user_spans: [{
    id: 'user-1', execution_id: 'ex-child', parent_span_id: 'child', name: 'database',
    status: 'ok', offset_ms: 20, duration_ms: 20, attributes: '{"table":"items"}',
  }],
  log_entries: [
    { id: 1, execution_id: 'ex-root', span_id: 'root', ts: '2026-08-11T12:00:00Z', level: 'info', message: 'start', fields: '{"request":1}' },
    { id: 2, execution_id: 'ex-child', span_id: 'child', ts: '2026-08-11T12:00:00.010Z', level: 'error', message: 'failed' },
  ],
}

describe('TraceWaterfall', () => {
  it('renders real focusable span buttons and a stacked mobile-first timeline', () => {
    const wrapper = mount(TraceWaterfall, { props: { trace } })
    const rows = wrapper.findAll('[data-span-key]')
    expect(rows).toHaveLength(3)
    expect(rows.every((row) => row.element.tagName === 'BUTTON')).toBe(true)
    expect(rows[0].attributes('aria-expanded')).toBe('false')
    expect(rows[0].find('.grid-cols-1').exists()).toBe(true)
    expect(wrapper.text()).toContain('External parent')
    expect(wrapper.text()).toContain('Invoked by gateway')
  })

  it('selects in place, exposes diagnostics, filters linked logs, and emits the secondary navigation action', async () => {
    const wrapper = mount(TraceWaterfall, { props: { trace } })
    const child = wrapper.find('[data-span-key="system:child"]')
    await child.trigger('click')
    expect(child.attributes('aria-expanded')).toBe('true')
    expect(wrapper.text()).toContain('HTTP 500')
    expect(wrapper.text()).toContain('Cold start')
    expect(wrapper.text()).toContain('worker failed')

    const selectedLogs = wrapper.findAll('button').find((button) => button.text() === 'Execution logs')
    await selectedLogs.trigger('click')
    expect(wrapper.text()).toContain('failed')
    expect(wrapper.text()).not.toContain('startFields')

    const open = wrapper.findAll('button').find((button) => button.text().includes('Open invocation'))
    await open.trigger('click')
    expect(wrapper.emitted('open-invocation')).toEqual([['ex-child']])
  })

  it('defaults code selection to honestly labelled execution logs and preserves valid selection on refresh', async () => {
    const wrapper = mount(TraceWaterfall, { props: { trace: { ...trace, trace_id: 'same' } } })
    await wrapper.find('[data-span-key="user:user-1"]').trigger('click')
    expect(wrapper.text()).toContain('Logs belong to the containing invocation; code-span correlation is not recorded.')
    expect(wrapper.find('[aria-label="Structured log entries"]').text()).toContain('failed')
    expect(wrapper.find('[aria-label="Structured log entries"]').text()).not.toContain('start')
    await wrapper.setProps({ trace: { ...trace, trace_id: 'same', log_entries: [...trace.log_entries, { id: 3, execution_id: 'ex-child', message: 'late log' }] } })
    expect(wrapper.find('[data-span-key="user:user-1"]').attributes('aria-expanded')).toBe('true')
    expect(wrapper.text()).toContain('late log')
    await wrapper.setProps({ trace: { trace_id: 'same', spans: [], log_entries: [] } })
    expect(wrapper.findAll('button').find((button) => button.text() === 'Execution logs').attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('shows cold/status text before expansion and offers bounded keyboard-button focus and fit', async () => {
    const delayed = { ...trace, total_duration_ms: 3600001, spans: [trace.spans[0], { ...trace.spans[1], offset_ms: 3600000, duration_ms: 1 }], user_spans: [] }
    const wrapper = mount(TraceWaterfall, { props: { trace: delayed } })
    expect(wrapper.find('[aria-label="Waterfall legend"]').text()).toContain('Execution')
    expect(wrapper.find('[aria-label="Waterfall legend"]').text()).not.toContain('Warm')
    const child = wrapper.find('[data-span-key="system:child"]')
    expect(child.text()).toContain('Cold')
    expect(child.text()).toContain('error')
    await child.trigger('click')
    const button = (text) => wrapper.findAll('button').find((item) => item.text() === text)
    await button('Focus selected span').trigger('click')
    expect(wrapper.find('[data-time-ruler]').text()).toContain('+3 ms')
    expect(wrapper.text()).toContain('Before window')
    expect(child.find('span.absolute').attributes('style')).toContain('33.333')
    await button('Fit trace').trigger('click')
    expect(wrapper.find('[data-time-ruler]').text()).toContain('1 h')
    expect(wrapper.text()).toContain('1 h 0.001 s from trace start')
    wrapper.unmount()
  })

  it('renders a zero-duration endpoint as a line, not a fictitious duration bar', () => {
    const wrapper = mount(TraceWaterfall, { props: { trace: { spans: [{ span_id: 'zero', offset_ms: 10, duration_ms: 0 }], total_duration_ms: 10 } } })
    const marker = wrapper.find('[data-span-key="system:zero"] span.absolute')
    expect(marker.attributes('style')).toContain('width: 0%')
    expect(marker.attributes('style')).toContain('border-left: 2px solid')
    expect(marker.attributes('style')).toContain('translateX(-100%)')
    wrapper.unmount()
  })
})
