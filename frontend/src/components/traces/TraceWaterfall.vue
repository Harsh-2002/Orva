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
        class="flex items-center gap-3 text-xs text-foreground-muted"
        aria-label="Waterfall legend"
      >
        <span class="inline-flex items-center gap-1.5"><i class="w-2.5 h-2.5 rounded-sm bg-primary" />Warm</span>
        <span class="inline-flex items-center gap-1.5"><i class="w-2.5 h-2.5 rounded-sm bg-info" />Code</span>
        <span class="inline-flex items-center gap-1.5"><i class="w-2.5 h-2.5 rounded-sm bg-warning bar-stripe" />Outlier</span>
        <span class="inline-flex items-center gap-1.5"><i class="w-2.5 h-2.5 rounded-sm bg-danger bar-hatch" />Error</span>
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
          <span class="grid grid-cols-1 sm:grid-cols-[minmax(13rem,0.9fr)_minmax(12rem,1.4fr)_5rem] gap-2 sm:items-center">
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
                </span>
                <span class="block text-xs text-foreground-muted truncate">{{ row.relationship }}</span>
              </span>
              <span class="sr-only">{{ accessibleStatus(row) }}</span>
            </span>

            <span
              class="relative block h-5 w-full rounded bg-surface"
              :aria-label="`Starts at ${row.offset_ms || 0} milliseconds and runs for ${row.duration_ms || 0} milliseconds`"
            >
              <span
                class="absolute top-1 h-3 rounded-sm min-w-px"
                :class="barClass(row)"
                :style="barStyle(row)"
              />
            </span>

            <span class="flex sm:block justify-between items-center font-mono text-xs sm:text-right">
              <span class="sm:hidden text-foreground-muted">+{{ row.offset_ms || 0 }}ms</span>
              <span class="text-foreground-strong">{{ row.duration_ms || 0 }}ms</span>
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
                  {{ row.cold_start ? 'Cold start' : 'Warm' }}
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
            <button
              v-if="row.execution_id"
              type="button"
              class="touch-expand-sm inline-flex h-8 items-center gap-2 rounded-md px-3 text-xs text-foreground border border-border hover:bg-surface-hover focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
              @click="$emit('open-invocation', row.execution_id)"
            >
              Open invocation <ExternalLink
                class="w-3.5 h-3.5"
                aria-hidden="true"
              />
            </button>
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
            <summary class="touch-expand-sm min-h-6 inline-flex items-center cursor-pointer text-foreground-muted hover:text-foreground-strong focus:outline-none focus-visible:ring-2 focus-visible:ring-primary rounded">
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
          <button
            type="button"
            class="touch-expand-xs h-7 px-2.5 rounded-md text-xs border focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
            :class="logScope === 'all' ? 'bg-primary text-primary-foreground border-primary' : 'bg-surface text-foreground-muted border-border'"
            :aria-pressed="logScope === 'all'"
            @click="logScope = 'all'"
          >
            All logs
          </button>
          <button
            type="button"
            class="touch-expand-xs h-7 px-2.5 rounded-md text-xs border focus:outline-none focus-visible:ring-2 focus-visible:ring-primary disabled:opacity-50"
            :class="logScope === 'selected' ? 'bg-primary text-primary-foreground border-primary' : 'bg-surface text-foreground-muted border-border'"
            :disabled="!selectedRow"
            :aria-pressed="logScope === 'selected'"
            @click="logScope = 'selected'"
          >
            Selected span
          </button>
        </div>
      </div>
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
            <summary class="touch-expand-sm min-h-6 inline-flex items-center cursor-pointer text-xs text-foreground-muted hover:text-foreground-strong focus:outline-none focus-visible:ring-2 focus-visible:ring-primary rounded">
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
import { buildTraceRows, logsForRow } from '@/utils/traceLayout'

const props = defineProps({ trace: { type: Object, required: true } })
defineEmits(['open-invocation'])

const selectedKey = ref('')
const logScope = ref('all')
const rows = computed(() => buildTraceRows(props.trace))
const selectedRow = computed(() => rows.value.find((row) => row.key === selectedKey.value) || null)
const visibleLogs = computed(() => logScope.value === 'selected'
  ? logsForRow(props.trace.log_entries, selectedRow.value)
  : (props.trace.log_entries || []))

watch(selectedRow, (row) => {
  if (!row && logScope.value === 'selected') logScope.value = 'all'
})

const select = (row) => {
  selectedKey.value = selectedKey.value === row.key ? '' : row.key
}

const total = computed(() => Math.max(1, props.trace.total_duration_ms || 1))
const barStyle = (row) => {
  const left = Math.max(0, Math.min(100, ((row.offset_ms || 0) / total.value) * 100))
  const width = Math.max(0.75, Math.min(100 - left, ((row.duration_ms || 0) / total.value) * 100))
  return { left: `${left}%`, width: `${width}%` }
}
// Outcome carries a second visual channel besides hue. The collapsed row never
// prints row.status — the word only appears once a row is expanded — so a
// 20-span waterfall scanned for the failure was red-vs-violet or amber-vs-violet
// and nothing else, which either common colour deficiency collapses (WCAG
// 1.4.1). Error bars are hatched, outlier bars striped; the textures are defined
// in the scoped block below and repeated on the legend swatches.
const barClass = (row) => {
  if (row.status === 'error') return 'bg-danger/80 bar-hatch'
  if (row.is_outlier) return 'bg-warning/85 bar-stripe'
  return row.type === 'user' ? 'bg-info/80' : 'bg-primary/85'
}
const indentStyle = (depth) => ({ paddingLeft: `${Math.min(depth, 6) * 0.8}rem` })
const safeKey = (key) => key.replace(/[^a-zA-Z0-9_-]/g, '-')
const accessibleStatus = (row) => row.type === 'user'
  ? `${row.status}. Duration ${row.duration_ms || 0} milliseconds.`
  : `${row.status}. ${row.cold_start ? 'Cold start.' : 'Warm.'} Duration ${row.duration_ms || 0} milliseconds.`
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
