import apiClient, { getApiKey, fnClient } from './client'

// Health check
export const getHealth = () => apiClient.get('/system/health')

// Functions
export const listFunctions = (params) => apiClient.get('/functions', { params })

export const getFunction = (nameOrId) => apiClient.get(`/functions/${nameOrId}`)

export const getFunctionSource = (fnId) => apiClient.get(`/functions/${fnId}/source`)

export const deleteFunction = (id) => apiClient.delete(`/functions/${id}`)

// Deploy: create function + deploy code inline.
// data: { name, runtime, code, entrypoint? }
export const deployFunction = async (data) => {
  // Step 1: Create function if it doesn't exist.
  let fnId
  try {
    const createResp = await apiClient.post('/functions', {
      name: data.name,
      runtime: data.runtime,
    })
    fnId = createResp.data.id
  } catch (err) {
    // If function already exists (409), look it up.
    if (err.response && err.response.status === 409) {
      const listResp = await apiClient.get('/functions')
      const fn = (listResp.data.functions || []).find(f => f.name === data.name)
      if (fn) {
        fnId = fn.id
      } else {
        throw err
      }
    } else {
      throw err
    }
  }

  // Step 2: Deploy code inline.
  return apiClient.post(`/functions/${fnId}/deploy-inline`, {
    code: data.code || data.file,
    filename: data.entrypoint,
  })
}

// Deploy inline for existing function (used by Editor).
export const deployInline = (fnId, code, filename) =>
  apiClient.post(`/functions/${fnId}/deploy-inline`, { code, filename })

// Invoke function by ID. URL uses short form: /fn/{id} not /fn/fn_{id}.
export const invokeFunction = (fnId, payload) =>
  fnClient.post(`/${fnId.replace(/^fn_/, '')}`, payload, { responseType: 'text' })

// Invoke function by name (resolves to ID first).
export const invokeFunctionByName = async (name, payload) => {
  const listResp = await apiClient.get('/functions')
  const fn = (listResp.data.functions || []).find(f => f.name === name)
  if (!fn) throw new Error(`Function "${name}" not found`)
  return fnClient.post(`/${fn.id.replace(/^fn_/, '')}`, payload, { responseType: 'text' })
}

// Executions (invocations in Orva-fx terminology)
export const listInvocations = (params) => apiClient.get('/executions', { params })

export const getInvocation = (id) => apiClient.get(`/executions/${id}`)

export const getInvocationLogs = (id) => apiClient.get(`/executions/${id}/logs`)

// Live Activity feed — historical companion to the SSE event stream.
// params: source, actor_id, since (unix-millis), until, status_min, q,
// limit (default 200, max 1000), cursor (ts millis from prior page).
export const listActivity = (params) => apiClient.get('/activity', { params })

// Per-function KV inspector. Same axios apiClient + same error shape as
// the rest of the surface. Keys can contain ":" / "/" so we always
// encode them; both fnId-as-id (fn_xxx) and fnId-as-name work.
export const kvList = (fnId, params) =>
  apiClient.get(`/functions/${encodeURIComponent(fnId)}/kv`, { params })

export const kvGet = (fnId, key) =>
  apiClient.get(`/functions/${encodeURIComponent(fnId)}/kv/${encodeURIComponent(key)}`)

export const kvPut = (fnId, key, body) =>
  apiClient.put(`/functions/${encodeURIComponent(fnId)}/kv/${encodeURIComponent(key)}`, body)

export const kvDelete = (fnId, key) =>
  apiClient.delete(`/functions/${encodeURIComponent(fnId)}/kv/${encodeURIComponent(key)}`)

// API Keys
export const listApiKeys = () => apiClient.get('/keys')

export const createApiKey = (data) => apiClient.post('/keys', data)

export const deleteApiKey = (id) => apiClient.delete(`/keys/${id}`)

// Runtimes
export const listRuntimes = () => apiClient.get('/runtimes')

// Syscalls
export const listSyscalls = () => apiClient.get('/syscalls')

// Metrics (returns Prometheus text format)
export const getMetrics = () => apiClient.get('/system/metrics', { responseType: 'text' })

// Structured metrics for the dashboard. Same data as /system/metrics but
// pre-shaped so we don't parse Prometheus text in the browser.
export const getMetricsJSON = () => apiClient.get('/system/metrics.json')

// Deployments (async build pipeline).
export const getDeployment = (id) => apiClient.get(`/deployments/${id}`)
export const getDeploymentLogs = (id, fromSeq = 0, limit = 200) =>
  apiClient.get(`/deployments/${id}/logs`, { params: { from: fromSeq, limit } })
export const listDeployments = (fnId, limit = 50) =>
  apiClient.get(`/functions/${fnId}/deployments`, { params: { limit } })

// SSE — returns a native EventSource. Caller is responsible for `.close()`.
// We can't use the axios client for SSE; the browser's EventSource sends
// the session cookie automatically (same-origin), and API-key auth is not
// supported for SSE today. UI uses session auth in the dashboard anyway.
export const streamDeployment = (id) =>
  new EventSource(`/api/v1/deployments/${id}/stream`)

// rollbackFunction posts to the Round-G rollback endpoint. Body must
// include either deployment_id (preferred — disambiguates same-hash
// deploys) or code_hash. Returns the new (synthetic, source=rollback)
// deployment record.
export const rollbackFunction = (id, body) =>
  apiClient.post(`/functions/${id}/rollback`, body)

