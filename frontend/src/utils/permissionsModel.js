// Pure view-model seam for the function Permissions page.
//
// This is NOT a REST contract. It validates and copies a snapshot handed in by
// a future wrapper, and it mirrors backend/internal/workloadiam/validate.go so
// a draft is rejected locally for the reasons the server would reject it. The
// mirror is advisory: the server stays authoritative and this file never grants
// or approves anything. Nothing here touches Vue, the DOM or the network.

export const LIMITS = Object.freeze({
  schemaVersion: 1,
  maxPolicyBytes: 32 * 1024,
  maxStatements: 64,
  maxTargets: 256,
  maxSelectorRunes: 256,
  maxActionsPerStatement: 10,
  maxTriggers: 3,
  maxHistory: 32,
})

export const ACTION_IDS = Object.freeze([
  'kv.get', 'kv.list', 'kv.put', 'kv.delete', 'kv.incr', 'kv.cas',
  'functions.invoke', 'jobs.enqueue', 'crons.upsert', 'spans.emit',
])

export const ACTION_LABELS = Object.freeze({
  'kv.get': 'Read a key',
  'kv.list': 'List keys',
  'kv.put': 'Write',
  'kv.delete': 'Delete',
  'kv.incr': 'Increment',
  'kv.cas': 'Compare and set',
  'functions.invoke': 'Call',
  'jobs.enqueue': 'Queue a job',
  'crons.upsert': 'Create or update a schedule',
  'spans.emit': 'Emit spans',
})

export const TRIGGER_IDS = Object.freeze(['f2f', 'job', 'cron'])
export const TRIGGER_LABELS = Object.freeze({
  f2f: 'Function call',
  job: 'Background job',
  cron: 'Schedule',
})

export const CONTEXT_LABELS = Object.freeze({
  execution: 'While handling a request',
  bootstrap: 'At startup',
})

// Which kind of statement the add form builds.
export const KINDS = Object.freeze({
  'kv-key': { label: 'One storage key', actions: ['kv.get', 'kv.put', 'kv.delete', 'kv.incr', 'kv.cas'] },
  'kv-prefix': { label: 'Storage keys by prefix', actions: ['kv.get', 'kv.list', 'kv.put', 'kv.delete', 'kv.incr', 'kv.cas'] },
  function: { label: 'Another function', actions: ['functions.invoke', 'jobs.enqueue'] },
  'cron-name': { label: 'One schedule', actions: ['crons.upsert'] },
  'cron-prefix': { label: 'Schedules by prefix', actions: ['crons.upsert'] },
  spans: { label: 'Execution spans', actions: ['spans.emit'] },
})

// Presets only pick actions the operator can then see and change. Counters
// deliberately omits kv.get; the warning explains why it is also needed.
export const PRESETS = Object.freeze({
  read: { label: 'Read only', actions: ['kv.get', 'kv.list'] },
  readwrite: { label: 'Read and write', actions: ['kv.get', 'kv.list', 'kv.put', 'kv.delete'] },
  counters: { label: 'Counters', actions: ['kv.incr', 'kv.cas'] },
})

const ENFORCEMENTS = ['active', 'inactive', 'unknown']
const TARGET_STATES = ['resolved', 'missing', 'unknown']
const NIL_UUID = '00000000-0000-0000-0000-000000000000'
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
const SHA_RE = /^[0-9a-f]{64}$/
const LONE_SURROGATE = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/
const U64_MAX = 18446744073709551615n
const I64_MIN = -9223372036854775808n
const I64_MAX = 9223372036854775807n

const isObject = (v) => {
  if (v === null || typeof v !== 'object' || Array.isArray(v)) return false
  const proto = Object.getPrototypeOf(v)
  return proto === Object.prototype || proto === null
}
const own = (o, k) => Object.prototype.hasOwnProperty.call(o, k)

export const isCanonicalUuid = (s) => typeof s === 'string' && s !== NIL_UUID && UUID_RE.test(s)
export const isSha256 = (s) => typeof s === 'string' && SHA_RE.test(s)

// Go counts runes and requires valid UTF-8; JS strings with a lone surrogate
// are the only ones that cannot be encoded. NUL is allowed, as it is there.
export function isValidLiteral(s, allowEmpty) {
  if (typeof s !== 'string') return false
  if (s === '' && !allowEmpty) return false
  if (LONE_SURROGATE.test(s)) return false
  return Array.from(s).length <= LIMITS.maxSelectorRunes
}

