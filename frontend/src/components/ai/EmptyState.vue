<template>
  <!--
    EmptyState — the new-chat greeting shown above a vertically-centered composer
    until the first message is sent. The starter prompts are Orva-specific and
    FILL the composer on click (they don't send), so the operator can read, tweak,
    and then send. Greeting and prompts are both drawn at random on each mount, so
    a fresh chat shows a fresh set.

    There is deliberately no subhead. It used to read "Ask about this instance or
    operate it with natural language", which described the input box the operator
    was already looking at. The prompts below say what this can do far better
    than a sentence about it could.
  -->
  <div class="text-center">
    <h2 class="text-lg font-semibold tracking-tight text-foreground-strong">
      {{ greeting }}
    </h2>

    <!-- The chips wrap in a centred row and size to their prompt; line-clamp-2
         caps a long one at two lines so a single chip can never dominate the
         row. No icons, just the prompt. -->
    <div class="mx-auto mt-5 flex max-w-xl flex-wrap justify-center gap-2">
      <button
        v-for="(p, i) in suggestions"
        :key="i"
        type="button"
        class="touch-expand-sm rounded-full border border-border bg-surface px-3.5 py-2 text-left text-xs leading-snug text-foreground-muted transition-colors hover:border-foreground-muted hover:bg-surface-hover hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background"
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

// Greetings rotate so a new chat does not read like a saved screenshot. Kept in
// the product's register: dry, short, assumes the operator knows what they came
// for. No exclamation marks, no "Hi there".
const GREETINGS = [
  'What would you like to do?',
  'Where should we start?',
  'What needs attention?',
  'What are we looking at?',
  'What can I check for you?',
  'What do you need?',
  'Where do you want to dig in?',
]

// Prompts an operator would plausibly type on a real instance, grouped by the
// job they are doing. Each is kept to roughly two lines at the chip width so the
// row stays even. The bias is toward diagnosis and day-two work, because that is
// what somebody opens a control plane for; a couple of authoring prompts are
// here because a fresh instance has nothing to diagnose yet.
const POOL = [
  // Something is wrong, find it.
  'Any errors in the last 24 hours?',
  'Why did my last deployment fail?',
  'Show me the slowest functions right now.',
  'Which executions timed out today?',
  'Find the trace for the last failed request.',
  'Are any background jobs stuck or failing?',
  'Check for failed webhook deliveries.',
  'Which function is closest to its memory limit?',
  'Show the error rate over the last hour.',
  'What changed on this instance recently?',

  // Day-two operations.
  'List my cron schedules and when they next run.',
  'Which functions have egress enabled?',
  'What is my current egress policy blocking?',
  'Show API keys and what each one can do.',
  'Which functions are public with no auth?',
  'Set a rate limit on a function.',
  'Increase the memory limit for a function.',
  'Show warm pool usage per function.',
  'What is using the most storage?',

  // Understanding the instance.
  'How many functions do I have, and in what runtimes?',
  'What is my system health right now?',
  'Explain what happens when a function times out.',
  'Show me how to invoke a function from the CLI.',

  // Authoring, for a fresh instance with nothing to diagnose.
  'Write a Python function that returns the current UTC time.',
  'Write a Node function that verifies a webhook signature.',
  'Create a function that runs every hour and logs a heartbeat.',
  'Walk me through deploying my first function.',
]

// Fisher-Yates shuffle, then take n. Runs once per mount; EmptyState is
// re-created whenever a new chat opens, so every new chat gets a fresh rotation.
function sample(arr, n) {
  const copy = [...arr]
  for (let i = copy.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1))
    ;[copy[i], copy[j]] = [copy[j], copy[i]]
  }
  return copy.slice(0, n)
}

const greeting = ref(sample(GREETINGS, 1)[0])
const suggestions = ref(sample(POOL, 3))
</script>
