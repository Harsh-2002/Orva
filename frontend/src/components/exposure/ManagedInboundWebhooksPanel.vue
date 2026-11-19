<template>
  <div class="space-y-6">
    <div class="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-foreground-strong tracking-tight">
          Inbound webhooks
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 measure leading-body">
          Signed URLs that invoke
          <router-link
            :to="`/functions/${functionName}`"
            class="text-foreground-strong underline"
          >
            {{ functionName }}
          </router-link>
          from external services. Each change is checked against the trigger and
          owner authority it was read from.
        </p>
        <p class="mt-1.5 text-[11px] text-foreground-muted">
          <span class="uppercase tracking-wider text-[10px]">Owner</span>
          <code
            class="ml-1.5 font-mono break-all text-foreground"
            data-testid="inbound-owner"
          >{{ functionId }}</code>
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-xs text-foreground-muted">
          {{ rows.length }} {{ rows.length === 1 ? 'trigger' : 'triggers' }}
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
          data-testid="inbound-new"
          @click="openCreate"
        >
          <Plus class="w-3.5 h-3.5" />
          New trigger
        </Button>
      </div>
    </div>

    <p
      v-if="isReadOnly"
      data-testid="inbound-readonly"
      class="rounded-lg border border-border bg-surface p-3 text-sm text-foreground-muted"
      role="status"
    >
      Read-only. Creating, editing, pausing and deleting triggers needs current
      write access on this server.
    </p>

    <div
      v-if="staleNotice"
      data-testid="inbound-stale"
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

    <!-- The plaintext secret exists only in the create response. It is never
         carried by get/list/update, so this is the operator's one chance. -->
    <div
      v-if="created"
      data-testid="inbound-secret-once"
      class="rounded-lg border border-warning-ring bg-warning-tint p-4 space-y-3"
    >
      <div class="flex items-center justify-between gap-4">
        <div class="text-sm text-warning-fg font-medium">
          Trigger created. Copy the secret now. It will not be shown again.
        </div>
        <button
          type="button"
          class="inline-flex items-center shrink-0 rounded px-2 -mx-2 touch-expand-sm text-xs text-warning-fg hover:text-foreground-strong transition-colors"
          @click="created = null"
        >
          Dismiss
        </button>
      </div>
      <div class="text-xs space-y-2">
        <div>
          <span class="text-foreground-muted uppercase tracking-wider text-[10px]">URL</span>
          <code class="ml-2 font-mono text-foreground-strong break-all">{{ origin + created.trigger_url }}</code>
          <button
            type="button"
            class="inline-flex items-center rounded px-2 touch-expand-sm text-warning-fg hover:text-foreground-strong transition-colors"
            @click="copy(origin + created.trigger_url)"
          >
            Copy URL
          </button>
        </div>
        <div>
          <span class="text-foreground-muted uppercase tracking-wider text-[10px]">Secret</span>
          <code
            class="ml-2 font-mono text-foreground-strong break-all"
            data-testid="inbound-created-secret"
          >{{ created.secret }}</code>
          <button
            type="button"
            class="inline-flex items-center rounded px-2 touch-expand-sm text-warning-fg hover:text-foreground-strong transition-colors"
            @click="copy(created.secret)"
          >
            Copy secret
          </button>
        </div>
      </div>
    </div>

    <LoadError
      v-if="loadError"
      what="Inbound webhooks"
      :message="loadError"
      :on-retry="refresh"
    />

    <p
      v-if="truncated"
      class="text-xs text-foreground-muted"
      role="status"
    >
      Showing the first 256 triggers. Delete one to see the rest.
    </p>

    <div class="bg-background border border-border rounded-lg overflow-x-auto scrollable">
      <ul class="sm:hidden divide-y divide-border">
        <li
          v-for="row in rows"
          :key="row.id"
          class="px-4 py-3"
          data-testid="inbound-card"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2 flex-wrap">
                <span class="font-medium text-foreground-strong truncate">{{ row.name }}</span>
                <span
                  class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] border"
                  :class="row.active
                    ? 'bg-success-tint text-success-fg border-success-ring'
                    : 'bg-surface text-foreground-muted border-border'"
                >{{ row.active ? 'active' : 'paused' }}</span>
                <span
                  class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] border"
                  :class="publishedClass(row)"
                  data-testid="inbound-published"
                >{{ publishedLabel(row) }}</span>
              </div>
              <div class="mt-1 text-[11px] text-foreground-muted font-mono break-all">
                /webhook/{{ row.id }}
              </div>
              <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-foreground-muted">
                <span class="font-mono">{{ row.signature_format }}</span>
                <span
                  v-if="row.secret_preview"
                  class="font-mono"
                >{{ row.secret_preview }}</span>
                <span class="font-mono">v{{ row.version }}</span>
              </div>
            </div>
            <div class="flex items-center gap-1 shrink-0">
              <IconButton
                :icon="Edit"
                title="Edit trigger"
                :disabled="isReadOnly || busyId === row.id"
                @click="openEdit(row)"
              />
              <IconButton
                :icon="row.active ? Pause : Play"
                :title="row.active ? 'Pause this trigger' : 'Resume this trigger'"
                :disabled="isReadOnly || busyId === row.id"
                @click="toggleActive(row)"
              />
              <IconButton
                :icon="Trash2"
                variant="danger"
                title="Delete trigger"
                :disabled="isReadOnly || busyId === row.id"
                @click="remove(row)"
              />
            </div>
          </div>
        </li>
        <li
          v-if="loaded && !loadError && !rows.length"
          class="px-4 py-12 text-center text-sm text-foreground-muted"
        >
          No inbound triggers yet.
        </li>
      </ul>

      <table class="hidden sm:table w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th
              scope="col"
              class="px-6 py-3 font-medium"
            >
              Name
            </th>
            <th
              scope="col"
              class="px-6 py-3 font-medium hidden md:table-cell"
            >
              URL
            </th>
            <th
              scope="col"
              class="px-6 py-3 font-medium"
            >
              Format
            </th>
            <th
              scope="col"
              class="px-6 py-3 font-medium hidden md:table-cell"
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
              class="px-6 py-3 font-medium hidden lg:table-cell"
            >
              Active
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
            :key="row.id"
            class="hover:bg-surface/50 transition-colors"
            data-testid="inbound-row"
          >
            <td class="px-6 py-4 font-medium text-foreground-strong">
              <div class="flex flex-col">
                <span>{{ row.name }}</span>
                <span class="text-[11px] text-foreground-muted font-mono break-all">{{ row.id }}</span>
              </div>
            </td>
            <td class="px-6 py-4 font-mono text-xs text-foreground-muted hidden md:table-cell">
              <span class="break-all">/webhook/{{ row.id }}</span>
            </td>
            <td class="px-6 py-4">
              <span class="inline-flex items-center px-2 py-0.5 rounded text-xs border bg-surface text-foreground-muted border-border font-mono">
                {{ row.signature_format }}
              </span>
              <div
                v-if="row.secret_preview"
                class="text-[11px] text-foreground-muted font-mono mt-1"
              >
                {{ row.secret_preview }}
              </div>
            </td>
            <td
              class="px-6 py-4 text-xs text-foreground-muted font-mono hidden md:table-cell"
              data-testid="inbound-version"
            >
              {{ row.version }}
            </td>
            <td class="px-6 py-4">
              <span
                class="inline-flex items-center px-2 py-0.5 rounded text-xs border"
                :class="publishedClass(row)"
                data-testid="inbound-published"
              >{{ publishedLabel(row) }}</span>
            </td>
            <td class="px-6 py-4 hidden lg:table-cell">
              <span
                class="inline-flex items-center px-2 py-0.5 rounded text-xs border"
                :class="row.active
                  ? 'bg-success-tint text-success-fg border-success-ring'
                  : 'bg-surface text-foreground-muted border-border'"
              >{{ row.active ? 'active' : 'paused' }}</span>
            </td>
            <td class="px-6 py-4 text-right">
              <div class="inline-flex items-center gap-1">
                <IconButton
                  :icon="Edit"
                  title="Edit trigger"
                  :disabled="isReadOnly || busyId === row.id"
                  @click="openEdit(row)"
                />
                <IconButton
                  :icon="row.active ? Pause : Play"
                  :title="row.active ? 'Pause this trigger' : 'Resume this trigger'"
                  :disabled="isReadOnly || busyId === row.id"
                  @click="toggleActive(row)"
                />
                <IconButton
                  :icon="Trash2"
                  variant="danger"
                  title="Delete trigger"
                  :disabled="isReadOnly || busyId === row.id"
                  @click="remove(row)"
                />
              </div>
            </td>
          </tr>
          <tr v-if="loaded && !loadError && !rows.length">
            <td
              colspan="7"
              class="px-6 py-12 text-center text-foreground-muted text-sm"
            >
              No inbound triggers yet.
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <Modal
      :model-value="draft.open"
      :title="draft.mode === 'edit' ? 'Edit trigger' : 'New inbound trigger'"
      size="lg"
      @update:model-value="$event ? null : closeDraft()"
    >
      <div class="space-y-4">
        <div>
          <label
            for="inbound-name"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
          >Name</label>
          <input
            id="inbound-name"
            v-model="draft.name"
            data-testid="inbound-name-input"
            placeholder="github-deploys"
            class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
          >
        </div>
        <div>
          <span class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">Signature format</span>
          <FilterSelect
            v-model="draft.format"
            :options="formatOptions"
            label="Signature format"
            trigger-id="inbound-format"
            wide
          />
        </div>
        <div>
          <label
            for="inbound-header"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
          >Signature header</label>
          <input
            id="inbound-header"
            v-model="draft.header"
            placeholder="leave empty for the format's default"
            class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground font-mono focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
          >
        </div>
        <div class="flex items-center gap-2">
          <input
            id="inbound-active"
            v-model="draft.active"
            type="checkbox"
            class="w-4 h-4 rounded border-border bg-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background"
          >
          <label
            for="inbound-active"
            class="text-sm text-foreground"
          >Accept calls</label>
        </div>
        <p class="text-xs text-foreground-muted">
          The signing secret is generated server-side and returned once, on
          create. Rotating it means deleting this trigger and creating another.
        </p>
        <p
          v-if="draft.error"
          data-testid="inbound-error"
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
          data-testid="inbound-save"
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
import { computed, reactive, ref, watch, onActivated, onDeactivated } from 'vue'
import { Plus, RefreshCw, Edit, Trash2, Pause, Play } from '@lucide/vue'
import { managedExposure, SIGNATURE_FORMATS } from '@/api/managedExposure.js'
import Button from '@/components/common/Button.vue'
import FilterSelect from '@/components/common/FilterSelect.vue'
import IconButton from '@/components/common/IconButton.vue'
import LoadError from '@/components/common/LoadError.vue'
import Modal from '@/components/common/Modal.vue'
import { copyText } from '@/utils/clipboard'
import { useConfirmStore } from '@/stores/confirm'

