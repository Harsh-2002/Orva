<template>
  <div class="space-y-4">
    <!-- Page header — matches Functions / Schedules style. -->
    <div class="flex items-start justify-between gap-4 flex-wrap">
      <div>
        <h1 class="text-xl font-semibold text-foreground-strong tracking-tight">
          Jobs
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
          Queued background work with automatic retries.
        </p>
      </div>
      <div class="flex items-center gap-2 text-xs text-foreground-muted">
        Background queue · {{ totalCount }} jobs
      </div>
    </div>

    <!-- Status filter strip.
         Mobile (<sm): chips scroll horizontally inside the strip with
         scroll-snap so the active filter doesn't get lost.

         The actions used to sit in this same row, pinned with shrink-0. On a
         360px phone that pair took ~180px of a ~330px content box, so the
         filter strip was left a ~150px window onto ~430px of chips and the
         next chip was sliced by the Enqueue button beside it. It read as a
         rendering bug rather than a scroller. Below sm the actions take their
         own row and the strip gets the full width; sm+ is unchanged. -->
    <div class="flex flex-col gap-2 sm:flex-row sm:items-center">
      <div class="flex items-center gap-2 sm:flex-wrap overflow-x-auto sm:overflow-visible scrollable snap-x min-w-0 flex-1">
        <Button
          v-for="opt in statusOptions"
          :key="opt.value"
          variant="chip"
          size="xs"
          :active="statusFilter === opt.value"
          class="shrink-0 snap-start"
          @click="selectStatus(opt.value)"
        >
          {{ opt.label }}
          <span
            v-if="counts[opt.value] !== undefined"
            class="ml-1 opacity-70 tabular-nums"
          >{{ counts[opt.value] }}</span>
        </Button>
      </div>
      <div class="flex items-center gap-2 shrink-0">
        <Button
          size="xs"
          @click="openEnqueue"
        >
          <Plus class="w-3 h-3" />
          Enqueue
        </Button>
        <Button
          variant="secondary"
          size="xs"
          @click="loadJobs"
        >
          <RefreshCcw class="w-3 h-3" />
          Refresh
        </Button>
      </div>
    </div>

    <!-- Enqueue drawer — minimal. The dashboard's job here is to be a
         convenient operator UI for one-off enqueues; real producers
         call the SDK / REST API directly. The "Schedule for later"
         toggle exposes v0.4 C2b. -->
    <Drawer
      v-model="enqueue.open"
      title="Enqueue a job"
      width="560px"
    >
      <div class="p-5 space-y-5 text-sm">
        <div>
          <label class="text-xs uppercase tracking-wider text-foreground-muted">Function</label>
          <select
            v-model="enqueue.fnId"
            class="mt-2 w-full bg-surface border border-border rounded px-3 py-2 text-sm text-foreground-strong focus:outline-none focus:border-focus-ring"
          >
            <option
              v-for="f in functions"
              :key="f.id"
              :value="f.id"
            >
              {{ f.name }} ({{ runtimeLabel(f.runtime) }})
            </option>
          </select>
        </div>
        <div>
          <label class="text-xs uppercase tracking-wider text-foreground-muted">Payload (JSON)</label>
          <textarea
            v-model="enqueue.payload"
            rows="6"
            spellcheck="false"
            class="mt-2 w-full bg-surface border border-border rounded p-3 text-xs text-foreground-strong font-mono focus:outline-none focus:border-focus-ring"
            placeholder="{&quot;hello&quot;:&quot;world&quot;}"
          />
        </div>
        <div>
          <label class="flex items-center gap-2 text-xs text-foreground-muted">
            <input
              v-model="enqueue.scheduleLater"
              type="checkbox"
            >
            Schedule for later
          </label>
          <p class="text-[11px] text-foreground-muted mt-1">
            Off: runs on the next scheduler tick (~5s). On: holds until the timestamp below.
          </p>
        </div>
        <div v-if="enqueue.scheduleLater">
          <label class="text-xs uppercase tracking-wider text-foreground-muted">Run at (local time)</label>
          <input
            v-model="enqueue.scheduledAt"
            type="datetime-local"
            class="mt-2 w-full bg-surface border border-border rounded px-3 py-2 text-sm text-foreground-strong focus:outline-none focus:border-focus-ring"
          >
        </div>
        <div
          v-if="enqueue.error"
          class="text-xs text-danger-fg"
        >
          {{ enqueue.error }}
        </div>
      </div>
      <template #footer>
        <div class="flex items-center justify-end gap-2">
          <Button
            variant="ghost"
            size="sm"
            @click="enqueue.open = false"
          >
            Cancel
          </Button>
          <Button
            size="sm"
            :disabled="!enqueue.fnId || enqueue.saving"
            :loading="enqueue.saving"
            @click="submitEnqueue"
          >
            Enqueue
          </Button>
        </div>
      </template>
    </Drawer>

    <LoadError
      v-if="loadError"
      what="Jobs"
      :message="loadError"
      :on-retry="loadJobs"
      class="mb-3"
    />

    <!-- Table. -->
    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <!-- Mobile (<sm) stacked-card list. Surfaces every column the
           desktop table hides (status, attempts, scheduled, finished)
           so nothing is silently dropped on small screens. -->
      <ul class="sm:hidden divide-y divide-border">
        <li
          v-for="job in filteredJobs"
          :key="job.id"
          class="px-4 py-3"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div class="font-medium text-foreground-strong truncate">
                {{ job.function_name || job.function_id }}
              </div>
              <div class="text-[10px] text-foreground-muted font-mono break-all">
                {{ job.id }}
              </div>
              <div class="mt-2">
                <span
                  class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] font-medium border"
                  :class="statusPill(jobStatus(job)).classes"
                >
                  <component
                    :is="statusPill(jobStatus(job)).icon"
                    class="w-3 h-3 shrink-0"
                    aria-hidden="true"
                  />
                  {{ jobStatus(job) }}
                </span>
              </div>
              <p
                v-if="job.last_error"
                class="text-[11px] text-danger-fg mt-1 break-all"
                :title="job.last_error"
              >
                {{ job.last_error }}
              </p>
              <dl class="mt-2 grid grid-cols-2 gap-x-3 gap-y-1 text-[11px] text-foreground-muted">
                <div>
                  <dt class="uppercase tracking-wider text-[10px]">
                    Attempts
                  </dt>
                  <dd>{{ job.attempts }} / {{ job.max_attempts }}</dd>
                </div>
                <div>
                  <dt class="uppercase tracking-wider text-[10px]">
                    Scheduled
                  </dt>
                  <dd>{{ formatDate(job.scheduled_at) }}</dd>
                </div>
                <div class="col-span-2">
                  <dt class="uppercase tracking-wider text-[10px]">
                    Finished
                  </dt>
                  <dd>{{ job.finished_at ? formatDate(job.finished_at) : EMPTY }}</dd>
                </div>
              </dl>
            </div>
            <div class="flex items-center gap-1 shrink-0">
              <IconButton
                v-if="job.status === 'failed'"
                :icon="RotateCcw"
                variant="success"
                title="Retry"
                @click="retry(job)"
              />
              <IconButton
                :icon="Trash2"
                variant="danger"
                title="Delete"
                @click="remove(job)"
              />
            </div>
          </div>
        </li>
        <li
          v-if="loaded && !loadError && filteredJobs.length === 0"
          class="px-4 py-12 text-center"
        >
          <p class="text-foreground-muted text-sm">
            {{ statusFilter === 'all' ? 'No jobs yet.' : `No ${statusFilter} jobs.` }}
          </p>
          <p class="text-foreground-muted text-xs mt-1">
            Enqueue jobs from a function with <code class="font-mono text-xs">orva.jobs.enqueue()</code>.
          </p>
        </li>
      </ul>

      <table class="hidden sm:table w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th class="px-6 py-3 font-medium">
              Function
            </th>
            <th class="px-6 py-3 font-medium">
              Status
            </th>
            <th class="px-6 py-3 font-medium hidden md:table-cell">
              Attempts
            </th>
            <th class="px-6 py-3 font-medium hidden lg:table-cell">
              Scheduled
            </th>
            <th class="px-6 py-3 font-medium hidden xl:table-cell">
              Finished
            </th>
            <th class="px-6 py-3 font-medium text-right">
              Actions
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr
            v-for="job in filteredJobs"
            :key="job.id"
            class="hover:bg-surface/50 transition-colors"
          >
            <td class="px-6 py-4 font-medium text-foreground-strong">
              <div class="flex flex-col">
                <span>{{ job.function_name || job.function_id }}</span>
                <span class="text-[10px] text-foreground-muted font-mono">{{ job.id }}</span>
              </div>
            </td>
            <td class="px-6 py-4">
              <span
                class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] font-medium border"
                :class="statusPill(jobStatus(job)).classes"
              >
                <component
                  :is="statusPill(jobStatus(job)).icon"
                  class="w-3 h-3 shrink-0"
                  aria-hidden="true"
                />
                {{ jobStatus(job) }}
              </span>
              <p
                v-if="job.last_error"
                class="text-[11px] text-danger-fg mt-1 truncate max-w-xs"
                :title="job.last_error"
              >
                {{ job.last_error }}
              </p>
            </td>
            <td class="px-6 py-4 text-foreground-muted text-xs hidden md:table-cell">
              {{ job.attempts }} / {{ job.max_attempts }}
            </td>
            <td class="px-6 py-4 text-foreground-muted text-xs hidden lg:table-cell">
              {{ formatDate(job.scheduled_at) }}
            </td>
            <td class="px-6 py-4 text-foreground-muted text-xs hidden xl:table-cell">
              {{ job.finished_at ? formatDate(job.finished_at) : EMPTY }}
            </td>
            <td class="px-6 py-4 text-right">
              <div class="inline-flex items-center gap-1">
                <IconButton
                  v-if="job.status === 'failed'"
                  :icon="RotateCcw"
                  variant="success"
                  title="Retry"
                  @click="retry(job)"
                />
                <IconButton
                  :icon="Trash2"
                  variant="danger"
                  title="Delete"
                  @click="remove(job)"
                />
              </div>
            </td>
          </tr>
          <tr v-if="loaded && !loadError && filteredJobs.length === 0">
            <td
              colspan="6"
              class="px-6 py-12 text-center"
            >
              <p class="text-foreground-muted text-sm">
                {{ statusFilter === 'all' ? 'No jobs yet.' : `No ${statusFilter} jobs.` }}
              </p>
              <p class="text-foreground-muted text-xs mt-1">
                Enqueue jobs from a function with <code class="font-mono text-xs">orva.jobs.enqueue()</code>.
              </p>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
