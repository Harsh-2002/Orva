<template>
  <ManagedInboundWebhooks
    v-if="ready && iam.available"
    :name="name"
  />
  <LegacyInboundWebhooks
    v-else-if="ready"
    :name="name"
  />
  <p
    v-else
    data-testid="inbound-capability-pending"
    class="text-sm text-foreground-muted"
    role="status"
  >
    Checking managed access…
  </p>
</template>

<script setup>
import { computed } from 'vue'
import { useManagedIAMStore } from '@/stores/managedIAM.js'
import LegacyInboundWebhooks from './LegacyInboundWebhooks.vue'
import ManagedInboundWebhooks from './ManagedInboundWebhooks.vue'

defineOptions({ name: 'InboundWebhooksView' })
defineProps({ name: { type: String, default: '' } })

// The route keeps one URL for both modes. In managed mode the legacy
// name-resolved flow is not served at all: owner and trigger selectors must be
// exact UUIDs and there is no /sign endpoint, so a different view is required
// rather than a flag inside the old one.
//
// The router guard only fire-and-forgets this check for non-managed routes, so
// the legacy view must not mount before an answer exists — on a managed server
// it would issue name-based reads that 400.
const iam = useManagedIAMStore()
const ready = computed(() => iam.checked === true)
iam.check()
</script>
