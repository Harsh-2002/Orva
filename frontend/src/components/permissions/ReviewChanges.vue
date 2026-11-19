<template>
  <section
    class="rounded-lg border border-border bg-surface"
    aria-labelledby="perm-changes-title"
  >
    <header class="space-y-1 border-b border-border px-4 py-3">
      <h2
        id="perm-changes-title"
        class="text-sm font-semibold text-foreground-strong"
      >
        Review changes
      </h2>
      <p class="measure text-xs text-foreground-muted">
        This is your draft compared with the stored policy. Nothing changes until you save, and this page shows the new policy only after the server confirms it.
      </p>
    </header>
    <ul
      id="permissions-change-list"
      class="divide-y divide-border"
      aria-label="Changes to be saved"
    >
      <li
        v-for="(c, i) in changes"
        :key="i"
        class="flex min-w-0 gap-3 px-4 py-3 text-sm"
      >
        <span class="w-16 shrink-0 font-medium text-foreground-strong">{{ c.kind }}</span>
        <span class="min-w-0 break-words text-foreground">{{ c.text }}</span>
      </li>
      <li
        v-if="!changes.length"
        class="px-4 py-3 text-sm text-foreground"
      >
        No changes.
      </li>
    </ul>
    <div
      v-if="notes.length"
      class="space-y-2 border-t border-border px-4 py-3"
    >
      <p
        v-for="n in notes"
        :key="n"
        role="note"
        class="flex items-start gap-2 rounded-md border border-warning-ring bg-warning-tint px-3 py-2 text-xs text-warning-fg"
      >
        <TriangleAlert
          class="mt-0.5 h-3.5 w-3.5 shrink-0"
          aria-hidden="true"
        />
        <span>{{ n }}</span>
      </p>
    </div>
  </section>
</template>

<script setup>
import { TriangleAlert } from '@lucide/vue'

defineProps({
  // [{ kind: 'Added' | 'Removed', text }]
  changes: { type: Array, default: () => [] },
  notes: { type: Array, default: () => [] },
})
</script>
