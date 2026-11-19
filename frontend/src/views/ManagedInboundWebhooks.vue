<template>
  <div class="space-y-4">
    <p
      v-if="!iam.available"
      data-testid="managed-inbound-unavailable"
      class="rounded-lg border border-border bg-surface p-4 text-sm text-foreground-muted"
      role="status"
    >
      Managed inbound webhooks are unavailable: the server does not report
      managed IAM.
    </p>
    <template v-else>
      <p
        v-if="directory.loading.value && !directory.fresh.value"
        class="text-sm text-foreground-muted"
        role="status"
      >
        Resolving the function…
      </p>
      <LoadError
        v-else-if="directory.error.value"
        what="Functions"
        :message="directory.error.value"
        @retry="directory.refresh()"
      />
      <p
        v-else-if="!functionId"
        data-testid="managed-inbound-unknown-function"
        class="rounded-lg border border-border bg-surface p-4 text-sm text-foreground-muted"
        role="status"
      >
        No managed function named "{{ name }}" exists. Open inbound webhooks from
        the function page.
      </p>
      <ManagedInboundWebhooksPanel
        v-else
        :function-id="functionId"
        :function-name="name"
      />
    </template>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useManagedIAMStore } from '@/stores/managedIAM.js'
import { listManagedIAMFunctions } from '@/api/managedPolicies.js'
import { useIAMResource } from '@/composables/useIAMResource.js'
import LoadError from '@/components/common/LoadError.vue'
import ManagedInboundWebhooksPanel from '@/components/exposure/ManagedInboundWebhooksPanel.vue'

defineOptions({ name: 'ManagedInboundWebhooksView' })

const props = defineProps({ name: { type: String, default: '' } })

// The URL carries a name; every managed selector needs the exact owner UUID,
// so the directory is the only accepted translation and a miss is a refusal,
// never a guess.
const iam = useManagedIAMStore()
const directory = useIAMResource(() => 'functions', (_, signal) => listManagedIAMFunctions(signal))
const functionId = computed(() => {
  if (!directory.fresh.value) return ''
  return directory.data.value.find(fn => fn.name === props.name)?.id || ''
})
</script>
