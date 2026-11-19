<template>
  <div class="space-y-6">
    <!-- Page header -->
    <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div class="min-w-0">
        <component
          :is="inlineOnly ? 'h2' : 'h1'"
          class="text-xl font-semibold tracking-tight text-foreground-strong"
        >
          {{ inlineOnly ? 'Inline permissions' : 'Permissions' }}
        </component>
        <p class="mt-1.5 measure text-sm leading-body text-foreground-muted">
          <template v-if="inlineOnly">
            Inline source for
          </template>
          <template v-else>
            What
          </template>
          <span class="font-medium text-foreground-strong">{{ displayName }}</span>
          {{ inlineOnly ? '. Attached policies are unchanged.' : 'may do, and which callers it accepts.' }}
        </p>
        <code class="mt-1 block break-all font-mono text-xs text-foreground-strong">{{ functionId }}</code>
      </div>
      <div class="flex flex-wrap items-center gap-2 sm:shrink-0">
        <Button
          v-if="screen === 'view' && view === 'ready' && canEdit"
          :disabled="busy"
          @click="startEdit"
        >
          <Pencil
            class="h-4 w-4"
            aria-hidden="true"
          />
          {{ inlineOnly ? (isEmptyPolicy ? 'Create inline permissions' : 'Edit inline permissions') : (isEmptyPolicy ? 'Create policy' : 'Edit permissions') }}
        </Button>
        <Button
          v-else-if="screen === 'edit' && !dirty && !wizard"
          variant="secondary"
          :disabled="busy"
          @click="closeEditor"
        >
          Done
        </Button>
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
      :class="screen === 'view'
        ? 'flex items-start gap-3 rounded-md border border-info-ring bg-info-tint px-4 py-3 text-xs text-info-fg'
        : 'flex items-center gap-2 text-xs text-info-fg'"
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
      <p class="mx-auto mt-1.5 measure-long text-sm text-foreground-muted">
        {{ blockText }}
      </p>
    </div>

    <template v-else-if="view === 'ready'">
      <!-- Current state: one plain sentence, with the words that matter -->
      <div
        class="space-y-2 border-y border-border py-3"
        aria-label="Permission state"
        role="group"
      >
        <div class="flex flex-wrap items-center gap-x-2 gap-y-2">
          <Badge
            size="sm"
            :variant="enforcementBadge.variant"
          >
            {{ enforcementBadge.text }}
          </Badge>
          <Badge
            v-if="reviewBadge"
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
          v-if="screen === 'view' || access.enforcement !== 'active'"
          class="measure text-sm text-foreground"
        >
          {{ enforcementText }}
        </p>
      </div>

      <p
        v-if="latest.host.reviewRequired || latest.host.quarantined"
        role="status"
        class="flex items-start gap-3 rounded-md border border-border bg-surface px-4 py-3 text-xs text-foreground"
      >
        <ShieldAlert
          class="mt-0.5 h-4 w-4 shrink-0"
          aria-hidden="true"
        />
        Retained, not in force. {{ latest.host.quarantined ? 'Quarantined' : 'Needs review' }};
        {{ screen === 'view' ? 'what is listed below is stored, not enforced.' : 'the stored policy is not enforced. Draft changes stay unsaved until you review and save.' }}
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
          <code class="font-mono">{{ conflict.from }}</code> to
          <code class="font-mono">{{ water === null ? latest.host.revision : String(water) }}</code>). Your draft is kept. Discard it and reload to save.
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
            @click="onDiscard"
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
            Only the artifact or approval changed.
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
          The stored policy is not valid, so it is not shown or editable.
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

      <!-- ===== Current stored policy (read) ===== -->
      <template v-else-if="screen === 'view'">
        <section
          class="rounded-lg border border-border bg-surface"
          aria-labelledby="perm-outbound-title"
        >
          <header class="border-b border-border px-4 py-3">
            <h2
              id="perm-outbound-title"
              class="text-sm font-semibold text-foreground-strong"
            >
              {{ inlineOnly ? (authorityUnconfirmed ? 'Last known inline platform grants (unverified)' : 'Inline platform grants') : 'What this function may do' }}
            </h2>
            <p class="mt-1 measure text-xs text-foreground-muted">
              {{ inlineOnly ? 'Inline source only. Attached sources are in effective access above.' : (isEmptyPolicy ? 'No permissions are stored. Where enforcement is on, protected actions are denied by default.' : 'Stored policy, by service. Calling another function also needs that function to accept this one.') }}
            </p>
          </header>
          <AccessSummary
            :function-id="functionId"
            :statements="stored.allow"
            :callers="stored.inbound"
            :targets="latest.targets"
            :warnings="warnings"
            :unconfirmed="unconfirmed"
            :inline-only="inlineOnly"
          />
        </section>

        <section
          class="rounded-lg border border-border bg-surface"
          aria-labelledby="perm-inbound-title"
        >
          <header class="border-b border-border px-4 py-3">
            <h2
              id="perm-inbound-title"
              class="text-sm font-semibold text-foreground-strong"
            >
              {{ inlineOnly ? (authorityUnconfirmed ? 'Last known inline caller grants (unverified)' : 'Inline caller grants') : 'Who may call this function' }}
            </h2>
            <p class="mt-1 measure text-xs text-foreground-muted">
              {{ inlineOnly ? 'Inline acceptance only. Callers also need their own outbound allowance.' : 'Matched by function ID. A caller also needs its own allowance.' }}
            </p>
          </header>
          <InboundCallers
            :owner-id="functionId"
            :callers="stored.inbound"
            :targets="latest.targets"
            :unconfirmed="unconfirmed"
            :inline-only="inlineOnly"
          />
        </section>
      </template>

      <!-- ===== Editing a draft ===== -->
      <section
        v-else-if="screen === 'edit'"
        class="space-y-4"
        aria-labelledby="perm-edit-title"
      >
        <header class="space-y-3">
          <div>
            <h2
              id="perm-edit-title"
              class="text-sm font-semibold text-foreground-strong"
            >
              {{ inlineOnly ? 'Edit inline permissions' : 'Edit permissions' }}
            </h2>
            <p class="mt-1 measure text-xs text-foreground-muted">
              Changes stay in a draft until you review and save.
            </p>
          </div>
          <SegmentedControl
            v-model="tab"
            aria-label="Part of the policy"
            :options="TABS"
          />
        </header>

        <template v-if="tab === 'access'">
          <PermissionWizard
            v-if="wizard && wizard.kind === 'permission'"
            :key="wizard.key"
            :function-id="functionId"
            :targets="latest.targets"
            :initial="wizardInitial"
            :problem="statementProblem"
            :disabled="busy || !canEdit"
            @commit="onCommitStatement"
            @cancel="closeWizard"
          />
          <div
            v-else
            class="rounded-lg border border-border bg-surface"
          >
            <div class="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
              <p class="min-w-0 measure text-xs text-foreground-muted">
                One allowance each. Calling another function also needs that function to accept this one.
              </p>
              <Button
                v-if="canEdit"
                :disabled="busy"
                @click="openWizard('permission', null)"
              >
                <Plus
                  class="h-4 w-4"
                  aria-hidden="true"
                />
                Add permission
              </Button>
            </div>
            <StatementList
              :function-id="functionId"
              :statements="draft.allow"
              :targets="latest.targets"
              :warnings="warnings"
              :unconfirmed="unconfirmed"
              :editable="canEdit"
              :disabled="busy"
              :inline-only="inlineOnly"
              @edit="(i) => openWizard('permission', i)"
              @remove="onRemoveStatement"
            />
          </div>
        </template>

        <template v-else>
          <CallerWizard
            v-if="wizard && wizard.kind === 'caller'"
            :key="wizard.key"
            :owner-id="functionId"
            :targets="latest.targets"
            :initial="wizardInitial"
            :problem="inboundProblem"
            :disabled="busy || !canEdit"
            @commit="onCommitCaller"
            @cancel="closeWizard"
          />
          <div
            v-else
            class="rounded-lg border border-border bg-surface"
          >
            <div class="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-3">
              <p class="min-w-0 measure text-xs text-foreground-muted">
                An accepted caller can start this function. It also needs its own allowance.
              </p>
              <Button
                v-if="canEdit"
                :disabled="busy"
                @click="openWizard('caller', null)"
              >
                <Plus
                  class="h-4 w-4"
                  aria-hidden="true"
                />
                Add caller
              </Button>
            </div>
            <InboundCallers
              :owner-id="functionId"
              :callers="draft.inbound"
              :targets="latest.targets"
              :unconfirmed="unconfirmed"
              :editable="canEdit"
              :disabled="busy"
              :inline-only="inlineOnly"
              @edit="(i) => openWizard('caller', i)"
              @remove="onRemoveInbound"
            />
          </div>
        </template>
      </section>

      <!-- ===== Final review before saving ===== -->
      <ReviewChanges
        v-else
        :changes="draftChanges"
        :notes="reviewNotes"
      />

      <!-- Advanced: secondary, never first -->
      <section
        v-if="screen !== 'review' && !wizard"
        class="rounded-lg border border-border bg-surface"
        aria-labelledby="perm-advanced-title"
      >
        <header class="flex flex-wrap items-center justify-between gap-3 px-4 py-3">
          <div class="min-w-0">
            <h2
              id="perm-advanced-title"
              class="text-sm font-semibold text-foreground-strong"
            >
              Advanced
            </h2>
            <p class="mt-0.5 text-xs text-foreground-muted">
              Review status, JSON and history.
            </p>
          </div>
          <Button
            variant="secondary"
            size="sm"
            :aria-expanded="advancedOpen ? 'true' : 'false'"
            aria-controls="permissions-advanced"
            @click="advancedOpen = !advancedOpen"
          >
            <ChevronDown
              class="h-3.5 w-3.5 transition-transform duration-150"
              :class="advancedOpen ? 'rotate-180' : ''"
              aria-hidden="true"
            />
            {{ advancedOpen ? 'Hide advanced' : 'Show advanced' }}
          </Button>
        </header>
        <div
          id="permissions-advanced"
          :hidden="!advancedOpen"
          class="border-t border-border"
        >
          <h3 class="border-b border-border bg-background px-4 py-2 text-xs font-medium uppercase tracking-wide text-foreground-muted">
            Review
          </h3>
          <ReviewPanel
            :host="latest.host"
            :can-review="access.canReview"
            :dirty="dirty"
            :pending="reviewLatched"
            :disabled="reviewBlocked"
            @review="onReview"
          />
          <h3 class="border-y border-border bg-background px-4 py-2 text-xs font-medium uppercase tracking-wide text-foreground-muted">
            {{ inlineOnly ? 'Inline policy JSON' : 'Policy JSON' }}
          </h3>
          <PolicyJson
            :policy="screen === 'edit' ? draft : stored"
            :label="inlineOnly ? 'Only this inline source is editable; the effective union is never saved as inline.' : (screen === 'edit' ? 'Your draft as it would be saved.' : 'The stored policy as the server reads it.')"
          />
          <h3 class="border-y border-border bg-background px-4 py-2 text-xs font-medium uppercase tracking-wide text-foreground-muted">
            History
          </h3>
          <HistoryList
            :entries="latest.history"
            :unconfirmed="!!unconfirmed"
          />
        </div>
      </section>

      <SaveBar
        v-if="dirty"
        :count="diff.total"
        :stage="screen === 'review' ? 'review' : 'edit'"
        :hint="saveHint"
        :save-label="saveError && saveError.kind === 'error' ? 'Retry save' : 'Save permissions'"
        :can-save="canSave"
        :saving="busy"
        :review-disabled="!!wizard"
        @review="goReview"
        @back="backToEdit"
        @save="onSave"
        @discard="onDiscard"
      />
    </template>
  </div>
