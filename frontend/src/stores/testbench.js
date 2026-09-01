// Test-bench state, keyed by function id.
//
// The request outlives whichever surface is showing it: the editor's result
// strip and /functions/:name/test are two views onto one request, so running
// from either and then switching does not reset what you typed. Keyed by
// function id rather than held in a component, because the editor is inside a
// <keep-alive> and would otherwise carry one function's request onto another.
import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  invokeFunctionFull, getInvocationLogs,
  listFixtures, updateFixture, deleteFixture,
} from '@/api/endpoints'
import { getApiKey } from '@/api/client'

export const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']

const blankRequest = () => ({
  method: 'POST',
  path: '/',
  headers: [],
  body: '{"name": "World"}',
})

// A run is kept whole so the workbench can show history: comparing this run
// against the previous one is the thing a single `output` ref made impossible.
const blankRun = () => ({
  status: '',
  durationMs: '',
  body: '',
  error: '',
  failed: false,
  executionId: '',
  stderr: [],
  structured: [],
  logsState: 'idle',   // idle | loading | loaded | error | no-execution
  logsError: '',
  logsPromise: null,
  at: null,
})

const headersObject = (req) => {
  const out = {}
  for (const h of req.headers || []) {
    const k = (h.key || '').trim()
    if (k) out[k] = h.value ?? ''
  }
  return out
}

// A JSON body with no Content-Type is the most common way a first test 415s.
const withContentType = (headers, method, body) => {
  if (method === 'GET' || method === 'DELETE') return headers
  const has = Object.keys(headers).some((k) => k.toLowerCase() === 'content-type')
  if (!has && (body || '').trim()) headers['Content-Type'] = 'application/json'
  return headers
}

// POSIX single-quoting: everything inside is literal, and a quote of its own is
// spliced in as '\''. A body holding an apostrophe used to be the whole risk.
const shellQuote = (s) => `'${String(s ?? '').replace(/'/g, "'\\''")}'`

// Never the operator's real key. A copied command is one paste away from an
// issue or a chat, and the docs' own examples carry the same placeholder.
// auth_mode 'none' gets no header at all: it is not needed, so it is noise.
const AUTH_HEADERS = {
  platform_key: ['X-Orva-API-Key: <YOUR_KEY>'],
  signed: ['X-Orva-Timestamp: <UNIX_SECONDS>', 'X-Orva-Signature: sha256=<HMAC_SHA256_OF_TIMESTAMP.BODY>'],
}

// The same request the Run button sends, as a runnable curl command. It calls
// withContentType rather than restating the rule, so the two cannot drift.
export const buildCurlCommand = ({ url = '', request = {}, authMode = 'none' } = {}) => {
  const method = (request.method || 'POST').toUpperCase()
  const body = request.body || ''
  const own = headersObject(request)
  const named = new Set(Object.keys(own).map((k) => k.toLowerCase()))

  const lines = [`curl -X ${method} ${shellQuote(url)}`]
  for (const h of AUTH_HEADERS[authMode] || []) {
    if (!named.has(h.slice(0, h.indexOf(':')).toLowerCase())) lines.push(`-H ${shellQuote(h)}`)
  }
  for (const [k, v] of Object.entries(withContentType(own, method, body))) {
    lines.push(`-H ${shellQuote(`${k}: ${v}`)}`)
  }
  // endpoints.js withholds a body from GET and HEAD, so the command must too.
  // --data-raw, never --data: a body starting with @ makes --data read a local
  // file instead of sending the text, which the Run button never does.
  if (body && method !== 'GET' && method !== 'HEAD') lines.push(`--data-raw ${shellQuote(body)}`)
  return lines.join(' \\\n  ')
}

