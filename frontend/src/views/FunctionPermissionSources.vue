<template>
  <div class="space-y-6">
    <header>
      <h1 class="text-xl font-semibold tracking-tight text-foreground-strong">
        Permissions
      </h1>
      <p class="mt-1.5 measure text-sm leading-body text-foreground-muted">
        {{ source ? `Platform access for ${source.function.name}.` : 'Platform access for this function.' }}
      </p>
    </header>
    <EffectiveAccess
      v-if="source"
      :source="source"
      :unconfirmed="unconfirmed"
    />
    <section
      v-if="approval"
      class="flex flex-wrap items-center justify-between gap-3 rounded-md border border-warning-ring bg-warning-tint px-4 py-3"
      aria-label="Approval"
      data-testid="permissions-approval"
    >
      <p class="measure text-sm text-foreground">
        <span class="font-medium text-foreground-strong">Not active yet.</span>
        {{ approval.text }}
        <span
          v-if="approveStale"
          role="alert"
          class="block text-warning-fg"
        >{{ approveStale }}</span>
      </p>
      <Button
        v-if="approval.canApprove"
        :disabled="!!approveReason || approving"
        :title="approveReason"
        data-testid="permissions-approve"
        @click="approve"
      >
        Review and approve
      </Button>
    </section>
    <section
      v-if="source"
      class="space-y-4"
      aria-label="Attached policies and effective access"
    >
      <h2 class="text-lg font-semibold text-foreground-strong">
        Permission sources
      </h2>
      <p class="measure text-sm leading-body text-foreground-muted">
        Removing a source keeps overlapping grants.
      </p>
      <p
        v-if="unconfirmed"
        role="status"
        class="text-sm text-warning-fg"
      >
        Stale source snapshot. Sources and approvals are unverified.
      </p>
      <Button
        variant="secondary"
        :disabled="actions.loading.value || actions.busy.value"
        @click="actions.refresh()"
      >
        Refresh effective access
      </Button>
      <ul class="space-y-4">
        <li class="space-y-2 border-t border-border pt-4">
          <h3 class="text-sm font-semibold text-foreground-strong">
            Inline source
          </h3>
          <p class="text-xs text-foreground-muted">
            Edited in the guided editor below.
          </p>
          <p
            v-if="!source.inline_policy.allow.length && !source.inline_policy.inbound.length"
            class="text-sm text-foreground"
          >
            No inline grants.
          </p>
          <ul class="space-y-2 text-sm">
            <li
              v-for="(statement, i) in source.inline_policy.allow"
              :key="i"
            >
              Inline allowance: {{ statementSummary(statement) }}
            </li>
            <li
              v-for="caller in source.inline_policy.inbound"
              :key="caller.caller_id"
              class="break-all"
            >
              Inline caller: {{ caller.caller_id }} ({{ caller.triggers.join(', ') }}).
            </li>
          </ul>
        </li>
        <li
          v-for="a in source.attachments"
          :key="a.policy_id"
          class="space-y-3 border-t border-border pt-4"
        >
          <div class="flex flex-wrap items-center justify-between gap-3">
            <Button
              variant="ghost"
              @click="$emit('policy', a.policy_id)"
            >
              {{ a.name }} · Version {{ a.version }}
            </Button>
            <span class="text-xs text-foreground-muted">{{ unconfirmed ? 'Last known source state (unverified)' : a.revoked ? 'Revoked, not contributing' : a.enabled ? 'Enabled source' : 'Disabled, not contributing' }}</span>
            <Button
              v-if="source.host.can_manage"
              variant="secondary"
              :disabled="!!changeReason"
              :title="changeReason"
              @click="prepareDetach(a)"
            >
              Review detach
            </Button>
          </div>
          <p class="text-xs text-foreground-muted">
            Counts only while enabled, not revoked and approved.
          </p>
          <ul class="space-y-2 text-sm">
            <li
              v-for="(statement, i) in a.policy.allow"
              :key="i"
            >
              Source allowance: {{ statementSummary(statement) }}
            </li>
            <li
              v-for="caller in a.policy.inbound"
              :key="caller.caller_id"
              class="break-all"
            >
              Source caller: {{ caller.caller_id }} ({{ caller.triggers.join(', ') }}).
            </li>
            <li v-if="!a.policy.allow.length && !a.policy.inbound.length">
              No grants.
            </li>
          </ul>
        </li>
      </ul>
      <p
        v-if="!source.attachments.length"
        class="text-sm text-foreground-muted"
      >
        No named policies attached.
      </p>
      <Button
        v-if="source.host.can_manage"
        variant="secondary"
        :disabled="!!changeReason"
        :title="changeReason"
        @click="$emit('policy', '')"
      >
        Manage policy attachments
      </Button>
      <p
        v-if="changeReason"
        role="status"
        class="text-sm text-foreground-muted"
      >
        {{ changeReason }}
      </p>
      <p
        v-if="actions.error.value"
        role="alert"
        class="text-sm text-danger-fg"
      >
        {{ actions.error.value }}
      </p>
      <section
        v-if="detach && !unconfirmed"
        class="space-y-3 border-t border-border pt-4"
        aria-label="Review detach"
      >
        <h3 class="text-sm font-semibold text-foreground-strong">
          Effective access after detaching {{ detach.name }}
        </h3>
        <p class="text-sm text-foreground-muted">
          Only {{ detach.name }} is removed. Other sources keep their grants. Preview only, not approval.
        </p>
        <StatementList
          :function-id="functionId"
          :statements="detach.impact.owners[0].after.allow"
        />
        <InboundCallers
          :owner-id="functionId"
          :callers="detach.impact.owners[0].after.inbound"
        />
        <Button
          variant="danger"
          :loading="actions.busy.value"
          :disabled="!!changeReason"
          :title="changeReason"
          @click="applyDetach"
        >
          Detach reviewed policy
        </Button>
        <Button
          variant="ghost"
          @click="detach = null"
        >
          Cancel
        </Button>
      </section>
      <details>
        <summary class="cursor-pointer text-sm text-link">
          Read-only effective policy JSON
        </summary>
        <pre class="mt-3 overflow-x-auto text-xs">{{ JSON.stringify(source.effective_policy, null, 2) }}</pre>
      </details>
      <p class="border-t border-border pt-4 text-sm text-foreground-muted">
        The editor below changes inline permissions only.
      </p>
    </section>
    <FunctionPermissions
      :function-id="functionId"
      v-bind="pageState"
      :preview="false"
      inline-only
      :authority-unconfirmed="inlineUnconfirmed"
      :read-only-reason="inlineReadOnlyReason"
      @retry="refresh"
      @save="mutateInline('save', $event)"
      @review="mutateInline('review', $event)"
    />
  </div>
