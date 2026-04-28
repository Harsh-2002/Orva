<template>
  <Teleport to="body">
    <Transition name="drawer-fade">
      <div
        v-if="modelValue"
        class="fixed inset-0 z-50"
      >
        <div
          class="absolute inset-0 bg-black/50 backdrop-blur-[2px]"
          @click="close"
        />
        <Transition name="drawer-slide">
          <div
            v-if="modelValue"
            class="absolute right-0 top-0 bottom-0 bg-background border-l border-border shadow-2xl flex flex-col"
            :style="{ width: width }"
            @keydown.esc="close"
            tabindex="-1"
            ref="root"
          >
            <header class="px-5 py-3 border-b border-border flex items-center justify-between shrink-0">
              <div class="text-sm font-medium text-white">
                <slot name="title">{{ title }}</slot>
              </div>
              <button
                class="text-foreground-muted hover:text-white transition-colors"
                @click="close"
                aria-label="Close"
              >
                <X class="w-4 h-4" />
              </button>
            </header>
            <div class="flex-1 overflow-y-auto">
              <slot />
            </div>
            <footer
              v-if="$slots.footer"
              class="px-5 py-3 border-t border-border shrink-0"
            >
              <slot name="footer" />
            </footer>
          </div>
        </Transition>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup>
import { ref, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { X } from 'lucide-vue-next'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  title: { type: String, default: '' },
  width: { type: String, default: '560px' },
})
const emit = defineEmits(['update:modelValue'])

const root = ref(null)

const close = () => emit('update:modelValue', false)

// Focus the drawer on open so Esc works without a click first.
watch(() => props.modelValue, async (v) => {
  if (v) {
    await nextTick()
    root.value?.focus?.()
  }
})

const onKey = (e) => {
  if (e.key === 'Escape' && props.modelValue) close()
}
onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>

<style scoped>
.drawer-fade-enter-active,
.drawer-fade-leave-active {
  transition: opacity 150ms ease;
}
.drawer-fade-enter-from,
.drawer-fade-leave-to {
  opacity: 0;
}

.drawer-slide-enter-active,
.drawer-slide-leave-active {
  transition: transform 200ms cubic-bezier(0.4, 0, 0.2, 1);
}
.drawer-slide-enter-from,
.drawer-slide-leave-to {
  transform: translateX(100%);
}
</style>
