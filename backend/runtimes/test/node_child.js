// Child half of node_sdk_channel.test.js: runs one scenario against the SDK
// and prints the outcome as JSON on stdout.
'use strict'
const orva = require('../node/orva.js')
const { kv } = orva

const errInfo = (e) => ({ name: e && e.name, status: e && e.status, message: e && e.message, current: e && e.currentValue })
const attempt = async (fn) => {
  try {
    return { ok: await fn() }
  } catch (e) {
    return { err: errInfo(e) }
  }
}

const scenarios = {
  async roundtrip() {
    await kv.put('a', { n: 1 }, { ttlSeconds: 5 })
    await kv.put('b', 'two')
    return {
      get: await kv.get('a'),
      miss: await kv.get('nope', 'dflt'),
      list: await kv.list({ prefix: '', limit: 10, cursor: 'c0' }),
      many: await kv.getMany(['a', 'zzz']),
      putMany: await kv.putMany([{ key: 'x', value: 1, ttlSeconds: 1 }, { key: 'y', value: 2 }]),
      delMany: await kv.deleteMany(['x', 'y', 'q']),
      incr: await kv.incr('ctr', 3),
      cas: await kv.cas('a', { n: 1 }, { n: 2 }),
      del: await kv.delete('b'),
      delMissing: await kv.delete('gone'),
    }
  },
  async errors() {
    return {
      getBusy: await attempt(() => kv.get('busy')),
      putBusy: await attempt(() => kv.put('busy', 1)),
      incrBad: await attempt(() => kv.incr('bad')),
      mismatch: await attempt(() => kv.cas('m', 1, 2)),
      isCasClass: (await attempt(() => kv.cas('m', 1, 2))).err.name === 'OrvaCASMismatch',
      batchErr: await attempt(() => kv.putMany([{ key: 'k', value: 1 }])),
    }
  },
  async many() {
    const res = await Promise.all(Array.from({ length: 200 }, (_, i) => kv.incr(`k${i}`, i)))
    return { res }
  },
  async eof() {
    const first = await attempt(() => kv.get('x'))
    const second = await attempt(() => kv.get('y'))
    return { first, second }
  },
  async big() {
    return await attempt(() => kv.put('k', 'x'.repeat(5 << 20)))
  },
  async badlen() {
    return await attempt(() => kv.get('k'))
  },
  async lease() {
    await kv.get('before')
    orva._setLease('L-1')
    await kv.get('after')
    orva._setLease('L-2')
    await kv.get('after2')
    return {}
  },
  async timeout() {
    orva._setChannelTimeout(150)
    return await attempt(() => kv.get('slow'))
  },
  async http() {
    // channel absent: legacy HTTP transport; non-kv ops stay on HTTP too.
    await kv.put('k', 'v')
    const v = await kv.get('k')
    const job = await orva.jobs.enqueue('fn2', { a: 1 })
    return { v, job }
  },
}

scenarios[process.argv[2]]().then(
  (r) => {
    process.stdout.write(JSON.stringify(r))
  },
  (e) => {
    process.stdout.write(JSON.stringify({ crashed: String(e && e.stack) }))
    process.exitCode = 1
  }
)
