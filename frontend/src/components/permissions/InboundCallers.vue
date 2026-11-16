<template>
  <div>
    <p
      v-if="!callers.length"
      class="px-4 py-4 text-sm text-foreground"
    >
      No callers accepted. No other function is accepted as a caller.
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
            {{ c.target.resolved ? c.target.label : 'Unresolved function' }}
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
            v-if="c.triggers.includes('cron')"
            class="text-xs text-foreground-muted"
          >
            Schedule acceptance covers a function's own schedules only.
          </p>
        </div>
        <Button
          v-if="editable"
          variant="secondary"
          size="sm"
          class="self-start"
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
      </li>
    </ul>

    <div
      v-if="editable"
      class="space-y-4 border-t border-border px-4 py-4"
      role="group"
      aria-labelledby="add-caller-title"
    >
      <h3
        id="add-caller-title"
        class="text-sm font-medium text-foreground-strong"
      >
        Accept a caller
      </h3>
      <Input
        v-model="callerId"
        label="Caller function UUID"
        hint="Lowercase UUID. Callers are matched by this ID, never by name."
        :error="errors.callerId || ''"
        :disabled="disabled"
        @keydown.enter.prevent="submit"
      />
      <div class="space-y-2">
        <p
          id="add-caller-triggers"
          class="text-xs font-medium uppercase tracking-wide text-foreground-muted"
        >
          Triggers
        </p>
        <div
          class="flex flex-wrap gap-2"
          role="group"
          aria-labelledby="add-caller-triggers"
        >
          <Button
            v-for="t in TRIGGER_IDS"
            :key="t"
            variant="chip"
            size="xs"
            :active="triggers.includes(t)"
            :disabled="disabled"
            @click="toggle(t)"
          >
            {{ TRIGGER_LABELS[t] }}
            <code class="font-mono text-[11px]">{{ t }}</code>
          </Button>
        </div>
        <p
          v-if="errors.triggers"
          role="alert"
          class="text-xs text-danger-fg"
        >
          {{ errors.triggers }}
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
          Accept caller
        </Button>
        <span class="text-xs text-foreground-muted">Nothing changes until you press Save permissions.</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { Plus, Trash2 } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import Input from '@/components/common/Input.vue'
import { TRIGGER_IDS, TRIGGER_LABELS, isCanonicalUuid, targetInfo } from '@/utils/permissionsModel.js'

const props = defineProps({
  callers: { type: Array, required: true },
  targets: { type: Array, default: () => [] },
  problem: { type: String, default: '' },
  editable: Boolean,
  disabled: Boolean,
})

const emit = defineEmits(['add', 'remove'])

const callerId = ref('')
const triggers = ref([])
const errors = ref({})

const rows = computed(() => props.callers.map((c) => ({ ...c, target: targetInfo({ targets: props.targets }, c.caller_id) })))

const toggle = (t) => {
  triggers.value = triggers.value.includes(t) ? triggers.value.filter((x) => x !== t) : [...triggers.value, t]
}

function submit() {
  const next = {}
  if (!isCanonicalUuid(callerId.value)) next.callerId = 'Enter the caller UUID in lowercase.'
  if (!triggers.value.length) next.triggers = 'Pick at least one trigger.'
  errors.value = next
  if (Object.keys(next).length) return
  emit('add', { callerId: callerId.value, triggers: [...triggers.value] })
}

// The parent clears these by remounting through a key after a successful add.
defineExpose({ reset: () => { callerId.value = ''; triggers.value = []; errors.value = {} } })
</script>