</template>

<script setup>
import { computed, ref, watch } from 'vue'
import { ChevronDown, Info, Pencil, Plus, RefreshCw, ShieldAlert, TriangleAlert } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import Badge from '@/components/common/Badge.vue'
import LoadError from '@/components/common/LoadError.vue'
import StatementList from '@/components/permissions/StatementList.vue'
import AccessSummary from '@/components/permissions/AccessSummary.vue'
import PermissionWizard from '@/components/permissions/PermissionWizard.vue'
import CallerWizard from '@/components/permissions/CallerWizard.vue'
import ReviewChanges from '@/components/permissions/ReviewChanges.vue'
import PolicyJson from '@/components/permissions/PolicyJson.vue'
import SegmentedControl from '@/components/common/SegmentedControl.vue'
import InboundCallers from '@/components/permissions/InboundCallers.vue'
import ReviewPanel from '@/components/permissions/ReviewPanel.vue'
import HistoryList from '@/components/permissions/HistoryList.vue'
import SaveBar from '@/components/permissions/SaveBar.vue'
import {
  addInbound, addStatement, buildReviewPayload, buildSavePayload, clonePolicy, deriveAccess,
  diffPolicies, hostFingerprint, normalizeSnapshot, policyWarnings, parseCounter,
  callerTriggerProblem, removeInbound, removeStatement, replaceInbound, replaceStatement, statementsEqual,
  statementSummary, validatePolicy, TRIGGER_LABELS,
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
  inlineOnly: { type: Boolean, default: false },
  authorityUnconfirmed: { type: String, default: '' },
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
// conflict: held until an explicit discard, so a later A cannot undo A, B, A.
// candidate: only a matching snapshot seen mid-save, never an acknowledgement.
const conflict = ref(null)
// Newest revision seen for this owner. Only an owner change resets it.
const water = ref(null)
const compareOpen = ref(false)
// What is on screen. view: the stored policy. edit: the draft, one guided step
// at a time. review: the draft against the stored policy, where Save lives.
const mode = ref('view')
const tab = ref('access')
// null, or { kind: 'permission' | 'caller', index: number | null, key }.
const wizard = ref(null)
const wizardKey = ref(0)
const advancedOpen = ref(false)
const TABS = [
  { value: 'access', label: 'What it can do' },
  { value: 'callers', label: 'Who can call it' },
]
const statementProblem = ref('')
const inboundProblem = ref('')

const busy = computed(() => props.saving || latched.value)
const readable = computed(() => !!latest.value && access.value.identityOk && access.value.canRead)
const shownPolicy = computed(() => {
  if (!readable.value || !latest.value.policy) return null
  return draft.value || latest.value.policy
})
// The stored policy, exactly as the server last sent it. It never shows a draft.
const stored = computed(() => (readable.value ? latest.value.policy : null))
const isEmptyPolicy = computed(() => !!stored.value && !stored.value.allow.length && !stored.value.inbound.length)
const diff = computed(() => (base.value && draft.value
  ? diffPolicies(base.value.policy, draft.value)
  : { statements: { added: [], removed: [] }, inbound: { added: [], removed: [] }, total: 0 }))
const dirty = computed(() => diff.value.total > 0)
const stale = computed(() => dirty.value && !!conflict.value)
const shownRevision = computed(() => (latest.value && access.value.identityOk ? parseCounter(latest.value.host.revision) : null))
const obsolete = computed(() => water.value !== null && shownRevision.value !== null && shownRevision.value < water.value)
const conflictLocked = computed(() => !!props.saveError && props.saveError.kind === 'conflict'
  && !!base.value && conflictFp.value === base.value.fp && latestFp.value === base.value.fp)
// Same guards as save: a refused or conflicting request must be refreshed first.
const reviewBlocked = computed(() => busy.value || props.loading || !!props.loadError
  || reviewLatched.value || forbidden.value || conflictLocked.value || obsolete.value)
// While a refresh is pending or failed, an empty list is old news, not a fact.
const unconfirmed = computed(() => (props.loading ? 'loading' : props.loadError ? 'error' : props.authorityUnconfirmed))
const canSave = computed(() => dirty.value && canEdit.value && !stale.value && !busy.value
  && !obsolete.value && !conflictLocked.value && !!draft.value && validatePolicy(draft.value).ok)
// Editing needs a stored policy and a live right to change it. Transient states
// (loading, saving) are not denial: they only disable controls.
const denied = computed(() => !latest.value || !latest.value.policy || !access.value.canManage
  || access.value.enforcement !== 'active' || forbidden.value)
const screen = computed(() => {
  if (mode.value === 'view') return 'view'
  if (!dirty.value && (denied.value || mode.value === 'review')) return 'view'
  return mode.value
})
const warnings = computed(() => {
  const p = screen.value === 'view' ? stored.value : shownPolicy.value
  return p ? policyWarnings(p) : []
})

function sync() {
  const l = latest.value
  conflict.value = null
  if (l && l.policy && l.function.id === props.functionId) {
    base.value = { fp: hostFingerprint(l), policy: clonePolicy(l.policy) }
    draft.value = clonePolicy(l.policy)
  } else {
    base.value = null
    draft.value = null
  }
}

// Returns true when this snapshot is older than one already seen.
function observe(l) {
  const rev = l && l.function.id === props.functionId ? parseCounter(l.host.revision) : null
  if (rev === null) return false
  if (water.value !== null && rev < water.value) return true
  water.value = rev
  return false
}

// Throw away the draft and every transient message, then start from the server.
function adopt() {
  latched.value = false
  reviewLatched.value = false
  compareOpen.value = false
  statementProblem.value = ''
  inboundProblem.value = ''
  mode.value = 'view'
  tab.value = 'access'
  wizard.value = null
  sync()
}

sync()
observe(latest.value)
// Declared first so an owner change clears state before the snapshot watcher.
watch(() => props.functionId, () => {
  water.value = null
  adopt()
  observe(latest.value)
})
watch(latest, (l) => {
  const older = observe(l)
  if (!l || !l.policy || l.function.id !== props.functionId) return
  if (!dirty.value) {
    sync()
    return
  }
  // A dirty draft settles only on a clean, current, post-completion snapshot
  // that already holds exactly what it asked for. Anything else keeps the base.
  const clean = !older && !props.saveError && !props.loadError && !props.loading
    && !!draft.value && diffPolicies(l.policy, draft.value).total === 0
  const inFlight = props.saving || latched.value
  if (clean && !inFlight && (!conflict.value || conflict.value.candidate)) {
    sync()
  } else if (conflict.value) {
    if (!clean) conflict.value = { ...conflict.value, candidate: false }
  } else if (base.value && base.value.fp !== hostFingerprint(l)) {
    conflict.value = { from: base.value.fp.split('|')[0], candidate: clean }
  }
})
watch(latestFp, () => { latched.value = false; reviewLatched.value = false })
watch(() => props.saving, (now, was) => {
  if (!was || now) return
  latched.value = false
  if (props.saveError) reviewLatched.value = false
})
watch(() => props.loading, (now, was) => { if (was && !now) reviewLatched.value = false })
watch(() => props.saveError, (e) => {
  latched.value = false
  // A terminal error with nothing in flight frees a review request to retry.
  if (e && !props.saving) reviewLatched.value = false
  if (e && conflict.value) conflict.value = { ...conflict.value, candidate: false }
  if (e && e.kind === 'conflict') conflictFp.value = base.value ? base.value.fp : latestFp.value
})
watch(dirty, (v) => {
  emit('dirty-change', v)
  if (!v && conflict.value) sync()
})
// A wizard never outlives the right to use it, the screen it lives on, or its tab.
watch(denied, (v) => { if (v) wizard.value = null })
watch(screen, (s) => { if (s !== 'edit') wizard.value = null })
watch(tab, () => { wizard.value = null; statementProblem.value = ''; inboundProblem.value = '' })

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
  empty: ['No permissions to show', 'Nothing loaded yet.'],
  'other-owner': ['Not shown', 'This snapshot belongs to a different function, so nothing here is shown or editable.'],
  'no-access': ['No access', 'You do not have access to this function\'s permissions.'],
}
const blockTitle = computed(() => (view.value === 'unavailable' ? 'Permissions unavailable' : (blocks[view.value] || ['', ''])[0]))
const blockText = computed(() => (view.value === 'unavailable'
  ? (normalized.value.problems[0] || 'The permissions data could not be read.')
  : (blocks[view.value] || ['', ''])[1]))

