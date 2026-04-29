// Orva Node.js SDK — kv, invoke, jobs.
//
// Routes through ORVA_API_BASE (loopback) using ORVA_INTERNAL_TOKEN that
// was injected at worker spawn. Both env vars must be present in
// production; absent in tests where the SDK throws OrvaUnavailableError.
//
// Usage:
//   const { kv, invoke, jobs } = require('orva')
//   await kv.put('count', 42, { ttlSeconds: 60 })
//   const n = await kv.get('count', 0)
//   const result = await invoke('resize-image', { url: '...' })
//   await jobs.enqueue('send-welcome-email', { to: 'ada@example.com' })

class OrvaError extends Error {
  constructor(message, status = 0) {
    super(message)
    this.name = 'OrvaError'
    this.status = status
  }
}

class OrvaUnavailableError extends OrvaError {
  constructor(message) {
    super(message, 0)
    this.name = 'OrvaUnavailableError'
  }
}

const _apiBase = () => process.env.ORVA_API_BASE || ''
const _token   = () => process.env.ORVA_INTERNAL_TOKEN || ''
const _fnID    = () => process.env.ORVA_FUNCTION_ID || ''

async function _request(method, path, { body = null, headers = {} } = {}) {
  const base  = _apiBase()
  const token = _token()
  if (!base || !token) {
    throw new OrvaUnavailableError(
      'Orva SDK not available (missing ORVA_API_BASE or ORVA_INTERNAL_TOKEN)'
    )
  }
  const init = {
    method,
    headers: {
      'X-Orva-Internal-Token': token,
      'Content-Type': 'application/json',
      ...headers,
    },
  }
  if (body != null) {
    init.body = typeof body === 'string' ? body : JSON.stringify(body)
  }
  const res  = await fetch(base + path, init)
  const text = await res.text()
  return { status: res.status, body: text }
}

// ── KV ──────────────────────────────────────────────────────────────

const kv = {
  /**
   * Read a value previously stored with kv.put. Returns the default
   * when the key is missing or expired. Values are JSON-decoded.
   */
  async get(key, defaultValue = null) {
    const fn = _fnID()
    const { status, body } = await _request('GET', `/api/v1/_kv/${fn}/${key}`)
    if (status === 404) return defaultValue
    if (status >= 400)  throw new OrvaError(`kv.get(${key}) failed: ${body}`, status)
    const data = JSON.parse(body)
    return data.value != null ? JSON.parse(data.value) : defaultValue
  },

  /**
   * Store a value. value is JSON-encoded. opts.ttlSeconds=0 (default)
   * means no expiry; positive means expire after that many seconds.
   */
  async put(key, value, { ttlSeconds = 0 } = {}) {
    const fn = _fnID()
    const payload = { value: JSON.stringify(value), ttl_seconds: ttlSeconds | 0 }
    const { status, body } = await _request('PUT', `/api/v1/_kv/${fn}/${key}`, { body: payload })
    if (status >= 400) throw new OrvaError(`kv.put(${key}) failed: ${body}`, status)
  },

  async delete(key) {
    const fn = _fnID()
    const { status, body } = await _request('DELETE', `/api/v1/_kv/${fn}/${key}`)
    if (status >= 400 && status !== 404) {
      throw new OrvaError(`kv.delete(${key}) failed: ${body}`, status)
    }
  },

  async list({ prefix = '', limit = 100 } = {}) {
    const fn  = _fnID()
    const qs  = new URLSearchParams()
    qs.set('limit', String(limit))
    if (prefix) qs.set('prefix', prefix)
    const { status, body } = await _request('GET', `/api/v1/_kv/${fn}?${qs}`)
    if (status >= 400) throw new OrvaError(`kv.list failed: ${body}`, status)
    const data = JSON.parse(body)
    // value is already a JSON object on the wire; no second parse needed.
    return data.keys || []
  },
}

// ── Function-to-function invoke ─────────────────────────────────────

/**
 * Invoke another Orva function by friendly name. Returns the parsed
 * {statusCode, headers, body} envelope. body is JSON-decoded when
 * possible; remains a string otherwise.
 */
async function invoke(functionName, payload = {}) {
  const headers = {}
  // Forward inbound call depth so nested invokes hit the cap.
  const incoming = process.env.ORVA_CALL_DEPTH
  if (incoming) headers['X-Orva-Call-Depth'] = incoming

  const { status, body } = await _request(
    'POST',
    `/api/v1/_internal/invoke/${functionName}`,
    { body: payload, headers }
  )

  if (status === 404) throw new OrvaError(`function not found: ${functionName}`, 404)
  if (status === 507) throw new OrvaError('call depth exceeded', 507)
  if (status >= 400)  throw new OrvaError(`invoke(${functionName}) failed: ${body}`, status)

  const env = JSON.parse(body)
  if (typeof env.body === 'string') {
    try {
      env.body = JSON.parse(env.body)
    } catch {
      // leave as string
    }
  }
  return env
}

// ── Background jobs ─────────────────────────────────────────────────

const jobs = {
  /** Enqueue a fire-and-forget job. Returns the job id. */
  async enqueue(functionName, payload = {}, { maxAttempts = 3, scheduledAt = null } = {}) {
    const bodyObj = {
      function_name: functionName,
      payload,
      max_attempts: maxAttempts | 0,
    }
    if (scheduledAt) bodyObj.scheduled_at = scheduledAt

    const { status, body } = await _request('POST', '/api/v1/jobs', { body: bodyObj })
    if (status >= 400) throw new OrvaError(`jobs.enqueue failed: ${body}`, status)
    return JSON.parse(body).id
  },
}

module.exports = { kv, invoke, jobs, OrvaError, OrvaUnavailableError }
