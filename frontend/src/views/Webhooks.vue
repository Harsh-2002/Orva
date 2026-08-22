<template>
  <div class="space-y-6">
    <!-- Page header. Title and subhead are one block so the subhead sits at
         its own mt-1.5 rather than picking up the page's section rhythm on
         top of it, the way every other list view is built. -->
    <div class="flex items-start justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold text-white tracking-tight">
          Webhooks
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
          Send signed system events to external URLs.
        </p>
      </div>
      <Button @click="openCreate">
        <Plus class="w-4 h-4" />
        New webhook
      </Button>
    </div>

    <!-- Subscriptions list. The table used to render at every width with
         four of its six columns hidden, which put the row actions ~230px
         to the right of a 375px viewport. Mobile now gets stacked cards
         carrying every column and every action; the table starts at sm. -->
    <LoadError
      v-if="loadError"
      what="Webhooks"
      :message="loadError"
      :on-retry="loadSubscriptions"
      class="mb-3"
    />

    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <ul class="sm:hidden divide-y divide-border">
        <li
          v-for="sub in subscriptions"
          :key="sub.id"
          class="px-4 py-3"
        >
          <div class="flex items-start justify-between gap-2">
            <!-- The desktop row opens deliveries on row click; on the card
                 that affordance is a real button so it stays reachable by
                 keyboard and announces itself. -->
            <button
              type="button"
              class="min-w-0 flex-1 text-left rounded focus:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background"
              @click="openDeliveries(sub)"
            >
              <div class="font-medium text-white truncate">
                {{ sub.name }}
              </div>
              <div class="text-[10px] text-foreground-muted font-mono break-all">
                {{ sub.id }}
              </div>
              <div class="mt-1 text-xs text-foreground-muted font-mono break-all">
                {{ sub.url }}
              </div>
              <div class="mt-2 flex flex-wrap items-center gap-1.5">
                <span
                  class="inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-xs font-medium border"
                  :class="statusPill(sub)"
                >
                  <span
                    class="w-1.5 h-1.5 rounded-full"
                    :class="statusDot(sub)"
                  />
                  {{ statusLabel(sub) }}
                </span>
                <span
                  v-for="ev in eventsBadgeList(sub)"
                  :key="ev"
                  class="inline-flex items-center px-1.5 py-0.5 rounded text-xs bg-surface border border-border text-foreground font-mono"
                >{{ ev }}</span>
              </div>
              <div class="mt-1.5 text-[11px] text-foreground-muted">
                Last delivery {{ sub.last_delivery_at ? formatDate(sub.last_delivery_at) : EMPTY }}
              </div>
            </button>
            <div class="flex items-center gap-1 shrink-0">
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
          </div>
        </li>
        <li
          v-if="loaded && !loadError && subscriptions.length === 0"
          class="px-4 py-12 text-center"
        >
          <p class="text-foreground-muted text-sm">
            No webhooks yet.
          </p>
          <p class="text-foreground-muted text-xs mt-1">
            Add an endpoint to receive signed system events.
          </p>
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
              Events
            </th>
            <th
              scope="col"
              class="px-6 py-3 font-medium"
            >
              Status
            </th>
            <th
              scope="col"
              class="px-6 py-3 font-medium hidden lg:table-cell"
            >
              Last delivery
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
            v-for="sub in subscriptions"
            :key="sub.id"
            class="hover:bg-surface/40 transition-colors"
          >
            <td class="px-6 py-4 font-medium text-white">
              <!-- The drawer trigger is a real button, as it already is on the
                   mobile card. It used to be a @click on the bare <tr>, which
                   left delivery history unreachable by keyboard at every width
                   the table renders at, with no other route to it. -->
              <button
                type="button"
                class="touch-expand-sm w-full text-left cursor-pointer rounded-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-primary"
                @click="openDeliveries(sub)"
              >
                <div class="flex flex-col">
                  <span>{{ sub.name }}</span>
                  <span class="text-xs text-foreground-muted font-mono">{{ sub.id }}</span>
                </div>
              </button>
            </td>
            <td class="px-6 py-4 text-xs text-foreground-muted truncate max-w-xs hidden md:table-cell">
              {{ sub.url }}
            </td>
            <td class="px-6 py-4">
              <div class="flex flex-wrap gap-1">
                <span
                  v-for="ev in eventsBadgeList(sub)"
                  :key="ev"
                  class="inline-flex items-center px-1.5 py-0.5 rounded text-xs bg-surface border border-border text-foreground font-mono"
                >{{ ev }}</span>
              </div>
            </td>
            <td class="px-6 py-4">
              <span
                class="inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-xs font-medium border"
                :class="statusPill(sub)"
              >
                <span
                  class="w-1.5 h-1.5 rounded-full"
                  :class="statusDot(sub)"
                />
                {{ statusLabel(sub) }}
              </span>
            </td>
            <td class="px-6 py-4 text-foreground-muted text-xs hidden lg:table-cell">
              {{ sub.last_delivery_at ? formatDate(sub.last_delivery_at) : EMPTY }}
            </td>
            <td class="px-6 py-4 text-right">
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
          <tr v-if="loaded && !loadError && subscriptions.length === 0">
            <td
              colspan="6"
              class="px-6 py-12 text-center"
            >
              <p class="text-foreground-muted text-sm">
                No webhooks yet.
              </p>
              <p class="text-foreground-muted text-xs mt-1">
                Add an endpoint to receive signed system events.
              </p>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Create / Edit modal -->
    <Modal
      :model-value="showForm"
      :title="editingId ? 'Edit webhook' : 'New webhook'"
      size="lg"
      @update:model-value="$event ? null : closeForm()"
    >
      <div
        v-if="!mintedSecret"
        class="space-y-4"
      >
        <div>
          <label
            for="webhook-name"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
          >Name</label>
          <input
            id="webhook-name"
            v-model="form.name"
            placeholder="ops-slack"
            class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-white focus:border-white"
          >
        </div>
        <div>
          <label
            for="webhook-url"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
          >Receiver URL</label>
          <input
            id="webhook-url"
            v-model="form.url"
            placeholder="https://hooks.slack.com/services/..."
            class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground font-mono focus:outline-none focus:ring-1 focus:ring-white focus:border-white"
          >
          <p class="text-xs text-foreground-muted mt-1.5">
            The receiver must respond 2xx within 15s. Failed deliveries retry up to 5× with exponential backoff.
          </p>
        </div>
        <div>
          <span
            id="webhook-events-label"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
          >Events</span>
          <div
            class="flex flex-wrap gap-1.5"
            role="group"
            aria-labelledby="webhook-events-label"
          >
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
          <p class="text-xs text-foreground-muted mt-1.5">
            Pick <code class="font-mono">*</code> to receive every event. The badges beside it are the {{ systemEventCount }} system events that can fire today.
          </p>
        </div>
        <div class="flex items-center gap-2 pt-1">
          <input
            id="enabled"
            v-model="form.enabled"
            type="checkbox"
            class="w-4 h-4 rounded border-border bg-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background"
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
        class="space-y-3"
      >
        <div class="flex items-center gap-2 text-success-fg">
          <CheckCircle class="w-5 h-5" />
          <span class="text-sm font-medium">Webhook created</span>
        </div>
        <p class="text-xs text-foreground-muted">
          Copy this secret <span class="text-foreground font-medium">now</span>. It won't be shown again.
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

      <template #footer>
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
      </template>
    </Modal>

    <!-- Deliveries drawer. Shared Drawer.vue rather than a hand-rolled
         overlay, so Esc-to-close, the mobile bottom-sheet shape and the
         safe-area padding come for free. 42rem keeps the desktop panel
         the width the old max-w-2xl gave it. -->
    <Drawer
      :model-value="drawerOpen"
      width="42rem"
      @update:model-value="$event ? null : closeDrawer()"
    >
      <template #title>
        Deliveries · {{ drawerSub?.name }}
      </template>

      <div class="p-4 space-y-2">
        <p class="text-xs text-foreground-muted font-mono break-all">
          {{ drawerSub?.id }}
        </p>
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
              class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium border"
              :class="deliveryPill(d.status)"
            >
              {{ d.status }}
            </span>
          </div>
          <div class="flex items-center justify-between text-xs text-foreground-muted gap-2 flex-wrap">
            <span class="font-mono break-all">{{ d.id }}</span>
            <span>{{ formatDate(d.created_at) }}</span>
          </div>
          <div class="flex items-center justify-between text-xs text-foreground-muted gap-2 flex-wrap">
            <span>attempts {{ d.attempts }} / {{ d.max_attempts }}</span>
            <span v-if="d.response_status">HTTP {{ d.response_status }}</span>
          </div>
          <p
            v-if="d.last_error"
            class="text-xs text-danger-fg truncate"
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
    </Drawer>
  </div>
