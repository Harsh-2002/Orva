<template>
  <main class="mx-auto max-w-5xl p-4 sm:p-8 space-y-6">
    <header class="space-y-3 border-b border-border pb-5">
      <p class="text-sm text-warning-fg">
        Live scratch IAM · KV workflow · not production-ready
      </p>
      <p class="text-sm text-foreground-muted">
        Direct Factory dispatch, not the serving pool. Only private SDK storage access is exercised here.
      </p>
      <SegmentedControl
        v-model="owner"
        aria-label="Scratch function"
        :options="options"
      />
      <div class="flex flex-wrap items-center gap-3">
        <Button
          :loading="invoking"
          :disabled="!owner || invoking"
          @click="invoke"
        >
          Invoke KV demo
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
import FunctionPermissions from '@/views/FunctionPermissions.vue'
import { permissionsAPI } from '@/api/permissions.js'
import { usePermissions } from '@/composables/usePermissions.js'

const owner = ref('')
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
  return response.json()
}
const controller = usePermissions(() => owner.value, permissionsAPI(request))
const pageState = computed(() => Object.fromEntries(
  ['snapshot', 'loading', 'loadError', 'saving', 'saveError'].map((key) => [key, controller[key].value]),
))
watch(owner, () => { invokeToken += 1; invokeAbort?.abort(); invoking.value = false; result.value = null; error.value = '' })
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
    const value = await request(`/api/scratch/invoke/${id}`, { method: 'POST', signal: invokeAbort.signal })
    if (token === invokeToken && id === owner.value) result.value = value
  } catch {
    if (token === invokeToken) error.value = 'Scratch invocation failed. Retry.'
  } finally {
    if (token === invokeToken) invoking.value = false
  }
}
</script>
