import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  LIMITS, isCanonicalUuid, isValidLiteral, parseDecimal, validatePolicy, normalizeSnapshot,
  buildStatement, addStatement, removeStatement, addInbound, diffPolicies, policyWarnings,
  deriveAccess, buildSavePayload, buildReviewPayload, hostFingerprint, describeStatement,
  presetActions, emptyPolicy, targetInfo, parseCounter, statementSummary,
} from '../src/utils/permissionsModel.js'
import { makeSnapshot, policy, caps, ACTOR_ID, FN_ID, OTHER_FN_ID, CALLER_ID, MISSING_ID, DIGEST_A, DIGEST_B } from './fixtures/permissionsFixtures.js'

const stmt = (over) => ({ actions: ['kv.get'], resource: { own_kv: { key: 'k' } }, context: 'execution', ...over })
const withAllow = (...allow) => ({ schema_version: 1, allow, inbound: [] })

test('UUIDs must be lowercase canonical with a valid version and variant', () => {
  assert.equal(isCanonicalUuid(FN_ID), true)
  assert.equal(isCanonicalUuid(FN_ID.toUpperCase()), false)
  assert.equal(isCanonicalUuid('00000000-0000-0000-0000-000000000000'), false)
  assert.equal(isCanonicalUuid('0190a1b2-c3d4-0e5f-8a6b-1c2d3e4f5a6b'), false) // version 0
  assert.equal(isCanonicalUuid('0190a1b2-c3d4-7e5f-0a6b-1c2d3e4f5a6b'), false) // variant
  assert.equal(isCanonicalUuid(` ${FN_ID}`), false)
  assert.equal(isCanonicalUuid(null), false)
})

test('selectors follow the Go literal rules: UTF-8 valid, at most 256 runes, empty only for prefixes', () => {
  assert.equal(isValidLiteral('a'.repeat(256), false), true)
  assert.equal(isValidLiteral('a'.repeat(257), false), false)
  assert.equal(isValidLiteral('\u{1F600}'.repeat(256), false), true) // runes, not UTF-16 units
  assert.equal(isValidLiteral('\u{1F600}'.repeat(257), false), false)
  assert.equal(isValidLiteral('', false), false)
  assert.equal(isValidLiteral('', true), true)
  assert.equal(isValidLiteral('\uD800', true), false) // lone surrogate cannot be UTF-8
  assert.equal(isValidLiteral('a\u0000b', false), true) // NUL is not banned by the Go validator
  assert.equal(isValidLiteral(' Key ', false), true) // never trimmed or case-folded
})

test('revision and epoch stay decimal strings beyond MAX_SAFE_INTEGER', () => {
  assert.equal(parseDecimal('9007199254740993'), 9007199254740993n)
  assert.equal(parseDecimal('18446744073709551615'), 18446744073709551615n)
  assert.equal(parseDecimal('18446744073709551616'), null)
  assert.equal(parseDecimal('-1'), null)
  assert.equal(parseDecimal('-9223372036854775808', { signed: true }), -9223372036854775808n)
  assert.equal(parseDecimal('9223372036854775808', { signed: true }), null)
  for (const bad of ['01', '1.0', '1e3', ' 1', '', '-0', 7, null]) assert.equal(parseDecimal(bad, { signed: true }), null)
  const s = normalizeSnapshot(makeSnapshot({ host: { revision: '9007199254740993', authorityEpoch: '9223372036854775807' } }))
  assert.equal(s.snapshot.host.revision, '9007199254740993')
  assert.equal(s.snapshot.host.authorityEpoch, '9223372036854775807')
  assert.equal(normalizeSnapshot(makeSnapshot({ host: { revision: 7 } })).status, 'unavailable')
})

// workload_iam_schema.go: revision and authority_epoch are INTEGER CHECK(> 0),
// so 0, negatives and anything above 2^63-1 can never be a stored value.
test('host and history counters are positive signed 64-bit decimal strings', () => {
  assert.equal(parseCounter('1'), 1n)
  assert.equal(parseCounter('9223372036854775807'), 9223372036854775807n)
  for (const bad of ['0', '-5', '-0', '01', '1.0', '9223372036854775808', '', ' 1', 7, null, undefined]) {
    assert.equal(parseCounter(bad), null, String(bad))
  }
  for (const host of [{ revision: '0' }, { revision: '9223372036854775808' }, { authorityEpoch: '0' }, { authorityEpoch: '-5' }]) {
    assert.equal(normalizeSnapshot(makeSnapshot({ host })).status, 'unavailable', JSON.stringify(host))
  }
  const h = makeSnapshot()
  h.history[0].authorityEpoch = '0'
  h.history[1].revision = '9223372036854775808'
  assert.equal(normalizeSnapshot(h).snapshot.history.length, 0)
})

