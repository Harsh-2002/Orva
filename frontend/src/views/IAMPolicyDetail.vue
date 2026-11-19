<template>
  <section
    v-if="state.data.value"
    class="space-y-5 border-t border-border pt-6"
    aria-label="Policy details"
  >
    <header class="flex flex-wrap items-start justify-between gap-4">
      <div class="min-w-0 space-y-2">
        <h2 class="text-lg font-semibold text-foreground-strong">
          {{ state.data.value.policy.name }}
        </h2>
        <p
          v-if="!state.data.value.can_manage"
          class="text-sm text-foreground-muted"
        >
          Read only. An administrator must change permissions.
        </p>
      </div>
      <Button
        variant="secondary"
        :disabled="state.busy.value"
        @click="reviewed = null; state.refresh()"
      >
        Refresh policy
      </Button>
    </header>
    <div class="flex flex-wrap items-start gap-x-5 gap-y-2">
      <details>
        <summary class="cursor-pointer text-sm text-link">
          Policy ID
        </summary>
        <div class="mt-2 flex flex-wrap items-center gap-2">
          <code class="break-all text-xs text-foreground-muted">{{ policyId }}</code>
          <Button
            size="xs"
            variant="secondary"
            class="touch-expand-xs"
            @click="copyPolicyId"
          >
            {{ copied ? 'Copied' : 'Copy' }}
          </Button>
        </div>
      </details>
      <details>
        <summary class="cursor-pointer text-sm text-link">
          Versions
        </summary>
        <p class="measure mt-2 text-sm text-foreground-muted">
          {{ state.fresh.value ? 'Latest verified version' : 'Last known version (unverified)' }} {{ state.data.value.policy.latest_version }}. Attached functions keep their reviewed version until explicit rollout.
        </p>
      </details>
    </div>
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
        <div class="space-y-3 border-t border-border pt-4">
          <h3 class="text-sm font-semibold text-foreground-strong">
            Attach or roll out a version
          </h3>
          <fieldset
            :disabled="!!changeReason"
            aria-label="Attach or roll out a version"
            :aria-describedby="selected.length ? undefined : attachHintId"
            class="space-y-2"
          >
            <Input
              v-model="version"
              label="Pinned version"
              hint="An existing immutable version number. No automatic follow-latest."
              @update:model-value="versionTouched = true"
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
              Attach…
            </Button>
            <p
              v-if="!selected.length"
              :id="attachHintId"
              class="text-sm text-foreground-muted"
            >
              Select at least one function.
            </p>
          </fieldset>
        </div>
      </template>
      <p
        v-if="attached.length"
        role="status"
        class="text-sm text-success-fg"
      >
        Attached to <template
          v-for="(id, index) in attached"
          :key="id"
        >
          <RouterLink
            :to="`/functions/${id}/permissions`"
            class="text-link hover:underline underline-offset-4"
          >{{ label(id) }}</RouterLink>{{ index < attached.length - 1 ? ', ' : '' }}
        </template>. {{ attached.length === 1 ? 'Check its Permissions page and approve it there if it is not active yet.' : 'Check each Permissions page and approve any function that is not active yet.' }}
      </p>
      <div
        v-if="state.data.value.can_manage && (!state.data.value.policy.revoked || !state.data.value.attachments.length)"
        class="flex flex-wrap gap-3 border-t border-border pt-4"
      >
        <Button
          v-if="!state.data.value.policy.revoked"
          variant="danger"
          :disabled="!!changeReason"
          :title="changeReason"
          @click="prepare('revoke')"
        >
          Revoke everywhere…
        </Button>
        <Button
          v-if="!state.data.value.attachments.length"
          variant="danger"
          :disabled="!!changeReason"
          :title="changeReason"
          @click="reviewed = { operation: 'delete', body: { definition_revision: state.data.value.policy.definition_revision, membership_revision: state.data.value.policy.membership_revision, confirm: true }, impact: null }"
        >
          Delete…
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
import { computed, ref, useId, watch } from 'vue'
import { RouterLink } from 'vue-router'
import Button from '@/components/common/Button.vue'
import Input from '@/components/common/Input.vue'
import StatementList from '@/components/permissions/StatementList.vue'
import InboundCallers from '@/components/permissions/InboundCallers.vue'
import ManagedPolicyDraft from '@/components/permissions/ManagedPolicyDraft.vue'
import { sourceExpectation } from '@/api/managedPolicies.js'
import { useIAMResource } from '@/composables/useIAMResource.js'
import { parseCounter } from '@/utils/permissionsModel.js'
import { copyText } from '@/utils/clipboard'
const props = defineProps({ policyId: { type: String, required: true }, api: { type: Object, required: true }, functions: { type: Array, default: () => [] } })
const emit = defineEmits(['deleted', 'changed', 'attachments'])
const state = useIAMResource(() => props.policyId, (id, signal) => props.api.get(id, signal))
const changeReason = computed(() => {
  if (!state.data.value?.can_manage) return 'Read only. An administrator must change this policy.'
  if (state.busy.value || state.loading.value) return 'A permissions operation is in progress. Wait for the current revision.'
  if (!state.fresh.value) return 'Current policy state is unverified. Refresh before changing it.'
  if (state.error.value) return 'Refresh this policy and review the current revision before changing it.'
  return ''
})
const selected = ref([])
const version = ref('')
const versionTouched = ref(false)
// Prefill the verified latest version for convenience; once edited, the
// operator's explicit pin wins and no read ever overwrites it (no follow-latest).
watch(
  () => state.data.value?.policy.latest_version,
  (latest) => {
    if (latest != null && !versionTouched.value) version.value = String(latest)
  },
  { immediate: true }
)
const reviewed = ref(null)
const editing = ref(false)
const editingRevision = ref('')
const copied = ref(false)
async function copyPolicyId() { copied.value = await copyText(props.policyId) }
const attached = ref([])
const attachHintId = useId()
// The list rows have no attachment data of their own, so the detail reports its
// verified count for the selected policy. The id travels with the count so a
// late emission can never label another policy, and a stale/refreshing snapshot
// clears the count instead of leaving an unverified one behind.
const attachmentCount = computed(() => (
  state.fresh.value && Number.isInteger(state.data.value?.attachments?.length)
    ? state.data.value.attachments.length
    : null
))
watch(attachmentCount, (count) => emit('attachments', { id: props.policyId, count }), { immediate: true })
function startEdit() {
  if (changeReason.value) return
  editingRevision.value = state.data.value.policy.definition_revision
  editing.value = true
}
const label = (id) => props.functions.find((f) => f.id === id)?.name || id
watch(() => props.policyId, () => { selected.value = []; reviewed.value = null; editing.value = false; copied.value = false; attached.value = [] })
async function update({ policy }) {
  if (changeReason.value) return
  const result = await state.mutate((signal) => props.api.mutate(props.policyId, 'update', { definition_revision: editingRevision.value, policy }, signal))
  if (result) { editing.value = false; emit('changed') }
}
async function prepare(operation) {
  if (changeReason.value) return
  reviewed.value = null
  attached.value = []
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
  if (result) {
    if (plan.operation === 'attach') { selected.value = []; attached.value = plan.body.owners.map((owner) => owner.function_id) }
    emit('changed')
  }
  if (result && plan.operation === 'delete') emit('deleted')
}
</script>
