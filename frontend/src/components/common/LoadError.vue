<template>
  <!--
    Shown when a list failed to load.

    Every list view used to `catch (e) { console.error(e) }` and then fall
    through to its "No X yet" empty state, so a failed request was
    indistinguishable from an empty account: the operator was told, with
    confidence, that they had nothing. Absence of data is not evidence of
    absence, and on a security-adjacent surface (keys, channels, egress) that
    reads as reassurance.
  -->
  <div
    role="alert"
    class="flex items-start gap-3 px-4 py-3 rounded-md border border-danger-ring bg-danger-tint text-xs"
  >
    <TriangleAlert class="w-4 h-4 shrink-0 mt-0.5 text-danger-fg" />
    <div class="min-w-0 flex-1 space-y-1">
      <p class="text-danger-fg leading-snug">
        {{ what }} could not be loaded, so this list is not showing what you have.
      </p>
      <p
        v-if="message"
        class="font-mono text-[11px] text-foreground-muted break-words leading-snug"
      >
        {{ message }}
      </p>
    </div>
    <button
      v-if="onRetry"
      type="button"
      class="shrink-0 text-danger-fg hover:text-white underline underline-offset-2 touch-expand-sm px-1"
      @click="onRetry"
    >
      Retry
    </button>
  </div>
</template>

<script setup>
import { TriangleAlert } from '@lucide/vue'

defineProps({
  // What failed, as a noun phrase: "Jobs", "API keys".
  what: { type: String, required: true },
  // The server's message, when there is one worth showing.
  message: { type: String, default: '' },
  onRetry: { type: Function, default: null },
})
</script>
