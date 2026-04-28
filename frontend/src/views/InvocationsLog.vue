<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-xl font-semibold text-white tracking-tight">
          Invocation Logs
        </h1>
        <p class="text-xs text-foreground-muted mt-1">
          Click any row to inspect status, latency, and stderr.
        </p>
      </div>
      <Button variant="secondary" @click="refresh">
        <RefreshCw class="w-4 h-4 mr-2" :class="{ 'animate-spin': loading }" />
        Refresh
      </Button>
    </div>

    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <table class="w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th class="px-6 py-3 font-medium">Time</th>
            <th class="px-6 py-3 font-medium">Function</th>
            <th class="px-6 py-3 font-medium">Status</th>
            <th class="px-6 py-3 font-medium hidden md:table-cell">Cold</th>
            <th class="px-6 py-3 font-medium hidden lg:table-cell">HTTP</th>
            <th class="px-6 py-3 font-medium hidden sm:table-cell">Duration</th>
            <th class="px-6 py-3 font-medium text-right hidden xl:table-cell">ID</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr
            v-for="log in logs"
            :key="log.id"
            class="hover:bg-surface/50 transition-colors cursor-pointer"
            @click="openDetail(log)"
          >
            <td class="px-6 py-4 text-foreground">
              {{ formatTime(log.started_at) }}
            </td>
            <td class="px-6 py-4 font-medium text-white">
              <span
                class="hover:underline"
                @click.stop="router.push('/functions/' + getFnName(log.function_id))"
              >
                {{ getFnName(log.function_id) }}
              </span>
            </td>
            <td class="px-6 py-4">
              <StatusBadge :status="log.status" />
            </td>
            <td class="px-6 py-4 hidden md:table-cell">
              <span
                v-if="log.cold_start"
                class="inline-flex items-center px-2 py-0.5 rounded text-xs border bg-background font-mono text-blue-400 border-blue-900/40"
              >
                cold
              </span>
              <span v-else class="text-foreground-muted text-xs">—</span>
            </td>
            <td class="px-6 py-4 text-foreground-muted font-mono text-xs hidden lg:table-cell">
              {{ log.status_code ?? '—' }}
            </td>
            <td class="px-6 py-4 text-foreground-muted font-mono text-xs hidden sm:table-cell">
              {{ log.duration_ms != null ? log.duration_ms + 'ms' : '—' }}
            </td>
            <td class="px-6 py-4 text-right text-foreground-muted font-mono text-xs hidden xl:table-cell">
              {{ log.id?.substring(0, 12) }}
            </td>
          </tr>
          <tr v-if="logs.length === 0">
            <td colspan="7" class="px-6 py-8 text-center text-foreground-muted">
              No invocations yet.
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <Drawer v-model="drawerOpen" :title="drawerTitle" width="640px">
      <div v-if="detailLoading" class="p-6 text-sm text-foreground-muted">
        Loading…
      </div>
      <div v-else-if="!selected" class="p-6 text-sm text-foreground-muted">
        Nothing selected.
      </div>
      <div v-else class="p-5 space-y-5">
        <!-- Status badges row -->
        <div class="flex items-center gap-2 flex-wrap">
          <StatusBadge :status="selected.status" />
          <span
            v-if="selected.cold_start"
            class="inline-flex items-center px-2.5 py-1 rounded text-xs border bg-background font-mono text-blue-400 border-blue-900/40"
          >
            cold start
          </span>
          <span
            v-if="selected.status_code"
            class="inline-flex items-center px-2.5 py-1 rounded text-xs border bg-background font-mono text-foreground-muted"
          >
            HTTP {{ selected.status_code }}
          </span>
        </div>

        <!-- Stat grid -->
        <div class="grid grid-cols-2 gap-3 text-sm">
          <Stat label="Duration" :value="selected.duration_ms != null ? selected.duration_ms + ' ms' : '—'" />
          <Stat label="Response size" :value="selected.response_size != null ? formatBytes(selected.response_size) : '—'" />
          <Stat label="Started" :value="formatTime(selected.started_at)" />
          <Stat label="Finished" :value="selected.finished_at ? formatTime(selected.finished_at) : '—'" />
          <Stat label="Function" :value="getFnName(selected.function_id)" />
          <Stat label="Execution ID" :value="selected.id" mono />
        </div>

        <!-- Error message -->
        <div v-if="selected.error_message">
          <div class="text-xs uppercase tracking-wider text-foreground-muted mb-2">Error</div>
          <pre class="bg-red-950/30 border border-red-900/40 rounded p-3 text-xs text-red-300 font-mono whitespace-pre-wrap break-words">{{ selected.error_message }}</pre>
        </div>

        <!-- Stderr tail -->
        <div>
          <div class="flex items-center justify-between mb-2">
            <div class="text-xs uppercase tracking-wider text-foreground-muted">
              Stderr <span class="text-[10px] normal-case text-foreground-muted/70">(stdout is the response body — not stored)</span>
            </div>
            <button
              v-if="stderrText"
              class="text-xs text-foreground-muted hover:text-white"
              @click="copy(stderrText)"
            >
              {{ copied ? 'copied!' : 'copy' }}
            </button>
          </div>
          <pre
            class="bg-surface border border-border rounded p-3 text-xs text-foreground font-mono overflow-auto max-h-72 whitespace-pre-wrap break-words"
          >{{ stderrText || '(no stderr captured)' }}</pre>
        </div>
      </div>
    </Drawer>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { RefreshCw } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import Drawer from '@/components/common/Drawer.vue'
