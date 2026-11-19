<template>
  <div class="space-y-6">
    <div class="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-foreground-strong tracking-tight">
          Routes
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
          Public paths that invoke one function. Every change is checked against
          the mapping and owner authority it was read from, so a concurrent edit
          fails instead of overwriting it.
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-xs text-foreground-muted">
          {{ rows.length }} {{ rows.length === 1 ? 'route' : 'routes' }}
        </span>
        <Button
          variant="secondary"
          size="md"
          @click="refresh"
        >
          <RefreshCw
            class="w-3.5 h-3.5"
            :class="{ 'animate-spin': loading }"
          />
          Refresh
        </Button>
        <Button
          size="md"
          :disabled="isReadOnly"
          data-testid="route-new"
          @click="openCreate"
        >
          <Plus class="w-3.5 h-3.5" />
          New route
        </Button>
      </div>
    </div>

    <p
      v-if="isReadOnly"
      data-testid="route-readonly"
      class="rounded-lg border border-border bg-surface p-3 text-sm text-foreground-muted"
      role="status"
    >
      Read-only. Creating, rebinding and deleting routes needs current write
      access on this server.
    </p>

    <div
      v-if="staleNotice"
      data-testid="route-stale"
      class="rounded-lg border border-warning-ring bg-warning-tint p-3 text-sm text-warning-fg"
      role="status"
    >
      <div class="flex items-start justify-between gap-3">
        <span>{{ staleNotice }}</span>
        <button
          type="button"
          class="shrink-0 rounded px-2 -mx-2 touch-expand-sm text-xs text-warning-fg hover:text-foreground-strong transition-colors"
          @click="staleNotice = ''"
        >
          Dismiss
        </button>
      </div>
    </div>

    <LoadError
      v-if="loadError"
      what="Routes"
      :message="loadError"
      :on-retry="refresh"
    />

    <p
      v-if="truncated"
      class="text-xs text-foreground-muted"
      role="status"
    >
      Showing the first 256 mappings. Delete one to see the rest.
    </p>

    <div class="bg-background border border-border rounded-lg overflow-x-auto scrollable">
      <ul class="sm:hidden divide-y divide-border">
        <li
          v-for="row in rows"
          :key="row.path"
          class="px-4 py-3"
          data-testid="route-card"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div class="font-mono text-sm text-foreground-strong break-all">
                {{ row.path }}
              </div>
              <div class="mt-1 text-xs text-foreground-muted break-all">
                {{ nameOf(row.function_id) }}
              </div>
              <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-foreground-muted">
                <span class="font-mono">v{{ row.version }}</span>
                <span class="font-mono">{{ row.methods || '*' }}</span>
                <span
                  class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] border"
                  :class="publishedClass(row)"
                >{{ publishedLabel(row) }}</span>
              </div>
            </div>
            <div class="flex items-center gap-1 shrink-0">
              <IconButton
                :icon="Edit"
                title="Rebind this route"
                :disabled="isReadOnly || busyPath === row.path"
                @click="openRebind(row)"
              />
              <IconButton
                :icon="Trash2"
                variant="danger"
                title="Delete route"
                :disabled="isReadOnly || busyPath === row.path"
                @click="remove(row)"
              />
            </div>
          </div>
        </li>
        <li
          v-if="loaded && !loadError && !rows.length"
          class="px-4 py-12 text-center text-sm text-foreground-muted"
        >
          No custom routes yet.
        </li>
      </ul>

      <table class="hidden sm:table w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th
              scope="col"
              class="px-6 py-3 font-medium"
            >
              Path
            </th>
            <th
              scope="col"
              class="px-6 py-3 font-medium"
            >
              Function
            </th>
            <th
              scope="col"
              class="px-6 py-3 font-medium hidden md:table-cell"
            >
              Methods
            </th>
            <th
              scope="col"
              class="px-6 py-3 font-medium"
            >
              Version
            </th>
            <th
              scope="col"
              class="px-6 py-3 font-medium"
            >
              Published
            </th>
            <th
              scope="col"
              class="px-6 py-3 font-medium text-right"
            >
              Actions
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr
            v-for="row in rows"
            :key="row.path"
            class="hover:bg-surface/50 transition-colors"
            data-testid="route-row"
          >
            <td class="px-6 py-4 font-mono text-xs text-foreground-strong break-all">
              {{ row.path }}
            </td>
            <td class="px-6 py-4">
              <div class="flex flex-col">
                <span class="text-foreground-strong">{{ nameOf(row.function_id) }}</span>
                <span class="text-[11px] text-foreground-muted font-mono break-all">{{ row.function_id }}</span>
              </div>
            </td>
            <td class="px-6 py-4 text-xs text-foreground-muted font-mono hidden md:table-cell">
              {{ row.methods || '*' }}
            </td>
            <td
              class="px-6 py-4 text-xs text-foreground-muted font-mono"
              data-testid="route-version"
            >
              {{ row.version }}
            </td>
            <td class="px-6 py-4">
              <span
                class="inline-flex items-center px-2 py-0.5 rounded text-xs border"
                :class="publishedClass(row)"
                data-testid="route-published"
              >{{ publishedLabel(row) }}</span>
            </td>
            <td class="px-6 py-4 text-right">
              <div class="inline-flex items-center gap-1">
                <IconButton
                  :icon="Edit"
                  title="Rebind this route"
                  :disabled="isReadOnly || busyPath === row.path"
                  @click="openRebind(row)"
                />
                <IconButton
                  :icon="Trash2"
                  variant="danger"
                  title="Delete route"
                  :disabled="isReadOnly || busyPath === row.path"
                  @click="remove(row)"
                />
              </div>
            </td>
          </tr>
          <tr v-if="loaded && !loadError && !rows.length">
            <td
              colspan="6"
              class="px-6 py-12 text-center text-foreground-muted text-sm"
            >
              No custom routes yet.
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <Modal
      :model-value="draft.open"
      :title="draft.mode === 'rebind' ? 'Rebind route' : 'New route'"
      size="lg"
      @update:model-value="$event ? null : closeDraft()"
    >
      <div class="space-y-4">
        <div>
          <label
            for="route-path"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
          >Path</label>
          <input
            id="route-path"
            v-model="draft.path"
            data-testid="route-path-input"
            :disabled="draft.mode === 'rebind'"
            placeholder="/hooks/stripe"
            class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground font-mono disabled:opacity-60 focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
          >
          <p class="text-xs text-foreground-muted mt-1.5">
            Prefix routes end in <code class="font-mono">/*</code>. A path cannot be
            renamed in place; delete it and create the new one.
          </p>
        </div>
        <div>
          <span class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">Function</span>
          <FilterSelect
            v-model="draft.functionId"
            :options="functionOptions"
            label="Function"
            trigger-id="route-function"
            wide
          />
          <p
            v-if="!functionOptions.length"
            class="text-xs text-foreground-muted mt-1.5"
          >
            No managed functions are available to bind yet.
          </p>
        </div>
        <div>
          <label
            for="route-methods"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
          >Methods</label>
          <input
            id="route-methods"
            v-model="draft.methods"
            placeholder="*"
            class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground font-mono focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
          >
          <p class="text-xs text-foreground-muted mt-1.5">
            Leave empty for every method, or list them as
            <code class="font-mono">GET,POST</code>.
          </p>
        </div>
        <p
          v-if="draft.error"
          data-testid="route-error"
          class="text-xs text-danger-fg"
        >
          {{ draft.error }}
        </p>
      </div>
      <template #footer>
        <Button
          variant="ghost"
          @click="closeDraft"
        >
          Cancel
        </Button>
        <Button
          data-testid="route-save"
          :disabled="!canSave"
          :loading="draft.saving"
          @click="save"
        >
          {{ draft.saving ? 'Saving…' : (draft.stale ? 'Save again' : 'Save') }}
        </Button>
      </template>
    </Modal>
  </div>
</template>

<script setup>
import { computed, onActivated, onDeactivated, onMounted, reactive, ref } from 'vue'
import { Plus, RefreshCw, Edit, Trash2 } from '@lucide/vue'
import { managedExposure } from '@/api/managedExposure.js'
import Button from '@/components/common/Button.vue'
import FilterSelect from '@/components/common/FilterSelect.vue'
import IconButton from '@/components/common/IconButton.vue'
import LoadError from '@/components/common/LoadError.vue'
import Modal from '@/components/common/Modal.vue'
import { useConfirmStore } from '@/stores/confirm'

defineOptions({ name: 'ManagedRoutesPanel' })

const props = defineProps({
  // Injected so tests can drive the exact wire shape without a server.
  api: { type: Object, default: () => managedExposure },
  // { id, name } from the managed function directory; ids are the owners.
  functions: { type: Array, default: () => [] },
  readOnly: Boolean,
})

const confirmStore = useConfirmStore()
const rows = ref([])
const loaded = ref(false)
const loading = ref(false)
const loadError = ref('')
const truncated = ref(false)
const denied = ref(false)
const staleNotice = ref('')
const busyPath = ref('')

// A 403 is the only honest signal that this principal may read but not write:
// the capability endpoint reports managed mode, not per-operator permissions.
const isReadOnly = computed(() => props.readOnly || denied.value)
const functionOptions = computed(() => props.functions.map(fn => ({ value: fn.id, label: fn.name })))
const nameOf = (id) => props.functions.find(fn => fn.id === id)?.name || id

// The draft survives a conflict: the operator re-reads, then decides.
const draft = reactive({
  open: false,
  mode: 'create',
  path: '',
  functionId: '',
  methods: '',
  saving: false,
  stale: false,
  error: '',
})

const canSave = computed(() => !draft.saving && draft.path.trim() !== '' && draft.functionId !== '')

const publishedLabel = (row) => (row.published ? 'published' : 'not published')
const publishedClass = (row) => (row.published
  ? 'bg-success-tint text-success-fg border-success-ring'
  : 'bg-surface text-foreground-muted border-border')

async function refresh() {
  loading.value = true
  try {
    const result = await props.api.listRoutes()
    rows.value = result.routes
    truncated.value = result.truncated
    loadError.value = ''
  } catch (error) {
    if (error?.forbidden) {
      denied.value = true
      loadError.value = 'Reading routes needs read access on this server.'
    } else {
      loadError.value = error?.message || 'Request failed'
    }
  } finally {
    loading.value = false
    loaded.value = true
  }
}

function openCreate() {
  if (isReadOnly.value) return
  Object.assign(draft, { open: true, mode: 'create', path: '', functionId: '', methods: '', saving: false, stale: false, error: '' })
}

function openRebind(row) {
  if (isReadOnly.value) return
  Object.assign(draft, {
    open: true,
    mode: 'rebind',
    path: row.path,
    functionId: row.function_id,
    methods: row.methods === '*' ? '' : (row.methods || ''),
    saving: false,
    stale: false,
    error: '',
  })
}

function closeDraft() {
  draft.open = false
  draft.stale = false
  draft.error = ''
}

async function save() {
  if (!canSave.value || isReadOnly.value) return
  const path = draft.path.trim()
  const functionId = draft.functionId
  draft.saving = true
  draft.error = ''
  draft.stale = false
  try {
    // Never reuse a token from the list: read the mapping and its owners now,
    // so a rebind also carries the current owner and the intended one.
    const fresh = await props.api.inspectRoute(path, { functionId })
    await props.api.setRoute({
      path,
      functionId,
      methods: draft.methods.trim(),
      version: fresh.version,
      expected: fresh.expected,
    })
    closeDraft()
    await refresh()
    confirmStore.notify({ title: 'Route saved', message: path })
  } catch (error) {
    if (error?.forbidden) {
      denied.value = true
      draft.error = 'You do not have write access to routes.'
    } else if (error?.stale) {
      draft.stale = true
      draft.error = 'The route or its owner authority changed since it was read. Review the current row, then save again.'
      await refresh()
    } else {
      draft.error = error?.message || 'Save failed'
    }
  } finally {
    draft.saving = false
  }
}

async function remove(row) {
  if (isReadOnly.value) return
  const ok = await confirmStore.ask({
    title: `Delete route "${row.path}"?`,
    message: 'Public invocation at this path stops immediately. The function and its deployments are unchanged.',
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  busyPath.value = row.path
  try {
    const fresh = await props.api.inspectRoute(row.path)
    await props.api.deleteRoute({ path: row.path, version: fresh.version, expected: fresh.expected })
    await refresh()
    confirmStore.notify({ title: 'Route deleted', message: row.path })
  } catch (error) {
    if (error?.forbidden) {
      denied.value = true
    } else if (error?.stale) {
      staleNotice.value = 'The route changed since it was read and was not deleted. The list below is current; review it and try again.'
      await refresh()
    } else {
      confirmStore.notify({ title: 'Delete failed', message: error?.message || 'Request failed', danger: true })
    }
  } finally {
    busyPath.value = ''
  }
}

onMounted(refresh)

// Keep-alive: a return visit re-reads rather than acting on tokens captured
// before the operator left the page. The flag keeps the first activation from
// double-fetching on top of onMounted.
let wasDeactivated = false
onActivated(() => {
  if (!wasDeactivated) return
  wasDeactivated = false
  refresh()
})
onDeactivated(() => { wasDeactivated = true })

defineExpose({ refresh })
</script>
