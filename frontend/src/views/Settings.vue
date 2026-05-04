<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-xl font-semibold text-white tracking-tight">
        Settings
      </h1>
      <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-relaxed">
        Operator-level controls for the running Orva instance.
      </p>
    </div>

    <!-- Storage card. Shows orva.db / functions tree / WAL sizes plus
         a "Compact" affordance that runs SQLite VACUUM via the
         admin-gated POST /api/v1/system/vacuum endpoint. -->
    <div class="bg-background border border-border rounded-lg p-5 space-y-4">
      <div class="flex items-start justify-between gap-4">
        <div>
          <div class="text-sm font-semibold text-white flex items-center gap-2">
            <HardDrive class="w-4 h-4 text-foreground-muted" />
            Storage
          </div>
          <p class="text-xs text-foreground-muted mt-1 max-w-prose">
            On-disk breakdown of the data directory.
            <code class="text-[11px]">VACUUM</code> rewrites
            <code class="text-[11px]">orva.db</code>, drops the freelist, and
            shrinks the file. The operation holds an exclusive lock; writes
            block briefly while it runs.
          </p>
        </div>
        <Button
          variant="secondary"
          :loading="vacuuming"
          :disabled="!storage || vacuuming"
          @click="askVacuum"
        >
          <Wand2 class="w-4 h-4" />
          Compact database
        </Button>
      </div>

      <!-- Skeleton while we wait for the first response. -->
      <div
        v-if="!storage && !storageError"
        class="text-xs text-foreground-muted italic"
      >
        Loading storage stats…
      </div>

      <div
        v-if="storageError"
        class="rounded-md border border-red-700/40 bg-red-950/30 p-3 text-xs text-red-200"
      >
        <div class="font-semibold text-red-100 mb-1">Failed to load storage stats</div>
        <div class="font-mono break-all">{{ storageError }}</div>
      </div>

      <div v-if="storage" class="space-y-3">
        <!-- Stacked bar — proportions of total. -->
        <div class="h-2 w-full rounded-full overflow-hidden bg-border/60 flex">
          <div
            v-if="dbPct > 0"
            class="bg-sky-500 h-full"
            :style="{ width: dbPct + '%' }"
            :title="`orva.db: ${formatBytes(storage.db_bytes)}`"
          />
          <div
            v-if="walPct > 0"
            class="bg-amber-500 h-full"
            :style="{ width: walPct + '%' }"
            :title="`WAL: ${formatBytes(storage.wal_bytes)}`"
          />
          <div
            v-if="fnPct > 0"
            class="bg-emerald-500 h-full"
            :style="{ width: fnPct + '%' }"
            :title="`functions/: ${formatBytes(storage.functions_bytes)}`"
          />
        </div>

        <!-- Numeric breakdown. -->
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-1.5 text-xs">
          <div class="flex items-center justify-between">
            <span class="flex items-center gap-2 text-foreground-muted">
              <span class="w-2 h-2 rounded-sm bg-sky-500"></span>
              orva.db
            </span>
            <span class="font-mono text-white">{{ formatBytes(storage.db_bytes) }}</span>
          </div>
          <div class="flex items-center justify-between">
            <span class="flex items-center gap-2 text-foreground-muted">
              <span class="w-2 h-2 rounded-sm bg-emerald-500"></span>
              functions/
            </span>
            <span class="font-mono text-white">{{ formatBytes(storage.functions_bytes) }}</span>
          </div>
          <div v-if="storage.wal_bytes > 0" class="flex items-center justify-between">
            <span class="flex items-center gap-2 text-foreground-muted">
              <span class="w-2 h-2 rounded-sm bg-amber-500"></span>
              orva.db-wal
            </span>
            <span class="font-mono text-white">{{ formatBytes(storage.wal_bytes) }}</span>
          </div>
          <div class="flex items-center justify-between">
            <span class="text-foreground-muted">total</span>
            <span class="font-mono text-white font-semibold">{{ formatBytes(storage.total_bytes) }}</span>
          </div>
        </div>

        <!-- VACUUM hint — only shown when there's something to reclaim. -->
        <div
          v-if="reclaimableBytes > 0"
          class="text-[11px] text-foreground-muted pt-1 border-t border-border"
        >
          {{ formatBytes(reclaimableBytes) }} reclaimable
          ({{ storage.db_free_pages }} free SQLite pages)
        </div>
      </div>

      <!-- Last-vacuum result. Sticks until next vacuum or page reload. -->
      <div
        v-if="lastVacuum"
        class="rounded-md border border-emerald-700/40 bg-emerald-950/30 p-3 text-xs text-emerald-200"
      >
        Compacted in {{ lastVacuum.duration_ms }} ms — freed
        <span class="font-mono">{{ formatBytes(lastVacuum.freed_bytes) }}</span>
        ({{ formatBytes(lastVacuum.before_bytes) }} →
        {{ formatBytes(lastVacuum.after_bytes) }}).
      </div>
      <div
        v-if="vacuumError"
        class="rounded-md border border-red-700/40 bg-red-950/30 p-3 text-xs text-red-200"
      >
        <div class="font-semibold text-red-100 mb-1">Compact failed</div>
        <div class="font-mono break-all">{{ vacuumError }}</div>
      </div>
    </div>

    <!-- Account card — change password + logout. -->
    <div class="bg-background border border-border rounded-lg p-5 space-y-4">
      <div>
        <div class="text-sm font-semibold text-white flex items-center gap-2">
          <KeyRound class="w-4 h-4 text-foreground-muted" />
          Account
        </div>
        <p class="text-xs text-foreground-muted mt-1">
          Update your password or end your session.
        </p>
      </div>

      <form
        class="space-y-3 pt-2 border-t border-border"
        @submit.prevent="handleChangePassword"
      >
        <div class="text-xs font-medium text-foreground-muted uppercase tracking-wide">
          Change password
        </div>
        <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
          <div class="flex flex-col gap-1">
            <label class="text-xs text-foreground-muted">Current password</label>
            <input
              v-model="pwForm.current"
              type="password"
              autocomplete="current-password"
              class="bg-surface border border-border rounded-md px-3 py-2 text-sm text-white placeholder:text-foreground-muted focus:outline-none focus:ring-1 focus:ring-primary"
              placeholder="••••••••"
            >
          </div>
          <div class="flex flex-col gap-1">
            <label class="text-xs text-foreground-muted">New password</label>
            <input
              v-model="pwForm.next"
              type="password"
              autocomplete="new-password"
              class="bg-surface border border-border rounded-md px-3 py-2 text-sm text-white placeholder:text-foreground-muted focus:outline-none focus:ring-1 focus:ring-primary"
              placeholder="••••••••"
            >
          </div>
          <div class="flex flex-col gap-1">
            <label class="text-xs text-foreground-muted">Confirm new password</label>
            <input
              v-model="pwForm.confirm"
              type="password"
              autocomplete="new-password"
              class="bg-surface border border-border rounded-md px-3 py-2 text-sm text-white placeholder:text-foreground-muted focus:outline-none focus:ring-1 focus:ring-primary"
              placeholder="••••••••"
            >
          </div>
        </div>

        <div
          v-if="pwError"
          class="rounded-md border border-red-700/40 bg-red-950/30 p-2.5 text-xs text-red-200"
        >
          {{ pwError }}
        </div>
        <div
          v-if="pwSuccess"
          class="rounded-md border border-emerald-700/40 bg-emerald-950/30 p-2.5 text-xs text-emerald-200"
        >
          Password updated successfully.
        </div>

        <Button
          type="submit"
          variant="primary"
          :loading="pwLoading"
          :disabled="pwLoading"
        >
          <KeyRound class="w-4 h-4" />
          Update password
        </Button>
      </form>

      <div class="pt-2 border-t border-border">
        <Button
          variant="danger"
          @click="handleLogout"
        >
          <LogOut class="w-4 h-4" />
          Log out
        </Button>
      </div>
    </div>

    <!-- Connected applications card — OAuth grants from claude.ai
         web, ChatGPT web, etc. Each row maps to one active
         oauth_access_tokens row. Revoke flips revoked_at; the next
         /mcp call from that connector returns 401. -->
    <div class="bg-background border border-border rounded-lg p-5 space-y-4">
      <div class="flex items-start justify-between gap-4">
        <div>
          <div class="text-sm font-semibold text-white flex items-center gap-2">
            <Plug class="w-4 h-4 text-foreground-muted" />
            Connected applications
          </div>
          <p class="text-xs text-foreground-muted mt-1 max-w-prose">
            Apps you've granted access to your Orva via OAuth.
            Connect new ones from the
            <RouterLink to="/docs#mcp" class="text-primary hover:underline">Docs</RouterLink>
            page.
          </p>
        </div>
        <span
          v-if="connectedApps.length > 0"
          class="text-xs text-foreground-muted self-center"
        >
          {{ connectedApps.length }} active
        </span>
      </div>

      <div
        v-if="connectedAppsError"
        class="rounded-md border border-red-700/40 bg-red-950/30 p-3 text-xs text-red-200"
      >
        <div class="font-semibold text-red-100 mb-1">Failed to load connected apps</div>
        <div class="font-mono break-all">{{ connectedAppsError }}</div>
      </div>

      <div
        v-else-if="connectedAppsLoading"
        class="text-xs text-foreground-muted italic"
      >
        Loading…
      </div>

      <div
        v-else-if="connectedApps.length === 0"
        class="rounded-md border border-dashed border-border p-6 text-center"
      >
        <Plug class="w-8 h-8 text-foreground-muted mx-auto mb-2 opacity-40" />
        <p class="text-xs text-foreground-muted">
          No connected applications yet.
        </p>
        <p class="text-[11px] text-foreground-muted mt-1">
          Add Orva as a custom connector in
          <span class="text-foreground-muted">claude.ai</span> or
          <span class="text-foreground-muted">ChatGPT</span> and it'll
          appear here.
        </p>
      </div>

      <ul v-else class="divide-y divide-border -mx-5">
        <li
          v-for="app in connectedApps"
          :key="app.id"
          class="px-5 py-3 flex items-start gap-3"
        >
          <component
            :is="iconForClient(app.client_name).icon"
            class="w-5 h-5 mt-0.5 shrink-0"
            :class="iconForClient(app.client_name).accent"
          />
          <div class="flex-1 min-w-0">
            <div class="text-sm font-medium text-white truncate">
              {{ app.client_name }}
            </div>
            <div class="text-[11px] text-foreground-muted mt-0.5 flex flex-wrap gap-x-3 gap-y-0.5">
              <span>Authorized {{ formatRelative(app.issued_at) }}</span>
              <span v-if="app.last_used_at">
                · Last used {{ formatRelative(app.last_used_at) }}
              </span>
              <span v-else class="italic opacity-70">· Never used</span>
              <span v-if="app.refresh_expires_at">
                · Re-consent {{ formatRelative(app.refresh_expires_at) }}
              </span>
            </div>
            <div class="flex flex-wrap gap-1 mt-2">
              <span
                v-for="s in scopeList(app.scope)"
                :key="s"
                class="text-[10px] px-1.5 py-0.5 rounded font-mono"
                :class="scopeBadgeClass(s)"
              >
                {{ s }}
              </span>
            </div>
          </div>
          <button
            type="button"
            class="text-xs text-foreground-muted hover:text-red-400 transition-colors flex items-center gap-1 shrink-0 self-center"
            :disabled="revokingId === app.id"
            @click="revokeApp(app)"
          >
            <Trash2 class="w-3.5 h-3.5" />
            Revoke
          </button>
        </li>
      </ul>
    </div>

    <!-- Active sessions card — operator's own browser logins. The
         calling session is flagged `current` and shows no Revoke
         button (use the Logout button in the Account card instead). -->
    <div class="bg-background border border-border rounded-lg p-5 space-y-4">
      <div class="flex items-start justify-between gap-4">
        <div>
          <div class="text-sm font-semibold text-white flex items-center gap-2">
            <Monitor class="w-4 h-4 text-foreground-muted" />
            Active sessions
          </div>
          <p class="text-xs text-foreground-muted mt-1 max-w-prose">
            Browsers signed in to this Orva. Revoke a session and that
            browser will need to log in again on its next request.
          </p>
        </div>
        <span
          v-if="sessions.length > 0"
          class="text-xs text-foreground-muted self-center"
        >
          {{ sessions.length }} active
        </span>
      </div>

      <div
        v-if="sessionsError"
        class="rounded-md border border-red-700/40 bg-red-950/30 p-3 text-xs text-red-200"
      >
        <div class="font-semibold text-red-100 mb-1">Failed to load sessions</div>
        <div class="font-mono break-all">{{ sessionsError }}</div>
      </div>

      <ul v-else class="divide-y divide-border -mx-5">
        <li
          v-for="s in sessions"
          :key="s.prefix"
          class="px-5 py-3 flex items-start gap-3"
        >
          <Monitor
            class="w-5 h-5 mt-0.5 shrink-0"
            :class="s.current ? 'text-emerald-400' : 'text-foreground-muted'"
          />
          <div class="flex-1 min-w-0">
            <div class="text-sm font-medium text-white flex items-center gap-2 flex-wrap">
              <span v-if="s.current">This session</span>
              <span v-else class="font-mono text-xs">{{ maskPrefix(s.prefix) }}</span>
              <span
                v-if="s.current"
                class="text-[10px] px-1.5 py-0.5 rounded bg-emerald-500/15 text-emerald-300 font-medium"
              >
                current
              </span>
            </div>
            <div class="text-[11px] text-foreground-muted mt-0.5">
              Signed in {{ formatRelative(s.created_at) }}
              · expires {{ formatRelative(s.expires_at) }}
            </div>
          </div>
          <button
            v-if="!s.current"
            type="button"
            class="text-xs text-foreground-muted hover:text-red-400 transition-colors flex items-center gap-1 shrink-0 self-center"
            :disabled="revokingPrefix === s.prefix"
            @click="revokeOtherSession(s)"
          >
            <Trash2 class="w-3.5 h-3.5" />
            Revoke
          </button>
        </li>
      </ul>
    </div>

    <!-- Backup / Restore card. -->
    <div class="bg-background border border-border rounded-lg p-5 space-y-4">
      <div class="flex items-start justify-between gap-4">
        <div>
          <div class="text-sm font-semibold text-white flex items-center gap-2">
            <DatabaseBackup class="w-4 h-4 text-foreground-muted" />
            Backup &amp; Restore
          </div>
          <p class="text-xs text-foreground-muted mt-1 max-w-prose">
            Download a single tarball with the full SQLite database and every deployed
            function version. Restore on a fresh machine to migrate or recover from disk
            loss. Backups are produced with <code class="text-[11px]">VACUUM&nbsp;INTO</code>
            so they are point-in-time consistent even under load.
          </p>
        </div>
      </div>

      <div class="flex flex-col sm:flex-row gap-3 pt-2 border-t border-border">
        <Button
          variant="primary"
          @click="downloadBackup"
        >
          <Download class="w-4 h-4" />
          Download backup
        </Button>
        <Button
          variant="secondary"
          :loading="restoring"
          @click="pickRestoreFile"
        >
          <Upload class="w-4 h-4" />
          Restore from backup
        </Button>
        <input
          ref="fileInput"
          type="file"
          accept=".tar.gz,.tgz,application/gzip"
          class="hidden"
          @change="onFileSelected"
        >
      </div>

      <!-- Restore status panel — surfaces backend errors verbatim because
           the operator needs to know exactly what broke. -->
      <div
        v-if="restoreError"
        class="rounded-md border border-red-700/40 bg-red-950/30 p-3 text-xs text-red-200"
      >
        <div class="font-semibold text-red-100 mb-1">Restore failed</div>
        <div class="font-mono break-all">{{ restoreError }}</div>
      </div>
      <div
        v-if="restoreOk"
        class="rounded-md border border-emerald-700/40 bg-emerald-950/30 p-3 text-xs text-emerald-200"
      >
        Restore complete. Reload the page to pick up the new data.
        <button
          class="underline ml-1"
          @click="reload"
        >
          Reload now
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter, RouterLink } from 'vue-router'
import {
  Download,
  Upload,
  DatabaseBackup,
  HardDrive,
  Wand2,
  KeyRound,
  LogOut,
  Plug,
  Monitor,
  Trash2,
} from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import { useConfirmStore } from '@/stores/confirm'
import { useAuthStore } from '@/stores/auth'
import {
  uploadRestore,
  getStorage,
  runVacuum,
  listConnectedApps,
  revokeConnectedApp,
  listSessions,
  revokeSession,
} from '@/api/endpoints'
import { formatRelative } from '@/utils/time'
import { iconForClient } from '@/utils/connectorIcons'

