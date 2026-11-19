<template>
  <section
    class="space-y-5"
    aria-label="Managed policy draft"
  >
    <header>
      <h2 class="text-lg font-semibold text-foreground-strong">
        {{ existing ? 'Create a new version' : 'Create a reusable policy' }}
      </h2>
      <p class="mt-1 text-sm text-foreground-muted">
        Own resources belong to each attached function. Saving a version does not attach it or change existing pins.
      </p>
    </header>
    <Input
      v-if="!existing"
      v-model="name"
      label="Policy name"
      :disabled="disabled"
    />
    <PermissionWizard
      v-if="wizard"
      function-id=""
      managed
      :targets="targets"
      :disabled="disabled"
      @cancel="wizard = false"
      @commit="add"
    />
    <template v-else>
      <StatementList
        :statements="draft.allow"
        :targets="targets"
      />
      <div
        v-if="!reviewing"
        class="flex flex-wrap gap-3"
      >
        <Button
          v-for="(_, index) in draft.allow"
          :key="index"
          variant="ghost"
          :disabled="disabled"
          @click="draft = { ...draft, allow: draft.allow.filter((_, i) => i !== index) }"
        >
          Remove permission {{ index + 1 }}
        </Button>
      </div>
      <p
        v-if="draft.inbound.length"
        class="text-sm text-foreground-muted"
      >
        Existing caller acceptance is retained in this version.
      </p>
      <p
        v-if="problem"
        role="alert"
        class="text-sm text-danger-fg"
      >
        {{ problem }}
      </p>
      <div class="flex flex-wrap gap-3">
        <Button
          v-if="!reviewing"
          variant="secondary"
          :disabled="disabled"
          @click="wizard = true"
        >
          Add permission
        </Button>
        <Button
          v-if="!reviewing"
          :disabled="disabled"
          @click="review"
        >
          Review and save
        </Button>
        <Button
          v-if="reviewing"
          :disabled="disabled"
          :loading="disabled"
          @click="$emit('save', { name, policy: draft })"
        >
          Save policy
        </Button>
        <Button
          v-if="reviewing"
          variant="secondary"
          :disabled="disabled"
          @click="reviewing = false"
        >
          Back to draft
        </Button>
        <Button
          variant="ghost"
          :disabled="disabled"
          @click="$emit('cancel')"
        >
          Cancel
        </Button>
      </div>
      <p
        v-if="reviewing"
        role="status"
        class="text-sm text-foreground"
      >
        Review the complete policy above. This version is immutable after saving. Attachment and rollout require a separate review.
      </p>
      <details class="border-t border-border pt-4">
        <summary class="cursor-pointer text-sm text-link">
          Advanced: read-only JSON
        </summary>
        <pre class="mt-3 overflow-x-auto text-xs">{{ JSON.stringify(draft, null, 2) }}</pre>
      </details>
    </template>
  </section>
</template>

<script setup>
import { ref } from 'vue'
import Input from '@/components/common/Input.vue'
import Button from '@/components/common/Button.vue'
import PermissionWizard from './PermissionWizard.vue'
import StatementList from './StatementList.vue'
import { validatePolicy } from '@/utils/permissionsModel.js'

const props = defineProps({ initial: { type: Object, default: () => ({ schema_version: 1, allow: [], inbound: [] }) }, targets: { type: Array, default: () => [] }, existing: Boolean, disabled: Boolean })
defineEmits(['save', 'cancel'])
const name = ref('')
const draft = ref(JSON.parse(JSON.stringify(props.initial)))
const wizard = ref(false)
const reviewing = ref(false)
const problem = ref('')
function add(statement) {
  const checked = validatePolicy({ ...draft.value, allow: [...draft.value.allow, statement] })
  if (!checked.ok) { problem.value = checked.problems.join(' '); return }
  draft.value = checked.policy
  wizard.value = false
  problem.value = ''
}
function review() {
  if (!props.existing && (!name.value || name.value.trim() !== name.value || name.value.length > 128)) { problem.value = 'Enter a policy name of 1 to 128 characters without surrounding spaces.'; return }
  const checked = validatePolicy(draft.value)
  if (!checked.ok) { problem.value = checked.problems.join(' '); return }
  problem.value = ''
  reviewing.value = true
}
</script>
