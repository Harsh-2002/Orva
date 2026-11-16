<template>
  <div
    class="space-y-4 border-t border-border px-4 py-4"
    role="group"
    aria-labelledby="add-access-title"
  >
    <h3
      id="add-access-title"
      class="text-sm font-medium text-foreground-strong"
    >
      Add access
    </h3>

    <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
      <div class="flex flex-col gap-1.5">
        <label
          for="add-access-kind"
          class="text-xs font-medium uppercase tracking-wide text-foreground-muted"
        >Applies to</label>
        <FilterSelect
          v-model="kind"
          label="Applies to"
          trigger-id="add-access-kind"
          wide
          :disabled="disabled"
          :options="kindOptions"
        />
      </div>
      <div class="flex flex-col gap-1.5">
        <label
          for="add-access-context"
          class="text-xs font-medium uppercase tracking-wide text-foreground-muted"
        >When</label>
        <FilterSelect
          v-model="context"
          label="When"
          trigger-id="add-access-context"
          wide
          :disabled="disabled"
          :options="contextOptions"
        />
      </div>

      <Input
        v-if="hasValue"
        v-model="value"
        :label="valueLabel"
        :hint="valueHint"
        :error="errors.value || ''"
        :disabled="disabled"
        placeholder=""
        @keydown.enter.prevent="submit"
      />
      <Input
        v-if="hasTarget"
        v-model="targetId"
        :label="kind === 'function' ? 'Function UUID' : 'Runs function UUID'"
        hint="Lowercase UUID. The function is matched by this ID, never by name."
        :error="errors.targetId || ''"
        :disabled="disabled"
        @keydown.enter.prevent="submit"
      />
    </div>

    <div class="space-y-2">
      <p
        id="add-access-actions"
        class="text-xs font-medium uppercase tracking-wide text-foreground-muted"
      >
        Actions
      </p>
      <div
        v-if="presets.length"
        class="flex flex-wrap items-center gap-2"
      >
        <Button
          v-for="p in presets"
          :key="p.id"
          variant="secondary"
          size="xs"
          :disabled="disabled"
          @click="applyPreset(p.id)"
        >
          {{ p.label }}
        </Button>
        <span class="text-xs text-foreground-muted">Presets only select actions below.</span>
      </div>
      <div
        class="flex flex-wrap gap-2"
        role="group"
        aria-labelledby="add-access-actions"
      >
        <Button
          v-for="id in available"
          :key="id"
          variant="chip"
          size="xs"
          :active="actions.includes(id)"
          :disabled="disabled"
          @click="toggle(id)"
        >
          {{ ACTION_LABELS[id] }}
          <code class="font-mono text-[11px]">{{ id }}</code>
        </Button>
      </div>
      <p
        v-if="needsGet"
        role="note"
        class="text-xs text-warning-fg"
      >
        Increment and compare and set also need Read a key (kv.get) on the same keys. It is not added for you.
      </p>
      <p
        v-if="errors.actions || errors.context"
        role="alert"
        class="text-xs text-danger-fg"
      >
        {{ errors.actions || errors.context }}
      </p>
    </div>

    <p
      v-if="problem"
      role="alert"
      class="text-xs text-danger-fg"
    >
      {{ problem }}
    </p>

    <div class="flex flex-wrap items-center gap-3">
      <Button
        variant="secondary"
        :disabled="disabled"
        @click="submit"
      >
        <Plus
          class="h-4 w-4"
          aria-hidden="true"
        />
        Add to draft
      </Button>
      <span class="text-xs text-foreground-muted">Nothing changes until you press Save permissions.</span>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { Plus } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import Input from '@/components/common/Input.vue'
import FilterSelect from '@/components/common/FilterSelect.vue'
import {
  ACTION_LABELS, CONTEXT_LABELS, KINDS, PRESETS, actionsFor, buildStatement, presetActions,
} from '@/utils/permissionsModel.js'

defineProps({
  // A policy-level reason the last add was refused (limits, size).
  problem: { type: String, default: '' },
  disabled: Boolean,
})

const emit = defineEmits(['add'])

const kind = ref('kv-key')
const context = ref('execution')
const value = ref('')
const targetId = ref('')
const actions = ref([])
const errors = ref({})

const kindOptions = Object.entries(KINDS).map(([v, k]) => ({ value: v, label: k.label }))
const isKv = computed(() => kind.value.startsWith('kv-'))
const contextOptions = computed(() => Object.entries(CONTEXT_LABELS).map(([v, label]) => ({
  value: v, label, disabled: v === 'bootstrap' && !isKv.value,
})))
const hasValue = computed(() => ['kv-key', 'kv-prefix', 'cron-name', 'cron-prefix'].includes(kind.value))
const hasTarget = computed(() => ['function', 'cron-name', 'cron-prefix'].includes(kind.value))
const valueLabel = computed(() => ({
  'kv-key': 'Key', 'kv-prefix': 'Key prefix', 'cron-name': 'Schedule name', 'cron-prefix': 'Schedule name prefix',
}[kind.value]))
const valueHint = computed(() => (kind.value.endsWith('prefix')
  ? 'Matched byte for byte, case sensitive, not trimmed. Empty matches everything.'
  : 'Matched byte for byte, case sensitive, not trimmed.'))
const available = computed(() => actionsFor(kind.value, context.value))
const presets = computed(() => (isKv.value
  ? Object.entries(PRESETS).map(([id, p]) => ({ id, label: p.label }))
  : []))
const needsGet = computed(() => (actions.value.includes('kv.incr') || actions.value.includes('kv.cas'))
  && !actions.value.includes('kv.get'))

// Changing what the statement applies to drops selections that no longer fit.
watch([kind, context], () => {
  if (context.value === 'bootstrap' && !isKv.value) context.value = 'execution'
  actions.value = actions.value.filter((a) => available.value.includes(a))
  errors.value = {}
})

const toggle = (id) => {
  actions.value = actions.value.includes(id) ? actions.value.filter((a) => a !== id) : [...actions.value, id]
}
const applyPreset = (id) => { actions.value = presetActions(id, kind.value, context.value) }

function submit() {
  const built = buildStatement({
    kind: kind.value, value: value.value, targetId: targetId.value, context: context.value, actions: actions.value,
  })
  errors.value = built.errors
  if (built.ok) emit('add', built.statement)
}
</script>
