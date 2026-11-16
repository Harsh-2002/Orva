<template>
  <div class="space-y-6">
    <!-- Page header -->
    <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div class="min-w-0">
        <h1 class="text-xl font-semibold tracking-tight text-foreground-strong">
          Permissions
        </h1>
        <p class="mt-1.5 max-w-prose text-sm leading-body text-foreground-muted">
          What
          <span class="font-medium text-foreground-strong">{{ displayName }}</span>
          may do with platform actions, and which callers it accepts.
        </p>
        <code class="mt-1 block break-all font-mono text-xs text-foreground-strong">{{ functionId }}</code>
      </div>
      <div class="flex flex-wrap items-center gap-2 sm:shrink-0">
        <Button
          variant="secondary"
          :disabled="loading || busy"
          @click="$emit('retry')"
        >
          <RefreshCw
            class="h-4 w-4"
            aria-hidden="true"
          />
          Refresh
        </Button>
      </div>
    </div>

    <p
      v-if="preview"
      role="status"
      class="flex items-start gap-3 rounded-md border border-info-ring bg-info-tint px-4 py-3 text-xs text-info-fg"
    >
      <Info
        class="mt-0.5 h-4 w-4 shrink-0"
        aria-hidden="true"
      />
      UI preview. No permissions are applied.
    </p>

    <LoadError
      v-if="loadError"
      what="Permissions"
      :message="loadError"
      :on-retry="() => $emit('retry')"
    />

    <p
      v-if="view === 'loading'"
      class="text-sm text-foreground-muted"
    >
      Loading permissions for {{ displayName }}.
    </p>

    <div
      v-else-if="view === 'empty' || view === 'unavailable' || view === 'other-owner' || view === 'no-access'"
      class="rounded-lg border border-border bg-surface px-6 py-10 text-center"
    >
      <h2 class="text-sm font-semibold text-foreground-strong">
        {{ blockTitle }}
      </h2>
      <p class="mx-auto mt-1.5 max-w-prose text-sm text-foreground-muted">
        {{ blockText }}
      </p>
    </div>

    <template v-else-if="view === 'ready'">
      <!-- State line: words, never colour alone -->
      <div
        class="flex flex-wrap items-center gap-x-2 gap-y-2 border-y border-border py-3"
        aria-label="Permission state"
        role="group"
      >
        <Badge
          size="sm"
          :variant="enforcementBadge.variant"
        >
          {{ enforcementBadge.text }}
        </Badge>
        <Badge
          size="sm"
          :variant="reviewBadge.variant"
        >
          {{ reviewBadge.text }}
        </Badge>
        <Badge
          v-if="!canEdit"
          size="sm"
          variant="gray"
        >
          Read only
        </Badge>
        <span class="text-xs text-foreground">
          Revision <code class="font-mono text-foreground-strong">{{ latest.host.revision }}</code>
        </span>
      </div>

      <p
        v-if="enforcementNotice"
        role="status"
        class="flex items-start gap-3 rounded-md border border-border bg-surface px-4 py-3 text-xs text-foreground"
      >
        <ShieldAlert
          class="mt-0.5 h-4 w-4 shrink-0"
          aria-hidden="true"
        />
        {{ enforcementNotice }}
      </p>
      <p
        v-if="latest.host.reviewRequired || latest.host.quarantined"
        role="status"
        class="flex items-start gap-3 rounded-md border border-border bg-surface px-4 py-3 text-xs text-foreground"
      >
        <ShieldAlert
          class="mt-0.5 h-4 w-4 shrink-0"
          aria-hidden="true"
        />
        Retained, not in force. {{ latest.host.quarantined ? 'This policy is quarantined' : 'This policy needs review' }};
        what is listed below is stored, not enforced.
      </p>
      <p
        v-if="lockNote"
        role="status"
        class="text-xs text-foreground"
      >
        {{ lockNote }}
      </p>

      <!-- Save and conflict messages -->
      <div
        v-if="saveError"
        role="alert"
        class="flex items-start gap-3 rounded-md border border-danger-ring bg-danger-tint px-4 py-3 text-xs"
      >
        <TriangleAlert
          class="mt-0.5 h-4 w-4 shrink-0 text-danger-fg"
          aria-hidden="true"
        />
        <div class="min-w-0 flex-1 space-y-1">
          <p class="leading-snug text-danger-fg">
            {{ saveErrorText }}
          </p>
          <p
            v-if="saveError.message"
            class="break-words font-mono text-[11px] text-foreground"
          >
            {{ saveError.message }}
          </p>
        </div>
        <Button
          v-if="saveError.kind === 'conflict'"
          variant="secondary"
          size="sm"
          @click="$emit('retry')"
        >
          Refresh
        </Button>
      </div>

      <div
        v-if="stale"
        role="alert"
        class="space-y-3 rounded-md border border-warning-ring bg-warning-tint px-4 py-3 text-xs"
      >
        <p class="leading-snug text-warning-fg">
          The server version changed while you were editing (revision
          <code class="font-mono">{{ base.fp.split('|')[0] }}</code> to
          <code class="font-mono">{{ latest.host.revision }}</code>). Saving is off. Your draft is kept.
        </p>
        <div class="flex flex-wrap gap-2">
          <Button
            variant="secondary"
            size="sm"
            :aria-expanded="compareOpen ? 'true' : 'false'"
            aria-controls="permissions-server-changes"
            @click="compareOpen = !compareOpen"
          >
            {{ compareOpen ? 'Hide comparison' : 'Compare with server' }}
          </Button>
          <Button
            variant="secondary"
            size="sm"
            :disabled="busy"
            @click="adopt"
          >
            Discard draft and reload
          </Button>
        </div>
        <ul
          id="permissions-server-changes"
          :hidden="!compareOpen"
          class="space-y-1.5 text-foreground"
          aria-label="Changes on the server since you started"
        >
          <li
            v-for="(c, i) in serverChanges"
            :key="i"
            class="flex min-w-0 gap-2"
          >
            <span class="w-14 shrink-0 font-medium text-foreground-strong">{{ c.kind }}</span>
            <span class="min-w-0 break-all">{{ c.text }}</span>
          </li>
          <li v-if="!serverChanges.length">
            No statements or callers changed. The artifact or approval changed.
          </li>
        </ul>
      </div>

      <!-- Stored policy cannot be read as a policy -->
      <section
        v-if="!shownPolicy"
        class="space-y-2 rounded-lg border border-border bg-surface px-4 py-4"
        aria-labelledby="perm-invalid-title"
      >
        <h2
          id="perm-invalid-title"
          class="text-sm font-semibold text-foreground-strong"
        >
          Stored policy unavailable
        </h2>
        <p class="text-sm text-foreground">
          The stored policy is not valid, so it is not shown as permissions and cannot be edited here.
        </p>
        <ul class="space-y-1 font-mono text-xs text-foreground">
          <li
            v-for="p in latest.policyProblems.slice(0, 5)"
            :key="p"
            class="break-words"
          >
            {{ p }}
          </li>
        </ul>
      </section>

      <template v-else>
        <!-- Outbound -->
        <section
          class="rounded-lg border border-border bg-surface"
          aria-labelledby="perm-outbound-title"
        >
          <header class="border-b border-border px-4 py-3">
            <h2
              id="perm-outbound-title"
              class="text-sm font-semibold text-foreground-strong"
            >
              What this function may do
            </h2>
            <p class="mt-1 max-w-prose text-xs text-foreground-muted">
              An allowance here is one half. A call to another function also needs that function to accept this one as a caller, so a listed allowance is not proof the call works.
            </p>
          </header>
          <StatementList
            :statements="shownPolicy.allow"
            :targets="latest.targets"
            :warnings="warnings"
            :editable="canEdit"
            :disabled="busy"
            @remove="onRemoveStatement"
          />
          <StatementForm
            v-if="canEdit"
            :key="statementFormKey"
            :problem="statementProblem"
            :disabled="busy"
            @add="onAddStatement"
          />
        </section>

        <!-- Inbound -->
        <section
          class="rounded-lg border border-border bg-surface"
          aria-labelledby="perm-inbound-title"
        >
          <header class="border-b border-border px-4 py-3">
            <h2
              id="perm-inbound-title"
              class="text-sm font-semibold text-foreground-strong"
            >
              Who may call this function
            </h2>
            <p class="mt-1 max-w-prose text-xs text-foreground-muted">
              Callers are matched by function ID. A caller also needs its own allowance to call this function.
            </p>
          </header>
          <InboundCallers
            :key="inboundFormKey"
            :callers="shownPolicy.inbound"
            :targets="latest.targets"
            :problem="inboundProblem"
            :editable="canEdit"
            :disabled="busy"
            @add="onAddInbound"
            @remove="onRemoveInbound"
          />
        </section>
      </template>

      <!-- Review -->
      <section
        class="rounded-lg border border-border bg-surface"
        aria-labelledby="perm-review-title"
      >
        <header class="border-b border-border px-4 py-3">
          <h2
            id="perm-review-title"
            class="text-sm font-semibold text-foreground-strong"
          >
            Review
          </h2>
        </header>
        <ReviewPanel
          :host="latest.host"
          :can-review="access.canReview"
          :dirty="dirty"
          :pending="reviewLatched"
          :disabled="busy || loading || !!loadError || reviewLatched"
          @review="onReview"
        />
      </section>

      <!-- History -->
      <section
        class="rounded-lg border border-border bg-surface"
        aria-labelledby="perm-history-title"
      >
        <header class="border-b border-border px-4 py-3">
          <h2
            id="perm-history-title"
            class="text-sm font-semibold text-foreground-strong"
          >
            History
          </h2>
        </header>
        <HistoryList :entries="latest.history" />
      </section>

      <SaveBar
        v-if="dirty"
        :count="diff.total"
        :changes="draftChanges"
        :hint="saveHint"
        :save-label="saveError && saveError.kind === 'error' ? 'Retry save' : 'Save permissions'"
        :can-save="canSave"
        :saving="busy"
        @save="onSave"
        @discard="adopt"
      />
    </template>
  </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { Info, RefreshCw, ShieldAlert, TriangleAlert } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import Badge from '@/components/common/Badge.vue'