const enforcementBadge = computed(() => {
  if (props.inlineOnly && props.authorityUnconfirmed) return { variant: 'warning', text: 'Snapshot stale' }
  const e = access.value.enforcement
  if (e === 'active') return { variant: 'success', text: 'Enforcement active' }
  if (e === 'inactive') return { variant: 'warning', text: 'Enforcement off' }
  return { variant: 'gray', text: 'Enforcement unknown' }
})
// Only a problem is a badge. Reviewed / not reviewed lives under Advanced.
const reviewBadge = computed(() => {
  if (props.inlineOnly && props.authorityUnconfirmed) return null
  const h = latest.value.host
  if (h.quarantined) return { variant: 'error', text: 'Quarantined' }
  if (h.reviewRequired) return { variant: 'warning', text: 'Review required' }
  return null
})
const enforcementText = computed(() => {
  const e = access.value.enforcement
  if (props.inlineOnly && props.authorityUnconfirmed) return 'Inline baseline and approval are unverified. Your draft is kept and is not effective access.'
  if (props.inlineOnly) return 'Stored inline grants, not total or active access. Drafts change this source only.'
  if (e === 'inactive') return 'Permission enforcement is not enabled on this server. The stored policy does not restrict the function; editing is off.'
  if (e === 'unknown') return 'The server did not report whether enforcement is on, so editing is off.'
  return screen.value === 'view'
    ? 'Enforcement is on. Below is the stored policy.'
    : 'Enforcement is on. Draft changes are not saved or applied.'
})
const lockNote = computed(() => {
  if (!readable.value) return ''
  if (props.readOnlyReason) return props.readOnlyReason
  if (obsolete.value) return `Response is older than revision ${water.value}. Refresh to save or review.`
  if (props.loading || props.loadError || !latest.value.policy) return ''
  if (!access.value.canManage) {
    return dirty.value
      ? 'Your permission to change this policy was removed. The draft cannot be saved.'
      : 'You can view this policy but not change it.'
  }
  return ''
})
const saveErrorText = computed(() => ({
  conflict: 'The policy changed on the server. Your draft is kept. Refresh, then reload or discard.',
  forbidden: 'The server refused this change. Your draft is kept; saving is locked.',
  validation: 'The server rejected this policy. Adjust the draft and try again.',
  error: 'Saving failed. Your draft is kept. Retry save.',
}[props.saveError && props.saveError.kind] || 'Saving failed. Your draft is kept.'))
const saveHint = computed(() => {
  if (obsolete.value) return 'Saving is off: this response is older than one already received.'
  if (stale.value) return 'Saving is off until you reload the server version.'
  if (conflictLocked.value) return 'Refresh to continue.'
  if (forbidden.value) return 'Saving is locked.'
  if (!canEdit.value) return 'Saving is off.'
  return screen.value === 'review' ? 'Not saved or applied until you press Save permissions.' : 'Not saved or applied until you review the changes and save.'
})

