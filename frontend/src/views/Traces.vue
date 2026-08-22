<template>
  <div class="space-y-6">
    <header class="flex items-start justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-white tracking-tight">
          Traces
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
          Invocation chains across functions and triggers.
        </p>
      </div>
      <Button
        variant="secondary"
        size="sm"
        :loading="loading"
        @click="refresh"
      >
        <RefreshCw
          class="w-3.5 h-3.5"
          aria-hidden="true"
        /> Refresh
      </Button>
    </header>

    <section
      aria-labelledby="trace-filters-heading"
      class="space-y-2"
    >
      <h2
        id="trace-filters-heading"
        class="sr-only"
      >
        Trace filters
      </h2>
      <div class="flex flex-col sm:flex-row sm:items-center gap-2 sm:flex-wrap">
        <label class="relative w-full sm:flex-1 sm:min-w-[260px] sm:max-w-[420px]">
          <span class="sr-only">Function ID or name</span>
          <Search
            class="w-3.5 h-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-foreground-muted/60 pointer-events-none"
            aria-hidden="true"
          />
          <input
            v-model.trim="fnFilter"
            placeholder="Function ID or exact name…"
            class="h-10 w-full bg-background border border-border rounded-md pl-8 pr-3 text-xs text-foreground placeholder-foreground-muted/60 focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
            @keydown.enter="refresh"
          >
        </label>
        <div
          class="flex items-center gap-2 overflow-x-auto scrollable snap-x min-w-0"
          aria-label="Time range"
        >
          <Button
            v-for="preset in timePresets"
            :key="preset.value"
            variant="chip"
            size="xs"
            :active="timePreset === preset.value"
            :aria-pressed="timePreset === preset.value"
            class="shrink-0 snap-start"
            @click="setTimePreset(preset.value)"
          >
            {{ preset.label }}
          </Button>
        </div>
      </div>
      <div
        class="flex items-center gap-2 overflow-x-auto scrollable snap-x min-w-0"
        aria-label="Trace status"
      >
        <Button
          v-for="opt in statusOptions"
          :key="opt.value"
          variant="chip"
          size="xs"
          :active="statusFilter === opt.value"
          :aria-pressed="statusFilter === opt.value"
          class="shrink-0 snap-start"
          @click="setStatusFilter(opt.value)"
        >
          {{ opt.label }}
        </Button>
        <span
          class="text-foreground-muted/40"
          aria-hidden="true"
        >·</span>
        <Button
          variant="chip"
          size="xs"
          :active="outlierOnly"
          :aria-pressed="outlierOnly"
          class="shrink-0 snap-start"
          @click="toggleOutlier"
        >
          <Flag
            class="w-3 h-3"
            aria-hidden="true"
          /> Outliers
        </Button>
      </div>
    </section>

    <div
      v-if="error"
      class="rounded-md border border-danger-ring bg-danger-tint p-3 text-xs text-danger-fg"
      role="alert"
    >
      {{ error }}
    </div>
    <div
      v-else-if="!traces.length && !loading"
      class="bg-background border border-border rounded-lg p-8 text-center text-sm text-foreground-muted"
    >
      <Network
        class="w-6 h-6 mx-auto mb-3 text-foreground-muted/50"
        aria-hidden="true"
      />
      <p>No traces in this view.</p>
      <p class="text-xs mt-1 text-foreground-muted/60">
        Try a wider time range or invoke a function.
      </p>
    </div>

    <section
      v-else
      aria-labelledby="trace-results-heading"
      class="bg-background border border-border rounded-lg overflow-hidden"
    >
      <h2
        id="trace-results-heading"
        class="sr-only"
      >
        Trace results
      </h2>
      <!-- Track budget, and why these widths: the section is overflow-hidden
           inside a document that suppresses horizontal scroll, so anything
           wider than the content box is clipped silently rather than
           scrolled. At md the content box is 707 px (768 minus 2x p-page) =
           46.5rem at this project's 95 % root; tracks + 5x gap-3 + px-4 must
           stay under that. The old set needed 50.75rem and lopped the Status
           column off every tablet. This one needs 44.75rem, so it also
           survives the narrower box at lg, where the sidebar takes 13rem
           back. Keep the two grid-cols values below identical. -->
      <div
        class="hidden md:grid grid-cols-[8rem_6.5rem_minmax(8rem,1fr)_4.5rem_5rem_7rem] gap-3 px-4 py-3 text-xs text-foreground-muted uppercase tracking-label bg-surface border-b border-border"
        aria-hidden="true"
      >
        <span>Time</span><span>Trace</span><span>Entry function</span><span>Duration</span><span>Spans</span><span>Status</span>
      </div>
      <div class="divide-y divide-border">
        <button
          v-for="item in traces"
          :key="item.trace_id"
          type="button"
          class="w-full min-h-14 px-4 py-3 text-left hover:bg-surface/55 focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary transition-colors"
          :aria-label="traceLabel(item)"
          @click="openTrace(item.trace_id)"
        >
          <span class="grid grid-cols-1 md:grid-cols-[8rem_6.5rem_minmax(8rem,1fr)_4.5rem_5rem_7rem] gap-2 md:gap-3 md:items-center text-xs">
            <time class="font-mono text-foreground-muted whitespace-nowrap">{{ formatTime(item.started_at) }}</time>
            <code class="font-mono text-foreground-muted">{{ shortID(item.trace_id) }}</code>
            <span class="min-w-0">
              <span class="block text-sm text-white truncate">{{ item.function_name || item.root_function_id }}</span>
              <span class="block text-foreground-muted truncate">
                {{ relationshipSummary(item) }}
                <template v-if="item.error_count"> · <span class="text-danger-fg">{{ item.error_count }} error{{ item.error_count === 1 ? '' : 's' }}</span></template>
                <template v-if="item.cold_start_count"> · {{ item.cold_start_count }} cold</template>
              </span>
            </span>
            <span class="font-mono text-white md:text-foreground-muted">{{ item.duration_ms }}ms</span>
            <span class="text-foreground-muted">{{ item.span_count }} span{{ item.span_count === 1 ? '' : 's' }}</span>
            <!-- The flag is decorative here: the row button carries an
                 aria-label, which replaces the accessible name of everything
                 inside it, so a label on this icon would never be read.
                 traceLabel() carries the outlier wording instead. -->
            <span class="flex items-center gap-2"><StatusBadge :status="item.status" /><Flag
              v-if="item.is_outlier"
              class="w-3.5 h-3.5 text-warning-fg"
              aria-hidden="true"
            /></span>
          </span>
        </button>
      </div>
    </section>

    <div
      v-if="nextCursor"
      class="flex justify-center"
    >
      <Button
        variant="ghost"
        size="sm"
        :loading="loading"
        @click="loadMore"
      >
        Load more
      </Button>
    </div>
    <p
      class="sr-only"
      aria-live="polite"
    >
      {{ loading ? 'Loading traces' : `${traces.length} traces loaded` }}
    </p>
  </div>
