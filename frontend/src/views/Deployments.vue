<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-xl font-semibold text-white tracking-tight">
          Deployments
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
          History for
          <router-link
            :to="`/functions/${fnName}`"
            class="text-white underline"
          >
            {{ fnName }}
          </router-link>
        </p>
      </div>
      <div class="flex items-center gap-2">
        <Button
          variant="secondary"
          @click="$router.push(`/functions/${fnName}`)"
        >
          <UploadCloud class="w-4 h-4 mr-2" />
          Deploy New Version
        </Button>
        <Button
          variant="secondary"
          @click="refresh"
        >
          <RefreshCw
            class="w-4 h-4 mr-2"
            :class="{ 'animate-spin': loading }"
          />
          Refresh
        </Button>
      </div>
    </div>

    <!-- Active version banner -->
    <div
      v-if="activeFn"
      class="bg-background border border-border rounded-lg p-4 flex items-center gap-4"
    >
      <div class="w-10 h-10 rounded-md bg-success/15 border border-success/30 flex items-center justify-center shrink-0">
        <CheckCircle2 class="w-5 h-5 text-success" />
      </div>
      <div class="flex-1 min-w-0">
        <div class="flex items-center gap-2 flex-wrap">
          <span class="text-sm text-white font-medium">Currently serving</span>
          <span class="text-xs px-2 py-0.5 rounded bg-success/15 text-success border border-success/30 font-mono">
            v{{ activeFn.version }}
          </span>
          <span
            v-if="activeFn.status !== 'active'"
            class="text-xs px-2 py-0.5 rounded bg-amber-500/15 text-amber-400 border border-amber-500/30"
          >
            status: {{ activeFn.status }}
          </span>
        </div>
        <div class="text-xs text-foreground-muted mt-1 font-mono truncate">
          hash: {{ activeFn.code_hash || EMPTY }} · runtime: {{ activeFn.runtime }} · updated {{ formatTime(activeFn.updated_at) }}
        </div>
      </div>
    </div>

    <div
      v-if="error"
      class="bg-red-950/30 border border-red-900/40 rounded p-3 text-xs text-red-300"
    >
      {{ error }}
    </div>

    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <!-- Mobile (<sm) stacked-row list. -->
      <ul class="sm:hidden divide-y divide-border">
        <li
          v-for="d in deployments"
          :key="d.id"
          class="px-4 py-3 cursor-pointer active:bg-surface-hover/50 transition-colors"
          :class="isActive(d) ? 'bg-success/5' : ''"
          @click="open(d)"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2 flex-wrap">
                <span
                  class="font-mono text-xs"
                  :class="isActive(d) ? 'text-white font-semibold' : 'text-foreground-muted'"
                >v{{ d.version }}</span>
                <StatusBadge :status="d.status" />
                <span
                  v-if="isActive(d)"
                  class="px-1.5 py-0.5 rounded text-[10px] bg-success/15 text-success border border-success/30"
                >Active</span>
              </div>
              <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-foreground-muted">
                <span>{{ formatTime(d.submitted_at) }}</span>
                <span v-if="d.duration_ms != null" class="font-mono">{{ d.duration_ms }} ms</span>
                <span v-if="d.phase">{{ d.phase }}</span>
              </div>
            </div>
            <Button
              v-if="canRollback(d)"
              size="xs"
              variant="ghost"
              class="shrink-0"
              :disabled="rollingBack"
              @click.stop="rollbackTo(d)"
            >
              <RotateCcw class="w-3 h-3" /> Rollback
            </Button>
          </div>
        </li>
        <li
          v-if="!loading && deployments.length === 0"
          class="px-6 py-8 text-center text-sm text-foreground-muted"
        >
          No deployments yet for this function.
        </li>
      </ul>

      <table class="hidden sm:table w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th class="px-6 py-3 font-medium">
              Version
            </th>
            <th class="px-6 py-3 font-medium">
              Submitted
            </th>
            <th class="px-6 py-3 font-medium">
              Status
            </th>
            <th class="px-6 py-3 font-medium hidden md:table-cell">
              Phase
            </th>
            <th class="px-6 py-3 font-medium hidden sm:table-cell">
              Duration
            </th>
            <th class="px-6 py-3 font-medium hidden xl:table-cell">
              Deployment ID
            </th>
            <th class="px-6 py-3 font-medium text-right">
              Actions
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr
            v-for="d in deployments"
            :key="d.id"
            class="hover:bg-surface/50 transition-colors cursor-pointer"
            :class="isActive(d) ? 'bg-success/5' : ''"
            @click="open(d)"
          >
            <td class="px-6 py-4 font-mono text-xs">
              <div class="flex items-center gap-2">
                <span
                  class="text-white"
                  :class="isActive(d) ? 'font-semibold' : 'text-foreground-muted'"
                >v{{ d.version }}</span>
                <span
                  v-if="isActive(d)"
                  class="px-1.5 py-0.5 rounded text-[10px] bg-success/15 text-success border border-success/30 normal-case"
                >Active</span>
              </div>
            </td>
            <td class="px-6 py-4 text-foreground">
              {{ formatTime(d.submitted_at) }}
            </td>
            <td class="px-6 py-4">
              <StatusBadge :status="d.status" />
            </td>
            <td class="px-6 py-4 text-foreground-muted text-xs hidden md:table-cell">
              {{ d.phase || EMPTY }}
            </td>
            <td class="px-6 py-4 text-foreground-muted font-mono text-xs hidden sm:table-cell">
              {{ d.duration_ms != null ? d.duration_ms + 'ms' : EMPTY }}
            </td>
            <td class="px-6 py-4 text-foreground-muted font-mono text-xs hidden xl:table-cell">
              {{ d.id?.substring(0, 14) }}
            </td>
            <td
              class="px-6 py-4 text-right text-xs"
              @click.stop
            >
              <Button
                v-if="canRollback(d)"
                size="xs"
                variant="ghost"
                :disabled="rollingBack"
                @click="rollbackTo(d)"
              >
                <RotateCcw class="w-3 h-3" /> Rollback
              </Button>
              <span
                v-else-if="d.source === 'rollback'"
                class="text-foreground-muted/50"
              >via rollback</span>
              <span
                v-else
                class="text-foreground-muted/30"
              >{{ EMPTY }}</span>
            </td>
          </tr>
          <tr v-if="!loading && deployments.length === 0">
            <td
              colspan="7"
              class="px-6 py-8 text-center text-foreground-muted"
            >
              No deployments yet for this function.
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <Drawer v-model="drawerOpen" :title="drawerTitle" width="640px">
      <div v-if="!selected" class="p-6 text-sm text-foreground-muted">Nothing selected.</div>
      <div v-else class="p-5 space-y-4">
        <div class="flex items-center gap-2 flex-wrap">
          <StatusBadge :status="selected.status" />
          <span
            v-if="selected.phase"
            class="inline-flex items-center px-2.5 py-1 rounded text-xs border bg-background font-mono text-foreground-muted"
          >
            {{ selected.phase }}
          </span>
        </div>

        <div class="grid grid-cols-2 gap-3 text-sm">
          <Stat label="Version" :value="`v${selected.version}`" mono />
          <Stat label="Duration" :value="selected.duration_ms != null ? selected.duration_ms + ' ms' : EMPTY" />
          <Stat label="Submitted" :value="formatTime(selected.submitted_at)" />
          <Stat label="Finished" :value="selected.finished_at ? formatTime(selected.finished_at) : EMPTY" />
        </div>

        <div v-if="selected.error_message">
          <h3 class="text-xs uppercase tracking-wider text-foreground-muted mb-2">Error</h3>
          <pre class="bg-red-950/30 border border-red-900/40 rounded p-3 text-xs text-red-300 font-mono whitespace-pre-wrap break-words">{{ selected.error_message }}</pre>
        </div>

        <div>
          <div class="flex items-center justify-between mb-2">
            <h3 class="text-xs uppercase tracking-wider text-foreground-muted">Build log</h3>
            <span v-if="streamConnected" class="text-[10px] text-green-400">live</span>
          </div>
          <pre
            class="bg-surface border border-border rounded p-3 text-xs text-foreground font-mono overflow-auto max-h-96 whitespace-pre-wrap break-words"
          >{{ logText || '(no logs available)' }}</pre>
        </div>
      </div>
    </Drawer>
  </div>
