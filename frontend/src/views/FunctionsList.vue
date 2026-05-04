<template>
  <div class="space-y-4">
    <div class="flex items-center justify-between gap-4 flex-wrap">
      <h1 class="text-xl font-semibold text-white tracking-tight">
        Functions
      </h1>
      <Button @click="router.push('/functions/new')">
        <Plus class="w-4 h-4" />
        New Function
      </Button>
    </div>

    <!-- Search strip — matches the Logs filter aesthetic. -->
    <div class="flex items-center gap-2 flex-wrap">
      <div class="relative flex-1 min-w-[280px] max-w-[440px]">
        <Search class="w-3.5 h-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-foreground-muted/60 pointer-events-none" />
        <input
          v-model="search"
          placeholder="Search by name, runtime, or function id…"
          class="w-full bg-background border border-border rounded-md pl-8 pr-3 py-1.5 text-xs text-foreground placeholder-foreground-muted/60 focus:outline-none focus:border-white"
        >
      </div>
      <span class="text-[11px] text-foreground-muted">
        {{ filtered.length }} of {{ functions.length }}
      </span>
    </div>

    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <table class="w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th class="px-4 py-3 w-8">
              <input
                type="checkbox"
                :checked="allChecked"
                :indeterminate.prop="someChecked && !allChecked"
                class="w-3.5 h-3.5 rounded border-border bg-background"
                @change="toggleAll"
              >
            </th>
            <th class="px-4 py-3 font-medium">
              Name
            </th>
            <th class="px-4 py-3 font-medium hidden sm:table-cell">
              Runtime
            </th>
            <th class="px-4 py-3 font-medium hidden lg:table-cell">
              Resources
            </th>
            <th class="px-4 py-3 font-medium hidden md:table-cell">
              Function ID
            </th>
            <th class="px-4 py-3 font-medium hidden xl:table-cell">
              Last Update
            </th>
            <th class="px-4 py-3 font-medium text-right">
              Actions
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr
            v-for="fn in filtered"
            :key="fn.id"
            class="hover:bg-surface/50 transition-colors"
            :class="{ 'bg-surface/30': selected.has(fn.id) }"
          >
            <td class="px-4 py-3 align-middle">
              <input
                :checked="selected.has(fn.id)"
                type="checkbox"
                class="w-3.5 h-3.5 rounded border-border bg-background"
                @change="toggleOne(fn.id)"
              >
            </td>
            <td class="px-4 py-3 font-medium text-white">
              <div class="flex items-center gap-2 flex-wrap">
                <span>{{ fn.name }}</span>
                <span
                  v-if="fn.network_mode === 'egress'"
                  class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-full text-[10px] bg-amber-500/15 text-amber-300 border border-amber-500/30"
                  title="Outbound network enabled"
                >
                  <Globe class="w-3 h-3" /> egress
                </span>
                <span
                  v-if="fn.auth_mode && fn.auth_mode !== 'none'"
                  class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-full text-[10px] bg-sky-500/15 text-sky-300 border border-sky-500/30"
                  :title="fn.auth_mode === 'platform_key' ? 'Requires Orva API key' : 'Requires HMAC signature'"
                >
                  <Lock class="w-3 h-3" /> {{ fn.auth_mode === 'platform_key' ? 'key' : 'signed' }}
                </span>
                <span
                  v-if="fn.rate_limit_per_min > 0"
                  class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-full text-[10px] bg-violet-500/15 text-violet-300 border border-violet-500/30"
                  :title="`Rate limit: ${fn.rate_limit_per_min}/min per IP`"
                >
                  <Gauge class="w-3 h-3" /> {{ fn.rate_limit_per_min }}/m
                </span>
              </div>
            </td>
            <td class="px-4 py-3 text-foreground hidden sm:table-cell">
              <span class="inline-flex items-center px-2 py-0.5 rounded text-xs border border-border bg-background text-foreground-muted font-mono">
                {{ fn.runtime }}
              </span>
            </td>
            <td class="px-4 py-3 text-foreground-muted font-mono text-xs hidden lg:table-cell">
              {{ fn.cpus }} CPU / {{ fn.memory_mb }}MB
            </td>
            <td class="px-4 py-3 hidden md:table-cell">
              <div class="flex items-center gap-2">
                <code class="text-xs font-mono text-foreground-muted bg-surface px-2 py-1 rounded border border-border">{{ fn.id }}</code>
                <Button
                  variant="secondary"
                  size="xs"
                  :title="copiedId === fn.id ? 'Copied!' : 'Copy invoke URL to clipboard'"
                  @click="copyUrl(fn)"
                >
                  <Check
                    v-if="copiedId === fn.id"
                    class="w-3.5 h-3.5 text-success"
                  />
                  <Copy
                    v-else
                    class="w-3.5 h-3.5"
                  />
                  {{ copiedId === fn.id ? 'Copied' : 'Copy URL' }}
                </Button>
              </div>
            </td>
            <td class="px-4 py-3 text-foreground-muted hidden xl:table-cell">
              {{ new Date(fn.updated_at).toLocaleDateString() }}
            </td>
            <td class="px-4 py-3 text-right">
              <div class="inline-flex items-center gap-1">
                <IconButton
                  :icon="Pencil"
                  title="Edit function"
                  @click="router.push('/functions/' + fn.name)"
                />
                <IconButton
                  :icon="Trash2"
                  variant="danger"
                  title="Delete function"
                  :disabled="deletingId === fn.id"
                  @click="deleteFn(fn)"
                />
              </div>
            </td>
          </tr>
          <tr v-if="!filtered.length && !loading">
            <td
              colspan="7"
              class="px-6 py-8 text-center text-foreground-muted"
            >
              {{ search ? 'No matches.' : 'No functions yet — click New Function to deploy your first.' }}
            </td>
          </tr>
        </tbody>
      </table>

      <!-- Load more — offset-paginated -->
      <div
        v-if="hasMore"
        class="flex justify-center border-t border-border py-3 bg-surface/30"
      >
        <button
          class="text-xs text-foreground-muted hover:text-white transition-colors flex items-center gap-1.5"
          :disabled="loading"
          @click="loadMore"
        >
          <RefreshCw
            v-if="loading"
            class="w-3 h-3 animate-spin"
          />
          {{ loading ? 'Loading…' : `Load more (${total - functions.length} remaining)` }}
        </button>
      </div>
    </div>

    <!-- Floating bulk-action footer. Slides up when 1+ rows selected. -->
    <transition name="fade">
      <div
        v-if="selected.size"
        class="fixed bottom-4 left-1/2 -translate-x-1/2 z-30 flex items-center gap-3 bg-background border border-border shadow-2xl rounded-full pl-4 pr-2 py-2"
      >
        <span class="text-xs text-white">
          {{ selected.size }} selected
        </span>
        <span class="w-px h-4 bg-border" />
        <button
          class="text-xs text-foreground-muted hover:text-white transition-colors px-2 py-1"
          @click="selected = new Set()"
        >
          Clear
        </button>
        <Button
          variant="danger"
          size="sm"
          class="!rounded-full px-4"
          :loading="bulkDeleting"
          @click="bulkDelete"
        >
          <Trash2 class="w-3.5 h-3.5" /> Delete {{ selected.size }}
        </Button>
      </div>
    </transition>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onActivated, onDeactivated, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { Plus, Pencil, Trash2, Copy, Check, Globe, Search, RefreshCw, Lock, Gauge } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import IconButton from '@/components/common/IconButton.vue'
