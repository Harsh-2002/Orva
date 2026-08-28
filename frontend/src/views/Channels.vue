<template>
  <div class="space-y-6">
    <!-- Page header — matches ApiKeys.vue / FunctionsList.vue:
         left-aligned title, right-aligned primary action. Subtitle
         carries the full description including the trust boundary,
         so the page reads as one coherent intro instead of a header
         followed by a separate alert banner. -->
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-foreground-strong tracking-tight">
          Channels
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
          Expose selected functions as scoped MCP tools.
        </p>
      </div>
      <Button @click="openCreate">
        <Plug class="w-4 h-4" />
        New channel
      </Button>
    </div>

    <!-- One-time token reveal after Create / Rotate. Same amber-card
         pattern as ApiKeys.vue, plus an extra URL/header hint row
         since channels are usually configured in another tool. -->
    <div
      v-if="createdToken"
      class="bg-background border border-warning-ring rounded-lg p-4 space-y-3"
    >
      <div class="flex items-start justify-between gap-3">
        <div>
          <h2 class="text-xs font-bold text-warning-fg uppercase tracking-wider">
            Copy this token now
          </h2>
          <div class="text-xs text-foreground-muted mt-0.5">
            Store it securely, then add it to your MCP client.
          </div>
        </div>
        <button
          class="touch-expand-iconbtn inline-flex items-center justify-center rounded text-foreground-muted hover:text-foreground-strong transition-colors"
          title="Dismiss"
          aria-label="Dismiss channel token"
          @click="createdToken = ''"
        >
          <X class="w-4 h-4" />
        </button>
      </div>
      <div class="flex items-center gap-2">
        <code class="flex-1 font-mono text-sm text-foreground-strong break-all bg-surface px-3 py-2 rounded border border-border">{{ createdToken }}</code>
        <button
          class="shrink-0 px-3 py-2 rounded-md border border-border bg-surface-hover hover:bg-surface text-foreground-muted hover:text-foreground-strong transition-colors flex items-center gap-1.5 text-xs touch-expand-sm"
          @click="copyCreated"
        >
          <Check
            v-if="createdCopied"
            class="w-3.5 h-3.5 text-success"
          />
          <Copy
            v-else
            class="w-3.5 h-3.5"
          />
          {{ createdCopied ? 'Copied' : 'Copy' }}
        </button>
      </div>
      <div class="text-xs text-foreground-muted flex flex-wrap items-center gap-x-3 gap-y-1">
        <span>URL <code class="text-foreground bg-surface px-1.5 py-0.5 rounded">{{ mcpURL }}</code></span>
        <span>Header <code class="text-foreground bg-surface px-1.5 py-0.5 rounded">Authorization: Bearer &lt;token&gt;</code></span>
      </div>
    </div>

    <!-- Inline create form. Card shape, focus-ring style, label
         typography all mirror ApiKeys.vue's create form. -->
    <div
      v-if="creating"
      class="bg-background border border-border rounded-lg p-5 space-y-4"
    >
      <div class="text-sm font-semibold text-foreground-strong">
        New channel
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        <div>
          <label
            for="channel-name"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
          >Name</label>
          <input
            id="channel-name"
            v-model="newChannel.name"
            placeholder="e.g. support-bot"
            class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
          >
        </div>
        <div>
          <label
            for="channel-expiry"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
          >Expires in</label>
          <FilterSelect
            v-model="newChannel.expiresInDays"
            :options="expiryOptions"
            label="Expiry"
            trigger-id="channel-expiry"
            wide
          />
        </div>
      </div>
      <div>
        <label
          for="channel-description"
          class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
        >Description (optional)</label>
        <input
          id="channel-description"
          v-model="newChannel.description"
          placeholder="What this channel is for"
          class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
        >
      </div>
      <!-- A <label> cannot name a button, and this one pointed at nothing: it
           captions the picker below it. Named group instead, matching the shape
           used for the Webhooks event chips. -->
      <div
        role="group"
        aria-labelledby="channel-functions-label"
      >
        <div class="flex items-center justify-between mb-1.5">
          <span
            id="channel-functions-label"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide"
          >Functions</span>
          <span
            v-if="newChannel.functionIds.length > 0"
            class="text-[11px] text-foreground-muted"
          >{{ newChannel.functionIds.length }} selected</span>
        </div>
        <Button
          variant="secondary"
          @click="pickerOpen = true"
        >
          <Boxes class="w-4 h-4" />
          {{ newChannel.functionIds.length === 0 ? 'Pick functions' : 'Edit selection' }}
        </Button>
      </div>
      <div
        v-if="createError"
        class="rounded-md border border-danger-ring bg-danger-tint p-3 text-xs text-danger-fg flex items-start gap-2"
      >
        <AlertCircle class="w-4 h-4 text-danger-fg shrink-0 mt-0.5" />
        <span>{{ createError }}</span>
      </div>
      <div class="flex gap-2 pt-1">
        <Button
          variant="ghost"
          @click="cancelCreate"
        >
          Cancel
        </Button>
        <Button
          :disabled="!canSubmit || submitting"
          :loading="submitting"
          @click="submitCreate"
        >
          Generate token
        </Button>
      </div>
    </div>

    <!-- Channels list — mirrors ApiKeys.vue table chrome exactly:
         px-6 py-4 cells, `<th>` labels on own line, hover row tint,
         IconButton actions, and the same semantic warning/danger tokens
         for the "Never used" / "Expired" hints, so an identical state
         reads as an identical colour across the two views. -->
    <div class="bg-background border border-border rounded-lg overflow-x-auto scrollable">
      <!-- Mobile (<sm) stacked-row list. -->
      <ul class="sm:hidden divide-y divide-border">
        <li
          v-for="c in channels"
          :key="c.id"
          class="px-4 py-3"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2 flex-wrap">
                <span class="font-medium text-foreground-strong truncate">{{ c.name }}</span>
                <span class="inline-flex items-center gap-1 text-[11px] text-foreground-muted">
                  <Boxes class="w-3 h-3" /> {{ c.function_count }}
                </span>
              </div>
              <div
                v-if="c.description"
                class="mt-1 text-xs text-foreground-muted line-clamp-2"
              >
                {{ c.description }}
              </div>
              <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-foreground-muted">
                <code class="font-mono">{{ c.prefix }}…</code>
                <span v-if="c.last_used_at">used {{ formatRelative(c.last_used_at) }}</span>
                <span
                  v-else
                  class="text-warning-fg"
                >never used</span>
                <span
                  v-if="c.expires_at && isExpired(c.expires_at)"
                  class="text-danger-fg"
                >expired</span>
                <span v-else-if="c.expires_at">expires {{ formatRelative(c.expires_at) }}</span>
              </div>
            </div>
            <div class="flex items-center gap-1 shrink-0">
              <IconButton
                :icon="Pencil"
                title="Edit the functions in this channel"
                :disabled="savingFunctionsFor === c.id"
                @click="openEditFunctions(c)"
              />
              <IconButton
                :icon="RotateCcw"
                title="Rotate token"
                @click="rotate(c)"
              />
              <IconButton
                :icon="Trash2"
                variant="danger"
                title="Delete channel"
                @click="remove(c)"
              />
            </div>
          </div>
        </li>
        <li
          v-if="channels.length === 0"
          class="px-6 py-8 text-center text-sm text-foreground-muted"
        >
          {{ loadError ? `Could not load channels: ${loadError}` : 'No channels yet.' }}
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
              class="px-6 py-3 font-medium"
            >
              Functions
            </th>
            <th
              scope="col"
              class="px-6 py-3 font-medium hidden sm:table-cell"
            >
              Prefix
            </th>
            <th
              scope="col"
              class="px-6 py-3 font-medium hidden md:table-cell"
            >
              Last used
            </th>
            <th
              scope="col"
              class="px-6 py-3 font-medium hidden lg:table-cell"
            >
              Expires
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
            v-for="c in channels"
            :key="c.id"
            class="hover:bg-surface/50 transition-colors"
          >
            <td class="px-6 py-4">
              <div class="font-medium text-foreground-strong">
                {{ c.name }}
              </div>
              <div
                v-if="c.description"
                class="text-xs text-foreground-muted mt-0.5 line-clamp-1 max-w-md"
              >
                {{ c.description }}
              </div>
            </td>
            <td class="px-6 py-4">
              <span class="inline-flex items-center gap-1.5 text-foreground-muted">
                <Boxes class="w-3.5 h-3.5" />
                <span class="tabular-nums">{{ c.function_count }}</span>
              </span>
            </td>
            <td class="px-6 py-4 hidden sm:table-cell">
              <code class="text-foreground-muted font-mono text-xs">{{ c.prefix }}…</code>
            </td>
            <td class="px-6 py-4 hidden md:table-cell">
              <span
                v-if="c.last_used_at"
                class="text-foreground-muted"
              >{{ formatRelative(c.last_used_at) }}</span>
              <span
                v-else
                class="text-warning-fg text-xs"
              >Never used</span>
            </td>
            <td class="px-6 py-4 hidden lg:table-cell">
              <span
                v-if="!c.expires_at"
                class="text-foreground-muted"
              >Never</span>
              <span
                v-else-if="isExpired(c.expires_at)"
                class="text-danger-fg text-xs"
              >Expired {{ formatRelative(c.expires_at) }}</span>
              <span
                v-else
                class="text-foreground-muted"
              >{{ formatRelative(c.expires_at) }}</span>
            </td>
            <td class="px-6 py-4 text-right">
              <div class="inline-flex justify-end gap-1">
                <IconButton
                  :icon="Pencil"
                  title="Edit the functions in this channel"
                  :disabled="savingFunctionsFor === c.id"
                  @click="openEditFunctions(c)"
                />
                <IconButton
                  :icon="RotateCcw"
                  title="Rotate token"
                  @click="rotate(c)"
                />
                <IconButton
                  :icon="Trash2"
                  variant="danger"
                  title="Delete channel"
                  @click="remove(c)"
                />
              </div>
            </td>
          </tr>
          <tr v-if="channels.length === 0">
            <td
              colspan="6"
              class="px-6 py-8 text-center text-foreground-muted"
            >
              <template v-if="loadError">
                Could not load channels: {{ loadError }}
              </template>
              <template v-else>
                No channels yet. Click <span class="text-foreground-strong">New channel</span> to bundle functions for an agent.
              </template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Function picker modal. -->
    <FunctionPickerModal
      v-if="pickerOpen"
      :selected="editingChannel ? editingFunctionIds : newChannel.functionIds"
      @close="closePicker"
      @apply="onPickerApply"
    />
  </div>
