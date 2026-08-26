<template>
  <div class="space-y-6">
    <div class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold text-foreground-strong tracking-tight">
          Scheduled Jobs
        </h1>
        <p class="text-sm text-foreground-muted mt-1.5 max-w-prose leading-body">
          Run functions on a cron schedule.
        </p>
      </div>
      <Button @click="showCreateModal = true">
        <PlusCircle class="w-4 h-4" />
        New Schedule
      </Button>
    </div>

    <LoadError
      v-if="loadError"
      what="Scheduled jobs"
      :message="loadError"
      :on-retry="loadJobs"
      class="mb-3"
    />

    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <!-- Mobile (<sm) stacked-row list. -->
      <ul class="sm:hidden divide-y divide-border">
        <li
          v-for="job in jobs"
          :key="job.id"
          class="px-4 py-3"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2 flex-wrap">
                <span class="font-medium text-foreground truncate">{{ job.function_name }}</span>
                <span
                  v-if="job.name"
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
                  {{ job.enabled ? 'Active' : 'Paused' }}
                </span>
              </div>
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
            <div class="flex items-center gap-1 shrink-0">
              <IconButton
                :icon="job.enabled ? Pause : Play"
                :title="job.enabled ? 'Pause' : 'Resume'"
                @click="toggleSchedule(job)"
              />
              <IconButton
                :icon="Edit"
                title="Edit"
                @click="editSchedule(job)"
              />
              <IconButton
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
            class="hover:bg-surface-hover transition-colors"
          >
            <td class="px-6 py-4 font-medium text-foreground max-w-[16rem]">
              <div class="flex items-center gap-2 min-w-0">
                <span class="truncate">{{ job.function_name }}</span>
                <!-- A schedule with a name was declared by the function's own
                     code; the dashboard cannot set one. That is what makes an
                     unexpected entry here visible as one. -->
                <span
                  v-if="job.name"
                  class="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium border bg-surface-subtle text-foreground-muted border-border shrink-0"
                  :title="`Declared by the function itself as &quot;${job.name}&quot;, not created here`"
                >
                  SDK
                </span>
              </div>
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
                {{ job.enabled ? 'Active' : 'Paused' }}
              </span>
            </td>
            <td class="px-6 py-4 text-foreground-muted text-xs hidden md:table-cell">
              {{ job.last_run_at ? formatDate(job.last_run_at) : EMPTY }}
            </td>
            <td class="px-6 py-4 text-foreground-muted text-xs hidden lg:table-cell">
              {{ job.next_run_at ? formatDate(job.next_run_at) : EMPTY }}
            </td>
            <td class="px-6 py-4 text-right">
              <div class="inline-flex items-center gap-1">
                <IconButton
                  :icon="job.enabled ? Pause : Play"
                  :title="job.enabled ? 'Pause' : 'Resume'"
                  @click="toggleSchedule(job)"
                />
                <IconButton
                  :icon="Edit"
                  title="Edit"
                  @click="editSchedule(job)"
                />
                <IconButton
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
      :title="editingJob ? 'Edit Schedule' : 'Create Schedule'"
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
          <select
            id="cron-function"
            v-model="form.function_name"
            class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
            :disabled="!!editingJob"
          >
            <option value="">
              Select a function
            </option>
            <option
              v-for="fn in functions"
              :key="fn.name"
              :value="fn.name"
            >
              {{ fn.name }} ({{ runtimeLabel(fn.runtime) }})
            </option>
          </select>
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
              {{ type === 'simple' ? 'Natural Language' : 'Cron Expression' }}
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
                class="text-xs font-medium text-foreground-muted block mb-1.5"
              >Frequency</label>
              <select
                id="cron-frequency"
                v-model="simpleSchedule.frequency"
                class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
                @change="updateCronFromSimple"
              >
                <option value="minute">
                  Every Minute
                </option>
                <option value="hour">
                  Hourly
                </option>
                <option value="day">
                  Daily
                </option>
                <option value="week">
                  Weekly
                </option>
                <option value="month">
                  Monthly
                </option>
              </select>
            </div>

            <div v-if="['hour', 'day', 'week', 'month'].includes(simpleSchedule.frequency)">
              <label
                for="cron-minute"
                class="text-xs font-medium text-foreground-muted block mb-1.5"
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
                class="text-xs font-medium text-foreground-muted block mb-1.5"
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
                class="text-xs font-medium text-foreground-muted block mb-1.5"
              >Day of Week</label>
              <select
                id="cron-day-of-week"
                v-model="simpleSchedule.dayOfWeek"
                class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
                @change="updateCronFromSimple"
              >
                <option value="0">
                  Sunday
                </option>
                <option value="1">
                  Monday
                </option>
                <option value="2">
                  Tuesday
                </option>
                <option value="3">
                  Wednesday
                </option>
                <option value="4">
                  Thursday
                </option>
                <option value="5">
                  Friday
                </option>
                <option value="6">
                  Saturday
                </option>
              </select>
            </div>

            <div v-if="simpleSchedule.frequency === 'month'">
              <label
                for="cron-day-of-month"
                class="text-xs font-medium text-foreground-muted block mb-1.5"
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
              Generated Expression
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
              class="text-xs font-medium text-foreground-muted block mb-1.5"
            >Cron Expression</label>
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
          <select
            id="cron-timezone"
            v-model="form.timezone"
            aria-describedby="cron-timezone-hint"
            class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
          >
            <option
              v-for="tz in timezoneOptions"
              :key="tz"
              :value="tz"
            >
              {{ tz }}{{ tz === detectedTZ ? '  (your browser)' : '' }}
            </option>
          </select>
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
          :disabled="!form.function_name || !form.cron"
          @click="saveSchedule"
        >
          {{ editingJob ? 'Update' : 'Create' }} Schedule
        </Button>
      </template>
    </Modal>
  </div>
