<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-white tracking-tight">
          Activity
        </h1>
        <p class="text-xs text-foreground-muted mt-1">
          Live feed of every API call hitting Orva — UI clicks, REST/SDK,
          MCP tools, webhook deliveries.
        </p>
      </div>
      <div class="flex items-center gap-2">
        <span
          class="text-[11px] inline-flex items-center gap-1.5"
          :class="paused ? 'text-amber-400' : (eventsStore.connected ? 'text-emerald-400' : 'text-foreground-muted')"
        >
          <span
            class="w-1.5 h-1.5 rounded-full"
            :class="paused ? 'bg-amber-400' : (eventsStore.connected ? 'bg-emerald-400 animate-pulse' : 'bg-foreground-muted')"
          />
          {{ paused ? 'Paused' : (eventsStore.connected ? 'Live' : 'Reconnecting…') }}
        </span>
        <Button
          variant="secondary"
          size="xs"
          @click="paused = !paused"
        >
          <Pause v-if="!paused" class="w-3.5 h-3.5" />
          <Play v-else class="w-3.5 h-3.5" />
          {{ paused ? 'Resume' : 'Pause' }}
        </Button>
        <Button
          variant="secondary"
          size="xs"
          @click="refresh"
        >
          <RefreshCw class="w-3.5 h-3.5" :class="{ 'animate-spin': loading }" />
          Refresh
        </Button>
      </div>
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
        @click="filters.source = opt.value; refresh()"
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
        @click="filters.statusBucket = opt.value; refresh()"
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
        @click="filters.range = opt.value; refresh()"
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

    <div v-if="hasMore" class="flex justify-center">
      <Button variant="secondary" size="xs" :loading="loading" @click="loadMore">
        Load more
      </Button>
    </div>

    <!-- Detail drawer -->
    <Drawer v-model="drawerOpen" :title="drawerTitle" width="640px">
      <div v-if="drawerRow" class="space-y-5 text-sm">
        <div class="grid grid-cols-2 gap-3">
          <div>
            <div class="text-[10px] uppercase tracking-wide text-foreground-muted">Time</div>
            <div class="text-white font-mono mt-0.5">{{ formatFullTime(drawerRow.ts) }}</div>
          </div>
          <div>
            <div class="text-[10px] uppercase tracking-wide text-foreground-muted">Source</div>
            <div class="mt-0.5"><SourceTag :source="drawerRow.source" /></div>
          </div>
          <div>
            <div class="text-[10px] uppercase tracking-wide text-foreground-muted">Actor</div>
            <div class="text-white mt-0.5">{{ drawerRow.actor_label || '—' }}</div>
            <div v-if="drawerRow.actor_id" class="text-[11px] text-foreground-muted font-mono">{{ drawerRow.actor_id }}</div>
          </div>
          <div>
            <div class="text-[10px] uppercase tracking-wide text-foreground-muted">Status</div>
            <div class="mt-0.5">
              <StatusBadge v-if="drawerRow.status" :status="statusLabel(drawerRow.status)" />
              <span v-else class="text-foreground-muted text-xs">—</span>
              <span v-if="drawerRow.status" class="ml-2 text-xs text-foreground-muted font-mono">{{ drawerRow.status }}</span>
            </div>
          </div>
          <div>
            <div class="text-[10px] uppercase tracking-wide text-foreground-muted">Method</div>
            <div class="text-white font-mono mt-0.5">{{ drawerRow.method || '—' }}</div>
          </div>
          <div>
            <div class="text-[10px] uppercase tracking-wide text-foreground-muted">Duration</div>
            <div class="text-white font-mono mt-0.5">{{ formatDuration(drawerRow.duration_ms) }}</div>
          </div>
        </div>

        <div>
          <div class="text-[10px] uppercase tracking-wide text-foreground-muted mb-1">Path / Tool</div>
          <div class="bg-surface border border-border rounded px-3 py-2 text-xs text-white font-mono break-all">
            {{ drawerRow.path || '—' }}
          </div>
        </div>

        <div>
          <div class="text-[10px] uppercase tracking-wide text-foreground-muted mb-1">Summary</div>
          <div class="text-foreground">{{ drawerRow.summary || '—' }}</div>
        </div>

        <div v-if="drawerRow.request_id">
          <div class="text-[10px] uppercase tracking-wide text-foreground-muted mb-1">Request ID</div>
          <div class="text-xs font-mono text-foreground-muted break-all">{{ drawerRow.request_id }}</div>
        </div>

        <div v-if="prettyMetadata">
          <div class="text-[10px] uppercase tracking-wide text-foreground-muted mb-1">Metadata</div>
          <pre class="bg-surface border border-border rounded p-3 text-xs text-foreground font-mono overflow-auto max-h-72 whitespace-pre-wrap break-words">{{ prettyMetadata }}</pre>
        </div>
      </div>
    </Drawer>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, onActivated, onDeactivated } from 'vue'
import { Search, RefreshCw, Pause, Play } from 'lucide-vue-next'
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
const rows = ref([])
const loading = ref(false)
const paused = ref(false)
const hasMore = ref(false)
const cursor = ref(0)
const drawerOpen = ref(false)
const drawerRow = ref(null)

const MAX_LIVE_ROWS = 1000

// ── Counts (per-source pill suffix) ────────────────────────────────
const counts = computed(() => {
  const out = {}
  for (const r of rows.value) {
    out[r.source] = (out[r.source] || 0) + 1
  }
  out[''] = rows.value.length
  return out
})

// ── Fetching history (initial + on filter change + Load more) ──────
const buildParams = (extra = {}) => {
  const p = { limit: 200 }
  if (filters.value.source) p.source = filters.value.source
  if (filters.value.statusBucket === 'err') p.status_min = 400
  if (filters.value.q) p.q = filters.value.q
  const since = rangeMS(filters.value.range)
  if (since) p.since = Date.now() - since
  return Object.assign(p, extra)
}

const rangeMS = (key) => {
  switch (key) {
    case '5m':  return 5 * 60_000
    case '1h':  return 60 * 60_000
    case '24h': return 24 * 60 * 60_000
    case '7d':  return 7 * 24 * 60 * 60_000
    default:    return 0
  }
}

const refresh = async () => {
  loading.value = true
  try {
    const res = await listActivity(buildParams())
    rows.value = res.data?.rows || []
    cursor.value = res.data?.next_cursor || 0
    hasMore.value = cursor.value > 0
  } finally {
    loading.value = false
  }
}

const loadMore = async () => {
  if (!cursor.value) return
  loading.value = true
  try {
    const res = await listActivity(buildParams({ cursor: cursor.value }))
    rows.value = rows.value.concat(res.data?.rows || [])
    cursor.value = res.data?.next_cursor || 0
    hasMore.value = cursor.value > 0
  } finally {
    loading.value = false
  }
}

// ── Live subscription via SSE ───────────────────────────────────────
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
  if (paused.value) return
  if (!matchesFilters(row)) return
  rows.value.unshift(row)
  if (rows.value.length > MAX_LIVE_ROWS) {
    rows.value = rows.value.slice(0, MAX_LIVE_ROWS)
  }
}

// ── Search debounce ─────────────────────────────────────────────────
let searchTimer = null
const onSearchInput = () => {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(refresh, 250)
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
  refresh()
})
onUnmounted(unsubscribeLive)
onActivated(() => { subscribeLive(); refresh() })
onDeactivated(unsubscribeLive)
</script>
