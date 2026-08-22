<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-xl font-semibold text-white tracking-tight">
        System Overview
      </h1>
      <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
        Live platform health and activity.
      </p>
    </div>

    <!-- Operational state, first, because the daily loop starts with "is
         anything broken right now". What used to sit here was a four-up row of
         icon + label + big number, which is the hero-metric template DESIGN.md
         bans by name, and none of its four numbers could answer that question:
         an instance whose webhook function started 500ing twenty minutes ago
         read as entirely green. The counts did not go away, they moved into the
         meta row below where they belong, in mono, next to each other. -->
    <section class="bg-background border border-border rounded-lg p-5 space-y-4">
      <div class="flex items-start justify-between gap-4 flex-wrap">
        <div class="min-w-0">
          <h2
            class="text-h2 font-semibold"
            :class="failures.length ? 'text-danger-fg' : 'text-white'"
          >
            {{ healthHeadline }}
          </h2>
          <p class="text-xs text-foreground-muted mt-1">
            {{ healthSubhead }}
          </p>
        </div>
        <router-link
          v-if="sample.length"
          to="/invocations"
          class="shrink-0 inline-flex items-center gap-1 text-xs text-foreground-muted hover:text-white transition-colors rounded touch-expand-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
        >
          {{ failures.length ? 'Inspect failures' : 'All invocations' }}
          <ArrowRight class="w-3.5 h-3.5" />
        </router-link>
      </div>

      <!-- Proportion, not a percentage headline: a rate computed over a
           20-sample window would read as authoritative and would not be. The
           bar shows the window, the caption names its size. -->
      <template v-if="sample.length">
        <StackedBar
          :total="sample.length"
          :segments="healthSegments"
        />
        <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-foreground-muted">
          <span class="flex items-center gap-1.5">
            <span class="w-2 h-2 rounded-full bg-success/70" />
            {{ okCount }} succeeded
          </span>
          <span
            v-if="failures.length"
            class="flex items-center gap-1.5"
          >
            <span class="w-2 h-2 rounded-full bg-danger/70" />
            {{ failures.length }} failed
          </span>
          <span>across the last {{ sample.length }} invocations</span>
        </div>
      </template>

      <div class="flex flex-wrap items-center gap-x-4 gap-y-1 border-t border-border pt-3 text-xs text-foreground-muted font-mono">
        <span>{{ system.functionsCount }} functions</span>
        <span>{{ m.active_requests ?? 0 }} in flight</span>
        <span>{{ formatBig(m.totals?.invocations ?? 0) }} invocations</span>
        <span>{{ formatPct(m.rates?.cold_start_pct) }} cold</span>
      </div>
    </section>

    <!-- The other half of "notice a failure": which ones, and a way in. -->
    <section
      v-if="failures.length"
      class="bg-background border border-border rounded-lg"
    >
      <div class="px-5 pt-5 pb-3">
        <h2 class="text-sm font-semibold text-white">
          Recent failures
        </h2>
        <div class="text-xs text-foreground-muted mt-1">
          Newest first. Open one to read its stderr and request.
        </div>
      </div>
      <ul class="divide-y divide-border">
        <li
          v-for="f in failures.slice(0, 5)"
          :key="f.id"
        >
          <router-link
            :to="`/invocations?execution=${f.id}`"
            class="flex items-center justify-between gap-3 px-5 py-3 hover:bg-surface-hover transition-colors focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary"
          >
            <span class="min-w-0 flex-1">
              <span class="block truncate text-sm text-white">{{ fnNameFor(f.function_id) }}</span>
              <span class="block truncate text-xs text-foreground-muted font-mono">{{ f.error_message || f.status }}</span>
            </span>
            <span class="shrink-0 flex items-center gap-3">
              <span class="hidden sm:inline text-xs text-foreground-muted font-mono">{{ f.duration_ms }}ms</span>
              <StatusBadge :status="f.status" />
            </span>
          </router-link>
        </li>
      </ul>
    </section>

    <!-- Latency + host -->
    <div class="grid grid-cols-1 lg:grid-cols-3 gap-4">
      <!-- Latency -->
      <div class="bg-background border border-border rounded-lg p-5 lg:col-span-1">
        <div class="mb-3">
          <h2 class="text-sm font-semibold text-white">
            Response time
          </h2>
          <div class="text-xs text-foreground-muted mt-1">
            Invocation latency by percentile.
          </div>
        </div>
        <LatencyBars
          :p50="m.latency_ms?.p50"
          :p95="m.latency_ms?.p95"
          :p99="m.latency_ms?.p99"
        />
      </div>

      <!-- Host resources — single stacked memory bar tells the whole story -->
      <div class="bg-background border border-border rounded-lg p-5 lg:col-span-2 space-y-5">
        <div>
          <h2 class="text-sm font-semibold text-white">
            Host machine
          </h2>
          <div class="text-xs text-foreground-muted mt-1">
            Capacity and current memory use.
          </div>
        </div>

        <div class="grid grid-cols-2 gap-4 text-sm">
          <div>
            <div class="text-xs uppercase tracking-wider text-foreground-muted">
              CPU / worker slots
            </div>
            <div class="text-lg font-mono text-white mt-0.5">
              {{ m.host?.num_cpu ?? '?' }} / {{ m.host?.effective_cpu_workers ?? '?' }}
            </div>
          </div>
          <div>
            <div class="text-xs uppercase tracking-wider text-foreground-muted">
              Memory in use
            </div>
            <div class="text-lg font-mono text-white mt-0.5">
              {{ formatMB(memUsed) }} <span class="text-foreground-muted text-sm">/ {{ formatMB(memTotal) }}</span>
            </div>
            <div class="text-xs text-foreground-muted mt-0.5">
              {{ memUsedPct.toFixed(1) }}% used · {{ formatMB(memEffective) }} allocatable
            </div>
          </div>
        </div>

        <!-- Stacked bar: ACTUAL usage (total = in use + free), what `docker stats`
             reflects. The warm-pool reservation is a separate admission-control
             budget that overlaps actual usage, so it's called out below rather
             than shown as its own segment (which would double-count). -->
        <div class="space-y-2">
          <!-- Both fills are lifted so each clears 3:1 against the separator
               rule StackedBar draws between them. The previous pair
               (bg-info/70 against bg-success/40) measured 2.06:1 across the
               boundary, which is the one thing this bar exists to show. -->
          <StackedBar
            :total="memTotal"
            :segments="[
              { label: 'In use', value: memUsed, color: 'bg-info' },
              { label: 'Free', value: memFree, color: 'bg-success/55' },
            ]"
          />
          <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-foreground-muted">
            <span class="flex items-center gap-1.5">
              <span class="w-2 h-2 rounded-full bg-info" />
              {{ formatMB(memUsed) }} in use
            </span>
            <span class="flex items-center gap-1.5">
              <span class="w-2 h-2 rounded-full bg-success/55" />
              {{ formatMB(memFree) }} free
            </span>
            <span>
              {{ formatMB(memReserved) }} reserved for warm pools
            </span>
          </div>
        </div>
      </div>
    </div>

    <!-- Build pipeline + sandbox stats -->
    <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
      <!-- Build pipeline -->
      <div class="bg-background border border-border rounded-lg p-5 space-y-3">
        <div>
          <h2 class="text-sm font-semibold text-white">
            Builds
          </h2>
          <div class="text-xs text-foreground-muted mt-1">
            Deployment work in progress.
          </div>
        </div>
        <div class="grid grid-cols-3 gap-3">
          <Stat
            label="In queue"
            :value="m.build_queue?.pending ?? 0"
          />
          <Stat
            label="Build workers"
            :value="m.build_queue?.workers ?? 0"
          />
          <Stat
            label="Built so far"
            :value="formatBig(m.totals?.builds ?? 0)"
          />
        </div>
        <div
          v-if="(m.totals?.build_errors ?? 0) > 0"
          class="text-xs text-danger-fg flex items-center gap-1.5 pt-1"
        >
          <span class="w-1.5 h-1.5 rounded-full bg-danger" />
          {{ m.totals.build_errors }} build{{ m.totals.build_errors === 1 ? ' has' : 's have' }} failed since start
        </div>
      </div>

      <!-- Sandbox -->
      <div class="bg-background border border-border rounded-lg p-5 space-y-3">
        <div>
          <h2 class="text-sm font-semibold text-white">
            Sandbox activity
          </h2>
          <div class="text-xs text-foreground-muted mt-1">
            Current sandbox reuse and startup activity.
          </div>
        </div>
        <div class="grid grid-cols-3 gap-3">
          <Stat
            label="Running now"
            :value="m.sandbox?.active ?? 0"
          />
          <Stat
            label="Reused"
            :value="formatBig(m.totals?.warm_hits ?? 0)"
          />
          <Stat
            label="Spawned fresh"
            :value="formatBig(m.totals?.cold_starts ?? 0)"
          />
        </div>
      </div>
    </div>

    <!-- Per-function pools: live capacity and the two useful resource signals. -->
    <div v-if="(m.pools || []).length">
      <div class="flex items-baseline justify-between mb-3">
        <div>
          <h2 class="text-sm font-semibold text-white">
            Warm pools ({{ m.pools.length }})
          </h2>
          <div class="text-xs text-foreground-muted mt-1">
            Ready sandboxes by function.
          </div>
        </div>
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
        <div
          v-for="p in m.pools"
          :key="p.function_id"
          class="bg-background border border-border rounded-lg p-4 space-y-3"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <router-link
                v-if="p.function_name"
                :to="{ name: 'function-detail', params: { name: p.function_name } }"
                class="touch-expand-xs block truncate text-sm font-medium text-white hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
              >
                {{ p.function_name }}
              </router-link>
              <div
                v-else
                class="truncate font-mono text-sm font-medium text-white"
              >
                {{ p.function_id }}
              </div>
            </div>
            <div class="text-right shrink-0">
              <div class="text-xs text-foreground-muted">
                Capacity
              </div>
              <div class="text-xs font-mono text-white">
                {{ p.effective_max }} max
              </div>
              <div class="text-[11px] text-foreground-muted">
                {{ formatLimit(p.limiting_reason) }}
              </div>
            </div>
          </div>

          <div class="grid grid-cols-3 gap-2">
            <PoolStat
              label="Ready / desired"
              :value="`${p.idle} / ${p.desired_workers}`"
            />
            <PoolStat
              label="Busy / queued"
              :value="`${p.busy} / ${p.queued}`"
            />
            <PoolStat
              label="Calls / sec"
              :value="formatRate(p.stable_rate)"
            />
          </div>

          <div>
            <Sparkline :points="poolHistoryFor(p.function_id)" />
            <div class="text-xs text-foreground-muted mt-1">
              Traffic, last 5 minutes
            </div>
          </div>

          <div class="grid grid-cols-2 gap-3 border-t border-border pt-3 text-xs">
            <div>
              <div class="text-foreground-muted">
                Service p95
              </div>
              <div class="mt-0.5 font-mono text-white">
                {{ p.service_p95_ms?.toFixed?.(1) ?? 0 }} ms
              </div>
            </div>
            <div>
              <div class="text-foreground-muted">
                Queue / cold p95
              </div>
              <div class="mt-0.5 font-mono text-white">
                {{ p.queue_wait_p95_ms?.toFixed?.(1) ?? 0 }}
                <span class="text-foreground-muted">/ {{ p.cold_start_p95_ms?.toFixed?.(1) ?? 0 }} ms</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
    <!--
      Empty state for first-time visitors. Promotes the only meaningful
      action (deploy a function) from muted body copy to a primary CTA.
      Without this, an operator who has just spun up the container
      lands on the Dashboard, sees four "0" tiles, and has no obvious
      next step. The CTA routes straight to /functions/new.
    -->
    <div
      v-else
      class="bg-background border border-border rounded-lg p-8 text-center space-y-4"
    >
      <div>
        <div class="text-sm text-white">
          No warm pools yet
        </div>
        <div class="text-xs text-foreground-muted mt-1 max-w-prose mx-auto leading-body">
          Deploy a function to start collecting runtime metrics.
        </div>
      </div>
      <div>
        <Button @click="$router.push('/functions/new')">
          <Plus class="w-4 h-4" />
          Deploy your first function
        </Button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { EMPTY } from '@/utils/format'
