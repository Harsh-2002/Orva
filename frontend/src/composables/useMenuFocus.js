import { watch } from 'vue'

// Anything inside a menu panel that can hold keyboard focus. Narrower than
// useFocusTrap's list on purpose: a Popover panel only ever holds buttons, the
// model filter box and the manual-model form.
const FOCUSABLE = 'button:not([disabled]), input:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])'

// Popover renders a Drawer below this width (it mirrors Popover's own check).
// The Drawer already focuses itself, traps Tab and closes on Escape, so a
// second trap inside it would cut its Close button out of the cycle.
const isSheet = () => window.matchMedia('(max-width: 639px)').matches

/**
 * useMenuFocus — keyboard reachability for a menu rendered inside <Popover>.
 *
 * Popover teleports its panel to <body>, so the panel is the last thing in the
 * document: Tab from the trigger walked the whole page before it reached the
 * menu, and the panel's own `@keydown.esc` never fired because nothing inside
 * it ever held focus. Both made the model and reasoning pickers unusable
 * without a mouse. So the menu moves focus itself.
 *
 * @param {Ref<HTMLElement|null>} panelRef — the menu body inside the panel.
 * @param {Ref<HTMLElement|null>} triggerRef — the button that opens the menu.
 * @returns {{onMenuKeydown: (e: KeyboardEvent, close: () => void) => void}}
 */
export function useMenuFocus(panelRef, triggerRef) {
  watch(panelRef, (el, prev) => {
    if (isSheet()) return
    if (el) {
      el.querySelector(FOCUSABLE)?.focus()
    } else if (prev && (prev.contains(document.activeElement) || document.activeElement === document.body)) {
      // Hand focus back only when the menu still held it. `prev` is checked
      // first because Popover fades the panel out: the ref is nulled at once
      // but the focused item lingers in the DOM for the leave transition, so
      // <body> is not yet the active element. A click on some other control
      // has already moved focus and must keep it.
      triggerRef.value?.focus()
    }
  })

  function onMenuKeydown(e, close) {
    if (e.key === 'Escape') {
      close()
      return
    }
    if (e.key !== 'Tab' || isSheet()) return
    const items = Array.from(panelRef.value?.querySelectorAll(FOCUSABLE) || [])
    if (!items.length) return
    const first = items[0]
    const last = items[items.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }

  return { onMenuKeydown }
}