</template>

<script setup>
defineOptions({ name: 'WebhooksView' })

import { EMPTY } from '@/utils/format'
import { ref, computed, onMounted, onUnmounted, onActivated, onDeactivated } from 'vue'
import {
  Plus, Edit, Trash2, CheckCircle, Copy, Check, Zap, RotateCcw,
} from '@lucide/vue'
import {
  listWebhooks, createWebhook, updateWebhook, deleteWebhook, testWebhook,
  listWebhookDeliveries, retryWebhookDelivery,
} from '@/api/endpoints'
import { useConfirmStore } from '@/stores/confirm'
import { copyText } from '@/utils/clipboard'
import Button from '@/components/common/Button.vue'
import IconButton from '@/components/common/IconButton.vue'
import LoadError from '@/components/common/LoadError.vue'
import Modal from '@/components/common/Modal.vue'
import Drawer from '@/components/common/Drawer.vue'

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

// '*' is the catch-all, not an event, so it doesn't count toward the
// number quoted in the picker's help text.
const systemEventCount = allEvents.filter((e) => e.value !== '*').length

const subscriptions = ref([])
const loadError = ref('')
const loaded = ref(false)
const showForm = ref(false)
const editingId = ref(null)
const saving = ref(false)
const mintedSecret = ref('')
const mintedCopied = ref(false)
const form = ref({ name: '', url: '', events: ['*'], enabled: true })

