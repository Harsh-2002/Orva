import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import axios from 'axios'

// Auth uses /auth/* paths (not /api/v1/*), so we need a separate client.
const authClient = axios.create({
  baseURL: '',
  timeout: 30000,
  withCredentials: true,
  headers: { 'Content-Type': 'application/json' },
})

// expiringSoonHours is the threshold at which we surface the "session
// expiring soon" toast. 12 h on a 7-day session ≈ 93% through; visible
// enough for daily users to act, infrequent enough to avoid nagging.
const expiringSoonHours = 12

// hasUser is the "does any user exist on this instance" boolean. We
// persist it to localStorage because /auth/status is consulted on every
// page load, and during heavy load tests the backend can briefly fail
// or time out — without the cache, a transient error on refresh would
// bounce the user to /onboarding (which is the wrong page for a returning
// user). Once we've ever observed `has_user: true`, we never forget it
// without an explicit operator action (DB wipe, which is rare and
// detectable: a fresh DB has no sessions, so /auth/me would 401 anyway
// and the user would be sent to /login).
const hasUserKey = 'orva.hasUser'
const loadHasUserCache = () => {
  const v = localStorage.getItem(hasUserKey)
  if (v === 'true') return true
  if (v === 'false') return false
  return null
}
const saveHasUserCache = (v) => {
  if (v === null) localStorage.removeItem(hasUserKey)
  else localStorage.setItem(hasUserKey, v ? 'true' : 'false')
}

export const useAuthStore = defineStore('auth', () => {
  const user = ref(null)
  const isAuthenticated = ref(false)
  const loading = ref(false)
  const hasUser = ref(loadHasUserCache()) // null = unknown, true/false = checked (and persisted)
  const expiresAt = ref(null) // ISO string from server, or null
  const refreshing = ref(false)
  const refreshDismissedUntil = ref(0) // epoch ms; suppress toast until this time

  // Persist hasUser whenever it changes (sticky to true once seen).
  const setHasUser = (v) => {
    hasUser.value = v
    saveHasUserCache(v)
  }

  const login = async (username, password) => {
    loading.value = true
    try {
      const res = await authClient.post('/auth/login', { username, password })
      user.value = res.data.user
      isAuthenticated.value = true
      setHasUser(true)
      return { success: true }
    } catch (error) {
      return {
        success: false,
        error: error.response?.data?.error?.message || 'Login failed'
      }
    } finally {
      loading.value = false
    }
  }

  const onboard = async (username, password) => {
    loading.value = true
    try {
      const res = await authClient.post('/auth/onboard', { username, password })
      user.value = res.data.user
      isAuthenticated.value = true
      setHasUser(true)
      return { success: true }
    } catch (error) {
      return {
        success: false,
        error: error.response?.data?.error?.message || 'Setup failed'
      }
    } finally {
      loading.value = false
    }
  }

  const logout = async () => {
    try {
      await authClient.post('/auth/logout')
    } catch {}
    user.value = null
    isAuthenticated.value = false
    // Keep hasUser=true so the next navigation routes to /login, not /onboarding.
  }

  // fetchAuthStatus asks the backend whether any user exists. The result
  // determines onboarding-vs-login routing. We cache it for the lifetime of
  // the page load — `has_user` only flips false→true once (the first
  // onboard) and never the other way without an admin manually wiping the
  // DB. Re-fetching on every navigation was the source of a bug where a
  // transient network error would force a logged-in user into onboarding.
  // `force: true` re-asks the backend — used after logout for safety.
  const fetchAuthStatus = async ({ force = false } = {}) => {
    if (!force && hasUser.value !== null) return hasUser.value
    try {
      const res = await authClient.get('/auth/status')
      setHasUser(!!res.data.has_user)
      return hasUser.value
    } catch {
      // Network blip — keep the prior value if we have one. Default to
      // `true` on first failure so we route to /login (which is friendlier
      // than dumping a returning user into Onboarding). The localStorage
      // cache means this branch only fires on first-ever load with a
      // transient backend failure.
      if (hasUser.value === null) setHasUser(true)
      return hasUser.value
    }
  }

  const checkAuth = async () => {
    try {
      const res = await authClient.get('/auth/me')
      user.value = res.data
      isAuthenticated.value = true
      setHasUser(true)
      // Server now returns expires_at — capture for the expiry toast.
      expiresAt.value = res.data.expires_at || null
      return true
    } catch {
      user.value = null
      isAuthenticated.value = false
      expiresAt.value = null
      return false
    }
  }

  // refreshSession exchanges the current cookie for a fresh 7-day one.
  // Called from the "Stay signed in" toast action.
  const refreshSession = async () => {
    refreshing.value = true
    try {
      const res = await authClient.post('/auth/refresh')
      expiresAt.value = res.data.expires_at || null
      return { success: true }
    } catch (error) {
      isAuthenticated.value = false
      expiresAt.value = null
      return { success: false, error: error.response?.data?.error?.message || 'Refresh failed' }
    } finally {
      refreshing.value = false
    }
  }

  // secondsUntilExpiry — negative once the session is past its TTL.
  // Reactive via Date.now() polling done by the consumer (Layout polls 30s).
  const secondsUntilExpiry = computed(() => {
    if (!expiresAt.value) return null
    return (new Date(expiresAt.value).getTime() - Date.now()) / 1000
  })

  // shouldShowExpiryToast — true when the session is in the last
  // expiringSoonHours window AND the user hasn't dismissed it recently.
  const shouldShowExpiryToast = computed(() => {
    if (!isAuthenticated.value) return false
    const s = secondsUntilExpiry.value
    if (s == null || s <= 0) return false
    if (Date.now() < refreshDismissedUntil.value) return false
    return s <= expiringSoonHours * 3600
  })

  const dismissExpiryToast = () => {
    // Hide for 1 hour at a time; the toast comes back if the user lingers.
    refreshDismissedUntil.value = Date.now() + 60 * 60 * 1000
  }

  return {
    user, isAuthenticated, loading, hasUser, expiresAt, refreshing,
    secondsUntilExpiry, shouldShowExpiryToast,
    login, onboard, logout, fetchAuthStatus, checkAuth, refreshSession,
    dismissExpiryToast,
  }
})
