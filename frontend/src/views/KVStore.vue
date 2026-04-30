<template>
  <div class="space-y-6">
    <!-- Header: function context + count/size badge + + Set key CTA -->
    <div class="flex items-start justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-white tracking-tight">
          KV Store
        </h1>
        <p class="text-xs text-foreground-muted mt-1">
          Per-function key/value state for
          <router-link
            :to="`/functions/${fnName}`"
            class="text-white underline"
          >
            {{ fnName }}
          </router-link>
          — values are JSON, optional TTL.
        </p>
      </div>
      <div class="flex items-center gap-2">
        <span class="text-xs text-foreground-muted">
          {{ total }} {{ total === 1 ? 'key' : 'keys' }}
          <span v-if="totalSize > 0">· {{ formatBytes(totalSize) }}</span>
        </span>
        <Button
          variant="secondary"
          size="sm"
          @click="refresh"
        >
          <RefreshCw class="w-3.5 h-3.5" :class="{ 'animate-spin': loading }" />
          Refresh
        </Button>
        <Button
          size="sm"
          @click="openSet()"
        >
          <Plus class="w-3.5 h-3.5" />
          Set key
        </Button>
      </div>
    </div>

    <!-- Filter strip: prefix search. Other platforms (Cloudflare KV,
         Upstash) ship just this for browse — text input handles 95% of
         the actual operator workflow. -->
    <div class="flex items-center gap-2 flex-wrap">
      <div class="relative flex-1 min-w-[260px] max-w-[420px]">
        <Search class="w-3.5 h-3.5 absolute left-2.5 top-1/2 -translate-y-1/2 text-foreground-muted/60 pointer-events-none" />
        <input
          v-model="prefix"
          placeholder="Search by key prefix… (e.g. user:)"
          class="w-full bg-background border border-border rounded-md pl-8 pr-3 py-1.5 text-xs text-foreground placeholder-foreground-muted/60 focus:outline-none focus:border-white"
          @input="onPrefixInput"
        >
      </div>
      <button
        v-if="prefix"
        class="text-[11px] text-foreground-muted hover:text-white px-2 py-1.5 transition-colors"
        @click="prefix = ''; refresh()"
      >
        Clear
      </button>
      <span
        v-if="truncated"
        class="text-[11px] text-amber-400/80"
      >
        Showing first {{ rows.length }} — narrow the prefix to see more.
      </span>
    </div>

    <!-- Table -->
    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <table class="w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th class="px-4 py-3">Key</th>
            <th class="px-4 py-3 hidden md:table-cell">Value preview</th>
            <th class="px-4 py-3 w-28 hidden sm:table-cell">TTL</th>
            <th class="px-4 py-3 w-20 hidden lg:table-cell">Size</th>
            <th class="px-4 py-3 w-28 hidden md:table-cell">Updated</th>
            <th class="px-4 py-3 w-10 text-right" />
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr
            v-for="row in rows"
            :key="row.key"
            class="hover:bg-surface/40 cursor-pointer transition-colors"
            @click="openInspect(row)"
          >
            <td class="px-4 py-3 font-mono text-xs text-white truncate max-w-[360px]">
              {{ row.key }}
            </td>
            <td class="px-4 py-3 font-mono text-xs text-foreground-muted truncate max-w-[420px] hidden md:table-cell">
              {{ valuePreview(row.value) }}
            </td>
            <td class="px-4 py-3 hidden sm:table-cell">
              <span
                v-if="row.expires_at"
                class="text-xs"
                :class="ttlClass(row.expires_at)"
              >
                {{ formatTTL(row.expires_at) }}
              </span>
              <span v-else class="text-foreground-muted text-xs">—</span>
            </td>
            <td class="px-4 py-3 text-xs font-mono text-foreground-muted hidden lg:table-cell">
              {{ formatBytes(row.size_bytes) }}
            </td>
            <td class="px-4 py-3 text-xs text-foreground-muted hidden md:table-cell">
              {{ formatRelative(row.updated_at) }}
            </td>
            <td
              class="px-4 py-3 text-right"
              @click.stop
            >
              <IconButton
                :icon="Trash2"
                variant="danger"
                title="Delete key"
                @click="confirmDelete(row)"
              />
            </td>
          </tr>
          <tr v-if="!loading && !rows.length">
            <td colspan="6" class="px-4 py-12 text-center text-foreground-muted text-sm">
              <template v-if="prefix">
                No keys match
                <code class="bg-surface px-1.5 py-0.5 rounded text-xs font-mono">{{ prefix }}</code>.
              </template>
              <template v-else>
                No keys yet — your function will write here when it calls
                <code class="bg-surface px-1.5 py-0.5 rounded text-xs font-mono">orva.kv.put(...)</code>.
              </template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Inspect / edit drawer.
         Same drawer everywhere: full key + TTL countdown + JSON
         textarea + Save / Delete. The textarea uses native validation
         (parse on save) so we don't bring in a Monaco-style editor for
         what's almost always <1 KB JSON. -->
    <Drawer
      v-model="inspect.open"
      :title="inspect.row ? inspect.row.key : 'Inspect key'"
      width="640px"
    >
      <div v-if="inspect.row" class="p-5 space-y-5 text-sm">
        <!-- Stat strip -->
        <div class="grid grid-cols-2 gap-3">
          <div class="bg-surface border border-border rounded p-3 min-w-0">
            <div class="text-[10px] uppercase tracking-wider text-foreground-muted mb-1">Key</div>
            <div class="text-xs text-white font-mono break-all">{{ inspect.row.key }}</div>
          </div>
          <div class="bg-surface border border-border rounded p-3 min-w-0">
            <div class="text-[10px] uppercase tracking-wider text-foreground-muted mb-1">TTL</div>
            <div class="text-xs text-white font-mono" :class="ttlClass(inspect.row.expires_at)">
              {{ inspect.row.expires_at ? formatTTL(inspect.row.expires_at) : 'Never' }}
            </div>
          </div>
          <div class="bg-surface border border-border rounded p-3 min-w-0">
            <div class="text-[10px] uppercase tracking-wider text-foreground-muted mb-1">Updated</div>
            <div class="text-xs text-white font-mono truncate">{{ formatFullTime(inspect.row.updated_at) }}</div>
          </div>
          <div class="bg-surface border border-border rounded p-3 min-w-0">
            <div class="text-[10px] uppercase tracking-wider text-foreground-muted mb-1">Size</div>
            <div class="text-xs text-white font-mono">{{ formatBytes(inspect.row.size_bytes) }}</div>
          </div>
        </div>

        <!-- TTL editor (number input). Operator can extend / shorten the
             expiry. Empty string + 0 = "never". -->
        <div>
          <label class="text-xs uppercase tracking-wider text-foreground-muted">
            TTL seconds (0 = never)
          </label>
          <input
            v-model.number="inspect.ttlSeconds"
            type="number"
            min="0"
            class="mt-2 w-full bg-surface border border-border rounded px-3 py-2 text-sm text-white font-mono focus:outline-none focus:border-white"
          >
        </div>

        <!-- Value editor -->
        <div>
          <div class="flex items-center justify-between mb-2">
            <label class="text-xs uppercase tracking-wider text-foreground-muted">Value (JSON)</label>
            <span
              v-if="inspect.error"
              class="text-xs text-red-400"
            >{{ inspect.error }}</span>
          </div>
          <textarea
            v-model="inspect.text"
            rows="14"
            spellcheck="false"
            class="w-full bg-surface border border-border rounded p-3 text-xs text-white font-mono leading-relaxed focus:outline-none focus:border-white whitespace-pre overflow-x-auto"
          />
        </div>
      </div>

      <template #footer>
        <div class="flex items-center justify-between">
          <Button
            variant="danger"
            size="sm"
            :disabled="saving"
            @click="confirmDeleteFromDrawer"
          >
            <Trash2 class="w-3.5 h-3.5" />
            Delete
          </Button>
          <div class="flex items-center gap-2">
            <Button
              variant="ghost"
              size="sm"
              @click="inspect.open = false"
            >
              Cancel
            </Button>
            <Button
              size="sm"
              :disabled="saving"
              :loading="saving"
              @click="saveInspect"
            >
              Save
            </Button>
          </div>
        </div>
      </template>
    </Drawer>

    <!-- Set-key drawer: same layout, key field is editable, no Delete. -->
    <Drawer
      v-model="setKey.open"
      title="Set key"
      width="640px"
    >
      <div class="p-5 space-y-5 text-sm">
        <div>
          <label class="text-xs uppercase tracking-wider text-foreground-muted">Key</label>
          <input
            v-model="setKey.key"
            placeholder="e.g. user:42"
            class="mt-2 w-full bg-surface border border-border rounded px-3 py-2 text-sm text-white font-mono focus:outline-none focus:border-white"
            spellcheck="false"
          >
        </div>
        <div>
          <label class="text-xs uppercase tracking-wider text-foreground-muted">
            TTL seconds (0 = never)
          </label>
          <input
            v-model.number="setKey.ttlSeconds"
            type="number"
            min="0"
            class="mt-2 w-full bg-surface border border-border rounded px-3 py-2 text-sm text-white font-mono focus:outline-none focus:border-white"
          >
        </div>
        <div>
          <div class="flex items-center justify-between mb-2">
            <label class="text-xs uppercase tracking-wider text-foreground-muted">Value (JSON)</label>
            <span
              v-if="setKey.error"
              class="text-xs text-red-400"
            >{{ setKey.error }}</span>
          </div>
          <textarea
            v-model="setKey.text"
            rows="14"
            spellcheck="false"
            placeholder='{"hello": "world"}'
            class="w-full bg-surface border border-border rounded p-3 text-xs text-white font-mono leading-relaxed focus:outline-none focus:border-white whitespace-pre overflow-x-auto"
          />
        </div>
      </div>
      <template #footer>
        <div class="flex items-center justify-end gap-2">
          <Button
            variant="ghost"
            size="sm"
            @click="setKey.open = false"
          >
            Cancel
          </Button>
          <Button
            size="sm"
            :disabled="saving || !setKey.key.trim()"
            :loading="saving"
            @click="saveSetKey"
          >
            Save
          </Button>
        </div>
      </template>
    </Drawer>
  </div>