test('the snapshot owner must be a canonical UUID', () => {
  for (const id of ['', 'not-a-uuid', FN_ID.toUpperCase(), '00000000-0000-0000-0000-000000000000']) {
    const s = makeSnapshot()
    s.function.id = id
    assert.equal(normalizeSnapshot(s).status, 'unavailable', id)
  }
})

// workload_iam.go validateIAMActor: kind in api_key|session|oauth|system and a
// canonical UUID. Nothing else reaches the audit table, so nothing else renders.
test('history actors have a closed kind and a canonical UUID, never free text', () => {
  const kept = (actor) => {
    const s = makeSnapshot()
    s.history[0].actor = actor
    return normalizeSnapshot(s).snapshot.history.length
  }
  for (const kind of ['api_key', 'session', 'oauth', 'system']) assert.equal(kept({ kind, id: ACTOR_ID }), 2, kind)
  for (const actor of [
    { kind: 'api_key', id: 'Bearer SYNTHETIC_NOT_A_REAL_CREDENTIAL' },
    { kind: 'system', id: 'token' },
    { kind: 'user', id: ACTOR_ID },
    { kind: 'admin', id: OTHER_FN_ID },
    { kind: 'API_KEY', id: ACTOR_ID },
    { kind: 'session', id: ACTOR_ID.toUpperCase() },
    { kind: 'oauth' },
  ]) assert.equal(kept(actor), 1, JSON.stringify(actor))
})

test('sparse policy arrays are rejected before holes can serialise as null', () => {
  assert.equal(validatePolicy({ schema_version: 1, allow: Array(1), inbound: [] }).ok, false)
  assert.equal(validatePolicy({ schema_version: 1, allow: [], inbound: Array(1) }).ok, false)
  const sparseActions = { actions: Array(1), resource: { own_kv: { key: 'k' } }, context: 'execution' }
  assert.equal(validatePolicy(withAllow(sparseActions)).ok, false)
  assert.equal(validatePolicy({ schema_version: 1, allow: [], inbound: [{ caller_id: CALLER_ID, triggers: Array(1) }] }).ok, false)
  assert.equal(normalizeSnapshot(makeSnapshot({ policy: { schema_version: 1, allow: Array(1), inbound: [] } })).snapshot.policy, null)
})

test('the add forms reject unknown or repeated actions and triggers instead of filtering them', () => {
  const kv = (actions) => buildStatement({ kind: 'kv-key', value: 'k', actions })
  assert.equal(kv(['kv.get']).ok, true)
  for (const actions of [['kv.get', 'unknown'], ['kv.get', 'kv.get'], Array(1), null, 'kv.get']) {
    assert.equal(kv(actions).ok, false, JSON.stringify(actions))
  }
  const base = { schema_version: 1, allow: [], inbound: [] }
  assert.equal(addInbound(base, OTHER_FN_ID, ['f2f', 'job']).ok, true)
  for (const triggers of [['f2f', 'unknown'], ['f2f', 'f2f'], Array(1), null]) {
    assert.equal(addInbound(base, OTHER_FN_ID, triggers).ok, false, JSON.stringify(triggers))
  }
})

test('change lines drop only the redundant selector word and keep literals and actions exact', () => {
  const kvKey = { actions: ['kv.get', 'kv.put'], resource: { own_kv: { key: 'state' } }, context: 'execution' }
  assert.equal(statementSummary(kvKey), 'Own storage, one key state: kv.get, kv.put')
  assert.equal(statementSummary({ ...kvKey, resource: { own_kv: { prefix: ' config/ ' } } }), 'Own storage, keys with a prefix  config/ : kv.get, kv.put')
  assert.equal(statementSummary({ ...kvKey, resource: { own_kv: { prefix: '' } } }), 'Own storage, every key prefix (empty): kv.get, kv.put')
  const cron = { actions: ['crons.upsert'], resource: { own_cron: { name: 'nightly', target_id: FN_ID } }, context: 'execution' }
  assert.equal(statementSummary(cron), 'Own schedule name nightly: crons.upsert')
  assert.equal(statementSummary({ ...cron, resource: { own_cron: { prefix: 'nt-', target_id: FN_ID } } }), 'Own schedules, by prefix nt-: crons.upsert')
  assert.equal(statementSummary(policy().allow[2]), `Another function ${OTHER_FN_ID}: functions.invoke`)
  assert.equal(statementSummary(policy().allow[0]), 'Own storage, keys with a prefix config/: kv.get, kv.list (at startup)')
  assert.doesNotMatch(statementSummary(kvKey), /key key/)
})

