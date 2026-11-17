<template>
  <p
    v-if="!rows.length"
    class="px-4 py-4 text-sm text-foreground"
  >
    <template v-if="unconfirmed === 'stale'">
      Current grants are not verified. Do not infer absence from this unconfirmed summary; inspect the last known stored sources separately.
    </template>
    <template v-else-if="unconfirmed">
      {{ unconfirmed === 'error' ? 'Not confirmed. The refresh failed, and the last loaded list was empty.' : 'Not confirmed yet. The last loaded list was empty; refreshing.' }}
    </template>
    <template v-else-if="inlineOnly">
      No inline platform grants. Attached policy sources may still grant access.
    </template>
    <template v-else>
      No protected platform actions allowed.
    </template>
  </p>
  <ul
    v-else
    class="divide-y divide-border"
    :aria-label="listLabel"
  >
    <li
      v-for="row in rows"
      :key="row.index"
      class="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-start sm:justify-between"
    >
      <div class="min-w-0 flex-1 space-y-2">
        <h3 class="text-sm font-medium text-foreground-strong">
          {{ inlineOnly ? 'Inline allowance: ' : '' }}{{ row.sentence }}
        </h3>

        <p
          v-if="row.d.selector"
          class="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5 text-xs text-foreground-muted"
        >
          <span class="text-[10px] uppercase tracking-wider">{{ row.d.selector.mode }}</span>
          <code
            v-if="row.d.selector.value !== ''"
            class="min-w-0 whitespace-pre-wrap break-all font-mono text-xs text-foreground-strong"
          >{{ row.d.selector.value }}</code>
          <span
            v-else
            class="text-foreground"
          >{{ row.d.kind === 'cron' ? 'empty, matches every schedule' : 'empty, matches every key' }}</span>
          <span
            v-if="row.padded"
            class="text-foreground"
          >(includes leading or trailing spaces)</span>
        </p>

        <p
          v-if="row.d.targetId"
          class="min-w-0 text-xs text-foreground-muted"
        >
          <span class="text-[10px] uppercase tracking-wider">{{ row.d.kind === 'cron' ? 'runs' : 'function' }}</span>
          <span
            v-if="row.target.resolved"
            class="ml-2 text-foreground"
          >{{ row.target.label }}</span>
          <span
            v-else
            class="ml-2 text-foreground"
          >Unresolved</span>
          <code class="ml-2 break-all font-mono text-xs text-foreground-strong">{{ row.d.targetId }}</code>
        </p>

        <ul
          class="flex flex-wrap gap-1.5"
          :aria-label="`Actions for ${row.d.title}`"
        >
          <li
            v-for="a in row.d.actions"
            :key="a.id"
            :title="a.id"
            class="inline-flex items-center gap-1.5 rounded-md border border-border bg-background px-2 py-0.5 text-xs text-foreground"
          >
            {{ a.label }}
            <code class="font-mono text-[11px] text-foreground-muted">{{ a.id }}</code>
          </li>
        </ul>

        <p
          v-for="note in row.d.notes"
          :key="note"
          class="text-xs text-foreground-muted"
        >
          {{ note }}
        </p>
        <p
          v-for="w in row.warnings"
          :key="w.code"
          role="note"
          class="flex items-start gap-2 rounded-md border border-warning-ring bg-warning-tint px-3 py-2 text-xs text-warning-fg"
        >
          <TriangleAlert
            class="mt-0.5 h-3.5 w-3.5 shrink-0"
            aria-hidden="true"
          />
          <span>{{ w.text }}</span>
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
          :aria-label="`Edit ${label(row)}`"
          @click="$emit('edit', row.index)"
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
          :aria-label="`Remove ${label(row)}`"
          @click="$emit('remove', row.index)"
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
</template>

<script setup>
import { computed } from 'vue'
import { Pencil, Trash2, TriangleAlert } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import { describeStatement, humanStatement, targetInfo } from '@/utils/permissionsModel.js'

const props = defineProps({
  statements: { type: Array, required: true },
  functionId: { type: String, default: '' },
  targets: { type: Array, default: () => [] },
  warnings: { type: Array, default: () => [] },
  // '' | 'loading' | 'error': an empty list is not shown as a fact while set.
  unconfirmed: { type: String, default: '' },
  // Show only these statement positions (a service group), keeping their real index.
  only: { type: Array, default: null },
  listLabel: { type: String, default: 'Allowed platform actions' },
  inlineOnly: Boolean,
  editable: Boolean,
  disabled: Boolean,
})

defineEmits(['remove', 'edit'])

const label = (row) => `${row.d.title} ${row.d.selector ? row.d.selector.value : row.d.targetId || ''}`.trim()

const rows = computed(() => props.statements.map((s, index) => {
  const d = describeStatement(s)
  const target = d.targetId ? targetInfo({ targets: props.targets }, d.targetId) : null
  return {
    index,
    d,
    sentence: humanStatement(s, target && target.resolved ? target.label : null, props.functionId || null),
    padded: !!d.selector && /^\s|\s$/.test(d.selector.value),
    target,
    warnings: props.warnings.filter((w) => w.index === index),
  }
}).filter((r) => !props.only || props.only.includes(r.index)))
</script>