import LoadError from '@/components/common/LoadError.vue'
import StatementList from '@/components/permissions/StatementList.vue'
import StatementForm from '@/components/permissions/StatementForm.vue'
import InboundCallers from '@/components/permissions/InboundCallers.vue'
import ReviewPanel from '@/components/permissions/ReviewPanel.vue'
import HistoryList from '@/components/permissions/HistoryList.vue'
import SaveBar from '@/components/permissions/SaveBar.vue'
import {
  addInbound, addStatement, buildReviewPayload, buildSavePayload, clonePolicy, deriveAccess,
  describeStatement, diffPolicies, hostFingerprint, normalizeSnapshot, policyWarnings,
  removeInbound, removeStatement, validatePolicy, TRIGGER_LABELS,
} from '@/utils/permissionsModel.js'

// Presentational. The page owns a local draft and emits events; it never
// fetches, never routes, and never treats a save as applied until the parent
// passes a fresh snapshot. `snapshot` is read through normalizeSnapshot and is
// never mutated.
const props = defineProps({
  functionId: { type: String, required: true },
  functionName: { type: String, default: '' },
  snapshot: { type: Object, default: null },
  loading: { type: Boolean, default: false },
  loadError: { type: String, default: null },
  saving: { type: Boolean, default: false },
  // { kind: 'conflict' | 'forbidden' | 'validation' | 'error', message }
  saveError: { type: Object, default: null },
  readOnlyReason: { type: String, default: '' },
  // Adds the synthetic-preview banner. It never widens capabilities.
  preview: { type: Boolean, default: false },
})