</template>

<script setup>
defineOptions({ name: 'TracesView' })

import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Flag, Network, RefreshCw, Search } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import StatusBadge from '@/components/common/StatusBadge.vue'
import { listTraces } from '@/api/endpoints'
import { shortID } from '@/utils/traceLayout'

const router = useRouter()
const traces = ref([])
const loading = ref(false)
const error = ref('')
const nextCursor = ref('')
const fnFilter = ref('')
const statusFilter = ref('')
const outlierOnly = ref(false)
const timePreset = ref('all')

const statusOptions = [
  { value: '', label: 'All status' },
  { value: 'success', label: 'Success' },
  { value: 'error', label: 'Errors' },
]
const timePresets = [
  { value: '15m', label: '15m', ms: 15 * 60 * 1000 },
  { value: '1h', label: '1h', ms: 60 * 60 * 1000 },
  { value: '6h', label: '6h', ms: 6 * 60 * 60 * 1000 },
  { value: '24h', label: '24h', ms: 24 * 60 * 60 * 1000 },
  { value: '7d', label: '7d', ms: 7 * 24 * 60 * 60 * 1000 },
  { value: 'all', label: 'All time', ms: 0 },
]

const fetchTraces = async ({ append = false } = {}) => {
  loading.value = true
  error.value = ''
  try {
    const params = { limit: 50 }
    if (fnFilter.value) params.function_id = fnFilter.value
    if (statusFilter.value) params.status = statusFilter.value
    if (outlierOnly.value) params.outlier_only = '1'
    const preset = timePresets.find((item) => item.value === timePreset.value)
    if (preset?.ms) params.since = new Date(Date.now() - preset.ms).toISOString()
    if (append && nextCursor.value) params.before = nextCursor.value
    const incoming = (await listTraces(params)).data
    traces.value = append ? [...traces.value, ...(incoming?.traces || [])] : (incoming?.traces || [])
    nextCursor.value = incoming?.next_cursor || ''
  } catch (err) {
    error.value = err?.response?.data?.error?.message || err?.message || 'Failed to load traces.'
  } finally {
    loading.value = false
  }
}
const refresh = () => { nextCursor.value = ''; fetchTraces() }
const loadMore = () => fetchTraces({ append: true })
const setStatusFilter = (value) => { statusFilter.value = value; refresh() }
const setTimePreset = (value) => { timePreset.value = value; refresh() }
const toggleOutlier = () => { outlierOnly.value = !outlierOnly.value; refresh() }
const openTrace = (traceID) => router.push(`/traces/${traceID}`)
const formatTime = (iso) => new Date(iso).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
const relationshipSummary = (item) => item.external_parent_span_id ? `External · ${item.trigger || 'entry'}` : (item.trigger || 'entry')
// An aria-label on the row button replaces the accessible name computed from
// its subtree, so everything drawn inside the row has to be restated here or it
// is inaudible: the timestamp, the error and cold-start counts, and the outlier
// flag — which has no other representation in the list, so filtering by
// Outliers would otherwise produce a list whose defining property is silent.
const traceLabel = (item) => {
  const parts = [
    item.function_name || item.root_function_id,
    formatTime(item.started_at),
    item.status,
    `${item.duration_ms} milliseconds`,
    `${item.span_count} span${item.span_count === 1 ? '' : 's'}`,
  ]
  if (item.error_count) parts.push(`${item.error_count} error${item.error_count === 1 ? '' : 's'}`)
  if (item.cold_start_count) parts.push(`${item.cold_start_count} cold start${item.cold_start_count === 1 ? '' : 's'}`)
  if (item.is_outlier) parts.push('latency outlier')
  return parts.join(', ')
}

onMounted(refresh)
</script>
