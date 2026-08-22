// Layout geometry: does anything overflow, and does anything get clipped?
//
// This is the suite that cannot be replaced by reading source. `html, body {
// overflow-x: hidden }` is deliberate in this project (DESIGN.md section 7):
// the document never scrolls sideways. The cost is that a container which
// exceeds its width is CLIPPED silently, with no scrollbar and no visual cue.
// Nothing in the markup says "this got cut off"; the box has to be measured.
//
// Two real defects of exactly this shape shipped: the Traces status column was
// clipped between 768 and 832px because a six-track grid needed 771px and the
// card was overflow-hidden, and every Docs reference table clipped its
// right-hand columns on a phone for the same reason.

export const meta = {
  id: 'responsive',
  title: 'No horizontal overflow and no silently clipped content',
}

const PROBE = () => {
  const de = document.documentElement
  const out = { overflow: Math.max(0, de.scrollWidth - de.clientWidth), clipped: [], wide: [] }

  const describe = (el) => {
    const cls = typeof el.className === 'string'
      ? el.className.trim().split(/\s+/).slice(0, 5).join('.') : ''
    return `${el.tagName.toLowerCase()}${el.id ? '#' + el.id : ''}${cls ? '.' + cls : ''}`.slice(0, 110)
  }

  for (const el of document.querySelectorAll('*')) {
    const s = getComputedStyle(el)
    if (s.display === 'none' || s.visibility === 'hidden') continue
    const r = el.getBoundingClientRect()
    if (r.width <= 1 || r.height <= 1) continue

    // Content wider than its own box, in a container that cannot scroll.
    if (el.scrollWidth > el.clientWidth + 2 && el.clientWidth > 0) {
      const clips = s.overflowX === 'hidden' || s.overflowX === 'clip'
      // Deliberate single-line truncation shows an ellipsis and usually carries
      // a title; that is a design decision, not a clipped layout. Form controls
      // scroll their own value natively.
      const truncates = s.textOverflow === 'ellipsis'
      const isField = el.tagName === 'INPUT' || el.tagName === 'TEXTAREA'
      if (clips && !truncates && !isField) {
        out.clipped.push({ el: describe(el), hidden: el.scrollWidth - el.clientWidth })
      }
    }

    // An element extending past the viewport's right edge, with nothing able
    // to scroll it back into view.
    //
    // Extending past the edge is not itself a defect: the overflow contract
    // says a wide table or code block scrolls INSIDE its own bounds, so its
    // rows legitimately start off-screen and the operator scrolls the
    // container. What is a defect is the same geometry with no scrollable
    // ancestor, because the document will not scroll either. Without this
    // distinction the check reports every house-pattern table and every line
    // of CodeMirror.
    if (r.right > de.clientWidth + 1 && r.width < de.clientWidth * 3) {
      let reachable = false
      for (let n = el.parentElement; n && n !== document.body; n = n.parentElement) {
        const os = getComputedStyle(n).overflowX
        if ((os === 'auto' || os === 'scroll') && n.scrollWidth > n.clientWidth + 2) {
          reachable = true
          break
        }
      }
      if (!reachable) out.wide.push({ el: describe(el), over: Math.round(r.right - de.clientWidth) })
    }
  }

  const uniq = (a) => [...new Map(a.map((x) => [x.el, x])).values()]
  out.clipped = uniq(out.clipped).slice(0, 8)
  out.wide = uniq(out.wide).slice(0, 8)
  return out
}

export async function onPage({ page, route, viewport, report }) {
  const where = `${viewport.name} ${route.name}`
  const r = await page.evaluate(PROBE)

  report.record('responsive', where, 'document does not scroll horizontally',
    r.overflow > 0 ? [`document is ${r.overflow}px wider than the viewport`] : [])

  report.record('responsive', where, 'no content clipped without a scroll affordance',
    r.clipped.map((c) => `${c.el} hides ${c.hidden}px behind overflow:hidden`))

  report.record('responsive', where, 'nothing extends past the viewport edge',
    r.wide.map((w) => `${w.el} ends ${w.over}px past the right edge`))
}