</template>

<script setup>
import { ref, reactive, computed, onMounted, onActivated, onDeactivated } from 'vue'
import { useRoute } from 'vue-router'
import { Search, RefreshCw, Plus, Trash2 } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import IconButton from '@/components/common/IconButton.vue'
import Drawer from '@/components/common/Drawer.vue'
import { kvList, kvPut, kvDelete } from '@/api/endpoints'
import { useConfirmStore } from '@/stores/confirm'

const route = useRoute()
const confirmStore = useConfirmStore()

const fnName = computed(() => route.params.name)

// ── State ────────────────────────────────────────────────────────────
const rows = ref([])
const total = ref(0)
const truncated = ref(false)
const loading = ref(false)
const saving = ref(false)
const prefix = ref('')

// Inspect drawer state. row holds the wire entry; text is the editable
// JSON; ttlSeconds is what we send back on Save (0 = never).
const inspect = reactive({
  open: false,
  row: null,
  text: '',
  ttlSeconds: 0,
  error: '',
})

const setKey = reactive({
  open: false,
  key: '',
  text: '',
  ttlSeconds: 0,
  error: '',
})

// ── Derived ─────────────────────────────────────────────────────────
const totalSize = computed(() =>
  rows.value.reduce((sum, r) => sum + (r.size_bytes || 0), 0),
)