const drawerOpen = ref(false)
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
  if (!sub.enabled) return 'bg-warning-tint text-warning-fg border-warning-ring'
  if (sub.last_status === 'failed') return 'bg-danger-tint text-danger-fg border-danger-ring'
  if (sub.last_status === 'ok') return 'bg-success-tint text-success-fg border-success-ring'
  return 'bg-warning-tint text-warning-fg border-warning-ring'
}
const statusDot = (sub) => {
  if (!sub.enabled) return 'bg-warning-fg'
  if (sub.last_status === 'failed') return 'bg-danger-fg'
  if (sub.last_status === 'ok') return 'bg-success-fg'
  return 'bg-warning-fg'
}
const statusLabel = (sub) => {
  if (!sub.enabled) return 'paused'
  if (sub.last_status === 'failed') return 'failing'
  if (sub.last_status === 'ok') return 'healthy'
  return 'pending first delivery'
}

const deliveryPill = (s) => {
  switch (s) {
    case 'pending':   return 'bg-warning-tint text-warning-fg border-warning-ring'
    case 'running':   return 'bg-info-tint text-info-fg border-info-ring'
    case 'succeeded': return 'bg-success-tint text-success-fg border-success-ring'
    case 'failed':    return 'bg-danger-tint text-danger-fg border-danger-ring'
    default:          return 'bg-surface text-foreground-muted border-border'
  }
}

const formatDate = (s) => {
  if (!s) return EMPTY
  return new Date(s).toLocaleString('en-US', {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

const loadSubscriptions = async () => {
  try {
    const res = await listWebhooks()
    subscriptions.value = res.data.subscriptions || []
    loadError.value = ''
  } catch (e) {
    loadError.value = e?.response?.data?.error?.message || e?.message || 'Request failed'
  } finally {
    loaded.value = true
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
      message: `Will deliver to ${sub.url} within 5s. Open the webhook to watch the delivery.`,
    })
  } catch (e) {
    confirmStore.notify({ title: 'Test failed', message: e.message, danger: true })
  }
}

const stopDrawerPoll = () => {
  if (drawerPollTimer) clearInterval(drawerPollTimer)
  drawerPollTimer = null
}
const startDrawerPoll = (id) => {
  stopDrawerPoll()
  // Auto-refresh while the drawer is open so retries become visible.
  drawerPollTimer = setInterval(() => loadDeliveries(id), 4000)
}

const openDeliveries = async (sub) => {
  drawerSub.value = sub
  deliveries.value = []
  drawerOpen.value = true
  await loadDeliveries(sub.id)
  startDrawerPoll(sub.id)
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
  drawerOpen.value = false
  stopDrawerPoll()
  // drawerSub/deliveries stay put until the next open: the panel is still on
  // screen for the length of its slide-out and would otherwise blank out.
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

// Layout.vue wraps views in <keep-alive>, so navigating away deactivates this
// view rather than unmounting it: without onDeactivated the 4s delivery poll
// would keep firing for the rest of the session. onActivated also runs on the
// first mount, where onMounted has already loaded and no drawer is open, so
// the deactivated flag keeps that pass from double-fetching.
let wasDeactivated = false
onActivated(() => {
  if (!wasDeactivated) return
  wasDeactivated = false
  loadSubscriptions()
  if (drawerOpen.value && drawerSub.value) startDrawerPoll(drawerSub.value.id)
})
onDeactivated(() => {
  wasDeactivated = true
  stopDrawerPoll()
})
onUnmounted(stopDrawerPoll)
</script>
