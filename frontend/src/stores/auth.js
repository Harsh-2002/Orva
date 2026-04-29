import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import axios from 'axios'

// Auth uses /api/v1/auth/* paths; a separate client avoids the /api/v1 baseURL prefix issues.
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

// Drop any leftover localStorage cache from older builds. We used to cache
// hasUser there, but that broke fresh installs where the operator wiped
// the DB while a browser still had the old "true" stuck — they'd be sent
// to /login on an instance that has no users yet. /auth/status is cheap;
// always ask the server.
try { localStorage.removeItem('orva.hasUser') } catch {}

export const useAuthStore = defineStore('auth', () => {
  const user = ref(null)
  const isAuthenticated = ref(false)
  const loading = ref(false)
  const hasUser = ref(null) // null = unknown, true/false = answered by /auth/status
  const expiresAt = ref(null) // ISO string from server, or null
  const refreshing = ref(false)
  const refreshDismissedUntil = ref(0) // epoch ms; suppress toast until this time

  const setHasUser = (v) => { hasUser.value = v }

  const login = async (username, password) => {
    loading.value = true
    try {
      const res = await authClient.post('/api/v1/auth/login', { username, password })
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
      const res = await authClient.post('/api/v1/auth/onboard', { username, password })
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
      await authClient.post('/api/v1/auth/logout')
    } catch {}
    user.value = null
    isAuthenticated.value = false
    // Keep hasUser=true so the next navigation routes to /login, not /onboarding.
  }

  // fetchAuthStatus asks the backend whether any user exists. Cached for
  // the lifetime of the page load — `has_user` only flips false→true once
  // (the first onboard) and we re-check on every full page load. The
  // router guard calls this first; we re-fetch when force=true (e.g. after
  // logout, where we want a fresh answer).
  const fetchAuthStatus = async ({ force = false } = {}) => {
    if (!force && hasUser.value !== null) return hasUser.value
    try {
      const res = await authClient.get('/api/v1/auth/status')
      setHasUser(!!res.data.has_user)
      return hasUser.value
    } catch {
      // Network blip — assume "user exists" so we route to /login rather
      // than dumping a returning operator into Onboarding. Onboarding is
      // a one-time flow; login is the safer default on first-touch error.
      if (hasUser.value === null) setHasUser(true)
      return hasUser.value
    }
  }

  const checkAuth = async () => {
    try {
      const res = await authClient.get('/api/v1/auth/me')
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
      const res = await authClient.post('/api/v1/auth/refresh')
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
