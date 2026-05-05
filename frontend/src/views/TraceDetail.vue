<template>
  <div class="space-y-6">
    <!-- Page header — title + breadcrumb back-link, matches the rest of
         the dashboard. The right slot is empty here because the trace
         id IS the page identity (rendered in the summary card below). -->
    <div class="flex items-start justify-between gap-4">
      <div>
        <button
          class="inline-flex items-center gap-1 text-xs text-foreground-muted hover:text-white mb-1 transition-colors"
          @click="router.push('/traces')"
        >
          <ArrowLeft class="w-3.5 h-3.5" />
          All traces
        </button>
        <h1 class="text-xl font-semibold text-white tracking-tight">
          Trace
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
          Causal tree of every span produced by this invocation chain.
          Click any span row to jump to its execution in the Invocations log.
        </p>
      </div>
    </div>

    <div
      v-if="error"
      class="rounded-md border border-red-700/40 bg-red-950/30 p-3 text-xs text-red-200"
    >
      {{ error }}
    </div>

    <div
      v-else-if="loading && !trace"
      class="text-xs text-foreground-muted italic"
    >
      Loading trace…
    </div>

    <template v-else-if="trace">
      <!-- Summary card — same shell as Settings cards: bg-background +
           border + rounded-lg + p-5 + space-y. Stats are dt/dd-style
           label-on-top so the eye scans top-to-bottom. -->
      <div class="bg-background border border-border rounded-lg p-5 space-y-4">
        <div class="flex items-center gap-3 flex-wrap text-xs">
          <span class="text-foreground-muted uppercase tracking-wide">
            trace id
          </span>
          <code class="bg-surface text-white px-2 py-0.5 rounded font-mono">
            {{ trace.trace_id }}
          </code>
          <button
            class="p-1 rounded hover:bg-surface text-foreground-muted hover:text-white transition-colors"
            title="Copy trace id"
            @click="copyID"
          >
            <Copy class="w-3.5 h-3.5" />
          </button>
        </div>

        <div class="grid grid-cols-2 sm:grid-cols-4 gap-4 pt-3 border-t border-border text-xs">
          <div>
            <div class="text-foreground-muted uppercase tracking-wide mb-1">
              Trigger
            </div>
            <span class="inline-flex items-center px-2 py-0.5 rounded text-xs border bg-background font-mono text-foreground-muted border-border lowercase">
              {{ trace.trigger || EMPTY }}
            </span>
          </div>
          <div>
            <div class="text-foreground-muted uppercase tracking-wide mb-1">
              Total duration
            </div>
            <div class="text-white font-mono">{{ trace.total_duration_ms }}ms</div>
          </div>
          <div>
            <div class="text-foreground-muted uppercase tracking-wide mb-1">
              Spans
            </div>
            <div class="text-white">{{ trace.span_count }}</div>
          </div>
          <div>
            <div class="text-foreground-muted uppercase tracking-wide mb-1">
              Status
            </div>
            <div class="flex items-center gap-2">
              <StatusBadge :status="trace.status" />
              <span
                v-if="trace.has_outlier"
                class="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-amber-300"
              >
                <Flag class="w-3 h-3" /> Outlier
              </span>
            </div>
          </div>
        </div>
      </div>

      <!-- Waterfall card. Each span is a row with offset bar; bar
           position/width is computed in JS from offset_ms/duration_ms.
           Bar colors align with the broader dashboard palette: primary
           for warm successes, amber for outliers, red for errors. -->
      <div class="bg-background border border-border rounded-lg p-5">
        <div class="text-xs text-foreground-muted uppercase tracking-wide mb-4">
          Waterfall
        </div>
        <div class="space-y-1.5">
          <div
            v-for="(s, i) in trace.spans"
            :key="s.span_id || `s${i}`"
            class="grid grid-cols-12 gap-2 items-center text-xs hover:bg-surface/40 px-2 py-1.5 rounded cursor-pointer transition-colors"
            @click="onSpanClick(s)"
          >
            <div class="col-span-3 truncate flex items-center gap-1.5">
              <span class="text-white">{{ s.function_name || s.function_id }}</span>
              <span class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] border bg-background font-mono text-foreground-muted border-border lowercase">
                {{ s.trigger || EMPTY }}
              </span>
              <Flag v-if="s.is_outlier" class="w-3 h-3 text-amber-400" />
            </div>

            <div class="col-span-7 relative h-4">
              <div
                class="absolute h-2 top-1 rounded-sm"
                :class="barClass(s)"
                :style="barStyle(s)"
                :title="`+${s.offset_ms}ms · ${s.duration_ms}ms`"
              />
            </div>

            <div class="col-span-2 text-right font-mono">
              <span class="text-white">{{ s.duration_ms }}ms</span>
              <span v-if="s.baseline_p95_ms" class="block text-[10px] text-foreground-muted">
                p95 {{ s.baseline_p95_ms }}ms
              </span>
            </div>
          </div>
        </div>
      </div>

      <!-- Span list — same table shell as Activity / Invocations / Traces. -->
      <div class="bg-background border border-border rounded-lg overflow-x-auto">
        <table class="w-full text-sm text-left">
          <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
            <tr>
              <th class="px-4 py-3 w-32">Span</th>
              <th class="px-4 py-3">Function</th>
              <th class="px-4 py-3 w-28 hidden md:table-cell">Trigger</th>
              <th class="px-4 py-3 w-24 text-right">Offset</th>
              <th class="px-4 py-3 w-24 text-right">Duration</th>
              <th class="px-4 py-3 w-24">Status</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-border">
            <tr
              v-for="s in trace.spans"
              :key="`tbl-${s.span_id}`"
              class="hover:bg-surface/40 cursor-pointer transition-colors"
              @click="onSpanClick(s)"
            >
              <td class="px-4 py-2.5 font-mono text-xs text-foreground-muted">
                {{ s.span_id?.slice(0, 11) || EMPTY }}
              </td>
              <td class="px-4 py-2.5 text-white">
                {{ s.function_name || s.function_id }}
              </td>
              <td class="px-4 py-2.5 hidden md:table-cell">
                <span class="inline-flex items-center px-2 py-0.5 rounded text-xs border bg-background font-mono text-foreground-muted border-border lowercase">
                  {{ s.trigger || EMPTY }}
                </span>
              </td>
              <td class="px-4 py-2.5 text-right font-mono text-xs text-foreground-muted">
                +{{ s.offset_ms }}ms
              </td>
              <td class="px-4 py-2.5 text-right font-mono text-xs text-white">
                {{ s.duration_ms }}ms
              </td>
              <td class="px-4 py-2.5">
                <StatusBadge :status="s.status" />
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </div>
</template>