export const useTestbenchStore = defineStore('testbench', () => {
  const requests = ref({})   // fnId -> request
  const runs = ref({})       // fnId -> run[] (newest first)
  const fixtures = ref({})   // fnId -> fixture[]
  const invoking = ref(false)

  const requestFor = (fnId) => {
    if (!fnId) return blankRequest()
    if (!requests.value[fnId]) requests.value[fnId] = blankRequest()
    return requests.value[fnId]
  }

  const runsFor = (fnId) => runs.value[fnId] || []
  const latestRun = (fnId) => runsFor(fnId)[0] || null
  const fixturesFor = (fnId) => fixtures.value[fnId] || []

  const setRequest = (fnId, patch) => {
    if (!fnId) return
    requests.value[fnId] = { ...requestFor(fnId), ...patch }
  }

  // Both log paths come from the execution record, which is the only place
  // either one exists: console.log/print are rerouted to stderr by the
  // adapters, and orva.log.* is parsed server-side out of that same stderr
  // into log_entries. The editor could never show either, because it never
  // read X-Orva-Execution-ID off the invoke response.
  // The server writes the log row with AsyncInsertExecutionLog, so it is
  // routinely not there yet when the invoke response lands: fetching once made a
  // handler that logged four lines report that it had logged nothing. Poll
  // briefly, then accept the empty answer as real.
  const LOG_ATTEMPTS = 12
  const LOG_INTERVAL_MS = 150

  const loadLogs = async (run) => {
    if (!run.executionId) {
      run.logsState = 'no-execution'
      return
    }
    run.logsState = 'loading'
    for (let i = 0; i < LOG_ATTEMPTS; i++) {
      try {
        const { data } = await getInvocationLogs(run.executionId, { quiet404: true })
        const raw = data?.stderr || ''
        run.stderr = raw ? raw.split('\n').filter((l) => l !== '') : []
        run.structured = data?.log_entries || []
        // An empty answer is NOT proof the handler was silent: a 200 can arrive
        // with nothing while the write is still in flight. Measured on a 2ms run
        // -- it reported "logged nothing" for a handler that logged three lines,
        // while the same handler at 4ms rendered them. Keep asking.
        if (run.stderr.length || run.structured.length) {
          run.logsState = 'loaded'
          return
        }
      } catch (err) {
        // 404 is the documented shape for "no logs row yet".
        if (err?.response?.status !== 404) {
          run.logsState = 'error'
          run.logsError = err?.response?.data?.error?.message || err?.message || 'could not read the execution log'
          return
        }
      }
      if (i < LOG_ATTEMPTS - 1) await new Promise((r) => setTimeout(r, LOG_INTERVAL_MS))
    }
    run.logsState = 'loaded'
  }

  const invoke = async (fnId) => {
    if (!fnId) return null
    const req = requestFor(fnId)
    const run = blankRun()
    invoking.value = true
    try {
      const headers = withContentType(headersObject(req), req.method, req.body)
      headers['X-Orva-API-Key'] = headers['X-Orva-API-Key'] || getApiKey()

      const res = await invokeFunctionFull(fnId, {
        method: req.method,
        path: req.path || '/',
        headers,
        body: req.body || '',
      })
      const text = typeof res.data === 'string' ? res.data : JSON.stringify(res.data)
      run.status = `${res.status}`
      run.durationMs = res.headers?.['x-orva-duration-ms'] || ''
      run.executionId = res.headers?.['x-orva-execution-id'] || ''
      try { run.body = JSON.stringify(JSON.parse(text), null, 2) } catch { run.body = text }
      // Only 5xx is a bug to debug; a deliberate 401 from an authz check is not.
      run.failed = typeof res.status === 'number' && res.status >= 500
    } catch (err) {
      if (err.response) {
        run.status = `${err.response.status}`
        run.durationMs = err.response.headers?.['x-orva-duration-ms'] || ''
        run.executionId = err.response.headers?.['x-orva-execution-id'] || ''
        // Formatted like the success body: this is the one an operator reads.
        const t = err.response.data
        const raw = typeof t === 'string' ? t : JSON.stringify(t)
        try { run.error = JSON.stringify(JSON.parse(raw), null, 2) } catch { run.error = raw }
        run.failed = err.response.status >= 500
      } else {
        run.error = err.message || 'Invocation failed'
        run.status = 'Error'
        run.failed = true
      }
    } finally {
      invoking.value = false
    }
    run.at = new Date().toISOString()
    // History first, logs after: the run object is reactive, so the response
    // paints immediately and the log panel fills in when the poll resolves.
    // Awaiting the poll here left the whole page blank for up to two seconds.
    runs.value[fnId] = [run, ...runsFor(fnId)].slice(0, 20)
    // Write through the REACTIVE entry, not the raw object: Vue tracks the
    // proxy, so mutating `run` after it lands here would fill the logs in and
    // never re-render -- the same trap stores/ai.js documents for streaming.
    const live = runs.value[fnId][0]
    live.logsPromise = loadLogs(live)
    return live
  }

  const loadFixtures = async (fnId) => {
    if (!fnId) return
    try {
      const { data } = await listFixtures(fnId)
      fixtures.value[fnId] = data?.fixtures || []
    } catch {
      fixtures.value[fnId] = []
    }
  }

  const saveFixture = async (fnId, name) => {
    const req = requestFor(fnId)
    // PUT-by-name is an upsert on (function_id, name), so re-saving a name
    // overwrites without a second confirm.
    await updateFixture(fnId, name, {
      name,
      method: req.method,
      path: req.path || '/',
      headers: headersObject(req),
      body: req.body || '',
    })
    await loadFixtures(fnId)
  }

  const applyFixture = (fnId, fx) => {
    setRequest(fnId, {
      method: fx.method || 'POST',
      path: fx.path || '/',
      body: fx.body || '',
      headers: Object.entries(fx.headers || {}).map(([key, value]) => ({ key, value })),
    })
  }

  const removeFixture = async (fnId, fx) => {
    await deleteFixture(fnId, fx.name)
    await loadFixtures(fnId)
  }

  return {
    requests, runs, fixtures, invoking,
    requestFor, runsFor, latestRun, fixturesFor, setRequest,
    invoke, loadFixtures, saveFixture, applyFixture, removeFixture,
  }
})