// Revision and epoch travel as decimal strings. They are compared with BigInt
// and never pass through Number or parseInt, which round above 2^53.
export function parseDecimal(s, { signed = false } = {}) {
  if (typeof s !== 'string' || s.length === 0 || s.length > 20) return null
  if (!(signed ? /^-?(0|[1-9][0-9]*)$/ : /^(0|[1-9][0-9]*)$/).test(s) || s === '-0') return null
  const v = BigInt(s)
  if (signed ? v < I64_MIN || v > I64_MAX : v > U64_MAX) return null
  return v
}

// Policy revisions, authority epochs and audit rows are SQLite INTEGER columns
// checked `> 0`, so the only valid values are 1..2^63-1 as decimal strings.
export function parseCounter(s) {
  if (typeof s !== 'string' || s.length === 0 || s.length > 19 || !/^[1-9][0-9]*$/.test(s)) return null
  const v = BigInt(s)
  return v > I64_MAX ? null : v
}

// Array(n) has holes that map/filter skip and JSON.stringify writes as null.
const isDense = (a) => {
  if (!Array.isArray(a)) return false
  for (let i = 0; i < a.length; i += 1) if (!(i in a)) return false
  return true
}

// Mirrors the workload_iam_audit CHECK constraint and validateIAMActor.
export const ACTOR_KINDS = Object.freeze(['api_key', 'session', 'oauth', 'system'])

export const shortDigest = (d) => (isSha256(d) ? d.slice(0, 12) : '')

// ---------------------------------------------------------------- policy

export const emptyPolicy = () => ({ schema_version: 1, allow: [], inbound: [] })
export const clonePolicy = (p) => JSON.parse(JSON.stringify(p))

function readExactKeys(obj, allowed, path, problems) {
  if (!isObject(obj)) {
    problems.push(`${path} must be an object`)
    return false
  }
  let ok = true
  for (const k of Object.keys(obj)) {
    if (!allowed.includes(k)) {
      problems.push(`${path}.${k} is not a policy field`)
      ok = false
    }
  }
  return ok
}

function readSelector(obj, path, problems, label) {
  if (!readExactKeys(obj, label, path, problems)) return null
  const [a, b] = label
  const hasA = own(obj, a)
  const hasB = own(obj, b)
  if (hasA === hasB) {
    problems.push(`${path} needs exactly one of ${a} or ${b}`)
    return null
  }
  const mode = hasA ? a : b
  const value = obj[mode]
  if (!isValidLiteral(value, mode === 'prefix')) {
    problems.push(`${path}.${mode} must be ${mode === 'prefix' ? 'text' : 'non-empty text'} of at most ${LIMITS.maxSelectorRunes} characters`)
    return null
  }
  return { mode, value }
}

function readResource(r, path, problems) {
  if (!readExactKeys(r, ['own_kv', 'function', 'own_cron', 'own_execution'], path, problems)) return null
  const present = ['own_kv', 'function', 'own_cron', 'own_execution'].filter((k) => own(r, k))
  if (present.length !== 1) {
    problems.push(`${path} needs exactly one resource`)
    return null
  }
  const k = present[0]
  const v = r[k]
  if (k === 'own_kv') {
    const sel = readSelector(v, `${path}.own_kv`, problems, ['key', 'prefix'])
    return sel && { own_kv: { [sel.mode]: sel.value } }
  }
  if (k === 'function') {
    if (!readExactKeys(v, ['id'], `${path}.function`, problems)) return null
    if (!isCanonicalUuid(v.id)) {
      problems.push(`${path}.function.id must be a lowercase UUID`)
      return null
    }
    return { function: { id: v.id } }
  }
  if (k === 'own_cron') {
    if (!isObject(v)) {
      problems.push(`${path}.own_cron must be an object`)
      return null
    }
    const { target_id: targetId, ...selectorOnly } = v
    const sel = readSelector(selectorOnly, `${path}.own_cron`, problems, ['name', 'prefix'])
    if (!isCanonicalUuid(targetId)) {
      problems.push(`${path}.own_cron.target_id must be a lowercase UUID`)
      return null
    }
    return sel && { own_cron: { [sel.mode]: sel.value, target_id: targetId } }
  }
  if (!isObject(v) || Object.keys(v).length) {
    problems.push(`${path}.own_execution must be an empty object`)
    return null
  }
  return { own_execution: {} }
}

function actionFitsResource(action, r) {
  switch (action) {
    case 'kv.list': return !!r.own_kv && own(r.own_kv, 'prefix')
    case 'kv.get': case 'kv.put': case 'kv.delete': case 'kv.incr': case 'kv.cas': return !!r.own_kv
    case 'functions.invoke': case 'jobs.enqueue': return !!r.function
    case 'crons.upsert': return !!r.own_cron
    case 'spans.emit': return !!r.own_execution
    default: return false
  }
}

