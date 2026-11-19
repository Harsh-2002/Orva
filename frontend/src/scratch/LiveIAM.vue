<template>
  <main class="mx-auto max-w-5xl p-4 sm:p-8 space-y-6">
    <header class="space-y-3 border-b border-border pb-5">
      <p class="text-sm text-warning-fg">
        Live scratch IAM · KV, buffered calls and streams · not production-ready
      </p>
      <p class="text-sm text-foreground-muted">
        Public invoke handler and real warm pool. Not production serving activation.
      </p>
      <SegmentedControl
        v-model="owner"
        aria-label="Scratch function"
        :options="options"
      />
      <SegmentedControl
        v-model="operation"
        aria-label="Scratch operation"
        :options="operationOptions"
      />
      <div
        v-if="operation !== 'kv'"
        class="max-w-md space-y-1.5"
      >
        <label
          for="scratch-call-target"
          class="text-xs font-medium text-foreground-muted"
        >{{ operation === 'stream' ? 'Stream source' : 'Call target' }}</label>
        <FilterSelect
          v-model="target"
          trigger-id="scratch-call-target"
          :label="operation === 'stream' ? 'Choose stream source' : 'Choose call target'"
          :options="targetOptions"
          :disabled="invoking || stream.active.value || !targetOptions.length"
          wide
        />
      </div>
      <SegmentedControl
        v-if="operation === 'stream'"
        v-model="length"
        aria-label="Stream length"
        size="xs"
        :options="lengthOptions"
      />
      <p
        v-if="operation === 'stream'"
        class="max-w-prose text-xs text-foreground-muted"
      >
        The selected function reads the source function's stream through the SDK. Removing the call
        permission here, or the accepted caller on the source, cuts a running stream.
      </p>
      <div class="flex flex-wrap items-center gap-3">
        <Button
          v-if="operation === 'stream'"
          :disabled="!owner || stream.active.value || !selectedTarget"
          @click="startStream"
        >
          Start stream
        </Button>
        <Button
          v-else
          :loading="invoking"
          :disabled="!owner || invoking || (operation === 'call' && !selectedTarget)"
          @click="invoke"
        >
          {{ operation === 'call' ? 'Invoke function call' : 'Invoke KV demo' }}
        </Button>
        <p
          v-if="result && operation !== 'stream'"
          role="status"
          class="text-sm"
          data-testid="invocation-result"
        >
          {{ result.response.decision }}
        </p>
        <p
          v-if="error"
          role="alert"
          class="text-sm text-danger-fg"
        >
          {{ error }}
        </p>
      </div>
      <p
        v-if="result && operation !== 'stream'"
        class="text-xs text-foreground-muted break-all"
      >
        Worker {{ result.worker }} · Execution {{ result.execution }} · Artifact {{ result.artifact }}
      </p>
      <p
        v-if="result?.response.callee && operation !== 'stream'"
        class="text-xs text-foreground-muted break-all"
        data-testid="callee-result"
      >
        Callee {{ result.response.callee.context.functionID }} · Execution {{ result.response.callee.context.execution }}
        · Trace {{ result.response.callee.context.trace }} · Depth {{ result.response.callee.context.depth }}
      </p>
      <section
        v-if="stream.state.phase !== 'idle'"
        class="space-y-3 rounded-lg border border-border bg-surface p-4"
        aria-labelledby="scratch-stream-title"
        data-testid="stream-panel"
      >
        <div class="flex flex-wrap items-center justify-between gap-2">
          <div class="min-w-0">
            <h2
              id="scratch-stream-title"
              class="text-sm font-semibold text-foreground-strong"
            >
              Stream
            </h2>
            <p class="break-all text-xs text-foreground-muted">
              {{ stream.state.callerLabel }} reading {{ stream.state.targetLabel }}
            </p>
          </div>
          <div class="flex items-center gap-2">
            <Badge
              size="sm"
              :variant="phaseBadge.variant"
              data-testid="stream-phase"
            >
              {{ phaseBadge.text }}
            </Badge>
            <Button
              v-if="stream.active.value"
              variant="secondary"
              size="sm"
              @click="stream.stop"
            >
              Stop
            </Button>
            <Button
              v-else
              variant="ghost"
              size="sm"
              @click="stream.dismiss"
            >
              Clear
            </Button>
          </div>
        </div>
        <p
          class="font-mono text-xs text-foreground-strong"
          data-testid="stream-count"
        >
          {{ stream.state.chunks }} of {{ stream.state.expected }} chunks received
        </p>
        <ol
          v-if="stream.state.recent.length"
          class="space-y-0.5 font-mono text-xs text-foreground"
          aria-label="Latest chunks"
        >
          <li
            v-for="line in stream.state.recent"
            :key="line"
          >
            {{ line }}
          </li>
        </ol>
        <p
          v-if="phaseNote"
          class="max-w-prose text-xs text-foreground-muted"
          data-testid="stream-note"
        >
          {{ phaseNote }}
        </p>
        <p
          v-if="stream.state.execution"
          class="break-all text-xs text-foreground-muted"
        >
          Worker {{ stream.state.worker }} · Execution {{ stream.state.execution }} · Artifact {{ stream.state.artifact }}
        </p>
        <p
          v-if="stream.state.callee"
          class="break-all text-xs text-foreground-muted"
          data-testid="stream-callee"
        >
          Source {{ stream.state.callee.functionID }} · Execution {{ stream.state.callee.execution }}
          · Trace {{ stream.state.callee.trace }} · Depth {{ stream.state.callee.depth }}
        </p>
      </section>
    </header>
    <FunctionPermissions
      v-if="owner"
      :function-id="owner"
      v-bind="pageState"
      :preview="false"
      @retry="controller.refresh"
      @save="controller.save"
      @review="controller.review"
    />
    <p
      v-else
      role="status"
      class="text-sm text-foreground-muted"
    >
      {{ error || 'Loading scratch functions…' }}
    </p>
  </main>
