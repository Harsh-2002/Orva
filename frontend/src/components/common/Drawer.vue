<template>
  <Teleport to="body">
    <Transition name="drawer-fade">
      <div
        v-if="modelValue"
        class="fixed inset-0 z-50 pointer-events-none"
      >
        <!-- Click-outside to close. Transparent — no overlay, no blur,
             no dimming. The drawer reads as an inline panel that slides
             in from the right, not a modal floating on top of a darkened
             page. -->
        <div
          class="absolute inset-0 pointer-events-auto"
          @click="close"
        />
        <Transition name="drawer-slide">
          <!--
            Mobile (<sm): bottom-sheet shape. inset-x-0 bottom-0, full
            width minus 0 px (no inset; the sheet sits flush at the
            bottom edge so the operator's thumb stays in reach), max-h
            85dvh so a tall sheet doesn't shove the page header off
            screen. pb-safe keeps content clear of the iOS home
            indicator. Border lives on the top edge only.

            Desktop (sm+): right-anchored side panel as before. The
            CSS variable --drawer-w is consumed by sm:w-[var(...)] so
            the parent can pass any width string the design wants.
          -->
          <div
            v-if="modelValue"
            class="absolute pointer-events-auto bg-background flex flex-col
                   inset-x-0 bottom-0 max-h-[85dvh] border-t border-border rounded-t-lg pb-safe
                   sm:inset-x-auto sm:right-0 sm:top-0 sm:bottom-0 sm:max-h-none sm:border-t-0 sm:border-l sm:rounded-none sm:pb-0
                   sm:w-[var(--drawer-w,560px)]"
            :style="{ '--drawer-w': width }"
            @keydown.esc="close"
            tabindex="-1"
            ref="root"
          >
            <header class="px-5 py-3 border-b border-border flex items-center justify-between shrink-0">
              <div class="text-sm font-medium text-white truncate">
                <slot name="title">{{ title }}</slot>
              </div>
              <button
                class="text-foreground-muted hover:text-white transition-colors touch-expand-iconbtn -mr-1"
                @click="close"
                aria-label="Close"
              >
                <X class="w-4 h-4" />
              </button>
            </header>
            <div class="flex-1 overflow-y-auto scrollable">
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

/* Mobile bottom-sheet slide: enters from below the viewport. */
.drawer-slide-enter-active,
.drawer-slide-leave-active {
  transition: transform 200ms cubic-bezier(0.4, 0, 0.2, 1);
}
.drawer-slide-enter-from,
.drawer-slide-leave-to {
  transform: translateY(100%);
}

/* Desktop side-panel slide: enters from the right edge. */
@media (min-width: 640px) {
  .drawer-slide-enter-from,
  .drawer-slide-leave-to {
    transform: translateX(100%);
  }
}

/* Honour reduced-motion: drop the slide entirely; the fade still runs. */
@media (prefers-reduced-motion: reduce) {
  .drawer-slide-enter-active,
  .drawer-slide-leave-active {
    transition: none;
  }
  .drawer-slide-enter-from,
  .drawer-slide-leave-to {
    transform: none;
  }
}
</style>
