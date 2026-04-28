<template>
  <Teleport to="body">
    <Transition name="toast">
      <div
        v-if="visible"
        class="fixed bottom-6 right-6 z-50 max-w-sm bg-background border border-border shadow-2xl rounded-lg p-4 flex items-start gap-3"
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
