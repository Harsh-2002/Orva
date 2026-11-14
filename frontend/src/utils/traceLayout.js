const byStart = (a, b) => {
  const offset = (a.offset_ms || 0) - (b.offset_ms || 0)
  if (offset) return offset
  return String(a.span_id || a.id || '').localeCompare(String(b.span_id || b.id || ''))
}

export function relationshipLabel(span, parent, externalParentSpanID = '') {
  if (!parent) {
    if (externalParentSpanID) return `External parent ${shortID(externalParentSpanID)}`
    switch (span.trigger) {
      case 'cron': return 'Cron entry'
      case 'job': return 'Job entry'
      case 'replay': return 'Replay entry'
      case 'webhook':
      case 'inbound': return 'Webhook entry'
      default: return 'Trace entry'
    }
  }
  switch (span.trigger) {
    case 'f2f': return `Invoked by ${parent.label}`
    case 'job': return `Job from ${parent.label}`
    case 'cron': return `Cron from ${parent.label}`
    case 'replay': return `Replay of ${parent.label}`
    default: return `Child of ${parent.label}`
  }
}

export function buildTraceRows(trace) {
  const spans = Array.isArray(trace?.spans) ? trace.spans.filter(Boolean) : []
  const userSpans = Array.isArray(trace?.user_spans) ? trace.user_spans.filter(Boolean) : []
  const nodes = [
    ...spans.map((span) => ({ kind: 'system', id: span.span_id, value: span })),
    ...userSpans.map((span) => ({ kind: 'user', id: span.id, value: span })),
  ]
  const byID = new Map(nodes.map((node) => [node.id, node]))
  const children = new Map()
  for (const node of nodes) {
    const key = byID.has(node.value.parent_span_id) ? node.value.parent_span_id : ''
    if (!children.has(key)) children.set(key, [])
    children.get(key).push(node)
  }

  const rows = []
  const visited = new Set()
  const append = (node, depth, parent = null) => {
    const visitKey = `${node.kind}:${node.id}`
    if (visited.has(visitKey)) return
    visited.add(visitKey)
    const span = node.value
    const isSystem = node.kind === 'system'
    const label = isSystem ? (span.function_name || span.function_id) : span.name
    const row = isSystem
      ? {
          ...span,
          key: visitKey,
          type: 'system',
          label,
          depth,
          relationship: relationshipLabel(span, parent, parent ? '' : trace?.external_parent_span_id),
        }
      : {
          ...span,
          span_id: span.id,
          key: visitKey,
          type: 'user',
          label,
          depth,
          relationship: parent ? `Code span in ${parent.label}` : 'Code span',
        }
    row.offset_ms = nonnegative(row.offset_ms)
    row.duration_ms = nonnegative(row.duration_ms)
    rows.push(row)
    for (const child of (children.get(node.id) || []).sort((a, b) => byStart(a.value, b.value))) {
      append(child, depth + 1, row)
    }
  }

  for (const root of (children.get('') || []).sort((a, b) => byStart(a.value, b.value))) append(root, 0)
  // Corrupt/cyclic legacy rows should stay inspectable rather than vanish.
  for (const node of [...nodes].sort((a, b) => byStart(a.value, b.value))) append(node, 0)
  return rows
}

export function logsForRow(logs, row) {
  const entries = Array.isArray(logs) ? logs.filter(Boolean) : []
  if (!row) return entries
  return row.execution_id ? entries.filter((entry) => entry.execution_id === row.execution_id) : []
}

const nonnegative = (value) => Number.isFinite(value) ? Math.min(Number.MAX_SAFE_INTEGER, Math.max(0, value)) : 0

export function traceExtent(trace, rows = buildTraceRows(trace)) {
  return rows.reduce((extent, row) => Math.max(extent, nonnegative(row.offset_ms) + nonnegative(row.duration_ms)), Math.max(1, nonnegative(trace?.total_duration_ms)))
}

export function timeWindow(start, end, extent) {
  const total = Math.max(1, nonnegative(extent))
  const width = Math.min(total, Math.max(1, nonnegative(end - start)))
  const left = Math.min(Math.max(0, nonnegative(start)), total - width)
  return { start: left, end: left + width }
}

export function focusWindow(row, extent) {
  const duration = nonnegative(row?.duration_ms)
  const center = nonnegative(row?.offset_ms) + duration / 2
  const width = Math.max(1, duration * 3)
  return timeWindow(center - width / 2, center + width / 2, extent)
}

export function spanGeometry(row, window) {
  const start = nonnegative(row?.offset_ms)
  const duration = nonnegative(row?.duration_ms)
  const end = start + duration
  const width = window.end - window.start
  if (!Number.isFinite(width) || width <= 0) return { outside: 'Invalid window' }
  if (end < window.start || (duration > 0 && end === window.start)) return { outside: 'Before window' }
  if (start > window.end || (duration > 0 && start === window.end)) return { outside: 'After window' }
  const left = Math.max(0, (start - window.start) / width * 100)
  const visible = (Math.min(end, window.end) - Math.max(start, window.start)) / width * 100
  return { left, width: duration === 0 ? 0 : Math.min(100 - left, Math.max(0.75, visible)), zero: duration === 0,
    clippedStart: start < window.start, clippedEnd: end > window.end }
}

export function formatTraceTime(value) {
  const ms = nonnegative(value)
  if (ms < 1000) return `${Number(ms.toFixed(3))} ms`
  const parts = []
  let remaining = ms
  for (const [unit, size] of [['h', 3600000], ['min', 60000]]) {
    const count = Math.floor(remaining / size)
    if (count) parts.push(`${count} ${unit}`)
    remaining %= size
  }
  if (remaining) parts.push(`${Number((remaining / 1000).toFixed(6))} s`)
  return parts.join(' ')
}

export function shortID(value) {
  if (!value) return '—'
  return value.length > 12 ? `${value.slice(0, 12)}…` : value
}
