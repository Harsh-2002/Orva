// Shared time formatting helpers. Multiple Settings cards (API keys,
// connected applications, active sessions) all want "Xm ago" /
// "in Xd" rendering — one source so they stay consistent and we
// don't grow N divergent copies.

/**
 * Render an ISO timestamp (or Date / millis) as a humanised relative
 * string: "just now", "5m ago", "2h ago", "3d ago", "in 28d", etc.
 *
 * Past values get "ago" suffix; future values get "in" prefix.
 * Granularity drops as distance grows so "5 minutes" and "5 months"
 * are equally legible at a glance.
 */
export function formatRelative(when) {
  if (when == null) return ''
  const ts = typeof when === 'number' ? when : new Date(when).getTime()
  if (Number.isNaN(ts)) return ''
  const ms = ts - Date.now()
  const abs = Math.abs(ms)
  const past = ms < 0
  const mins = Math.round(abs / 60000)
  if (mins < 1) return past ? 'just now' : 'in <1m'
  if (mins < 60) return past ? `${mins}m ago` : `in ${mins}m`
  const hrs = Math.round(mins / 60)
  if (hrs < 24) return past ? `${hrs}h ago` : `in ${hrs}h`
  const days = Math.round(hrs / 24)
  if (days < 90) return past ? `${days}d ago` : `in ${days}d`
  const months = Math.round(days / 30)
  return past ? `${months}mo ago` : `in ${months}mo`
}

/** True when the timestamp is in the past. */
export function isExpired(when) {
  if (when == null) return false
  const ts = typeof when === 'number' ? when : new Date(when).getTime()
  if (Number.isNaN(ts)) return false
  return ts < Date.now()
}