defineOptions({ name: 'JobsView' })

import { EMPTY } from '@/utils/format'
import { ref, reactive, computed, onMounted, onUnmounted, onActivated, onDeactivated } from 'vue'
import { Trash2, RotateCcw, RefreshCcw, Plus, CheckCircle2, XCircle, Clock, Circle } from '@lucide/vue'
import { listJobs, retryJob, deleteJob, enqueueJob, listFunctions } from '@/api/endpoints'
import { useConfirmStore } from '@/stores/confirm'
import Button from '@/components/common/Button.vue'
import IconButton from '@/components/common/IconButton.vue'
import Drawer from '@/components/common/Drawer.vue'
import LoadError from '@/components/common/LoadError.vue'
import { runtimeLabel } from '@/utils/runtime'

const confirmStore = useConfirmStore()

const jobs = ref([])
const loadError = ref('')
const loaded = ref(false)
const functions = ref([])
const statusFilter = ref('all')
// countSource is always an unfiltered page: the pills count from it so they
// stay meaningful while a filter narrows the table.
const countSource = ref([])
const truncated = ref(false)
let pollTimer = null

// Enqueue drawer state. scheduleLater toggles whether the request body
// carries scheduled_at — when off the scheduler picks the row up on
// the next tick (~5s).
const enqueue = reactive({
  open: false,
  fnId: '',
  payload: '{}',
  scheduleLater: false,
  scheduledAt: '',
  saving: false,
  error: '',
})

