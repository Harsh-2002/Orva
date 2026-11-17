<template>
  <section
    v-if="state.data.value"
    class="space-y-5 border-t border-border pt-6"
    aria-label="Policy details"
  >
    <header class="space-y-2">
      <Button
        variant="secondary"
        :disabled="state.busy.value"
        @click="reviewed = null; state.refresh()"
      >
        Refresh policy
      </Button>
      <h2 class="text-lg font-semibold text-foreground-strong">
        {{ state.data.value.policy.name }}
      </h2>
      <code class="block break-all text-xs text-foreground-muted">{{ policyId }}</code>
      <p class="text-sm text-foreground-muted">
        {{ state.fresh.value ? 'Latest verified version' : 'Last known version (unverified)' }} {{ state.data.value.policy.latest_version }}. Attached functions keep their reviewed version until explicit rollout.
      </p>
      <p
        v-if="!state.data.value.can_manage"
        class="text-sm text-foreground-muted"
      >
        Read only. An administrator must change permissions.
      </p>
    </header>
    <p
      v-if="!state.fresh.value"
      role="status"
      class="text-sm text-warning-fg"
    >
      Stale policy snapshot. Source membership, versions and approval are not currently verified. Changes wait for a new matching read.
    </p>
    <p
      v-if="state.error.value"
      role="alert"
      class="text-sm text-danger-fg"
    >
      {{ state.error.value }}
    </p>
    <ManagedPolicyDraft
      v-if="editing && state.data.value.can_manage"
      :initial="state.data.value.version.policy"
      existing
      :disabled="!!changeReason"
      @save="update"
      @cancel="editing = false"
    />
    <template v-else>
      <StatementList :statements="state.data.value.version.policy.allow" />
      <h3 class="text-sm font-semibold text-foreground-strong">
        Attached versions
      </h3>
      <p
        v-if="!state.data.value.attachments.length"
        class="text-sm text-foreground-muted"
      >
        Not attached to a function.
      </p>
      <ul class="space-y-2 text-sm">
        <li
          v-for="owner in state.data.value.attachments"
          :key="owner.owner_id"
          class="break-all"
        >
          {{ label(owner.owner_id) }} · Version {{ owner.version }} · {{ owner.enabled ? 'Enabled' : 'Disabled' }}
        </li>
      </ul>
      <template v-if="state.data.value.can_manage && !state.data.value.policy.revoked">
        <Button
          variant="secondary"
          :disabled="!!changeReason"
          :title="changeReason"
          @click="startEdit"
        >
          Create new version
        </Button>
        <fieldset
          :disabled="!!changeReason"
          class="space-y-2 border-t border-border pt-4"
        >
          <legend class="pt-4 text-sm font-semibold text-foreground-strong">
            Attach or roll out a version
          </legend>
          <Input
            v-model="version"
            label="Pinned version"
            hint="An existing immutable version number. No automatic follow-latest."
          />
          <label
            v-for="fn in functions"
            :key="fn.id"
            class="flex min-h-[44px] items-center gap-3 text-sm"
          >
            <input
              v-model="selected"
              type="checkbox"
              :value="fn.id"
              class="h-4 w-4 accent-primary"
            >
            <span class="min-w-0 break-all">{{ fn.name }}</span>
          </label>
          <Button
            :disabled="!selected.length || !!changeReason"
            :title="changeReason"
            @click="prepare('attach')"
          >
            Review attachment
          </Button>
        </fieldset>
      </template>
      <div
        v-if="state.data.value.can_manage"
        class="flex flex-wrap gap-3"
      >
        <Button
          v-if="!state.data.value.policy.revoked"
          variant="danger"
          :disabled="!!changeReason"
          :title="changeReason"
          @click="prepare('revoke')"
        >
          Review global revoke
        </Button>
        <Button
          v-if="!state.data.value.attachments.length"
          variant="danger"
          :disabled="!!changeReason"
          :title="changeReason"
          @click="reviewed = { operation: 'delete', body: { definition_revision: state.data.value.policy.definition_revision, membership_revision: state.data.value.policy.membership_revision, confirm: true }, impact: null }"
        >
          Review deletion
        </Button>
      </div>
      <section
        v-if="reviewed && state.data.value.can_manage"
        class="space-y-4 rounded-lg border border-border p-4"
        aria-label="Review policy change"
      >
        <h3 class="text-sm font-semibold text-foreground-strong">
          {{ reviewed.operation === 'attach' ? 'Review explicit attachment' : 'Review permanent removal' }}
        </h3>
        <p class="text-sm text-foreground-muted">
          {{ reviewed.operation === 'revoke' ? 'This policy ID will never grant access again. Other policy and inline sources remain.' : 'Only this source changes. Review residual access below before confirming.' }}
        </p>
        <div
          v-for="owner in reviewed.impact?.owners || []"
          :key="owner.function_id"
          class="space-y-2"
        >
          <p class="break-all text-sm">
            {{ label(owner.function_id) }}
          </p>
          <h4 class="text-xs font-semibold text-foreground-muted">
            Effective access after change
          </h4>
          <StatementList :statements="owner.after.allow" />
          <InboundCallers
            :owner-id="owner.function_id"
            :callers="owner.after.inbound"
          />
        </div>
        <Button
          :variant="reviewed.operation === 'attach' ? 'primary' : 'danger'"
          :loading="state.busy.value"
          :disabled="!!changeReason"
          :title="changeReason"
          @click="apply"
        >
          {{ reviewed.operation === 'attach' ? 'Attach reviewed version' : reviewed.operation === 'revoke' ? 'Revoke policy permanently' : 'Delete unattached policy' }}
        </Button>
        <Button
          variant="ghost"
          :disabled="state.busy.value"
          @click="reviewed = null"
        >
          Cancel
        </Button>
      </section>
      <details class="border-t border-border pt-4">
        <summary class="cursor-pointer text-sm text-link">
          Advanced: JSON and audit history
        </summary>
        <pre class="mt-3 overflow-x-auto text-xs">{{ JSON.stringify(state.data.value, null, 2) }}</pre>
      </details>
    </template>
  </section>
  <p
    v-else
    role="status"
    class="text-sm text-foreground-muted"
  >
    {{ state.error.value || 'Loading policy…' }}
  </p>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import Button from '@/components/common/Button.vue'
