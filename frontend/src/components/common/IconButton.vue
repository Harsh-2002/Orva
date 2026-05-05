<template>
  <!--
    IconButton — fixed 28×28 (h-7 w-7) square button for icon-only
    actions in tables, drawers, and dense toolbars. Replaces every
    ad-hoc `p-1` / `p-1.5` raw <button class="text-foreground-muted
    hover:text-...">` that was scattered across views before.

    Use this when the action is communicated entirely by the icon + a
    title tooltip (Edit, Delete, Retry, Test). When you need a label
    next to the icon, prefer <Button size="xs" variant="..."> instead.

    Variants control the hover color signal:
      default — muted → foreground (neutral edits, "Test webhook")
      danger  — muted → error      (Delete)
      success — muted → success    (Retry)

    Layout note: the button is square so a strip of three never
    misaligns vertically. Padding is computed so the icon sits with
    1px of breathing room on every side at the standard 14px (w-3.5)
    icon size lucide-vue-next ships.
  -->
  <button
    type="button"
    :title="title"
    :disabled="disabled"
    :aria-label="title"
    :class="[
      'inline-flex items-center justify-center h-7 w-7 rounded-md transition-colors touch-expand-iconbtn',
      'focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-offset-background',
      'disabled:opacity-40 disabled:cursor-not-allowed',
      variantClasses,
    ]"
  >
    <component
      :is="icon"
      class="w-3.5 h-3.5"
    />
  </button>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  // Pass a lucide-vue-next (or any Vue) icon component, e.g. Trash2.
  icon: { type: [Object, Function], required: true },
  // Tooltip text — required for accessibility since the button has no
  // text label.
  title: { type: String, required: true },
  variant: {
    type: String,
    default: 'default',
    validator: (v) => ['default', 'danger', 'success', 'primary'].includes(v),
  },
  disabled: Boolean,
})

const variantClasses = computed(() => {
  switch (props.variant) {
    case 'danger':
      return 'text-foreground-muted hover:text-error hover:bg-surface-hover focus:ring-red-500'
    case 'success':
      return 'text-foreground-muted hover:text-success hover:bg-surface-hover focus:ring-green-500'
    // Brand-accent variant: violet instead of green for "this just
    // worked" affordances inside the dashboard chrome (Copy URL, Copy
    // ID, etc.). Green is reserved for genuine semantic-success states
    // like deployment-succeeded badges; the brand accent is the right
    // signal for "your click registered" so the dashboard doesn't read
    // as a six-colour zoo. Solid primary-violet text + matching ring
    // on focus, surface-hover background tint to anchor the moment.
    case 'primary':
      return 'text-primary hover:text-primary-hover bg-primary/10 hover:bg-primary/15 focus:ring-primary'
    default:
      return 'text-foreground-muted hover:text-foreground hover:bg-surface-hover focus:ring-primary'
  }
})
</script>
