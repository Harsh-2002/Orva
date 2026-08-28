<template>
  <teleport to="body">
    <transition name="fade">
      <div
        v-if="modelValue"
        class="fixed inset-0 z-40 flex items-end sm:items-center justify-center overflow-y-auto bg-scrim backdrop-blur-sm pt-safe pl-safe pr-safe p-0 sm:p-4"
        @click.self="close"
      >
        <!--
          Mobile (default): a bottom sheet, the same shape Drawer.vue uses.
          It used to be a desktop dialog with the margins turned down -- a
          bordered, fully rounded card floating against the top of the screen,
          which is the one presentation a phone has no idiom for. Anchored to
          the bottom edge with only the top corners rounded, it reads as a
          sheet, it is where the thumb already is, and every overlay in the
          product now behaves the same way.

          100dvh in the body's max-height calc means the on-screen keyboard
          shrinks dvh and the scrollable area follows; no visualViewport
          listener needed. pb-safe sits on the panel rather than the backdrop
          so the sheet's own fill reaches the bottom edge of the screen
          instead of stopping short of the home indicator.

          Desktop (sm+): items-center returns, the max-w sizes return, and the
          panel is a content-sized centred dialog again.
        -->
        <div
          ref="dialogRoot"
          class="modal-panel w-full bg-background border-t border-border rounded-t-2xl shadow-xl pb-safe my-0 flex flex-col max-w-full max-h-[92dvh] sm:my-auto sm:max-h-none sm:rounded-lg sm:border sm:pb-0"
          :class="sizeClass"
          role="dialog"
          aria-modal="true"
          :aria-labelledby="titleId"
        >
          <!-- Grab handle: the affordance that says "this drags/dismisses"
               on a phone. Hidden from sm up, where the shape is a dialog. -->
          <div
            class="mx-auto mt-2 h-1 w-9 shrink-0 rounded-full bg-border sm:hidden"
            aria-hidden="true"
          />
          <header class="flex items-center justify-between px-5 py-3 border-b border-border shrink-0">
            <div class="flex items-center gap-2 min-w-0">
              <component
                :is="icon"
                v-if="icon"
                class="w-4 h-4 text-foreground-muted shrink-0"
              />
              <h3
                :id="titleId"
                class="text-sm font-semibold text-foreground-strong tracking-tight truncate"
              >
                {{ title }}
              </h3>
            </div>
            <IconButton
              :icon="X"
              icon-size="md"
              :title="`Close ${title}`"
              class="-mr-1.5 shrink-0"
              @click="close"
            />
          </header>
          <div class="p-5 overflow-y-auto scrollable flex-1 min-h-0 sm:max-h-[70vh]">
            <slot />
          </div>
          <footer
            v-if="$slots.footer"
            class="px-5 py-3 border-t border-border flex items-center justify-end gap-2 bg-surface/40 shrink-0"
          >
            <slot name="footer" />
          </footer>
        </div>
      </div>
    </transition>
  </teleport>
</template>

<script setup>
defineOptions({ name: 'CommonModal' })

import { computed, ref, toRef, onMounted, onUnmounted } from 'vue'
import { X } from '@lucide/vue'
import IconButton from './IconButton.vue'
import { useFocusTrap } from '@/composables/useFocusTrap'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  title: { type: String, required: true },
  icon: { type: [Object, Function], default: null },
  size: {
    type: String,
    default: 'md',
    validator: (v) => ['sm', 'md', 'lg', 'xl'].includes(v),
  },
})

const emit = defineEmits(['update:modelValue'])

// Stable id for aria-labelledby. The title prop varies per call site
// (different per Modal usage) but each Modal mount gets a unique id
// so screen readers correctly announce the title at open time.
const titleId = `modal-title-${Math.random().toString(36).slice(2, 10)}`
const dialogRoot = ref(null)

// Focus trap: activates whenever modelValue is true. Captures focus
// before the dialog opens, sets inert on #app to disable the rest of
// the document, traps Tab/Shift-Tab inside the dialog, restores focus
// to the trigger on close.
useFocusTrap(dialogRoot, toRef(props, 'modelValue'))

const sizeClass = computed(() => {
  // Below sm the modal always fills the viewport (max-w-full above).
  // From sm up it caps at the requested width.
  switch (props.size) {
    case 'sm': return 'sm:max-w-sm'
    case 'lg': return 'sm:max-w-2xl'
    case 'xl': return 'sm:max-w-4xl'
    default: return 'sm:max-w-lg'
  }
})

const close = () => emit('update:modelValue', false)

const onKey = (e) => {
  if (e.key === 'Escape' && props.modelValue) close()
}
onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>

<style scoped>
/* A sheet arrives from the edge it is anchored to. Without this the panel
   fades in place, which is the one thing a bottom sheet never does on a phone
   and the reason it read as a desktop dialog wearing a sheet's shape.
   Transform only -- animating a layout property is banned by
   frontend/test/responsive.test.js, and rightly: it relayouts every frame. */
@media (max-width: 639px) {
  .fade-enter-active .modal-panel,
  .fade-leave-active .modal-panel {
    transition: transform 200ms cubic-bezier(0.32, 0.72, 0, 1);
  }

  .fade-enter-from .modal-panel,
  .fade-leave-to .modal-panel {
    transform: translateY(100%);
  }
}

@media (prefers-reduced-motion: reduce) {
  .fade-enter-active .modal-panel,
  .fade-leave-active .modal-panel {
    transition: none;
  }

  .fade-enter-from .modal-panel,
  .fade-leave-to .modal-panel {
    transform: none;
  }
}
</style>
