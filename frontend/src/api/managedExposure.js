import client from './client.js'
import { isCanonicalUuid } from '../utils/permissionsModel.js'

// Managed custom-route and inbound-webhook mutations. Every write carries the
// exact `version` and per-owner `expected` tokens from a read of the same
// mapping; the server rejects anything else with 409 rather than retrying.
// Counters stay decimal *strings* end to end — a Number() round trip would
// reorder or round a value above 2^53 and turn a valid CAS into a guess.

export const MAX_EXPOSURE_ROWS = 256
const MAX_PATH = 4096
const MAX_METHODS = 256
const MAX_NAME = 256
const MAX_HEADER = 256

const DECIMAL = /^(0|[1-9][0-9]*)$/
const POSITIVE = /^[1-9][0-9]*$/

export const SIGNATURE_FORMATS = [
  'hmac_sha256_hex',
  'hmac_sha256_base64',
  'github',
  'stripe',
  'slack',
]

const invalid = (message) => Object.assign(new Error(message), { invalid: true, status: 400 })

const bounded = (value, max, what) => {
  if (typeof value !== 'string' || value.length === 0 || value.length > max || value.includes('\uFFFD')) {
    throw invalid(`${what} must be a non-empty string of at most ${max} characters.`)
  }
  return value
}

// Owners and trigger ids are exact canonical lowercase UUIDs on the managed
// API. Names are never adopted and other spellings are a 400.
export function canonicalExposureOwner(id, what = 'Function') {
  if (!isCanonicalUuid(id)) throw invalid(`${what} must be a canonical UUID.`)
  return id
}

export function exposureVersion(value) {
  if (typeof value !== 'string' || value.length > 19 || !DECIMAL.test(value)) {
    throw invalid('Inspect the mapping again: its version token is missing or malformed.')
  }
  return value
}

// expected is owner UUID -> {policy_revision, authority_epoch}, both decimal
// strings. One owner normally; a route rebind carries the old and new owners.
export function exposureExpected(expected) {
  if (!expected || typeof expected !== 'object' || Array.isArray(expected)) {
    throw invalid('Inspect the mapping again: its authority tokens are missing.')
  }
  const owners = Object.keys(expected)
  if (owners.length < 1 || owners.length > 2) {
    throw invalid('Inspect the mapping again: its authority tokens are malformed.')
  }
  const out = {}
  for (const owner of owners) {
    if (!isCanonicalUuid(owner)) throw invalid('An expected owner is not a canonical UUID.')
    const row = expected[owner]
    if (!row || typeof row !== 'object' || Array.isArray(row)) {
      throw invalid('Inspect the mapping again: its authority tokens are malformed.')
    }
    const keys = Object.keys(row).sort().join(',')
    if (keys !== 'authority_epoch,policy_revision') {
      throw invalid('Inspect the mapping again: its authority tokens are malformed.')
    }
    const revision = row.policy_revision
    const epoch = row.authority_epoch
    if (typeof revision !== 'string' || revision.length > 19 || !POSITIVE.test(revision)
      || typeof epoch !== 'string' || epoch.length > 19 || !POSITIVE.test(epoch)) {
      throw invalid('Inspect the mapping again: its authority tokens are malformed.')
    }
    out[owner] = { policy_revision: revision, authority_epoch: epoch }
  }
  return out
}

const casTokens = (version, expected) => ({
  version: exposureVersion(version),
  expected: exposureExpected(expected),
})

const stamp = (payload) => {
  if (!payload || typeof payload !== 'object') throw invalid('The server returned an unusable exposure response.')
  const version = exposureVersion(payload.version)
  const expected = exposureExpected(payload.expected)
  return { version, expected }
}

const freshness = (payload, what) => {
  if (!payload || typeof payload !== 'object') throw invalid(`The server returned an unusable ${what} response.`)
  return stamp(payload)
}

const optionalText = (value, max, what) => (value === undefined || value === null ? undefined : bounded(value, max, what))

function exposureFailure(error) {
  const status = error?.response?.status
  const body = error?.response?.data?.error
  const failure = new Error(body?.message || error?.message || 'Exposure request failed.')
  if (Number.isInteger(status)) failure.status = status
  failure.code = body?.code || ''
  // 409 means the read the request was built from is stale. The operator must
  // inspect again and resubmit deliberately; a blind retry repeats the guess.
  failure.stale = status === 409
  failure.invalid = status === 400
  failure.forbidden = status === 403
  failure.exposureMapped = true
  return failure
}

async function defaultRequest(url, { body, ...options } = {}) {
  return (await client.request({ baseURL: '/', url, data: body, ...options })).data
}

