// WCAG 2.1 AA checks that need a rendered page.
//
// PRODUCT.md commits to AA across the dashboard. Some of that is checkable from
// source and is already guarded by frontend/test/responsive.test.js. The rest
// needs the DOM as the browser actually assembled it: an accessible name can
// come from a label, aria-label, aria-labelledby, a title, or wrapping text,
// and only the rendered tree knows which of those resolved.
//
// The keyboard-operability check is the one that has caught the most here. A
// @click on a bare <li> or <tr> looks fine, works with a mouse, and is
// completely unreachable by keyboard: a Level A failure that shipped on four
// separate list views.

export const meta = {
  id: 'accessibility',
  title: 'WCAG 2.1 AA checks that require a rendered page',
}

const PROBE = () => {
  const out = {
    unnamed: [], clickableNonInteractive: [], headings: [],
    dialogs: [], contrast: [], duplicateIds: [],
  }

  const describe = (el) => {
    const cls = typeof el.className === 'string'
      ? el.className.trim().split(/\s+/).slice(0, 4).join('.') : ''
    return `${el.tagName.toLowerCase()}${el.id ? '#' + el.id : ''}${cls ? '.' + cls : ''}`.slice(0, 100)
  }
  const visible = (el) => {
    const s = getComputedStyle(el)
    if (s.display === 'none' || s.visibility === 'hidden') return false
    const r = el.getBoundingClientRect()
    return r.width > 1 && r.height > 1
  }

  // Accessible name, resolved the way a screen reader would: the attribute
  // chain first, then a wrapping or associated <label>, then visible content.
  // Names come from textContent, not innerText. innerText is layout-aware and
  // returns "" for anything inside a collapsed section, which reported the AI
  // settings panel's labelled <select> and its "Save provider" button as having
  // no accessible name at all.
  const accessibleName = (el) => {
    const aria = el.getAttribute('aria-label')
    if (aria && aria.trim()) return aria.trim()
    const by = el.getAttribute('aria-labelledby')
    if (by) {
      const txt = by.split(/\s+/).map((id) => document.getElementById(id)?.textContent || '').join(' ').trim()
      if (txt) return txt
    }
    // A <label> names its control by its own *text alternative*, not by its
    // text content, so an aria-label on the label counts and wins over the
    // text inside it. Missing that reported every card-view row checkbox on
    // /invocations as unnamed: the control is a bare box, the wrapping label
    // carries `aria-label="Select invocation <id>"`, and Chrome's own AX tree
    // resolves exactly that string.
    const labelName = (lab) => {
      if (!lab) return ''
      const a = lab.getAttribute('aria-label')
      return (a && a.trim()) || lab.textContent.trim()
    }
    if (el.id) {
      const named = labelName(document.querySelector(`label[for="${CSS.escape(el.id)}"]`))
      if (named) return named
    }
    const wrapping = labelName(el.closest('label'))
    if (wrapping) return wrapping
    // placeholder is a real name source for text inputs per HTML-AAM, ranked
    // below label and above title. It is weak labelling -- it disappears the
    // moment the field has a value -- but "weak name" is a different finding
    // from "no name", and conflating them buries the fields that truly have
    // nothing.
    const placeholder = el.getAttribute('placeholder')
    if (placeholder && placeholder.trim()) return placeholder.trim()
    const title = el.getAttribute('title')
    if (title && title.trim()) return title.trim()
    if (el.tagName === 'INPUT' && ['submit', 'button', 'reset'].includes(el.type)) {
      if (el.value && el.value.trim()) return el.value.trim()
    }
    return (el.textContent || '').trim()
  }

  // 4.1.2 Name, Role, Value / 1.3.1 Info and Relationships.
  for (const el of document.querySelectorAll('input, select, textarea, button, a[href], [role="button"]')) {
    if (!visible(el)) continue
    if (el.tagName === 'INPUT' && el.type === 'hidden') continue
    if (!accessibleName(el)) out.unnamed.push(describe(el))
  }

  // 2.1.1 Keyboard. A click handler on a non-interactive element with no
  // tabindex and no role cannot be reached or activated without a pointer.
  // Vue binds via addEventListener, so the attribute is not visible here; the
  // cursor:pointer style plus a non-interactive tag is the reliable signal.
  const FOCUSABLE = 'button, a[href], [role="button"], input, select, textarea, [tabindex]'
  for (const el of document.querySelectorAll('li, tr, td, div, span')) {
    if (!visible(el)) continue
    if (getComputedStyle(el).cursor !== 'pointer') continue
    if (el.closest('button, a[href], label, [role="button"]')) continue
    // CodeMirror renders its own spans and manages its own keyboard model --
    // the editor is reachable and operable via its textarea, not via the
    // decorated spans this test would otherwise flag.
    if (el.closest('.cm-editor')) continue
    if (el.querySelector(FOCUSABLE)) continue
    if (el.hasAttribute('tabindex') || el.hasAttribute('role')) continue

    // cursor:pointer inherits, so a clickable <tr> makes every one of its <td>s
    // match this test too. Only the element that actually owns the handler is
    // worth reporting -- and if that element already contains a real control,
    // the row IS keyboard reachable and there is nothing to report at all.
    // Without this the Activity table, which deliberately pairs a row-level
    // @click with a focusable button in its first cell, was flagged six times
    // for a defect it had already solved.
    let owner = el
    for (let p = el.parentElement; p && p !== document.body; p = p.parentElement) {
      if (getComputedStyle(p).cursor === 'pointer') owner = p
    }
    if (owner !== el) continue
    if (owner.querySelector(FOCUSABLE)) continue

    out.clickableNonInteractive.push(describe(el))
  }

  // 1.3.1 heading structure.
  const hs = [...document.querySelectorAll('h1,h2,h3,h4,h5,h6')].filter(visible)
  const levels = hs.map((h) => Number(h.tagName[1]))
  const h1s = [...document.querySelectorAll('h1')].length
  if (h1s === 0) out.headings.push('no <h1> on the page')
  if (h1s > 1) out.headings.push(`${h1s} <h1> elements; a page should have one`)
  for (let i = 1; i < levels.length; i++) {
    if (levels[i] - levels[i - 1] > 1) {
      out.headings.push(`heading jumps from h${levels[i - 1]} to h${levels[i]} (${hs[i].innerText.slice(0, 30)})`)
      break
    }
  }

  // 4.1.2 for dialogs: an open dialog must announce itself and have a name.
  for (const el of document.querySelectorAll('[role="dialog"], dialog[open]')) {
    if (!visible(el)) continue
    if (!accessibleName(el)) out.dialogs.push(`${describe(el)} has role=dialog but no accessible name`)
    if (el.getAttribute('aria-modal') !== 'true') {
      out.dialogs.push(`${describe(el)} is a dialog without aria-modal="true"`)
    }
  }

  // 1.4.3 Contrast (minimum), for text against its resolved background.
  const lum = (c) => {
    const [r, g, b] = c.map((v) => {
      const s = v / 255
      return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4)
    })
    return 0.2126 * r + 0.7152 * g + 0.0722 * b
  }
  // Colour arrives in whatever form the engine computed, and that is not always
  // rgb(). Tailwind v4 compiles every `/NN` alpha to color-mix(in oklab, ...),
  // which Chrome serialises as oklab(). A parser that understands only rgb()
  // returns null for those and the caller skips the element -- so the check
  // silently exempted exactly the faded text it exists to catch. Two of the
  // theme's real AA failures were invisible to this suite for that reason.
  const enc = (v) => {
    const s = v <= 0.0031308 ? 12.92 * v : 1.055 * Math.pow(v, 1 / 2.4) - 0.055
    return Math.max(0, Math.min(255, s * 255))
  }
  const fromOklab = (L, a, b) => {
    const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3
    const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3
    const s = (L - 0.0894841775 * a - 1.2914855480 * b) ** 3
    return [
      enc(4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s),
      enc(-1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s),
      enc(-0.0041960863 * l - 0.7034186147 * m + 1.7076147010 * s),
    ]
  }
  const parse = (str) => {
    if (!str) return null
    const s = str.trim()
    if (s === 'transparent') return { rgb: [0, 0, 0], a: 0 }
    const fn = /^([a-z-]+)\((.*)\)$/i.exec(s)
    if (!fn) return null
    const kind = fn[1].toLowerCase()
    const [head, tailRaw] = fn[2].split('/')
    const pct = (x) => (String(x).trim().endsWith('%') ? parseFloat(x) / 100 : parseFloat(x))
    const v = (head.match(/-?[\d.]+%?/g) || []).map(pct)
    const a = tailRaw === undefined ? 1 : pct(tailRaw)
    if (kind === 'rgb' || kind === 'rgba') {
      return { rgb: [v[0], v[1], v[2]], a: v.length > 3 ? v[3] : a }
    }
    if (kind === 'oklab') return { rgb: fromOklab(v[0], v[1], v[2]), a }
    if (kind === 'oklch') {
      const h = ((v[2] || 0) * Math.PI) / 180
      return { rgb: fromOklab(v[0], (v[1] || 0) * Math.cos(h), (v[1] || 0) * Math.sin(h)), a }
    }
    if (kind === 'color') {
      // color(srgb r g b [/ a]) -- drop the colourspace keyword, keep the three.
      const c = (head.replace(/^\s*[a-z0-9-]+/i, '').match(/-?[\d.]+/g) || []).map(Number)
      return { rgb: c.slice(0, 3).map((x) => Math.max(0, Math.min(1, x)) * 255), a }
    }
    return null
  }

  // What the page paints when nothing above an element is opaque. Reading it
  // from the document rather than hardcoding one theme's near-black: the same
  // constant cannot be right for a canvas that is #0E0D09 at night and #F8F7F4
  // by day, and being wrong here inverts every ratio computed through it.
  const CANVAS = (() => {
    for (const n of [document.body, document.documentElement]) {
      const c = n && parse(getComputedStyle(n).backgroundColor)
      if (c && c.a >= 0.999) return c.rgb
    }
    return [255, 255, 255]
  })()

  // Walk up for the first opaque background, compositing any translucent
  // layers on the way, which is what the eye actually sees.
  const bgOf = (el) => {
    const stack = []
    for (let n = el; n && n !== document.documentElement.parentNode; n = n.parentElement) {
      const c = parse(getComputedStyle(n).backgroundColor)
      if (c && c.a > 0) { stack.push(c); if (c.a >= 0.999) break }
    }
    let out = stack.length && stack[stack.length - 1].a >= 0.999 ? stack.pop().rgb : CANVAS
    for (let i = stack.length - 1; i >= 0; i--) {
      const { rgb, a } = stack[i]
      out = out.map((v, k) => rgb[k] * a + v * (1 - a))
    }
    return out
  }
  const ratio = (a, b) => {
    const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p)
    return (x + 0.05) / (y + 0.05)
  }

  const seen = new Set()
  for (const el of document.querySelectorAll('p, span, a, button, label, td, th, li, h1, h2, h3, div')) {
    if (!visible(el)) continue
    const own = [...el.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim().length > 2)
    if (!own) continue
    const s = getComputedStyle(el)
    const fg = parse(s.color)
    if (!fg) continue
    const size = parseFloat(s.fontSize)
    const bold = Number(s.fontWeight) >= 700
    // AA: 3:1 for large text (>=24px, or >=18.66px bold), else 4.5:1.
    const large = size >= 24 || (bold && size >= 18.66)
    const need = large ? 3 : 4.5
    // Translucent text is what the eye sees composited, not what the token
    // says. `text-foreground-muted/40` is a real pattern here and measuring it
    // at full strength reports a ratio nobody can read.
    const bg = bgOf(el)
    const seenFg = fg.a >= 0.999 ? fg.rgb : fg.rgb.map((v, k) => v * fg.a + bg[k] * (1 - fg.a))
    const got = ratio(seenFg, bg)
    if (got + 0.05 < need) {
      const key = `${describe(el)}|${s.color}`
      if (seen.has(key)) continue
      seen.add(key)
      out.contrast.push(
        `${describe(el)} "${(el.innerText || '').trim().slice(0, 24)}" is ${got.toFixed(2)}:1, needs ${need}:1 at ${size.toFixed(0)}px`,
      )
    }
  }

  // 4.1.1: duplicate ids break every aria reference that points at one.
  const ids = {}
  for (const el of document.querySelectorAll('[id]')) ids[el.id] = (ids[el.id] || 0) + 1
  for (const [id, n] of Object.entries(ids)) if (n > 1) out.duplicateIds.push(`id="${id}" appears ${n} times`)

  const cap = (a, n) => [...new Set(a)].slice(0, n)
  out.unnamed = cap(out.unnamed, 10)
  out.clickableNonInteractive = cap(out.clickableNonInteractive, 8)
  out.contrast = cap(out.contrast, 10)
  out.duplicateIds = cap(out.duplicateIds, 5)
  out.dialogs = cap(out.dialogs, 5)
  return out
}

export async function onPage({ page, route, viewport, report }) {
  // Most of these are viewport-independent, but the page is not: below sm the
  // list views render an entirely different subtree (cards, not a table) and
  // the sidebar becomes a drawer. Running only on laptop never measured any of
  // it. Two viewports cover both branches without paying for all seven.
  if (viewport.name !== 'laptop' && viewport.name !== 'phone') return
  const where = `${viewport.name} ${route.name}`
  const r = await page.evaluate(PROBE)

  report.record('accessibility', where, 'interactive elements have an accessible name (4.1.2)',
    r.unnamed.map((e) => `${e} has no name from a label, aria-label, title or text`))

  report.record('accessibility', where, 'no click handlers on non-interactive elements (2.1.1)',
    r.clickableNonInteractive.map((e) => `${e} looks clickable but is not focusable`))

  report.record('accessibility', where, 'heading structure is sound (1.3.1)', r.headings)

  report.record('accessibility', where, 'dialogs announce themselves (4.1.2)', r.dialogs)

  report.record('accessibility', where, 'text meets AA contrast (1.4.3)', r.contrast)

  report.record('accessibility', where, 'element ids are unique (4.1.1)', r.duplicateIds)
}
