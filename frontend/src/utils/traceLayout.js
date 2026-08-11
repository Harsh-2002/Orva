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
  const spans = trace?.spans || []
  const userSpans = trace?.user_spans || []
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
  if (!row) return logs || []
  return (logs || []).filter((entry) =>
    entry.span_id === row.span_id ||
    (row.type === 'system' && entry.execution_id === row.execution_id),
  )
}

export function shortID(value) {
  if (!value) return '—'
  return value.length > 12 ? `${value.slice(0, 12)}…` : value
}
