<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <div>
        <h1 class="text-xl font-semibold text-foreground tracking-tight">
          Scheduled Jobs
        </h1>
        <p class="text-sm text-foreground-muted mt-1">
          Automate function execution with cron schedules
        </p>
      </div>
      <Button @click="showCreateModal = true">
        <PlusCircle class="w-4 h-4" />
        New Schedule
      </Button>
    </div>

    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <table class="w-full text-sm text-left">
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
            :key="job.function_name"
            class="hover:bg-surface-hover transition-colors"
          >
            <td class="px-6 py-4 font-medium text-foreground">
              {{ job.function_name }}
            </td>
            <td class="px-6 py-4">
              <div class="flex flex-col gap-1">
                <span class="text-foreground font-mono text-xs">{{ job.cron_expression }}</span>
                <span class="text-foreground-muted text-[10px]">{{ humanizeCron(job.cron_expression) }}</span>
              </div>
            </td>
            <td class="px-6 py-4 hidden sm:table-cell">
              <span
                class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium"
                :class="job.enabled ? 'bg-success/10 text-success border border-success/30' : 'bg-foreground-muted/10 text-foreground-muted border border-foreground-muted/30'"
              >
                {{ job.enabled ? 'Active' : 'Paused' }}
              </span>
            </td>
            <td class="px-6 py-4 text-foreground-muted text-xs hidden md:table-cell">
              {{ job.last_run ? formatDate(job.last_run) : '—' }}
            </td>
            <td class="px-6 py-4 text-foreground-muted text-xs hidden lg:table-cell">
              {{ job.next_run ? formatDate(job.next_run) : '—' }}
            </td>
            <td class="px-6 py-4 text-right flex items-center justify-end gap-2">
              <button 
                class="text-foreground-muted hover:text-foreground transition-colors p-1"
                :title="job.enabled ? 'Pause' : 'Resume'"
                @click="toggleSchedule(job)"
              >
                <component
                  :is="job.enabled ? Pause : Play"
                  class="w-4 h-4"
                />
              </button>
              <button 
                class="text-foreground-muted hover:text-foreground transition-colors p-1"
                title="Edit"
                @click="editSchedule(job)"
              >
                <Edit class="w-4 h-4" />
              </button>
              <button 
                class="text-foreground-muted hover:text-error transition-colors p-1"
                title="Delete"
                @click="deleteSchedule(job.function_name)"
              >
                <Trash2 class="w-4 h-4" />
              </button>
            </td>
          </tr>
          <tr v-if="jobs.length === 0">
            <td
              colspan="6"
              class="px-6 py-12 text-center"
            >
              <Clock class="w-12 h-12 text-foreground-muted mx-auto mb-3 opacity-30" />
              <p class="text-foreground-muted">
                No scheduled jobs yet.
              </p>
              <p class="text-foreground-muted text-xs mt-1">
                Create your first schedule to automate function execution.
              </p>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Create/Edit Modal -->
    <div
      v-if="showCreateModal"
      class="fixed inset-0 bg-black/80 backdrop-blur-sm flex items-center justify-center z-50 p-4"
    >
      <div class="bg-surface border border-border rounded-lg w-full max-w-2xl shadow-2xl shadow-black/50 max-h-[90vh] overflow-y-auto">
        <div class="border-b border-border px-6 py-4 flex items-center justify-between sticky top-0 bg-surface">
          <h2 class="text-lg font-semibold text-foreground">
            {{ editingJob ? 'Edit Schedule' : 'Create Schedule' }}
          </h2>
          <button
            class="text-foreground-muted hover:text-foreground"
            @click="closeModal"
          >
            <X class="w-5 h-5" />
          </button>
        </div>

        <div class="p-6 space-y-5">
          <!-- Function Selection -->
          <div>
            <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-2">Function</label>
            <select 
              v-model="form.function_name"
              class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary"
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
                {{ fn.name }} ({{ fn.runtime }})
              </option>
            </select>
          </div>

          <!-- Schedule Type Tabs -->
          <div>
            <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-2">Schedule Type</label>
            <div class="flex gap-2 bg-background rounded-lg p-1 border border-border">
              <button 
                v-for="type in ['simple', 'advanced']" 
                :key="type"
                class="flex-1 py-2 px-3 text-sm font-medium rounded transition-colors"
                :class="scheduleType === type ? 'bg-primary text-primary-foreground shadow-sm' : 'text-foreground-muted hover:text-foreground'"
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
            <div class="grid grid-cols-3 gap-3">
              <div>
                <label class="text-xs font-medium text-foreground-muted block mb-1.5">Frequency</label>
                <select
                  v-model="simpleSchedule.frequency"
                  class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-primary"
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
                <label class="text-xs font-medium text-foreground-muted block mb-1.5">At Minute</label>
                <input
                  v-model.number="simpleSchedule.minute"
                  type="number"
                  min="0"
                  max="59"
                  class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-primary"
                  @input="updateCronFromSimple"
                >
              </div>

              <div v-if="['day', 'week', 'month'].includes(simpleSchedule.frequency)">
                <label class="text-xs font-medium text-foreground-muted block mb-1.5">At Hour</label>
                <input
                  v-model.number="simpleSchedule.hour"
                  type="number"
                  min="0"
                  max="23"
                  class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-primary"
                  @input="updateCronFromSimple"
                >
              </div>

              <div v-if="simpleSchedule.frequency === 'week'">
                <label class="text-xs font-medium text-foreground-muted block mb-1.5">Day of Week</label>
                <select
                  v-model="simpleSchedule.dayOfWeek"
                  class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-primary"
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
                <label class="text-xs font-medium text-foreground-muted block mb-1.5">Day of Month</label>
                <input
                  v-model.number="simpleSchedule.dayOfMonth"
                  type="number"
                  min="1"
                  max="31"
                  class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-primary"
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
              <label class="text-xs font-medium text-foreground-muted block mb-1.5">Cron Expression</label>
              <input 
                v-model="form.cron"
                placeholder="* * * * *"
                class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm font-mono text-foreground focus:outline-none focus:ring-2 focus:ring-primary"
              >
              <p class="text-xs text-foreground-muted mt-1.5">
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

          <!-- Enabled Toggle -->
          <div class="flex items-center gap-3">
            <input 
              id="enabled-toggle"
              v-model="form.enabled" 
              type="checkbox"
              class="w-4 h-4 text-primary bg-background border-border rounded focus:ring-primary focus:ring-2"
            >
            <label
              for="enabled-toggle"
              class="text-sm font-medium text-foreground cursor-pointer"
            >
              Enable schedule immediately
            </label>
          </div>
        </div>

        <div class="border-t border-border px-6 py-4 flex items-center justify-end gap-3 bg-surface sticky bottom-0">
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
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { PlusCircle, Trash2, Clock, X, Edit, Play, Pause } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import { listCronSchedules, createCronSchedule, deleteCronSchedule, listFunctions } from '@/api/endpoints'
import { useConfirmStore } from '@/stores/confirm'

