<template>
  <div class="space-y-6">
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
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
      <div class="flex flex-wrap items-center gap-2 sm:shrink-0">
        <Button
          variant="secondary"
          @click="$router.push(`/functions/${fnName}`)"
        >
          <UploadCloud class="w-4 h-4 mr-2" />
          New version
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
      class="border-y border-border py-3 flex items-start sm:items-center gap-3"
    >
      <CheckCircle2 class="w-4 h-4 text-success-fg shrink-0" />
      <div class="flex-1 min-w-0">
        <div class="flex items-center gap-2 flex-wrap">
          <span class="text-sm text-white font-medium">Currently serving</span>
          <span
            v-if="liveVersion"
            class="text-xs px-2 py-0.5 rounded bg-success-tint text-success-fg border border-success-ring font-mono"
          >
            v{{ liveVersion }}
          </span>
          <span
            v-if="activeFn.status !== 'active'"
            class="text-xs px-2 py-0.5 rounded bg-warning-tint text-warning-fg border border-warning-ring"
          >
            status: {{ activeFn.status }}
          </span>
        </div>
        <!-- Wraps rather than truncates: on anything narrower than a wide
             desktop the single clipped line hid runtime and updated-at
             completely, and a 64-char hash ate the whole row on a phone.
             Full hash stays available on hover. -->
        <div class="text-xs text-foreground-muted mt-1 font-mono">
          hash: <span :title="activeFn.code_hash || ''">{{ activeShortHash }}</span> · runtime: {{ runtimeLabel(activeFn.runtime) }} · updated {{ formatTime(activeFn.updated_at) }}
        </div>
      </div>
    </div>

    <div
      v-if="error"
      class="bg-danger-tint border border-danger-ring rounded p-3 text-xs text-danger-fg"
      role="alert"
    >
      {{ error }}
    </div>

    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <!-- Mobile (<sm) stacked-row list. -->
      <ul class="sm:hidden divide-y divide-border">
        <li
          v-for="d in deployments"
          :key="d.id"
          class="px-4 py-3 transition-colors"
          :class="isActive(d) ? 'bg-success-tint/40' : ''"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <!-- The card body is the drawer trigger. It has to be a real
                   button: the @click used to sit on the bare <li>, which put
                   the drawer out of reach of the keyboard entirely. It cannot
                   wrap the whole row, because the row carries its own links. -->
              <button
                type="button"
                class="touch-expand-sm w-full text-left cursor-pointer rounded-sm active:bg-surface-hover/50 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
                :aria-label="detailLabel(d)"
                @click="open(d)"
              >
                <div class="flex items-center gap-2 flex-wrap">
                  <span
                    class="font-mono text-xs"
                    :class="isActive(d) ? 'text-white font-semibold' : 'text-foreground-muted'"
                  >v{{ d.version }}</span>
                  <StatusBadge
                    v-if="d.status !== 'succeeded'"
                    :status="d.status"
                  />
                  <span
                    v-if="isActive(d)"
                    class="px-1.5 py-0.5 rounded text-xs bg-success text-background font-semibold"
                  >Live</span>
                </div>
                <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-foreground-muted">
                  <span class="font-mono">{{ (d.code_hash || '').slice(0, 10) || EMPTY }}</span>
                  <span v-if="sameCodeAsLive(d) && !isActive(d)">same code as live</span>
                  <span>{{ formatTime(d.submitted_at) }}</span>
                </div>
              </button>
            </div>
            <div class="shrink-0 flex items-center gap-1">
              <!-- touch-expand-xs only bites on coarse pointers, so this
                   matches the 44px floor of the Button beside it without
                   growing the row for a mouse. -->
              <router-link
                v-if="canCompare(d)"
                :to="{ name: 'function-diff', params: { name: fnName }, query: { from: d.id, to: activeDeploymentId } }"
                class="text-foreground-muted hover:text-white text-xs flex items-center gap-1 px-2 py-1 rounded-sm touch-expand-xs focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
                :title="`Compare v${d.version} with the live version`"
              >
                <GitCompare class="w-3 h-3" /> Compare
              </router-link>
              <Button
                v-if="canRollback(d)"
                size="xs"
                variant="ghost"
                :disabled="rollingBack"
                @click="rollbackTo(d)"
              >
                <RotateCcw class="w-3 h-3" /> Rollback
              </Button>
            </div>
          </div>
        </li>
        <li
          v-if="!loading && deployments.length === 0"
          class="px-6 py-8 text-center text-sm text-foreground-muted"
        >
          No deployments yet.
        </li>
      </ul>

      <table class="hidden sm:table w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th class="px-6 py-3 font-medium">
              Version
            </th>
            <th class="px-6 py-3 font-medium hidden md:table-cell">
              Code
            </th>
            <th class="px-6 py-3 font-medium">
              Status
            </th>
            <th class="px-6 py-3 font-medium hidden lg:table-cell">
              Submitted
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
            :class="isActive(d) ? 'bg-success-tint/40' : ''"
            @click="open(d)"
          >
            <td class="px-6 py-4 font-mono text-xs">
              <div class="flex items-center gap-2">
                <!-- Row clicks stay on the <tr> for the mouse, but the drawer
                     needs a real control to be reachable by keyboard. Rendered
                     bare, so the desktop cell looks exactly as it did. -->
                <button
                  type="button"
                  class="touch-expand-xs text-white text-left rounded-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
                  :class="isActive(d) ? 'font-semibold' : 'text-foreground-muted'"
                  :aria-label="detailLabel(d)"
                  @click.stop="open(d)"
                >
                  {{ 'v' + d.version }}
                </button>
                <span
                  v-if="isActive(d)"
                  class="px-1.5 py-0.5 rounded text-xs bg-success text-background font-semibold normal-case"
                >Live</span>
              </div>
            </td>
            <td class="px-6 py-4 font-mono text-xs hidden md:table-cell">
              <span
                class="text-foreground-muted"
                :title="d.code_hash || ''"
              >{{ (d.code_hash || '').slice(0, 10) || EMPTY }}</span>
              <span
                v-if="sameCodeAsLive(d) && !isActive(d)"
                class="ml-2 normal-case text-foreground-muted/70 font-sans"
              >same as live</span>
            </td>
            <td class="px-6 py-4">
              <!-- A succeeded deployment is the unremarkable case and there are
                   usually ten of them, so a green badge on every row competed
                   with the green that means "this is what is running". Only a
                   status worth reacting to gets the badge. -->
              <StatusBadge
                v-if="d.status !== 'succeeded'"
                :status="d.status"
              />
              <span
                v-else
                class="text-xs text-foreground-muted"
              >succeeded</span>
            </td>
            <td class="px-6 py-4 text-foreground hidden lg:table-cell">
              {{ formatTime(d.submitted_at) }}
            </td>
            <td
              class="px-6 py-4 text-right text-xs"
              @click.stop
            >
              <div class="inline-flex items-center gap-2 justify-end">
                <router-link
                  v-if="canCompare(d)"
                  :to="{ name: 'function-diff', params: { name: fnName }, query: { from: d.id, to: activeDeploymentId } }"
                  class="touch-expand-xs text-foreground-muted hover:text-white flex items-center gap-1 rounded-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
                  :title="`Compare v${d.version} with the live version, v${liveVersion}`"
                >
                  <GitCompare class="w-3 h-3" /> Compare with live
                </router-link>
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
                  v-if="isActive(d)"
                  class="text-foreground-muted/60"
                >serving now</span>
                <span
                  v-else-if="!canCompare(d) && !canRollback(d)"
                  class="text-foreground-muted/30"
                >{{ EMPTY }}</span>
              </div>
            </td>
          </tr>
          <tr v-if="!loading && deployments.length === 0">
            <td
              colspan="5"
              class="px-6 py-8 text-center text-foreground-muted"
            >
              No deployments yet.
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <Drawer
      v-model="drawerOpen"
      :title="drawerTitle"
      width="640px"
    >
      <div
        v-if="!selected"
        class="p-6 text-sm text-foreground-muted"
      >
        Nothing selected.
      </div>
      <div
        v-else
        class="p-5 space-y-4"
      >
        <div class="flex items-center gap-2 flex-wrap">
          <StatusBadge :status="selected.status" />
          <span
            v-if="selected.phase"
            class="inline-flex items-center px-2.5 py-1 rounded text-xs border bg-background font-mono text-foreground-muted"
          >
            {{ selected.phase }}
          </span>
        </div>

        <!-- Origin, code hash and deployment id moved here from the table.
             In a row they were noise on every line; here they are the detail
             an operator opens the drawer for. -->
        <div class="grid grid-cols-2 gap-3 text-sm">
          <Stat
            label="Version"
            :value="`v${selected.version}`"
            mono
          />
          <Stat
            label="Code hash"
            :value="(selected.code_hash || '').slice(0, 12) || EMPTY"
            mono
          />
          <Stat
            label="Deployment ID"
            :value="selected.id || EMPTY"
            mono
          />
          <Stat
            label="Duration"
            :value="selected.duration_ms != null ? selected.duration_ms + ' ms' : EMPTY"
          />
          <Stat
            label="Submitted"
            :value="formatTime(selected.submitted_at)"
          />
          <Stat
            label="Finished"
            :value="selected.finished_at ? formatTime(selected.finished_at) : EMPTY"
          />
        </div>

        <div v-if="selected.error_message">
          <h3 class="text-xs uppercase tracking-wider text-foreground-muted mb-2">
            Error
          </h3>
          <pre class="bg-danger-tint border border-danger-ring rounded p-3 text-xs text-danger-fg font-mono whitespace-pre-wrap break-words">{{ selected.error_message }}</pre>
        </div>

        <div>
          <div class="flex items-center justify-between mb-2">
            <h3 class="text-xs uppercase tracking-wider text-foreground-muted">
              Build log
            </h3>
            <span
              v-if="streamConnected"
              class="text-xs text-success-fg"
            >live</span>
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
defineOptions({ name: 'DeploymentsView' })

