import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useManagedIAMStore } from '@/stores/managedIAM.js'
import Layout from '@/components/layout/Layout.vue'
import { isCanonicalUuid } from '@/utils/permissionsModel.js'
import { listManagedIAMFunctions } from '@/api/managedPolicies.js'

// The Editor takes `?mode=edit` as a prop for the same reason it takes :name as
// one: it is keep-alived, and useRoute() keeps moving under a deactivated view.
const editorProps = (r) => ({ ...r.params, mode: r.query.mode === 'edit' ? 'edit' : '' })

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
        { path: 'ai', name: 'ai', component: () => import('@/views/AI.vue') },
        { path: 'functions', name: 'functions', component: () => import('@/views/FunctionsList.vue') },
        // Managed-only: the router guard refuses this when the capability is
        // absent, and ManagedRoutes re-checks it before drawing edit controls.
        { path: 'routes', name: 'managed-routes', component: () => import('@/views/ManagedRoutes.vue'), meta: { managedIAM: true } },
        // props: true, not useRoute(), on every scoped view below: Layout.vue
        // keep-alives the router-view and Vue does not patch a cached
        // instance's props, so an off-screen view stops following the URL.
        { path: 'functions/:name', name: 'function-detail', component: () => import('@/views/Editor.vue'), props: editorProps },
        { path: 'functions/:name/deployments', name: 'function-deployments', component: () => import('@/views/Deployments.vue'), props: true },
        { path: 'functions/:name/diff', name: 'function-diff', component: () => import('@/views/FunctionDiff.vue'), props: (r) => ({ name: r.params.name, from: r.query.from || '', to: r.query.to || '' }) },
        { path: 'functions/:name/kv', name: 'function-kv', component: () => import('@/views/KVStore.vue') },
        { path: 'functions/:name/inbound-webhooks', name: 'function-inbound-webhooks', component: () => import('@/views/InboundWebhooks.vue'), props: true },
        { path: 'functions/:name/test', name: 'function-test', component: () => import('@/views/TestWorkbench.vue'), props: true },
        { path: 'functions/:functionId/permissions', name: 'function-permissions', component: () => import('@/views/ManagedFunctionPermissions.vue'), props: true, meta: { managedIAM: true } },
        { path: 'iam/policies/:policyId?', name: 'iam-policies', component: () => import('@/views/ManagedIAMPolicies.vue'), props: true, meta: { managedIAM: true } },
        // Canonical create route. The Editor keys off its `name` prop, which
        // this route leaves empty -- that is what puts it in create mode, and
        // why it resolves props too rather than inheriting a stale one.
        { path: 'functions/new', name: 'function-new', component: () => import('@/views/Editor.vue'), props: editorProps },
        // Legacy /deploy redirects to the canonical create route. Kept so
        // any bookmarks or external links keep working; new code points
        // at /functions/new.
        { path: 'deploy', redirect: { name: 'function-new' } },
        { path: 'cron', name: 'cron', component: () => import('@/views/CronJobs.vue') },
        { path: 'jobs', name: 'jobs', component: () => import('@/views/Jobs.vue') },
        { path: 'activity', name: 'activity', component: () => import('@/views/Activity.vue') },
        { path: 'invocations', name: 'invocations', component: () => import('@/views/InvocationsLog.vue') },
        { path: 'traces', name: 'traces', component: () => import('@/views/Traces.vue') },
        { path: 'traces/:id', name: 'trace-detail', component: () => import('@/views/TraceDetail.vue'), props: true },
        { path: 'api-keys', name: 'api-keys', component: () => import('@/views/ApiKeys.vue') },
        { path: 'channels', name: 'channels', component: () => import('@/views/Channels.vue') },
        { path: 'webhooks', name: 'webhooks', component: () => import('@/views/Webhooks.vue') },
        { path: 'firewall', name: 'firewall', component: () => import('@/views/Firewall.vue') },
        { path: 'settings', name: 'settings', component: () => import('@/views/Settings.vue') },
        { path: 'docs', name: 'docs', component: () => import('@/views/Docs.vue') },
      ],
    },
    // Catch-all 404 — surfaces the path the user tried so typos are
    // easy to spot. Anything not matched above lands here, including
    // unknown nested routes and stale bookmarks pointing at removed
    // pages. Marked requiresAuth=false so an unauthenticated request
    // for a missing path doesn't bounce them into the login redirect
    // loop; the page itself is harmless info.
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: () => import('@/views/NotFound.vue'),
      meta: { requiresAuth: false },
    },
  ],
})

// The managed views address a function by id, but the rest of the dashboard
// (and anyone sharing a link) uses its name. Resolve an exact name from the
// same directory those views list. A partial or failed lookup is "no such
// function" rather than a guess, and listManagedIAMFunctions already refuses
// a non-canonical id.
async function managedFunctionId(name) {
  try {
    const functions = await listManagedIAMFunctions()
    return functions.find((fn) => fn.name === name)?.id ?? null
  } catch {
    return null
  }
}

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
    const iam = useManagedIAMStore()
    if (to.meta.managedIAM) {
      const notFound = () => next({
        name: 'not-found',
        params: { pathMatch: to.path.slice(1).split('/') },
        replace: true,
      })
      // Capability first: an instance without managed IAM must not spend the
      // directory request deciding a route it refuses either way.
      if (!(await iam.check({ fresh: true }))) return notFound()
      const { functionId, policyId } = to.params
      if (functionId && !isCanonicalUuid(functionId)) {
        // /functions/<name>/permissions is a URL operators type and share.
        // Replace it with the canonical one so the keep-alive view only ever
        // receives an id, and no managed view has to accept both.
        const id = await managedFunctionId(functionId)
        if (!id) return notFound()
        return next({
          name: to.name,
          params: { ...to.params, functionId: id },
          query: to.query,
          hash: to.hash,
          replace: true,
        })
      }
      if (policyId && !isCanonicalUuid(policyId)) return notFound()
    } else {
      void iam.check()
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
