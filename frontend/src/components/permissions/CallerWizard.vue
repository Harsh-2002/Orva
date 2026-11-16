<template>
  <WizardShell
    :title="initial ? 'Change an accepted caller' : 'Accept a caller'"
    :steps="STEPS"
    :step="step"
    :first-step="initial ? 2 : 1"
    :final-label="initial ? 'Update caller' : 'Add to policy'"
    :error="errors.step || (step === 3 ? problem : '')"
    :disabled="disabled"
    @back="back"
    @next="next"
    @cancel="$emit('cancel')"
  >
    <!-- 1. Caller -->
    <div
      v-if="step === 1"
      class="space-y-4"
    >
      <p class="text-xs text-foreground-muted">
        Choose the function that is allowed to start this one. Callers are matched by function ID, never by name.
      </p>
      <div
        v-if="resolved.length"
        class="flex flex-col gap-1.5"
      >
        <label
          for="wizard-caller"
          class="text-xs font-medium uppercase tracking-wide text-foreground-muted"
        >Caller function</label>
        <FilterSelect
          :model-value="showManual ? MANUAL : callerId"
          label="Choose a function"
          trigger-id="wizard-caller"
          wide
          :disabled="disabled"
          :options="targetOptions"
          @update:model-value="pickCaller"
        />
      </div>
      <Input
        v-if="showManual"
        v-model="callerId"
        label="Caller function ID"
        hint="Lowercase UUID."
        :error="errors.callerId || ''"
        :disabled="disabled"
        @keydown.enter.prevent="next"
      />
      <p
        v-else-if="callerId"
        class="text-xs text-foreground-muted"
      >
        Function ID <code class="break-all font-mono text-foreground-strong">{{ callerId }}</code>
      </p>
      <p
        v-if="!showManual && errors.callerId"
        role="alert"
        class="text-xs text-danger-fg"
      >
        {{ errors.callerId }}
      </p>
    </div>

    <!-- 2. How it may start this function -->
    <fieldset
      v-else-if="step === 2"
      class="space-y-1"
      :disabled="disabled"
    >
      <legend class="mb-1 text-xs font-medium uppercase tracking-wide text-foreground-muted">
        How may it start this function?
      </legend>
      <label
        v-for="t in TRIGGER_IDS"
        :key="t"
        :class="['flex min-h-11 items-start gap-3 rounded-md px-3 py-2.5 touch-expand-row', blocked(t) ? 'opacity-60' : 'cursor-pointer hover:bg-surface-hover']"
      >
        <input
          type="checkbox"
          :checked="triggers.includes(t)"
          :disabled="blocked(t)"
          class="mt-0.5 h-4 w-4 shrink-0 accent-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus-ring"
          @change="toggle(t)"
        >
        <span class="min-w-0">
          <span class="block text-sm font-medium text-foreground-strong">
            {{ TRIGGER_LABELS[t] }}
            <code class="ml-1 font-mono text-[11px] font-normal text-foreground-muted">{{ t }}</code>
          </span>
          <span
            v-if="t === 'cron' && !selfCaller"
            :class="['block text-xs', triggers.includes('cron') ? 'text-danger-fg' : 'text-foreground-muted']"
          >Only this function's own schedules are supported, so a schedule can be accepted only when the caller is this function itself.</span>
          <span
            v-else
            class="block text-xs text-foreground-muted"
          >{{ TRIGGER_HELP[t] }}</span>
        </span>
      </label>
      <p
        v-if="triggers.includes('cron')"
        class="px-3 text-xs text-foreground-muted"
      >
        Schedule acceptance covers a function's own schedules only.
      </p>
    </fieldset>

    <!-- 3. Review -->
    <div
      v-else
      class="space-y-3"
    >
      <p class="text-sm text-foreground-strong">
        Accepts {{ who }} when it starts this function by {{ how }}.
      </p>
      <p class="break-all text-xs text-foreground-muted">
        Caller ID <code class="font-mono text-foreground-strong">{{ callerId }}</code>
      </p>
      <p
        role="note"
        class="flex items-start gap-2 rounded-md border border-warning-ring bg-warning-tint px-3 py-2 text-xs text-warning-fg"
      >
        <TriangleAlert
          class="mt-0.5 h-3.5 w-3.5 shrink-0"
          aria-hidden="true"
        />
        <span>This is one half. That function also needs its own permission to call this one.</span>
      </p>
      <p class="text-xs text-foreground-muted">
        Adding this changes your draft only. Nothing is saved until you review all changes.
      </p>
    </div>
  </WizardShell>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { TriangleAlert } from '@lucide/vue'
