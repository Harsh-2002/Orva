import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { readManagedIAMCapability } from '@/api/managedPolicies.js'
import { useAuthStore } from './auth.js'

export const useManagedIAMStore = defineStore('managed-iam', () => {
  const auth = useAuthStore()
  const available = ref(false)
  // A caller that must not guess between managed and legacy views has to wait
  // for the answer: `available` being false means both "not yet asked" and
  // "not managed".
  const checked = ref(false)
  let pending

  watch([() => auth.isAuthenticated, () => auth.user?.id], () => {
    pending = undefined
    checked.value = false
    available.value = false
  }, { flush: 'sync' })

  function check({ fresh = false } = {}) {
    if (!auth.isAuthenticated) return false
    if (pending) return pending
    if (checked.value && !fresh) return available.value
    if (fresh) checked.value = false
    const request = readManagedIAMCapability()
      // A rejected probe is an absent capability, never a pending one.
      .catch(() => false)
      .then((enabled) => {
        if (pending !== request) return false
        pending = undefined
        checked.value = true
        available.value = enabled === true
        return available.value
      })
    pending = request
    return request
  }

  return { available, checked, check }
})
