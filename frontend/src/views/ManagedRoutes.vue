<template>
  <div class="space-y-4">
    <p
      v-if="!iam.available"
      data-testid="managed-routes-unavailable"
      class="rounded-lg border border-border bg-surface p-4 text-sm text-foreground-muted"
      role="status"
    >
      Managed routes are unavailable: this server does not report the managed
      IAM capability.
    </p>
    <template v-else>
      <LoadError
        v-if="directory.error.value"
        what="Functions for route owners"
        :message="directory.error.value"
        @retry="directory.refresh()"
      />
      <p
        v-if="directory.loading.value && !directory.fresh.value"
        class="text-sm text-foreground-muted"
        role="status"
      >
        Loading functions…
      </p>
      <ManagedRoutesPanel :functions="functions" />
    </template>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useManagedIAMStore } from '@/stores/managedIAM.js'
import { listManagedIAMFunctions } from '@/api/managedPolicies.js'
import { useIAMResource } from '@/composables/useIAMResource.js'
import LoadError from '@/components/common/LoadError.vue'
import ManagedRoutesPanel from '@/components/exposure/ManagedRoutesPanel.vue'

defineOptions({ name: 'ManagedRoutesView' })

// The router guard has already confirmed the capability; this second read is
// the same fact the sidebar uses, so a direct navigation can never show edit
// controls on a server that is not managed.
const iam = useManagedIAMStore()
const directory = useIAMResource(() => 'functions', (_, signal) => listManagedIAMFunctions(signal))
const functions = computed(() => (directory.fresh.value ? directory.data.value : []))
</script>
