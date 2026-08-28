// Controls agree with their neighbours.
//
// Every other suite measures a control against a standard. That is how the app
// reached 40 off-system controls, 9 radius values and a `Refresh` drawn at three
// heights with three icon sizes across six views while 2520 assertions passed:
// `Function` at 26.6px passed, `STATUS` at 26.6px passed, and the fact that one
// was 10px uppercase beside the other at 11.4px sentence-case was invisible to
// both. A standard cannot see a pair. These three checks are relational.

export const meta = {
  id: 'consistency',
  title: 'Controls agree with their neighbours',
}

// Populations, not per-control rules: the point is that the *set* stays small.
// A tenth radius is drift even when each one is individually defensible.
const RADII = [0, 5.7, 7.6, 'pill']
const ICONS = [11.4, 13.3, 15.2, 17.1, 19]
const TOL = 1.2

// A row is allowed to mix these with anything: a 36x20 switch whose geometry is
// the switch, and the x inside a DNS chip, sized by the chip around it.
const ROW_EXEMPT = ['rule-toggle', 'dns-chip-x']

// The same word, drawn deliberately differently in two places.
const LABEL_EXEMPT = [
  // The composer's model button is a full control when the rail is open and a
  // bare glyph when it is collapsed. Same label, two roles.
  'no model',
]

const PROBE = ({ radii, icons, tol, rowExempt, labelExempt }) => {
  const vis = (el) => {
    const s = getComputedStyle(el)
    if (s.display === 'none' || s.visibility === 'hidden' || s.opacity === '0') return false
    const r = el.getBoundingClientRect()
    return r.width > 1 && r.height > 1
  }
  const describe = (el) => {
    const cls = typeof el.className === 'string'
      ? el.className.trim().split(/\s+/).filter((c) => !/^(focus|hover|group|disabled|dark|sm:|md:|lg:)/.test(c)).slice(0, 3).join('.')
      : ''
    return `${el.tagName.toLowerCase()}${cls ? '.' + cls : ''}`.slice(0, 60)
  }
  // The label a human reads, not the wrapper's textContent: a control whose text
  // sits three spans deep still says one word.
  const labelOf = (el) => (el.getAttribute('aria-label') || el.textContent || '')
    .replace(/\s+/g, ' ').trim().toLowerCase().slice(0, 32)
  const radiusOf = (cs) => {
    const v = parseFloat(cs.borderTopLeftRadius) || 0
    return v > 100 ? 'pill' : v
  }
  const near = (v, set) => set.some((s) => s === v || (typeof s === 'number' && Math.abs(s - v) <= tol))

  const sel = 'button, a[href], [role="button"], input[type=submit]'
  const controls = []
  for (const el of document.querySelectorAll(sel)) {
    if (!vis(el)) continue
    if (rowExempt.some((c) => el.classList.contains(c))) continue
    if (el.tagName === 'A' && el.closest('p, li, td, .doc-prose, .doc-lede')) continue
    const r = el.getBoundingClientRect()
    if (r.height > 60) continue
    const cs = getComputedStyle(el)
    const lh = parseFloat(cs.lineHeight) || parseFloat(cs.fontSize) * 1.5
    if (lh > 0 && r.height > lh * 2.2) continue
    const container = [...el.children].some((c) => {
      const d = getComputedStyle(c).display
      return d === 'block' || d === 'flex' || d === 'grid'
    })
    if (container && r.width > 160) continue
    // Own text, not a descendant's: what decides whether font-size is comparable.
    const own = [...el.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim())
      || [...el.querySelectorAll('span')].some((s) =>
        [...s.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim()))
    const svg = el.querySelector('svg')
    controls.push({
      el, r, cs,
      h: r.height,
      fs: parseFloat(cs.fontSize),
      tt: cs.textTransform,
      rad: radiusOf(cs),
      icon: svg ? Math.round(svg.getBoundingClientRect().width * 10) / 10 : 0,
      label: labelOf(el),
      texted: own,
      desc: describe(el),
    })
  }

  const rows = []
  const pop = []
  const labels = []

  // 1. Siblings in a row. "Row" is the nearest ancestor holding more than one
  //    control, because a filter strip's triggers are cousins -- each sits in
  //    its own Popover root -- not siblings.
  //
  //    The walk stops at a table cell. Columns of a data row are different type
  //    roles by design -- a mono timestamp, a medium function name, a ghost
  //    action -- and they only share a line because the row centres them.
  const bands = new Map()
  for (const c of controls) {
    const cell = c.el.closest('td, th')
    let a = c.el.parentElement
    for (let i = 0; a && i < 6; i++) {
      if (cell && !cell.contains(a)) { a = null; break }
      if (controls.filter((x) => x !== c && a.contains(x.el)).length) break
      a = a.parentElement
    }
    if (!a) continue
    const key = `${a.dataset.rowKey || (a.dataset.rowKey = String(Math.random()))}|${Math.round((c.r.top + c.r.height / 2) / 4)}`
    if (!bands.has(key)) bands.set(key, [])
    bands.get(key).push(c)
  }
  for (const group of bands.values()) {
    if (group.length < 2) continue
    const spread = (vals) => Math.max(...vals) - Math.min(...vals)
    const hs = group.map((c) => c.h)
    if (spread(hs) > tol) {
      rows.push(`row [${group.map((c) => `"${c.label.slice(0, 12)}" ${c.h.toFixed(1)}`).join(' | ')}] disagree on height`)
      continue
    }
    const texted = group.filter((c) => c.texted)
    if (texted.length > 1) {
      if (spread(texted.map((c) => c.fs)) > 0.6) {
        rows.push(`row [${texted.map((c) => `"${c.label.slice(0, 12)}" ${c.fs.toFixed(1)}px`).join(' | ')}] disagree on label size`)
        continue
      }
      if (new Set(texted.map((c) => c.tt)).size > 1) {
        rows.push(`row [${texted.map((c) => `"${c.label.slice(0, 12)}" ${c.tt}`).join(' | ')}] disagree on case`)
        continue
      }
    }
    const rads = [...new Set(group.map((c) => String(c.rad)))]
    if (rads.length > 1) {
      rows.push(`row [${group.map((c) => `"${c.label.slice(0, 12)}" ${c.rad}`).join(' | ')}] disagree on radius`)
    }
  }

  // 2. Populations stay inside the declared sets.
  const offRad = new Map()
  const offIco = new Map()
  for (const c of controls) {
    if (!near(c.rad, radii)) offRad.set(`${c.desc} ${c.rad}`, c.label)
    if (c.icon && !near(c.icon, icons)) offIco.set(`${c.desc} ${c.icon}px`, c.label)
  }
  for (const [k, v] of offRad) pop.push(`radius outside the set: ${k} "${v.slice(0, 12)}"`)
  for (const [k, v] of offIco) pop.push(`icon size outside the set: ${k} "${v.slice(0, 12)}"`)

  // 3. Handed back to the runner: the same label in another view is a separate
  //    page, so the comparison cannot happen here.
  for (const c of controls) {
    if (!c.label || c.label.length < 3) continue
    if (labelExempt.includes(c.label)) continue
    labels.push({ label: c.label, h: c.h, fs: c.fs, icon: c.icon, desc: c.desc })
  }

  return { rows: rows.slice(0, 8), pop: pop.slice(0, 8), labels }
}

