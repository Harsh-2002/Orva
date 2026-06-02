<template>
  <!--
    Popover — an anchored menu that escapes whatever overflow container its
    trigger lives in. The chat composer sits in a bottom footer; a plain
    absolutely-positioned dropdown would be clipped, so on desktop the panel is
    Teleported to <body> and positioned with `position: fixed` computed from the
    trigger's rect (opening UPWARD, since the composer is page-bottom). On mobile
    it becomes a Drawer bottom-sheet instead — thumb-reachable, never clipped.

    Usage:
      <Popover title="Reasoning">
        <template #trigger="{ open }"> …button… </template>
        <template #default="{ close }"> …menu items… </template>
      </Popover>
  -->
  <span
    ref="triggerEl"
    class="inline-flex"
  >
    <slot
      name="trigger"
      :open="isOpen"
      :toggle="toggle"
    />
  </span>

  <!-- Desktop: anchored fixed popover, Teleported out of the footer overflow. -->
  <Teleport to="body">
    <Transition name="fade">
      <div
        v-if="isOpen && !isMobile"
        ref="panelEl"
        class="fixed z-50 min-w-[200px] max-w-[min(320px,calc(100vw-1rem))] bg-background border border-border rounded-lg shadow-xl overflow-hidden"
        :style="panelStyle"
        role="menu"
        @keydown.esc="close"
      >
        <slot :close="close" />
      </div>
    </Transition>
  </Teleport>

  <!-- Mobile: bottom-sheet drawer. -->
  <Drawer
    v-if="isMobile"
    v-model="isOpen"
    :title="title"
  >
    <div class="py-1">
      <slot :close="close" />
    </div>
  </Drawer>
</template>

<script setup>
import { ref, nextTick, onMounted, onUnmounted } from 'vue'
import Drawer from './Drawer.vue'

defineProps({
  title: { type: String, default: '' },
})

const isOpen = ref(false)
const triggerEl = ref(null)
const panelEl = ref(null)
const panelStyle = ref({})

// Mobile detection mirrors Tailwind's `sm` breakpoint (640px).
const isMobile = ref(false)
let mq = null
const syncMobile = () => { if (mq) isMobile.value = mq.matches }

function place() {
  const t = triggerEl.value?.firstElementChild || triggerEl.value
  if (!t) return
  const r = t.getBoundingClientRect()
  // Open upward from the trigger; clamp horizontally into the viewport.
  const left = Math.max(8, Math.min(r.left, window.innerWidth - 8 - 200))
  panelStyle.value = {
    left: `${left}px`,
    bottom: `${window.innerHeight - r.top + 6}px`,
  }
  // Re-clamp once the panel has a measured width.
  nextTick(() => {
    const p = panelEl.value
    if (!p) return
    const w = p.offsetWidth
    const clampedLeft = Math.max(8, Math.min(r.left, window.innerWidth - 8 - w))
    panelStyle.value = { ...panelStyle.value, left: `${clampedLeft}px` }
  })
}

function open() {
  isOpen.value = true
  if (!isMobile.value) nextTick(place)
}
function close() { isOpen.value = false }
function toggle() { isOpen.value ? close() : open() }

function onDocPointer(e) {
  if (!isOpen.value || isMobile.value) return
  if (panelEl.value?.contains(e.target) || triggerEl.value?.contains(e.target)) return
  close()
}
function onResize() {
  if (isOpen.value && !isMobile.value) place()
}

onMounted(() => {
  mq = window.matchMedia('(max-width: 639px)')
  syncMobile()
  mq.addEventListener('change', syncMobile)
  document.addEventListener('pointerdown', onDocPointer, true)
  window.addEventListener('resize', onResize)
})
onUnmounted(() => {
  mq?.removeEventListener('change', syncMobile)
  document.removeEventListener('pointerdown', onDocPointer, true)
  window.removeEventListener('resize', onResize)
})

defineExpose({ open, close })
</script>
