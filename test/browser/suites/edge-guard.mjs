// Horizontal strips do not rubber-band past their ends.
//
// This exists because the defect was reported from a phone three times and
// "verified fixed" twice against a headless Chromium that cannot reproduce it.
// Chromium clamps scrollLeft at its maximum whatever the stylesheet says, so
// measuring the scroll position proves nothing: the strip is not moving, the
// engine is painting an elastic overscroll on top of it.
//
// What IS observable on every engine is whether the touchmove was cancelled.
// `utils/edgeGuard.js` calls preventDefault when a horizontal gesture pushes a
// scroller that is already at that end, and a cancelled event is a fact any
// browser will report. So the assertion is about the guard being installed and
// firing at the ends and NOT firing in the middle -- not about pixels.

export const meta = {
  id: 'edge-guard',
  title: 'Horizontal strips are clamped at their ends',
}

// Routes that carry a scrolling filter strip.
const STRIPS = ['activity', 'jobs', 'traces']

export async function onPage({ page, route, viewport, report }) {
  if (!viewport.touch) return
  if (!STRIPS.includes(route.name)) return
  const where = `${viewport.name} ${route.name}`

  const found = await page.evaluate(() => {
    // The filter strip specifically, and only where it is genuinely a scroll
    // container. From sm up these strips are `sm:overflow-visible` and wrap
    // instead of scrolling, so scrollWidth still exceeds clientWidth while
    // nothing scrolls -- picking by overflow alone found a wrapped strip at
    // tablet width and asserted end-clamping on something with no ends.
    const el = [...document.querySelectorAll('.swipe-x')].find((n) => {
      const ox = getComputedStyle(n).overflowX
      return (ox === 'auto' || ox === 'scroll') && n.scrollWidth - n.clientWidth > 1
    })
    if (!el) return null
    const r = el.getBoundingClientRect()
    return { x: r.x + Math.min(60, r.width / 4), y: r.y + r.height / 2, width: r.width }
  })
  // A strip that does not overflow at this width has no ends to defend.
  if (!found) return report.skip('edge-guard', where, 'strip is clamped at both ends',
    'the strip wraps rather than scrolls at this width')

  // Instrument: record whether each touchmove came back cancelled.
  await page.evaluate(() => {
    window.__egCancelled = []
    document.addEventListener('touchmove', (e) => {
      window.__egCancelled.push(e.defaultPrevented)
    }, { passive: true })
  })

  const cdp = await page.context().newCDPSession(page)
  const drag = async (from, to, steps = 5) => {
    await cdp.send('Input.dispatchTouchEvent', {
      type: 'touchStart', touchPoints: [{ x: from, y: found.y }],
    })
    for (let i = 1; i <= steps; i++) {
      const x = from + ((to - from) * i) / steps
      await cdp.send('Input.dispatchTouchEvent', {
        type: 'touchMove', touchPoints: [{ x, y: found.y }],
      })
      await page.waitForTimeout(30)
    }
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
    await page.waitForTimeout(60)
  }

  const failures = []

  // 1. At the start, dragging further right must be cancelled.
  await page.evaluate(() => {
    const el = [...document.querySelectorAll('.swipe-x')].find((n) => {
      const ox = getComputedStyle(n).overflowX
      return (ox === 'auto' || ox === 'scroll') && n.scrollWidth - n.clientWidth > 1
    })
    el.scrollLeft = 0
    window.__egCancelled = []
  })
  await drag(found.x, found.x + 140)
  const atStart = await page.evaluate(() => window.__egCancelled.some(Boolean))
  if (!atStart) {
    failures.push('dragging right at scrollLeft 0 was not cancelled: the strip can be ' +
      'pulled past its first chip')
  }

  // 2. In the middle, an ordinary scroll must NOT be cancelled -- a guard that
  //    swallows every move has broken scrolling rather than fixed bouncing.
  await page.evaluate(() => {
    const el = [...document.querySelectorAll('.swipe-x')].find((n) => {
      const ox = getComputedStyle(n).overflowX
      return (ox === 'auto' || ox === 'scroll') && n.scrollWidth - n.clientWidth > 1
    })
    el.scrollLeft = Math.floor((el.scrollWidth - el.clientWidth) / 2)
    window.__egCancelled = []
  })
  await drag(found.x + 120, found.x + 40)
  const midCancelled = await page.evaluate(() => window.__egCancelled.some(Boolean))
  if (midCancelled) {
    failures.push('a mid-range horizontal drag was cancelled: the strip cannot be scrolled')
  }

  // 3. A vertical gesture starting on the strip must never be cancelled, or the
  //    page cannot be scrolled by anyone whose thumb lands on a filter row.
  await page.evaluate(() => { window.__egCancelled = [] })
  await cdp.send('Input.dispatchTouchEvent', {
    type: 'touchStart', touchPoints: [{ x: found.x, y: found.y }],
  })
  for (let i = 1; i <= 5; i++) {
    await cdp.send('Input.dispatchTouchEvent', {
      type: 'touchMove', touchPoints: [{ x: found.x, y: found.y - i * 24 }],
    })
    await page.waitForTimeout(30)
  }
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] })
  const vertCancelled = await page.evaluate(() => window.__egCancelled.some(Boolean))
  if (vertCancelled) {
    failures.push('a vertical drag starting on the strip was cancelled: the page cannot ' +
      'be scrolled from a filter row')
  }

  report.record('edge-guard', where, 'strip is clamped at both ends', failures)
}