</template>

<script setup>
import { EMPTY } from '@/utils/format'
import { ref, computed, onMounted, onBeforeUnmount, watch, h } from 'vue'
import { useEventsStore } from '@/stores/events'
import { useRoute } from 'vue-router'
import { RefreshCw, UploadCloud, CheckCircle2, RotateCcw } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import Drawer from '@/components/common/Drawer.vue'
import StatusBadge from '@/components/common/StatusBadge.vue'
import { listDeployments, getDeployment, getDeploymentLogs, listFunctions, rollbackFunction } from '@/api/endpoints'
import { useConfirmStore } from '@/stores/confirm'

const confirmStore = useConfirmStore()

const route = useRoute()
const fnName = computed(() => route.params.name)

const fnId = ref(null)
const activeFn = ref(null)  // the currently-active function record (for active-version banner)
const deployments = ref([])
const loading = ref(false)
const error = ref('')
const rollingBack = ref(false)

// canRollback: only succeeded deploys (with a known code_hash) that aren't
// currently active. Failed/queued/building rows have no artifact to point
// the symlink at; the active row is a no-op.
const canRollback = (d) =>
  d &&
  d.status === 'succeeded' &&
  d.code_hash &&
  !isActive(d)

// describeRollbackDiff: compare current function vs target deployment
// snapshot and return human-readable diff lines. Mirror of the helper in
// Editor.vue so users see the same blast-radius preview from either page.
const describeRollbackDiff = (fn, snap) => {
  const lines = []
  const cur = fn.env_vars || {}
  const next = snap.env_vars || {}
  const added = Object.keys(next).filter((k) => !(k in cur))
  const removed = Object.keys(cur).filter((k) => !(k in next))
  const changed = Object.keys(next).filter((k) => k in cur && cur[k] !== next[k])
  if (added.length)   lines.push(`+ Add env: ${added.join(', ')}`)
  if (removed.length) lines.push(`- Remove env: ${removed.join(', ')}`)
  if (changed.length) lines.push(`~ Change env: ${changed.join(', ')}`)
  const num = (label, a, b, suffix = '') => {
    if (a !== b) lines.push(`~ ${label}: ${a}${suffix} → ${b}${suffix}`)
  }
  num('Memory', fn.memory_mb, snap.memory_mb, ' MB')
  num('CPUs', fn.cpus, snap.cpus)
  num('Timeout', fn.timeout_ms, snap.timeout_ms, ' ms')
  num('Network', fn.network_mode || 'none', snap.network_mode || 'none')
  num('Auth gate', fn.auth_mode || 'none', snap.auth_mode || 'none')
  num('Rate limit', fn.rate_limit_per_min || 0, snap.rate_limit_per_min || 0, '/min')
  num('Max concurrency', fn.max_concurrency || 0, snap.max_concurrency || 0)
  num('Concurrency policy', fn.concurrency_policy || 'queue', snap.concurrency_policy || 'queue')
  return lines
}

