import { computed, reactive } from 'vue'

// Scratch-only client for the fixture's streaming route. It renders what the
// public response actually delivered and never decides authorization: a denial
// is whatever the server sent, and bytes received before it stay listed.
const RECENT = 6
const MAX_LINE = 512

export const STREAM_LENGTHS = Object.freeze({
  short: { count: 5, delayMs: 50 },
  long: { count: 40, delayMs: 300 },
})

const blank = () => ({
  phase: 'idle', // idle | connecting | streaming | completed | denied | stopped | failed
  callerId: '',
  callerLabel: '',
  targetLabel: '',
  chunks: 0,
  expected: 0,
  recent: [],
  execution: '',
  worker: '',
  artifact: '',
  callee: null,
})

export function useScratchStream(fetchImpl = (...args) => fetch(...args)) {
  const state = reactive(blank())
  let token = 0
  let controller = null

  const active = computed(() => state.phase === 'connecting' || state.phase === 'streaming')

  function reset(next) {
    Object.assign(state, blank(), next)
  }

  function absorb(line, mine) {
    // Nothing after a terminal marker or a Stop is applied.
    if (mine !== token || !active.value) return
    if (line.startsWith('{')) {
      let head
      try { head = JSON.parse(line) } catch { return }
      if (head?.kind === 'callee' && head.context) {
        const c = head.context
        state.callee = { functionID: c.functionID, execution: c.execution, trace: c.trace, depth: c.depth }
      }
      return
    }
    if (/^chunk-\d+$/.test(line)) {
      state.chunks += 1
      state.recent = [...state.recent, line.slice(0, MAX_LINE)].slice(-RECENT)
      return
    }
    if (line === 'end') state.phase = 'completed'
    else if (line === 'error:IAM_DENIED') state.phase = 'denied'
    else if (line.startsWith('error:')) state.phase = 'failed'
  }

  async function start({ callerId, callerLabel, targetLabel, length = 'short' }) {
    if (active.value) return
    const mine = ++token
    const shape = STREAM_LENGTHS[length] || STREAM_LENGTHS.short
    reset({ phase: 'connecting', callerId, callerLabel, targetLabel, expected: shape.count })
    controller = new AbortController()
    try {
      const response = await fetchImpl(`/fn/${callerId}/stream`, {
        method: 'POST',
        signal: controller.signal,
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ mode: 'stream-call', target: targetLabel, ...shape }),
      })
      if (mine !== token) return
      state.execution = response.headers.get('X-Orva-Execution-ID') || ''
      state.worker = response.headers.get('X-Scratch-Worker') || ''
      state.artifact = response.headers.get('X-Scratch-Artifact') || ''
      if (!response.ok || !response.body) {
        state.phase = 'failed'
        return
      }
      state.phase = 'streaming'
      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let pending = ''
      for (;;) {
        const { value, done } = await reader.read()
        if (mine !== token) return
        if (done) break
        pending += decoder.decode(value, { stream: true })
        let at
        while ((at = pending.indexOf('\n')) >= 0) {
          absorb(pending.slice(0, at), mine)
          pending = pending.slice(at + 1)
        }
      }
      if (mine === token && state.phase === 'streaming') state.phase = 'failed'
    } catch {
      if (mine === token) state.phase = state.phase === 'stopped' ? 'stopped' : 'failed'
    } finally {
      if (mine === token) controller = null
    }
  }

  // Stop closes the client connection; the server then cancels the callee.
  function stop() {
    if (!active.value) return
    state.phase = 'stopped'
    controller?.abort()
  }

  function dismiss() {
    if (active.value) return
    token += 1
    reset({})
  }

  function dispose() {
    token += 1
    controller?.abort()
    controller = null
  }

  return { state, active, start, stop, dismiss, dispose }
}
