<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between gap-4">
      <h1 class="text-xl font-semibold text-white tracking-tight">
        API Keys
      </h1>
      <Button @click="openCreate">
        <KeyRound class="w-4 h-4" />
        New Key
      </Button>
    </div>

    <!-- One-time secret reveal after Create. Stays visible until dismissed. -->
    <div
      v-if="createdKey"
      class="bg-background border border-amber-700/40 rounded-lg p-4 space-y-2"
    >
      <div class="flex items-start justify-between gap-3">
        <div>
          <div class="text-xs font-bold text-amber-300 uppercase tracking-wider">
            Copy this key now
          </div>
          <div class="text-xs text-foreground-muted mt-0.5">
            It will not be shown again. Anyone with this key can invoke your functions.
          </div>
        </div>
        <button
          class="text-foreground-muted hover:text-white"
          title="Dismiss"
          @click="createdKey = ''"
        >
          <X class="w-4 h-4" />
        </button>
      </div>
      <div class="flex items-center gap-2">
        <code class="flex-1 font-mono text-sm text-white break-all bg-surface px-3 py-2 rounded border border-border">{{ createdKey }}</code>
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
    </div>

    <!-- Inline create form. Shows when openCreate() is invoked. -->
    <div
      v-if="creating"
      class="bg-background border border-border rounded-lg p-5 space-y-4"
    >
      <div class="text-sm font-semibold text-white">
        New API Key
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        <div>
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">Name</label>
          <input
            v-model="newKey.name"
            placeholder="e.g. ci-deployer"
            class="w-full bg-surface-hover border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:border-white"
          >
        </div>
        <div>
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">Expires in</label>
          <select
            v-model="newKey.expiresInDays"
            class="w-full bg-surface-hover border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:border-white"
          >
            <option :value="0">
              Never
            </option>
            <option :value="1">
              1 day
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
            <option :value="365">
              1 year
            </option>
          </select>
        </div>
      </div>
      <div class="flex gap-2 pt-1">
        <Button
          :disabled="!newKey.name.trim() || submitting"
          :loading="submitting"
          @click="submitCreate"
        >
          Generate Key
        </Button>
        <Button
          variant="secondary"
          @click="cancelCreate"
        >
          Cancel
        </Button>
      </div>
    </div>

    <!-- Keys list. -->
    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <table class="w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th class="px-6 py-3 font-medium">
              Name
            </th>
            <th class="px-6 py-3 font-medium hidden sm:table-cell">
              Prefix
            </th>
            <th class="px-6 py-3 font-medium hidden xl:table-cell">
              Created
            </th>
            <th class="px-6 py-3 font-medium hidden md:table-cell">
              Last Used
            </th>
            <th class="px-6 py-3 font-medium hidden lg:table-cell">
              Expires
            </th>
            <th class="px-6 py-3 font-medium text-right">
              Actions
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr
            v-for="key in keys"
            :key="key.id"
            class="hover:bg-surface/50 transition-colors"
          >
            <td class="px-6 py-4 text-white font-medium">
              {{ key.name || 'Unnamed' }}
            </td>
            <td class="px-6 py-4 text-foreground-muted font-mono text-xs hidden sm:table-cell">
              {{ key.prefix ? key.prefix + '…' : '—' }}
            </td>
            <td class="px-6 py-4 text-foreground-muted hidden xl:table-cell">
              {{ formatDate(key.created_at) }}
            </td>
            <td class="px-6 py-4 hidden md:table-cell">
              <span
                v-if="key.last_used_at"
                class="text-foreground-muted"
              >{{ formatRelative(key.last_used_at) }}</span>
              <span
                v-else
                class="text-amber-400/70 text-xs"
              >Never used</span>
            </td>
            <td class="px-6 py-4 hidden lg:table-cell">
              <span
                v-if="!key.expires_at"
                class="text-foreground-muted"
              >Never</span>
              <span
                v-else-if="isExpired(key.expires_at)"
                class="text-red-400 text-xs"
              >Expired {{ formatRelative(key.expires_at) }}</span>
              <span
                v-else
                class="text-foreground-muted"
              >{{ formatRelative(key.expires_at) }}</span>
            </td>
            <td class="px-6 py-4 text-right">
              <button
                class="text-red-400 hover:text-red-300 text-xs font-medium"
                @click="removeKey(key)"
              >
                Delete
              </button>
            </td>
          </tr>
          <tr v-if="keys.length === 0">
            <td
              colspan="6"
              class="px-6 py-8 text-center text-foreground-muted"
            >
              No API keys yet. Click <span class="text-white">New Key</span> to generate one.
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { KeyRound, Copy, Check, X } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import { listApiKeys, createApiKey, deleteApiKey } from '@/api/endpoints'
import { copyText } from '@/utils/clipboard'
import { useConfirmStore } from '@/stores/confirm'

const confirmStore = useConfirmStore()

const keys = ref([])
const createdKey = ref('')
const createdCopied = ref(false)
const creating = ref(false)
const submitting = ref(false)
const newKey = ref({ name: '', expiresInDays: 0 })

const loadKeys = async () => {
  const res = await listApiKeys()
  keys.value = res.data.keys || []
}

const openCreate = () => {
  newKey.value = { name: '', expiresInDays: 0 }
  creating.value = true
}

const cancelCreate = () => {
  creating.value = false
  newKey.value = { name: '', expiresInDays: 0 }
}

const submitCreate = async () => {
  submitting.value = true
  try {
    const body = { name: newKey.value.name.trim() }
    if (newKey.value.expiresInDays > 0) body.expires_in_days = newKey.value.expiresInDays
    const res = await createApiKey(body)
    createdKey.value = res.data.key
    createdCopied.value = false
    creating.value = false
    await loadKeys()
  } catch (e) {
    console.error(e)
    confirmStore.notify({ title: 'Failed to create key', message: e?.response?.data?.error?.message || 'Unknown error', danger: true })
  } finally {
    submitting.value = false
  }
}

const copyCreated = async () => {
  const ok = await copyText(createdKey.value)
  if (ok) {
    createdCopied.value = true
    setTimeout(() => { createdCopied.value = false }, 1500)
  } else {
    confirmStore.notify({ title: 'Copy failed', message: 'Could not copy to clipboard. Select the key manually:\n\n' + createdKey.value })
  }
}

const removeKey = async (key) => {
  const ok = await confirmStore.ask({
    title: 'Delete API key?',
    message: `"${key.name || key.id}" will stop working immediately. This cannot be undone.`,
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  try {
    await deleteApiKey(key.id)
    await loadKeys()
  } catch (e) {
    console.error(e)
    confirmStore.notify({ title: 'Failed to delete key', message: e?.response?.data?.error?.message || 'Unknown error', danger: true })
  }
}

const formatDate = (date) => new Date(date).toLocaleString()

const formatRelative = (date) => {
  const ms = new Date(date).getTime() - Date.now()
  const abs = Math.abs(ms)
  const past = ms < 0
  const mins = Math.round(abs / 60000)
  if (mins < 1) return past ? 'just now' : 'in <1m'
  if (mins < 60) return past ? `${mins}m ago` : `in ${mins}m`
  const hrs = Math.round(mins / 60)
  if (hrs < 24) return past ? `${hrs}h ago` : `in ${hrs}h`
  const days = Math.round(hrs / 24)
  if (days < 90) return past ? `${days}d ago` : `in ${days}d`
  const months = Math.round(days / 30)
  return past ? `${months}mo ago` : `in ${months}mo`
}

const isExpired = (date) => new Date(date).getTime() < Date.now()

onMounted(loadKeys)
</script>
