<template>
  <div class="space-y-4">
    <IAMPolicies
      :policy-id="policyId"
      :api="api"
      :functions="directory.fresh.value ? directory.data.value : []"
      @select="select"
    />
    <p
      v-if="directory.loading.value"
      class="text-sm text-foreground-muted"
      role="status"
    >
      Loading functions for policy attachments…
    </p>
    <LoadError
      v-else-if="directory.error.value"
      what="Functions for policy attachments"
      :message="directory.error.value"
      @retry="directory.refresh()"
    />
  </div>
</template>

<script setup>
import { useRouter } from 'vue-router'
import { listManagedIAMFunctions, managedIAMAPI as api } from '@/api/managedPolicies.js'
import LoadError from '@/components/common/LoadError.vue'
import { useIAMResource } from '@/composables/useIAMResource.js'
import IAMPolicies from './IAMPolicies.vue'

defineProps({ policyId: { type: String, default: '' } })
const router = useRouter()
const directory = useIAMResource(() => 'functions', (_, signal) => listManagedIAMFunctions(signal))
function select(policyId) {
  return router.push({ name: 'iam-policies', params: { policyId } })
}
</script>
