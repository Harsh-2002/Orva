<template>
  <div class="space-y-6">
    <!-- Page header -->
    <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div class="min-w-0">
        <h1 class="text-xl font-semibold text-foreground-strong tracking-tight">
          Test
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
          Send a request to
          <router-link
            :to="`/functions/${fnName}`"
            class="text-foreground-strong underline"
          >
            {{ fnName }}
          </router-link>
          and read what the handler wrote while it ran.
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2 sm:shrink-0">
        <Button
          variant="secondary"
          @click="$router.push(`/functions/${fnName}`)"
        >
          <Code2 class="w-4 h-4" />
          Editor
        </Button>
        <Button
          :disabled="!canRun"
          :loading="invoking"
          :title="runTitle"
          @click="run"
        >
          <Play class="w-4 h-4" />
          Run
        </Button>
      </div>
    </div>

    <LoadError
      v-if="loadError"
      what="This function"
      :message="loadError"
      :on-retry="load"
    />

    <p
      v-else-if="loading"
      class="text-sm text-foreground-muted"
    >
      Loading {{ fnName }}.
    </p>

    <div
      v-else-if="!fn"
      class="bg-surface border border-border rounded-lg px-6 py-10 text-center"
    >
      <h2 class="text-sm font-semibold text-foreground-strong">
        No function named {{ fnName }}
      </h2>
      <p class="text-sm text-foreground-muted mt-1.5">
        It was renamed or deleted while this page was open.
      </p>
      <router-link
        to="/functions"
        class="text-link text-sm underline underline-offset-2 inline-flex items-center touch-expand-sm mt-3"
      >
        All functions
      </router-link>
    </div>

    <template v-else>
      <!-- Invoke URL, built from window.location.origin so it stays correct on
           a LAN IP or behind a TLS-terminating proxy. -->
      <div class="border-y border-border py-3 flex flex-col gap-1.5 sm:flex-row sm:items-center sm:gap-3">
        <span class="text-[10px] uppercase tracking-wider text-foreground-muted shrink-0">
          Invoke URL
        </span>
        <code class="font-mono text-xs text-foreground-strong break-all min-w-0 flex-1">{{ invokeUrl }}</code>
        <button
          type="button"
          class="touch-expand-xs inline-flex items-center gap-1.5 self-start h-7 rounded-md px-2 text-xs text-foreground-muted hover:text-foreground-strong transition-colors shrink-0"
          @click="copyUrl"
        >
          <Copy class="w-3 h-3" />
          {{ urlCopied ? 'Copied' : 'Copy' }}
        </button>
      </div>

      <div
        v-if="!isDeployed"
        role="status"
        class="flex items-start gap-3 rounded-md border border-warning-ring bg-warning-tint px-4 py-3 text-xs"
      >
        <TriangleAlert class="w-4 h-4 shrink-0 mt-0.5 text-warning-fg" />
        <p class="text-warning-fg leading-snug">
          {{ fnName }} has no deployed version, so there is nothing to invoke yet. Deploy it from the
          <router-link
            :to="`/functions/${fnName}`"
            class="underline underline-offset-2 hover:text-foreground-strong"
          >
            editor
          </router-link>
          and this page starts working. You can still set the request up here.
        </p>
      </div>

      <div class="grid grid-cols-1 gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.15fr)] lg:items-start">
        <!-- Request column -->
        <div class="space-y-6 min-w-0">
          <section class="bg-surface border border-border rounded-lg">
            <header class="px-4 py-3 border-b border-border flex items-center justify-between gap-3">
              <h2 class="text-sm font-semibold text-foreground-strong">
                Request
              </h2>
              <span class="hidden sm:inline text-[11px] text-foreground-muted">
                Ctrl or Cmd + Enter runs it
              </span>
            </header>

            <div class="px-4 py-3 flex items-center gap-2 border-b border-border">
              <label
                for="tw-method"
                class="sr-only"
              >Request method</label>
              <FilterSelect
                v-model="req.method"
                :options="methodOptions"
                label="Method"
                trigger-id="tw-method"
                bare
              />
              <label
                for="tw-path"
                class="sr-only"
              >Request path</label>
              <input
                id="tw-path"
                v-model="req.path"
                spellcheck="false"
                placeholder="/"
                class="h-7 flex-1 min-w-0 rounded-md border border-border bg-background px-2.5 font-mono text-xs text-foreground placeholder-foreground-muted focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
              >
            </div>

            <div class="px-4 py-3 border-b border-border space-y-2">
              <div class="flex items-center justify-between gap-3">
                <h3 class="text-[11px] font-semibold uppercase tracking-wider text-foreground-muted">
                  Headers
                </h3>
                <Button
                  variant="secondary"
                  size="xs"
                  @click="addHeader"
                >
                  <Plus class="w-3 h-3" />
                  Add header
                </Button>
              </div>

              <p
                v-if="!req.headers.length"
                class="text-xs text-foreground-muted leading-snug"
              >
                None set. A body sent with POST, PUT or PATCH gets Content-Type: application/json unless you name one here.
              </p>
              <div
                v-for="(header, idx) in req.headers"
                v-else
                :key="idx"
                class="flex items-center gap-2"
              >
                <label
                  :for="`tw-header-name-${idx}`"
                  class="sr-only"
                >Header {{ idx + 1 }} name</label>
                <input
                  :id="`tw-header-name-${idx}`"
                  v-model="header.key"
                  spellcheck="false"
                  placeholder="Header name"
                  class="h-8 flex-1 min-w-0 rounded-md border border-border bg-background px-2.5 font-mono text-xs text-foreground placeholder-foreground-muted focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
                >
                <label
                  :for="`tw-header-value-${idx}`"
                  class="sr-only"
                >Header {{ idx + 1 }} value</label>
                <input
                  :id="`tw-header-value-${idx}`"
                  v-model="header.value"
                  spellcheck="false"
                  placeholder="value"
                  class="h-8 flex-1 min-w-0 rounded-md border border-border bg-background px-2.5 font-mono text-xs text-foreground placeholder-foreground-muted focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
                >
                <IconButton
                  :icon="X"
                  :title="`Remove header ${header.key || idx + 1}`"
                  variant="danger"
                  size="md"
                  @click="removeHeader(idx)"
                />
              </div>
            </div>

            <div class="px-4 py-3 space-y-2">
              <div class="flex items-center justify-between gap-3">
                <h3 class="text-[11px] font-semibold uppercase tracking-wider text-foreground-muted">
                  Body
                </h3>
                <span class="font-mono text-[11px] text-foreground-muted tabular-nums">
                  {{ req.body.length }} chars
                </span>
              </div>
              <label
                for="tw-body"
                class="sr-only"
              >Request body</label>
              <textarea
                id="tw-body"
                v-model="req.body"
                spellcheck="false"
                placeholder="{}"
                class="w-full min-h-64 resize-y rounded-md border border-border bg-background p-3 font-mono text-xs text-foreground placeholder-foreground-muted focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
              />
            </div>
          </section>

          <!-- Fixtures get a panel rather than the popover they had: they are
               server-side records the MCP test_function_with_fixture tool reads. -->
          <section class="bg-surface border border-border rounded-lg">
            <header class="px-4 py-3 border-b border-border flex items-center justify-between gap-3">
              <h2 class="text-sm font-semibold text-foreground-strong">
                Saved requests
              </h2>
              <Button
                variant="secondary"
                size="xs"
                @click="saveCurrent"
              >
                <Save class="w-3 h-3" />
                Save current
              </Button>
            </header>

            <ul
              v-if="fixtures.length"
              class="divide-y divide-border"
            >
              <li
                v-for="fx in fixtures"
                :key="fx.id || fx.name"
                class="flex items-center gap-1 px-2 py-1"
              >
                <button
                  type="button"
                  class="touch-expand-row h-8 flex min-w-0 flex-1 items-center gap-2 rounded-md px-2 text-left hover:bg-surface-hover transition-colors"
                  :title="`Load ${fx.name} into the request`"
                  @click="applyFixture(fx)"
                >
                  <span class="font-mono text-[10px] text-foreground-muted shrink-0">{{ fx.method }}</span>
                  <span class="truncate text-xs text-foreground-strong">{{ fx.name }}</span>
                  <span class="truncate font-mono text-[10px] text-foreground-muted">{{ fx.path }}</span>
                </button>
                <IconButton
                  :icon="Trash2"
                  :title="`Delete ${fx.name}`"
                  variant="danger"
                  size="md"
                  @click="deleteFixture(fx)"
                />
              </li>
            </ul>
            <p
              v-else
              class="px-4 py-4 text-xs text-foreground-muted leading-snug"
            >
              Nothing saved yet. Set a request up above and choose Save current to replay it later, or to hand its name to the MCP test_function_with_fixture tool.
            </p>
          </section>
        </div>

        <!-- Result column -->
        <div class="space-y-6 min-w-0">
          <section class="bg-surface border border-border rounded-lg">
            <header class="px-4 py-3 border-b border-border flex flex-wrap items-center justify-between gap-x-3 gap-y-2">
              <div class="flex items-center gap-2 min-w-0">
                <h2 class="text-sm font-semibold text-foreground-strong">
                  Response
                </h2>
                <span
                  v-if="selectedRun && selectedIdx > 0"
                  class="text-[11px] text-foreground-muted"
                >
                  an earlier run, not the latest
                </span>
              </div>
              <div
                v-if="selectedRun"
                class="flex items-center gap-2 shrink-0"
              >
                <component
                  :is="statusIcon(selectedRun)"
                  class="w-3.5 h-3.5"
                  :class="statusTextClass(selectedRun)"
                  aria-hidden="true"
                />
                <Badge
                  :variant="statusVariant(selectedRun)"
                  size="sm"
                >
                  <span class="font-mono">{{ selectedRun.status || 'no status' }}</span>
                  <span class="ml-1.5">{{ statusWord(selectedRun) }}</span>
                </Badge>
                <span class="font-mono text-[11px] text-foreground-muted tabular-nums">
                  {{ durationLabel(selectedRun) }}
                </span>
              </div>
            </header>

            <!-- The outcome, not the body: a live region wrapping the response
                 would read a whole traceback aloud on every run. -->
            <p
              role="status"
              class="sr-only"
            >
              {{ liveStatus }}
            </p>

            <div class="px-4 py-3">
              <p
                v-if="!selectedRun"
                class="text-xs text-foreground-muted leading-snug"
              >
                No runs yet. Choose Run to invoke {{ fnName }} with the request on the left.
              </p>
              <template v-else>
                <pre
                  v-if="responseText"
                  class="max-h-[26rem] overflow-auto scrollable rounded-md border border-border bg-background p-3 font-mono text-xs whitespace-pre-wrap break-words"
                  :class="selectedRun.error ? 'text-danger-fg' : 'text-foreground'"
                >{{ responseText }}</pre>
                <p
                  v-else
                  class="text-xs text-foreground-muted"
                >
                  The handler answered with an empty body.
                </p>
              </template>
            </div>
          </section>

          <!-- Both log paths. console.log / print reach Orva as stderr because
               stdout carries the framed protocol response; orva.log.* is parsed
               out of that same stream into log_entries. -->
          <section class="bg-surface border border-border rounded-lg">
            <header class="px-4 py-3 border-b border-border flex items-center justify-between gap-3">
              <h2 class="text-sm font-semibold text-foreground-strong">
                Function logs
              </h2>
              <Button
                v-if="selectedRun && selectedRun.executionId"
                variant="secondary"
                size="xs"
                :loading="reloadingLogs"
                title="Fetch this run's logs again"
                @click="reloadLogs"
              >
                <RefreshCw class="w-3 h-3" />
                Reload
              </Button>
            </header>

            <div class="px-4 py-3 space-y-4">
              <LoadError
                v-if="logsError"
                what="Function logs"
                :message="logsError"
                :on-retry="reloadLogs"
              />

              <p
                v-if="!selectedRun"
                class="text-xs text-foreground-muted"
              >
                Run the function to see what it logged.
              </p>
              <p
                v-else-if="selectedRun.logsState === 'loading'"
                class="text-xs text-foreground-muted"
              >
                Reading the execution record.
              </p>
              <p
                v-else-if="!selectedRun.executionId"
                class="text-xs text-foreground-muted leading-snug"
              >
                This run got no execution id back, so there is no record to read logs from. The response panel above holds the transport error.
              </p>
              <template v-else-if="selectedRun.logsState !== 'loading'">
                <div>
                  <div class="flex flex-wrap items-baseline justify-between gap-x-3">
                    <h3 class="text-[11px] font-semibold uppercase tracking-wider text-foreground-muted">
                      Structured logs
                    </h3>
                    <span class="font-mono text-[10px] text-foreground-muted">orva.log.info(), orva.log.error()</span>
                  </div>
                  <div
                    v-if="selectedRun.structured.length"
                    class="mt-2 max-h-72 overflow-auto scrollable rounded-md border border-border bg-background divide-y divide-border"
                  >
                    <div
                      v-for="entry in selectedRun.structured"
                      :key="entry.id"
                      class="px-3 py-1.5 flex flex-wrap items-baseline gap-x-2 gap-y-0.5 font-mono text-[11px]"
                    >
                      <span class="text-foreground-muted tabular-nums shrink-0">{{ logTime(entry.ts) }}</span>
                      <span
                        class="text-[10px] uppercase shrink-0"
                        :class="levelClass(entry.level)"
                      >{{ entry.level }}</span>
                      <span class="min-w-0 text-foreground-strong break-words">{{ entry.message }}</span>
                      <code
                        v-if="entry.fields"
                        class="w-full min-w-0 text-[10px] text-foreground-muted break-all"
                      >{{ entry.fields }}</code>
                    </div>
                  </div>
                  <!-- Not v-else: a fetch that failed knows nothing about what
                       the handler logged, so it must not claim "none". -->
                  <p
                    v-else-if="!logsError"
                    class="mt-2 text-xs text-foreground-muted leading-snug"
                  >
                    None from this run. Call orva.log.info() in the handler and the level, message and fields land here.
                  </p>
                </div>

                <div>
                  <div class="flex flex-wrap items-baseline justify-between gap-x-3">
                    <h3 class="text-[11px] font-semibold uppercase tracking-wider text-foreground-muted">
                      Console output
                    </h3>
                    <span class="font-mono text-[10px] text-foreground-muted">console.log(), print()</span>
                  </div>
                  <pre
                    v-if="selectedRun.stderr.length"
                    class="mt-2 max-h-72 overflow-auto scrollable rounded-md border border-border bg-background p-3 font-mono text-[11px] text-foreground whitespace-pre-wrap break-words"
                  >{{ selectedRun.stderr.join('\n') }}</pre>
                  <p
                    v-else-if="!logsError"
                    class="mt-2 text-xs text-foreground-muted leading-snug"
                  >
                    None from this run. The runtime routes both onto stderr, because stdout carries the handler's response.
                  </p>
                </div>

                <p
                  v-if="!logsError && !selectedRun.structured.length && !selectedRun.stderr.length"
                  class="text-xs text-foreground-muted leading-snug"
                >
                  This run logged nothing. Add orva.log.info() or console.log() to the handler and run it again.
                </p>
              </template>
            </div>
          </section>

          <section class="bg-surface border border-border rounded-lg">
            <header class="px-4 py-3 border-b border-border flex items-center justify-between gap-3">
              <h2 class="text-sm font-semibold text-foreground-strong">
                Run history
              </h2>
              <Button
                v-if="selectedIdx > 0"
                variant="secondary"
                size="xs"
                @click="selectedIdx = 0"
              >
                Back to latest
              </Button>
            </header>

            <ul
              v-if="runs.length"
              class="divide-y divide-border"
            >
              <li
                v-for="(item, idx) in runs"
                :key="`${item.at}-${idx}`"
              >
                <button
                  type="button"
                  class="touch-expand-row w-full min-h-10 px-4 py-2 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-left transition-colors hover:bg-surface-hover"
                  :class="idx === selectedIdx ? 'bg-surface-hover' : ''"
                  :aria-current="idx === selectedIdx ? 'true' : undefined"
                  @click="selectedIdx = idx"
                >
                  <component
                    :is="statusIcon(item)"
                    class="w-3.5 h-3.5 shrink-0"
                    :class="statusTextClass(item)"
                    aria-hidden="true"
                  />
                  <span class="font-mono text-xs text-foreground-strong shrink-0">{{ item.status || 'no status' }}</span>
                  <span class="text-xs text-foreground-muted">{{ statusWord(item) }}</span>
                  <span class="font-mono text-[11px] text-foreground-muted tabular-nums ml-auto">{{ durationLabel(item) }}</span>
                  <span class="font-mono text-[11px] text-foreground-muted tabular-nums">{{ logTime(item.at) }}</span>
                </button>
              </li>
            </ul>
            <div
              v-else
              class="px-4 py-4 space-y-2"
            >
              <p class="text-xs text-foreground-muted leading-snug">
                No runs yet. History keeps the last 20 of this browser session, so you can hold one run against the one before it. It is not stored on the server.
              </p>
              <router-link
                to="/invocations"
                class="text-link text-xs underline underline-offset-2 inline-flex items-center touch-expand-sm"
              >
                Invocations has the permanent record
              </router-link>
            </div>
          </section>
        </div>
      </div>
    </template>
  </div>
