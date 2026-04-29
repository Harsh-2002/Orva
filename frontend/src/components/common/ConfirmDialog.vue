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
import { onMounted, onUnmounted } from 'vue'
import { AlertTriangle, HelpCircle } from 'lucide-vue-next'
import Button from './Button.vue'
import { useConfirmStore } from '@/stores/confirm'

const confirm = useConfirmStore()

const onKey = (e) => {
  if (!confirm.visible) return
  if (e.key === 'Escape') confirm.settle(false)
  if (e.key === 'Enter') confirm.settle(true)
}

onMounted(() => window.addEventListener('keydown', onKey))
onUnmounted(() => window.removeEventListener('keydown', onKey))
</script>