</template>

<script setup>
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import Button from '@/components/common/Button.vue'
import StatementList from '@/components/permissions/StatementList.vue'
import InboundCallers from '@/components/permissions/InboundCallers.vue'
import EffectiveAccess from '@/components/permissions/EffectiveAccess.vue'
import { buildReviewPayload, parseCounter, statementSummary } from '@/utils/permissionsModel.js'
import { useConfirmStore } from '@/stores/confirm.js'
import FunctionPermissions from './FunctionPermissions.vue'
import { usePermissions } from '@/composables/usePermissions.js'
import { useIAMResource } from '@/composables/useIAMResource.js'
import { permissionsWire } from '@/api/permissions.js'
import { inlineSnapshot, sourceExpectation } from '@/api/managedPolicies.js'
const props = defineProps({ functionId: { type: String, required: true }, api: { type: Object, required: true } })
defineEmits(['policy'])
let lifetime = 0
let inlineOperation = 0
watch(() => props.functionId, () => { lifetime += 1; inlineOperation += 1 }, { flush: 'sync' })
const wrap = (raw) => ({ ...inlineSnapshot(raw), source: raw })
const actions = useIAMResource(() => props.functionId, (id, signal) => props.api.permissions(id, signal))
async function ownedMutation(kind, id, payload, signal) {
  const epoch = lifetime
  const operation = ++inlineOperation
  const raw = await props.api[kind](id, permissionsWire(payload, kind === 'review'), signal)
  const received = parseCounter(raw?.host?.revision)
  const expected = parseCounter(payload.expected.revision)
  if (epoch !== lifetime || operation !== inlineOperation || raw?.function?.id !== id
    || received === null || expected === null || received < expected) {
    throw new Error('Uncorrelated permissions acknowledgement.')
  }
  if (id === props.functionId) actions.expect(raw)
  return wrap(raw)
}
const controller = usePermissions(() => props.functionId, {
  get: async (id, signal) => wrap(await props.api.permissions(id, signal)),
  save: (id, payload, signal) => ownedMutation('inline', id, payload, signal),
  review: (id, payload, signal) => ownedMutation('review', id, payload, signal),
})
const source = computed(() => actions.data.value)
const pageState = computed(() => {
  const snapshot = controller.snapshot.value
  return {
    snapshot: snapshot ? Object.fromEntries(Object.entries(snapshot).filter(([key]) => key !== 'source')) : null,
    ...Object.fromEntries(['loading', 'loadError', 'saveError'].map((key) => [key, controller[key].value])),
    saving: controller.saving.value || actions.busy.value,
  }
})
const detach = ref(null)
const unconfirmed = computed(() => actions.fresh.value ? '' : 'stale')
const inlineUnconfirmed = computed(() => unconfirmed.value
  || (controller.snapshot.value?.host.revision !== source.value?.host.revision ? 'stale' : ''))
