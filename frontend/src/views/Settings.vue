<template>
  <div class="space-y-6">
    <div>
      <h1 class="text-xl font-semibold text-foreground tracking-tight">
        Settings
      </h1>
      <p class="text-xs text-foreground-muted mt-1">
        Operator-level controls for the running Orva instance.
      </p>
    </div>

    <!-- Storage card. Shows orva.db / functions tree / WAL sizes plus
         a "Compact" affordance that runs SQLite VACUUM via the
         admin-gated POST /api/v1/system/vacuum endpoint. -->
    <div class="bg-background border border-border rounded-lg p-5 space-y-4">
      <div class="flex items-start justify-between gap-4">
        <div>
          <div class="text-sm font-semibold text-white flex items-center gap-2">
            <HardDrive class="w-4 h-4 text-foreground-muted" />
            Storage
          </div>
          <p class="text-xs text-foreground-muted mt-1 max-w-prose">
            On-disk breakdown of the data directory.
            <code class="text-[11px]">VACUUM</code> rewrites
            <code class="text-[11px]">orva.db</code>, drops the freelist, and
            shrinks the file. The operation holds an exclusive lock; writes
            block briefly while it runs.
          </p>
        </div>
        <Button
          variant="secondary"
          :loading="vacuuming"
          :disabled="!storage || vacuuming"
          @click="askVacuum"
        >
          <Wand2 class="w-4 h-4" />
          Compact database
        </Button>
      </div>

      <!-- Skeleton while we wait for the first response. -->
      <div
        v-if="!storage && !storageError"
        class="text-xs text-foreground-muted italic"
      >
        Loading storage stats…
      </div>

      <div
        v-if="storageError"
        class="rounded-md border border-red-700/40 bg-red-950/30 p-3 text-xs text-red-200"
      >
        <div class="font-semibold text-red-100 mb-1">Failed to load storage stats</div>
        <div class="font-mono break-all">{{ storageError }}</div>
      </div>

      <div v-if="storage" class="space-y-3">
        <!-- Stacked bar — proportions of total. -->
        <div class="h-2 w-full rounded-full overflow-hidden bg-border/60 flex">
          <div
            v-if="dbPct > 0"
            class="bg-sky-500 h-full"
            :style="{ width: dbPct + '%' }"
            :title="`orva.db: ${formatBytes(storage.db_bytes)}`"
          />
          <div
            v-if="walPct > 0"
            class="bg-amber-500 h-full"
            :style="{ width: walPct + '%' }"
            :title="`WAL: ${formatBytes(storage.wal_bytes)}`"
          />
          <div
            v-if="fnPct > 0"
            class="bg-emerald-500 h-full"
            :style="{ width: fnPct + '%' }"
            :title="`functions/: ${formatBytes(storage.functions_bytes)}`"
          />
        </div>

        <!-- Numeric breakdown. -->
        <div class="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-1.5 text-xs">
          <div class="flex items-center justify-between">
            <span class="flex items-center gap-2 text-foreground-muted">
              <span class="w-2 h-2 rounded-sm bg-sky-500"></span>
              orva.db
            </span>
            <span class="font-mono text-white">{{ formatBytes(storage.db_bytes) }}</span>
          </div>
          <div class="flex items-center justify-between">
            <span class="flex items-center gap-2 text-foreground-muted">
              <span class="w-2 h-2 rounded-sm bg-emerald-500"></span>
              functions/
            </span>
            <span class="font-mono text-white">{{ formatBytes(storage.functions_bytes) }}</span>
          </div>
          <div v-if="storage.wal_bytes > 0" class="flex items-center justify-between">
            <span class="flex items-center gap-2 text-foreground-muted">
              <span class="w-2 h-2 rounded-sm bg-amber-500"></span>
              orva.db-wal
            </span>
            <span class="font-mono text-white">{{ formatBytes(storage.wal_bytes) }}</span>
          </div>
          <div class="flex items-center justify-between">
            <span class="text-foreground-muted">total</span>
            <span class="font-mono text-white font-semibold">{{ formatBytes(storage.total_bytes) }}</span>
          </div>
        </div>

        <!-- VACUUM hint — only shown when there's something to reclaim. -->
        <div
          v-if="reclaimableBytes > 0"
          class="text-[11px] text-foreground-muted pt-1 border-t border-border"
        >
          {{ formatBytes(reclaimableBytes) }} reclaimable
          ({{ storage.db_free_pages }} free SQLite pages)
        </div>
      </div>

      <!-- Last-vacuum result. Sticks until next vacuum or page reload. -->
      <div
        v-if="lastVacuum"
        class="rounded-md border border-emerald-700/40 bg-emerald-950/30 p-3 text-xs text-emerald-200"
      >
        Compacted in {{ lastVacuum.duration_ms }} ms — freed
        <span class="font-mono">{{ formatBytes(lastVacuum.freed_bytes) }}</span>
        ({{ formatBytes(lastVacuum.before_bytes) }} →
        {{ formatBytes(lastVacuum.after_bytes) }}).
      </div>
      <div
        v-if="vacuumError"
        class="rounded-md border border-red-700/40 bg-red-950/30 p-3 text-xs text-red-200"
      >
        <div class="font-semibold text-red-100 mb-1">Compact failed</div>
        <div class="font-mono break-all">{{ vacuumError }}</div>
      </div>
    </div>

    <!-- Backup / Restore card. -->
    <div class="bg-background border border-border rounded-lg p-5 space-y-4">
      <div class="flex items-start justify-between gap-4">
        <div>
          <div class="text-sm font-semibold text-white flex items-center gap-2">
            <DatabaseBackup class="w-4 h-4 text-foreground-muted" />
            Backup &amp; Restore
          </div>
          <p class="text-xs text-foreground-muted mt-1 max-w-prose">
            Download a single tarball with the full SQLite database and every deployed
            function version. Restore on a fresh machine to migrate or recover from disk
            loss. Backups are produced with <code class="text-[11px]">VACUUM&nbsp;INTO</code>
            so they are point-in-time consistent even under load.
          </p>
        </div>
      </div>

      <div class="flex flex-col sm:flex-row gap-3 pt-2 border-t border-border">
        <Button
          variant="primary"
          @click="downloadBackup"
        >
          <Download class="w-4 h-4" />
          Download backup
        </Button>
        <Button
          variant="secondary"
          :loading="restoring"
          @click="pickRestoreFile"
        >
          <Upload class="w-4 h-4" />
          Restore from backup
        </Button>
        <input
          ref="fileInput"
          type="file"
          accept=".tar.gz,.tgz,application/gzip"
          class="hidden"
          @change="onFileSelected"
        >
      </div>

      <!-- Restore status panel — surfaces backend errors verbatim because
           the operator needs to know exactly what broke. -->
      <div
        v-if="restoreError"
        class="rounded-md border border-red-700/40 bg-red-950/30 p-3 text-xs text-red-200"
      >
        <div class="font-semibold text-red-100 mb-1">Restore failed</div>
        <div class="font-mono break-all">{{ restoreError }}</div>
      </div>
      <div
        v-if="restoreOk"
        class="rounded-md border border-emerald-700/40 bg-emerald-950/30 p-3 text-xs text-emerald-200"
      >
        Restore complete. Reload the page to pick up the new data.
        <button
          class="underline ml-1"
          @click="reload"
        >
          Reload now
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { Download, Upload, DatabaseBackup, HardDrive, Wand2 } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import { useConfirmStore } from '@/stores/confirm'
import { uploadRestore, getStorage, runVacuum } from '@/api/endpoints'