// ---- change text (diff entries to readable lines)
const statementLine = statementSummary
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
const editable = () => canEdit.value && !busy.value && !!draft.value && screen.value === 'edit'

function startEdit() {
  if (!canEdit.value || !draft.value || busy.value) return
  mode.value = 'edit'
  tab.value = 'access'
  // First use: go straight into the guided path instead of an empty list.
  if (isEmptyPolicy.value) openWizard('permission', null)
}
// Done never throws away a half-filled step or a draft, and never runs mid-save.
function closeEditor() {
  if (dirty.value || wizard.value || busy.value) return
  mode.value = 'view'
  wizard.value = null
}
function openWizard(kind, index) {
  if (!editable()) return
  statementProblem.value = ''
  inboundProblem.value = ''
  wizardKey.value += 1
  wizard.value = { kind, index, key: wizardKey.value }
}
function closeWizard() {
  wizard.value = null
  statementProblem.value = ''
  inboundProblem.value = ''
}
const wizardInitial = computed(() => {
  const w = wizard.value
  if (!w || w.index === null || !draft.value) return null
  return (w.kind === 'permission' ? draft.value.allow : draft.value.inbound)[w.index] || null
})

function onCommitStatement(statement) {
  const w = wizard.value
  if (!editable() || !w || w.kind !== 'permission') return
  if (draft.value.allow.some((s, i) => i !== w.index && statementsEqual(s, statement))) {
    statementProblem.value = 'That permission is already in the policy.'
    return
  }
  const r = w.index === null ? addStatement(draft.value, statement) : replaceStatement(draft.value, w.index, statement)
  if (!r.ok) {
    statementProblem.value = r.problems[0]
    return
  }
  draft.value = r.policy
  closeWizard()
}
function onRemoveStatement(index) {
  if (!editable()) return
  draft.value = removeStatement(draft.value, index)
}
function onCommitCaller({ callerId, triggers }) {
  const w = wizard.value
  if (!editable() || !w || w.kind !== 'caller') return
  // Schedule acceptance is for this function's own schedules; never author it for another.
  const unsupported = callerTriggerProblem(callerId, triggers, props.functionId)
  if (unsupported) {
    inboundProblem.value = unsupported
    return
  }
  const r = w.index === null ? addInbound(draft.value, callerId, triggers) : replaceInbound(draft.value, w.index, callerId, triggers)
  if (!r.ok) {
    inboundProblem.value = r.problems[0]
    return
  }
  draft.value = r.policy
  closeWizard()
}
function onRemoveInbound(index) {
  if (!editable()) return
  draft.value = removeInbound(draft.value, index)
}

