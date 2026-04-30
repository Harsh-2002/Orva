<template>
  <div class="space-y-4">
    <!-- Page header -->
    <div class="flex items-center justify-between gap-4 flex-wrap">
      <h1 class="text-xl font-semibold text-white tracking-tight">
        Webhooks
      </h1>
      <Button @click="openCreate">
        <Plus class="w-4 h-4" />
        New webhook
      </Button>
    </div>

    <p class="text-xs text-foreground-muted max-w-2xl leading-relaxed">
      System events fan out to operator-configured URLs. Subscriptions are
      global. Payloads are HMAC-SHA256 signed (header
      <code class="font-mono text-[11px] px-1.5 py-0.5 rounded bg-surface border border-border text-white">X-Orva-Signature</code>);
      the receiver verifies with the secret you copy at create time.
    </p>

    <!-- Subscriptions table -->
    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <table class="w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th class="px-4 py-3 font-medium">
              Name
            </th>
            <th class="px-4 py-3 font-medium hidden md:table-cell">
              URL
            </th>
            <th class="px-4 py-3 font-medium hidden sm:table-cell">
              Events
            </th>
            <th class="px-4 py-3 font-medium">
              Status
            </th>
            <th class="px-4 py-3 font-medium hidden lg:table-cell">
              Last delivery
            </th>
            <th class="px-4 py-3 font-medium text-right">
              Actions
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr
            v-for="sub in subscriptions"
            :key="sub.id"
            class="hover:bg-surface/40 transition-colors cursor-pointer"
            @click="openDeliveries(sub)"
          >
            <td class="px-4 py-3 font-medium text-white">
              <div class="flex flex-col">
                <span>{{ sub.name }}</span>
                <span class="text-[10px] text-foreground-muted font-mono">{{ sub.id }}</span>
              </div>
            </td>
            <td class="px-4 py-3 text-xs text-foreground-muted truncate max-w-xs hidden md:table-cell">
              {{ sub.url }}
            </td>
            <td class="px-4 py-3 hidden sm:table-cell">
              <div class="flex flex-wrap gap-1">
                <span
                  v-for="ev in eventsBadgeList(sub)"
                  :key="ev"
                  class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] bg-surface border border-border text-foreground font-mono"
                >{{ ev }}</span>
              </div>
            </td>
            <td class="px-4 py-3">
              <span
                class="inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-[11px] font-medium border"
                :class="statusPill(sub)"
              >
                <span
                  class="w-1.5 h-1.5 rounded-full"
                  :class="statusDot(sub)"
                />
                {{ statusLabel(sub) }}
              </span>
            </td>
            <td class="px-4 py-3 text-foreground-muted text-xs hidden lg:table-cell">
              {{ sub.last_delivery_at ? formatDate(sub.last_delivery_at) : '—' }}
            </td>
            <td
              class="px-4 py-3 text-right"
              @click.stop
            >
              <div class="inline-flex items-center gap-1">
                <IconButton
                  :icon="Zap"
                  title="Send test event"
                  @click="testSubscription(sub)"
                />
                <IconButton
                  :icon="Edit"
                  title="Edit"
                  @click="openEdit(sub)"
                />
                <IconButton
                  :icon="Trash2"
                  variant="danger"
                  title="Delete"
                  @click="removeSubscription(sub)"
                />
              </div>
            </td>
          </tr>
          <tr v-if="subscriptions.length === 0">
            <td
              colspan="6"
              class="px-4 py-12 text-center"
            >
              <Webhook class="w-10 h-10 text-foreground-muted mx-auto mb-3 opacity-30" />
              <p class="text-foreground-muted text-sm">
                No webhooks yet.
              </p>
              <p class="text-foreground-muted text-xs mt-1">
                Wire ops integrations: Slack on deploy failures, PagerDuty on cron failures, etc.
              </p>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Create / Edit modal -->
    <div
      v-if="showForm"
      class="fixed inset-0 bg-black/80 backdrop-blur-sm flex items-center justify-center z-50 p-4"
      @click.self="closeForm"
    >
      <div class="bg-surface border border-border rounded-lg w-full max-w-xl shadow-2xl shadow-black/50 max-h-[90vh] overflow-y-auto">
        <div class="border-b border-border px-6 py-4 flex items-center justify-between">
          <h2 class="text-base font-semibold text-foreground">
            {{ editingId ? 'Edit webhook' : 'New webhook' }}
          </h2>
          <IconButton
            :icon="X"
            title="Close"
            @click="closeForm"
          />
        </div>

        <div
          v-if="!mintedSecret"
          class="p-6 space-y-4"
        >
          <div>
            <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">Name</label>
            <input
              v-model="form.name"
              placeholder="ops-slack"
              class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-white focus:border-white"
            >
          </div>
          <div>
            <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">Receiver URL</label>
            <input
              v-model="form.url"
              placeholder="https://hooks.slack.com/services/..."
              class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground font-mono focus:outline-none focus:ring-1 focus:ring-white focus:border-white"
            >
            <p class="text-[11px] text-foreground-muted mt-1.5">
              The receiver must respond 2xx within 15s. Failed deliveries retry up to 5× with exponential backoff.
            </p>
          </div>
          <div>
            <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">Events</label>
            <div class="flex flex-wrap gap-1.5">
              <Button
                v-for="ev in allEvents"
                :key="ev.value"
                variant="chip"
                size="xs"
                :active="form.events.includes(ev.value)"
                class="font-mono"
                @click="toggleEvent(ev.value)"
              >
                {{ ev.value }}
              </Button>
            </div>
            <p class="text-[11px] text-foreground-muted mt-1.5">
              Pick <code class="font-mono">*</code> to receive every event. Each badge above is one of the 8 system events that can fire today.
            </p>
          </div>
          <div class="flex items-center gap-2 pt-1">
            <input
              id="enabled"
              v-model="form.enabled"
              type="checkbox"
              class="w-4 h-4 rounded border-border bg-background"
            >
            <label
              for="enabled"
              class="text-sm text-foreground"
            >Enabled</label>
          </div>
        </div>

        <!-- Secret-shown-once view (only on create) -->
        <div
          v-else
          class="p-6 space-y-3"
        >
          <div class="flex items-center gap-2 text-success">
            <CheckCircle class="w-5 h-5" />
            <span class="text-sm font-medium">Webhook created</span>
          </div>
          <p class="text-xs text-foreground-muted">
            Copy this secret <span class="text-foreground font-medium">now</span> — it won't be shown again. The receiver uses it to verify HMAC signatures.
          </p>
          <div class="bg-background border border-border rounded p-3 font-mono text-xs break-all flex items-center gap-2">
            <code class="flex-1 text-foreground">{{ mintedSecret }}</code>
            <IconButton
              :icon="mintedCopied ? Check : Copy"
              :title="mintedCopied ? 'Copied' : 'Copy secret'"
              @click="copyMinted"
            />
          </div>
        </div>

        <div class="border-t border-border px-6 py-4 flex justify-end gap-2">
          <Button
            v-if="!mintedSecret"
            variant="ghost"
            @click="closeForm"
          >
            Cancel
          </Button>
          <Button
            v-if="!mintedSecret"
            :disabled="!canSubmit || saving"
            @click="save"
          >
            {{ saving ? 'Saving…' : (editingId ? 'Save' : 'Create') }}
          </Button>
          <Button
            v-else
            @click="closeForm"
          >
            Done
          </Button>
        </div>
      </div>
    </div>

    <!-- Deliveries drawer -->
    <div
      v-if="drawerSub"
      class="fixed inset-0 bg-black/60 backdrop-blur-sm flex justify-end z-50"
      @click.self="closeDrawer"
    >
      <div class="bg-background border-l border-border w-full max-w-2xl h-full overflow-y-auto">
        <div class="border-b border-border px-6 py-4 flex items-center justify-between bg-surface sticky top-0">
          <div class="min-w-0">
            <h2 class="text-base font-semibold text-foreground truncate">
              Deliveries · {{ drawerSub.name }}
            </h2>
            <p class="text-[11px] text-foreground-muted font-mono truncate">
              {{ drawerSub.id }}
            </p>
          </div>
          <IconButton
            :icon="X"
            title="Close"
            @click="closeDrawer"
          />
        </div>

        <div class="p-4 space-y-2">
          <div
            v-if="!deliveries.length"
            class="text-center text-foreground-muted text-sm py-12"
          >
            No deliveries yet. Trigger a system event or use
            <span class="text-foreground">Send test event</span> to seed one.
          </div>
          <div
            v-for="d in deliveries"
            :key="d.id"
            class="bg-surface border border-border rounded p-3 space-y-1.5"
          >
            <div class="flex items-center justify-between gap-2 flex-wrap">
              <code class="text-xs font-mono text-foreground">{{ d.event_name }}</code>
              <span
                class="inline-flex items-center px-2 py-0.5 rounded text-[10px] font-medium border"
                :class="deliveryPill(d.status)"
              >
                {{ d.status }}
              </span>
            </div>
            <div class="flex items-center justify-between text-[11px] text-foreground-muted gap-2 flex-wrap">
              <span class="font-mono">{{ d.id }}</span>
              <span>{{ formatDate(d.created_at) }}</span>
            </div>
            <div class="flex items-center justify-between text-[11px] text-foreground-muted gap-2 flex-wrap">
              <span>attempts {{ d.attempts }} / {{ d.max_attempts }}</span>
              <span v-if="d.response_status">HTTP {{ d.response_status }}</span>
            </div>
            <p
              v-if="d.last_error"
              class="text-[11px] text-error truncate"
              :title="d.last_error"
            >
              {{ d.last_error }}
            </p>
            <Button
              v-if="d.status === 'failed'"
              size="xs"
              variant="ghost"
              @click="retryDelivery(d)"
            >
              <RotateCcw class="w-3.5 h-3.5" />
              Retry
            </Button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import {
  Plus, Edit, Trash2, X, Webhook, CheckCircle, Copy, Check, Zap, RotateCcw,
} from 'lucide-vue-next'
import {
  listWebhooks, createWebhook, updateWebhook, deleteWebhook, testWebhook,
  listWebhookDeliveries, retryWebhookDelivery,
} from '@/api/endpoints'
import { useConfirmStore } from '@/stores/confirm'
import { copyText } from '@/utils/clipboard'
import Button from '@/components/common/Button.vue'
import IconButton from '@/components/common/IconButton.vue'

