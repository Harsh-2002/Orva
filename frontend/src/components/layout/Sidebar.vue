<template>
  <!-- Mobile top bar — only shown <lg. Holds a hamburger that opens the
       sidebar as a drawer over the page. Above lg the sidebar is inline. -->
  <header
    class="lg:hidden fixed top-0 inset-x-0 h-14 bg-background border-b border-border z-30 flex items-center justify-between px-4"
  >
    <div class="flex items-center gap-2 text-white font-mono">
      <OrvaLogo class="w-6 h-6" />
      <span class="font-bold tracking-tight">Orva</span>
    </div>
    <button
      class="p-2 rounded-md text-foreground-muted hover:text-white hover:bg-surface transition-colors"
      :aria-label="open ? 'Close menu' : 'Open menu'"
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
    class="bg-background border-r border-border flex flex-col h-full shrink-0 z-40
           w-64 lg:w-52
           fixed inset-y-0 left-0 transform transition-transform duration-150 ease-out
           lg:static lg:translate-x-0 lg:transform-none lg:transition-none"
    :class="open ? 'translate-x-0' : '-translate-x-full lg:translate-x-0'"
  >
    <div class="h-16 flex items-center px-6 border-b border-border">
      <div class="flex items-center gap-3 text-white font-mono tracking-tight text-lg">
        <OrvaLogo class="w-8 h-8" />
        <span class="font-bold tracking-tight text-white">Orva</span>
      </div>
    </div>

    <nav class="flex-1 p-4 space-y-1 overflow-y-auto">
      <router-link
        v-for="item in navItems"
        :key="item.path"
        :to="item.path"
        :class="[
          'flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-colors duration-150 group font-medium',
          isActive(item.path)
            ? 'text-white bg-primary shadow-lg shadow-purple-900/20'
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
import { ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import OrvaLogo from '../OrvaLogo.vue'
import {
  Gauge,
  Boxes,
  Rocket,
  CalendarClock,
  ListChecks,
  Activity,
  ListTree,
  Fingerprint,
  Webhook,
  ShieldHalf,
  Settings,
  LibraryBig,
  Menu,
  X,
} from 'lucide-vue-next'

const route = useRoute()
const open = ref(false)

watch(() => route.fullPath, () => { open.value = false })

// Sidebar nav — single-word labels, ordered operational → admin →
// reference. Icons chosen for distinct silhouettes (Gauge, Boxes,
// Rocket, Activity, Fingerprint, ShieldHalf, LibraryBig) so each item
// is recognizable at a glance instead of a row of similar shields.
const navItems = [
  { path: '/',            label: 'Overview',    icon: Gauge },
  { path: '/functions',   label: 'Functions',   icon: Boxes },
  { path: '/deploy',      label: 'Deploy',      icon: Rocket },
  { path: '/cron',        label: 'Schedules',   icon: CalendarClock },
  { path: '/jobs',        label: 'Jobs',        icon: ListChecks },
  { path: '/activity',    label: 'Activity',    icon: Activity },
  { path: '/invocations', label: 'Invocations', icon: ListTree },
  { path: '/api-keys',    label: 'Keys',        icon: Fingerprint },
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
