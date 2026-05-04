<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-xl font-semibold text-white tracking-tight">
        Activity
      </h1>
      <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-relaxed">
        Live feed of every API call hitting Orva — UI clicks, REST/SDK,
        MCP tools, webhook deliveries.
      </p>
    </div>

    <!-- Filter strip -->
    <div class="flex items-center gap-2 flex-wrap">
      <div class="relative flex-1 min-w-[260px] max-w-[420px]">
        <Search class="w-3.5 h-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-foreground-muted/60 pointer-events-none" />
        <input
          v-model="filters.q"
          placeholder="Search path, summary, actor…"
          class="w-full bg-background border border-border rounded-md pl-8 pr-3 py-1.5 text-xs text-foreground placeholder-foreground-muted/60 focus:outline-none focus:border-white"
          @input="onSearchInput"
        >
      </div>

      <Button
        v-for="opt in sourceOptions"
        :key="opt.value"
        variant="chip"
        size="xs"
        :active="filters.source === opt.value"
        @click="filters.source = opt.value; reset()"
      >
        {{ opt.label }}
        <span
          v-if="counts[opt.value] != null && opt.value !== ''"
          class="ml-1 opacity-60"
        >{{ counts[opt.value] }}</span>
      </Button>

      <span class="text-foreground-muted/40">·</span>

      <Button
        v-for="opt in statusOptions"
        :key="opt.value"
        variant="chip"
        size="xs"
        :active="filters.statusBucket === opt.value"
        @click="filters.statusBucket = opt.value; reset()"
      >
        {{ opt.label }}
      </Button>

      <span class="text-foreground-muted/40">·</span>

      <Button
        v-for="opt in rangeOptions"
        :key="opt.value"
        variant="chip"
        size="xs"
        :active="filters.range === opt.value"
        @click="filters.range = opt.value; reset()"
      >
        {{ opt.label }}
      </Button>
    </div>

    <!-- Table -->
    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <table class="w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th class="px-4 py-3 w-32">Time</th>
            <th class="px-4 py-3 w-24">Source</th>
            <th class="px-4 py-3 w-40 hidden md:table-cell">Actor</th>
            <th class="px-4 py-3 w-20 hidden sm:table-cell">Method</th>
            <th class="px-4 py-3">Path / Tool</th>
            <th class="px-4 py-3 w-16 hidden sm:table-cell">Status</th>
            <th class="px-4 py-3 w-20 hidden lg:table-cell">Duration</th>
            <th class="px-4 py-3 hidden xl:table-cell">Summary</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr
            v-for="row in rows"
            :key="rowKey(row)"
            class="hover:bg-surface/40 cursor-pointer transition-colors"
            @click="openDrawer(row)"
          >
            <td class="px-4 py-2.5 font-mono text-xs text-foreground-muted">
              {{ formatTime(row.ts) }}
            </td>
            <td class="px-4 py-2.5">
              <SourceTag :source="row.source" />
            </td>
            <td class="px-4 py-2.5 hidden md:table-cell">
              <div class="text-xs text-white truncate max-w-[200px]">
                {{ row.actor_label || row.actor_id || '—' }}
              </div>
              <div
                v-if="row.actor_label && row.actor_id && row.actor_label !== row.actor_id"
                class="text-[10px] text-foreground-muted/70 font-mono truncate"
              >
                {{ row.actor_id }}
              </div>
            </td>
            <td class="px-4 py-2.5 text-xs font-mono text-foreground-muted hidden sm:table-cell">
              {{ row.method || '—' }}
            </td>
            <td class="px-4 py-2.5 text-xs font-mono text-white truncate max-w-[440px]">
              {{ row.path || '—' }}
            </td>
            <td class="px-4 py-2.5 hidden sm:table-cell">
              <StatusBadge
                v-if="row.status"
                :status="statusLabel(row.status)"
              />
              <span v-else class="text-foreground-muted text-xs">—</span>
            </td>
            <td class="px-4 py-2.5 text-xs font-mono text-foreground-muted hidden lg:table-cell">
              {{ formatDuration(row.duration_ms) }}
            </td>
            <td class="px-4 py-2.5 text-xs text-foreground-muted truncate max-w-[280px] hidden xl:table-cell">
              {{ row.summary }}
            </td>
          </tr>
          <tr v-if="!rows.length">
            <td colspan="8" class="px-4 py-12 text-center text-foreground-muted text-sm">
              No activity yet. Drive any action — open the dashboard,
              call a function, fire an MCP tool — and rows will land here.
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Pagination: numbered pages over the historical query. Live SSE
         prepends new rows on page 1 only — when the operator paginates
         back, we stop appending live rows (the page they're reading
         must stay stable) but the live tail keeps building in memory
         and rejoins the top when they come back. -->
    <div
      v-if="totalPages > 1"
      class="flex items-center justify-between text-xs"
    >
      <div class="text-foreground-muted">
        Page {{ page }} of {{ totalPages }} · {{ totalKnown }}{{ hasMoreBeyondKnown ? '+' : '' }} rows
      </div>
      <div class="flex items-center gap-1">
        <Button
          variant="secondary"
          size="xs"
          :disabled="page <= 1"
          @click="goToPage(page - 1)"
        >
          <ChevronLeft class="w-3.5 h-3.5" />
          Prev
        </Button>
        <Button
          v-for="p in visiblePages"
          :key="p"
          :variant="p === page ? 'primary' : 'secondary'"
          size="xs"
          @click="goToPage(p)"
        >
          {{ p }}
        </Button>
        <Button
          variant="secondary"
          size="xs"
          :disabled="page >= totalPages && !hasMoreBeyondKnown"
          @click="goToPage(page + 1)"
        >
          Next
          <ChevronRight class="w-3.5 h-3.5" />
        </Button>
      </div>
    </div>

    <!-- Detail drawer -->
    <Drawer v-model="drawerOpen" :title="drawerTitle" width="640px">
      <div v-if="drawerRow" class="p-5 space-y-5 text-sm">
        <!-- Stat grid — 2 cols, mirrors InvocationsLog drawer for visual parity. -->
        <div class="grid grid-cols-2 gap-3">
          <div class="bg-surface border border-border rounded p-3 min-w-0">
            <div class="text-[10px] uppercase tracking-wider text-foreground-muted mb-1">Time</div>
            <div class="text-xs text-white font-mono truncate">{{ formatFullTime(drawerRow.ts) }}</div>
          </div>
          <div class="bg-surface border border-border rounded p-3 min-w-0">
            <div class="text-[10px] uppercase tracking-wider text-foreground-muted mb-1">Source</div>
            <SourceTag :source="drawerRow.source" />
          </div>
          <div class="bg-surface border border-border rounded p-3 min-w-0">
            <div class="text-[10px] uppercase tracking-wider text-foreground-muted mb-1">Actor</div>
            <div class="text-sm text-white truncate">{{ drawerRow.actor_label || '—' }}</div>
            <div v-if="drawerRow.actor_id" class="text-[11px] text-foreground-muted font-mono truncate mt-0.5">{{ drawerRow.actor_id }}</div>
          </div>
          <div class="bg-surface border border-border rounded p-3 min-w-0">
            <div class="text-[10px] uppercase tracking-wider text-foreground-muted mb-1">Status</div>
            <div class="flex items-center gap-2">
              <StatusBadge v-if="drawerRow.status" :status="statusLabel(drawerRow.status)" />
              <span v-else class="text-foreground-muted text-xs">—</span>
              <span v-if="drawerRow.status" class="text-xs text-foreground-muted font-mono">HTTP {{ drawerRow.status }}</span>
            </div>
          </div>
          <div class="bg-surface border border-border rounded p-3 min-w-0">
            <div class="text-[10px] uppercase tracking-wider text-foreground-muted mb-1">Method</div>
            <div class="text-xs text-white font-mono truncate">{{ drawerRow.method || '—' }}</div>
          </div>
          <div class="bg-surface border border-border rounded p-3 min-w-0">
            <div class="text-[10px] uppercase tracking-wider text-foreground-muted mb-1">Duration</div>
            <div class="text-xs text-white font-mono">{{ formatDuration(drawerRow.duration_ms) }}</div>
          </div>
        </div>

        <!-- Path / tool — full-width -->
        <div>
          <div class="text-xs uppercase tracking-wider text-foreground-muted mb-2">Path / Tool</div>
          <pre class="bg-surface border border-border rounded p-3 text-xs text-white font-mono whitespace-pre-wrap break-all">{{ drawerRow.path || '—' }}</pre>
        </div>

        <!-- Summary -->
        <div>
          <div class="text-xs uppercase tracking-wider text-foreground-muted mb-2">Summary</div>
          <div class="text-foreground break-words">{{ drawerRow.summary || '—' }}</div>
        </div>

        <!-- Request id -->
        <div v-if="drawerRow.request_id">
          <div class="text-xs uppercase tracking-wider text-foreground-muted mb-2">Request ID</div>
          <pre class="bg-surface border border-border rounded p-3 text-xs text-foreground-muted font-mono whitespace-pre-wrap break-all">{{ drawerRow.request_id }}</pre>
        </div>

        <!-- Metadata JSON -->
        <div v-if="prettyMetadata">
          <div class="text-xs uppercase tracking-wider text-foreground-muted mb-2">Metadata</div>
          <pre class="bg-surface border border-border rounded p-3 text-xs text-foreground font-mono overflow-auto max-h-72 whitespace-pre-wrap break-words">{{ prettyMetadata }}</pre>
        </div>
      </div>
    </Drawer>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, onActivated, onDeactivated } from 'vue'