</template>

<script setup>
import { computed, onActivated, onBeforeUnmount, onDeactivated, ref, watch } from 'vue'
import {
  CheckCircle2, Code2, Copy, Info, Play, Plus, RefreshCw, Save, Trash2,
  TriangleAlert, X, XCircle,
} from '@lucide/vue'
import Badge from '@/components/common/Badge.vue'
import Button from '@/components/common/Button.vue'
import FilterSelect from '@/components/common/FilterSelect.vue'
import IconButton from '@/components/common/IconButton.vue'
import LoadError from '@/components/common/LoadError.vue'
import { getInvocationLogs, listFunctions } from '@/api/endpoints'
import { useConfirmStore } from '@/stores/confirm'
import { METHODS, useTestbenchStore } from '@/stores/testbench'
import { copyText } from '@/utils/clipboard'

// The function this view is scoped to. A route prop, not useRoute(): Vue does
// not patch a keep-alived view's props, so a cached view stops following the URL.
const props = defineProps({ name: { type: String, default: '' } })
const fnName = computed(() => props.name)

const store = useTestbenchStore()
const confirmStore = useConfirmStore()

const methodOptions = METHODS.map((m) => ({ value: m, label: m }))

const fn = ref(null)
const loading = ref(false)
const loadError = ref('')
const selectedIdx = ref(0)
const logsError = ref('')
const reloadingLogs = ref(false)
const urlCopied = ref(false)

