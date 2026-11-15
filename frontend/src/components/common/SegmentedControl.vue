<template>
  <div
    ref="root"
    role="radiogroup"
    :aria-label="ariaLabel"
    class="inline-flex items-center gap-0.5 rounded-lg border border-border bg-surface p-0.5"
  >
    <button
      v-for="(opt, i) in options"
      :key="opt.value"
      type="button"
      role="radio"
      :aria-checked="opt.value === modelValue"
      :tabindex="isTabStop(i) ? 0 : -1"
      :disabled="opt.disabled"
      :title="opt.title"
      :data-value="opt.value"
      :class="[
        'inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-md font-medium transition-colors duration-150 focus:outline-none focus-visible:ring-2 focus-visible:ring-focus-ring disabled:opacity-50 disabled:cursor-not-allowed',
        size === 'xs' ? 'h-6 px-2.5 text-xs touch-expand-xs' : 'h-7 px-3 text-xs touch-expand-sm',
        opt.value === modelValue
          ? 'bg-background text-foreground-strong border border-border shadow-sm'
          : 'border border-transparent text-foreground-muted hover:text-foreground-strong hover:bg-surface-hover',
      ]"
      @click="select(opt)"
      @keydown="onKeydown($event, i)"
    >
      <component
        :is="opt.icon"
        v-if="opt.icon"
        class="w-3.5 h-3.5 shrink-0"
        aria-hidden="true"
      />
      {{ opt.label }}
    </button>
  </div>
</template>

<script setup>
//   SegmentedControl — the dashboard's one "pick one of a few views" control
//   (View / Edit, Side-by-side / Unified).
//
//   A mode switch that is drawn as a lowercase caption reads as a label, not
//   something you can press (the Compare versions switch was exactly that). This
//   is a bordered group on a recessed track with a raised selected segment, so
//   both the options and the current choice are visible at a glance, and every
//   option is a real button.
//
//   Semantics: a radio group, because exactly one option is selected and the
//   choice changes what the page shows rather than navigating. Arrow keys move
//   the selection, Home/End jump, and only the selected option is in the tab
//   order (roving tabindex), as for native radios.
//
//   Sizes follow Button: xs = h-7, sm = h-8 (touch-expand-* raises the hit area
//   on coarse pointers; the visible height stays dense for a mouse).

import { ref } from 'vue'

const props = defineProps({
  modelValue: { type: String, default: '' },
  // [{ value, label, icon?, title?, disabled? }]
  options: { type: Array, required: true },
  ariaLabel: { type: String, required: true },
  size: { type: String, default: 'sm', validator: (v) => ['xs', 'sm'].includes(v) },
})
const emit = defineEmits(['update:modelValue'])

const root = ref(null)

// The selected option is the tab stop; with nothing selected, the first enabled one.
const isTabStop = (i) => {
  const selected = props.options.findIndex((o) => o.value === props.modelValue)
  if (selected !== -1) return i === selected
  return i === props.options.findIndex((o) => !o.disabled)
}

const select = (opt) => {
  if (opt.disabled || opt.value === props.modelValue) return
  emit('update:modelValue', opt.value)
}

const onKeydown = (e, i) => {
  const enabled = props.options.map((o, idx) => (o.disabled ? -1 : idx)).filter((idx) => idx !== -1)
  const at = enabled.indexOf(i)
  let next = null
  if (e.key === 'ArrowRight' || e.key === 'ArrowDown') next = enabled[(at + 1) % enabled.length]
  else if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') next = enabled[(at - 1 + enabled.length) % enabled.length]
  else if (e.key === 'Home') next = enabled[0]
  else if (e.key === 'End') next = enabled[enabled.length - 1]
  if (next === null) return
  e.preventDefault()
  select(props.options[next])
  root.value?.querySelector(`[data-value="${props.options[next].value}"]`)?.focus()
}
</script>