// Save is only reachable from the review screen.
function goReview() {
  if (dirty.value && mode.value === 'edit' && !wizard.value && !busy.value) mode.value = 'review'
}
// Discarding is deliberate and never runs while a save is in flight.
function onDiscard() {
  if (busy.value) return
  adopt()
}
function backToEdit() {
  if (!busy.value && mode.value === 'review') mode.value = 'edit'
}

// Short warnings shown with the changes, never in place of them.
const reviewNotes = computed(() => {
  if (!dirty.value || !draft.value) return []
  const d = diff.value
  const out = []
  if (d.statements.added.some((s) => s.resource.function) || d.inbound.added.length) {
    out.push('Calls between functions need both sides: this one allowed to call, the other accepting it. Check the other function too.')
  }
  if (d.statements.added.some((s) => s.context === 'bootstrap')) {
    out.push('Startup access runs at load, before any request, and can only read and list.')
  }
  if (d.statements.added.some((s) => s.resource.own_kv && s.resource.own_kv.prefix === '')) {
    out.push("A permission on every key covers all of this function's storage, including keys added later.")
  }
  if (d.statements.added.some((s) => s.resource.own_cron && s.resource.own_cron.prefix === '')) {
    out.push('Covers every schedule for this function, including future ones.')
  }
  for (const w of policyWarnings(draft.value)) {
    out.push(`Incomplete: ${statementSummary(draft.value.allow[w.index])}. ${w.text}`)
  }
  return out
})

function onSave() {
  // Only the review screen saves; a stray event from any other screen is refused.
  if (screen.value !== 'review' || !canSave.value) return
  const r = buildSavePayload(latest.value, draft.value)
  if (!r.ok) return
  latched.value = true
  emit('save', r.payload)
}

function onReview() {
  if (!access.value.canReview || dirty.value || reviewBlocked.value) return
  const r = buildReviewPayload(latest.value)
  if (!r.ok) return
  reviewLatched.value = true
  emit('review', r.payload)
}
</script>