</template>

<script setup>
defineOptions({ name: 'ChannelsView' })

import { ref, computed, onMounted } from 'vue'
import { Plug, Boxes, Copy, Check, X, Trash2, RotateCcw, AlertCircle, Pencil } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import FilterSelect from '@/components/common/FilterSelect.vue'
import IconButton from '@/components/common/IconButton.vue'
import FunctionPickerModal from '@/components/channels/FunctionPickerModal.vue'
import {
  listChannels,
  createChannel,
  rotateChannel,
  deleteChannel,
  setChannelFunctions,
  getChannel,
} from '@/api/endpoints'
import { copyText } from '@/utils/clipboard'
import { formatRelative, isExpired } from '@/utils/time'
import { useConfirmStore } from '@/stores/confirm'

const confirmStore = useConfirmStore()

const channels = ref([])
const createdToken = ref('')
const createdCopied = ref(false)
const creating = ref(false)
const submitting = ref(false)
const createError = ref('')
const pickerOpen = ref(false)

const expiryOptions = [
  { value: 0, label: 'Never' },
  { value: 7, label: '7 days' },
  { value: 30, label: '30 days' },
  { value: 90, label: '90 days' },
]
const newChannel = ref({
  name: '',
  description: '',
  expiresInDays: 0,
  functionIds: [],
})

