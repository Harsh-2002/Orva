import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  managedExposureAPI, exposureVersion, exposureExpected, canonicalExposureOwner,
} from '../src/api/managedExposure.js'

const OWNER = '0190000a-0000-7000-8000-000000000001'
const OTHER = '0190000b-0000-7000-8000-000000000002'
const TRIGGER = '01900000-0000-7000-8000-0000000000aa'
const EXPECTED = { [OWNER]: { policy_revision: '7', authority_epoch: '9007199254740993' } }

const recorder = (reply = {}) => {
  const calls = []
  const request = async (url, options = {}) => {
    calls.push({ url, ...options })
    return typeof reply === 'function' ? reply(url, options) : reply
  }
  return { calls, request }
}

test('CAS tokens reach the wire as the exact decimal strings that were read', async () => {
  const { calls, request } = recorder({ status: 'saved' })
  const api = managedExposureAPI(request)
  await api.setRoute({
    path: '/hooks/stripe',
    functionId: OWNER,
    version: '9007199254740993',
    expected: EXPECTED,
  })
  assert.equal(calls.length, 1)
  assert.equal(calls[0].url, '/api/v1/routes')
  assert.equal(calls[0].method, 'POST')
  assert.deepEqual(calls[0].body, {
    version: '9007199254740993',
    expected: { [OWNER]: { policy_revision: '7', authority_epoch: '9007199254740993' } },
    path: '/hooks/stripe',
    function_id: OWNER,
  })
  // A Number() round trip would have produced 9007199254740992.
  assert.equal(calls[0].body.expected[OWNER].authority_epoch, '9007199254740993')
})

test('a rebind sends only the tokens it was given, never a synthesised owner', async () => {
  const { calls, request } = recorder({ version: '3', expected: { ...EXPECTED, [OTHER]: { policy_revision: '1', authority_epoch: '2' } } })
  const api = managedExposureAPI(request)
  await api.inspectRoute('/hooks/stripe', { functionId: OTHER })
  assert.deepEqual(calls[0], {
    url: '/api/v1/exposures',
    params: { kind: 'route', key: '/hooks/stripe', function_id: OTHER },
    signal: undefined,
  })
})

test('owners and trigger ids must be canonical lowercase UUIDs', async () => {
  const { calls, request } = recorder({})
  const api = managedExposureAPI(request)
  for (const bad of ['Payments', OWNER.toUpperCase(), OWNER.replace(/-/g, ''), '00000000-0000-0000-0000-000000000000']) {
    await assert.rejects(api.inspectRoute('/x', { functionId: bad }), /canonical UUID/)
  }
  await assert.rejects(api.deleteInbound(OWNER, 'not-a-uuid', { version: '1', expected: EXPECTED }), /canonical UUID/)
  assert.equal(calls.length, 0)
  assert.equal(canonicalExposureOwner(OWNER), OWNER)
})

test('version tokens are decimal strings, and zero is only valid where the server allows it', async () => {
  assert.equal(exposureVersion('0'), '0')
  assert.equal(exposureVersion('12'), '12')
  for (const bad of [0, 12, '', '01', '-1', '1.0', '9'.repeat(20), null, undefined]) {
    assert.throws(() => exposureVersion(bad), /version token/)
  }
})

test('an expected map keeps both owners, rejects unknown fields, and never coerces counters', async () => {
  const two = exposureExpected({
    [OWNER]: { policy_revision: '1', authority_epoch: '2' },
    [OTHER]: { policy_revision: '3', authority_epoch: '4' },
  })
  assert.deepEqual(two, {
    [OWNER]: { policy_revision: '1', authority_epoch: '2' },
    [OTHER]: { policy_revision: '3', authority_epoch: '4' },
  })
  assert.throws(() => exposureExpected({ [OWNER]: { policy_revision: 1, authority_epoch: '2' } }), /authority tokens/)
  assert.throws(() => exposureExpected({ [OWNER]: { policy_revision: '1', authority_epoch: '2', extra: '3' } }), /authority tokens/)
  assert.throws(() => exposureExpected({ [OWNER]: { policy_revision: '0', authority_epoch: '2' } }), /authority tokens/)
  assert.throws(() => exposureExpected({}), /authority tokens/)
})

