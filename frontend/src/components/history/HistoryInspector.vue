<template>
  <section
    class="border border-border rounded-lg bg-background p-4 sm:p-5 space-y-4 min-w-0"
    aria-label="Deleted-work history"
  >
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h2 class="text-sm font-semibold text-foreground-strong">
          Deleted-work history
        </h2>
        <p class="text-sm text-foreground-muted mt-1">
          Metadata only. Original payload is unavailable. Archives cannot execute, resume or be adopted.
        </p>
      </div>
      <Button
        variant="secondary"
        :disabled="loading || busy"
        @click="refresh(false)"
      >
        Refresh history
      </Button>
    </div>
    <p
      v-if="!fresh"
      role="status"
      class="text-sm text-warning-fg"
    >
      History or capacity is stale or unknown. Refresh before reviewing.
    </p>
    <p
      v-if="loading"
      role="status"
      class="text-sm text-foreground-muted"
    >
      Loading history…
    </p>
    <p
      v-if="error || mutationError"
      role="alert"
      class="text-sm text-danger-fg break-words"
    >
      {{ error || mutationError }}
    </p>
    <p
      v-if="notice"
      role="status"
      class="text-sm text-foreground-strong break-all"
    >
      {{ notice }}
    </p>
    <div
      v-if="capacity"
      class="text-sm text-foreground-muted space-y-1"
    >
      <p>Canonical metadata: <span class="font-mono">{{ capacity.rows_used }} / {{ capacity.max_rows }}</span> rows and <span class="font-mono">{{ capacity.bytes_used }} / {{ capacity.max_bytes }}</span> bytes used.</p>
      <p>Reserved within those caps: <span class="font-mono">{{ capacity.rows_reserved }}</span> rows, <span class="font-mono">{{ capacity.bytes_reserved }}</span> bytes. Hard limits: 100,000 rows / 64 MiB.</p>
      <p>90 days means purge eligibility, not automatic expiry. Reserved entries cannot purge. {{ capacity.can_manage ? 'Management needs live server authorization.' : 'Read-only access. Management is unavailable.' }}</p>
    </div>
    <ul class="divide-y divide-border">
      <li
        v-for="row in rows"
        :key="`${row.kind}:${row.id}`"
        class="py-3 flex flex-wrap items-center gap-3"
      >
        <div class="min-w-0 flex-1 text-sm">
          <p class="text-foreground-strong">
            {{ row.kind }} <span v-if="row.reserved">(reserved)</span>
          </p>
          <p class="font-mono text-xs break-all text-foreground-muted">
            {{ row.id }}
          </p>
        </div>
        <Button
          variant="secondary"
          size="sm"
          :disabled="loading || busy"
          :aria-label="`Inspect ${row.kind} ${row.id}`"
          @click="inspect(row)"
        >
          Inspect
        </Button>
      </li>
    </ul>
    <p
      v-if="fresh && !rows.length"
      class="text-sm text-foreground-muted"
    >
      No archived metadata on this page.
    </p>
    <Button
      v-if="rows.length === 100"
      variant="secondary"
      :disabled="loading || busy"
      @click="refresh(true)"
    >
      Next history page
    </Button>
    <section
      v-if="selected"
      class="border-t border-border pt-4 space-y-4 min-w-0"
    >
      <h3
        ref="detailHeading"
        tabindex="-1"
        class="text-sm font-semibold text-foreground-strong"
      >
        Archived {{ selected.kind }}
      </h3>
      <dl class="text-sm space-y-2 break-all">
        <div>
          <dt class="text-foreground-muted">
            Original UUID
          </dt><dd class="font-mono">
            {{ selected.id }}
          </dd>
        </div>
        <div>
          <dt class="text-foreground-muted">
            Original origin
          </dt><dd>{{ selected.metadata.origin?.origin_kind || 'Audit metadata' }}</dd>
        </div>
        <div>
          <dt class="text-foreground-muted">
            Last-known target UUID (not a current binding)
          </dt><dd class="font-mono">
            {{ selected.metadata.target_id || selected.metadata.owner_id || 'Not applicable' }}
          </dd>
        </div>
        <div>
          <dt class="text-foreground-muted">
            Purge eligible at
          </dt><dd>{{ selected.metadata.eligible_at || 'Audit entry: server checks eligibility' }}</dd>
        </div>
      </dl>
      <details>
        <summary class="text-sm text-foreground-muted cursor-pointer">
          Advanced metadata
        </summary><pre class="mt-2 text-xs whitespace-pre-wrap break-all font-mono text-foreground-muted">{{ JSON.stringify(selected.metadata, null, 2) }}</pre>
      </details>
      <div
        v-if="canManage"
        class="flex flex-wrap gap-3"
      >
        <Button
          v-if="['job','cron'].includes(selected.kind) && !selected.reserved"
          variant="secondary"
          :disabled="!detailFresh || busy || loading"
          @click="start"
        >
          Create fresh work
        </Button>
        <Button
          variant="danger"
          :disabled="!detailFresh || selected.reserved || busy || loading"
          @click="purgeReview = true; reviewed = ''"
        >
          Review purge
        </Button>
      </div>
      <p
        v-if="purgeReview"
        class="text-sm text-foreground-strong"
      >
        Permanently purge only {{ selected.kind }} {{ selected.id }} if eligible. This removes metadata, never executes work.
      </p>
      <Button
        v-if="purgeReview && canManage"
        variant="danger"
        :disabled="!detailFresh || busy || loading"
        @click="mutate('purge')"
      >
        Confirm purge
      </Button>
      <form
        v-if="editing"
        class="space-y-4"
        @submit.prevent="review"
      >
        <fieldset
          :disabled="busy"
          class="space-y-4 min-w-0"
        >
          <h3
            ref="formHeading"
            tabindex="-1"
            class="text-sm font-semibold text-foreground-strong"
          >
            New operator-origin {{ selected.kind }}
          </h3>
          <p class="text-sm text-foreground-muted">
            No original payload is retained. Choose a new target UUID; names never bind automatically.
          </p>
          <FilterSelect
            v-model="draft.target"
            :options="targets"
            label="New target UUID"
            placeholder="Choose a new target UUID"
            wide
          />
          <p class="font-mono text-xs break-all text-foreground-muted">
            {{ draft.target || 'No target selected' }}
          </p>
          <label class="block text-sm text-foreground-muted">Fresh payload (JSON)
            <textarea
              v-model="draft.payload"
              rows="4"
              required
              class="history-field font-mono"
              aria-label="Fresh payload (JSON)"
            />
          </label>
          <template v-if="selected.kind === 'job'">
            <label class="block text-sm text-foreground-muted">Run at (UTC RFC3339)<input
              v-model="draft.at"
              required
              class="history-field"
              aria-label="Run at (UTC RFC3339)"
            ></label>
            <label class="block text-sm text-foreground-muted">Maximum attempts<input
              v-model.number="draft.attempts"
              type="number"
              min="1"
              max="100"
              required
              class="history-field"
              aria-label="Maximum attempts"
            ></label>
          </template>
          <template v-else>
            <label
              v-for="field in ['name','expression','timezone']"
              :key="field"
              class="block text-sm text-foreground-muted"
            >{{ field }}<input
              v-model="draft[field]"
              required
              class="history-field"
              :aria-label="field"
            ></label>
            <label class="flex items-center gap-2 min-h-11 text-sm text-foreground-muted"><input
              v-model="draft.enabled"
              type="checkbox"
            >Enable new schedule</label>
          </template>
          <Button
            v-if="canManage"
            type="submit"
            variant="secondary"
            :disabled="!detailFresh || busy || loading"
          >
            Review fresh work
          </Button>
        </fieldset>
      </form>
      <section
        v-if="reviewed"
        class="border-t border-border pt-4 space-y-3"
      >
        <h3
          ref="reviewHeading"
          tabindex="-1"
          class="text-sm font-semibold text-foreground-strong"
        >
          Confirm new operator work
        </h3>
        <p class="text-sm text-foreground-muted break-all">
          Source: {{ selected.kind }} {{ selected.id }}. New target UUID: {{ draft.target }}. A new work ID and old-to-new audit are created. Original authority and work are not restored.
        </p>
        <pre class="text-xs font-mono whitespace-pre-wrap break-all text-foreground-strong">{{ reviewed }}</pre>
        <Button
          v-if="canManage"
          :disabled="!detailFresh || busy || loading"
          :loading="busy"
          @click="mutate('reissue')"
        >
          Confirm new work
        </Button>
      </section>
    </section>
  </section>
