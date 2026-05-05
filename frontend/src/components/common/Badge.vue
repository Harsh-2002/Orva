<template>
  <span :class="badgeClasses">
    <slot />
  </span>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  variant: {
    type: String,
    default: 'default',
    validator: (value) => ['default', 'primary', 'success', 'warning', 'error', 'info', 'gray'].includes(value),
  },
  size: {
    type: String,
    default: 'md',
    validator: (value) => ['sm', 'md', 'lg'].includes(value),
  },
  dot: {
    type: Boolean,
    default: false,
  },
})

const badgeClasses = computed(() => {
  const variants = {
    default: 'bg-surface text-foreground-muted border border-border',
    primary: 'bg-primary/20 text-primary border border-primary/30',
    success: 'bg-success-tint text-success-fg border border-success-ring',
    warning: 'bg-warning-tint text-warning-fg border border-warning-ring',
    error:   'bg-danger-tint text-danger-fg border border-danger-ring',
    info:    'bg-info-tint text-info-fg border border-info-ring',
    gray:    'bg-surface text-foreground-muted border border-border',
  }

  const sizes = {
    sm: 'px-2 py-0.5 text-xs',
    md: 'px-2.5 py-1 text-sm',
    lg: 'px-3 py-1.5 text-base',
  }

  const base = 'inline-flex items-center font-medium rounded-full'
  const dotClass = props.dot ? 'gap-1.5' : ''
  
  return `${base} ${variants[props.variant]} ${sizes[props.size]} ${dotClass}`
})
</script>