const statusOptions = [
  { value: 'all',       label: 'All' },
  { value: 'pending',   label: 'Pending' },
  { value: 'running',   label: 'Running' },
  { value: 'succeeded', label: 'Succeeded' },
  { value: 'failed',    label: 'Failed' },
]

const totalCount = computed(() => jobs.value.length)
// The server already applied the filter; no second pass in the browser.
const filteredJobs = computed(() => jobs.value)
const counts = computed(() => {
  const c = { all: countSource.value.length }
  for (const j of countSource.value) c[j.status] = (c[j.status] || 0) + 1
  return c
})

// State is encoded three ways — color token + glyph + the status text —
// so it never relies on hue alone (WCAG 1.4.1). No animate-pulse on the
// running state: motion budget is <=150ms, so it uses a static info token.
// jobStatus separates the two things the stored status conflates.
//
// A job that fails and has attempts left goes back to 'pending' with a later
// scheduled_at (database/jobs.go RequeueForRetry), so a job that has already
// failed twice and is backing off is stored identically to one that has never
// run. The operator scanning this page cannot tell "waiting to start" from
// "failing repeatedly", which is the difference they came to find.
//
// attempts already distinguishes them, and the API already returns it, so this
// needs no schema change: pending with attempts spent is a retry.
const jobStatus = (job) =>
  job.status === 'pending' && (job.attempts || 0) > 0 ? 'retrying' : job.status

const statusPill = (s) => {
  switch (s) {
    case 'pending':
      return { classes: 'bg-warning-tint text-warning-fg border-warning-ring', icon: Clock }
    case 'retrying':
      // Danger, not warning: it has already failed. The count beside it says
      // how close this is to being given up on.
      return { classes: 'bg-danger-tint text-danger-fg border-danger-ring', icon: RotateCcw }
    case 'running':
      return { classes: 'bg-info-tint text-info-fg border-info-ring', icon: Clock }
    case 'succeeded':
      return { classes: 'bg-success-tint text-success-fg border-success-ring', icon: CheckCircle2 }
    case 'failed':
      return { classes: 'bg-danger-tint text-danger-fg border-danger-ring', icon: XCircle }
    default:
      return { classes: 'bg-surface text-foreground-muted border-border', icon: Circle }
  }
}

