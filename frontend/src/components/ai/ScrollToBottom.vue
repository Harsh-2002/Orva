<template>
  <!--
    ScrollToBottom — a floating affordance that appears when the operator has
    scrolled up away from the live edge (autoscroll pauses then). Clicking it
    jumps back to the newest message. Centered above the composer.
  -->
  <Transition name="fade">
    <button
      v-if="visible"
      type="button"
      class="absolute bottom-3 left-1/2 -translate-x-1/2 z-10 flex items-center gap-1.5 rounded-full border border-border bg-surface px-3 py-1.5 text-xs text-foreground-muted shadow-lg shadow-black/30 transition-colors hover:text-white hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background"
      aria-label="Scroll to latest"
      @click="$emit('click')"
    >
      <ArrowDown class="h-3.5 w-3.5" />
      Latest
    </button>
  </Transition>
</template>

<script setup>
import { ArrowDown } from '@lucide/vue'

defineProps({
  visible: { type: Boolean, default: false },
})
defineEmits(['click'])
</script>
