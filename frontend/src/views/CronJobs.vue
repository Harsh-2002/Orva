<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-foreground-strong tracking-tight">
          Scheduled jobs
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
          Run functions on a cron schedule.
        </p>
      </div>
      <div class="flex gap-2">
        <Button
          v-if="historyEnabled"
          variant="secondary"
          @click="historyOpen = !historyOpen"
        >
          History
        </Button>
        <Button
          v-if="canManage"
          :disabled="blocked"
          @click="openCreate"
        >
          <PlusCircle class="w-4 h-4" />
          New schedule
        </Button>
        <Button
          ref="refreshControl"
          variant="secondary"
          :disabled="refreshing || saving"
          @click="loadJobs"
        >
          Refresh
        </Button>
      </div>
    </div>
    <HistoryInspector
      v-if="historyEnabled"
      v-show="historyOpen"
      :active="historyOpen"
    />

    <LoadError
      v-if="loadError"
      what="Scheduled jobs"
      :message="loadError"
      :on-retry="loadJobs"
      class="mb-3"
    />
    <p
      v-if="mutationError"
      role="alert"
      class="text-sm text-danger break-words"
    >
      {{ mutationError }}
    </p>
    <p
      v-if="stale"
      role="status"
      class="text-sm text-foreground-muted"
    >
      Snapshot is stale. Refresh and review current authority before changing schedules.
    </p>
    <p
      v-else-if="loaded && !canManage"
      class="text-sm text-foreground-muted"
    >
      Read-only. Schedule management requires current admin authority.
    </p>

    <div class="bg-background border border-border rounded-lg overflow-x-auto scrollable">
      <!-- Mobile (<sm) stacked-row list. -->
      <ul class="sm:hidden divide-y divide-border">
        <li
          v-for="job in jobs"
          :key="job.id"
          :data-schedule-id="job.id"
          class="px-4 py-3 cursor-pointer"
          @click="rowAction(() => editSchedule(job))($event)"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2 flex-wrap">
                <button
                  type="button"
                  class="inline-flex items-center min-h-[1.5rem] touch-expand-xs max-w-full text-sm font-medium text-foreground truncate text-left rounded-md hover:underline underline-offset-4 focus:outline-none focus-visible:ring-2 focus-visible:ring-focus-ring"
                  @click="editSchedule(job)"
                >
                  {{ job.function_name }}
                </button>
                <span
                  v-if="job.name && !managed"
                  class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium border bg-surface-subtle text-foreground-muted border-border"
                  :title="`Declared by the function itself as &quot;${job.name}&quot;, not created here`"
                >
                  SDK
                </span>
                <span
                  class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] font-medium border"
                  :class="job.enabled ? 'bg-success-tint text-success-fg border-success-ring' : 'bg-warning-tint text-warning-fg border-warning-ring'"
                >
                  <component
                    :is="job.enabled ? CheckCircle2 : Clock"
                    class="h-3 w-3 shrink-0"
                    aria-hidden="true"
                  />
                  {{ job.enabled ? (managed ? 'Enabled' : 'Active') : (managed ? 'Disabled' : 'Paused') }}
                </span>
              </div>
              <JobAuthority
                v-if="managed"
                :job="job"
                kind="schedule"
                :can-manage="canManage && !blocked"
                :stale="stale"
                :busy="saving"
                @resume="resumeSchedule"
              />
              <div class="mt-1 text-[11px] text-foreground font-mono break-all">
                {{ job.cron_expression }}
              </div>
              <div class="mt-0.5 text-[11px] text-foreground-muted">
                {{ humanizeCron(job.cron_expression) }}
                <span class="text-foreground-muted">· {{ job.timezone || 'UTC' }}</span>
              </div>
              <div class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-foreground-muted">
                <span>last {{ job.last_run_at ? formatDate(job.last_run_at) : EMPTY }}</span>
                <span>next {{ job.next_run_at ? formatDate(job.next_run_at) : EMPTY }}</span>
              </div>
            </div>
            <div
              v-if="canManage"
              class="flex items-center gap-1 shrink-0"
            >
              <IconButton
                v-if="!managed || job.authority_state === 'active'"
                :disabled="blocked"
                :icon="job.enabled ? Pause : Play"
                :title="job.enabled ? 'Disable schedule' : 'Enable schedule'"
                @click="toggleSchedule(job)"
              />
              <IconButton
                v-if="!managed || job.authority_state === 'active'"
                :disabled="blocked"
                :icon="Edit"
                title="Edit"
                @click="editSchedule(job)"
              />
              <IconButton
                :disabled="blocked"
                :icon="Trash2"
                variant="danger"
                title="Delete"
                @click="deleteSchedule(job)"
              />
            </div>
          </div>
        </li>
        <li
          v-if="loaded && !loadError && jobs.length === 0"
          class="px-6 py-12 text-center"
        >
          <p class="text-foreground-muted">
            No scheduled jobs yet.
          </p>
          <p class="text-foreground-muted text-xs mt-1">
            Create a schedule to run a function automatically.
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
              Schedule
            </th>
            <th class="px-6 py-3 font-medium hidden sm:table-cell">
              Status
            </th>
            <th class="px-6 py-3 font-medium hidden md:table-cell">
              Last Run
            </th>
            <th class="px-6 py-3 font-medium hidden lg:table-cell">
              Next Run
            </th>
            <th class="px-6 py-3 font-medium text-right">
              Actions
            </th>
          </tr>
        </thead>
        <tbody class="divide-y divide-border">
          <tr
            v-for="job in jobs"
            :key="job.id"
            :data-schedule-id="job.id"
            class="hover:bg-surface-hover transition-colors cursor-pointer"
            @click="rowAction(() => editSchedule(job))($event)"
          >
            <td class="px-6 py-4 font-medium text-foreground max-w-[16rem]">
              <div class="flex items-center gap-2 min-w-0">
                <button
                  type="button"
                  class="inline-flex items-center min-h-[1.5rem] touch-expand-xs max-w-full truncate text-left rounded-md hover:underline underline-offset-4 focus:outline-none focus-visible:ring-2 focus-visible:ring-focus-ring"
                  @click="editSchedule(job)"
                >
                  {{ job.function_name }}
                </button>
                <!-- A schedule with a name was declared by the function's own
                     code; the dashboard cannot set one. That is what makes an
                     unexpected entry here visible as one. -->
                <span
                  v-if="job.name && !managed"
                  class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium border bg-surface-subtle text-foreground-muted border-border shrink-0"
                  :title="`Declared by the function itself as &quot;${job.name}&quot;, not created here`"
                >
                  SDK
                </span>
              </div>
              <JobAuthority
                v-if="managed"
                :job="job"
                kind="schedule"
                :can-manage="canManage && !blocked"
                :stale="stale"
                :busy="saving"
                @resume="resumeSchedule"
              />
            </td>
            <td class="px-6 py-4">
              <div class="flex flex-col gap-1">
                <span class="text-foreground font-mono text-xs break-all">{{ job.cron_expression }}</span>
                <span class="text-foreground-muted text-[10px]">
                  {{ humanizeCron(job.cron_expression) }}
                  <span class="text-foreground-muted">· {{ job.timezone || 'UTC' }}</span>
                </span>
              </div>
            </td>
            <td class="px-6 py-4 hidden sm:table-cell">
              <span
                class="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium border"
                :class="job.enabled ? 'bg-success-tint text-success-fg border-success-ring' : 'bg-warning-tint text-warning-fg border-warning-ring'"
              >
                <component
                  :is="job.enabled ? CheckCircle2 : Clock"
                  class="h-3 w-3 shrink-0"
                  aria-hidden="true"
                />
                {{ job.enabled ? (managed ? 'Enabled' : 'Active') : (managed ? 'Disabled' : 'Paused') }}
              </span>
            </td>
            <td class="px-6 py-4 text-foreground-muted text-xs hidden md:table-cell">
              {{ job.last_run_at ? formatDate(job.last_run_at) : EMPTY }}
            </td>
            <td class="px-6 py-4 text-foreground-muted text-xs hidden lg:table-cell">
              {{ job.next_run_at ? formatDate(job.next_run_at) : EMPTY }}
            </td>
            <td class="px-6 py-4 text-right">
              <div
                v-if="canManage"
                class="inline-flex items-center gap-1"
              >
                <IconButton
                  v-if="!managed || job.authority_state === 'active'"
                  :disabled="blocked"
                  :icon="job.enabled ? Pause : Play"
                  :title="job.enabled ? 'Disable schedule' : 'Enable schedule'"
                  @click="toggleSchedule(job)"
                />
                <IconButton
                  v-if="!managed || job.authority_state === 'active'"
                  :disabled="blocked"
                  :icon="Edit"
                  title="Edit"
                  @click="editSchedule(job)"
                />
                <IconButton
                  :disabled="blocked"
                  :icon="Trash2"
                  variant="danger"
                  title="Delete"
                  @click="deleteSchedule(job)"
                />
              </div>
            </td>
          </tr>
          <tr v-if="loaded && !loadError && jobs.length === 0">
            <td
              colspan="6"
              class="px-6 py-12 text-center"
            >
              <p class="text-foreground-muted">
                No scheduled jobs yet.
              </p>
              <p class="text-foreground-muted text-xs mt-1">
                Create a schedule to run a function automatically.
              </p>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Create/Edit Modal -->
    <Modal
      :model-value="showCreateModal"
      :title="editingJob ? 'Edit schedule' : 'Create schedule'"
      size="lg"
      @update:model-value="(v) => { if (!v) closeModal() }"
    >
      <div class="space-y-5">
        <!-- Function Selection -->
        <div>
          <label
            for="cron-function"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-2"
          >Function</label>
          <FilterSelect
            v-model="form.function_name"
            :options="fnOptions"
            :disabled="!!editingJob"
            label="Select a function"
            trigger-id="cron-function"
            wide
          />
        </div>

        <!-- Schedule Type Tabs -->
        <div>
          <span
            id="cron-schedule-type-label"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-2"
          >Schedule Type</span>
          <div
            class="flex gap-2 bg-background rounded-lg p-1 border border-border"
            role="group"
            aria-labelledby="cron-schedule-type-label"
          >
            <button
              v-for="type in ['simple', 'advanced']"
              :key="type"
              class="flex-1 py-2 px-3 text-sm font-medium rounded transition-colors touch-expand-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background"
              :class="scheduleType === type ? 'bg-primary text-primary-foreground shadow-sm' : 'text-foreground-muted hover:text-foreground'"
              :aria-pressed="scheduleType === type"
              @click="scheduleType = type"
            >
              {{ type === 'simple' ? 'Natural language' : 'Cron expression' }}
            </button>
          </div>
        </div>

        <!-- Simple Schedule -->
        <div
          v-if="scheduleType === 'simple'"
          class="space-y-4"
        >
          <div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
            <div>
              <label
                for="cron-frequency"
                class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
              >Frequency</label>
              <FilterSelect
                v-model="simpleSchedule.frequency"
                :options="frequencyOptions"
                label="Frequency"
                trigger-id="cron-frequency"
                wide
                @update:model-value="updateCronFromSimple"
              />
            </div>

            <div v-if="['hour', 'day', 'week', 'month'].includes(simpleSchedule.frequency)">
              <label
                for="cron-minute"
                class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
              >At Minute</label>
              <input
                id="cron-minute"
                v-model.number="simpleSchedule.minute"
                type="number"
                min="0"
                max="59"
                class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
                @input="updateCronFromSimple"
              >
            </div>

            <div v-if="['day', 'week', 'month'].includes(simpleSchedule.frequency)">
              <label
                for="cron-hour"
                class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
              >At Hour</label>
              <input
                id="cron-hour"
                v-model.number="simpleSchedule.hour"
                type="number"
                min="0"
                max="23"
                class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
                @input="updateCronFromSimple"
              >
            </div>

            <div v-if="simpleSchedule.frequency === 'week'">
              <label
                for="cron-day-of-week"
                class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
              >Day of Week</label>
              <FilterSelect
                v-model="simpleSchedule.dayOfWeek"
                :options="dayOfWeekOptions"
                label="Day of Week"
                trigger-id="cron-day-of-week"
                wide
                @update:model-value="updateCronFromSimple"
              />
            </div>

            <div v-if="simpleSchedule.frequency === 'month'">
              <label
                for="cron-day-of-month"
                class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
              >Day of Month</label>
              <input
                id="cron-day-of-month"
                v-model.number="simpleSchedule.dayOfMonth"
                type="number"
                min="1"
                max="31"
                class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
                @input="updateCronFromSimple"
              >
            </div>
          </div>

          <div class="bg-background border border-border rounded-lg p-4">
            <div class="text-xs font-medium text-foreground-muted uppercase tracking-wide mb-2">
              Generated expression
            </div>
            <div class="font-mono text-sm text-foreground">
              {{ form.cron }}
            </div>
            <div class="text-xs text-foreground-muted mt-1">
              {{ humanizeCron(form.cron) }}
            </div>
          </div>
        </div>

        <!-- Advanced Schedule -->
        <div
          v-if="scheduleType === 'advanced'"
          class="space-y-3"
        >
          <div>
            <label
              for="cron-expression"
              class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
            >Cron expression</label>
            <input
              id="cron-expression"
              v-model="form.cron"
              placeholder="* * * * *"
              aria-describedby="cron-expression-hint"
              class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm font-mono text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
            >
            <p
              id="cron-expression-hint"
              class="text-xs text-foreground-muted mt-1.5"
            >
              Format: minute hour day month weekday
            </p>
          </div>

          <div class="bg-background border border-border rounded-lg p-4">
            <div class="text-xs font-medium text-foreground-muted uppercase tracking-wide mb-2">
              Preview
            </div>
            <div class="text-xs text-foreground">
              {{ humanizeCron(form.cron) }}
            </div>
          </div>
        </div>

        <!-- Timezone -->
        <div>
          <label
            for="cron-timezone"
            class="block text-xs font-medium text-foreground-muted uppercase tracking-wide mb-1.5"
          >
            Timezone
          </label>
          <FilterSelect
            v-model="form.timezone"
            :options="timezonePickerOptions"
            label="Timezone"
            trigger-id="cron-timezone"
            wide
          />
          <div
            id="cron-timezone-hint"
            class="text-xs text-foreground-muted mt-1.5"
          >
            The cron expression is interpreted in this zone (e.g.
            <code class="bg-surface px-1 rounded">0 9 * * *</code>
            with timezone
            <code class="bg-surface px-1 rounded">{{ form.timezone }}</code>
            fires at 9 AM local time every day.
          </div>
        </div>

        <!-- Enabled Toggle -->
        <div class="flex items-center gap-3">
          <input
            id="enabled-toggle"
            v-model="form.enabled"
            type="checkbox"
            class="w-4 h-4 accent-primary focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background"
          >
          <label
            for="enabled-toggle"
            class="text-sm font-medium text-foreground cursor-pointer"
          >
            Enable schedule immediately
          </label>
        </div>
      </div>

      <template #footer>
        <Button
          variant="ghost"
          @click="closeModal"
        >
          Cancel
        </Button>
        <Button
          :disabled="blocked || !canManage || !form.function_name || !form.cron"
          @click="saveSchedule"
        >
          {{ editingJob ? 'Update' : 'Create' }} schedule
        </Button>
      </template>
    </Modal>
  </div>
</template>

<script setup>
defineProps({ historyEnabled: { type: Boolean, default: false } })
import HistoryInspector from '@/components/history/HistoryInspector.vue'
const historyOpen = ref(false)
import { humanizeCron } from '@/utils/cronDescription'
import { EMPTY } from '@/utils/format'
import { ref, computed, nextTick, onMounted, onActivated, onDeactivated, onBeforeUnmount } from 'vue'
import { PlusCircle, Trash2, Clock, Edit, Play, Pause, CheckCircle2 } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import FilterSelect from '@/components/common/FilterSelect.vue'
import IconButton from '@/components/common/IconButton.vue'
import { rowAction } from '@/utils/rowNav'
import Modal from '@/components/common/Modal.vue'
import LoadError from '@/components/common/LoadError.vue'
import { listCronSchedules, createCronSchedule, updateCronSchedule, deleteCronSchedule, resumeCronSchedule, listFunctions, browserTimezone } from '@/api/endpoints'
import JobAuthority from '@/components/jobs/JobAuthority.vue'
import { useConfirmStore } from '@/stores/confirm'
import { runtimeLabel } from '@/utils/runtime'

// Detect the operator's browser timezone so new schedules default to
// it (operators expect "every day at 9 AM" to mean their 9 AM, not
// orvad's process 9 AM which is typically UTC in containers).
const detectedTZ = browserTimezone()

// timezoneOptions is a curated list — full IANA list has 600+ zones,
// but ~95% of operators want one of these or their own browser TZ.
// `detectedTZ` is always shown first (and labelled) so the operator's
// own zone is one click away, then a few major hubs by region.
const timezoneOptions = (() => {
  const major = [
    'UTC',
    'America/Los_Angeles', 'America/New_York', 'America/Chicago', 'America/Denver',
    'America/Sao_Paulo',
    'Europe/London', 'Europe/Berlin', 'Europe/Paris', 'Europe/Moscow',
    'Africa/Lagos', 'Africa/Cairo', 'Africa/Johannesburg',
    'Asia/Dubai', 'Asia/Kolkata', 'Asia/Singapore', 'Asia/Shanghai', 'Asia/Tokyo',
    'Australia/Sydney',
    'Pacific/Auckland',
  ]
  const set = new Set([detectedTZ, ...major])
  return [...set]
})()

const confirmStore = useConfirmStore()

const jobs = ref([])
const loadError = ref('')
const loaded = ref(false)
const canManage = ref(false)
const managed = ref(false)
const stale = ref(true)
const refreshing = ref(false)
const saving = ref(false)
const mutationError = ref('')
const blocked = computed(() => stale.value || refreshing.value || saving.value)
let active = false
let generation = 0
let functionGeneration = 0
const refreshControl = ref(null)
const functions = ref([])
const fnOptions = computed(() => [
  { value: '', label: 'Select a function' },
  ...functions.value.map((fn) => ({ value: fn.name, label: `${fn.name} (${runtimeLabel(fn.runtime)})` })),
])
const frequencyOptions = [
  { value: 'minute', label: 'Every Minute' },
  { value: 'hour', label: 'Hourly' },
  { value: 'day', label: 'Daily' },
  { value: 'week', label: 'Weekly' },
  { value: 'month', label: 'Monthly' },
]
const dayOfWeekOptions = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
  .map((label, i) => ({ value: String(i), label }))
const timezonePickerOptions = timezoneOptions.map((tz) => ({
  value: tz, label: tz === detectedTZ ? `${tz}  (your browser)` : tz,
}))
const showCreateModal = ref(false)
const editingJob = ref(null)
const scheduleType = ref('simple')

const form = ref({
  function_name: '',
  cron: '0 0 * * *',
  timezone: detectedTZ,
  enabled: true
})

const simpleSchedule = ref({
  frequency: 'day',
  minute: 0,
  hour: 0,
  dayOfWeek: 1,
  dayOfMonth: 1
})

const loadJobs = async () => {
  if (!active || saving.value) return
  const request = ++generation
  refreshing.value = true
  try {
    const res = await listCronSchedules()
    if (!active || request !== generation) return
    if (!Array.isArray(res.data.schedules)) throw new Error('Invalid schedule response')
    jobs.value = res.data.schedules
    managed.value = res.data.cron_controls === true || jobs.value.some(row => row.authority_state)
    canManage.value = managed.value ? res.data.cron_controls === true && res.data.can_manage === true : true
    stale.value = false
    loadError.value = ''
  } catch (e) {
    if (!active || request !== generation) return
    stale.value = true
    loadError.value = e?.response?.data?.error?.message || e?.message || 'Request failed'
  } finally {
    if (active && request === generation) { loaded.value = true; refreshing.value = false }
  }
}

const loadFunctions = async () => {
  const request = ++functionGeneration
  try {
    const res = await listFunctions()
    if (!active || request !== functionGeneration) return
    functions.value = res.data.functions || []
  } catch (e) {
    if (!active || request !== functionGeneration) return
    confirmStore.notify({
      title: 'Could not load functions',
      message: e?.response?.data?.error?.message || e?.message || 'Unknown error',
      danger: true,
    })
  }
}

const updateCronFromSimple = () => {
  const { frequency, minute, hour, dayOfWeek, dayOfMonth } = simpleSchedule.value
  
  switch (frequency) {
    case 'minute':
      form.value.cron = '* * * * *'
      break
    case 'hour':
      form.value.cron = `${minute} * * * *`
      break
    case 'day':
      form.value.cron = `${minute} ${hour} * * *`
      break
    case 'week':
      form.value.cron = `${minute} ${hour} * * ${dayOfWeek}`
      break
    case 'month':
      form.value.cron = `${minute} ${hour} ${dayOfMonth} * *`
      break
  }
}

const formatDate = (date) => {
  return new Date(date).toLocaleString('en-US', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit'
  })
}