// rollbackTo posts to the rollback endpoint and refreshes the table on
// success. Re-uses the deployment_id (not the hash) so the audit trail
// records exactly which historical row was restored. Pulls the target's
// snapshot so the confirm dialog can preview env + settings changes.
const rollbackTo = async (d) => {
  if (!fnId.value || !d?.id || rollingBack.value) return
  const shortHash = (d.code_hash || '').slice(0, 12)

  let diffMessage = `Code hash ${shortHash}. Current ${activeFn.value ? 'v' + activeFn.value.version : 'version'} stays in history.`
  try {
    const fullDep = await getDeployment(d.id)
    const snap = fullDep?.data?.snapshot
    if (snap && activeFn.value) {
      const lines = describeRollbackDiff(activeFn.value, snap)
      if (lines.length) {
        diffMessage = `Rolling back to v${d.version} (code ${shortHash}) will also change:\n\n${lines.join('\n')}\n\nSecrets keep their current values — they aren't part of the rollback.`
      } else {
        diffMessage = `Rolling back to v${d.version} (code ${shortHash}). Settings and env are already identical, so only the code changes.`
      }
    }
  } catch (e) {
    // fall through to default message
  }

  const ok = await confirmStore.ask({
    title: `Restore v${d.version}?`,
    message: diffMessage,
    confirmLabel: 'Rollback',
  })
  if (!ok) return
  rollingBack.value = true
  try {
    await rollbackFunction(fnId.value, { deployment_id: d.id })
    await refresh()
  } catch (err) {
    const code = err.response?.data?.error?.code || ''
    const msg = err.response?.data?.error?.message || err.message || 'Rollback failed'
    if (code === 'VERSION_GCD') {
      confirmStore.notify({ title: 'Version unavailable', message: `This version has been garbage-collected and can no longer be restored.\n\n${msg}`, danger: true })
    } else {
      confirmStore.notify({ title: 'Rollback failed', message: msg, danger: true })
    }
  } finally {
    rollingBack.value = false
  }
}

