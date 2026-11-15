// SDK channel tests for backend/runtimes/node/orva.js. Run: node --test backend/runtimes/test/
// The parent plays the host: the child gets a real socketpair as fd 3.
'use strict'
const test = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const http = require('node:http')
const path = require('node:path')
const { spawn } = require('node:child_process')

const CHILD = path.join(__dirname, 'node_child.js')

// Run a scenario. `host(msg, reply, ctx)` is called per request frame.
function run(scenario, host, { env = {}, channel = true, onSocket } = {}) {
  return new Promise((resolve, reject) => {
    const e = { ...process.env }
    delete e.ORVA_SDK_FD
    delete e.ORVA_SDK_PROTO
    if (channel) Object.assign(e, { ORVA_SDK_FD: '3', ORVA_SDK_PROTO: '1' })
    Object.assign(e, env)
    const child = spawn(process.execPath, [CHILD, scenario], {
      env: e,
      stdio: ['ignore', 'pipe', 'inherit', 'pipe'],
    })
    const sock = child.stdio[3]
    const frames = []
    const ctx = { frames, sock, child }
    const reply = (id, r) => send({ id, ok: true, r })
    const fail = (id, status, code, message) => send({ id, ok: false, e: { status, code, message } })
    const send = (obj) => {
      const body = Buffer.from(JSON.stringify(obj))
      const hdr = Buffer.alloc(4)
      hdr.writeUInt32BE(body.length)
      if (!sock.destroyed) sock.write(Buffer.concat([hdr, body]))
    }
    ctx.reply = reply
    ctx.fail = fail
    ctx.send = send
    let buf = Buffer.alloc(0)
    sock.on('data', (c) => {
      buf = Buffer.concat([buf, c])
      while (buf.length >= 4 && buf.length >= 4 + buf.readUInt32BE(0)) {
        const n = buf.readUInt32BE(0)
        const msg = JSON.parse(buf.subarray(4, 4 + n).toString())
        buf = buf.subarray(4 + n)
        frames.push(msg)
        host(msg, ctx)
      }
    })
    sock.on('error', () => {})
    if (onSocket) onSocket(ctx)
    let out = ''
    child.stdout.on('data', (d) => (out += d))
    child.on('error', reject)
    child.on('close', () => {
      try {
        resolve({ out: JSON.parse(out), frames })
      } catch (err) {
        reject(new Error(`child output not JSON: ${out}`))
      }
    })
  })
}

// A tiny in-memory KV host.
function kvHost() {
  const store = new Map()
  return (msg, ctx) => {
    const a = msg.a || {}
    switch (msg.op) {
      case 'kv.get':
        if (!store.has(a.key)) return ctx.fail(msg.id, 404, 'NOT_FOUND', 'no such key')
        return ctx.reply(msg.id, { value: store.get(a.key) })
      case 'kv.put':
        store.set(a.key, a.value)
        return ctx.reply(msg.id, {})
      case 'kv.delete':
        if (!store.delete(a.key)) return ctx.fail(msg.id, 404, 'NOT_FOUND', 'no such key')
        return ctx.reply(msg.id, {})
      case 'kv.list':
        return ctx.reply(msg.id, { keys: [...store.keys()].map((key) => ({ key })), next_cursor: `after:${a.cursor}` })
      case 'kv.batch':
        return ctx.reply(msg.id, {
          results: a.ops.map((o) => {
            if (o.op === 'put') {
              store.set(o.key, o.value)
              return { op: 'put', key: o.key, found: true }
            }
            if (o.op === 'delete') return { op: 'delete', key: o.key, found: store.delete(o.key) }
            return { op: 'get', key: o.key, found: store.has(o.key), value: store.get(o.key) }
          }),
        })
      case 'kv.incr': {
        const v = (store.get(a.key) || 0) + a.delta
        store.set(a.key, v)
        return ctx.reply(msg.id, { value: v })
      }
      case 'kv.cas': {
        const cur = store.has(a.key) ? store.get(a.key) : null
        if (JSON.stringify(cur) !== JSON.stringify(a.expected)) return ctx.reply(msg.id, { ok: false, current: cur })
        store.set(a.key, a.new)
        return ctx.reply(msg.id, { ok: true })
      }
    }
    return ctx.fail(msg.id, 400, 'UNKNOWN_OP', msg.op)
  }
}

