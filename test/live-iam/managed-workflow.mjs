import fs from 'node:fs'
import { execFileSync } from 'node:child_process'

if (process.env.ORVA_MANAGED_SCRATCH !== 'disposable-vm') throw new Error('Explicit disposable VM required')
const base = 'http://127.0.0.1:8443'
const keys = JSON.parse(fs.readFileSync('/evidence/credentials.json', 'utf8'))
const events = []
const record = (kind, detail) => {
  events.push({ kind, ...detail })
  fs.writeFileSync('/evidence/managed-results.json', JSON.stringify(events, null, 2), { mode: 0o600 })
}
async function request(path, { method = 'GET', body, key = keys.admin, status = 200 } = {}) {
  const result = await fetch(base + path, { method, headers: { 'Content-Type': 'application/json', 'X-Orva-API-Key': key }, body: body === undefined ? undefined : JSON.stringify(body) })
  const value = await result.json()
  if (result.status !== status) throw new Error(`${method} ${path}: status ${result.status}, expected ${status}`)
  return value
}
function cli(args, body) {
  const raw = execFileSync('/orva', args, { input: JSON.stringify(body), encoding: 'utf8', env: { HOME: '/evidence/cli-home', ORVA_ENDPOINT: base, ORVA_API_KEY: keys.admin, NO_COLOR: '1' } })
  const value = JSON.parse(raw)
  record('slim_cli', { args, result: value })
  return value
}
async function mcp(name, args, error = false) {
  const reply = await fetch(base + '/mcp', { method: 'POST', headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', 'X-Orva-API-Key': keys.admin, 'MCP-Protocol-Version': '2025-06-18' },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/call', params: { name, arguments: args } }) })
  let raw = await reply.text()
  if (raw.startsWith('event:') || raw.startsWith('data:')) raw = raw.split('\n').find((line) => line.startsWith('data: ')).slice(6)
  const message = JSON.parse(raw)
  if (Boolean(message.error || message.result?.isError) !== error) throw new Error(`Unexpected MCP result for ${name}`)
  const value = message.result?.structuredContent
  record('external_mcp', { name, result: value, rejected: error })
  return value
}
const source = (id) => request(`/api/v1/functions/${id}/permissions`)
const get = (id) => request(`/api/v1/iam/policies/${id}?history=true`)
async function owners(ids) {
  return Promise.all(ids.map(async (id) => {
    const s = await source(id)
    return { function_id: id, expected: { revision: s.host.revision, current_artifact: s.host.current_artifact, approved_artifact: s.host.approved_artifact } }
  }))
}
async function decisions(functions, expected) {
  for (const fn of functions) {
    const result = await request(`/fn/${fn.id}/demo`, { method: 'POST', body: {} })
    if (result.decision !== expected[fn.runtime]) throw new Error(`${fn.runtime}: expected ${expected[fn.runtime]}, got ${result.decision}`)
    if (result.decision === 'ALLOWED' && result.value !== `scratch-value-${fn.runtime}`) throw new Error(`${fn.runtime} read another function's namespace`)
    const snapshot = await source(fn.id)
    const latest = snapshot.history[0]
    if (latest.revision !== snapshot.host.revision || latest.authorityEpoch !== snapshot.host.authority_epoch) throw new Error('Audit/counter mismatch')
    record('actual_private_sdk', { runtime: fn.runtime, owner: fn.id, decision: result.decision, host: snapshot.host, latest_audit: latest, inline_policy: snapshot.inline_policy, attachments: snapshot.attachments.map(({ policy_id, version, enabled }) => ({ policy_id, version, enabled })) })
  }
}
function browser(file) {
  const raw = execFileSync('playwright-cli', ['--raw', '-s=managed', 'run-code', `--filename=/src/test/live-iam/${file}`], { encoding: 'utf8' })
  // Raw CLI returns the function's JSON result without browser configuration.
  const result = JSON.parse(raw)
  record('browser', { phase: file, result })
  return result
}
const functions = (await request('/api/scratch/functions')).sort((a, b) => a.runtime.localeCompare(b.runtime))
const node = functions.find((f) => f.runtime === 'node')
const python = functions.find((f) => f.runtime === 'python')
await decisions(functions, { node: 'IAM_DENIED', python: 'IAM_DENIED' })
let p = browser('managed-create-browser.js').policy
cli(['iam', 'policies', 'attach', p.id, '--request', '@-'], { membership_revision: p.membership_revision, version: '1', owners: await owners([node.id]) })
p = (await get(p.id)).policy
await mcp('attach_iam_policy', { id: p.id, request: { membership_revision: p.membership_revision, version: '1', owners: await owners([python.id]) } })
await decisions(functions, { node: 'ALLOWED', python: 'ALLOWED' })
browser('managed-source-browser.js')
browser('managed-version-browser.js')
await decisions(functions, { node: 'ALLOWED', python: 'ALLOWED' })
p = (await get(p.id)).policy
const rollout = { membership_revision: p.membership_revision, version: '2', owners: await owners(functions.map((f) => f.id)) }
await mcp('get_iam_policy_impact', { id: p.id, request: { operation: 'attach', ...rollout } })
await mcp('attach_iam_policy', { id: p.id, request: rollout })
await decisions(functions, { node: 'IAM_DENIED', python: 'IAM_DENIED' })
const reader = { schema_version: 1, allow: [{ actions: ['kv.get'], resource: { own_kv: { key: 'demo' } }, context: 'execution' }], inbound: [] }
const inboundReader = { ...reader, inbound: [{ caller_id: node.id, triggers: ['f2f'] }] }
let q = (await mcp('create_iam_policy', { request: { name: `overlap-${p.id}`, policy: inboundReader } })).policy
await mcp('attach_iam_policy', { id: q.id, request: { membership_revision: q.membership_revision, version: '1', owners: await owners(functions.map((f) => f.id)) } })
p = (await get(p.id)).policy
cli(['iam', 'policies', 'attach', p.id, '--request', '@-'], { membership_revision: p.membership_revision, version: '1', owners: await owners(functions.map((f) => f.id)) })
let inline = (await owners([node.id]))[0].expected
await request(`/api/v1/functions/${node.id}/permissions`, { method: 'PUT', body: { expected: inline, policy: reader } })
const before = await Promise.all(functions.map((f) => source(f.id)))
q = (await get(q.id)).policy
const badOwners = await owners(functions.map((f) => f.id))
badOwners[0].expected.revision = '1'
await request(`/api/v1/iam/policies/${q.id}/attach`, { method: 'POST', body: { membership_revision: q.membership_revision, version: '1', owners: badOwners }, status: 409 })
const after = await Promise.all(functions.map((f) => source(f.id)))
if (before.some((s, i) => JSON.stringify(s) !== JSON.stringify(after[i]))) throw new Error('One stale owner partially committed batch')
record('negative', { stale_one_owner_rejected_all: true })
p = (await get(p.id)).policy
cli(['--yes', 'iam', 'policies', 'detach', p.id, '--request', '@-'], { membership_revision: p.membership_revision, owners: await owners([node.id]), confirm: true })
await decisions(functions, { node: 'ALLOWED', python: 'ALLOWED' })
browser('managed-residual-source-browser.js')
browser('managed-retained-browser.js')
q = (await get(q.id)).policy
await mcp('revoke_iam_policy', { id: q.id, request: { definition_revision: q.definition_revision, membership_revision: q.membership_revision, owners: await owners(functions.map((f) => f.id)), confirm: true } })
await decisions(functions, { node: 'ALLOWED', python: 'ALLOWED' })
p = (await get(p.id)).policy
await mcp('revoke_iam_policy', { id: p.id, request: { definition_revision: p.definition_revision, membership_revision: p.membership_revision, owners: await owners([python.id]), confirm: true } })
await decisions(functions, { node: 'ALLOWED', python: 'IAM_DENIED' })
inline = (await owners([node.id]))[0].expected
await request(`/api/v1/functions/${node.id}/permissions`, { method: 'PUT', body: { expected: inline, policy: { schema_version: 1, allow: [], inbound: [] } } })
await decisions(functions, { node: 'IAM_DENIED', python: 'IAM_DENIED' })
await request('/api/v1/iam/policies', { method: 'POST', key: keys.reader, body: { name: 'forbidden', policy: reader }, status: 403 })
browser('managed-final-source-browser.js')
record('complete', { real_pool_sdk: true, ui_created_v1: true, ui_created_v2: true, slim_cli: true, external_mcp: true, residual_grants: true, terminal_revoke: true, readonly_denied: true })
console.log('Managed workflow PASS. Safe results written to /evidence/managed-results.json.')
