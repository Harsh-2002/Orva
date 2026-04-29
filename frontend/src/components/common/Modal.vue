<template>
  <teleport to="body">
    <transition name="fade">
      <div
        v-if="modelValue"
        class="fixed inset-0 z-40 flex items-start sm:items-center justify-center p-4 overflow-y-auto bg-black/60 backdrop-blur-sm"
        @click.self="close"
      >
        <div
          class="w-full bg-background border border-border rounded-lg shadow-xl my-auto"
          :class="sizeClass"
          role="dialog"
          aria-modal="true"
        >
          <header class="flex items-center justify-between px-5 py-3 border-b border-border">
            <div class="flex items-center gap-2">
              <component
                :is="icon"
                v-if="icon"
                class="w-4 h-4 text-foreground-muted"
              />
              <h3 class="text-sm font-semibold text-white tracking-tight">
                {{ title }}
              </h3>
            </div>
            <button
              class="p-1.5 rounded text-foreground-muted hover:text-white hover:bg-surface-hover transition-colors"
              :aria-label="`Close ${title}`"
              @click="close"
            >
              <X class="w-4 h-4" />
            </button>
          </header>
          <div class="p-5 max-h-[70vh] overflow-y-auto">
            <slot />
          </div>
          <footer
            v-if="$slots.footer"
            class="px-5 py-3 border-t border-border flex items-center justify-end gap-2 bg-surface/40"
          >
            <slot name="footer" />
          </footer>
        </div>
      </div>
    </transition>
  </teleport>
</template>

<script setup>
import { computed, onMounted, onUnmounted } from 'vue'
import { X } from 'lucide-vue-next'

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

const sizeClass = computed(() => {
  switch (props.size) {
    case 'sm': return 'max-w-sm'
    case 'lg': return 'max-w-2xl'
    case 'xl': return 'max-w-4xl'
    default: return 'max-w-lg'
  }
})

const close = () => emit('update:modelValue', false)

const onKey = (e) => {
  if (e.key === 'Escape' && props.modelValue) close()
}
onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>
