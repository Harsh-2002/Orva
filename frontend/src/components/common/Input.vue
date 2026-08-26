<template>
  <div class="flex flex-col gap-1.5 w-full">
    <label
      v-if="label"
      :for="inputId"
      class="text-xs font-medium text-foreground-muted uppercase tracking-wide"
    >
      {{ label }} <span
        v-if="required"
        class="text-danger-fg"
      >*</span>
    </label>
    <div class="relative">
      <!--
        text-base sm:text-sm: 16 px on mobile (the smallest font-size
        iOS Safari accepts without auto-zooming on focus), 14 px from
        sm up where the dashboard's information density wins. Keeps
        the operator on a stable viewport when they tap any field.
      -->
      <input
        :id="inputId"
        :type="type"
        :value="modelValue"
        class="w-full h-10 bg-background border border-border rounded-md px-3 text-base sm:text-sm text-foreground placeholder-foreground-muted focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring transition-colors duration-200"
        :class="{'pl-9': icon}"
        :placeholder="placeholder"
        :disabled="disabled"
        :required="required"
        :aria-invalid="error ? 'true' : undefined"
        :aria-describedby="describedBy"
        @input="$emit('update:modelValue', $event.target.value)"
      >
      <div
        v-if="icon"
        class="absolute left-3 top-1/2 -translate-y-1/2 text-foreground-muted"
      >
        <component
          :is="icon"
          class="w-4 h-4"
        />
      </div>
    </div>
    <span
      v-if="error"
      :id="errorId"
      class="text-xs text-danger-fg"
      role="alert"
    >{{ error }}</span>
    <span
      v-if="hint && !error"
      :id="hintId"
      class="text-xs text-foreground-muted"
    >{{ hint }}</span>
  </div>
</template>

<script setup>
import { computed, useId } from 'vue'

const props = defineProps({
  modelValue: {
    type: [String, Number],
    default: ''
  },
  label: {
    type: String,
    default: ''
  },
  type: {
    type: String,
    default: 'text'
  },
  placeholder: {
    type: String,
    default: ''
  },
  id: {
    type: String,
    default: ''
  },
  error: {
    type: String,
    default: ''
  },
  hint: {
    type: String,
    default: ''
  },
  icon: {
    type: Object,
    default: null
  },
  required: Boolean,
  disabled: Boolean
})

const generatedId = useId()
const inputId = computed(() => props.id || `field-${generatedId}`)
const errorId = computed(() => `${inputId.value}-error`)
const hintId = computed(() => `${inputId.value}-hint`)
const describedBy = computed(() => {
  if (props.error) return errorId.value
  if (props.hint) return hintId.value
  return undefined
})

defineEmits(['update:modelValue'])
</script>
