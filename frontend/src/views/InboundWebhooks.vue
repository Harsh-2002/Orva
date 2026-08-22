<template>
  <div class="space-y-6">
    <!-- Header. The title block plus the count + Refresh + New trigger
         toolbar has a min-content width wider than a 375 px content box,
         and a non-wrapping row is silently clipped under the global
         overflow-x: hidden. Column under sm, the original row from sm up. -->
    <div class="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-white tracking-tight">
          Inbound webhooks
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
          Signed URLs that invoke
          <router-link
            :to="`/functions/${fnName}`"
            class="text-white underline"
          >
            {{ fnName }}
          </router-link>
          from external services.
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-xs text-foreground-muted">
          {{ rows.length }} {{ rows.length === 1 ? 'trigger' : 'triggers' }}
        </span>
        <Button
          variant="secondary"
          size="sm"
          @click="refresh"
        >
          <RefreshCw
            class="w-3.5 h-3.5"
            :class="{ 'animate-spin': loading }"
          />
          Refresh
        </Button>
        <Button
          size="sm"
          @click="openCreate()"
        >
          <Plus class="w-3.5 h-3.5" />
          New trigger
        </Button>
      </div>
    </div>

    <!-- Just-created banner: shows the plaintext secret ONCE. -->
    <div
      v-if="lastCreated"
      class="rounded-lg border border-warning-ring bg-warning-tint p-4 space-y-3"
    >
      <div class="flex items-center justify-between gap-4">
        <div class="text-sm text-warning-fg font-medium">
          Trigger created. Copy the secret now. It will not be shown again.
        </div>
        <button
          class="inline-flex items-center shrink-0 rounded px-2 -mx-2 touch-expand-sm text-xs text-warning-fg hover:text-white transition-colors"
          @click="lastCreated = null"
        >
          Dismiss
        </button>
      </div>
      <div class="text-xs space-y-2">
        <div>
          <span class="text-foreground-muted uppercase tracking-wider text-[10px]">URL</span>
          <code class="ml-2 font-mono text-white break-all">{{ origin + lastCreated.trigger_url }}</code>
          <button
            class="inline-flex items-center rounded px-2 touch-expand-sm text-warning-fg hover:text-white transition-colors"
            @click="copy(origin + lastCreated.trigger_url)"
          >
            Copy URL
          </button>
        </div>
        <div>
          <span class="text-foreground-muted uppercase tracking-wider text-[10px]">Secret</span>
          <code class="ml-2 font-mono text-white break-all">{{ lastCreated.secret }}</code>
          <button
            class="inline-flex items-center rounded px-2 touch-expand-sm text-warning-fg hover:text-white transition-colors"
            @click="copy(lastCreated.secret)"
          >
            Copy secret
          </button>
        </div>
        <div>
          <span class="text-foreground-muted uppercase tracking-wider text-[10px]">Sample curl</span>
          <pre class="mt-1 bg-background border border-border rounded p-3 text-[11px] font-mono text-white whitespace-pre-wrap overflow-x-auto">{{ sampleCurl(lastCreated) }}</pre>
        </div>
      </div>
    </div>

    <!-- Table -->
    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <!-- Mobile (<sm) stacked-row list. -->
      <ul class="sm:hidden divide-y divide-border">
        <li
          v-for="row in rows"
          :key="row.id"
          class="px-4 py-3"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2 flex-wrap">
                <span class="font-medium text-white truncate">{{ row.name }}</span>
                <span
                  class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] border"
                  :class="row.active
                    ? 'bg-success-tint text-success-fg border-success-ring'
                    : 'bg-surface text-foreground-muted border-border'"
                >{{ row.active ? 'active' : 'paused' }}</span>
              </div>
              <div class="mt-1 text-[11px] text-foreground-muted font-mono break-all">
                {{ origin }}/webhook/{{ row.id }}
              </div>
              <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-foreground-muted">
                <span class="font-mono">{{ row.signature_format }}</span>
                <!-- secret_preview is an lg+ column on desktop, so the card is
                     the only place a phone can see which secret is in force. -->
                <span
                  v-if="row.secret_preview"
                  class="font-mono"
                >{{ row.secret_preview }}</span>
                <span>created {{ formatDate(row.created_at) }}</span>
              </div>
            </div>
            <div class="flex items-center gap-1 shrink-0">
              <IconButton
                :icon="Send"
                variant="success"
                title="Send a test payload"
                @click="openTest(row)"
              />
              <IconButton
                :icon="row.active ? Pause : Play"
                :title="row.active ? 'Pause this trigger' : 'Resume this trigger'"
                :disabled="busyId === row.id"
                @click="toggleActive(row)"
              />
              <IconButton
                :icon="Trash2"
                variant="danger"
                title="Delete"
                @click="confirmRemove(row)"
              />
            </div>
          </div>
        </li>
        <li
          v-if="!loading && !rows.length"
          class="px-6 py-12 text-center text-sm text-foreground-muted"
        >
          No inbound triggers yet.
        </li>
      </ul>

      <table class="hidden sm:table w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th
              scope="col"
              class="px-6 py-3"
            >
              Name
            </th>
            <th
              scope="col"
              class="px-6 py-3 hidden md:table-cell"
            >
              URL
            </th>
            <th
              scope="col"
              class="px-6 py-3 hidden sm:table-cell"
            >
              Format
            </th>
            <th
              scope="col"
              class="px-6 py-3 hidden lg:table-cell"
            >
              Secret
            </th>
            <th
              scope="col"
              class="px-6 py-3 hidden md:table-cell"
            >
              Active
            </th>
            <th
              scope="col"
              class="px-6 py-3 hidden lg:table-cell"
            >
              Created
            </th>
            <th
              scope="col"
              class="px-6 py-3 text-right"
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
          >
            <td class="px-6 py-4 font-medium text-white">
              <div class="flex flex-col">
                <span>{{ row.name }}</span>
                <span class="text-xs text-foreground-muted font-mono">{{ row.id }}</span>
              </div>
            </td>
            <td class="px-6 py-4 font-mono text-xs text-foreground-muted hidden md:table-cell">
              <span class="break-all">{{ origin }}/webhook/{{ row.id }}</span>
            </td>
            <td class="px-6 py-4 hidden sm:table-cell">
              <span
                class="inline-flex items-center px-2 py-0.5 rounded text-xs border bg-surface text-foreground-muted border-border font-mono"
              >
                {{ row.signature_format }}
              </span>
            </td>
            <td class="px-6 py-4 font-mono text-xs text-foreground-muted hidden lg:table-cell">
              {{ row.secret_preview }}
            </td>
            <td class="px-6 py-4 hidden md:table-cell">
              <span
                class="inline-flex items-center px-2 py-0.5 rounded text-xs border"
                :class="row.active
                  ? 'bg-success-tint text-success-fg border-success-ring'
                  : 'bg-surface text-foreground-muted border-border'"
              >
                {{ row.active ? 'active' : 'paused' }}
              </span>
            </td>
            <td class="px-6 py-4 text-foreground-muted text-xs hidden lg:table-cell">
              {{ formatDate(row.created_at) }}
            </td>
            <td class="px-6 py-4 text-right">
              <div class="inline-flex items-center gap-1">
                <IconButton
                  :icon="Send"
                  variant="success"
                  title="Send a test payload"
                  @click="openTest(row)"
                />
                <IconButton
                  :icon="row.active ? Pause : Play"
                  :title="row.active ? 'Pause this trigger' : 'Resume this trigger'"
                  :disabled="busyId === row.id"
                  @click="toggleActive(row)"
                />
                <IconButton
                  :icon="Trash2"
                  variant="danger"
                  title="Delete"
                  @click="confirmRemove(row)"
                />
              </div>
            </td>
          </tr>
          <tr v-if="!loading && !rows.length">
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

    <!-- Create drawer -->
    <Drawer
      v-model="create.open"
      title="New inbound trigger"
      width="560px"
    >
      <div class="p-5 space-y-5 text-sm">
        <div>
          <label
            for="inbound-name"
            class="text-xs uppercase tracking-wider text-foreground-muted"
          >Name</label>
          <input
            id="inbound-name"
            v-model="create.name"
            placeholder="e.g. github-deploys"
            class="mt-2 w-full bg-surface border border-border rounded px-3 py-2 text-sm text-white focus:outline-none focus:border-white"
          >
        </div>
        <div>
          <label
            for="inbound-format"
            class="text-xs uppercase tracking-wider text-foreground-muted"
          >Signature format</label>
          <select
            id="inbound-format"
            v-model="create.format"
            class="mt-2 w-full bg-surface border border-border rounded px-3 py-2 text-sm text-white focus:outline-none focus:border-white"
          >
            <option value="hmac_sha256_hex">
              hmac_sha256_hex (default)
            </option>
            <option value="hmac_sha256_base64">
              hmac_sha256_base64
            </option>
            <option value="github">
              github (X-Hub-Signature-256)
            </option>
            <option value="stripe">
              stripe (Stripe-Signature)
            </option>
            <option value="slack">
              slack (X-Slack-Signature)
            </option>
          </select>
          <p class="text-[11px] text-foreground-muted mt-2">
            Pick the format your upstream service produces. The header name is
            stamped automatically; you can override on the row after creation.
          </p>
        </div>
        <div
          v-if="create.error"
          class="text-xs text-danger-fg"
        >
          {{ create.error }}
        </div>
      </div>
      <template #footer>
        <div class="flex items-center justify-end gap-2">
          <Button
            variant="ghost"
            size="sm"
            @click="create.open = false"
          >
            Cancel
          </Button>
          <Button
            size="sm"
            :disabled="creating || !create.name.trim()"
            :loading="creating"
            @click="saveCreate"
          >
            Create
          </Button>
        </div>
      </template>
    </Drawer>

    <!-- Test drawer. The server signs with the secret it already holds and
         returns the headers; the browser then POSTs to the real /webhook/{id}
         path, so the test exercises the same verification a provider hits.
         This used to ask the operator to paste the secret back in and sign in
         the browser, which could only produce 2 of the 5 formats. -->
    <Drawer
      v-model="test.open"
      :title="`Test ${test.row?.name || 'trigger'}`"
      width="640px"
    >
      <div
        v-if="test.row"
        class="p-5 space-y-5 text-sm"
      >
        <p class="text-xs text-foreground-muted">
          Signed as <span class="font-mono text-foreground">{{ test.row.signature_format }}</span>
          and delivered to the live trigger URL.
        </p>
        <div>
          <label
            for="inbound-test-body"
            class="text-xs uppercase tracking-wider text-foreground-muted"
          >Body (raw)</label>
          <textarea
            id="inbound-test-body"
            v-model="test.body"
            rows="6"
            spellcheck="false"
            class="mt-2 w-full bg-surface border border-border rounded p-3 text-xs text-white font-mono focus:outline-none focus:border-white"
          />
        </div>
        <div
          v-if="test.error"
          class="text-xs text-danger-fg"
        >
          {{ test.error }}
        </div>
        <div
          v-if="test.response"
          class="space-y-1"
        >
          <span class="text-xs uppercase tracking-wider text-foreground-muted">Response (HTTP {{ test.response.status }})</span>
          <pre class="bg-background border border-border rounded p-3 text-[11px] font-mono text-white whitespace-pre-wrap overflow-x-auto">{{ test.response.body }}</pre>
        </div>
      </div>
      <template #footer>
        <div class="flex items-center justify-end gap-2">
          <Button
            variant="ghost"
            size="sm"
            @click="test.open = false"
          >
            Close
          </Button>
          <Button
            size="sm"
            :disabled="testing"
            :loading="testing"
            @click="runTest"
          >
            Send test
          </Button>
        </div>
      </template>
    </Drawer>
  </div>
