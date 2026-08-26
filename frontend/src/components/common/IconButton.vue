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
    misaligns vertically. There is no padding -- the box is h-7 w-7 and the
    glyph is centred in it by flex. (This comment used to describe a computed
    padding that has never been in the class list, and called w-3.5 "14px"; at
    the 95% root it is 13.3px, leaving 6.65px a side.)

    `iconSize` exists because a dialog header wants a 15.2px glyph in the same
    26.6px box, and the only way to get one used to be to hand-roll the whole
    button -- which is how the Modal and Drawer close buttons ended up 11.4px
    apart doing identical work.
  -->
  <button
    type="button"
    :title="title"
    :disabled="disabled"
    :aria-label="title"
    :class="[
      'inline-flex items-center justify-center rounded-md transition-colors touch-expand-iconbtn',
      size === 'md' ? 'h-8 w-8' : 'h-7 w-7',
      'focus:outline-none focus-visible:ring-2 focus-visible:ring-offset-2 focus-visible:ring-offset-background',
      'disabled:opacity-40 disabled:cursor-not-allowed',
      variantClasses,
    ]"
  >
    <component
      :is="icon"
      :class="iconSize === 'md' ? 'w-4 h-4' : 'w-3.5 h-3.5'"
    />
  </button>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  // Pass an @lucide/vue (or any Vue) icon component, e.g. Trash2.
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
  // 'sm' is the dense default (13.3px glyph, for table and toolbar strips).
  // 'md' is 15.2px, for a dialog header where the close control reads as
  // chrome rather than as a row action.
  iconSize: {
    type: String,
    default: 'sm',
    validator: (v) => ['sm', 'md'].includes(v),
  },
  // Box size. 'sm' (26.6px) is the dense default for table and toolbar strips.
  // 'md' (30.4px) matches Button size="sm", so an icon-only control can sit in
  // a row of small text buttons without being the odd one out -- which is what
  // the chat header and the composer toolbar were hand-rolling.
  size: {
    type: String,
    default: 'sm',
    validator: (v) => ['sm', 'md'].includes(v),
  },
})

const variantClasses = computed(() => {
  switch (props.variant) {
    case 'danger':
      return 'text-foreground-muted hover:text-danger-fg hover:bg-surface-hover focus-visible:ring-danger'
    case 'success':
      return 'text-foreground-muted hover:text-success hover:bg-surface-hover focus-visible:ring-success'
    // Brand-accent variant: violet instead of green for "this just
    // worked" affordances inside the dashboard chrome (Copy URL, Copy
    // ID, etc.). Green is reserved for genuine semantic-success states
    // like deployment-succeeded badges; the brand accent is the right
    // signal for "your click registered" so the dashboard doesn't read
    // as a six-colour zoo. Solid primary-violet text + matching ring
    // on focus, surface-hover background tint to anchor the moment.
    case 'primary':
      return 'text-primary hover:text-primary-hover bg-primary/10 hover:bg-primary/15 focus-visible:ring-primary'
    default:
      return 'text-foreground-muted hover:text-foreground hover:bg-surface-hover focus-visible:ring-primary'
  }
})
</script>