const confirmStore = useConfirmStore()

const fileInput = ref(null)
const restoring = ref(false)
const restoreError = ref('')
const restoreOk = ref(false)

// Storage card state.
const storage = ref(null)
const storageError = ref('')
const vacuuming = ref(false)
const vacuumError = ref('')
const lastVacuum = ref(null)

// Bar segments — clamp so nothing renders as 0px-but-visible.
const dbPct = computed(() =>
  storage.value && storage.value.total_bytes > 0
    ? Math.max(0.5, (storage.value.db_bytes / storage.value.total_bytes) * 100)
    : 0,
)
const walPct = computed(() =>
  storage.value && storage.value.total_bytes > 0 && storage.value.wal_bytes > 0
    ? Math.max(0.5, (storage.value.wal_bytes / storage.value.total_bytes) * 100)
    : 0,
)
const fnPct = computed(() =>
  storage.value && storage.value.total_bytes > 0
    ? Math.max(0.5, (storage.value.functions_bytes / storage.value.total_bytes) * 100)
    : 0,
)

// Upper bound on what VACUUM could reclaim — every page on the
// freelist is dead weight, so freelist_count × page_size is the
// optimistic estimate. The actual freed bytes after VACUUM is
// usually slightly higher (page-level fragmentation gets repacked).
const reclaimableBytes = computed(() => {
  if (!storage.value) return 0
  return (storage.value.db_free_pages || 0) * (storage.value.db_page_size || 0)
})

