<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <h1 class="text-xl font-semibold text-white tracking-tight">
        Functions
      </h1>
      <Button @click="router.push('/deploy')">
        <Plus class="w-4 h-4" />
        New Function
      </Button>
    </div>

    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <table class="w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th class="px-6 py-3 font-medium">
              Name
            </th>
            <th class="px-6 py-3 font-medium hidden sm:table-cell">
              Runtime
            </th>
            <th class="px-6 py-3 font-medium hidden lg:table-cell">
              Resources
            </th>
            <th class="px-6 py-3 font-medium hidden md:table-cell">
              Function ID
            </th>
            <th class="px-6 py-3 font-medium hidden xl:table-cell">
              Last Update
            </th>
            <th class="px-6 py-3 font-medium text-right">
              Actions
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr
            v-for="fn in functions"
            :key="fn.name"
            class="hover:bg-surface/50 transition-colors"
          >
            <td class="px-6 py-4 font-medium text-white">
              {{ fn.name }}
            </td>
            <td class="px-6 py-4 text-foreground hidden sm:table-cell">
              <span class="inline-flex items-center px-2 py-1 rounded text-xs border border-border bg-background text-foreground-muted font-mono">
                {{ fn.runtime }}
              </span>
            </td>
            <td class="px-6 py-4 text-foreground-muted font-mono text-xs hidden lg:table-cell">
              {{ fn.cpus }} CPU / {{ fn.memory_mb }}MB
            </td>
            <td class="px-6 py-4 hidden md:table-cell">
              <div class="flex items-center gap-2">
                <code class="text-xs font-mono text-foreground-muted bg-surface px-2 py-1 rounded border border-border">{{ fn.id }}</code>
                <button
                  class="shrink-0 inline-flex items-center gap-1 px-2 py-1 rounded border border-border bg-surface-hover hover:bg-surface text-foreground-muted hover:text-white transition-colors text-[11px]"
                  :class="copiedId === fn.id ? 'text-success border-success/40' : ''"
                  :title="copiedId === fn.id ? 'Copied!' : 'Copy invoke URL to clipboard'"
                  @click="copyUrl(fn)"
                >
                  <Check
                    v-if="copiedId === fn.id"
                    class="w-3.5 h-3.5"
                  />
                  <Copy
                    v-else
                    class="w-3.5 h-3.5"
                  />
                  {{ copiedId === fn.id ? 'Copied' : 'Copy URL' }}
                </button>
              </div>
            </td>
            <td class="px-6 py-4 text-foreground-muted hidden xl:table-cell">
              {{ new Date(fn.updated_at).toLocaleDateString() }}
            </td>
            <td class="px-6 py-4 text-right">
              <div class="inline-flex items-center gap-1">
                <button
                  class="p-1.5 rounded text-foreground-muted hover:text-white hover:bg-surface transition-colors"
                  title="Edit function"
                  @click="router.push('/functions/' + fn.name)"
                >
                  <Pencil class="w-4 h-4" />
                </button>
                <button
                  class="p-1.5 rounded text-foreground-muted hover:text-red-400 hover:bg-surface transition-colors disabled:opacity-50"
                  :disabled="deletingId === fn.id"
                  title="Delete function"
                  @click="deleteFn(fn)"
                >
                  <Trash2 class="w-4 h-4" />
                </button>
              </div>
            </td>
          </tr>
          <tr v-if="functions.length === 0">
            <td
              colspan="6"
              class="px-6 py-8 text-center text-foreground-muted"
            >
              No functions found.
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { Plus, Pencil, Trash2, Copy, Check } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import apiClient from '@/api/client'
import { listFunctions } from '@/api/endpoints'
import { copyText } from '@/utils/clipboard'

const router = useRouter()
const functions = ref([])
const copiedId = ref('')
const deletingId = ref('')

// Built from window.location.origin so localhost / custom IPs / reverse-proxy
// public hostnames all just work — whatever URL the browser used to reach the
// UI is the right URL to invoke functions.
const invokeUrlFor = (fn) => `${window.location.origin}/api/v1/invoke/${fn.id}/`

const copyUrl = async (fn) => {
  const ok = await copyText(invokeUrlFor(fn))
  if (ok) {
    copiedId.value = fn.id
    setTimeout(() => { if (copiedId.value === fn.id) copiedId.value = '' }, 1500)
  } else {
    alert('Could not copy to clipboard. URL:\n\n' + invokeUrlFor(fn))
  }
}

const loadFns = async () => {
  try {
    const response = await listFunctions()
    functions.value = response.data.functions || []
  } catch (e) {
    console.error(e)
  }
}

const deleteFn = async (fn) => {
  if (!confirm(`Delete function "${fn.name}"? This is irreversible — code, deployments, secrets, and routes for this function are removed.`)) return
  deletingId.value = fn.id
  try {
    await apiClient.delete(`/functions/${fn.id}`)
    await loadFns()
  } catch (e) {
    const msg = e.response?.data?.error?.message || e.message || 'Delete failed'
    alert('Delete failed: ' + msg)
  } finally {
    deletingId.value = ''
  }
}

onMounted(loadFns)
</script>
