// Synthetic permissions fixtures. Every value here is invented for tests and
// the UI preview; none of it describes a real function, caller or digest.

export const FN_ID = '0190a1b2-c3d4-7e5f-8a6b-1c2d3e4f5a6b'
export const OTHER_FN_ID = '0190a1b2-c3d4-7e5f-8a6b-1c2d3e4f5a6c'
export const CALLER_ID = '0190a1b2-c3d4-7e5f-9a6b-1c2d3e4f5a70'
export const MISSING_ID = '0190a1b2-c3d4-7e5f-9a6b-1c2d3e4f5a71'
export const DIGEST_A = 'a'.repeat(64)
export const DIGEST_B = 'b'.repeat(64)

export const caps = (over = {}) => ({
  canRead: true, canManage: true, canReview: false, canEditPrivilegedCode: false, ...over,
})

export const policy = () => ({
  schema_version: 1,
  allow: [
    { actions: ['kv.get', 'kv.list'], resource: { own_kv: { prefix: 'config/' } }, context: 'bootstrap' },
    { actions: ['kv.get', 'kv.put'], resource: { own_kv: { key: 'state' } }, context: 'execution' },
    { actions: ['functions.invoke'], resource: { function: { id: OTHER_FN_ID } }, context: 'execution' },
  ],
  inbound: [{ caller_id: CALLER_ID, triggers: ['f2f', 'job'] }],
})

export function makeSnapshot(over = {}) {
  const { host = {}, ...rest } = over
  return {
    function: { id: FN_ID, name: 'preview-worker' },
    policy: policy(),
    host: {
      revision: '7',
      authorityEpoch: '3',
      currentArtifact: DIGEST_A,
      approvedArtifact: DIGEST_A,
      reviewRequired: false,
      quarantined: false,
      enforcement: 'active',
      capabilities: caps(),
      ...host,
    },
    targets: [
      { id: OTHER_FN_ID, label: 'preview-renderer', state: 'resolved' },
      { id: CALLER_ID, label: null, state: 'missing' },
    ],
    history: [
      { revision: '7', authorityEpoch: '3', digest: DIGEST_A, actor: { kind: 'user', id: 'preview-operator' }, changedAt: '2026-01-02T03:04:05Z' },
      { revision: '6', authorityEpoch: '3', digest: DIGEST_B, actor: { kind: 'user', id: 'preview-operator' }, changedAt: '2026-01-01T03:04:05Z' },
    ],
    ...rest,
  }
}
