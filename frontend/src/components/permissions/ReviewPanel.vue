<template>
  <div class="space-y-3 px-4 py-3">
    <p class="text-sm text-foreground">
      {{ summary }}
    </p>
    <dl class="grid grid-cols-1 gap-x-6 gap-y-2 text-xs sm:grid-cols-2">
      <div class="min-w-0">
        <dt class="text-foreground-muted">
          Current artifact
        </dt>
        <dd class="font-mono text-foreground-strong">
          {{ host.currentArtifact ? shortDigest(host.currentArtifact) : 'unknown' }}
        </dd>
      </div>
      <div class="min-w-0">
        <dt class="text-foreground-muted">
          Approved artifact
        </dt>
        <dd class="font-mono text-foreground-strong">
          {{ host.approvedArtifact ? shortDigest(host.approvedArtifact) : 'none' }}
        </dd>
      </div>
    </dl>

    <details class="text-xs">
      <summary class="touch-expand-sm inline-flex cursor-pointer items-center text-link underline underline-offset-2">
        Show full digests and revision
      </summary>
      <dl class="mt-2 space-y-2">
        <div>
          <dt class="text-foreground-muted">
            Current artifact (SHA-256)
          </dt>
          <dd class="break-all font-mono text-foreground-strong">
            {{ host.currentArtifact || 'unknown' }}
          </dd>
        </div>
        <div>
          <dt class="text-foreground-muted">
            Approved artifact (SHA-256)
          </dt>
          <dd class="break-all font-mono text-foreground-strong">
            {{ host.approvedArtifact || 'none' }}
          </dd>
        </div>
        <div>
          <dt class="text-foreground-muted">
            Policy revision and authority epoch
          </dt>
          <dd class="break-all font-mono text-foreground-strong">
            {{ host.revision }} / {{ host.authorityEpoch }}
          </dd>
        </div>
      </dl>
    </details>

    <div
      v-if="canReview && host.currentArtifact"
      class="flex flex-wrap items-center gap-3 border-t border-border pt-3"
    >
      <Button
        variant="secondary"
        :disabled="disabled || dirty"
        :loading="pending"
        @click="$emit('review')"
      >
        Request review
      </Button>
      <p class="min-w-0 flex-1 text-xs text-foreground-muted">
        {{ dirty ? 'Save or discard your changes first. Review applies to the stored policy.' : 'Asks the server for a review of this artifact and the stored policy. Nothing is approved until the server says so.' }}
      </p>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import Button from '@/components/common/Button.vue'
import { shortDigest } from '@/utils/permissionsModel.js'

const props = defineProps({
  host: { type: Object, required: true },
  canReview: Boolean,
  dirty: Boolean,
  pending: Boolean,
  disabled: Boolean,
})

defineEmits(['review'])

const summary = computed(() => {
  const { currentArtifact: cur, approvedArtifact: app, quarantined, reviewRequired } = props.host
  if (quarantined) return 'Quarantined. The stored policy is retained but not in force until it is reviewed.'
  if (reviewRequired) return 'Review required. The stored policy is retained but not in force until it is reviewed.'
  if (!cur) return 'The current artifact is unknown, so it cannot be compared with the approved one.'
  if (!app) return 'No artifact has been approved yet.'
  return cur === app ? 'The current artifact matches the approved artifact.' : 'The current artifact differs from the approved artifact.'
})
</script>
