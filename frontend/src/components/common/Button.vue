<template>
  <button
    :class="[
      'inline-flex items-center justify-center gap-2 rounded-md font-medium transition-all duration-200 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-offset-background disabled:opacity-50 disabled:cursor-not-allowed',
      sizeClasses,
      variantClasses
    ]"
    :disabled="disabled || loading"
  >
    <svg 
      v-if="loading" 
      class="animate-spin -ml-1 mr-2 h-4 w-4" 
      xmlns="http://www.w3.org/2000/svg" 
      fill="none" 
      viewBox="0 0 24 24"
    >
      <circle
        class="opacity-25"
        cx="12"
        cy="12"
        r="10"
        stroke="currentColor"
        stroke-width="4"
      />
      <path
        class="opacity-75"
        fill="currentColor"
        d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
      />
    </svg>
    <slot />
  </button>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  variant: {
    type: String,
    default: 'primary',
    validator: (value) => ['primary', 'secondary', 'danger', 'ghost'].includes(value)
  },
  size: {
    type: String,
    default: 'md',
    validator: (value) => ['sm', 'md', 'lg'].includes(value)
  },
  disabled: Boolean,
  loading: Boolean
})

const sizeClasses = computed(() => {
  switch (props.size) {
    case 'sm': return 'h-8 px-3 text-xs'
    case 'lg': return 'h-12 px-6 text-base'
    default: return 'h-10 px-4 text-sm'
  }
})

const variantClasses = computed(() => {
  switch (props.variant) {
    case 'secondary':
      return 'bg-secondary text-secondary-foreground hover:bg-secondary/80 border border-border shadow-sm'
    case 'danger':
      return 'bg-red-900/30 text-red-400 border border-red-900/50 hover:bg-red-900/50 focus:ring-red-500'
    case 'ghost':
      return 'bg-transparent text-foreground-muted hover:text-foreground hover:bg-surface-hover'
    default: // primary
      return 'bg-primary text-primary-foreground hover:bg-primary/90 focus:ring-primary shadow-md shadow-primary/20 border border-transparent'
  }
})
</script>