test('a 409 becomes a stale error, not a retry', async () => {
  let attempts = 0
  const request = async () => {
    attempts += 1
    throw Object.assign(new Error('conflict'), {
      response: { status: 409, data: { error: { code: 'IAM_CONFLICT', message: 'Exposure or authority changed; inspect again' } } },
    })
  }
  const api = managedExposureAPI(request)
  await assert.rejects(api.deleteRoute({ path: '/x', version: '2', expected: EXPECTED }), (error) => {
    assert.equal(error.stale, true)
    assert.equal(error.status, 409)
    assert.equal(error.code, 'IAM_CONFLICT')
    assert.match(error.message, /inspect again/)
    return true
  })
  assert.equal(attempts, 1)
})

test('403 and 400 keep their status so the caller can offer a read-only or a fixed form', async () => {
  const reject = (status, code) => async () => {
    throw Object.assign(new Error('x'), { response: { status, data: { error: { code, message: 'nope' } } } })
  }
  await assert.rejects(managedExposureAPI(reject(403, 'FORBIDDEN')).setRoute({ path: '/x', functionId: OWNER, version: '1', expected: EXPECTED }), (e) => e.forbidden === true && e.stale === false)
  await assert.rejects(managedExposureAPI(reject(400, 'VALIDATION')).setRoute({ path: '/x', functionId: OWNER, version: '1', expected: EXPECTED }), (e) => e.invalid === true && e.stale === false)
})

test('inbound writes leave owner and id to the path, and delete confirms', async () => {
  const { calls, request } = recorder({ inbound_webhook: { id: TRIGGER }, secret: 's', trigger_url: '/webhook/x' })
  const api = managedExposureAPI(request)
  await api.createInbound(OWNER, { name: 'ci', signatureFormat: 'github', version: '0', expected: EXPECTED })
  assert.equal(calls[0].url, `/api/v1/functions/${OWNER}/inbound-webhooks`)
  assert.equal(calls[0].method, 'POST')
  assert.equal('function_id' in calls[0].body, false)
  assert.equal('id' in calls[0].body, false)
  assert.equal(calls[0].body.version, '0')

  await api.updateInbound(OWNER, TRIGGER, { active: false, version: '4', expected: EXPECTED })
  assert.equal(calls[1].url, `/api/v1/functions/${OWNER}/inbound-webhooks/${TRIGGER}`)
  assert.deepEqual(calls[1].body, {
    version: '4', expected: EXPECTED, active: false,
  })

  await api.deleteInbound(OWNER, TRIGGER, { version: '5', expected: EXPECTED })
  assert.deepEqual(calls[2].body, { version: '5', expected: EXPECTED, confirm: true })
})

test('route delete sends the path in the query and never repeats it in the body', async () => {
  const { calls, request } = recorder({ status: 'deleted' })
  const api = managedExposureAPI(request)
  await api.deleteRoute({ path: '/hooks/stripe', version: '3', expected: EXPECTED })
  assert.deepEqual(calls[0], {
    url: '/api/v1/routes',
    method: 'DELETE',
    params: { path: '/hooks/stripe' },
    body: { version: '3', expected: EXPECTED, confirm: true },
    signal: undefined,
  })
})

test('list responses carry version and published per row, and fail closed on a bad shape', async () => {
  const api = managedExposureAPI(async (url) => {
    if (url === '/api/v1/routes') {
      return { routes: [{ path: '/a', function_id: OWNER, methods: '*', version: '2', published: true }], truncated: false }
    }
    return {
      inbound_webhooks: [{ id: TRIGGER, version: '0', published: false }],
      expected: EXPECTED,
      truncated: true,
    }
  })
  const routes = await api.listRoutes()
  assert.equal(routes.routes[0].version, '2')
  assert.equal(routes.routes[0].published, true)
  assert.equal(routes.truncated, false)

  const inbound = await api.listInbound(OWNER)
  assert.equal(inbound.rows[0].version, '0')
  assert.equal(inbound.truncated, true)
  assert.deepEqual(inbound.expected, EXPECTED)

  await assert.rejects(managedExposureAPI(async () => ({ routes: [{ path: '/a', version: 2 }] })).listRoutes(), /version token/)
  await assert.rejects(managedExposureAPI(async () => ({ routes: null })).listRoutes(), /route list response/)
})
