<template>
  <!--
    ThinkingBlock — renders a model's reasoning stream. While the tokens arrive
    (`part.streaming`) it auto-expands, shows a live elapsed timer, and shimmers
    the "Thinking…" label so the operator knows the model is working through a
    long pre-answer gap. Once the answer starts (streaming flips false) it
    collapses to a quiet "Thought for Ns" summary that re-expands on click. The
    reasoning itself is muted monospace, visually distinct from the sans answer.
  -->
  <div class="rounded-lg border border-border/70 bg-surface/40">
    <button
      type="button"
      class="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary"
      :aria-expanded="expanded"
      @click="toggle"
    >
      <Brain
        class="w-3.5 h-3.5 shrink-0"
        :class="streaming ? 'text-primary' : 'text-foreground-muted'"
      />
      <span
        class="text-xs font-medium"
        :class="streaming ? 'thinking-shimmer text-foreground' : 'text-foreground-muted'"
      >
        {{ label }}
      </span>
      <span class="flex-1" />
      <ChevronDown
        class="w-3.5 h-3.5 shrink-0 text-foreground-muted transition-transform duration-150"
        :class="{ 'rotate-180': expanded }"
      />
    </button>
    <div
      v-if="expanded"
      class="px-3 pb-2.5"
    >
      <div class="border-t border-border/70 pt-2 text-xs leading-relaxed text-foreground-muted font-mono whitespace-pre-wrap break-words">
        {{ part.text }}
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { Brain, ChevronDown } from '@lucide/vue'

const props = defineProps({
  part: { type: Object, required: true },
})

const streaming = computed(() => !!props.part.streaming)

// Live elapsed timer (only meaningful for parts that streamed this session;
// rehydrated parts from the DB carry no startedAt and show no timer).
const now = ref(Date.now())
const frozen = ref(null)
let timer = null

const seconds = computed(() => {
  if (frozen.value != null) return frozen.value
  if (props.part.startedAt == null) return null
  return Math.max(0, Math.round((now.value - props.part.startedAt) / 1000))
})

const label = computed(() => {
  if (streaming.value) {
    const s = seconds.value
    return s != null ? `Thinking… ${s}s` : 'Thinking…'
  }
  const s = seconds.value
  return s != null ? `Thought for ${s}s` : 'Reasoning'
})

// Auto-expand while streaming; the user can still collapse it manually.
const userToggled = ref(false)
const expanded = ref(streaming.value)
function toggle() {
  userToggled.value = true
  expanded.value = !expanded.value
}

function startTimer() {
  stopTimer()
  timer = setInterval(() => { now.value = Date.now() }, 1000)
}
function stopTimer() {
  if (timer) { clearInterval(timer); timer = null }
}

watch(streaming, (on, was) => {
  if (on) {
    if (!userToggled.value) expanded.value = true
    startTimer()
  } else {
    // Freeze the final duration and auto-collapse (unless the user opened it).
    if (props.part.startedAt != null) frozen.value = Math.max(0, Math.round((Date.now() - props.part.startedAt) / 1000))
    stopTimer()
    if (was && !userToggled.value) expanded.value = false
  }
})

onMounted(() => { if (streaming.value) startTimer() })
onUnmounted(stopTimer)
</script>

<style scoped>
/* Subtle breathing shimmer on the "Thinking…" label while reasoning streams. */
.thinking-shimmer {
  animation: thinking-pulse 1.6s ease-in-out infinite;
}
@keyframes thinking-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.55; }
}
@media (prefers-reduced-motion: reduce) {
  .thinking-shimmer {
    animation: none;
  }
}
</style>