// MCP URL: same scheme/host as the dashboard. Operators paste this
// alongside the token into their agent's MCP config.
const mcpURL = computed(() => `${window.location.origin}/mcp`)

const canSubmit = computed(
  () => newChannel.value.name.trim() && newChannel.value.functionIds.length > 0,
)

// A failed fetch used to render "No channels yet", which reads as "you have
// none" rather than "we could not ask".
const loadError = ref('')

const load = async () => {
  try {
    const res = await listChannels()
    channels.value = res.data.channels || []
    loadError.value = ''
  } catch (e) {
    loadError.value = e?.response?.data?.error?.message || e?.message || 'Request failed'
  }
}

const openCreate = () => {
  newChannel.value = { name: '', description: '', expiresInDays: 0, functionIds: [] }
  createError.value = ''
  creating.value = true
}
const cancelCreate = () => {
  creating.value = false
}

// Editing a channel's function set. setChannelFunctions was exported from
// the API module and imported by nothing, so the only channel operations the
// dashboard offered were Rotate and Delete -- changing which functions an
// agent could reach meant deleting the channel and re-issuing its token to
// whoever was using it.
const editingChannel = ref(null)
const editingFunctionIds = ref([])
const savingFunctionsFor = ref('')

// The LIST response carries no function rows -- Channel.FunctionIDs is
// documented as zero-length on List* -- so the detail has to be fetched
// before the picker can show what is currently selected.
const openEditFunctions = async (c) => {
  savingFunctionsFor.value = c.id
  try {
    const res = await getChannel(c.id)
    editingChannel.value = { ...c, functions: res.data?.functions || [] }
    editingFunctionIds.value = res.data?.function_ids || []
    pickerOpen.value = true
  } catch (e) {
    confirmStore.notify({
      title: 'Failed to load channel',
      message: e?.response?.data?.error?.message || 'Unknown error',
      danger: true,
    })
  } finally {
    savingFunctionsFor.value = ''
  }
}