const confirmStore = useConfirmStore()

// Operator-facing event catalog. Mirrors the backend's allowedEvents
// in handlers/webhooks.go — keep in sync if the catalog grows.
const allEvents = [
  { value: '*' },
  { value: 'deployment.succeeded' },
  { value: 'deployment.failed' },
  { value: 'function.created' },
  { value: 'function.updated' },
  { value: 'function.deleted' },
  { value: 'execution.error' },
  { value: 'cron.failed' },
  { value: 'job.succeeded' },
  { value: 'job.failed' },
]

const subscriptions = ref([])
const showForm = ref(false)
const editingId = ref(null)
const saving = ref(false)
const mintedSecret = ref('')
const mintedCopied = ref(false)
const form = ref({ name: '', url: '', events: ['*'], enabled: true })

const drawerSub = ref(null)
const deliveries = ref([])
let drawerPollTimer = null

const canSubmit = computed(() =>
  form.value.name.trim() && form.value.url.trim() && form.value.events.length > 0
)

const eventsBadgeList = (sub) => {
  if (!sub.events || sub.events.length === 0) return ['*']
  return sub.events.length > 3
    ? [...sub.events.slice(0, 2), `+${sub.events.length - 2}`]
    : sub.events
}

const statusPill = (sub) => {
  if (!sub.enabled) return 'bg-surface text-foreground-muted border-border'
  if (sub.last_status === 'failed') return 'bg-error/10 text-error border-error/30'
  if (sub.last_status === 'ok') return 'bg-success/10 text-success border-success/30'
  return 'bg-amber-500/10 text-amber-300 border-amber-500/30'
}
const statusDot = (sub) => {
  if (!sub.enabled) return 'bg-foreground-muted/40'
  if (sub.last_status === 'failed') return 'bg-error'
  if (sub.last_status === 'ok') return 'bg-success'
  return 'bg-amber-400'
}
const statusLabel = (sub) => {
  if (!sub.enabled) return 'paused'
  if (sub.last_status === 'failed') return 'failing'
  if (sub.last_status === 'ok') return 'healthy'
  return 'pending first delivery'
}

