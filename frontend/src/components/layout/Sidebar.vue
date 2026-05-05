<template>
  <!-- Mobile top bar — only shown <lg. Holds a hamburger that opens the
       sidebar as a drawer over the page. Above lg the sidebar is inline.
       pt-safe keeps the bar clear of the iOS notch / status bar in PWA
       mode; on non-notched hardware the inset is 0 and nothing changes. -->
  <header
    class="lg:hidden fixed top-0 inset-x-0 h-14 bg-background border-b border-border z-30 flex items-center justify-between px-4 pt-safe pl-safe pr-safe"
  >
    <div class="flex items-center gap-2 text-white font-mono">
      <OrvaLogo class="w-6 h-6" />
      <span class="font-bold tracking-tight">Orva</span>
    </div>
    <button
      ref="toggleBtn"
      class="p-2 rounded-md text-foreground-muted hover:text-white hover:bg-surface transition-colors touch-expand-iconbtn"
      :aria-label="open ? 'Close menu' : 'Open menu'"
      :aria-expanded="open"
      aria-controls="primary-navigation"
      @click="open = !open"
    >
      <Menu
        v-if="!open"
        class="w-5 h-5"
      />
      <X
        v-else
        class="w-5 h-5"
      />
    </button>
  </header>

  <!-- Backdrop (mobile only when drawer open). -->
  <transition name="fade">
    <div
      v-if="open"
      class="lg:hidden fixed inset-0 bg-black/50 z-30 backdrop-blur-sm"
      @click="open = false"
    />
  </transition>

  <aside
    id="primary-navigation"
    ref="drawerEl"
    class="bg-background border-r border-border flex flex-col h-full shrink-0 z-40
           w-64 lg:w-52
           fixed inset-y-0 left-0 transform transition-transform duration-150 ease-out
           lg:static lg:translate-x-0 lg:transform-none lg:transition-none
           pt-safe pb-safe pl-safe"
    :class="open ? 'translate-x-0' : '-translate-x-full lg:translate-x-0'"
    @touchstart="onTouchStart"
    @touchmove="onTouchMove"
    @touchend="onTouchEnd"
  >
    <div class="h-16 flex items-center px-6 border-b border-border">
      <div class="flex items-center gap-3 text-white font-mono tracking-tight text-lg">
        <OrvaLogo class="w-8 h-8" />
        <span class="font-bold tracking-tight text-white">Orva</span>
      </div>
    </div>

    <nav class="flex-1 p-4 space-y-1 overflow-y-auto scrollable">
      <!--
        Active-row treatment is flat-by-default per DESIGN.md. The
        previous shadow-lg shadow-purple-900/20 was the single most
        AI-templatey pixel in the app. New shape: a translucent
        primary tint plus white text and white icon. Three signals
        ("you are here") with no side-stripe (DESIGN.md absolute ban)
        and no glow. Hover stays bg-surface-hover so the inactive
        state has a clear progression on pointer-over.
      -->
      <router-link
        v-for="item in navItems"
        :key="item.path"
        :to="item.path"
        :class="[
          'flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors duration-150 group font-medium',
          isActive(item.path)
            ? 'text-white bg-primary/15'
            : 'text-foreground-muted hover:text-white hover:bg-surface-hover'
        ]"
        @click="open = false"
      >
        <component
          :is="item.icon"
          class="w-4 h-4 transition-colors"
          :class="isActive(item.path) ? 'text-white' : 'text-foreground-muted group-hover:text-white'"
        />
        <span>{{ item.label }}</span>
      </router-link>
    </nav>
  </aside>
</template>

<script setup>
import { ref, watch, nextTick } from 'vue'
import { useRoute } from 'vue-router'
import OrvaLogo from '../OrvaLogo.vue'
import {
  Gauge,
  Boxes,
  CalendarClock,
  ListChecks,
  Activity,
  ListTree,
  Network,
  Fingerprint,
  Plug,
  Webhook,
  ShieldHalf,
  Settings,
  LibraryBig,
  Menu,
  X,
} from 'lucide-vue-next'

const route = useRoute()
const open = ref(false)
const drawerEl = ref(null)
const toggleBtn = ref(null)

watch(() => route.fullPath, () => { open.value = false })

// Focus discipline: when the drawer opens, move focus into it (first
// nav link) so keyboard users land where they can navigate. When it
// closes, restore focus to the hamburger toggle so the operator's
// place isn't lost. activeElement is the natural reference point;
// we cache it on open and use it on close so a route navigation
// from the drawer (which closes it) returns focus to the page link
// rather than yanking back to the toggle.
watch(open, async (isOpen) => {
  await nextTick()
  if (isOpen) {
    const firstLink = drawerEl.value?.querySelector('a[href]')
    firstLink?.focus?.()
  } else {
    // Only restore focus to the toggle if the drawer was the active region.
    if (drawerEl.value?.contains(document.activeElement)) {
      toggleBtn.value?.focus?.()
    }
  }
})

// Swipe-left-to-close on the drawer. Threshold of 60 px horizontal
// movement, with vertical movement under 40 px (so a normal vertical
// scroll inside the nav doesn't trip it). Only active below lg —
// the drawer is static-positioned on desktop so swipe is a no-op
// up there anyway, but we early-return to avoid wasted work.
let touchStartX = 0
let touchStartY = 0
let touchActive = false

const onTouchStart = (e) => {
  if (window.innerWidth >= 1024) return // lg+ — drawer is inline
  if (!open.value) return
  const t = e.touches[0]
  touchStartX = t.clientX
  touchStartY = t.clientY
  touchActive = true
}

const onTouchMove = (e) => {
  if (!touchActive) return
  const t = e.touches[0]
  const dx = t.clientX - touchStartX
  const dy = Math.abs(t.clientY - touchStartY)
  // Mostly horizontal, leftward, past the threshold.
  if (dx < -60 && dy < 40) {
    open.value = false
    touchActive = false
  }
}

const onTouchEnd = () => {
  touchActive = false
}

// Sidebar nav — single-word labels, ordered operational → admin →
// reference. Icons chosen for distinct silhouettes (Gauge, Boxes,
// Rocket, Activity, Fingerprint, ShieldHalf, LibraryBig) so each item
// is recognizable at a glance instead of a row of similar shields.
const navItems = [
  { path: '/',            label: 'Overview',    icon: Gauge },
  { path: '/functions',   label: 'Functions',   icon: Boxes },
  { path: '/cron',        label: 'Schedules',   icon: CalendarClock },
  { path: '/jobs',        label: 'Jobs',        icon: ListChecks },
  { path: '/activity',    label: 'Activity',    icon: Activity },
  { path: '/invocations', label: 'Invocations', icon: ListTree },
  { path: '/traces',      label: 'Traces',      icon: Network },
  { path: '/api-keys',    label: 'Keys',        icon: Fingerprint },
  { path: '/channels',  label: 'Channels',  icon: Plug },
  { path: '/webhooks',    label: 'Webhooks',    icon: Webhook },
  { path: '/firewall',    label: 'Firewall',    icon: ShieldHalf },
  { path: '/settings',    label: 'Settings',    icon: Settings },
  { path: '/docs',        label: 'Docs',        icon: LibraryBig },
]

const isActive = (path) => {
  if (path === '/') return route.path === '/'
  return route.path.startsWith(path)
}
</script>
