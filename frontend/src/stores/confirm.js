import { defineStore } from 'pinia'
import { ref } from 'vue'

// Global confirm dialog. Replaces window.confirm() / window.alert() /
// window.prompt() so we get consistent theming and don't get the focus-
// stealing browser pop-up. Usage:
//   const c = useConfirmStore()
//   if (await c.ask({ title: 'Delete?', message: '…', danger: true })) { … }
//   await c.notify({ title: 'Deployed', message: '…' })
//   const name = await c.prompt({ title: 'Save fixture as…', placeholder: 'name' })
export const useConfirmStore = defineStore('confirm', () => {
  const visible = ref(false)
  const title = ref('')
  const message = ref('')
  const confirmLabel = ref('Confirm')
  const cancelLabel = ref('Cancel')
  const danger = ref(false)
  const noticeOnly = ref(false)
  // Prompt-mode state — when promptMode is true the dialog renders a
  // text input under the message and resolves with the typed string
  // (or null on cancel). Replaces window.prompt().
  const promptMode = ref(false)
  const promptValue = ref('')
  const promptPlaceholder = ref('')
  let resolver = null

  const ask = (opts = {}) => {
    title.value = opts.title || 'Are you sure?'
    message.value = opts.message || ''
    confirmLabel.value = opts.confirmLabel || 'Confirm'
    cancelLabel.value = opts.cancelLabel || 'Cancel'
    danger.value = !!opts.danger
    noticeOnly.value = false
    promptMode.value = false
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
    promptMode.value = false
    visible.value = true
    return new Promise((resolve) => { resolver = resolve })
  }

  // prompt shows a themed text-input dialog — replacement for window.prompt().
  // Resolves with the typed string (trim left to caller) on confirm,
  // or null on cancel/escape so callers can short-circuit cleanly.
  const prompt = (opts = {}) => {
    title.value = opts.title || 'Enter a value'
    message.value = opts.message || ''
    confirmLabel.value = opts.confirmLabel || 'OK'
    cancelLabel.value = opts.cancelLabel || 'Cancel'
    danger.value = !!opts.danger
    noticeOnly.value = false
    promptMode.value = true
    promptValue.value = opts.defaultValue || ''
    promptPlaceholder.value = opts.placeholder || ''
    visible.value = true
    return new Promise((resolve) => { resolver = resolve })
  }

  const settle = (result) => {
    visible.value = false
    if (resolver) {
      // For prompt dialogs, settle(true) resolves with the typed value;
      // settle(false) resolves with null. Confirm/notify keep the
      // boolean shape callers already expect.
      if (promptMode.value) {
        resolver(result ? promptValue.value : null)
      } else {
        resolver(result)
      }
      resolver = null
    }
    promptMode.value = false
  }

  return {
    visible, title, message, confirmLabel, cancelLabel, danger, noticeOnly,
    promptMode, promptValue, promptPlaceholder,
    ask, notify, prompt, settle,
  }
})
