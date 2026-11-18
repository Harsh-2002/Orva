/* global ReadableStream */
import { describe, expect, it } from 'vitest'
import { STREAM_LENGTHS, useScratchStream } from '@/scratch/useScratchStream.js'

const encoder = new TextEncoder()
// A response whose body the test feeds by hand, so timing is deterministic.
function feed() {
  let controller
  const body = new ReadableStream({ start(c) { controller = c } })
  return {
    send: (text) => controller.enqueue(encoder.encode(text)),
    close: () => controller.close(),
    response: (extra = {}) => ({
      ok: true,
      body,
      headers: { get: (k) => ({ 'X-Orva-Execution-ID': 'exec-1', 'X-Scratch-Worker': 'w-1', 'X-Scratch-Artifact': 'a-1' })[k] || null },
      ...extra,
    }),
  }
}
const flush = () => new Promise((resolve) => setTimeout(resolve, 0))
const input = { callerId: 'caller-id', callerLabel: 'scratch-node', targetLabel: 'scratch-python' }
const head = (kind) => `${JSON.stringify({ kind, context: { functionID: 'callee-id', execution: 'e2', trace: 't1', depth: '1' } })}\n`

describe('scratch stream client', () => {
  it('posts the stream mode to the caller, tracks heads and completes only on the end marker', async () => {
    const f = feed()
    let seen
    const s = useScratchStream(async (url, options) => { seen = { url, options }; return f.response() })
    const run = s.start({ ...input, length: 'long' })
    await flush()
    expect(seen.url).toBe('/fn/caller-id/stream')
    expect(JSON.parse(seen.options.body)).toEqual({ mode: 'stream-call', target: 'scratch-python', ...STREAM_LENGTHS.long })
    expect(s.state.phase).toBe('streaming')
    expect(s.state).toMatchObject({ execution: 'exec-1', worker: 'w-1', artifact: 'a-1' })
    f.send(head('caller') + head('callee'))
    for (let i = 1; i <= 8; i += 1) f.send(`chunk-${String(i).padStart(3, '0')}\n`)
    await flush()
    expect(s.state.chunks).toBe(8)
    expect(s.state.recent).toHaveLength(6)
    expect(s.state.recent.at(-1)).toBe('chunk-008')
    expect(s.state.callee).toEqual({ functionID: 'callee-id', execution: 'e2', trace: 't1', depth: '1' })
    expect(s.state.phase).toBe('streaming')
    f.send('end\n')
    f.close()
    await run
    expect(s.state.phase).toBe('completed')
  })

  it('reassembles lines split across reads', async () => {
    const f = feed()
    const s = useScratchStream(async () => f.response())
    const run = s.start(input)
    await flush()
    f.send('chun')
    f.send('k-001\nchunk-0')
    f.send('02\nend\n')
    f.close()
    await run
    expect(s.state.chunks).toBe(2)
    expect(s.state.phase).toBe('completed')
  })

  it('keeps delivered chunks on denial and applies nothing after the marker', async () => {
    const f = feed()
    const s = useScratchStream(async () => f.response())
    const run = s.start(input)
    await flush()
    f.send('chunk-001\nchunk-002\nerror:IAM_DENIED\nchunk-003\nend\n')
    f.close()
    await run
    expect(s.state.phase).toBe('denied')
    expect(s.state.chunks).toBe(2)
  })

  it('treats a close without a marker, a generic error marker and a bad response as failures', async () => {
    for (const body of ['chunk-001\n', 'error:CALL_ERROR\n']) {
      const f = feed()
      const s = useScratchStream(async () => f.response())
      const run = s.start(input)
      await flush()
      f.send(body)
      f.close()
      await run
      expect(s.state.phase).toBe('failed')
    }
    const s = useScratchStream(async () => ({ ok: false, status: 403, body: null, headers: { get: () => null } }))
    await s.start(input)
    expect(s.state.phase).toBe('failed')
    const rejected = useScratchStream(async () => { throw new Error('network') })
    await rejected.start(input)
    expect(rejected.state.phase).toBe('failed')
  })

  it('stops by aborting the request and ignores later bytes', async () => {
    const f = feed()
    let signal
    const s = useScratchStream(async (_url, options) => { signal = options.signal; return f.response() })
    const run = s.start(input)
    await flush()
    f.send('chunk-001\n')
    await flush()
    s.stop()
    expect(s.state.phase).toBe('stopped')
    expect(signal.aborted).toBe(true)
    f.send('chunk-002\n')
    await Promise.race([run, flush()])
    expect(s.state.chunks).toBe(1)
    expect(s.state.phase).toBe('stopped')
    expect(s.active.value).toBe(false)
  })

  it('ignores a second start while active and discards a stale stream after clearing', async () => {
    const first = feed()
    const calls = []
    const s = useScratchStream(async (url) => { calls.push(url); return first.response() })
    const run = s.start(input)
    await flush()
    await s.start({ ...input, callerId: 'other' })
    expect(calls).toEqual(['/fn/caller-id/stream'])
    s.stop()
    s.dismiss()
    expect(s.state.phase).toBe('idle')
    first.send('chunk-009\nend\n')
    await Promise.race([run, flush()])
    expect(s.state.chunks).toBe(0)
    expect(s.state.phase).toBe('idle')
  })

  it('dispose aborts a running stream without leaving a new phase behind', async () => {
    const f = feed()
    let signal
    const s = useScratchStream(async (_url, options) => { signal = options.signal; return f.response() })
    const run = s.start(input)
    await flush()
    s.dispose()
    expect(signal.aborted).toBe(true)
    await Promise.race([run, flush()])
    expect(s.state.phase).toBe('streaming')
  })
})
