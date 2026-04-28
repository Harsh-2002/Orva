<template>
  <aside class="w-52 bg-background border-r border-border flex flex-col h-full shrink-0 transition-colors duration-300">
    <div class="h-16 flex items-center px-6 border-b border-border">
      <div class="flex items-center gap-3 text-white font-mono tracking-tight text-lg">
        <OrvaLogo class="w-8 h-8" />
        <span class="font-bold tracking-tight text-white">Orva</span>
      </div>
    </div>

    <nav class="flex-1 p-4 space-y-1">
      <router-link
        v-for="item in navItems"
        :key="item.path"
        :to="item.path"
        :class="[
          'flex items-center gap-3 px-3 py-2.5 rounded-md text-sm transition-all duration-200 group font-medium',
          isActive(item.path)
            ? 'text-white bg-primary shadow-lg shadow-purple-900/20'
            : 'text-foreground-muted hover:text-white hover:bg-surface-hover'
        ]"
      >
        <component
          :is="item.icon"
          class="w-4 h-4 transition-colors"
          :class="isActive(item.path) ? 'text-white' : 'text-foreground-muted group-hover:text-white'"
        />
        <span>{{ item.label }}</span>
      </router-link>
    </nav>

    <div class="p-4 border-t border-border space-y-4">
      <!-- User Info & Logout -->
      <div class="flex items-center justify-between">
        <div class="flex items-center gap-2 text-xs text-foreground-muted">
          <div class="w-6 h-6 rounded-full bg-primary/20 flex items-center justify-center text-primary font-bold text-[10px]">
            {{ auth.user?.username?.charAt(0).toUpperCase() || 'U' }}
          </div>
          <span class="font-medium">{{ auth.user?.username || 'User' }}</span>
        </div>
        <button
          class="p-1.5 rounded-md hover:bg-surface text-foreground-muted hover:text-danger transition-colors"
          title="Logout"
          @click="handleLogout"
        >
          <LogOut class="w-4 h-4" />
        </button>
      </div>
    </div>
  </aside>
</template>

<script setup>
import { useRoute, useRouter } from 'vue-router'
import OrvaLogo from '../OrvaLogo.vue'
import { useAuthStore } from '@/stores/auth'
import {
  LayoutDashboard,
  Zap,
  PlusCircle,
  Terminal,
  Shield,
  LogOut,
} from 'lucide-vue-next'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const handleLogout = async () => {
  await auth.logout()
  router.push('/login')
}

const navItems = [
  { path: '/', label: 'Overview', icon: LayoutDashboard },
  { path: '/functions', label: 'Functions', icon: Zap },
  { path: '/deploy', label: 'New Function', icon: PlusCircle },
  { path: '/invocations', label: 'Logs & Activity', icon: Terminal },
  { path: '/api-keys', label: 'Access Keys', icon: Shield },
]

const isActive = (path) => {
  if (path === '/') return route.path === '/'
  return route.path.startsWith(path)
}
</script>
