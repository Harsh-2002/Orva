<template>
  <div class="min-h-dvh flex items-center justify-center bg-background px-6 py-16">
    <div class="max-w-lg w-full">
      <div class="flex items-center gap-3 mb-10">
        <OrvaLogo class="w-7 h-7" />
        <span class="font-semibold tracking-tight text-foreground text-lg">Orva</span>
      </div>

      <p class="text-xs uppercase tracking-[0.2em] text-foreground-muted mb-3">
        Error 404
      </p>
      <h1 class="text-3xl sm:text-4xl font-semibold text-foreground mb-4 leading-tight">
        Page not found.
      </h1>
      <p class="text-sm text-foreground-muted mb-4">
        This Orva instance has no page at:
      </p>

      <div class="bg-surface border border-border rounded-md px-3 py-2 mb-8 overflow-x-auto">
        <code class="text-xs font-mono text-foreground break-all">{{ tried }}</code>
      </div>

      <div class="flex flex-wrap gap-3">
        <button
          class="inline-flex items-center gap-2 px-4 py-2 rounded-md bg-foreground-strong text-background text-sm font-medium hover:bg-foreground transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
          @click="goHome"
        >
          <ArrowLeft class="w-4 h-4" />
          Go to Functions
        </button>
        <button
          class="inline-flex items-center gap-2 px-4 py-2 rounded-md border border-border text-sm text-foreground hover:bg-surface-hover transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
          @click="goDocs"
        >
          <BookOpen class="w-4 h-4" />
          Open docs
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft, BookOpen } from '@lucide/vue'
import OrvaLogo from '@/components/OrvaLogo.vue'

const route = useRoute()
const router = useRouter()

const tried = computed(() => route.fullPath || '/')

const goHome = () => router.push('/functions')
const goDocs = () => router.push('/docs')
</script>