const confirmStore = useConfirmStore()
const auth = useAuthStore()
const router = useRouter()

// Account card state.
const pwForm = ref({ current: '', next: '', confirm: '' })
const pwLoading = ref(false)
const pwError = ref('')
const pwSuccess = ref(false)

const handleChangePassword = async () => {
  pwError.value = ''
  pwSuccess.value = false
  if (!pwForm.value.current || !pwForm.value.next || !pwForm.value.confirm) {
    pwError.value = 'All three fields are required.'
    return
  }
  if (pwForm.value.next.length < 8) {
    pwError.value = 'New password must be at least 8 characters.'
    return
  }
  if (pwForm.value.next !== pwForm.value.confirm) {
    pwError.value = 'New password and confirmation do not match.'
    return
  }
  pwLoading.value = true
  try {
    await auth.changePassword(pwForm.value.current, pwForm.value.next)
    pwSuccess.value = true
    pwForm.value = { current: '', next: '', confirm: '' }
  } catch (err) {
    pwError.value = err?.response?.data?.error?.message || 'Failed to update password.'
  } finally {
    pwLoading.value = false
  }
}

const handleLogout = async () => {
  await auth.logout()
  router.push('/login')
}

const fileInput = ref(null)
const restoring = ref(false)
const restoreError = ref('')
const restoreOk = ref(false)

