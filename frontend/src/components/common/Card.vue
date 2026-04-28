<template>
  <div :class="cardClasses">
    <div
      v-if="$slots.header"
      class="px-6 py-4 border-b border-border"
    >
      <slot name="header" />
    </div>
    <div :class="bodyClasses">
      <slot />
    </div>
    <div
      v-if="$slots.footer"
      class="px-6 py-4 border-t border-border bg-surface/50"
    >
      <slot name="footer" />
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  padding: {
    type: String,
    default: 'normal',
    validator: (value) => ['none', 'sm', 'normal', 'lg'].includes(value),
  },
  hoverable: {
    type: Boolean,
    default: false,
  },
})

const cardClasses = computed(() => {
  const base = 'bg-surface border border-border rounded-lg'
  const hover = props.hoverable ? 'hover:border-foreground-muted transition-colors' : ''
  return `${base} ${hover}`
})

const bodyClasses = computed(() => {
  const paddingMap = {
    none: '',
    sm: 'p-4',
    normal: 'px-6 py-4',
    lg: 'p-8',
  }
  return paddingMap[props.padding] || ''
})
</script>