const formatDate = (s) => {
  if (!s) return EMPTY
  return new Date(s).toLocaleString('en-US', {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

const PAGE = 200

// Jobs were fetched as a hard-capped 200 rows and filtered in the browser,
// so past 200 jobs the "Failed" pill listed whatever failures happened to be
// in the newest page rather than the failures. The backend has supported a
// server-side status filter all along.
//
// The count pills are derived from an UNFILTERED page, so when a status is
// selected we fetch both: the filtered list is what the table shows, the
// unfiltered page is what the pills count. Two requests only while a filter
// is active.
// Changing the filter now has to refetch, because the narrowing happens on
// the server rather than in a computed over already-loaded rows.
const selectStatus = async (value) => {
  statusFilter.value = value
  await loadJobs()
}

const loadJobs = async () => {
  try {
    const [all, filtered] = await Promise.all([
      listJobs({ limit: PAGE }),
      statusFilter.value === 'all'
        ? Promise.resolve(null)
        : listJobs({ limit: PAGE, status: statusFilter.value }),
    ])
    countSource.value = all.data.jobs || []
    jobs.value = filtered ? (filtered.data.jobs || []) : countSource.value
    truncated.value = countSource.value.length === PAGE
    loadError.value = ''
  } catch (e) {
    // A failed fetch must not fall through to "No jobs yet" -- that tells the
    // operator they have nothing when we simply do not know.
    loadError.value = e?.response?.data?.error?.message || e?.message || 'Request failed'
  } finally {
    loaded.value = true
  }
}

const retry = async (job) => {
  try {
    await retryJob(job.id)
    await loadJobs()
  } catch (e) {
    confirmStore.notify({ title: 'Retry failed', message: e.message, danger: true })
  }
}

const remove = async (job) => {
  const ok = await confirmStore.ask({
    title: 'Delete job?',
    message: `Job ${job.id} will be removed. This cannot be undone.`,
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  try {
    await deleteJob(job.id)
    await loadJobs()
  } catch (e) {
    confirmStore.notify({ title: 'Delete failed', message: e.message, danger: true })
  }
}

const openEnqueue = async () => {
  enqueue.error = ''
  enqueue.payload = '{}'
  enqueue.scheduleLater = false
  enqueue.scheduledAt = ''
  if (functions.value.length === 0) {
    try {
      const res = await listFunctions()
      functions.value = res.data?.functions || []
      if (functions.value.length && !enqueue.fnId) {
        enqueue.fnId = functions.value[0].id
      }
    } catch (e) {
      console.error('list functions failed', e)
    }
  }
  enqueue.open = true
}

const submitEnqueue = async () => {
  enqueue.error = ''
  // Parse payload JSON up-front so a typo doesn't blow up server-side.
  let payload
  try {
    payload = enqueue.payload.trim() ? JSON.parse(enqueue.payload) : {}
  } catch (e) {
    enqueue.error = 'Payload must be valid JSON: ' + e.message
    return
  }
  const body = { function_id: enqueue.fnId, payload }
  if (enqueue.scheduleLater) {
    if (!enqueue.scheduledAt) {
      enqueue.error = 'Pick a date/time, or untick "Schedule for later".'
      return
    }
    // datetime-local fields are interpreted as the user's local zone.
    // Convert to UTC ISO so the backend (which stores UTC) doesn't get
    // mismatched.
    body.scheduled_at = new Date(enqueue.scheduledAt).toISOString()
  }
  enqueue.saving = true
  try {
    await enqueueJob(body)
    enqueue.open = false
    await loadJobs()
  } catch (e) {
    enqueue.error = e?.response?.data?.error?.message || e.message || 'Enqueue failed'
  } finally {
    enqueue.saving = false
  }
}

// Auto-refresh every 5s while the page is on screen so running jobs
// visibly transition. Starting is guarded so an HMR pass or a re-entry
// can never leak a second interval.
const startPoll = () => {
  if (!pollTimer) pollTimer = setInterval(loadJobs, 5000)
}
const stopPoll = () => {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

onMounted(() => {
  loadJobs()
  startPoll()
})

// Layout.vue wraps views in <keep-alive>, so navigating away deactivates this
// view rather than unmounting it: onBeforeUnmount alone left the poll hitting
// the API for the rest of the session. onActivated also fires on the first
// mount, where onMounted has already loaded and started the timer, so the
// running timer is what tells the two apart.
onActivated(() => {
  if (pollTimer) return
  loadJobs()
  startPoll()
})
onDeactivated(stopPoll)
onUnmounted(stopPoll)
</script>