const confirmStore = useConfirmStore()

const jobs = ref([])
const functions = ref([])
const showCreateModal = ref(false)
const editingJob = ref(null)
const scheduleType = ref('simple')

const form = ref({
  function_name: '',
  cron: '0 0 * * *',
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
  } catch (e) {
    console.error('Failed to load cron jobs', e)
  }
}

const loadFunctions = async () => {
  try {
    const res = await listFunctions()
    functions.value = res.data.functions || []
  } catch (e) {
    console.error('Failed to load functions', e)
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

const humanizeCron = (cron) => {
  if (!cron) return 'Invalid expression'
  
  const parts = cron.trim().split(/\s+/)
  if (parts.length !== 5) return 'Invalid format (use 5 fields)'
  
  const [min, hour, day, month, dow] = parts
  
  if (cron === '* * * * *') return 'Every minute'
  if (min !== '*' && hour === '*' && day === '*' && month === '*' && dow === '*') {
    return `Every hour at minute ${min}`
  }
  if (min !== '*' && hour !== '*' && day === '*' && month === '*' && dow === '*') {
    return `Every day at ${hour.padStart(2, '0')}:${min.padStart(2, '0')}`
  }
  if (min !== '*' && hour !== '*' && day === '*' && month === '*' && dow !== '*') {
    const days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
    return `Every ${days[dow]} at ${hour.padStart(2, '0')}:${min.padStart(2, '0')}`
  }
  if (min !== '*' && hour !== '*' && day !== '*' && month === '*' && dow === '*') {
    return `On day ${day} of every month at ${hour.padStart(2, '0')}:${min.padStart(2, '0')}`
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
    await createCronSchedule(form.value.function_name, {
      cron: form.value.cron,
      enabled: form.value.enabled
    })
    await loadJobs()
    closeModal()
  } catch (e) {
    console.error('Failed to create schedule', e)
    confirmStore.notify({ title: 'Failed to create schedule', danger: true })
  }
}

const editSchedule = (job) => {
  editingJob.value = job
  form.value = {
    function_name: job.function_name,
    cron: job.cron_expression,
    enabled: job.enabled
  }
  showCreateModal.value = true
}

const toggleSchedule = async (job) => {
  try {
    await createCronSchedule(job.function_name, {
      cron: job.cron_expression,
      enabled: !job.enabled
    })
    await loadJobs()
  } catch (e) {
    console.error('Failed to toggle schedule', e)
  }
}

const deleteSchedule = async (functionName) => {
  const ok = await confirmStore.ask({
    title: 'Delete schedule?',
    message: `Cron schedule for "${functionName}" will be removed.`,
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return

  try {
    await deleteCronSchedule(functionName)
    await loadJobs()
  } catch (e) {
    console.error('Failed to delete schedule', e)
  }
}

const closeModal = () => {
  showCreateModal.value = false
  editingJob.value = null
  form.value = {
    function_name: '',
    cron: '0 0 * * *',
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
