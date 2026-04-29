import { defineStore } from 'pinia'
import { ref } from 'vue'

// Global confirm dialog. Replaces window.confirm() so we get consistent
// theming and don't get the focus-stealing browser pop-up. Usage:
//   const c = useConfirmStore()
//   if (await c.ask({ title: 'Delete?', message: '…', danger: true })) { … }
export const useConfirmStore = defineStore('confirm', () => {
  const visible = ref(false)
  const title = ref('')
  const message = ref('')
  const confirmLabel = ref('Confirm')
  const cancelLabel = ref('Cancel')
  const danger = ref(false)
  const noticeOnly = ref(false)
  let resolver = null

  const ask = (opts = {}) => {
    title.value = opts.title || 'Are you sure?'
    message.value = opts.message || ''
    confirmLabel.value = opts.confirmLabel || 'Confirm'
    cancelLabel.value = opts.cancelLabel || 'Cancel'
    danger.value = !!opts.danger
    noticeOnly.value = false
    visible.value = true
    return new Promise((resolve) => { resolver = resolve })
  }

  // notify shows a single-button dialog — replacement for window.alert().
  // Resolves true when dismissed; callers can ignore the return value.
  const notify = (opts = {}) => {
    title.value = opts.title || 'Notice'
    message.value = opts.message || ''
    confirmLabel.value = opts.confirmLabel || 'OK'
    danger.value = !!opts.danger
    noticeOnly.value = true
    visible.value = true
    return new Promise((resolve) => { resolver = resolve })
  }

  const settle = (result) => {
    visible.value = false
    if (resolver) { resolver(result); resolver = null }
  }

  return {
    visible, title, message, confirmLabel, cancelLabel, danger, noticeOnly,
    ask, notify, settle,
  }
})
