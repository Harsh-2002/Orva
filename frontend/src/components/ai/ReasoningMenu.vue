<template>
  <!--
    ReasoningMenu — the brain-icon control in the composer. Picks the reasoning
    effort the request asks the model for: Off / Think / Deep. The brain tints
    primary whenever reasoning is on, so the current state is legible at a glance
    without opening the menu.

    Model-awareness is cosmetic, never a gate: there's no reliable per-model
    capability API, and a custom model like `qwen3.6-27b` reasons without
    matching any name list. So we always offer all three levels and only show a
    quiet "may be ignored by this model" note when the selected model's name
    doesn't match a known reasoning family. The real signal is whether thinking
    frames actually arrive.
  -->
  <Popover title="Reasoning">
    <template #trigger="{ toggle }">
      <button
        type="button"
        class="touch-expand-sm inline-flex h-8 items-center justify-center gap-1.5 rounded-lg px-2.5 text-xs transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
        :class="active
          ? 'text-primary bg-primary/15 hover:bg-primary/20'
          : 'text-foreground-muted hover:text-foreground hover:bg-surface-hover'"
        :title="`Reasoning: ${currentLabel}`"
        aria-label="Reasoning level"
        @click="toggle"
      >
        <Brain class="w-4 h-4 shrink-0" />
        <span v-if="active">{{ currentLabel }}</span>
      </button>
    </template>

    <template #default="{ close }">
      <div class="py-1">
        <p class="px-3 pt-1.5 pb-1 text-[10px] uppercase tracking-label text-foreground-muted">
          Reasoning
        </p>
        <button
          v-for="lv in LEVELS"
          :key="lv.v"
          type="button"
          class="flex w-full items-center gap-2.5 px-3 py-2 text-left text-sm transition-colors focus-visible:outline-none focus-visible:bg-surface-hover"
          :class="store.thinking === lv.v ? 'text-white bg-primary/15' : 'text-foreground hover:bg-surface-hover'"
          role="menuitemradio"
          :aria-checked="store.thinking === lv.v"
          @click="pick(lv.v, close)"
        >
          <component
            :is="lv.icon"
            class="w-3.5 h-3.5 shrink-0"
            :class="store.thinking === lv.v ? 'text-primary' : 'text-foreground-muted'"
          />
          <span class="flex-1">
            <span class="block">{{ lv.label }}</span>
            <span class="block text-[11px] text-foreground-muted">{{ lv.hint }}</span>
          </span>
          <Check
            v-if="store.thinking === lv.v"
            class="w-3.5 h-3.5 shrink-0 text-primary"
          />
        </button>
        <p
          v-if="active && !modelLikelyReasons"
          class="px-3 pt-1.5 pb-1.5 mt-1 border-t border-border text-[11px] text-foreground-muted leading-snug"
        >
          {{ store.selectedModel || 'This model' }} may ignore reasoning requests.
        </p>
      </div>
    </template>
  </Popover>
</template>

<script setup>
import { computed } from 'vue'
import { Brain, Check, Zap, Sparkles, Ban } from 'lucide-vue-next'
import Popover from '@/components/common/Popover.vue'
import { useAIStore } from '@/stores/ai'

const store = useAIStore()

const LEVELS = [
  { v: 'off', label: 'Off', hint: 'Answer directly', icon: Ban },
  { v: 'standard', label: 'Think', hint: 'Reason before answering', icon: Zap },
  { v: 'deep', label: 'Deep', hint: 'Extended reasoning', icon: Sparkles },
]

const active = computed(() => store.thinking && store.thinking !== 'off')
const currentLabel = computed(() => LEVELS.find((l) => l.v === store.thinking)?.label || 'Off')

// Cosmetic-only allowlist for the "may be ignored" note. Never gates the choice.
const REASONING_PATTERNS = [
  /\bo[1-9]/i, /gpt-5/i, /-thinking/i, /reason/i, /deepseek-r/i,
  /claude.*(sonnet|opus)/i, /qwen.*(think|3)/i, /grok.*(3|4)/i,
]
const modelLikelyReasons = computed(() => {
  const m = store.selectedModel || ''
  return REASONING_PATTERNS.some((re) => re.test(m))
})

function pick(level, close) {
  store.setThinking(level)
  close()
}
</script>
