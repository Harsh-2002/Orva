<template>
  <div class="flex h-screen w-full bg-background overflow-hidden font-sans antialiased text-foreground">
    <Sidebar />
    <main class="flex-1 flex flex-col min-w-0 overflow-hidden relative pt-14 lg:pt-0">
      <!-- keep-alive caches mounted views across navigations. Without it,
           clicking back to a previously-visited route re-creates the view,
           which means another onMounted() + fetch + brief empty-state
           flash before rows appear. With keep-alive, the view's state +
           rows are preserved; SSE keeps them current in the background.

           Editor is included so unsaved code doesn't get nuked when the
           user nips out to /docs or /firewall and back. -->
      <router-view v-slot="{ Component }">
        <keep-alive :max="10">
          <component
            :is="Component"
            class="flex-1 overflow-auto p-4 md:p-8"
          />
        </keep-alive>
      </router-view>
    </main>

    <!-- Session-expiring-soon prompt. The store gates visibility on
         expires_at (set from /auth/me) and a 12-h threshold. -->
    <Toast
      :visible="auth.shouldShowExpiryToast"
      :action-loading="auth.refreshing"
      title="Session expiring soon"
      action-label="Stay signed in"
      @action="onRefresh"
      @dismiss="auth.dismissExpiryToast"
    >
      Your session expires in {{ formatRemaining }}. Click to extend it for another 7 days.
    </Toast>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import Sidebar from './Sidebar.vue'
import Toast from '@/components/common/Toast.vue'
import { useSystemStore } from '@/stores/system'
import { useEventsStore } from '@/stores/events'
import { useAuthStore } from '@/stores/auth'

const system = useSystemStore()
const events = useEventsStore()
const auth = useAuthStore()

// Re-poll Date.now() every 30s so secondsUntilExpiry recomputes and the
// toast appears once the threshold is crossed without the user navigating.
// (computed() doesn't re-run on its own — it needs a reactive dep change.)
const tick = ref(0)
let timer = null

const formatRemaining = computed(() => {
  // Touch tick so this re-evaluates every 30 s.
  tick.value
  const s = auth.secondsUntilExpiry
  if (s == null || s <= 0) return '—'
  if (s < 60) return `${Math.floor(s)}s`
  if (s < 3600) return `${Math.floor(s / 60)} min`
  return `${Math.floor(s / 3600)} h`
})

const onRefresh = async () => {
  const r = await auth.refreshSession()
  if (!r.success) {
    // Couldn't refresh — bounce the user to /login. The router guard will
    // redirect them properly.
    window.location.href = '/login'
  }
}

onMounted(async () => {
  await auth.checkAuth()
  // Open the SSE connection FIRST so subscribers added by system.connect()
  // start receiving events as soon as the browser connects.
  events.connect()
  system.connect()
  timer = setInterval(() => { tick.value++ }, 30000)
})

onUnmounted(() => {
  system.disconnect()
  events.disconnect()
  if (timer) clearInterval(timer)
})
</script>
