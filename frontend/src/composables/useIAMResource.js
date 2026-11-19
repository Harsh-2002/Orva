import { nextTick, onActivated, onBeforeUnmount, onDeactivated, ref, watch } from 'vue'

export function useIAMResource(scope, load) {
  const data = ref(null)
  const loading = ref(false)
  const busy = ref(false)
  const error = ref('')
  const fresh = ref(false)
  let generation = 0
  let sequence = 0
  let active = true
  let readController
  let writeController
  let queued = false
  let mutation = 0
  const highWater = new Map()
  const revision = (value) => {
    const raw = value?.host?.revision ?? value?.policy?.definition_revision ?? value?.revision
    return typeof raw === 'string' && /^[1-9][0-9]*$/.test(raw) ? BigInt(raw) : null
  }
  function invalidate() { fresh.value = false; sequence += 1; readController?.abort(); loading.value = false }
  function expect(value) {
    // A policy mutation acknowledgement is not a function's combined revision.
    const sameKind = !data.value?.function || value?.function?.id === scope()
    const n = sameKind ? revision(value) : null
    if (n !== null && (highWater.get(scope()) ?? 0n) < n) highWater.set(scope(), n)
    invalidate()
  }
  const cancel = () => { generation += 1; mutation += 1; invalidate(); writeController?.abort(); busy.value = false; queued = false }
  async function refresh(preserveError = false) {
    if (!active || !scope()) return
    if (busy.value) { queued = true; return }
    const epoch = generation
    const key = scope()
    const token = ++sequence
    readController?.abort()
    readController = new AbortController()
    loading.value = true
    fresh.value = false
    if (!preserveError) error.value = ''
    const current = () => active && generation === epoch && scope() === key && sequence === token
    try {
      const value = await load(key, readController.signal)
      if (!current()) return
      const n = revision(value)
      if ((value?.function && value.function.id !== key) || (n !== null && n < (highWater.get(key) ?? 0n))) {
        throw new Error('An obsolete permissions response was rejected. Refresh the current owner.')
      }
      if (n !== null) highWater.set(key, n)
      data.value = value
      fresh.value = true
    }
    catch (e) { if (current()) error.value = e.message || 'Permissions could not be loaded. Retry.' }
    finally { if (current()) loading.value = false }
  }
  async function mutate(effect) {
    if (!active || busy.value || !scope()) return null
    const epoch = generation
    const key = scope()
    const write = ++mutation
    invalidate()
    writeController = new AbortController()
    const current = () => active && epoch === generation && key === scope() && write === mutation
    busy.value = true
    loading.value = false
    error.value = ''
    try {
      const value = await effect(writeController.signal)
      if (!current()) return null
      expect(value)
      busy.value = false
      await nextTick()
      if (!current()) return null
      await refresh()
      return current() ? value : null
    } catch (e) {
      if (current()) {
        busy.value = false
        fresh.value = false
        error.value = e.status === 409 ? 'Permissions changed. Refresh and review again before saving.' : (e.message || 'The change failed. Retry.')
        await nextTick()
        if (current()) await refresh(true)
      }
      return null
    } finally {
      if (current() && queued) { queued = false; await refresh(true) }
    }
  }
  watch(scope, () => { cancel(); data.value = null; error.value = ''; void refresh() }, { immediate: true, flush: 'sync' })
  onDeactivated(() => { active = false; cancel() })
  onActivated(() => { if (!active) { active = true; void refresh() } })
  onBeforeUnmount(() => { active = false; cancel() })
  return { data, fresh, loading, busy, error, refresh, mutate, invalidate, expect }
}
