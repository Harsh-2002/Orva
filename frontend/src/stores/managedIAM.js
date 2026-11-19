import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { readManagedIAMMode } from '@/api/managedPolicies.js'
import { useAuthStore } from './auth.js'

export const useManagedIAMStore = defineStore('managed-iam', () => {
  const auth = useAuthStore()
  const available = ref(false)
  // A caller that must not guess between managed and legacy views has to wait
  // for the answer: `available` being false means both "not yet asked" and
  // "not managed".
  const checked = ref(false)
  const mode = ref('unknown')
  let pending

  watch([() => auth.isAuthenticated, () => auth.user?.id], () => {
    pending = undefined
    checked.value = false
    available.value = false
    mode.value = 'unknown'
  }, { flush: 'sync' })

  function check({ fresh = false } = {}) {
    if (!auth.isAuthenticated) return false
    if (!pending && checked.value && !fresh) return available.value
    return checkMode({ fresh }).then((result) => result === 'managed')
  }

  async function checkMode({ fresh = false } = {}) {
    if (!auth.isAuthenticated) return 'unknown'
    if (pending) return pending
    if (mode.value !== 'unknown' && !fresh) return mode.value
    if (fresh) {
      checked.value = false
      mode.value = 'unknown'
    }
    const request = readManagedIAMMode()
      .catch(() => 'unknown')
      .then((result) => {
        if (pending !== request) return 'unknown'
        pending = undefined
        mode.value = result
        checked.value = true
        available.value = result === 'managed'
        return result
      })
    pending = request
    return request
  }

  return { available, checked, mode, check, checkMode }
})
