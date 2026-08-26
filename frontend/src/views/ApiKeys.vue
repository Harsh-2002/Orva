<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-foreground-strong tracking-tight">
          API Keys
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
          Tokens for REST and MCP clients. Secrets are shown once.
        </p>
      </div>
      <Button @click="openCreate">
        <KeyRound class="w-4 h-4" />
        New Key
      </Button>
    </div>

    <!-- One-time secret reveal after Create. Stays visible until dismissed.
         House notice shape: semantic tint + ring on the card, tint foreground
         on the heading, so it reads the same as every other warning block. -->
    <div
      v-if="createdKey"
      class="rounded-lg border border-warning-ring bg-warning-tint p-4 space-y-3"
    >
      <div class="flex items-start justify-between gap-3">
        <div>
          <h2 class="text-xs font-bold text-warning-fg uppercase tracking-wider">
            Copy this key now
          </h2>
          <div class="text-xs text-foreground-muted mt-0.5">
            Store it securely. This secret will not be shown again.
          </div>
        </div>
        <button
          class="inline-flex items-center justify-center shrink-0 rounded text-foreground-muted hover:text-foreground-strong transition-colors touch-expand-iconbtn"
          title="Dismiss"
          aria-label="Dismiss API key"
          @click="createdKey = ''"
        >
          <X class="w-4 h-4" />
        </button>
      </div>
      <div class="flex items-center gap-2">
        <code class="flex-1 font-mono text-sm text-foreground-strong break-all bg-surface px-3 py-2 rounded border border-border">{{ createdKey }}</code>
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
    </div>

    <!-- Inline create form. Shows when openCreate() is invoked. -->
    <div
      v-if="creating"
      class="bg-background border border-border rounded-lg p-5 space-y-4"
    >
      <div class="text-sm font-semibold text-foreground-strong">
        New API Key
      </div>
      <div class="grid grid-cols-1 md:grid-cols-2 gap-3">
        <div>
          <label
            for="api-key-name"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
          >Name</label>
          <input
            id="api-key-name"
            v-model="newKey.name"
            placeholder="e.g. ci-deployer"
            class="w-full bg-surface-hover border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:border-focus-ring"
          >
        </div>
        <div>
          <label
            for="api-key-expiry"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
          >Expires in</label>
          <select
            id="api-key-expiry"
            v-model="newKey.expiresInDays"
            class="w-full bg-surface-hover border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:border-focus-ring"
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
      <div>
        <span
          class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
        >Permissions</span>
        <!-- A grid, not flex-wrap. Wrapping laid the four options out by their
             own content width, and the hints run from "Call functions" to
             "Keys, channels, firewall, backup and restore" -- a 3x spread. So
             the wrap points were decided by hint length: two on the first row,
             then one, then one, with no column a reader could scan down. A
             declared column count makes the four align whatever the copy
             says. -->
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label
            v-for="perm in permissionOptions"
            :key="perm.value"
            class="flex items-start gap-2.5 text-sm text-foreground cursor-pointer"
          >
            <input
              v-model="newKey.permissions"
              type="checkbox"
              :value="perm.value"
              class="mt-0.5 accent-primary"
            >
            <span>
              <span class="font-medium">{{ perm.label }}</span>
              <span class="block text-xs text-foreground-muted">{{ perm.hint }}</span>
            </span>
          </label>
        </div>
        <p
          v-if="!newKey.permissions.length"
          class="text-xs text-danger-fg mt-1.5"
        >
          Select at least one permission.
        </p>
      </div>
      <!-- Cancel first, then the primary: the order Button.vue documents, and
           the one every dialog in the app already uses. This row had it
           inverted. Stacking below sm also puts the primary nearest the thumb
           and stops the pair sitting as a short left-aligned huddle with ~90px
           of dead space beside it. -->
      <div class="flex flex-col gap-2 pt-1 sm:flex-row">
        <Button
          variant="ghost"
          @click="cancelCreate"
        >
          Cancel
        </Button>
        <Button
          :disabled="!newKey.name.trim() || !newKey.permissions.length || submitting"
          :loading="submitting"
          @click="submitCreate"
        >
          Generate Key
        </Button>
      </div>
    </div>

    <!-- Keys list. -->
    <LoadError
      v-if="loadError"
      what="API keys"
      :message="loadError"
      :on-retry="loadKeys"
      class="mb-3"
    />

    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <!-- Mobile (<sm) stacked-row list — name + prefix on the primary
           line, last-used + expires as secondary metadata, delete on
           the right. The desktop table returns at sm+. -->
      <ul class="sm:hidden divide-y divide-border">
        <li
          v-for="key in keys"
          :key="key.id"
          class="px-4 py-3"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <!-- No flex-wrap. With it, whether the prefix sat beside the
                   name or dropped below it was decided by how long the
                   operator's name was, so two rows of the same list rendered
                   as two different shapes and the delete button lined up
                   against a different thing in each. The name truncates
                   instead; the prefix is already elided and holds its place. -->
              <div class="flex items-center gap-2">
                <span class="min-w-0 truncate font-medium text-foreground-strong">{{ key.name || 'Unnamed' }}</span>
                <code
                  v-if="key.prefix"
                  class="shrink-0 rounded bg-surface px-1.5 py-0.5 font-mono text-[11px] text-foreground-muted"
                >{{ key.prefix }}…</code>
              </div>
              <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-foreground-muted">
                <span v-if="key.last_used_at">used {{ formatRelative(key.last_used_at) }}</span>
                <span
                  v-else
                  class="text-warning-fg"
                >never used</span>
                <span v-if="!key.expires_at">no expiry</span>
                <span
                  v-else-if="isExpired(key.expires_at)"
                  class="text-danger-fg"
                >expired {{ formatRelative(key.expires_at) }}</span>
                <span v-else>expires {{ formatRelative(key.expires_at) }}</span>
              </div>
            </div>
            <IconButton
              :icon="Trash2"
              variant="danger"
              title="Delete key"
              @click="removeKey(key)"
            />
          </div>
        </li>
        <li
          v-if="loaded && !loadError && keys.length === 0"
          class="px-6 py-8 text-center text-sm text-foreground-muted"
        >
          No API keys yet.
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
              class="px-6 py-3 font-medium hidden sm:table-cell"
            >
              Prefix
            </th>
            <th
              scope="col"
              class="px-6 py-3 font-medium hidden xl:table-cell"
            >
              Created
            </th>
            <th
              scope="col"
              class="px-6 py-3 font-medium hidden md:table-cell"
            >
              Last Used
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
            v-for="key in keys"
            :key="key.id"
            class="hover:bg-surface/50 transition-colors"
          >
            <td class="px-6 py-4 text-foreground-strong font-medium">
              {{ key.name || 'Unnamed' }}
            </td>
            <td class="px-6 py-4 text-foreground-muted font-mono text-xs hidden sm:table-cell">
              {{ key.prefix ? key.prefix + '…' : EMPTY }}
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
                class="text-warning-fg text-xs"
              >Never used</span>
            </td>
            <td class="px-6 py-4 hidden lg:table-cell">
              <span
                v-if="!key.expires_at"
                class="text-foreground-muted"
              >Never</span>
              <span
                v-else-if="isExpired(key.expires_at)"
                class="text-danger-fg text-xs"
              >Expired {{ formatRelative(key.expires_at) }}</span>
              <span
                v-else
                class="text-foreground-muted"
              >{{ formatRelative(key.expires_at) }}</span>
            </td>
            <td class="px-6 py-4 text-right">
              <IconButton
                :icon="Trash2"
                variant="danger"
                title="Delete key"
                @click="removeKey(key)"
              />
            </td>
          </tr>
          <tr v-if="loaded && !loadError && keys.length === 0">
            <td
              colspan="6"
              class="px-6 py-8 text-center text-foreground-muted"
            >
              No API keys yet.
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { EMPTY } from '@/utils/format'
import { ref, onMounted } from 'vue'
import { KeyRound, Copy, Check, X, Trash2 } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import IconButton from '@/components/common/IconButton.vue'
import LoadError from '@/components/common/LoadError.vue'
import { listApiKeys, createApiKey, deleteApiKey } from '@/api/endpoints'
import { copyText } from '@/utils/clipboard'
import { formatRelative, isExpired } from '@/utils/time'
import { useConfirmStore } from '@/stores/confirm'