import { computed, onMounted, onUnmounted, h } from 'vue'
import { ArrowRight, Plus } from '@lucide/vue'
import { useSystemStore } from '@/stores/system'
import Button from '@/components/common/Button.vue'
import StatusBadge from '@/components/common/StatusBadge.vue'

const system = useSystemStore()

const m = computed(() => system.metrics || {})

const poolHistoryFor = (fnId) => system.poolHistory[fnId] || []

// The metrics endpoint carries no failure counter (totals is invocations,
// cold_starts, warm_hits, builds, build_errors), so the health readout is
// computed from the execution window the store already fetches. That window is
// a sample, not a rate, and the copy says so rather than rounding it into a
// percentage that would look like a measurement.
const sample = computed(() => system.recentInvocations || [])
const failures = computed(() => sample.value.filter((e) => e.status !== 'success'))
const okCount = computed(() => sample.value.length - failures.value.length)

const healthSegments = computed(() => [
  { label: 'Succeeded', value: okCount.value, color: 'bg-success/70' },
  { label: 'Failed', value: failures.value.length, color: 'bg-danger/70' },
])

const healthHeadline = computed(() => {
  if (!sample.value.length) return 'No invocations yet'
  const n = failures.value.length
  if (!n) return 'No failures in recent activity'
  return `${n} failed invocation${n === 1 ? '' : 's'}`
})