const fnId = computed(() => fn.value?.id || '')
const invoking = computed(() => store.invoking)

// A function row exists from the moment it is created; code only arrives with
// the first successful deploy, and there is nothing to invoke until it does.
const isDeployed = computed(() => !!(fn.value?.active_deployment_id || fn.value?.code_hash))
const canRun = computed(() => !!fnId.value && isDeployed.value && !invoking.value)
const runTitle = computed(() =>
  isDeployed.value
    ? 'Invoke with the request on the left'
    : `${fnName.value} has no deployed version to invoke`)

const blankRequest = { method: 'POST', path: '/', headers: [], body: '' }
const req = computed(() => store.requests[fnId.value] || blankRequest)
const fixtures = computed(() => store.fixturesFor(fnId.value))
const runs = computed(() => store.runsFor(fnId.value))
const selectedRun = computed(() => runs.value[selectedIdx.value] || null)
// fnClient sets no validateStatus, so axios rejects every 4xx/5xx; the store
// formats that body the same way it formats a success body.
const responseText = computed(() => selectedRun.value?.error || selectedRun.value?.body || '')

const invokeUrl = computed(() => {
  if (!fnId.value) return ''
  const path = req.value.path && req.value.path !== '/' ? req.value.path : ''
  const suffix = !path || path.startsWith('/') ? path : `/${path}`
  return `${window.location.origin}/fn/${fnId.value}${suffix}`
})