</template>

<script setup>
import { EMPTY } from '@/utils/format'
import { ref, onMounted } from 'vue'
import { PlusCircle, Trash2, Clock, Edit, Play, Pause, CheckCircle2 } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import IconButton from '@/components/common/IconButton.vue'
import Modal from '@/components/common/Modal.vue'
import LoadError from '@/components/common/LoadError.vue'
import { listCronSchedules, createCronSchedule, updateCronSchedule, deleteCronSchedule, listFunctions, browserTimezone } from '@/api/endpoints'
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
const functions = ref([])
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
  try {
    const res = await listCronSchedules()
    jobs.value = res.data.schedules || []
    loadError.value = ''
  } catch (e) {
    loadError.value = e?.response?.data?.error?.message || e?.message || 'Request failed'
  } finally {
    loaded.value = true
  }
}

const loadFunctions = async () => {
  try {
    const res = await listFunctions()
    functions.value = res.data.functions || []
  } catch (e) {
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

// A cron field is only describable in words when it is a single in-range
// integer. Ranges, lists, steps and day names ("1-5", "1,3,5", "*/2", "MON")
// index nothing in a weekday table and pad to nonsense in a clock reading, so
// they must fall through to the literal expression instead of being spelled
// out. `0 9 * * 1-5` used to render "Every undefined at 09:00".
const plainField = (field, lo, hi) => {
  if (!/^\d{1,2}$/.test(field)) return false
  const n = Number(field)
  return n >= lo && n <= hi
}

const humanizeCron = (cron) => {
  if (!cron) return 'Invalid expression'

  const parts = cron.trim().split(/\s+/)
  if (parts.length !== 5) return 'Invalid format (use 5 fields)'

  const [min, hour, day, month, dow] = parts

  if (cron === '* * * * *') return 'Every minute'

  const atTime = `${hour.padStart(2, '0')}:${min.padStart(2, '0')}`

  if (plainField(min, 0, 59) && hour === '*' && day === '*' && month === '*' && dow === '*') {
    return `Every hour at minute ${min}`
  }
  if (plainField(min, 0, 59) && plainField(hour, 0, 23) && day === '*' && month === '*' && dow === '*') {
    return `Every day at ${atTime}`
  }
  if (plainField(min, 0, 59) && plainField(hour, 0, 23) && day === '*' && month === '*' && plainField(dow, 0, 6)) {
    const days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
    return `Every ${days[Number(dow)]} at ${atTime}`
  }
  if (plainField(min, 0, 59) && plainField(hour, 0, 23) && plainField(day, 1, 31) && month === '*' && dow === '*') {
    return `On day ${day} of every month at ${atTime}`
  }

  return `Custom: ${cron}`
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
  try {
    if (editingJob.value) {
      // Edit existing — use the schedule id we tracked when opening the modal.
      await updateCronSchedule(editingJob.value.id, {
        function_id: editingJob.value.function_id,
        cron: form.value.cron,
        timezone: form.value.timezone,
        enabled: form.value.enabled,
      })
    } else {
      await createCronSchedule(form.value.function_name, {
        cron: form.value.cron,
        timezone: form.value.timezone,
        enabled: form.value.enabled,
      })
    }
    await loadJobs()
    closeModal()
  } catch (e) {
    confirmStore.notify({
      title: 'Failed to save schedule',
      message: e?.response?.data?.error?.message || e?.message || 'Unknown error',
      danger: true,
    })
  }
}

const editSchedule = (job) => {
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
  try {
    await updateCronSchedule(job.id, {
      function_id: job.function_id,
      enabled: !job.enabled,
    })
    await loadJobs()
  } catch (e) {
    confirmStore.notify({
      title: job.enabled ? 'Failed to pause schedule' : 'Failed to resume schedule',
      message: e?.response?.data?.error?.message || e?.message || 'Unknown error',
      danger: true,
    })
  }
}

const deleteSchedule = async (job) => {
  const ok = await confirmStore.ask({
    title: 'Delete schedule?',
    message: `Cron schedule for "${job.function_name}" will be removed.`,
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return

  try {
    await deleteCronSchedule(job.id, job.function_id)
    await loadJobs()
  } catch (e) {
    confirmStore.notify({
      title: 'Failed to delete schedule',
      message: e?.response?.data?.error?.message || e?.message || 'Unknown error',
      danger: true,
    })
  }
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

onMounted(() => {
  loadJobs()
  loadFunctions()
  updateCronFromSimple()
})
</script>
