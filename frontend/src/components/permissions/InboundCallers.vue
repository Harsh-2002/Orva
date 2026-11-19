<template>
  <div>
    <p
      v-if="!callers.length"
      class="px-4 py-4 text-sm text-foreground"
    >
      <template v-if="unconfirmed === 'stale'">
        Current caller acceptance is not verified. Do not infer absence from this unconfirmed summary; inspect the last known stored sources separately.
      </template>
      <template v-else-if="unconfirmed">
        {{ unconfirmed === 'error' ? 'Not confirmed. The refresh failed, and the last loaded list was empty.' : 'Not confirmed yet. The last loaded list was empty; refreshing.' }}
      </template>
      <template v-else-if="inlineOnly">
        No inline caller grants. Attached policy sources may still accept callers.
      </template>
      <template v-else>
        No callers accepted. No other function is accepted as a caller.
      </template>
    </p>
    <ul
      v-else
      class="divide-y divide-border"
      aria-label="Accepted callers"
    >
      <li
        v-for="(c, i) in rows"
        :key="c.caller_id"
        class="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-start sm:justify-between"
      >
        <div class="min-w-0 flex-1 space-y-2">
          <h3 class="text-sm font-medium text-foreground-strong">
            {{ inlineOnly ? 'Inline caller: ' : '' }}{{ c.target.resolved ? c.target.label : 'Unresolved function' }}
            <span class="font-normal text-foreground">{{ c.usable.length ? `may start this function by ${how(c.usable)}.` : 'is listed, but nothing it can do here is supported.' }}</span>
          </h3>
          <code class="block break-all font-mono text-xs text-foreground-strong">{{ c.caller_id }}</code>
          <ul
            class="flex flex-wrap gap-1.5"
            :aria-label="`Accepted triggers for ${c.caller_id}`"
          >
            <li
              v-for="t in c.triggers"
              :key="t"
              class="inline-flex items-center gap-1.5 rounded-md border border-border bg-background px-2 py-0.5 text-xs text-foreground"
            >
              {{ TRIGGER_LABELS[t] }}
              <code class="font-mono text-[11px] text-foreground-muted">{{ t }}</code>
            </li>
          </ul>
          <p
            v-if="c.unsupportedCron"
            role="note"
            class="flex items-start gap-2 rounded-md border border-warning-ring bg-warning-tint px-3 py-2 text-xs text-warning-fg"
          >
            <TriangleAlert
              class="mt-0.5 h-3.5 w-3.5 shrink-0"
              aria-hidden="true"
            />
            <span>Not supported: schedule acceptance works only when the caller is this function itself, so this entry does not let that function start this one by a schedule.</span>
          </p>
          <p
            v-else-if="c.triggers.includes('cron')"
            class="text-xs text-foreground-muted"
          >
            Schedule acceptance covers a function's own schedules only.
          </p>
        </div>
        <div
          v-if="editable"
          class="flex flex-wrap gap-2 self-start"
        >
          <Button
            variant="secondary"
            size="sm"
            :disabled="disabled"
            :aria-label="`Edit accepted caller ${c.caller_id}`"
            @click="$emit('edit', i)"
          >
            <Pencil
              class="h-3.5 w-3.5"
              aria-hidden="true"
            />
            Edit
          </Button>
          <Button
            variant="secondary"
            size="sm"
            :disabled="disabled"
            :aria-label="`Remove accepted caller ${c.caller_id}`"
            @click="$emit('remove', i)"
          >
            <Trash2
              class="h-3.5 w-3.5"
              aria-hidden="true"
            />
            Remove
          </Button>
        </div>
      </li>
    </ul>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { Pencil, Trash2, TriangleAlert } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import { TRIGGER_LABELS, targetInfo } from '@/utils/permissionsModel.js'

const props = defineProps({
  callers: { type: Array, required: true },
  targets: { type: Array, default: () => [] },
  // '' | 'loading' | 'error': an empty list is not shown as a fact while set.
  unconfirmed: { type: String, default: '' },
  // The function being shown. Empty means schedule acceptance cannot be confirmed.
  ownerId: { type: String, default: '' },
  editable: Boolean,
  disabled: Boolean,
  inlineOnly: Boolean,
})

defineEmits(['edit', 'remove'])

const rows = computed(() => props.callers.map((c) => {
  const unsupportedCron = c.triggers.includes('cron') && c.caller_id !== props.ownerId
  return {
    ...c,
    target: targetInfo({ targets: props.targets }, c.caller_id),
    unsupportedCron,
    usable: c.triggers.filter((t) => t !== 'cron' || !unsupportedCron),
  }
}))

const WORDS = { f2f: 'a direct call', job: 'a background job', cron: 'a schedule' }
const how = (triggers) => {
  const w = triggers.map((t) => WORDS[t])
  return w.length < 2 ? w.join('') : `${w.slice(0, -1).join(', ')} or ${w[w.length - 1]}`
}
</script>
