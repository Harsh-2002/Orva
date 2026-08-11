import { describe, expect, it } from 'vitest'
import { buildTraceRows, logsForRow } from '../../src/utils/traceLayout.js'

const trace = {
  external_parent_span_id: 'upstream-abcdef123456',
  spans: [
    { span_id: 'root', execution_id: 'ex-root', function_name: 'gateway', parent_span_id: 'upstream-abcdef123456', trigger: 'http', offset_ms: 0 },
    { span_id: 'job', execution_id: 'ex-job', function_name: 'worker', parent_span_id: 'root', trigger: 'job', offset_ms: 8 },
    { span_id: 'invoke', execution_id: 'ex-invoke', function_name: 'mailer', parent_span_id: 'job', trigger: 'f2f', offset_ms: 12 },
  ],
  user_spans: [
    { id: 'custom', execution_id: 'ex-root', parent_span_id: 'root', name: 'validate', offset_ms: 4 },
    { id: 'query', execution_id: 'ex-root', parent_span_id: 'custom', name: 'lookup', offset_ms: 5 },
  ],
}

describe('trace causal layout', () => {
  it('orders and indents system and code spans by parent relationships', () => {
    const rows = buildTraceRows(trace)
    expect(rows.map((row) => row.key)).toEqual([
      'system:root', 'user:custom', 'user:query', 'system:job', 'system:invoke',
    ])
    expect(rows.map((row) => row.depth)).toEqual([0, 1, 2, 1, 2])
    expect(rows[0].relationship).toContain('External parent')
    expect(rows[2].relationship).toBe('Code span in validate')
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
})
