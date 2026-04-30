import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import Layout from '@/components/layout/Layout.vue'

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: '/login',
      name: 'login',
      component: () => import('@/views/Login.vue'),
      meta: { requiresAuth: false }
    },
    {
      path: '/onboarding',
      name: 'onboarding',
      component: () => import('@/views/Onboarding.vue'),
      meta: { requiresAuth: false }
    },
    {
      path: '/',
      component: Layout,
      meta: { requiresAuth: true },
      children: [
        { path: '', name: 'dashboard', component: () => import('@/views/Dashboard.vue') },
        { path: 'functions', name: 'functions', component: () => import('@/views/FunctionsList.vue') },
        { path: 'functions/:name', name: 'function-detail', component: () => import('@/views/Editor.vue') },
        { path: 'functions/:name/deployments', name: 'function-deployments', component: () => import('@/views/Deployments.vue') },
        { path: 'functions/:name/kv', name: 'function-kv', component: () => import('@/views/KVStore.vue') },
        { path: 'deploy', name: 'deploy', component: () => import('@/views/Editor.vue') },
        { path: 'cron', name: 'cron', component: () => import('@/views/CronJobs.vue') },
        { path: 'jobs', name: 'jobs', component: () => import('@/views/Jobs.vue') },
        { path: 'activity', name: 'activity', component: () => import('@/views/Activity.vue') },
        { path: 'invocations', name: 'invocations', component: () => import('@/views/InvocationsLog.vue') },
        { path: 'api-keys', name: 'api-keys', component: () => import('@/views/ApiKeys.vue') },
        { path: 'webhooks', name: 'webhooks', component: () => import('@/views/Webhooks.vue') },
        { path: 'firewall', name: 'firewall', component: () => import('@/views/Firewall.vue') },
        { path: 'docs', name: 'docs', component: () => import('@/views/Docs.vue') },
      ],
    },
  ],
})

// Auth guard. The decision matrix:
//
//   hasUser  isAuthed  destination          → action
//   ------   --------  -----------          ---------
//   false    *         /onboarding          allow
//   false    *         anything-else        → /onboarding
//   true     true      /login or /onboarding → /dashboard
//   true     true      protected             allow
//   true     false     /login                allow
//   true     false     protected             → /login?redirect=…
//
// `hasUser` is fetched once and cached (auth store handles that). Network
// errors on /auth/status leave the cached value alone instead of dumping
// users into onboarding.
// Short-circuit no-op navigations (e.g. clicking the link for the route
// you're already on). Without this, vue-router still emits the full
// resolve cycle which re-runs the auth guard's network calls and causes
// a visible flicker on rapid clicks.
router.beforeEach((to, from, next) => {
  if (from.fullPath === to.fullPath && from.name) return next(false)
  next()
})

router.beforeEach(async (to, _from, next) => {
  const auth = useAuthStore()
  const hasUser = await auth.fetchAuthStatus()

  // First-run flow.
  if (!hasUser) {
    if (to.name === 'onboarding') return next()
    return next({ name: 'onboarding', replace: true })
  }

  // Returning operators — verify session before deciding.
  if (auth.isAuthenticated === false) {
    // We may have a valid cookie we haven't checked yet on this page load.
    await auth.checkAuth()
  }

  // Users exist + already logged in.
  if (auth.isAuthenticated) {
    if (to.name === 'onboarding' || to.name === 'login') {
      return next({ name: 'dashboard' })
    }
    return next()
  }

  // Users exist + not logged in.
  if (to.name === 'login') return next()
  if (to.name === 'onboarding') return next({ name: 'login', replace: true })

  // Anything else → bounce to login with the original target preserved.
  next({ name: 'login', query: { redirect: to.fullPath } })
})

export default router