test('kv round trips over the channel, with no identity or lease in frames', async () => {
  const { out, frames } = await run('roundtrip', kvHost())
  assert.deepEqual(out.get, { n: 1 })
  assert.equal(out.miss, 'dflt')
  assert.deepEqual(out.list, { keys: [{ key: 'a' }, { key: 'b' }], nextCursor: 'after:c0' })
  assert.deepEqual(out.many, { a: { n: 1 }, zzz: null })
  assert.equal(out.delMany, 2)
  assert.equal(out.incr, 3)
  assert.equal(out.cas, true)
  const ops = frames.map((f) => f.op)
  assert.deepEqual([...new Set(ops)].sort(), ['kv.batch', 'kv.cas', 'kv.delete', 'kv.get', 'kv.incr', 'kv.list', 'kv.put'])
  const put = frames.find((f) => f.op === 'kv.put')
  assert.deepEqual(put.a, { key: 'a', value: { n: 1 }, ttl_seconds: 5 })
  for (const f of frames) {
    assert.deepEqual(Object.keys(f).filter((k) => !['id', 'op', 'a'].includes(k)), [])
    for (const k of ['function_id', 'execution_id', 'trace_id', 'token', 'depth']) assert.ok(!(k in f.a))
  }
})

test('error mapping preserves public SDK statuses', async () => {
  const host = (msg, ctx) => {
    const k = msg.a.key
    if (k === 'busy') return ctx.fail(msg.id, 429, 'TOO_MANY_INFLIGHT', 'slow down')
    if (k === 'bad') return ctx.fail(msg.id, 400, 'VALIDATION', 'bad delta')
    if (msg.op === 'kv.cas') return ctx.reply(msg.id, { ok: false, current: 'cur' })
    if (msg.op === 'kv.batch') return ctx.reply(msg.id, { results: [{ key: 'k', error: 'boom' }] })
    return ctx.fail(msg.id, 500, 'X', 'x')
  }
  const { out } = await run('errors', host)
  assert.equal(out.getBusy.err.name, 'OrvaError')
  assert.equal(out.getBusy.err.status, 429)
  assert.equal(out.putBusy.err.status, 429)
  assert.equal(out.incrBad.err.status, 400)
  assert.equal(out.mismatch.err.name, 'OrvaCASMismatch')
  assert.equal(out.mismatch.err.status, 409)
  assert.equal(out.mismatch.err.current, 'cur')
  assert.equal(out.isCasClass, true)
  assert.equal(out.batchErr.err.status, 500)
})

test('200 concurrent calls with out-of-order replies; never more than 32 outstanding', async () => {
  let outstanding = 0
  let max = 0
  const held = []
  let timer = null
  const flush = (ctx) => {
    held.reverse() // answer in the opposite order of arrival
    while (held.length) {
      const m = held.pop()
      outstanding--
      ctx.reply(m.id, { value: m.a.delta * 10 })
    }
  }
  const host = (msg, ctx) => {
    outstanding++
    max = Math.max(max, outstanding)
    held.push(msg)
    clearTimeout(timer)
    if (held.length >= 7) flush(ctx)
    else timer = setTimeout(() => flush(ctx), 5)
  }
  const { out, frames } = await run('many', host)
  assert.equal(frames.length, 200)
  assert.ok(max <= 32, `max outstanding ${max}`)
  assert.deepEqual(out.res, Array.from({ length: 200 }, (_, i) => i * 10))
})

test('more than 32 outstanding are queued client-side, not rejected', async () => {
  let held = []
  let released = false
  let seenAtRelease = 0
  const host = (msg, ctx) => {
    if (released) return ctx.reply(msg.id, { value: 1 })
    held.push(msg)
    if (held.length === 32) {
      setTimeout(() => {
        seenAtRelease = ctx.frames.length // no 33rd frame while all 32 unanswered
        released = true
        for (const m of held) ctx.reply(m.id, { value: 1 })
        held = []
      }, 150)
    }
  }
  const { out, frames } = await run('many', host)
  assert.equal(seenAtRelease, 32)
  assert.equal(frames.length, 200)
  assert.equal(out.res.length, 200)
})

test('channel EOF rejects pending and later calls with OrvaUnavailableError', async () => {
  const host = (msg, ctx) => {
    if (msg.a.key === 'x') ctx.sock.destroy()
  }
  const { out } = await run('eof', host)
  assert.equal(out.first.err.name, 'OrvaUnavailableError')
  assert.equal(out.second.err.name, 'OrvaUnavailableError')
})