defineOptions({ name: 'ManagedInboundWebhooksPanel' })

const props = defineProps({
  // Injected so tests can drive the exact wire shape without a server.
  api: { type: Object, default: () => managedExposure },
  functionId: { type: String, required: true },
  functionName: { type: String, default: '' },
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
const busyId = ref('')
const created = ref(null)
const origin = window.location.origin

const isReadOnly = computed(() => props.readOnly || denied.value)
const formatOptions = SIGNATURE_FORMATS.map(value => ({ value, label: value }))

// The create/update draft survives a conflict so the operator can re-read and
// resubmit deliberately instead of retyping the trigger.
const draft = reactive({
  open: false,
  mode: 'create',
  id: '',
  name: '',
  format: 'hmac_sha256_hex',
  header: '',
  active: true,
  saving: false,
  stale: false,
  error: '',
})

const canSave = computed(() => !draft.saving && draft.name.trim() !== '')
const publishedLabel = (row) => (row.published ? 'published' : 'not published')
const publishedClass = (row) => (row.published
  ? 'bg-success-tint text-success-fg border-success-ring'
  : 'bg-surface text-foreground-muted border-border')

async function refresh() {
  loading.value = true
  try {
    const result = await props.api.listInbound(props.functionId)
    rows.value = result.rows
    truncated.value = result.truncated
    loadError.value = ''
  } catch (error) {
    if (error?.forbidden) {
      denied.value = true
      loadError.value = 'Reading inbound webhooks needs read access on this server.'
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
  Object.assign(draft, {
    open: true, mode: 'create', id: '', name: '', format: 'hmac_sha256_hex',
    header: '', active: true, saving: false, stale: false, error: '',
  })
}

function openEdit(row) {
  if (isReadOnly.value) return
  Object.assign(draft, {
    open: true,
    mode: 'edit',
    id: row.id,
    name: row.name,
    format: row.signature_format,
    header: row.signature_header || '',
    active: row.active,
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

const draftFields = () => {
  const fields = {
    name: draft.name.trim(),
    signatureFormat: draft.format,
    active: draft.active,
  }
  const header = draft.header.trim()
  if (header) fields.signatureHeader = header
  return fields
}

async function save() {
  if (!canSave.value || isReadOnly.value) return
  const mode = draft.mode
  const id = draft.id
  const name = draft.name.trim()
  draft.saving = true
  draft.error = ''
  draft.stale = false
  try {
    if (mode === 'create') {
      // A fresh inspection supplies the create-time owner tokens; a new
      // trigger's version is zero and never inferred from another row.
      const fresh = await props.api.inspectInbound(props.functionId)
      const result = await props.api.createInbound(props.functionId, {
        ...draftFields(), version: fresh.version, expected: fresh.expected,
      })
      created.value = {
        id: result.inbound_webhook?.id || '',
        trigger_url: result.trigger_url || `/webhook/${result.inbound_webhook?.id || ''}`,
        secret: result.secret || '',
      }
    } else {
      const fresh = await props.api.inspectInbound(props.functionId, { id })
      await props.api.updateInbound(props.functionId, id, {
        ...draftFields(), version: fresh.version, expected: fresh.expected,
      })
    }
    closeDraft()
    await refresh()
    if (mode === 'edit') confirmStore.notify({ title: 'Trigger updated', message: name })
  } catch (error) {
    if (error?.forbidden) {
      denied.value = true
      draft.error = 'You do not have write access to inbound webhooks.'
    } else if (error?.stale) {
      draft.stale = true
      draft.error = 'The trigger or its owner authority changed since it was read. Review the current row, then save again.'
      await refresh()
    } else {
      draft.error = error?.message || 'Save failed'
    }
  } finally {
    draft.saving = false
  }
}

async function toggleActive(row) {
  if (isReadOnly.value || busyId.value) return
  busyId.value = row.id
  try {
    // The full CAS body, from a read taken now: the version is per trigger and
    // a listing row's value may already be behind another operator.
    const fresh = await props.api.inspectInbound(props.functionId, { id: row.id })
    await props.api.updateInbound(props.functionId, row.id, {
      active: !row.active, version: fresh.version, expected: fresh.expected,
    })
    await refresh()
  } catch (error) {
    if (error?.forbidden) {
      denied.value = true
    } else if (error?.stale) {
      staleNotice.value = 'The trigger changed since it was read and was not ' + (row.active ? 'paused' : 'resumed') + '. The list below is current.'
      await refresh()
    } else {
      confirmStore.notify({
        title: row.active ? 'Failed to pause trigger' : 'Failed to resume trigger',
        message: error?.message || 'Request failed',
        danger: true,
      })
    }
  } finally {
    busyId.value = ''
  }
}

async function remove(row) {
  if (isReadOnly.value) return
  const ok = await confirmStore.ask({
    title: `Delete trigger "${row.name}"?`,
    message: 'The URL and its signing secret stop working immediately. This cannot be undone; rotation means delete and recreate.',
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  busyId.value = row.id
  try {
    const fresh = await props.api.inspectInbound(props.functionId, { id: row.id })
    await props.api.deleteInbound(props.functionId, row.id, { version: fresh.version, expected: fresh.expected })
    await refresh()
    confirmStore.notify({ title: 'Trigger deleted', message: row.name })
  } catch (error) {
    if (error?.forbidden) {
      denied.value = true
    } else if (error?.stale) {
      staleNotice.value = 'The trigger changed since it was read and was not deleted. The list below is current; review it and try again.'
      await refresh()
    } else {
      confirmStore.notify({ title: 'Delete failed', message: error?.message || 'Request failed', danger: true })
    }
  } finally {
    busyId.value = ''
  }
}

async function copy(text) {
  if (await copyText(text)) {
    confirmStore.notify({ title: 'Copied', message: '' })
    return
  }
  confirmStore.notify({
    title: 'Copy failed',
    message: 'Could not copy to clipboard. Select the value manually:\n\n' + text,
  })
}

// The owner changed: every row, the one-time secret and an open drawer belong
// to the previous function.
watch(() => props.functionId, () => {
  rows.value = []
  created.value = null
  staleNotice.value = ''
  closeDraft()
  refresh()
}, { immediate: true })

// Layout.vue keep-alives the router-view: coming back should re-read rather
// than show whatever another operator has since replaced (their version tokens
// would be stale). The flag keeps the first activation from double-fetching.
let wasDeactivated = false
onActivated(() => {
  if (!wasDeactivated) return
  wasDeactivated = false
  refresh()
})
onDeactivated(() => { wasDeactivated = true })
</script>