const healthSubhead = computed(() => {
  if (!sample.value.length) return 'Deploy a function and invoke it to start collecting activity.'
  if (!failures.value.length) return 'Every recent invocation returned a success status.'
  return 'Newest failures are listed below.'
})

// Executions carry function_id only. The store keeps the id-to-name map from
// its function listing; the warm-pool list is a second source but an incomplete
// one, since a function scaled to zero has no pool entry, and a function that is
// failing is exactly the one an operator may have already scaled down.
const fnNameFor = (id) => {
  const named = system.functionNames?.[id]
  if (named) return named
  const pool = (m.value.pools || []).find((x) => x.function_id === id)
  return pool?.function_name || id
}

const formatPct = (v) => (v == null ? EMPTY : `${v.toFixed(1)}%`)
const formatRate = (v) => (v == null ? '0' : v.toFixed(1))
const formatLimit = (v) => (v ? v.replaceAll('_', ' ') : 'calculating')

// Compact human-readable byte sizes. Server reports memory in MB; we show
// GB once we cross 1 GB so the host card doesn't overflow with five-digit
// numbers like "11961 MB".
const formatMB = (mb) => {
  const v = mb || 0
  if (v >= 1024) return `${(v / 1024).toFixed(1)} GB`
  return `${Math.round(v)} MB`
}
// Compact integers — 12345 → 12.3k, 1234567 → 1.2M. Used in tiles where
// the raw count would otherwise dominate the visual weight of the card.
const formatBig = (n) => {
  const v = Number(n) || 0
  if (v >= 1_000_000) return `${(v / 1_000_000).toFixed(1)}M`
  if (v >= 1_000)     return `${(v / 1_000).toFixed(1)}k`
  return String(v)
}