// --- Stubs for features not yet in backend ---
export const getFunctionVersions = () => Promise.resolve({ data: [] })
export const rollbackFunctionVersion = (id, body) => apiClient.post(`/functions/${id}/rollback`, body)
export const deleteFunctionVersion = () => Promise.resolve({ data: {} })
export const listSecrets = () => Promise.resolve({ data: [] })
export const upsertSecret = () => Promise.resolve({ data: {} })
export const deleteSecret = () => Promise.resolve({ data: {} })

// ── Cron schedules (Phase 1) ────────────────────────────────────────
//
// The CronJobs.vue UI was built around a {function_name, cron, enabled}
// shape; the backend uses {function_id, cron_expr, enabled, payload}.
// These shims translate so neither side has to know about the other.

// Lookup function by friendly name. Used internally by the cron shims
// to resolve the UI's `function_name` arg into a function_id for the
// backend route. Throws if the function doesn't exist.
const _resolveFnId = async (functionName) => {
  const listResp = await apiClient.get('/functions')
  const fn = (listResp.data.functions || []).find(f => f.name === functionName)
  if (!fn) throw new Error(`Function "${functionName}" not found`)
  return fn.id
}

// Augment a backend cron row with the UI's expected aliases so the
// existing template (which references cron_expression and function_name)
// keeps rendering without changes.
const _decorateSchedule = (row) => ({
  ...row,
  // UI uses cron_expression; backend uses cron_expr.
  cron_expression: row.cron_expr,
  // function_name is already filled in by the ListAll join; keep it as-is.
})

export const listCronSchedules = async () => {
  const res = await apiClient.get('/cron')
  const schedules = (res.data.schedules || []).map(_decorateSchedule)
  return { data: { schedules } }
}

export const getCronSchedule = async (id) => {
  // No standalone GET-by-id endpoint; pull from the list.
  const list = await listCronSchedules()
  const found = list.data.schedules.find(s => s.id === id)
  return { data: found || null }
}

// createCronSchedule(functionName, {cron, timezone?, enabled, payload?})
export const createCronSchedule = async (functionName, body) => {
  const fnId = await _resolveFnId(functionName)
  const payload = {
    cron_expr: body.cron,
    timezone: body.timezone || browserTimezone(),
    enabled: body.enabled !== false,
    payload: body.payload ?? {},
  }
  const res = await apiClient.post(`/functions/${fnId}/cron`, payload)
  return { data: _decorateSchedule(res.data) }
}

// updateCronSchedule(scheduleId, {function_id, cron?, timezone?, enabled?, payload?})
// function_id is required so the route matches.
export const updateCronSchedule = async (scheduleId, body) => {
  const fnId = body.function_id
  if (!fnId) throw new Error('updateCronSchedule: function_id is required')
  const payload = {}
  if (body.cron !== undefined)     payload.cron_expr = body.cron
  if (body.timezone !== undefined) payload.timezone  = body.timezone
  if (body.enabled !== undefined)  payload.enabled   = body.enabled
  if (body.payload !== undefined)  payload.payload   = body.payload
  const res = await apiClient.put(`/functions/${fnId}/cron/${scheduleId}`, payload)
  return { data: _decorateSchedule(res.data) }
}

// browserTimezone returns the user's IANA name (e.g. "Asia/Kolkata") so
// new schedules default to the operator's local zone instead of the
// orvad process timezone (typically UTC in containers).
export const browserTimezone = () => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

// deleteCronSchedule(scheduleId, functionId) — both required because the
// backend route is /functions/{fn}/cron/{id}. CronJobs.vue currently
// passes (function_name) only; the view will be updated to pass both.
export const deleteCronSchedule = async (scheduleId, functionId) => {
  if (!functionId) throw new Error('deleteCronSchedule: functionId is required')
  return apiClient.delete(`/functions/${functionId}/cron/${scheduleId}`)
}

// ── Jobs queue (Phase 5) ────────────────────────────────────────────

// Lists jobs with optional filters: status ('pending'|'running'|...) and
// function_id. Default limit=50.
export const listJobs = (params = {}) =>
  apiClient.get('/jobs', { params })

export const getJob = (id) => apiClient.get(`/jobs/${id}`)

export const enqueueJob = (body) => apiClient.post('/jobs', body)

export const retryJob = (id) => apiClient.post(`/jobs/${id}/retry`)

export const deleteJob = (id) => apiClient.delete(`/jobs/${id}`)

// ── Webhook subscriptions (Phase v0.3) ──────────────────────────────

export const listWebhooks = () => apiClient.get('/webhooks')
export const createWebhook = (body) => apiClient.post('/webhooks', body)
export const getWebhook = (id) => apiClient.get(`/webhooks/${id}`)
export const updateWebhook = (id, body) => apiClient.put(`/webhooks/${id}`, body)
export const deleteWebhook = (id) => apiClient.delete(`/webhooks/${id}`)
export const testWebhook = (id) => apiClient.post(`/webhooks/${id}/test`)
export const listWebhookDeliveries = (id) => apiClient.get(`/webhooks/${id}/deliveries`)
export const retryWebhookDelivery = (id) => apiClient.post(`/webhooks/deliveries/${id}/retry`)