import { Search, ChevronLeft, ChevronRight } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import Drawer from '@/components/common/Drawer.vue'
import StatusBadge from '@/components/common/StatusBadge.vue'
import SourceTag from '@/components/common/SourceTag.vue'
import { listActivity } from '@/api/endpoints'
import { useEventsStore } from '@/stores/events'

const eventsStore = useEventsStore()

// ── Filters ─────────────────────────────────────────────────────────
const sourceOptions = [
  { label: 'All',      value: '' },
  { label: 'Web',      value: 'web' },
  { label: 'API',      value: 'api' },
  { label: 'MCP',      value: 'mcp' },
  { label: 'SDK',      value: 'sdk' },
  { label: 'Webhook',  value: 'webhook' },
  { label: 'Internal', value: 'internal' },
]
const statusOptions = [
  { label: 'All',     value: '' },
  { label: 'Success', value: 'ok' },
  { label: 'Errors',  value: 'err' },
]
const rangeOptions = [
  { label: '5m', value: '5m' },
  { label: '1h', value: '1h' },
  { label: '24h', value: '24h' },
  { label: '7d', value: '7d' },
]

const filters = ref({
  q: '',
  source: '',
  statusBucket: '',
  range: '24h',
})

// ── State ───────────────────────────────────────────────────────────
// historyRows  = rows fetched for the current page (server-side).
// liveRows     = rows that arrived via SSE while the user is on page 1.
//                Rendered ABOVE historyRows on page 1, dropped on
//                navigate-away to keep the visible page stable.
// page / pageSize / totalKnown drive numbered pagination. The server
// hands back next_cursor when more rows exist; we use it to detect
// whether the next page exists without forcing an upfront full count.
const historyRows = ref([])
const liveRows    = ref([])
const drawerOpen  = ref(false)
const drawerRow   = ref(null)
const totalKnown  = ref(0)
const hasMoreBeyondKnown = ref(false)

