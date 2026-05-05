<template>
  <!--
    Button — the canonical interactive primitive for the dashboard.

    Variants:
      primary   — filled purple, the page-level CTA (Deploy, Create…)
      secondary — bordered, the page-level companion (Refresh, Cancel…)
      danger    — red, destructive confirmation
      ghost     — transparent, low-visual modal Cancel + tertiary clicks
      chip      — unfilled border + :active toggle, used for filter pills
                  on Jobs / Webhooks / CronJobs status strips. Pair with
                  size="xs" for the standard chip rhythm.

    Sizes:
      xs  — h-7  px-2.5 text-xs   (filter pills, refresh-icons, drawer actions)
      sm  — h-8  px-3   text-xs   (compact CTAs)
      md  — h-10 px-4   text-sm   (default; page-header CTAs)
      lg  — h-12 px-6   text-base (rare; only for big landing-style actions)

    Modal footer convention:
      <Button variant="ghost">Cancel</Button>
      <Button>Save</Button>          // primary, default
    Stick to that pair across every modal so the dashboard reads as a
    single design system. See Modal.vue's #footer slot.
  -->
  <button
    :class="[
      'inline-flex items-center justify-center gap-2 rounded-md font-medium transition-colors duration-150 focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-offset-background disabled:opacity-50 disabled:cursor-not-allowed',
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
    validator: (value) => ['primary', 'secondary', 'danger', 'ghost', 'chip'].includes(value)
  },
  size: {
    type: String,
    default: 'md',
    validator: (value) => ['xs', 'sm', 'md', 'lg'].includes(value)
  },
  // active is meaningful only on variant="chip" (filter-pill toggles).
  // Other variants ignore it.
  active: { type: Boolean, default: false },
  disabled: Boolean,
  loading: Boolean
})

const sizeClasses = computed(() => {
  // Visible heights stay as-is (xs=28, sm=32, md=40, lg=48). xs and sm
  // ride below the 44×44 touch-target floor on their own, so we layer a
  // transparent ::before via touch-expand-* that extends the click region
  // vertically to 44 px without changing the rendered button bounds. md
  // and lg are already at-or-above 44 px and don't need expansion.
  switch (props.size) {
    case 'xs': return 'h-7 px-2.5 text-xs touch-expand-xs'
    case 'sm': return 'h-8 px-3 text-xs touch-expand-sm'
    case 'lg': return 'h-12 px-6 text-base'
    default:   return 'h-10 px-4 text-sm'
  }
})

const variantClasses = computed(() => {
  switch (props.variant) {
    case 'secondary':
      return 'bg-secondary text-secondary-foreground hover:bg-secondary/80 border border-border shadow-sm'
    case 'danger':
      return 'bg-red-600 text-white border border-red-500 hover:bg-red-500 focus:ring-red-500 shadow-md shadow-red-600/30'
    case 'ghost':
      return 'bg-transparent text-foreground-muted hover:text-foreground hover:bg-surface-hover'
    case 'chip':
      // Filter-pill toggle. `active=true` flips to filled primary so the
      // currently-selected filter visually owns the strip; the rest stay
      // ghost-bordered until clicked. No shadow — chips sit flat on the
      // surface, unlike CTAs which lift.
      return props.active
        ? 'bg-primary text-primary-foreground border border-primary'
        : 'bg-surface text-foreground-muted border border-border hover:text-white hover:border-foreground-muted'
    default: // primary
      return 'bg-primary text-primary-foreground hover:bg-primary/90 focus:ring-primary shadow-md shadow-primary/20 border border-transparent'
  }
})
</script>