// Storage card state.
const storage = ref(null)
const storageError = ref('')
const vacuuming = ref(false)
const vacuumError = ref('')
const lastVacuum = ref(null)

// Bar segments — clamp so nothing renders as 0px-but-visible.
const dbPct = computed(() =>
  storage.value && storage.value.total_bytes > 0
    ? Math.max(0.5, (storage.value.db_bytes / storage.value.total_bytes) * 100)
    : 0,
)
const walPct = computed(() =>
  storage.value && storage.value.total_bytes > 0 && storage.value.wal_bytes > 0
    ? Math.max(0.5, (storage.value.wal_bytes / storage.value.total_bytes) * 100)
    : 0,
)
const fnPct = computed(() =>
  storage.value && storage.value.total_bytes > 0
    ? Math.max(0.5, (storage.value.functions_bytes / storage.value.total_bytes) * 100)
    : 0,
)

// Upper bound on what VACUUM could reclaim — every page on the
// freelist is dead weight, so freelist_count × page_size is the
// optimistic estimate. The actual freed bytes after VACUUM is
// usually slightly higher (page-level fragmentation gets repacked).
const reclaimableBytes = computed(() => {
  if (!storage.value) return 0
  return (storage.value.db_free_pages || 0) * (storage.value.db_page_size || 0)
})