// Memory derived state — kept here so the template stays declarative.
const memTotal     = computed(() => m.value.host?.mem_total_mb ?? 0)
// Reserved = the warm-pool admission-control budget (Σ ready+busy workers ×
// 1.5 × memory_mb). It's headroom the scheduler holds, NOT actual consumption.
const memReserved  = computed(() => m.value.host?.mem_reserved_mb ?? 0)
// Actual host usage = total - available (from /proc/meminfo) — this is what
// `docker stats`/`free` show. Idle warm sandboxes use a fraction of their
// reservation, so memUsed is typically far below memReserved.
const memAvailable = computed(() => m.value.host?.mem_available_mb ?? 0)
const memEffective = computed(() => m.value.host?.effective_memory_capacity_mb ?? 0)
const memUsed      = computed(() => Math.max(0, memTotal.value - memAvailable.value))
const memFree      = computed(() => Math.max(0, memTotal.value - memUsed.value))
const memUsedPct   = computed(() => (memTotal.value > 0 ? (memUsed.value / memTotal.value) * 100 : 0))

onMounted(() => system.connect())
onUnmounted(() => system.disconnect())

// Three horizontal bars normalised against the p99 (the worst case).
// p50 sits in green, p95 amber, p99 red — a visual hint of the long-tail
// shape without turning the panel into a chart. When all three values
// are similar the bars look uniform; when latency tail-heavy the p99
// extends well past p50.
const LatencyBars = {
  props: { p50: Number, p95: Number, p99: Number },
  setup(p) {
    return () => {
      // Latency bars use semantic status tints — p50 = success (the
      // happy-path baseline), p95 = warning (degraded but acceptable),
      // p99 = danger (worst-case 1-in-100). Future palette change is
      // a four-token edit, not a six-site rewrite.
      const rows = [
        { label: 'p50', ms: p.p50, color: 'bg-success/70' },
        { label: 'p95', ms: p.p95, color: 'bg-warning/70' },
        // p99 runs at full strength: bg-danger/70 over the track measured
        // 2.87:1, under the 3:1 WCAG 1.4.11 floor for a graphical object.
        { label: 'p99', ms: p.p99, color: 'bg-danger' },
      ]
      // Anchor bar widths to the worst observed value so the relative
      // shape is obvious. If all three are ~equal the bars sit near full;
      // if p99 is much higher, p50 and p95 collapse — exactly the read
      // operators want from a glance at the panel.
      const max = Math.max(p.p50 || 0, p.p95 || 0, p.p99 || 0, 1)
      return h('div', { class: 'space-y-2.5' },
        rows.map((r) => {
          const pct = r.ms == null ? 0 : (r.ms / max) * 100
          return h('div', { class: 'space-y-1' }, [
            h('div', { class: 'flex items-baseline justify-between text-xs' }, [
              h('span', { class: 'font-mono uppercase text-foreground-muted tracking-wider' }, r.label),
              h('span', { class: 'font-mono text-white' }, r.ms == null ? EMPTY : `${r.ms}ms`),
            ]),
            // Track is thicker on phones: a 6 px rule is legible on a desktop
            // monitor at arm's length and close to invisible in the hand.
            // The fill scales rather than resizing — animating width relayouts
            // the row on every metrics poll, and the house rule is transform
            // and opacity only.
            h('div', {
              class: 'h-2 sm:h-1.5 bg-surface rounded overflow-hidden',
              role: 'img',
              'aria-label': `${r.label} latency: ${r.ms == null ? 'no samples yet' : `${r.ms} milliseconds`}`,
            }, [
              h('div', {
                class: `h-full w-full origin-left ${r.color} transition-transform duration-500 ease-out motion-reduce:transition-none`,
                style: { transform: `scaleX(${(pct / 100).toFixed(4)})` },
              }),
            ]),
          ])
        })
      )
    }
  },
}