test('oversized request is refused before anything is written', async () => {
  const { out, frames } = await run('big', () => {})
  assert.equal(out.err.name, 'OrvaError')
  assert.match(out.err.message, /too large/)
  assert.equal(frames.length, 0)
})

test('a multi-megabyte reply arriving in small chunks is reassembled', async () => {
  const host = (msg, ctx) => {
    const body = Buffer.from(JSON.stringify({ id: msg.id, ok: true, r: { value: 'y'.repeat(3 << 20) } }))
    const frame = Buffer.concat([Buffer.from([body.length >>> 24, (body.length >>> 16) & 255, (body.length >>> 8) & 255, body.length & 255]), body])
    let off = 0
    const pump = () => {
      if (off >= frame.length) return
      ctx.sock.write(frame.subarray(off, off + 7919))
      off += 7919
      setImmediate(pump)
    }
    pump()
  }
  const { out } = await run('bigreply', host)
  assert.equal(out.len, 3 << 20)
})

test('reply length above 4 MiB closes the channel', async () => {
  const host = (msg, ctx) => {
    const hdr = Buffer.alloc(4)
    hdr.writeUInt32BE((4 << 20) + 1)
    ctx.sock.write(hdr)
  }
  const { out } = await run('badlen', host)
  assert.equal(out.err.name, 'OrvaUnavailableError')
})

test('a call that never gets a reply times out with OrvaError status 0', async () => {
  const { out } = await run('timeout', () => {})
  assert.equal(out.err.name, 'OrvaError')
  assert.equal(out.err.status, 0)
  assert.match(out.err.message, /timed out/)
})

test('lease is absent before the first lease and attached after', async () => {
  const { frames } = await run('lease', kvHost())
  assert.deepEqual(frames.map((f) => f.lease), [undefined, 'L-1', 'L-2'])
})

function httpSink() {
  const seen = []
  const server = http.createServer((req, res) => {
    seen.push(`${req.method} ${req.url}`)
    req.resume()
    res.end('{}')
  })
  return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve({ server, seen, port: server.address().port })))
}

for (const [name, env] of [
  ['SDK permission disabled (channel env absent)', {}],
  ['protocol missing', { ORVA_SDK_FD: '3' }],
  ['protocol unsupported', { ORVA_SDK_FD: '3', ORVA_SDK_PROTO: '2' }],
  ['descriptor missing', { ORVA_SDK_PROTO: '1' }],
  ['descriptor invalid', { ORVA_SDK_FD: 'bad', ORVA_SDK_PROTO: '1' }],
  ['descriptor negative', { ORVA_SDK_FD: '-1', ORVA_SDK_PROTO: '1' }],
  ['descriptor is standard I/O', { ORVA_SDK_FD: '1', ORVA_SDK_PROTO: '1' }],
  ['descriptor not a socket', { ORVA_TEST_NONSOCKET: '1', ORVA_SDK_PROTO: '1' }],
  ['descriptor closed', { ORVA_SDK_FD: '999999', ORVA_SDK_PROTO: '1' }],
  ['descriptor out of range', { ORVA_SDK_FD: '9'.repeat(100), ORVA_SDK_PROTO: '1' }],
]) {
  test(`all operations fail closed without HTTP: ${name}`, async () => {
    const h = await httpSink()
    try {
      const { out, frames } = await run('unavailable', () => assert.fail('channel used'), {
        channel: false,
        env: { ...env, ORVA_API_BASE: `http://127.0.0.1:${h.port}`, ORVA_INTERNAL_TOKEN: 'tok', ORVA_FUNCTION_ID: 'f1' },
      })
      assert.equal(Object.keys(out.errors).length, 13)
      for (const [op, result] of Object.entries(out.errors)) {
        assert.equal(result.err?.name, 'OrvaUnavailableError', op)
        assert.equal(result.err.status, 0, op)
        assert.match(result.err.message, /sdk permission.*refresh.*adapter/i, op)
      }
      assert.equal(out.spanRet, 7)
      assert.equal(out.spanError.err.message, 'user failure')
      assert.equal(frames.length, 0)
      assert.deepEqual(h.seen, [])
    } finally {
      h.server.close()
    }
  })
}

