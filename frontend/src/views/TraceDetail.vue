<template>
  <div class="space-y-5">
    <div class="flex items-start justify-between gap-4">
      <div>
        <button
          class="text-xs text-foreground-muted hover:text-white inline-flex items-center gap-1 mb-1"
          @click="router.push('/traces')"
        >
          <ArrowLeft class="w-3.5 h-3.5" />
          All traces
        </button>
        <h1 class="text-xl font-semibold text-foreground tracking-tight">
          Trace
        </h1>
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
      <!-- Header card: trace_id + summary stats -->
      <div class="bg-background border border-border rounded-lg p-4 space-y-3">
        <div class="flex items-center gap-3 flex-wrap text-xs">
          <span class="text-foreground-muted">trace_id</span>
          <code class="bg-surface text-white px-2 py-0.5 rounded font-mono">{{ trace.trace_id }}</code>
          <button
            class="p-1 rounded hover:bg-surface text-foreground-muted hover:text-white"
            title="Copy trace id"
            @click="copyID"
          >
            <Copy class="w-3.5 h-3.5" />
          </button>
        </div>

        <div class="grid grid-cols-2 sm:grid-cols-4 gap-4 pt-2 border-t border-border text-xs">
          <div>
            <div class="text-foreground-muted mb-0.5">Trigger</div>
            <div class="text-white uppercase tracking-wide">{{ trace.trigger || '—' }}</div>
          </div>
          <div>
            <div class="text-foreground-muted mb-0.5">Total duration</div>
            <div class="text-white font-mono">{{ trace.total_duration_ms }}ms</div>
          </div>
          <div>
            <div class="text-foreground-muted mb-0.5">Spans</div>
            <div class="text-white">{{ trace.span_count }}</div>
          </div>
          <div>
            <div class="text-foreground-muted mb-0.5">Status</div>
            <div>
              <span
                class="px-1.5 py-0.5 rounded text-[10px] font-medium uppercase"
                :class="trace.status === 'success'
                  ? 'bg-emerald-500/15 text-emerald-300'
                  : 'bg-red-500/15 text-red-300'"
              >
                {{ trace.status }}
              </span>
              <span v-if="trace.has_outlier" class="ml-2 inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-amber-300">
                <Flag class="w-3 h-3" /> Outlier
              </span>
            </div>
          </div>
        </div>
      </div>

      <!-- Waterfall: each span is a row with offset bar -->
      <div class="bg-background border border-border rounded-lg p-4">
        <div class="text-xs text-foreground-muted mb-3">Waterfall</div>
        <div class="space-y-1.5">
          <div
            v-for="(s, i) in trace.spans"
            :key="s.span_id || `s${i}`"
            class="grid grid-cols-12 gap-2 items-center text-xs hover:bg-surface/40 px-2 py-1 rounded cursor-pointer"
            @click="onSpanClick(s)"
          >
            <!-- function + trigger column -->
            <div class="col-span-3 truncate">
              <span class="text-white">{{ s.function_name || s.function_id }}</span>
              <span class="ml-1.5 text-[10px] uppercase tracking-wide text-foreground-muted">
                {{ s.trigger || '' }}
              </span>
              <Flag v-if="s.is_outlier" class="w-3 h-3 inline ml-1 text-amber-400" />
            </div>

            <!-- bar column -->
            <div class="col-span-7 relative h-4">
              <div
                class="absolute h-2 top-1 rounded-sm"
                :class="barClass(s)"
                :style="barStyle(s)"
                :title="`+${s.offset_ms}ms · ${s.duration_ms}ms`"
              />
            </div>

            <!-- duration column -->
            <div class="col-span-2 text-right font-mono">
              <span class="text-white">{{ s.duration_ms }}ms</span>
              <span v-if="s.baseline_p95_ms" class="block text-[10px] text-foreground-muted">
                p95 {{ s.baseline_p95_ms }}ms
              </span>
            </div>
          </div>
        </div>
      </div>

      <!-- Span list as a table for scanning + jumping to InvocationsLog -->
      <div class="bg-background border border-border rounded-lg overflow-hidden">
        <table class="w-full text-xs">
          <thead class="bg-surface/50 text-foreground-muted">
            <tr>
              <th class="text-left font-medium px-3 py-2">Span</th>
              <th class="text-left font-medium px-3 py-2">Function</th>
              <th class="text-left font-medium px-3 py-2">Trigger</th>
              <th class="text-right font-medium px-3 py-2">Offset</th>
              <th class="text-right font-medium px-3 py-2">Duration</th>
              <th class="text-left font-medium px-3 py-2">Status</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="s in trace.spans"
              :key="`tbl-${s.span_id}`"
              class="border-t border-border hover:bg-surface/40 cursor-pointer"
              @click="onSpanClick(s)"
            >
              <td class="px-3 py-2 font-mono text-foreground-muted">
                {{ s.span_id?.slice(0, 11) || '—' }}
              </td>
              <td class="px-3 py-2 text-white">
                {{ s.function_name || s.function_id }}
              </td>
              <td class="px-3 py-2 text-foreground-muted uppercase tracking-wide text-[10px]">
                {{ s.trigger || '—' }}
              </td>
              <td class="px-3 py-2 text-right font-mono text-foreground-muted">
                +{{ s.offset_ms }}ms
              </td>
              <td class="px-3 py-2 text-right font-mono text-white">
                {{ s.duration_ms }}ms
              </td>
              <td class="px-3 py-2">
                <span
                  class="px-1.5 py-0.5 rounded text-[10px] font-medium uppercase"
                  :class="s.status === 'success'
                    ? 'bg-emerald-500/15 text-emerald-300'
                    : 'bg-red-500/15 text-red-300'"
                >
                  {{ s.status }}
                </span>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </template>
  </div>
</template>

<script setup>
import { ref, onMounted, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, Copy, Flag } from 'lucide-vue-next'
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
