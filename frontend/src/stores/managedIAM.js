import { defineStore } from 'pinia'
import { ref, watch } from 'vue'
import { readManagedIAMCapability } from '@/api/managedPolicies.js'
import { useAuthStore } from './auth.js'

export const useManagedIAMStore = defineStore('managed-iam', () => {
  const auth = useAuthStore()
  const available = ref(false)
  let checked = false
  let pending

  watch([() => auth.isAuthenticated, () => auth.user?.id], () => {
    pending = undefined
    checked = false
    available.value = false
  }, { flush: 'sync' })

  function check({ fresh = false } = {}) {
    if (!auth.isAuthenticated) return false
    if (pending) return pending
    if (checked && !fresh) return available.value
    const request = readManagedIAMCapability().then((enabled) => {
      if (pending !== request) return false
      pending = undefined
      checked = true
      available.value = enabled
      return enabled
    })
    pending = request
    return request
  }

  return { available, check }
})