const saveSchedule = async () => {
  if (!canManage.value || blocked.value) return
  saving.value = true
  const request = ++generation
  try {
    if (editingJob.value) {
      // Edit existing — use the schedule id we tracked when opening the modal.
      await updateCronSchedule(editingJob.value.id, {
        function_id: editingJob.value.function_id,
        cron: form.value.cron,
        timezone: form.value.timezone,
        enabled: form.value.enabled,
        ...expectations(editingJob.value),
      })
    } else {
      await createCronSchedule(form.value.function_name, {
        cron: form.value.cron,
        timezone: form.value.timezone,
        enabled: form.value.enabled,
      })
    }
    if (!active || request !== generation) return
    saving.value = false
    mutationError.value = ''
    closeModal()
    await loadJobs()
    if (active) { await nextTick(); refreshControl.value?.$el?.focus() }
  } catch (e) {
    if (active && request === generation) mutationFailed(e)
  } finally {
    if (active && request === generation) saving.value = false
  }
}

const editSchedule = (job) => {
  if (!canManage.value || blocked.value || (managed.value && job.authority_state !== 'active')) return
  editingJob.value = job
  form.value = {
    function_name: job.function_name,
    cron: job.cron_expression,
    timezone: job.timezone || 'UTC',
    enabled: job.enabled
  }
  // The natural-language controls cannot round-trip an arbitrary expression:
  // they only build five shapes, and any change to one of them overwrites the
  // loaded cron. Opening on the expression the job actually runs keeps the
  // form honest and stops a weekday 9am job silently becoming daily midnight.
  scheduleType.value = 'advanced'
  showCreateModal.value = true
}

