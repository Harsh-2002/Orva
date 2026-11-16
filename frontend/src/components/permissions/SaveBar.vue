<template>
  <section
    class="sticky bottom-0 z-10 space-y-3 rounded-lg border border-border bg-surface px-4 py-3"
    aria-label="Unsaved permission changes"
  >
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <p
        class="min-w-0 text-sm text-foreground"
        role="status"
      >
        {{ count }} unsaved {{ count === 1 ? 'change' : 'changes' }}.
        <span class="text-foreground-muted">{{ hint }}</span>
      </p>

      <div
        v-if="confirming"
        class="flex flex-wrap items-center gap-2 sm:shrink-0"
        role="group"
        aria-label="Confirm discard"
      >
        <span class="text-sm font-medium text-foreground-strong">Discard {{ count === 1 ? 'this change' : `these ${count} changes` }}?</span>
        <Button
          variant="secondary"
          :disabled="saving"
          @click="keepEditing"
        >
          Keep editing
        </Button>
        <Button
          variant="danger"
          :disabled="saving"
          @click="confirm"
        >
          Discard changes
        </Button>
      </div>
      <div
        v-else
        class="flex flex-wrap items-center gap-2 sm:shrink-0"
      >
        <Button
          variant="ghost"
          :disabled="saving"
          @click="confirming = true"
        >
          Discard
        </Button>
        <Button
          v-if="stage === 'review'"
          variant="secondary"
          :disabled="saving"
          @click="$emit('back')"
        >
          Back to editing
        </Button>
        <Button
          v-if="stage === 'review'"
          :disabled="!canSave"
          :loading="saving"
          @click="$emit('save')"
        >
          {{ saveLabel }}
        </Button>
        <Button
          v-else
          :disabled="saving || reviewDisabled"
          @click="$emit('review')"
        >
          Review changes
        </Button>
      </div>
    </div>
  </section>
</template>

<script setup>
import { ref } from 'vue'
import Button from '@/components/common/Button.vue'

const props = defineProps({
  count: { type: Number, required: true },
  hint: { type: String, default: '' },
  // edit: Discard / Review changes. review: Discard / Back / Save.
  stage: { type: String, default: 'edit' },
  saveLabel: { type: String, default: 'Save permissions' },
  canSave: Boolean,
  saving: Boolean,
  // An open guided step has to be finished or cancelled before changes are reviewed.
  reviewDisabled: Boolean,
})

const emit = defineEmits(['review', 'back', 'save', 'discard'])

// Inline confirmation: no native dialog and no store, so the page stays a
// plain component. Discarding is the only way a draft is thrown away on purpose.
const confirming = ref(false)
function keepEditing() {
  if (!props.saving) confirming.value = false
}
function confirm() {
  if (props.saving) return
  confirming.value = false
  emit('discard')
}
</script>
