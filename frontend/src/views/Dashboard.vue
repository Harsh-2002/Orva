<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-xl font-semibold text-foreground tracking-tight">System Overview</h1>
      </div>
      <div
        class="flex items-center gap-2 text-xs"
        :class="system.isConnected ? 'text-success' : 'text-error'"
        :title="system.isConnected ? 'Live event stream connected' : 'Reconnecting…'"
      >
        <span class="relative flex h-2 w-2">
          <span
            v-if="system.isConnected"
            class="animate-ping absolute inline-flex h-full w-full rounded-full bg-success opacity-60"
          />
          <span
            class="relative inline-flex rounded-full h-2 w-2"
            :class="system.isConnected ? 'bg-success' : 'bg-error'"
          />
        </span>
        {{ system.isConnected ? 'Live' : 'Reconnecting…' }}
      </div>
    </div>

    <!-- Top-line numbers -->
    <div class="grid grid-cols-2 md:grid-cols-4 gap-4">
      <Tile label="Functions"          :value="system.functionsCount" :icon="Boxes" />
      <Tile label="Active requests"    :value="m.active_requests ?? 0" :icon="Activity" />
      <Tile label="Total invocations"  :value="m.totals?.invocations ?? 0" :icon="TrendingUp" />
      <Tile label="Cold-start %"       :value="formatPct(m.rates?.cold_start_pct)" :icon="Snowflake" />
    </div>

    <!-- Latency + host -->
    <div class="grid grid-cols-1 lg:grid-cols-3 gap-4">
      <!-- Latency -->
      <div class="bg-background border border-border rounded-lg p-5 lg:col-span-1">
        <div class="flex items-center justify-between mb-3">
          <div class="text-xs font-bold text-white uppercase tracking-wider">
            Latency
          </div>
          <div class="text-[10px] text-foreground-muted font-mono">
            ~last 8k calls
          </div>
        </div>
        <LatencyBars
          :p50="m.latency_ms?.p50"
          :p95="m.latency_ms?.p95"
          :p99="m.latency_ms?.p99"
        />
      </div>

      <!-- Host resources -->
      <div class="bg-background border border-border rounded-lg p-5 lg:col-span-2 space-y-4">
        <div class="text-xs font-bold text-white uppercase tracking-wider">Host</div>
        <div class="grid grid-cols-2 gap-4 text-sm">
          <div>
            <div class="text-[10px] uppercase tracking-wider text-foreground-muted">CPU</div>
            <div class="text-lg font-mono text-white">{{ m.host?.num_cpu ?? '?' }}</div>
            <div class="text-[10px] text-foreground-muted">cores</div>
          </div>
          <div>
            <div class="text-[10px] uppercase tracking-wider text-foreground-muted">Goroutines</div>
            <div class="text-lg font-mono text-white">{{ m.host?.num_goroutines ?? '?' }}</div>
            <div class="text-[10px] text-foreground-muted">live</div>
          </div>
        </div>

        <!-- Memory bars -->
        <div class="space-y-3 pt-1">
          <Bar
            label="Available"
            :value="m.host?.mem_available_mb ?? 0"
            :total="m.host?.mem_total_mb ?? 0"
            unit="MB"
            color="bg-green-500/70"
          />
          <Bar
            label="Reserved by pools"
            :value="m.host?.mem_reserved_mb ?? 0"
            :total="m.host?.mem_total_mb ?? 0"
            unit="MB"
            color="bg-blue-500/70"
          />
        </div>
      </div>
    </div>

    <!-- Build pipeline + sandbox stats -->
    <div class="grid grid-cols-1 md:grid-cols-2 gap-4">
      <div class="bg-background border border-border rounded-lg p-5 space-y-3">
        <div class="text-xs font-bold text-white uppercase tracking-wider">Build pipeline</div>
        <div class="grid grid-cols-3 gap-3 text-sm">
          <Stat label="Pending"   :value="m.build_queue?.pending ?? 0" />
          <Stat label="Workers"   :value="m.build_queue?.workers ?? 0" />
          <Stat label="Total"     :value="m.totals?.builds ?? 0" />
        </div>
        <div v-if="(m.totals?.build_errors ?? 0) > 0" class="text-xs text-red-400">
          {{ m.totals.build_errors }} build error(s) lifetime
        </div>
      </div>

      <div class="bg-background border border-border rounded-lg p-5 space-y-3">
        <div class="text-xs font-bold text-white uppercase tracking-wider">Sandbox</div>
        <div class="grid grid-cols-3 gap-3 text-sm">
          <Stat label="Active"     :value="m.sandbox?.active ?? 0" />
          <Stat label="Lifetime"   :value="m.sandbox?.total ?? 0" />
          <Stat label="Cold/Warm"  :value="(m.totals?.cold_starts ?? 0) + ' / ' + (m.totals?.warm_hits ?? 0)" />
        </div>
      </div>
    </div>

    <!-- Per-function pool cards -->
    <div v-if="(m.pools || []).length">
      <div class="text-xs font-bold text-white uppercase tracking-wider mb-3">
        Warm pools ({{ m.pools.length }})
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
        <div
          v-for="p in m.pools"
          :key="p.function_id"
          class="bg-background border border-border rounded-lg p-4 space-y-3"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <div class="text-sm font-medium text-white truncate">{{ p.function_name || p.function_id }}</div>
              <div class="text-[10px] text-foreground-muted font-mono truncate">{{ p.function_id }}</div>
            </div>
            <div class="text-right">
              <div class="text-xs text-foreground-muted">target {{ p.target }}</div>
              <div class="text-[10px] text-foreground-muted">cap {{ p.dynamic_max }}</div>
            </div>
          </div>

          <div class="grid grid-cols-3 gap-2 text-xs">
            <Mini label="idle" :value="p.idle" />
            <Mini label="busy" :value="p.busy" />
            <Mini label="rate" :value="formatRate(p.rate_ewma)" suffix="rps" />
          </div>

          <Sparkline :points="poolHistoryFor(p.function_id)" />

          <div class="flex items-center justify-between text-[10px] text-foreground-muted">
            <span>↑{{ p.scale_ups }} · ↓{{ p.scale_downs }}</span>
            <span>spawn {{ p.spawned }} · kill {{ p.killed }}</span>
            <span>lat {{ p.latency_ewma_ms?.toFixed?.(1) ?? 0 }}ms</span>
          </div>
        </div>
      </div>
    </div>
    <div
      v-else
      class="bg-background border border-border rounded-lg p-8 text-center text-sm text-foreground-muted"
    >
      No warm pools yet — deploy a function to see live load metrics.
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, h } from 'vue'
import { Activity, Boxes, TrendingUp, Snowflake } from 'lucide-vue-next'
import { useSystemStore } from '@/stores/system'