import { EMPTY } from '@/utils/format'
import { ref, computed, onMounted, onBeforeUnmount, watch, h } from 'vue'
import { useEventsStore } from '@/stores/events'
import { useRoute } from 'vue-router'
import { RefreshCw, UploadCloud, CheckCircle2, RotateCcw, GitCompare } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import Drawer from '@/components/common/Drawer.vue'
import StatusBadge from '@/components/common/StatusBadge.vue'
import { listDeployments, getDeployment, getDeploymentLogs, listFunctions, rollbackFunction } from '@/api/endpoints'
import { useConfirmStore } from '@/stores/confirm'
import { describeSnapshotDiff } from '@/utils/rollbackDiff'
import { runtimeLabel } from '@/utils/runtime'

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

// activeDeploymentId: the row that's currently serving. Used as the
// implicit "compare against" target for every Compare link in the list.
// Null until refresh() lands the deployments + activeFn pair.
// The version actually serving. functions.version is a monotonic counter for
// numbering new deployments, so after promoting an older version the two
// deliberately disagree and only this one is true.
const liveVersion = computed(() => {
  const row = deployments.value.find((d) => isActive(d))
  return row ? row.version : null
})

const activeDeploymentId = computed(() => {
  const row = deployments.value.find((d) => isActive(d))
  return row?.id || null
})

