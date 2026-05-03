<template>
  <div class="space-y-6">
    <!-- Page header — matches Activity / Invocations / FunctionsList. -->
    <div class="flex items-start justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-white tracking-tight">
          Traces
        </h1>
        <p class="text-xs text-foreground-muted mt-1 max-w-prose">
          Causal chains across HTTP, F2F invokes, jobs, cron, and inbound
          webhooks. One row per trace; click to see the full waterfall of
          spans.
        </p>
      </div>
      <Button
        variant="secondary"
        size="sm"
        :loading="loading"
        @click="refresh"
      >
        <RefreshCw class="w-3.5 h-3.5" />
        Refresh
      </Button>
    </div>

    <!-- Filter strip — search + chip groups, mirrors Activity.vue. -->
    <div class="flex items-center gap-2 flex-wrap">
      <div class="relative flex-1 min-w-[260px] max-w-[420px]">
        <Search class="w-3.5 h-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-foreground-muted/60 pointer-events-none" />
        <input
          v-model="fnFilter"
          placeholder="Filter by function id or name…"
          class="w-full bg-background border border-border rounded-md pl-8 pr-3 py-1.5 text-xs text-foreground placeholder-foreground-muted/60 focus:outline-none focus:border-white"
          @keydown.enter="refresh"
        >
      </div>

      <Button
        v-for="opt in statusOptions"
        :key="opt.value"
        variant="chip"
        size="xs"
        :active="statusFilter === opt.value"
        @click="setStatusFilter(opt.value)"
      >
        {{ opt.label }}
      </Button>

      <span class="text-foreground-muted/40">·</span>

      <Button
        variant="chip"
        size="xs"
        :active="outlierOnly"
        @click="toggleOutlier"
      >
        <Flag class="w-3 h-3" />
        Outliers only
      </Button>
    </div>

    <!-- Error / empty / loading -->
    <div
      v-if="error"
      class="rounded-md border border-red-700/40 bg-red-950/30 p-3 text-xs text-red-200"
    >
      {{ error }}
    </div>

    <div
      v-else-if="!traces.length && !loading"
      class="bg-background border border-border rounded-lg p-8 text-center text-sm text-foreground-muted"
    >
      <Network class="w-6 h-6 mx-auto mb-3 text-foreground-muted/50" />
      <p>No traces yet.</p>
      <p class="text-xs mt-1 text-foreground-muted/60">
        Hit a function over HTTP or fire a cron and they'll show up here.
      </p>
    </div>

    <!-- Table — same shell as Activity / Invocations. -->
    <div
      v-else
      class="bg-background border border-border rounded-lg overflow-x-auto"
    >
      <table class="w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th class="px-4 py-3 w-32">Time</th>
            <th class="px-4 py-3 w-40">Trace</th>
            <th class="px-4 py-3">Root function</th>
            <th class="px-4 py-3 w-28 hidden md:table-cell">Trigger</th>
            <th class="px-4 py-3 w-24 text-right hidden sm:table-cell">Duration</th>
            <th class="px-4 py-3 w-24">Status</th>
            <th class="px-4 py-3 w-10"></th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr
            v-for="t in traces"
            :key="t.trace_id"
            class="hover:bg-surface/40 cursor-pointer transition-colors"
            @click="openTrace(t.trace_id)"
          >
            <td class="px-4 py-2.5 font-mono text-xs text-foreground-muted whitespace-nowrap">
              {{ formatTime(t.started_at) }}
            </td>
            <td class="px-4 py-2.5 font-mono text-xs text-foreground-muted">
              {{ t.trace_id.slice(0, 11) }}
            </td>
            <td class="px-4 py-2.5 text-white">
              {{ t.function_name || t.root_function_id }}
            </td>
            <td class="px-4 py-2.5 hidden md:table-cell">
              <span class="inline-flex items-center px-2 py-0.5 rounded text-xs border bg-background font-mono text-foreground-muted border-border lowercase">
                {{ t.trigger || '—' }}
              </span>
            </td>
            <td class="px-4 py-2.5 text-right font-mono text-xs text-foreground-muted hidden sm:table-cell">
              {{ t.duration_ms != null ? `${t.duration_ms}ms` : '—' }}
            </td>
            <td class="px-4 py-2.5">
              <StatusBadge :status="t.status" />
            </td>
            <td class="px-4 py-2.5 text-right">
              <Flag
                v-if="t.is_outlier"
                class="w-3.5 h-3.5 text-amber-400 inline"
                title="Latency outlier vs P95 baseline"
              />
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="nextCursor" class="flex justify-center">
      <Button variant="ghost" size="sm" @click="loadMore">
        Load more
      </Button>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { RefreshCw, Search, Flag, Network } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import StatusBadge from '@/components/common/StatusBadge.vue'
import { listTraces } from '@/api/endpoints'

const router = useRouter()

const traces = ref([])
const loading = ref(false)
const error = ref('')
const nextCursor = ref('')

const fnFilter = ref('')
const statusFilter = ref('')
const outlierOnly = ref(false)

// Status filter chip group. Matches Activity.vue's status bucket pattern
// so the dashboard reads as one design system.
const statusOptions = [
  { value: '',         label: 'All' },
  { value: 'success',  label: 'Success' },
  { value: 'error',    label: 'Errors' },
]

const fetchTraces = async ({ append = false } = {}) => {
  loading.value = true
  error.value = ''
  try {
    const params = { limit: 50 }
    if (fnFilter.value) params.function_id = fnFilter.value
    if (statusFilter.value) params.status = statusFilter.value
    if (outlierOnly.value) params.outlier_only = '1'
    if (append && nextCursor.value) params.before = nextCursor.value

    const res = await listTraces(params)
    const incoming = res.data?.traces || []
    if (append) {
      traces.value.push(...incoming)
    } else {
      traces.value = incoming
    }
    nextCursor.value = res.data?.next_cursor || ''
  } catch (err) {
    error.value = err?.response?.data?.error?.message || err?.message || 'failed to load traces'
  } finally {
    loading.value = false
  }
}

const refresh = () => {
  nextCursor.value = ''
  fetchTraces()
}

const loadMore = () => fetchTraces({ append: true })

const setStatusFilter = (v) => {
  statusFilter.value = v
  refresh()
}

const toggleOutlier = () => {
  outlierOnly.value = !outlierOnly.value
  refresh()
}

const openTrace = (traceID) => router.push(`/traces/${traceID}`)

// Time-only format matches Activity.vue. Daily-or-less-frequent views
// (Invocations, Jobs) use full timestamps; high-volume live feeds like
// this one keep the cell narrow.
const formatTime = (iso) => {
  if (!iso) return '—'
  const d = new Date(iso)
  return d.toLocaleTimeString(undefined, { hour12: false })
}

onMounted(refresh)
</script>