test('a valid policy round-trips to an owned copy in Go field order', () => {
  const input = policy()
  const out = validatePolicy(input)
  assert.equal(out.ok, true)
  assert.deepEqual(out.policy, input)
  assert.notEqual(out.policy, input)
  assert.notEqual(out.policy.allow[0], input.allow[0])
  assert.deepEqual(Object.keys(out.policy), ['schema_version', 'allow', 'inbound'])
  out.policy.allow[0].actions.push('kv.put')
  assert.deepEqual(input.allow[0].actions, ['kv.get', 'kv.list']) // source untouched
})

test('dangerous shapes are rejected, not coerced', () => {
  const bad = [
    { ...policy(), allow: null },
    { ...policy(), inbound: null },
    { ...policy(), schema_version: 2 },
    { ...policy(), extra: true },
    { ...policy(), allow: [{ ...policy().allow[0], approved: true }] },
    withAllow(stmt({ resource: { own_kv: { key: 'a' }, function: { id: FN_ID } } })), // union with two members
    withAllow(stmt({ resource: {} })),
    withAllow(stmt({ resource: { own_kv: { key: 'a', prefix: 'b' } } })),
    withAllow(stmt({ resource: { own_kv: {} } })),
    withAllow(stmt({ resource: { own_kv: { key: null } } })),
    withAllow(stmt({ resource: { own_kv: { key: '' } } })), // empty exact key
    withAllow(stmt({ actions: ['kv.list'] })), // list needs a prefix
    withAllow(stmt({ actions: ['kv.get', 'kv.get'] })),
    withAllow(stmt({ actions: ['kv.nope'] })),
    withAllow(stmt({ actions: [] })),
    withAllow(stmt({ actions: ['kv.put'], context: 'bootstrap' })), // bootstrap is read only
    withAllow(stmt({ context: 'later' })),
    withAllow(stmt({ actions: ['functions.invoke'], resource: { function: { id: FN_ID.toUpperCase() } } })),
    withAllow(stmt({ actions: ['spans.emit'], resource: { own_execution: { x: 1 } } })),
    withAllow(stmt({ actions: ['crons.upsert'], resource: { own_cron: { name: 'n', target_id: 'nope' } } })),
    { ...policy(), inbound: [{ caller_id: CALLER_ID, triggers: [] }] },
    { ...policy(), inbound: [{ caller_id: CALLER_ID, triggers: ['job', 'job'] }] },
    { ...policy(), inbound: [{ caller_id: CALLER_ID, triggers: ['http'] }] },
    JSON.parse('{"schema_version":1,"allow":[],"inbound":[],"__proto__":{"x":1}}'),
  ]
  bad.forEach((p, i) => assert.equal(validatePolicy(p).ok, false, `case ${i} should fail`))
  assert.equal(validatePolicy(withAllow(stmt({ actions: ['kv.list'], resource: { own_kv: { prefix: '' } } }))).ok, true)
})

test('count and size limits match the server', () => {
  const many = (n) => withAllow(...Array.from({ length: n }, (_, i) => stmt({ resource: { own_kv: { key: `k${i}` } } })))
  assert.equal(validatePolicy(many(LIMITS.maxStatements)).ok, true)
  assert.equal(validatePolicy(many(LIMITS.maxStatements + 1)).ok, false)
  const callers = (n) => ({ schema_version: 1, allow: [], inbound: Array.from({ length: n }, (_, i) => ({ caller_id: `0190a1b2-c3d4-7e5f-9a6b-${String(i).padStart(12, '0')}`, triggers: ['f2f'] })) })
  assert.equal(validatePolicy(callers(LIMITS.maxTargets)).ok, true)
  assert.equal(validatePolicy(callers(LIMITS.maxTargets + 1)).ok, false)
  const big = withAllow(...Array.from({ length: 64 }, (_, i) => stmt({ resource: { own_kv: { key: `${i}${'x'.repeat(256 - String(i).length)}` } } })))
  assert.equal(validatePolicy(big).ok, true) // 64 * ~330 bytes stays under 32 KiB
  const heavy = withAllow(...Array.from({ length: 64 }, (_, i) => stmt({ resource: { own_kv: { key: `${i}${'<'.repeat(255 - String(i).length)}` } } })))
  assert.equal(validatePolicy(heavy).ok, false) // '<' marshals to six bytes in Go
})

