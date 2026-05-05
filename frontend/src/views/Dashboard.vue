<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-xl font-semibold text-white tracking-tight">System Overview</h1>
      <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">Live snapshot of what your platform is doing right now.</p>
    </div>

    <!-- Top-line numbers — every tile has a one-line "what does this mean" -->
    <div class="grid grid-cols-2 md:grid-cols-4 gap-4">
      <Tile
        label="Functions"
        hint="Deployed in this workspace"
        :value="system.functionsCount"
        :icon="Boxes"
      />
      <Tile
        label="In flight"
        hint="Requests being handled right now"
        :value="m.active_requests ?? 0"
        :icon="Activity"
      />
      <Tile
        label="Invocations"
        hint="Total calls served since the platform started"
        :value="formatBig(m.totals?.invocations ?? 0)"
        :icon="TrendingUp"
      />
      <Tile
        label="Cold starts"
        hint="Calls that had to spawn a fresh sandbox"
        :value="formatPct(m.rates?.cold_start_pct)"
        :icon="Snowflake"
      />
    </div>

    <!-- Latency + host -->
    <div class="grid grid-cols-1 lg:grid-cols-3 gap-4">
      <!-- Latency -->
      <div class="bg-background border border-border rounded-lg p-5 lg:col-span-1">
        <div class="mb-3">
          <h2 class="text-xs font-bold text-white uppercase tracking-wider">
            Response time
          </h2>
          <div class="text-[11px] text-foreground-muted mt-1">
            How long calls take to come back. p99 is the worst-case 1-in-100.
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
          <h2 class="text-xs font-bold text-white uppercase tracking-wider">
            Host machine
          </h2>
          <div class="text-[11px] text-foreground-muted mt-1">
            The server Orva is running on, and how much of its RAM your warm sandboxes are holding.
          </div>
        </div>

        <div class="grid grid-cols-2 gap-4 text-sm">
          <div>
            <div class="text-[10px] uppercase tracking-wider text-foreground-muted">CPU cores</div>
            <div class="text-lg font-mono text-white mt-0.5">{{ m.host?.num_cpu ?? '?' }}</div>
            <div class="text-[11px] text-foreground-muted mt-0.5">
              available to functions on this host
            </div>
          </div>
          <div>
            <div class="text-[10px] uppercase tracking-wider text-foreground-muted">Memory in use</div>
            <div class="text-lg font-mono text-white mt-0.5">
              {{ formatMB(memReserved) }} <span class="text-foreground-muted text-sm">/ {{ formatMB(memTotal) }}</span>
            </div>
            <div class="text-[11px] text-foreground-muted mt-0.5">
              {{ memUsedPct.toFixed(1) }}% reserved by warm sandbox pools
            </div>
          </div>
        </div>

        <!-- Stacked bar: total = reserved + free. Shows the whole picture in one row. -->
        <div class="space-y-2">
          <StackedBar
            :total="memTotal"
            :segments="[
              { label: 'Reserved by warm pools', value: memReserved, color: 'bg-info/70' },
              { label: 'Free',                   value: memFree,     color: 'bg-success/40' },
            ]"
          />
          <div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-foreground-muted">
            <span class="flex items-center gap-1.5">
              <span class="w-2 h-2 rounded-full bg-info/70" />
              {{ formatMB(memReserved) }} held by warm sandboxes ready to serve
            </span>
            <span class="flex items-center gap-1.5">
              <span class="w-2 h-2 rounded-full bg-success/40" />
              {{ formatMB(memFree) }} free for new pools or other workloads
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
          <h2 class="text-xs font-bold text-white uppercase tracking-wider">
            Builds
          </h2>
          <div class="text-[11px] text-foreground-muted mt-1">
            Where deploys go: extracted, dependencies installed, then activated.
          </div>
        </div>
        <div class="grid grid-cols-3 gap-3">
          <Stat
            label="In queue"
            :value="m.build_queue?.pending ?? 0"
            hint="waiting to start"
          />
          <Stat
            label="Build workers"
            :value="m.build_queue?.workers ?? 0"
            hint="parallel slots"
          />
          <Stat
            label="Built so far"
            :value="formatBig(m.totals?.builds ?? 0)"
            hint="lifetime total"
          />
        </div>
        <div
          v-if="(m.totals?.build_errors ?? 0) > 0"
          class="text-xs text-red-400 flex items-center gap-1.5 pt-1"
        >
          <span class="w-1.5 h-1.5 rounded-full bg-red-400" />
          {{ m.totals.build_errors }} build{{ m.totals.build_errors === 1 ? ' has' : 's have' }} failed since start
        </div>
      </div>

      <!-- Sandbox -->
      <div class="bg-background border border-border rounded-lg p-5 space-y-3">
        <div>
          <h2 class="text-xs font-bold text-white uppercase tracking-wider">
            Sandbox activity
          </h2>
          <div class="text-[11px] text-foreground-muted mt-1">
            Each invocation runs inside an isolated nsjail sandbox process.
          </div>
        </div>
        <div class="grid grid-cols-3 gap-3">
          <Stat
            label="Running now"
            :value="m.sandbox?.active ?? 0"
            hint="serving a request"
          />
          <Stat
            label="Reused"
            :value="formatBig(m.totals?.warm_hits ?? 0)"
            hint="warm-pool hits"
          />
          <Stat
            label="Spawned fresh"
            :value="formatBig(m.totals?.cold_starts ?? 0)"
            hint="cold starts"
          />
        </div>
      </div>
    </div>

    <!-- Per-function pool cards — each card explains what it is. -->
    <div v-if="(m.pools || []).length">
      <div class="flex items-baseline justify-between mb-3">
        <div>
          <h2 class="text-xs font-bold text-white uppercase tracking-wider">
            Warm pools ({{ m.pools.length }})
          </h2>
          <div class="text-[11px] text-foreground-muted mt-1">
            One pool per active function. Sandboxes stay ready so the next call doesn't pay a cold start.
          </div>
        </div>
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
        <div
          v-for="p in m.pools"
          :key="p.function_id"
          class="bg-background border border-border rounded-lg p-4 space-y-3"
        >
          <!-- Header -->
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <div class="text-sm font-medium text-white truncate">{{ p.function_name || p.function_id }}</div>
              <div class="text-[10px] text-foreground-muted font-mono truncate">{{ p.function_id }}</div>
            </div>
            <div class="text-right shrink-0">
              <div class="text-[10px] text-foreground-muted">Target / cap</div>
              <div class="text-xs font-mono text-white">
                {{ p.target }} <span class="text-foreground-muted">/</span> {{ p.dynamic_max }}
              </div>
            </div>
          </div>

          <!-- Right-now snapshot -->
          <div class="grid grid-cols-3 gap-2">
            <PoolStat
              label="Ready"
              :value="p.idle"
              hint="idle workers"
            />
            <PoolStat
              label="Busy"
              :value="p.busy"
              hint="serving now"
            />
            <PoolStat
              label="Calls / sec"
              :value="formatRate(p.rate_ewma)"
              hint="recent rate"
            />
          </div>

          <!-- Sparkline of incoming rate -->
          <div>
            <Sparkline :points="poolHistoryFor(p.function_id)" />
            <div class="text-[10px] text-foreground-muted mt-1">
              Recent calls per second (last 5 min)
            </div>
          </div>

          <!-- Lifetime + resource averages -->
          <div class="border-t border-border pt-3 grid grid-cols-2 gap-3 text-[11px]">
            <div>
              <div class="text-foreground-muted">Spawned · killed</div>
              <div class="font-mono text-white">{{ p.spawned }} · {{ p.killed }}</div>
            </div>
            <div>
              <div class="text-foreground-muted">Avg latency</div>
              <div class="font-mono text-white">{{ p.latency_ewma_ms?.toFixed?.(1) ?? 0 }} ms</div>
            </div>
            <div>
              <div class="text-foreground-muted">Avg memory</div>
              <div class="font-mono text-white" title="Average memory used per invocation vs allocated limit">
                {{ p.mem_used_avg_mb > 0 ? '~' + Math.round(p.mem_used_avg_mb) : EMPTY }}
                <span class="text-foreground-muted">/ {{ p.mem_limit_mb }} MB</span>
              </div>
            </div>
            <div>
              <div class="text-foreground-muted">Avg CPU</div>
              <div class="font-mono text-white" title="Average CPU cores consumed per invocation vs allocated">
                {{ p.cpu_frac_avg > 0 && p.cpu_limit > 0 ? (p.cpu_frac_avg * p.cpu_limit).toFixed(2) : EMPTY }}
                <span v-if="p.cpu_limit > 0" class="text-foreground-muted">/ {{ p.cpu_limit }} CPU</span>
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
        <div class="text-sm text-white">No warm pools yet</div>
        <div class="text-xs text-foreground-muted mt-1 max-w-prose mx-auto leading-body">
          Deploy your first function to see live worker pools, latency,
          and cold-start rate land in the tiles above.
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
import { Activity, Boxes, TrendingUp, Snowflake, Plus } from 'lucide-vue-next'
import { useSystemStore } from '@/stores/system'
import Button from '@/components/common/Button.vue'