import Input from '@/components/common/Input.vue'
import StatementList from '@/components/permissions/StatementList.vue'
import InboundCallers from '@/components/permissions/InboundCallers.vue'
import ManagedPolicyDraft from '@/components/permissions/ManagedPolicyDraft.vue'
import { sourceExpectation } from '@/api/managedPolicies.js'
import { useIAMResource } from '@/composables/useIAMResource.js'
import { parseCounter } from '@/utils/permissionsModel.js'
const props = defineProps({ policyId: { type: String, required: true }, api: { type: Object, required: true }, functions: { type: Array, default: () => [] } })
const emit = defineEmits(['deleted', 'changed'])
const state = useIAMResource(() => props.policyId, (id, signal) => props.api.get(id, signal))
const changeReason = computed(() => {
  if (!state.data.value?.can_manage) return 'Read only. An administrator must change this policy.'
  if (state.busy.value || state.loading.value) return 'A permissions operation is in progress. Wait for the current revision.'
  if (!state.fresh.value) return 'Current policy state is unverified. Refresh before changing it.'
  if (state.error.value) return 'Refresh this policy and review the current revision before changing it.'
  return ''
})
const selected = ref([])
const version = ref('1')
const reviewed = ref(null)
const editing = ref(false)
const editingRevision = ref('')
function startEdit() {
  if (changeReason.value) return
  editingRevision.value = state.data.value.policy.definition_revision
  editing.value = true
}
const label = (id) => props.functions.find((f) => f.id === id)?.name || id
watch(() => props.policyId, () => { selected.value = []; reviewed.value = null; editing.value = false })
async function update({ policy }) {
  if (changeReason.value) return
  const result = await state.mutate((signal) => props.api.mutate(props.policyId, 'update', { definition_revision: editingRevision.value, policy }, signal))
  if (result) { editing.value = false; emit('changed') }
}
async function prepare(operation) {
  if (changeReason.value) return
  reviewed.value = null
  if (operation === 'attach' && parseCounter(version.value) === null) { state.error.value = 'Enter a valid existing version number.'; return }
  const p = state.data.value.policy
  const ids = operation === 'revoke' ? state.data.value.attachments.map((a) => a.owner_id) : selected.value
  const result = await state.mutate(async (signal) => {
    const owners = await Promise.all(ids.map(async (id) => sourceExpectation(await props.api.permissions(id, signal))))
    const body = { membership_revision: p.membership_revision, owners }
    if (operation === 'attach') body.version = version.value
    else { body.definition_revision = p.definition_revision; body.confirm = true }
    const impact = await props.api.mutate(props.policyId, 'impact', { operation, ...body }, signal)
    return { operation, body, impact }
  })
  if (result) reviewed.value = result
}
async function apply() {
  if (changeReason.value || !reviewed.value) return
  const plan = reviewed.value
  reviewed.value = null
  const result = await state.mutate((signal) => props.api.mutate(props.policyId, plan.operation, plan.body, signal))
  if (result) emit('changed')
  if (result && plan.operation === 'delete') emit('deleted')
}
</script>
