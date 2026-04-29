<template>
  <div class="space-y-4">
    <!-- Page header — matches Functions / Schedules style. -->
    <div class="flex items-center justify-between gap-4 flex-wrap">
      <h1 class="text-xl font-semibold text-white tracking-tight">
        Jobs
      </h1>
      <div class="flex items-center gap-2 text-xs text-foreground-muted">
        Background queue · {{ totalCount }} jobs
      </div>
    </div>

    <!-- Status filter strip. -->
    <div class="flex items-center gap-2 flex-wrap">
      <button
        v-for="opt in statusOptions"
        :key="opt.value"
        class="px-2.5 py-1 rounded-md border text-xs transition-colors"
        :class="statusFilter === opt.value
          ? 'bg-primary text-primary-foreground border-primary'
          : 'bg-surface text-foreground-muted border-border hover:text-white hover:border-foreground-muted'"
        @click="statusFilter = opt.value"
      >
        {{ opt.label }}
        <span
          v-if="counts[opt.value] !== undefined"
          class="ml-1 opacity-70"
        >{{ counts[opt.value] }}</span>
      </button>
      <div class="flex-1" />
      <button
        class="px-2.5 py-1 rounded-md border border-border bg-surface text-foreground-muted hover:text-white text-xs flex items-center gap-1.5"
        @click="loadJobs"
      >
        <RefreshCcw class="w-3 h-3" />
        Refresh
      </button>
    </div>

    <!-- Table. -->
    <div class="bg-background border border-border rounded-lg overflow-x-auto">
      <table class="w-full text-sm text-left">
        <thead class="text-xs text-foreground-muted uppercase bg-surface border-b border-border">
          <tr>
            <th class="px-4 py-3 font-medium">
              Function
            </th>
            <th class="px-4 py-3 font-medium hidden sm:table-cell">
              Status
            </th>
            <th class="px-4 py-3 font-medium hidden md:table-cell">
              Attempts
            </th>
            <th class="px-4 py-3 font-medium hidden lg:table-cell">
              Scheduled
            </th>
            <th class="px-4 py-3 font-medium hidden xl:table-cell">
              Finished
            </th>
            <th class="px-4 py-3 font-medium text-right">
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
            <td class="px-4 py-3 font-medium text-white">
              <div class="flex flex-col">
                <span>{{ job.function_name || job.function_id }}</span>
                <span class="text-[10px] text-foreground-muted font-mono">{{ job.id }}</span>
              </div>
            </td>
            <td class="px-4 py-3 hidden sm:table-cell">
              <span
                class="inline-flex items-center px-2 py-0.5 rounded text-[11px] font-medium border"
                :class="statusPill(job.status)"
              >
                {{ job.status }}
              </span>
              <p
                v-if="job.last_error"
                class="text-[11px] text-error mt-1 truncate max-w-xs"
                :title="job.last_error"
              >
                {{ job.last_error }}
              </p>
            </td>
            <td class="px-4 py-3 text-foreground-muted text-xs hidden md:table-cell">
              {{ job.attempts }} / {{ job.max_attempts }}
            </td>
            <td class="px-4 py-3 text-foreground-muted text-xs hidden lg:table-cell">
              {{ formatDate(job.scheduled_at) }}
            </td>
            <td class="px-4 py-3 text-foreground-muted text-xs hidden xl:table-cell">
              {{ job.finished_at ? formatDate(job.finished_at) : '—' }}
            </td>
            <td class="px-4 py-3 text-right">
              <button
                v-if="job.status === 'failed'"
                class="text-foreground-muted hover:text-foreground transition-colors p-1 mr-1"
                title="Retry"
                @click="retry(job)"
              >
                <RotateCcw class="w-4 h-4" />
              </button>
              <button
                class="text-foreground-muted hover:text-error transition-colors p-1"
                title="Delete"
                @click="remove(job)"
              >
                <Trash2 class="w-4 h-4" />
              </button>
            </td>
          </tr>
          <tr v-if="filteredJobs.length === 0">
            <td
              colspan="6"
              class="px-4 py-12 text-center"
            >
              <Inbox class="w-10 h-10 text-foreground-muted mx-auto mb-3 opacity-30" />
              <p class="text-foreground-muted text-sm">
                {{ statusFilter === 'all' ? 'No jobs yet.' : `No ${statusFilter} jobs.` }}
              </p>
              <p class="text-foreground-muted text-xs mt-1">
                Enqueue from inside a function with
                <code class="font-mono text-[11px] px-1.5 py-0.5 rounded bg-surface border border-border">orva.jobs.enqueue(name, payload)</code>.
              </p>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { Trash2, RotateCcw, RefreshCcw, Inbox } from 'lucide-vue-next'
import { listJobs, retryJob, deleteJob } from '@/api/endpoints'
import { useConfirmStore } from '@/stores/confirm'

const confirmStore = useConfirmStore()

const jobs = ref([])
const statusFilter = ref('all')
let pollTimer = null

const statusOptions = [
  { value: 'all',       label: 'All' },
  { value: 'pending',   label: 'Pending' },
  { value: 'running',   label: 'Running' },
  { value: 'succeeded', label: 'Succeeded' },
  { value: 'failed',    label: 'Failed' },
]

const totalCount = computed(() => jobs.value.length)
const filteredJobs = computed(() =>
  statusFilter.value === 'all'
    ? jobs.value
    : jobs.value.filter((j) => j.status === statusFilter.value)
)
const counts = computed(() => {
  const c = { all: jobs.value.length }
  for (const j of jobs.value) c[j.status] = (c[j.status] || 0) + 1
  return c
})

const statusPill = (s) => {
  switch (s) {
    case 'pending':
      return 'bg-amber-500/10 text-amber-300 border-amber-500/30'
    case 'running':
      return 'bg-sky-500/15 text-sky-300 border-sky-500/30 animate-pulse'
    case 'succeeded':
      return 'bg-success/10 text-success border-success/30'
    case 'failed':
      return 'bg-error/10 text-error border-error/30'
    default:
      return 'bg-surface text-foreground-muted border-border'
  }
}

const formatDate = (s) => {
  if (!s) return '—'
  return new Date(s).toLocaleString('en-US', {
    month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit',
  })
}

const loadJobs = async () => {
  try {
    const res = await listJobs({ limit: 200 })
    jobs.value = res.data.jobs || []
  } catch (e) {
    console.error('Failed to load jobs', e)
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

onMounted(() => {
  loadJobs()
  // Auto-refresh every 5s while the page is open so running jobs
  // visibly transition. Cleared on unmount.
  pollTimer = setInterval(loadJobs, 5000)
})

onBeforeUnmount(() => {
  if (pollTimer) clearInterval(pollTimer)
})
</script>