const confirmStore = useConfirmStore()

const keys = ref([])
const loadError = ref('')
const loaded = ref(false)
const createdKey = ref('')
const createdCopied = ref(false)
const creating = ref(false)
const submitting = ref(false)
// The form sent only {name, expires_in_days}, so the server applied its
// default of all four permissions and EVERY dashboard-minted key was full
// admin -- strictly more permissive than the CLI, which defaults to invoke
// only. Least privilege is not reachable if the UI cannot express it.
const permissionOptions = [
  { value: 'invoke', label: 'Invoke', hint: 'Call functions' },
  { value: 'read', label: 'Read', hint: 'List and inspect resources' },
  { value: 'write', label: 'Write', hint: 'Create, update and delete resources' },
  { value: 'admin', label: 'Admin', hint: 'Keys, channels, firewall, backup and restore' },
]
const defaultPermissions = () => ['invoke', 'read']
const newKey = ref({ name: '', expiresInDays: 0, permissions: defaultPermissions() })

const loadKeys = async () => {
  try {
    const res = await listApiKeys()
    keys.value = res.data.keys || []
    loadError.value = ''
  } catch (e) {
    // Without this the list stayed empty and the view asserted "No API keys
    // yet" -- reassuring, and wrong, on the one screen where believing you have
    // no credentials outstanding actually matters.
    loadError.value = e?.response?.data?.error?.message || e?.message || 'Request failed'
  } finally {
    loaded.value = true
  }
}

const openCreate = () => {
  newKey.value = { name: '', expiresInDays: 0, permissions: defaultPermissions() }
  creating.value = true
}

const cancelCreate = () => {
  creating.value = false
  newKey.value = { name: '', expiresInDays: 0, permissions: defaultPermissions() }
}

const submitCreate = async () => {
  submitting.value = true
  try {
    const body = { name: newKey.value.name.trim() }
    if (newKey.value.expiresInDays > 0) body.expires_in_days = newKey.value.expiresInDays
    // Always send permissions explicitly. Omitting the field makes the
    // server fall back to all four.
    body.permissions = [...newKey.value.permissions]
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

onMounted(loadKeys)
</script>
