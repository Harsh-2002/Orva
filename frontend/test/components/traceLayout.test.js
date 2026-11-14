import { describe, expect, it } from 'vitest'
import { buildTraceRows, logsForRow, traceExtent, timeWindow, focusWindow, spanGeometry, formatTraceTime } from '../../src/utils/traceLayout.js'

const trace = {
  external_parent_span_id: 'upstream-abcdef123456',
  spans: [
    { span_id: 'root', execution_id: 'ex-root', function_name: 'gateway', parent_span_id: 'upstream-abcdef123456', trigger: 'http', offset_ms: 0 },
    { span_id: 'job', execution_id: 'ex-job', function_name: 'worker', parent_span_id: 'root', trigger: 'job', offset_ms: 8 },
    { span_id: 'invoke', execution_id: 'ex-invoke', function_name: 'mailer', parent_span_id: 'job', trigger: 'f2f', offset_ms: 12 },
  ],
  user_spans: [
    { id: 'custom', execution_id: 'ex-root', parent_span_id: 'root', name: 'validate', offset_ms: 4 },
    { id: 'query', execution_id: 'ex-root', parent_span_id: 'root', name: 'lookup', offset_ms: 5 },
  ],
}

describe('trace causal layout', () => {
  it('orders and indents system and code spans by parent relationships', () => {
    const rows = buildTraceRows(trace)
    expect(rows.map((row) => row.key)).toEqual([
      'system:root', 'user:custom', 'user:query', 'system:job', 'system:invoke',
    ])
    expect(rows.map((row) => row.depth)).toEqual([0, 1, 1, 1, 2])
    expect(rows[0].relationship).toContain('External parent')
    expect(rows[2].relationship).toBe('Code span in gateway')
    expect(rows[3].relationship).toBe('Job from gateway')
    expect(rows[4].relationship).toBe('Invoked by worker')
  })

  it('associates logs with the selected system span without leaking siblings', () => {
    const logs = [
      { id: 1, execution_id: 'ex-root', span_id: 'root' },
      { id: 2, execution_id: 'ex-job', span_id: 'job' },
    ]
    expect(logsForRow(logs, buildTraceRows(trace)[3])).toEqual([logs[1]])
  })

  it('scopes code selection to the containing execution, not a fabricated code/log correlation', () => {
    const logs = [{ execution_id: 'ex-root', span_id: 'root' }, { execution_id: 'ex-job', span_id: 'custom' }]
    expect(logsForRow(logs, buildTraceRows(trace)[1])).toEqual([logs[0]])
    expect(logsForRow([{}], { type: 'user' })).toEqual([])
    expect(logsForRow({}, null)).toEqual([])
  })

  it('keeps cyclic legacy rows inspectable and sanitizes absent/malformed timing', () => {
    expect(buildTraceRows({ spans: {} })).toEqual([])
    const rows = buildTraceRows({ spans: [null, { span_id: 'a', parent_span_id: 'b', offset_ms: -1 },
      { span_id: 'b', parent_span_id: 'a', offset_ms: Infinity, duration_ms: NaN }] })
    expect(rows).toHaveLength(2)
    expect(rows.every((row) => row.offset_ms === 0 && row.duration_ms === 0)).toBe(true)
    expect(traceExtent({ total_duration_ms: NaN }, rows)).toBe(1)
  })
})

describe('bounded trace viewport', () => {
  it('focuses a 1ms job an hour later without an unbounded pixel canvas', () => {
    const row = { offset_ms: 3600000, duration_ms: 1 }
    const extent = traceExtent({ total_duration_ms: 1 }, [row])
    const window = focusWindow(row, extent)
    expect(window.end).toBe(3600001)
    expect(window.end - window.start).toBe(3)
    expect(spanGeometry(row, window).width).toBeCloseTo(100 / 3)
    expect(formatTraceTime(3600001)).toBe('1 h 0.001 s')
  })

  it('clips both ends and caps a minimum visual width at the remaining rail', () => {
    expect(spanGeometry({ offset_ms: 0, duration_ms: 100 }, { start: 10, end: 20 })).toMatchObject({ left: 0, width: 100, clippedStart: true, clippedEnd: true })
    const edge = spanGeometry({ offset_ms: 99.9, duration_ms: 0.01 }, { start: 0, end: 100 })
    expect(edge.left + edge.width).toBeLessThanOrEqual(100)
    expect(spanGeometry({ offset_ms: 100, duration_ms: 1 }, { start: 0, end: 100 }).outside).toBe('After window')
    expect(spanGeometry({ offset_ms: 0, duration_ms: 10 }, { start: 10, end: 20 }).outside).toBe('Before window')
  })

  it('represents a true zero as a marker, including the exact end boundary', () => {
    expect(spanGeometry({ offset_ms: 100, duration_ms: 0 }, { start: 0, end: 100 })).toMatchObject({ left: 100, width: 0, zero: true })
    expect(spanGeometry({ offset_ms: -4, duration_ms: Infinity }, { start: 0, end: 1 })).toMatchObject({ left: 0, width: 0, zero: true })
    expect(spanGeometry({}, { start: 0, end: NaN }).outside).toBe('Invalid window')
    expect(timeWindow(-10, 20, 100)).toEqual({ start: 0, end: 30 })
    expect(timeWindow(NaN, Infinity, NaN)).toEqual({ start: 0, end: 1 })
    expect(timeWindow(100, 90, 100)).toEqual({ start: 99, end: 100 })
  })
})
