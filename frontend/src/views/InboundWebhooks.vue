<template>
  <ManagedInboundWebhooks
    v-if="iam.available"
    :name="name"
  />
  <LegacyInboundWebhooks
    v-else
    :name="name"
  />
</template>

<script setup>
import { useManagedIAMStore } from '@/stores/managedIAM.js'
import LegacyInboundWebhooks from './LegacyInboundWebhooks.vue'
import ManagedInboundWebhooks from './ManagedInboundWebhooks.vue'

defineOptions({ name: 'InboundWebhooksView' })
defineProps({ name: { type: String, default: '' } })

// The route keeps one URL for both modes. In managed mode the legacy
// name-resolved flow is not served at all: owner and trigger selectors must be
// exact UUIDs and there is no /sign endpoint, so a different view is required
// rather than a flag inside the old one.
const iam = useManagedIAMStore()
</script>
