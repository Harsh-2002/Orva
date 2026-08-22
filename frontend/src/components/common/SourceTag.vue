<template>
  <span
    class="inline-flex items-center gap-1.5 px-2 py-0.5 rounded text-xs border bg-background font-mono uppercase tracking-wide"
    :class="tagClass"
  >
    <component
      :is="meta.icon"
      class="w-3 h-3 shrink-0"
      aria-hidden="true"
    />
    {{ source || EMPTY }}
  </span>
</template>

<script setup>
import { EMPTY } from '@/utils/format'
import { computed } from 'vue'
import { Globe, Terminal, Plug, Package, Webhook, Clock, Cog, Circle } from '@lucide/vue'

// SourceTag renders the "where this call came from" pill on the Activity page.
// Seven known sources, each with a distinct hue AND a distinct glyph so an
// operator can scan a busy feed and tell a UI click from a CLI deploy from an
// MCP tool from a webhook delivery.
//
// The glyph is a scanning aid, not a replacement: these are abstract concepts
// with no marks anyone already knows, so the word stays. That is the opposite
// call from RuntimeTag, where Python and Node have marks the reader recognises
// instantly and the word can move to the accessible name. See DESIGN.md
// section 7.
//
// The glyph also carries the distinction when hue cannot. The six source hues
// were picked to sit apart from StatusBadge's success/error/warning palette,
// but violet-vs-indigo and teal-vs-emerald are a coin flip for a red-green
// colour-blind operator, and these pills sit inches from status pills in the
// same feed. Shape settles it without re-picking the palette.

const props = defineProps({
  source: { type: String, default: '' },
})

const SOURCES = {
  web: { icon: Globe, cls: 'text-indigo-300 border-indigo-900/40' },
  api: { icon: Terminal, cls: 'text-sky-300 border-sky-900/40' },
  mcp: { icon: Plug, cls: 'text-violet-300 border-violet-900/40' },
  sdk: { icon: Package, cls: 'text-teal-300 border-teal-900/40' },
  webhook: { icon: Webhook, cls: 'text-amber-300 border-amber-900/40' },
  cron: { icon: Clock, cls: 'text-emerald-300 border-emerald-900/40' },
  internal: { icon: Cog, cls: 'text-foreground-muted border-border' },
}

const meta = computed(
  () => SOURCES[props.source] || { icon: Circle, cls: 'text-foreground-muted border-border' },
)
const tagClass = computed(() => meta.value.cls)
</script>