test('the form never adds rights silently and rejects what Go rejects', () => {
  const ok = buildStatement({ kind: 'kv-key', value: 'n', actions: ['kv.incr', 'kv.cas'] })
  assert.deepEqual(ok.statement.actions, ['kv.cas', 'kv.incr'].sort((a, b) => ['kv.incr', 'kv.cas'].indexOf(a) - ['kv.incr', 'kv.cas'].indexOf(b)))
  assert.equal(ok.statement.actions.includes('kv.get'), false)
  const added = addStatement(emptyPolicy(), ok.statement)
  assert.equal(added.ok, true)
  assert.equal(policyWarnings(added.policy)[0].code, 'needs-kv-get')
  const fixed = addStatement(added.policy, buildStatement({ kind: 'kv-key', value: 'n', actions: ['kv.get'] }).statement)
  assert.deepEqual(policyWarnings(fixed.policy), [])
  const prefixRead = addStatement(added.policy, buildStatement({ kind: 'kv-prefix', value: '', actions: ['kv.get'] }).statement)
  assert.deepEqual(policyWarnings(prefixRead.policy), []) // an empty prefix covers every key
  assert.equal(buildStatement({ kind: 'kv-key', value: '', actions: ['kv.get'] }).ok, false)
  assert.equal(buildStatement({ kind: 'kv-key', value: 'a', actions: [] }).errors.actions.length > 0, true)
  assert.equal(buildStatement({ kind: 'function', targetId: 'x', actions: ['functions.invoke'] }).errors.targetId.length > 0, true)
  assert.equal(buildStatement({ kind: 'kv-key', value: 'a', context: 'bootstrap', actions: ['kv.put'] }).ok, false)
  assert.equal(buildStatement({ kind: 'function', targetId: OTHER_FN_ID, context: 'bootstrap', actions: ['functions.invoke'] }).ok, false)
  assert.deepEqual(presetActions('counters', 'kv-key', 'execution'), ['kv.incr', 'kv.cas'])
  assert.deepEqual(presetActions('read', 'kv-key', 'execution'), ['kv.get']) // list needs a prefix
  assert.deepEqual(presetActions('readwrite', 'kv-prefix', 'bootstrap'), ['kv.get', 'kv.list'])
})

test('edits return new policies and never mutate their input', () => {
  const base = policy()
  const frozen = JSON.stringify(base)
  const next = removeStatement(base, 1)
  assert.equal(next.allow.length, 2)
  assert.equal(JSON.stringify(base), frozen)
  const dup = addInbound(base, CALLER_ID, ['cron'])
  assert.equal(dup.ok, false)
  const fresh = addInbound(base, MISSING_ID, ['cron', 'f2f'])
  assert.deepEqual(fresh.policy.inbound[1].triggers, ['f2f', 'cron'])
  assert.equal(JSON.stringify(base), frozen)
})

test('diff ignores reordering and counts real changes', () => {
  const base = policy()
  const reordered = { ...policy(), allow: [...policy().allow].reverse() }
  assert.equal(diffPolicies(base, reordered).total, 0)
  assert.equal(diffPolicies(base, removeStatement(base, 0)).statements.removed.length, 1)
  const changed = { ...policy(), inbound: [{ caller_id: CALLER_ID, triggers: ['f2f'] }] }
  const d = diffPolicies(base, changed)
  assert.equal(d.inbound.added.length + d.inbound.removed.length, 2)
})

test('snapshots: malformed host or schema is unavailable, missing capabilities are false', () => {
  assert.equal(normalizeSnapshot(null).status, 'unavailable')
  assert.equal(normalizeSnapshot({}).status, 'unavailable')
  assert.equal(normalizeSnapshot(makeSnapshot({ host: { enforcement: 'maybe' } })).status, 'unavailable')
  assert.equal(normalizeSnapshot(makeSnapshot({ host: { currentArtifact: 'abc' } })).status, 'unavailable')
  assert.equal(normalizeSnapshot(makeSnapshot({ host: { currentArtifact: undefined } })).status, 'unavailable')
  assert.equal(normalizeSnapshot(makeSnapshot({ policy: { ...policy(), schema_version: 2 } })).status, 'unavailable')
  const noCaps = makeSnapshot()
  delete noCaps.host.capabilities
  assert.deepEqual(normalizeSnapshot(noCaps).snapshot.host.capabilities,
    { canRead: false, canManage: false, canReview: false, canEditPrivilegedCode: false })
  const truthy = normalizeSnapshot(makeSnapshot({ host: { capabilities: { canRead: 'true', canManage: 1, canReview: {} } } }))
  assert.equal(truthy.snapshot.host.capabilities.canManage, false) // only literal true counts
})