function readStatement(s, path, problems) {
  if (!readExactKeys(s, ['actions', 'resource', 'context'], path, problems)) return null
  let ok = true
  if (s.context !== 'execution' && s.context !== 'bootstrap') {
    problems.push(`${path}.context must be execution or bootstrap`)
    ok = false
  }
  const resource = readResource(s.resource, `${path}.resource`, problems)
  const actions = []
  if (!Array.isArray(s.actions) || s.actions.length === 0 || s.actions.length > LIMITS.maxActionsPerStatement || !isDense(s.actions)) {
    problems.push(`${path}.actions must list 1 to ${LIMITS.maxActionsPerStatement} actions`)
    return null
  }
  for (const a of s.actions) {
    if (!ACTION_IDS.includes(a) || actions.includes(a)) {
      problems.push(`${path}.actions has an unknown or repeated action`)
      ok = false
    } else if (resource && !actionFitsResource(a, resource)) {
      problems.push(`${path}: ${a} does not apply to this resource`)
      ok = false
    } else if (s.context === 'bootstrap' && a !== 'kv.get' && a !== 'kv.list') {
      problems.push(`${path}: startup access allows only kv.get and kv.list`)
      ok = false
    } else {
      actions.push(a)
    }
  }
  return ok && resource ? { actions, resource, context: s.context } : null
}

function readAcceptance(a, path, problems) {
  if (!readExactKeys(a, ['caller_id', 'triggers'], path, problems)) return null
  let ok = true
  if (!isCanonicalUuid(a.caller_id)) {
    problems.push(`${path}.caller_id must be a lowercase UUID`)
    ok = false
  }
  const triggers = []
  if (!Array.isArray(a.triggers) || a.triggers.length === 0 || a.triggers.length > LIMITS.maxTriggers || !isDense(a.triggers)) {
    problems.push(`${path}.triggers must list 1 to ${LIMITS.maxTriggers} triggers`)
    return null
  }
  for (const t of a.triggers) {
    if (!TRIGGER_IDS.includes(t) || triggers.includes(t)) {
      problems.push(`${path}.triggers has an unknown or repeated trigger`)
      ok = false
    } else {
      triggers.push(t)
    }
  }
  return ok ? { caller_id: a.caller_id, triggers } : null
}

// Approximates Go's json.Marshal bytes for a canonical policy: HTML-sensitive
// characters and U+2028/2029 are escaped to six bytes there.
export function policyBytes(policy) {
  const text = JSON.stringify(policy).replace(/[<>&\u2028\u2029]/g, (c) =>
    `\\u${c.charCodeAt(0).toString(16).padStart(4, '0')}`)
  return new TextEncoder().encode(text).length
}

// Returns { ok, problems, policy }. `policy` is a fresh canonical copy in Go's
// field order, or null when anything is wrong: an invalid stored policy is
// never normalised into authority.
export function validatePolicy(raw) {
  const problems = []
  if (!readExactKeys(raw, ['schema_version', 'allow', 'inbound'], 'policy', problems)) {
    return { ok: false, problems, policy: null }
  }
  if (raw.schema_version !== LIMITS.schemaVersion) problems.push('policy schema version is not supported')
  if (!Array.isArray(raw.allow) || !Array.isArray(raw.inbound)) {
    problems.push('allow and inbound must both be lists')
    return { ok: false, problems, policy: null }
  }
  if (raw.allow.length > LIMITS.maxStatements) problems.push(`at most ${LIMITS.maxStatements} statements`)
  if (raw.inbound.length > LIMITS.maxTargets) problems.push(`at most ${LIMITS.maxTargets} callers`)
  if (problems.length) return { ok: false, problems, policy: null }
  if (!isDense(raw.allow) || !isDense(raw.inbound)) {
    return { ok: false, problems: ['allow and inbound must not contain empty slots'], policy: null }
  }

  const allow = raw.allow.map((s, i) => readStatement(s, `allow[${i}]`, problems))
  const inbound = raw.inbound.map((a, i) => readAcceptance(a, `inbound[${i}]`, problems))
  if (problems.length) return { ok: false, problems, policy: null }

  const targets = inbound.length + allow.filter((s) => s.resource.function).length
  if (targets > LIMITS.maxTargets) problems.push(`callers plus called functions must stay within ${LIMITS.maxTargets}`)
  const policy = { schema_version: 1, allow, inbound }
  if (!problems.length && policyBytes(policy) > LIMITS.maxPolicyBytes) problems.push('policy exceeds 32 KiB')
  return problems.length ? { ok: false, problems, policy: null } : { ok: true, problems, policy }
}

// ----------------------------------------------------------- description