// Stat: compact label and value used in Builds and Sandbox cards.
const Stat = {
  props: { label: String, value: [String, Number] },
  setup(p) {
    return () =>
      h('div', { class: 'bg-surface border border-border rounded p-3 flex flex-col h-full' }, [
        h('div', { class: 'text-xs uppercase tracking-wider text-foreground-muted' }, p.label),
        h('div', { class: 'text-lg font-mono text-white mt-0.5' }, String(p.value ?? 0)),
      ])
  },
}

// PoolStat: compact version for per-function cards.
const PoolStat = {
  props: { label: String, value: [String, Number] },
  setup(p) {
    return () =>
      h('div', { class: 'bg-surface border border-border rounded p-2.5 flex flex-col h-full' }, [
        h('div', { class: 'text-xs uppercase tracking-wider text-foreground-muted' }, p.label),
        h('div', { class: 'text-base font-mono text-white mt-0.5 leading-none' }, String(p.value ?? 0)),
      ])
  },
}

// StackedBar: one row, multiple coloured segments adding up to total.
// Used by the host memory panel so a single bar conveys "of total RAM,
// X is held by warm pools, Y is free" without two separate gauges.
//
// Segments are parted by a 2px rule in the page background instead of abutting.
// The boundary between two segments is the only thing this bar exists to show,
// and WCAG 1.4.11 wants 3:1 across it — which no pair drawn here can reach on a
// near-black canvas. in-use/free measured 2.06:1, and lifting both toward each
// other makes it worse (bg-info + bg-success/70 lands at 1.88:1); succeeded and
// failed are green against red, which sit within half a stop of each other at
// any alpha. A dark rule sidesteps the pair: each fill only has to clear 3:1
// against the rule, and every token used below does.
const StackedBar = {
  props: {
    total:    { type: Number, required: true },
    segments: { type: Array,  required: true }, // [{ label, value, color }]
  },
  setup(p) {
    return () => {
      const total = p.total > 0 ? p.total : 1
      // Empty segments are dropped rather than drawn at 0% width: the separator
      // border would still paint, notching the bar for data that isn't there.
      const drawn = p.segments.filter((s) => s.value > 0)
      return h('div', {
        class: 'h-3 sm:h-2.5 bg-surface rounded overflow-hidden flex',
        role: 'img',
        'aria-label': p.segments.map((s) => `${s.label}: ${s.value} of ${p.total}`).join('; '),
      },
        drawn.map((seg, i) =>
          h('div', {
            class: `h-full ${seg.color}${i > 0 ? ' border-l-2 border-background' : ''}`,
            style: { width: `${((seg.value / total) * 100).toFixed(2)}%` },
            title: `${seg.label}: ${seg.value}`,
          })
        )
      )
    }
  },
}