const fetchStorage = async () => {
  try {
    storageError.value = ''
    const res = await getStorage()
    storage.value = res.data
  } catch (err) {
    storageError.value = err?.response?.data?.error?.message || err?.message || 'unknown error'
  }
}

const askVacuum = async () => {
  const ok = await confirmStore.ask({
    title: 'Compact database?',
    message:
      'VACUUM rewrites orva.db to drop the freelist and shrink the file. ' +
      'It holds an exclusive lock for the duration; every other writer (deploys, ' +
      'invocations recording executions, KV puts, job enqueues) blocks until ' +
      'it returns. Typical runtime is sub-second, but a heavily-loaded instance ' +
      'can stall for several seconds.',
    confirmLabel: 'Compact',
    danger: false,
  })
  if (!ok) return

  vacuuming.value = true
  vacuumError.value = ''
  lastVacuum.value = null
  try {
    const res = await runVacuum()
    lastVacuum.value = res.data
    // Refresh the storage card so the operator sees the new sizes
    // immediately instead of having to reload the page.
    await fetchStorage()
  } catch (err) {
    vacuumError.value = err?.response?.data?.error?.message || err?.message || 'vacuum failed'
  } finally {
    vacuuming.value = false
  }
}

const formatBytes = (n) => {
  if (n == null || isNaN(n)) return '—'
  const k = 1024
  if (n < k) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n
  let i = -1
  while (v >= k && i < units.length - 1) {
    v /= k
    i++
  }
  return `${v.toFixed(2)} ${units[i]}`
}

