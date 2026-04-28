<template>
  <div class="h-full flex flex-col bg-background">
    <!-- Header -->
    <header class="sticky top-0 z-20 flex items-center justify-between px-6 py-5 border-b border-border bg-surface/95 backdrop-blur-sm">
      <div>
        <h1 class="text-2xl font-bold tracking-tight text-white mb-1">
          API Documentation
        </h1>
        <p class="text-sm text-foreground-muted">
          Official REST API reference for Orva Platform
        </p>
      </div>
    </header>

    <!-- Content -->
    <div class="flex-1 overflow-y-auto">
      <div
        v-if="loading"
        class="flex justify-center py-20"
      >
        <div class="animate-spin rounded-full h-10 w-10 border-2 border-primary border-t-transparent" />
      </div>
      
      <div
        v-else
        class="max-w-5xl mx-auto px-6 md:px-10 py-10"
      >
        <!-- Rich API Documentation -->
        <article class="docs-content space-y-8">
          <div v-html="content" />
        </article>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { marked } from 'marked'

const content = ref('')
const loading = ref(true)

// Configure marked for better rendering
marked.setOptions({
  highlight: function(code, lang) {
    return `<pre class="language-${lang}"><code>${code}</code></pre>`
  },
  breaks: true,
  gfm: true
})

onMounted(async () => {
  try {
    const response = await fetch('/API.md')
    if (!response.ok) throw new Error('Failed to fetch docs')
    const text = await response.text()
    content.value = await marked.parse(text)
  } catch (error) {
    console.error('Failed to load docs:', error)
    content.value = `
      <div class="bg-red-500/10 border border-red-500/20 rounded-lg p-6 text-center">
        <p class="text-red-400 font-medium">Failed to load documentation</p>
        <p class="text-sm text-red-300/70 mt-2">Please ensure API.md is available in the public directory</p>
      </div>
    `
  } finally {
    loading.value = false
  }
})
</script>

<style scoped>
.docs-content :deep(h1) {
  @apply text-3xl font-bold text-white mb-4 pb-3 border-b border-border;
}

.docs-content :deep(h2) {
  @apply text-2xl font-semibold text-white mt-12 mb-4 pb-2 border-b border-border/50;
}

.docs-content :deep(h3) {
  @apply text-xl font-semibold text-white mt-8 mb-3;
}

.docs-content :deep(h4) {
  @apply text-lg font-medium text-white mt-6 mb-2;
}

.docs-content :deep(p) {
  @apply text-foreground-muted leading-relaxed mb-4;
}

.docs-content :deep(a) {
  @apply text-violet-400 hover:text-violet-300 underline decoration-violet-400/30 hover:decoration-violet-300 transition-colors;
}

.docs-content :deep(code) {
  @apply bg-surface px-2 py-0.5 rounded text-sm font-mono text-pink-400 border border-border;
}

.docs-content :deep(pre) {
  @apply bg-[#0d0c15] border border-border rounded-lg p-4 overflow-x-auto mb-6 shadow-lg;
}

.docs-content :deep(pre code) {
  @apply bg-transparent border-0 p-0 text-foreground text-sm leading-relaxed;
}

.docs-content :deep(ul),
.docs-content :deep(ol) {
  @apply text-foreground-muted mb-4 ml-6 space-y-2;
}

.docs-content :deep(li) {
  @apply leading-relaxed;
}

.docs-content :deep(ul li) {
  @apply list-disc;
}

.docs-content :deep(ol li) {
  @apply list-decimal;
}

.docs-content :deep(blockquote) {
  @apply border-l-4 border-primary bg-primary/5 pl-4 py-2 my-4 italic text-foreground-muted;
}

.docs-content :deep(table) {
  @apply w-full border-collapse border border-border rounded-lg overflow-hidden mb-6;
}

.docs-content :deep(thead) {
  @apply bg-surface;
}

.docs-content :deep(th) {
  @apply text-left text-sm font-semibold text-white px-4 py-3 border-b border-border;
}

.docs-content :deep(td) {
  @apply text-sm text-foreground-muted px-4 py-3 border-b border-border/50;
}

.docs-content :deep(tr:last-child td) {
  @apply border-b-0;
}

.docs-content :deep(tr:hover) {
  @apply bg-surface/50;
}

.docs-content :deep(hr) {
  @apply border-border my-8;
}

.docs-content :deep(strong) {
  @apply text-white font-semibold;
}

.docs-content :deep(em) {
  @apply text-foreground-muted italic;
}

/* Custom badge/tag styles for HTTP methods */
.docs-content :deep(code:has-text("GET")),
.docs-content :deep(code:has-text("POST")),
.docs-content :deep(code:has-text("PUT")),
.docs-content :deep(code:has-text("DELETE")) {
  @apply font-bold;
}
</style>