const inlineReadOnlyReason = computed(() => {
  if (!actions.fresh.value) return 'Authority unverified. Refresh before saving or reviewing.'
  if (!source.value?.host.can_manage) return 'Read only. Inline changes cannot be saved.'
  if (inlineUnconfirmed.value) return 'Inline baseline is out of date. Refresh, then reload or discard the draft.'
  return ''
})
const changeReason = computed(() => {
  if (!source.value?.host.can_manage) return 'Read only. An administrator must change these sources.'
  if (controller.saving.value || actions.busy.value) return 'An operation is in progress. Wait before changing sources.'
  if (controller.loading.value || actions.loading.value) return 'Refreshing. Wait for the current revision.'
  if (!actions.fresh.value) return 'Authority unverified. Refresh before changing sources.'
  if (controller.loadError.value || controller.saveError.value || actions.error.value) return 'Refresh and review the current revision before changing sources.'
  return ''
})
// The approve action reviews the stored combination for the current deployment;
// it lives here because this is where an inactive function is noticed.
const approval = computed(() => {
  const h = source.value?.host
  if (!h || !h.current_artifact || !['managed', 'scratch_opt_in'].includes(h.enforcement)) return null
  if (!h.review_required && !h.quarantined && h.current_artifact === h.approved_artifact) return null
  const why = h.quarantined ? 'This function is quarantined' : 'This deployment\'s permissions have not been approved'
  if (!h.can_manage) return { canApprove: false, text: `${why}. An administrator must review and approve them.` }
  return { canApprove: true, text: `${why}. Approving turns on the stored grants below for the current deployment.` }
})
const approveReason = computed(() => {
  if (changeReason.value) return changeReason.value
  if (controller.snapshot.value?.host.revision !== source.value?.host.revision) return 'Refresh before approving.'
  return ''
})
const approving = ref(false)
const approveStale = ref('')
// The confirmation dialog is async: approve only the owner, page lifetime and
// exact expectations the operator saw when it opened.
async function approve() {
  if (approveReason.value || approving.value) return
  const r = buildReviewPayload(controller.snapshot.value)
  if (!r.ok) return
  const seen = { epoch: lifetime, owner: props.functionId, expected: JSON.stringify(r.payload.expected) }
  approving.value = true
  approveStale.value = ''
  try {
    const ok = await useConfirmStore().ask({
      title: `Approve ${source.value.function.name}?`,
      message: 'The stored grants listed on this page become active for the current deployment.',
      confirmLabel: 'Approve',
    })
    if (!ok || seen.epoch !== lifetime || seen.owner !== props.functionId) return
    const now = buildReviewPayload(controller.snapshot.value)
    if (approveReason.value || !now.ok || JSON.stringify(now.payload.expected) !== seen.expected) {
      approveStale.value = 'Permissions changed while you were confirming. Review them again.'
      return
    }
    await mutateInline('review', r.payload)
  } finally {
    if (seen.epoch === lifetime) approving.value = false
  }
}
watch(source, () => { detach.value = null })
watch(() => props.functionId, () => { detach.value = null; approving.value = false; approveStale.value = '' })
function refresh() { detach.value = null; void actions.refresh(); void controller.refresh() }
async function refreshInspector(epoch = lifetime) {
  await nextTick()
  await nextTick()
  if (epoch === lifetime) await actions.refresh(true)
}
async function mutateInline(kind, payload) {
  const epoch = lifetime
  actions.invalidate()
  await controller[kind](payload)
  if (epoch === lifetime) await refreshInspector(epoch)
}
const unsubscribe = props.api.subscribeMutations?.((pending) => {
  actions.invalidate()
  if (!pending) void refreshInspector()
})
onBeforeUnmount(() => { lifetime += 1; unsubscribe?.() })
async function prepareDetach(attachment) {
  if (changeReason.value) return
  detach.value = null
  const owner = sourceExpectation(source.value)
  const result = await actions.mutate(async (signal) => {
    const policy = await props.api.get(attachment.policy_id, signal)
    const body = { membership_revision: policy.policy.membership_revision, owners: [owner], confirm: true }
    const impact = await props.api.mutate(attachment.policy_id, 'impact', { operation: 'detach', ...body }, signal)
    return { id: attachment.policy_id, name: attachment.name, body, impact }
  })
  if (result) detach.value = result
}
async function applyDetach() {
  if (changeReason.value || !detach.value) return
  const plan = detach.value
  detach.value = null
  const result = await actions.mutate((signal) => props.api.mutate(plan.id, 'detach', plan.body, signal))
  if (result) await actions.refresh()
}
</script>
