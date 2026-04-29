// Events store: a single EventSource connected to /api/v1/events that
// fans typed callbacks out to whatever views care. Replaces the old
// per-page setInterval polls of /system/metrics.json + /executions.
//
// One stream per browser tab; the browser's per-origin connection cap
// (~6 on HTTP/1.1) makes per-page streams expensive, so anywhere that
// needs live data hooks into this store and filters by event type
// client-side. The server side fans out cheaply.

import { defineStore } from 'pinia'
import { ref, readonly } from 'vue'

const RECONNECT_BACKOFF_MS = [500, 1000, 2000, 5000, 10000]

export const useEventsStore = defineStore('events', () => {
  const connected = ref(false)
  const reconnectAttempt = ref(0)

  // Subscribers map: event-type -> Set<callback>. Callback receives the
  // parsed event payload. Using a Set so the same callback can be added
  // and removed cleanly (Vue's onUnmounted in a component will remove it).
  const subscribers = new Map()
  let es = null
  let reconnectTimer = null

  const dispatch = (type, data) => {
    const set = subscribers.get(type)
    if (!set) return
    for (const cb of set) {
      try { cb(data) } catch (e) { console.error('events callback error', e) }
    }
  }

  const wireHandlers = (source) => {
    // Generic message handler — for events without a `type` field on the
    // EventSource (i.e. default messages). Not used by the server today
    // but cheap to keep.
    source.onopen = () => {
      connected.value = true
      reconnectAttempt.value = 0
    }

    // Server uses named events: `event: metrics`, `event: deployment`, ...
    // Each addEventListener registers for that specific event type.
    const types = ['metrics', 'execution', 'deployment', 'function']
    for (const t of types) {
      source.addEventListener(t, (ev) => {
        try {
          const data = JSON.parse(ev.data)
          dispatch(t, data)
        } catch (e) {
          console.warn('failed to parse SSE payload', e, ev.data)
        }
      })
    }

    source.onerror = () => {
      connected.value = false
      if (es) {
        try { es.close() } catch {}
        es = null
      }
      // Exponential-ish backoff on reconnect. EventSource auto-reconnects
      // by default but we control the cadence explicitly so the connection-
      // state pill in the UI shows a sensible "reconnecting in N s".
      const delay = RECONNECT_BACKOFF_MS[Math.min(reconnectAttempt.value, RECONNECT_BACKOFF_MS.length - 1)]
      reconnectAttempt.value += 1
      clearTimeout(reconnectTimer)
      reconnectTimer = setTimeout(() => connect(), delay)
    }
  }

  const connect = () => {
    if (es) return
    try {
      es = new EventSource('/api/v1/events', { withCredentials: true })
      wireHandlers(es)
    } catch (e) {
      console.error('failed to open /api/v1/events', e)
      connected.value = false
    }
  }

  const disconnect = () => {
    clearTimeout(reconnectTimer)
    reconnectTimer = null
    if (es) {
      try { es.close() } catch {}
      es = null
    }
    connected.value = false
    reconnectAttempt.value = 0
  }

  // subscribe(type, cb) returns an unsubscribe function. Idiomatic for
  // Vue components: `const unsub = events.subscribe('metrics', fn);
  //                  onUnmounted(unsub)`.
  const subscribe = (type, cb) => {
    if (!subscribers.has(type)) subscribers.set(type, new Set())
    subscribers.get(type).add(cb)
    return () => {
      const set = subscribers.get(type)
      if (set) set.delete(cb)
    }
  }

  return {
    connected: readonly(connected),
    reconnectAttempt: readonly(reconnectAttempt),
    connect,
    disconnect,
    subscribe,
  }
})