onMounted(fetchStorage)

// ── Connected applications ──────────────────────────────────────────

const connectedApps = ref([])
const connectedAppsLoading = ref(false)
const connectedAppsError = ref('')
const revokingId = ref('')

const fetchConnectedApps = async () => {
  connectedAppsLoading.value = true
  connectedAppsError.value = ''
  try {
    const res = await listConnectedApps()
    connectedApps.value = res.data.apps || []
  } catch (err) {
    connectedAppsError.value = err?.response?.data?.error?.message || err?.message || 'unknown error'
  } finally {
    connectedAppsLoading.value = false
  }
}

const revokeApp = async (app) => {
  const ok = await confirmStore.ask({
    title: `Revoke ${app.client_name}?`,
    message:
      `${app.client_name} will lose access immediately. Any in-flight ` +
      'request will fail with 401. The connector can be re-authorized ' +
      'at any time from the originating app.',
    confirmLabel: 'Revoke',
    danger: true,
  })
  if (!ok) return
  revokingId.value = app.id
  try {
    await revokeConnectedApp(app.id)
    await fetchConnectedApps()
  } catch (err) {
    connectedAppsError.value = err?.response?.data?.error?.message || err?.message || 'failed to revoke'
  } finally {
    revokingId.value = ''
  }
}

