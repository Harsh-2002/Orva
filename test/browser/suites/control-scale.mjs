// Every control lands on the size ladder.
//
// This is the check that was missing while the app grew to at least twenty
// distinct button heights on a fine pointer against Button.vue's four. The
// drift was invisible from source: a control's real height is padding plus a
// line box plus whatever a scoped rule did, resolved against a root scaled to
// 95%, and about forty controls declared no height at all and simply rendered
// at ink -- 15.2px for a text action, 13.3px for a label wrapping a checkbox.
// Only a render says so.
//
// Two ladders, because a phone is not a dense desktop toolbar. They map 1:1,
// and each is internally even; having two is fine, having two that do not
// correspond is not.
//
//   fine    text 22.8 | xs 26.6 | sm 30.4 | md 38.0 | lg 45.6
//   coarse             xs 32    | sm 36   | md 40   | row 44
//
// What is deliberately NOT a ladder item, and why the probe skips it:
//
//   An <a> inside running prose is a link. Giving it a box would set the line
//   height of the paragraph around it, which is also why WCAG 2.5.5 exempts an
//   inline target.
//
//   A full-bleed row or card whose entire body is the click target is sized by
//   its content -- a two-line invocation row is not a button that has gone
//   wrong.

export const meta = {
  id: 'control-scale',
  title: 'Controls land on the size ladder',
}

const LADDERS = {
  fine: [22.8, 26.6, 30.4, 38.0, 45.6],
  coarse: [32, 36, 40, 44],
}

// Each rung carries a type size, and Button.vue is the authority: xs/sm are
// text-xs, md is text-sm, lg is text-base. The ladder was pinned and the type
// was not, so bespoke classes drifted -- eight of them, from 10px to 12.5px,
// putting two sizes in one toolbar and three in one docs page. Height alone is
// not proportion.
// 22.8 is deliberately absent. The ladder above names it the "text" rung: a
// control that IS text -- a row link, a table-cell action -- and takes the type
// size of whatever it sits in, not a button's. The boxed rungs start at xs.
const RUNG_FONT = { 26.6: 11.4, 30.4: 11.4, 38.0: 13.3, 45.6: 15.2 }
// A tenth of a pixel is a rounding artefact; a whole one is a different token.
const FONT_TOL = 0.35

// Half a pixel is sub-pixel layout; a whole one is a decision. 1.2 leaves room
// for a border resolving differently without letting a real rung slip.
const TOL = 1.2

// Controls that are off the ladder on purpose. Each needs a reason, because an
// exemption without one is just a silenced failure.
const EXEMPT = [
  // A 36x20 role="switch" track. The geometry IS the switch.
  'rule-toggle',
  // The x inside a DNS chip whose own content box is 17.25px: the text rung
  // would inflate the chip around it. It reaches a real 44px on coarse.
  'dns-chip-x',
]

const PROBE = ({ ladder, tol, exempt, rungFont, fontTol }) => {
  const out = []
  const vis = (el) => {
    const s = getComputedStyle(el)
    if (s.display === 'none' || s.visibility === 'hidden') return false
    const r = el.getBoundingClientRect()
    return r.width > 1 && r.height > 1
  }
  const describe = (el) => {
    const cls = typeof el.className === 'string'
      ? el.className.trim().split(/\s+/).filter((c) => !/^(focus|hover|group|disabled)/.test(c)).slice(0, 4).join('.')
      : ''
    return `${el.tagName.toLowerCase()}${cls ? '.' + cls : ''}`.slice(0, 80)
  }

  const sel = 'button, a[href], [role="button"], input[type=submit], select'
  const seen = new Set()
  for (const el of document.querySelectorAll(sel)) {
    if (!vis(el)) continue
    if (exempt.some((c) => el.classList.contains(c))) continue
    // Inline prose link, not a control.
    if (el.tagName === 'A' && el.closest('p, li, td, .doc-prose, .doc-lede')) continue
    const r = el.getBoundingClientRect()
    if (r.height > 60) continue
    // A card or row body that happens to be a button. The giveaway is that it
    // CONTAINS layout rather than a label: stacked block-level children, sized
    // by them. A ladder item holds a label and maybe an icon.
    const cs = getComputedStyle(el)
    const container = [...el.children].some((c) => {
      const d = getComputedStyle(c).display
      return d === 'block' || d === 'flex' || d === 'grid'
    })
    // ...or its label simply runs to more than one line. Either way it is
    // sized by its content rather than by a rung.
    const lh = parseFloat(cs.lineHeight) || parseFloat(cs.fontSize) * 1.5
    // The width guard does not apply here: a label that runs to two lines is
    // content-sized whether it is 140px wide in a table cell or full-bleed in
    // a card. The ladder is a statement about single-line control heights.
    const multiline = lh > 0 && r.height > lh * 2.2
    if (multiline) continue
    if (container && r.width > 160) continue

    const near = ladder.reduce((a, b) => (Math.abs(b - r.height) < Math.abs(a - r.height) ? b : a))
    const delta = r.height - near

    // On the rung: the type has to match it too. Skipped for an icon-only
    // control, where font-size renders nothing and is not a proportion.
    if (Math.abs(delta) <= tol) {
      const want = rungFont[near]
      // Text that is actually painted, anywhere inside -- not just a direct
      // child, because most labels sit in a span, and not merely present,
      // because a label hidden at this width leaves an icon-only control.
      // Both halves were learned the hard way: the direct-child version
      // skipped six of the controls this rule exists for, and the
      // textContent version flagged a copy button whose label is
      // display:none above 640px.
      const painted = (n) => {
        if (n.classList.contains('sr-only')) return false
        const s2 = getComputedStyle(n)
        return s2.display !== 'none' && s2.visibility !== 'hidden'
      }
      const hasText = [el, ...el.querySelectorAll('*')].some((n) =>
        painted(n) && [...n.childNodes].some((c) => c.nodeType === 3 && c.textContent.trim()))
      if (want && hasText) {
        const fs = parseFloat(cs.fontSize)
        if (Math.abs(fs - want) > fontTol) {
          const key = `f|${describe(el)}|${fs}`
          if (!seen.has(key)) {
            seen.add(key)
            out.push(`${describe(el)} "${(el.textContent || '').trim().slice(0, 14)}" is ` +
              `${fs}px on the ${near}px rung, which takes ${want}px`)
          }
        }
      }
      continue
    }

    const key = `${describe(el)}|${r.height.toFixed(1)}`
    if (seen.has(key)) continue
    seen.add(key)
    out.push(`${describe(el)} "${(el.textContent || '').trim().slice(0, 14)}" is ` +
      `${r.height.toFixed(1)}px, ${delta > 0 ? '+' : ''}${delta.toFixed(1)} off the ${near}px rung`)
  }
  return out.slice(0, 12)
}

export async function onPage({ page, route, viewport, report }) {
  // One fine and one coarse viewport is the whole question: a rung is a rung
  // at every width, and running all seven would report the same rows seven
  // times.
  if (viewport.name !== 'laptop' && viewport.name !== 'phone') return

  const kind = viewport.touch ? 'coarse' : 'fine'
  const bad = await page.evaluate(PROBE, { ladder: LADDERS[kind], tol: TOL, exempt: EXEMPT,
    rungFont: kind === 'fine' ? RUNG_FONT : {}, fontTol: FONT_TOL })

  report.record('control-scale', `${viewport.name} ${route.name}`,
    `controls sit on the ${kind}-pointer ladder, and take its type size`, bad)
}
