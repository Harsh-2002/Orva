<template>
  <!--
    FilterSelect — the app's only picker. A native <select> hands a phone its OS
    wheel, which is fine alone and wrong beside a control that opens a sheet.

    Built on Popover, not a bare absolute panel: every filter strip here is an
    overflow-x scroller, so an absolutely positioned menu is clipped by its own
    toolbar, and Popover teleports out and becomes a bottom sheet below sm.

    Long lists get a type-ahead box — the one thing a <select> was better at.
  -->
  <Popover
    :title="label"
    :wide="wide"
  >
    <template #trigger="{ open, toggle }">
      <!-- The filled state says "a filter is applied", so a wide picker never
           wears it: a form field always has a value and would read as pinned
           on, beside an outlined field of the same role. -->
      <button
        :id="triggerId"
        ref="triggerRef"
        type="button"
        :disabled="disabled"
        :class="[
          'touch-expand-xs inline-flex items-center gap-1.5 rounded-md border transition-colors',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary',
          'disabled:cursor-not-allowed disabled:opacity-50',
          wide
            ? 'h-10 w-full justify-between px-3 text-sm touch-expand-md'
            : 'h-7 px-2.5 text-xs',
          bare && 'font-mono',
          active && !wide
            ? 'border-primary bg-primary text-primary-foreground'
            : wide
              ? 'border-border bg-background text-foreground hover:border-foreground-muted'
              : 'border-border bg-surface text-foreground-muted hover:text-foreground-strong hover:border-foreground-muted',
        ]"
        :aria-haspopup="'menu'"
        :aria-expanded="open ? 'true' : 'false'"
        @click="toggle"
      >
        <span
          v-if="!wide && !bare"
          class="text-[10px] uppercase tracking-wider"
        >{{ label }}{{ active ? ':' : '' }}</span>
        <span
          v-if="active || wide || bare"
          class="min-w-0 truncate"
        >{{ activeLabel }}</span>
        <ChevronDown
          :class="wide ? 'h-3.5 w-3.5 shrink-0 opacity-70' : 'h-3 w-3 shrink-0 opacity-60'"
          aria-hidden="true"
        />
      </button>
    </template>

    <template #default="{ close }">
      <!-- The filter box earns its place past a handful of options; below that
           it is a keyboard trap between the trigger and the first row. -->
      <div
        v-if="options.length > SEARCH_AT"
        class="sticky top-0 z-10 bg-background px-2 pb-2 pt-2"
      >
        <input
          v-model="query"
          type="text"
          :placeholder="`Filter ${label.toLowerCase()}`"
          class="h-8 w-full rounded-md border border-border bg-background px-2.5 text-xs text-foreground placeholder-foreground-muted focus:border-focus-ring focus:outline-none focus:ring-1 focus:ring-focus-ring"
          :aria-label="`Filter ${label.toLowerCase()}`"
        >
      </div>

      <div
        class="max-h-72 overflow-y-auto scrollable py-1"
        role="none"
      >
        <template
          v-for="opt in shown"
          :key="opt.header ? `h-${opt.label}` : opt.value"
        >
          <p
            v-if="opt.header"
            class="px-3 pb-1 pt-2 text-[10px] uppercase tracking-wider text-foreground-muted"
          >
            {{ opt.label }}
          </p>
          <button
            v-else
            type="button"
            role="menuitemradio"
            :aria-checked="opt.value === modelValue ? 'true' : 'false'"
            :disabled="opt.disabled"
            class="touch-expand-sm flex w-full items-center gap-2 px-3 py-1.5 text-left text-sm text-foreground transition-colors hover:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-40"
            @click="pick(opt.value, close)"
          >
            <Check
              class="h-3.5 w-3.5 shrink-0"
              :class="opt.value === modelValue ? 'opacity-100' : 'opacity-0'"
              aria-hidden="true"
            />
            <span class="min-w-0 flex-1 truncate">{{ opt.label }}</span>
          </button>
        </template>

        <p
          v-if="!shown.length"
          class="px-3 py-2 text-xs text-foreground-muted"
        >
          Nothing matches.
        </p>
      </div>
    </template>
  </Popover>
</template>

<script setup>
defineOptions({ name: 'CommonFilterSelect' })

import { ref, computed, watch } from 'vue'
import { ChevronDown, Check } from '@lucide/vue'
import Popover from './Popover.vue'

// Below this a filter box costs more than it saves.
const SEARCH_AT = 8

const props = defineProps({
  // [{ value, label, disabled? }], or { header: true, label } for a group
  // heading. A value of '' is the reset row: selectable, never "active", which
  // keeps the trigger unfilled while "All" is chosen.
  options: { type: Array, required: true },
  modelValue: { type: [String, Number], default: '' },
  label: { type: String, required: true },
  // Full-width field shape, for a form grid rather than a filter strip.
  wide: Boolean,
  // Chip height with no label prefix -- the value is the whole trigger, for a
  // dense inline bar where "METHOD: GET" would not fit.
  bare: Boolean,
  disabled: Boolean,
  triggerId: { type: String, default: undefined },
})

const emit = defineEmits(['update:modelValue'])

const query = ref('')
const triggerRef = ref(null)

const active = computed(() =>
  props.options.find((o) => !o.header && o.value === props.modelValue && o.value !== ''))

const activeLabel = computed(() => {
  if (active.value) return active.value.label
  return props.options.find((o) => !o.header && o.value === props.modelValue)?.label ?? props.label
})

const shown = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return props.options
  return props.options.filter((o) => !o.header && String(o.label).toLowerCase().includes(q))
})

const pick = (value, close) => {
  emit('update:modelValue', value)
  close()
  // Focus returns to the trigger, or the next Tab starts from the top of the
  // document -- the panel it came from no longer exists.
  triggerRef.value?.focus?.()
}

// A stale query would silently hide options the next time it opens.
watch(() => props.options, () => { query.value = '' })
</script>
