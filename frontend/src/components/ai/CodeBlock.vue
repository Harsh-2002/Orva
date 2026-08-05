<template>
  <!--
    CodeBlock — a standalone fenced-code block for assistant messages and
    tool-call JSON. Top bar carries an uppercase language label + a copy button
    that flips Copy→Check for 1.2s (the same affordance the Docs page uses, so
    the dashboard reads as one design system). Very long blocks clamp to a
    preview height with a fade + Show-all toggle so a 400-line dump doesn't
    swamp the conversation.
  -->
  <div class="cb">
    <div class="cb-bar">
      <span class="cb-lang">{{ lang || 'text' }}</span>
      <button
        class="cb-copy"
        type="button"
        :title="copied ? 'Copied' : 'Copy code'"
        @click="onCopy"
      >
        <component
          :is="copied ? Check : Copy"
          class="w-3 h-3"
        />
        {{ copied ? 'Copied' : 'Copy' }}
      </button>
    </div>
    <div
      class="cb-scroll"
      :class="{ 'cb-clamped': clamped }"
    >
      <pre class="cb-pre"><code
        class="hljs"
        v-html="highlighted"
      /></pre>
      <div
        v-if="clamped"
        class="cb-fade"
      />
    </div>
    <button
      v-if="collapsible"
      class="cb-expand"
      type="button"
      @click="clamped = !clamped"
    >
      <component
        :is="clamped ? ChevronsUpDown : ChevronsDownUp"
        class="w-3 h-3"
      />
      {{ clamped ? `Show all ${lineCount} lines` : 'Collapse' }}
    </button>
  </div>
</template>

<script setup>
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { Copy, Check, ChevronsUpDown, ChevronsDownUp } from '@lucide/vue'
import { highlightCode } from '@/utils/highlight'
import { copyText } from '@/utils/clipboard'

const props = defineProps({
  code: { type: String, required: true },
  lang: { type: String, default: '' },
  // Blocks longer than this clamp to a preview with a Show-all toggle.
  collapseAfter: { type: Number, default: 24 },
})

const lineCount = computed(() => props.code.split('\n').length)
const collapsible = computed(() => lineCount.value > props.collapseAfter)
const clamped = ref(collapsible.value)

// Re-highlighting the full block on every streamed token is the dominant cost of
// a live code block; throttle to ~12/s (leading + trailing) so the first and
// final render are exact. Static (loaded) blocks highlight once at setup.
const highlighted = ref(highlightCode(props.code, props.lang))
let hlTimer = null
let lastHl = 0
watch(
  () => props.code,
  () => {
    const since = Date.now() - lastHl
    const run = () => { lastHl = Date.now(); highlighted.value = highlightCode(props.code, props.lang) }
    if (since >= 80) run()
    else { clearTimeout(hlTimer); hlTimer = setTimeout(run, 80 - since) }
  },
)
onBeforeUnmount(() => clearTimeout(hlTimer))

const copied = ref(false)
let resetTimer = null
async function onCopy() {
  const ok = await copyText(props.code)
  if (!ok) return
  copied.value = true
  clearTimeout(resetTimer)
  resetTimer = setTimeout(() => { copied.value = false }, 1200)
}
</script>

<style scoped>
.cb {
  background: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 0.6rem;
  overflow: hidden;
  margin: 0.5rem 0;
}
.cb-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.4rem 0.7rem;
  background: var(--color-surface);
  border-bottom: 1px solid var(--color-border);
}
.cb-lang {
  font-family: var(--font-mono);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  color: var(--color-foreground-muted);
}
.cb-copy {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  padding: 0.25rem 0.55rem;
  background: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 0.35rem;
  color: var(--color-foreground-muted);
  font-family: var(--font-sans);
  font-size: 11px;
  cursor: pointer;
  transition: color 120ms, border-color 120ms, background 120ms;
}
.cb-copy:hover {
  color: var(--color-foreground);
  border-color: color-mix(in srgb, var(--color-primary) 60%, transparent);
  background: var(--color-surface-hover);
}
.cb-copy:focus-visible,
.cb-expand:focus-visible {
  outline: 2px solid var(--color-primary);
  outline-offset: 2px;
  color: var(--color-foreground);
}
.cb-scroll {
  position: relative;
}
.cb-clamped {
  max-height: 16rem;
  overflow: hidden;
}
.cb-pre {
  margin: 0;
  padding: 0.8rem 0.95rem;
  overflow-x: auto;
  font-family: var(--font-mono);
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--color-foreground-muted);
  background: var(--color-background);
}
.cb-pre code {
  background: transparent !important;
  padding: 0 !important;
  font-family: inherit;
  font-size: inherit;
  line-height: inherit;
}
.cb-fade {
  position: absolute;
  inset: auto 0 0 0;
  height: 3.5rem;
  pointer-events: none;
  background: linear-gradient(180deg, transparent 0%, var(--color-background) 92%);
}
.cb-expand {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  width: 100%;
  padding: 0.4rem 0.7rem;
  background: var(--color-surface);
  border: 0;
  border-top: 1px solid var(--color-border);
  color: var(--color-foreground-muted);
  font-family: var(--font-sans);
  font-size: 11px;
  cursor: pointer;
  transition: color 120ms, background 120ms;
}
.cb-expand:hover {
  color: var(--color-foreground);
  background: var(--color-surface-hover);
}
</style>
