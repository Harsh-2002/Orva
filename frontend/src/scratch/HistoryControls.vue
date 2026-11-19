<template>
  <main class="max-w-5xl mx-auto p-4 sm:p-8 space-y-5">
    <p class="text-sm text-warning-fg">
      Scratch history qualification. Not production activation.
    </p>
    <nav
      class="flex flex-wrap gap-3"
      aria-label="History views"
    >
      <Button
        variant="secondary"
        @click="view = 'jobs'"
      >
        Jobs view
      </Button>
      <Button
        variant="secondary"
        @click="view = 'cron'"
      >
        Cron view
      </Button>
    </nav>
    <KeepAlive>
      <component
        :is="view === 'jobs' ? Jobs : CronJobs"
        :history-enabled="true"
      />
    </KeepAlive>
  </main>
</template>
<script setup>
import { ref } from 'vue'
import Jobs from '@/views/Jobs.vue'
import CronJobs from '@/views/CronJobs.vue'
import Button from '@/components/common/Button.vue'
const view = ref('jobs')
</script>