<script setup>
import { EMPTY } from '@/utils/format'
import { ref, onMounted, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, Copy, Flag } from 'lucide-vue-next'
import StatusBadge from '@/components/common/StatusBadge.vue'
import { getTrace } from '@/api/endpoints'

const route = useRoute()
const router = useRouter()

const trace = ref(null)
const loading = ref(false)
const error = ref('')

const fetchTrace = async () => {
  loading.value = true
  error.value = ''
  try {
    const res = await getTrace(route.params.id)
    trace.value = res.data
  } catch (err) {
    if (err?.response?.status === 404) {
      error.value = 'No spans found for that trace.'
    } else {
      error.value = err?.response?.data?.error?.message || err?.message || 'failed to load trace'
    }
  } finally {
    loading.value = false
  }
}

const total = computed(() => Math.max(1, trace.value?.total_duration_ms || 1))

const barStyle = (s) => {
  const left = (s.offset_ms / total.value) * 100
  // Clamp width so a 0ms span still shows a thin marker.
  const width = Math.max(0.5, (s.duration_ms / total.value) * 100)
  return { left: `${left}%`, width: `${width}%` }
}

const barClass = (s) => {
  if (s.status === 'error') return 'bg-red-500/70'
  if (s.is_outlier) return 'bg-amber-500/80'
  return 'bg-primary/80'
}

const onSpanClick = (s) => {
  if (!s.execution_id) return
  router.push({ path: '/invocations', query: { exec: s.execution_id } })
}

const copyID = async () => {
  if (!trace.value?.trace_id) return
  try {
    await navigator.clipboard.writeText(trace.value.trace_id)
  } catch {
    // ignore — clipboard may be blocked in non-https; user can select-copy.
  }
}

onMounted(fetchTrace)
</script>
