import { nextTick, onActivated, onBeforeUnmount, onDeactivated, ref, watch } from 'vue'

const message = 'The permissions request failed. Retry.'
const failure = (e) => ({
  kind: ({ 409: 'conflict', 403: 'forbidden', 400: 'validation', 422: 'validation' })[e?.status] || 'error',
  message,
})

export function usePermissions(functionId, api) {
  const snapshot = ref(null)
  const loading = ref(false)
  const loadError = ref(null)
  const saving = ref(false)
  const saveError = ref(null)
  let lifetime = 0
  let readToken = 0
  let mutationToken = 0
  let active = true
  let readAbort
  let mutationAbort
  let queuedRefresh = false

  function cancel() {
    lifetime += 1
    readToken += 1
    mutationToken += 1
    readAbort?.abort()
    mutationAbort?.abort()
    loading.value = false
    saving.value = false
    queuedRefresh = false
  }
  async function refresh() {
    if (!active) return
    if (saving.value) { queuedRefresh = true; return }
    const owner = functionId()
    const epoch = lifetime
    const token = ++readToken
    readAbort?.abort()
    readAbort = new AbortController()
    const current = () => active && epoch === lifetime && owner === functionId() && token === readToken
    loading.value = true
    loadError.value = null
    try {
      const value = await api.get(owner, readAbort.signal)
      if (current()) snapshot.value = value
    } catch {
      if (current()) loadError.value = message
    } finally {
      if (current()) loading.value = false
    }
  }
  async function mutate(kind, payload) {
    if (!active || saving.value) return
    const owner = functionId()
    const epoch = lifetime
    const token = ++mutationToken
    readToken += 1
    readAbort?.abort()
    loading.value = false
    mutationAbort = new AbortController()
    const current = () => active && epoch === lifetime && owner === functionId() && token === mutationToken
    saving.value = true
    saveError.value = null
    loadError.value = null
    try {
      const value = await api[kind](owner, payload, mutationAbort.signal)
      if (!current()) return
      saving.value = false
      await nextTick()
      if (current()) snapshot.value = value
    } catch (e) {
      if (!current()) return
      saving.value = false
      saveError.value = failure(e)
      // The page must observe the terminal error before any queued refresh.
      await nextTick()
    } finally {
      if (current() && queuedRefresh) {
        queuedRefresh = false
        await refresh()
      }
    }
  }
  watch(functionId, () => {
    cancel()
    snapshot.value = null
    saveError.value = null
    loadError.value = null
    if (active) void refresh()
  }, { immediate: true, flush: 'sync' })
  onDeactivated(() => { active = false; cancel() })
  onActivated(() => {
    if (!active) { active = true; void refresh() }
  })
  onBeforeUnmount(() => { active = false; cancel() })
  return { snapshot, loading, loadError, saving, saveError, refresh,
    save: (payload) => mutate('save', payload), review: (payload) => mutate('review', payload) }
}