const system = useSystemStore()

const m = computed(() => system.metrics || {})

const poolHistoryFor = (fnId) => system.poolHistory[fnId] || []

const formatPct = (v) => (v == null ? EMPTY : `${v.toFixed(1)}%`)
const formatRate = (v) => (v == null ? '0' : v.toFixed(1))

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
const memTotal    = computed(() => m.value.host?.mem_total_mb ?? 0)
const memReserved = computed(() => m.value.host?.mem_reserved_mb ?? 0)
const memFree     = computed(() => Math.max(0, memTotal.value - memReserved.value))
const memUsedPct  = computed(() => (memTotal.value > 0 ? (memReserved.value / memTotal.value) * 100 : 0))

onMounted(() => system.connect())
onUnmounted(() => system.disconnect())

// ── Tile: top-line metric card with icon + label + big number + hint ──
// `h-full` so siblings in a grid row stretch to the tallest. The number
// owns the visual weight; the hint underneath says what the number means.
const Tile = {
  props: { label: String, value: [String, Number], icon: Object, hint: String },
  setup(p) {
    return () =>
      h('div', {
        class: 'bg-background border border-border rounded-lg p-5 flex flex-col h-full hover:border-primary/50 transition-colors group',
      }, [
        h('div', { class: 'flex items-center justify-between mb-3' }, [
          h('span', { class: 'text-xs font-medium text-foreground-muted uppercase tracking-wide' }, p.label),
          p.icon ? h(p.icon, { class: 'w-4 h-4 text-foreground-muted group-hover:text-primary' }) : null,
        ]),
        h('div', { class: 'text-2xl font-mono text-foreground leading-none' }, String(p.value)),
        p.hint ? h('div', { class: 'text-[11px] text-foreground-muted mt-auto pt-3 leading-snug' }, p.hint) : null,
      ])
  },
}

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
        { label: 'p99', ms: p.p99, color: 'bg-danger/70' },
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
            h('div', { class: 'flex items-baseline justify-between text-[11px]' }, [
              h('span', { class: 'font-mono uppercase text-foreground-muted tracking-wider' }, r.label),
              h('span', { class: 'font-mono text-white' }, r.ms == null ? EMPTY : `${r.ms}ms`),
            ]),
            h('div', { class: 'h-1.5 bg-surface rounded overflow-hidden' }, [
              h('div', {
                class: `h-full ${r.color} transition-[width] duration-500 ease-out`,
                style: { width: `${pct.toFixed(1)}%` },
              }),
            ]),
          ])
        })
      )
    }
  },
}

