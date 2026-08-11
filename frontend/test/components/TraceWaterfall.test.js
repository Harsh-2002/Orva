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

    const selectedLogs = wrapper.findAll('button').find((button) => button.text() === 'Selected span')
    await selectedLogs.trigger('click')
    expect(wrapper.text()).toContain('failed')
    expect(wrapper.text()).not.toContain('startFields')

    const open = wrapper.findAll('button').find((button) => button.text().includes('Open invocation'))
    await open.trigger('click')
    expect(wrapper.emitted('open-invocation')).toEqual([['ex-child']])
  })
})

