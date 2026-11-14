<template>
  <section
    class="bg-background border border-border rounded-lg overflow-hidden"
    aria-labelledby="waterfall-heading"
  >
    <div class="flex flex-wrap items-center justify-between gap-3 px-4 sm:px-5 py-4 border-b border-border">
      <div>
        <h2
          id="waterfall-heading"
          class="text-sm font-semibold text-foreground-strong tracking-tight"
        >
          Causal waterfall
        </h2>
        <p class="text-xs text-foreground-muted mt-1">
          Select a span to inspect it here.
        </p>
      </div>
      <!-- Swatches are squares rather than dots so the two textured ones read
           at this size: they carry the same hatch / stripe the bars use, which
           is what makes the legend teach the second channel instead of only
           the hue. -->
      <div
        class="flex flex-wrap items-center gap-3 text-xs text-foreground-muted"
        aria-label="Waterfall legend"
      >
        <span class="inline-flex items-center gap-1.5"><i class="w-2.5 h-2.5 rounded-sm bg-primary" />Execution</span>
        <span class="inline-flex items-center gap-1.5"><i class="w-2.5 h-2.5 rounded-sm bg-info" />Code</span>
        <span class="inline-flex items-center gap-1.5"><i class="w-2.5 h-2.5 rounded-sm bg-warning bar-stripe" />Outlier</span>
        <span class="inline-flex items-center gap-1.5"><i class="w-2.5 h-2.5 rounded-sm bg-danger bar-hatch" />Error</span>
      </div>
    </div>

    <div class="px-3 sm:px-4 py-3 border-b border-border space-y-3">
      <div class="flex flex-wrap items-center gap-2">
        <Button
          variant="secondary"
          size="xs"
          :disabled="!selectedRow"
          @click="focusSelected"
        >
          Focus selected span
        </Button>
        <Button
          variant="ghost"
          size="xs"
          @click="fitTrace"
        >
          Fit trace
        </Button>
        <Button
          variant="ghost"
          size="xs"
          :disabled="window.end - window.start <= 1"
          @click="zoom(0.5)"
        >
          Zoom in
        </Button>
        <Button
          variant="ghost"
          size="xs"
          :disabled="window.end - window.start >= extent"
          @click="zoom(2)"
        >
          Zoom out
        </Button>
      </div>
      <p
        class="text-xs text-foreground-muted"
        role="status"
      >
        Window: +{{ formatTraceTime(window.start) }} to +{{ formatTraceTime(window.end) }} from trace start.
        Spans outside this window remain selectable. Zero-duration spans use a line marker.
      </p>
      <div
        class="grid grid-cols-1 sm:grid-cols-[minmax(0,0.9fr)_minmax(0,1.4fr)_5rem] gap-2 text-xs text-foreground-muted"
        aria-label="Time ruler"
      >
        <span>Time from window start</span>
        <div
          class="relative h-6 border-b border-border font-mono tabular-nums"
          data-time-ruler
        >
          <i
            class="absolute bottom-0 left-0 h-1 border-l border-border"
            aria-hidden="true"
          />
          <i
            class="absolute bottom-0 left-1/2 h-1 border-l border-border"
            aria-hidden="true"
          />
          <i
            class="absolute bottom-0 right-0 h-1 border-r border-border"
            aria-hidden="true"
          />
          <span
            v-for="(tick, index) in ticks"
            :key="index"
            class="absolute top-0 whitespace-nowrap"
            :style="{ left: `${index * 50}%`, transform: `translateX(-${index * 50}%)` }"
            :title="formatTraceTime(tick)"
          >+{{ rulerLabel(tick) }}</span>
        </div>
      </div>
    </div>

    <div class="divide-y divide-border/70">
      <div
        v-for="row in rows"
        :key="row.key"
      >
        <button
          type="button"
          class="group w-full min-h-[44px] px-3 sm:px-4 py-2.5 text-left hover:bg-surface/55 focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary transition-colors"
          :aria-expanded="selectedKey === row.key"
          :aria-controls="`span-detail-${safeKey(row.key)}`"
          :data-span-key="row.key"
          @click="select(row)"
        >
          <span class="grid grid-cols-1 sm:grid-cols-[minmax(0,0.9fr)_minmax(0,1.4fr)_5rem] gap-2 sm:items-center">
            <span
              class="min-w-0 flex items-center gap-2"
              :style="indentStyle(row.depth)"
            >
              <ChevronRight
                class="w-3.5 h-3.5 shrink-0 text-foreground-muted transition-transform"
                :class="selectedKey === row.key ? 'rotate-90' : ''"
                aria-hidden="true"
              />
              <span class="min-w-0">
                <span class="flex items-center gap-1.5 min-w-0">
                  <span class="text-sm text-foreground-strong truncate">{{ row.label }}</span>
                  <span
                    v-if="row.type === 'user'"
                    class="text-xs text-foreground-muted border border-border rounded px-1"
                  >code</span>
                  <span
                    v-if="row.type === 'system' && row.cold_start"
                    class="shrink-0 text-xs text-foreground-muted"
                  >Cold</span>
                </span>
                <span class="block text-xs text-foreground-muted truncate">{{ row.relationship }}</span>
                <span class="block text-xs text-foreground-muted break-words">{{ row.status || 'Unknown' }} · +{{ formatTraceTime(row.offset_ms) }}<template v-if="row.is_outlier"> · Outlier</template></span>
              </span>
            </span>

            <span
              class="relative block h-5 w-full rounded bg-surface overflow-hidden"
              :aria-label="`Starts at ${row.offset_ms || 0} milliseconds and runs for ${row.duration_ms || 0} milliseconds`"
            >
              <i
                class="absolute inset-y-0 left-1/2 border-l border-border"
                aria-hidden="true"
              />
              <span
                v-if="!geometry(row).outside"
                class="absolute top-1 h-3 rounded-sm"
                :class="barClass(row)"
                :style="barStyle(row)"
                :title="`${geometry(row).clippedStart ? 'Continues before window. ' : ''}${geometry(row).clippedEnd ? 'Continues after window.' : ''}`"
              />
              <span
                v-else
                class="block text-center text-xs text-foreground-muted"
              >{{ geometry(row).outside }}</span>
            </span>

            <span class="flex sm:block justify-between items-center font-mono text-xs sm:text-right">
              <span class="sm:hidden text-foreground-muted">Duration</span>
              <span
                class="text-foreground-strong"
                :title="`${row.duration_ms} milliseconds`"
              >{{ formatTraceTime(row.duration_ms) }}</span>
              <span
                v-if="row.baseline_p95_ms"
                class="block text-xs text-foreground-muted"
              >p95 {{ row.baseline_p95_ms }}ms</span>
            </span>
          </span>
        </button>

        <div
          v-if="selectedKey === row.key"
          :id="`span-detail-${safeKey(row.key)}`"
          class="px-4 sm:px-5 py-4 bg-surface/45 border-t border-border/70"
          role="region"
          :aria-label="`${row.label} span details`"
        >
          <div class="flex flex-wrap items-start justify-between gap-4">
            <dl class="grid grid-cols-2 sm:grid-cols-4 gap-x-6 gap-y-3 text-xs min-w-0">
              <div>
                <dt class="text-foreground-muted">
                  Status
                </dt>
                <dd class="text-foreground-strong mt-0.5">
                  {{ row.status }}<span v-if="row.status_code"> · HTTP {{ row.status_code }}</span>
                </dd>
              </div>
              <div v-if="row.type === 'system'">
                <dt class="text-foreground-muted">
                  Worker
                </dt>
                <dd class="text-foreground-strong mt-0.5">
                  {{ row.cold_start ? 'Cold start' : row.cold_start === false ? 'Warm' : 'Unknown' }}
                </dd>
              </div>
              <div>
                <dt class="text-foreground-muted">
                  Timing
                </dt>
                <dd class="text-foreground-strong mt-0.5 font-mono">
                  +{{ row.offset_ms || 0 }}ms · {{ row.duration_ms || 0 }}ms
                </dd>
              </div>
              <div v-if="row.baseline_p95_ms">
                <dt class="text-foreground-muted">
                  Baseline
                </dt>
                <dd class="text-foreground-strong mt-0.5">
                  {{ baselineComparison(row) }}
                </dd>
              </div>
            </dl>
            <Button
              v-if="row.execution_id"
              variant="secondary"
              size="sm"
              @click="$emit('open-invocation', row.execution_id)"
            >
              Open invocation <ExternalLink
                class="w-3.5 h-3.5"
                aria-hidden="true"
              />
            </Button>
          </div>
          <p
            v-if="row.error_message"
            class="mt-3 rounded border border-danger-ring bg-danger-tint px-3 py-2 text-xs text-danger-fg"
            role="alert"
          >
            {{ row.error_message }}
          </p>
          <details
            v-if="row.attributes"
            class="mt-3 text-xs"
          >
            <summary class="touch-expand-sm min-h-6 inline-flex items-center gap-1.5 cursor-pointer list-none text-foreground-muted hover:text-foreground-strong focus:outline-none focus-visible:ring-2 focus-visible:ring-primary rounded [&::-webkit-details-marker]:hidden">
              <ChevronRight
                class="w-3.5 h-3.5 shrink-0 transition-transform group-open:rotate-90"
                aria-hidden="true"
              />
              Structured attributes
            </summary>
            <pre class="mt-1 overflow-x-auto rounded bg-background p-3 text-foreground whitespace-pre-wrap">{{ prettyJSON(row.attributes) }}</pre>
          </details>
        </div>
      </div>
    </div>

    <div class="border-t border-border px-4 sm:px-5 py-4">
      <div class="flex flex-wrap items-center justify-between gap-3 mb-3">
        <h3 class="text-sm font-medium text-foreground-strong">
          Structured logs <span class="text-foreground-muted font-normal">({{ visibleLogs.length }})</span>
        </h3>
        <div
          class="flex gap-2"
          aria-label="Log scope"
        >
          <Button
            variant="chip"
            size="xs"
            :active="logScope === 'all'"
            :aria-pressed="logScope === 'all'"
            @click="logScope = 'all'"
          >
            All logs
          </Button>
          <Button
            variant="chip"
            size="xs"
            :active="logScope === 'selected'"
            :disabled="!selectedRow?.execution_id"
            :aria-pressed="logScope === 'selected'"
            @click="logScope = 'selected'"
          >
            Execution logs
          </Button>
        </div>
      </div>
      <p
        v-if="logScope === 'selected'"
        class="text-xs text-foreground-muted mb-3"
      >
        <template v-if="selectedRow?.type === 'user'">
          Logs belong to the containing invocation; code-span correlation is not recorded.
        </template>
        <template v-else>
          Logs for the selected invocation.
        </template>
      </p>
      <p
        v-if="!visibleLogs.length"
        class="text-xs text-foreground-muted py-2"
      >
        No structured logs in this view.
      </p>
      <ol
        v-else
        class="space-y-1 font-mono text-xs"
        aria-label="Structured log entries"
      >
        <li
          v-for="entry in visibleLogs"
          :key="entry.id"
          class="rounded px-2 py-2"
          :class="entry.level === 'error' ? 'bg-danger-tint' : 'hover:bg-surface/60'"
        >
          <div class="flex flex-wrap items-baseline gap-x-2 gap-y-1">
            <time class="text-xs text-foreground-muted tabular-nums">{{ formatLogTime(entry.ts) }}</time>
            <span
              class="text-xs uppercase tracking-label"
              :class="logLevelClass(entry.level)"
            >{{ entry.level }}</span>
            <span class="text-foreground-strong break-words">{{ entry.message }}</span>
          </div>
          <details
            v-if="entry.fields"
            class="mt-1"
          >
            <summary class="touch-expand-sm min-h-6 inline-flex items-center gap-1.5 cursor-pointer list-none text-xs text-foreground-muted hover:text-foreground-strong focus:outline-none focus-visible:ring-2 focus-visible:ring-primary rounded [&::-webkit-details-marker]:hidden">
              <ChevronRight
                class="w-3.5 h-3.5 shrink-0 transition-transform group-open:rotate-90"
                aria-hidden="true"
              />
              Fields
            </summary>
            <pre class="overflow-x-auto rounded bg-background p-3 text-foreground whitespace-pre-wrap">{{ prettyJSON(entry.fields) }}</pre>
          </details>
        </li>
      </ol>
    </div>
  </section>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { ChevronRight, ExternalLink } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import { buildTraceRows, logsForRow, traceExtent, timeWindow, focusWindow, spanGeometry, formatTraceTime } from '@/utils/traceLayout'

