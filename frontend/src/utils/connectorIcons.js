// Maps an OAuth client_name (best-effort, substring match) to a
// distinctive icon + Tailwind accent class for the Settings →
// Connected applications card. Keep additive; unknown apps fall
// through to the generic Plug.
//
// ChatGPT and Claude get their official brand glyphs (single-color
// SVG, currentColor — inherit the accent). Generic clients get
// Lucide abstracts. The brand glyphs are nominative use (identifying
// the connecting OAuth client by its DCR-supplied client_name),
// which is what every "Connected apps" UI does. Inlined SVGs only —
// no external runtime fetches.

import {
  MousePointer2,
  Code2,
  Terminal,
  Plug,
} from 'lucide-vue-next'
import ChatGPTIcon from '@/components/icons/brand/ChatGPTIcon.vue'
import ClaudeIcon from '@/components/icons/brand/ClaudeIcon.vue'

export function iconForClient(name) {
  const n = (name || '').toLowerCase()
  // Order matters: more specific matches first.
  if (n.includes('claude code')) return { icon: Terminal, accent: 'text-orange-400' }
  if (n.includes('chatgpt') || n.includes('openai')) return { icon: ChatGPTIcon, accent: 'text-emerald-400' }
  if (n.includes('claude') || n.includes('anthropic')) return { icon: ClaudeIcon, accent: 'text-orange-400' }
  if (n.includes('cursor')) return { icon: MousePointer2, accent: 'text-blue-400' }
  if (n.includes('vscode') || n.includes('vs code') || n.includes('code')) {
    return { icon: Code2, accent: 'text-blue-400' }
  }
  return { icon: Plug, accent: 'text-foreground-muted' }
}