import StatusBadge from '@/components/common/StatusBadge.vue'
import { listInvocations, getInvocation, getInvocationLogs, listFunctions } from '@/api/endpoints'
import { copyText } from '@/utils/clipboard'

const router = useRouter()
const logs = ref([])
const loading = ref(false)
const drawerOpen = ref(false)
const detailLoading = ref(false)
const selected = ref(null)
const stderrText = ref('')
const copied = ref(false)
const fnMap = ref({})
let pollTimer = null

const drawerTitle = computed(() =>
  selected.value ? `Invocation · ${selected.value.id?.substring(0, 14)}` : 'Invocation'
)

const Stat = {
  props: { label: String, value: [String, Number], mono: Boolean },
  template: `
    <div class="bg-surface border border-border rounded p-3">
      <div class="text-[10px] uppercase tracking-wider text-foreground-muted mb-1">{{ label }}</div>
      <div class="text-sm text-white" :class="mono && 'font-mono text-xs'">{{ value }}</div>
    </div>`,
}

const formatTime = (ts) => (ts ? new Date(ts).toLocaleString() : '—')

const formatBytes = (n) => {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

const getFnName = (id) => fnMap.value[id] || id?.slice(0, 12) || '?'

const loadFnMap = async () => {
  try {
    const res = await listFunctions()
    ;(res.data.functions || []).forEach((f) => (fnMap.value[f.id] = f.name))
  } catch {}
}

const fetchLogs = async () => {
  loading.value = true
  try {
    const res = await listInvocations({ limit: 100 })
    logs.value = res.data.executions || []
  } catch (e) {
    console.error('Failed to fetch logs:', e)
  } finally {
    loading.value = false
  }
}

const refresh = () => fetchLogs()

const openDetail = async (log) => {
  selected.value = log
  drawerOpen.value = true
  detailLoading.value = true
  stderrText.value = ''
  copied.value = false
  try {
    const [detailRes, logsRes] = await Promise.allSettled([
      getInvocation(log.id),
      getInvocationLogs(log.id),
    ])
    if (detailRes.status === 'fulfilled') {
      // Server returns the full Execution row — overlay over the row data.
      selected.value = { ...log, ...detailRes.value.data }
    }
    if (logsRes.status === 'fulfilled') {
      stderrText.value = logsRes.value.data.stderr || ''
    }
  } finally {
    detailLoading.value = false
  }
}

const copy = async (text) => {
  if (await copyText(text)) {
    copied.value = true
    setTimeout(() => (copied.value = false), 1500)
  }
}

onMounted(async () => {
  await loadFnMap()
  await fetchLogs()
  pollTimer = setInterval(fetchLogs, 5000)
})

onUnmounted(() => {
  clearInterval(pollTimer)
})
</script>
