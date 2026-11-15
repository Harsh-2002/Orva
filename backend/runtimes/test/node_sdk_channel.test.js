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
    const e = { ...process.env, ...env }
    delete e.ORVA_SDK_FD
    delete e.ORVA_SDK_PROTO
    if (channel) Object.assign(e, { ORVA_SDK_FD: '3', ORVA_SDK_PROTO: '1' })
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

test('error mapping matches the HTTP path', async () => {
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

function httpHost() {
  const seen = []
  const server = http.createServer((req, res) => {
    let b = ''
    req.on('data', (d) => (b += d))
    req.on('end', () => {
      seen.push({ method: req.method, url: req.url, token: req.headers['x-orva-internal-token'], body: b })
      res.setHeader('content-type', 'application/json')
      if (req.url.startsWith('/api/v1/jobs')) return res.end(JSON.stringify({ id: 'job1' }))
      if (req.method === 'GET') return res.end(JSON.stringify({ value: 'v' }))
      res.end('{}')
    })
  })
  return new Promise((resolve) => server.listen(0, '127.0.0.1', () => resolve({ server, seen, port: server.address().port })))
}

for (const [name, channel, env] of [
  ['env absent', false, {}],
  ['fd set but ORVA_SDK_PROTO unset', false, { ORVA_SDK_FD: '3' }],
]) {
  test(`falls back to HTTP when ${name}`, async () => {
    const h = await httpHost()
    try {
      const { out, frames } = await run('http', () => assert.fail('channel used'), {
        channel,
        env: { ...env, ORVA_API_BASE: `http://127.0.0.1:${h.port}`, ORVA_INTERNAL_TOKEN: 'tok', ORVA_FUNCTION_ID: 'f1' },
      })
      assert.equal(out.v, 'v')
      assert.equal(frames.length, 0)
      assert.deepEqual(h.seen.slice(0, 2).map((s) => `${s.method} ${s.url}`), ['PUT /api/v1/_kv/f1/k', 'GET /api/v1/_kv/f1/k'])
      assert.equal(h.seen[0].token, 'tok')
    } finally {
      h.server.close()
    }
  })
}

test('non-kv ops stay on HTTP even when the channel is open', async () => {
  const h = await httpHost()
  try {
    const { out, frames } = await run('http', kvHost(), {
      env: { ORVA_API_BASE: `http://127.0.0.1:${h.port}`, ORVA_INTERNAL_TOKEN: 'tok', ORVA_FUNCTION_ID: 'f1' },
    })
    assert.equal(out.job.id, 'job1')
    assert.deepEqual(h.seen.map((s) => s.url), ['/api/v1/jobs'])
    assert.deepEqual(frames.map((f) => f.op), ['kv.put', 'kv.get'])
  } finally {
    h.server.close()
  }
})

test('adapters carry the orva-sdk-proto:1 marker near the top', () => {
  for (const f of ['node/adapter.js', 'python/adapter.py']) {
    const head = fs.readFileSync(path.join(__dirname, '..', f), 'utf8').split('\n').slice(0, 40).join('\n')
    assert.match(head, /orva-sdk-proto:1/, f)
  }
})
