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
export const listCronSchedules = () => Promise.resolve({ data: [] })
export const getCronSchedule = () => Promise.resolve({ data: null })
export const createCronSchedule = () => Promise.resolve({ data: {} })
export const deleteCronSchedule = () => Promise.resolve({ data: {} })