// ── Fetch ───────────────────────────────────────────────────────────
const refresh = async () => {
  loading.value = true
  try {
    const params = { limit: 200 }
    if (prefix.value) params.prefix = prefix.value
    const res = await kvList(fnName.value, params)
    rows.value = res.data?.entries || []
    total.value = res.data?.total ?? rows.value.length
    truncated.value = !!res.data?.truncated
  } catch (e) {
    console.error('kvList failed', e)
    confirmStore.notify({
      title: 'Failed to load KV',
      message: e?.response?.data?.error?.message || 'Unknown error',
      danger: true,
    })
  } finally {
    loading.value = false
  }
}

let prefixTimer = null
const onPrefixInput = () => {
  clearTimeout(prefixTimer)
  prefixTimer = setTimeout(refresh, 250)
}

// ── Inspect drawer ─────────────────────────────────────────────────
const openInspect = (row) => {
  inspect.row = row
  // SDKs in different runtimes (Python's orva.kv vs the Node SDK)
  // sometimes double-encode values: a dict written via Python ends up
  // stored as a JSON string of the dict, so `row.value` arrives as
  // a string that itself parses as JSON. Detect that and unwrap before
  // pretty-printing so the textarea shows the real shape, not an
  // escape-laden one-line string.
  inspect.text = prettyJSON(row.value)
  inspect.ttlSeconds = row.expires_at ? Math.max(0, Math.floor((new Date(row.expires_at) - Date.now()) / 1000)) : 0
  inspect.error = ''
  inspect.open = true
}

const saveInspect = async () => {
  let parsed
  try {
    parsed = JSON.parse(inspect.text)
  } catch (e) {
    inspect.error = 'Invalid JSON: ' + e.message
    return
  }
  inspect.error = ''
  saving.value = true
  try {
    await kvPut(fnName.value, inspect.row.key, {
      value: parsed,
      ttl_seconds: inspect.ttlSeconds || 0,
    })
    inspect.open = false
    await refresh()
  } catch (e) {
    inspect.error = e?.response?.data?.error?.message || 'Save failed'
  } finally {
    saving.value = false
  }
}

