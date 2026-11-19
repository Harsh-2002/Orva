<template>
  <div
    v-if="job.authority_state || kind === 'schedule'"
    class="mt-2 space-y-1 text-xs text-foreground-muted"
  >
    <p>
      Authority: <strong class="font-medium text-foreground-strong">{{ stale ? 'unknown (stale snapshot)' : (job.authority_state || 'unknown') }}</strong>
      <span v-if="job.authority_reason"> ({{ job.authority_reason }})</span>
    </p>
    <p class="break-all">
      Source: {{ job.origin_kind }}
      <span
        v-if="job.creator_id"
        class="font-mono"
      >{{ job.creator_id }}</span>
      <span
        v-if="kind === 'schedule' && job.operator_id"
        class="font-mono"
      >{{ job.operator_kind }} {{ job.operator_id }}</span>
    </p>
    <p
      v-if="kind === 'schedule'"
      class="break-all"
    >
      Config revision: <span class="font-mono">{{ job.config_revision || 'unknown' }}</span>.
      Authority generation: <span class="font-mono">{{ job.authority_generation || 'unknown' }}</span>.
    </p>
    <p
      v-if="job.authority_state !== 'active'"
      class="measure"
    >
      Stored work is not authorized to run. Restoring permissions does not resume it.
    </p>
    <Button
      v-if="canManage && !stale && job.authority_state === 'suspended' && (kind === 'schedule' ? job.origin_kind !== 'legacy_unknown' : job.status === 'pending')"
      variant="secondary"
      size="xs"
      :disabled="busy"
      @click="$emit('resume', job)"
    >
      {{ kind === 'schedule' ? 'Resume schedule' : 'Resume job' }}
    </Button>
    <p v-if="kind === 'schedule' && job.authority_state === 'quarantined'">
      Resume unavailable. Legacy origin requires separate adoption review, which is not available.
    </p>
  </div>
</template>

<script setup>
import Button from '@/components/common/Button.vue'
defineProps({
  job: { type: Object, required: true },
  canManage: Boolean,
  busy: Boolean,
  kind: { type: String, default: 'job' },
  stale: Boolean,
})
defineEmits(['resume'])
</script>
