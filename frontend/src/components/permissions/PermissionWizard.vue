<template>
  <WizardShell
    :title="initial ? 'Change a permission' : 'Add a permission'"
    :steps="STEPS"
    :step="step"
    :first-step="initial ? 2 : 1"
    :final-label="initial ? 'Update permission' : 'Add to policy'"
    :error="errors.step || (step === 4 ? problem : '')"
    :disabled="disabled"
    @back="back"
    @next="next"
    @cancel="$emit('cancel')"
  >
    <!-- 1. Service -->
    <fieldset
      v-if="step === 1"
      class="space-y-2"
      :disabled="disabled"
    >
      <legend class="sr-only">
        Service
      </legend>
      <p class="text-xs text-foreground-muted">
        Pick what this permission is about. You choose what it can do next.
      </p>
      <label
        v-for="id in SERVICE_ORDER"
        :key="id"
        :class="[ROW, form.service === id ? 'border-primary bg-surface-hover' : 'border-border hover:border-foreground-muted']"
      >
        <input
          type="radio"
          name="permission-service"
          :value="id"
          :checked="form.service === id"
          :class="INPUT"
          @change="pickService(id)"
        >
        <span class="min-w-0">
          <span class="block text-sm font-medium text-foreground-strong">{{ SERVICES[id].label }}</span>
          <span class="block text-xs text-foreground-muted">{{ SERVICES[id].purpose }}</span>
        </span>
      </label>
    </fieldset>

    <!-- 2. Actions -->
    <div
      v-else-if="step === 2"
      class="space-y-4"
    >
      <fieldset
        v-if="form.service === 'storage'"
        class="space-y-2"
        :disabled="disabled"
      >
        <legend class="mb-1 text-xs font-medium uppercase tracking-wide text-foreground-muted">
          When can it use this?
        </legend>
        <label
          v-for="c in CONTEXTS"
          :key="c.id"
          :class="[ROW, form.context === c.id ? 'border-primary bg-surface-hover' : 'border-border hover:border-foreground-muted']"
        >
          <input
            type="radio"
            name="permission-context"
            :value="c.id"
            :checked="form.context === c.id"
            :class="INPUT"
            @change="setContext(c.id)"
          >
          <span class="min-w-0">
            <span class="block text-sm font-medium text-foreground-strong">{{ c.label }}</span>
            <span class="block text-xs text-foreground-muted">{{ c.help }}</span>
          </span>
        </label>
      </fieldset>

      <fieldset
        v-for="g in groups"
        :key="g.group || 'all'"
        class="space-y-1"
        :disabled="disabled"
      >
        <legend class="mb-1 text-xs font-medium uppercase tracking-wide text-foreground-muted">
          {{ g.group || 'What can it do?' }}
        </legend>
        <label
          v-for="id in g.ids"
          :key="id"
          :class="[ROW, 'border-transparent', blocked(id) ? 'opacity-60' : 'hover:bg-surface-hover']"
        >
          <input
            type="checkbox"
            :checked="form.actions.includes(id)"
            :disabled="blocked(id)"
            :class="INPUT"
            @change="toggle(id)"
          >
          <span class="min-w-0">
            <span class="block text-sm font-medium text-foreground-strong">
              {{ ACTION_LABELS[id] }}
              <code class="ml-1 font-mono text-[11px] font-normal text-foreground-muted">{{ id }}</code>
            </span>
            <span
              v-if="invalidAtStartup(id)"
              class="block text-xs text-danger-fg"
            >{{ form.actions.includes(id) ? 'Not allowed at startup. Untick it, or choose While handling a request.' : 'Not available at startup. Startup access is read only.' }}</span>
            <span
              v-else
              class="block text-xs text-foreground-muted"
            >{{ ACTION_HELP[id] }}</span>
          </span>
        </label>
      </fieldset>

      <p
        v-if="needsGet"
        role="note"
        class="flex items-start gap-2 rounded-md border border-warning-ring bg-warning-tint px-3 py-2 text-xs text-warning-fg"
      >
        <TriangleAlert
          class="mt-0.5 h-3.5 w-3.5 shrink-0"
          aria-hidden="true"
        />
        <span>Increment and Compare and set also need Read a key (kv.get) on the same keys. Read is not added for you. Tick it here, or this permission will be incomplete.</span>
      </p>
    </div>

    <!-- 3. Resources -->
    <div
      v-else-if="step === 3"
      class="space-y-4"
    >
      <template v-if="form.service === 'storage'">
        <div class="space-y-1.5">
          <p
            id="wizard-keys-label"
            class="text-xs font-medium uppercase tracking-wide text-foreground-muted"
          >
            Which keys?
          </p>
          <SegmentedControl
            v-model="form.mode"
            aria-label="Which keys"
            :options="storageModes"
          />
          <p
            v-if="listPicked"
            class="text-xs text-foreground-muted"
          >
            List keys works on a prefix or on every key, not on one exact key.
          </p>
        </div>
        <Input
          v-if="form.mode === 'key' || form.mode === 'prefix'"
          v-model="form.value"
          :label="form.mode === 'key' ? 'Key' : 'Key prefix'"
          :hint="form.mode === 'prefix' ? 'Keys that start with this text. Matched exactly, case sensitive, not trimmed. To allow every key, choose Every key.' : 'Matched exactly, case sensitive, not trimmed.'"
          :error="errors.value || ''"
          :disabled="disabled"
          @keydown.enter.prevent="next"
        />
        <p
          v-else
          role="note"
          class="flex items-start gap-2 rounded-md border border-warning-ring bg-warning-tint px-3 py-2 text-xs text-warning-fg"
        >
          <TriangleAlert
            class="mt-0.5 h-3.5 w-3.5 shrink-0"
            aria-hidden="true"
          />
          <span>Broad: this covers every key in this function's storage, including keys added later.</span>
        </p>
      </template>

      <template v-else-if="form.service === 'functions'">
        <div
          v-if="resolved.length"
          class="flex flex-col gap-1.5"
        >
          <label
            for="wizard-function"
            class="text-xs font-medium uppercase tracking-wide text-foreground-muted"
          >Function</label>
          <FilterSelect
            :model-value="showManual ? MANUAL : form.targetId"
            label="Choose a function"
            trigger-id="wizard-function"
            wide
            :disabled="disabled"
            :options="targetOptions"
            @update:model-value="pickTarget"
          />
        </div>
        <Input
          v-if="showManual"
          v-model="form.targetId"
          label="Function ID"
          hint="Lowercase UUID. The function is matched by this ID, never by name."
          :error="errors.targetId || ''"
          :disabled="disabled"
          @keydown.enter.prevent="next"
        />
        <p
          v-else-if="form.targetId"
          class="text-xs text-foreground-muted"
        >
          Function ID <code class="break-all font-mono text-foreground-strong">{{ form.targetId }}</code>
        </p>
        <p
          v-if="!showManual && errors.targetId"
          role="alert"
          class="text-xs text-danger-fg"
        >
          {{ errors.targetId }}
        </p>
        <p class="text-xs text-foreground-muted">
          Takes effect only if that function also accepts this one as a caller.
        </p>
      </template>

      <template v-else-if="form.service === 'schedules'">
        <div class="space-y-1.5">
          <p class="text-xs font-medium uppercase tracking-wide text-foreground-muted">
            Which schedules?
          </p>
          <SegmentedControl
            v-model="form.mode"
            aria-label="Which schedules"
            :options="scheduleModes"
          />
        </div>
        <Input
          v-if="form.mode !== 'all'"
          v-model="form.value"
          :label="form.mode === 'prefix' ? 'Schedule name prefix' : 'Schedule name'"
          :hint="form.mode === 'prefix' ? 'Schedules whose names start with this text. Matched exactly, case sensitive. To allow every schedule, choose Every schedule.' : 'Matched exactly, case sensitive, not trimmed.'"
          :error="errors.value || ''"
          :disabled="disabled"
          @keydown.enter.prevent="next"
        />
        <p
          v-else
          role="note"
          class="flex items-start gap-2 rounded-md border border-warning-ring bg-warning-tint px-3 py-2 text-xs text-warning-fg"
        >
          <TriangleAlert
            class="mt-0.5 h-3.5 w-3.5 shrink-0"
            aria-hidden="true"
          />
          <span>Broad: this covers every schedule that runs this function, including ones added later.</span>
        </p>
        <p
          v-if="form.targetId && form.targetId !== functionId"
          role="alert"
          class="text-xs text-warning-fg"
        >
          Unsupported stored target
          <code class="break-all font-mono">{{ form.targetId }}</code>.
          Schedules may run only this function. Cancel, remove this permission and add a new one.
        </p>
        <p
          v-else
          class="text-xs text-foreground-muted"
        >
          These schedules run this function
          <code class="break-all font-mono text-foreground-strong">{{ form.targetId || functionId }}</code>.
          Today only schedules that run this function itself are supported.
        </p>
      </template>

      <p
        v-else
        class="text-sm text-foreground"
      >
        This applies to the execution that is currently running. There is nothing to choose.
      </p>
    </div>

    <!-- 4. Review -->
    <div
      v-else
      class="space-y-3"
    >
      <p class="text-sm text-foreground-strong">
        {{ sentence }}
      </p>
      <dl
        v-if="detail"
        class="grid grid-cols-1 gap-x-6 gap-y-2 text-xs sm:grid-cols-[8rem_1fr]"
      >
        <dt class="text-foreground-muted">
          Service
        </dt>
        <dd class="text-foreground">
          {{ SERVICES[form.service].label }}
        </dd>
        <dt class="text-foreground-muted">
          Actions
        </dt>
        <dd class="flex flex-wrap gap-1.5">
          <span
            v-for="a in detail.actions"
            :key="a.id"
            class="inline-flex items-center gap-1.5 rounded-md border border-border bg-background px-2 py-0.5 text-foreground"
          >
            {{ a.label }}
            <code class="font-mono text-[11px] text-foreground-muted">{{ a.id }}</code>
          </span>
        </dd>
        <template v-if="detail.selector">
          <dt class="text-foreground-muted">
            {{ detail.kind === 'cron' ? 'Schedule' : 'Keys' }}
          </dt>
          <dd class="min-w-0 text-foreground">
            <code
              v-if="detail.selector.value !== ''"
              class="whitespace-pre-wrap break-all font-mono text-foreground-strong"
            >{{ detail.selector.value }}</code>
            <span v-else>{{ detail.kind === 'cron' ? 'every schedule' : 'every key' }}</span>
            <span
              v-if="detail.selector.value !== ''"
              class="text-foreground-muted"
            > ({{ selectorMode }})</span>
          </dd>
        </template>
        <template v-if="detail.targetId">
          <dt class="text-foreground-muted">
            {{ detail.kind === 'cron' ? 'Runs' : 'Function' }}
          </dt>
          <dd class="min-w-0 text-foreground">
            <span v-if="targetLabel">{{ targetLabel }}</span>
            <code class="break-all font-mono text-foreground-strong"> {{ detail.targetId }}</code>
          </dd>
        </template>
      </dl>
      <p
        v-for="note in notes"
        :key="note"
        role="note"
        class="flex items-start gap-2 rounded-md border border-warning-ring bg-warning-tint px-3 py-2 text-xs text-warning-fg"
      >
        <TriangleAlert
          class="mt-0.5 h-3.5 w-3.5 shrink-0"
          aria-hidden="true"
        />
        <span>{{ note }}</span>
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
import SegmentedControl from '@/components/common/SegmentedControl.vue'
import WizardShell from '@/components/permissions/WizardShell.vue'
import {
  ACTION_HELP, ACTION_LABELS, SERVICES, SERVICE_ACTIONS, SERVICE_ORDER, buildStatement, describeStatement,
  emptyForm, formToInput, humanStatement, statementToForm, targetInfo,
} from '@/utils/permissionsModel.js'

