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
        class="fixed z-50 min-w-[200px] max-w-[min(30rem,calc(100vw-1rem))] bg-background border border-border rounded-lg shadow-xl overflow-y-auto overflow-x-hidden scrollable"
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

// Roughly a dozen rows plus the search field. Long enough that scrolling is
// rare, short enough that the menu never becomes the page.
const MAX_PANEL = 384

const isOpen = ref(false)
const triggerEl = ref(null)
const panelEl = ref(null)
const panelStyle = ref({})

// The bottom sheet is for touch, not for narrow.
//
// This used to key off Tailwind's sm breakpoint alone, so dragging a desktop
// window under 640px swapped the anchored menu for a full-width sheet that
// slides up from the bottom edge. To someone using a mouse that reads as the
// menu opening somewhere else entirely, and it is: the sheet exists because a
// thumb needs a big reachable target and an anchored popover is fiddly to hit
// on a phone. Neither is true of a mouse in a small window.
//
// Width still matters (a sheet on a wide touchscreen would be silly), so this
// is both conditions, not either.
const isMobile = ref(false)
let mq = null
const syncMobile = () => { if (mq) isMobile.value = mq.matches }

// Which side the panel opens toward, decided once per opening.
//
// It used to be recomputed from the panel's CURRENT height on every call, which
// made the menu jump: the model list loads asynchronously, so the first
// measurement saw a near-empty panel, chose a direction and a top for something
// about 40px tall, and then the content arrived. A menu anchored to the chat
// composer at the bottom of the viewport got placed just below its trigger and
// then grew off the bottom of the screen.
//
// Space, not content, decides the side. Space does not change while the menu is
// open, so the panel stays where it was put and only its height changes.
function place() {
  const t = triggerEl.value?.firstElementChild || triggerEl.value
  if (!t) return
  const r = t.getBoundingClientRect()
  const edge = 8
  const gap = 6
  const spaceBelow = window.innerHeight - r.bottom - gap - edge
  const spaceAbove = r.top - gap - edge

  // Anchor to whichever side has more room, and cap the panel to that room so
  // it can never run off screen no matter how long the list turns out to be.
  // The panel scrolls internally past that point.
  //
  // MAX_PANEL is a second, absolute cap, and it is what makes a menu opened
  // upward and one opened downward read as the same control. Without it the
  // panel simply took whatever space its side offered: anchored to the chat
  // composer it grew to roughly 800px and ran nearly the full height of the
  // window, while the same menu in Settings came out a few hundred px tall.
  // Same component, same list, two different objects. Direction still follows
  // the available space, because a menu at the bottom of the viewport has
  // nowhere to go but up, but the size no longer does.
  const up = spaceAbove > spaceBelow
  const room = Math.max(120, Math.floor(up ? spaceAbove : spaceBelow))
  const maxHeight = Math.min(MAX_PANEL, room)

  const width = panelEl.value?.offsetWidth || 200
  const left = Math.max(edge, Math.min(r.left, window.innerWidth - edge - width))

  // Pin the edge nearest the trigger. Pinning `bottom` when opening upward is
  // what keeps the panel attached to its trigger while its height changes:
  // a `top` computed from a height that has not arrived yet is exactly the bug
  // this replaces.
  panelStyle.value = up
    ? { left: `${left}px`, bottom: `${window.innerHeight - r.top + gap}px`, top: 'auto', maxHeight: `${maxHeight}px` }
    : { left: `${left}px`, top: `${r.bottom + gap}px`, bottom: 'auto', maxHeight: `${maxHeight}px` }

  // Re-clamp horizontally once the panel has a measured width.
  nextTick(() => {
    const el = panelEl.value
    if (!el) return
    const w = el.offsetWidth
    const clamped = Math.max(edge, Math.min(r.left, window.innerWidth - edge - w))
    panelStyle.value = { ...panelStyle.value, left: `${clamped}px` }
  })
}

function open() {
  isOpen.value = true
  if (!isMobile.value) {
    nextTick(() => {
      place()
      observePanel(panelEl.value)
    })
  }
}
function close() {
  isOpen.value = false
  ro?.disconnect()
  ro = null
}
function toggle() { isOpen.value ? close() : open() }

function onDocPointer(e) {
  if (!isOpen.value || isMobile.value) return
  if (panelEl.value?.contains(e.target) || triggerEl.value?.contains(e.target)) return
  close()
}
function onResize() {
  if (isOpen.value && !isMobile.value) place()
}

// The panel's contents can arrive after it opens (a model list being fetched, a
// search field appearing once there are enough options, a filter shortening the
// list). Re-running place() keeps the horizontal clamp honest; the anchored edge
// is pinned to the trigger, so nothing moves vertically while the operator reads.
let ro = null
function observePanel(el) {
  ro?.disconnect()
  if (!el || typeof window.ResizeObserver === 'undefined') return
  ro = new window.ResizeObserver(() => { if (isOpen.value && !isMobile.value) place() })
  ro.observe(el)
}

onMounted(() => {
  mq = window.matchMedia('(max-width: 639px) and (pointer: coarse)')
  syncMobile()
  mq.addEventListener('change', syncMobile)
  document.addEventListener('pointerdown', onDocPointer, true)
  window.addEventListener('resize', onResize)
})
onUnmounted(() => {
  ro?.disconnect()
  mq?.removeEventListener('change', syncMobile)
  document.removeEventListener('pointerdown', onDocPointer, true)
  window.removeEventListener('resize', onResize)
})

defineExpose({ open, close })
</script>
