<template>
  <!--
    ChatHeader — a thin chat-column header: a mobile hamburger that opens the
    conversation rail, the assistant title, and an export action. Model and
    reasoning controls live in the composer; AI configuration lives on the
    Settings page. So the header stays calm.

    The page heading is visually hidden (as in Editor.vue): the chat shell has
    no room for the standard text-xl header, and rendering the conversation
    title as the <h1> made it the one h1 in the app smaller than the <h2> the
    empty state draws right below it — the type scale reporting the opposite
    hierarchy from the markup.
  -->
  <header class="flex h-16 shrink-0 items-center justify-between gap-3 border-b border-border px-4">
    <h1 class="sr-only">
      AI assistant
    </h1>
    <div class="flex min-w-0 items-center gap-2">
      <button
        class="touch-expand-iconbtn -ml-1 rounded-md p-2 text-foreground-muted transition-colors hover:bg-surface-hover hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background md:hidden"
        aria-label="Conversations"
        @click="$emit('toggle-rail')"
      >
        <PanelLeft class="h-4 w-4" />
      </button>
      <MessageSquare class="hidden h-4 w-4 shrink-0 text-foreground-muted md:block" />
      <p class="truncate text-sm font-semibold tracking-tight text-white">
        {{ title }}
      </p>
    </div>
    <div class="flex items-center gap-0.5">
      <button
        v-if="canExport"
        class="touch-expand-iconbtn -mr-1 rounded-md p-2 text-foreground-muted transition-colors hover:bg-surface-hover hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background"
        aria-label="Export conversation as Markdown"
        title="Export conversation"
        @click="$emit('export')"
      >
        <Download class="h-4 w-4" />
      </button>
    </div>
  </header>
</template>

<script setup>
import { MessageSquare, PanelLeft, Download } from '@lucide/vue'

defineProps({
  title: { type: String, default: 'Assistant' },
  canExport: { type: Boolean, default: false },
})
defineEmits(['toggle-rail', 'export'])
</script>
