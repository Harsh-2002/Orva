<template>
  <Teleport to="body">
    <Transition name="drawer-fade">
      <div
        v-if="modelValue"
        class="fixed inset-0 z-50 pointer-events-none"
      >
        <!-- Click-outside to close.

             Transparent at sm+ on purpose: the desktop drawer reads as an
             inline panel sliding in from the right, not a modal floating over
             a darkened page.

             Below sm it is not that panel. It is a bottom sheet with
             aria-modal="true" and a focus trap, it covers the control the
             operator was just using (on /ai that is the composer), and this
             catcher already swallows every tap on the page behind it. With no
             scrim the page stayed fully lit and looked live while being
             untappable, which reads as a broken screen rather than a modal.
             Dim what is genuinely no longer interactive. -->
        <div
          class="absolute inset-0 pointer-events-auto bg-scrim sm:bg-transparent"
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
            The --drawer-w CSS variable feeds the responsive width utility so
            the parent can pass any width string the design wants.
          -->
          <div
            v-if="modelValue"
            ref="root"
            class="absolute pointer-events-auto bg-background flex flex-col
                   inset-x-0 bottom-0 max-h-[85dvh] border-t border-border rounded-t-lg pb-safe
                   sm:inset-x-auto sm:right-0 sm:top-0 sm:bottom-0 sm:max-h-none sm:border-t-0 sm:border-l sm:rounded-none sm:pb-0
                   sm:w-[var(--drawer-w,560px)]"
            :style="{ '--drawer-w': width }"
            tabindex="-1"
            role="dialog"
            aria-modal="true"
            :aria-label="title || 'Panel'"
            @keydown.esc="close"
          >
            <header class="px-5 py-3 border-b border-border flex items-center justify-between shrink-0">
              <div class="text-sm font-medium text-foreground-strong truncate">
                <slot name="title">
                  {{ title }}
                </slot>
              </div>
              <!-- IconButton, not a hand-rolled one. This declared neither a
                   height nor padding, so on a mouse it was a 15.2px target --
                   the same control in Modal.vue is 26.6px, an 11.4px split in
                   chrome an operator reads as identical. -->
              <IconButton
                :icon="X"
                icon-size="md"
                title="Close"
                class="-mr-1"
                @click="close"
              />
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
defineOptions({ name: 'CommonDrawer' })

import { ref, toRef, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { X } from '@lucide/vue'
import IconButton from './IconButton.vue'
import { useFocusTrap } from '@/composables/useFocusTrap'

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

// Modal, ConfirmDialog and CommandPalette all trap focus; this was the one
// teleported surface that did not, and Teleport appends it after #app, so
// Shift-Tab from its first control walked backwards into the page behind it.
// That page is not dimmed and the drawer's click-catcher only stops the mouse,
// so a keyboard user could activate a delete button on a row underneath while
// the drawer was open. It backs the detail panels on Activity, Invocations,
// Deployments, Jobs and KVStore, so it is the widest dialog surface here.
useFocusTrap(root, toRef(props, 'modelValue'))

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