</template>

<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Badge from '@/components/common/Badge.vue'
import Button from '@/components/common/Button.vue'
import SegmentedControl from '@/components/common/SegmentedControl.vue'
import FilterSelect from '@/components/common/FilterSelect.vue'
import FunctionPermissions from '@/views/FunctionPermissions.vue'
import { permissionsAPI } from '@/api/permissions.js'
import { usePermissions } from '@/composables/usePermissions.js'
import { useScratchStream } from '@/scratch/useScratchStream.js'

const owner = ref('')
const operation = ref('kv')
const target = ref('')
const length = ref('short')
const operationOptions = [
  { value: 'kv', label: 'KV demo' },
  { value: 'call', label: 'Buffered call' },
  { value: 'stream', label: 'Stream' },
]
const lengthOptions = [
  { value: 'short', label: 'Short' },
  { value: 'long', label: 'Long, about 12 s' },
]
const functions = ref([])
const options = computed(() => functions.value.map((f) => ({ value: f.id, label: f.runtime === 'node' ? 'Node.js' : 'Python' })))
const result = ref(null)
const error = ref('')
const invoking = ref(false)
const lifetime = new AbortController()
const stream = useScratchStream()
let invokeToken = 0
let invokeAbort
async function request(path, { method = 'GET', body, signal } = {}) {
  const response = await fetch(path, {
    method, signal, headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (!response.ok) throw Object.assign(new Error('Scratch request failed'), { status: response.status })
  const value = await response.json()
  if (path.startsWith('/fn/')) return {
    response: value,
    execution: response.headers.get('X-Orva-Execution-ID'),
    worker: response.headers.get('X-Scratch-Worker'),
    artifact: response.headers.get('X-Scratch-Artifact'),
  }
  return value
}
const controller = usePermissions(() => owner.value, permissionsAPI(request))
const targetOptions = computed(() => {
  const snapshot = controller.snapshot.value
  if (snapshot?.function.id !== owner.value) return []
  return snapshot.targets.filter(t => t.id !== owner.value && t.state === 'resolved' && t.label)
    .map(t => ({ value: t.id, label: t.label }))
})
const selectedTarget = computed(() => targetOptions.value.find(t => t.value === target.value))
const pageState = computed(() => Object.fromEntries(
  ['snapshot', 'loading', 'loadError', 'saving', 'saveError'].map((key) => [key, controller[key].value]),
))
const BADGES = {
  connecting: { variant: 'info', text: 'Connecting' },
  streaming: { variant: 'success', text: 'Streaming' },
  completed: { variant: 'success', text: 'Completed' },
  denied: { variant: 'warning', text: 'Denied' },
  stopped: { variant: 'gray', text: 'Stopped' },
  failed: { variant: 'error', text: 'Failed' },
}
const phaseBadge = computed(() => BADGES[stream.state.phase] || BADGES.failed)
const phaseNote = computed(() => {
  const n = stream.state.chunks
  const kept = n === 1 ? '1 chunk was' : `${n} chunks were`
  switch (stream.state.phase) {
    case 'denied': return `A permission was removed or never granted. ${n ? `${kept} delivered earlier and ${n === 1 ? 'stays' : 'stay'} listed.` : 'No data was delivered.'} The stream sent nothing after the denial.`
    case 'stopped': return `You stopped the stream. ${n ? `${kept} received before that.` : 'Nothing had arrived yet.'}`
    case 'failed': return 'The stream ended without a completion marker.'
    default: return ''
  }
})
// A running stream belongs to the function that started it, so changing the
// shown function or operation neither cancels nor relabels it. Stop does.
watch([owner, operation, target], () => { invokeToken += 1; invokeAbort?.abort(); invoking.value = false; result.value = null; error.value = '' })
watch(owner, () => { target.value = '' })
onMounted(async () => {
  try {
    functions.value = (await request('/api/scratch/functions', { signal: lifetime.signal })).sort((a, b) => a.runtime.localeCompare(b.runtime))
    owner.value = functions.value[0]?.id || ''
  } catch {
    if (!lifetime.signal.aborted) error.value = 'Scratch authentication or connection failed.'
  }
})
onBeforeUnmount(() => { lifetime.abort(); invokeAbort?.abort(); invokeToken += 1; stream.dispose() })
function startStream() {
  const id = owner.value
  const callerLabel = functions.value.find(f => f.id === id)?.name || id
  if (!selectedTarget.value) return
  stream.start({ callerId: id, callerLabel, targetLabel: selectedTarget.value.label, length: length.value })
}
async function invoke() {
  const id = owner.value
  const token = ++invokeToken
  invokeAbort = new AbortController()
  invoking.value = true
  error.value = ''
  try {
    if (operation.value !== 'kv' && (operation.value !== 'call' || !selectedTarget.value)) return
    const body = operation.value === 'call'
      ? { mode: 'call', target: selectedTarget.value.label, marker: 'calls-workflow' }
      : { marker: 'kv-workflow' }
    const value = await request(`/fn/${id}/demo`, { method: 'POST', body, signal: invokeAbort.signal })
    if (token === invokeToken && id === owner.value) result.value = value
  } catch {
    if (token === invokeToken) error.value = 'Scratch invocation failed. Retry.'
  } finally {
    if (token === invokeToken) invoking.value = false
  }
}
</script>
