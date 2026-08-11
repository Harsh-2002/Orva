<template>
  <!--
    EmptyState — the new-chat greeting shown above a vertically-centered composer
    until the first message is sent. The starter prompts are Orva-specific and
    FILL the composer on click (they don't send), so the operator can read, tweak,
    and then send. Four are drawn at random from POOL on each mount, so a fresh
    chat shows a fresh, rotating set.
  -->
  <div class="text-center">
    <h2 class="text-lg font-semibold tracking-tight text-white">
      What would you like to do?
    </h2>
    <p class="mx-auto mt-1.5 max-w-md text-sm leading-relaxed text-foreground-muted">
      Ask about this instance or operate it with natural language.
    </p>

    <!-- Equal-height cards: a fixed min-height floor plus line-clamp-2 keeps every
         card the same size whether its prompt is one line or two, so a longer
         prompt never breaks the grid rhythm. No icons, just the prompt. -->
    <div class="mx-auto mt-5 flex max-w-xl flex-wrap justify-center gap-2">
      <button
        v-for="(p, i) in suggestions"
        :key="i"
        type="button"
        class="rounded-md px-3 py-2 text-left text-xs leading-snug text-foreground-muted transition-colors hover:bg-surface-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background"
        @click="$emit('pick', p)"
      >
        <span class="line-clamp-2">{{ p }}</span>
      </button>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'

defineEmits(['pick'])

// A pool of short, instance-aware prompts. Each is kept to about two lines at the
// card width so the grid stays uniform. Four are sampled per mount (see below).
const POOL = [
  'How many functions do I have?',
  'Which function ran most recently?',
  'Any errors in the last 24 hours?',
  'Show my most recent executions.',
  'List my deployed functions.',
  'Summarize today’s invocation errors.',
  'Show failed deployments and why.',
  'What’s my system health right now?',
  'Show storage usage for my instance.',
  'List my cron schedules.',
  'Are any background jobs failing?',
  'Check for failed webhook deliveries.',
  'Which runtimes are available?',
  'Show my slowest functions by duration.',
  'Which functions have egress enabled?',
  'List my secrets by name only.',
  'Write a Python function that returns the current UTC time.',
  'Write a Node function that echoes the request body.',
  'Create an hourly cron schedule for a function.',
  'Walk me through deploying a new function.',
]

// Fisher-Yates shuffle, then take four. Runs once per mount; since EmptyState is
// re-created whenever a new chat opens, every new chat gets a fresh rotation.
function sample(arr, n) {
  const copy = [...arr]
  for (let i = copy.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1))
    ;[copy[i], copy[j]] = [copy[j], copy[i]]
  }
  return copy.slice(0, n)
}

const suggestions = ref(sample(POOL, 3))
</script>
