<template>
  <!--
    MessageList — the scrollable conversation timeline.

    The scroller is full-bleed (scrollbar hugs the window edge) but content sits
    in a centered, readable column so prose never runs past ~80ch on a wide
    operator monitor. Vertical rhythm is turn-aware: a new user prompt opens a
    turn with generous space above it; the assistant reply and any tool cards it
    triggers sit tight beneath, reading as one grouped exchange.

    Autoscroll follows the live edge ONLY while the operator is already near the
    bottom. Scroll up mid-stream and it pauses, surfacing a "Latest" button to
    jump back — so reading earlier output isn't yanked away by new tokens.
  -->
  <div class="relative min-h-0 flex-1">
    <div
      ref="scroller"
      class="scrollable h-full overflow-y-auto px-4"
      role="log"
      aria-live="polite"
      aria-atomic="false"
      aria-label="Chat messages"
      @scroll="onScroll"
    >
      <div class="mx-auto w-full max-w-3xl py-6">
        <template
          v-for="(item, index) in items"
          :key="item.id ?? index"
        >
          <Message
            v-if="item.kind === 'message'"
            :role="item.role"
            :parts="item.parts"
            :can-regenerate="item.role === 'assistant' && !streaming && index === lastAssistantIndex"
            :editable="item.role === 'user' && !streaming"
            :deletable="!streaming && !!item.id"
            :class="gap(index)"
            @regenerate="$emit('regenerate')"
            @edit="$emit('edit', { id: item.id, content: $event })"
            @delete="$emit('delete', item.id)"
          />
          <ToolCallCard
            v-else-if="item.kind === 'tool'"
            :tool="item"
            :class="gap(index)"
            @approve="$emit('approve', $event)"
            @reject="$emit('reject', $event)"
          />
          <ErrorCard
            v-else-if="item.kind === 'error'"
            :message="item.message"
            :class="gap(index)"
            @retry="$emit('retry')"
            @dismiss="$emit('dismiss', item.id)"
          />
        </template>
        <TypingIndicator
          v-if="showTyping"
          :class="items.length ? 'mt-4' : ''"
        />
      </div>
    </div>

    <ScrollToBottom
      :visible="!pinned"
      @click="jumpToBottom"
    />
  </div>
</template>

<script setup>
import { ref, computed, watch, nextTick, onMounted } from 'vue'
import Message from './Message.vue'
import ToolCallCard from './ToolCallCard.vue'
import ErrorCard from './ErrorCard.vue'
import TypingIndicator from './TypingIndicator.vue'
import ScrollToBottom from './ScrollToBottom.vue'

const props = defineProps({
  items: { type: Array, required: true },
  streaming: { type: Boolean, default: false }
})

defineEmits(['approve', 'reject', 'regenerate', 'edit', 'delete', 'retry', 'dismiss'])

// Index of the last assistant message — only it gets the Regenerate action.
const lastAssistantIndex = computed(() => {
  for (let i = props.items.length - 1; i >= 0; i--) {
    const it = props.items[i]
    if (it.kind === 'message' && it.role === 'assistant') return i
  }
  return -1
})

// Show the typing indicator while streaming but before the assistant has produced
// any visible content yet (the model time-to-first-token gap, or post-approval
// work). It's replaced the instant thinking/text streams in.
const showTyping = computed(() => {
  if (!props.streaming) return false
  const last = props.items[props.items.length - 1]
  if (!last) return false
  if (last.kind === 'tool') return true
  if (last.kind === 'message') {
    if (last.role === 'user') return true
    const hasContent = (last.parts || []).some(
      (p) => (p.type === 'text' && p.text) || (p.type === 'thinking' && p.text)
    )
    return !hasContent
  }
  return false
})

const scroller = ref(null)
// pinned = following the live edge. Flips off when the operator scrolls up.
const pinned = ref(true)

// Turn-aware top gap: a new user prompt opens a turn (generous space); the
// assistant reply groups under it; tool cards attach tightly to the turn.
function gap(index) {
  if (index === 0) return ''
  const cur = props.items[index]
  const prev = props.items[index - 1]
  if (cur.kind === 'message' && cur.role === 'user') return 'mt-8'   // new turn
  if (cur.kind === 'tool') return 'mt-2'                             // attach to turn
  return prev.kind === 'tool' ? 'mt-3' : 'mt-4'                      // assistant reply
}

// Length of all text in the most recent item. Streaming replaces the trailing
// part immutably, so item count alone never changes during a token stream — we
// watch this to keep the viewport pinned while tokens arrive.
function lastTextLength() {
  const last = props.items[props.items.length - 1]
  if (!last || last.kind !== 'message' || !Array.isArray(last.parts)) return 0
  let total = 0
  for (const part of last.parts) {
    total += part && typeof part.text === 'string' ? part.text.length : 0
  }
  return total
}

function isNearBottom() {
  const el = scroller.value
  if (!el) return true
  return el.scrollTop + el.clientHeight >= el.scrollHeight - 100
}

function onScroll() {
  pinned.value = isNearBottom()
}

async function scrollToBottom() {
  await nextTick()
  const el = scroller.value
  if (el) el.scrollTop = el.scrollHeight
}

function jumpToBottom() {
  pinned.value = true
  scrollToBottom()
}

// Follow new content only while pinned; otherwise leave the operator where they
// scrolled and let the "Latest" button bring them back.
watch(
  () => [props.items.length, props.streaming, lastTextLength()],
  () => { if (pinned.value) scrollToBottom() }
)

onMounted(scrollToBottom)
</script>
