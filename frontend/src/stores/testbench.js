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
  at: null,
})

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

  // Both log paths come from the execution record, which is the only place
  // either one exists: console.log/print are rerouted to stderr by the
  // adapters, and orva.log.* is parsed server-side out of that same stderr
  // into log_entries. The editor could never show either, because it never
  // read X-Orva-Execution-ID off the invoke response.
  const loadLogs = async (run) => {
    if (!run.executionId) return
    try {
      const { data } = await getInvocationLogs(run.executionId)
      const raw = data?.stderr || ''
      run.stderr = raw ? raw.split('\n').filter((l) => l !== '') : []
      run.structured = data?.log_entries || []
    } catch {
      // A handler that wrote nothing has no logs row; that is not an error.
    }
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
      run.durationMs = res.headers?.['x-orva-duration-ms'] || '?'
      run.executionId = res.headers?.['x-orva-execution-id'] || ''
      try { run.body = JSON.stringify(JSON.parse(text), null, 2) } catch { run.body = text }
      // Only 5xx is a bug to debug; a deliberate 401 from an authz check is not.
      run.failed = typeof res.status === 'number' && res.status >= 500
    } catch (err) {
      if (err.response) {
        run.status = `${err.response.status}`
        run.durationMs = err.response.headers?.['x-orva-duration-ms'] || '?'
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
    await loadLogs(run)
    runs.value[fnId] = [run, ...runsFor(fnId)].slice(0, 20)
    return run
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
