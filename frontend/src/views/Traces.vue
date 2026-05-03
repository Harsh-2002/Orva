<template>
  <div class="space-y-6">
    <div class="flex items-start justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-foreground tracking-tight">
          Traces
        </h1>
        <p class="text-xs text-foreground-muted mt-1 max-w-prose">
          Causal chains across HTTP, F2F invokes, jobs, cron, and inbound webhooks.
          One row per trace; click to see the full waterfall of spans.
        </p>
      </div>
      <button
        class="inline-flex items-center gap-1.5 px-3 py-1.5 text-xs rounded-md border border-border hover:bg-surface text-foreground-muted hover:text-white transition-colors"
        @click="refresh"
      >
        <RefreshCw class="w-3.5 h-3.5" :class="{ 'animate-spin': loading }" />
        Refresh
      </button>
    </div>

    <!-- Filters -->
    <div class="flex flex-wrap items-center gap-2 text-xs">
      <label class="text-foreground-muted">Function</label>
      <input
        v-model="fnFilter"
        type="text"
        placeholder="fn id or name (optional)"
        class="bg-surface border border-border rounded-md px-2 py-1 text-xs text-white w-56 focus:outline-none focus:ring-1 focus:ring-primary"
        @keydown.enter="refresh"
      >

      <span class="mx-2 text-foreground-muted">|</span>

      <button
        v-for="f in statusFilters"
        :key="f.value"
        class="px-2 py-0.5 rounded-md border border-border transition-colors"
        :class="statusFilter === f.value
          ? 'bg-primary/20 border-primary text-white'
          : 'text-foreground-muted hover:text-white hover:bg-surface'"
        @click="setStatusFilter(f.value)"
      >
        {{ f.label }}
      </button>

      <span class="mx-2 text-foreground-muted">|</span>

      <button
        class="px-2 py-0.5 rounded-md border border-border transition-colors"
        :class="outlierOnly
          ? 'bg-amber-500/20 border-amber-500/50 text-amber-200'
          : 'text-foreground-muted hover:text-white hover:bg-surface'"
        @click="toggleOutlier"
      >
        <Flag class="w-3 h-3 inline -mt-0.5 mr-0.5" />
        Outliers only
      </button>
    </div>

    <div
      v-if="error"
      class="rounded-md border border-red-700/40 bg-red-950/30 p-3 text-xs text-red-200"
    >
      {{ error }}
    </div>

    <div
      v-else-if="!traces.length && !loading"
      class="rounded-md border border-border bg-background p-6 text-center text-xs text-foreground-muted"
    >
      No traces yet. Hit a function over HTTP or fire a cron and they'll show up here.
    </div>

    <div
      v-else-if="loading && !traces.length"
      class="text-xs text-foreground-muted italic"
    >
      Loading traces…
    </div>

    <div
      v-else
      class="bg-background border border-border rounded-lg overflow-hidden"
    >
      <table class="w-full text-xs">
        <thead class="bg-surface/50 text-foreground-muted">
          <tr>
            <th class="text-left font-medium px-3 py-2">Time</th>
            <th class="text-left font-medium px-3 py-2">Trace</th>
            <th class="text-left font-medium px-3 py-2">Root function</th>
            <th class="text-left font-medium px-3 py-2">Trigger</th>
            <th class="text-right font-medium px-3 py-2">Duration</th>
            <th class="text-left font-medium px-3 py-2">Status</th>
            <th class="text-left font-medium px-3 py-2"></th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="t in traces"
            :key="t.trace_id"
            class="border-t border-border hover:bg-surface/40 cursor-pointer transition-colors"
            @click="openTrace(t.trace_id)"
          >
            <td class="px-3 py-2 text-foreground-muted whitespace-nowrap font-mono">
              {{ formatTime(t.started_at) }}
            </td>
            <td class="px-3 py-2 font-mono text-white">
              <span class="text-foreground-muted">{{ t.trace_id.slice(0, 11) }}</span>
            </td>
            <td class="px-3 py-2 text-white">
              {{ t.function_name || t.root_function_id }}
            </td>
            <td class="px-3 py-2">
              <span class="px-1.5 py-0.5 rounded text-[10px] font-medium uppercase tracking-wide bg-surface text-foreground-muted">
                {{ t.trigger || '—' }}
              </span>
            </td>
            <td class="px-3 py-2 text-right font-mono">
              {{ t.duration_ms != null ? `${t.duration_ms}ms` : '—' }}
            </td>
            <td class="px-3 py-2">
              <span
                class="px-1.5 py-0.5 rounded text-[10px] font-medium uppercase"
                :class="t.status === 'success'
                  ? 'bg-emerald-500/15 text-emerald-300'
                  : 'bg-red-500/15 text-red-300'"
              >
                {{ t.status }}
              </span>
            </td>
            <td class="px-3 py-2">
              <Flag
                v-if="t.is_outlier"
                class="w-3.5 h-3.5 text-amber-400"
                title="Latency outlier vs P95 baseline"
              />
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <div v-if="nextCursor" class="text-center">
      <button
        class="text-xs text-foreground-muted hover:text-white px-3 py-1.5 rounded-md border border-border hover:bg-surface transition-colors"
        @click="loadMore"
      >
        Load more
      </button>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { RefreshCw, Flag } from 'lucide-vue-next'
import { listTraces } from '@/api/endpoints'

const router = useRouter()

const traces = ref([])
const loading = ref(false)
const error = ref('')
const nextCursor = ref('')

const fnFilter = ref('')
const statusFilter = ref('')
const outlierOnly = ref(false)

const statusFilters = [
  { value: '', label: 'All' },
  { value: 'success', label: 'Success' },
  { value: 'error', label: 'Error' },
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

const formatTime = (iso) => {
  if (!iso) return '—'
  const d = new Date(iso)
  return d.toLocaleTimeString(undefined, { hour12: false })
}

onMounted(refresh)
</script>
