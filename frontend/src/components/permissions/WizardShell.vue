<template>
  <section
    ref="card"
    class="scroll-mt-16 rounded-lg border border-border bg-surface lg:scroll-mt-4"
    :aria-labelledby="titleId"
  >
    <header class="space-y-3 border-b border-border px-4 py-3">
      <h3
        :id="titleId"
        class="text-sm font-semibold text-foreground-strong"
      >
        {{ title }}
      </h3>
    </header>

    <div class="space-y-4 px-4 py-4">
      <h4
        ref="heading"
        tabindex="-1"
        class="text-sm font-medium text-foreground-strong focus:outline-none"
      >
        Step {{ step }} of {{ steps.length }}: {{ steps[step - 1] }}
      </h4>
      <slot />
      <p
        v-if="error"
        role="alert"
        class="text-xs text-danger-fg"
      >
        {{ error }}
      </p>
    </div>

    <footer class="flex flex-wrap items-center gap-2 border-t border-border px-4 py-3">
      <Button
        variant="ghost"
        :disabled="disabled"
        @click="$emit('cancel')"
      >
        Cancel
      </Button>
      <div class="flex flex-wrap items-center gap-2 sm:ml-auto">
        <Button
          v-if="step > firstStep"
          variant="secondary"
          :disabled="disabled"
          @click="$emit('back')"
        >
          Back
        </Button>
        <Button
          :disabled="disabled"
          @click="$emit('next')"
        >
          {{ step === steps.length ? finalLabel : 'Next' }}
        </Button>
      </div>
    </footer>
  </section>
</template>

<script setup>
import { nextTick, onMounted, ref, useId, watch } from 'vue'
import Button from '@/components/common/Button.vue'

const props = defineProps({
  title: { type: String, required: true },
  steps: { type: Array, required: true },
  step: { type: Number, required: true },
  // The first step a person can go Back to; an edit may start further in.
  firstStep: { type: Number, default: 1 },
  finalLabel: { type: String, default: 'Add to policy' },
  error: { type: String, default: '' },
  disabled: Boolean,
})

defineEmits(['back', 'next', 'cancel'])

const titleId = `wizard-${useId()}`
const card = ref(null)
const heading = ref(null)
// Keep the task, not the repeated page context, in view after every step.
const focusHeading = () => nextTick(() => {
  heading.value?.focus({ preventScroll: true })
  card.value?.scrollIntoView?.({ block: 'start', behavior: 'instant' })
})
watch(() => props.step, focusHeading)
onMounted(focusHeading)
</script>
