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

// Tokens, not raw palette classes. The border is the same hue at 30% rather
// than a separate -900 step, so it re-anchors against whichever canvas is
// behind it instead of assuming a dark one.
const SOURCES = {
  web: { icon: Globe, cls: 'text-source-web border-source-web/30' },
  api: { icon: Terminal, cls: 'text-source-api border-source-api/30' },
  mcp: { icon: Plug, cls: 'text-source-mcp border-source-mcp/30' },
  sdk: { icon: Package, cls: 'text-source-sdk border-source-sdk/30' },
  webhook: { icon: Webhook, cls: 'text-source-webhook border-source-webhook/30' },
  cron: { icon: Clock, cls: 'text-source-cron border-source-cron/30' },
  internal: { icon: Cog, cls: 'text-foreground-muted border-border' },
}

const meta = computed(
  () => SOURCES[props.source] || { icon: Circle, cls: 'text-foreground-muted border-border' },
)
const tagClass = computed(() => meta.value.cls)
</script>
