<template>
  <div class="space-y-6">
    <!-- Header -->
    <div class="flex items-start justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-white tracking-tight">
          Inbound webhooks
        </h1>
        <p class="text-xs text-foreground-muted mt-1">
          External services POST to a signed URL to fire
          <router-link
            :to="`/functions/${fnName}`"
            class="text-white underline"
          >
            {{ fnName }}
          </router-link>
          — set the secret here, configure it on the upstream service.
        </p>
      </div>
      <div class="flex items-center gap-2">
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
      class="rounded-lg border border-amber-500/40 bg-amber-500/10 p-4 space-y-3"
    >
      <div class="flex items-center justify-between gap-4">
        <div class="text-sm text-amber-200 font-medium">
          Trigger created — copy the secret now. It will not be shown again.
        </div>
        <button
          class="text-xs text-amber-200/80 hover:text-white"
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
            class="ml-2 text-amber-200 hover:text-white"
            @click="copy(origin + lastCreated.trigger_url)"
          >
            Copy URL
          </button>
        </div>
        <div>
          <span class="text-foreground-muted uppercase tracking-wider text-[10px]">Secret</span>
          <code class="ml-2 font-mono text-white break-all">{{ lastCreated.secret }}</code>
          <button
            class="ml-2 text-amber-200 hover:text-white"
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
      <table class="w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th class="px-4 py-3">Name</th>
            <th class="px-4 py-3 hidden md:table-cell">URL</th>
            <th class="px-4 py-3 hidden sm:table-cell">Format</th>
            <th class="px-4 py-3 hidden lg:table-cell">Secret</th>
            <th class="px-4 py-3 hidden md:table-cell">Active</th>
            <th class="px-4 py-3 hidden lg:table-cell">Created</th>
            <th class="px-4 py-3 text-right">Actions</th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr
            v-for="row in rows"
            :key="row.id"
            class="hover:bg-surface/50 transition-colors"
          >
            <td class="px-4 py-3 font-medium text-white">
              <div class="flex flex-col">
                <span>{{ row.name }}</span>
                <span class="text-[10px] text-foreground-muted font-mono">{{ row.id }}</span>
              </div>
            </td>
            <td class="px-4 py-3 font-mono text-xs text-foreground-muted hidden md:table-cell">
              <span class="break-all">{{ origin }}/webhook/{{ row.id }}</span>
            </td>
            <td class="px-4 py-3 hidden sm:table-cell">
              <span
                class="inline-flex items-center px-2 py-0.5 rounded text-[11px] border bg-surface text-foreground-muted border-border font-mono"
              >
                {{ row.signature_format }}
              </span>
            </td>
            <td class="px-4 py-3 font-mono text-xs text-foreground-muted hidden lg:table-cell">
              {{ row.secret_preview }}
            </td>
            <td class="px-4 py-3 hidden md:table-cell">
              <span
                class="inline-flex items-center px-2 py-0.5 rounded text-[11px] border"
                :class="row.active
                  ? 'bg-success/10 text-success border-success/30'
                  : 'bg-surface text-foreground-muted border-border'"
              >
                {{ row.active ? 'active' : 'paused' }}
              </span>
            </td>
            <td class="px-4 py-3 text-foreground-muted text-xs hidden lg:table-cell">
              {{ formatDate(row.created_at) }}
            </td>
            <td class="px-4 py-3 text-right">
              <div class="inline-flex items-center gap-1">
                <IconButton
                  :icon="Send"
                  variant="success"
                  title="Send a test payload"
                  @click="openTest(row)"
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
              class="px-4 py-12 text-center text-foreground-muted text-sm"
            >
              No inbound triggers yet. Click <strong>+ New trigger</strong> to mint one.
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Create drawer -->
    <Drawer
      v-model="create.open"
      title="New inbound webhook trigger"
      width="560px"
    >
      <div class="p-5 space-y-5 text-sm">
        <div>
          <label class="text-xs uppercase tracking-wider text-foreground-muted">Name</label>
          <input
            v-model="create.name"
            placeholder="e.g. github-deploys"
            class="mt-2 w-full bg-surface border border-border rounded px-3 py-2 text-sm text-white focus:outline-none focus:border-white"
          >
        </div>
        <div>
          <label class="text-xs uppercase tracking-wider text-foreground-muted">Signature format</label>
          <select
            v-model="create.format"
            class="mt-2 w-full bg-surface border border-border rounded px-3 py-2 text-sm text-white focus:outline-none focus:border-white"
          >
            <option value="hmac_sha256_hex">hmac_sha256_hex (default)</option>
            <option value="hmac_sha256_base64">hmac_sha256_base64</option>
            <option value="github">github (X-Hub-Signature-256)</option>
            <option value="stripe">stripe (Stripe-Signature)</option>
            <option value="slack">slack (X-Slack-Signature)</option>
          </select>
          <p class="text-[11px] text-foreground-muted mt-2">
            Pick the format your upstream service produces. The header name is
            stamped automatically; you can override on the row after creation.
          </p>
        </div>
        <div
          v-if="create.error"
          class="text-xs text-error"
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

    <!-- Test drawer — operator pastes the secret they captured at create
         time. Browser computes the HMAC and POSTs to the trigger URL. -->
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
          Paste the plaintext secret you captured when you created this trigger.
          Orva does not store the plaintext — it can only show the preview
          ({{ test.row.secret_preview }}).
        </p>
        <div>
          <label class="text-xs uppercase tracking-wider text-foreground-muted">Secret</label>
          <input
            v-model="test.secret"
            type="password"
            placeholder="paste 64-hex secret"
            spellcheck="false"
            class="mt-2 w-full bg-surface border border-border rounded px-3 py-2 text-sm text-white font-mono focus:outline-none focus:border-white"
          >
        </div>
        <div>
          <label class="text-xs uppercase tracking-wider text-foreground-muted">Body (raw)</label>
          <textarea
            v-model="test.body"
            rows="6"
            spellcheck="false"
            class="mt-2 w-full bg-surface border border-border rounded p-3 text-xs text-white font-mono focus:outline-none focus:border-white"
          />
        </div>
        <div
          v-if="test.error"
          class="text-xs text-error"
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
            :disabled="testing || !test.secret.trim()"
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
import { ref, reactive, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { Plus, Trash2, Send, RefreshCw } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import IconButton from '@/components/common/IconButton.vue'
import Drawer from '@/components/common/Drawer.vue'
import { getFunction, listInboundWebhooks, createInboundWebhook, deleteInboundWebhook } from '@/api/endpoints'
import { useConfirmStore } from '@/stores/confirm'

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
  if (!s) return '—'
  return new Date(s).toLocaleString()
}

