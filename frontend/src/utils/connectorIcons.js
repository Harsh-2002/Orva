// Maps an OAuth client_name (best-effort, substring match) to a
// distinctive lucide icon + Tailwind accent class for the Settings →
// Connected applications card. Keep additive; unknown apps fall
// through to the generic Plug.
//
// We intentionally avoid shipping brand SVGs (Anthropic / OpenAI
// trademarks). Lucide's abstract glyphs keep us legally clean and
// visually consistent with the rest of the dashboard.

import {
  MessageSquare,
  Sparkles,
  MousePointer2,
  Code2,
  Terminal,
  Plug,
} from 'lucide-vue-next'

export function iconForClient(name) {
  const n = (name || '').toLowerCase()
  // Order matters: more specific matches first.
  if (n.includes('claude code')) return { icon: Terminal, accent: 'text-orange-400' }
  if (n.includes('chatgpt') || n.includes('openai')) return { icon: MessageSquare, accent: 'text-emerald-400' }
  if (n.includes('claude') || n.includes('anthropic')) return { icon: Sparkles, accent: 'text-orange-400' }
  if (n.includes('cursor')) return { icon: MousePointer2, accent: 'text-blue-400' }
  if (n.includes('vscode') || n.includes('vs code') || n.includes('code')) {
    return { icon: Code2, accent: 'text-blue-400' }
  }
  return { icon: Plug, accent: 'text-foreground-muted' }
}
