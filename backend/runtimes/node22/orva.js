// Orva Node.js SDK — kv, invoke, jobs, crons, trace, log, context.
//
// Routes through ORVA_API_BASE (loopback) using ORVA_INTERNAL_TOKEN that
// was injected at worker spawn. Both env vars must be present in
// production; absent in tests where the SDK throws OrvaUnavailableError
// (unless __test_mode__ has supplied an override implementation).
//
// One-file design: no build step, no deps beyond the Node standard
// library. The shape mirrors the Python SDK byte-for-byte on the wire so
// parity tests can deploy the same payload across both runtimes.

'use strict'

// SDK version baked at adapter-embed time. Bumped in lockstep with the
// server. The string is sent on every internal-token call so operators
// can see drift in deployment logs.
const SDK_VERSION = '0.6.0'

const COMMON_HEADERS = { 'Content-Type': 'application/json' }

// ── Errors ──────────────────────────────────────────────────────────

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

class OrvaCASMismatch extends OrvaError {
  constructor(currentValue) {
    super('kv.cas: precondition failed', 409)
    this.name = 'OrvaCASMismatch'
    this.currentValue = currentValue
  }
}

// ── Test-mode hook ──────────────────────────────────────────────────
//
// __test_mode__ swaps the entire internal request implementation. Used
// by user tests that exercise their handler without standing up Orva.
// Pass `null` to restore the real transport.
let _testImpl = null
function __test_mode__(impl) {
  _testImpl = impl
}

// ── Environment accessors ───────────────────────────────────────────

const _apiBase = () => process.env.ORVA_API_BASE || ''
const _token = () => process.env.ORVA_INTERNAL_TOKEN || ''
const _fnID = () => process.env.ORVA_FUNCTION_ID || ''
const _execID = () => process.env.ORVA_EXECUTION_ID || ''
const _traceID = () => process.env.ORVA_TRACE_ID || ''
const _spanID = () => process.env.ORVA_SPAN_ID || ''
const _callDepth = () => parseInt(process.env.ORVA_CALL_DEPTH || '0', 10) || 0
const _timeoutMs = () => parseInt(process.env.ORVA_TIMEOUT_MS || '30000', 10) || 30000
const _memoryMb = () => parseInt(process.env.ORVA_MEMORY_MB || '64', 10) || 64

function _traceHeaders() {
  const h = {}
  const trace = _traceID()
  const span = _spanID()
  const fn = _fnID()
  const exec = _execID()
  if (trace) h['X-Orva-Trace-Id'] = trace
  if (span) h['X-Orva-Span-Id'] = span
  if (fn) h['X-Orva-Caller-Function'] = fn
  if (fn) h['X-Orva-Function-Id'] = fn
  if (exec) h['X-Orva-Execution-Id'] = exec
  h['X-Orva-SDK-Version'] = SDK_VERSION
  return h
}

// ── HTTP transport ──────────────────────────────────────────────────

const DEFAULT_TIMEOUT_MS = 30000

async function _request(method, path, opts = {}) {
  if (_testImpl && _testImpl.request) {
    return _testImpl.request(method, path, opts)
  }
  const base = _apiBase()
  const token = _token()
  if (!base || !token) {
    throw new OrvaUnavailableError(
      'Orva SDK not available (missing ORVA_API_BASE or ORVA_INTERNAL_TOKEN)'
    )
  }
  const timeoutMs = opts.timeoutMs ?? DEFAULT_TIMEOUT_MS
  const controller =
    typeof AbortController === 'function' ? new AbortController() : null
  const tid = controller
    ? setTimeout(() => controller.abort(new Error('timeout')), timeoutMs)
    : null
  const init = {
    method,
    headers: {
      'X-Orva-Internal-Token': token,
      ...COMMON_HEADERS,
      ..._traceHeaders(),
      ...(opts.headers || {}),
    },
    signal: controller ? controller.signal : undefined,
  }
  if (opts.body != null) {
    init.body =
      typeof opts.body === 'string' ? opts.body : JSON.stringify(opts.body)
  }
  let res
  try {
    res = await fetch(base + path, init)
  } catch (err) {
    if (tid) clearTimeout(tid)
    if (err && err.name === 'AbortError') {
      throw new OrvaError(`request timed out after ${timeoutMs}ms`, 0)
    }
    throw new OrvaError(`request failed: ${err.message || err}`, 0)
  }
  if (tid) clearTimeout(tid)
  const text = await res.text()
  return { status: res.status, body: text, headers: res.headers }
}

