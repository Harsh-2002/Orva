<template>
  <!--
    ErrorCard — a failed turn rendered inline where it happened, with Retry
    (re-runs the last action) and Dismiss. Replaces the old fake assistant ⚠️
    message so an error reads as a distinct, recoverable state rather than a
    response.
  -->
  <div
    class="flex items-start gap-2.5 rounded-lg border border-danger-ring bg-danger-tint px-3.5 py-3 text-sm"
    role="alert"
  >
    <AlertTriangle
      class="mt-0.5 h-4 w-4 shrink-0 text-danger-fg"
      aria-hidden="true"
    />
    <div class="min-w-0 flex-1">
      <p class="font-medium text-danger-fg">
        Something went wrong
      </p>
      <p class="mt-0.5 break-words text-foreground-muted">
        {{ message }}
      </p>
      <div class="mt-2.5 flex items-center gap-2">
        <Button
          size="xs"
          variant="secondary"
          @click="$emit('retry')"
        >
          <RotateCcw class="h-3.5 w-3.5" /> Retry
        </Button>
        <Button
          size="xs"
          variant="ghost"
          @click="$emit('dismiss')"
        >
          Dismiss
        </Button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { AlertTriangle, RotateCcw } from '@lucide/vue'
import Button from '@/components/common/Button.vue'

defineProps({ message: { type: String, default: '' } })
defineEmits(['retry', 'dismiss'])
</script>
