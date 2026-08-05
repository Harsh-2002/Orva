<template>
  <span
    class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs border bg-background font-mono"
    :class="meta.classes"
  >
    <component
      :is="meta.icon"
      class="h-3 w-3 shrink-0"
      aria-hidden="true"
    />
    {{ status }}
  </span>
</template>

<script setup>
import { computed } from 'vue'
import { CheckCircle2, XCircle, Clock, Circle } from '@lucide/vue'

// StatusBadge consolidates the small colored pill that Deployments,
// InvocationsLog, and FunctionsList all rendered with their own copies
// of the same status→class map. Two domains overlap here:
//   - deployment statuses: queued | building | succeeded | failed
//   - invocation statuses: success | error | timeout
// State is encoded three ways (color + glyph + the status text itself) so it
// never relies on hue alone (WCAG 1.4.1). Colors use the semantic -fg/-ring
// tokens so a palette change is a one-line edit.

const props = defineProps({
  status: { type: String, required: true },
})

const meta = computed(() => {
  switch (props.status) {
    case 'succeeded':
    case 'success':
    case 'active':
      return { classes: 'text-success-fg border-success-ring', icon: CheckCircle2 }

    case 'failed':
    case 'error':
    case 'crashed':
      return { classes: 'text-danger-fg border-danger-ring', icon: XCircle }

    case 'queued':
    case 'building':
    case 'pending':
    case 'timeout':
      return { classes: 'text-warning-fg border-warning-ring', icon: Clock }

    default:
      return { classes: 'text-foreground-muted border-border', icon: Circle }
  }
})
</script>
