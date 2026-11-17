<template>
  <main class="mx-auto max-w-5xl p-4 sm:p-8 space-y-6">
    <header class="space-y-3 border-b border-border pb-5">
      <p class="text-sm text-warning-fg">
        Live scratch IAM · KV and buffered calls · not production-ready
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
        v-if="operation === 'call'"
        class="max-w-md space-y-1.5"
      >
        <label
          for="scratch-call-target"
          class="text-xs font-medium text-foreground-muted"
        >Call target</label>
        <FilterSelect
          v-model="target"
          trigger-id="scratch-call-target"
          label="Choose call target"
          :options="targetOptions"
          :disabled="invoking || !targetOptions.length"
          wide
        />
      </div>
      <p class="text-xs text-foreground-muted">
        Streams are disabled in this candidate. Two-sided buffered calls and KV only.
      </p>
      <div class="flex flex-wrap items-center gap-3">
        <Button
          :loading="invoking"
          :disabled="!owner || invoking || (operation === 'call' && !selectedTarget)"
          @click="invoke"
        >
          {{ operation === 'call' ? 'Invoke function call' : 'Invoke KV demo' }}
        </Button>
        <p
          v-if="result"
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
        v-if="result"
        class="text-xs text-foreground-muted break-all"
      >
        Worker {{ result.worker }} · Execution {{ result.execution }} · Artifact {{ result.artifact }}
      </p>
      <p
        v-if="result?.response.callee"
        class="text-xs text-foreground-muted break-all"
        data-testid="callee-result"
      >
        Callee {{ result.response.callee.context.functionID }} · Execution {{ result.response.callee.context.execution }}
        · Trace {{ result.response.callee.context.trace }} · Depth {{ result.response.callee.context.depth }}
      </p>
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
import Button from '@/components/common/Button.vue'
import SegmentedControl from '@/components/common/SegmentedControl.vue'
import FilterSelect from '@/components/common/FilterSelect.vue'
import FunctionPermissions from '@/views/FunctionPermissions.vue'
import { permissionsAPI } from '@/api/permissions.js'
import { usePermissions } from '@/composables/usePermissions.js'

const owner = ref('')
const operation = ref('kv')
const target = ref('')
const operationOptions = [
  { value: 'kv', label: 'KV demo' },
  { value: 'call', label: 'Buffered call' },
  { value: 'stream', label: 'Stream', disabled: true, title: 'Not implemented or qualified in this candidate' },
]
const functions = ref([])
const options = computed(() => functions.value.map((f) => ({ value: f.id, label: f.runtime === 'node' ? 'Node.js' : 'Python' })))
const result = ref(null)
const error = ref('')
const invoking = ref(false)
const lifetime = new AbortController()
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
onBeforeUnmount(() => { lifetime.abort(); invokeAbort?.abort(); invokeToken += 1 })
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