import Input from '@/components/common/Input.vue'
import FilterSelect from '@/components/common/FilterSelect.vue'
import WizardShell from '@/components/permissions/WizardShell.vue'
import { TRIGGER_HELP, TRIGGER_IDS, TRIGGER_LABELS, callerTriggerProblem, isCanonicalUuid, targetInfo } from '@/utils/permissionsModel.js'

const props = defineProps({
  targets: { type: Array, default: () => [] },
  // An accepted caller to change: { caller_id, triggers }. Null starts at step 1.
  initial: { type: Object, default: null },
  // The function being edited. Schedule acceptance is for its own schedules only.
  ownerId: { type: String, required: true },
  problem: { type: String, default: '' },
  disabled: Boolean,
})
const emit = defineEmits(['commit', 'cancel'])

const STEPS = ['Caller', 'Triggers', 'Review']
const MANUAL = '__manual__'

const callerId = ref(props.initial ? props.initial.caller_id : '')
const triggers = ref(props.initial ? [...props.initial.triggers] : [])
const step = ref(props.initial ? 2 : 1)
const errors = ref({})

const resolved = computed(() => props.targets.filter((t) => t.state === 'resolved' && t.label))
const manual = ref(false)
const showManual = computed(() => manual.value || !resolved.value.length)
const targetOptions = computed(() => [
  ...resolved.value.map((t) => ({ value: t.id, label: `${t.label} · ${t.id}` })),
  { value: MANUAL, label: 'Enter a function ID…' },
])

function pickCaller(v) {
  manual.value = v === MANUAL
  callerId.value = v === MANUAL ? '' : v
}
const selfCaller = computed(() => callerId.value === props.ownerId)
// A schedule cannot newly be ticked for another function, and a tick that is
// already there stays so it can be corrected; it is never dropped silently.
const blocked = (t) => t === 'cron' && !selfCaller.value && !triggers.value.includes('cron')
const toggle = (t) => {
  if (blocked(t)) return
  triggers.value = triggers.value.includes(t) ? triggers.value.filter((x) => x !== t) : [...triggers.value, t]
}
watch([callerId, triggers], () => { errors.value = {} }, { deep: true })

function validate(n) {
  const e = {}
  if (n === 1 && !isCanonicalUuid(callerId.value)) {
    e.callerId = showManual.value ? 'Enter the caller function ID in lowercase.' : 'Choose a function, or enter its function ID.'
  }
  if (n === 2 && !triggers.value.length) e.step = 'Pick at least one way to continue.'
  if (n === 2 && triggers.value.length) e.step = callerTriggerProblem(callerId.value, triggers.value, props.ownerId) || undefined
  if (e.step === undefined) delete e.step
  return e
}
function next() {
  if (props.disabled) return
  const e = validate(step.value)
  if (Object.keys(e).length) {
    errors.value = e
    return
  }
  if (step.value < 3) {
    errors.value = {}
    step.value += 1
    return
  }
  const all = { ...validate(1), ...validate(2) }
  if (Object.keys(all).length) {
    errors.value = { step: all.step || all.callerId }
    return
  }
  emit('commit', { callerId: callerId.value, triggers: [...triggers.value] })
}
function back() {
  if (props.disabled || step.value <= (props.initial ? 2 : 1)) return
  errors.value = {}
  step.value -= 1
}

const who = computed(() => {
  const info = targetInfo({ targets: props.targets }, callerId.value)
  return info.resolved ? info.label : 'the function with this ID'
})
const how = computed(() => {
  const words = TRIGGER_IDS.filter((t) => triggers.value.includes(t)).map((t) => ({
    f2f: 'a direct call', job: 'a background job', cron: 'a schedule',
  }[t]))
  return words.length < 2 ? words.join('') : `${words.slice(0, -1).join(', ')} or ${words[words.length - 1]}`
})
</script>
