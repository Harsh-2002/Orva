// Orva Node.js SDK — kv, invoke, jobs, crons, trace, log, context.
//
// Uses only the inherited worker SDK channel. Without it, operations throw
// OrvaUnavailableError (unless __test_mode__ supplies an implementation).
//
// One-file design: no build step, no deps beyond the Node standard
// library. The shape mirrors the Python SDK byte-for-byte on the wire so
// parity tests can deploy the same payload across both runtimes.

'use strict'

// SDK version baked at adapter-embed time.
const SDK_VERSION = '0.7.0'

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
    super(`${message}. Check the function's sdk permission and refresh the runtime adapter.`, 0)
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

const _fnID = () => process.env.ORVA_FUNCTION_ID || ''
const _execID = () => process.env.ORVA_EXECUTION_ID || ''
const _traceID = () => process.env.ORVA_TRACE_ID || ''
const _spanID = () => process.env.ORVA_SPAN_ID || ''
const _callDepth = () => parseInt(process.env.ORVA_CALL_DEPTH || '0', 10) || 0
const _timeoutMs = () => parseInt(process.env.ORVA_TIMEOUT_MS || '30000', 10) || 30000
const _memoryMb = () => parseInt(process.env.ORVA_MEMORY_MB || '64', 10) || 64

const DEFAULT_TIMEOUT_MS = 30000

// ── SDK channel ─────────────────────────────────────────────────────
//
// A private AF_UNIX socketpair the host passes to the worker as fd
// ORVA_SDK_FD (with ORVA_SDK_PROTO=1). Frames are [u32 BE length][JSON];
// requests {id, op, lease?, a}, replies {id, ok, r | e:{status,code,message}}.
// The client never sends identity: the host derives it from the connection
// and the lease. Missing or invalid channel configuration fails closed.

const _net = require('net')
const _fs = require('fs')

const MAX_FRAME = 4 << 20 // 4 MiB, both directions
const MAX_INFLIGHT = 32 // host refuses the 33rd with 429, so queue instead

// The adapter stores the per-dispatch lease here (it does not import the SDK).
const _LEASE_KEY = Symbol.for('orva.sdk.lease')
const _currentLease = () => globalThis[_LEASE_KEY] || ''
function _setLease(lease) {
  globalThis[_LEASE_KEY] = typeof lease === 'string' ? lease : ''
}

// Per-call deadline includes queue wait.
let _channelTimeoutMs = DEFAULT_TIMEOUT_MS
function _setChannelTimeout(ms) {
  _channelTimeoutMs = ms
}

class _Channel {
  constructor(fd) {
    this.sock = new _net.Socket({ fd, readable: true, writable: true })
    this.inflight = new Map() // id -> entry; timed-out entries stay until the host replies
    this.queue = []
    this.nextId = 1
    this.dead = null
    this.buf = Buffer.alloc(0)
    this.parts = []
    this.have = 0
    this.need = 0
    this.sock.on('data', (chunk) => this._onData(chunk))
    this.sock.on('error', (err) => this._fail(new OrvaUnavailableError(`SDK channel error: ${err.message || err}`)))
    this.sock.on('close', () => this._fail(new OrvaUnavailableError('SDK channel closed')))
    this.sock.on('end', () => this._fail(new OrvaUnavailableError('SDK channel closed')))
    this.sock.unref() // only keeps the process alive while calls are pending
  }

