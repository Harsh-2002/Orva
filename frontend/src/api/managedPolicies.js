import client from './client.js'
import { isCanonicalUuid, parseCounter } from '../utils/permissionsModel.js'

const uuid = (id) => {
  if (!isCanonicalUuid(id)) throw new Error('A canonical UUID is required.')
  return id
}
const policyPath = (id) => `/api/v1/iam/policies/${uuid(id)}`
export const functionPath = (id) => `/api/v1/functions/${uuid(id)}/permissions`
const defaultRequest = async (url, { body, ...options } = {}) => {
  try { return (await client.request({ baseURL: '/', url, data: body, ...options, sessionOnly: true })).data }
  catch (error) { throw Object.assign(new Error(error.response?.data?.error?.message || 'Permissions request failed.'), { status: error.response?.status }) }
}

export async function readManagedIAMCapability(request = defaultRequest) {
  try {
    const result = await request('/api/v1/iam/capabilities', {
      quietStatuses: [401, 403, 404],
      headers: { 'Cache-Control': 'no-cache' },
    })
    return result?.managed === true
  } catch {
    return false
  }
}

export function managedPoliciesAPI(request = defaultRequest) {
  const listeners = new Set()
  let pending = 0
  const notify = () => { for (const listener of listeners) listener(pending) }
  async function write(url, options) {
    pending += 1
    notify()
    try { return await request(url, options) }
    finally { pending -= 1; notify() }
  }
  return {
    subscribeMutations: (listener) => { listeners.add(listener); return () => listeners.delete(listener) },
    list: (signal) => request('/api/v1/iam/policies', { signal }),
    get: (id, signal) => request(`${policyPath(id)}?history=true`, { signal }),
    create: (body, signal) => write('/api/v1/iam/policies', { method: 'POST', body, signal }),
    mutate: (id, operation, body, signal) => {
      if (!['update', 'impact', 'attach', 'detach', 'revoke', 'delete'].includes(operation)) throw new Error('Unsupported policy operation.')
      return (operation === 'impact' ? request : write)(`${policyPath(id)}/${operation}`, { method: 'POST', body, signal })
    },
    permissions: (id, signal) => request(functionPath(id), { signal }),
    inline: (id, body, signal) => write(functionPath(id), { method: 'PUT', body, signal }),
    review: (id, body, signal) => write(`${functionPath(id)}/review`, { method: 'POST', body, signal }),
  }
}

export const managedIAMAPI = managedPoliciesAPI()

export async function listManagedIAMFunctions(signal, request = defaultRequest) {
  const functions = new Map()
  let total
  for (let offset = 0; total === undefined || offset < total;) {
    const page = await request('/api/v1/functions', { signal, params: { limit: 1000, offset } })
    if (!Number.isSafeInteger(page?.total) || page.total < 0
      || (!Array.isArray(page.functions) && !(page.functions === null && page.total === 0))) {
      throw new Error('The function list response was invalid.')
    }
    total = Math.min(total ?? page.total, page.total)
    const rows = page.functions ?? []
    if (!rows.length) {
      if (offset < total) throw new Error('The function list changed. Refresh functions.')
      break
    }
    for (const fn of rows) {
      if (!isCanonicalUuid(fn?.id) || typeof fn.name !== 'string' || !fn.name || functions.has(fn.id)) {
        throw new Error('The function list changed or was invalid. Refresh functions.')
      }
      functions.set(fn.id, { id: fn.id, name: fn.name })
    }
    offset += rows.length
  }
  return [...functions.values()]
}

export function sourceExpectation(source) {
  if (!source || !isCanonicalUuid(source.function?.id) || parseCounter(source.host?.revision) === null) throw new Error('Refresh function permissions before reviewing changes.')
  return {
    function_id: source.function.id,
    expected: {
      revision: source.host.revision,
      current_artifact: source.host.current_artifact,
      approved_artifact: source.host.approved_artifact,
    },
  }
}

// The incumbent inline editor never sees the compiled union.
export function inlineSnapshot(source) {
  return {
    function: source.function,
    policy: source.inline_policy,
    targets: [],
    host: {
      revision: source.host.revision, authorityEpoch: source.host.authority_epoch,
      currentArtifact: source.host.current_artifact, approvedArtifact: source.host.approved_artifact,
      reviewRequired: source.host.review_required, quarantined: source.host.quarantined, enforcement: 'active',
      capabilities: { canRead: true, canManage: source.host.can_manage, canReview: source.host.can_manage, canEditPrivilegedCode: false },
    },
    history: source.history,
  }
}
