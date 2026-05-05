<template>
  <teleport to="body">
    <transition name="fade">
      <div
        v-if="modelValue"
        class="fixed inset-0 z-40 flex items-stretch sm:items-center justify-center overflow-y-auto bg-black/60 backdrop-blur-sm pt-safe pb-safe pl-safe pr-safe p-2 sm:p-4"
        @click.self="close"
      >
        <!--
          Mobile (default): the modal sits flush at the top with 8 px page
          padding all around (sm:p-4 returns the desktop 16 px), the
          flex container is items-stretch so the inner panel can grow
          downward inside the safe-area-padded outer. We use 100dvh in
          the body's max-height calc so when the on-screen keyboard
          appears, dvh shrinks and the body's scrollable area follows;
          no JS visualViewport listener needed.

          Desktop (sm+): items-center returns; max-w sizes return; the
          panel becomes content-sized again.
        -->
        <div
          ref="dialogRoot"
          class="w-full bg-background border border-border rounded-lg shadow-xl my-0 sm:my-auto flex flex-col max-w-full"
          :class="sizeClass"
          role="dialog"
          aria-modal="true"
          :aria-labelledby="titleId"
        >
          <header class="flex items-center justify-between px-5 py-3 border-b border-border shrink-0">
            <div class="flex items-center gap-2 min-w-0">
              <component
                :is="icon"
                v-if="icon"
                class="w-4 h-4 text-foreground-muted shrink-0"
              />
              <h3
                :id="titleId"
                class="text-sm font-semibold text-white tracking-tight truncate"
              >
                {{ title }}
              </h3>
            </div>
            <button
              class="p-1.5 -mr-1.5 rounded text-foreground-muted hover:text-white hover:bg-surface-hover transition-colors touch-expand-iconbtn shrink-0"
              :aria-label="`Close ${title}`"
              @click="close"
            >
              <X class="w-4 h-4" />
            </button>
          </header>
          <div class="p-5 overflow-y-auto scrollable flex-1 max-h-[calc(100dvh-9rem)] sm:max-h-[70vh]">
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
import { computed, ref, toRef, onMounted, onUnmounted } from 'vue'
import { X } from 'lucide-vue-next'
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
