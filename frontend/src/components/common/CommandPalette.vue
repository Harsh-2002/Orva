<template>
  <!--
    Cmd-K / Ctrl-K command palette. Operator-grade affordance the
    Linear / Tailscale / Raycast family makes muscle memory; PRODUCT.md
    commits to "operator-grade, calm, technical" and the lack of any
    keyboard shortcut layer was the most-felt missing piece.

    Triggered globally via the useKeybindings composable in main.js.
    Items: route nav (Functions, Invocations, Jobs, etc.) plus a few
    quick-action shortcuts (New function). Filter on type. Enter
    activates; arrow keys move; Esc closes.
  -->
  <Teleport to="body">
    <Transition name="fade">
      <div
        v-if="open"
        class="fixed inset-0 z-50 flex items-start justify-center bg-black/60 backdrop-blur-sm pt-[10vh] sm:pt-[15vh] px-4"
        @click.self="close"
      >
        <div
          ref="dialogRoot"
          class="w-full max-w-lg bg-background border border-border rounded-lg shadow-xl overflow-hidden"
          role="dialog"
          aria-modal="true"
          aria-labelledby="command-palette-label"
        >
          <div class="flex items-center gap-2 px-4 py-3 border-b border-border">
            <Search class="w-4 h-4 text-foreground-muted shrink-0" />
            <!-- Visually-hidden label sits inside the input row so screen
                 readers announce "Command palette, search routes" on
                 open. The input's placeholder isn't sufficient (placeholders
                 don't get announced as labels). -->
            <span id="command-palette-label" class="sr-only">Command palette</span>
            <input
              ref="searchInput"
              v-model="query"
              type="text"
              placeholder="Search routes, actions…"
              class="flex-1 bg-transparent border-0 text-base sm:text-sm text-white placeholder-foreground-muted focus:outline-none"
              @keydown.down.prevent="moveSelection(1)"
              @keydown.up.prevent="moveSelection(-1)"
              @keydown.enter.prevent="activate(filtered[selectedIdx])"
              @keydown.esc="close"
            >
            <kbd class="hidden sm:inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-mono text-foreground-muted bg-surface border border-border">esc</kbd>
          </div>
          <ul
            ref="listRef"
            class="max-h-[60dvh] overflow-y-auto scrollable py-1"
            role="listbox"
          >
            <!--
              min-h-[44px] meets the WCAG 2.5.5 touch target; py-2.5
              gives visual breathing room for the hint kbd badges
              without breaking the operator-grade density. text-sm
              on desktop, text-base on mobile (the on-screen keyboard
              never appears while the palette is open since the input
              is the focus target — but text-base also reads better
              at thumb distance).
            -->
            <li
              v-for="(item, idx) in filtered"
              :key="item.id"
              role="option"
              :aria-selected="idx === selectedIdx"
              :class="[
                'flex items-center gap-3 px-4 py-2.5 min-h-[44px] text-base sm:text-sm cursor-pointer',
                idx === selectedIdx
                  ? 'bg-primary/15 text-white'
                  : 'text-foreground hover:bg-surface-hover'
              ]"
              @click="activate(item)"
              @mouseenter="selectedIdx = idx"
            >
              <component :is="item.icon" class="w-4 h-4 shrink-0 text-foreground-muted" />
              <span class="flex-1 truncate">{{ item.label }}</span>
              <span
                v-if="item.shortcut"
                class="hidden sm:inline-flex items-center gap-1 text-[10px] font-mono text-foreground-muted"
              >
                <kbd
                  v-for="key in item.shortcut"
                  :key="key"
                  class="px-1.5 py-0.5 rounded bg-surface border border-border"
                >{{ key }}</kbd>
              </span>
            </li>
            <li
              v-if="!filtered.length"
              class="px-4 py-6 text-center text-sm text-foreground-muted"
            >
              Nothing matches "{{ query }}".
            </li>
          </ul>
          <div class="px-4 py-2 border-t border-border bg-surface/40 flex items-center justify-between text-[10px] text-foreground-muted">
            <span class="flex items-center gap-2">
              <kbd class="px-1.5 py-0.5 rounded font-mono bg-surface border border-border">↑↓</kbd>
              <span>navigate</span>
            </span>
            <span class="flex items-center gap-2">
              <kbd class="px-1.5 py-0.5 rounded font-mono bg-surface border border-border">↵</kbd>
              <span>activate</span>
            </span>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<script setup>
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useFocusTrap } from '@/composables/useFocusTrap'
import {
  Search, Gauge, Boxes, CalendarClock, ListChecks, Activity, ListTree,
  Network, Fingerprint, Plug, Webhook, ShieldHalf, Settings as SettingsIcon,
  LibraryBig, Plus,
} from 'lucide-vue-next'

const router = useRouter()
const open = ref(false)
const query = ref('')
const selectedIdx = ref(0)
const searchInput = ref(null)
const listRef = ref(null)
const dialogRoot = ref(null)

// Focus trap on the palette so Tab cycles between the search input
// and the list options (and back). The existing nextTick(() =>
// searchInput.focus()) at the show() handler still fires; the composable's
// first-focusable activation finds the search input as the first
// focusable in DOM order, so the two paths reinforce rather than fight.
useFocusTrap(dialogRoot, open)