// theme|viewport|label -> first sighting, so a second one can be compared to it.
const sightings = new Map()

export async function onPage({ page, route, viewport, theme, report }) {
  if (viewport.name !== 'laptop' && viewport.name !== 'phone') return

  const { rows, pop, labels } = await page.evaluate(PROBE, {
    radii: RADII, icons: ICONS, tol: TOL, rowExempt: ROW_EXEMPT, labelExempt: LABEL_EXEMPT,
  })

  const where = `${viewport.name} ${route.name}`
  report.record('consistency', where, 'controls in a row agree', rows)
  report.record('consistency', where, 'radius and icon sizes stay in the declared sets', pop)

  for (const l of labels) {
    const key = `${theme}|${viewport.name}|${l.label}`
    const first = sightings.get(key)
    if (!first) { sightings.set(key, { ...l, route: route.name }); continue }
    if (first.route === route.name) continue
    if (first.drift) continue
    const dh = Math.abs(first.h - l.h) > TOL
    const df = Math.abs(first.fs - l.fs) > 0.6
    const di = first.icon !== l.icon && Math.abs(first.icon - l.icon) > TOL
    if (dh || df || di) {
      first.drift = `"${l.label}" is ${l.h.toFixed(1)}px/${l.fs.toFixed(1)}px/icon ${l.icon} on ${route.name}` +
        ` but ${first.h.toFixed(1)}px/${first.fs.toFixed(1)}px/icon ${first.icon} on ${first.route}`
      first.theme = theme
      first.viewport = viewport.name
    }
  }
}

// Cross-route drift can only be judged once every route has been seen.
export async function afterViewport({ viewport, theme, report }) {
  if (viewport.name !== 'laptop' && viewport.name !== 'phone') return
  const drift = []
  for (const [key, v] of sightings) {
    if (!key.startsWith(`${theme}|${viewport.name}|`)) continue
    if (v.drift) drift.push(v.drift)
  }
  report.record('consistency', viewport.name, 'the same label is drawn the same way in every view', drift.slice(0, 10))
}