const messageOf = (e) => e?.response?.data?.error?.message || e?.message || 'Request failed'

const fetchFn = async () => {
  const res = await listFunctions()
  return (res.data?.functions || []).find((f) => f.name === props.name) || null
}

const load = async () => {
  loading.value = true
  loadError.value = ''
  logsError.value = ''
  fn.value = null
  if (!props.name) {
    loading.value = false
    return
  }
  try {
    fn.value = await fetchFn()
    if (fn.value) {
      store.requestFor(fn.value.id)
      await store.loadFixtures(fn.value.id)
    }
  } catch (e) {
    // A lookup that failed is not an unknown function. Saying "no such
    // function" here is the empty-state lie LoadError exists to prevent.
    loadError.value = messageOf(e)
  } finally {
    loading.value = false
  }
}

// Silent re-read on the way back in: a deploy can land from the editor while
// this view sits in keep-alive, and the banner would still say there is no code.
const refresh = async () => {
  if (!props.name || loadError.value) return
  try {
    fn.value = await fetchFn()
  } catch {
    // Keep showing what loaded; the operator did not ask for this fetch.
  }
}

watch(() => props.name, () => {
  selectedIdx.value = 0
  load()
}, { immediate: true })

watch(selectedIdx, () => { logsError.value = '' })

