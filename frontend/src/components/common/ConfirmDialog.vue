<template>
  <transition name="fade">
    <div
      v-if="confirm.visible"
      class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-sm"
      @click.self="confirm.settle(false)"
      @keydown.esc="confirm.settle(false)"
    >
      <div
        class="w-full max-w-md bg-background border border-border rounded-lg shadow-xl p-6 space-y-4"
        role="dialog"
        aria-modal="true"
      >
        <div class="flex items-start gap-3">
          <div
            class="shrink-0 w-9 h-9 rounded-full flex items-center justify-center"
            :class="confirm.danger ? 'bg-red-500/15 text-red-400' : 'bg-primary/15 text-primary'"
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
            <h3 class="text-base font-semibold text-white">
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
              class="mt-3 w-full bg-surface-hover border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:border-white"
              @keydown.enter.stop.prevent="confirm.settle(true)"
            >
          </div>
        </div>
        <div class="flex justify-end gap-2 pt-2">
          <Button
            v-if="!confirm.noticeOnly"
            variant="secondary"
            @click="confirm.settle(false)"
          >
            {{ confirm.cancelLabel }}
          </Button>
          <Button
            :variant="confirm.danger ? 'danger' : 'primary'"
            @click="confirm.settle(true)"
          >
            {{ confirm.confirmLabel }}
          </Button>
        </div>
      </div>
    </div>
  </transition>
</template>

<script setup>
import { nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { AlertTriangle, HelpCircle } from 'lucide-vue-next'
import Button from './Button.vue'
import { useConfirmStore } from '@/stores/confirm'

const confirm = useConfirmStore()
const promptInput = ref(null)

// Auto-focus the prompt input when the dialog opens in prompt mode so
// the operator can start typing immediately. Without this they have to
// click into the field, which feels worse than the native prompt.
watch(
  () => confirm.visible && confirm.promptMode,
  (active) => {
    if (active) nextTick(() => promptInput.value?.focus())
  },
)

const onKey = (e) => {
  if (!confirm.visible) return
  if (e.key === 'Escape') confirm.settle(false)
  // Enter on prompt-mode is handled by the input's @keydown.enter so the
  // typed value commits cleanly; the global Enter handler only fires for
  // confirm/notify dialogs without a focused input.
  if (e.key === 'Enter' && !confirm.promptMode) confirm.settle(true)
}

onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>
