<template>
  <!-- Mobile top bar — only shown <lg. Holds a hamburger that opens the
       sidebar as a drawer over the page. Above lg the sidebar is inline.

       The safe-area inset pads the <header>, while the 56 px content row is an
       inner div. Putting both on one element (h-14 + pt-safe) made the inset
       eat the row instead of adding to it, so on a notched phone in standalone
       mode the mark and the hamburger were squeezed into whatever height was
       left. Layout.vue offsets the page by pt-topbar, which is this same
       56 px + inset sum. -->
  <!-- z-40, one above the drawer backdrop. They used to both be z-30, so the
       backdrop won on DOM order and sat on top of this header: with the menu
       open, tapping the X did nothing, because the backdrop was swallowing the
       pointer event. The close control has to outrank the scrim that its own
       drawer puts up. -->
  <header
    class="lg:hidden fixed top-0 inset-x-0 bg-background border-b border-border z-40 pt-safe pl-safe pr-safe"
  >
    <div class="h-14 flex items-center justify-between px-4">
      <BrandLockup />
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
    </div>
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
      <BrandLockup />
    </div>

    <nav class="flex-1 p-3 space-y-1 overflow-y-auto scrollable">
      <router-link
        v-for="item in primaryItems"
        :key="item.path"
        :to="item.path"
        :class="[
          'flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors duration-150 group font-medium touch-expand-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary',
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

      <div
        v-for="group in navGroups"
        :key="group.id"
        class="pt-1"
      >
        <button
          type="button"
          class="flex w-full items-center gap-3 rounded-md px-3 py-2.5 text-sm font-medium touch-expand-sm text-foreground-muted transition-colors hover:bg-surface-hover hover:text-white focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary"
          :class="groupIsActive(group) ? 'text-white' : ''"
          :aria-expanded="expandedGroups[group.id]"
          :aria-controls="`nav-group-${group.id}`"
          @click="expandedGroups[group.id] = !expandedGroups[group.id]"
        >
          <component
            :is="group.icon"
            class="h-4 w-4"
          />
          <span class="flex-1 text-left">{{ group.label }}</span>
          <ChevronDown
            class="h-3.5 w-3.5 transition-transform"
            :class="expandedGroups[group.id] ? 'rotate-0' : '-rotate-90'"
          />
        </button>
        <div
          v-show="expandedGroups[group.id]"
          :id="`nav-group-${group.id}`"
          class="ml-3 mt-1 space-y-0.5 border-l border-border pl-2"
        >
          <router-link
            v-for="item in group.items"
            :key="item.path"
            :to="item.path"
            :class="[
              'flex items-center gap-3 px-3 py-2 rounded-md text-sm transition-colors duration-150 group font-medium touch-expand-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary',
              isActive(item.path)
                ? 'text-white bg-primary/15'
                : 'text-foreground-muted hover:text-white hover:bg-surface-hover'
            ]"
            @click="open = false"
          >
            <component
              :is="item.icon"
              class="h-4 w-4"
            />
            <span>{{ item.label }}</span>
          </router-link>
        </div>
      </div>

      <div class="mt-2 space-y-1 border-t border-border pt-2">
        <router-link
          v-for="item in secondaryItems"
          :key="item.path"
          :to="item.path"
          :class="[
            'flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors duration-150 group font-medium touch-expand-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary',
            isActive(item.path)
              ? 'text-white bg-primary/15'
              : 'text-foreground-muted hover:text-white hover:bg-surface-hover'
          ]"
          @click="open = false"
        >
          <component
            :is="item.icon"
            class="h-4 w-4"
          />
          <span>{{ item.label }}</span>
        </router-link>
      </div>
    </nav>
  </aside>
</template>

<script setup>
import { ref, watch, nextTick } from 'vue'
import { useRoute } from 'vue-router'
import BrandLockup from './BrandLockup.vue'
import {
  Gauge,
  MessagesSquare,
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
  ChevronDown,
} from '@lucide/vue'

const route = useRoute()
const open = ref(false)
const drawerEl = ref(null)
const toggleBtn = ref(null)
const isActive = (path) => {
  if (path === '/') return route.path === '/'
  return route.path.startsWith(path)
}

const primaryItems = [
  { path: '/',          label: 'Overview',  icon: Gauge },
  { path: '/ai',        label: 'Chat',      icon: MessagesSquare },
  { path: '/functions', label: 'Functions', icon: Boxes },
]

const navGroups = [
  {
    id: 'automation',
    label: 'Automation',
    icon: CalendarClock,
    items: [
      { path: '/cron', label: 'Schedules', icon: CalendarClock },
      { path: '/jobs', label: 'Jobs', icon: ListChecks },
    ],
  },
  {
    id: 'observe',
    label: 'Observe',
    icon: Activity,
    items: [
      { path: '/activity', label: 'Activity', icon: Activity },
      { path: '/invocations', label: 'Invocations', icon: ListTree },
      { path: '/traces', label: 'Traces', icon: Network },
    ],
  },
  {
    id: 'connect',
    label: 'Connect',
    icon: Plug,
    items: [
      { path: '/api-keys', label: 'Keys', icon: Fingerprint },
      { path: '/channels', label: 'Channels', icon: Plug },
      { path: '/webhooks', label: 'Webhooks', icon: Webhook },
      { path: '/firewall', label: 'Egress', icon: ShieldHalf },
    ],
  },
]

const secondaryItems = [
  { path: '/settings', label: 'Settings', icon: Settings },
  { path: '/docs', label: 'Docs', icon: LibraryBig },
]

const expandedGroups = ref(Object.fromEntries(navGroups.map((group) => [group.id, false])))
const groupIsActive = (group) => group.items.some((item) => isActive(item.path))
const revealActiveGroup = () => {
  const active = navGroups.find(groupIsActive)
  if (active) expandedGroups.value[active.id] = true
}

revealActiveGroup()
watch(() => route.fullPath, () => {
  open.value = false
  revealActiveGroup()
})

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

</script>
