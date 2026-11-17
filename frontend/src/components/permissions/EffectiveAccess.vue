<template>
  <section
    class="space-y-4"
    aria-label="Effective access"
  >
    <header class="space-y-2">
      <h1 class="text-xl font-semibold tracking-tight text-foreground-strong">
        Permissions
      </h1>
      <h2 class="text-lg font-semibold text-foreground-strong">
        Effective access
      </h2>
      <p class="max-w-prose text-sm text-foreground-muted">
        Combined inline and enabled, non-revoked pinned sources for {{ source.function.name }}.
        Removing one source leaves any overlapping grants from the others.
      </p>
      <p
        role="status"
        class="max-w-prose text-sm text-foreground"
      >
        {{ stateText }}
      </p>
      <p class="max-w-prose text-xs text-foreground-muted">
        Execution grants apply during a request. Startup grants apply only while loading the function, before a request.
        An accepted caller still needs its own outbound allowance. This snapshot is not proof of any particular invocation.
      </p>
    </header>
    <template v-if="valid">
      <section
        class="rounded-lg border border-border bg-surface"
        aria-label="Effective platform access"
      >
        <h3 class="border-b border-border px-4 py-3 text-sm font-semibold text-foreground-strong">
          {{ active ? 'What this function may do now' : unconfirmed ? 'Last known platform grants, not currently verified' : 'Platform access without active approval' }}
        </h3>
        <AccessSummary
          :function-id="source.function.id"
          :statements="active ? policy.allow : []"
          :callers="active ? policy.inbound : []"
          :unconfirmed="confirmation ? 'stale' : ''"
        />
      </section>
      <section
        class="rounded-lg border border-border bg-surface"
        aria-label="Effective caller acceptance"
      >
        <h3 class="border-b border-border px-4 py-3 text-sm font-semibold text-foreground-strong">
          {{ active ? 'Who may call this function now' : unconfirmed ? 'Last known callers, not currently verified' : 'Caller acceptance without active approval' }}
        </h3>
        <InboundCallers
          :owner-id="source.function.id"
          :callers="active ? policy.inbound : []"
          :unconfirmed="confirmation ? 'stale' : ''"
        />
      </section>
      <section
        v-if="!active && (policy.allow.length || policy.inbound.length)"
        class="space-y-2"
        :aria-label="retainedTitle"
      >
        <h3 class="text-sm font-semibold text-foreground-strong">
          {{ retainedTitle }}
        </h3>
        <p class="text-sm text-foreground-muted">
          Retained for inspection only. The allowances below do not establish current access.
        </p>
        <ul class="space-y-2 text-sm">
          <li
            v-for="(statement, i) in policy.allow"
            :key="i"
          >
            {{ retainedInactive ? 'Stored allowance, not active' : 'Stored allowance, active state not confirmed' }}: {{ statementSummary(statement) }}
          </li>
          <li
            v-for="caller in policy.inbound"
            :key="caller.caller_id"
            class="break-all"
          >
            {{ retainedInactive ? 'Stored caller, not active' : 'Stored caller, active state not confirmed' }}: {{ caller.caller_id }} ({{ caller.triggers.join(', ') }}).
          </li>
        </ul>
      </section>
    </template>
  </section>
</template>

<script setup>
import { computed } from 'vue'
import AccessSummary from './AccessSummary.vue'
import InboundCallers from './InboundCallers.vue'
import { statementSummary, validatePolicy } from '@/utils/permissionsModel.js'

const props = defineProps({
  source: { type: Object, required: true },
  unconfirmed: { type: String, default: '' },
})
const validation = computed(() => validatePolicy(props.source.effective_policy))
const valid = computed(() => validation.value.ok)
const policy = computed(() => validation.value.policy)
const active = computed(() => {
  const h = props.source.host
  return valid.value && !props.unconfirmed && h.enforcement === 'scratch_opt_in'
    && !h.review_required && !h.quarantined && !!h.current_artifact
    && h.current_artifact === h.approved_artifact
})
const stateText = computed(() => {
  const h = props.source.host
  if (props.unconfirmed) return 'Stale snapshot. Current access is not confirmed. Last known stored grants and approval are not currently verified; refreshing does not apply your draft.'
  if (!valid.value) return 'Effective permissions are unavailable. No access is inferred from unreadable policy data.'
  if (h.quarantined) return 'No effective grants are active: this function is quarantined. Stored sources are retained, not in force.'
  if (h.review_required) return 'No effective grants are active: the combined permissions require review. Stored sources are retained, not in force.'
  if (h.enforcement !== 'scratch_opt_in') return 'Enforcement is not confirmed by this scratch host. Stored sources do not prove current access.'
  if (!h.current_artifact || h.current_artifact !== h.approved_artifact) return 'No effective grants are confirmed for the current artifact. Approval is missing, unknown or no longer matches.'
  return 'The combined source revision is approved for the reported current artifact. Protected requests still check identity, readiness and execution scope.'
})
const confirmation = computed(() => props.unconfirmed
  || (props.source.host.enforcement !== 'scratch_opt_in' || !props.source.host.current_artifact ? 'error' : ''))
const retainedInactive = computed(() => !props.unconfirmed && (props.source.host.quarantined
  || props.source.host.review_required || (props.source.host.enforcement === 'scratch_opt_in'
    && !!props.source.host.current_artifact && props.source.host.current_artifact !== props.source.host.approved_artifact)))
const retainedTitle = computed(() => retainedInactive.value
  ? 'Stored effective grants, not active' : 'Stored effective grants, active state not confirmed')
</script>
