// Whole-row navigation for list views: the row (or card) opens its detail page,
// as the owner expects, while the name inside it stays a real <router-link> so
// keyboard and screen-reader users have a focusable target (a clickable <tr>
// alone is invisible to them; see test/browser/CLAUDE.md). The row handler is
// for the mouse and must never fight the controls inside the row.

// Anything inside the row that does its own thing, or that holds text you may
// want to select (ids): a click that starts there is not a row click.
const OWN_CLICK = 'a, button, input, select, textarea, label, summary, [role="button"], [role="menu"], [data-no-row]'

// rowClickTarget decides whether a click on a row should navigate. `selection`
// is window.getSelection()?.toString(): dragging across an id to copy it ends in
// a click on the row and must not navigate away.
export function shouldNavigateOnRowClick(event, selection = '') {
  if (event.defaultPrevented) return false
  if (event.button !== undefined && event.button !== 0) return false
  // composedPath, not target.closest: clicking a control can re-render it (the
  // copy button swaps its icon) before the event bubbles to the row, leaving
  // event.target detached and closest() blind to the button it was inside. The
  // path is fixed when the event is dispatched.
  const path = event.composedPath?.() ?? []
  const inControl = path.length
    ? path.some((el) => el.matches?.(OWN_CLICK))
    : !!event.target?.closest?.(OWN_CLICK)
  if (inControl) return false
  if (selection) return false
  return true
}

// rowNav returns the @click handler for a row. Cmd/Ctrl-click opens a new tab,
// like the link in the same row would.
export function rowNav(router, to) {
  return (event) => {
    const selection = typeof window !== 'undefined' ? window.getSelection?.()?.toString() : ''
    if (!shouldNavigateOnRowClick(event, selection)) return
    if (event.metaKey || event.ctrlKey) {
      window.open(router.resolve(to).href, '_blank', 'noopener')
      return
    }
    router.push(to)
  }
}

// rowAction is rowNav for rows whose destination is not a route (an edit modal):
// it runs `action` under the same rules.
export function rowAction(action) {
  return (event) => {
    const selection = typeof window !== 'undefined' ? window.getSelection?.()?.toString() : ''
    if (shouldNavigateOnRowClick(event, selection)) action()
  }
}
