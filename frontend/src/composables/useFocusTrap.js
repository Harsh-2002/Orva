import { watch, nextTick } from 'vue'

// Focusable selector. Anything that can receive keyboard focus AND that
// the operator can usefully interact with. Excludes negative tabindex
// (decorative focus targets), hidden inputs, and disabled controls.
const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'textarea:not([disabled])',
  'select:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
  'audio[controls]',
  'video[controls]',
  'details > summary',
].join(',')

/**
 * useFocusTrap — accessibility primitive for dialog-style components
 * (Modal, ConfirmDialog, CommandPalette, Drawer).
 *
 * On activate (when isActiveRef flips true):
 *   1. Captures the currently-focused element so focus can be restored
 *      when the dialog closes.
 *   2. Sets the `inert` attribute on `#app` so the rest of the document
 *      becomes non-interactive: pointer events, keyboard focus, AND
 *      screen-reader virtual-cursor are all blocked. Vue's Teleport-to-body
 *      ensures the dialog itself sits OUTSIDE #app and stays interactive.
 *      This is what closes WCAG 2.4.11 (focus not obscured) + 2.1.2
 *      (no keyboard trap on the page) cleanly. Tab-cycling alone leaves
 *      SR users able to read the page underneath, which is broken.
 *   3. Focuses the first element marked `[autofocus]` if any, otherwise
 *      the first focusable descendant. The Modal/ConfirmDialog title
 *      itself is not focusable, so focus lands on the first interactive
 *      control (typically the close button or the primary CTA).
 *   4. Adds a keydown listener that cycles Tab / Shift-Tab inside the
 *      dialog: pressing Tab on the last focusable wraps to the first;
 *      Shift-Tab on the first wraps to the last.
 *
 * On deactivate (isActiveRef flips false):
 *   - Removes the keydown listener.
 *   - Removes `inert` from #app.
 *   - Restores focus to the captured element if it's still in the DOM.
 *
 * Usage:
 *   import { useFocusTrap } from '@/composables/useFocusTrap'
 *   const dialogRoot = ref(null)
 *   const isOpen = ref(false)
 *   useFocusTrap(dialogRoot, isOpen)
 *
 * @param {Ref<HTMLElement|null>} elRef — the dialog's content root.
 *   Must be a Vue ref pointing at the element that contains all
 *   focusable descendants (typically the panel, NOT the backdrop).
 * @param {Ref<boolean>} isActiveRef — drives activate/deactivate.
 */
export function useFocusTrap(elRef, isActiveRef) {
  let savedFocus = null
  let keydownListener = null
  let appEl = null

  const activate = async () => {
    savedFocus = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : null

    appEl = document.getElementById('app')
    if (appEl) appEl.setAttribute('inert', '')

    // Wait for Vue to render the dialog so refs are populated and the
    // initial-focus query has DOM to walk.
    await nextTick()

    const root = elRef.value
    if (!root) return

    // Prefer an explicit autofocus target; otherwise focus first focusable.
    const autofocus = root.querySelector('[autofocus]')
    const target = autofocus || root.querySelector(FOCUSABLE)
    target?.focus?.()

    keydownListener = (e) => {
      if (e.key !== 'Tab') return
      const focusables = Array.from(root.querySelectorAll(FOCUSABLE))
        .filter((el) => !el.hasAttribute('inert') && el.offsetParent !== null)
      if (!focusables.length) {
        e.preventDefault()
        return
      }
      const first = focusables[0]
      const last = focusables[focusables.length - 1]
      const active = document.activeElement
      if (e.shiftKey && active === first) {
        e.preventDefault()
        last.focus()
      } else if (!e.shiftKey && active === last) {
        e.preventDefault()
        first.focus()
      }
    }
    document.addEventListener('keydown', keydownListener)
  }

  const deactivate = () => {
    if (keydownListener) {
      document.removeEventListener('keydown', keydownListener)
      keydownListener = null
    }
    if (appEl) {
      appEl.removeAttribute('inert')
      appEl = null
    }
    // Restore focus to the trigger element if it still exists. The
    // isConnected check handles the case where the trigger was removed
    // from the DOM during the dialog's lifetime (rare, but possible
    // with v-if on the trigger).
    if (savedFocus && savedFocus.isConnected && typeof savedFocus.focus === 'function') {
      savedFocus.focus()
    }
    savedFocus = null
  }

  watch(isActiveRef, (active) => {
    if (active) {
      activate()
    } else {
      deactivate()
    }
  }, { immediate: false })
}