const PAGE_SIZE       = 100
const MAX_LIVE_BUFFER = 200

const page  = ref(1)
const pages = ref([{ since: undefined, until: undefined }])

// Visible rows = live (only on page 1) + paginated history.
const rows = computed(() => {
  if (page.value === 1) return [...liveRows.value, ...historyRows.value]
  return historyRows.value
})

// Counts pill suffix — based on the rows currently visible. Cheap.
const counts = computed(() => {
  const out = {}
  for (const r of rows.value) {
    out[r.source] = (out[r.source] || 0) + 1
  }
  out[''] = rows.value.length
  return out
})

// totalPages is a best-effort: we know about (pages loaded so far).
// If the server tells us there's more, we tack on a "+" indicator.
const totalPages = computed(() => Math.max(pages.value.length, page.value))
const visiblePages = computed(() => {
  // Show up to 5 pages around the current one, plus first + last.
  const t = totalPages.value
  const c = page.value
  const set = new Set([1, t, c - 1, c, c + 1])
  return [...set].filter((p) => p >= 1 && p <= t).sort((a, b) => a - b)
})

const rangeMS = (key) => {
  switch (key) {
    case '5m':  return 5 * 60_000
    case '1h':  return 60 * 60_000
    case '24h': return 24 * 60 * 60_000
    case '7d':  return 7 * 24 * 60 * 60_000
    default:    return 0
  }
}

const buildParams = (extra = {}) => {
  const p = { limit: PAGE_SIZE }
  if (filters.value.source) p.source = filters.value.source
  if (filters.value.statusBucket === 'err') p.status_min = 400
  if (filters.value.q) p.q = filters.value.q
  const since = rangeMS(filters.value.range)
  if (since) p.since = Date.now() - since
  return Object.assign(p, extra)
}

