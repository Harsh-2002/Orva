// The 44px floor on coarse pointers.
//
// PRODUCT.md commits to a 44 by 44 effective target on mobile and DESIGN.md
// lists undersized controls as a named Don't. Neither can be checked from
// source: a control's real box is padding plus line-height plus whatever a
// scoped rule did, resolved against a root scaled to 95%, inside a
// `@media (pointer: coarse)` block. `h-10` reads as 40px and renders as 38.
//
// Two subtleties this encodes, both of which produced wrong answers when they
// were left implicit:
//
//   Tablets are coarse pointers. Checking only below 640px silently exempts
//   every tablet, which is where a first pass on this project stopped and why
//   six undersized controls survived it.
//
//   A control wrapped in a padded label inherits the label's target. A native
//   checkbox glyph cannot be resized without breaking its rendering, so the
//   convention is to wrap it; measuring the input alone reports a false
//   failure, and exempting checkboxes outright hides the real ones.

export const meta = {
  id: 'touch-targets',
  title: 'Interactive controls meet 44x44 on coarse pointers',
}

const FLOOR = 43.5 // half a pixel of slack for sub-pixel layout

const PROBE = (floor) => {
  if (!matchMedia('(pointer: coarse)').matches) return { skipped: true, small: [] }

  const describe = (el) => {
    const cls = typeof el.className === 'string'
      ? el.className.trim().split(/\s+/).slice(0, 4).join('.') : ''
    return `${el.tagName.toLowerCase()}${el.id ? '#' + el.id : ''}${cls ? '.' + cls : ''}`.slice(0, 100)
  }

  const small = []
  const sel = 'button, a[href], input, select, textarea, [role="button"], [role="tab"], [role="menuitem"]'
  for (const el of document.querySelectorAll(sel)) {
    const s = getComputedStyle(el)
    if (s.display === 'none' || s.visibility === 'hidden') continue
    const r = el.getBoundingClientRect()
    if (r.width <= 1 && r.height <= 1) continue // visually hidden, e.g. sr-only

    // WCAG 2.5.5 exempts a control inline in a sentence, where enlarging it
    // would set the line height of running prose.
    if (s.display === 'inline' && el.closest('p, span, li, td')) continue

    // A wrapping label is the real target for the control inside it.
    const label = el.closest('label')
    const box = label ? label.getBoundingClientRect() : r

    if (box.height < floor || box.width < floor) {
      small.push({
        el: describe(el),
        w: Math.round(box.width),
        h: Math.round(box.height),
        viaLabel: !!label,
      })
    }
  }
  const uniq = [...new Map(small.map((x) => [x.el, x])).values()]
  return { skipped: false, small: uniq.slice(0, 20) }
}

export async function onPage({ page, route, viewport, report }) {
  const where = `${viewport.name} ${route.name}`
  if (!viewport.touch) return

  const r = await page.evaluate(PROBE, FLOOR)
  if (r.skipped) {
    return report.skip('touch-targets', where, 'controls meet the 44px floor',
      'the page did not match (pointer: coarse)')
  }

  report.record('touch-targets', where, 'controls meet the 44px floor',
    r.small.map((t) => `${t.el} is ${t.w}x${t.h}${t.viaLabel ? ' (measured on its wrapping label)' : ''}`))
}