const fetchStorage = async () => {
  try {
    storageError.value = ''
    const res = await getStorage()
    storage.value = res.data
  } catch (err) {
    storageError.value = err?.response?.data?.error?.message || err?.message || 'unknown error'
  }
}

const askVacuum = async () => {
  const ok = await confirmStore.ask({
    title: 'Compact database?',
    message:
      'VACUUM rewrites orva.db to drop the freelist and shrink the file. ' +
      'It holds an exclusive lock for the duration; every other writer (deploys, ' +
      'invocations recording executions, KV puts, job enqueues) blocks until ' +
      'it returns. Typical runtime is sub-second, but a heavily-loaded instance ' +
      'can stall for several seconds.',
    confirmLabel: 'Compact',
    danger: false,
  })
  if (!ok) return

  vacuuming.value = true
  vacuumError.value = ''
  lastVacuum.value = null
  try {
    const res = await runVacuum()
    lastVacuum.value = res.data
    // Refresh the storage card so the operator sees the new sizes
    // immediately instead of having to reload the page.
    await fetchStorage()
  } catch (err) {
    vacuumError.value = err?.response?.data?.error?.message || err?.message || 'vacuum failed'
  } finally {
    vacuuming.value = false
  }
}

const formatBytes = (n) => {
  if (n == null || isNaN(n)) return '—'
  const k = 1024
  if (n < k) return `${n} B`
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n
  let i = -1
  while (v >= k && i < units.length - 1) {
    v /= k
    i++
  }
  return `${v.toFixed(2)} ${units[i]}`
}

onMounted(fetchStorage)

// downloadBackup just hands the URL to the browser. The session cookie
// is sent automatically (same-origin), the server replies with
// Content-Disposition: attachment; filename=…, and the browser writes
// the file to the user's downloads dir without our UI having to buffer
// the whole tarball in memory.
const downloadBackup = () => {
  // Adding a cache-buster + same-tab navigation triggers a download
  // for application/gzip with a Content-Disposition header without
  // navigating away from this view. We could also use a hidden <a>;
  // window.location.assign keeps the implementation tiny.
  window.location.assign('/api/v1/backup?ts=' + Date.now())
}

const pickRestoreFile = () => {
  restoreError.value = ''
  restoreOk.value = false
  fileInput.value?.click()
}

const onFileSelected = async (e) => {
  const file = e.target.files?.[0]
  // Reset the input so picking the same file twice in a row still
  // fires `change`.
  e.target.value = ''
  if (!file) return

  const ok = await confirmStore.ask({
    title: 'Restore from backup?',
    message:
      `This will replace the live database and function code with the contents of "${file.name}". ` +
      'The current orva.db is moved aside as orva.db.before-restore-<timestamp> in case rollback is needed. ' +
      'You will need to reload after restore completes.',
    confirmLabel: 'Restore',
    danger: true,
  })
  if (!ok) return

  restoring.value = true
  restoreError.value = ''
  restoreOk.value = false
  try {
    await uploadRestore(file)
    restoreOk.value = true
  } catch (err) {
    restoreError.value = err?.response?.data?.error?.message || err?.message || 'Restore failed'
  } finally {
    restoring.value = false
  }
}

const reload = () => {
  window.location.reload()
}
</script>