export function describeStatement(stmt) {
  const r = stmt.resource
  const out = {
    context: stmt.context,
    contextLabel: CONTEXT_LABELS[stmt.context] || stmt.context,
    actions: stmt.actions.map((id) => ({ id, label: ACTION_LABELS[id] || id })),
    kind: 'spans', title: '', selector: null, targetId: null, notes: [],
  }
  if (r.own_kv) {
    out.kind = 'kv'
    const prefix = own(r.own_kv, 'prefix')
    const value = prefix ? r.own_kv.prefix : r.own_kv.key
    out.selector = { mode: prefix ? 'prefix' : 'key', value }
    out.title = !prefix ? 'Own storage, one key' : value === '' ? 'Own storage, every key' : 'Own storage, keys with a prefix'
    if (prefix && value === '') out.notes.push('An empty prefix matches every key.')
  } else if (r.function) {
    out.kind = 'function'
    out.title = 'Another function'
    out.targetId = r.function.id
    out.notes.push('Takes effect only if that function also accepts this one as a caller.')
  } else if (r.own_cron) {
    out.kind = 'cron'
    const prefix = own(r.own_cron, 'prefix')
    out.selector = { mode: prefix ? 'prefix' : 'name', value: prefix ? r.own_cron.prefix : r.own_cron.name }
    out.title = prefix ? 'Own schedules, by prefix' : 'Own schedule'
    out.targetId = r.own_cron.target_id
    out.notes.push('Today only schedules that run this function itself are supported.')
  } else {
    out.title = 'Own execution spans'
    out.notes.push('Applies only to the execution that is running.')
  }
  return out
}

// One-line text for a statement in change lists. The selector literal and the
// action IDs are printed exactly. The selector mode word is dropped only when
// the title already says it ("one key", "keys with a prefix", "by prefix").
export function statementSummary(stmt) {
  const d = describeStatement(stmt)
  let where = ''
  if (d.selector) {
    const redundant = d.title.toLowerCase().includes(d.selector.mode)
    where = ` ${redundant ? '' : `${d.selector.mode} `}${d.selector.value === '' ? '(empty)' : d.selector.value}`
  } else if (d.targetId) {
    where = ` ${d.targetId}`
  }
  return `${d.title}${where}: ${d.actions.map((a) => a.id).join(', ')}${d.context === 'bootstrap' ? ' (at startup)' : ''}`
}

// Statements that cannot work as written. These explain; they never edit.
export function policyWarnings(policy) {
  const warnings = []
  const reads = policy.allow.filter((s) => s.context === 'execution' && s.actions.includes('kv.get') && s.resource.own_kv)
  const covered = (kv) => reads.some((s) => {
    const c = s.resource.own_kv
    if (own(kv, 'key')) return own(c, 'key') ? c.key === kv.key : kv.key.startsWith(c.prefix)
    return own(c, 'prefix') && kv.prefix.startsWith(c.prefix)
  })
  policy.allow.forEach((s, i) => {
    if (s.context !== 'execution' || !s.resource.own_kv) return
    const needs = s.actions.filter((a) => a === 'kv.incr' || a === 'kv.cas')
    if (needs.length && !covered(s.resource.own_kv)) {
      warnings.push({ index: i, code: 'needs-kv-get', text: `${needs.join(' and ')} also needs kv.get on the same keys. This statement is incomplete without it.` })
    }
  })
  return warnings
}

// ------------------------------------------------------------ draft edits

export function actionsFor(kind, context) {
  const all = (KINDS[kind] || { actions: [] }).actions
  return context === 'bootstrap' ? all.filter((a) => a === 'kv.get' || a === 'kv.list') : all
}

export function presetActions(preset, kind, context) {
  const allowed = actionsFor(kind, context)
  return ((PRESETS[preset] || { actions: [] }).actions).filter((a) => allowed.includes(a))
}

