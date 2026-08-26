<template>
  <div class="space-y-6">
    <header>
      <button
        type="button"
        class="min-h-11 inline-flex items-center gap-1 text-xs text-foreground-muted hover:text-foreground-strong rounded focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
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
      <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
        Follow work across functions without leaving the trace.
      </p>
    </header>

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

    <template v-else-if="trace">
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
            class="min-h-11 min-w-11 inline-flex items-center justify-center rounded text-foreground-muted hover:text-foreground-strong hover:bg-surface focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
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
              <StatusBadge :status="trace.status" />
            </dd>
          </div>
          <div>
            <dt class="inline text-foreground-muted">
              Duration
            </dt><dd class="inline text-foreground-strong font-mono">
              {{ trace.total_duration_ms }}ms
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
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, Copy, Flag } from '@lucide/vue'
import StatusBadge from '@/components/common/StatusBadge.vue'
import TraceWaterfall from '@/components/traces/TraceWaterfall.vue'
import { getTrace } from '@/api/endpoints'
import { shortID } from '@/utils/traceLayout'

const route = useRoute()
const router = useRouter()
const trace = ref(null)
const loading = ref(false)
const error = ref('')

const fetchTrace = async () => {
  loading.value = true
  error.value = ''
  try {
    trace.value = (await getTrace(route.params.id)).data
  } catch (err) {
    error.value = err?.response?.status === 404
      ? 'No spans found for that trace.'
      : err?.response?.data?.error?.message || err?.message || 'Failed to load trace.'
  } finally {
    loading.value = false
  }
}
const openInvocation = (executionID) => router.push({ path: '/invocations', query: { exec: executionID } })
const copyID = async () => {
  if (!trace.value?.trace_id) return
  try { await navigator.clipboard.writeText(trace.value.trace_id) } catch { /* selection remains available */ }
}

onMounted(fetchTrace)
</script>