test('explicit test-mode override remains local and reset restores fail-closed behavior', async () => {
  const { out, frames } = await run('testHook', () => assert.fail('channel used'), {
    channel: false, env: { ORVA_FUNCTION_ID: 'f1' },
  })
  assert.equal(out.value, 'mock')
  assert.deepEqual(out.seen, [{ method: 'GET', path: '/api/v1/_kv/f1/a%2Fb', opts: {} }])
  assert.equal(out.restored.err.name, 'OrvaUnavailableError')
  assert.deepEqual(frames, [])
})

// Host for the PR2 ops. `state` collects what the host saw.
function opsHost(state = {}) {
  state.handles = new Map()
  state.n = 0
  const chunk = (i) => Buffer.from(`c${i}`).toString('base64')
  return (msg, ctx) => {
    const a = msg.a || {}
    switch (msg.op) {
      case 'jobs.enqueue':
        if (a.function_name === 'bad') return ctx.fail(msg.id, 400, 'VALIDATION', 'bad job')
        return ctx.reply(msg.id, { id: 'job9', replayed: a.idempotency_key === 'k' })
      case 'crons.upsert':
        if (a.schedule === 'bad') return ctx.fail(msg.id, 403, 'SDK_SCOPE_VIOLATION', 'no')
        return ctx.reply(msg.id, { id: 'cron1', name: a.name })
      case 'spans.emit':
        if (a.name === 's') return ctx.fail(msg.id, 403, 'SDK_SCOPE_VIOLATION', 'no')
        return ctx.reply(msg.id, {})
      case 'invoke':
        if (a.name === 'missing') return ctx.fail(msg.id, 404, 'NOT_FOUND', 'function not found')
        if (a.name === 'deep') return ctx.fail(msg.id, 507, 'MAX_CALL_DEPTH', 'too deep')
        if (a.name === 'busy') return ctx.fail(msg.id, 429, 'FUNCTION_BUSY', 'busy')
        if (a.name === 'plain') return ctx.reply(msg.id, { statusCode: 200, headers: {}, body: 'not json' })
        return ctx.reply(msg.id, { statusCode: 200, headers: { a: 'b' }, body: '{"echo":' + JSON.stringify(a.body.q) + '}' })
      case 'invoke.stream.open': {
        if (a.name === 'missing') return ctx.fail(msg.id, 404, 'NOT_FOUND', 'function not found')
        const handle = `h${++state.n}`
        state.handles.set(handle, { name: a.name, i: 0 })
        return ctx.reply(msg.id, { handle, status: 200, headers: {} })
      }
      case 'invoke.stream.next': {
        const st = state.handles.get(a.handle)
        if (!st) return ctx.fail(msg.id, 404, 'NOT_FOUND', 'no such handle')
        st.i++
        if (st.name === 'mid' && st.i === 3) {
          state.handles.delete(a.handle)
          return ctx.fail(msg.id, 502, 'INVOKE_FAILED', 'callee died')
        }
        if (st.name === 'five' && st.i === 3 && state.eofAtThree) return ctx.sock.destroy()
        if (st.i <= 5) return ctx.reply(msg.id, { chunk_b64: chunk(st.i), done: false })
        state.handles.delete(a.handle)
        return ctx.reply(msg.id, { chunk_b64: '', done: true })
      }
      case 'invoke.stream.close':
        state.closed = (state.closed || []).concat(a.handle)
        state.handles.delete(a.handle)
        return ctx.reply(msg.id, {})
    }
    return ctx.fail(msg.id, 400, 'UNKNOWN_OP', msg.op)
  }
}

const noIdentity = (frames) => {
  for (const f of frames) {
    assert.deepEqual(Object.keys(f).filter((k) => !['id', 'op', 'a', 'lease'].includes(k)), [])
    for (const k of ['function_id', 'execution_id', 'trace_id', 'span_id', 'token', 'depth', 'call_depth', 'headers']) {
      assert.ok(!(k in f.a), `identity field ${k} in ${f.op}`)
    }
  }
}

const IDENT_ENV = { ORVA_TRACE_ID: 'tr1', ORVA_SPAN_ID: 'sp1', ORVA_EXECUTION_ID: 'ex1', ORVA_CALL_DEPTH: '2', ORVA_FUNCTION_ID: 'f1' }

