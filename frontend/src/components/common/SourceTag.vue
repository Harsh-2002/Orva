<template>
  <span
    class="inline-flex items-center px-2 py-0.5 rounded text-xs border bg-background font-mono uppercase tracking-wide"
    :class="tagClass"
  >
    {{ source || '—' }}
  </span>
</template>

<script setup>
import { computed } from 'vue'

// SourceTag renders the coloured "where this call came from" pill on
// the Activity page. Six known sources, each with a distinct hue so
// an operator can scan a busy feed and instantly tell apart a UI
// click from a CLI deploy from an MCP tool from a webhook delivery.
//
// Hues chosen to read well on the dashboard's dark surface and to
// not collide with StatusBadge's success/error/warning palette.

const props = defineProps({
  source: { type: String, default: '' },
})

const tagClass = computed(() => {
  switch (props.source) {
    case 'web':      return 'text-indigo-300 border-indigo-900/40'
    case 'api':      return 'text-sky-300 border-sky-900/40'
    case 'mcp':      return 'text-violet-300 border-violet-900/40'
    case 'sdk':      return 'text-teal-300 border-teal-900/40'
    case 'webhook':  return 'text-amber-300 border-amber-900/40'
    case 'cron':     return 'text-emerald-300 border-emerald-900/40'
    case 'internal': return 'text-foreground-muted border-border'
    default:         return 'text-foreground-muted border-border'
  }
})
</script>