const props = defineProps({
  functionId: { type: String, required: true },
  targets: { type: Array, default: () => [] },
  // A statement to change. Null starts a new permission at step 1.
  initial: { type: Object, default: null },
  // A policy-level reason the last add was refused (duplicate, limits, size).
  problem: { type: String, default: '' },
  disabled: Boolean,
})
const emit = defineEmits(['commit', 'cancel'])

const STEPS = ['Service', 'Actions', 'Resources', 'Review']
const MANUAL = '__manual__'
const ROW = 'flex min-h-11 cursor-pointer items-start gap-3 rounded-md border px-3 py-2.5 touch-expand-row'
const INPUT = 'mt-0.5 h-4 w-4 shrink-0 accent-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-focus-ring'
const CONTEXTS = [
  { id: 'execution', label: 'While handling a request', help: 'The usual case: the function uses it when it runs.' },
  { id: 'bootstrap', label: 'At startup', help: 'When the function loads, before any request. Read and list only.' },
]

const form = ref(props.initial ? statementToForm(props.initial) : emptyForm())
const step = ref(props.initial ? 2 : 1)
const errors = ref({})

const resolved = computed(() => props.targets.filter((t) => t.state === 'resolved' && t.label))
const manual = ref(!!props.initial && form.value.service === 'functions'
  && !props.targets.some((t) => t.id === form.value.targetId && t.state === 'resolved' && t.label))