// goToPage swaps in the next page of history. Page 1 always uses no
// cursor (newest-first); page N uses pages[N-1].cursor (the next_cursor
// the server returned at the end of page N-1).
const goToPage = async (p) => {
  if (p < 1) return
  // We can only go forward as far as we have a recorded cursor for.
  if (p > pages.value.length + 1) return

  // Need to fetch page p? Always — we don't cache page rows because
  // live activity invalidates them anyway.
  const cursorForPage = pages.value[p - 1]?.cursor
  const res = await listActivity(buildParams(cursorForPage ? { cursor: cursorForPage } : {}))
  historyRows.value = res.data?.rows || []
  const next = res.data?.next_cursor || 0
  hasMoreBeyondKnown.value = next > 0

  // Record this page's bounds for back-nav and the next page's cursor.
  if (!pages.value[p - 1]) pages.value[p - 1] = {}
  pages.value[p - 1].cursor = cursorForPage
  if (next) {
    if (!pages.value[p]) pages.value[p] = {}
    pages.value[p].cursor = next
  } else {
    // No more pages — trim the recorded list.
    pages.value = pages.value.slice(0, p)
  }

  totalKnown.value = (p - 1) * PAGE_SIZE + historyRows.value.length

  page.value = p
  // On page > 1, freeze the live tail so the visible page stays stable.
  if (p > 1) liveRows.value = []
}

// resetAndReload returns the user to page 1 and re-runs the query
// from scratch with the current filters. Called after any filter
// change.
const reset = async () => {
  pages.value = [{ since: undefined, until: undefined }]
  liveRows.value = []
  page.value = 1
  await goToPage(1)
}

// Live tail.
let unsub = null

const matchesFilters = (row) => {
  if (filters.value.source && row.source !== filters.value.source) return false
  if (filters.value.statusBucket === 'err' && (row.status || 0) < 400) return false
  if (filters.value.q) {
    const q = filters.value.q.toLowerCase()
    const hay = (row.path + ' ' + row.summary + ' ' + row.actor_label).toLowerCase()
    if (!hay.includes(q)) return false
  }
  return true
}

const onLiveActivity = (row) => {
  if (!matchesFilters(row)) return
  // Only render live rows on page 1; on deeper pages the user's scroll
  // would jump if we prepended.
  if (page.value !== 1) return
  liveRows.value.unshift(row)
  if (liveRows.value.length > MAX_LIVE_BUFFER) {
    liveRows.value = liveRows.value.slice(0, MAX_LIVE_BUFFER)
  }
}

// Search debounce.
let searchTimer = null
const onSearchInput = () => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(reset, 250)
}

// ── Drawer ──────────────────────────────────────────────────────────
const openDrawer = (row) => {
  drawerRow.value = row
  drawerOpen.value = true
}
const drawerTitle = computed(() => {
  if (!drawerRow.value) return 'Activity'
  return drawerRow.value.summary || (drawerRow.value.method + ' ' + drawerRow.value.path)
})
const prettyMetadata = computed(() => {
  if (!drawerRow.value?.metadata) return ''
  try {
    return JSON.stringify(JSON.parse(drawerRow.value.metadata), null, 2)
  } catch {
    return drawerRow.value.metadata
  }
})

// ── Helpers ────────────────────────────────────────────────────────
const formatTime = (ms) => {
  if (!ms) return '—'
  const d = new Date(ms)
  return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
}
const formatFullTime = (ms) => {
  if (!ms) return '—'
  return new Date(ms).toLocaleString()
}
const formatDuration = (ms) => {
  if (ms == null) return '—'
  if (ms < 1) return '<1ms'
  if (ms < 1000) return ms + 'ms'
  return (ms / 1000).toFixed(2) + 's'
}
const statusLabel = (status) => {
  if (!status) return ''
  if (status >= 500) return 'error'
  if (status >= 400) return 'failed'
  if (status >= 200) return 'success'
  return 'pending'
}
const rowKey = (row) => row.id ? `db-${row.id}` : `live-${row.ts}-${row.request_id}-${row.path}`

// ── Lifecycle ──────────────────────────────────────────────────────
const subscribeLive = () => {
  if (unsub) return
  unsub = eventsStore.subscribe('activity', onLiveActivity)
  eventsStore.connect()
}
const unsubscribeLive = () => {
  if (unsub) { unsub(); unsub = null }
}

onMounted(() => {
  subscribeLive()
  reset()
})
onUnmounted(unsubscribeLive)
onActivated(() => { subscribeLive(); reset() })
onDeactivated(unsubscribeLive)
</script>