// Item registry. Routes follow the sidebar; actions follow the most
// common operator verbs. Keep flat — no grouping headings inside the
// list; the filter handles disambiguation. Order is "what an operator
// reaches for first" not alphabetical.
const items = [
  { id: 'fn-new',      label: 'New function',      icon: Plus,         action: () => router.push('/functions/new'), shortcut: ['c', 'n'] },
  { id: 'go-fns',      label: 'Functions',         icon: Boxes,        action: () => router.push('/functions'),     shortcut: ['g', 'f'] },
  { id: 'go-inv',      label: 'Invocations',       icon: ListTree,     action: () => router.push('/invocations'),   shortcut: ['g', 'i'] },
  { id: 'go-jobs',     label: 'Jobs',              icon: ListChecks,   action: () => router.push('/jobs'),           shortcut: ['g', 'j'] },
  { id: 'go-cron',     label: 'Schedules',         icon: CalendarClock,action: () => router.push('/cron') },
  { id: 'go-activity', label: 'Activity',          icon: Activity,     action: () => router.push('/activity') },
  { id: 'go-traces',   label: 'Traces',            icon: Network,      action: () => router.push('/traces') },
  { id: 'go-keys',     label: 'API Keys',          icon: Fingerprint,  action: () => router.push('/api-keys') },
  { id: 'go-channels', label: 'Channels',          icon: Plug,         action: () => router.push('/channels') },
  { id: 'go-hooks',    label: 'Webhooks',          icon: Webhook,      action: () => router.push('/webhooks') },
  { id: 'go-fw',       label: 'Firewall',          icon: ShieldHalf,   action: () => router.push('/firewall') },
  { id: 'go-settings', label: 'Settings',          icon: SettingsIcon, action: () => router.push('/settings') },
  { id: 'go-docs',     label: 'Docs',              icon: LibraryBig,   action: () => router.push('/docs') },
  { id: 'go-overview', label: 'Overview',          icon: Gauge,        action: () => router.push('/') },
]

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return items
  return items.filter((it) => it.label.toLowerCase().includes(q))
})

watch(filtered, () => { selectedIdx.value = 0 })

const moveSelection = (delta) => {
  const n = filtered.value.length
  if (!n) return
  selectedIdx.value = (selectedIdx.value + delta + n) % n
  nextTick(() => {
    const li = listRef.value?.querySelectorAll('li[role="option"]')[selectedIdx.value]
    li?.scrollIntoView?.({ block: 'nearest' })
  })
}

const activate = (item) => {
  if (!item) return
  close()
  // Defer the action by a tick so the close transition can start
  // before the route swap, avoiding a flash of the palette over the
  // new view.
  nextTick(() => item.action())
}

const close = () => {
  open.value = false
  query.value = ''
  selectedIdx.value = 0
}

const show = () => {
  open.value = true
  nextTick(() => searchInput.value?.focus())
}

// Global keybindings: cmd/ctrl-K opens; the two-key sequences (g f, g i,
// g j, c n) are tracked via a tiny finite-state machine. The sequence
// timer resets if more than 800 ms passes between keys, so an idle "g"
// followed minutes later by a "f" doesn't fire navigation.
let pendingPrefix = ''
let prefixTimer = null
const SEQUENCE_WINDOW_MS = 800

const isTypingTarget = (el) => {
  if (!el) return false
  const tag = el.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  if (el.isContentEditable) return true
  // CodeMirror's content area is contenteditable; the check above
  // catches it. The prefixed shortcuts must not fire while editing
  // function source.
  return false
}

const onGlobalKey = (e) => {
  // Cmd/Ctrl-K — works even inside inputs (Linear convention).
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault()
    open.value ? close() : show()
    return
  }
  // Cmd/Ctrl-S in the Editor → trigger the Deploy button. The Editor
  // listens for this same shortcut in its own onMounted and runs
  // deployFunction(); we just suppress the browser's "save page" dialog.
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
    if (window.location.pathname.includes('/functions/')) {
      e.preventDefault()
      window.dispatchEvent(new CustomEvent('orva:deploy'))
      return
    }
  }
  // Two-key sequences. Skip if a typing target has focus, or if any
  // modifier is held (those are reserved for cmd-K / cmd-S etc.).
  if (e.metaKey || e.ctrlKey || e.altKey) return
  if (isTypingTarget(document.activeElement)) return

  if (!pendingPrefix) {
    if (e.key === 'g' || e.key === 'c') {
      pendingPrefix = e.key
      clearTimeout(prefixTimer)
      prefixTimer = setTimeout(() => { pendingPrefix = '' }, SEQUENCE_WINDOW_MS)
      return
    }
    return
  }
  // We have a pending prefix, this is the second key.
  const seq = pendingPrefix + e.key
  pendingPrefix = ''
  clearTimeout(prefixTimer)
  const match = items.find((it) => it.shortcut && it.shortcut.join('') === seq)
  if (match) {
    e.preventDefault()
    match.action()
  }
}

onMounted(() => {
  window.addEventListener('keydown', onGlobalKey)
})
onUnmounted(() => {
  window.removeEventListener('keydown', onGlobalKey)
  clearTimeout(prefixTimer)
})

defineExpose({ show, close })
</script>