const confirmDeleteFromDrawer = async () => {
  if (!inspect.row) return
  const ok = await confirmStore.ask({
    title: 'Delete key?',
    message: `"${inspect.row.key}" will be removed from this function's KV store. This cannot be undone.`,
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  await deleteKey(inspect.row.key)
  inspect.open = false
}

// ── Set-key drawer ─────────────────────────────────────────────────
const openSet = () => {
  setKey.key = ''
  setKey.text = ''
  setKey.ttlSeconds = 0
  setKey.error = ''
  setKey.open = true
}

const saveSetKey = async () => {
  const key = setKey.key.trim()
  if (!key) {
    setKey.error = 'Key is required'
    return
  }
  // Default to a JSON-shaped string if the textarea is empty so the
  // backend's "value must be valid JSON" check passes. Operators
  // sometimes just want to set a sentinel.
  let raw = setKey.text.trim() || '""'
  let parsed
  try {
    parsed = JSON.parse(raw)
  } catch (e) {
    setKey.error = 'Invalid JSON: ' + e.message
    return
  }
  setKey.error = ''
  saving.value = true
  try {
    await kvPut(fnName.value, key, {
      value: parsed,
      ttl_seconds: setKey.ttlSeconds || 0,
    })
    setKey.open = false
    await refresh()
  } catch (e) {
    setKey.error = e?.response?.data?.error?.message || 'Save failed'
  } finally {
    saving.value = false
  }
}

// ── Row delete (table icon button) ─────────────────────────────────
const confirmDelete = async (row) => {
  const ok = await confirmStore.ask({
    title: 'Delete key?',
    message: `"${row.key}" will be removed from this function's KV store.`,
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  await deleteKey(row.key)
}

const deleteKey = async (key) => {
  try {
    await kvDelete(fnName.value, key)
    await refresh()
  } catch (e) {
    confirmStore.notify({
      title: 'Failed to delete key',
      message: e?.response?.data?.error?.message || 'Unknown error',
      danger: true,
    })
  }
}

// ── Helpers ────────────────────────────────────────────────────────

// deepParse strips a layer of JSON-string wrapping, recursively. The
// Python SDK (orva.kv) sometimes stores dict values as their JSON
// string form, which round-trips here as a string that itself parses
// as JSON. Operators expect to see the *actual* stored shape, not
// "{\"foo\":...}" with escaped quotes; this normalises that.
const deepParse = (v, depth = 3) => {
  if (depth <= 0 || typeof v !== 'string') return v
  const s = v.trim()
  // Cheap heuristic: only attempt parse when the string looks like JSON.
  if (!s || !'{["tfn0123456789-'.includes(s[0])) return v
  try {
    const parsed = JSON.parse(s)
    return deepParse(parsed, depth - 1)
  } catch {
    return v
  }
}

// prettyJSON renders a value as multi-line indented JSON, after
// stripping any double-encoding. Used for the drawer textarea.
const prettyJSON = (v) => {
  const u = deepParse(v)
  try {
    return JSON.stringify(u, null, 2)
  } catch {
    return String(v)
  }
}

const valuePreview = (val) => {
  if (val === null || val === undefined) return '—'
  const u = deepParse(val)
  if (typeof u === 'string') {
    // Wrap in quotes so it's visually distinct from objects/numbers.
    const s = JSON.stringify(u)
    return s.length > 80 ? s.slice(0, 80) + '…' : s
  }
  try {
    const s = JSON.stringify(u)
    return s.length > 80 ? s.slice(0, 80) + '…' : s
  } catch {
    return String(u)
  }
}

const formatBytes = (n) => {
  if (n == null) return '—'
  if (n < 1024) return n + ' B'
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB'
  return (n / 1024 / 1024).toFixed(1) + ' MB'
}

const formatRelative = (iso) => {
  if (!iso) return '—'
  const ms = Date.now() - new Date(iso).getTime()
  if (ms < 0) return 'just now'
  const s = Math.floor(ms / 1000)
  if (s < 60) return s + 's ago'
  const m = Math.floor(s / 60)
  if (m < 60) return m + 'm ago'
  const h = Math.floor(m / 60)
  if (h < 24) return h + 'h ago'
  return Math.floor(h / 24) + 'd ago'
}

const formatFullTime = (iso) => (iso ? new Date(iso).toLocaleString() : '—')

const formatTTL = (iso) => {
  const ms = new Date(iso).getTime() - Date.now()
  if (ms <= 0) return 'expired'
  const s = Math.floor(ms / 1000)
  if (s < 60) return 'in ' + s + 's'
  const m = Math.floor(s / 60)
  if (m < 60) return 'in ' + m + 'm'
  const h = Math.floor(m / 60)
  if (h < 24) return 'in ' + h + 'h ' + (m % 60) + 'm'
  return 'in ' + Math.floor(h / 24) + 'd ' + (h % 24) + 'h'
}

const ttlClass = (iso) => {
  if (!iso) return 'text-foreground-muted'
  const ms = new Date(iso).getTime() - Date.now()
  if (ms <= 60_000) return 'text-red-400'
  if (ms <= 5 * 60_000) return 'text-amber-400'
  return 'text-foreground-muted'
}

// ── Lifecycle ──────────────────────────────────────────────────────
onMounted(refresh)
onActivated(refresh)
onDeactivated(() => {
  clearTimeout(prefixTimer)
})
</script>