const deliveryPill = (s) => {
  switch (s) {
    case 'pending':   return 'bg-amber-500/10 text-amber-300 border-amber-500/30'
    case 'running':   return 'bg-sky-500/15 text-sky-300 border-sky-500/30 animate-pulse'
    case 'succeeded': return 'bg-success/10 text-success border-success/30'
    case 'failed':    return 'bg-error/10 text-error border-error/30'
    default:          return 'bg-surface text-foreground-muted border-border'
  }
}

const formatDate = (s) => {
  if (!s) return '—'
  return new Date(s).toLocaleString('en-US', {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

const loadSubscriptions = async () => {
  try {
    const res = await listWebhooks()
    subscriptions.value = res.data.subscriptions || []
  } catch (e) {
    console.error('Failed to load webhooks', e)
  }
}

const openCreate = () => {
  editingId.value = null
  form.value = { name: '', url: '', events: ['*'], enabled: true }
  mintedSecret.value = ''
  mintedCopied.value = false
  showForm.value = true
}
const openEdit = (sub) => {
  editingId.value = sub.id
  form.value = {
    name: sub.name,
    url: sub.url,
    events: [...(sub.events || ['*'])],
    enabled: sub.enabled,
  }
  mintedSecret.value = ''
  showForm.value = true
}
const closeForm = () => {
  showForm.value = false
  editingId.value = null
  mintedSecret.value = ''
}

const toggleEvent = (e) => {
  const idx = form.value.events.indexOf(e)
  if (idx >= 0) form.value.events.splice(idx, 1)
  else form.value.events.push(e)
}

const save = async () => {
  if (!canSubmit.value || saving.value) return
  saving.value = true
  try {
    if (editingId.value) {
      await updateWebhook(editingId.value, {
        name: form.value.name.trim(),
        url: form.value.url.trim(),
        events: form.value.events,
        enabled: form.value.enabled,
      })
      await loadSubscriptions()
      closeForm()
    } else {
      const res = await createWebhook({
        name: form.value.name.trim(),
        url: form.value.url.trim(),
        events: form.value.events,
        enabled: form.value.enabled,
      })
      mintedSecret.value = res.data.secret
      await loadSubscriptions()
    }
  } catch (e) {
    confirmStore.notify({
      title: 'Failed to save webhook',
      message: e?.response?.data?.error?.message || e.message,
      danger: true,
    })
  } finally {
    saving.value = false
  }
}

const copyMinted = async () => {
  const ok = await copyText(mintedSecret.value)
  if (ok) {
    mintedCopied.value = true
    setTimeout(() => { mintedCopied.value = false }, 1500)
  }
}

const removeSubscription = async (sub) => {
  const ok = await confirmStore.ask({
    title: `Delete "${sub.name}"?`,
    message: 'Future events will not fire to this URL. Existing deliveries will be removed too.',
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  try {
    await deleteWebhook(sub.id)
    await loadSubscriptions()
  } catch (e) {
    confirmStore.notify({ title: 'Delete failed', message: e.message, danger: true })
  }
}

const testSubscription = async (sub) => {
  try {
    await testWebhook(sub.id)
    confirmStore.notify({
      title: 'Test event queued',
      message: `Will deliver to ${sub.url} within 5s. Open the row to watch the delivery.`,
    })
  } catch (e) {
    confirmStore.notify({ title: 'Test failed', message: e.message, danger: true })
  }
}

const openDeliveries = async (sub) => {
  drawerSub.value = sub
  await loadDeliveries(sub.id)
  // Auto-refresh while drawer is open so retries become visible.
  drawerPollTimer = setInterval(() => loadDeliveries(sub.id), 4000)
}
const loadDeliveries = async (id) => {
  try {
    const res = await listWebhookDeliveries(id)
    deliveries.value = res.data.deliveries || []
  } catch (e) {
    console.error('Failed to load deliveries', e)
  }
}
const closeDrawer = () => {
  drawerSub.value = null
  deliveries.value = []
  if (drawerPollTimer) clearInterval(drawerPollTimer)
  drawerPollTimer = null
}

const retryDelivery = async (d) => {
  try {
    await retryWebhookDelivery(d.id)
    if (drawerSub.value) await loadDeliveries(drawerSub.value.id)
  } catch (e) {
    confirmStore.notify({ title: 'Retry failed', message: e.message, danger: true })
  }
}

onMounted(() => loadSubscriptions())
onBeforeUnmount(() => {
  if (drawerPollTimer) clearInterval(drawerPollTimer)
})
</script>