// ── KV ──────────────────────────────────────────────────────────────

const kv = {
  /** Read a JSON-decoded value. Returns defaultValue on missing/expired. */
  async get(key, defaultValue = null) {
    const fn = _fnID()
    const { status, body } = await _request(
      'GET',
      `/api/v1/_kv/${fn}/${encodeURIComponent(key)}`
    )
    if (status === 404) return defaultValue
    if (status >= 400) throw new OrvaError(`kv.get(${key}) failed: ${body}`, status)
    const data = JSON.parse(body)
    return data.value != null ? data.value : defaultValue
  },

  /** Upsert a value. ttlSeconds=0 disables expiry. */
  async put(key, value, { ttlSeconds = 0 } = {}) {
    const fn = _fnID()
    const { status, body } = await _request(
      'PUT',
      `/api/v1/_kv/${fn}/${encodeURIComponent(key)}`,
      { body: { value, ttl_seconds: ttlSeconds | 0 } }
    )
    if (status >= 400) throw new OrvaError(`kv.put(${key}) failed: ${body}`, status)
  },

  async delete(key) {
    const fn = _fnID()
    const { status, body } = await _request(
      'DELETE',
      `/api/v1/_kv/${fn}/${encodeURIComponent(key)}`
    )
    if (status >= 400 && status !== 404) {
      throw new OrvaError(`kv.delete(${key}) failed: ${body}`, status)
    }
  },

  /**
   * List entries. Pass `cursor` to resume from a previous page; the
   * response's nextCursor is the cursor for the page after this one
   * (empty string when there are no more rows).
   */
  async list({ prefix = '', limit = 100, cursor = '' } = {}) {
    const fn = _fnID()
    const qs = new URLSearchParams()
    qs.set('limit', String(limit))
    if (prefix) qs.set('prefix', prefix)
    if (cursor) qs.set('cursor', cursor)
    const { status, body } = await _request('GET', `/api/v1/_kv/${fn}?${qs}`)
    if (status >= 400) throw new OrvaError(`kv.list failed: ${body}`, status)
    const data = JSON.parse(body)
    return {
      keys: data.keys || [],
      nextCursor: data.next_cursor || '',
    }
  },

  /** Read N keys in one round trip. Missing keys map to null. */
  async getMany(keys) {
    if (!keys || keys.length === 0) return {}
    const fn = _fnID()
    const ops = keys.map((k) => ({ op: 'get', key: k }))
    const { status, body } = await _request('POST', `/api/v1/_kv/${fn}/batch`, {
      body: { ops },
    })
    if (status >= 400) throw new OrvaError(`kv.getMany failed: ${body}`, status)
    const data = JSON.parse(body)
    const out = {}
    for (const r of data.results || []) {
      out[r.key] = r.found ? r.value : null
    }
    return out
  },

  /** Write N entries in one transaction. */
  async putMany(entries) {
    if (!entries || entries.length === 0) return
    const fn = _fnID()
    const ops = entries.map((e) => ({
      op: 'put',
      key: e.key,
      value: e.value,
      ttl_seconds: (e.ttlSeconds | 0) || 0,
    }))
    const { status, body } = await _request('POST', `/api/v1/_kv/${fn}/batch`, {
      body: { ops },
    })
    if (status >= 400) throw new OrvaError(`kv.putMany failed: ${body}`, status)
  },

  /** Delete N keys in one transaction. Returns the number removed. */
  async deleteMany(keys) {
    if (!keys || keys.length === 0) return 0
    const fn = _fnID()
    const ops = keys.map((k) => ({ op: 'delete', key: k }))
    const { status, body } = await _request('POST', `/api/v1/_kv/${fn}/batch`, {
      body: { ops },
    })
    if (status >= 400) throw new OrvaError(`kv.deleteMany failed: ${body}`, status)
    const data = JSON.parse(body)
    return (data.results || []).filter((r) => r.found).length
  },

  /** Atomic increment. Missing keys are treated as 0. Returns new value. */
  async incr(key, delta = 1, { ttlSeconds = 0 } = {}) {
    const fn = _fnID()
    const { status, body } = await _request(
      'POST',
      `/api/v1/_kv/${fn}/${encodeURIComponent(key)}/incr`,
      { body: { delta, ttl_seconds: ttlSeconds | 0 } }
    )
    if (status >= 400) throw new OrvaError(`kv.incr(${key}) failed: ${body}`, status)
    return JSON.parse(body).value
  },

  /**
   * Compare-and-swap. `expected===null` means "key must not exist".
   * Returns true on success; on mismatch, throws OrvaCASMismatch carrying
   * the current value so callers can retry.
   */
  async cas(key, expected, newValue, { ttlSeconds = 0 } = {}) {
    const fn = _fnID()
    const { status, body } = await _request(
      'POST',
      `/api/v1/_kv/${fn}/${encodeURIComponent(key)}/cas`,
      {
        body: {
          expected: expected === null ? null : expected,
          new: newValue,
          ttl_seconds: ttlSeconds | 0,
        },
      }
    )
    if (status >= 400) throw new OrvaError(`kv.cas(${key}) failed: ${body}`, status)
    const data = JSON.parse(body)
    if (!data.ok) throw new OrvaCASMismatch(data.current ?? null)
    return true
  },
}