// scope → list. Always parse fresh from the row; the API returns
// space-separated per RFC 6749 §3.3.
const scopeList = (s) => (s || '').split(/\s+/).filter(Boolean)

// Tailwind classes per scope. Severity gradient: read=neutral,
// invoke=blue, write=amber, admin=red. OIDC scopes (openid/email/
// profile) get the neutral gray — they're informational.
const scopeBadgeClass = (s) => {
  switch (s) {
    case 'admin':
      return 'bg-red-500/15 text-red-300'
    case 'write':
      return 'bg-amber-500/15 text-amber-300'
    case 'invoke':
      return 'bg-sky-500/15 text-sky-300'
    case 'read':
      return 'bg-foreground-muted/15 text-foreground-muted'
    default:
      return 'bg-foreground-muted/10 text-foreground-muted'
  }
}

// ── Active sessions ─────────────────────────────────────────────────

const sessions = ref([])
const sessionsError = ref('')
const revokingPrefix = ref('')

const fetchSessions = async () => {
  sessionsError.value = ''
  try {
    const res = await listSessions()
    sessions.value = res.data.sessions || []
  } catch (err) {
    sessionsError.value = err?.response?.data?.error?.message || err?.message || 'unknown error'
  }
}

const revokeOtherSession = async (s) => {
  const ok = await confirmStore.ask({
    title: 'Revoke this session?',
    message:
      'The browser using this session will be logged out on its next ' +
      'request. Use this if you suspect a device was lost or to clean ' +
      'up old logins.',
    confirmLabel: 'Revoke',
    danger: true,
  })
  if (!ok) return
  revokingPrefix.value = s.prefix
  try {
    await revokeSession(s.prefix)
    await fetchSessions()
  } catch (err) {
    sessionsError.value = err?.response?.data?.error?.message || err?.message || 'failed to revoke'
  } finally {
    revokingPrefix.value = ''
  }
}

// Show a few characters of the prefix so the operator can disambiguate
// rows without exposing the full token. "o••••••••42a3" pattern: first
// + last 4, dots in between.
const maskPrefix = (p) => {
  if (!p || p.length < 8) return p
  return p.slice(0, 1) + '••••••••' + p.slice(-4)
}

onMounted(fetchConnectedApps)
onMounted(fetchSessions)

// downloadBackup just hands the URL to the browser. The session cookie
// is sent automatically (same-origin), the server replies with
// Content-Disposition: attachment; filename=…, and the browser writes
// the file to the user's downloads dir without our UI having to buffer
// the whole tarball in memory.
const downloadBackup = () => {
  // Adding a cache-buster + same-tab navigation triggers a download
  // for application/gzip with a Content-Disposition header without
  // navigating away from this view. We could also use a hidden <a>;
  // window.location.assign keeps the implementation tiny.
  window.location.assign('/api/v1/backup?ts=' + Date.now())
}

const pickRestoreFile = () => {
  restoreError.value = ''
  restoreOk.value = false
  fileInput.value?.click()
}

const onFileSelected = async (e) => {
  const file = e.target.files?.[0]
  // Reset the input so picking the same file twice in a row still
  // fires `change`.
  e.target.value = ''
  if (!file) return

  const ok = await confirmStore.ask({
    title: 'Restore from backup?',
    message:
      `This will replace the live database and function code with the contents of "${file.name}". ` +
      'The current orva.db is moved aside as orva.db.before-restore-<timestamp> in case rollback is needed. ' +
      'You will need to reload after restore completes.',
    confirmLabel: 'Restore',
    danger: true,
  })
  if (!ok) return

  restoring.value = true
  restoreError.value = ''
  restoreOk.value = false
  try {
    await uploadRestore(file)
    restoreOk.value = true
  } catch (err) {
    restoreError.value = err?.response?.data?.error?.message || err?.message || 'Restore failed'
  } finally {
    restoring.value = false
  }
}

const reload = () => {
  window.location.reload()
}
</script>
