// describeSnapshotDiff compares two deployment snapshots and returns
// human-readable lines summarising what changes when moving from `a`
// to `b`. Caller order determines the direction of the "+ Add" /
// "- Remove" / "~ Change" prefixes — top-down reads as "if you go
// from a → b".
//
// Used by:
//   - Rollback confirm dialogs in Deployments.vue + Editor.vue
//     (a = current function record, b = target snapshot)
//   - FunctionDiff.vue metadata panel
//     (a = from-deployment snapshot, b = to-deployment snapshot)
//
// Format prioritises legibility over completeness: env vars by name;
// numeric/string fields as old → new; identical fields are omitted.
export function describeSnapshotDiff(a, b) {
  const lines = []

  // Env vars: classify into added / removed / changed.
  const cur = a.env_vars || {}
  const next = b.env_vars || {}
  const added = Object.keys(next).filter((k) => !(k in cur))
  const removed = Object.keys(cur).filter((k) => !(k in next))
  const changed = Object.keys(next).filter((k) => k in cur && cur[k] !== next[k])
  if (added.length)   lines.push(`+ Add env: ${added.join(', ')}`)
  if (removed.length) lines.push(`- Remove env: ${removed.join(', ')}`)
  if (changed.length) lines.push(`~ Change env: ${changed.join(', ')}`)

  // Spawn config — only mention what differs.
  const num = (label, x, y, suffix = '') => {
    if (x !== y) lines.push(`~ ${label}: ${x}${suffix} → ${y}${suffix}`)
  }
  num('Memory', a.memory_mb, b.memory_mb, ' MB')
  num('CPUs', a.cpus, b.cpus)
  num('Timeout', a.timeout_ms, b.timeout_ms, ' ms')
  num('Network', a.network_mode || 'none', b.network_mode || 'none')
  num('Auth gate', a.auth_mode || 'none', b.auth_mode || 'none')
  num('Rate limit', a.rate_limit_per_min || 0, b.rate_limit_per_min || 0, '/min')
  num('Max concurrency', a.max_concurrency || 0, b.max_concurrency || 0)
  num('Concurrency policy', a.concurrency_policy || 'queue', b.concurrency_policy || 'queue')

  return lines
}
