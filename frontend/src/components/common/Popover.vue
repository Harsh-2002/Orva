<template>
  <!--
    Popover — an anchored menu that escapes whatever overflow container its
    trigger lives in. The chat composer sits in a bottom footer; a plain
    absolutely-positioned dropdown would be clipped, so on desktop the panel is
    Teleported to <body> and positioned with `position: fixed` computed from the
    trigger's rect. It opens toward the side with usable viewport space, which
    sends the chat composer upward and Settings controls downward. On mobile it
    becomes a Drawer bottom-sheet instead — thumb-reachable, never clipped.

    Usage:
      <Popover title="Reasoning">
        <template #trigger="{ open }"> …button… </template>
        <template #default="{ close }"> …menu items… </template>
      </Popover>
  -->
  <span
    ref="triggerEl"
    :class="wide ? 'flex w-full' : 'inline-flex'"
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
defineOptions({ name: 'CommonPopover' })

import { ref, nextTick, onMounted, onUnmounted } from 'vue'
import Drawer from './Drawer.vue'

defineProps({
  title: { type: String, default: '' },
  wide: { type: Boolean, default: false },
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
  const edge = 8
  const gap = 6
  const panelWidth = panelEl.value?.offsetWidth || 200
  const panelHeight = panelEl.value?.offsetHeight || 0
  const spaceBelow = window.innerHeight - r.bottom - gap - edge
  const spaceAbove = r.top - gap - edge
  // Settings triggers usually have room below; composer triggers sit at the
  // viewport bottom. Choose from real space instead of assuming one context.
  const opensDown = spaceBelow >= panelHeight || spaceBelow >= spaceAbove
  const left = Math.max(edge, Math.min(r.left, window.innerWidth - edge - panelWidth))
  const top = opensDown
    ? Math.min(r.bottom + gap, window.innerHeight - edge - panelHeight)
    : Math.max(edge, r.top - gap - panelHeight)
  panelStyle.value = {
    left: `${left}px`,
    top: `${top}px`,
    bottom: 'auto',
  }
  // Re-clamp once the panel has a measured width.
  nextTick(() => {
    const p = panelEl.value
    if (!p) return
    const w = p.offsetWidth
    const clampedLeft = Math.max(edge, Math.min(r.left, window.innerWidth - edge - w))
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