// Builds one statement from form fields and returns field-keyed errors.
export function buildStatement({ kind, value = '', targetId = '', context = 'execution', actions = [] }) {
  const errors = {}
  if (!KINDS[kind]) return { ok: false, errors: { kind: 'Choose what this grants access to.' }, statement: null }
  // Unknown or repeated actions are rejected, never filtered away: the draft
  // must contain exactly what the operator picked or nothing.
  const wellFormed = isDense(actions) && actions.every((a, i) => ACTION_IDS.includes(a) && actions.indexOf(a) === i)
  const picked = wellFormed ? ACTION_IDS.filter((a) => actions.includes(a)) : []
  if (!wellFormed) errors.actions = 'These actions are not recognised or are repeated.'
  else if (!picked.length) errors.actions = 'Pick at least one action.'
  if (context === 'bootstrap' && !kind.startsWith('kv-')) errors.context = 'Startup access applies to storage only.'
  if (wellFormed && context === 'bootstrap' && picked.some((a) => a !== 'kv.get' && a !== 'kv.list')) errors.actions = 'Startup access allows only reading and listing.'
  let resource
  if (kind === 'kv-key' || kind === 'kv-prefix') {
    const prefix = kind === 'kv-prefix'
    if (!isValidLiteral(value, prefix)) errors.value = `Enter ${prefix ? '' : 'a '}text of at most ${LIMITS.maxSelectorRunes} characters.`
    resource = { own_kv: { [prefix ? 'prefix' : 'key']: value } }
  } else if (kind === 'function') {
    if (!isCanonicalUuid(targetId)) errors.targetId = 'Enter the function UUID in lowercase.'
    resource = { function: { id: targetId } }
  } else if (kind === 'cron-name' || kind === 'cron-prefix') {
    const prefix = kind === 'cron-prefix'
    if (!isValidLiteral(value, prefix)) errors.value = `Enter ${prefix ? '' : 'a '}text of at most ${LIMITS.maxSelectorRunes} characters.`
    if (!isCanonicalUuid(targetId)) errors.targetId = 'Enter the target function UUID in lowercase.'
    resource = { own_cron: { [prefix ? 'prefix' : 'name']: value, target_id: targetId } }
  } else {
    resource = { own_execution: {} }
  }
  if (Object.keys(errors).length) return { ok: false, errors, statement: null }
  const checked = readStatement({ actions: picked, resource, context }, 'statement', [])
  return checked
    ? { ok: true, errors, statement: checked }
    : { ok: false, errors: { actions: 'These actions do not apply to this resource.' }, statement: null }
}

const finish = (policy) => {
  const v = validatePolicy(policy)
  return v.ok ? { ok: true, policy: v.policy, problems: [] } : { ok: false, policy: null, problems: v.problems }
}

export const addStatement = (policy, statement) =>
  finish({ ...clonePolicy(policy), allow: [...clonePolicy(policy).allow, statement] })

export const removeStatement = (policy, index) => {
  const p = clonePolicy(policy)
  p.allow = p.allow.filter((_, i) => i !== index)
  return finish(p).policy || p
}

export function addInbound(policy, callerId, triggers) {
  const p = clonePolicy(policy)
  if (!isDense(triggers) || triggers.some((t, i) => !TRIGGER_IDS.includes(t) || triggers.indexOf(t) !== i)) {
    return { ok: false, policy: null, problems: ['These triggers are not recognised or are repeated.'] }
  }
  if (p.inbound.some((a) => a.caller_id === callerId)) {
    return { ok: false, policy: null, problems: ['That caller is already accepted. Remove it first to change its triggers.'] }
  }
  p.inbound.push({ caller_id: callerId, triggers: TRIGGER_IDS.filter((t) => triggers.includes(t)) })
  return finish(p)
}

export const removeInbound = (policy, index) => {
  const p = clonePolicy(policy)
  p.inbound = p.inbound.filter((_, i) => i !== index)
  return finish(p).policy || p
}

// Order-insensitive comparison: reordering is not a permission change.
const statementKey = (s) => JSON.stringify({ a: [...s.actions].sort(), r: s.resource, c: s.context })
const inboundKey = (a) => JSON.stringify({ c: a.caller_id, t: [...a.triggers].sort() })

function multisetDiff(before, after, key) {
  const unmatched = (from, against) => {
    const pool = new Map()
    against.forEach((v) => pool.set(key(v), (pool.get(key(v)) || 0) + 1))
    return from.filter((v) => {
      const k = key(v)
      if (!pool.get(k)) return true
      pool.set(k, pool.get(k) - 1)
      return false
    })
  }
  return { added: unmatched(after, before), removed: unmatched(before, after) }
}

export function diffPolicies(base, draft) {
  const statements = multisetDiff(base.allow, draft.allow, statementKey)
  const inbound = multisetDiff(base.inbound, draft.inbound, inboundKey)
  const total = statements.added.length + statements.removed.length + inbound.added.length + inbound.removed.length
  return { statements, inbound, total }
}

// ------------------------------------------------------------- snapshots

const bool = (v) => v === true

function readCapabilities(raw) {
  const c = isObject(raw) ? raw : {}
  return {
    canRead: bool(c.canRead),
    canManage: bool(c.canManage),
    canReview: bool(c.canReview),
    canEditPrivilegedCode: bool(c.canEditPrivilegedCode),
  }
}

function unavailable(problem) {
  return { status: 'unavailable', problems: [problem], snapshot: null }
}

