<template>
  <!--
    Teleport to body so the dialog escapes the App tree. Required for
    the focus-trap composable's `inert` strategy: setting inert on
    #app while the dialog is open disables the rest of the document
    (including SR virtual-cursor and pointer events). Modal.vue,
    Drawer.vue, and CommandPalette already use this pattern; ConfirmDialog
    joins them so all four dialog surfaces share the same a11y shape.
  -->
  <Teleport to="body">
    <transition name="fade">
      <div
        v-if="confirm.visible"
        class="fixed inset-0 z-50 flex items-end sm:items-center justify-center bg-scrim backdrop-blur-sm pt-safe pb-safe pl-safe pr-safe p-2 sm:p-4"
        @click.self="confirm.settle(false)"
        @keydown.esc="confirm.settle(false)"
      >
        <!--
          Mobile bottom-sheet shape: items-end + rounded-top-only so the
          dialog feels like a tray rising into the viewport, not a centred
          card; thumb reach for the confirm button is shorter at the bottom.
          sm+ returns to the centred card.
        -->
        <div
          ref="dialogRoot"
          class="w-full sm:max-w-md bg-background border border-border rounded-t-lg sm:rounded-lg shadow-xl p-5 sm:p-6 space-y-4 max-h-[calc(100dvh-1rem)] overflow-y-auto scrollable"
          role="dialog"
          aria-modal="true"
          :aria-labelledby="titleId"
        >
          <div class="flex items-start gap-3">
            <!-- Warning glyph anchors confirm/notice dialogs; a prompt (rename,
                 save-as) is input-focused and reads cleaner without it. -->
            <div
              v-if="!confirm.promptMode"
              class="shrink-0 w-9 h-9 rounded-full flex items-center justify-center"
              :class="confirm.danger ? 'bg-danger-tint text-danger-fg' : 'bg-primary/15 text-primary'"
            >
              <AlertTriangle
                v-if="confirm.danger"
                class="w-5 h-5"
              />
              <HelpCircle
                v-else
                class="w-5 h-5"
              />
            </div>
            <div class="flex-1 min-w-0">
              <h3
                :id="titleId"
                class="text-sm font-semibold text-foreground-strong tracking-tight"
              >
                {{ confirm.title }}
              </h3>
              <p
                v-if="confirm.message"
                class="text-sm text-foreground-muted mt-1 whitespace-pre-line break-words"
              >
                {{ confirm.message }}
              </p>
              <input
                v-if="confirm.promptMode"
                ref="promptInput"
                v-model="confirm.promptValue"
                :placeholder="confirm.promptPlaceholder"
                type="text"
                class="mt-3 w-full h-10 bg-background border border-border rounded-md px-3 text-sm text-foreground placeholder-foreground-muted transition-colors duration-200 focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
                @keydown.enter.stop.prevent="confirm.settle(true)"
              >
            </div>
          </div>
          <div class="flex flex-col-reverse sm:flex-row sm:justify-end gap-2 pt-2">
            <Button
              v-if="!confirm.noticeOnly"
              variant="ghost"
              class="w-full sm:w-auto"
              @click="confirm.settle(false)"
            >
              {{ confirm.cancelLabel }}
            </Button>
            <Button
              :variant="confirm.danger ? 'danger' : 'primary'"
              class="w-full sm:w-auto"
              @click="confirm.settle(true)"
            >
              {{ confirm.confirmLabel }}
            </Button>
          </div>
        </div>
      </div>
    </transition>
  </Teleport>
</template>

<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { AlertTriangle, HelpCircle } from '@lucide/vue'
import Button from './Button.vue'
import { useConfirmStore } from '@/stores/confirm'
import { useFocusTrap } from '@/composables/useFocusTrap'

const confirm = useConfirmStore()
const promptInput = ref(null)
const dialogRoot = ref(null)
// Stable id for aria-labelledby. The title text is dynamic (different
// per ask/notify/prompt call) but the element id stays the same;
// screen readers re-announce the new title content on dialog open.
const titleId = 'confirm-dialog-title'

// Focus trap follows the dialog's visible state. The composable
// activates on confirm.visible flip-true and deactivates on flip-false.
// Coexists with the prompt-mode auto-focus below: the composable
// focuses first-focusable on activate (typically the prompt input
// since it's first in DOM order in prompt mode), then the existing
// scrollIntoView at line ~110 still fires post-tick.
useFocusTrap(dialogRoot, computed(() => confirm.visible))

// Auto-focus the prompt input when the dialog opens in prompt mode so
// the operator can start typing immediately. Without this they have to
// click into the field, which feels worse than the native prompt.
//
// On mobile we also scrollIntoView({block:'center'}) once the on-screen
// keyboard has had a moment to appear (50 ms is enough on iOS Safari +
// Android Chrome to let visualViewport settle). Without this the input
// can sit underneath the keyboard for a beat after focus, which feels
// broken even though the modal itself is keyboard-aware via 100dvh.
watch(
  () => confirm.visible && confirm.promptMode,
  (active) => {
    if (active) {
      nextTick(() => {
        const input = promptInput.value
        if (!input) return
        input.focus()
        if (window.innerWidth < 640) {
          setTimeout(() => {
            input.scrollIntoView({ block: 'center', behavior: 'smooth' })
          }, 50)
        }
      })
    }
  },
)

const onKey = (e) => {
  if (!confirm.visible) return
  if (e.key === 'Escape') confirm.settle(false)
  // There is deliberately no global Enter handler. There used to be one, and it
  // confirmed the dialog no matter what was focused: the focus trap lands on the
  // first focusable descendant, which is Cancel, so Enter on Cancel dispatched a
  // keydown that bubbled to window and settled true. Cancel's own settle(false)
  // then no-opped, because the store nulls its resolver on the first settle. The
  // net effect was that Enter on Cancel deleted the function, and that dialog is
  // the single confirmation behind delete, bulk delete, key revocation, firewall
  // rule deletion and backup restore.
  //
  // Enter needs no handler here: it already activates whichever button has focus,
  // natively. Prompt mode commits through the input's own @keydown.enter above.
}

onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>