const toggleSchedule = async (job) => {
  await mutateSchedule(job, () => updateCronSchedule(job.id, {
      function_id: job.function_id,
      enabled: !job.enabled,
      ...expectations(job),
    }))
}

const deleteSchedule = async (job) => {
  if (!canManage.value || blocked.value) return
  const ok = await confirmStore.ask({
    title: 'Delete schedule?',
    message: `Cron schedule for "${job.function_name}" will be removed.`,
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return

  await mutateSchedule(job, () => deleteCronSchedule(job.id, job.function_id, expectations(job)))
}
const expectations = job => managed.value ? { expected_config_revision: job.config_revision, expected_authority_generation: job.authority_generation } : {}
const mutationFailed = error => {
  saving.value = false
  stale.value = true
  mutationError.value = error?.response?.status === 409
    ? 'Schedule changed or is not eligible. Refresh and review before trying again.'
    : (error?.response?.data?.error?.message || error?.message || 'Schedule change failed')
}
const mutateSchedule = async (job, send) => {
  if (!active || !canManage.value || blocked.value) return
  if (managed.value && (typeof job.config_revision !== 'string' || typeof job.authority_generation !== 'string')) { stale.value = true; return }
  saving.value = true
  const request = ++generation
  try {
    await send()
    if (!active || request !== generation) return
    saving.value = false
    mutationError.value = ''
    await loadJobs()
    if (active) { await nextTick(); refreshControl.value?.$el?.focus() }
  } catch (error) {
    if (active && request === generation) mutationFailed(error)
  } finally {
    if (active && request === generation) saving.value = false
  }
}
const resumeSchedule = async job => {
  if (!canManage.value || blocked.value || job.authority_state !== 'suspended') return
  const ok = await confirmStore.ask({ title: 'Resume schedule?', message: 'Revalidate its original authority and schedule the next future firing only. Missed firings will not run.', confirmLabel: 'Resume schedule' })
  if (ok) await mutateSchedule(job, () => resumeCronSchedule(job.id, job.function_id, expectations(job)))
}
const openCreate = () => {
  if (!canManage.value || blocked.value) return
  loadFunctions()
  showCreateModal.value = true
}

const closeModal = () => {
  showCreateModal.value = false
  editingJob.value = null
  form.value = {
    function_name: '',
    cron: '0 0 * * *',
    timezone: detectedTZ,
    enabled: true
  }
  simpleSchedule.value = {
    frequency: 'day',
    minute: 0,
    hour: 0,
    dayOfWeek: 1,
    dayOfMonth: 1
  }
  scheduleType.value = 'simple'
}

const activate = () => {
  if (active) return
  active = true
  loadJobs()
  updateCronFromSimple()
}
const deactivate = () => { active = false; generation++; functionGeneration++; saving.value = false; refreshing.value = false; stale.value = true; closeModal() }
onMounted(activate)
onActivated(activate)
onDeactivated(deactivate)
onBeforeUnmount(deactivate)
</script>