test('jobs, crons, spans and invoke go over the channel with no identity', async () => {
  const { out, frames } = await run('opsOk', opsHost(), { env: IDENT_ENV })
  assert.deepEqual(out.job, { id: 'job9', replayed: true })
  assert.deepEqual(out.cron, { id: 'cron1', name: 'nightly' })
  assert.equal(out.spanRet, 7)
  assert.deepEqual(out.inv, { statusCode: 200, headers: { a: 'b' }, body: { echo: 1 } })
  assert.equal(out.invString.body, 'not json')
  assert.deepEqual(frames.map((f) => f.op), ['jobs.enqueue', 'crons.upsert', 'spans.emit', 'invoke', 'invoke'])
  assert.deepEqual(frames[0].a, { function_name: 'fn2', payload: { a: 1 }, max_attempts: 5, idempotency_key: 'k', idempotency_window_seconds: 60 })
  assert.deepEqual(frames[1].a, { name: 'nightly', schedule: '0 3 * * *', payload: { x: 1 }, timezone: 'UTC', enabled: true })
  assert.equal(frames[2].a.name, 'work')
  assert.equal(frames[2].a.status, 'ok')
  assert.deepEqual(frames[2].a.attributes, { a: 1 })
  assert.equal(typeof frames[2].a.duration_ms, 'number')
  assert.deepEqual(frames[3].a, { name: 'callee', body: { q: 1 } })
  assert.deepEqual(frames[4].a, { name: 'plain', body: {} })
  assert.ok(frames.every((f) => f.lease === 'L-1'))
  noIdentity(frames)
})

test('op errors preserve public OrvaError statuses', async () => {
  const { out } = await run('opsErrors', opsHost(), { env: IDENT_ENV })
  assert.equal(out.notFound.err.status, 404)
  assert.match(out.notFound.err.message, /function not found: missing/)
  assert.equal(out.depth.err.status, 507)
  assert.equal(out.depth.err.message, 'call depth exceeded')
  assert.equal(out.busy.err.status, 429)
  assert.equal(out.job.err.status, 400)
  assert.equal(out.job.err.name, 'OrvaError')
  assert.equal(out.cron.err.status, 403)
  assert.equal(out.spanSwallowed, 'fine') // span ingestion errors never reach user code
})

test('invokeStream: 5 chunks, early break closes, mid-stream error, open error', async () => {
  const state = {}
  const { out, frames } = await run('streams', opsHost(state), { env: IDENT_ENV })
  assert.deepEqual(out.full, ['c1|Uint8Array', 'c2|Uint8Array', 'c3|Uint8Array', 'c4|Uint8Array', 'c5|Uint8Array'])
  assert.deepEqual(out.early, ['c1|Uint8Array', 'c2|Uint8Array'])
  assert.deepEqual(out.mid, ['c1|Uint8Array', 'c2|Uint8Array'])
  assert.equal(out.midErr.name, 'OrvaError')
  assert.equal(out.midErr.status, 502)
  assert.equal(out.openErr.err.status, 404)
  assert.match(out.openErr.err.message, /function not found: missing/)
  // h1 finished on its own, h2 was closed by the early break, h3 failed (host already closed it).
  assert.deepEqual(state.closed, ['h2'])
  assert.deepEqual(
    frames.map((f) => (f.op === 'invoke.stream.next' || f.op === 'invoke.stream.close' ? `${f.op}:${f.a.handle}` : f.op)),
    [
      'invoke.stream.open', ...Array(6).fill('invoke.stream.next:h1'),
      'invoke.stream.open', 'invoke.stream.next:h2', 'invoke.stream.next:h2', 'invoke.stream.close:h2',
      'invoke.stream.open', 'invoke.stream.next:h3', 'invoke.stream.next:h3', 'invoke.stream.next:h3',
      'invoke.stream.open',
    ]
  )
  assert.deepEqual(frames[0].a, { name: 'five', body: { p: 1 } })
  assert.deepEqual(frames[1].a, { handle: 'h1' })
  noIdentity(frames)
})

test('channel EOF during a stream raises OrvaUnavailableError', async () => {
  const state = { eofAtThree: true }
  const { out } = await run('streamEof', opsHost(state), { env: IDENT_ENV })
  assert.deepEqual(out.got, ['c1', 'c2'])
  assert.equal(out.res.err.name, 'OrvaUnavailableError')
})

test('adapters carry the orva-sdk-proto:1 marker near the top', () => {
  for (const f of ['node/adapter.js', 'python/adapter.py']) {
    const head = fs.readFileSync(path.join(__dirname, '..', f), 'utf8').split('\n').slice(0, 40).join('\n')
    assert.match(head, /orva-sdk-proto:1/, f)
  }
})