// ── Function-to-function invoke ─────────────────────────────────────

async function invoke(functionName, payload = {}, { timeoutMs = DEFAULT_TIMEOUT_MS } = {}) {
  const headers = {}
  const incoming = process.env.ORVA_CALL_DEPTH
  if (incoming) headers['X-Orva-Call-Depth'] = incoming

  const { status, body } = await _request(
    'POST',
    `/api/v1/_internal/invoke/${functionName}`,
    { body: payload, headers, timeoutMs }
  )

  if (status === 404) throw new OrvaError(`function not found: ${functionName}`, 404)
  if (status === 507) throw new OrvaError('call depth exceeded', 507)
  if (status >= 400) throw new OrvaError(`invoke(${functionName}) failed: ${body}`, status)

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

/**
 * Streaming variant. Returns an async iterable of Uint8Array chunks. The
 * server's chunked-transfer response is piped straight through; the
 * inner statusCode arrives via the response's HTTP status, inner headers
 * via X-Orva-Inner-* response headers (available on the iterable's
 * `.headers` after the first chunk).
 */
function invokeStream(functionName, payload = {}, { timeoutMs = DEFAULT_TIMEOUT_MS } = {}) {
  return {
    [Symbol.asyncIterator]() {
      let reader = null
      let started = false
      return {
        async next() {
          if (!started) {
            started = true
            const base = _apiBase()
            const token = _token()
            if (!base || !token) {
              throw new OrvaUnavailableError(
                'Orva SDK not available (missing ORVA_API_BASE or ORVA_INTERNAL_TOKEN)'
              )
            }
            const controller = new AbortController()
            const tid = setTimeout(() => controller.abort(new Error('timeout')), timeoutMs)
            let res
            try {
              res = await fetch(`${base}/api/v1/_internal/invoke/${functionName}/stream`, {
                method: 'POST',
                headers: {
                  'X-Orva-Internal-Token': token,
                  ...COMMON_HEADERS,
                  ..._traceHeaders(),
                  'X-Orva-Call-Depth': String(_callDepth()),
                },
                body: JSON.stringify(payload),
                signal: controller.signal,
              })
            } catch (err) {
              clearTimeout(tid)
              throw new OrvaError(`invokeStream(${functionName}) failed: ${err.message || err}`, 0)
            }
            clearTimeout(tid)
            if (res.status === 404) throw new OrvaError(`function not found: ${functionName}`, 404)
            if (res.status === 507) throw new OrvaError('call depth exceeded', 507)
            if (res.status >= 400) {
              const text = await res.text()
              throw new OrvaError(`invokeStream(${functionName}) failed: ${text}`, res.status)
            }
            reader = res.body.getReader()
          }
          const { value, done } = await reader.read()
          if (done) return { value: undefined, done: true }
          return { value, done: false }
        },
        async return() {
          if (reader) await reader.cancel().catch(() => {})
          return { value: undefined, done: true }
        },
      }
    },
  }
}

// ── Background jobs ─────────────────────────────────────────────────

const jobs = {
  async enqueue(functionName, payload = {}, opts = {}) {
    const bodyObj = {
      function_name: functionName,
      payload,
      max_attempts: (opts.maxAttempts | 0) || 3,
    }
    if (opts.scheduledAt) bodyObj.scheduled_at = opts.scheduledAt
    if (opts.idempotencyKey) bodyObj.idempotency_key = opts.idempotencyKey
    if (opts.idempotencyWindowSeconds) {
      bodyObj.idempotency_window_seconds = opts.idempotencyWindowSeconds | 0
    }

    const { status, body, headers } = await _request('POST', '/api/v1/jobs', { body: bodyObj })
    if (status >= 400) throw new OrvaError(`jobs.enqueue failed: ${body}`, status)
    const parsed = JSON.parse(body)
    const replayed =
      (headers && headers.get && headers.get('x-idempotency-replayed') === 'true') ||
      parsed.replayed === true
    return { id: parsed.id, replayed }
  },
}

// ── Cron-from-code ──────────────────────────────────────────────────

const crons = {
  /**
   * Idempotent upsert of a cron schedule attached to this function.
   * Identifies the row by (function_id, name) — subsequent calls with
   * the same name update the existing schedule in place.
   */
  async upsert(name, schedule, opts = {}) {
    const bodyObj = { name, schedule }
    if (opts.payload !== undefined) bodyObj.payload = opts.payload
    if (opts.timezone) bodyObj.timezone = opts.timezone
    if (opts.enabled !== undefined) bodyObj.enabled = !!opts.enabled
    const { status, body } = await _request('POST', '/api/v1/_internal/crons', {
      body: bodyObj,
    })
    if (status >= 400) throw new OrvaError(`crons.upsert(${name}) failed: ${body}`, status)
    return JSON.parse(body)
  },
}

// ── User-defined spans ──────────────────────────────────────────────

const trace = {
  /**
   * Wrap `fn` in a child span. The span's duration is the wall-clock
   * time of the awaited fn. Errors are recorded with status="error" and
   * rethrown unchanged so callers can choose their own handling.
   */
  async span(name, fn, attrs = undefined) {
    const startedAt = new Date()
    const t0 = Date.now()
    let ok = true
    let errMsg = ''
    try {
      return await fn()
    } catch (e) {
      ok = false
      errMsg = e && e.message ? e.message : String(e)
      throw e
    } finally {
      const durationMs = Date.now() - t0
      // Fire-and-forget — never block the user's code waiting on span
      // ingestion. We deliberately swallow errors so a flaky loopback
      // never breaks the handler.
      _request('POST', '/api/v1/_internal/spans', {
        body: {
          name,
          started_at: startedAt.toISOString(),
          duration_ms: durationMs,
          status: ok ? 'ok' : 'error',
          error_message: errMsg,
          attributes: attrs,
        },
      }).catch(() => {})
    }
  },
}

// ── Structured logging ──────────────────────────────────────────────

function _emitLog(level, msg, fields) {
  const rec = {
    ts: new Date().toISOString(),
    level,
    message: typeof msg === 'string' ? msg : JSON.stringify(msg),
  }
  if (fields && typeof fields === 'object') rec.fields = fields
  const span = _spanID()
  if (span) rec.span_id = span
  // Magic-prefix line on stderr — parsed by the server in proxy.Forward.
  // Wrapped in a try since process.stderr.write can fail mid-shutdown.
  try {
    process.stderr.write('__ORVA_LOG_JSON__' + JSON.stringify(rec) + '\n')
  } catch {
    // ignore
  }
}

const log = {
  debug(msg, fields) { _emitLog('debug', msg, fields) },
  info(msg, fields) { _emitLog('info', msg, fields) },
  warn(msg, fields) { _emitLog('warn', msg, fields) },
  error(msg, fields) { _emitLog('error', msg, fields) },
}

// ── Secrets accessor ────────────────────────────────────────────────
//
// Currently a thin wrapper over process.env. The indirection lets us add
// per-secret access auditing later without changing user code.

const secrets = {
  get(name) {
    return process.env[name]
  },
}

// ── Webhook helper ──────────────────────────────────────────────────
//
// Pure local parsing of the event Orva passes to a function whose entry
// is an inbound webhook trigger. The server has already verified the
// HMAC before calling the handler — verified is always true here unless
// the function is called directly without a webhook trigger.

function _firstHeader(event, ...names) {
  if (!event || !event.headers) return ''
  for (const n of names) {
    const v =
      event.headers[n] ??
      event.headers[n.toLowerCase()] ??
      event.headers[n.toUpperCase()]
    if (v) return v
  }
  return ''
}

const webhook = {
  parse(event) {
    const headers = event && event.headers ? event.headers : {}
    const trigger = _firstHeader(event, 'x-orva-trigger')
    const webhookId = _firstHeader(event, 'x-orva-inbound-webhook-id')
    let source = 'unknown'
    let eventType = ''
    if (_firstHeader(event, 'X-GitHub-Event')) {
      source = 'github'
      eventType = _firstHeader(event, 'X-GitHub-Event')
    } else if (_firstHeader(event, 'Stripe-Signature')) {
      source = 'stripe'
      eventType = _firstHeader(event, 'Stripe-Event-Type')
    } else if (_firstHeader(event, 'X-Slack-Signature')) {
      source = 'slack'
    } else if (
      _firstHeader(event, 'X-Hub-Signature-256') ||
      _firstHeader(event, 'X-Signature')
    ) {
      source = 'hmac'
    }
    let payload = event && event.body !== undefined ? event.body : null
    if (typeof payload === 'string' && payload.length > 0) {
      try {
        payload = JSON.parse(payload)
      } catch {
        // leave as string
      }
    }
    return {
      verified: trigger === 'inbound_webhook',
      source,
      eventType,
      webhookId,
      payload,
      headers,
    }
  },
}

// ── Context (env-var snapshot exposed as an object) ─────────────────
//
// Lazy getters so tests can mutate process.env at runtime without
// recomputing the SDK.

const context = {
  get functionId() { return _fnID() },
  get executionId() { return _execID() },
  get traceId() { return _traceID() },
  get spanId() { return _spanID() },
  get callDepth() { return _callDepth() },
  get timeoutMs() { return _timeoutMs() },
  get memoryMb() { return _memoryMb() },
  get sdkVersion() { return SDK_VERSION },
}

module.exports = {
  kv,
  invoke,
  invokeStream,
  jobs,
  crons,
  trace,
  log,
  secrets,
  webhook,
  context,
  OrvaError,
  OrvaUnavailableError,
  OrvaCASMismatch,
  __test_mode__,
  SDK_VERSION,
}