const props = defineProps({ trace: { type: Object, required: true } })
defineEmits(['open-invocation'])

const selectedKey = ref('')
const logScope = ref('all')
const rows = computed(() => buildTraceRows(props.trace))
const selectedRow = computed(() => rows.value.find((row) => row.key === selectedKey.value) || null)
const visibleLogs = computed(() => logScope.value === 'selected'
  ? logsForRow(props.trace.log_entries, selectedRow.value)
  : logsForRow(props.trace.log_entries, null))

watch(selectedRow, (row) => {
  if (!row) { selectedKey.value = ''; logScope.value = 'all' }
})
watch(() => props.trace.trace_id, () => { selectedKey.value = ''; logScope.value = 'all'; viewport.value = null })

const select = (row) => {
  selectedKey.value = selectedKey.value === row.key ? '' : row.key
  logScope.value = selectedKey.value && row.execution_id ? 'selected' : 'all'
}

const extent = computed(() => traceExtent(props.trace, rows.value))
const viewport = ref(null)
const window = computed(() => viewport.value ? timeWindow(viewport.value.start, viewport.value.end, extent.value) : { start: 0, end: extent.value })
const ticks = computed(() => [0, (window.value.end - window.value.start) / 2, window.value.end - window.value.start])
const rulerLabel = (ms) => {
  const width = window.value.end - window.value.start
  const [unit, size] = width >= 3600000 ? ['h', 3600000] : width >= 60000 ? ['min', 60000] : width >= 1000 ? ['s', 1000] : ['ms', 1]
  return `${Number((ms / size).toFixed(3))} ${unit}`
}
const fitTrace = () => { viewport.value = null }
const focusSelected = () => { if (selectedRow.value) viewport.value = focusWindow(selectedRow.value, extent.value) }
const zoom = (factor) => {
  const center = (window.value.start + window.value.end) / 2
  const half = (window.value.end - window.value.start) * factor / 2
  viewport.value = timeWindow(center - half, center + half, extent.value)
}
const geometry = (row) => spanGeometry(row, window.value)
const barStyle = (row) => {
  const bar = geometry(row)
  return { left: `${bar.left}%`, width: `${bar.width}%`, borderLeft: bar.zero ? '2px solid var(--color-foreground-strong)' : undefined,
    transform: bar.zero && bar.left === 100 ? 'translateX(-100%)' : undefined }
}
// Text and texture distinguish outcomes without relying on hue alone.
const barClass = (row) => {
  if (row.status === 'error') return 'bg-danger/80 bar-hatch'
  if (row.is_outlier) return 'bg-warning/85 bar-stripe'
  return row.type === 'user' ? 'bg-info/80' : 'bg-primary/85'
}
const indentStyle = (depth) => ({ paddingLeft: `${Math.min(depth, 6) * 0.8}rem` })
const safeKey = (key) => key.replace(/[^a-zA-Z0-9_-]/g, '-')
const baselineComparison = (row) => {
  const baseline = row.baseline_p95_ms || 0
  if (!baseline) return 'No baseline'
  const delta = Math.round((((row.duration_ms || 0) - baseline) / baseline) * 100)
  return delta === 0 ? 'At P95' : `${Math.abs(delta)}% ${delta > 0 ? 'above' : 'below'} P95`
}
const prettyJSON = (value) => {
  try { return JSON.stringify(JSON.parse(value), null, 2) } catch { return value }
}
const formatLogTime = (ts) => {
  const d = new Date(ts)
  return Number.isNaN(d.valueOf()) ? ts : `${d.toLocaleTimeString(undefined, { hour12: false })}.${String(d.getMilliseconds()).padStart(3, '0')}`
}
const logLevelClass = (level) => ({ error: 'text-danger-fg', warn: 'text-warning-fg', debug: 'text-foreground-muted' }[level] || 'text-info-fg')
</script>

<style scoped>
/* Texture overlays for span outcome (see barClass). Painted with the page
   background so the cut-out reads on any fill underneath, which keeps the fill
   itself on its own semantic token. Two different angles and periods so the two
   textures are told apart by shape, not only by the colour they sit on. */
.bar-hatch {
  background-image: repeating-linear-gradient(
    135deg,
    color-mix(in srgb, var(--color-background) 70%, transparent) 0 2px,
    transparent 2px 5px
  );
}

.bar-stripe {
  background-image: repeating-linear-gradient(
    90deg,
    color-mix(in srgb, var(--color-background) 70%, transparent) 0 2px,
    transparent 2px 6px
  );
}
</style>