// Validates and copies a snapshot. status 'unavailable' means nothing may be
// shown or edited. A snapshot without canRead carries no policy, targets or
// history at all, so they cannot be rendered by mistake.
export function normalizeSnapshot(raw) {
  if (!isObject(raw)) return unavailable('No permissions snapshot.')
  const fn = raw.function
  if (!isObject(fn) || !isCanonicalUuid(fn.id) || typeof fn.name !== 'string') {
    return unavailable('The snapshot does not identify a function.')
  }
  const h = raw.host
  if (!isObject(h)) return unavailable('The snapshot has no host state.')
  const revision = parseCounter(h.revision)
  const epoch = parseCounter(h.authorityEpoch)
  const artifactOk = (a) => a === null || isSha256(a)
  if (revision === null || epoch === null) return unavailable('Revision or authority epoch is not a valid decimal string.')
  if (!artifactOk(h.currentArtifact) || !artifactOk(h.approvedArtifact)) return unavailable('An artifact digest is not valid.')
  if (typeof h.reviewRequired !== 'boolean' || typeof h.quarantined !== 'boolean') return unavailable('Review state is missing.')
  if (!ENFORCEMENTS.includes(h.enforcement)) return unavailable('Enforcement state is not recognised.')

  const snapshot = {
    function: { id: fn.id, name: fn.name },
    host: {
      revision: h.revision,
      authorityEpoch: h.authorityEpoch,
      currentArtifact: h.currentArtifact,
      approvedArtifact: h.approvedArtifact,
      reviewRequired: h.reviewRequired,
      quarantined: h.quarantined,
      enforcement: h.enforcement,
      capabilities: readCapabilities(h.capabilities),
    },
    policy: null,
    policyProblems: [],
    targets: [],
    history: [],
    readable: false,
  }
  if (!snapshot.host.capabilities.canRead) return { status: 'ready', problems: [], snapshot }
  snapshot.readable = true

  if (!isObject(raw.policy) || raw.policy.schema_version !== LIMITS.schemaVersion) {
    return unavailable('The policy schema version is not supported.')
  }
  const checked = validatePolicy(raw.policy)
  snapshot.policy = checked.policy
  snapshot.policyProblems = checked.problems

  const seen = new Set()
  for (const t of Array.isArray(raw.targets) ? raw.targets.slice(0, LIMITS.maxTargets) : []) {
    if (!isObject(t) || !isCanonicalUuid(t.id) || seen.has(t.id) || !TARGET_STATES.includes(t.state)) continue
    if (t.label !== null && typeof t.label !== 'string') continue
    seen.add(t.id)
    snapshot.targets.push({ id: t.id, label: t.label, state: t.state })
  }
  for (const e of Array.isArray(raw.history) ? raw.history.slice(0, LIMITS.maxHistory) : []) {
    if (!isObject(e) || parseCounter(e.revision) === null || parseCounter(e.authorityEpoch) === null) continue
    // The kind set is closed and the ID a canonical UUID, so nothing
    // credential-shaped can reach the page through the history list.
    if (!isSha256(e.digest) || !isObject(e.actor) || !ACTOR_KINDS.includes(e.actor.kind) || !isCanonicalUuid(e.actor.id)) continue
    if (typeof e.changedAt !== 'string' || Number.isNaN(Date.parse(e.changedAt))) continue
    snapshot.history.push({
      revision: e.revision, authorityEpoch: e.authorityEpoch, digest: e.digest,
      actor: { kind: e.actor.kind, id: e.actor.id }, changedAt: e.changedAt,
    })
  }
  return { status: 'ready', problems: [], snapshot }
}

// What the draft was based on. A change in any part while the draft is dirty
// is a conflict, never a silent overwrite.
export const hostFingerprint = (s) =>
  s ? `${s.host.revision}|${s.host.currentArtifact ?? ''}|${s.host.approvedArtifact ?? ''}` : ''

export function targetInfo(snapshot, id) {
  const t = snapshot && snapshot.targets.find((x) => x.id === id)
  if (t && t.state === 'resolved' && t.label) return { id, label: t.label, resolved: true }
  return { id, label: null, resolved: false }
}

// Capabilities come only from the server snapshot. `preview` is deliberately
// not an input: a fixture cannot widen what the capability flags deny.
export function deriveAccess({ snapshot, functionId, loading = false, loadError = null, saving = false, readOnlyReason = '' }) {
  const identityOk = !!snapshot && snapshot.function.id === functionId && functionId !== ''
  const caps = identityOk ? snapshot.host.capabilities : readCapabilities(null)
  const canRead = identityOk && caps.canRead
  const policyOk = canRead && snapshot.policy !== null
  const enforcing = identityOk && snapshot.host.enforcement === 'active'
  const blocked = loading || !!loadError || saving || !!readOnlyReason
  return {
    identityOk,
    canRead,
    canManage: caps.canManage,
    canReview: caps.canReview,
    enforcement: identityOk ? snapshot.host.enforcement : 'unknown',
    canEditPolicy: policyOk && caps.canManage && enforcing && !blocked,
  }
}