</template>

<script setup>
import { EMPTY } from '@/utils/format'
import { ref, reactive, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { Plus, Trash2, Send, RefreshCw, Pause, Play } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import IconButton from '@/components/common/IconButton.vue'
import Drawer from '@/components/common/Drawer.vue'
import { listInboundWebhooks, createInboundWebhook, deleteInboundWebhook, updateInboundWebhook, signInboundWebhook } from '@/api/endpoints'
import { useConfirmStore } from '@/stores/confirm'
import { copyText } from '@/utils/clipboard'

const route = useRoute()
const confirmStore = useConfirmStore()

const fnName = computed(() => route.params.name)
const fnId = ref('')
const rows = ref([])
const loading = ref(false)
const creating = ref(false)
const testing = ref(false)
const lastCreated = ref(null)
const origin = computed(() => window.location.origin)

const create = reactive({
  open: false,
  name: '',
  format: 'hmac_sha256_hex',
  error: '',
})

const test = reactive({
  open: false,
  row: null,
  secret: '',
  body: '{"hello":"orva"}',
  error: '',
  response: null,
})

const formatDate = (s) => {
  if (!s) return EMPTY
  return new Date(s).toLocaleString()
}

const refresh = async () => {
  loading.value = true
  try {
    // The inbound-webhook sub-resource endpoints (list/create/delete) resolve
    // names to ids server-side, so the URL slug works directly for every call.
    // We still do not fetch the canonical id first -- it would be a wasted
    // round trip. (The note that used to be here said GET /functions/{id}
    // accepts only a UUID and would 404 on a name; that was true, and is the
    // bug that also made older functions unopenable from the dashboard. It
    // now resolves names like every other function endpoint.)
    if (!fnId.value) fnId.value = fnName.value
    const res = await listInboundWebhooks(fnId.value)
    rows.value = res.data?.inbound_webhooks || []
  } catch (e) {
    console.error('load inbound webhooks failed', e)
    confirmStore.notify({
      title: 'Failed to load inbound webhooks',
      message: e?.response?.data?.error?.message || e.message,
      danger: true,
    })
  } finally {
    loading.value = false
  }
}

const openCreate = () => {
  create.name = ''
  create.format = 'hmac_sha256_hex'
  create.error = ''
  create.open = true
}

const saveCreate = async () => {
  const name = create.name.trim()
  if (!name) {
    create.error = 'Name is required'
    return
  }
  creating.value = true
  create.error = ''
  try {
    const res = await createInboundWebhook(fnId.value, {
      name,
      signature_format: create.format,
    })
    lastCreated.value = {
      ...res.data.inbound_webhook,
      secret: res.data.secret,
      trigger_url: res.data.trigger_url,
    }
    create.open = false
    await refresh()
  } catch (e) {
    create.error = e?.response?.data?.error?.message || 'Create failed'
  } finally {
    creating.value = false
  }
}

const confirmRemove = async (row) => {
  const ok = await confirmStore.ask({
    title: 'Delete inbound webhook?',
    message: `Trigger "${row.name}" (${row.id}) will stop accepting calls immediately. This cannot be undone.`,
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  try {
    await deleteInboundWebhook(fnId.value, row.id)
    await refresh()
  } catch (e) {
    confirmStore.notify({
      title: 'Delete failed',
      message: e?.response?.data?.error?.message || e.message,
      danger: true,
    })
  }
}

const busyId = ref('')

// The badge rendered active/paused from the start, but there was no way to
// change it: updateInboundWebhook was exported from the API module and
// imported by nothing, though the server has always supported the PUT. The
// only way to stop a trigger was to delete it, which destroys its secret and
// its URL -- so pausing a noisy integration for ten minutes meant re-issuing
// credentials to whoever was calling it.
const toggleActive = async (row) => {
  busyId.value = row.id
  try {
    await updateInboundWebhook(fnId.value, row.id, { active: !row.active })
    await refresh()
  } catch (e) {
    confirmStore.notify({
      title: row.active ? 'Failed to pause trigger' : 'Failed to resume trigger',
      message: e?.response?.data?.error?.message || 'Unknown error',
      danger: true,
    })
  } finally {
    busyId.value = ''
  }
}

const openTest = (row) => {
  test.row = row
  test.body = '{"hello":"orva"}'
  test.error = ''
  test.response = null
  test.open = true
}

const runTest = async () => {
  test.error = ''
  test.response = null
  testing.value = true
  try {
    // The server signs with the secret it already holds, and knows every
    // format -- including the timestamped ones (stripe, slack) that need a
    // second header. The browser then delivers to the real trigger URL, so a
    // pass here means a provider's request would pass too.
    const signed = await signInboundWebhook(fnName.value, test.row.id, test.body)
    const url = origin.value + '/webhook/' + test.row.id
    const resp = await fetch(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...signed.data.headers,
      },
      body: test.body,
    })
    const text = await resp.text()
    test.response = { status: resp.status, body: text }
  } catch (e) {
    test.error = e.message || 'Test failed'
  } finally {
    testing.value = false
  }
}

const sampleCurl = (row) => {
  const url = origin.value + row.trigger_url
  const fmt = row.signature_format
  if (fmt === 'github') {
    return [
      `BODY='{"hello":"orva"}'`,
      `SIG=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "${row.secret}" | sed 's/^.* //')`,
      `curl -X POST "${url}" \\`,
      `  -H "Content-Type: application/json" \\`,
      `  -H "${row.signature_header}: sha256=$SIG" \\`,
      `  -d "$BODY"`,
    ].join('\n')
  }
  if (fmt === 'stripe') {
    return [
      `BODY='{"hello":"orva"}'`,
      `TS=$(date +%s)`,
      `SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "${row.secret}" | sed 's/^.* //')`,
      `curl -X POST "${url}" \\`,
      `  -H "Content-Type: application/json" \\`,
      `  -H "${row.signature_header}: t=$TS,v1=$SIG" \\`,
      `  -d "$BODY"`,
    ].join('\n')
  }
  if (fmt === 'slack') {
    return [
      `BODY='{"hello":"orva"}'`,
      `TS=$(date +%s)`,
      `SIG=$(printf 'v0:%s:%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "${row.secret}" | sed 's/^.* //')`,
      `curl -X POST "${url}" \\`,
      `  -H "Content-Type: application/json" \\`,
      `  -H "X-Slack-Request-Timestamp: $TS" \\`,
      `  -H "${row.signature_header}: v0=$SIG" \\`,
      `  -d "$BODY"`,
    ].join('\n')
  }
  // hmac_sha256_hex / hmac_sha256_base64 default.
  const dgst = fmt === 'hmac_sha256_base64'
    ? `openssl dgst -sha256 -hmac "${row.secret}" -binary | base64`
    : `openssl dgst -sha256 -hmac "${row.secret}" | sed 's/^.* //'`
  return [
    `BODY='{"hello":"orva"}'`,
    `SIG=$(printf '%s' "$BODY" | ${dgst})`,
    `curl -X POST "${url}" \\`,
    `  -H "Content-Type: application/json" \\`,
    `  -H "${row.signature_header}: $SIG" \\`,
    `  -d "$BODY"`,
  ].join('\n')
}

// navigator.clipboard is undefined outside a secure context, and Orva is
// routinely reached over plain HTTP at a LAN address. This used to call it
// directly and swallow the throw, so "Copy secret" did nothing and said
// nothing -- and the plaintext secret is shown exactly once, so the operator
// lost their only copy and had to delete and re-create the trigger.
const copy = async (text) => {
  if (await copyText(text)) {
    confirmStore.notify({ title: 'Copied', message: '', danger: false })
    return
  }
  confirmStore.notify({
    title: 'Copy failed',
    message: 'Could not copy to clipboard. Select the value manually:\n\n' + text,
  })
}

onMounted(refresh)
</script>
