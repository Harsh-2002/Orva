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
import { ref } from 'vue'
import { Download, Upload, DatabaseBackup } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import { useConfirmStore } from '@/stores/confirm'
import { uploadRestore } from '@/api/endpoints'

const confirmStore = useConfirmStore()

const fileInput = ref(null)
const restoring = ref(false)
const restoreError = ref('')
const restoreOk = ref(false)

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