export function managedExposureAPI(request = defaultRequest) {
  // The status mapping belongs to this contract, not to the transport, so an
  // injected request (tests, alternate clients) reports the same stale flag.
  const call = async (url, options) => {
    try {
      return await request(url, options)
    } catch (error) {
      throw error?.exposureMapped ? error : exposureFailure(error)
    }
  }
  return {
    async inspectRoute(path, { functionId = '', signal } = {}) {
      bounded(path, MAX_PATH, 'Route path')
      const params = { kind: 'route', key: path }
      if (functionId) params.function_id = canonicalExposureOwner(functionId)
      return freshness(await call('/api/v1/exposures', { params, signal }), 'route inspection')
    },

    async inspectInbound(functionId, { id = '', signal } = {}) {
      canonicalExposureOwner(functionId)
      const params = { kind: 'inbound', function_id: functionId }
      if (id) params.key = canonicalExposureOwner(id, 'Trigger')
      const out = await call('/api/v1/exposures', { params, signal })
      const tokens = freshness(out, 'inbound inspection')
      return {
        ...tokens,
        inboundWebhook: out.inbound_webhook ?? null,
        inboundPublished: out.inbound_published === true,
      }
    },

    async listRoutes(signal) {
      const out = await call('/api/v1/routes', { signal })
      if (!out || !Array.isArray(out.routes) || out.routes.length > MAX_EXPOSURE_ROWS) {
        throw invalid('The route list response was invalid.')
      }
      for (const row of out.routes) exposureVersion(row?.version)
      return { routes: out.routes, truncated: out.truncated === true }
    },

    async setRoute({ path, functionId, methods, version, expected, signal }) {
      bounded(path, MAX_PATH, 'Route path')
      canonicalExposureOwner(functionId)
      const body = { ...casTokens(version, expected), path, function_id: functionId }
      if (methods !== undefined && methods !== null && methods !== '') {
        body.methods = bounded(methods, MAX_METHODS, 'Methods')
      }
      return call('/api/v1/routes', { method: 'POST', body, signal })
    },

    async deleteRoute({ path, version, expected, signal }) {
      bounded(path, MAX_PATH, 'Route path')
      // The path travels in the query only; a copy in the body is a 400.
      return call('/api/v1/routes', {
        method: 'DELETE',
        params: { path },
        body: { ...casTokens(version, expected), confirm: true },
        signal,
      })
    },

    async listInbound(functionId, signal) {
      canonicalExposureOwner(functionId)
      const out = await call(`/api/v1/functions/${functionId}/inbound-webhooks`, { signal })
      if (!out || !Array.isArray(out.inbound_webhooks) || out.inbound_webhooks.length > MAX_EXPOSURE_ROWS) {
        throw invalid('The inbound webhook list response was invalid.')
      }
      for (const row of out.inbound_webhooks) exposureVersion(row?.version)
      return {
        rows: out.inbound_webhooks,
        expected: exposureExpected(out.expected),
        truncated: out.truncated === true,
      }
    },

    async getInbound(functionId, id, signal) {
      canonicalExposureOwner(functionId)
      canonicalExposureOwner(id, 'Trigger')
      const out = await call(`/api/v1/functions/${functionId}/inbound-webhooks/${id}`, { signal })
      if (!out || !out.inbound_webhook) throw invalid('The server returned an unusable trigger response.')
      return {
        ...stamp(out),
        row: out.inbound_webhook,
        published: out.published === true,
      }
    },

    // Owner and id travel in the path; the handler injects them and rejects a
    // body that repeats either.
    async createInbound(functionId, { name, signatureFormat, signatureHeader, active, version, expected, signal }) {
      canonicalExposureOwner(functionId)
      const body = { ...casTokens(version, expected), name: bounded(name, MAX_NAME, 'Name') }
      const format = optionalText(signatureFormat, 64, 'Signature format')
      if (format !== undefined) {
        if (!SIGNATURE_FORMATS.includes(format)) throw invalid('Unknown signature format.')
        body.signature_format = format
      }
      const header = optionalText(signatureHeader, MAX_HEADER, 'Signature header')
      if (header !== undefined) body.signature_header = header
      if (active !== undefined) {
        if (typeof active !== 'boolean') throw invalid('Active must be a boolean.')
        body.active = active
      }
      return call(`/api/v1/functions/${functionId}/inbound-webhooks`, { method: 'POST', body, signal })
    },

    async updateInbound(functionId, id, { name, signatureFormat, signatureHeader, active, version, expected, signal }) {
      canonicalExposureOwner(functionId)
      canonicalExposureOwner(id, 'Trigger')
      const body = casTokens(version, expected)
      const next = optionalText(name, MAX_NAME, 'Name')
      if (next !== undefined) body.name = next
      const format = optionalText(signatureFormat, 64, 'Signature format')
      if (format !== undefined) {
        if (!SIGNATURE_FORMATS.includes(format)) throw invalid('Unknown signature format.')
        body.signature_format = format
      }
      const header = optionalText(signatureHeader, MAX_HEADER, 'Signature header')
      if (header !== undefined) body.signature_header = header
      if (active !== undefined) {
        if (typeof active !== 'boolean') throw invalid('Active must be a boolean.')
        body.active = active
      }
      return call(`/api/v1/functions/${functionId}/inbound-webhooks/${id}`, { method: 'PUT', body, signal })
    },

    async deleteInbound(functionId, id, { version, expected, signal }) {
      canonicalExposureOwner(functionId)
      canonicalExposureOwner(id, 'Trigger')
      return call(`/api/v1/functions/${functionId}/inbound-webhooks/${id}`, {
        method: 'DELETE',
        body: { ...casTokens(version, expected), confirm: true },
        signal,
      })
    },
  }
}

export const managedExposure = managedExposureAPI()
