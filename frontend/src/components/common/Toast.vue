<template>
  <Teleport to="body">
    <Transition name="toast">
      <!--
        Mobile: bottom-centred with a comfortable 8 px gap from each
        edge, pb-safe to clear the iOS home indicator. The 100% width
        minus the inset-x is capped at max-w-sm so toasts on a wide
        phone (414 px+) don't sprawl. Desktop (sm+): bottom-right
        anchored as before, 24 px from each edge.
      -->
      <div
        v-if="visible"
        class="fixed z-50 bg-background border border-border shadow-2xl rounded-lg p-4 flex items-start gap-3
               inset-x-2 bottom-2 max-w-sm mx-auto pb-safe
               sm:inset-x-auto sm:bottom-6 sm:right-6 sm:mx-0 sm:pb-4"
      >
        <div class="flex-1 min-w-0">
          <div v-if="title" class="text-sm font-medium text-white mb-0.5">{{ title }}</div>
          <div class="text-xs text-foreground-muted">
            <slot />
          </div>
        </div>
        <div class="flex flex-col gap-2 shrink-0">
          <button
            v-if="actionLabel"
            class="px-3 py-1 rounded text-xs font-medium bg-white text-black hover:bg-foreground-muted transition-colors"
            :disabled="actionLoading"
            @click="$emit('action')"
          >
            {{ actionLoading ? '…' : actionLabel }}
          </button>
          <button
            v-if="dismissible"
            class="text-foreground-muted hover:text-white text-xs"
            @click="$emit('dismiss')"
          >
            Dismiss
          </button>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup>
defineProps({
  visible: { type: Boolean, default: false },
  title: { type: String, default: '' },
  actionLabel: { type: String, default: '' },
  actionLoading: { type: Boolean, default: false },
  dismissible: { type: Boolean, default: true },
})
defineEmits(['action', 'dismiss'])
</script>

<style scoped>
.toast-enter-active,
.toast-leave-active {
  transition: all 200ms cubic-bezier(0.4, 0, 0.2, 1);
}
.toast-enter-from,
.toast-leave-to {
  opacity: 0;
  transform: translateY(8px);
}
</style>