const system = useSystemStore()

const m = computed(() => system.metrics || {})

const poolHistoryFor = (fnId) => system.poolHistory[fnId] || []

const formatPct = (v) => (v == null ? '—' : `${v.toFixed(1)}%`)
const formatRate = (v) => (v == null ? '0' : v.toFixed(1))

onMounted(() => system.connect())
onUnmounted(() => system.disconnect())

// ── Tiny inline render-fn components ───────────────────────────────────
const Tile = {
  props: { label: String, value: [String, Number], icon: Object },
  setup(p) {
    return () =>
      h('div', {
        class: 'bg-background border border-border rounded-lg p-5 flex flex-col justify-between h-28 hover:border-primary/50 transition-colors group',
      }, [
        h('div', { class: 'flex items-center justify-between' }, [
          h('span', { class: 'text-xs font-medium text-foreground-muted uppercase tracking-wide' }, p.label),
          p.icon ? h(p.icon, { class: 'w-4 h-4 text-foreground-muted group-hover:text-primary' }) : null,
        ]),
        h('div', { class: 'text-2xl font-mono text-foreground' }, String(p.value)),
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
      const rows = [
        { label: 'p50', ms: p.p50, color: 'bg-emerald-500/70' },
        { label: 'p95', ms: p.p95, color: 'bg-amber-500/70' },
        { label: 'p99', ms: p.p99, color: 'bg-rose-500/70' },
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
              h('span', { class: 'font-mono text-white' }, r.ms == null ? '—' : `${r.ms}ms`),
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

const Stat = {
  props: { label: String, value: [String, Number] },
  setup(p) {
    return () =>
      h('div', { class: 'bg-surface border border-border rounded p-3' }, [
        h('div', { class: 'text-[10px] uppercase tracking-wider text-foreground-muted' }, p.label),
        h('div', { class: 'text-base font-mono text-white' }, String(p.value ?? 0)),
      ])
  },
}

const Mini = {
  props: { label: String, value: [String, Number], suffix: String },
  setup(p) {
    return () =>
      h('div', { class: 'bg-surface border border-border rounded p-2 text-center' }, [
        h('div', { class: 'text-[9px] uppercase text-foreground-muted' }, p.label),
        h('div', { class: 'text-sm font-mono text-white' }, [
          String(p.value ?? 0),
          p.suffix ? h('span', { class: 'text-[9px] text-foreground-muted ml-1' }, p.suffix) : null,
        ]),
      ])
  },
}

const Bar = {
  props: { label: String, value: Number, total: Number, unit: String, color: String },
  setup(p) {
    return () => {
      const pct = p.total > 0 ? (p.value / p.total) * 100 : 0
      return h('div', null, [
        h('div', { class: 'flex items-center justify-between text-xs mb-1' }, [
          h('span', { class: 'text-foreground-muted' }, p.label),
          h('span', { class: 'font-mono text-white' }, `${p.value} / ${p.total} ${p.unit}`),
        ]),
        h('div', { class: 'h-2 bg-surface rounded overflow-hidden' }, [
          h('div', { class: `h-full ${p.color || 'bg-primary'}`, style: { width: `${pct.toFixed(1)}%` } }),
        ]),
      ])
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
