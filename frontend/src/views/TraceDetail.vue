<template>
  <div class="space-y-6">
    <header class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <button
          type="button"
          class="touch-expand-sm inline-flex h-8 items-center gap-1 rounded-md px-2 text-xs text-foreground-muted hover:text-foreground-strong rounded focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
          @click="router.push('/traces')"
        >
          <ArrowLeft
            class="w-3.5 h-3.5"
            aria-hidden="true"
          /> All traces
        </button>
        <h1 class="text-xl font-semibold text-foreground-strong tracking-tight">
          Trace diagnostics
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 measure leading-body">
          Follow work across functions without leaving the trace.
        </p>
      </div>
      <Button
        variant="secondary"
        :loading="loading"
        @click="fetchTrace"
      >
        Refresh
      </Button>
    </header>
    <p
      class="text-xs text-foreground-muted"
      role="status"
    >
      {{ loading ? 'Updating trace…' : error && trace ? 'Showing previously loaded data. Refresh failed; retry with Refresh.' : 'Refreshes every 5 seconds while this view is open, including delayed jobs.' }}
    </p>

    <div
      v-if="error"
      class="rounded-md border border-danger-ring bg-danger-tint p-3 text-xs text-danger-fg"
      role="alert"
    >
      {{ error }}
    </div>
    <div
      v-else-if="loading && !trace"
      class="text-xs text-foreground-muted italic"
      aria-live="polite"
    >
      Loading trace…
    </div>

    <template v-if="trace">
      <section aria-labelledby="trace-summary-heading">
        <div class="flex items-center gap-2 flex-wrap">
          <h2
            id="trace-summary-heading"
            class="sr-only"
          >
            Trace summary
          </h2>
          <code class="bg-surface text-foreground-strong px-2 py-1 rounded font-mono text-xs break-all">{{ trace.trace_id }}</code>
          <button
            type="button"
            class="touch-expand-iconbtn inline-flex h-7 w-7 items-center justify-center rounded text-foreground-muted hover:text-foreground-strong hover:bg-surface focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
            aria-label="Copy trace ID"
            @click="copyID"
          >
            <Copy
              class="w-3.5 h-3.5"
              aria-hidden="true"
            />
          </button>
          <span
            v-if="trace.external_parent_span_id"
            class="text-xs text-foreground-muted"
          >External parent <code>{{ shortID(trace.external_parent_span_id) }}</code></span>
        </div>
        <dl class="mt-3 flex flex-wrap gap-x-6 gap-y-2 text-xs">
          <div>
            <dt class="inline text-foreground-muted">
              Status
            </dt><dd class="inline">
              <StatusBadge :status="trace.status || 'unknown'" />
            </dd>
          </div>
          <div>
            <dt class="inline text-foreground-muted">
              Duration
            </dt><dd class="inline text-foreground-strong font-mono">
              {{ formatTraceTime(trace.total_duration_ms) }}
            </dd>
          </div>
          <div>
            <dt class="inline text-foreground-muted">
              Spans
            </dt><dd class="inline text-foreground-strong">
              {{ trace.span_count }}
            </dd>
          </div>
          <div v-if="trace.error_count">
            <dt class="inline text-foreground-muted">
              Errors
            </dt><dd class="inline text-danger-fg">
              {{ trace.error_count }}
            </dd>
          </div>
          <div>
            <dt class="inline text-foreground-muted">
              Cold starts
            </dt><dd class="inline text-foreground-strong">
              {{ trace.cold_start_count || 0 }}
            </dd>
          </div>
          <div
            v-if="trace.has_outlier"
            class="inline-flex items-center gap-1 text-warning-fg"
          >
            <Flag
              class="w-3 h-3"
              aria-hidden="true"
            /> Outlier
          </div>
        </dl>
      </section>

      <TraceWaterfall
        :trace="trace"
        @open-invocation="openInvocation"
      />
    </template>
  </div>
</template>

<script setup>
import { onActivated, onBeforeUnmount, onDeactivated, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ArrowLeft, Copy, Flag } from '@lucide/vue'
import StatusBadge from '@/components/common/StatusBadge.vue'
import Button from '@/components/common/Button.vue'
import TraceWaterfall from '@/components/traces/TraceWaterfall.vue'
import { getTrace } from '@/api/endpoints'
import { shortID, formatTraceTime } from '@/utils/traceLayout'

// The trace this view is scoped to. A route prop, not useRoute(): Vue does
// not patch a keep-alived view's props, so a cached view stops following the URL.
const props = defineProps({ id: { type: String, default: '' } })

const router = useRouter()
const trace = ref(null)
const loading = ref(false)
const error = ref('')
let active = false
let generation = 0
let timer

const fetchTrace = async () => {
  if (!active || !props.id || loading.value) return
  clearTimeout(timer)
  const request = ++generation
  const id = props.id
  const current = () => active && request === generation && id === props.id
  loading.value = true
  error.value = ''
  try {
    const data = (await getTrace(id)).data
    if (!current()) return
    if (!data || typeof data !== 'object' || !data.trace_id) throw new Error('Trace response was empty. Try Refresh.')
    trace.value = data
  } catch (err) {
    if (!current()) return
    error.value = err?.response?.status === 404
      ? 'No spans found for that trace.'
      : err?.response?.data?.error?.message || err?.message || 'Failed to load trace.'
  } finally {
    if (current()) {
      loading.value = false
      timer = setTimeout(fetchTrace, 5000)
    }
  }
}
const openInvocation = (executionID) => router.push({ path: '/invocations', query: { exec: executionID } })
const copyID = async () => {
  if (!trace.value?.trace_id) return
  try { await navigator.clipboard.writeText(trace.value.trace_id) } catch { /* selection remains available */ }
}

// Clearing first matters as much as refetching: without it the previous trace
// stayed on screen for the whole request, under the new trace's URL.
watch(() => props.id, () => {
  ++generation
  clearTimeout(timer)
  loading.value = false
  error.value = ''
  trace.value = null
  fetchTrace()
}, { flush: 'sync' })
const activate = () => {
  if (active) return
  active = true
  fetchTrace()
}
const stop = () => {
  active = false
  ++generation
  clearTimeout(timer)
  loading.value = false
}
onMounted(activate)
onActivated(activate)
onDeactivated(stop)
onBeforeUnmount(stop)
</script>