const showManual = computed(() => manual.value || !resolved.value.length)
// The id stays in the option text so typing either part finds the function.
const targetOptions = computed(() => [
  ...resolved.value.map((t) => ({ value: t.id, label: `${t.label} · ${t.id}` })),
  { value: MANUAL, label: 'Enter a function ID…' },
])

const groups = computed(() => SERVICE_ACTIONS[form.value.service] || [])
const startup = computed(() => form.value.context === 'bootstrap' && form.value.service === 'storage')
const invalidAtStartup = (id) => startup.value && id !== 'kv.get' && id !== 'kv.list'
// A write that is already ticked stays tickable-off, so it can be corrected;
// a new one cannot be added while startup is chosen.
const blocked = (id) => invalidAtStartup(id) && !form.value.actions.includes(id)
const listPicked = computed(() => form.value.actions.includes('kv.list'))
const needsGet = computed(() => form.value.service === 'storage'
  && (form.value.actions.includes('kv.incr') || form.value.actions.includes('kv.cas'))
  && !form.value.actions.includes('kv.get'))
const storageModes = [
  { value: 'key', label: 'Exact key' },
  { value: 'prefix', label: 'Prefix' },
  { value: 'all', label: 'Every key' },
]
const scheduleModes = [
  { value: 'name', label: 'Exact name' },
  { value: 'prefix', label: 'Name prefix' },
  { value: 'all', label: 'Every schedule' },
]