const run = async () => {
  if (!canRun.value) return
  logsError.value = ''
  await store.invoke(fnId.value)
  selectedIdx.value = 0
}

const addHeader = () => req.value.headers.push({ key: '', value: '' })
const removeHeader = (idx) => req.value.headers.splice(idx, 1)

const applyFixture = (fx) => store.applyFixture(fnId.value, fx)

const saveCurrent = async () => {
  if (!fnId.value) return
  const raw = await confirmStore.prompt({
    title: 'Save request',
    message: 'Name it so you can replay it here, or hand the name to the MCP test_function_with_fixture tool.',
    placeholder: 'e.g. happy-path, signed-stripe, empty-body',
    confirmLabel: 'Save',
  })
  const name = (raw || '').trim()
  if (!name) return
  try {
    await store.saveFixture(fnId.value, name)
  } catch (e) {
    confirmStore.notify({ title: 'Save failed', message: messageOf(e), danger: true })
  }
}

const deleteFixture = async (fx) => {
  const ok = await confirmStore.ask({
    title: `Delete "${fx.name}"?`,
    message: 'This removes the saved request only. Function code and execution history are untouched.',
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  try {
    await store.removeFixture(fnId.value, fx)
  } catch (e) {
    confirmStore.notify({ title: 'Delete failed', message: messageOf(e), danger: true })
  }
}

// The store fetches logs once, straight after the response returns, while the
// execution row is still being written asynchronously. This is the retry.
const reloadLogs = async () => {
  const target = selectedRun.value
  if (!target?.executionId || reloadingLogs.value) return
  reloadingLogs.value = true
  logsError.value = ''
  try {
    const { data } = await getInvocationLogs(target.executionId)
    const raw = data?.stderr || ''
    target.stderr = raw ? raw.split('\n').filter((l) => l !== '') : []
    target.structured = data?.log_entries || []
  } catch (e) {
    logsError.value = messageOf(e)
  } finally {
    reloadingLogs.value = false
  }
}

const copyUrl = async () => {
  if (!await copyText(invokeUrl.value)) {
    confirmStore.notify({ title: 'Copy failed', message: `Select the URL by hand:\n\n${invokeUrl.value}`, danger: true })
    return
  }
  urlCopied.value = true
  setTimeout(() => { urlCopied.value = false }, 1500)
}

// Colour never carries the outcome on its own: every status prints the code, a
// word and an icon, all derived from this one class.
const statusClass = (item) => {
  const code = Number(item?.status)
  if (!item?.status || !Number.isFinite(code)) return 'failed'
  if (code >= 500) return 'server'
  if (code >= 400) return 'client'
  if (code >= 300) return 'redirect'
  if (code >= 200) return 'ok'
  return 'info'
}
const STATUS_WORD = {
  ok: 'Success', redirect: 'Redirect', client: 'Client error',
  server: 'Server error', failed: 'Never answered', info: 'Informational',
}
const STATUS_VARIANT = {
  ok: 'success', redirect: 'info', client: 'warning',
  server: 'error', failed: 'error', info: 'info',
}
const STATUS_TEXT = {
  ok: 'text-success-fg', redirect: 'text-info-fg', client: 'text-warning-fg',
  server: 'text-danger-fg', failed: 'text-danger-fg', info: 'text-info-fg',
}
const STATUS_ICON = {
  ok: CheckCircle2, redirect: Info, client: TriangleAlert,
  server: XCircle, failed: XCircle, info: Info,
}
const statusWord = (item) => STATUS_WORD[statusClass(item)]
const statusVariant = (item) => STATUS_VARIANT[statusClass(item)]
const statusTextClass = (item) => STATUS_TEXT[statusClass(item)]
const statusIcon = (item) => STATUS_ICON[statusClass(item)]

const durationLabel = (item) => {
  const ms = item?.durationMs
  return ms && ms !== '?' ? `${ms} ms` : 'no timing'
}

const liveStatus = computed(() => {
  const item = selectedRun.value
  if (!item) return 'No runs yet.'
  return `Response ${item.status || 'with no status'}, ${statusWord(item)}, ${durationLabel(item)}.`
})

const logTime = (ts) => {
  if (!ts) return ''
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return String(ts)
  return `${d.toLocaleTimeString(undefined, { hour12: false })}.${String(d.getMilliseconds()).padStart(3, '0')}`
}

const levelClass = (level) => {
  switch (level) {
    case 'error': return 'text-danger-fg'
    case 'warn': return 'text-warning-fg'
    case 'debug': return 'text-foreground-muted'
    default: return 'text-info-fg'
  }
}

const onKey = (e) => {
  if ((e.metaKey || e.ctrlKey) && e.key === 'Enter') {
    e.preventDefault()
    run()
  }
}
let entered = false
const unbind = () => window.removeEventListener('keydown', onKey)
onActivated(() => {
  window.addEventListener('keydown', onKey)
  if (entered) refresh()
  entered = true
})
onDeactivated(unbind)
// onDeactivated does not fire when keep-alive evicts this instance at :max.
onBeforeUnmount(unbind)
</script>