const emit = defineEmits(['retry', 'save', 'review', 'dirty-change'])

const normalized = computed(() => normalizeSnapshot(props.snapshot))
const latest = computed(() => (normalized.value.status === 'ready' ? normalized.value.snapshot : null))
const latestFp = computed(() => hostFingerprint(latest.value))
const access = computed(() => deriveAccess({
  snapshot: latest.value,
  functionId: props.functionId,
  loading: props.loading,
  loadError: props.loadError,
  saving: false,
  readOnlyReason: props.readOnlyReason,
}))
const forbidden = computed(() => !!props.saveError && props.saveError.kind === 'forbidden')
const canEdit = computed(() => access.value.canEditPolicy && !forbidden.value)

// base: what the draft was started from. draft: the operator's working copy.
const base = ref(null)
const draft = ref(null)
const latched = ref(false)
const reviewLatched = ref(false)
const conflictFp = ref('')
const compareOpen = ref(false)
const statementFormKey = ref(0)
const inboundFormKey = ref(0)
const statementProblem = ref('')
const inboundProblem = ref('')

const busy = computed(() => props.saving || latched.value)
const readable = computed(() => !!latest.value && access.value.identityOk && access.value.canRead)
const shownPolicy = computed(() => {
  if (!readable.value || !latest.value.policy) return null
  return draft.value || latest.value.policy
})
const diff = computed(() => (base.value && draft.value
  ? diffPolicies(base.value.policy, draft.value)
  : { statements: { added: [], removed: [] }, inbound: { added: [], removed: [] }, total: 0 }))