// Stat: bigger label, value, and a one-line hint. Used in the Builds and
// Sandbox cards where each metric deserves a sentence of context. Same
// height across siblings so the row reads as a unit.
const Stat = {
  props: { label: String, value: [String, Number], hint: String },
  setup(p) {
    return () =>
      h('div', { class: 'bg-surface border border-border rounded p-3 flex flex-col h-full' }, [
        h('div', { class: 'text-[10px] uppercase tracking-wider text-foreground-muted' }, p.label),
        h('div', { class: 'text-lg font-mono text-white mt-0.5' }, String(p.value ?? 0)),
        p.hint ? h('div', { class: 'text-[10px] text-foreground-muted mt-auto pt-1.5 leading-snug' }, p.hint) : null,
      ])
  },
}

// PoolStat: compact version for the per-function cards. Slightly smaller
// number, hint underneath, fixed-height so the three columns in a pool
// card always line up vertically.
const PoolStat = {
  props: { label: String, value: [String, Number], hint: String },
  setup(p) {
    return () =>
      h('div', { class: 'bg-surface border border-border rounded p-2.5 flex flex-col h-full' }, [
        h('div', { class: 'text-[10px] uppercase tracking-wider text-foreground-muted' }, p.label),
        h('div', { class: 'text-base font-mono text-white mt-0.5 leading-none' }, String(p.value ?? 0)),
        p.hint ? h('div', { class: 'text-[10px] text-foreground-muted mt-auto pt-1.5' }, p.hint) : null,
      ])
  },
}

// StackedBar: one row, multiple coloured segments adding up to total.
// Used by the host memory panel so a single bar conveys "of total RAM,
// X is held by warm pools, Y is free" without two separate gauges.
const StackedBar = {
  props: {
    total:    { type: Number, required: true },
    segments: { type: Array,  required: true }, // [{ label, value, color }]
  },
  setup(p) {
    return () => {
      const total = p.total > 0 ? p.total : 1
      return h('div', {
        class: 'h-2.5 bg-surface rounded overflow-hidden flex',
        role: 'img',
        'aria-label': p.segments.map((s) => `${s.label}: ${s.value} of ${p.total}`).join('; '),
      },
        p.segments.map((seg) =>
          h('div', {
            class: `h-full ${seg.color}`,
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
        return h('div', { class: 'h-8 flex items-center text-[10px] text-foreground-muted' }, '(collecting samples…)')
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
      return h(
        'svg',
        { viewBox: `0 0 ${w} ${hh}`, class: 'w-full h-8 text-blue-400', preserveAspectRatio: 'none' },
        [h('path', { d: path, fill: 'none', stroke: 'currentColor', 'stroke-width': '1.5' })]
      )
    }
  },
}
</script>
