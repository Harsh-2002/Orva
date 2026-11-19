<template>
  <main class="mx-auto max-w-5xl space-y-6 p-4 sm:p-8">
    <header class="space-y-4 border-b border-border pb-5">
      <p
        role="status"
        class="text-sm text-warning-fg"
      >
        Scratch named-policy workflow. Production IAM is not activated.
      </p>
      <nav
        class="flex flex-wrap gap-3"
        aria-label="IAM scratch navigation"
      >
        <Button
          :variant="section === 'policies' ? 'primary' : 'secondary'"
          @click="section = 'policies'"
        >
          Policies
        </Button>
        <Button
          v-for="fn in functions"
          :key="fn.id"
          :variant="owner === fn.id && section === 'function' ? 'primary' : 'secondary'"
          @click="owner = fn.id; section = 'function'"
        >
          {{ fn.name }}
        </Button>
      </nav>
      <div
        v-if="owner"
        class="flex flex-wrap items-center gap-3"
      >
        <Button
          variant="secondary"
          :loading="invoking"
          @click="invoke"
        >
          Invoke own KV
        </Button>
        <p
          v-if="decision"
          role="status"
          class="text-sm"
          data-testid="managed-sdk-decision"
        >
          {{ decision }}
        </p>
      </div>
      <p
        v-if="error"
        role="alert"
        class="text-sm text-danger-fg"
      >
        {{ error }}
      </p>
    </header>
    <IAMPolicies
      v-if="section === 'policies'"
      :policy-id="policy"
      :functions="functions"
      :api="api"
      @select="policy = $event"
    />
    <FunctionPermissionSources
      v-else-if="owner"
      :key="owner"
      :function-id="owner"
      :api="api"
      @policy="policy = $event; section = 'policies'"
    />
  </main>
</template>

<script setup>
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import Button from '@/components/common/Button.vue'
import IAMPolicies from '@/views/IAMPolicies.vue'
import FunctionPermissionSources from '@/views/FunctionPermissionSources.vue'
import { managedPoliciesAPI } from '@/api/managedPolicies.js'
const functions = ref([])
const owner = ref('')
const policy = ref('')
const section = ref('policies')
const decision = ref('')
const error = ref('')
const invoking = ref(false)
const lifetime = new AbortController()
let requestToken = 0
async function request(path, { method = 'GET', body, signal } = {}) {
  const response = await fetch(path, { method, signal, headers: { 'Content-Type': 'application/json' }, body: body === undefined ? undefined : JSON.stringify(body) })
  const value = await response.json()
  if (!response.ok) throw Object.assign(new Error(value.error?.message || 'Scratch request failed.'), { status: response.status })
  return value
}
const api = managedPoliciesAPI(request)
watch(owner, () => { requestToken += 1; decision.value = ''; invoking.value = false })
onMounted(async () => {
  try { functions.value = await request('/api/scratch/functions', { signal: lifetime.signal }); owner.value = functions.value[0]?.id || '' }
  catch (e) { if (!lifetime.signal.aborted) error.value = e.message }
})
onBeforeUnmount(() => { lifetime.abort(); requestToken += 1 })
async function invoke() {
  const token = ++requestToken
  const id = owner.value
  invoking.value = true
  try {
    const result = await request(`/fn/${id}/demo`, { method: 'POST', body: {}, signal: lifetime.signal })
    if (token === requestToken && id === owner.value) decision.value = result.decision
  } catch (e) { if (token === requestToken) error.value = e.message }
  finally { if (token === requestToken) invoking.value = false }
}
</script>