// A row is "active" when its version matches the function's current version
// AND the deployment succeeded. (A failed deploy that came after a succeeded
// one doesn't take over.)
const isActive = (d) =>
  activeFn.value &&
  d.version === activeFn.value.version &&
  d.status === 'succeeded'

const drawerOpen = ref(false)
const selected = ref(null)
const logLines = ref([])
const streamConnected = ref(false)
let activeStream = null

const drawerTitle = computed(() =>
  selected.value ? `Deployment · ${selected.value.id?.substring(0, 14)}` : 'Deployment'
)
const logText = computed(() => logLines.value.join('\n'))

const formatTime = (ts) => (ts ? new Date(ts).toLocaleString() : EMPTY)

const Stat = {
  props: { label: String, value: [String, Number], mono: Boolean },
  setup(p) {
    return () =>
      h('div', { class: 'bg-surface border border-border rounded p-3' }, [
        h('div', { class: 'text-[10px] uppercase tracking-wider text-foreground-muted mb-1' }, p.label),
        h('div', { class: ['text-sm text-white', p.mono && 'font-mono text-xs'].filter(Boolean) }, String(p.value)),
      ])
  },
}

const resolveFn = async () => {
  const res = await listFunctions()
  const fn = (res.data.functions || []).find((f) => f.name === fnName.value)
  if (!fn) throw new Error(`Function "${fnName.value}" not found`)
  return fn
}

const refresh = async () => {
  loading.value = true
  error.value = ''
  try {
    const fn = await resolveFn()
    fnId.value = fn.id
    activeFn.value = fn
    const res = await listDeployments(fnId.value, 100)
    deployments.value = res.data.deployments || []
  } catch (err) {
    error.value = err.message || 'Failed to load deployments'
  } finally {
    loading.value = false
  }
}

const open = async (d) => {
  selected.value = d
  drawerOpen.value = true
  logLines.value = []
  streamConnected.value = false

  // Load the full record + initial log dump.
  try {
    const detail = await getDeployment(d.id)
    selected.value = { ...d, ...detail.data }
  } catch {}
  try {
    const logs = await getDeploymentLogs(d.id, 0, 1000)
    logLines.value = (logs.data.logs || []).map(formatLogLine)
  } catch {}

  // For a still-building deployment, attach an SSE stream for live tail.
  // Terminal deployments don't need streaming (history fetch was enough).
  if (d.status === 'queued' || d.status === 'building') {
    attachStream(d.id)
  }
}

const formatLogLine = (l) => `[${l.stream || 'log'}] ${l.line}`

const attachStream = (id) => {
  closeStream()
  const es = new EventSource(`/api/v1/deployments/${id}/stream`)
  activeStream = es
  streamConnected.value = true
  es.addEventListener('log', (e) => {
    try {
      const line = JSON.parse(e.data)
      logLines.value.push(formatLogLine(line))
    } catch {}
  })
  es.addEventListener('succeeded', () => closeStream(true))
  es.addEventListener('failed', () => closeStream(true))
  es.onerror = () => {
    if (es.readyState === EventSource.CLOSED) closeStream()
  }
}

const closeStream = (refreshRow = false) => {
  if (activeStream) {
    try { activeStream.close() } catch {}
    activeStream = null
  }
  streamConnected.value = false
  if (refreshRow && selected.value) {
    // Pull final state once the stream terminates.
    getDeployment(selected.value.id)
      .then((res) => { selected.value = { ...selected.value, ...res.data } })
      .catch(() => {})
    refresh()
  }
}

watch(drawerOpen, (open) => { if (!open) closeStream() })

// Live updates: deployment events fire on every phase / status change of
// the build pipeline; function events fire on rollback retargets. Either
// is a reason to refresh this page. Coalesce both so a build that emits
// 4 phase events doesn't trigger 4 list fetches.
const events = useEventsStore()
let refreshTimer = null
const scheduleRefresh = () => {
  if (refreshTimer) return
  refreshTimer = setTimeout(() => {
    refreshTimer = null
    refresh()
  }, 300)
}
let unsubDep = null
let unsubFn = null

onMounted(() => {
  refresh()
  unsubDep = events.subscribe('deployment', scheduleRefresh)
  unsubFn = events.subscribe('function', scheduleRefresh)
})
onBeforeUnmount(() => {
  closeStream()
  if (unsubDep) { unsubDep(); unsubDep = null }
  if (unsubFn) { unsubFn(); unsubFn = null }
  if (refreshTimer) { clearTimeout(refreshTimer); refreshTimer = null }
})
</script>
