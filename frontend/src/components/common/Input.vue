<template>
  <div class="flex flex-col gap-1.5 w-full">
    <label
      v-if="label"
      class="text-xs font-medium text-foreground-muted uppercase tracking-wide"
    >
      {{ label }} <span
        v-if="required"
        class="text-red-500"
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
        :type="type"
        :value="modelValue"
        class="w-full bg-background border border-border rounded-md px-3 py-2 text-base sm:text-sm text-foreground placeholder-foreground-muted/50 focus:outline-none focus:ring-1 focus:ring-white focus:border-white transition-colors duration-200"
        :class="{'pl-9': icon}"
        :placeholder="placeholder"
        :disabled="disabled"
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
      class="text-xs text-red-500"
    >{{ error }}</span>
    <span
      v-if="hint && !error"
      class="text-xs text-foreground-muted"
    >{{ hint }}</span>
  </div>
</template>

<script setup>
defineProps({
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

defineEmits(['update:modelValue'])
</script>