test('without canRead no policy, targets or history are carried', () => {
  const s = normalizeSnapshot(makeSnapshot({ host: { capabilities: caps({ canRead: false, canManage: true }) } })).snapshot
  assert.equal(s.policy, null)
  assert.deepEqual(s.targets, [])
  assert.deepEqual(s.history, [])
  assert.equal(deriveAccess({ snapshot: s, functionId: FN_ID }).canEditPolicy, false)
})

test('an invalid stored policy is retained as a problem, not normalised into authority', () => {
  const s = normalizeSnapshot(makeSnapshot({ policy: { schema_version: 1, allow: [{ actions: ['kv.get'] }], inbound: [] } })).snapshot
  assert.equal(s.policy, null)
  assert.ok(s.policyProblems.length > 0)
  assert.equal(deriveAccess({ snapshot: s, functionId: FN_ID }).canEditPolicy, false)
})

test('access: edit needs identity, manage, active enforcement and no lock', () => {
  const snap = (host) => normalizeSnapshot(makeSnapshot({ host })).snapshot
  const base = { snapshot: snap({}), functionId: FN_ID }
  assert.equal(deriveAccess(base).canEditPolicy, true)
  assert.equal(deriveAccess({ ...base, functionId: OTHER_FN_ID }).canRead, false) // other owner
  assert.equal(deriveAccess({ ...base, functionId: OTHER_FN_ID }).canEditPolicy, false)
  assert.equal(deriveAccess({ ...base, snapshot: snap({ enforcement: 'inactive' }) }).canEditPolicy, false)
  assert.equal(deriveAccess({ ...base, snapshot: snap({ enforcement: 'unknown' }) }).canEditPolicy, false)
  assert.equal(deriveAccess({ ...base, snapshot: snap({ capabilities: caps({ canManage: false }) }) }).canEditPolicy, false)
  for (const lock of [{ saving: true }, { loading: true }, { loadError: 'x' }, { readOnlyReason: 'why' }]) {
    assert.equal(deriveAccess({ ...base, ...lock }).canEditPolicy, false)
  }
})

test('save and review payloads carry policy plus expected host state only', () => {
  const snap = normalizeSnapshot(makeSnapshot({ host: { capabilities: caps({ canReview: true }) } })).snapshot
  const draft = removeStatement(snap.policy, 0)
  const saved = buildSavePayload(snap, draft)
  assert.deepEqual(Object.keys(saved.payload).sort(), ['expected', 'policy'])
  assert.deepEqual(saved.payload.expected, { revision: '7', currentArtifact: DIGEST_A, approvedArtifact: DIGEST_A })
  assert.notEqual(saved.payload.policy, draft)
  assert.equal(buildSavePayload(snap, { ...draft, approved: true }).ok, false)
  assert.deepEqual(buildReviewPayload(snap).payload.policy, snap.policy)
  const none = normalizeSnapshot(makeSnapshot({ host: { currentArtifact: null } })).snapshot
  assert.equal(buildReviewPayload(none).ok, false)
})

test('fingerprint changes with revision or either artifact', () => {
  const f = (host) => hostFingerprint(normalizeSnapshot(makeSnapshot({ host })).snapshot)
  const base = f({})
  assert.notEqual(f({ revision: '8' }), base)
  assert.notEqual(f({ currentArtifact: DIGEST_B }), base)
  assert.notEqual(f({ approvedArtifact: null }), base)
  assert.equal(f({ quarantined: true }), base) // display-only state is not a draft base
})

test('descriptions never claim effect and unresolved targets stay UUID-bound', () => {
  const d = describeStatement(policy().allow[2])
  assert.match(d.notes.join(' '), /also accepts this one/)
  const snap = normalizeSnapshot(makeSnapshot()).snapshot
  assert.deepEqual(targetInfo(snap, OTHER_FN_ID), { id: OTHER_FN_ID, label: 'preview-renderer', resolved: true })
  assert.deepEqual(targetInfo(snap, CALLER_ID), { id: CALLER_ID, label: null, resolved: false })
  assert.equal(targetInfo(snap, MISSING_ID).label, null)
})
