<template>
  <FunctionPermissionSources
    :function-id="functionId"
    :api="api"
    @policy="openPolicy"
  />
</template>

<script setup>
import { useRouter } from 'vue-router'
import FunctionPermissionSources from './FunctionPermissionSources.vue'
import { managedIAMAPI as api } from '@/api/managedPolicies.js'

defineProps({ functionId: { type: String, required: true } })
const router = useRouter()
function openPolicy(policyId) {
  return router.push({ name: 'iam-policies', params: { policyId } })
}
</script>