const refresh = async () => {
  loading.value = true
  try {
    // The backend resolves names → ids on /functions/{id_or_name}/inbound-webhooks,
    // so we hit it directly with the URL slug. Best-effort getFunction call still
    // populates fnId for downstream callers that prefer the canonical id, but a
    // failure there must not block list/CRUD — the slug works either way.
    if (!fnId.value) {
      try {
        const fn = await getFunction(fnName.value)
        fnId.value = fn.data.id || fn.data.function?.id || fnName.value
      } catch {
        fnId.value = fnName.value
      }
    }
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

const openTest = (row) => {
  test.row = row
  test.secret = ''
  test.body = '{"hello":"orva"}'
  test.error = ''
  test.response = null
  test.open = true
}

// signBodyHMACHex computes HMAC-SHA256 of body with the given secret in
// hex. Used for hmac_sha256_hex (raw) and github (sha256= prefix).
const signBodyHMACHex = async (secret, body) => {
  const enc = new TextEncoder()
  const key = await crypto.subtle.importKey(
    'raw', enc.encode(secret),
    { name: 'HMAC', hash: 'SHA-256' },
    false, ['sign'],
  )
  const sig = await crypto.subtle.sign('HMAC', key, enc.encode(body))
  return [...new Uint8Array(sig)].map((b) => b.toString(16).padStart(2, '0')).join('')
}

const runTest = async () => {
  test.error = ''
  test.response = null
  testing.value = true
  try {
    const fmt = test.row.signature_format
    let headerValue
    if (fmt === 'hmac_sha256_hex') {
      headerValue = await signBodyHMACHex(test.secret.trim(), test.body)
    } else if (fmt === 'github') {
      headerValue = 'sha256=' + (await signBodyHMACHex(test.secret.trim(), test.body))
    } else {
      test.error = `Browser test only signs hmac_sha256_hex and github. ` +
        `For ${fmt}, use the CLI or curl with openssl.`
      return
    }
    const url = origin.value + '/webhook/' + test.row.id
    const resp = await fetch(url, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        [test.row.signature_header]: headerValue,
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

const copy = async (text) => {
  try {
    await navigator.clipboard.writeText(text)
    confirmStore.notify({ title: 'Copied', message: '', danger: false })
  } catch (e) {
    console.warn('clipboard write failed', e)
  }
}

onMounted(refresh)
</script>
