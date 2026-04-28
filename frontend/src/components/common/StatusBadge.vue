<template>
  <span
    class="inline-flex items-center px-2 py-0.5 rounded text-xs border bg-background font-mono"
    :class="badgeClass"
  >
    {{ status }}
  </span>
</template>

<script setup>
import { computed } from 'vue'

// StatusBadge consolidates the small colored pill that Deployments,
// InvocationsLog, and FunctionsList all rendered with their own copies
// of the same status→class map. Two domains overlap here:
//   - deployment statuses: queued | building | succeeded | failed
//   - invocation statuses: success | error | timeout
// Both are handled in one place; unknown statuses fall back to a neutral
// foreground-muted look.

const props = defineProps({
  status: { type: String, required: true },
})

const badgeClass = computed(() => {
  switch (props.status) {
    // Deployment terminal states + invocation success.
    case 'succeeded':
    case 'success':
    case 'active':
      return 'text-green-400 border-green-900/30'

    // Failure / error states across both domains.
    case 'failed':
    case 'error':
    case 'crashed':
      return 'text-red-400 border-red-900/30'

    // In-flight / soft-warn states.
    case 'queued':
    case 'building':
    case 'pending':
    case 'timeout':
      return 'text-amber-400 border-amber-900/30'

    default:
      return 'text-foreground-muted border-border'
  }
})
</script>