const dirty = computed(() => diff.value.total > 0)
const stale = computed(() => dirty.value && !!latest.value && !!base.value && base.value.fp !== latestFp.value)
const conflictLocked = computed(() => !!props.saveError && props.saveError.kind === 'conflict'
  && !!base.value && conflictFp.value === base.value.fp && latestFp.value === base.value.fp)
const canSave = computed(() => dirty.value && canEdit.value && !stale.value && !busy.value
  && !conflictLocked.value && !!draft.value && validatePolicy(draft.value).ok)
const warnings = computed(() => (shownPolicy.value ? policyWarnings(shownPolicy.value) : []))

function sync() {
  const l = latest.value
  if (l && l.policy && l.function.id === props.functionId) {
    base.value = { fp: hostFingerprint(l), policy: clonePolicy(l.policy) }
    draft.value = clonePolicy(l.policy)
  } else {
    base.value = null
    draft.value = null
  }
}

// Throw away the draft and every transient message, then start from the server.
function adopt() {
  latched.value = false
  reviewLatched.value = false
  compareOpen.value = false
  statementProblem.value = ''
  inboundProblem.value = ''
  statementFormKey.value += 1
  inboundFormKey.value += 1
  sync()
}

sync()
// Declared first so an owner change clears state before the snapshot watcher.
watch(() => props.functionId, adopt)
watch(latest, (l) => {
  if (!l || !l.policy || l.function.id !== props.functionId) return
  // A clean page follows the server. A dirty one keeps its draft, unless the
  // new snapshot already contains exactly what the draft asked for.
  if (!dirty.value || (draft.value && diffPolicies(l.policy, draft.value).total === 0)) sync()
})
watch(latestFp, () => { latched.value = false; reviewLatched.value = false })
watch(() => props.saving, (now, was) => { if (was && !now) latched.value = false })
watch(() => props.loading, (now, was) => { if (was && !now) reviewLatched.value = false })
watch(() => props.saveError, (e) => {
  latched.value = false
  if (e && e.kind === 'conflict') conflictFp.value = base.value ? base.value.fp : latestFp.value
})
watch(dirty, (v) => emit('dirty-change', v))

const view = computed(() => {
  if (!latest.value) {
    if (props.loading) return 'loading'
    if (props.snapshot) return 'unavailable'
    return props.loadError ? 'none' : 'empty'
  }
  if (!access.value.identityOk) return 'other-owner'
  if (!access.value.canRead) return 'no-access'
  return 'ready'
})
const displayName = computed(() => props.functionName
  || (latest.value && access.value.identityOk ? latest.value.function.name : '')
  || 'this function')

const blocks = {
  empty: ['No permissions to show', 'Nothing has been loaded for this function yet.'],
  'other-owner': ['Not shown', 'This snapshot belongs to a different function, so none of it is shown or editable here.'],
  'no-access': ['No access', 'You do not have access to read this function\'s permissions.'],
}
const blockTitle = computed(() => (view.value === 'unavailable' ? 'Permissions unavailable' : (blocks[view.value] || ['', ''])[0]))
const blockText = computed(() => (view.value === 'unavailable'
  ? (normalized.value.problems[0] || 'The permissions data could not be read, so nothing is shown or editable.')
  : (blocks[view.value] || ['', ''])[1]))

