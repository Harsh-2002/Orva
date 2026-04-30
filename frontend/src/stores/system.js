import { defineStore } from 'pinia'
import { ref } from 'vue'
import { listFunctions, listInvocations, getMetricsJSON } from '@/api/endpoints'
import { useEventsStore } from '@/stores/events'

// poolHistorySize is how many ticks of per-pool rate_ewma we retain for
// the dashboard sparkline. 60 ticks × 5s/tick = 5 min of context.
const poolHistorySize = 60

export const useSystemStore = defineStore('system', () => {
  const isConnected = ref(false)

  // metrics is the raw JSON snapshot — the UI reads everything from this
  // single source of truth instead of stitching together multiple
  // endpoints. Initialized from a one-shot GET on connect to seed the
  // page (race-free hydration before the first SSE event arrives), then
  // patched in-place as `metrics` events stream in.
  const metrics = ref(null)
  const functionsCount = ref(0)
  const recentInvocations = ref([])

  // poolHistory[fn_id] = ring of recent rate_ewma values for sparkline.
  const poolHistory = ref({})

  let unsubMetrics = null
  let unsubExecution = null
  let unsubFunction = null

  // Apply a metrics snapshot to local state, including per-pool history
  // ring buffer maintenance. Used both by the initial GET and by every
  // streamed metrics event.
  const applyMetrics = (snap) => {
    metrics.value = snap

    const seen = new Set()
    for (const p of snap.pools || []) {
      seen.add(p.function_id)
      const ring = poolHistory.value[p.function_id] || []
      ring.push(p.rate_ewma)
      if (ring.length > poolHistorySize) ring.splice(0, ring.length - poolHistorySize)
      poolHistory.value[p.function_id] = ring
    }
    for (const fid of Object.keys(poolHistory.value)) {
      if (!seen.has(fid)) delete poolHistory.value[fid]
    }
  }

  // seed: one-shot fetch to populate the page before the first SSE event
  // arrives. Without this the dashboard renders empty for ~5 s on first
  // load (until the first metrics tick).
  const seed = async () => {
    try {
      const [metricsRes, fnRes, invRes] = await Promise.all([
        getMetricsJSON(),
        listFunctions().catch(() => ({ data: { functions: [], total: 0 } })),
        listInvocations({ limit: 20 }).catch(() => ({ data: { executions: [] } })),
      ])
      applyMetrics(metricsRes.data)
      functionsCount.value = fnRes.data.total ?? (fnRes.data.functions || []).length
      recentInvocations.value = invRes.data.executions || []
      isConnected.value = true
    } catch (err) {
      console.error('seed fetch error:', err)
      isConnected.value = false
    }
  }

  // connect: seed once, then subscribe to live events. The events store
  // owns the EventSource lifecycle so this just registers callbacks.
  const connect = () => {
    const ev = useEventsStore()
    seed()
    unsubMetrics = ev.subscribe('metrics', (data) => {
      applyMetrics(data)
      // functionsCount is part of metrics-derived state; update from snap
      // when present (server emits the same MetricsJSONShape).
      isConnected.value = true
    })
    unsubExecution = ev.subscribe('execution', (data) => {
      // Prepend the new execution to the recent list and trim. data is
      // the full Execution row.
      recentInvocations.value = [data, ...recentInvocations.value].slice(0, 20)
    })
    // Function tile auto-updates: increment / decrement on create / delete,
    // leave alone on plain updates. Cheaper than re-listing, and the
    // metrics tick on the next ~5s tick will reconcile any drift.
    unsubFunction = ev.subscribe('function', (data) => {
      // v0.3: registry now publishes split actions (created / updated /
      // deleted) instead of upsert. We only adjust the counter on
      // create / delete; updates leave it stable.
      if (data.action === 'deleted') {
        functionsCount.value = Math.max(0, functionsCount.value - 1)
      } else if (data.action === 'created') {
        functionsCount.value = functionsCount.value + 1
      }
    })
  }

  const disconnect = () => {
    if (unsubMetrics) { unsubMetrics(); unsubMetrics = null }
    if (unsubExecution) { unsubExecution(); unsubExecution = null }
    if (unsubFunction) { unsubFunction(); unsubFunction = null }
    isConnected.value = false
  }

  return {
    isConnected,
    metrics,
    functionsCount,
    recentInvocations,
    poolHistory,
    connect,
    disconnect,
  }
})