// canCompare: any succeeded non-active row whose source is still on disk
// — we have an active version to compare against AND the row points at
// a real code_hash. Mirrors canRollback but stays available even when
// rollingBack is in flight (compare is read-only).
const canCompare = (d) =>
  d &&
  d.status === 'succeeded' &&
  d.code_hash &&
  !isActive(d) &&
  activeDeploymentId.value

// rollbackTo posts to the rollback endpoint and refreshes the table on
// success. Re-uses the deployment_id (not the hash) so the audit trail
// records exactly which historical row was restored. Pulls the target's
// snapshot so the confirm dialog can preview env + settings changes.
const rollbackTo = async (d) => {
  if (!fnId.value || !d?.id || rollingBack.value) return
  const shortHash = (d.code_hash || '').slice(0, 12)

  // Whether the code itself moves. Redeploying unchanged code writes a new
  // deployment row with a new version and the same hash, so a row can be a
  // different version of the same code — in which case rollback restores its
  // settings and env, and saying "only the code changes" would be backwards.
  const codeChanges = !!activeFn.value?.code_hash && d.code_hash !== activeFn.value.code_hash

  let diffMessage = `v${d.version} becomes the live version. Code ${shortHash}.`
  let settingsLines = []
  let knowSettings = false
  try {
    const fullDep = await getDeployment(d.id)
    const snap = fullDep?.data?.snapshot
    if (snap && activeFn.value) {
      knowSettings = true
      settingsLines = describeSnapshotDiff(activeFn.value, snap)
    }
  } catch {
    // Fall through to the generic message; the API is still the authority.
  }

  if (knowSettings) {
    if (settingsLines.length && codeChanges) {
      diffMessage = `v${d.version} becomes the live version. Code changes to ${shortHash}, and:\n\n${settingsLines.join('\n')}\n\nSecrets keep their current values.`
    } else if (settingsLines.length) {
      diffMessage = `v${d.version} becomes the live version. Its code is the same as the version running now, so only settings change:\n\n${settingsLines.join('\n')}\n\nSecrets keep their current values.`
    } else if (codeChanges) {
      diffMessage = `v${d.version} becomes the live version. Only the code changes, to ${shortHash}; settings and env already match.`
    } else {
      // Nothing at all would move, and the API refuses it. Say so here rather
      // than sending a request that can only come back as an error.
      confirmStore.notify({
        title: `v${d.version} is identical to the live version`,
        message: `v${d.version} has the same code and the same settings as v${liveVersion.value}, which is serving now, so making it live would change nothing.`,
      })
      return
    }
  }

  const ok = await confirmStore.ask({
    title: `Make v${d.version} live?`,
    message: diffMessage,
    confirmLabel: 'Make live',
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

// Rollback is append-only: restoring v9 does not make v9 active again, it
// creates a NEW version whose content is v9's. That is the right model (history
// stays immutable, and Heroku/Kubernetes behave the same way), but the page
// used to show only a flat list of version numbers, so an operator who rolled
// back saw a version they had never deployed become active with nothing
// explaining where it came from. These three helpers exist to say it out loud.

// sameCodeAsLive marks versions whose code is byte-identical to what is running.
// Redeploys of unchanged code are common, so a version list can be ten rows of
// the same code; without this the operator cannot tell which rollback would
// actually change the handler and which only moves settings.
const sameCodeAsLive = (d) =>
  !!d?.code_hash && !!activeFn.value?.code_hash && d.code_hash === activeFn.value.code_hash

// Which deployment is live is a fact the server records, not something to
// infer. It used to be "version == the function's version", which only worked
// because rollback appended a row; now rollback promotes an existing
// deployment and the function's version counter stays where it is.
const isActive = (d) =>
  !!activeFn.value?.active_deployment_id &&
  d.id === activeFn.value.active_deployment_id

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

// 12 hex chars is what the editor version list, the diff view and the rollback
// confirm all show; the banner was the only place printing all 64.
const activeShortHash = computed(() => {
  const hash = activeFn.value?.code_hash || ''
  return hash ? hash.slice(0, 12) : EMPTY
})

// Accessible name for the row trigger, since the visible label is just "v3".
// Skips the timestamp when there is none rather than reading out EMPTY's dash.
const detailLabel = (d) => {
  const when = d.submitted_at ? `, submitted ${formatTime(d.submitted_at)}` : ''
  return `Open deployment v${d.version}, ${d.status}${when}`
}

const Stat = {
  props: { label: String, value: [String, Number], mono: Boolean },
  setup(p) {
    return () =>
      h('div', { class: 'bg-surface border border-border rounded p-3' }, [
        h('div', { class: 'text-xs uppercase tracking-wider text-foreground-muted mb-1' }, p.label),
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