const expectedOf = (snapshot) => ({
  revision: snapshot.host.revision,
  currentArtifact: snapshot.host.currentArtifact,
  approvedArtifact: snapshot.host.approvedArtifact,
})

// Save/review payloads carry the policy document and the host state it was
// based on, and nothing else: no actor, no capability, no approval claim.
export function buildSavePayload(snapshot, draft) {
  const v = validatePolicy(draft)
  if (!v.ok) return { ok: false, problems: v.problems, payload: null }
  return { ok: true, problems: [], payload: { policy: v.policy, expected: expectedOf(snapshot) } }
}

export function buildReviewPayload(snapshot) {
  if (!snapshot || !snapshot.policy || !snapshot.host.currentArtifact) {
    return { ok: false, problems: ['No stored policy or current artifact to review.'], payload: null }
  }
  return { ok: true, problems: [], payload: { policy: clonePolicy(snapshot.policy), expected: expectedOf(snapshot) } }
}

// ------------------------------------------------------- guided builder
// Helpers for the step-by-step builder. They only describe and assemble; every
// statement still passes through buildStatement/validatePolicy before it joins
// a draft, so the guided path can never produce what the parser would refuse.

export const SERVICE_ORDER = Object.freeze(['storage', 'functions', 'schedules', 'tracing'])
export const SERVICES = Object.freeze({
  storage: { label: 'Storage', purpose: "Read and write this function's own stored keys." },
  functions: { label: 'Functions', purpose: 'Call other functions or queue background jobs for them.' },
  schedules: { label: 'Schedules', purpose: 'Create and update schedules that run this function.' },
  tracing: { label: 'Tracing', purpose: "Add spans to this function's own running execution." },
})

export const serviceOf = (stmt) => {
  const r = stmt.resource
  if (r.own_kv) return 'storage'
  if (r.function) return 'functions'
  if (r.own_cron) return 'schedules'
  return 'tracing'
}

// What each action lets the function do, in plain words, grouped for step 2.
export const ACTION_HELP = Object.freeze({
  'kv.get': 'Look up the value stored under a key.',
  'kv.list': 'See which keys exist. Needs a prefix or every key, not one exact key.',
  'kv.put': 'Create a key or overwrite its value.',
  'kv.delete': 'Remove keys.',
  'kv.incr': 'Add to a number stored under a key. Also needs Read a key.',
  'kv.cas': 'Change a value only if it still holds the one you expect. Also needs Read a key.',
  'functions.invoke': 'Run the function and wait for its reply.',
  'jobs.enqueue': 'Queue a background job that runs the function later.',
  'crons.upsert': 'Add a schedule or change an existing one.',
  'spans.emit': 'Record timing spans on the execution that is running.',
})

export const SERVICE_ACTIONS = Object.freeze({
  storage: [
    { group: 'Read', ids: ['kv.get', 'kv.list'] },
    { group: 'Write', ids: ['kv.put', 'kv.delete', 'kv.incr', 'kv.cas'] },
  ],
  functions: [{ group: '', ids: ['functions.invoke', 'jobs.enqueue'] }],
  schedules: [{ group: '', ids: ['crons.upsert'] }],
  tracing: [{ group: '', ids: ['spans.emit'] }],
})

const VERBS = {
  'kv.get': 'read', 'kv.list': 'list', 'kv.put': 'write', 'kv.delete': 'delete', 'kv.incr': 'increment', 'kv.cas': 'compare and set',
  'functions.invoke': 'call', 'jobs.enqueue': 'queue jobs for', 'crons.upsert': 'create or update schedules', 'spans.emit': 'emit spans',
}
const joinWords = (xs) => (xs.length < 2 ? xs.join('') : `${xs.slice(0, -1).join(', ')} and ${xs[xs.length - 1]}`)
const quote = (v) => `“${v}”`

// One sentence a first-time reader can act on. Literals are quoted exactly.
export function humanStatement(stmt, targetLabel = null, selfId = null) {
  const r = stmt.resource
  const verbs = joinWords(stmt.actions.map((a) => VERBS[a] || a))
  const when = stmt.context === 'bootstrap' ? ' at startup' : ''
  if (r.own_kv) {
    const prefix = own(r.own_kv, 'prefix')
    const v = prefix ? r.own_kv.prefix : r.own_kv.key
    const what = !prefix ? `the key ${quote(v)}` : v === '' ? 'every key' : `keys starting with ${quote(v)}`
    return `Can ${verbs} ${what} in its own storage${when}.`
  }
  if (r.function) return `Can ${verbs} ${targetLabel || `function ${r.function.id}`}${when}.`
  if (r.own_cron) {
    if (selfId && r.own_cron.target_id !== selfId) {
      return `Unsupported stored schedule target ${r.own_cron.target_id}. Schedules may run only this function.`
    }
    const prefix = own(r.own_cron, 'prefix')
    const v = prefix ? r.own_cron.prefix : r.own_cron.name
    const which = !prefix ? `named ${quote(v)}` : v === '' ? 'of any name' : `whose names start with ${quote(v)}`
    const target = selfId ? 'this function' : `function ${r.own_cron.target_id}`
    return `Can ${verbs} ${which}, running ${target}${when}.`
  }
  return `Can ${verbs} on its own running execution${when}.`
}