const closePicker = () => {
  pickerOpen.value = false
  editingChannel.value = null
}

const onPickerApply = async (ids) => {
  if (!editingChannel.value) {
    newChannel.value.functionIds = ids
    pickerOpen.value = false
    return
  }
  const target = editingChannel.value
  savingFunctionsFor.value = target.id
  pickerOpen.value = false
  editingChannel.value = null
  try {
    // Carry the existing per-function tool descriptions through, or the
    // replace drops them -- the same way the CLI used to.
    const descriptions = {}
    for (const f of target.functions || []) {
      if (f.description && ids.includes(f.function_id)) {
        descriptions[f.function_id] = f.description
      }
    }
    await setChannelFunctions(target.id, {
      function_ids: ids,
      ...(Object.keys(descriptions).length ? { descriptions } : {}),
    })
    await load()
  } catch (e) {
    confirmStore.notify({
      title: 'Failed to update channel functions',
      message: e?.response?.data?.error?.message || 'Unknown error',
      danger: true,
    })
  } finally {
    savingFunctionsFor.value = ''
  }
}

const submitCreate = async () => {
  submitting.value = true
  createError.value = ''
  try {
    const body = {
      name: newChannel.value.name.trim(),
      description: newChannel.value.description.trim(),
      function_ids: newChannel.value.functionIds,
    }
    if (newChannel.value.expiresInDays > 0) {
      body.expires_in_days = newChannel.value.expiresInDays
    }
    const res = await createChannel(body)
    createdToken.value = res.data.token
    creating.value = false
    await load()
  } catch (err) {
    createError.value = err?.response?.data?.error?.message || 'Failed to create channel.'
  } finally {
    submitting.value = false
  }
}

const copyCreated = async () => {
  if (!createdToken.value) return
  if (await copyText(createdToken.value)) {
    createdCopied.value = true
    setTimeout(() => { createdCopied.value = false }, 1500)
  }
}

const rotate = async (c) => {
  const ok = await confirmStore.ask({
    title: `Rotate ${c.name}?`,
    message:
      'A new token will be issued. The previous token stops working ' +
      'immediately. Agents using it will need the new value.',
    confirmLabel: 'Rotate',
    danger: true,
  })
  if (!ok) return
  const res = await rotateChannel(c.id)
  createdToken.value = res.data.token
  await load()
}

const remove = async (c) => {
  const ok = await confirmStore.ask({
    title: `Delete ${c.name}?`,
    message:
      `${c.name} will lose MCP access immediately. Functions inside ` +
      'are not affected. Re-create the channel if you need it again.',
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  await deleteChannel(c.id)
  await load()
}

onMounted(load)
</script>
