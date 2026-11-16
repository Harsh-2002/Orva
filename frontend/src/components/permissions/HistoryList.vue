<template>
  <div>
    <div class="px-4 py-3">
      <Button
        variant="secondary"
        size="sm"
        :aria-expanded="open ? 'true' : 'false'"
        aria-controls="permissions-history-list"
        @click="open = !open"
      >
        <ChevronDown
          class="h-3.5 w-3.5 transition-transform duration-150"
          :class="open ? 'rotate-180' : ''"
          aria-hidden="true"
        />
        {{ open ? 'Hide' : 'Show' }} {{ entries.length }} {{ entries.length === 1 ? 'revision' : 'revisions' }}
      </Button>
    </div>
    <div
      id="permissions-history-list"
      :hidden="!open"
    >
      <p
        v-if="open && !entries.length"
        class="px-4 pb-4 text-sm text-foreground"
      >
        No revisions recorded.
      </p>
      <ol
        v-else-if="open"
        class="divide-y divide-border border-t border-border"
        aria-label="Policy revisions, newest first"
      >
        <li
          v-for="e in entries"
          :key="`${e.revision}-${e.digest}`"
          class="space-y-1 px-4 py-3 text-xs"
        >
          <p class="flex flex-wrap items-baseline gap-x-3 gap-y-0.5">
            <span class="text-sm font-medium text-foreground-strong">Revision {{ e.revision }}</span>
            <time
              :datetime="e.changedAt"
              class="text-foreground"
            >{{ when(e.changedAt) }}</time>
          </p>
          <p class="break-all text-foreground">
            By {{ e.actor.kind }}
            <code class="font-mono text-foreground-strong">{{ e.actor.id }}</code>
          </p>
          <p class="break-all text-foreground">
            Policy digest
            <code class="font-mono text-foreground-strong">{{ e.digest }}</code>
          </p>
        </li>
      </ol>
      <p
        v-if="open && entries.length >= LIMITS.maxHistory"
        class="border-t border-border px-4 py-3 text-xs text-foreground-muted"
      >
        Only the most recent {{ LIMITS.maxHistory }} revisions are shown.
      </p>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { ChevronDown } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import { LIMITS } from '@/utils/permissionsModel.js'

defineProps({ entries: { type: Array, required: true } })

const open = ref(false)
const when = (iso) => new Date(iso).toLocaleString()
</script>