const Sparkline = {
  props: { points: { type: Array, default: () => [] } },
  setup(p) {
    return () => {
      const pts = p.points || []
      if (pts.length < 2) {
        return h('div', { class: 'h-10 sm:h-8 flex items-center text-xs text-foreground-muted' }, 'Collecting samples…')
      }
      const max = Math.max(...pts, 1)
      const w = 100
      const hh = 32
      const step = w / (pts.length - 1)
      const path = pts
        .map((v, i) => {
          const x = (i * step).toFixed(2)
          const y = (hh - (v / max) * hh).toFixed(2)
          return `${i === 0 ? 'M' : 'L'}${x},${y}`
        })
        .join(' ')
      const peak = Math.max(...pts)
      return h(
        'svg',
        {
          // preserveAspectRatio="none" stretches the 100-unit viewBox to
          // whatever the card is wide, which also stretches the stroke: the
          // same line rendered thin-and-wide on a desktop card and noticeably
          // heavier in a narrow phone card. non-scaling-stroke pins the stroke
          // to 1.5 device-independent px at every width, so the trace reads
          // identically on a 375 px phone and a 1920 px monitor.
          viewBox: `0 0 ${w} ${hh}`,
          // --color-link, not --color-primary: the trace is the only rendering
          // of per-pool traffic on the card (there is no numeric readout beside
          // it), and primary on the card background measures 2.25:1, well under
          // the 3:1 WCAG 1.4.11 floor for a graphical object. link clears 5:1.
          class: 'w-full h-10 sm:h-8 text-link',
          preserveAspectRatio: 'none',
          role: 'img',
          'aria-label': `Traffic over the last 5 minutes, ${pts.length} samples, peak ${peak} per second`,
        },
        [h('path', {
          d: path,
          fill: 'none',
          stroke: 'currentColor',
          'stroke-width': '1.5',
          'stroke-linecap': 'round',
          'stroke-linejoin': 'round',
          'vector-effect': 'non-scaling-stroke',
        })]
      )
    }
  },
}
</script>