  call(op, args, timeoutMs) {
    const limitMs = timeoutMs ?? _channelTimeoutMs
    if (this.dead) return Promise.reject(this.dead)
    const id = this.nextId++
    const msg = { id, op, a: args }
    const lease = _currentLease()
    if (lease) msg.lease = lease
    const body = Buffer.from(JSON.stringify(msg), 'utf8')
    if (body.length > MAX_FRAME) {
      return Promise.reject(new OrvaError(`${op}: request too large (${body.length} bytes, max ${MAX_FRAME})`, 413))
    }
    const hdr = Buffer.allocUnsafe(4)
    hdr.writeUInt32BE(body.length, 0)
    return new Promise((resolve, reject) => {
      const entry = { id, op, frame: Buffer.concat([hdr, body]), resolve, reject, settled: false, timer: null }
      entry.timer = setTimeout(() => {
        if (entry.settled) return
        entry.settled = true
        const qi = this.queue.indexOf(entry)
        if (qi >= 0) this.queue.splice(qi, 1)
        // A sent request keeps its in-flight slot until the host answers it.
        reject(new OrvaError(`request timed out after ${limitMs}ms`, 0))
        this._pump()
        this._refresh()
      }, limitMs)
      this.queue.push(entry)
      this._pump()
      this._refresh()
    })
  }

  _pump() {
    while (!this.dead && this.queue.length > 0 && this.inflight.size < MAX_INFLIGHT) {
      const entry = this.queue.shift()
      this.inflight.set(entry.id, entry)
      this.sock.write(entry.frame, (err) => {
        if (err) this._fail(new OrvaUnavailableError(`SDK channel write failed: ${err.message || err}`))
      })
    }
  }

  _refresh() {
    // Abandoned (timed-out) requests still hold a slot but must not keep the
    // process alive.
    let waiting = this.queue.length > 0
    for (const entry of this.inflight.values()) if (!entry.settled) waiting = true
    if (waiting) this.sock.ref()
    else this.sock.unref()
  }

  _onData(chunk) {
    // Large frames arrive in many chunks: collect them and join once the whole
    // frame is here, instead of re-copying the accumulated bytes per chunk.
    this.parts.push(chunk)
    this.have += chunk.length
    if (this.need && this.have < this.need) return
    this.buf = this.parts.length === 1 ? this.parts[0] : Buffer.concat(this.parts, this.have)
    this.parts = []
    this.have = 0
    this.need = 0
    for (;;) {
      if (this.buf.length < 4) break
      const n = this.buf.readUInt32BE(0)
      if (n < 1 || n > MAX_FRAME) {
        this._fail(new OrvaUnavailableError(`SDK channel protocol error: bad frame length ${n}`))
        return
      }
      if (this.buf.length < 4 + n) {
        this.need = 4 + n
        break
      }
      let msg
      try {
        msg = JSON.parse(this.buf.subarray(4, 4 + n).toString('utf8'))
      } catch {
        this._fail(new OrvaUnavailableError('SDK channel protocol error: invalid JSON'))
        return
      }
      this.buf = this.buf.subarray(4 + n)
      this._onReply(msg)
    }
    if (this.buf.length) {
      this.parts.push(this.buf)
      this.have = this.buf.length
    }
    this.buf = Buffer.alloc(0)
  }

  _onReply(msg) {
    const entry = msg && this.inflight.get(msg.id)
    if (!entry) return
    this.inflight.delete(msg.id)
    if (!entry.settled) {
      entry.settled = true
      clearTimeout(entry.timer)
      if (msg.ok) {
        entry.resolve({ status: 200, data: msg.r === undefined ? {} : msg.r, body: '' })
      } else {
        const e = msg.e || {}
        entry.resolve({
          status: Number(e.status) || 500,
          body: `${e.code || 'ERROR'}: ${e.message || ''}`,
        })
      }
    }
    this._pump()
    this._refresh()
  }

  _fail(err) {
    if (this.dead) return
    this.dead = err
    const all = [...this.inflight.values(), ...this.queue]
    this.inflight.clear()
    this.queue = []
    for (const entry of all) {
      if (entry.settled) continue
      entry.settled = true
      clearTimeout(entry.timer)
      entry.reject(err)
    }
    try { this.sock.destroy() } catch { /* already closed */ }
  }
}

let _chan // undefined = not probed yet, null = not available, else _Channel