function pickService(id) {
  if (form.value.service === id) return
  form.value = { ...emptyForm(), service: id }
  manual.value = false
}
// Choosing a context never edits the selected actions. Choices that no longer
// fit stay visible and block Next until the operator unticks them.
function setContext(c) {
  form.value.context = c
}
function toggle(id) {
  if (blocked(id)) return
  const a = form.value.actions
  form.value.actions = a.includes(id) ? a.filter((x) => x !== id) : [...a, id]
}
function pickTarget(v) {
  if (v === MANUAL) {
    manual.value = true
    form.value.targetId = ''
  } else {
    manual.value = false
    form.value.targetId = v
  }
}

// Any edit clears messages that described the old input.
watch(form, () => { errors.value = {} }, { deep: true })

function validate(n) {
  const e = {}
  if (n === 1 && !form.value.service) e.step = 'Choose a service to continue.'
  if (n === 2 && !form.value.actions.length) e.step = 'Pick at least one action to continue.'
  if (n === 2 && form.value.actions.length) {
    const bad = form.value.actions.filter(invalidAtStartup)
    if (bad.length) e.step = `Startup access can only read and list. Untick ${bad.map((a) => ACTION_LABELS[a]).join(', ')}, or choose While handling a request.`
  }
  if (n === 3) {
    const f = form.value
    // Nothing widens on its own: a blank prefix is not "every key", and an exact
    // key is not turned into a prefix to make List fit.
    if (f.service === 'storage' && f.mode === 'key' && listPicked.value) {
      e.step = 'List keys needs a prefix or every key. Choose Prefix or Every key, or go back and untick List keys.'
    }
    if (f.service === 'storage' && f.mode === 'prefix' && f.value === '') {
      e.value = 'Enter a prefix, or choose Every key to allow all keys.'
    }
    if (f.service === 'schedules' && f.mode === 'prefix' && f.value === '') {
      e.value = 'Enter a prefix, or choose Every schedule to allow all schedules.'
    }
    if (f.service === 'schedules' && f.targetId && f.targetId !== props.functionId) {
      e.step = 'Schedules may run only this function. Cancel, remove this permission and add a new one.'
    }
    const built = buildStatement(formToInput(f, props.functionId))
    if (!built.ok) {
      const { actions, context, ...fields } = built.errors
      for (const [k, v] of Object.entries(fields)) if (!e[k]) e[k] = v
      if (!e.step && (actions || context)) e.step = actions || context
    }
    if (form.value.service === 'functions' && !showManual.value && !form.value.targetId) {
      e.targetId = 'Choose a function, or enter its function ID.'
    }
  }
  return e
}

