<template>
  <!--
    Composer — the chat input shell. A single rounded card holds the textarea on
    top and a toolbar beneath it: reasoning + model controls on the left, the
    send button on the right. While a response streams the send button flips to a
    Stop control that aborts the request. `docked` toggles the bottom-footer
    chrome (true at page bottom while chatting; false when centered on a new
    chat, where AI.vue owns the vertical centering).
  -->
  <component
    :is="docked ? 'footer' : 'div'"
    class="shrink-0 px-4"
    :class="docked ? 'pt-3 pb-composer' : 'py-0'"
  >
    <div class="mx-auto w-full max-w-3xl">
      <div
        class="rounded-2xl border border-border bg-surface transition-colors focus-within:border-foreground-muted/50"
        :class="{ 'opacity-60': disabled }"
      >
        <textarea
          ref="ta"
          v-model="text"
          rows="1"
          :disabled="disabled"
          :placeholder="placeholder"
          aria-label="Message the assistant"
          class="max-h-40 w-full resize-none overflow-y-auto bg-transparent px-3.5 pt-3 pb-1.5 text-sm text-foreground placeholder-foreground-muted/60 focus:outline-none disabled:cursor-not-allowed"
          @input="autogrow"
          @keydown="onKey"
        />
        <div class="flex items-center gap-1.5 px-2 pb-2">
          <template v-if="!disabled">
            <ReasoningMenu />
            <ModelMenu />
          </template>
          <span class="flex-1" />
          <button
            v-if="streaming"
            type="button"
            class="touch-expand-iconbtn inline-flex h-8 w-8 items-center justify-center rounded-lg bg-surface-hover text-foreground transition-colors hover:bg-border focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
            aria-label="Stop generating (Esc)"
            title="Stop generating (Esc)"
            @click="$emit('stop')"
          >
            <Square class="h-3.5 w-3.5 fill-current" />
          </button>
          <button
            v-else
            type="button"
            class="touch-expand-iconbtn inline-flex h-8 w-8 items-center justify-center rounded-lg bg-primary text-primary-foreground shadow-sm transition-colors hover:bg-primary-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-surface disabled:opacity-40 disabled:shadow-none"
            :disabled="disabled || !modelReady || !text.trim()"
            aria-label="Send message"
            title="Send message"
            @click="submit"
          >
            <ArrowUp class="h-4 w-4" />
          </button>
        </div>
      </div>
    </div>
  </component>
</template>

<script setup>
import { ref, computed, nextTick } from 'vue'
import { ArrowUp, Square } from 'lucide-vue-next'
import ReasoningMenu from './ReasoningMenu.vue'
import ModelMenu from './ModelMenu.vue'

const props = defineProps({
  streaming: { type: Boolean, default: false },
  disabled: { type: Boolean, default: false },
  docked: { type: Boolean, default: true },
  // False when a provider is configured but no model is selected (e.g. the
  // endpoint reported no models) — Send is blocked so we don't fire a doomed
  // request. The model menu already shows "No model" as the cue.
  modelReady: { type: Boolean, default: true },
})

const emit = defineEmits(['send', 'stop'])

const text = ref('')
const ta = ref(null)

const placeholder = computed(() =>
  props.disabled ? 'Configure a provider to start' : 'Message the assistant…'
)

function autogrow() {
  const el = ta.value
  if (!el) return
  el.style.height = 'auto'
  el.style.height = `${el.scrollHeight}px`
}

function submit() {
  const value = text.value.trim()
  if (!value || props.streaming || props.disabled || !props.modelReady) return
  emit('send', value)
  text.value = ''
  nextTick(() => {
    if (ta.value) ta.value.style.height = 'auto'
  })
}

function onKey(e) {
  if (e.key === 'Enter' && !e.shiftKey) {
    e.preventDefault()
    submit()
  }
}

// Used by the new-chat starter cards: drop a prompt into the box, size it, and
// focus so the cursor is ready for the operator to tweak and send.
function setText(value) {
  text.value = value
  nextTick(() => {
    autogrow()
    ta.value?.focus()
  })
}

defineExpose({ focus: () => ta.value?.focus(), setText })
</script>