// Returns the open channel, a dead one (calls then reject with
// OrvaUnavailableError), or null when this worker has none.
function _channel() {
  if (_chan !== undefined) return _chan
  _chan = null
  if (process.env.ORVA_SDK_PROTO !== '1') return _chan
  const raw = process.env.ORVA_SDK_FD || ''
  if (!/^\d+$/.test(raw)) return _chan
  const fd = parseInt(raw, 10)
  if (!Number.isSafeInteger(fd) || fd < 3) return _chan
  try {
    if (!_fs.fstatSync(fd).isSocket()) return _chan
    _chan = new _Channel(fd)
  } catch {
    _chan = null
  }
  return _chan
}

// ── KV ──────────────────────────────────────────────────────────────
//
// Every kv call goes through the private SDK channel, without identity fields:
//   kv.get {key}                       -> {value, expires_at?}   (404 = missing)
//   kv.put {key, value, ttl_seconds?}  -> {}
//   kv.delete {key}                    -> {}                     (404 ignored)
//   kv.list {prefix?, limit, cursor?}  -> {keys, next_cursor}
//   kv.batch {ops}                     -> {results}
//   kv.incr {key, delta, ttl_seconds?} -> {value}
//   kv.cas {key, expected, new, ttl_seconds?} -> {ok, current?}

async function _kv(op, args, method, path, opts) {
  // Method/path metadata exists only for the public unit-test hook.
  if (_testImpl && _testImpl.request) {
    return _testImpl.request(method, path, opts || {})
  }
  return _requireChannel().call(op, args, opts && opts.timeoutMs)
}

function _requireChannel() {
  const ch = _channel()
  if (!ch) throw new OrvaUnavailableError('Orva SDK channel unavailable (missing or invalid descriptor/protocol)')
  return ch
}

// Parsed JSON of a channel or test-hook reply.
function _data(res) {
  return res.data !== undefined ? res.data : JSON.parse(res.body)
}