function enterStep(n) {
  const f = form.value
  // Only an unchosen scope gets a starting point. A chosen one is never rewritten.
  if (n === 3 && f.service === 'storage' && !f.mode) f.mode = listPicked.value ? 'prefix' : 'key'
  if (n === 3 && f.service === 'schedules' && !f.mode) f.mode = 'name'
  errors.value = {}
  step.value = n
}

const built = computed(() => (step.value === 4 ? buildStatement(formToInput(form.value, props.functionId)) : null))

function next() {
  if (props.disabled) return
  const e = validate(step.value)
  if (Object.keys(e).length) {
    errors.value = e
    return
  }
  if (step.value < 4) {
    enterStep(step.value + 1)
    return
  }
  // Every step is checked again, so nothing reaches the draft that a step refused.
  const all = { ...validate(1), ...validate(2), ...validate(3) }
  if (Object.keys(all).length || !built.value || !built.value.ok) {
    errors.value = { step: all.step || 'This permission is not valid. Go back and check each step.' }
    return
  }
  emit('commit', built.value.statement)
}
function back() {
  if (!props.disabled && step.value > (props.initial ? 2 : 1)) enterStep(step.value - 1)
}

const statement = computed(() => (built.value && built.value.ok ? built.value.statement : null))
const detail = computed(() => (statement.value ? describeStatement(statement.value) : null))
const targetLabel = computed(() => {
  const id = detail.value && detail.value.targetId
  const info = id ? targetInfo({ targets: props.targets }, id) : null
  return info && info.resolved ? info.label : ''
})
const selectorMode = computed(() => (detail.value && detail.value.selector.mode === 'prefix' ? 'prefix' : 'exact'))
const sentence = computed(() => (statement.value ? humanStatement(statement.value, targetLabel.value, props.functionId) : 'This permission is not valid yet.'))
const notes = computed(() => {
  const out = []
  const f = form.value
  if (needsGet.value) out.push('Incomplete: Increment and Compare and set also need Read a key (kv.get) on the same keys. Go back and tick Read.')
  if (f.service === 'functions') out.push('This is one half. A call works only if the other function also accepts this one as a caller.')
  if (startup.value) out.push('Startup access runs when the function loads, before any request. It can only read and list.')
  if (f.service === 'storage' && f.mode === 'all') out.push("This covers every key in this function's storage, including keys added later.")
  if (f.service === 'schedules' && f.mode === 'all') out.push('This covers every schedule that runs this function, including ones added later.')
  return out
})
</script>
