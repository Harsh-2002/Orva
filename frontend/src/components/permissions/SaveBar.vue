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
      <div class="flex flex-wrap items-center gap-2 sm:shrink-0">
        <Button
          variant="secondary"
          :aria-expanded="open ? 'true' : 'false'"
          aria-controls="permissions-change-list"
          @click="open = !open"
        >
          {{ open ? 'Hide changes' : 'Review changes' }}
        </Button>
        <Button
          variant="ghost"
          :disabled="saving"
          @click="$emit('discard')"
        >
          Discard
        </Button>
        <Button
          :disabled="!canSave"
          :loading="saving"
          @click="$emit('save')"
        >
          {{ saveLabel }}
        </Button>
      </div>
    </div>
    <ul
      id="permissions-change-list"
      :hidden="!open"
      class="space-y-1.5 text-xs"
      aria-label="Changes to be saved"
    >
      <li
        v-for="(c, i) in changes"
        :key="i"
        class="flex min-w-0 gap-2 text-foreground"
      >
        <span class="w-14 shrink-0 font-medium text-foreground-strong">{{ c.kind }}</span>
        <span class="min-w-0 break-all">{{ c.text }}</span>
      </li>
    </ul>
  </section>
</template>

<script setup>
import { ref } from 'vue'
import Button from '@/components/common/Button.vue'

defineProps({
  count: { type: Number, required: true },
  changes: { type: Array, default: () => [] },
  hint: { type: String, default: '' },
  saveLabel: { type: String, default: 'Save permissions' },
  canSave: Boolean,
  saving: Boolean,
})

defineEmits(['save', 'discard'])

const open = ref(false)
</script>