const enforcementBadge = computed(() => {
  const e = access.value.enforcement
  if (e === 'active') return { variant: 'success', text: 'Enforcement active' }
  if (e === 'inactive') return { variant: 'warning', text: 'Enforcement off' }
  return { variant: 'gray', text: 'Enforcement unknown' }
})
const reviewBadge = computed(() => {
  const h = latest.value.host
  if (h.quarantined) return { variant: 'error', text: 'Quarantined' }
  if (h.reviewRequired) return { variant: 'warning', text: 'Review required' }
  if (h.currentArtifact && h.currentArtifact === h.approvedArtifact) return { variant: 'success', text: 'Reviewed' }
  return { variant: 'gray', text: 'Not reviewed' }
})
const enforcementNotice = computed(() => {
  const e = access.value.enforcement
  if (e === 'inactive') return 'Permission enforcement is not enabled on this server. This is the stored policy; it does not restrict the function, and editing is off.'
  if (e === 'unknown') return 'The server did not report whether permissions are enforced, so editing is off.'
  return ''
})
const lockNote = computed(() => {
  if (!readable.value) return ''
  if (props.readOnlyReason) return props.readOnlyReason
  if (props.loading || props.loadError || !latest.value.policy) return ''
  if (!access.value.canManage) {
    return dirty.value
      ? 'Your permission to change this policy was removed. Your draft is kept for reference and cannot be saved.'
      : 'You can view this policy but not change it.'
  }
  return ''
})
const saveErrorText = computed(() => ({
  conflict: 'The policy changed on the server. Your draft is kept. Refresh to see the server version, then reload or discard.',
  forbidden: 'The server refused this change. Your draft is kept, but saving is locked.',
  validation: 'The server rejected this policy. Your draft is kept; adjust it and try again.',
  error: 'Saving failed. Your draft is kept. Use Retry save to try again.',
}[props.saveError && props.saveError.kind] || 'Saving failed. Your draft is kept.'))
const saveHint = computed(() => {
  if (stale.value) return 'Saving is off until you reload the server version.'
  if (conflictLocked.value) return 'Refresh to continue.'
  if (forbidden.value) return 'Saving is locked.'
  if (!canEdit.value) return 'Saving is off.'
  return 'Not saved or applied until you press Save permissions.'
})

// ---- change text (diff entries to readable lines)
const statementLine = (s) => {
  const d = describeStatement(s)
  const where = d.selector ? ` ${d.selector.mode} ${d.selector.value === '' ? '(empty)' : d.selector.value}` : d.targetId ? ` ${d.targetId}` : ''
  return `${d.title}${where}: ${d.actions.map((a) => a.id).join(', ')}${d.context === 'bootstrap' ? ' (at startup)' : ''}`
}
const callerLine = (a) => `Caller ${a.caller_id}: ${a.triggers.map((t) => TRIGGER_LABELS[t]).join(', ')}`
const linesOf = (d) => [
  ...d.statements.added.map((s) => ({ kind: 'Added', text: statementLine(s) })),
  ...d.statements.removed.map((s) => ({ kind: 'Removed', text: statementLine(s) })),
  ...d.inbound.added.map((a) => ({ kind: 'Added', text: callerLine(a) })),
  ...d.inbound.removed.map((a) => ({ kind: 'Removed', text: callerLine(a) })),
]
const draftChanges = computed(() => linesOf(diff.value))
const serverChanges = computed(() => (stale.value && latest.value.policy
  ? linesOf(diffPolicies(base.value.policy, latest.value.policy))
  : []))

// ---- editing (all against the local draft)
const editable = () => canEdit.value && !busy.value && !!draft.value

function onAddStatement(statement) {
  if (!editable()) return
  const r = addStatement(draft.value, statement)
  if (!r.ok) {
    statementProblem.value = r.problems[0]
    return
  }
  draft.value = r.policy
  statementProblem.value = ''
  statementFormKey.value += 1
}
function onRemoveStatement(index) {
  if (editable()) draft.value = removeStatement(draft.value, index)
}
function onAddInbound({ callerId, triggers }) {
  if (!editable()) return
  const r = addInbound(draft.value, callerId, triggers)
  if (!r.ok) {
    inboundProblem.value = r.problems[0]
    return
  }
  draft.value = r.policy
  inboundProblem.value = ''
  inboundFormKey.value += 1
}
function onRemoveInbound(index) {
  if (editable()) draft.value = removeInbound(draft.value, index)
}

function onSave() {
  if (!canSave.value) return
  const r = buildSavePayload(latest.value, draft.value)
  if (!r.ok) return
  latched.value = true
  emit('save', r.payload)
}

function onReview() {
  if (!access.value.canReview || dirty.value || busy.value || reviewLatched.value || props.loading || props.loadError) return
  const r = buildReviewPayload(latest.value)
  if (!r.ok) return
  reviewLatched.value = true
  emit('review', r.payload)
}
</script>