import apiClient from '@/api/client'
import { listFunctions } from '@/api/endpoints'
import { copyText } from '@/utils/clipboard'
import { useConfirmStore } from '@/stores/confirm'
import { useEventsStore } from '@/stores/events'

const confirmStore = useConfirmStore()
const router = useRouter()

const PAGE_SIZE = 25

const functions = ref([])
const total = ref(0)
const loading = ref(false)
const search = ref('')
const copiedId = ref('')
const deletingId = ref('')
const bulkDeleting = ref(false)
const selected = ref(new Set())

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return functions.value
  return functions.value.filter((fn) =>
    fn.name?.toLowerCase().includes(q) ||
    fn.runtime?.toLowerCase().includes(q) ||
    fn.id?.toLowerCase().includes(q),
  )
})

const hasMore = computed(() => functions.value.length < total.value)

const allChecked = computed(() =>
  filtered.value.length > 0 && filtered.value.every((fn) => selected.value.has(fn.id)),
)
const someChecked = computed(() =>
  filtered.value.some((fn) => selected.value.has(fn.id)),
)

const toggleOne = (id) => {
  const next = new Set(selected.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  selected.value = next
}
const toggleAll = () => {
  if (allChecked.value) {
    selected.value = new Set()
  } else {
    const next = new Set(selected.value)
    filtered.value.forEach((fn) => next.add(fn.id))
    selected.value = next
  }
}

const invokeUrlFor = (fn) => `${window.location.origin}/fn/${fn.id}`

const copyUrl = async (fn) => {
  const ok = await copyText(invokeUrlFor(fn))
  if (ok) {
    copiedId.value = fn.id
    setTimeout(() => { if (copiedId.value === fn.id) copiedId.value = '' }, 1500)
  } else {
    confirmStore.notify({
      title: 'Copy failed',
      message: 'Could not copy to clipboard. URL:\n\n' + invokeUrlFor(fn),
    })
  }
}

const fetchPage = async (offset) => {
  loading.value = true
  try {
    const res = await listFunctions({ limit: PAGE_SIZE, offset })
    const rows = res.data.functions || []
    total.value = res.data.total ?? rows.length
    if (offset === 0) functions.value = rows
    else functions.value = [...functions.value, ...rows]
  } catch (e) {
    console.error(e)
  } finally {
    loading.value = false
  }
}

const loadMore = () => fetchPage(functions.value.length)
const refresh = () => fetchPage(0)

const deleteFn = async (fn) => {
  const ok = await confirmStore.ask({
    title: `Delete "${fn.name}"?`,
    message: 'This is irreversible — code, deployments, secrets, and routes for this function are removed.',
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  deletingId.value = fn.id
  try {
    await apiClient.delete(`/functions/${fn.id}`)
    await refresh()
    selected.value.delete(fn.id)
    selected.value = new Set(selected.value)
  } catch (e) {
    const msg = e.response?.data?.error?.message || e.message || 'Delete failed'
    confirmStore.notify({ title: 'Delete failed', message: msg, danger: true })
  } finally {
    deletingId.value = ''
  }
}

const bulkDelete = async () => {
  const n = selected.value.size
  const ok = await confirmStore.ask({
    title: `Delete ${n} ${n === 1 ? 'function' : 'functions'}?`,
    message: 'Each one is irreversible — code, deployments, secrets, and routes are removed.',
    confirmLabel: `Delete ${n}`,
    danger: true,
  })
  if (!ok) return
  bulkDeleting.value = true
  const ids = [...selected.value]
  let failed = 0
  try {
    // Fire deletes sequentially — keeps the server's writer happy
    // and lets us surface the first failure cleanly.
    for (const id of ids) {
      try {
        await apiClient.delete(`/functions/${id}`)
      } catch {
        failed++
      }
    }
    selected.value = new Set()
    await refresh()
    if (failed) {
      confirmStore.notify({
        title: 'Some deletes failed',
        message: `${failed} of ${ids.length} could not be deleted.`,
        danger: true,
      })
    }
  } finally {
    bulkDeleting.value = false
  }
}

// Live updates via SSE — function events fire on any Set / Delete in the
// registry, deployment events fire on build phase changes (which often
// flip status to active). Coalesce both into a single throttled refresh
// so a burst of events from a deploy doesn't trigger N list fetches.
const events = useEventsStore()
let refreshTimer = null
const scheduleRefresh = () => {
  if (refreshTimer) return
  refreshTimer = setTimeout(() => {
    refreshTimer = null
    fetchPage(0)
  }, 300)
}
let unsubFn = null
let unsubDep = null

onMounted(() => {
  fetchPage(0)
  unsubFn = events.subscribe('function', scheduleRefresh)
  unsubDep = events.subscribe('deployment', scheduleRefresh)
})
onUnmounted(() => {
  if (unsubFn) { unsubFn(); unsubFn = null }
  if (unsubDep) { unsubDep(); unsubDep = null }
  if (refreshTimer) { clearTimeout(refreshTimer); refreshTimer = null }
})
// Keep-alive: refresh on re-activation in case events fired while the
// page was cached and not subscribed.
onActivated(() => fetchPage(0))
onDeactivated(() => {
  if (refreshTimer) { clearTimeout(refreshTimer); refreshTimer = null }
})
</script>