// ---- form <-> statement. mode: storage key|prefix|all, schedules name|prefix.
export const emptyForm = () => ({
  service: '', mode: '', value: '', targetId: '', context: 'execution', actions: [],
})

export function formToInput(form, selfId) {
  const base = { context: form.service === 'storage' ? form.context : 'execution', actions: form.actions }
  if (form.service === 'storage') {
    if (form.mode === 'key') return { ...base, kind: 'kv-key', value: form.value }
    return { ...base, kind: 'kv-prefix', value: form.mode === 'all' ? '' : form.value }
  }
  if (form.service === 'functions') return { ...base, kind: 'function', targetId: form.targetId }
  if (form.service === 'schedules') {
    const prefix = form.mode === 'prefix' || form.mode === 'all'
    return { ...base, kind: prefix ? 'cron-prefix' : 'cron-name', value: form.mode === 'all' ? '' : form.value, targetId: form.targetId || selfId }
  }
  if (form.service === 'tracing') return { ...base, kind: 'spans' }
  return { ...base, kind: '' }
}

export function statementToForm(stmt) {
  const f = { ...emptyForm(), service: serviceOf(stmt), context: stmt.context, actions: [...stmt.actions] }
  const r = stmt.resource
  if (r.own_kv) {
    const prefix = own(r.own_kv, 'prefix')
    f.mode = !prefix ? 'key' : r.own_kv.prefix === '' ? 'all' : 'prefix'
    f.value = prefix ? r.own_kv.prefix : r.own_kv.key
  } else if (r.function) {
    f.targetId = r.function.id
  } else if (r.own_cron) {
    const prefix = own(r.own_cron, 'prefix')
    f.mode = !prefix ? 'name' : r.own_cron.prefix === '' ? 'all' : 'prefix'
    f.value = prefix ? r.own_cron.prefix : r.own_cron.name
    f.targetId = r.own_cron.target_id
  }
  return f
}

export const statementsEqual = (a, b) => statementKey(a) === statementKey(b)

// Replaces one statement in place. Every other entry is carried over untouched.
export function replaceStatement(policy, index, statement) {
  const p = clonePolicy(policy)
  if (!Number.isInteger(index) || index < 0 || index >= p.allow.length) {
    return { ok: false, policy: null, problems: ['That permission is no longer in the draft.'] }
  }
  p.allow[index] = statement
  return finish(p)
}

export function replaceInbound(policy, index, callerId, triggers) {
  const p = clonePolicy(policy)
  if (!Number.isInteger(index) || index < 0 || index >= p.inbound.length) {
    return { ok: false, policy: null, problems: ['That caller is no longer in the draft.'] }
  }
  if (!isDense(triggers) || triggers.some((t, i) => !TRIGGER_IDS.includes(t) || triggers.indexOf(t) !== i)) {
    return { ok: false, policy: null, problems: ['These triggers are not recognised or are repeated.'] }
  }
  if (p.inbound.some((a, i) => i !== index && a.caller_id === callerId)) {
    return { ok: false, policy: null, problems: ['That caller is already accepted.'] }
  }
  p.inbound[index] = { caller_id: callerId, triggers: TRIGGER_IDS.filter((t) => triggers.includes(t)) }
  return finish(p)
}

export const TRIGGER_HELP = Object.freeze({
  f2f: 'It calls this function directly.',
  job: 'It queues a background job that runs this function.',
  cron: 'It sets up a schedule that runs this function.',
})

// Schedule acceptance is enforced for the function's own schedules only, so a
// cron trigger from any other caller can never work. The structural validator
// still accepts it (it may already be stored); the guided path refuses to author it.
export function callerTriggerProblem(callerId, triggers, ownerId) {
  return triggers.includes('cron') && callerId !== ownerId
    ? "Schedule acceptance works only for this function's own schedules, so the caller must be this function. Untick Schedule, or choose this function."
    : ''
}

// Read-only canonical JSON of an authorable policy: no host metadata at all.
export function canonicalPolicyJson(policy) {
  const v = validatePolicy(policy)
  return v.ok ? JSON.stringify(v.policy, null, 2) : ''
}
