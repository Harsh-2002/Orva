<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-white tracking-tight">
          Channels
        </h1>
        <p class="text-xs text-foreground-muted mt-1 max-w-prose">
          Bundle deployed functions and expose them as MCP tools to an
          agentic workflow. Each channel has its own bearer token;
          presenting that token at <code class="text-[11px]">/mcp</code>
          reveals only the bundled functions and nothing else from your Orva.
        </p>
      </div>
      <Button @click="openCreate">
        <Plug class="w-4 h-4" />
        New Channel
      </Button>
    </div>

    <!-- Risk banner. The channel boundary covers the MCP surface,
         not what bundled functions do at runtime. Tell operators so
         they don't get surprised. -->
    <div class="rounded-md border border-amber-700/30 bg-amber-950/15 px-4 py-3 text-xs text-amber-200/90 leading-relaxed">
      <div class="font-semibold text-amber-200 mb-0.5">Heads up</div>
      Channel tokens grant invoke-only MCP access to the bundled
      functions. Bundled functions remain as powerful as you've
      configured them — including any Orva REST SDK calls they make
      from inside their sandbox. The channel boundary protects the
      MCP surface, not the runtime blast radius.
    </div>

    <!-- One-time token reveal after Create / Rotate. -->
    <div
      v-if="createdToken"
      class="bg-background border border-amber-700/40 rounded-lg p-4 space-y-3"
    >
      <div class="flex items-start justify-between gap-3">
        <div>
          <div class="text-xs font-bold text-amber-300 uppercase tracking-wider">
            Copy this token now
          </div>
          <div class="text-xs text-foreground-muted mt-0.5">
            It will not be shown again. Configure it in your agent's MCP client.
          </div>
        </div>
        <button
          class="text-foreground-muted hover:text-white"
          title="Dismiss"
          @click="createdToken = ''"
        >
          <X class="w-4 h-4" />
        </button>
      </div>
      <div class="flex items-center gap-2">
        <code class="flex-1 font-mono text-sm text-white break-all bg-surface px-3 py-2 rounded border border-border">{{ createdToken }}</code>
        <button
          class="px-3 py-2 rounded-md border border-border bg-surface-hover hover:bg-surface text-foreground-muted hover:text-white transition-colors flex items-center gap-1.5 text-xs"
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
      <div class="text-[11px] text-foreground-muted">
        URL: <code class="text-foreground">{{ mcpURL }}</code>
        &nbsp;·&nbsp; Header: <code class="text-foreground">Authorization: Bearer &lt;token&gt;</code>
      </div>
    </div>

    <!-- Inline create form. -->
    <div
      v-if="creating"
      class="bg-background border border-border rounded-lg p-5 space-y-4"
    >
      <div class="text-sm font-semibold text-white">
        New channel
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        <div>
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">Name</label>
          <input
            v-model="newChannel.name"
            placeholder="e.g. support-bot"
            class="w-full bg-surface-hover border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:border-white"
          >
        </div>
        <div>
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">Expires in</label>
          <select
            v-model="newChannel.expiresInDays"
            class="w-full bg-surface-hover border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:border-white"
          >
            <option :value="0">
              Never
            </option>
            <option :value="7">
              7 days
            </option>
            <option :value="30">
              30 days
            </option>
            <option :value="90">
              90 days
            </option>
          </select>
        </div>
      </div>
      <div>
        <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">Description (optional)</label>
        <input
          v-model="newChannel.description"
          placeholder="What this channel is for"
          class="w-full bg-surface-hover border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:border-white"
        >
      </div>
      <div>
        <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">
          Functions <span class="text-white">({{ newChannel.functionIds.length }} selected)</span>
        </label>
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
        class="rounded-md border border-red-700/40 bg-red-950/30 p-3 text-xs text-red-200"
      >
        {{ createError }}
      </div>
      <div class="flex gap-2 pt-1">
        <Button
          :disabled="!canSubmit || submitting"
          :loading="submitting"
          @click="submitCreate"
        >
          Generate token
        </Button>
        <Button
          variant="secondary"
          @click="cancelCreate"
        >
          Cancel
        </Button>
      </div>
    </div>

    <!-- Channels list. -->
    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <table class="w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th class="px-6 py-3 font-medium">Name</th>
            <th class="px-6 py-3 font-medium">Functions</th>
            <th class="px-6 py-3 font-medium hidden md:table-cell">Prefix</th>
            <th class="px-6 py-3 font-medium hidden lg:table-cell">Last used</th>
            <th class="px-6 py-3 font-medium hidden xl:table-cell">Expires</th>
            <th class="px-6 py-3 font-medium text-right">Actions</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr
            v-for="c in channels"
            :key="c.id"
            class="hover:bg-surface/50 transition-colors"
          >
            <td class="px-6 py-3">
              <div class="font-medium text-white">{{ c.name }}</div>
              <div
                v-if="c.description"
                class="text-xs text-foreground-muted mt-0.5 line-clamp-1"
              >{{ c.description }}</div>
            </td>
            <td class="px-6 py-3 text-foreground-muted">{{ c.function_count }}</td>
            <td class="px-6 py-3 hidden md:table-cell">
              <code class="text-xs text-foreground-muted">{{ c.prefix }}…</code>
            </td>
            <td class="px-6 py-3 hidden lg:table-cell text-foreground-muted text-xs">
              {{ c.last_used_at ? formatRelative(c.last_used_at) : 'Never' }}
            </td>
            <td class="px-6 py-3 hidden xl:table-cell text-foreground-muted text-xs">
              {{ c.expires_at ? formatRelative(c.expires_at) : 'Never' }}
            </td>
            <td class="px-6 py-3">
              <div class="flex justify-end gap-1">
                <button
                  class="text-xs text-foreground-muted hover:text-white px-2 py-1 rounded transition-colors"
                  title="Rotate token"
                  @click="rotate(c)"
                >
                  <RotateCcw class="w-3.5 h-3.5" />
                </button>
                <button
                  class="text-xs text-foreground-muted hover:text-red-400 px-2 py-1 rounded transition-colors"
                  title="Delete channel"
                  @click="remove(c)"
                >
                  <Trash2 class="w-3.5 h-3.5" />
                </button>
              </div>
            </td>
          </tr>
          <tr v-if="channels.length === 0">
            <td
              colspan="6"
              class="px-6 py-8 text-center text-foreground-muted"
            >
              No channels yet. Click <span class="text-white">New Channel</span> to bundle functions for an agent.
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Function picker modal. -->
    <FunctionPickerModal
      v-if="pickerOpen"
      :selected="newChannel.functionIds"
      @close="pickerOpen = false"
      @apply="onPickerApply"
    />
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { Plug, Boxes, Copy, Check, X, Trash2, RotateCcw } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import FunctionPickerModal from '@/components/channels/FunctionPickerModal.vue'
import {
  listChannels,
  createChannel,
  rotateChannel,
  deleteChannel,
} from '@/api/endpoints'
import { copyText } from '@/utils/clipboard'
import { formatRelative } from '@/utils/time'
import { useConfirmStore } from '@/stores/confirm'

const confirmStore = useConfirmStore()

const channels = ref([])
const createdToken = ref('')
const createdCopied = ref(false)
const creating = ref(false)
const submitting = ref(false)
const createError = ref('')
const pickerOpen = ref(false)

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

const load = async () => {
  const res = await listChannels()
  channels.value = res.data.channels || []
}

const openCreate = () => {
  newChannel.value = { name: '', description: '', expiresInDays: 0, functionIds: [] }
  createError.value = ''
  creating.value = true
}
const cancelCreate = () => {
  creating.value = false
}

const onPickerApply = (ids) => {
  newChannel.value.functionIds = ids
  pickerOpen.value = false
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
      'immediately — agents using it will need the new value.',
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
