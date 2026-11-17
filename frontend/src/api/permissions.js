import { isCanonicalUuid, normalizeSnapshot, parseCounter, isSha256, validatePolicy } from '../utils/permissionsModel.js'

function expectedWire(expected) {
  if (!expected || Object.keys(expected).sort().join() !== 'approvedArtifact,currentArtifact,revision'
    || parseCounter(expected.revision) === null
    || ![expected.currentArtifact, expected.approvedArtifact].every((v) => v === null || isSha256(v))) {
    throw new Error('Invalid permissions expectations')
  }
  return {
    revision: expected.revision,
    current_artifact: expected.currentArtifact,
    approved_artifact: expected.approvedArtifact,
  }
}

export function permissionsWire(payload, review = false) {
  if (!payload || Object.keys(payload).sort().join() !== 'expected,policy') throw new Error('Invalid permissions payload')
  const checked = validatePolicy(payload.policy)
  if (!checked.ok) throw new Error('Invalid policy')
  const expected = expectedWire(payload.expected)
  return review ? { expected } : { expected, policy: checked.policy }
}

// Inject the authenticated transport; neither this adapter nor the page stores a credential.
export function permissionsAPI(request) {
  const path = (id) => {
    if (!isCanonicalUuid(id)) throw new Error('Invalid function UUID')
    return `/api/v1/functions/${id}/permissions`
  }
  const read = async (id, options) => {
    const raw = await request(path(id) + (options.review ? '/review' : ''), options)
    const result = normalizeSnapshot(raw)
    if (result.status !== 'ready' || result.snapshot.function.id !== id) throw new Error('Invalid permissions response')
    return result.snapshot
  }
  return {
    get: (id, signal) => read(id, { method: 'GET', signal }),
    save: (id, payload, signal) => read(id, { method: 'PUT', body: permissionsWire(payload), signal }),
    review: (id, payload, signal) => read(id, { method: 'POST', body: permissionsWire(payload, true), review: true, signal }),
  }
}