</template>

<script setup>
import { computed, nextTick, onActivated, onBeforeUnmount, onDeactivated, reactive, ref, watch } from 'vue'
import { historyRequest } from '@/api/history'
import { listFunctions } from '@/api/endpoints'
import Button from '@/components/common/Button.vue'
import FilterSelect from '@/components/common/FilterSelect.vue'

const props = defineProps({ active: { type: Boolean, default: true } })
const rows = ref([]), capacity = ref(null), selected = ref(null), functions = ref([])
const loading = ref(false), busy = ref(false), fresh = ref(false), detailFresh = ref(false)
const error = ref(''), mutationError = ref(''), notice = ref(''), editing = ref(false), reviewed = ref(''), purgeReview = ref(false)
const detailHeading = ref(null), formHeading = ref(null), reviewHeading = ref(null)
const draft = reactive({ target: '', payload: '', at: '', attempts: 1, name: '', expression: '', timezone: '', enabled: false })
// `live` is a plain flag, so it must not gate this cached computed: evaluated while deactivated it
// would cache false with no reactive dependency and never recover after KeepAlive reactivation.
const canManage = computed(() => props.active && fresh.value && capacity.value?.can_manage === true)
const targets = computed(() => functions.value.filter(f => f.status === 'active' && f.id !== selected.value?.metadata.target_id).map(f => ({ value: f.id, label: `${f.name} (${f.id})` })))
const drafts = new Map()
const key = row => row ? `${row.kind}:${row.id}` : ''
const blankDraft = () => ({ target: '', payload: '', at: '', attempts: 1, name: '', expression: '', timezone: '', enabled: false })
let live = true, epoch = 0, readSeq = 0, controller
const current = token => live && props.active && epoch === token.epoch
const currentRead = token => current(token) && readSeq === token.seq
const invalidate = () => {
  epoch++; readSeq++; controller?.abort(); fresh.value = false; detailFresh.value = false
  if (busy.value) notice.value = 'Mutation outcome unknown after leaving. Inspect history before any retry.'
  loading.value = false; reviewed.value = ''; purgeReview.value = false
}
const readLane = () => { readSeq++; controller?.abort(); controller = new AbortController(); return { epoch, seq: readSeq } }
const matches = (row, expected) => row?.kind === expected.kind && row?.id === expected.id && row?.metadata && typeof row.metadata === 'object'
async function refresh(next = false) {
  if (!live || !props.active || busy.value) return
  const token = readLane()
  fresh.value = false; detailFresh.value = false; reviewed.value = ''; purgeReview.value = false; loading.value = true
  const after = next && rows.value.length ? { kind: rows.value.at(-1).kind, id: rows.value.at(-1).id } : undefined
  const inspected = selected.value && { kind: selected.value.kind, id: selected.value.id }
  try {
    const [list, cap, detail, targetList] = await Promise.all([
      historyRequest('list', JSON.stringify({ limit: 100, ...(after ? { after } : {}) }), controller.signal),
      historyRequest('capacity', '{}', controller.signal),
      inspected ? historyRequest('get', JSON.stringify(inspected), controller.signal) : null,
      editing.value ? listFunctions() : null,
    ])
    if (!currentRead(token)) return
    if (!Array.isArray(list.data) || !['rows_used', 'bytes_used', 'rows_reserved', 'bytes_reserved', 'max_rows', 'max_bytes'].every(field => /^\d+$/.test(cap.data?.[field]))) throw new Error('Invalid snapshot')
    if (inspected && (!matches(detail?.data, inspected) || key(selected.value) !== key(inspected))) throw new Error('Mismatched archive')
    if (detail) { selected.value = detail.data; detailFresh.value = true }
    if (targetList) functions.value = targetList.data?.functions || []
    rows.value = list.data; capacity.value = cap.data; fresh.value = true
    error.value = ''
  } catch {
    if (currentRead(token)) error.value = 'History refresh failed. Snapshot is stale; no changes are authorized.'
  } finally { if (currentRead(token)) loading.value = false }
}
async function inspect(row) {
  if (busy.value || loading.value) return
  const token = readLane()
  loading.value = true; detailFresh.value = false; reviewed.value = ''; purgeReview.value = false
  try {
    const result = await historyRequest('get', JSON.stringify({ kind: row.kind, id: row.id }), controller.signal)
    if (!currentRead(token)) return
    if (!matches(result.data, row)) throw new Error('Mismatched archive')
    if (editing.value) drafts.set(key(selected.value), { ...draft })
    selected.value = result.data; detailFresh.value = true
    editing.value = drafts.has(key(result.data))
    Object.assign(draft, drafts.get(key(result.data)) || blankDraft())
    error.value = ''
    await nextTick(); if (currentRead(token)) detailHeading.value?.focus()
  } catch {
    if (currentRead(token)) { fresh.value = false; error.value = 'Inspection failed. The selected metadata is stale or unavailable.' }
  } finally { if (currentRead(token)) loading.value = false }
}
async function start() {
  if (!canManage.value || !detailFresh.value) return
  const token = readLane()
  loading.value = true; reviewed.value = ''; purgeReview.value = false
  try {
    const result = await listFunctions()
    if (!currentRead(token)) return
    functions.value = result.data?.functions || []
    if (!editing.value) Object.assign(draft, blankDraft())
    editing.value = true
    await nextTick(); if (currentRead(token)) formHeading.value?.focus()
  } catch {
    if (currentRead(token)) { fresh.value = false; error.value = 'Fresh targets unavailable. Refresh before reviewing changes.' }
  } finally { if (currentRead(token)) loading.value = false }
}
function request() {
  if (!targets.value.some(f => f.value === draft.target) || !draft.payload.trim()) throw new Error('Choose an exact new target UUID and enter a fresh JSON payload.')
  JSON.parse(draft.payload)
  const config = selected.value.kind === 'job'
    ? { scheduled_at: draft.at, max_attempts: draft.attempts }
    : { name: draft.name, expression: draft.expression, timezone: draft.timezone, enabled: draft.enabled }
  const base = JSON.stringify({ source: { kind: selected.value.kind, id: selected.value.id }, target_id: draft.target, reviewed: true, confirm: true, ...config })
  return `${base.slice(0, -1)},"payload":${draft.payload}}`
}
async function review() {
  if (!canManage.value || !detailFresh.value || busy.value) return
  try { reviewed.value = request(); error.value = ''; mutationError.value = ''; const token = { epoch }; await nextTick(); if (current(token)) reviewHeading.value?.focus() }
  catch { error.value = 'Choose an exact new target UUID and enter valid fresh payload/config.' }
}
watch(draft, () => { reviewed.value = ''; purgeReview.value = false; if (editing.value) drafts.set(key(selected.value), { ...draft }) })
async function mutate(op) {
  if (!canManage.value || !detailFresh.value || busy.value || loading.value) return
  let raw
  if (op === 'reissue') {
    try { if (!reviewed.value || reviewed.value !== request()) return }
    catch { reviewed.value = ''; error.value = 'Fresh payload/config changed. Review again.'; return }
    raw = reviewed.value
  } else {
    if (!purgeReview.value || selected.value.reserved) return
    raw = JSON.stringify({ selected: [{ kind: selected.value.kind, id: selected.value.id }], confirm: true })
  }
  readSeq++; controller?.abort()
  const token = { epoch }
  const sourceKey = key(selected.value)
  busy.value = true; fresh.value = false; detailFresh.value = false; error.value = ''; mutationError.value = ''; notice.value = ''
  try {
    const result = await historyRequest(op, raw)
    if (!current(token)) return
    if (op === 'reissue' ? result.data?.origin !== 'operator' || !/^[0-9a-f-]{36}$/i.test(result.data?.new_id) : result.data?.purged !== true) throw new Error('Outcome unknown')
    notice.value = op === 'reissue' ? `Created new operator work ${result.data.new_id}. Inspect its audit before retrying.` : 'Selected metadata purged. Audit retained.'
    drafts.delete(sourceKey)
    reviewed.value = ''; purgeReview.value = false; editing.value = false; selected.value = null
    busy.value = false
    await refresh(false)
  } catch (e) {
    if (current(token)) {
      reviewed.value = ''; purgeReview.value = false
      mutationError.value = e?.response?.status === 409
      ? 'Conflict or capacity refusal. Snapshot is stale. Draft retained; refresh and inspect before reviewing again.'
      : 'Request failed or outcome unknown. Draft retained; inspect history before retrying.'
    }
  } finally { busy.value = false }
}
watch(() => props.active, value => { if (value) refresh(false); else invalidate() }, { immediate: true })
onActivated(() => { live = true; if (props.active && !fresh.value && !loading.value) refresh(false) })
onDeactivated(() => { live = false; invalidate() })
onBeforeUnmount(() => { live = false; invalidate() })
</script>

<style scoped>
@reference "../../style.css";
.history-field { @apply mt-2 w-full bg-surface border border-border rounded-md p-3 text-sm text-foreground-strong focus:outline-none focus:ring-1 focus:ring-focus-ring; }
</style>