const kv = {
  /** Read a JSON-decoded value. Returns defaultValue on missing/expired. */
  async get(key, defaultValue = null) {
    const fn = _fnID()
    const res = await _kv('kv.get', { key }, 'GET', `/api/v1/_kv/${fn}/${encodeURIComponent(key)}`)
    if (res.status === 404) return defaultValue
    if (res.status >= 400) throw new OrvaError(`kv.get(${key}) failed: ${res.body}`, res.status)
    const data = _data(res)
    return data.value != null ? data.value : defaultValue
  },

  /** Upsert a value. Omit ttlSeconds to preserve expiry; 0 clears it. */
  async put(key, value, options = {}) {
    const fn = _fnID()
    const requestBody = { value }
    if (Object.hasOwn(options, 'ttlSeconds')) {
      requestBody.ttl_seconds = Number(options.ttlSeconds)
    }
    const res = await _kv(
      'kv.put',
      { key, ...requestBody },
      'PUT',
      `/api/v1/_kv/${fn}/${encodeURIComponent(key)}`,
      { body: requestBody }
    )
    if (res.status >= 400) throw new OrvaError(`kv.put(${key}) failed: ${res.body}`, res.status)
  },

  async delete(key) {
    const fn = _fnID()
    const res = await _kv('kv.delete', { key }, 'DELETE', `/api/v1/_kv/${fn}/${encodeURIComponent(key)}`)
    if (res.status >= 400 && res.status !== 404) {
      throw new OrvaError(`kv.delete(${key}) failed: ${res.body}`, res.status)
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
    const args = { limit: Number(limit) }
    if (prefix) args.prefix = prefix
    if (cursor) args.cursor = cursor
    const res = await _kv('kv.list', args, 'GET', `/api/v1/_kv/${fn}?${qs}`)
    if (res.status >= 400) throw new OrvaError(`kv.list failed: ${res.body}`, res.status)
    const data = _data(res)
    return {
      keys: data.keys || [],
      nextCursor: data.next_cursor || '',
    }
  },

  /** Read N keys in one round trip. Missing keys map to null. */
  async getMany(keys) {
    if (!keys || keys.length === 0) return {}
    const ops = keys.map((k) => ({ op: 'get', key: k }))
    const res = await _kvBatch(ops)
    if (res.status >= 400) throw new OrvaError(`kv.getMany failed: ${res.body}`, res.status)
    const data = _data(res)
    _assertAtomicBatch(data, 'kv.getMany')
    const out = {}
    for (const r of data.results || []) {
      out[r.key] = r.found ? r.value : null
    }
    return out
  },

  /** Write N entries in one transaction. */
  async putMany(entries) {
    if (!entries || entries.length === 0) return
    const ops = entries.map((e) => {
      const op = { op: 'put', key: e.key, value: e.value }
      if (Object.hasOwn(e, 'ttlSeconds')) op.ttl_seconds = Number(e.ttlSeconds)
      return op
    })
    const res = await _kvBatch(ops)
    if (res.status >= 400) throw new OrvaError(`kv.putMany failed: ${res.body}`, res.status)
    _assertAtomicBatch(_data(res), 'kv.putMany')
  },

  /** Delete N keys in one transaction. Returns the number removed. */
  async deleteMany(keys) {
    if (!keys || keys.length === 0) return 0
    const ops = keys.map((k) => ({ op: 'delete', key: k }))
    const res = await _kvBatch(ops)
    if (res.status >= 400) throw new OrvaError(`kv.deleteMany failed: ${res.body}`, res.status)
    const data = _data(res)
    _assertAtomicBatch(data, 'kv.deleteMany')
    return (data.results || []).filter((r) => r.found).length
  },

  /** Atomic increment. Missing keys are treated as 0. Returns new value. */
  async incr(key, delta = 1, options = {}) {
    const fn = _fnID()
    const requestBody = { delta }
    if (Object.hasOwn(options, 'ttlSeconds')) {
      requestBody.ttl_seconds = Number(options.ttlSeconds)
    }
    const res = await _kv(
      'kv.incr',
      { key, ...requestBody },
      'POST',
      `/api/v1/_kv/${fn}/${encodeURIComponent(key)}/incr`,
      { body: requestBody }
    )
    if (res.status >= 400) throw new OrvaError(`kv.incr(${key}) failed: ${res.body}`, res.status)
    return _data(res).value
  },

  /**
   * Compare-and-swap. `expected===null` means "key must not exist".
   * Returns true on success; on mismatch, throws OrvaCASMismatch carrying
   * the current value so callers can retry.
   */
  async cas(key, expected, newValue, options = {}) {
    const fn = _fnID()
    const requestBody = {
      expected: expected === null ? null : expected,
      new: newValue,
    }
    if (Object.hasOwn(options, 'ttlSeconds')) {
      requestBody.ttl_seconds = Number(options.ttlSeconds)
    }
    const res = await _kv(
      'kv.cas',
      { key, ...requestBody },
      'POST',
      `/api/v1/_kv/${fn}/${encodeURIComponent(key)}/cas`,
      { body: requestBody }
    )
    if (res.status >= 400) throw new OrvaError(`kv.cas(${key}) failed: ${res.body}`, res.status)
    const data = _data(res)
    if (!data.ok) throw new OrvaCASMismatch(data.current ?? null)
    return true
  },
}

function _kvBatch(ops) {
  return _kv('kv.batch', { ops }, 'POST', `/api/v1/_kv/${_fnID()}/batch`, { body: { ops } })
}

function _assertAtomicBatch(data, operation) {
  const failed = (data.results || []).find((result) => result.error)
  if (failed) {
    throw new OrvaError(`${operation} failed: ${failed.error}`, 500)
  }
}

// ── Function-to-function invoke ─────────────────────────────────────

async function invoke(functionName, payload = {}, { timeoutMs = DEFAULT_TIMEOUT_MS } = {}) {
  // Over the channel the host derives trace, span and depth itself: no headers.
  const headers = {}
  const incoming = process.env.ORVA_CALL_DEPTH
  if (incoming) headers['X-Orva-Call-Depth'] = incoming

  const res = await _kv(
    'invoke',
    { name: functionName, body: payload },
    'POST',
    `/api/v1/_internal/invoke/${functionName}`,
    { body: payload, headers, timeoutMs }
  )
  const { status, body } = res

  if (status === 404) throw new OrvaError(`function not found: ${functionName}`, 404)
  if (status === 507) throw new OrvaError('call depth exceeded', 507)
  if (status >= 400) throw new OrvaError(`invoke(${functionName}) failed: ${body}`, status)

  const env = _data(res)
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
 * inherited channel supplies one response chunk per next call.
 */
function invokeStream(functionName, payload = {}, { timeoutMs = DEFAULT_TIMEOUT_MS } = {}) {
  return {
    [Symbol.asyncIterator]() {
      let started = false
      let handle = null // channel stream handle while the host holds it open
      let finished = false
      const closeHandle = async () => {
        const h = handle
        handle = null
        if (h === null) return
        await _requireChannel().call('invoke.stream.close', { handle: h }, timeoutMs).catch(() => {})
      }
      // A channel reply is one chunk per call; empty non-final chunks are skipped.
      const nextFromChannel = async () => {
        const ch = _requireChannel()
        for (;;) {
          if (finished || handle === null) return { value: undefined, done: true }
          let res
          try {
            res = await ch.call('invoke.stream.next', { handle }, timeoutMs)
          } catch (err) {
            await closeHandle()
            finished = true
            throw err
          }
          if (res.status >= 400) {
            // The host has already closed the handle after a mid-stream failure.
            handle = null
            finished = true
            throw new OrvaError(`invokeStream(${functionName}) failed: ${res.body}`, res.status)
          }
          const data = _data(res)
          const b64 = data.chunk_b64 || ''
          if (data.done) {
            handle = null
            finished = true
          }
          if (b64) return { value: new Uint8Array(Buffer.from(b64, 'base64')), done: false }
          if (data.done) return { value: undefined, done: true }
        }
      }
      return {
        async next() {
          if (finished) return { value: undefined, done: true }
          if (!started) {
            started = true
            const ch = _requireChannel()
            const res = await ch.call('invoke.stream.open', { name: functionName, body: payload }, timeoutMs)
            if (res.status === 404) throw new OrvaError(`function not found: ${functionName}`, 404)
            if (res.status === 507) throw new OrvaError('call depth exceeded', 507)
            if (res.status >= 400) {
              throw new OrvaError(`invokeStream(${functionName}) failed: ${res.body}`, res.status)
            }
            handle = _data(res).handle
            if (finished) {
              await closeHandle()
              return { value: undefined, done: true }
            }
          }
          return nextFromChannel()
        },
        async return() {
          finished = true
          await closeHandle()
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

    const res = await _kv('jobs.enqueue', bodyObj, 'POST', '/api/v1/jobs', { body: bodyObj })
    const { status, body, headers } = res
    if (status >= 400) throw new OrvaError(`jobs.enqueue failed: ${body}`, status)
    const parsed = _data(res)
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
    const res = await _kv('crons.upsert', bodyObj, 'POST', '/api/v1/_internal/crons', {
      body: bodyObj,
    })
    if (res.status >= 400) throw new OrvaError(`crons.upsert(${name}) failed: ${res.body}`, res.status)
    return _data(res)
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
      // ingestion. We deliberately swallow errors so an unavailable channel
      // never breaks the handler.
      const spanBody = {
        name,
        started_at: startedAt.toISOString(),
        duration_ms: durationMs,
        status: ok ? 'ok' : 'error',
        error_message: errMsg,
        attributes: attrs,
      }
      _kv('spans.emit', spanBody, 'POST', '/api/v1/_internal/spans', { body: spanBody }).catch(() => {})
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
  _setLease,
  _setChannelTimeout,
}
